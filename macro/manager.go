package macro

import (
	"context"
	"fmt"
	"log/slog"
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
