package adb

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	pb "agent/pb"
)

// ManagedDevice holds the ADB device handle and its current state.
type ManagedDevice struct {
	Device         *Device
	DeviceID       string // usb id (e.g. "2-1.1.2") or serial as fallback
	Serial         string
	UsbID          string // usb port path from "adb devices -l"
	Product        string
	Model          string
	AndroidVersion string
	TracingDir     string // /sys/kernel/tracing or /sys/kernel/debug/tracing
	State          pb.DeviceState
}

// Manager manages ADB devices currently connected to this machine.
type Manager struct {
	mu      sync.RWMutex
	devices map[string]*ManagedDevice // keyed by DeviceID
}

func NewManager() *Manager {
	return &Manager{
		devices: make(map[string]*ManagedDevice),
	}
}

// adbDeviceEntry holds parsed info from "adb devices -l".
type adbDeviceEntry struct {
	serial  string
	usbID   string
	product string
	model   string
	device  string
}

// deviceID returns usb id if available, otherwise serial.
func (e *adbDeviceEntry) deviceID() string {
	if e.usbID != "" {
		return e.usbID
	}
	return e.serial
}

// Refresh discovers devices via "adb devices -l" and updates the internal state.
func (m *Manager) Refresh(ctx context.Context) {
	entries, err := listAdbDevices(ctx)
	if err != nil {
		slog.Warn("failed to list adb devices", "error", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Build set of current device IDs
	currentIDs := make(map[string]bool)
	for _, e := range entries {
		currentIDs[e.deviceID()] = true
	}

	// Mark missing devices as offline
	for id, md := range m.devices {
		if !currentIDs[id] && md.State != pb.DeviceState_DEVICE_STATE_OFFLINE {
			slog.Info("device disconnected", "device_id", id, "serial", md.Serial)
			md.State = pb.DeviceState_DEVICE_STATE_OFFLINE
		}
	}

	// Add new devices or bring back online
	for _, entry := range entries {
		id := entry.deviceID()
		if md, ok := m.devices[id]; ok {
			// Update serial in case it changed (reconnect)
			md.Serial = entry.serial
			if md.State == pb.DeviceState_DEVICE_STATE_OFFLINE {
				slog.Info("device reconnected", "device_id", id, "serial", entry.serial)
				md.State = pb.DeviceState_DEVICE_STATE_ONLINE
			}
			continue
		}

		dev := NewDevice(entry.serial)
		md := &ManagedDevice{
			Device:   dev,
			DeviceID: id,
			Serial:   entry.serial,
			UsbID:    entry.usbID,
			Product:  entry.product,
			Model:    entry.model,
			State:    pb.DeviceState_DEVICE_STATE_ONLINE,
		}

		if ver, err := dev.GetProp(ctx, "ro.build.version.release"); err == nil {
			md.AndroidVersion = ver
		}
		if md.Model == "" {
			if model, err := dev.GetProp(ctx, "ro.product.model"); err == nil {
				md.Model = model
			}
		}

		// Find tracing directory
		md.TracingDir = findTracingDir(ctx, dev)

		// Initialize tracing if found
		if md.TracingDir != "" {
			initTracing(ctx, dev, md.TracingDir)
		}

		slog.Info("device discovered", "device_id", id, "serial", entry.serial, "usb", entry.usbID, "model", md.Model, "android", md.AndroidVersion, "tracing_dir", md.TracingDir)
		m.devices[id] = md
	}
}

// StartRefreshLoop periodically refreshes the device list.
func (m *Manager) StartRefreshLoop(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Refresh(ctx)
			}
		}
	}()
}

// ListDevices returns info for all known devices.
func (m *Manager) ListDevices() []*pb.DeviceInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*pb.DeviceInfo, 0, len(m.devices))
	for _, md := range m.devices {
		result = append(result, &pb.DeviceInfo{
			DeviceId:       md.DeviceID,
			Serial:         md.Serial,
			State:          md.State,
			AndroidVersion: md.AndroidVersion,
			Model:          md.Model,
		})
	}
	return result
}

// GetDevice returns a managed device by DeviceID.
func (m *Manager) GetDevice(deviceID string) (*ManagedDevice, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	md, ok := m.devices[deviceID]
	if !ok {
		return nil, fmt.Errorf("device not found: %s", deviceID)
	}
	return md, nil
}

// GetOnlineDevices returns DeviceIDs of all online devices.
func (m *Manager) GetOnlineDevices() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var ids []string
	for _, md := range m.devices {
		if md.State == pb.DeviceState_DEVICE_STATE_ONLINE {
			ids = append(ids, md.DeviceID)
		}
	}
	return ids
}

// SetState updates a device's state.
func (m *Manager) SetState(deviceID string, state pb.DeviceState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if md, ok := m.devices[deviceID]; ok {
		md.State = state
	}
}

// ConnectDevice runs "adb connect" for a TCP device and refreshes.
func (m *Manager) ConnectDevice(ctx context.Context, serial string) error {
	dev := NewDevice(serial)
	if err := dev.Connect(ctx); err != nil {
		return err
	}
	m.Refresh(ctx)
	return nil
}

// DisconnectDevice runs "adb disconnect" for a device and refreshes.
func (m *Manager) DisconnectDevice(ctx context.Context, serial string) error {
	dev := NewDevice(serial)
	if err := dev.Disconnect(ctx); err != nil {
		return err
	}
	m.Refresh(ctx)
	return nil
}

// findTracingDir finds the tracing directory on a device.
func findTracingDir(ctx context.Context, dev *Device) string {
	out, err := dev.Shell(ctx, "[ -d /sys/kernel/tracing ] && echo exists")
	if err == nil && strings.Contains(out, "exists") {
		return "/sys/kernel/tracing"
	}
	out, err = dev.Shell(ctx, "[ -d /sys/kernel/debug/tracing ] && echo exists")
	if err == nil && strings.Contains(out, "exists") {
		return "/sys/kernel/debug/tracing"
	}
	return ""
}

// initTracing initializes ftrace on a device: disable all events, set 200MB buffer.
func initTracing(ctx context.Context, dev *Device, tracingDir string) {
	cmds := []string{
		fmt.Sprintf("echo 0 > %s/tracing_on", tracingDir),
		fmt.Sprintf("echo 0 > %s/trace", tracingDir),
		fmt.Sprintf("echo nop > %s/current_tracer", tracingDir),
		fmt.Sprintf("for f in $(find %s/events -name enable -type f); do echo 0 > $f; done", tracingDir),
		fmt.Sprintf("echo 0 > %s/options/markers", tracingDir),
		fmt.Sprintf("echo 0 > %s/options/trace_printk", tracingDir),
		fmt.Sprintf("echo 204800 > %s/buffer_size_kb", tracingDir),
	}
	for _, cmd := range cmds {
		if _, err := dev.Shell(ctx, cmd); err != nil {
			slog.Warn("init tracing cmd failed", "cmd", cmd, "error", err)
		}
	}
	if out, err := dev.Shell(ctx, fmt.Sprintf("cat %s/buffer_size_kb", tracingDir)); err == nil {
		slog.Info("trace buffer initialized", "size_kb", strings.TrimSpace(out))
	}
}

// listAdbDevices runs "adb devices -l" and returns parsed device entries.
func listAdbDevices(ctx context.Context) ([]adbDeviceEntry, error) {
	cmd := exec.CommandContext(ctx, "adb", "devices", "-l")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var entries []adbDeviceEntry
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "device" {
			continue
		}

		entry := adbDeviceEntry{serial: fields[0]}

		// Parse key:value pairs from remaining fields
		for _, f := range fields[2:] {
			k, v, ok := strings.Cut(f, ":")
			if !ok {
				continue
			}
			switch k {
			case "usb":
				entry.usbID = v
			case "product":
				entry.product = v
			case "model":
				entry.model = strings.ReplaceAll(v, "_", " ")
			case "device":
				entry.device = v
			}
		}

		entries = append(entries, entry)
	}
	return entries, nil
}
