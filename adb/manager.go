package adb

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "agent/pb"
)

// ManagedDevice holds the ADB device handle and its current state.
type ManagedDevice struct {
	Device         *Device
	DeviceID       string
	Serial         string
	UsbID          string
	Product        string
	Model          string
	AndroidVersion string
	Board          string
	Platform       string
	Hardware       string
	CpuAbi         string
	BuildID        string
	Manufacturer   string
	SdkVersion     int32
	TracingDir     string
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

// Count returns the number of currently tracked devices.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.devices)
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
//
// 락 보유 시간 최소화 정책:
//  1. 락 잡고 → state 갱신(disconnect/reconnect) + 신규 디바이스 후보 추출 후 즉시 unlock
//  2. 락 밖에서 → 신규 디바이스마다 getprop/initTracing 병렬 수행 (디바이스당 16+ adb shell 호출)
//  3. 락 다시 잡고 → 결과를 map 에 반영
//
// 이렇게 하지 않으면 디바이스 5대 신규일 때 80+ adb shell 호출이 락 안에서 직렬로 돌아
// ListDevices/GetDevice 등 다른 RPC 가 수 초간 블로킹된다.
func (m *Manager) Refresh(ctx context.Context) {
	entries, err := listAdbDevices(ctx)
	if err != nil {
		slog.Warn("failed to list adb devices", "error", err)
		return
	}

	// === Phase 1: 락 안에서 빠른 작업만 ===
	currentIDs := make(map[string]bool, len(entries))
	for _, e := range entries {
		currentIDs[e.deviceID()] = true
	}

	var newEntries []adbDeviceEntry
	m.mu.Lock()
	// Mark missing devices as offline
	for id, md := range m.devices {
		if !currentIDs[id] && md.State != pb.DeviceState_DEVICE_STATE_OFFLINE {
			slog.Info("device disconnected", "device_id", id, "serial", md.Serial)
			md.State = pb.DeviceState_DEVICE_STATE_OFFLINE
		}
	}
	// 기존 디바이스는 reconnect 처리, 신규는 phase 2 후보로 모음
	for _, entry := range entries {
		id := entry.deviceID()
		if md, ok := m.devices[id]; ok {
			md.Serial = entry.serial
			if md.State == pb.DeviceState_DEVICE_STATE_OFFLINE {
				slog.Info("device reconnected", "device_id", id, "serial", entry.serial)
				md.State = pb.DeviceState_DEVICE_STATE_ONLINE
			}
			continue
		}
		newEntries = append(newEntries, entry)
	}
	m.mu.Unlock()

	if len(newEntries) == 0 {
		return
	}

	// === Phase 2: 락 밖에서 신규 디바이스 메타 수집 (디바이스 간 병렬) ===
	type result struct {
		id string
		md *ManagedDevice
	}
	results := make([]result, len(newEntries))
	var wg sync.WaitGroup
	for i, entry := range newEntries {
		wg.Add(1)
		go func(i int, entry adbDeviceEntry) {
			defer wg.Done()
			results[i] = result{id: entry.deviceID(), md: probeDevice(ctx, entry)}
		}(i, entry)
	}
	wg.Wait()

	// === Phase 3: 락 잡고 map 갱신 ===
	m.mu.Lock()
	for _, r := range results {
		if r.md == nil {
			continue
		}
		// 락을 푼 사이 중복 등록되었거나 사라졌을 수 있음 — 재확인
		if _, ok := m.devices[r.id]; ok {
			continue
		}
		m.devices[r.id] = r.md
	}
	m.mu.Unlock()

	for _, r := range results {
		if r.md == nil {
			continue
		}
		slog.Info("device discovered", "device_id", r.id, "serial", r.md.Serial, "usb", r.md.UsbID,
			"model", r.md.Model, "board", r.md.Board, "platform", r.md.Platform, "android", r.md.AndroidVersion)
	}
}

// probeDevice — 락 밖에서 신규 디바이스 메타정보를 수집한다.
// 디바이스당 약 16번의 adb shell 호출이 직렬로 일어나므로 호출자는 디바이스 간 병렬화 권장.
func probeDevice(ctx context.Context, entry adbDeviceEntry) *ManagedDevice {
	dev := NewDevice(entry.serial)
	md := &ManagedDevice{
		Device:   dev,
		DeviceID: entry.deviceID(),
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
	if v, err := dev.GetProp(ctx, "ro.product.board"); err == nil {
		md.Board = v
	}
	if v, err := dev.GetProp(ctx, "ro.board.platform"); err == nil {
		md.Platform = v
	}
	if v, err := dev.GetProp(ctx, "ro.hardware"); err == nil {
		md.Hardware = v
	}
	if v, err := dev.GetProp(ctx, "ro.product.cpu.abi"); err == nil {
		md.CpuAbi = v
	}
	if v, err := dev.GetProp(ctx, "ro.build.display.id"); err == nil {
		md.BuildID = v
	}
	if v, err := dev.GetProp(ctx, "ro.product.manufacturer"); err == nil {
		md.Manufacturer = v
	}
	if v, err := dev.GetProp(ctx, "ro.build.version.sdk"); err == nil {
		if sdk, err := strconv.Atoi(v); err == nil {
			md.SdkVersion = int32(sdk)
		}
	}

	md.TracingDir = findTracingDir(ctx, dev)
	if md.TracingDir != "" {
		initTracing(ctx, dev, md.TracingDir)
	}
	return md
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
			Board:          md.Board,
			Platform:       md.Platform,
			Hardware:       md.Hardware,
			CpuAbi:         md.CpuAbi,
			BuildId:        md.BuildID,
			Manufacturer:   md.Manufacturer,
			SdkVersion:     md.SdkVersion,
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

// GetDeviceSerial returns the serial for a device ID (implements screen.DeviceResolver).
func (m *Manager) GetDeviceSerial(deviceID string) (string, error) {
	md, err := m.GetDevice(deviceID)
	if err != nil {
		return "", err
	}
	return md.Serial, nil
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
