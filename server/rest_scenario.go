package server

import (
	"context"
	"encoding/json"
	"net/http"

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

		req := &pb.RunScenarioRequest{}
		unmarshalOpts := protojson.UnmarshalOptions{DiscardUnknown: true}
		if err := unmarshalOpts.Unmarshal(raw, req); err != nil {
			writeError(w, http.StatusBadRequest, "decode scenario: "+err.Error())
			return
		}
		if req.BusyPolicy == "" {
			req.BusyPolicy = "reject"
		}

		// macroId → DB events 채우기 (db 있을 때만 = standalone).
		// portal frontend 의 AgentScenarioBuilder 가 step 에 macroId 만 담는 경우가 흔함.
		if db != nil {
			if err := hydrateMacroSteps(r.Context(), db, raw, req); err != nil {
				writeError(w, http.StatusBadRequest, "hydrate macro: "+err.Error())
				return
			}
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
			// not found 등은 caller 에 에러 전달
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
