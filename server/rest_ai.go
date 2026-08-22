package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent/ai"
	"agent/config"
	pb "agent/pb"
	"agent/scenario"
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

	// POST /api/agent/ai/chat/stream — 멀티턴 채팅 (집계 도구 선택 + 근거 노출)
	registerAIChatRoutes(mux, agent, client)

	// POST /api/agent/ai/scenario/generate
	//   body: { prompt: "자연어 요청", deviceContext?: "...", deviceId?: "..." }
	//     - deviceId 가 오면 백엔드가 그 기기의 설치앱 + 현재 activity 를 자동 조달해 프롬프트에 주입.
	//   resp: { steps: [{type, tool, params:{string:string}}...], loops: [{startStep,endStep,count}...], warnings?: [...] }
	mux.HandleFunc("/api/agent/ai/scenario/generate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleAIScenarioGenerate(w, r, agent, client)
	})
}

// aiGenReq — /ai/scenario/generate 요청 body.
type aiGenReq struct {
	Prompt        string `json:"prompt"`
	DeviceContext string `json:"deviceContext"` // 수동 힌트 (선택)
	DeviceId      string `json:"deviceId"`      // 오면 백엔드가 설치앱 + 현재 activity 자동 조달
}

// aiScenarioStep — 생성/검증된 시나리오 step. wire shape({type, tool?, params:{string:string}}) 와 일치.
type aiScenarioStep struct {
	Type   string            `json:"type"`
	Tool   string            `json:"tool"`
	Params map[string]string `json:"params"`
}

// aiScenarioLoop — 반복 구간. 모든 값 문자열 (step 인덱스/횟수).
type aiScenarioLoop struct {
	StartStep string `json:"startStep"`
	EndStep   string `json:"endStep"`
	Count     string `json:"count"`
}

// aiGenRaw — ollama 가 schema 로 뱉는 원 JSON. params 값은 string 강제되나 방어적으로 any 로 받아 정규화.
type aiGenRaw struct {
	Steps []struct {
		Type   string         `json:"type"`
		Tool   string         `json:"tool"`
		Params map[string]any `json:"params"`
	} `json:"steps"`
	Loops []struct {
		StartStep any `json:"startStep"`
		EndStep   any `json:"endStep"`
		Count     any `json:"count"`
	} `json:"loops"`
}

// handleAIScenarioGenerate — 자연어 → 시나리오 step 배열. ChatJSON(schema 강제) + 최소 검증.
// 파싱/검증 실패 시 1회 재시도(실패 사유를 프롬프트에 피드백). 무한 루프 없음.
//
// deviceId 가 오면 그 기기의 설치앱 + 현재 activity 를 자동 조달해 deviceContext 에 합성한다
// (launch_app 이 실존 package, tap_element 가 현재 화면 맥락을 쓰도록). 조달은 best-effort —
// 미연결/실패해도 컨텍스트 없이 일반 생성으로 계속한다.
func handleAIScenarioGenerate(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer, client *ai.Client) {
	var body aiGenReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if body.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt required")
		return
	}

	// deviceId 로 실제 기기 컨텍스트 조달 (best-effort). 수동 deviceContext 와 합쳐 프롬프트에 넣는다.
	deviceContext := body.DeviceContext
	if body.DeviceId != "" && agent != nil {
		if auto := buildDeviceScenarioContext(r.Context(), agent, body.DeviceId); auto != "" {
			if deviceContext == "" {
				deviceContext = auto
			} else {
				deviceContext = deviceContext + "\n\n" + auto
			}
		}
	}

	schema := ai.ScenarioSchema()
	user := ai.BuildScenarioUserPrompt(body.Prompt, deviceContext)

	var (
		steps    []aiScenarioStep
		loops    []aiScenarioLoop
		warnings []string
		feedback string
		lastErr  error
	)

	// 최대 2회: 첫 시도 + 파싱/전량 실패 시 피드백 재시도.
	for attempt := 0; attempt < 2; attempt++ {
		system := ai.ScenarioSystemPrompt(feedback)
		content, err := client.ChatJSON(r.Context(), system, user, schema)
		if err != nil {
			// ollama 미기동/모델 없음/취소 — 즉시 에러 반환 (재시도 무의미).
			writeError(w, http.StatusBadGateway, "ollama 생성 실패: "+err.Error())
			return
		}

		s, l, warns, perr := parseAndValidateScenario(content)
		if perr != nil {
			lastErr = perr
			feedback = perr.Error()
			continue // 재시도
		}
		if len(s) == 0 {
			// JSON 은 유효하나 살아남은 step 이 없음 → 피드백 후 재시도.
			lastErr = fmt.Errorf("유효한 step 이 하나도 없습니다")
			feedback = "생성한 step 이 모두 유효하지 않았습니다. schema 의 type enum 과 필수 params 를 지켜 다시 만드세요."
			continue
		}
		steps, loops, warnings = s, l, warns
		lastErr = nil
		break
	}

	if lastErr != nil {
		writeError(w, http.StatusUnprocessableEntity, "시나리오 생성 실패: "+lastErr.Error())
		return
	}

	resp := map[string]any{
		"steps": steps,
		"loops": loops,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	writeJSON(w, http.StatusOK, resp)
}

// parseAndValidateScenario — ollama content(JSON 문자열)를 파싱하고 step 별 최소 검증.
//
// 검증 실패한 step 은 버리고 warnings 에 사유를 담아 나머지는 살린다(관대 처리).
// JSON 자체가 깨졌으면 error 반환 → 상위에서 재시도.
func parseAndValidateScenario(content string) ([]aiScenarioStep, []aiScenarioLoop, []string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil, nil, fmt.Errorf("빈 응답")
	}

	var raw aiGenRaw
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, nil, nil, fmt.Errorf("JSON 파싱 실패: %v", err)
	}

	// 유효 type 집합 (scenario.Specs = 실행부 switch 와 동일 소스).
	validType := make(map[string]bool, len(ai.ScenarioStepTypes))
	for _, t := range ai.ScenarioStepTypes {
		validType[t] = true
	}

	var (
		steps    []aiScenarioStep
		warnings []string
	)
	for i, rs := range raw.Steps {
		typ := strings.TrimSpace(rs.Type)
		if typ == "" {
			warnings = append(warnings, fmt.Sprintf("step %d: type 누락 — 제외", i))
			continue
		}
		if !validType[typ] {
			warnings = append(warnings, fmt.Sprintf("step %d: 알 수 없는 type '%s' — 제외", i, typ))
			continue
		}

		// params 를 전부 string 으로 정규화 (schema 로 강제되나 방어적).
		params := make(map[string]string, len(rs.Params))
		for k, v := range rs.Params {
			params[k] = anyToString(v)
		}

		step := aiScenarioStep{Type: typ, Tool: strings.TrimSpace(rs.Tool), Params: params}

		// type 별 최소 검증 — 필수 param 없으면 제외.
		if reason := validateStepParams(step); reason != "" {
			warnings = append(warnings, fmt.Sprintf("step %d (%s): %s — 제외", i, typ, reason))
			continue
		}

		// app_macro 함정: AI 는 실존 macroId 를 모른다. 살리되 수동 지정 경고.
		if typ == "app_macro" {
			warnings = append(warnings, fmt.Sprintf("step %d: app_macro 는 기록된 매크로를 수동 지정해야 합니다 (AI 가 macroId 를 채울 수 없음)", i))
		}

		steps = append(steps, step)
	}

	// loops 정규화 — 모든 값 문자열. count<=0 이거나 인덱스 파싱 실패면 제외.
	var loops []aiScenarioLoop
	for i, rl := range raw.Loops {
		start := anyToString(rl.StartStep)
		end := anyToString(rl.EndStep)
		count := anyToString(rl.Count)
		si, e1 := strconv.Atoi(start)
		ei, e2 := strconv.Atoi(end)
		ci, e3 := strconv.Atoi(count)
		if e1 != nil || e2 != nil || e3 != nil {
			warnings = append(warnings, fmt.Sprintf("loop %d: 숫자가 아닌 값 — 제외", i))
			continue
		}
		if ci <= 0 || si < 0 || ei < si || si >= len(steps) {
			warnings = append(warnings, fmt.Sprintf("loop %d: 범위/횟수 유효하지 않음 (start=%d end=%d count=%d) — 제외", i, si, ei, ci))
			continue
		}
		loops = append(loops, aiScenarioLoop{StartStep: start, EndStep: end, Count: count})
	}

	warnings = warnUnclosedTrace(steps, warnings)

	return steps, loops, warnings, nil
}

// warnUnclosedTrace — trace_start / trace_stop 개수가 맞지 않으면 경고한다.
//
// trace 가 중지되지 않으면 수집이 계속 돌아 다음 잡까지 방해하고("trace already
// active") 결과 parquet 도 생성되지 않는다.
//
// 자동으로 trace_stop 을 끼워넣지는 않는다. 어디에 넣어야 옳은지는 맥락에 달려
// 있어서(예: loops 로 감싼 구간이면 끝에 붙이면 반복마다 중지되지 않는다) 잘못
// 보정하면 조용히 틀린 시나리오가 된다. 사용자가 캔버스에서 직접 배치하도록
// 경고만 남긴다.
func warnUnclosedTrace(steps []aiScenarioStep, warnings []string) []string {
	starts := make(map[string]int)
	stops := make(map[string]int)
	var order []string
	seen := make(map[string]bool)
	for _, s := range steps {
		t := s.Params["trace_type"]
		switch s.Type {
		case "trace_start":
			starts[t]++
		case "trace_stop":
			stops[t]++
		default:
			continue
		}
		if !seen[t] {
			seen[t] = true
			order = append(order, t)
		}
	}
	for _, t := range order {
		if starts[t] > stops[t] {
			warnings = append(warnings, fmt.Sprintf(
				"trace_stop(%s) 이 %d개 부족합니다 — 워크로드 뒤에 trace_stop 을 추가해야 트레이스가 중지됩니다",
				t, starts[t]-stops[t]))
		}
	}
	return warnings
}

// validateStepParams — type 별 param 검증. 빈 문자열 반환이면 통과, 아니면 제외 사유.
//
// 계약은 scenario.Specs 가 소유한다 (scenario/steptypes.go). 여기서 위임하므로
// 필수 param 뿐 아니라 enum 위반(clear_mode="forcestop" 같은 오타)도 함께 잡힌다.
func validateStepParams(s aiScenarioStep) string {
	return scenario.ValidateParams(s.Type, s.Tool, s.Params)
}

// anyToString — schema 로 string 이 강제되나, 모델이 숫자/불리언을 낼 경우 대비한 안전 변환.
func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		// 정수면 소수점 없이.
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// handleAIAnalyzeSSE — jobId 로 통계 요약을 얻어 ollama 에 스트리밍 해석을 요청, 토큰을 SSE 로 push.
func handleAIAnalyzeSSE(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer, client *ai.Client, jobID, kind string) {
	// 1) 통계 요약 + jobType 조달. (SSE 헤더를 쓰기 전에 실패하면 일반 HTTP 에러로 응답 가능.)
	jobType, summaryJSON := resolveAISummary(r.Context(), agent, jobID, kind)
	if summaryJSON == "" {
		writeError(w, http.StatusNotFound, "이 잡의 결과 통계를 찾을 수 없습니다: "+jobID)
		return
	}

	stream, ok := startAISSE(w, r)
	if !ok {
		return
	}
	defer stream.Close()

	// 2) 프롬프트 조립 + ollama 스트리밍. 토큰을 그대로 emit.
	system := ai.SystemPromptFor(jobType)
	user := ai.BuildUserPrompt(jobType, summaryJSON)

	err := client.Chat(stream.Ctx, system, user, func(token string) {
		stream.Emit("token", map[string]any{"text": token})
	})
	stream.StopKeepalive()

	if err != nil {
		// ctx 취소(클라이언트 disconnect)면 조용히 종료.
		if stream.Ctx.Err() != nil {
			return
		}
		stream.Emit("error", map[string]any{"error": err.Error()})
		return
	}
	stream.Emit("done", map[string]any{})
}

// aiSSEStream — AI 스트리밍 SSE 응답의 공통 배선.
//
// 단발 해석(handleAIAnalyzeSSE)과 채팅(handleAIChatSSE)이 공유한다. 동시 Write 직렬화와
// keepalive 는 둘 다 필요하다 — ollama 는 모델 로드 때문에 첫 토큰까지 수십 초가 걸릴 수
// 있어 keepalive 없이는 proxy 가 연결을 끊는다.
type aiSSEStream struct {
	Ctx context.Context

	w             http.ResponseWriter
	flusher       http.Flusher
	writeMu       sync.Mutex
	cancel        context.CancelFunc
	stopKeepalive chan struct{}
	stopOnce      sync.Once
}

// startAISSE — SSE 헤더를 쓰고 keepalive 고루틴을 띄운다.
// 반환된 stream 은 반드시 Close() 해야 한다(고루틴 정리 + ctx 취소).
//
// ok=false 면 이미 에러 응답을 보냈으므로 호출자는 그대로 리턴하면 된다.
func startAISSE(w http.ResponseWriter, r *http.Request) (*aiSSEStream, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return nil, false
	}
	setSSEHeaders(w)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	s := &aiSSEStream{
		Ctx:           ctx,
		w:             w,
		flusher:       flusher,
		cancel:        cancel,
		stopKeepalive: make(chan struct{}),
	}

	// keepalive — 30초. ollama 첫 토큰까지 오래 걸릴 수 있어(모델 로드) proxy timeout 방지.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopKeepalive:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.writeMu.Lock()
				fmt.Fprintf(s.w, ": keepalive %d\n\n", time.Now().Unix())
				s.flusher.Flush()
				s.writeMu.Unlock()
			}
		}
	}()
	return s, true
}

// Emit — 명명 SSE 이벤트 하나를 보낸다. 쓰기 실패 시 false.
func (s *aiSSEStream) Emit(event string, data any) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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
		if _, err := fmt.Fprintf(s.w, "event: %s\n", event); err != nil {
			return false
		}
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", payload); err != nil {
		return false
	}
	s.flusher.Flush()
	return true
}

// StopKeepalive — keepalive 고루틴만 멈춘다(스트림은 계속 쓸 수 있다).
// 여러 번 불러도 안전하다.
func (s *aiSSEStream) StopKeepalive() {
	s.stopOnce.Do(func() { close(s.stopKeepalive) })
}

// Close — keepalive 정리 + ctx 취소. defer 로 호출한다.
func (s *aiSSEStream) Close() {
	s.StopKeepalive()
	s.cancel()
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

// maxScenarioAppList — 프롬프트 폭증 방지용 설치앱 표시 상한.
// 프롬프트에 넣을 설치앱 최대 개수. 삼성 기기는 런처 앱이 60여 개라 40 이면 시계·노트 등
// 실사용 앱이 잘려 AI 가 패키지명을 못 찾는다. 14b 컨텍스트 여유 안에서 넉넉히 잡는다.
const maxScenarioAppList = 80

// buildDeviceScenarioContext — deviceId 로 그 기기의 설치앱 + 현재 activity 를 조달해
// 시나리오 프롬프트용 컨텍스트 문자열로 합성한다. best-effort — 조달 실패한 항목은 빼고 진행,
// 전부 실패하면 빈 문자열 반환(호출 측이 컨텍스트 없이 계속).
//
// 설치앱은 시스템 패키지(com.android.*, com.qualcomm.*, android 등)를 걸러 사용자앱 위주로,
// 상위 maxScenarioAppList 개까지만 넣고 잘렸으면 그 사실을 명시한다.
func buildDeviceScenarioContext(ctx context.Context, agent *DeviceAgentServer, deviceID string) string {
	var lines []string

	// 현재 포그라운드 activity.
	if act, err := agent.GetCurrentActivity(ctx, deviceID); err == nil && act != nil {
		if act.Component != "" {
			lines = append(lines, "현재 화면(activity): "+act.Component)
		} else if act.Package != "" {
			lines = append(lines, "현재 화면(package): "+act.Package)
		}
	}

	// 설치앱 목록 (사용자앱 위주로 필터 + 상한).
	if resp, err := agent.ListInstalledApps(ctx, &pb.ListInstalledAppsRequest{DeviceId: deviceID}); err == nil && resp != nil {
		var pkgs []string
		for _, app := range resp.GetApps() {
			pkg := app.GetPackageName()
			if pkg == "" || isSystemPackage(pkg) {
				continue
			}
			pkgs = append(pkgs, pkg)
		}
		if len(pkgs) > 0 {
			truncated := false
			if len(pkgs) > maxScenarioAppList {
				pkgs = pkgs[:maxScenarioAppList]
				truncated = true
			}
			label := "이 디바이스에 설치된 앱(package): " + strings.Join(pkgs, ", ")
			if truncated {
				label += fmt.Sprintf(" (일부만 표시 — 상위 %d개)", maxScenarioAppList)
			}
			lines = append(lines, label)
		}
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// isSystemPackage — launch_app 대상이 되기 어려운 시스템/벤더 패키지 판별.
// 프롬프트에서 제외해 사용자앱 위주로 노출한다.
// isSystemPackage — AI 컨텍스트에 넣지 않을 순수 OS 인프라 패키지 판별.
//
// 주의: ListInstalledApps 는 LAUNCHER 인텐트를 가진 "런처에 뜨는 앱"만 반환하므로
// (설정/시계/노트/계산기처럼 사용자가 실제 여는 앱 포함), 제조사 prefix(com.samsung.*,
// com.sec.*, com.qualcomm.* 등)를 통짜로 거르면 안 된다 — 삼성 기기의 시계·노트·계산기가
// 전부 제외되어 AI 가 패키지명을 못 찾고 지어낸다. 여기서는 명백한 백그라운드 인프라만 제외한다.
func isSystemPackage(pkg string) bool {
	if pkg == "android" {
		return true
	}
	systemPrefixes := []string{
		"com.google.android.gms", // Play services (런처에 뜨더라도 사용자 대상 아님)
		"com.google.android.gsf",
		"com.qualcomm.qti.",
		"vendor.",
		"org.codeaurora.",
	}
	for _, p := range systemPrefixes {
		if strings.HasPrefix(pkg, p) {
			return true
		}
	}
	return false
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
