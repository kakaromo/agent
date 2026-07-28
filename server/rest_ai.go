package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"agent/ai"
	"agent/config"
	"agent/storage/sqlitedb"
)

// AI 결과 해석 REST/SSE — 로컬 ollama 기반.
//
// 활성 조건: config [ai] enabled=true (사무실/standalone 무관).
// endpoint:
//   GET /api/agent/ai/status                         — enabled + reachable + model (UI 버튼 노출 판단)
//   GET /api/agent/ai/analyze/stream?jobId=&kind=    — SSE 스트리밍 해석 (event: token / done / error)
//
// 데이터 조달(새 통계 코드 없음):
//   - 라이브 잡: buildTraceSummary / buildBenchmarkSummary 재사용
//   - 만료/재시작 후: DB job_executions.result_summary (standalone 만, DB 있을 때)
//
// LLM 은 SQL 을 생성하지 않고 이미 집계된 summary JSON 만 해석한다(다각도 집계 확장 여지는
// summary 를 풍부하게 만드는 것으로 후속 처리). ollama 미기동/모델 없음은 에러가 아니라
// status=reachable:false 로 다뤄 UI 가 조용히 비활성한다.

// registerAIRoutes — NewHTTPRouter 에서 opts.AI.Enabled 일 때만 호출.
func registerAIRoutes(mux *http.ServeMux, agent *DeviceAgentServer, aicfg config.AIConfig) {
	client := ai.New(aicfg.Endpoint, aicfg.Model)

	// GET /api/agent/ai/status — enabled(true, 등록됐다는 것 자체가 enabled) + reachable + model.
	mux.HandleFunc("/api/agent/ai/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		reachable := client.Reachable(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":   true,
			"reachable": reachable,
			"model":     aicfg.Model,
			"endpoint":  aicfg.Endpoint,
		})
	})

	// GET /api/agent/ai/analyze/stream?jobId=&kind=
	mux.HandleFunc("/api/agent/ai/analyze/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		jobID := r.URL.Query().Get("jobId")
		if jobID == "" {
			writeError(w, http.StatusBadRequest, "jobId required")
			return
		}
		kind := r.URL.Query().Get("kind") // "trace" | "benchmark" | "" (자동 판별)
		handleAIAnalyzeSSE(w, r, agent, client, jobID, kind)
	})
}

// handleAIAnalyzeSSE — jobId 로 통계 요약을 얻어 ollama 에 스트리밍 해석을 요청, 토큰을 SSE 로 push.
func handleAIAnalyzeSSE(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer, client *ai.Client, jobID, kind string) {
	// 1) 통계 요약 + jobType 조달. (SSE 헤더를 쓰기 전에 실패하면 일반 HTTP 에러로 응답 가능.)
	jobType, summaryJSON := resolveAISummary(r.Context(), agent, jobID, kind)
	if summaryJSON == "" {
		writeError(w, http.StatusNotFound, "이 잡의 결과 통계를 찾을 수 없습니다: "+jobID)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	setSSEHeaders(w)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 동시 Write 직렬화 (토큰 emit + keepalive).
	var writeMu sync.Mutex
	emit := func(event string, data any) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		var payload []byte
		switch v := data.(type) {
		case nil:
			payload = []byte("{}")
		case []byte:
			payload = v
		case string:
			payload = []byte(v)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return false
			}
			payload = b
		}
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		if event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
				return false
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// keepalive — 30초. ollama 첫 토큰까지 오래 걸릴 수 있어(모델 로드) proxy timeout 방지.
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()
	stopKeepalive := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopKeepalive:
				return
			case <-ctx.Done():
				return
			case <-keepalive.C:
				writeMu.Lock()
				fmt.Fprintf(w, ": keepalive %d\n\n", time.Now().Unix())
				flusher.Flush()
				writeMu.Unlock()
			}
		}
	}()

	// 2) 프롬프트 조립 + ollama 스트리밍. 토큰을 그대로 emit.
	system := ai.SystemPromptFor(jobType)
	user := ai.BuildUserPrompt(jobType, summaryJSON)

	err := client.Chat(ctx, system, user, func(token string) {
		emit("token", map[string]any{"text": token})
	})
	close(stopKeepalive)

	if err != nil {
		// ctx 취소(클라이언트 disconnect)면 조용히 종료.
		if ctx.Err() != nil {
			return
		}
		emit("error", map[string]any{"error": err.Error()})
		return
	}
	emit("done", map[string]any{})
}

// resolveAISummary — jobId 로 (jobType, summaryJSON) 을 구한다.
//
// 우선순위:
//  1. DB(standalone)가 있으면 FindJobExecutionByJobID 로 Type + 저장된 ResultSummary 확인.
//     - kind 가 비어있으면 DB 의 Type 을 사용.
//     - 라이브 재계산이 가능하면 최신 summary 를, 아니면 DB 의 저장본(만료 잡)을 쓴다.
//  2. DB 가 없거나 못 찾으면 kind 또는 inferJobTypeFromAgent 로 type 을 정하고 라이브 재계산.
//
// summaryJSON 이 빈 문자열이면 조달 실패(호출자가 404).
func resolveAISummary(ctx context.Context, agent *DeviceAgentServer, jobID, kind string) (jobType, summaryJSON string) {
	var dbSummary string

	if agent.db != nil {
		if exec, err := agent.db.FindJobExecutionByJobID(ctx, jobID); err == nil && exec != nil {
			if kind == "" {
				kind = exec.Type
			}
			if exec.ResultSummary.Valid {
				dbSummary = exec.ResultSummary.String
			}
		} else if err != nil && !errors.Is(err, sqlitedb.ErrNotFound) {
			// DB 오류는 무시하고 라이브 경로로 계속 (best-effort).
		}
	}

	// jobType 확정: 명시 kind > DB Type(위에서 채움) > 라이브 추정.
	jobType = normalizeAIKind(kind)
	if jobType == "" {
		jobType = inferJobTypeFromAgent(agent, jobID)
	}

	// 라이브 재계산 시도 (agent 메모리에 잡이 살아있으면 최신 통계).
	live := liveSummary(ctx, agent, jobID, jobType)
	if live != "" {
		return jobType, live
	}
	// 라이브 실패 → DB 저장본(만료 잡) fallback.
	return jobType, dbSummary
}

// normalizeAIKind — 외부에서 온 kind 를 내부 jobType 으로 정규화.
func normalizeAIKind(kind string) string {
	switch kind {
	case "trace":
		return "trace"
	case "benchmark", "scenario":
		return "benchmark"
	default:
		return ""
	}
}

// liveSummary — agent 메모리 기준 최신 통계 요약 (없으면 빈 문자열).
func liveSummary(ctx context.Context, agent *DeviceAgentServer, jobID, jobType string) string {
	switch jobType {
	case "trace":
		s, _ := buildTraceSummary(ctx, agent, jobID)
		return s
	default: // benchmark / scenario
		s, _ := buildBenchmarkSummary(ctx, agent, jobID)
		return s
	}
}
