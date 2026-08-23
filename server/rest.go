package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	pb "agent/pb"
)

// registerRESTRoutes 는 /api/agent/* 핸들러를 mux 에 마운트한다.
//
// portal AgentController(/api/agent prefix) 와 path/method/응답 shape 가 동일하다.
// portal frontend 가 그대로 동작하도록 만든 호환 어댑터.
//
// Phase 1 범위:
//
//	Device(3) + Benchmark(5: run/status/result/cancel/delete) + Trace(5) = 13 endpoint
//	진행률 SSE(/benchmark/progress), Monitor SSE, Scenario, Server CRUD, Macro, Preset, Schedule, Archive, Execution
//	은 후속 Phase 에서 추가.
//
// serverId 쿼리 파라미터는 portal 호환을 위해 받기만 하고 무시한다 (standalone 은 self).
func registerRESTRoutes(mux *http.ServeMux, agent *DeviceAgentServer) {
	// ---------- Device ----------
	// GET /api/agent/devices?serverId=
	mux.HandleFunc("/api/agent/devices", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		resp, err := agent.ListDevices(r.Context(), &pb.ListDevicesRequest{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		devices := make([]map[string]any, 0, len(resp.GetDevices()))
		for _, d := range resp.GetDevices() {
			devices = append(devices, deviceToMap(d))
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
	})

	// POST /api/agent/devices/{serial}/connect
	// POST /api/agent/devices/{serial}/disconnect
	mux.HandleFunc("/api/agent/devices/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/agent/devices/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		serial, action := parts[0], parts[1]
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		switch action {
		case "connect":
			resp, err := agent.ConnectDevice(r.Context(), &pb.ConnectDeviceRequest{Serial: serial})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"success": resp.GetSuccess(),
				"message": resp.GetMessage(),
			})
		case "disconnect":
			resp, err := agent.DisconnectDevice(r.Context(), &pb.DisconnectDeviceRequest{Serial: serial})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"success": resp.GetSuccess(),
			})
		default:
			writeError(w, http.StatusNotFound, "unknown action: "+action)
		}
	})

	// ---------- Benchmark ----------
	// POST /api/agent/benchmark/run?serverId=
	mux.HandleFunc("/api/agent/benchmark/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readJSONBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		deviceIds := stringSlice(body["deviceIds"])
		toolStr, _ := body["tool"].(string)
		jobName, _ := body["jobName"].(string)
		busyPolicy, _ := body["busyPolicy"].(string)
		if busyPolicy == "" {
			busyPolicy = "reject"
		}
		params := stringMap(body["params"])

		req := &pb.RunBenchmarkRequest{
			DeviceIds:  deviceIds,
			Tool:       parseBenchmarkTool(toolStr),
			Params:     params,
			JobName:    jobName,
			BusyPolicy: busyPolicy,
		}
		resp, err := agent.RunBenchmark(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// portal AgentController.runBenchmark 의 jobExecutionService.save 와 같은 시점.
		if rec := currentRecorder(); rec != nil {
			rec.OnStart(r.Context(), resp.GetJobId(), "benchmark", toolStr, jobName, deviceIds, body)
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobId": resp.GetJobId()})
	})

	// GET /api/agent/benchmark/status?serverId=&jobId=
	mux.HandleFunc("/api/agent/benchmark/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		jobID := r.URL.Query().Get("jobId")
		if jobID == "" {
			writeError(w, http.StatusBadRequest, "jobId required")
			return
		}
		resp, err := agent.GetJobStatus(r.Context(), &pb.GetJobStatusRequest{JobId: jobID})
		if err != nil {
			// portal 은 'not found' 메시지 시 404 + state=failed 반환
			if strings.Contains(err.Error(), "not found") {
				// DB 의 stale 'running' 도 같이 정리.
				if rec := currentRecorder(); rec != nil {
					rec.OnState(r.Context(), jobID, "failed", "Job not found on agent (expired)")
				}
				writeJSON(w, http.StatusNotFound, map[string]any{
					"error": "Job not found: " + jobID,
					"state": "failed",
				})
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 잡 상태가 terminal 이면 DB 도 즉시 sync (SSE 를 별도 subscribe 안 한 경우 — Schedule fire, polling 등).
		state := jobStateString(resp.GetState())
		if isTerminalState(state) {
			if rec := currentRecorder(); rec != nil {
				rec.OnState(r.Context(), jobID, state, "")
				// agent 메모리에 결과가 살아있을 때 한 번 fetch → DB 영구 저장.
				// 재시작 후에는 메모리에 없어 skip (이미 DB 에 저장됐을 것).
				rec.OnResult(r.Context(), agent, jobID, "benchmark")
			}
		}
		statuses := make([]map[string]any, 0, len(resp.GetDeviceStatuses()))
		for _, s := range resp.GetDeviceStatuses() {
			statuses = append(statuses, deviceJobStatusToMap(s))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"jobId":            resp.GetJobId(),
			"state":            state,
			"totalDevices":     resp.GetTotalDevices(),
			"completedDevices": resp.GetCompletedDevices(),
			"failedDevices":    resp.GetFailedDevices(),
			"deviceStatuses":   statuses,
		})
	})

	// GET /api/agent/benchmark/result?serverId=&jobId=&deviceId=
	mux.HandleFunc("/api/agent/benchmark/result", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		jobID := r.URL.Query().Get("jobId")
		deviceID := r.URL.Query().Get("deviceId")
		if jobID == "" {
			writeError(w, http.StatusBadRequest, "jobId required")
			return
		}
		resp, err := agent.GetBenchmarkResult(r.Context(), &pb.GetBenchmarkResultRequest{
			JobId:    jobID,
			DeviceId: deviceID,
		})
		if err != nil {
			// 만료된 잡 (agent 메모리에서 사라짐) — portal status 패턴과 동일하게 404 + state:"failed" 반환.
			if strings.Contains(err.Error(), "not found") {
				if rec := currentRecorder(); rec != nil {
					rec.OnState(r.Context(), jobID, "failed", "Job not found on agent (expired)")
				}
				writeJSON(w, http.StatusNotFound, map[string]any{
					"error":   "Job not found: " + jobID,
					"state":   "failed",
					"results": []any{},
				})
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		results := make([]map[string]any, 0, len(resp.GetResults()))
		for _, br := range resp.GetResults() {
			results = append(results, benchmarkResultToMap(br))
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	})

	// ---------- Job management ----------
	// DELETE /api/agent/jobs/{jobId}?serverId=
	// POST   /api/agent/jobs/{jobId}/cancel?serverId=
	mux.HandleFunc("/api/agent/jobs/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/agent/jobs/")
		parts := strings.Split(rest, "/")
		if len(parts) == 0 || parts[0] == "" {
			writeError(w, http.StatusNotFound, "job id required")
			return
		}
		jobID := parts[0]
		switch {
		case len(parts) == 1 && r.Method == http.MethodDelete:
			resp, err := agent.DeleteJob(r.Context(), &pb.DeleteJobRequest{JobId: jobID})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"success": resp.GetSuccess(),
				"message": resp.GetMessage(),
			})
		case len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost:
			resp, err := agent.CancelJob(r.Context(), &pb.CancelJobRequest{JobId: jobID})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"success": resp.GetSuccess(),
				"message": resp.GetMessage(),
			})
		default:
			writeError(w, http.StatusNotFound, "not found: "+r.URL.Path)
		}
	})

	// ---------- Trace ----------
	// POST /api/agent/trace/start?serverId=
	mux.HandleFunc("/api/agent/trace/start", func(w http.ResponseWriter, r *http.Request) {
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
		traceType, _ := body["traceType"].(string)
		if traceType == "" {
			traceType = "ufs"
		}
		// 알 수 없는 trace_type 은 여기서 거절한다. 그냥 통과시키면 수집기가
		// 아무 이벤트도 안 켜고 "성공" 으로 끝나서, 오타가 "이벤트 0건" 으로만
		// 드러난다 — 원인 추적이 어려운 종류의 조용한 실패다.
		if !validTraceTypes[traceType] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"unknown traceType %q (allowed: ufs, block, both, ufscustom, fsio_ufs, fsio_block)", traceType))
			return
		}
		jobName, _ := body["jobName"].(string)
		var windowSec int32
		if v, ok := numberOf(body["windowSeconds"]); ok {
			windowSec = int32(v)
		}
		resp, err := agent.StartTrace(r.Context(), &pb.StartTraceRequest{
			DeviceId:      deviceID,
			TraceType:     traceType,
			WindowSeconds: windowSec,
			JobName:       jobName,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rec := currentRecorder(); rec != nil {
			rec.OnStart(r.Context(), resp.GetJobId(), "trace", traceType, jobName, []string{deviceID}, body)
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobId": resp.GetJobId()})
	})

	// POST /api/agent/trace/{jobId}/stop
	// POST /api/agent/trace/{jobId}/reparse
	mux.HandleFunc("/api/agent/trace/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/agent/trace/")
		parts := strings.Split(rest, "/")
		// Special: /api/agent/trace/result and /trace/raw 는 POST body 기반이라 별도 처리
		if len(parts) == 1 {
			switch parts[0] {
			case "result":
				handleTraceResult(w, r, agent)
				return
			case "raw":
				handleTraceRaw(w, r, agent)
				return
			}
		}
		if len(parts) != 2 {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		jobID, action := parts[0], parts[1]
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		switch action {
		case "stop":
			resp, err := agent.StopTrace(r.Context(), &pb.StopTraceRequest{JobId: jobID})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"success": resp.GetSuccess(),
				"message": resp.GetMessage(),
			})
		case "reparse":
			resp, err := agent.ReparseTrace(r.Context(), &pb.ReparseTraceRequest{JobId: jobID})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"success": resp.GetSuccess(),
				"message": resp.GetMessage(),
			})
		default:
			writeError(w, http.StatusNotFound, "unknown action: "+action)
		}
	})
}

// handleTraceResult: POST /api/agent/trace/result body { jobIds, filter?, latencyRangesMs? }
func handleTraceResult(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := readJSONBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	jobIDs := stringSlice(body["jobIds"])
	if len(jobIDs) == 0 {
		writeError(w, http.StatusBadRequest, "jobIds required")
		return
	}
	req := &pb.GetTraceResultRequest{JobIds: jobIDs}
	if fmap, ok := body["filter"].(map[string]any); ok {
		req.Filter = buildTraceFilter(fmap)
	}
	if arr, ok := body["latencyRangesMs"].([]any); ok {
		for _, x := range arr {
			if n, ok := numberOf(x); ok {
				req.LatencyRangesMs = append(req.LatencyRangesMs, n)
			}
		}
	}
	resp, err := agent.GetTraceResult(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobId": resp.GetJobId(),
		"stats": traceStatsToMap(resp.GetStats()),
	})
}

// handleTraceRaw: POST /api/agent/trace/raw body { jobIds, filter? }
func handleTraceRaw(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := readJSONBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	jobIDs := stringSlice(body["jobIds"])
	if len(jobIDs) == 0 {
		writeError(w, http.StatusBadRequest, "jobIds required")
		return
	}
	req := &pb.GetTraceRawDataRequest{JobIds: jobIDs}
	if fmap, ok := body["filter"].(map[string]any); ok {
		req.Filter = buildTraceFilter(fmap)
	}
	resp, err := agent.GetTraceRawData(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	events := make([]map[string]any, 0, len(resp.GetEvents()))
	for _, e := range resp.GetEvents() {
		events = append(events, traceEventToMap(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobId":         resp.GetJobId(),
		"totalEvents":   resp.GetTotalEvents(),
		"sampledEvents": resp.GetSampledEvents(),
		"isSampled":     resp.GetIsSampled(),
		"events":        events,
	})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSONBody(r *http.Request) (map[string]any, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// readRawBody — body 를 []byte 로 그대로 받는다. protojson 로 직접 unmarshal 하거나
// 풀 raw 보존이 필요한 경우 (scenario 의 macroId hydrate 등) 사용.
func readRawBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(r.Body)
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, x := range m {
		if s, ok := x.(string); ok {
			out[k] = s
		}
	}
	return out
}

// validTraceTypes — StartTrace 가 받아들이는 trace_type.
// scenario/steptypes.go 의 trace_start enum 과 같은 집합이어야 한다
// (ufscustom 은 파일 업로드 전용이라 그쪽 enum 에는 없다).
var validTraceTypes = map[string]bool{
	"ufs": true, "block": true, "both": true, "ufscustom": true,
	"fsio_ufs": true, "fsio_block": true,
}
