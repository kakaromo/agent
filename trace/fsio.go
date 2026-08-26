package trace

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agent/adb"
)

// bpftrace(fsiotrace) 기반 수집.
//
// ftrace 경로와 근본적으로 다르다:
//   - ftrace: 기기의 `events/*/enable` 을 켜고 `cat trace_pipe` 로 읽는다.
//   - fsio:   eBPF 바이너리를 push 해 실행하고 그 stdout(TSV) 을 읽는다.
//
// 그래서 switch case 추가가 아니라 수집 방식 자체가 분기한다.
// 출력 명세는 `../bpftrace/docs/OUTPUT_FORMAT.md` (TAB 17컬럼 TSV).

const (
	// fsiotraceBinary — tools/ 안의 로컬 바이너리 이름이자 기기에 push 될 이름.
	fsiotraceBinary = "fsiotrace"
	// fsiotraceRemotePath — 기기 측 경로. benchmark 도구들과 같은 위치.
	fsiotraceRemotePath = "/data/local/tmp/" + fsiotraceBinary
)

// IsFsioTraceType — bpftrace 계열 trace_type 인가.
func IsFsioTraceType(traceType string) bool {
	return traceType == "fsio_ufs" || traceType == "fsio_block"
}

// fsioOnlyLayer — trace_type 에 대응하는 fsiotrace `--only` 인자.
//
// ⚠ `--only` 는 **print 필터일 뿐 BPF 훅은 다 돈다.** 그래서 `--only ufs` 여도
// VFS/FS/BLK 훅이 흡수한 cross-layer 정보(풀패스 파일명, comm, syscall)가 UFS row 에
// 그대로 채워진다. 훅 자체를 끄는 `--no-blk`/`--no-fs` 를 쓰면 그 소스가 사라져
// 파일명이 빈다 — bpftrace 를 쓰는 이유가 없어지므로 쓰지 않는다.
func fsioOnlyLayer(traceType string) string {
	if traceType == "fsio_block" {
		return "blk"
	}
	return "ufs"
}

// prepareFsioDevice — fsiotrace 실행 전 사전 점검 + 바이너리 배포.
//
// root 가 아니면 **명확한 에러로 실패시킨다.** eBPF 는 root 가 필수라 그냥 실행하면
// 기기 쪽에서 조용히 죽고 빈 로그만 남는데, 그러면 "수집은 됐는데 이벤트가 0건"
// 으로 보여서 원인 추적이 어렵다. ftrace 경로는 root 없이 되는 경우가 있어
// 이 검사는 fsio 에만 건다.
func prepareFsioDevice(ctx context.Context, dev *adb.Device, toolsDir string) error {
	out, err := dev.Shell(ctx, "id -u")
	if err != nil {
		return fmt.Errorf("root 확인 실패: %w", err)
	}
	if strings.TrimSpace(out) != "0" {
		return fmt.Errorf("fsiotrace 는 root 가 필요하다 (id -u = %q). "+
			"userdebug 빌드에서 `adb root` 후 다시 시도할 것", strings.TrimSpace(out))
	}

	localPath := filepath.Join(toolsDir, fsiotraceBinary)
	if _, err := os.Stat(localPath); err != nil {
		return fmt.Errorf("%s 가 없다: %w — ../bpftrace 에서 빌드해 tools/ 에 두거나 "+
			"`bash scripts/build.sh` 산출물(out/aarch64/fsiotrace)을 복사할 것", localPath, err)
	}

	if err := pushIfNeeded(ctx, dev, localPath, fsiotraceRemotePath); err != nil {
		return fmt.Errorf("fsiotrace push 실패: %w", err)
	}
	if _, err := dev.Shell(ctx, "chmod 755 "+fsiotraceRemotePath); err != nil {
		return fmt.Errorf("chmod 실패: %w", err)
	}
	return nil
}

// pushIfNeeded — 기기에 없을 때만 push. benchmark/orchestrator.go 의 pushToolIfNeeded 와
// 같은 패턴이다 (그쪽은 unexported 라 재사용이 안 된다).
func pushIfNeeded(ctx context.Context, dev *adb.Device, localPath, remotePath string) error {
	out, err := dev.Shell(ctx, "ls "+remotePath+" 2>/dev/null && echo EXISTS")
	if err == nil && strings.Contains(out, "EXISTS") {
		return nil
	}
	return dev.Push(ctx, localPath, remotePath)
}

// buildFsioCommand — 기기에서 실행할 fsiotrace 커맨드.
//
// `-o` 를 주지 않으면 stdout 으로 TSV 를 흘리므로, 기존 ftrace 경로와 똑같이
// adb stdout 을 로그 파일로 리다이렉트하는 구조를 그대로 쓸 수 있다.
func buildFsioCommand(traceType string) string {
	return fmt.Sprintf("%s --only %s", fsiotraceRemotePath, fsioOnlyLayer(traceType))
}

// stopFsioOnDevice — 기기 측 fsiotrace 를 정상 종료시킨다.
//
// **SIGTERM 을 먼저 보내는 게 중요하다.** fsiotrace 는 시그널을 받으면 detach 후
// ringbuf 잔여 이벤트를 배수하고 끝내서 마지막 몇 건이 잘리지 않는다.
//
// adb 를 먼저 죽이면 stdout 파이프가 닫혀 EPIPE 로도 종료되긴 하지만, 그건
// fallback 경로다 (`../bpftrace/docs/USAGE.md` §7 — adb 리다이렉트 시에는 PTY 가
// 없어 Ctrl+C 가 기기까지 전달되지 않기 때문에 마련된 장치).
func stopFsioOnDevice(ctx context.Context, dev *adb.Device) {
	// pkill 실패는 무시한다 — 이미 죽었거나(-d 로 자체 종료) pkill 이 없는 기기일 수 있고,
	// 그 경우엔 뒤따르는 adb 종료의 EPIPE 경로가 받아 준다.
	_, _ = dev.Shell(ctx, "pkill -TERM "+fsiotraceBinary)
}

// fsioReadyTimeout — attach 완료 신호를 기다리는 상한.
//
// 넘겨도 수집은 그대로 진행한다(신호를 놓쳤을 뿐 살아 있을 수 있다). 다만 그 경우
// 앞부분 이벤트를 놓쳤을 가능성이 있으므로 경고를 남긴다.
const fsioReadyTimeout = 30 * time.Second

// startFsioCollector — fsiotrace 를 기기에서 실행하고 stdout 을 logFd 로 흘린다.
//
// **attach 가 끝날 때까지 기다렸다가 리턴한다.** 이게 없으면 시나리오에서 trace_start
// 다음 스텝이 곧바로 실행돼 **아직 훅이 안 붙은 구간의 IO 를 통째로 놓친다.**
// (Trace 탭은 사람이 버튼을 누르는 시간이 우연히 이 대기를 대신해 줘서 증상이 안 보였다.)
//
// fsiotrace 는 ringbuf 를 기본 512MB 로 잡는데, 커널이 이걸 미리 할당하는 시간이
// **메모리 상태에 따라 즉시~수십 초로 요동친다.** 그래서 고정 대기로는 못 맞추고
// 완료 신호를 봐야 한다 — stdout 모드에서는 poll 루프 직전의 "warn: stdout ..." 줄이
// 마지막 출력이라 그게 준비 완료 신호가 된다.
func startFsioCollector(ctx context.Context, serial, traceType string, logFd *os.File) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "adb", "-s", serial, "shell", buildFsioCommand(traceType))
	cmd.Stdout = logFd

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// ⚠ 버퍼 1 + non-blocking send. 예전엔 unbuffered + blocking send 였는데,
	// **타임아웃이 먼저 나면 받는 쪽이 사라져 이 goroutine 이 영영 막힌다.** 그러면
	// stderr 를 더 안 읽고, 파이프 버퍼(~64KB)가 차는 순간 fsiotrace 가 stderr 쓰기에서
	// 막혀 **이벤트 수집이 통째로 멎는다** — 느린 기기에서 조용히 잘린 trace 가 나온다.
	ready := make(chan struct{}, 1)
	go func() {
		sc := bufio.NewScanner(stderrPipe)
		signalled := false
		for sc.Scan() {
			line := sc.Text()
			// 진단 로그는 계속 흘린다 — attach 실패·유실 통계(diag[9])가 여기 나온다.
			slog.Info("fsiotrace", "serial", serial, "msg", line)
			if !signalled && isFsioReadyLine(line) {
				signalled = true
				select {
				case ready <- struct{}{}:
				default: // 이미 타임아웃으로 넘어갔다 — 알릴 곳이 없을 뿐 계속 읽는다
				}
			}
		}
	}()

	select {
	case <-ready:
		// attach 완료. 이제 워크로드를 돌려도 앞부분을 안 놓친다.
	case <-time.After(fsioReadyTimeout):
		slog.Warn("fsiotrace attach 신호를 기다리다 시간이 초과됐다; 초반 이벤트를 놓쳤을 수 있다",
			"serial", serial, "timeout", fsioReadyTimeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return cmd, nil
}

// isFsioReadyLine — fsiotrace 가 수집을 시작했음을 알리는 stderr 줄인가.
//
// stdout 모드: "warn: stdout 은 줄 단위로 flush 합니다 ..." (poll 루프 직전 마지막 출력)
// -o 모드   : "writing events to <path> ..."
// 둘 다 커버해 둔다 — 나중에 -o 로 바꿔도 이 함수는 그대로 동작한다.
func isFsioReadyLine(line string) bool {
	return strings.Contains(line, "warn: stdout") ||
		strings.Contains(line, "writing events to")
}
