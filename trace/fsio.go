package trace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// startFsioCollector — fsiotrace 를 기기에서 실행하고 stdout 을 logFd 로 흘린다.
func startFsioCollector(ctx context.Context, serial, traceType string, logFd *os.File) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "adb", "-s", serial, "shell", buildFsioCommand(traceType))
	cmd.Stdout = logFd
	// stderr 는 진단용 — fsiotrace 는 attach 실패/유실 통계를 여기로 낸다.
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
