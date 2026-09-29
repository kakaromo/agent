package adb

// DRAM 대역폭(Qualcomm bw_hwmon_meas) 수집 가능 여부 — 기기 발견 시 1회 확인.
// 수집 자체는 trace/dram.go.

import (
	"context"
	"fmt"
	"strings"
)

// dramProbeCmd — 이벤트와 ftrace instance 지원을 한 번에 확인. 출력: "event=0|1 inst=0|1".
func dramProbeCmd(tracingDir string) string {
	return fmt.Sprintf(`e=0; i=0; [ -d %[1]s/events/dcvs/bw_hwmon_meas ] && e=1; [ -d %[1]s/instances ] && i=1; echo "event=$e inst=$i"`, tracingDir)
}

// ProbeDramBw — 이 기기에서 DRAM 대역폭을 받을 수 있나. 불가면 화면에 보여줄 사유 문장.
//
// SoC 이름(ro.board.platform)이 아니라 **이벤트가 실제로 있는지**로 판정한다 —
// Qualcomm 이어도 qcom-dcvs.ko 가 안 올라오면 없고, Exynos 는 애초에 없다.
// platform 은 사유 문장에 이름을 넣는 데만 쓴다.
func ProbeDramBw(ctx context.Context, dev *Device, tracingDir, platform string) (bool, string) {
	if tracingDir == "" {
		return false, "ftrace 디렉토리가 없어 DRAM 대역폭을 받을 수 없어요"
	}
	out, err := dev.Shell(ctx, dramProbeCmd(tracingDir))
	if err != nil {
		return false, "기기 확인에 실패했어요: " + err.Error()
	}
	return parseDramProbe(out, platform)
}

func parseDramProbe(out, platform string) (bool, string) {
	name := platform
	if name == "" {
		name = "이 기기"
	}
	hasEvent := strings.Contains(out, "event=1")
	hasInst := strings.Contains(out, "inst=1")
	switch {
	case hasEvent && hasInst:
		return true, ""
	case hasEvent:
		return false, fmt.Sprintf("%s 는 ftrace instance 를 지원하지 않아 DRAM 대역폭을 따로 받을 수 없어요", name)
	case strings.Contains(out, "event="):
		return false, fmt.Sprintf("%s 에는 DRAM 대역폭 이벤트(bw_hwmon_meas)가 없어요 — Qualcomm 이 아니거나 qcom-dcvs 가 안 올라와 있어요", name)
	default:
		return false, "기기 응답을 해석하지 못했어요: " + strings.TrimSpace(out)
	}
}
