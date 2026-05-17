package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	pb "agent/pb"
)

// wsUpgrader — REST 어댑터용 WebSocket 업그레이더. standalone localhost 환경이라
// CheckOrigin 은 모두 허용 (screen/handler.go:15-19 와 동일 정책).
var wsUpgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  4 * 1024,
	WriteBufferSize: 64 * 1024,
}

// registerWSRoutes 는 mux 에 /ws/* 엔드포인트를 마운트한다.
//   /ws/jobs/{id}/progress  — benchmark/scenario/trace 의 JobProgress push
//   /ws/monitor             — DeviceMetrics 1초 단위 push
func registerWSRoutes(mux *http.ServeMux, agent *DeviceAgentServer) {
	mux.HandleFunc("/ws/jobs/", func(w http.ResponseWriter, r *http.Request) {
		// /ws/jobs/{id}/progress
		rest := strings.TrimPrefix(r.URL.Path, "/ws/jobs/")
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || parts[1] != "progress" {
			http.NotFound(w, r)
			return
		}
		handleJobProgressWS(w, r, agent, parts[0])
	})

	mux.HandleFunc("/ws/monitor", func(w http.ResponseWriter, r *http.Request) {
		handleMonitorWS(w, r, agent)
	})
}

// handleJobProgressWS — orchestrator 또는 traceMgr 의 progress 채널을 WS 로 forward.
// gRPC SubscribeJobProgress (grpc.go:101) 의 select 패턴을 그대로 답습.
func handleJobProgressWS(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer, jobID string) {
	ch, err := agent.orchestrator.SubscribeJobProgress(jobID)
	if err != nil {
		ch, err = agent.traceMgr.SubscribeProgress(jobID)
		if err != nil {
			http.Error(w, "job not found: "+jobID, http.StatusNotFound)
			return
		}
	}

	ws, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("ws upgrade failed", "path", r.URL.Path, "error", err)
		return
	}
	defer ws.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 동시 Write 직렬화 (progress + ping). screen/handler.go:100 패턴.
	var wsWriteMu sync.Mutex
	writeJSON := func(v any) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		return ws.WriteJSON(v)
	}
	writeClose := func(code int, msg string) {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, msg))
	}

	// reader goroutine — 클라이언트 close 감지용 (메시지 안 읽으면 ping/close 안 들어옴).
	go func() {
		defer cancel()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// keepalive ping.
	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			writeClose(websocket.CloseNormalClosure, "done")
			return
		case <-pingTicker.C:
			wsWriteMu.Lock()
			_ = ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			wsWriteMu.Unlock()
		case progress, ok := <-ch:
			if !ok {
				writeClose(websocket.CloseNormalClosure, "stream closed")
				return
			}
			payload, mErr := marshalOpts.Marshal(progress)
			if mErr != nil {
				slog.Warn("ws progress marshal failed", "error", mErr)
				continue
			}
			if err := writeJSON(jsonRaw(payload)); err != nil {
				return
			}
		}
	}
}

// handleMonitorWS — collector.StreamMetrics 를 fan-in 해서 WS push.
// gRPC MonitorDevices (grpc.go:545) 의 4단계 정리(cancel + drain + wg.Wait + close) 패턴 그대로.
func handleMonitorWS(w http.ResponseWriter, r *http.Request, agent *DeviceAgentServer) {
	q := r.URL.Query()
	intervalSec := uint32(parseFloat64(q.Get("interval_seconds"), 1))
	if intervalSec == 0 {
		if ms := q.Get("interval_ms"); ms != "" {
			if n, err := strconv.Atoi(ms); err == nil && n > 0 {
				intervalSec = uint32((n + 999) / 1000)
			}
		}
	}
	if intervalSec == 0 {
		intervalSec = 1
	}

	var deviceIDs []string
	if serials := q.Get("serials"); serials != "" {
		for _, s := range strings.Split(serials, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				deviceIDs = append(deviceIDs, s)
			}
		}
	}
	if len(deviceIDs) == 0 {
		deviceIDs = agent.manager.GetOnlineDevices()
	}
	if len(deviceIDs) == 0 {
		http.Error(w, "no online devices", http.StatusNotFound)
		return
	}

	ws, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("ws upgrade failed", "path", r.URL.Path, "error", err)
		return
	}
	defer ws.Close()

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

	// 4단계 정리 (CLAUDE.md "Goroutine 누수 방지" 패턴 + grpc.go:574 동일)
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

	var wsWriteMu sync.Mutex
	writeJSON := func(v any) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		return ws.WriteJSON(v)
	}

	// reader goroutine for close detection.
	go func() {
		defer cancel()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			wsWriteMu.Lock()
			_ = ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			wsWriteMu.Unlock()
		case metrics, ok := <-ch:
			if !ok {
				return
			}
			payload, mErr := marshalOpts.Marshal(metrics)
			if mErr != nil {
				slog.Warn("ws metrics marshal failed", "error", mErr)
				continue
			}
			if err := writeJSON(jsonRaw(payload)); err != nil {
				return
			}
		}
	}
}

// jsonRaw 는 이미 직렬화된 protojson 바이트를 WriteJSON 에 그대로 흘려보내기 위한 어댑터.
type jsonRaw []byte

func (r jsonRaw) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return []byte(r), nil
}
