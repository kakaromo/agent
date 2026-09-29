package trace

// DRAM 대역폭 수집 — Qualcomm `dcvs/bw_hwmon_meas` (bwmon-ddr) 를 **별도 ftrace instance** 로.
//
// 명세: bpftrace/docs/DRAM_BW.md.
//
// 왜 instance 인가:
//   - fsio 는 trace_pipe 를 안 쓴다(eBPF). 본 버퍼에 섞는 방식은 fsio 에서 불가능하다.
//   - ufs/block 도 본 버퍼에 섞지 않는다 — 수집 경로가 타입마다 달라지면 안 되고,
//     IO 로그(trace.log)와 DRAM 로그(dram.log)가 **항상 다른 파일**이어야 portal 의
//     "DRAM 로그 붙이기" 와 같은 모양이 된다.
//   - instance 는 자기 버퍼·자기 trace_clock 을 가진다. boot 로 맞춰 fsiotrace(boot)·
//     본 ftrace(boot) 와 같은 시간축에 둔다.
//
// 이벤트율은 ~250/s (4ms 창) 이라 비용은 무시할 수준이다.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"agent/adb"
)

// dramInstanceName — 우리가 만드는 ftrace instance. 다른 도구와 겹치지 않게 접두를 둔다.
const dramInstanceName = "move_dram"

// DramLogName — IO trace.log 옆에 남는 DRAM 원본 로그.
const DramLogName = "dram.log"

func dramInstanceDir(tracingDir string) string {
	return tracingDir + "/instances/" + dramInstanceName
}

// dramSetupCmd — instance 를 새로 만들고 bwmon-ddr 만 켠다. 성공하면 "ok" 를 출력.
//
// 이전 실행이 비정상 종료해 instance 가 남아 있을 수 있어 먼저 치운다(rmdir 은 비어 있지
// 않아도 instance 에 대해서는 동작한다 — 읽는 프로세스가 없을 때).
// 필터를 커널 안에서 거는 이유: 노드 3개 중 DRAM 은 ddr 하나뿐 — 이벤트량이 1/3 로 준다.
func dramSetupCmd(tracingDir string) string {
	d := dramInstanceDir(tracingDir)
	ev := d + "/events/dcvs/bw_hwmon_meas"
	return fmt.Sprintf(`[ -d %[1]s ] && { echo 0 > %[1]s/tracing_on; rmdir %[1]s; }; `+
		`mkdir %[1]s && echo boot > %[1]s/trace_clock && `+
		`echo 'name ~ "*bwmon-ddr*"' > %[2]s/filter && echo 1 > %[2]s/enable && `+
		`echo 1 > %[1]s/tracing_on && echo ok`, d, ev)
}

// dramTeardownCmd — 끄고 instance 를 지운다. 없는 기기에서도 에러 없이 끝난다.
func dramTeardownCmd(tracingDir string) string {
	d := dramInstanceDir(tracingDir)
	return fmt.Sprintf(`[ -d %[1]s ] && { echo 0 > %[1]s/tracing_on; rmdir %[1]s; }; true`, d)
}

// dramCollector — 실행 중인 DRAM 수집. TraceJob 이 들고 있다가 StopTrace 에서 정리한다.
type dramCollector struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	fd     *os.File
}

// startDramCollector — instance 를 켜고 trace_pipe 를 dram.log 로 받는다.
// 실패하면 사유와 함께 nil 을 돌려준다 — IO 수집은 계속 진행한다.
func startDramCollector(setupCtx context.Context, md *adb.ManagedDevice, tracingDir, logPath string) (*dramCollector, error) {
	out, err := md.Device.Shell(setupCtx, dramSetupCmd(tracingDir))
	// ⚠ 부분 문자열로 보지 않는다 — "Permission denied" 같은 에러 문구에도 "o","k" 가 섞일 수 있다.
	if err != nil || !hasLine(out, "ok") {
		md.Device.Shell(setupCtx, dramTeardownCmd(tracingDir))
		if err == nil {
			err = fmt.Errorf("instance 준비 실패: %s", strings.TrimSpace(out))
		}
		return nil, err
	}
	fd, err := os.Create(logPath)
	if err != nil {
		md.Device.Shell(setupCtx, dramTeardownCmd(tracingDir))
		return nil, fmt.Errorf("create dram log: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "adb", "-s", md.Serial, "shell",
		fmt.Sprintf("cat %s/trace_pipe", dramInstanceDir(tracingDir)))
	cmd.Stdout = fd
	if err := cmd.Start(); err != nil {
		cancel()
		fd.Close()
		md.Device.Shell(setupCtx, dramTeardownCmd(tracingDir))
		return nil, fmt.Errorf("start dram collector: %w", err)
	}
	go func() {
		// 수집기가 먼저 죽어도 IO 수집에는 영향이 없다 — 기록만 남긴다.
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			slog.Warn("DRAM 수집기가 먼저 종료됐다", "device", md.Serial, "error", err)
		}
	}()
	return &dramCollector{cmd: cmd, cancel: cancel, fd: fd}, nil
}

// stop — reader 를 먼저 죽이고 instance 를 지운다. 읽는 프로세스가 있으면 rmdir 이 EBUSY 라
// 순서가 중요하다. 기기 쪽 cat 이 늦게 정리될 수 있어 한 번 더 시도한다.
func (c *dramCollector) stop(ctx context.Context, md *adb.ManagedDevice, tracingDir string) {
	if c == nil {
		return
	}
	c.cancel()
	_ = c.fd.Close()
	if md == nil {
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		md.Device.Shell(ctx, dramTeardownCmd(tracingDir))
		out, _ := md.Device.Shell(ctx, fmt.Sprintf(`[ -d %s ] && echo left`, dramInstanceDir(tracingDir)))
		if !hasLine(out, "left") {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	slog.Warn("DRAM ftrace instance 가 남았다 — 다음 수집 시작 때 정리된다", "device", md.Serial)
}

// hasLine — 출력에 정확히 그 줄이 있는가 (부분 문자열 아님).
func hasLine(out, want string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}
