package screen

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	scrcpyServerPath  = "/data/local/tmp/scrcpy-server.jar"
	scrcpyServerClass = "com.genymobile.scrcpy.Server"
	scrcpyVersion     = "2.4"
)

// Session represents an active scrcpy session for a device.
type Session struct {
	DeviceID    string
	Serial      string
	DeviceName  string
	Width       uint16
	Height      uint16
	VideoConn   net.Conn // H.264 video stream (after header consumed)
	ControlConn net.Conn // control messages (touch, key)
	cmd         *exec.Cmd
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
}

// Close terminates the scrcpy session.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.VideoConn != nil {
		s.VideoConn.Close()
	}
	if s.ControlConn != nil {
		s.ControlConn.Close()
	}
	// Kill scrcpy on device
	exec.Command("adb", "-s", s.Serial, "shell", "pkill -f scrcpy").Run()
	slog.Info("scrcpy session closed", "device", s.DeviceID)
}

// Manager manages scrcpy sessions.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	toolsDir string
}

func NewManager(toolsDir string) *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		toolsDir: toolsDir,
	}
}

// StartSession starts a scrcpy session for a device.
func (m *Manager) StartSession(ctx context.Context, deviceID, serial string, maxSize, bitrate int) (*Session, error) {
	// Clean up existing session
	m.StopSession(deviceID)
	time.Sleep(500 * time.Millisecond)

	if maxSize <= 0 {
		maxSize = 1080
	}
	if bitrate <= 0 {
		bitrate = 4000000 // 4Mbps
	}

	// Kill any leftover scrcpy on device + remove host-side forwards 「only for this serial」.
	// 과거엔 `forward --remove-all` 을 호출했는데 그건 호스트의 모든 디바이스 forward
	// 를 제거해 동시 운영 중인 다른 scrcpy/trace gRPC 까지 끊었다. serial 매칭으로 좁힘.
	exec.Command("adb", "-s", serial, "shell", "pkill -f scrcpy").Run()
	removeScrcpyForwards(serial)
	time.Sleep(500 * time.Millisecond)

	// Push scrcpy-server
	serverJar := filepath.Join(m.toolsDir, "scrcpy-server")
	pushCmd := exec.CommandContext(ctx, "adb", "-s", serial, "push", serverJar, scrcpyServerPath)
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("push scrcpy-server: %w: %s", err, string(out))
	}

	// Find free local port
	localPort, err := getFreePort()
	if err != nil {
		return nil, fmt.Errorf("get free port: %w", err)
	}

	// ADB forward
	fwdSpec := fmt.Sprintf("tcp:%d", localPort)
	fwdCmd := exec.CommandContext(ctx, "adb", "-s", serial, "forward", fwdSpec, "localabstract:scrcpy")
	if out, err := fwdCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("adb forward: %w: %s", err, string(out))
	}

	// Start scrcpy-server on device
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	shellCmd := exec.CommandContext(sessionCtx, "adb", "-s", serial, "shell",
		fmt.Sprintf("CLASSPATH=%s app_process / %s %s tunnel_forward=true video=true audio=false control=true max_size=%d video_bit_rate=%d max_fps=30 video_codec_options=profile=1,level=4096,repeat-previous-frame-after=100000,max-bframes=0",
			scrcpyServerPath, scrcpyServerClass, scrcpyVersion, maxSize, bitrate))
	shellCmd.Stdout = nil
	shellCmd.Stderr = nil

	if err := shellCmd.Start(); err != nil {
		sessionCancel()
		return nil, fmt.Errorf("start scrcpy-server: %w", err)
	}

	// Wait for server to initialize
	time.Sleep(2 * time.Second)

	// Connect video socket (first connection)
	addr := fmt.Sprintf("localhost:%d", localPort)
	videoConn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		sessionCancel()
		shellCmd.Process.Kill()
		return nil, fmt.Errorf("connect video: %w", err)
	}

	// Connect control socket (second connection on same port)
	controlConn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		videoConn.Close()
		sessionCancel()
		shellCmd.Process.Kill()
		return nil, fmt.Errorf("connect control: %w", err)
	}

	// Read video header:
	// 1. dummy byte (1 byte)
	dummy := make([]byte, 1)
	if _, err := videoConn.Read(dummy); err != nil {
		videoConn.Close()
		controlConn.Close()
		sessionCancel()
		shellCmd.Process.Kill()
		return nil, fmt.Errorf("read dummy: %w", err)
	}

	// 2. device name (64 bytes) + codec_id (4 bytes) + width (2 bytes) + height (2 bytes)
	header := make([]byte, 72)
	n := 0
	for n < 72 {
		nn, err := videoConn.Read(header[n:])
		if err != nil {
			videoConn.Close()
			controlConn.Close()
			sessionCancel()
			shellCmd.Process.Kill()
			return nil, fmt.Errorf("read header: %w", err)
		}
		n += nn
	}

	deviceName := string(header[:64])
	// trim null bytes
	for i, b := range header[:64] {
		if b == 0 {
			deviceName = string(header[:i])
			break
		}
	}

	// After device name (64), there's codec_id (4 bytes), then initial video size
	// But the next 4 bytes might be packed differently. Let's read what we got.
	// From test: Resolution bytes are at offset 68-72, but it was width=0x150(336), height...
	// Actually scrcpy v2.4 sends: name(64) + "h264"(4) + width(2) + height(2)
	// But from our test: 0x00000150 at offset 68 = width, 0x000002d0 at offset 70 is cut
	// Actually frame 0 was "000002d0" which is height=720
	// So: header[64:68] = codec "h264", header[68:70] = width as uint16, but we only got 72 bytes
	// header[68:72] = 0x00000150 → this is 4 bytes, maybe width=uint32?

	// scrcpy protocol: after device name, sends codec_id(4 bytes ascii), then width(4 bytes BE), height(4 bytes BE)
	// But we read only 72 bytes. Let's read 4 more for height.
	heightBuf := make([]byte, 4)
	n = 0
	for n < 4 {
		nn, err := videoConn.Read(heightBuf[n:])
		if err != nil {
			break
		}
		n += nn
	}

	width := binary.BigEndian.Uint32(header[68:72])
	height := binary.BigEndian.Uint32(heightBuf)

	session := &Session{
		DeviceID:    deviceID,
		Serial:      serial,
		DeviceName:  deviceName,
		Width:       uint16(width),
		Height:      uint16(height),
		VideoConn:   videoConn,
		ControlConn: controlConn,
		cmd:         shellCmd,
		cancel:      sessionCancel,
	}

	m.mu.Lock()
	m.sessions[deviceID] = session
	m.mu.Unlock()

	// Cleanup on process exit
	go func() {
		shellCmd.Wait()
		session.Close()
		m.mu.Lock()
		delete(m.sessions, deviceID)
		m.mu.Unlock()
	}()

	// Remove forward on cleanup
	go func() {
		<-sessionCtx.Done()
		exec.Command("adb", "-s", serial, "forward", "--remove", fwdSpec).Run()
	}()

	slog.Info("scrcpy session started", "device", deviceID, "name", deviceName,
		"resolution", fmt.Sprintf("%dx%d", width, height), "port", localPort)
	return session, nil
}

// removeScrcpyForwards — adb forward --list 를 읽어 serial 의 scrcpy 관련 forward 만 제거한다.
// `--remove-all` 처럼 다른 디바이스/용도 forward 를 건드리지 않는다.
//
// `adb forward --list` 출력 라인 예: "<serial> tcp:12345 localabstract:scrcpy"
func removeScrcpyForwards(serial string) {
	out, err := exec.Command("adb", "forward", "--list").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		if fields[0] != serial {
			continue
		}
		// scrcpy 가 쓰는 localabstract 소켓만 제거. trace gRPC 등 다른 forward 는 그대로.
		if !strings.Contains(fields[2], "scrcpy") {
			continue
		}
		exec.Command("adb", "-s", serial, "forward", "--remove", fields[1]).Run()
	}
}

// StopSession stops a scrcpy session.
func (m *Manager) StopSession(deviceID string) {
	m.mu.Lock()
	session, ok := m.sessions[deviceID]
	if ok {
		delete(m.sessions, deviceID)
	}
	m.mu.Unlock()
	if ok {
		session.Close()
	}
}

// ListSessions returns all active session device IDs.
func (m *Manager) ListSessions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var ids []string
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

func getFreePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}
