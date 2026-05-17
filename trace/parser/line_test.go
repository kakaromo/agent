package parser

import "testing"

func TestParseUFSLine_SendReq(t *testing.T) {
	// 실제 안드로이드 trace.log 샘플 라인 (kworker comm, READ_10)
	line := `   kworker/u17:8-26003   [001] ..... 268879.519101: ufshcd_command: send_req: 1d84000.ufshc: tag: 46, DB: 0x0, size: 32768, IS: 0, LBA: 327748, opcode: 0x28 (READ_10), group_id: 0x0, hwq_id: 1`
	ev, ok := parseUFSLine(line)
	if !ok {
		t.Fatal("expected parse success")
	}
	if ev.Action != "send_req" {
		t.Errorf("action = %q, want send_req", ev.Action)
	}
	if ev.CPU != 1 {
		t.Errorf("CPU = %d, want 1", ev.CPU)
	}
	if ev.Tag != 46 {
		t.Errorf("Tag = %d, want 46", ev.Tag)
	}
	if ev.LBA != 327748 {
		t.Errorf("LBA = %d, want 327748", ev.LBA)
	}
	// size: 32768 bytes → ceil(32768/4096) = 8 (4KB units)
	if ev.Size != 8 {
		t.Errorf("Size (4KB units) = %d, want 8", ev.Size)
	}
	if ev.Opcode != "0x28" {
		t.Errorf("Opcode = %q, want 0x28", ev.Opcode)
	}
	if ev.HWQID != 1 {
		t.Errorf("HWQID = %d, want 1", ev.HWQID)
	}
	if ev.Time < 268879.519100 || ev.Time > 268879.519102 {
		t.Errorf("Time = %f, want ~268879.519101", ev.Time)
	}
}

func TestParseUFSLine_CompleteRsp_IdleComm(t *testing.T) {
	// <idle>-0 comm 케이스 (preempt depth 변동 flags 포함)
	line := `          <idle>-0       [003] d.h2. 268879.507791: ufshcd_command: complete_rsp: 1d84000.ufshc: tag: 21, DB: 0x0, size: 4096, IS: 0, LBA: 700758, opcode: 0x28 (READ_10), group_id: 0x0, hwq_id: 4`
	ev, ok := parseUFSLine(line)
	if !ok {
		t.Fatal("expected parse success")
	}
	if ev.Process != "<idle>-0" {
		t.Errorf("Process = %q, want <idle>-0", ev.Process)
	}
	if ev.Action != "complete_rsp" {
		t.Errorf("action = %q, want complete_rsp", ev.Action)
	}
	if ev.Tag != 21 {
		t.Errorf("Tag = %d", ev.Tag)
	}
}

func TestParseUFSLine_DebugLBA(t *testing.T) {
	line := `              sh-23726   [004] ..... 268879.507619: ufshcd_command: send_req: 1d84000.ufshc: tag: 21, DB: 0x0, size: 4096, IS: 0, LBA: 2305843009213693951, opcode: 0x28 (READ_10), group_id: 0x0, hwq_id: 4`
	ev, ok := parseUFSLine(line)
	if !ok {
		t.Fatal("expected parse success")
	}
	if ev.LBA != 0 {
		t.Errorf("debug LBA should normalize to 0, got %d", ev.LBA)
	}
}

func TestParseUFSLine_NonUFS_Rejected(t *testing.T) {
	line := `          <idle>-0       [003] d.h2. 268879.500000: sched_switch: prev_comm=foo`
	if _, ok := parseUFSLine(line); ok {
		t.Error("expected non-UFS line to be rejected")
	}
}

func TestParseUFSLine_OtherAction_Rejected(t *testing.T) {
	// ufshcd_command 인데 send_req/complete_rsp 가 아닌 액션
	line := `   kworker/u17:8-26003   [001] ..... 268879.519101: ufshcd_command: foo_bar: 1d84000.ufshc: tag: 46`
	if _, ok := parseUFSLine(line); ok {
		t.Error("expected unknown ufs action to be rejected")
	}
}

func TestParseFtraceHeader_BasicKworker(t *testing.T) {
	line := `    kworker/5:2H-25770   [005] ..... 268879.626314: ufshcd_command: send_req`
	h, ok := parseFtraceHeader(line)
	if !ok {
		t.Fatal("header parse failed")
	}
	if h.Process != "kworker/5:2H-25770" {
		t.Errorf("Process = %q", h.Process)
	}
	if h.CPU != 5 {
		t.Errorf("CPU = %d", h.CPU)
	}
	if h.Flags != "....." {
		t.Errorf("Flags = %q", h.Flags)
	}
}

func TestParseKV(t *testing.T) {
	payload := "tag: 46, DB: 0x0, size: 32768, IS: 0, LBA: 327748, opcode: 0x28 (READ_10), group_id: 0x0, hwq_id: 1"
	cases := []struct {
		key  string
		want string
	}{
		{"tag", "46"},
		{"size", "32768"},
		{"LBA", "327748"},
		{"opcode", "0x28 (READ_10)"},
		{"group_id", "0x0"},
		{"hwq_id", "1"},
	}
	for _, c := range cases {
		got, ok := parseKV(payload, c.key)
		if !ok {
			t.Errorf("parseKV(%q) not found", c.key)
			continue
		}
		if got != c.want {
			t.Errorf("parseKV(%q) = %q, want %q", c.key, got, c.want)
		}
	}
	if _, ok := parseKV(payload, "nope"); ok {
		t.Error("expected missing key to return ok=false")
	}
}

func TestParseUFSCustomLine(t *testing.T) {
	line := `0x28,12345,8192,100.500000,100.600000`
	ev, ok := parseUFSCustomLine(line)
	if !ok {
		t.Fatal("parse failed")
	}
	if ev.Opcode != "0x28" || ev.LBA != 12345 || ev.Size != 8192 {
		t.Errorf("fields wrong: %+v", ev)
	}
	if ev.StartTime != 100.5 || ev.EndTime != 100.6 {
		t.Errorf("times wrong: %f / %f", ev.StartTime, ev.EndTime)
	}
	// dtoc: (100.6 - 100.5) * 1000 = 100ms
	if ev.DtoC < 99.9 || ev.DtoC > 100.1 {
		t.Errorf("DtoC = %f, want ~100", ev.DtoC)
	}
}

func TestParseUFSCustomLine_Header(t *testing.T) {
	if _, ok := parseUFSCustomLine("opcode,lba,size,start_time,end_time"); ok {
		t.Error("header should be rejected")
	}
}

func TestAlignment(t *testing.T) {
	// 기본 64KB alignment → UFS 16 (4KB units), Block 128 (512B sectors)
	if !isUFSAligned(0) {
		t.Error("LBA 0 should be aligned")
	}
	if !isUFSAligned(16) {
		t.Error("LBA 16 should be aligned (64KB)")
	}
	if isUFSAligned(15) {
		t.Error("LBA 15 should NOT be aligned")
	}
	if !isBlockAligned(128) {
		t.Error("sector 128 should be aligned")
	}
	if isBlockAligned(64) {
		t.Error("sector 64 should NOT be aligned (half of 64KB)")
	}
}
