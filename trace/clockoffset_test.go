package trace

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClockSyncConcurrentAccess — StartTrace 가 ClockSync 를 쓰는 동안 다른 goroutine 이
// GetTraceJobInfo 로 읽는 상황을 재현한다.
//
// job 은 측정 **전에** 이미 m.jobs 에 등록되므로 이 경합은 실제로 일어난다.
// `go test -race` 로 돌려야 의미가 있다.
func TestClockSyncConcurrentAccess(t *testing.T) {
	job := &TraceJob{ID: "concurrent-test"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			job.Mu.Lock()
			job.ClockSync.Start = &ClockOffset{Offset: float64(i), RTTSec: 0.01, Samples: 3}
			job.Mu.Unlock()
		}
	}()

	for i := 0; i < 200; i++ {
		job.Mu.Lock()
		sync := job.ClockSync
		job.Mu.Unlock()
		// 복사본을 읽는 동안 writer 가 계속 쓴다.
		_ = sync.UncertaintySec()
		_, _ = sync.Usable()
	}
	<-done
}

// TestMeasureClockOffsetNeverBlocksCollection — 측정이 실패해도 **수집을 막지 않는다**는
// 이 함수의 계약을 지킨다. 예전에 nil device 로 panic 했는데, 그러면 오프셋만 못 재는
// 대신 트레이스 수집 전체가 죽는다 (계약과 정반대).
func TestMeasureClockOffsetNeverBlocksCollection(t *testing.T) {
	t.Run("nil device 는 panic 대신 nil", func(t *testing.T) {
		if off := MeasureClockOffset(context.Background(), nil); off != nil {
			t.Errorf("off = %+v, want nil", off)
		}
	})

	t.Run("취소된 ctx 는 즉시 nil", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if off := MeasureClockOffset(ctx, nil); off != nil {
			t.Errorf("off = %+v, want nil", off)
		}
	})
}

// TestMeasureBudgetCoversProbeLoop — MeasureBudget 이 루프를 다 돌 만큼 큰지.
// 작으면 표본이 잘려 최소값 채택이 무력해지는데도 `Usable()` 은 통과시킬 수 있다.
func TestMeasureBudgetCoversProbeLoop(t *testing.T) {
	need := time.Duration(offsetProbeCount) * (offsetProbeTimeout + offsetProbeGap)
	if MeasureBudget < need {
		t.Errorf("MeasureBudget=%v < 필요치 %v", MeasureBudget, need)
	}
}

func TestClockSyncSaveLoad(t *testing.T) {
	t.Run("왕복 후 값이 보존된다", func(t *testing.T) {
		dir := t.TempDir()
		want := TraceClockSync{
			Start: &ClockOffset{Offset: -1787692580.861554, RTTSec: 0.021, MeasuredAtSec: 1787692600.5, Samples: 5},
			Stop:  &ClockOffset{Offset: -1787692580.859, RTTSec: 0.019, MeasuredAtSec: 1787692630.2, Samples: 5},
		}
		SaveClockSync(dir, want)

		got, ok := LoadClockSync(dir)
		if !ok {
			t.Fatal("LoadClockSync ok = false, want true")
		}
		// offset 은 1e9 단위 큰 수라 float64 정밀도가 실제로 문제가 되는 자리다.
		if math.Abs(got.Start.Offset-want.Start.Offset) > 1e-6 {
			t.Errorf("Start.Offset = %v, want %v", got.Start.Offset, want.Start.Offset)
		}
		if math.Abs(got.Stop.RTTSec-want.Stop.RTTSec) > 1e-9 {
			t.Errorf("Stop.RTTSec = %v, want %v", got.Stop.RTTSec, want.Stop.RTTSec)
		}
		if got.Start.Samples != 5 {
			t.Errorf("Start.Samples = %d, want 5", got.Start.Samples)
		}
	})

	t.Run("측정이 없으면 파일을 안 만든다", func(t *testing.T) {
		dir := t.TempDir()
		SaveClockSync(dir, TraceClockSync{})
		if _, err := os.Stat(filepath.Join(dir, ClockSyncFileName)); !os.IsNotExist(err) {
			t.Error("빈 측정인데 파일이 생겼다")
		}
	})

	t.Run("파일이 없으면 false", func(t *testing.T) {
		if _, ok := LoadClockSync(t.TempDir()); ok {
			t.Error("ok = true, want false")
		}
	})

	t.Run("깨진 JSON 은 false — 조회 자체를 막지 않는다", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ClockSyncFileName), []byte("{not json"), 0o644)
		if _, ok := LoadClockSync(dir); ok {
			t.Error("ok = true, want false")
		}
	})

	t.Run("시작만 있는 상태도 저장된다", func(t *testing.T) {
		// StartTrace 직후 ~ StopTrace 전 상태. 이 시점에 agent 가 죽어도
		// 시작 오프셋은 남아야 한다.
		dir := t.TempDir()
		SaveClockSync(dir, TraceClockSync{Start: &ClockOffset{Offset: 100, RTTSec: 0.01, Samples: 3}})
		got, ok := LoadClockSync(dir)
		if !ok || got.Start == nil {
			t.Fatal("시작 측정이 보존되지 않았다")
		}
		if got.Stop != nil {
			t.Error("Stop = non-nil, want nil")
		}
		// 시작만으로도 구간 분할은 가능해야 한다 (drift 만 못 봄).
		if usable, reason := got.Usable(); !usable {
			t.Errorf("Usable() = false (%s), want true", reason)
		}
	})
}

func TestParseUptimeSeconds(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want float64
		ok   bool
	}{
		{"정상 2필드", "9574.90 33818.29", 9574.90, true},
		{"개행 포함", "9574.90 33818.29\n", 9574.90, true},
		{"CRLF (adb 가 붙이는 경우)", "9574.90 33818.29\r\n", 9574.90, true},
		{"앞뒤 공백", "  123.45 678.90  ", 123.45, true},
		{"첫 필드만", "42.5", 42.5, true},
		{"빈 문자열", "", 0, false},
		{"공백만", "   \n", 0, false},
		{"숫자 아님", "error: device offline", 0, false},
		// 부팅 직후가 아닌 한 uptime 은 양수다. 0/음수는 파싱은 됐지만 값이 이상한
		// 경우라, 그대로 쓰면 offset 이 통째로 틀어진다 — 거부하는 편이 안전하다.
		{"0 은 거부", "0.00 0.00", 0, false},
		{"음수는 거부", "-5.0 1.0", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseUptimeSeconds(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (in=%q)", ok, tt.ok, tt.in)
			}
			if ok && math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClockOffsetUsable(t *testing.T) {
	tests := []struct {
		name string
		c    ClockOffset
		want bool
	}{
		{"빠른 실기기", ClockOffset{RTTSec: 0.01, Samples: 5}, true},
		{"임계 경계 = 통과", ClockOffset{RTTSec: OffsetRTTThresholdSec, Samples: 3}, true},
		{"임계 초과", ClockOffset{RTTSec: OffsetRTTThresholdSec + 0.001, Samples: 3}, false},
		{"느린 에뮬레이터", ClockOffset{RTTSec: 21.4, Samples: 3}, false},
		{"샘플 0 = 측정 실패", ClockOffset{RTTSec: 0.01, Samples: 0}, false},
		// RTT 는 작지만 표본이 1개 — "최소값 채택" 이 없었으므로 믿지 않는다.
		// RTT 만 보면 통과해 버리는 함정이라 별도로 못 박는다.
		{"샘플 1 = RTT 작아도 거부", ClockOffset{RTTSec: 0.01, Samples: 1}, false},
		{"샘플 2 = 최소 요건", ClockOffset{RTTSec: 0.01, Samples: 2}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Usable(); got != tt.want {
				t.Errorf("Usable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClockOffsetHostToMonotonic(t *testing.T) {
	// 기기가 호스트보다 offset 만큼 앞선(혹은 뒤진) 축에 있다고 보고 변환한다.
	c := ClockOffset{Offset: -1787692580.0}
	// 호스트 wall clock 1787692600.5s → monotonic 20.5s
	got := c.HostToMonotonic(1787692600500)
	if math.Abs(got-20.5) > 1e-6 {
		t.Errorf("HostToMonotonic = %v, want 20.5", got)
	}
}

func TestClockOffsetUncertainty(t *testing.T) {
	// 불확실 폭은 RTT 의 절반 — 기기가 언제 읽었는지 모르는 구간이 왕복의 절반이라서.
	c := ClockOffset{RTTSec: 0.04}
	if got := c.UncertaintySec(); math.Abs(got-0.02) > 1e-9 {
		t.Errorf("UncertaintySec = %v, want 0.02", got)
	}
}

func TestTraceClockSyncDrift(t *testing.T) {
	t.Run("두 측정이 다 있으면 절대값", func(t *testing.T) {
		s := TraceClockSync{
			Start: &ClockOffset{Offset: 100.0, RTTSec: 0.01, Samples: 3},
			Stop:  &ClockOffset{Offset: 100.5, RTTSec: 0.01, Samples: 3},
		}
		d, ok := s.DriftSec()
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if math.Abs(d-0.5) > 1e-9 {
			t.Errorf("drift = %v, want 0.5", d)
		}
	})
	t.Run("역방향 drift 도 절대값", func(t *testing.T) {
		s := TraceClockSync{
			Start: &ClockOffset{Offset: 100.5},
			Stop:  &ClockOffset{Offset: 100.0},
		}
		d, _ := s.DriftSec()
		if math.Abs(d-0.5) > 1e-9 {
			t.Errorf("drift = %v, want 0.5", d)
		}
	})
	t.Run("종료 측정 없으면 판단 불가", func(t *testing.T) {
		s := TraceClockSync{Start: &ClockOffset{Offset: 100.0}}
		if _, ok := s.DriftSec(); ok {
			t.Error("ok = true, want false")
		}
	})
}

func TestTraceClockSyncUsable(t *testing.T) {
	fast := func(off float64) *ClockOffset {
		return &ClockOffset{Offset: off, RTTSec: 0.02, Samples: 5}
	}

	t.Run("정상 — drift 가 오차 범위 안", func(t *testing.T) {
		// 허용 폭 = 0.01 + 0.01 = 0.02. drift 0.005 는 측정 오차로 설명된다.
		s := TraceClockSync{Start: fast(100.0), Stop: fast(100.005)}
		if ok, reason := s.Usable(); !ok {
			t.Errorf("Usable() = false (%s), want true", reason)
		}
	})

	t.Run("drift 가 오차를 넘으면 거부", func(t *testing.T) {
		// 수집 중 시계가 실제로 움직인 경우 — 구간이 통째로 밀린다.
		s := TraceClockSync{Start: fast(100.0), Stop: fast(103.0)}
		ok, reason := s.Usable()
		if ok {
			t.Error("Usable() = true, want false")
		}
		if reason == "" {
			t.Error("이유 없이 비활성화하면 UI 가 원인을 못 알린다")
		}
	})

	t.Run("시작 측정 실패면 거부", func(t *testing.T) {
		s := TraceClockSync{}
		ok, reason := s.Usable()
		if ok || reason == "" {
			t.Errorf("Usable() = %v (%q), want false with reason", ok, reason)
		}
	})

	t.Run("표본 부족은 RTT 초과와 다른 이유를 준다", func(t *testing.T) {
		// 같은 문구를 쓰면 "느린 기기" 로 오해해서 엉뚱한 데를 본다.
		s := TraceClockSync{Start: &ClockOffset{Offset: 100, RTTSec: 0.01, Samples: 1}}
		ok, reason := s.Usable()
		if ok {
			t.Error("Usable() = true, want false")
		}
		if !strings.Contains(reason, "표본") {
			t.Errorf("reason = %q, 표본 부족임을 알려야 한다", reason)
		}
	})

	t.Run("RTT 임계 초과면 거부", func(t *testing.T) {
		s := TraceClockSync{Start: &ClockOffset{Offset: 100, RTTSec: 21.4, Samples: 3}}
		ok, reason := s.Usable()
		if ok {
			t.Error("Usable() = true, want false")
		}
		if reason == "" {
			t.Error("RTT 초과 이유가 비어 있다")
		}
	})

	t.Run("종료 측정이 없어도 시작만으로 진행", func(t *testing.T) {
		// 기기 분리 등으로 2차 측정을 못 할 수 있다. 없는 것을 실패로 치면
		// 정상 흐름이 과하게 막힌다 — drift 만 못 볼 뿐이다.
		s := TraceClockSync{Start: fast(100.0)}
		if ok, reason := s.Usable(); !ok {
			t.Errorf("Usable() = false (%s), want true", reason)
		}
	})

	t.Run("느린 종료 측정이어도 큰 drift 는 잡아낸다", func(t *testing.T) {
		// 호스트 슬립 복귀 시나리오: wall clock 이 30초 점프하고, 방금 깬 머신이라
		// 종료 probe 도 느리다. 예전엔 "못 믿을 측정" 이라고 버려서 usable=true 가
		// 나왔는데 — 그게 바로 30초 밀린 경계로 구간을 나누는 경로였다.
		// 느린 측정도 "수십 초 튀었다" 는 크기는 말해 준다.
		s := TraceClockSync{
			Start: fast(100.0),
			Stop:  &ClockOffset{Offset: 130.0, RTTSec: 6.0, Samples: 3},
		}
		ok, reason := s.Usable()
		if ok {
			t.Error("Usable() = true, want false — 30초 drift 를 놓쳤다")
		}
		if reason == "" {
			t.Error("drift 이유가 비어 있다")
		}
	})

	t.Run("느린 종료 측정의 오차 범위 안이면 통과", func(t *testing.T) {
		// 반대 방향 확인: 종료 측정이 느려 불확실 폭이 크면 budget 도 커져서
		// 그 안의 흔들림은 오탐으로 막지 않는다.
		// budget = 0.01 + 3.0 = 3.01 > drift 2.0
		s := TraceClockSync{
			Start: fast(100.0),
			Stop:  &ClockOffset{Offset: 102.0, RTTSec: 6.0, Samples: 3},
		}
		if ok, reason := s.Usable(); !ok {
			t.Errorf("Usable() = false (%s), want true", reason)
		}
	})
}

func TestTraceClockSyncUncertainty(t *testing.T) {
	t.Run("둘 중 나쁜 쪽을 택한다", func(t *testing.T) {
		// 낙관적으로 잡으면 "경계 모호" 로 표시해야 할 IO 를 놓친다.
		s := TraceClockSync{
			Start: &ClockOffset{RTTSec: 0.02},
			Stop:  &ClockOffset{RTTSec: 0.10},
		}
		if got := s.UncertaintySec(); math.Abs(got-0.05) > 1e-9 {
			t.Errorf("UncertaintySec = %v, want 0.05", got)
		}
	})
	t.Run("측정이 없으면 0", func(t *testing.T) {
		var s TraceClockSync
		if got := s.UncertaintySec(); got != 0 {
			t.Errorf("UncertaintySec = %v, want 0", got)
		}
	})
}
