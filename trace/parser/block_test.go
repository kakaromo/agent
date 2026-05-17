package parser

import "testing"

func blockEv(action string, time float64, sector uint64, size uint32, ioType string) BlockEvent {
	return BlockEvent{
		Time:   time,
		Action: action,
		Sector: sector,
		Size:   size,
		IOType: ioType,
	}
}

func TestProcessBlock_SimpleMatch(t *testing.T) {
	events := []BlockEvent{
		blockEv("block_rq_issue", 1.000, 1000, 8, "R"),
		blockEv("block_rq_complete", 1.002, 1000, 8, "R"),
	}
	out := ProcessBlock(events)
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	if !approx(out[1].DtoC, 2.0) {
		t.Errorf("DtoC = %f, want 2.0", out[1].DtoC)
	}
	if out[0].QD != 1 || out[1].QD != 0 {
		t.Errorf("QD = (%d,%d), want (1,0)", out[0].QD, out[1].QD)
	}
}

func TestProcessBlock_DedupDuplicateIssue(t *testing.T) {
	// 같은 (sector, io_op, size) 의 두 번째 issue 는 제거됨
	events := []BlockEvent{
		blockEv("block_rq_issue", 1.000, 1000, 8, "R"),
		blockEv("block_rq_issue", 1.001, 1000, 8, "R"), // dup
		blockEv("block_rq_complete", 1.002, 1000, 8, "R"),
	}
	out := ProcessBlock(events)
	if len(out) != 2 {
		t.Fatalf("expected 2 events after dedup, got %d", len(out))
	}
}

func TestProcessBlock_FlushSize0_Skipped(t *testing.T) {
	// Write + size=0 complete 는 dedup 단계에서 스킵됨
	events := []BlockEvent{
		blockEv("block_rq_issue", 1.000, 1000, 8, "W"),
		blockEv("block_rq_complete", 1.001, 1000, 0, "W"), // flush 중복 마커
		blockEv("block_rq_complete", 1.002, 1000, 8, "W"),
	}
	out := ProcessBlock(events)
	// issue 1, complete 1 (flush 마커 스킵) = 2
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
}

func TestProcessBlock_Continuous_SameOp(t *testing.T) {
	// 같은 io_op + 인접 sector → continuous
	events := []BlockEvent{
		blockEv("block_rq_issue", 1.0, 1000, 8, "R"),
		blockEv("block_rq_issue", 1.001, 1008, 8, "R"),
	}
	out := ProcessBlock(events)
	if !out[1].Continuous {
		t.Error("adjacent same-op should be continuous")
	}
}

func TestProcessBlock_NotContinuous_OtherOp(t *testing.T) {
	events := []BlockEvent{
		blockEv("block_rq_issue", 1.0, 1000, 8, "X"), // other
		blockEv("block_rq_issue", 1.001, 1008, 8, "X"),
	}
	out := ProcessBlock(events)
	if out[1].Continuous {
		t.Error("other-op must not be continuous")
	}
}

func TestProcessBlock_NormalizeSector_Flush(t *testing.T) {
	// parseBlockLine 측에서 normalize 하므로 여기는 단위만
	if normalizeSector(1000, 0, "F") != 0 {
		t.Error("flush should normalize to 0")
	}
	if normalizeSector(1000, 8, "FF") != 0 {
		t.Error("FF should normalize to 0")
	}
	if normalizeSector(1000, 8, "R") != 1000 {
		t.Error("R should keep sector")
	}
}

func TestProcessUFSCustom_QD(t *testing.T) {
	// req A: 1.0~1.005, req B: 1.002~1.003 → 시점별 QD
	events := []UFSCustomEvent{
		{Opcode: "0x28", LBA: 100, Size: 8, StartTime: 1.000, EndTime: 1.005},
		{Opcode: "0x28", LBA: 200, Size: 8, StartTime: 1.002, EndTime: 1.003},
	}
	out := ProcessUFSCustom(events)
	// 첫 요청 start_qd = 1, 두 번째 start_qd = 2 (둘이 겹침)
	if out[0].StartQD != 1 {
		t.Errorf("ev0 StartQD = %d, want 1", out[0].StartQD)
	}
	if out[1].StartQD != 2 {
		t.Errorf("ev1 StartQD = %d, want 2", out[1].StartQD)
	}
	// 두 번째 complete 시 qd 1, 그 후 첫번째 complete 시 qd 0
	if out[1].EndQD != 1 {
		t.Errorf("ev1 EndQD = %d, want 1", out[1].EndQD)
	}
	if out[0].EndQD != 0 {
		t.Errorf("ev0 EndQD = %d, want 0", out[0].EndQD)
	}
}
