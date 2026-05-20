// Package apkmgr 는 번들된 APK 파일(`<toolsDir>/apks/*.apk`) 을 디바이스에 설치/제거한다.
//
// 정책:
//   - APK 는 호스트의 `<toolsDir>/apks/` 폴더에 둔다. fio/iotest 와 동일한 위치 정책.
//   - 파일명만 받는다 (경로 traversal 금지) — 호스트 상 절대경로는 매니저가 합성한다.
//   - install 은 `adb install -r` 로 push + pm install 을 한번에 수행.
package apkmgr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agent/adb"
	pb "agent/pb"
)

const apkSubdir = "apks"

// Manager bundles list/install/uninstall of APK files.
type Manager struct {
	adbMgr   *adb.Manager
	toolsDir string
}

func NewManager(adbMgr *adb.Manager, toolsDir string) *Manager {
	return &Manager{adbMgr: adbMgr, toolsDir: toolsDir}
}

// ApksDir returns the absolute directory where bundled APKs live.
func (m *Manager) ApksDir() string {
	return filepath.Join(m.toolsDir, apkSubdir)
}

// List enumerates .apk files in <toolsDir>/apks (non-recursive).
func (m *Manager) List(_ context.Context, _ *pb.ListBundledApksRequest) (*pb.ListBundledApksResponse, error) {
	dir := m.ApksDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("ensure apks dir: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read apks dir: %w", err)
	}
	var apks []*pb.BundledApk
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.EqualFold(filepath.Ext(name), ".apk") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		apks = append(apks, &pb.BundledApk{
			Filename:   name,
			SizeBytes:  fi.Size(),
			ModifiedAt: fi.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(apks, func(i, j int) bool { return apks[i].Filename < apks[j].Filename })
	return &pb.ListBundledApksResponse{Apks: apks}, nil
}

// Install pushes <toolsDir>/apks/<filename> to the device and runs `pm install`.
func (m *Manager) Install(ctx context.Context, req *pb.InstallApkRequest) (*pb.InstallApkResponse, error) {
	localPath, err := m.resolveApk(req.ApkFilename)
	if err != nil {
		return nil, err
	}
	serial, err := m.adbMgr.GetDeviceSerial(req.DeviceId)
	if err != nil {
		return nil, fmt.Errorf("device not found: %w", err)
	}
	dev := adb.NewDevice(serial)

	// proto bool 기본값이 false 라 호출자가 "기본"으로 두면 reinstall 안 됨.
	// 운영상 -r 이 안전한 기본이라 항상 켠다 (벤치마크 반복 설치 시 데이터 보존 + 충돌 회피).
	out, installErr := dev.InstallApk(ctx, localPath, true, req.GrantRuntimePermissions)
	resp := &pb.InstallApkResponse{
		Success:     installErr == nil,
		Message:     strings.TrimSpace(out),
		PackageName: extractPackageFromFilename(req.ApkFilename),
	}
	if installErr != nil {
		return resp, installErr
	}
	return resp, nil
}

// Uninstall calls `adb uninstall` for the given package.
func (m *Manager) Uninstall(ctx context.Context, req *pb.UninstallApkRequest) (*pb.UninstallApkResponse, error) {
	if strings.TrimSpace(req.PackageName) == "" {
		return nil, fmt.Errorf("package_name is required")
	}
	serial, err := m.adbMgr.GetDeviceSerial(req.DeviceId)
	if err != nil {
		return nil, fmt.Errorf("device not found: %w", err)
	}
	dev := adb.NewDevice(serial)
	out, uninstallErr := dev.UninstallApk(ctx, req.PackageName, req.KeepData)
	resp := &pb.UninstallApkResponse{
		Success: uninstallErr == nil,
		Message: strings.TrimSpace(out),
	}
	if uninstallErr != nil {
		return resp, uninstallErr
	}
	return resp, nil
}

// resolveApk validates filename (no traversal) and returns an absolute path that exists.
func (m *Manager) resolveApk(filename string) (string, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "", fmt.Errorf("apk_filename is required")
	}
	if strings.ContainsAny(filename, `/\`) || filename == "." || filename == ".." {
		return "", fmt.Errorf("apk_filename must be a bare filename, got %q", filename)
	}
	if !strings.EqualFold(filepath.Ext(filename), ".apk") {
		return "", fmt.Errorf("apk_filename must end with .apk")
	}
	abs := filepath.Join(m.ApksDir(), filename)
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("apk file not found: %s", filename)
	}
	return abs, nil
}

// extractPackageFromFilename is best-effort — many APKs are named after their package,
// e.g. "com.antutu.ABenchMark.apk". 매칭 안 되면 빈 문자열을 돌려준다.
func extractPackageFromFilename(filename string) string {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if strings.Count(base, ".") >= 2 && !strings.ContainsAny(base, " ") {
		return base
	}
	return ""
}
