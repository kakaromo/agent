package parser

import (
	"math"
	"testing"
)

func approx(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

// helper — build a minimal UFSEvent with action/time/tag/lba/size/opcode.
func ufsEv(action string, time float64, tag uint32, lba uint64, size uint32, opcode string) UFSEvent {
	return UFSEvent{
		Time:   time,
		Action: action,
		Tag:    tag,
		LBA:    lba,
		Size:   size,
		Opcode: opcode,
	}
}

func TestProcessUFS_SimpleMatch(t *testing.T) {
	// send → complete pair. dtoc = 1ms, qd: 1 → 0
	events := []UFSEvent{
		ufsEv("send_req", 1.000, 7, 100, 8, "0x28"),
		ufsEv("complete_rsp", 1.001, 7, 100, 8, "0x28"),
	}
	out := ProcessUFS(events)
	if out[0].QD != 1 {
		t.Errorf("send_req QD = %d, want 1", out[0].QD)
	}
	if out[1].QD != 0 {
		t.Errorf("complete_rsp QD = %d, want 0", out[1].QD)
	}
	if !approx(out[1].DtoC, 1.0) {
		t.Errorf("DtoC = %f, want 1.0 ms", out[1].DtoC)
	}
}

func TestProcessUFS_Continuous(t *testing.T) {
	// 두 send_req 가 LBA 인접 + 같은 opcode → 두 번째가 continuous=true
	events := []UFSEvent{
		ufsEv("send_req", 1.0, 1, 100, 8, "0x28"), // ends at 108
		ufsEv("send_req", 1.001, 2, 108, 8, "0x28"),
	}
	out := ProcessUFS(events)
	if out[0].Continuous {
		t.Error("first send should not be continuous")
	}
	if !out[1].Continuous {
		t.Error("adjacent send should be continuous")
	}
}

func TestProcessUFS_NonContiguous(t *testing.T) {
	events := []UFSEvent{
		ufsEv("send_req", 1.0, 1, 100, 8, "0x28"),
		ufsEv("send_req", 1.001, 2, 200, 8, "0x28"), // gap
	}
	out := ProcessUFS(events)
	if out[1].Continuous {
		t.Error("non-adjacent send must not be continuous")
	}
}

func TestProcessUFS_CtoD_FromQD0(t *testing.T) {
	// send → complete (qd→0) → 새 send: 새 send 의 ctod = idle 구간
	events := []UFSEvent{
		ufsEv("send_req", 1.000, 1, 100, 8, "0x28"),
		ufsEv("complete_rsp", 1.005, 1, 100, 8, "0x28"), // qd→0 at t=1.005
		ufsEv("send_req", 1.010, 2, 200, 8, "0x28"),     // ctod = 5ms
	}
	out := ProcessUFS(events)
	if !approx(out[2].CtoD, 5.0) {
		t.Errorf("CtoD = %f, want 5.0", out[2].CtoD)
	}
}

func TestProcessUFS_TimeReversalGuarded(t *testing.T) {
	// stable sort 후에도 같은 time 의 complete→send 가 와도 dtoc 음수 방지
	events := []UFSEvent{
		ufsEv("complete_rsp", 1.0, 1, 100, 8, "0x28"), // 매칭되는 send 없음
	}
	out := ProcessUFS(events)
	if out[0].DtoC < 0 {
		t.Errorf("DtoC should not be negative, got %f", out[0].DtoC)
	}
}
