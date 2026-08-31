package benchmark

import (
	"context"
	"errors"
	"testing"

	pb "agent/pb"
)

// fakeTraceCtl — HostToDeviceMonotonic 만 쓰는 최소 구현.
type fakeTraceCtl struct {
	offset    float64
	usable    bool
	traceType string // TraceTypeOf 가 돌려줄 값 (빈 값 = 모르는 잡)

	// marker 폴백용 — markerOK 면 markerSeq 를 하나씩 돌려준다.
	markerOK  bool
	markerSeq []float64
	markerN   int
}

func (f *fakeTraceCtl) StartTrace(context.Context, *pb.StartTraceRequest) (string, error) {
	return "", nil
}
func (f *fakeTraceCtl) StopTrace(string) error    { return nil }
func (f *fakeTraceCtl) TraceTypeOf(string) string { return f.traceType }
func (f *fakeTraceCtl) WriteBoundaryMarker(_ context.Context, _ string, _, _ string) (float64, bool) {
	if !f.markerOK {
		return 0, false
	}
	if f.markerN >= len(f.markerSeq) {
		return 0, false
	}
	v := f.markerSeq[f.markerN]
	f.markerN++
	return v, true
}

// ⚠ 실제 드리프트 잡의 모양을 표현할 수 있어야 한다:
// **스텝 중에는 성공(usable=true)하는데** 종료 후 판정에서 못 믿게 되는 경우다
// (ClockSync.Stop 이 StopTrace 에서야 채워지기 때문). 이 모양을 못 만들면
// "폴백이 동작한다" 는 테스트가 실제로는 아무것도 보장하지 못한다.
func (f *fakeTraceCtl) HostToDeviceMonotonic(_ string, hostMillis int64) (float64, bool) {
	if !f.usable {
		return 0, false
	}
	return float64(hostMillis)/1000.0 + f.offset, true
}

func TestJobStepBoundaries(t *testing.T) {
	t.Run("기록한 순서대로 나온다", func(t *testing.T) {
		j := &Job{}
		for i := 0; i < 3; i++ {
			j.appendStepBoundary("dev1", &pb.StepBoundary{StepIndex: int32(i)})
		}
		got := j.takeStepBoundaries("dev1")
		if len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
		for i, b := range got {
			if b.GetStepIndex() != int32(i) {
				t.Errorf("[%d] stepIndex = %d", i, b.GetStepIndex())
			}
		}
	})

	t.Run("디바이스별로 분리된다", func(t *testing.T) {
		// 멀티 디바이스 잡에서 섞이면 구간이 남의 것과 뒤엉킨다.
		j := &Job{}
		j.appendStepBoundary("a", &pb.StepBoundary{StepIndex: 1})
		j.appendStepBoundary("b", &pb.StepBoundary{StepIndex: 2})
		if len(j.takeStepBoundaries("a")) != 1 || len(j.takeStepBoundaries("b")) != 1 {
			t.Error("디바이스별 분리 실패")
		}
	})

	t.Run("없으면 nil", func(t *testing.T) {
		j := &Job{}
		if got := j.takeStepBoundaries("none"); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("nil 은 무시", func(t *testing.T) {
		j := &Job{}
		j.appendStepBoundary("d", nil)
		if got := j.takeStepBoundaries("d"); got != nil {
			t.Errorf("nil 이 들어갔다: %v", got)
		}
	})

	t.Run("take 는 꺼내고 비운다 — 재시도 시 섞이면 안 된다", func(t *testing.T) {
		// runOnDeviceWithRetry 는 실패 시 같은 디바이스로 재실행하고 시도마다
		// storeResult 를 부른다. 안 비우면 2회차 결과에 1회차 구간이 섞인다.
		j := &Job{}
		j.appendStepBoundary("d", &pb.StepBoundary{StepIndex: 0})
		j.appendStepBoundary("d", &pb.StepBoundary{StepIndex: 1})
		if got := j.takeStepBoundaries("d"); len(got) != 2 {
			t.Fatalf("1회차 len = %d, want 2", len(got))
		}
		// 2회차: 새로 기록한 것만 나와야 한다
		j.appendStepBoundary("d", &pb.StepBoundary{StepIndex: 0})
		got := j.takeStepBoundaries("d")
		if len(got) != 1 {
			t.Errorf("2회차 len = %d, want 1 — 1회차 구간이 섞였다", len(got))
		}
	})

	t.Run("반환값은 복사본 — 내부 상태와 공유하지 않는다", func(t *testing.T) {
		j := &Job{}
		j.appendStepBoundary("d", &pb.StepBoundary{StepIndex: 1})
		got := j.takeStepBoundaries("d")
		got[0] = nil // 호출자가 슬라이스를 건드려도 내부에 영향이 없어야 한다
		j.mu.Lock()
		internal := j.stepBoundaries["d"]
		j.mu.Unlock()
		if len(internal) != 0 {
			t.Error("take 후에도 내부에 남아 있다")
		}
	})
}

func TestRecordStepBoundary(t *testing.T) {
	es := expandedStep{
		step:        &pb.ScenarioStep{Type: "scroll", Params: map[string]string{"label": "피드 스크롤"}},
		stepIndex:   2,
		loopIndex:   1,
		repeatIndex: 1,
	}

	t.Run("offset 이 있으면 mono 가 채워진다", func(t *testing.T) {
		o := &Orchestrator{traceMgr: &fakeTraceCtl{offset: 1000, usable: true}}
		j := &Job{}
		// 호스트 5.0s/7.0s + offset 1000 → 기기 monotonic 1005/1007
		o.recordStepBoundary(j, "dev1", es, "trace-1", 5000, 7000, nil)

		b := j.takeStepBoundaries("dev1")[0]
		if b.GetType() != "scroll" || b.GetLabel() != "피드 스크롤" {
			t.Errorf("type/label = %q/%q", b.GetType(), b.GetLabel())
		}
		if b.GetStartedMono() != 1005.0 || b.GetFinishedMono() != 1007.0 {
			t.Errorf("mono = %v..%v, want 1005..1007", b.GetStartedMono(), b.GetFinishedMono())
		}
		if !b.GetSuccess() {
			t.Error("success = false")
		}
	})

	t.Run("offset 을 못 믿으면 mono 는 0 — 호스트 시각은 남는다", func(t *testing.T) {
		// 구간 분할은 못 하지만 로그 대조는 가능해야 한다.
		o := &Orchestrator{traceMgr: &fakeTraceCtl{usable: false}}
		j := &Job{}
		o.recordStepBoundary(j, "dev1", es, "trace-1", 5000, 7000, nil)

		b := j.takeStepBoundaries("dev1")[0]
		if b.GetStartedMono() != 0 || b.GetFinishedMono() != 0 {
			t.Errorf("mono 가 채워졌다: %v..%v", b.GetStartedMono(), b.GetFinishedMono())
		}
		if b.GetStartedAt() != 5000 || b.GetFinishedAt() != 7000 {
			t.Error("호스트 시각까지 사라졌다")
		}
	})

	t.Run("trace 가 안 돌면 mono 없음", func(t *testing.T) {
		o := &Orchestrator{traceMgr: &fakeTraceCtl{offset: 1000, usable: true}}
		j := &Job{}
		o.recordStepBoundary(j, "dev1", es, "", 5000, 7000, nil) // traceJobID 없음

		if b := j.takeStepBoundaries("dev1")[0]; b.GetStartedMono() != 0 {
			t.Errorf("trace 없이 mono 가 채워졌다: %v", b.GetStartedMono())
		}
	})

	t.Run("실패한 스텝도 구간은 남는다", func(t *testing.T) {
		// 실패 구간에 어떤 IO 가 났는지가 오히려 분석 대상이다.
		o := &Orchestrator{}
		j := &Job{}
		o.recordStepBoundary(j, "dev1", es, "", 100, 200, errStepFailed)

		b := j.takeStepBoundaries("dev1")[0]
		if b.GetSuccess() {
			t.Error("success = true, want false")
		}
		if b.GetError() == "" {
			t.Error("에러 메시지가 비었다")
		}
	})
}

var errStepFailed = errors.New("step failed")

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("got %q, want a", got)
	}
	// trace_start 스텝: 실행 전엔 비어 있고 실행 후에 생긴다
	if got := firstNonEmpty("", "b"); got != "b" {
		t.Errorf("got %q, want b", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ⚠ marker 는 **폴백**이다. offset 이 멀쩡하면 기기에 쓰지 않는 쪽이 기본이어야 한다.
func TestMarkerFallback(t *testing.T) {
	es := expandedStep{
		step:      &pb.ScenarioStep{Type: "scroll"},
		stepIndex: 1, repeatIndex: 1,
	}

	// ⚠⚠ **marker 가 있으면 offset 값이 있어도 marker 를 쓴다.**
	//
	// 예전엔 반대였다("offset 이 되면 marker 를 안 쓴다"). 그게 드리프트를 영영 못 덮는
	// 원인이었다: HostToDeviceMonotonic 은 스텝 중엔 항상 성공하므로(ClockSync.Stop 이
	// 아직 nil) mono 가 "그럴듯하지만 밀린" 값으로 채워지고 marker 가 버려진다. 그 뒤
	// StopTrace 에서 drift 가 잡히면 UI 가 offset 구간을 거부해 **구간이 전멸한다.**
	//
	// marker 는 커널이 자기 시계로 찍은 값이라 adb 왕복이 오차에 안 들어간다 —
	// offset 보다 정확하므로 있으면 쓰는 것이 맞다.
	t.Run("marker 가 있으면 offset 보다 우선한다", func(t *testing.T) {
		f := &fakeTraceCtl{offset: 100, usable: true, markerOK: true, markerSeq: []float64{7, 8}}
		o := &Orchestrator{traceMgr: f}
		j := &Job{}
		mk := &stepMarks{begin: 7, end: 8}
		o.recordStepBoundaryWith(j, "dev1", es, "trace1", 1000, 2000, nil, mk)

		b := j.takeStepBoundaries("dev1")[0]
		if b.GetBoundarySource() != "trace_marker" {
			t.Errorf("marker 가 있는데 안 쓰였다: source=%q — 드리프트 잡에서 구간이 전멸한다",
				b.GetBoundarySource())
		}
		if b.GetStartedMono() != 7 {
			t.Errorf("marker 값이 아니다: %v", b.GetStartedMono())
		}
	})

	t.Run("marker 가 없으면 offset 값이 남는다", func(t *testing.T) {
		f := &fakeTraceCtl{offset: 100, usable: true}
		o := &Orchestrator{traceMgr: f}
		j := &Job{}
		o.recordStepBoundaryWith(j, "dev1", es, "trace1", 1000, 2000, nil, nil)

		b := j.takeStepBoundaries("dev1")[0]
		if b.GetBoundarySource() != "" {
			t.Errorf("marker 가 없는데 marker 로 표시됐다: %q", b.GetBoundarySource())
		}
		if b.GetStartedMono() != 101 { // 1000ms/1000 + 100
			t.Errorf("offset 값이 아니다: %v", b.GetStartedMono())
		}
	})

	t.Run("offset 이 안 되면 marker 로 채운다", func(t *testing.T) {
		f := &fakeTraceCtl{usable: false}
		o := &Orchestrator{traceMgr: f}
		j := &Job{}
		mk := &stepMarks{begin: 7.5, end: 8.25}
		o.recordStepBoundaryWith(j, "dev1", es, "trace1", 1000, 2000, nil, mk)

		b := j.takeStepBoundaries("dev1")[0]
		if b.GetStartedMono() != 7.5 || b.GetFinishedMono() != 8.25 {
			t.Errorf("marker 값이 안 들어갔다: %v~%v", b.GetStartedMono(), b.GetFinishedMono())
		}
		if b.GetBoundarySource() != "trace_marker" {
			t.Errorf("출처 표시가 없다: %q — 화면이 신뢰도를 못 알린다", b.GetBoundarySource())
		}
	})

	t.Run("한쪽만 찍힌 marker 는 쓰지 않는다", func(t *testing.T) {
		for _, mk := range []*stepMarks{
			{begin: 7.5, end: 0},  // 끝을 못 찍음
			{begin: 0, end: 8.25}, // 시작을 못 찍음
			{begin: 9, end: 8},    // 역전 (시각이 거꾸로)
			nil,
		} {
			f := &fakeTraceCtl{usable: false}
			o := &Orchestrator{traceMgr: f}
			j := &Job{}
			o.recordStepBoundaryWith(j, "dev1", es, "trace1", 1000, 2000, nil, mk)

			b := j.takeStepBoundaries("dev1")[0]
			if b.GetStartedMono() != 0 || b.GetFinishedMono() != 0 {
				t.Errorf("불완전한 marker(%+v)로 구간을 만들었다 — 반쪽 구간은 엉뚱한 범위를 그린다", mk)
			}
		}
	})
}

// ⚠⚠ 드리프트 잡: 스텝 중에는 offset 이 **성공**하지만(Stop==nil → Usable() 조기 true)
// 종료 후 drift 판정으로 못 믿게 된다. 그때 UI 는 offset 구간을 거부하므로
// (boundarySource 가 비어 있으면 clockSyncUsable 을 요구한다) marker 가 남아 있어야
// 구간이 살아남는다.
//
// 이 케이스가 이 폴백의 **존재 이유**다 — 구간이 조용히 밀리는데 그래프는 정상으로
// 보이는 상황이라, 여기서 못 덮으면 기능의 절반이 사라진다.
func TestMarkerFallback_DriftJobKeepsBoundaries(t *testing.T) {
	es := expandedStep{step: &pb.ScenarioStep{Type: "scroll"}, stepIndex: 1, repeatIndex: 1}
	// usable=true = 스텝 중 offset 이 성공하는 상태 (드리프트는 나중에 드러난다)
	f := &fakeTraceCtl{offset: 100, usable: true, markerOK: true, markerSeq: []float64{7, 8}}
	o := &Orchestrator{traceMgr: f}
	j := &Job{}
	mk := &stepMarks{begin: 7, end: 8}
	o.recordStepBoundaryWith(j, "dev1", es, "trace1", 1000, 2000, nil, mk)

	b := j.takeStepBoundaries("dev1")[0]
	if b.GetBoundarySource() != "trace_marker" {
		t.Fatalf("offset 이 스텝 중 성공했다고 marker 를 버렸다 (source=%q) — "+
			"드리프트가 드러나면 UI 가 이 구간을 거부해 Behavior 가 통째로 사라진다",
			b.GetBoundarySource())
	}
}
