package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"agent/ai"
	"agent/trace"
)

// 채팅 기반 결과 분석 — 단일 job 심층 질의.
//
// 한 턴의 흐름:
//
//	질문 → (1) LLM 이 집계 도구 선택   [ChatJSON + schema, temperature 0]
//	     → (2) Go 가 DuckDB 로 실행     [trace.RunAggregation — 검증된 코드]
//	     → (3) LLM 이 결과+히스토리로 답변 [ChatMessages 스트리밍]
//
// (1)이 틀려도 (2)는 검증된 코드라 **숫자 자체는 항상 정확**하다. 최악의 경우가 "질문과
// 다른 걸 계산했다"이고, event:tool 로 근거를 항상 노출하므로 사용자가 즉시 알아본다.
// LLM 은 SQL 을 생성하지 않는다 — text-to-SQL 의 "그럴듯한 오답" 위험이 구조적으로 없다.
//
// 서버는 대화 상태를 보관하지 않는다(stateless). 클라이언트가 매 턴 히스토리를 보낸다.

// 히스토리 상한 — 로컬 모델 컨텍스트(32K)를 넘기면 조용히 잘려 앞 내용을 잃는다.
const (
	// 유지할 최근 질문/답변 턴 수 (user+assistant 쌍 기준).
	maxChatTurns = 6
	// 집계 결과 JSON 을 원문으로 유지할 최근 개수. 그 이전 턴은 한 줄 요약으로 대체한다.
	//
	// 후속 질문이 직전 집계 결과의 숫자를 가리키는 경우가 많아("그 184초 근처") 결과를
	// 아예 안 남기면 대화가 끊긴다. 반대로 전부 남기면 컨텍스트가 선형으로 늘어난다.
	maxChatAggResults = 2
	// 히스토리 전체 문자 수 상한 (초과 시 오래된 것부터 버린다).
	maxChatChars = 24000
)

// aiChatMessage — 클라이언트가 보내는 대화 한 턴.
//
// Tool 은 assistant 메시지에 붙는 근거 요약이다. 클라이언트가 이전 턴에 받은
// event:tool 을 그대로 되돌려주면, 서버가 "어떤 집계를 근거로 답했는지"를 히스토리에
// 복원할 수 있다(서버가 상태를 안 갖기 때문에 필요하다).
type aiChatMessage struct {
	Role    string          `json:"role"`    // "user" | "assistant"
	Content string          `json:"content"` // 질문 또는 답변 텍스트
	Tool    string          `json:"tool"`    // 이 답변의 근거 집계 이름 (선택)
	AggJSON json.RawMessage `json:"aggJson"` // 이 답변의 근거 집계 결과 (선택)
}

// aiChatReq — POST /api/agent/ai/chat/stream 요청 body.
type aiChatReq struct {
	JobID    string          `json:"jobId"`
	Kind     string          `json:"kind"` // "trace" | "benchmark" | "" (자동 판별)
	Messages []aiChatMessage `json:"messages"`
}

// registerAIChatRoutes — registerAIRoutes 에서 호출. AI 활성 시에만 등록된다.
func registerAIChatRoutes(mux *http.ServeMux, agent *DeviceAgentServer, client *ai.Client) {
	// POST /api/agent/ai/chat/stream
	//
	// EventSource(GET)가 아니라 POST 인 이유: 대화 히스토리가 쿼리스트링에 담기지 않는다.
	// 클라이언트는 fetch + ReadableStream 으로 SSE 를 파싱한다.
	mux.HandleFunc("/api/agent/ai/chat/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleAIChatSSE(w, r, agent, client)
	})
}

// handleAIChatSSE — 채팅 한 턴 처리.
func handleAIChatSSE(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer, client *ai.Client) {
	var body aiChatReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if body.JobID == "" {
		writeError(w, http.StatusBadRequest, "jobId required")
		return
	}
	question := lastUserQuestion(body.Messages)
	if question == "" {
		writeError(w, http.StatusBadRequest, "질문이 비어 있습니다")
		return
	}

	// SSE 헤더를 쓰기 전에 컨텍스트를 조달한다 — 실패 시 일반 HTTP 에러로 응답 가능.
	jobType, summaryJSON := resolveAISummary(r.Context(), agent, body.JobID, body.Kind)
	if summaryJSON == "" {
		writeError(w, http.StatusNotFound, "이 job 의 결과 통계를 찾을 수 없습니다: "+body.JobID)
		return
	}

	stream, ok := startAISSE(w, r)
	if !ok {
		return
	}
	defer stream.Close()

	// ── 1) 집계 도구 선택 ──
	//
	// benchmark job 은 parquet 이 없어 집계 선택이 무의미하다(metrics 가 이미 작은 맵).
	// summary 를 배경으로 깔고 멀티턴만 지원한다.
	var agg *trace.AggResult
	if jobType == "trace" {
		agg = selectAndRunAggregation(stream.Ctx, agent, client, body.JobID, question)
		if agg != nil && agg.Tool != trace.AggNone {
			// 근거를 답변 토큰보다 **먼저** 보낸다 — UI 가 뱃지를 먼저 그린다.
			stream.Emit("tool", agg)
		}
	}

	// ── 2) 프롬프트 조립 ──
	msgs := buildChatMessages(jobType, summaryJSON, body.Messages, question, agg)

	// ── 3) 답변 스트리밍 ──
	err := client.ChatMessages(stream.Ctx, msgs, func(token string) {
		stream.Emit("token", map[string]any{"text": token})
	})
	stream.StopKeepalive()

	if err != nil {
		if stream.Ctx.Err() != nil {
			return // 클라이언트 disconnect — 조용히 종료
		}
		stream.Emit("error", map[string]any{"error": err.Error()})
		return
	}
	stream.Emit("done", map[string]any{})
}

// selectAndRunAggregation — LLM 에게 도구를 고르게 하고 실행한다.
//
// best-effort: 선택 실패·실행 실패 모두 nil 또는 Note 를 담은 결과를 반환하고, 답변
// 단계는 계속 진행한다(집계 없이 답하거나 "왜 답할 수 없는지" 를 설명하게 된다).
// 집계가 조용히 틀린 숫자를 내는 것보다 없는 편이 낫다.
func selectAndRunAggregation(ctx context.Context, agent *DeviceAgentServer, client *ai.Client, jobID, question string) *trace.AggResult {
	system := ai.ToolSelectSystemPrompt(trace.AggToolReference())
	schema := ai.ToolSelectSchema(trace.AggNames())

	content, err := client.ChatJSON(ctx, system, question, schema)
	if err != nil {
		slog.Warn("AI chat: 도구 선택 실패", "jobId", jobID, "err", err)
		return nil
	}

	var sel struct {
		Tool   string         `json:"tool"`
		Params map[string]any `json:"params"`
		Reason string         `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &sel); err != nil {
		slog.Warn("AI chat: 도구 선택 파싱 실패", "jobId", jobID, "content", content, "err", err)
		return nil
	}

	spec, found := trace.AggSpecByName(sel.Tool)
	if !found {
		slog.Warn("AI chat: 알 수 없는 도구 선택", "jobId", jobID, "tool", sel.Tool)
		return nil
	}

	// none / overview 는 parquet 접근이 없다.
	if !spec.NeedsDB {
		return &trace.AggResult{Tool: sel.Tool, Params: sel.Params}
	}

	infos, err := agent.collectTraceJobInfos([]string{jobID})
	if err != nil {
		// 수집 중(RUNNING/COLLECTING)이거나 결과가 없는 경우.
		return &trace.AggResult{Tool: sel.Tool, Params: sel.Params, Note: err.Error()}
	}

	res, err := trace.RunAggregation(infos, sel.Tool, sel.Params)
	if err != nil {
		slog.Warn("AI chat: 집계 실행 실패", "jobId", jobID, "tool", sel.Tool, "err", err)
		return &trace.AggResult{Tool: sel.Tool, Params: sel.Params, Note: err.Error()}
	}
	return res
}

// buildChatMessages — ollama 에 보낼 메시지 배열을 조립한다.
//
// 구성: system → 전체 summary 컨텍스트(첫 턴만) → 히스토리 → 이번 질문(+집계 결과)
//
// 히스토리는 두 층으로 자른다:
//   - 텍스트: 최근 maxChatTurns 턴
//   - 집계 결과 원문: 최근 maxChatAggResults 개 (그 이전은 한 줄 요약)
//
// 후속 질문이 직전 집계의 숫자를 가리키는 경우가 많아 결과를 남겨야 하지만, 전부
// 남기면 컨텍스트가 선형으로 늘어난다.
func buildChatMessages(jobType, summaryJSON string, history []aiChatMessage, question string, agg *trace.AggResult) []ai.Message {
	msgs := []ai.Message{
		{Role: "system", Content: ai.ChatSystemPrompt(jobType)},
	}

	// 전체 요약을 배경 지식으로 한 번 깔아둔다.
	msgs = append(msgs,
		ai.Message{Role: "user", Content: ai.BuildChatContextPrompt(jobType, summaryJSON)},
		ai.Message{Role: "assistant", Content: "네, 이 job 의 통계를 확인했습니다. 궁금한 점을 물어보세요."},
	)

	// 마지막 user 메시지(=이번 질문)는 히스토리에서 제외하고 아래에서 따로 붙인다.
	past := trimTrailingUser(history)
	past = tailTurns(past, maxChatTurns)

	// 최근 N개 집계만 원문 유지 — 뒤에서부터 세어 인덱스를 정한다.
	keepFrom := aggKeepFromIndex(past, maxChatAggResults)

	for i, m := range past {
		role := m.Role
		if role != "user" && role != "assistant" {
			continue
		}
		content := m.Content
		if role == "assistant" && m.Tool != "" {
			if i >= keepFrom && len(m.AggJSON) > 0 {
				content = fmt.Sprintf("(근거 집계 %s 결과: %s)\n%s", m.Tool, string(m.AggJSON), content)
			} else {
				content = fmt.Sprintf("(근거 집계: %s)\n%s", m.Tool, content)
			}
		}
		msgs = append(msgs, ai.Message{Role: role, Content: content})
	}

	// 이번 질문 + 이번 턴 집계 결과.
	aggLabel, aggJSON := aggForPrompt(agg)
	msgs = append(msgs, ai.Message{Role: "user", Content: ai.BuildChatUserPrompt(question, aggLabel, aggJSON)})

	return capChars(msgs, maxChatChars)
}

// aggForPrompt — 집계 결과를 프롬프트에 넣을 (라벨, JSON) 로 만든다.
// none 이거나 결과가 없으면 빈 문자열 → 질문만 넘어가고, 모델이 답할 수 없는 이유를 설명한다.
func aggForPrompt(agg *trace.AggResult) (string, string) {
	if agg == nil || agg.Tool == trace.AggNone {
		return "", ""
	}
	if agg.Note != "" && len(agg.Data) == 0 {
		// 집계를 못 돌린 경우 — 사유를 그대로 넘겨 모델이 설명하게 한다.
		return agg.Tool, fmt.Sprintf(`{"error": %q}`, agg.Note)
	}
	if len(agg.Data) == 0 {
		return "", ""
	}
	b, err := json.Marshal(agg.Data)
	if err != nil {
		return "", ""
	}
	return agg.Tool, string(b)
}

// lastUserQuestion — 히스토리 마지막의 user 메시지(=이번 질문).
func lastUserQuestion(msgs []aiChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return strings.TrimSpace(msgs[i].Content)
		}
	}
	return ""
}

// trimTrailingUser — 마지막 user 메시지를 뺀 나머지(=지난 대화).
func trimTrailingUser(msgs []aiChatMessage) []aiChatMessage {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[:i]
		}
	}
	return msgs
}

// tailTurns — 최근 n턴(user+assistant 쌍)만 남긴다.
func tailTurns(msgs []aiChatMessage, n int) []aiChatMessage {
	limit := n * 2
	if len(msgs) <= limit {
		return msgs
	}
	return msgs[len(msgs)-limit:]
}

// aggKeepFromIndex — 이 인덱스 이상의 assistant 메시지만 집계 결과 원문을 유지한다.
func aggKeepFromIndex(msgs []aiChatMessage, keep int) int {
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && len(msgs[i].AggJSON) > 0 {
			seen++
			if seen >= keep {
				return i
			}
		}
	}
	return 0
}

// capChars — 전체 문자 수가 상한을 넘으면 오래된 대화부터 버린다.
//
// system(0)과 배경 컨텍스트(1,2), 그리고 마지막 질문은 항상 유지한다 — 이것들이 빠지면
// 답변 품질이 아니라 동작 자체가 깨진다.
func capChars(msgs []ai.Message, limit int) []ai.Message {
	total := 0
	for _, m := range msgs {
		total += len(m.Content)
	}
	if total <= limit || len(msgs) <= 4 {
		return msgs
	}
	head := msgs[:3]           // system + 배경 컨텍스트 쌍
	tail := msgs[len(msgs)-1:] // 이번 질문
	mid := msgs[3 : len(msgs)-1]

	for total > limit && len(mid) > 0 {
		total -= len(mid[0].Content)
		mid = mid[1:]
	}
	out := make([]ai.Message, 0, len(head)+len(mid)+1)
	out = append(out, head...)
	out = append(out, mid...)
	out = append(out, tail...)
	return out
}
