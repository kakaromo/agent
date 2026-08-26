package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agent/adb"
)

// clockoffset — 호스트 wall clock ↔ 기기 CLOCK_MONOTONIC 오프셋 측정.
//
// **왜 필요한가.** parquet 의 `time` 컬럼은 **기기 부팅 기준 monotonic 절대초**다.
// ftrace 는 커널 monotonic clock 의 sec.usec 를, fsiotrace 는 `bpf_ktime_get_ns()` 를
// 그대로 찍고 (`trace/parser/fsio_line.go` 가 TSV 0번 컬럼을 재기준 없이 넣는다),
// 반면 시나리오 스텝 경계는 호스트의 `time.Now()` = wall clock 이다.
// 두 축은 기준점이 무관해 **offset 을 재지 않으면 절대 못 맞춘다.**
//
//	구간경계(monotonic초) = 호스트 wall clock 초 + Offset
//
// ⚠ 차트가 X축을 `min:'dataMin'` 으로 그려서 "trace 시작 기준 상대초" 처럼 보이지만
// 아니다. 상대초로 오해하면 offset 없이 뺄셈만으로 될 것 같아 이 측정을 건너뛰게 되고,
// 그 경로는 **조용히 틀린다** (구간이 통째로 밀려도 그래프는 정상으로 보인다).
//
// **정확도 상한 = adb 왕복(RTT)의 절반.** 기기 시각을 읽는 동안 왕복 지연이 끼는데,
// 그게 요청 방향과 응답 방향에 어떻게 배분됐는지 알 수 없기 때문이다. 그래서 NTP 와
// 같은 수법을 쓴다 — 여러 번 재서 **RTT 가 가장 짧은 샘플을 채택**한다 (왕복이 짧을수록
// 참값에 가깝다).

const (
	// offsetProbeCount — 한 번 측정할 때 왕복 횟수. RTT 최소 샘플을 고르기 위한 표본이다.
	// 늘리면 정확도가 조금 오르지만 StartTrace 지연이 그만큼 커진다.
	offsetProbeCount = 5

	// offsetProbeTimeout — 한 번의 왕복에 허용하는 시간. 이걸 넘기는 기기는 어차피
	// RTT 임계도 넘겨 구간 분할이 비활성화된다.
	offsetProbeTimeout = 5 * time.Second

	// offsetProbeGap — 왕복 사이 간격.
	//
	// 붙여서 재면 5개 샘플이 같은 순간의 상태(바쁜 USB 버스, 전력 상태 전이)를 공유해
	// **서로 상관된다** — 최소값을 골라도 표본이 1개인 것과 비슷해진다. 조금 띄우면
	// 조건이 달라진 표본이 섞여 최소값 채택이 의미를 갖는다.
	offsetProbeGap = 20 * time.Millisecond

	// MeasureBudget — 측정 전체에 필요한 시간 상한. 호출자가 컨텍스트 시간을 잡을 때
	// 쓰라고 노출한다.
	//
	// ⚠ 이보다 짧은 컨텍스트를 주면 루프가 중간에 잘려 표본이 줄고, 그러면 최소값
	// 채택이 무력해진다 (`Samples` 가 1이 되는 경로). 실제로 그렇게 잘린 적이 있어
	// 상수로 고정했다.
	MeasureBudget = offsetProbeCount * (offsetProbeTimeout + offsetProbeGap)

	// OffsetRTTThresholdSec — 이 값을 넘으면 구간 분할을 **비활성화**한다.
	//
	// 근거: 이 기능이 나누려는 behavior 는 스크롤처럼 수백 ms 짜리다. 불확실 폭(±RTT/2)이
	// 구간 길이에 육박하면 "어느 구간의 IO 인가" 가 사실상 무의미해진다. 0.5초면 ±250ms —
	// 이미 한 스텝을 통째로 삼킬 수 있는 크기라 여기서 끊는다.
	//
	// ⚠ 이 값은 **실기기 데이터로 조정해야 하는 임시값**이다. 실기기 adb RTT 는 보통
	// 수~수십 ms 라 넉넉히 통과하고, 느린 에뮬레이터(Cuttlefish, RTT 수십초)는 확실히
	// 걸린다 — 그 사이 어디가 맞는지는 실측이 필요하다.
	OffsetRTTThresholdSec = 0.5

	// minUsableSamples — 이 개수 미만이면 오프셋을 믿지 않는다.
	//
	// 이 측정의 정확도 논리는 통째로 **"여러 번 재서 RTT 최소를 고른다"** 에 기대고
	// 있다. 표본이 1개면 고를 게 없어서 그 논리가 사라지는데, RTT 값 자체는 작을 수
	// 있어 `RTTSec <= 임계` 만 보면 **정상처럼 통과한다.** 표본이 1개로 줄어드는 건
	// 대개 기기가 느리거나 컨텍스트가 잘렸다는 신호라 오히려 의심해야 할 상황이다.
	minUsableSamples = 2
)

// ClockOffset — 한 시점에 측정한 호스트↔기기 시계 오프셋.
type ClockOffset struct {
	// Offset — 더하면 호스트 wall clock 초가 기기 monotonic 초가 되는 값.
	//   monotonic = wall + Offset
	Offset float64 `json:"offset"`

	// RTTSec — 채택된 샘플의 adb 왕복 시간(초). 불확실 폭이 ±RTT/2 라 이 값이 곧
	// 이 측정의 신뢰도다.
	RTTSec float64 `json:"rttSec"`

	// MeasuredAtSec — 측정 시점의 호스트 wall clock(초). 시작/종료 두 측정의 drift 를
	// 볼 때 기준이 된다.
	MeasuredAtSec float64 `json:"measuredAtSec"`

	// Samples — 실제로 성공한 왕복 횟수. 최소값을 몇 개 중에서 골랐는지를 뜻한다.
	// 1이면 "최소값 채택" 이 사실상 없었던 것이라 신뢰도가 떨어진다 (minUsableSamples 참고).
	Samples int `json:"samples"`
}

// UncertaintySec — 이 측정의 불확실 폭(± 초). 구간 경계에서 이 안에 들어오는 IO 는
// "어느 구간인지 단정할 수 없다" 고 표시해야 한다.
func (c ClockOffset) UncertaintySec() float64 { return c.RTTSec / 2 }

// Usable — 이 측정으로 구간 분할을 해도 되는가. 임계를 넘으면 **조용히 쓰지 말고**
// 기능을 끄고 이유를 알려야 한다 — 틀린 구간도 그래프는 정상으로 보이기 때문이다.
func (c ClockOffset) Usable() bool {
	return c.Samples >= minUsableSamples && c.RTTSec <= OffsetRTTThresholdSec
}

// TraceClockSync — 한 trace 잡의 시계 정합 정보. 시작·종료 두 번 재서 drift 를 본다.
type TraceClockSync struct {
	Start *ClockOffset `json:"start,omitempty"`
	Stop  *ClockOffset `json:"stop,omitempty"`
}

// DriftSec — 두 측정 사이에 오프셋이 얼마나 움직였나(초).
//
// 이상적으로는 0 이다 — monotonic 과 wall clock 은 같은 속도로 흘러야 하므로.
// 크게 벌어졌다면 (a) 측정 자체가 부정확했거나 (b) 수집 중 호스트 시각이 조정됐다
// (NTP 동기화, 수동 변경, 슬립 복귀). 어느 쪽이든 **그 잡의 구간 분할은 못 믿는다.**
// 두 번째 반환값은 두 측정이 다 있는지 여부.
func (s TraceClockSync) DriftSec() (float64, bool) {
	if s.Start == nil || s.Stop == nil {
		return 0, false
	}
	d := s.Stop.Offset - s.Start.Offset
	if d < 0 {
		d = -d
	}
	return d, true
}

// Usable — 구간 분할에 써도 되는가. 판단 근거를 문자열로 함께 돌려준다 (UI 가 "왜
// 비활성화됐는지" 를 그대로 보여줄 수 있어야 한다 — 이유 없는 비활성화는 버그로 읽힌다).
func (s TraceClockSync) Usable() (bool, string) {
	if s.Start == nil {
		return false, "시작 시점 clock offset 을 측정하지 못했습니다."
	}
	if s.Start.Samples < minUsableSamples {
		// RTT 초과와 다른 원인이라 메시지를 나눈다 — 같은 문구를 쓰면 "느린 기기"
		// 로 오해해서 엉뚱한 데를 본다.
		return false, fmt.Sprintf(
			"clock offset 표본이 %d개뿐이라(최소 %d개) 오차를 가늠할 수 없습니다.",
			s.Start.Samples, minUsableSamples)
	}
	if !s.Start.Usable() {
		return false, fmt.Sprintf(
			"adb 왕복(RTT %.3fs)이 임계 %.3fs 를 넘어 구간 경계를 신뢰할 수 없습니다.",
			s.Start.RTTSec, OffsetRTTThresholdSec)
	}
	// 종료 측정은 없을 수 있다 (기기 분리 등). 그 경우 drift 를 못 보지만 시작 측정만으로
	// 진행한다 — 없는 것을 실패로 치면 정상 흐름이 과하게 막힌다.
	if s.Stop == nil {
		return true, ""
	}

	drift, ok := s.DriftSec()
	if !ok {
		return true, ""
	}

	// 허용 drift 는 두 측정의 불확실 폭 합. 그 안이면 측정 오차로 설명되고,
	// 넘으면 시계가 실제로 움직였다는 뜻이다.
	budget := s.Start.UncertaintySec() + s.Stop.UncertaintySec()
	if drift > budget {
		return false, fmt.Sprintf(
			"수집 중 시계가 %.3fs 어긋났습니다(허용 %.3fs). 구간이 통째로 밀렸을 수 있습니다.",
			drift, budget)
	}

	// ⚠ 종료 측정이 **자체로는 못 믿을 값**(RTT 임계 초과)이어도 위 검사는 그대로
	// 통과시킨다. 예전엔 이 경우 drift 검사를 통째로 건너뛰었는데, 그게 정확히
	// 놓치면 안 되는 상황을 놓쳤다:
	//
	//   호스트가 수집 중 슬립 → 깨어나며 wall clock 이 30초 점프 → 방금 깬 머신이라
	//   종료 probe 도 느림(RTT > 임계) → "못 믿을 측정" 이라고 버림 → 시작 측정만으로
	//   usable=true → **30초 밀린 경계로 구간을 나눈다.**
	//
	// 느린 측정은 offset 을 **정밀하게** 못 주지만 "수십 초 튀었다" 는 **크기**는
	// 충분히 말해 준다. 그 신호를 버리면 이 파일이 막으려는 실패(조용히 밀린 구간)가
	// 그대로 일어난다. 불확실 폭이 큰 만큼 budget 도 함께 커져 오탐은 억제된다.
	return true, ""
}

// UncertaintySec — 구간 경계에 표시할 불확실 폭(± 초). 두 측정이 있으면 더 나쁜 쪽을
// 택한다 (낙관적으로 잡으면 "경계 모호" 를 놓친다).
func (s TraceClockSync) UncertaintySec() float64 {
	u := 0.0
	if s.Start != nil {
		u = s.Start.UncertaintySec()
	}
	if s.Stop != nil && s.Stop.UncertaintySec() > u {
		u = s.Stop.UncertaintySec()
	}
	return u
}

// HostToMonotonic — 호스트 wall clock(ms) → 기기 monotonic(초).
// 스텝 경계를 parquet `time` 과 같은 축으로 옮길 때 쓴다.
func (c ClockOffset) HostToMonotonic(hostMillis int64) float64 {
	return float64(hostMillis)/1000.0 + c.Offset
}

// ClockSyncFileName — 오프셋을 담는 사이드카 파일명. trace.log/result_*.parquet 과
// 같은 OutputDir 에 둔다.
//
// **왜 파일로 남기나.** `TraceJob` 은 메모리에만 있고, agent 를 재시작하면
// `GetTraceJobInfo` 가 디스크 폴백으로 parquet 을 찾아낸다 — 즉 **데이터는 남는데
// 오프셋만 사라지는** 상태가 된다. 그러면 예전 잡은 구간 분할이 조용히 불가능해진다.
// parquet 옆에 같이 두면 둘의 수명이 같아진다.
const ClockSyncFileName = "clocksync.json"

// SaveClockSync — 오프셋을 OutputDir 에 저장. 실패해도 트레이스 흐름을 막지 않는다.
func SaveClockSync(dir string, sync TraceClockSync) {
	if sync.Start == nil && sync.Stop == nil {
		return // 측정이 아예 없으면 빈 파일을 남길 이유가 없다
	}
	data, err := json.Marshal(sync)
	if err != nil {
		slog.Warn("clocksync marshal failed", "dir", dir, "error", err)
		return
	}
	path := filepath.Join(dir, ClockSyncFileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		slog.Warn("clocksync write failed", "path", path, "error", err)
	}
}

// LoadClockSync — 저장된 오프셋을 읽는다. 없거나 깨졌으면 빈 값 + false.
// (오프셋이 없는 잡은 구간 분할만 못 할 뿐 조회는 정상이라 에러가 아니다.)
func LoadClockSync(dir string) (TraceClockSync, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ClockSyncFileName))
	if err != nil {
		return TraceClockSync{}, false
	}
	var sync TraceClockSync
	if err := json.Unmarshal(data, &sync); err != nil {
		slog.Warn("clocksync unmarshal failed", "dir", dir, "error", err)
		return TraceClockSync{}, false
	}
	return sync, true
}

// MeasureClockOffset — 호스트↔기기 오프셋을 측정한다.
//
// 방법: `cat /proc/uptime` 을 왕복시키면서 호출 전후의 호스트 시각을 잡는다.
// 기기가 uptime 을 읽은 순간은 그 사이 어딘가이므로, **왕복 중앙**을 그 시점으로 본다.
// 실제와의 차이는 최대 RTT/2 — 이것이 이 측정의 원리적 한계다.
//
//	                 send                    recv
//	host  ────────────┬───────────────────────┬──────────►
//	                  │◄──────── RTT ────────►│
//	                  │           ▲           │
//	                  │      기기가 읽은 시점  │  ← 어디인지 모른다 → 중앙으로 가정
//
// `/proc/uptime` 을 쓰는 이유: 첫 필드가 부팅 이후 초라 CLOCK_MONOTONIC 과 같은 기준이고,
// root 없이 읽히며, 어느 안드로이드에나 있다. (`-v monotonic` logcat 이 같은 축이라는
// 것도 이 값과 대조해 확인했다.)
//
// 실패해도 에러를 반환하지 않고 nil 을 준다 — 오프셋 측정 실패가 **트레이스 수집 자체를
// 막아서는 안 된다.** 구간 분할만 비활성화되면 된다.
func MeasureClockOffset(ctx context.Context, dev *adb.Device) *ClockOffset {
	// nil 방어 — 측정 실패는 구간 분할만 끄면 되는 일인데, 여기서 panic 하면
	// **트레이스 수집 전체가 죽는다.** 이 함수의 계약("수집을 막지 않는다")과 정반대라
	// 어떤 경로로든 nil 이 들어와도 조용히 포기하는 쪽이 맞다.
	if dev == nil {
		slog.Warn("clock offset skipped: device is nil")
		return nil
	}

	best := ClockOffset{RTTSec: -1}

	for i := 0; i < offsetProbeCount; i++ {
		if ctx.Err() != nil {
			break
		}
		if i > 0 {
			// 표본을 서로 상관되지 않게 조금 띄운다 (offsetProbeGap 주석 참고).
			select {
			case <-ctx.Done():
			case <-time.After(offsetProbeGap):
			}
		}
		probeCtx, cancel := context.WithTimeout(ctx, offsetProbeTimeout)
		sent := time.Now()
		out, err := dev.Shell(probeCtx, "cat /proc/uptime")
		recv := time.Now()
		cancel()
		if err != nil {
			// ⚠ 첫 왕복이 타임아웃이면 **더 안 재고 끝낸다.**
			//
			// 실측(Cuttlefish, RTT 18~28s)에서 5회를 다 돌며 25초를 태우고 결국
			// nil 을 냈다. StartTrace 가 그만큼 늦어지는데 결과는 처음부터 정해져
			// 있었다 — probe 타임아웃(5s)을 넘는 기기는 RTT 임계(0.5s)도 당연히
			// 넘으므로 어차피 거부된다. 느린 기기일수록 대가가 큰 낭비라 끊는다.
			if probeCtx.Err() != nil && best.Samples == 0 {
				slog.Warn("clock offset probe timed out; 더 재지 않고 중단",
					"serial", dev.Serial, "probe_timeout", offsetProbeTimeout)
				break
			}
			continue
		}
		uptime, ok := parseUptimeSeconds(out)
		if !ok {
			continue
		}

		rtt := recv.Sub(sent).Seconds()
		// 왕복 중앙을 기기가 읽은 시점으로 본다.
		midHost := float64(sent.UnixNano())/1e9 + rtt/2
		best.Samples++
		// RTT 최소 샘플 채택 — 왕복이 짧을수록 중앙 가정의 오차가 작다.
		if best.RTTSec < 0 || rtt < best.RTTSec {
			best.RTTSec = rtt
			best.Offset = uptime - midHost
			best.MeasuredAtSec = midHost
		}
	}

	if best.Samples == 0 {
		slog.Warn("clock offset measurement failed; 구간 분할이 비활성화된다", "serial", dev.Serial)
		return nil
	}
	slog.Info("clock offset measured",
		"serial", dev.Serial, "offset", best.Offset,
		"rtt_sec", best.RTTSec, "samples", best.Samples, "usable", best.Usable())
	return &best
}

// parseUptimeSeconds — `/proc/uptime` 첫 필드(부팅 이후 초)를 뽑는다.
// 형식: "9574.90 33818.29" (uptime idle).
func parseUptimeSeconds(out string) (float64, bool) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}
