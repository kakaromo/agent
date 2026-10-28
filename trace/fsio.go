package trace

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// fsiotraceRemotePath — 기기 측 배포·실행 경로.
	// fsiotrace 는 root 전용 eBPF 도구이므로 일반 benchmark 도구 경로가 아닌 /dev 에 둔다.
	fsiotraceRemotePath = "/dev/" + fsiotraceBinary
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
//
// ⚠ **다만 fsio_read 는 얘기가 다르다.** page-cache hit/miss 판정 row
// (`vfs_read:exit` / `readv:exit` / `mmap_fault:exit`)는 cross-layer 흡수 대상이
// 아니라 **VFS 레이어 행 그 자체**다(OUTPUT_FORMAT.md §4.7, 파서도 layer=="VFS"
// 로 거른다). 그래서 vfs 를 안 넣으면 Page Cache 통계와 mmap 집계가 통째로 빈다.
// includeVFS 는 그때 vfs 를 함께 켠다.
func fsioOnlyLayer(traceType string, includeVFS bool) string {
	base := "ufs"
	if traceType == "fsio_block" {
		base = "blk"
	}
	if includeVFS {
		return base + ",vfs"
	}
	return base
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

// pushIfNeeded — 기기에 없거나 **로컬과 크기가 다를 때** push.
//
// ⚠ 예전엔 "있으면 건너뜀" 이었다. /dev 는 tmpfs 라 재부팅 전까지 남으므로
// tools/ 의 바이너리를 갈아 끼워도 기기에선 **옛 바이너리가 계속 돌았다.**
// (--clock 이 없던 빌드가 남아 fsio 시각이 mono 축으로 찍힌 사례.)
// 크기 비교는 완벽하진 않지만 빌드가 바뀌면 사실상 항상 달라진다.
func pushIfNeeded(ctx context.Context, dev *adb.Device, localPath, remotePath string) error {
	if st, err := os.Stat(localPath); err == nil {
		out, err := dev.Shell(ctx, "stat -c %s "+remotePath+" 2>/dev/null")
		if err == nil && strings.TrimSpace(out) == strconv.FormatInt(st.Size(), 10) {
			return nil
		}
	}
	return dev.Push(ctx, localPath, remotePath)
}

// buildFsioCommand — 기기에서 실행할 fsiotrace 커맨드.
//
// `-o` 를 주지 않으면 stdout 으로 TSV 를 흘리므로, 기존 ftrace 경로와 똑같이
// adb stdout 을 로그 파일로 리다이렉트하는 구조를 그대로 쓸 수 있다.
//
// `--clock=boot` 는 **명시한다.** ftrace(trace_clock=boot)·DRAM instance·스텝 경계
// (/proc/uptime) 가 전부 CLOCK_BOOTTIME 이라 fsio 도 그 축이어야 겹쳐 그릴 수 있다.
// 기본값이 boot 이긴 하지만 그 옵션이 생기기 전 빌드는 bpf_ktime_get_ns()
// (= MONOTONIC, suspend 중 멈춤) 를 그대로 찍는다 — 축이 suspend 누적만큼 밀려도
// drift 검사는 못 잡는다. 명시하면 옛 바이너리는 알 수 없는 옵션으로 **즉시 실패**한다.
func buildFsioCommand(traceType string, includeVFS bool) string {
	return fmt.Sprintf("%s --clock=boot --only %s", fsiotraceRemotePath, fsioOnlyLayer(traceType, includeVFS))
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
//
// 화면이 꺼진 기기에서는 BPF 로드(verifier)가 30초를 넘고 EAGAIN 재시도까지 겹칠 때가 있다
// (2026-10-28 SMOKE: USB 를 뺀 직후 화면 꺼짐 4회 연속 30초 초과, 1회는 attach 전에 멈춰 빈 trace.
// 화면 켜짐은 15초, 몇 분 뒤 화면 꺼짐은 25초). 대기 시나리오가 화면을 끈 뒤 trace 를 켜므로 넉넉히 기다린다.
const fsioReadyTimeout = 180 * time.Second

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
func startFsioCollector(ctx context.Context, serial, traceType string, includeVFS bool, logFd *os.File) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "adb", "-s", serial, "shell", buildFsioCommand(traceType, includeVFS))
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

// ---- 기기 파일 기록 (fsio_output=device) ------------------------------------------
//
// stream 방식은 fsiotrace stdout 을 `adb shell` 로 받는다. 무선 adb 가 잠깐만 끊겨도
// adb shell 세션이 닫히며 fsiotrace 가 SIGHUP 을 받고 끝나고, 다시 붙어도 이어지지
// 않는다 (2026-10-29 3시간 대기: 131분째 5분 끊김으로 기록 종료).
//
// device 방식은 fsiotrace 를 **새 session(setsid)** 으로 띄워 adb 세션과 떼고,
// `-o <prefix>` 로 기기 tmpfs(/dev)에 쓴다. trace_stop 때 SIGTERM 으로 끝내고
// `<prefix>.events` 를 pull 해 호스트 trace.log 로 만든다. 형식은 stdout 과 같다
// (fsiotrace 가 같은 함수로 쓴다). 요약(diag)은 `<prefix>.summary`·`.stderr` 로 따로 남는다.

// FsioOutputDevice — StartTraceRequest.fsio_output 에서 기기 파일 기록을 뜻하는 값.
const FsioOutputDevice = "device"

// fsioDevicePrefix — 기기 측 기록 파일 prefix. 잡마다 다르게 해서 남은 파일과 섞이지 않게 한다.
func fsioDevicePrefix(jobID string) string {
	id := jobID
	if len(id) > 8 {
		id = id[:8]
	}
	return "/dev/fsio_" + id
}

// fsioPullTimeout — 기기 기록 파일을 가져오는 상한. 대기 6시간이면 200MB 안팎이라
// 무선 adb 로도 몇십 초면 되지만, 연결이 느린 경우를 위해 넉넉히 둔다.
var fsioPullTimeout = 10 * time.Minute

// fsioDeviceWaitTimeout — trace_stop 때 기기가 안 보이면 다시 붙기를 기다리는 상한.
// 수집은 기기에서 계속되고 있으므로, 잠깐 끊긴 연결이 돌아오면 그대로 가져올 수 있다.
var fsioDeviceWaitTimeout = 3 * time.Minute

// startFsioOnDevice — fsiotrace 를 기기 파일 기록으로 띄우고 attach 완료까지 기다린다.
//
// stream 방식처럼 준비 완료 줄("writing events to")을 기다린다. 상한을 넘기면 경고만
// 남기고 진행하는 것도 같다. 다만 그 사이에 fsiotrace 가 죽으면 바로 실패로 돌려준다.
func startFsioOnDevice(dev *adb.Device, serial, traceType string, includeVFS bool, prefix string) error {
	ctx, cancel := context.WithTimeout(context.Background(), fsioReadyTimeout+30*time.Second)
	defer cancel()

	// 두 개를 동시에 붙이면 훅이 겹쳐 둘 다 틀린다. 앞 잡이 남긴 프로세스가 있으면 멈춘다.
	if out, _ := dev.Shell(ctx, "pidof "+fsiotraceBinary); strings.TrimSpace(out) != "" {
		return fmt.Errorf("기기에 fsiotrace 가 이미 돌고 있다 (pid %s). 앞 수집을 멈춘 뒤 다시 시도할 것",
			strings.TrimSpace(out))
	}
	_, _ = dev.Shell(ctx, "rm -f "+prefix+".*")

	// </dev/null >/dev/null 로 adb shell 이 기다릴 fd 를 다 끊어야 바로 돌아온다.
	// --no-parent-watch: fsiotrace 는 부모(adb shell)가 사라지면 스스로 끝나는데,
	// 여기서는 일부러 떼어 놓았으므로 그 안전장치를 끈다 (끄지 않으면 attach 가 빨리
	// 끝나 부모가 살아 있을 때 기준을 잡은 경우 바로 종료된다).
	launch := fmt.Sprintf("setsid %s --no-parent-watch -o %s </dev/null >/dev/null 2>%s.stderr &",
		buildFsioCommand(traceType, includeVFS), prefix, prefix)
	if _, err := dev.Shell(ctx, launch); err != nil {
		return fmt.Errorf("fsiotrace 기기 실행 실패: %w", err)
	}

	deadline := time.Now().Add(fsioReadyTimeout)
	logged := 0
	for {
		out, _ := dev.Shell(ctx, "cat "+prefix+".stderr 2>/dev/null")
		var lines []string // 아직 비었으면 nil (빈 줄 하나로 세면 다음 회차의 첫 줄을 건너뛴다)
		if t := strings.TrimRight(out, "\n"); t != "" {
			lines = strings.Split(t, "\n")
		}
		for ; logged < len(lines); logged++ {
			if lines[logged] != "" {
				slog.Info("fsiotrace", "serial", serial, "msg", lines[logged])
			}
		}
		if strings.Contains(out, "writing events to") {
			return nil
		}
		if pid, _ := dev.Shell(ctx, "pidof "+fsiotraceBinary); strings.TrimSpace(pid) == "" {
			return fmt.Errorf("fsiotrace 가 attach 전에 끝났다 (%s.stderr 참고)", prefix)
		}
		if time.Now().After(deadline) {
			slog.Warn("fsiotrace attach 신호를 기다리다 시간이 초과됐다; 초반 이벤트를 놓쳤을 수 있다",
				"serial", serial, "timeout", fsioReadyTimeout, "output", "device")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// collectFsioFromDevice — 기기 측 fsiotrace 를 끝내고 기록 파일을 localLog 로 가져온다.
//
// 기기가 안 보이면 fsioDeviceWaitTimeout 동안 다시 붙기를 기다린다. 그래도 못 가져오면
// 기기에 파일을 남겨 두고 에러를 돌려준다 (/dev 는 재부팅 전까지 남는다).
func collectFsioFromDevice(getDev func() (*adb.Device, error), serial, prefix, localLog string) error {
	var dev *adb.Device
	waitUntil := time.Now().Add(fsioDeviceWaitTimeout)
	for {
		d, err := getDev()
		if err == nil {
			aliveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			alive := d.IsAlive(aliveCtx)
			cancel()
			if alive {
				dev = d
				break
			}
		}
		if time.Now().After(waitUntil) {
			return fmt.Errorf("기기가 %s 동안 안 보여 기록을 못 가져왔다. 기기의 %s.events 에 남아 있다",
				fsioDeviceWaitTimeout, prefix)
		}
		slog.Warn("fsiotrace 기록을 가져오려는데 기기가 안 보인다; 다시 붙기를 기다린다", "serial", serial)
		time.Sleep(10 * time.Second)
	}

	ctx, cancel := context.WithTimeout(context.Background(), fsioPullTimeout)
	defer cancel()

	// SIGTERM → fsiotrace 가 ringbuf 를 배수하고 파일을 flush·close 한 뒤 끝난다.
	stopFsioOnDevice(ctx, dev)
	exitUntil := time.Now().Add(30 * time.Second)
	for {
		pid, _ := dev.Shell(ctx, "pidof "+fsiotraceBinary)
		if strings.TrimSpace(pid) == "" {
			break
		}
		if time.Now().After(exitUntil) {
			slog.Warn("fsiotrace 가 30초 안에 끝나지 않았다; 지금까지 기록된 파일을 가져온다", "serial", serial)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 기기 쪽 진단 출력을 stream 방식과 같은 모양으로 로그에 남긴다.
	for _, f := range []string{".stderr", ".summary"} {
		if out, err := dev.Shell(ctx, "cat "+prefix+f+" 2>/dev/null"); err == nil {
			for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				if line != "" {
					slog.Info("fsiotrace", "serial", serial, "msg", line)
				}
			}
		}
	}

	if err := dev.Pull(ctx, prefix+".events", localLog); err != nil {
		return fmt.Errorf("기록 파일 pull 실패 (기기의 %s.events 에 남아 있다): %w", prefix, err)
	}
	_, _ = dev.Shell(ctx, "rm -f "+prefix+".*")
	return nil
}
