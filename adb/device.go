package adb

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const defaultTimeout = 60 * time.Second

// Device represents a single Android device connected via ADB.
type Device struct {
	Serial string
}

func NewDevice(serial string) *Device {
	return &Device{Serial: serial}
}

// Connect runs "adb connect <serial>".
func (d *Device) Connect(ctx context.Context) error {
	out, err := d.run(ctx, "connect", d.Serial)
	if err != nil {
		return fmt.Errorf("adb connect %s: %w", d.Serial, err)
	}
	if !strings.Contains(out, "connected") && !strings.Contains(out, "already") {
		return fmt.Errorf("adb connect %s: unexpected output: %s", d.Serial, out)
	}
	return nil
}

// Disconnect runs "adb disconnect <serial>".
func (d *Device) Disconnect(ctx context.Context) error {
	_, err := d.run(ctx, "disconnect", d.Serial)
	return err
}

// Shell runs a shell command on the device and returns stdout.
func (d *Device) Shell(ctx context.Context, cmd string) (string, error) {
	return d.run(ctx, "-s", d.Serial, "shell", cmd)
}

// ShellStream runs a shell command and streams stdout/stderr line-by-line via callbacks.
// onStdout/onStderr 콜백은 각각 다른 goroutine 에서 호출되므로 콜백 측이 thread-safe 해야 한다.
// 콜백은 라인 한 줄씩(개행 제외) 받는다. 명령이 끝나면 모인 stdout 전체를 반환한다 (기존
// Shell 호환을 위해 — iotest 결과 JSON 은 stdout 마지막에 한 번에 출력됨).
//
// ctx deadline 이 없으면 defaultTimeout 적용. 즉시 종료가 필요하면 ctx 를 cancel 하면 됨.
func (d *Device) ShellStream(
	ctx context.Context,
	cmd string,
	onStdout func(line string),
	onStderr func(line string),
) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	c := exec.CommandContext(ctx, "adb", "-s", d.Serial, "shell", cmd)
	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := c.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	if err := c.Start(); err != nil {
		return "", fmt.Errorf("adb start: %w", err)
	}

	// stdout 은 콜백 + 전체 모음 (호출자가 결과 JSON 파싱). stderr 는 콜백만.
	var stdoutBuf bytes.Buffer
	var stdoutMu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		streamLines(stdoutPipe, func(line string) {
			stdoutMu.Lock()
			stdoutBuf.WriteString(line)
			stdoutBuf.WriteByte('\n')
			stdoutMu.Unlock()
			if onStdout != nil {
				onStdout(line)
			}
		})
	}()
	go func() {
		defer wg.Done()
		streamLines(stderrPipe, func(line string) {
			if onStderr != nil {
				onStderr(line)
			}
		})
	}()

	wg.Wait()
	if err := c.Wait(); err != nil {
		return stdoutBuf.String(), fmt.Errorf("adb wait: %w", err)
	}
	return stdoutBuf.String(), nil
}

// streamLines reads a Reader line by line (개행 제외) and invokes onLine for each.
// 64MB 까지 라인 길이 허용 — iotest 결과 JSON 한 줄이 클 수 있어 여유.
func streamLines(r io.Reader, onLine func(line string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		onLine(sc.Text())
	}
}

// Push pushes a local file to the device.
func (d *Device) Push(ctx context.Context, local, remote string) error {
	_, err := d.run(ctx, "-s", d.Serial, "push", local, remote)
	return err
}

// Pull pulls a remote file from the device.
func (d *Device) Pull(ctx context.Context, remote, local string) error {
	_, err := d.run(ctx, "-s", d.Serial, "pull", remote, local)
	return err
}

// IsAlive checks if the device is reachable.
func (d *Device) IsAlive(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := d.Shell(ctx, "echo ok")
	return err == nil && strings.TrimSpace(out) == "ok"
}

// GetProp reads an Android system property.
func (d *Device) GetProp(ctx context.Context, prop string) (string, error) {
	out, err := d.Shell(ctx, "getprop "+prop)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// InstallApk installs a local .apk onto the device using `adb install`.
// flags: -r(reinstall), -g(grant runtime permissions). 반환은 adb stdout (성공/실패 메시지).
// adb install 은 내부적으로 push + pm install 을 수행한다.
func (d *Device) InstallApk(ctx context.Context, localPath string, reinstall, grantPerms bool) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
	}
	args := []string{"-s", d.Serial, "install"}
	if reinstall {
		args = append(args, "-r")
	}
	if grantPerms {
		args = append(args, "-g")
	}
	args = append(args, localPath)
	out, err := d.run(ctx, args...)
	// adb install 은 실패 시 stdout 에 "Failure [...]" 출력 후 exit 0 인 경우가 있다.
	if err == nil && strings.Contains(out, "Failure") {
		return out, fmt.Errorf("install failed: %s", strings.TrimSpace(out))
	}
	return out, err
}

// UninstallApk uninstalls a package from the device.
// keepData=true 이면 -k 로 사용자 데이터 + 캐시 보존.
func (d *Device) UninstallApk(ctx context.Context, packageName string, keepData bool) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	args := []string{"-s", d.Serial, "uninstall"}
	if keepData {
		args = append(args, "-k")
	}
	args = append(args, packageName)
	out, err := d.run(ctx, args...)
	if err == nil && strings.Contains(out, "Failure") {
		return out, fmt.Errorf("uninstall failed: %s", strings.TrimSpace(out))
	}
	return out, err
}

func (d *Device) run(ctx context.Context, args ...string) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "adb", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
