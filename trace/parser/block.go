package parser

import "sort"

// blockIOOp — Rust `processors::block` 의 io_type 첫 글자 분류.
// 'R'/'W'/'D'/그외 → "read"/"write"/"discard"/"other".
func blockIOOp(ioType string) string {
	if len(ioType) == 0 {
		return "other"
	}
	switch ioType[0] {
	case 'R':
		return "read"
	case 'W':
		return "write"
	case 'D':
		return "discard"
	default:
		return "other"
	}
}

// ProcessBlock — Rust `processors::block::block_bottom_half_latency_process` 의 Go 포팅.
//
// 두 단계:
//  1. 중복 issue 제거. (sector, io_op, size) 키. complete 는 (sector, io_op, size) 키에서
//     해당 키 제거. WS(Write+size=0) 중복 마커는 complete 단계에서 스킵.
//  2. dedup 결과에 대해 (sector, io_op) 키로 issue↔complete 매칭. dtoc/ctoc/ctod/qd/continuous.
func ProcessBlock(events []BlockEvent) []BlockEvent {
	if len(events) == 0 {
		return events
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Time < events[j].Time
	})

	// === Phase 1: dedup ===
	type dedupKey struct {
		Sector uint64
		IOOp   string
		Size   uint32
	}
	processedIssues := make(map[dedupKey]struct{}, len(events)/3+8)
	dedup := make([]BlockEvent, 0, len(events))

	for _, ev := range events {
		op := blockIOOp(ev.IOType)
		key := dedupKey{Sector: ev.Sector, IOOp: op, Size: ev.Size}
		switch ev.Action {
		case "block_rq_issue":
			if _, exists := processedIssues[key]; exists {
				continue
			}
			processedIssues[key] = struct{}{}
			dedup = append(dedup, ev)
		case "block_rq_complete":
			if len(ev.IOType) > 0 && ev.IOType[0] == 'W' && ev.Size == 0 {
				// Flush 중복 마커
				continue
			}
			delete(processedIssues, key)
			dedup = append(dedup, ev)
		default:
			dedup = append(dedup, ev)
		}
	}

	// === Phase 2: latency + continuity ===
	type matchKey struct {
		Sector uint64
		IOOp   string
	}
	reqTimes := make(map[matchKey]float64, len(dedup)/3+8)

	var currentQD uint32
	var lastCompleteTime float64
	var hasLastComplete bool
	var lastCompleteQD0Time float64
	var hasLastCompleteQD0 bool

	var prevEndSector uint64
	var prevIOOp string
	hasPrev := false

	var firstC bool
	var firstCompleteTime float64

	for i := range dedup {
		ev := &dedup[i]
		ev.Continuous = false
		op := blockIOOp(ev.IOType)
		key := matchKey{Sector: ev.Sector, IOOp: op}

		switch ev.Action {
		case "block_rq_issue", "Q":
			if op != "other" && hasPrev && ev.Sector == prevEndSector && op == prevIOOp {
				ev.Continuous = true
			}
			if op != "other" {
				prevEndSector = ev.Sector + uint64(ev.Size)
				prevIOOp = op
				hasPrev = true
			}

			reqTimes[key] = ev.Time
			currentQD++
			if currentQD == 1 {
				if hasLastCompleteQD0 {
					diff := ev.Time - lastCompleteQD0Time
					if diff >= 0 {
						ev.CtoD = diff * MilliSeconds
					}
				}
				firstC = true
				firstCompleteTime = ev.Time
			}

		case "block_rq_complete", "C":
			if issueTime, ok := reqTimes[key]; ok {
				delete(reqTimes, key)
				diff := ev.Time - issueTime
				if diff >= 0 {
					ev.DtoC = diff * MilliSeconds
				}
			}
			if firstC {
				diff := ev.Time - firstCompleteTime
				if diff >= 0 {
					ev.CtoC = diff * MilliSeconds
				}
				firstC = false
			} else if hasLastComplete {
				diff := ev.Time - lastCompleteTime
				if diff >= 0 {
					ev.CtoC = diff * MilliSeconds
				}
			}
			if currentQD > 0 {
				currentQD--
			}
			if currentQD == 0 {
				lastCompleteQD0Time = ev.Time
				hasLastCompleteQD0 = true
			}
			lastCompleteTime = ev.Time
			hasLastComplete = true
		}
		ev.QD = currentQD
	}

	return dedup
}
