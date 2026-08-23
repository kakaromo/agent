package parser

import "sort"

// FsioBlockEvent latency 후처리.
// Rust `../trace/src/processors/fsio_block.rs` 의 포팅.
//
// 매칭 키 = (devmajor, devminor, sector, rwbs), 정렬키 = time,
// qd 0→1 전환 시 ctod, dtoc/ctoc 음수 가드.
//
// 키가 `io_type` 이 아니라 `rwbs` 인 이유 — bpftrace 파서는 정책상 `io_type` 을
// **항상 빈 값**으로 둔다. io_type 을 키로 쓰면 (sector, "") 가 되어
//  ① 같은 섹터의 rwbs 가 다른 동시 요청을 구분하지 못하고
//  ② `io_op != ""` 가드 때문에 `continuous` 가 **영원히 false** 가 된다
//     (→ 통계의 continuous_count/ratio 가 항상 0).
//
// rwbs("WS"/"R"/"D"...) 가 판별 정보를 그대로 갖고 있으므로 rwbs 를 키로 쓴다.
//
// device 가 키에 있어야 하는 이유 — 섹터 주소공간은 **블록 디바이스마다 독립**이다.
// 여러 디스크(예: UFS LU 0/1 → sda/sdb)에 동시 IO 가 나가면 같은 sector 번호가
// 정상적으로 공존하므로, device 를 빼면 complete 가 다른 디스크의 issue 와 짝지어진다.

type blockKey struct {
	devMajor uint32
	devMinor uint32
	sector   uint64
	rwbs     string
}

// ProcessFsioBlock — FsioBlockEvent 리스트에 dtoc/ctoc/ctod/qd/continuous 를 채운다.
func ProcessFsioBlock(list []FsioBlockEvent) []FsioBlockEvent {
	if len(list) == 0 {
		return list
	}

	sort.SliceStable(list, func(i, j int) bool { return list[i].Time < list[j].Time })

	// ⚠ UFS 와 달리 **시간 만료로만** 닫는다. UFS 는 tag 가 재사용되는 자원이라
	// "같은 tag 에 새 send" 가 곧 "이전 건 종료" 신호가 되지만, block 의 키에 있는
	// sector 는 재사용되는 자원이 아니라 그런 신호가 없다. (자세히는 InFlight 주석)
	reqTimes := NewInFlight[blockKey, struct{}](len(list) / 3)
	// 미완결로 닫힌 건수 — 리포트에 드러낸다.
	var unfinished uint64
	var currentQD uint32
	var lastCompleteTime float64
	var hasLastComplete bool
	var lastCompleteQD0Time float64
	var hasLastCompleteQD0 bool
	var firstC bool
	var firstCompleteTime float64
	// continuous 비교: 직전 issue 의 (device, sector + size/512 = endSector, rwbs).
	// 섹터 주소공간이 디바이스마다 독립이라 device 가 다르면 섹터가 이어져도 연속이 아니다.
	var prevEndSector uint64
	var prevIOOp string
	var prevDevMajor, prevDevMinor uint32
	var hasPrev bool

	for i := range list {
		ev := &list[i]
		ioOp := ev.RWBS
		key := blockKey{devMajor: ev.DevMajor, devMinor: ev.DevMinor, sector: ev.Sector, rwbs: ioOp}

		switch ev.Action {
		case "block_rq_issue", "Q":
			if hasPrev && prevDevMajor == ev.DevMajor && prevDevMinor == ev.DevMinor &&
				ev.Sector == prevEndSector && ioOp == prevIOOp && ioOp != "" {
				ev.Continuous = true
			}
			prevEndSector = ev.Sector + ceilDiv64(uint64(ev.Size), 512)
			prevIOOp = ioOp
			prevDevMajor, prevDevMinor = ev.DevMajor, ev.DevMinor
			hasPrev = true

			// 미완결 보정 — 임계 시간을 넘긴 in-flight 를 닫고 qd 를 되돌린다.
			// 안 하면 놓친 complete 만큼 qd 가 영구히 부풀고, qd 가 0 으로 못
			// 돌아가 ctod 가 그 뒤로 전부 0 이 된다 (InFlight 주석 참고).
			if swept := reqTimes.Sweep(ev.Time); swept > 0 {
				currentQD = satSub(currentQD, uint32(swept))
				unfinished += swept
			}

			reqTimes.Insert(key, ev.Time, struct{}{})
			currentQD++
			if currentQD == 1 {
				if hasLastCompleteQD0 {
					if diff := ev.Time - lastCompleteQD0Time; diff >= 0 {
						ev.CtoD = diff * MilliSeconds
					}
				}
				firstC = true
				firstCompleteTime = ev.Time
			}

		case "block_rq_complete", "C":
			ev.Continuous = false
			if issueTime, ok := reqTimes.RemoveTime(key); ok {
				if diff := ev.Time - issueTime; diff >= 0 {
					ev.DtoC = diff * MilliSeconds
				}
			}
			if firstC {
				if diff := ev.Time - firstCompleteTime; diff >= 0 {
					ev.CtoC = diff * MilliSeconds
				}
				firstC = false
			} else if hasLastComplete {
				if diff := ev.Time - lastCompleteTime; diff >= 0 {
					ev.CtoC = diff * MilliSeconds
				}
			}
			currentQD = satSub(currentQD, 1)
			if currentQD == 0 {
				lastCompleteQD0Time = ev.Time
				hasLastCompleteQD0 = true
			}
			lastCompleteTime = ev.Time
			hasLastComplete = true
		}

		ev.QD = currentQD
	}

	// 트레이스 끝에 남은 in-flight — IO 도중 끝나면 정상적으로 남는다.
	unfinished += reqTimes.Finish()
	if unfinished > 0 {
		markUnfinishedBlock(list)
	}

	return list
}

// markUnfinishedBlock — complete 를 못 받은 issue 에 IsUnfinished 를 켠다.
//
// 키별 issue/complete 개수를 세어 남는 issue 를 뒤에서부터 표시한다. 뒤에서부터인
// 이유 — 같은 키가 여러 번 쓰였다면 짝을 못 찾은 건 나중 것일 가능성이 높다.
func markUnfinishedBlock(list []FsioBlockEvent) {
	completes := make(map[blockKey]int)
	for i := range list {
		ev := &list[i]
		if ev.Action == "block_rq_complete" || ev.Action == "C" {
			completes[blockKey{ev.DevMajor, ev.DevMinor, ev.Sector, ev.RWBS}]++
		}
	}
	for i := len(list) - 1; i >= 0; i-- {
		ev := &list[i]
		if ev.Action != "block_rq_issue" && ev.Action != "Q" {
			continue
		}
		key := blockKey{ev.DevMajor, ev.DevMinor, ev.Sector, ev.RWBS}
		if n := completes[key]; n > 0 {
			completes[key] = n - 1 // 짝이 있다
		} else {
			ev.IsUnfinished = true
		}
	}
}
