package screen

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
}

// ControlMessage is the JSON message from browser for input events.
type ControlMessage struct {
	Type   string          `json:"type"`   // "touch", "key", "scroll", "back"
	Touch  *TouchEvent     `json:"touch,omitempty"`
	Key    *KeyEvent       `json:"key,omitempty"`
	Scroll *ScrollEvent    `json:"scroll,omitempty"`
}

// EventRecorder records input events during macro recording.
type EventRecorder interface {
	IsRecording(deviceID string) bool
	RecordTouchEvent(deviceID string, action int, x, y float64, width, height int)
	RecordKeyEvent(deviceID string, keycode int)
	RecordScrollEvent(deviceID string, x, y float64, hScroll, vScroll int)
}

// Handler handles WebSocket connections for screen streaming.
type Handler struct {
	scrcpyMgr  *Manager
	adbManager DeviceResolver
	recorder   EventRecorder
}

// DeviceResolver resolves device_id to serial.
type DeviceResolver interface {
	GetDeviceSerial(deviceID string) (string, error)
}

// isIDR checks if H.264 Annex B data starts with an IDR NAL unit (type 5).
func isIDR(data []byte) bool {
	// Check for 4-byte start code
	if len(data) > 4 && data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1 {
		return (data[4] & 0x1f) == 5
	}
	// Check for 3-byte start code
	if len(data) > 3 && data[0] == 0 && data[1] == 0 && data[2] == 1 {
		return (data[3] & 0x1f) == 5
	}
	return false
}

func NewHandler(scrcpyMgr *Manager, resolver DeviceResolver) *Handler {
	return &Handler{
		scrcpyMgr:  scrcpyMgr,
		adbManager: resolver,
	}
}

// SetRecorder sets the event recorder for macro recording.
func (h *Handler) SetRecorder(r EventRecorder) {
	h.recorder = r
}

// ServeHTTP handles WebSocket upgrade for /ws/screen/{device_id}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract device_id from path: /ws/screen/{device_id}
	path := strings.TrimPrefix(r.URL.Path, "/ws/screen/")
	deviceID := strings.TrimSuffix(path, "/")
	if deviceID == "" {
		http.Error(w, "device_id required", http.StatusBadRequest)
		return
	}

	// Resolve serial
	serial, err := h.adbManager.GetDeviceSerial(deviceID)
	if err != nil {
		http.Error(w, "device not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Upgrade to WebSocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}
	defer ws.Close()
	ws.EnableWriteCompression(false) // compression adds latency

	// gorilla/websocket 은 동시 Write 비허용 — video frame, requestSync 응답, 초기 info JSON
	// 이 모두 같은 conn 에 쓰므로 mutex 로 직렬화한다.
	var wsWriteMu sync.Mutex
	wsWriteBinary := func(data []byte) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		return ws.WriteMessage(websocket.BinaryMessage, data)
	}
	wsWriteJSON := func(v interface{}) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		return ws.WriteJSON(v)
	}
	wsWriteClose := func(closeCode int, msg string) {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, msg))
	}

	slog.Info("screen session requested", "device", deviceID, "serial", serial)

	// Start scrcpy session
	session, err := h.scrcpyMgr.StartSession(r.Context(), deviceID, serial, 1080, 4000000)
	if err != nil {
		slog.Error("start scrcpy session failed", "device", deviceID, "error", err)
		wsWriteClose(websocket.CloseInternalServerErr, err.Error())
		return
	}
	defer h.scrcpyMgr.StopSession(deviceID)

	// Send initial info message with resolution
	wsWriteJSON(map[string]interface{}{
		"type":    "info",
		"device":  deviceID,
		"serial":  serial,
		"width":   session.Width,
		"height":  session.Height,
		"name":    session.DeviceName,
		"message": "scrcpy session started",
	})

	// Cache last config (SPS/PPS) and keyframe (IDR) for decoder reinit
	var cacheMu sync.Mutex
	var lastConfig []byte  // SPS/PPS packet
	var lastKeyframe []byte // IDR frame
	syncRequested := make(chan struct{}, 1)

	// Read scrcpy video packets and forward H.264 data to WebSocket.
	// scrcpy v2.x packet: [PTS:8bytes][size:4bytes][H.264 data:size bytes]
	//
	// 두 goroutine (video read, control read) 모두 추적해야 누수가 없다:
	//   - video 가 먼저 끝나면 ws.Close() 로 control 의 ws.ReadMessage 를 깨운다
	//   - control 이 먼저 끝나면 session.Close() 로 video 의 VideoConn.Read 를 깨운다
	//   - 마지막에 두 goroutine 모두 종료 대기
	videoDone := make(chan struct{})
	controlDone := make(chan struct{})
	go func() {
		defer close(videoDone)
		hdr := make([]byte, 12)
		for {
			if _, err := io.ReadFull(session.VideoConn, hdr); err != nil {
				if err != io.EOF {
					slog.Debug("video header read error", "error", err)
				}
				return
			}

			pts := binary.BigEndian.Uint64(hdr[0:8])
			size := int(binary.BigEndian.Uint32(hdr[8:12]))
			if size <= 0 || size > 4*1024*1024 {
				slog.Warn("invalid scrcpy packet size", "size", size)
				return
			}

			frame := make([]byte, size)
			if _, err := io.ReadFull(session.VideoConn, frame); err != nil {
				if err != io.EOF {
					slog.Debug("video frame read error", "error", err)
				}
				return
			}

			// Cache config and keyframe packets
			cacheMu.Lock()
			if pts == 0x8000000000000000 {
				// Config packet (SPS/PPS): PTS with MSB set
				lastConfig = append([]byte(nil), frame...)
			} else if len(frame) > 4 && isIDR(frame) {
				lastKeyframe = append([]byte(nil), frame...)
			}
			cacheMu.Unlock()

			if err := wsWriteBinary(frame); err != nil {
				slog.Debug("ws write error", "error", err)
				return
			}
		}
	}()

	// Read control messages from WebSocket and forward to scrcpy
	go func() {
		defer close(controlDone)
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					slog.Debug("ws read error", "error", err)
				}
				session.Close()
				return
			}

			var ctrl ControlMessage
			if err := json.Unmarshal(msg, &ctrl); err != nil {
				continue
			}

			var data []byte
			switch ctrl.Type {
			case "touch":
				if ctrl.Touch != nil {
					data = EncodeInjectTouchEvent(*ctrl.Touch)
					// Record for macro
					if h.recorder != nil && h.recorder.IsRecording(deviceID) {
						h.recorder.RecordTouchEvent(deviceID, ctrl.Touch.Action,
							ctrl.Touch.X, ctrl.Touch.Y, ctrl.Touch.Width, ctrl.Touch.Height)
					}
				}
			case "key":
				if ctrl.Key != nil {
					data = EncodeInjectKeycode(*ctrl.Key)
					if h.recorder != nil && h.recorder.IsRecording(deviceID) && ctrl.Key.Action == ActionDown {
						h.recorder.RecordKeyEvent(deviceID, ctrl.Key.Keycode)
					}
				}
			case "scroll":
				if ctrl.Scroll != nil {
					data = EncodeInjectScrollEvent(*ctrl.Scroll)
					if h.recorder != nil && h.recorder.IsRecording(deviceID) {
						h.recorder.RecordScrollEvent(deviceID, ctrl.Scroll.X, ctrl.Scroll.Y,
							ctrl.Scroll.HScroll, ctrl.Scroll.VScroll)
					}
				}
			case "back":
				data = EncodeBackOrScreenOn(ActionDown)
				if h.recorder != nil && h.recorder.IsRecording(deviceID) {
					h.recorder.RecordKeyEvent(deviceID, 4) // KEYCODE_BACK
				}
			case "requestSync":
				// Send cached config + keyframe to reinit decoder
				select {
				case syncRequested <- struct{}{}:
				default:
				}
				cacheMu.Lock()
				cfg := lastConfig
				kf := lastKeyframe
				cacheMu.Unlock()
				if cfg != nil {
					wsWriteBinary(cfg)
				}
				if kf != nil {
					wsWriteBinary(kf)
				}
				continue
			}

			if data != nil && session.ControlConn != nil {
				session.ControlConn.Write(data)
			}
		}
	}()

	// video 가 끝나면 control 의 ReadMessage 를 ws.Close() 로 깨워 종료시킨다.
	// 그 다음 두 goroutine 모두 끝날 때까지 대기 — 누수 방지.
	<-videoDone
	ws.Close()
	<-controlDone
	slog.Info("screen session ended", "device", deviceID)
}
