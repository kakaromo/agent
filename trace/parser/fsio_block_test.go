package parser

import "testing"

// Rust `../trace/src/processors/fsio_block.rs` 의 회귀 테스트 포팅.

// mkBlk — 마지막 인자는 **rwbs**. `io_type` 은 실제 파서와 동일하게 항상 빈 값으로 둔다.
func mkBlk(time float64, action string, sector uint64, size uint32, rwbs string) FsioBlockEvent {
	return FsioBlockEvent{
		Time:     time,
		Process:  "test",
		Action:   action,
		DevMajor: 8,
		DevMinor: 0,
		IOType:   "", // 파서 정책상 항상 빈 값
		Sector:   sector,
		Size:     size,
		Comm:     "test",
		RWBS:     rwbs,
	}
}

func mkBlkDev(time float64, action string, sector uint64, size uint32, rwbs string, devMinor uint32) FsioBlockEvent {
	e := mkBlk(time, action, sector, size, rwbs)
	e.DevMinor = devMinor
	return e
}

func findBlkAction(t *testing.T, list []FsioBlockEvent, action string) *FsioBlockEvent {
	t.Helper()
	for i := range list {
		if list[i].Action == action {
			return &list[i]
		}
	}
	t.Fatalf("action %q 를 못 찾음", action)
	return nil
}

func TestFsioBlockDtoCBasic(t *testing.T) {
	out := ProcessFsioBlock([]FsioBlockEvent{
		mkBlk(1.0, "block_rq_issue", 1000, 4096, "WS"),
		mkBlk(2.0, "block_rq_complete", 1000, 4096, "WS"),
	})
	if c := findBlkAction(t, out, "block_rq_complete"); !nearly(c.DtoC, 1000.0) {
		t.Errorf("dtoc = %v, want 1000", c.DtoC)
	}
}

// io_type 이 항상 빈 값이므로 rwbs 를 키로 써야 continuous 가 동작한다.
// io_type 을 키로 쓰면 continuous 가 영원히 false 다.
func TestFsioBlockContinuousWorksWhenIOTypeEmpty(t *testing.T) {
	out := ProcessFsioBlock([]FsioBlockEvent{
		mkBlk(1.0, "block_rq_issue", 1000, 4096, "WS"), // 4096/512 = 8 sector
		mkBlk(2.0, "block_rq_issue", 1008, 4096, "WS"),
	})
	if !out[1].Continuous {
		t.Error("continuous 가 false — io_type(빈 값) 을 키로 쓰고 있다")
	}
}

func TestFsioBlockNotContinuousWhenRWBSDiffers(t *testing.T) {
	out := ProcessFsioBlock([]FsioBlockEvent{
		mkBlk(1.0, "block_rq_issue", 1000, 4096, "WS"),
		mkBlk(2.0, "block_rq_issue", 1008, 4096, "R"), // 섹터는 이어지지만 종류가 다르다
	})
	if out[1].Continuous {
		t.Error("rwbs 가 다른데 연속으로 판정했다")
	}
}

// 같은 섹터라도 rwbs 가 다르면 별개 요청이다.
func TestFsioBlockDtoCPairsSameSectorDifferentRWBS(t *testing.T) {
	out := ProcessFsioBlock([]FsioBlockEvent{
		mkBlk(1.0, "block_rq_issue", 1000, 4096, "WS"),
		mkBlk(2.0, "block_rq_issue", 1000, 4096, "R"),
		mkBlk(3.0, "block_rq_complete", 1000, 4096, "WS"),
		mkBlk(6.0, "block_rq_complete", 1000, 4096, "R"),
	})
	var ws, r *FsioBlockEvent
	for i := range out {
		if out[i].Action != "block_rq_complete" {
			continue
		}
		if out[i].RWBS == "WS" {
			ws = &out[i]
		} else {
			r = &out[i]
		}
	}
	if ws == nil || r == nil {
		t.Fatal("complete 행을 못 찾음")
	}
	if !nearly(ws.DtoC, 2000.0) {
		t.Errorf("WS dtoc = %v, want 2000", ws.DtoC)
	}
	if !nearly(r.DtoC, 4000.0) {
		t.Errorf("R dtoc = %v, want 4000", r.DtoC)
	}
}

// 섹터 주소공간은 디바이스마다 독립 — device 를 키에서 빼면 다른 디스크와 짝지어진다.
func TestFsioBlockDtoCPairsSameSectorDifferentDevice(t *testing.T) {
	out := ProcessFsioBlock([]FsioBlockEvent{
		mkBlkDev(1.0, "block_rq_issue", 1000, 4096, "WS", 0),
		mkBlkDev(2.0, "block_rq_issue", 1000, 4096, "WS", 16),
		mkBlkDev(3.0, "block_rq_complete", 1000, 4096, "WS", 0),
		mkBlkDev(6.0, "block_rq_complete", 1000, 4096, "WS", 16),
	})
	var d0, d16 *FsioBlockEvent
	for i := range out {
		if out[i].Action != "block_rq_complete" {
			continue
		}
		if out[i].DevMinor == 0 {
			d0 = &out[i]
		} else {
			d16 = &out[i]
		}
	}
	if d0 == nil || d16 == nil {
		t.Fatal("complete 행을 못 찾음")
	}
	if !nearly(d0.DtoC, 2000.0) {
		t.Errorf("dev 8:0 dtoc = %v, want 2000", d0.DtoC)
	}
	if !nearly(d16.DtoC, 4000.0) {
		t.Errorf("dev 8:16 dtoc = %v, want 4000", d16.DtoC)
	}
}

func TestFsioBlockNotContinuousAcrossDevice(t *testing.T) {
	out := ProcessFsioBlock([]FsioBlockEvent{
		mkBlkDev(1.0, "block_rq_issue", 1000, 4096, "WS", 0),
		// 섹터는 이어지지만 디바이스가 다르다 → 연속 아님
		mkBlkDev(2.0, "block_rq_issue", 1008, 4096, "WS", 16),
	})
	if out[1].Continuous {
		t.Error("다른 디바이스를 연속으로 판정했다")
	}
}

func TestFsioBlockQDTransition0To1ComputesCtoD(t *testing.T) {
	out := ProcessFsioBlock([]FsioBlockEvent{
		mkBlk(1.0, "block_rq_issue", 1000, 4096, "WS"),
		mkBlk(2.0, "block_rq_complete", 1000, 4096, "WS"), // qd → 0
		mkBlk(3.0, "block_rq_issue", 2000, 4096, "WS"),    // qd 0→1 → ctod
	})
	if e := out[2]; !nearly(e.CtoD, 1000.0) {
		t.Errorf("ctod = %v, want 1000", e.CtoD)
	}
}

// block 은 tag 같은 재사용 신호가 없어 시간 만료로만 닫는다.
func TestFsioBlockMissingCompleteClosedByTimeout(t *testing.T) {
	out := ProcessFsioBlock([]FsioBlockEvent{
		mkBlk(1.0, "block_rq_issue", 1000, 4096, "WS"), // complete 없음
		mkBlk(2.0, "block_rq_issue", 2000, 4096, "WS"),
		mkBlk(2.5, "block_rq_complete", 2000, 4096, "WS"),
		// 임계(5초) 를 넘긴 시점의 issue → sweep 이 첫 건을 걷어간다.
		mkBlk(20.0, "block_rq_issue", 3000, 4096, "WS"),
		mkBlk(20.5, "block_rq_complete", 3000, 4096, "WS"),
	})
	if last := out[len(out)-1]; last.QD != 0 {
		t.Errorf("시간 만료로 qd 가 회복돼야 한다: qd=%d", last.QD)
	}
	// 미완결 issue 는 표시되고 dtoc 는 0 으로 남는다 ('모름' 이지 0ms 가 아니다).
	var orphan *FsioBlockEvent
	for i := range out {
		if out[i].Time == 1.0 {
			orphan = &out[i]
		}
	}
	if orphan == nil || !orphan.IsUnfinished {
		t.Error("미완결 표시가 없다")
	}
	if orphan != nil && orphan.DtoC != 0 {
		t.Errorf("미완결 dtoc = %v, want 0", orphan.DtoC)
	}
}

func TestFsioBlockNegativeTimeGuard(t *testing.T) {
	out := ProcessFsioBlock([]FsioBlockEvent{
		mkBlk(2.0, "block_rq_issue", 1000, 4096, "WS"),
		mkBlk(2.0, "block_rq_complete", 1000, 4096, "WS"),
	})
	for _, e := range out {
		if e.DtoC < 0 || e.CtoC < 0 || e.CtoD < 0 {
			t.Errorf("음수 latency: dtoc=%v ctoc=%v ctod=%v", e.DtoC, e.CtoC, e.CtoD)
		}
	}
}
