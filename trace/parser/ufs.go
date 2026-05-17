package parser

import "sort"

// ProcessUFS — Rust `processors::ufs::ufs_bottom_half_latency_process` 의 Go 포팅.
//
// 입력은 시간 정렬되지 않을 수 있으므로 stable sort 로 정렬한 뒤, send_req ↔ complete_rsp
// 를 (tag, opcode) 키로 매칭해 dtoc/ctoc/ctod/qd/continuous 를 채운다. aligned 는
// 라인 파싱 시점에 이미 채워져 있다.
func ProcessUFS(events []UFSEvent) []UFSEvent {
	if len(events) == 0 {
		return events
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Time < events[j].Time
	})

	type ufsKey struct {
		Tag    uint32
		Opcode string
	}
	reqTimes := make(map[ufsKey]float64, len(events)/3+8)

	var currentQD uint32
	var lastCompleteTime float64
	var hasLastComplete bool
	var lastCompleteQD0Time float64
	var hasLastCompleteQD0 bool

	type prevReq struct {
		LBA    uint64
		Size   uint32
		Opcode string
	}
	var prev prevReq
	var hasPrev bool

	var firstC bool
	var firstCompleteTime float64

	for i := range events {
		ev := &events[i]
		switch ev.Action {
		case "send_req":
			if hasPrev {
				prevEnd := prev.LBA + uint64(prev.Size)
				ev.Continuous = ev.LBA == prevEnd && ev.Opcode == prev.Opcode
			} else {
				ev.Continuous = false
			}
			prev = prevReq{LBA: ev.LBA, Size: ev.Size, Opcode: ev.Opcode}
			hasPrev = true

			reqTimes[ufsKey{Tag: ev.Tag, Opcode: ev.Opcode}] = ev.Time
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

		case "complete_rsp":
			ev.Continuous = false
			if currentQD > 0 {
				currentQD--
			}
			key := ufsKey{Tag: ev.Tag, Opcode: ev.Opcode}
			if sendTime, ok := reqTimes[key]; ok {
				delete(reqTimes, key)
				diff := ev.Time - sendTime
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
			if currentQD == 0 {
				lastCompleteQD0Time = ev.Time
				hasLastCompleteQD0 = true
			}
			lastCompleteTime = ev.Time
			hasLastComplete = true

		default:
			ev.Continuous = false
		}
		ev.QD = currentQD
	}

	return events
}
