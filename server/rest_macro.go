package server

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pb "agent/pb"
	"agent/storage/sqlitedb"
)

// registerMacroRoutes — portal AgentController 의 macro / app-macros endpoints.
//
// DB CRUD:
//
//	GET    /api/agent/app-macros
//	GET    /api/agent/app-macros/{id}
//	POST   /api/agent/app-macros
//	PUT    /api/agent/app-macros/{id}
//	DELETE /api/agent/app-macros/{id}
//	POST   /api/agent/app-macros/{id}/duplicate
//
// gRPC 위임:
//
//	GET    /api/agent/macro/installed-apps?serverId=&deviceId=
//	POST   /api/agent/macro/start-recording
//	POST   /api/agent/macro/stop-recording
//	POST   /api/agent/macro/replay
//	POST   /api/agent/macro/screenshot
//	POST   /api/agent/macro/ocr
func registerMacroRoutes(mux *http.ServeMux, agent *DeviceAgentServer, db *sqlitedb.DB) {
	// app-macros CRUD
	mux.HandleFunc("/api/agent/app-macros", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := db.ListAppMacros(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out := make([]map[string]any, 0, len(list))
			for _, m := range list {
				out = append(out, macroToMap(m))
			}
			writeJSON(w, http.StatusOK, out)
		case http.MethodPost:
			body, err := readJSONBody(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "decode: "+err.Error())
				return
			}
			m := buildMacroFromBody(body)
			saved, err := db.CreateAppMacro(r.Context(), m)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, macroToMap(saved))
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/agent/app-macros/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/agent/app-macros/")
		parts := strings.Split(rest, "/")
		if len(parts) == 0 || parts[0] == "" {
			writeError(w, http.StatusNotFound, "id required")
			return
		}
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id: "+parts[0])
			return
		}

		// /app-macros/{id}/duplicate
		if len(parts) == 2 && parts[1] == "duplicate" && r.Method == http.MethodPost {
			src, err := db.FindAppMacro(r.Context(), id)
			if errors.Is(err, sqlitedb.ErrNotFound) {
				writeError(w, http.StatusNotFound, "macro not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			clone := *src
			clone.ID = 0
			clone.Name = src.Name + " (copy)"
			created, err := db.CreateAppMacro(r.Context(), &clone)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, macroToMap(created))
			return
		}

		// /app-macros/{id}
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				m, err := db.FindAppMacro(r.Context(), id)
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "macro not found")
					return
				}
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, macroToMap(m))
			case http.MethodPut:
				body, err := readJSONBody(r)
				if err != nil {
					writeError(w, http.StatusBadRequest, "decode: "+err.Error())
					return
				}
				updated, err := db.UpdateAppMacro(r.Context(), id, buildMacroFromBody(body))
				if errors.Is(err, sqlitedb.ErrNotFound) {
					writeError(w, http.StatusNotFound, "macro not found")
					return
				}
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, macroToMap(updated))
			case http.MethodDelete:
				if err := db.DeleteAppMacro(r.Context(), id); err != nil {
					if errors.Is(err, sqlitedb.ErrNotFound) {
						writeError(w, http.StatusNotFound, "macro not found")
						return
					}
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"success": true})
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}

		writeError(w, http.StatusNotFound, "not found")
	})

	// macro/installed-apps  (gRPC ListInstalledApps)
	mux.HandleFunc("/api/agent/macro/installed-apps", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		deviceID := r.URL.Query().Get("deviceId")
		if deviceID == "" {
			writeError(w, http.StatusBadRequest, "deviceId required")
			return
		}
		resp, err := agent.ListInstalledApps(r.Context(), &pb.ListInstalledAppsRequest{DeviceId: deviceID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		apps := make([]map[string]any, 0, len(resp.GetApps()))
		for _, a := range resp.GetApps() {
			apps = append(apps, map[string]any{
				"packageName": a.GetPackageName(),
				"appName":     a.GetAppName(),
			})
		}
		writeJSON(w, http.StatusOK, apps)
	})

	// macro/ui-elements  (gRPC ListUiElements) — 요소 기반 시나리오 빌더용
	mux.HandleFunc("/api/agent/macro/ui-elements", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		deviceID := r.URL.Query().Get("deviceId")
		if deviceID == "" {
			writeError(w, http.StatusBadRequest, "deviceId required")
			return
		}
		// 기본값: clickable 요소만. clickableOnly=false 로 전체 요청 가능.
		clickableOnly := r.URL.Query().Get("clickableOnly") != "false"
		resp, err := agent.ListUiElements(r.Context(), &pb.ListUiElementsRequest{
			DeviceId:      deviceID,
			ClickableOnly: clickableOnly,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		els := make([]map[string]any, 0, len(resp.GetElements()))
		for _, e := range resp.GetElements() {
			els = append(els, uiElementToMap(e))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":      resp.GetSuccess(),
			"deviceWidth":  resp.GetDeviceWidth(),
			"deviceHeight": resp.GetDeviceHeight(),
			"elements":     els,
		})
	})

	// macro/start-recording
	mux.HandleFunc("/api/agent/macro/start-recording", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		deviceID, _ := body["deviceId"].(string)
		resp, err := agent.StartRecording(r.Context(), &pb.StartRecordingRequest{DeviceId: deviceID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":   resp.GetSuccess(),
			"sessionId": resp.GetSessionId(),
		})
	})

	// macro/stop-recording
	mux.HandleFunc("/api/agent/macro/stop-recording", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		deviceID, _ := body["deviceId"].(string)
		sessionID, _ := body["sessionId"].(string)
		resp, err := agent.StopRecording(r.Context(), &pb.StopRecordingRequest{
			DeviceId: deviceID, SessionId: sessionID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		events := make([]map[string]any, 0, len(resp.GetEvents()))
		for _, e := range resp.GetEvents() {
			events = append(events, macroEventToMap(e))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":      resp.GetSuccess(),
			"deviceWidth":  resp.GetDeviceWidth(),
			"deviceHeight": resp.GetDeviceHeight(),
			"events":       events,
		})
	})

	// macro/replay
	mux.HandleFunc("/api/agent/macro/replay", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		deviceID, _ := body["deviceId"].(string)
		jobID, _ := body["jobId"].(string)
		sourceWidth := int32(1080)
		sourceHeight := int32(2400)
		if v, ok := numberOf(body["sourceWidth"]); ok {
			sourceWidth = int32(v)
		}
		if v, ok := numberOf(body["sourceHeight"]); ok {
			sourceHeight = int32(v)
		}
		eventsRaw, _ := body["events"].([]any)
		events := make([]*pb.MacroEvent, 0, len(eventsRaw))
		for _, ev := range eventsRaw {
			if m, ok := ev.(map[string]any); ok {
				events = append(events, buildMacroEvent(m))
			}
		}
		resp, err := agent.ReplayMacro(r.Context(), &pb.ReplayMacroRequest{
			DeviceId:     deviceID,
			Events:       events,
			SourceWidth:  sourceWidth,
			SourceHeight: sourceHeight,
			JobId:        jobID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":    resp.GetSuccess(),
			"message":    resp.GetMessage(),
			"ocrResults": resp.GetOcrResults(),
			"metrics":    resp.GetMetrics(),
		})
	})

	// macro/screenshot
	mux.HandleFunc("/api/agent/macro/screenshot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		deviceID, _ := body["deviceId"].(string)
		resp, err := agent.TakeScreenshot(r.Context(), &pb.TakeScreenshotRequest{DeviceId: deviceID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":     resp.GetSuccess(),
			"width":       resp.GetWidth(),
			"height":      resp.GetHeight(),
			"imageBase64": base64.StdEncoding.EncodeToString(resp.GetImageData()),
		})
	})

	// macro/ocr
	mux.HandleFunc("/api/agent/macro/ocr", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		deviceID, _ := body["deviceId"].(string)
		pattern, _ := body["extractPattern"].(string)
		req := &pb.ScreenshotOcrRequest{
			DeviceId:       deviceID,
			ExtractPattern: pattern,
		}
		if r, ok := body["region"].(map[string]any); ok {
			req.Region = buildOcrRegion(r)
		}
		resp, err := agent.ScreenshotOcr(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":        resp.GetSuccess(),
			"fullText":       resp.GetFullText(),
			"extractedValue": resp.GetExtractedValue(),
			"imageBase64":    base64.StdEncoding.EncodeToString(resp.GetImageData()),
		})
	})
}

// ---------- helpers ----------

func buildMacroFromBody(body map[string]any) *sqlitedb.AppMacro {
	m := &sqlitedb.AppMacro{}
	if v, ok := body["name"].(string); ok {
		m.Name = v
	}
	if v, ok := body["description"].(string); ok {
		m.Description = v
	}
	if v, ok := body["packageName"].(string); ok {
		m.PackageName = sql.NullString{String: v, Valid: v != ""}
	}
	// eventsJson 은 string 또는 object 두 가지로 올 수 있다.
	if v, ok := body["eventsJson"].(string); ok {
		m.EventsJSON = v
	} else if v, exists := body["eventsJson"]; exists && v != nil {
		// object/array → string 화
		if data, err := json.Marshal(v); err == nil {
			m.EventsJSON = string(data)
		}
	}
	if v, ok := numberOf(body["deviceWidth"]); ok {
		m.DeviceWidth = sql.NullInt32{Int32: int32(v), Valid: true}
	}
	if v, ok := numberOf(body["deviceHeight"]); ok {
		m.DeviceHeight = sql.NullInt32{Int32: int32(v), Valid: true}
	}
	return m
}

func macroToMap(m *sqlitedb.AppMacro) map[string]any {
	out := map[string]any{
		"id":          m.ID,
		"name":        m.Name,
		"description": m.Description,
		"packageName": nullString(m.PackageName),
		"eventsJson":  m.EventsJSON,
		"createdAt":   m.CreatedAt.Format(time.RFC3339Nano),
		"updatedAt":   m.UpdatedAt.Format(time.RFC3339Nano),
	}
	if m.DeviceWidth.Valid {
		out["deviceWidth"] = m.DeviceWidth.Int32
	} else {
		out["deviceWidth"] = nil
	}
	if m.DeviceHeight.Valid {
		out["deviceHeight"] = m.DeviceHeight.Int32
	} else {
		out["deviceHeight"] = nil
	}
	return out
}

func macroEventToMap(e *pb.MacroEvent) map[string]any {
	m := map[string]any{
		"t":    e.GetT(),
		"type": e.GetType(),
	}
	if e.GetX() != 0 || e.GetY() != 0 {
		m["x"] = e.GetX()
		m["y"] = e.GetY()
	}
	if e.GetX2() != 0 || e.GetY2() != 0 {
		m["x2"] = e.GetX2()
		m["y2"] = e.GetY2()
	}
	if e.GetDuration() != 0 {
		m["duration"] = e.GetDuration()
	}
	if e.GetKeycode() != 0 {
		m["keycode"] = e.GetKeycode()
	}
	if e.GetSeconds() != 0 {
		m["seconds"] = e.GetSeconds()
	}
	if e.GetWaitMethod() != "" {
		m["waitMethod"] = e.GetWaitMethod()
	}
	if e.GetWaitPattern() != "" {
		m["waitPattern"] = e.GetWaitPattern()
	}
	if e.GetTimeout() != 0 {
		m["timeout"] = e.GetTimeout()
	}
	if e.GetPollInterval() != 0 {
		m["pollInterval"] = e.GetPollInterval()
	}
	if e.GetName() != "" {
		m["name"] = e.GetName()
	}
	if e.GetDirection() != "" {
		m["direction"] = e.GetDirection()
	}
	if e.GetMaxScrolls() != 0 {
		m["maxScrolls"] = e.GetMaxScrolls()
	}
	if e.GetScrollPause() != 0 {
		m["scrollPause"] = e.GetScrollPause()
	}
	if e.GetOcrPattern() != "" {
		m["ocrPattern"] = e.GetOcrPattern()
	}
	if region := e.GetOcrRegion(); region != nil {
		m["ocrRegion"] = ocrRegionToMap(region)
	}
	// 요소 기반 탭 셀렉터 (tap_element) + text 입력
	if e.GetElementResourceId() != "" {
		m["elementResourceId"] = e.GetElementResourceId()
	}
	if e.GetElementText() != "" {
		m["elementText"] = e.GetElementText()
	}
	if e.GetElementContentDesc() != "" {
		m["elementContentDesc"] = e.GetElementContentDesc()
	}
	if e.GetInputText() != "" {
		m["inputText"] = e.GetInputText()
	}
	// 패턴 매칭 (동적 콘텐츠 재현)
	if e.GetElementMatchMode() != "" {
		m["elementMatchMode"] = e.GetElementMatchMode()
	}
	if e.GetElementIndex() != 0 {
		m["elementIndex"] = e.GetElementIndex()
	}
	if e.GetElementContainerId() != "" {
		m["elementContainerId"] = e.GetElementContainerId()
	}
	return m
}

func uiElementToMap(e *pb.UiElement) map[string]any {
	return map[string]any{
		"resourceId":  e.GetResourceId(),
		"text":        e.GetText(),
		"contentDesc": e.GetContentDesc(),
		"class":       e.GetClass(),
		"clickable":   e.GetClickable(),
		"centerX":     e.GetCenterX(),
		"centerY":     e.GetCenterY(),
		"bounds": []int32{
			e.GetBoundLeft(), e.GetBoundTop(), e.GetBoundRight(), e.GetBoundBottom(),
		},
		"containerId": e.GetContainerId(),
	}
}

func buildMacroEvent(e map[string]any) *pb.MacroEvent {
	out := &pb.MacroEvent{}
	if v, ok := numberOf(e["t"]); ok {
		out.T = int64(v)
	}
	if v, ok := e["type"].(string); ok {
		out.Type = v
	}
	if v, ok := numberOf(e["x"]); ok {
		out.X = int32(v)
	}
	if v, ok := numberOf(e["y"]); ok {
		out.Y = int32(v)
	}
	if v, ok := numberOf(e["x2"]); ok {
		out.X2 = int32(v)
	}
	if v, ok := numberOf(e["y2"]); ok {
		out.Y2 = int32(v)
	}
	if v, ok := numberOf(e["duration"]); ok {
		out.Duration = int32(v)
	}
	if v, ok := numberOf(e["keycode"]); ok {
		out.Keycode = int32(v)
	}
	if v, ok := numberOf(e["seconds"]); ok {
		out.Seconds = int32(v)
	}
	if v, ok := e["waitMethod"].(string); ok {
		out.WaitMethod = v
	}
	if v, ok := e["waitPattern"].(string); ok {
		out.WaitPattern = v
	}
	if v, ok := numberOf(e["timeout"]); ok {
		out.Timeout = int32(v)
	}
	if v, ok := numberOf(e["pollInterval"]); ok {
		out.PollInterval = int32(v)
	}
	if v, ok := e["name"].(string); ok {
		out.Name = v
	}
	if v, ok := e["direction"].(string); ok {
		out.Direction = v
	}
	if v, ok := numberOf(e["maxScrolls"]); ok {
		out.MaxScrolls = int32(v)
	}
	if v, ok := numberOf(e["scrollPause"]); ok {
		out.ScrollPause = int32(v)
	}
	if v, ok := e["ocrPattern"].(string); ok {
		out.OcrPattern = v
	}
	if region, ok := e["ocrRegion"].(map[string]any); ok {
		out.OcrRegion = buildOcrRegion(region)
	}
	// 요소 기반 탭 셀렉터 (tap_element) + text 입력
	if v, ok := e["elementResourceId"].(string); ok {
		out.ElementResourceId = v
	}
	if v, ok := e["elementText"].(string); ok {
		out.ElementText = v
	}
	if v, ok := e["elementContentDesc"].(string); ok {
		out.ElementContentDesc = v
	}
	if v, ok := e["inputText"].(string); ok {
		out.InputText = v
	}
	// 패턴 매칭 (동적 콘텐츠 재현)
	if v, ok := e["elementMatchMode"].(string); ok {
		out.ElementMatchMode = v
	}
	if v, ok := numberOf(e["elementIndex"]); ok {
		out.ElementIndex = int32(v)
	}
	if v, ok := e["elementContainerId"].(string); ok {
		out.ElementContainerId = v
	}
	return out
}

func ocrRegionToMap(r *pb.OcrRegion) map[string]any {
	return map[string]any{
		"x": r.GetX(), "y": r.GetY(),
		"width": r.GetWidth(), "height": r.GetHeight(),
	}
}

func buildOcrRegion(r map[string]any) *pb.OcrRegion {
	out := &pb.OcrRegion{}
	if v, ok := numberOf(r["x"]); ok {
		out.X = int32(v)
	}
	if v, ok := numberOf(r["y"]); ok {
		out.Y = int32(v)
	}
	if v, ok := numberOf(r["width"]); ok {
		out.Width = int32(v)
	}
	if v, ok := numberOf(r["height"]); ok {
		out.Height = int32(v)
	}
	return out
}
