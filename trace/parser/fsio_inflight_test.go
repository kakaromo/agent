package parser

import "testing"

// Rust `../trace/src/processors/inflight.rs` 의 테스트 포팅.

func TestInFlightInsertReturnsPreviousOnKeyReuse(t *testing.T) {
	// UFS tag 재사용 — 이전 건은 미완결로 확정된다.
	f := NewInFlight[uint32, string](4)
	if _, replaced := f.Insert(7, 1.0, "first"); replaced {
		t.Fatal("첫 insert 는 교체가 아니다")
	}
	old, replaced := f.Insert(7, 2.0, "second")
	if !replaced || old != "first" {
		t.Fatalf("이전 건이 나와야 함: old=%q replaced=%v", old, replaced)
	}
	if f.ClosedCount() != 1 {
		t.Errorf("closed = %d, want 1", f.ClosedCount())
	}
	// 새 건은 살아 있다.
	v, ok := f.Remove(7)
	if !ok || v != "second" {
		t.Errorf("remove = %q, %v", v, ok)
	}
	if f.ClosedCount() != 1 {
		t.Errorf("정상 완료는 미완결이 아니다: closed = %d", f.ClosedCount())
	}
}

func TestInFlightSweepClosesOnlyStaleEntries(t *testing.T) {
	f := NewInFlight[uint32, struct{}](4)
	f.Insert(1, 100.0, struct{}{})
	f.Insert(2, 108.0, struct{}{})
	// 첫 sweep 은 lastSweep = -inf 라 바로 돈다.
	if n := f.Sweep(109.0); n != 1 {
		t.Errorf("sweep = %d, want 1 (100.0 건만 만료: 109-100 > 5)", n)
	}
	if _, ok := f.Remove(2); !ok {
		t.Error("108.0 건은 살아 있어야 함")
	}
}

func TestInFlightSweepIsThrottled(t *testing.T) {
	f := NewInFlight[uint32, struct{}](4)
	f.Insert(1, 0.0, struct{}{})
	f.Sweep(100.0) // 기준점
	f.Insert(2, 100.0, struct{}{})
	// 임계의 절반(2.5초) 안이라 돌지 않는다.
	if n := f.Sweep(101.0); n != 0 {
		t.Errorf("throttle 실패: sweep = %d", n)
	}
	// 넘어가면 돈다.
	if n := f.Sweep(110.0); n != 1 {
		t.Errorf("sweep = %d, want 1", n)
	}
}

func TestInFlightFinishClosesRemaining(t *testing.T) {
	f := NewInFlight[uint32, struct{}](4)
	f.Insert(1, 1.0, struct{}{})
	f.Insert(2, 2.0, struct{}{})
	if n := f.Finish(); n != 2 {
		t.Errorf("finish = %d, want 2", n)
	}
	if f.ClosedCount() != 2 {
		t.Errorf("closed = %d, want 2", f.ClosedCount())
	}
	if n := f.Finish(); n != 0 {
		t.Errorf("두 번 불러도 중복 계산 안 됨: %d", n)
	}
}
