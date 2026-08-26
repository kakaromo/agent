package benchmark

import (
	"context"
	"errors"
	"testing"

	pb "agent/pb"
)

// fakeTraceCtl — HostToDeviceMonotonic 만 쓰는 최소 구현.
type fakeTraceCtl struct {
	offset float64
	usable bool
}

func (f *fakeTraceCtl) StartTrace(context.Context, *pb.StartTraceRequest) (string, error) {
	return "", nil
}
func (f *fakeTraceCtl) StopTrace(string) error { return nil }
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
