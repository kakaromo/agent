package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"agent/adb"
)

// shellResizeMessage — xterm 측 frontend 가 JSON 으로 보내는 resize 이벤트.
//   { "type": "resize", "cols": 80, "rows": 24 }
type shellResizeMessage struct {
	Type string `json:"type"`
	Cols uint32 `json:"cols"`
	Rows uint32 `json:"rows"`
}

// shellWSHandler — xterm.js (WebSocket) ↔ adb.PTYSession bridge.
//
// 흐름:
//  1. URL 에서 deviceId 추출, query param cols/rows 로 PTY 초기 크기 결정
//  2. ManagedDevice.Device.ShellPTY 로 PTY 세션 열기
//  3. 두 goroutine + 4단계 cleanup (screen/handler.go 답습):
//       - recv goroutine: ws.ReadMessage → JSON resize 또는 text input
//       - send goroutine: pty.Read → ws.WriteMessage (text frame)
//
// gorilla/websocket 의 동시 Write 금지 정책에 따라 wsWriteMu 로 직렬화.
// path: /api/agent/shell/{deviceId} (portal 호환) 또는 /ws/shell/{deviceId}.
type shellWSHandler struct {
	adbManager *adb.Manager
}

func newShellWSHandler(mgr *adb.Manager) http.Handler {
	return &shellWSHandler{adbManager: mgr}
}

func (h *shellWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// path 가 /api/agent/shell/{id} 든 /ws/shell/{id} 든 동일 처리.
	// http.go 에서 StripPrefix 로 정규화하므로 여기선 그냥 마지막 segment.
	path := strings.TrimPrefix(r.URL.Path, "/ws/shell/")
	deviceID := strings.TrimSuffix(path, "/")
	if deviceID == "" {
		http.Error(w, "device_id required", http.StatusBadRequest)
		return
	}

	md, err := h.adbManager.GetDevice(deviceID)
	if err != nil {
		http.Error(w, "device not found: "+err.Error(), http.StatusNotFound)
		return
	}

	cols := uint32(parseUintQuery(r, "cols", 80))
	rows := uint32(parseUintQuery(r, "rows", 24))

	ws, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("shell ws upgrade failed", "error", err)
		return
	}
	defer ws.Close()
	ws.EnableWriteCompression(false)

	var wsWriteMu sync.Mutex
	wsWriteText := func(data []byte) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		return ws.WriteMessage(websocket.TextMessage, data)
	}
	wsWriteClose := func(code int, msg string) {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, msg))
	}

	slog.Info("shell session requested", "device", deviceID, "serial", md.Serial, "cols", cols, "rows", rows)

	pty, err := md.Device.ShellPTY(r.Context(), cols, rows)
	if err != nil {
		slog.Error("open pty failed", "device", deviceID, "error", err)
		wsWriteClose(websocket.CloseInternalServerErr, err.Error())
		return
	}
	defer pty.Close()

	// 두 방향 goroutine 추적 — screen/handler.go 패턴 답습.
	//   - send 가 먼저 끝나면 pty.Close 로 recv 의 ws.ReadMessage 깨움
	//   - recv 가 먼저 끝나면 pty.Close 로 send 의 pty.Read 깨움
	sendDone := make(chan struct{})
	recvDone := make(chan struct{})

	// send: pty → ws text frame
	go func() {
		defer close(sendDone)
		buf := make([]byte, 4096)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				if werr := wsWriteText(buf[:n]); werr != nil {
					slog.Debug("shell ws write error", "error", werr)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// recv: ws → pty
	go func() {
		defer close(recvDone)
		for {
			msgType, data, err := ws.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					slog.Debug("shell ws read error", "error", err)
				}
				return
			}
			switch msgType {
			case websocket.TextMessage:
				// JSON resize 이벤트 vs 일반 키 입력 구분: 단순 prefix 검사 (성능 무관, 메시지 크기 작음)
				trimmed := bytes_trim(data)
				if len(trimmed) > 0 && trimmed[0] == '{' {
					var rs shellResizeMessage
					if jerr := json.Unmarshal(trimmed, &rs); jerr == nil && rs.Type == "resize" {
						if rerr := pty.Resize(rs.Cols, rs.Rows); rerr != nil {
							slog.Debug("pty resize error", "error", rerr)
						}
						continue
					}
				}
				if _, werr := pty.Write(data); werr != nil {
					return
				}
			case websocket.BinaryMessage:
				// xterm 측 raw bytes (utf-8) — 그대로 pty 에 전달
				if _, werr := pty.Write(data); werr != nil {
					return
				}
			}
		}
	}()

	// 어느 한쪽이 끝나면 pty 닫아 상대 깨움 + WaitGroup 대용으로 두 채널 모두 wait.
	select {
	case <-sendDone:
		pty.Close()
		_ = ws.Close() // recv 의 ReadMessage 깨우기
	case <-recvDone:
		pty.Close()
	case <-r.Context().Done():
		pty.Close()
		_ = ws.Close()
	}
	<-sendDone
	<-recvDone

	// pty.Wait — exit code 받아 close frame 으로 통지.
	exitCode, _ := pty.Wait()
	wsWriteClose(websocket.CloseNormalClosure, "exit "+strconv.Itoa(exitCode))
	slog.Info("shell session ended", "device", deviceID, "exit", exitCode)
}

func parseUintQuery(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// bytes_trim — leading whitespace 만 빠르게 trim (json 시작 '{' 감지용).
func bytes_trim(b []byte) []byte {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	return b[i:]
}
