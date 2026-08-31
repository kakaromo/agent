package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	pb "agent/pb"
	"agent/storage/sqlitedb"
)

// registerScenarioRoutes — portal AgentController 의 /scenario/run 1:1 호환.
//
//	POST /api/agent/scenario/run?serverId=
//	body: { deviceIds[], scenarioName?, steps[], loops?[], repeat?, busyPolicy?, hasBranching?, edges?[] }
//
// portal frontend (runScenario in agent.ts) 가 보낸 JSON 을 protojson 으로 RunScenarioRequest
// 에 그대로 unmarshal — 모든 필드명이 camelCase + proto 와 일치하므로 추가 변환 거의 없음.
//
// 단 app_macro 스텝의 경우 frontend 가 macroId 만 보내고 events 는 생략할 수 있어, DB 에서
// 로드해서 채워준다 (portal Spring 의 buildMacroEvent 흐름과 동일).
func registerScenarioRoutes(mux *http.ServeMux, agent *DeviceAgentServer, db *sqlitedb.DB) {
	mux.HandleFunc("/api/agent/scenario/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		raw, err := readRawBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "read: "+err.Error())
			return
		}

		req, err := scenarioRequestFromRawBody(r.Context(), db, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		resp, err := agent.RunScenario(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// JobExecution hook (benchmark/trace 와 동일 패턴).
		if rec := currentRecorder(); rec != nil {
			var bodyForLog map[string]any
			_ = json.Unmarshal(raw, &bodyForLog)
			rec.OnStart(r.Context(), resp.GetJobId(), "scenario", "", req.GetScenarioName(),
				req.GetDeviceIds(), bodyForLog)
		}

		writeJSON(w, http.StatusOK, map[string]any{"jobId": resp.GetJobId()})
	})
}

// scenarioRequestFromRawBody — /scenario/run 이 받은 raw JSON body 를 RunScenarioRequest 로 변환.
//
// 수동 실행(REST)과 스케줄 자동 실행(schedule.Runner) 이 공유하는 단일 변환 경로:
//  1. protojson unmarshal (camelCase + proto 필드명 일치 전제, DiscardUnknown)
//  2. normalizeStepTools — 짧은 tool 이름("FIO") → proto enum 보정
//  3. hydrateMacroSteps — app_macro step 의 macroId → DB events 채우기 (db != nil)
//
// db 가 nil 이면 macro hydrate 를 건너뛴다(office 모드).
func scenarioRequestFromRawBody(ctx context.Context, db *sqlitedb.DB, raw []byte) (*pb.RunScenarioRequest, error) {
	req := &pb.RunScenarioRequest{}
	unmarshalOpts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := unmarshalOpts.Unmarshal(raw, req); err != nil {
		return nil, fmt.Errorf("decode scenario: %w", err)
	}
	if req.BusyPolicy == "" {
		req.BusyPolicy = "reject"
	}
	normalizeStepTools(raw, req)
	if db != nil {
		if err := hydrateMacroSteps(ctx, db, raw, req); err != nil {
			return nil, fmt.Errorf("hydrate macro: %w", err)
		}
		if err := hydrateLogcatProfile(ctx, db, req); err != nil {
			return nil, fmt.Errorf("hydrate logcat profile: %w", err)
		}
	}
	return req, nil
}

// hydrateLogcatProfile — `logcat_profile_id` 를 저장된 프로파일의 태그로 푼다.
//
// ⚠ 이게 없으면 **탐색 → 프로파일 → 측정** 루프가 핸드오프에서 끊긴다.
// UI 는 탐색에서 찾은 태그를 프로파일의 `tags` 에 넣어 저장하는데, 수집 쪽은
// 잡 파라미터 `logcat_tags` 만 봤다. 그래서 사용자는 measure 를 설정했다고 믿지만
// 실제로는 태그가 비어 explore(전체 버퍼)로 수집됐다 — 전체 수집은 그 자체가
// IO/CPU 를 써서 수백 ms 단위 TTFT 를 흔들므로 **측정값이 조용히 오염된다.**
//
// 명시적 `logcat_tags` 가 있으면 그쪽이 이긴다 (사용자가 직접 쓴 값이 우선).
// 프로파일을 못 찾으면 에러다 — 조용히 explore 로 떨어지면 위와 같은 오염이 난다.
func hydrateLogcatProfile(ctx context.Context, db *sqlitedb.DB, req *pb.RunScenarioRequest) error {
	params := req.GetParams()
	if params == nil {
		return nil
	}
	idStr := strings.TrimSpace(params["logcat_profile_id"])
	if idStr == "" {
		return nil
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return fmt.Errorf("logcat_profile_id 가 숫자가 아니다: %q", idStr)
	}
	prof, err := db.FindAILogProfile(ctx, id)
	if err != nil {
		return fmt.Errorf("profile %d: %w", id, err)
	}
	if prof == nil {
		return fmt.Errorf("logcat profile %d 을 찾을 수 없다", id)
	}
	// 이미 태그를 직접 지정했으면 건드리지 않는다.
	if strings.TrimSpace(params["logcat_tags"]) != "" {
		return nil
	}
	pat, err := sqlitedb.ParseAILogPatterns(prof.PatternsJSON)
	if err != nil {
		return fmt.Errorf("profile %d patterns: %w", id, err)
	}
	if len(pat.Tags) == 0 {
		// ⚠ 조용히 넘어가지 않는다. 태그 없는 프로파일로 측정하면 explore 로 떨어져
		// 전체 버퍼를 받는데, 사용자는 좁혀 받았다고 믿는다.
		return fmt.Errorf("logcat profile %d(%s) 에 tags 가 없다 — "+
			"measure 로 좁히려면 프로파일에 태그를 넣거나 logcat_tags 를 직접 지정할 것",
			id, prof.Name)
	}
	params["logcat_tags"] = strings.Join(pat.Tags, ",")
	return nil
}

// ScenarioRequestFromScheduleConfig — 스케줄 잡의 config(JSON 객체 문자열) 를 RunScenarioRequest 로 변환.
//
// ScheduledJob.Config 는 frontend 가 저장한 { stepsJson, loopsJson?, repeatCount?, deviceIds?, scenarioName?, busyPolicy? }
// 형태의 JSON 객체다. stepsJson/loopsJson 은 각각 배열이 문자열로 이스케이프된 필드라, 이를 풀어
// /scenario/run 과 동일한 shape({ steps, loops, repeat, ... }) 의 raw body 로 재조립한 뒤
// scenarioRequestFromRawBody 를 태운다 — 수동/자동 경로가 완전히 같은 변환을 쓰게 한다.
//
// schedule 패키지는 server 를 import 할 수 없으므로(cycle), DeviceAgentServer 메서드로 노출해
// schedule.JobRunner 인터페이스가 호출한다.
func ScenarioRequestFromScheduleConfig(ctx context.Context, db *sqlitedb.DB, config string, deviceIDs []string, scenarioName, busyPolicy string) (*pb.RunScenarioRequest, error) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(config), &cfg); err != nil {
		return nil, fmt.Errorf("parse schedule config: %w", err)
	}

	// stepsJson / loopsJson (이스케이프된 배열 문자열) → 실제 배열로 풀기
	assembled := map[string]any{}
	if s, ok := cfg["stepsJson"].(string); ok && s != "" {
		var steps []any
		if err := json.Unmarshal([]byte(s), &steps); err != nil {
			return nil, fmt.Errorf("parse stepsJson: %w", err)
		}
		assembled["steps"] = steps
	}
	if s, ok := cfg["loopsJson"].(string); ok && s != "" {
		var loops []any
		if err := json.Unmarshal([]byte(s), &loops); err != nil {
			return nil, fmt.Errorf("parse loopsJson: %w", err)
		}
		assembled["loops"] = loops
	}
	if v, ok := cfg["repeatCount"].(float64); ok && v > 0 {
		assembled["repeat"] = int32(v)
	}
	if len(deviceIDs) > 0 {
		assembled["deviceIds"] = deviceIDs
	}
	if scenarioName != "" {
		assembled["scenarioName"] = scenarioName
	}
	if busyPolicy != "" {
		assembled["busyPolicy"] = busyPolicy
	}

	raw, err := json.Marshal(assembled)
	if err != nil {
		return nil, fmt.Errorf("re-marshal scenario config: %w", err)
	}
	return scenarioRequestFromRawBody(ctx, db, raw)
}

// normalizeStepTools — body 의 steps[i].tool 이 "FIO" 같은 짧은 이름이면 proto enum 으로 변환.
// protojson 은 정식 이름("BENCHMARK_TOOL_FIO")만 인식하므로 미스매치 시 Tool=UNSPECIFIED 가 되어
// orchestrator 가 "unknown benchmark tool" 로 실패.
//
// hydrateMacroSteps 와 같은 패턴 — raw body 를 다시 한 번 부분 파싱해서 step.tool 을 맞춰준다.
func normalizeStepTools(raw []byte, req *pb.RunScenarioRequest) {
	if len(req.GetSteps()) == 0 {
		return
	}
	var bodyParsed struct {
		Steps []struct {
			Tool string `json:"tool"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &bodyParsed); err != nil {
		return
	}
	for i, step := range req.GetSteps() {
		if i >= len(bodyParsed.Steps) {
			continue
		}
		toolStr := bodyParsed.Steps[i].Tool
		if toolStr == "" {
			continue
		}
		// 이미 protojson 이 enum 매칭에 성공한 경우 (정식 BENCHMARK_TOOL_FIO 보낸 경우) 그대로 둠
		if step.GetTool() != pb.BenchmarkTool_BENCHMARK_TOOL_UNSPECIFIED {
			continue
		}
		req.Steps[i].Tool = parseBenchmarkTool(toolStr)
	}
}

// hydrateMacroSteps — proto RunScenarioRequest 의 type=app_macro step 중 macro 가
// 미완성(events 없음)이면 body 의 macroId 를 보고 DB AppMacro 에서 events JSON 을
// 파싱해 채워준다.
//
// protojson 의 unmarshal 은 frontend 가 보낸 macroId 가 oneof / 별도 필드라면 그대로
// 채우지만, frontend 가 step.macroId 를 plain JSON 으로 보내는 경우 raw body 에서 직접 읽어야 함.
// 따라서 raw body 의 steps[i].macroId 를 다시 parsing 한다.
func hydrateMacroSteps(ctx context.Context, db *sqlitedb.DB, raw []byte, req *pb.RunScenarioRequest) error {
	if len(req.GetSteps()) == 0 {
		return nil
	}
	var bodyParsed struct {
		Steps []struct {
			Type           string `json:"type"`
			MacroID        *int64 `json:"macroId,omitempty"`
			MacroName      string `json:"macroName,omitempty"`
			MacroClearMode string `json:"macroClearMode,omitempty"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &bodyParsed); err != nil {
		return nil // body 가 step 정보 없으면 skip — 다른 type 들은 protojson 이 처리
	}

	for i, step := range req.GetSteps() {
		if step.GetType() != "app_macro" {
			continue
		}
		if i >= len(bodyParsed.Steps) {
			continue
		}
		rawStep := bodyParsed.Steps[i]
		// 이미 macro 가 채워졌고 events 가 있으면 건너뜀
		if m := step.GetMacro(); m != nil && len(m.GetEvents()) > 0 {
			continue
		}
		// macroId 가 없으면 hydrate 할 게 없음
		if rawStep.MacroID == nil {
			continue
		}
		macro, err := db.FindAppMacro(ctx, *rawStep.MacroID)
		if err != nil {
			return err
		}

		// AppMacroConfig 빌드 (portal buildMacroEvent 흐름과 동일)
		cfg := &pb.AppMacroConfig{
			MacroId:   macro.ID,
			MacroName: macro.Name,
		}
		if rawStep.MacroName != "" {
			cfg.MacroName = rawStep.MacroName
		}
		if rawStep.MacroClearMode != "" {
			cfg.ClearMode = rawStep.MacroClearMode
		} else {
			cfg.ClearMode = "force_stop"
		}
		if macro.PackageName.Valid {
			cfg.PackageName = macro.PackageName.String
		}
		if macro.DeviceWidth.Valid {
			cfg.SourceWidth = macro.DeviceWidth.Int32
		} else {
			cfg.SourceWidth = 1080
		}
		if macro.DeviceHeight.Valid {
			cfg.SourceHeight = macro.DeviceHeight.Int32
		} else {
			cfg.SourceHeight = 2400
		}

		// events_json → MacroEvent list (UI 가 보낸 동일 shape, buildMacroEvent 재사용)
		var rawEvents []map[string]any
		if err := json.Unmarshal([]byte(macro.EventsJSON), &rawEvents); err == nil {
			for _, ev := range rawEvents {
				cfg.Events = append(cfg.Events, buildMacroEvent(ev))
			}
		}

		req.Steps[i].Macro = cfg
	}
	return nil
}
