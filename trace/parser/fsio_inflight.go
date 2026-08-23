package parser

import "math"

// 미완결 IO 보정 — complete 가 안 온 send 를 닫아 준다.
// Rust `../trace/src/processors/inflight.rs` 의 포팅.
//
// # 왜 필요한가
//
// bpftrace producer 는 complete 이벤트를 극소수 놓친다. 유실이 아니라 **구조적
// 한계**다 — `tp_btf` 의 프로그램별 재진입 가드 때문에, IRQ 문맥에서 여러 hw queue
// 의 완료가 같은 CPU 에 겹치면 두 번째가 스킵된다. ringbuf drop 이 0 이어도 발생하고,
// producer 쪽에서 고칠 수 없다고 결론이 나 있다 (bpftrace `docs/USAGE.md` §9).
// UFS 뿐 아니라 block 도 같은 이유로 간헐적으로 놓친다.
//
// 실측 0.036% (2271346 건 중 807). 비율은 작지만 **피해는 작지 않다.**
//
// # 보정 안 하면 무슨 일이 생기나
//
// currentQD 는 send 에서 +1, complete 에서 -1 하는 자유 실행 카운터다. complete
// 가 빠지면 그 +1 이 영영 안 돌아온다.
//
//  1. **qd 가 영구히 부풀어 오른다.** N 건 놓치면 그 뒤 **모든 행**의 qd 가 +N.
//     tag 64개짜리 장치에서 qd 800 같은 값이 나온다. qd 는 사용자 필터와 차트에도
//     쓰여서 조용히 잘못된 결과를 만든다.
//  2. **ctod 가 죽는다.** ctod 는 `currentQD == 1` 일 때만 계산되는데, qd 가 한 번
//     부풀면 0 으로 못 돌아가고 그 조건이 **영영 안 걸린다.** 즉 complete 하나만
//     빠져도 그 뒤로 ctod 가 전부 0 이다. qd 는 서서히 틀어지지만 ctod 는 한 방에
//     죽어서, 실은 이쪽이 더 심각하다.
//
// # 어떻게 닫나 — 종류마다 다르다
//
// **UFS: tag 재사용.** UFS tag 는 컨트롤러 전역 유일이라 같은 시점에 같은 tag 가
// 둘일 수 없다. 따라서 **같은 tag 에 새 send 가 오면 이전 건은 끝난 것**이다.
// 추측이 아니라 프로토콜이 보장하는 사실이라 이게 가장 정확한 신호다.
//
// **Block: 시간 만료.** block 의 짝짓기 키는 `(dev, sector, rwbs)` 인데 sector 는
// 재사용되는 자원이 아니라 "같은 키의 새 요청" 이라는 신호가 없다. 그래서 일정
// 시간이 지나도록 complete 가 안 오면 놓친 것으로 본다. 임계값은 휴리스틱이라
// 넉넉히 잡는다 — 진짜 느린 IO 를 미완결로 오판하는 것보다 조금 늦게 닫는 게 낫다.
// (UFS 도 tag 재사용이 안 일어난 채 트레이스가 끝날 수 있어 시간 만료를 같이 쓴다.)
//
// # 미완결로 판정된 건 어떻게 다루나
//
// **지연을 지어내지 않는다.** dtoc 는 0 으로 두고 IsUnfinished 로 표시해 지연
// 통계 모수에서 빠지게 한다. 0 이나 추정값으로 채우면 통계가 조용히 오염된다.
// 건수는 리포트에 드러낸다 — 0.036% 라 지금은 무해하지만, 이 값이 커지면 보정할
// 게 아니라 원인을 봐야 한다는 신호다.

// UnfinishedTimeoutSec — 미완결 판정 임계 시간(초).
//
// 이 시간이 지나도록 complete 가 안 오면 놓친 것으로 본다.
//
// 5초는 넉넉한 값이다 — 정상 IO 는 ms 단위이고, UFS/block 타임아웃도 보통 이보다
// 짧다. 짧게 잡으면 진짜 느린 IO(장치 스톨, 리셋 직전 등)를 미완결로 오판해서
// **실제 문제를 통계에서 지워 버린다.** 늦게 닫는 쪽이 안전하다.
const UnfinishedTimeoutSec float64 = 5.0

type inflightEntry[V any] struct {
	time  float64
	value V
}

// InFlight — in-flight send 추적 + 미완결 정리.
//
// K 는 짝짓기 키, V 는 send 쪽에서 넘길 상태(보통 send 시각 또는 메타 struct).
// 시간 만료를 위해 항목마다 send 시각을 따로 들고 있는다.
type InFlight[K comparable, V any] struct {
	m         map[K]inflightEntry[V]
	closed    uint64  // 미완결로 닫힌 누적 건수 — 리포트용
	lastSweep float64 // 마지막 만료 청소 시각. 매 이벤트마다 전체를 훑지 않기 위한 것
}

func NewInFlight[K comparable, V any](capacity int) *InFlight[K, V] {
	return &InFlight[K, V]{
		m:         make(map[K]inflightEntry[V], capacity),
		lastSweep: math.Inf(-1),
	}
}

// Insert — send 등록. 같은 키가 이미 있었으면 그 건은 **미완결로 확정**하고 돌려준다.
//
// UFS 의 tag 재사용 판정이 여기다. 호출부는 replaced 가 true 면 qd 를 하나
// 되돌려야 한다 — 그 send 의 +1 은 영영 안 돌아올 것이기 때문이다.
func (f *InFlight[K, V]) Insert(key K, time float64, value V) (old V, replaced bool) {
	if prev, ok := f.m[key]; ok {
		f.closed++
		old, replaced = prev.value, true
	}
	f.m[key] = inflightEntry[V]{time: time, value: value}
	return old, replaced
}

// Remove — complete 짝짓기. 정상 완료라 미완결 카운트에 넣지 않는다.
func (f *InFlight[K, V]) Remove(key K) (V, bool) {
	e, ok := f.m[key]
	if ok {
		delete(f.m, key)
	}
	return e.value, ok
}

// RemoveTime — 값 대신 send 시각을 돌려주는 판. 값에 시각을 따로 안 넣은 호출부(block)용.
func (f *InFlight[K, V]) RemoveTime(key K) (float64, bool) {
	e, ok := f.m[key]
	if ok {
		delete(f.m, key)
	}
	return e.time, ok
}

// Keys — 키를 직접 찾아 빼야 하는 경우(UFS 의 lun 미상 폴백)를 위한 접근자.
func (f *InFlight[K, V]) Keys() []K {
	ks := make([]K, 0, len(f.m))
	for k := range f.m {
		ks = append(ks, k)
	}
	return ks
}

// Sweep — now 기준 임계 시간을 넘긴 항목을 정리하고, 닫힌 개수를 돌려준다.
//
// 반환값만큼 호출부가 qd 를 되돌려야 한다.
//
// 매 이벤트마다 전체 맵을 훑으면 O(n²) 가 되므로 임계 시간의 절반마다만 돈다.
// 늦게 도는 만큼 qd 회복이 조금 늦어질 뿐 결과는 같다.
func (f *InFlight[K, V]) Sweep(now float64) uint64 {
	if now-f.lastSweep < UnfinishedTimeoutSec/2.0 {
		return 0
	}
	f.lastSweep = now
	var n uint64
	for k, e := range f.m {
		if now-e.time >= UnfinishedTimeoutSec {
			delete(f.m, k)
			n++
		}
	}
	f.closed += n
	return n
}

// Finish — 트레이스 끝에 남은 in-flight 를 전부 미완결로 확정한다.
//
// 트레이스가 IO 도중에 끝나면 정상적으로 남는 것이라, 이건 "유실" 과는
// 구분해서 봐야 한다. 그래도 미완결인 건 맞으므로 카운트에는 포함한다.
func (f *InFlight[K, V]) Finish() uint64 {
	n := uint64(len(f.m))
	f.closed += n
	clear(f.m)
	return n
}

// ClosedCount — 미완결로 닫힌 누적 건수.
func (f *InFlight[K, V]) ClosedCount() uint64 { return f.closed }

// Len — 현재 in-flight 개수.
func (f *InFlight[K, V]) Len() int { return len(f.m) }
