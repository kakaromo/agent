package adb

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// PTYSession — adb shell 의 양방향 PTY 세션 핸들.
//
// 외부 PTY 라이브러리 (creack/pty) 미사용 — adb daemon 이 디바이스 측에서 /dev/ptmx 를
// 할당하므로 host 측은 단순히 stdin/stdout pipe 만 wrap 하면 vi/htop 등 TUI 도 동작.
// resize 는 별도 control fd 가 없어 stdin 으로 inline `stty rows N cols M` 명령을 전송한다.
//
// 사용 패턴 (gRPC 핸들러 측):
//
//	pty, err := device.ShellPTY(ctx, cols, rows)
//	defer pty.Close()
//
//	go io.Copy(pty, clientInputReader) // 클라이언트 입력 → pty stdin
//	go io.Copy(clientOutputWriter, pty) // pty stdout → 클라이언트
//
//	exit, _ := pty.Wait()
type PTYSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser // stdout + stderr merged

	closeMu sync.Mutex
	closed  bool

	resizeMu sync.Mutex
	lastCols uint32
	lastRows uint32
}

// Write — 클라이언트 입력을 pty stdin 으로 전달 (io.Writer 구현).
func (p *PTYSession) Write(b []byte) (int, error) {
	return p.stdin.Write(b)
}

// Read — pty stdout 을 읽어 클라이언트로 전달 (io.Reader 구현).
func (p *PTYSession) Read(b []byte) (int, error) {
	return p.stdout.Read(b)
}

// Resize — 디바이스 측 PTY 의 창 크기를 변경. 별도 control fd 가 없어 stdin 에
// stty 명령을 보내야 하는데 그게 그대로 사용자 입력 줄에 echo 된다.
// 노이즈 최소화:
//   - 같은 크기 연속 호출 무시 (frontend 가 ResizeObserver 로 자주 호출함)
//   - Ctrl-U (\x15) prefix 로 사용자 입력 줄을 먼저 비우고, 끝에 Ctrl-L (\x0c) 로 화면 redraw
//     → stty 명령 자체는 보이지만 사용자 현재 명령은 안 깨지고, 다음 prompt 가 깨끗하게 재출력
func (p *PTYSession) Resize(cols, rows uint32) error {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	p.resizeMu.Lock()
	defer p.resizeMu.Unlock()
	if p.lastCols == cols && p.lastRows == rows {
		return nil // 같은 크기 — 무시
	}
	p.lastCols = cols
	p.lastRows = rows
	// \x15: kill-line, \x0c: form-feed (redraw)
	_, err := fmt.Fprintf(p.stdin, "\x15stty rows %d cols %d 2>/dev/null\n", rows, cols)
	return err
}

// Close — pty 종료. cmd 를 kill 하고 pipe 닫는다. 여러 번 호출돼도 안전.
func (p *PTYSession) Close() error {
	p.closeMu.Lock()
	defer p.closeMu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true

	// stdin close 로 디바이스 측 shell 이 EOF 받고 정상 종료 시도.
	_ = p.stdin.Close()

	// 그래도 안 죽으면 강제 kill.
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.stdout.Close()
	return nil
}

// Wait — 자식 프로세스가 끝날 때까지 대기. exit code 반환.
// Close() 후 호출되어도 안전 (kill 된 경우 -1 반환).
func (p *PTYSession) Wait() (int, error) {
	err := p.cmd.Wait()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

// ShellPTY — 디바이스에 대화형 shell 세션을 연다.
//
// 구현:
//  1. `adb -s {serial} -t -t shell` 을 spawn (`-t -t` 가 PTY 강제 할당)
//  2. stdin/stdout pipe wrap, stderr 는 stdout 으로 merge
//  3. 초기화 명령 한 줄 push: stty rows/cols + TERM 설정
//
// 호출자가 PTYSession.Close() 로 명시 정리할 것. ctx cancel 시에도 close 권장.
func (d *Device) ShellPTY(ctx context.Context, cols, rows uint32) (*PTYSession, error) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	// shell -tt : PTY 강제 할당 (shell 명령의 옵션이라 'shell' 뒤에 와야 함;
	// adb 의 -t N 은 transport id 옵션이므로 'shell' 앞에 오면 잘못된 의미가 된다).
	cmd := exec.CommandContext(ctx, "adb", "-s", d.Serial, "shell", "-tt")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("adb pty stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("adb pty stdout: %w", err)
	}
	// stderr 도 stdout 으로 (xterm 측에서는 같은 stream 으로 받는 게 자연스러움)
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("adb pty start: %w", err)
	}

	// 초기화: stty 크기 + TERM, clear. echo 는 끄지 않음 (사용자 입력 보이도록).
	// 한 줄에 묶어 보냄 → 디바이스 측 sh 가 즉시 해석.
	initCmd := fmt.Sprintf("stty rows %d cols %d 2>/dev/null; export TERM=xterm-256color; clear\n", rows, cols)
	if _, err := stdin.Write([]byte(initCmd)); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("adb pty init: %w", err)
	}

	return &PTYSession{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		lastCols: cols,
		lastRows: rows,
	}, nil
}
