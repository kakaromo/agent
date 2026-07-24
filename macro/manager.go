package macro

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"agent/adb"
	pb "agent/pb"
	"agent/screen"
)

// Manager handles app macro recording, replay, and OCR.
type Manager struct {
	adbMgr    *adb.Manager
	scrcpyMgr *screen.Manager
	mu        sync.Mutex
	sessions  map[string]*RecordSession // deviceID → active recording session
}

func NewManager(adbMgr *adb.Manager, scrcpyMgr *screen.Manager) *Manager {
	return &Manager{
		adbMgr:    adbMgr,
		scrcpyMgr: scrcpyMgr,
		sessions:  make(map[string]*RecordSession),
	}
}

// StartRecording begins recording input events on the device.
func (m *Manager) StartRecording(ctx context.Context, req *pb.StartRecordingRequest) (*pb.StartRecordingResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[req.DeviceId]; ok {
		return &pb.StartRecordingResponse{Success: false}, fmt.Errorf("already recording on device %s", req.DeviceId)
	}

	session := NewRecordSession(req.DeviceId)
	m.sessions[req.DeviceId] = session

	slog.Info("macro recording started", "device", req.DeviceId, "session", session.ID)
	return &pb.StartRecordingResponse{
		Success:   true,
		SessionId: session.ID,
	}, nil
}

// StopRecording stops recording and returns the captured events.
func (m *Manager) StopRecording(ctx context.Context, req *pb.StopRecordingRequest) (*pb.StopRecordingResponse, error) {
	m.mu.Lock()
	session, ok := m.sessions[req.DeviceId]
	if ok {
		delete(m.sessions, req.DeviceId)
	}
	m.mu.Unlock()

	if !ok {
		return &pb.StopRecordingResponse{Success: false}, fmt.Errorf("no active recording on device %s", req.DeviceId)
	}

	// Get device resolution
	serial, err := m.adbMgr.GetDeviceSerial(req.DeviceId)
	if err != nil {
		return &pb.StopRecordingResponse{Success: false}, err
	}
	width, height := getDeviceResolution(ctx, serial)

	events := session.GetEvents()
	slog.Info("macro recording stopped", "device", req.DeviceId, "events", len(events))

	return &pb.StopRecordingResponse{
		Success:      true,
		Events:       events,
		DeviceWidth:  int32(width),
		DeviceHeight: int32(height),
	}, nil
}

// RecordEvent records an input event during an active recording session.
// Called from the scrcpy WebSocket handler when recording is active.
func (m *Manager) RecordEvent(deviceID string, event *pb.MacroEvent) {
	m.mu.Lock()
	session, ok := m.sessions[deviceID]
	m.mu.Unlock()
	if ok {
		session.AddEvent(event)
	}
}

// IsRecording returns true if the device is currently recording.
func (m *Manager) IsRecording(deviceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[deviceID]
	return ok
}

// RecordTouchEvent records a touch event for macro recording (called from screen handler).
func (m *Manager) RecordTouchEvent(deviceID string, action int, x, y float64, width, height int) {
	evType := "tap"
	// Only record down events as taps (swipe detection needs up event tracking)
	if action == 0 { // ActionDown
		m.RecordEvent(deviceID, &pb.MacroEvent{
			Type: evType,
			X:    int32(x * float64(width)),
			Y:    int32(y * float64(height)),
		})
	}
}

// RecordKeyEvent records a key event for macro recording.
func (m *Manager) RecordKeyEvent(deviceID string, keycode int) {
	m.RecordEvent(deviceID, &pb.MacroEvent{
		Type:    "key",
		Keycode: int32(keycode),
	})
}

// RecordScrollEvent records a scroll event for macro recording.
func (m *Manager) RecordScrollEvent(deviceID string, x, y float64, hScroll, vScroll int) {
	// Convert scroll to swipe gesture
	// Not directly recorded as scroll — user can add wait/screenshot events manually
}

// ListInstalledApps returns launchable apps on the device (런처에서 실행 가능한 앱).
//
// third-party(-3) 만 가져오면 기본 유튜브·크롬 같은 시스템앱이 빠진다. 대신 LAUNCHER
// 인텐트를 가진 액티비티를 조회해 "런처에 아이콘으로 뜨는 앱"을 모두 포함한다. 배경
// 서비스/프로바이더는 제외되고, 사용자가 실제로 열 수 있는 앱만 남는다.
func (m *Manager) ListInstalledApps(ctx context.Context, req *pb.ListInstalledAppsRequest) (*pb.ListInstalledAppsResponse, error) {
	serial, err := m.adbMgr.GetDeviceSerial(req.DeviceId)
	if err != nil {
		return nil, fmt.Errorf("device not found: %w", err)
	}

	dev := adb.NewDevice(serial)

	// LAUNCHER 인텐트를 가진 액티비티 조회 → "package/activity" 라인들.
	out, err := dev.Shell(ctx,
		"cmd package query-activities --brief -a android.intent.action.MAIN -c android.intent.category.LAUNCHER")
	pkgs := parseLaunchablePackages(out)

	// query-activities 미지원/실패 시 third-party 목록으로 폴백.
	if err != nil || len(pkgs) == 0 {
		fallback, ferr := dev.Shell(ctx, "pm list packages -3")
		if ferr != nil {
			return nil, fmt.Errorf("list apps: %w", ferr)
		}
		pkgs = nil
		seen := make(map[string]bool)
		for _, line := range strings.Split(fallback, "\n") {
			pkg := strings.TrimPrefix(strings.TrimSpace(line), "package:")
			if pkg != "" && !seen[pkg] {
				seen[pkg] = true
				pkgs = append(pkgs, pkg)
			}
		}
	}

	apps := make([]*pb.InstalledApp, 0, len(pkgs))
	for _, pkg := range pkgs {
		apps = append(apps, &pb.InstalledApp{
			PackageName: pkg,
			AppName:     getAppLabel(ctx, dev, pkg),
		})
	}
	return &pb.ListInstalledAppsResponse{Apps: apps}, nil
}

// parseLaunchablePackages 는 `cmd package query-activities --brief` 출력에서
// 고유 패키지명을 추출한다. 라인 형식: "  com.example/com.example.MainActivity".
func parseLaunchablePackages(out string) []string {
	var pkgs []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// "package/activity" 형태만 관심. component name 은 '/' 를 포함한다.
		slash := strings.Index(line, "/")
		if slash <= 0 {
			continue
		}
		pkg := line[:slash]
		// 패키지명처럼 보이는지(점 포함, 공백 없음) 최소 검증.
		if strings.Contains(pkg, " ") || !strings.Contains(pkg, ".") {
			continue
		}
		if !seen[pkg] {
			seen[pkg] = true
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs
}

// ReplayMacro replays recorded events on the device using ADB input commands.
func (m *Manager) ReplayMacro(ctx context.Context, req *pb.ReplayMacroRequest) (*pb.ReplayMacroResponse, error) {
	serial, err := m.adbMgr.GetDeviceSerial(req.DeviceId)
	if err != nil {
		return nil, fmt.Errorf("device not found: %w", err)
	}

	dev := adb.NewDevice(serial)
	replayer := NewReplayer(dev, int(req.SourceWidth), int(req.SourceHeight))

	return replayer.Replay(ctx, req.Events)
}

// TakeScreenshot captures a screenshot from the device.
func (m *Manager) TakeScreenshot(ctx context.Context, req *pb.TakeScreenshotRequest) (*pb.TakeScreenshotResponse, error) {
	serial, err := m.adbMgr.GetDeviceSerial(req.DeviceId)
	if err != nil {
		return nil, fmt.Errorf("device not found: %w", err)
	}

	dev := adb.NewDevice(serial)
	return CaptureScreenshot(ctx, dev)
}

// ScreenshotOcr captures a screenshot and performs OCR.
func (m *Manager) ScreenshotOcr(ctx context.Context, req *pb.ScreenshotOcrRequest) (*pb.ScreenshotOcrResponse, error) {
	serial, err := m.adbMgr.GetDeviceSerial(req.DeviceId)
	if err != nil {
		return nil, fmt.Errorf("device not found: %w", err)
	}

	dev := adb.NewDevice(serial)
	return RunScreenshotOcr(ctx, dev, req.Region, req.ExtractPattern)
}

// ListUiElements 는 현재 화면의 uiautomator 요소 목록을 반환한다 (요소 기반 시나리오 빌더용).
func (m *Manager) ListUiElements(ctx context.Context, req *pb.ListUiElementsRequest) (*pb.ListUiElementsResponse, error) {
	serial, err := m.adbMgr.GetDeviceSerial(req.DeviceId)
	if err != nil {
		return nil, fmt.Errorf("device not found: %w", err)
	}

	dev := adb.NewDevice(serial)
	els, err := DumpUIElements(ctx, dev, req.ClickableOnly)
	if err != nil {
		return &pb.ListUiElementsResponse{Success: false}, fmt.Errorf("dump ui elements: %w", err)
	}

	width, height := getDeviceResolution(ctx, serial)
	out := make([]*pb.UiElement, 0, len(els))
	for _, e := range els {
		out = append(out, &pb.UiElement{
			ResourceId:  e.ResourceID,
			Text:        e.Text,
			ContentDesc: e.ContentDesc,
			Class:       e.Class,
			Clickable:   e.Clickable,
			CenterX:     int32(e.CenterX),
			CenterY:     int32(e.CenterY),
			BoundLeft:   int32(e.Bounds[0]),
			BoundTop:    int32(e.Bounds[1]),
			BoundRight:  int32(e.Bounds[2]),
			BoundBottom: int32(e.Bounds[3]),
			ContainerId: e.ContainerID,
		})
	}
	return &pb.ListUiElementsResponse{
		Success:      true,
		Elements:     out,
		DeviceWidth:  int32(width),
		DeviceHeight: int32(height),
	}, nil
}
