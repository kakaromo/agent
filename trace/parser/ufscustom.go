package parser

import "sort"

// ProcessUFSCustom — Rust `processors::ufscustom::ufscustom_bottom_half_latency_process` 의 Go 포팅.
//
// UFSCustom 은 (start_time, end_time) 두 timestamp 가 한 row 에 있다. 이벤트 기반으로
// QD 를 계산하고, 정렬 후 ctoc/ctod/continuous 를 채운다.
func ProcessUFSCustom(events []UFSCustomEvent) []UFSCustomEvent {
	if len(events) == 0 {
		return events
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].StartTime < events[j].StartTime
	})

	// QD 계산을 위한 시간순 (Start/Complete) 이벤트 시퀀스
	type evType uint8
	const (
		evStart evType = iota
		evComplete
	)
	type qdEvent struct {
		Time  float64
		Type  evType
		ReqIx int
	}
	qdEvents := make([]qdEvent, 0, len(events)*2)
	for i, e := range events {
		qdEvents = append(qdEvents,
			qdEvent{Time: e.StartTime, Type: evStart, ReqIx: i},
			qdEvent{Time: e.EndTime, Type: evComplete, ReqIx: i},
		)
	}
	sort.SliceStable(qdEvents, func(i, j int) bool {
		return qdEvents[i].Time < qdEvents[j].Time
	})

	type qdPair struct {
		Start uint32
		End   uint32
	}
	qdValues := make([]qdPair, len(events))
	var currentQD uint32
	for _, q := range qdEvents {
		switch q.Type {
		case evStart:
			currentQD++
			qdValues[q.ReqIx].Start = currentQD
		case evComplete:
			if currentQD > 0 {
				currentQD--
			}
			qdValues[q.ReqIx].End = currentQD
		}
	}

	var prevLBA uint64
	var prevSize uint32
	var prevOpcode string
	hasPrev := false
	var lastCompleteTime float64
	var hasLastComplete bool
	var lastQDZeroCompleteTime float64
	var hasLastQDZero bool

	for i := range events {
		ev := &events[i]
		ev.StartQD = qdValues[i].Start
		ev.EndQD = qdValues[i].End

		if hasPrev {
			ev.Continuous = ev.LBA == prevLBA+uint64(prevSize) && ev.Opcode == prevOpcode
		} else {
			ev.Continuous = false
		}

		if hasLastComplete {
			diff := ev.EndTime - lastCompleteTime
			if diff >= 0 {
				ev.CtoC = diff * MilliSeconds
			}
		}

		if ev.StartQD == 1 {
			if hasLastQDZero {
				diff := ev.StartTime - lastQDZeroCompleteTime
				if diff >= 0 {
					ev.CtoD = diff * MilliSeconds
				}
			}
		} else if hasLastComplete {
			diff := ev.StartTime - lastCompleteTime
			if diff >= 0 {
				ev.CtoD = diff * MilliSeconds
			}
		}

		lastCompleteTime = ev.EndTime
		hasLastComplete = true
		if ev.EndQD == 0 {
			lastQDZeroCompleteTime = ev.EndTime
			hasLastQDZero = true
		}

		prevLBA = ev.LBA
		prevSize = ev.Size
		prevOpcode = ev.Opcode
		hasPrev = true
	}

	return events
}
