package macro

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent/adb"
	pb "agent/pb"

	"github.com/google/uuid"
)

// RecordSession tracks events during a recording.
type RecordSession struct {
	ID        string
	DeviceID  string
	StartedAt time.Time
	mu        sync.Mutex
	events    []*pb.MacroEvent
}

func NewRecordSession(deviceID string) *RecordSession {
	return &RecordSession{
		ID:        uuid.New().String(),
		DeviceID:  deviceID,
		StartedAt: time.Now(),
	}
}

// AddEvent adds a timestamped event to the recording.
func (s *RecordSession) AddEvent(event *pb.MacroEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Set timestamp relative to recording start
	if event.T == 0 {
		event.T = time.Since(s.StartedAt).Milliseconds()
	}
	s.events = append(s.events, event)
}

// GetEvents returns a copy of all recorded events.
func (s *RecordSession) GetEvents() []*pb.MacroEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*pb.MacroEvent, len(s.events))
	copy(out, s.events)
	return out
}

// getDeviceResolution returns device screen width and height via ADB.
func getDeviceResolution(ctx context.Context, serial string) (width, height int) {
	dev := adb.NewDevice(serial)
	out, err := dev.Shell(ctx, "wm size")
	if err != nil {
		return 1080, 2400 // default
	}
	// Output: "Physical size: 1080x2400" or "Override size: 1080x2400\nPhysical size: 1440x3200"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Override size:") || strings.HasPrefix(line, "Physical size:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			dims := strings.TrimSpace(parts[1])
			wh := strings.Split(dims, "x")
			if len(wh) == 2 {
				w, _ := strconv.Atoi(strings.TrimSpace(wh[0]))
				h, _ := strconv.Atoi(strings.TrimSpace(wh[1]))
				if w > 0 && h > 0 {
					return w, h
				}
			}
		}
	}
	return 1080, 2400
}

// getDeviceActivityFocus returns the current focused activity string.
func getDeviceActivityFocus(ctx context.Context, dev *adb.Device) (string, error) {
	out, err := dev.Shell(ctx, "dumpsys window | grep mCurrentFocus")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// getDeviceUIText dumps UI hierarchy and returns matching text.
func getDeviceUIText(ctx context.Context, dev *adb.Device, pattern string) (bool, error) {
	_, err := dev.Shell(ctx, "uiautomator dump /sdcard/ui.xml")
	if err != nil {
		return false, fmt.Errorf("uiautomator dump: %w", err)
	}
	out, err := dev.Shell(ctx, "cat /sdcard/ui.xml")
	if err != nil {
		return false, fmt.Errorf("cat ui.xml: %w", err)
	}
	return strings.Contains(out, pattern), nil
}

// getAppLabel retrieves the human-readable app name from dumpsys.
func getAppLabel(ctx context.Context, dev *adb.Device, packageName string) string {
	// Try dumpsys package to get the app label
	out, err := dev.Shell(ctx, fmt.Sprintf("dumpsys package %s | grep 'label=' | head -1", packageName))
	if err == nil {
		out = strings.TrimSpace(out)
		// Format: "label=AnTuTu Benchmark" or similar
		if idx := strings.Index(out, "label="); idx >= 0 {
			label := strings.TrimSpace(out[idx+6:])
			if label != "" {
				return label
			}
		}
	}
	// Fallback: use last part of package name
	parts := strings.Split(packageName, ".")
	return parts[len(parts)-1]
}

// tesseractAvailable checks if tesseract is installed.
func tesseractAvailable() bool {
	_, err := exec.LookPath("tesseract")
	return err == nil
}
