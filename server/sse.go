package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent/adb"
	pb "agent/pb"
)

// SSE 어댑터 — portal AgentController.subscribeJobProgress / monitorDevices 와 동일.
//
// 형식 (W3C EventSource):
//   event: progress\n
//   data: {"jobId":"...", ...}\n
//   \n
//
// portal frontend 는 named event ('progress', 'metrics', 'complete', 'error') 로 리스닝.
// 우리도 동일한 이름을 사용해 frontend 변경 zero.

func registerSSERoutes(mux *http.ServeMux, agent *DeviceAgentServer) {
	// GET /api/agent/benchmark/progress?serverId=&jobId=
	mux.HandleFunc("/api/agent/benchmark/progress", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Query().Get("jobId")
		if jobID == "" {
			writeError(w, http.StatusBadRequest, "jobId required")
			return
		}
		handleProgressSSE(w, r, agent, jobID)
	})

	// GET /api/agent/monitoring/stream?serverId=&deviceIds=...&deviceIds=...&interval=5
	mux.HandleFunc("/api/agent/monitoring/stream", func(w http.ResponseWriter, r *http.Request) {
		handleMonitoringSSE(w, r, agent)
	})

	// GET /api/agent/devices/stream?serverId= — adb.Manager.AddDeviceChangeListener
	// 디바이스 USB 연결/끊김 즉시 push. portal frontend 의 polling 대체.
	mux.HandleFunc("/api/agent/devices/stream", func(w http.ResponseWriter, r *http.Request) {
		handleDevicesSSE(w, r, agent)
	})
}

// handleProgressSSE — orchestrator 또는 traceMgr 의 JobProgress 채널을 SSE 로 push.
// portal SubscribeJobProgress(grpc) ↔ SseEmitter 매핑 그대로.
func handleProgressSSE(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer, jobID string) {
	// 잡 종류를 모르므로 orchestrator 먼저, 실패 시 trace.
	ch, err := agent.orchestrator.SubscribeJobProgress(jobID)
	if err != nil {
		ch, err = agent.traceMgr.SubscribeProgress(jobID)
		if err != nil {
			writeError(w, http.StatusNotFound, "job not found: "+jobID)
			return
		}
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

	// 동시 Write 직렬화 (heartbeat + progress).
	var writeMu sync.Mutex

	emit := func(event string, data any) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		var payload []byte
		if data != nil {
			switch v := data.(type) {
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
		}
		// SSE 라인 작성
		if event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
				return false
			}
		}
		// data 가 비어도 한 줄 보내야 EventSource 가 receive 한다.
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// keepalive — 30초마다 comment 라인. portal 은 SseEmitter timeout 0 (무한) + 클라이언트 EventSource 가
	// 명시적 reconnect 정책. 우리는 idle 가 길어도 proxy timeout 에 안 끊기도록 한다.
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			writeMu.Lock()
			fmt.Fprintf(w, ": keepalive %d\n\n", time.Now().Unix())
			flusher.Flush()
			writeMu.Unlock()
		case progress, ok := <-ch:
			if !ok {
				// 채널 close = 잡 종료. complete 이벤트 발행 후 리턴.
				// 주의: 여기서 OnState 를 "completed" 로 강제하지 않는다. 채널 close 는 "종료"일 뿐
				// 완료를 뜻하지 않으며(cancel/fail 도 채널을 닫는다), 최종 state 는 orchestrator 의
				// finishHook(SetJobFinishHook)이 실제 job.State 로 정확히 DB 에 기록한다.
				// SSE 가 "completed" 로 덮으면 cancel 이 completed 로 오기록되는 race 가 발생한다.
				emit("complete", "{}")
				if rec := currentRecorder(); rec != nil {
					// 결과 metrics 만 DB 에 영구 저장 (agent 재시작 후에도 조회 가능). state 는 finishHook 담당.
					rec.OnResult(ctx, agent, jobID, inferJobTypeFromAgent(agent, jobID))
				}
				return
			}
			payload := progressToMap(progress)
			if !emit("progress", payload) {
				return
			}
			// 잡 상태가 종료 상태면 DB hook (portal 의 onNext 가 state 전이 인지 안하지만,
			// standalone 은 onCompleted 가 stream 종료 후에야 오므로 progress 안의 state 도 참고).
			state := jobStateString(progress.GetState())
			if rec := currentRecorder(); rec != nil {
				if isTerminalState(state) {
					errMsg := progress.GetError()
					rec.OnState(ctx, jobID, state, errMsg)
					rec.OnResult(ctx, agent, jobID, inferJobTypeFromAgent(agent, jobID))
				}
			}
		}
	}
}

// handleMonitoringSSE — collector.StreamMetrics 를 SSE 로 push.
// portal: deviceIds (repeated query param) + interval (sec).
func handleMonitoringSSE(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer) {
	q := r.URL.Query()
	deviceIDs := q["deviceIds"]
	// 다른 클라이언트가 콤마 묶음으로 보낼 수 있으니 분해.
	expanded := make([]string, 0, len(deviceIDs))
	for _, v := range deviceIDs {
		for _, s := range strings.Split(v, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				expanded = append(expanded, s)
			}
		}
	}
	deviceIDs = expanded
	if len(deviceIDs) == 0 {
		deviceIDs = agent.manager.GetOnlineDevices()
	}
	if len(deviceIDs) == 0 {
		writeError(w, http.StatusNotFound, "no online devices")
		return
	}

	intervalSec := uint32(5)
	if v := q.Get("interval"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			intervalSec = uint32(n)
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	setSSEHeaders(w)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	ch := make(chan *pb.DeviceMetrics, len(deviceIDs)*4)
	var wg sync.WaitGroup
	for _, id := range deviceIDs {
		wg.Add(1)
		go func(devID string) {
			defer wg.Done()
			agent.collector.StreamMetrics(ctx, devID, intervalSec, ch)
		}(id)
	}
	// 4단계 정리 (CLAUDE.md goroutine 누수 방지)
	defer func() {
		cancel()
		drainDone := make(chan struct{})
		go func() {
			for range ch {
			}
			close(drainDone)
		}()
		wg.Wait()
		close(ch)
		<-drainDone
	}()

	var writeMu sync.Mutex
	emit := func(event string, data any) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		var payload []byte
		if data != nil {
			b, err := json.Marshal(data)
			if err != nil {
				return false
			}
			payload = b
		}
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			writeMu.Lock()
			fmt.Fprintf(w, ": keepalive %d\n\n", time.Now().Unix())
			flusher.Flush()
			writeMu.Unlock()
		case m, ok := <-ch:
			if !ok {
				return
			}
			if !emit("metrics", metricsToMap(m)) {
				return
			}
		}
	}
}

// progressToMap — portal AgentController.subscribeJobProgress 의 onNext 가 만드는 LinkedHashMap 과 동일.
func progressToMap(p *pb.JobProgress) map[string]any {
	m := map[string]any{
		"jobId":           p.GetJobId(),
		"deviceId":        p.GetDeviceId(),
		"state":           jobStateString(p.GetState()),
		"message":         p.GetMessage(),
		"progressPercent": p.GetProgressPercent(),
		"error":           p.GetError(),
	}
	if len(p.GetMetrics()) > 0 {
		m["metrics"] = p.GetMetrics()
	}
	if p.GetRawOutput() != "" {
		m["rawOutput"] = p.GetRawOutput()
	}
	return m
}

func setSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// 프록시가 Buffering 못하게.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func isTerminalState(state string) bool {
	switch state {
	case "completed", "failed", "partially_failed", "cancelled":
		return true
	default:
		return false
	}
}

// inferJobTypeFromAgent — orchestrator 와 traceMgr 중 어느 쪽에 잡이 있는지로 type 결정.
// SSE 핸들러는 잡 type 을 모르고 일반 jobID 만 받기 때문에 fallback 추정 필요.
func inferJobTypeFromAgent(agent *DeviceAgentServer, jobID string) string {
	if _, err := agent.orchestrator.GetJobStatus(jobID); err == nil {
		return "benchmark"
	}
	if _, err := agent.traceMgr.GetJob(jobID); err == nil {
		return "trace"
	}
	return "benchmark" // 보수적 default
}

// handleDevicesSSE — adb.Manager 의 device change listener 를 SSE 로 흘려준다.
//
// 흐름:
//  1. 연결 직후 현재 디바이스 목록 한 번 push (event: devices) — 첫 화면 깜빡임 방지
//  2. Manager 에 listener 등록 — 변경 시 마다 buffered channel 에 신호
//  3. main loop: ctx.Done / 변경 신호 / 30초 keepalive 셋 중 깨어남
//
// 변경 payload 는 portal 호환 위해 ListDevices 와 동일 shape ({"devices":[...]}) 그대로 push.
// 미세한 incremental diff 보다 풀 목록 push 가 frontend reducer 단순화에 유리.
func handleDevicesSSE(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	setSSEHeaders(w)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var writeMu sync.Mutex
	emit := func(event string) bool {
		// 풀 목록 매번 새로 조회 — agent.ListDevices 는 락만 잡고 끝, 빠름.
		resp, err := agent.ListDevices(ctx, &pb.ListDevicesRequest{})
		if err != nil {
			return false
		}
		devices := make([]map[string]any, 0, len(resp.GetDevices()))
		for _, d := range resp.GetDevices() {
			devices = append(devices, deviceToMap(d))
		}
		payload, mErr := json.Marshal(map[string]any{"devices": devices})
		if mErr != nil {
			return false
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// 1. 초기 push
	if !emit("devices") {
		return
	}

	// 2. listener 등록
	notifyCh := make(chan struct{}, 4)
	cancelListener := agent.manager.AddDeviceChangeListener(func(_ adb.DeviceChange) {
		select {
		case notifyCh <- struct{}{}:
		default:
			// 채널 가득 — 어차피 다음 사이클에서 풀 목록 가져갈 거니 drop 해도 무방
		}
	})
	defer cancelListener()

	// 3. main loop
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-notifyCh:
			if !emit("devices") {
				return
			}
		case <-keepalive.C:
			writeMu.Lock()
			fmt.Fprintf(w, ": keepalive %d\n\n", time.Now().Unix())
			flusher.Flush()
			writeMu.Unlock()
		}
	}
}
