package parser

import (
	"math"
	"testing"
)

// Rust `../trace/src/processors/fsio_ufs.rs` 의 회귀 테스트 포팅.
// 각 테스트는 조용히 틀리는 종류의 버그를 고정한다.

func mkUfs(time float64, action string, tag uint32, lba uint64, size uint32, opcode uint8) FsioUfsEvent {
	return FsioUfsEvent{
		Time:    time,
		Process: "test",
		Action:  action,
		Tag:     tag,
		Opcode:  opcode,
		LUN:     0,
		LBA:     lba,
		Size:    size,
		Aligned: true,
		PID:     1,
		TID:     1,
		Comm:    "test",
		Syscall: "vfs_write",
		FS:      "ext4",
	}
}

func mkUfsLun(time float64, action string, tag uint32, lba uint64, size uint32, opcode, lun uint8) FsioUfsEvent {
	e := mkUfs(time, action, tag, lba, size, opcode)
	e.LUN = lun
	return e
}

func mkMgmt(time float64, action string, lun uint8, tag uint32) FsioUfsEvent {
	e := mkUfs(time, action, tag, 0, 0, 0)
	e.LUN = lun
	e.IsMgmt = true
	return e
}

func findAction(t *testing.T, list []FsioUfsEvent, action string) *FsioUfsEvent {
	t.Helper()
	for i := range list {
		if list[i].Action == action {
			return &list[i]
		}
	}
	t.Fatalf("action %q 를 못 찾음", action)
	return nil
}

func findActionLun(t *testing.T, list []FsioUfsEvent, action string, lun uint8) *FsioUfsEvent {
	t.Helper()
	for i := range list {
		if list[i].Action == action && list[i].LUN == lun {
			return &list[i]
		}
	}
	t.Fatalf("action %q lun %d 를 못 찾음", action, lun)
	return nil
}

func findTime(t *testing.T, list []FsioUfsEvent, time float64) *FsioUfsEvent {
	t.Helper()
	for i := range list {
		if list[i].Time == time {
			return &list[i]
		}
	}
	t.Fatalf("time %v 를 못 찾음", time)
	return nil
}

func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestFsioUFSDtoCBasic(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkUfs(1.0, "send_req", 7, 100, 4096, 0x2a),
		mkUfs(2.0, "complete_rsp", 7, 100, 4096, 0x2a),
	})
	c := findAction(t, out, "complete_rsp")
	if !nearly(c.DtoC, 1000.0) {
		t.Errorf("dtoc = %v, want 1000", c.DtoC)
	}
}

// mgmt 행은 데이터 IO 의 큐 상태 기계에 일절 관여하면 안 된다.
// 건드리면 mgmt 행 자신이 아니라 **앞뒤 데이터 IO 행의** qd/ctoc/ctod 가 틀어진다.
func TestFsioUFSMgmtRowsDoNotDisturbDataIOMetrics(t *testing.T) {
	dataIO := func() []FsioUfsEvent {
		return []FsioUfsEvent{
			mkUfs(1.0, "send_req", 1, 100, 4096, 0x2a),
			mkUfs(1.5, "send_req", 2, 101, 4096, 0x2a), // continuous 후보
			mkUfs(2.0, "complete_rsp", 1, 100, 4096, 0x2a),
			mkUfs(2.5, "complete_rsp", 2, 101, 4096, 0x2a),
			mkUfs(3.0, "send_req", 3, 500, 4096, 0x2a), // qd 0→1 → ctod
			mkUfs(3.5, "complete_rsp", 3, 500, 4096, 0x2a),
		}
	}

	baseline := ProcessFsioUFS(dataIO())

	// 같은 데이터 IO 사이사이에 mgmt 행을 촘촘히 끼워넣는다.
	mixed := append(dataIO(),
		mkMgmt(1.2, "upiu_query_req", 0, 7),
		mkMgmt(1.8, "upiu_query_rsp", 0, 7),
		mkMgmt(2.2, "uic_send", 0, 0),
		mkMgmt(2.7, "uic_complete", 0, 0),
		mkMgmt(3.2, "upiu_tm_req", 1, 4),
	)
	out := ProcessFsioUFS(mixed)

	var dataRows []FsioUfsEvent
	for _, e := range out {
		if !e.IsMgmt {
			dataRows = append(dataRows, e)
		}
	}
	if len(dataRows) != len(baseline) {
		t.Fatalf("데이터 행 수 = %d, want %d", len(dataRows), len(baseline))
	}
	for i, want := range baseline {
		got := dataRows[i]
		if got.Time != want.Time {
			t.Fatalf("정렬이 틀어졌다: %v vs %v", got.Time, want.Time)
		}
		if got.QD != want.QD {
			t.Errorf("qd 오염 (time=%v): %d vs %d", want.Time, got.QD, want.QD)
		}
		if got.DtoC != want.DtoC {
			t.Errorf("dtoc 오염 (time=%v): %v vs %v", want.Time, got.DtoC, want.DtoC)
		}
		if got.CtoC != want.CtoC {
			t.Errorf("ctoc 오염 (time=%v): %v vs %v", want.Time, got.CtoC, want.CtoC)
		}
		if got.CtoD != want.CtoD {
			t.Errorf("ctod 오염 (time=%v): %v vs %v", want.Time, got.CtoD, want.CtoD)
		}
		if got.Continuous != want.Continuous {
			t.Errorf("continuous 오염 (time=%v)", want.Time)
		}
	}
}

// 보정 안 하면 ① qd 가 영구 우상향 ② ctod 가 영영 계산 안 됨.
func TestFsioUFSMissingCompleteDoesNotDriftQDOrKillCtoD(t *testing.T) {
	// tag 5 의 complete 가 없다. 그 뒤 tag 5 로 새 send 가 오므로 tag 재사용으로 닫힌다.
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkUfs(1.0, "send_req", 5, 100, 4096, 0x2a),
		// complete 누락 (producer 가 놓침)
		mkUfs(2.0, "send_req", 6, 200, 4096, 0x2a),
		mkUfs(2.5, "complete_rsp", 6, 200, 4096, 0x2a),
		// tag 5 재사용 → 앞 건이 미완결로 닫히고 qd 가 회복된다.
		mkUfs(3.0, "send_req", 5, 300, 4096, 0x2a),
		mkUfs(3.5, "complete_rsp", 5, 300, 4096, 0x2a),
		// 여기서 qd 가 0 이어야 ctod 가 되살아난다.
		mkUfs(5.0, "send_req", 7, 400, 4096, 0x2a),
		mkUfs(5.5, "complete_rsp", 7, 400, 4096, 0x2a),
	})

	// 마지막 complete 에서 qd 가 0 으로 돌아와야 한다 (보정 전엔 1 이었다).
	if last := out[len(out)-1]; last.QD != 0 {
		t.Errorf("qd 드리프트 — 미완결이 안 닫혔다: qd=%d", last.QD)
	}

	// ctod 가 살아 있어야 한다. 보정 전엔 tag5 누락 이후 전부 0 이었다.
	if e := findTime(t, out, 5.0); e.CtoD <= 0 {
		t.Errorf("ctod 가 죽었다 — qd 가 0 으로 못 돌아간 것: ctod=%v", e.CtoD)
	}

	// 미완결 send 는 표시되고 dtoc 는 0 으로 남는다(0ms 가 아니라 '모름').
	orphan := findTime(t, out, 1.0)
	if !orphan.IsUnfinished {
		t.Error("미완결 표시가 없다")
	}
	if orphan.DtoC != 0 {
		t.Errorf("미완결 dtoc = %v, want 0 ('모름' 이지 0ms 가 아니다)", orphan.DtoC)
	}

	// 정상 완료된 건은 미완결이 아니다.
	n := 0
	for _, e := range out {
		if e.IsUnfinished {
			n++
		}
	}
	if n != 1 {
		t.Errorf("미완결 = %d 건, want 1", n)
	}
}

// tag 재사용이 안 일어난 채 흘러가는 건은 시간 만료가 걷어간다.
func TestFsioUFSStaleSendClosedByTimeout(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkUfs(1.0, "send_req", 5, 100, 4096, 0x2a), // complete 없음, tag 재사용도 없음
		mkUfs(2.0, "send_req", 6, 200, 4096, 0x2a),
		mkUfs(2.5, "complete_rsp", 6, 200, 4096, 0x2a),
		// 임계(5초) 를 넘긴 시점의 send → sweep 이 tag5 를 걷어간다.
		mkUfs(20.0, "send_req", 7, 300, 4096, 0x2a),
		mkUfs(20.5, "complete_rsp", 7, 300, 4096, 0x2a),
	})
	if last := out[len(out)-1]; last.QD != 0 {
		t.Errorf("시간 만료로 qd 가 회복돼야 한다: qd=%d", last.QD)
	}
	if !findTime(t, out, 1.0).IsUnfinished {
		t.Error("만료된 send 에 미완결 표시가 없다")
	}
}

// LU 가 다르면 같은 tag 가 동시에 in-flight 인 게 정상이다.
func TestFsioUFSDtoCPairsSameTagDifferentLun(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkUfsLun(1.0, "send_req", 7, 100, 4096, 0x2a, 0),
		mkUfsLun(2.0, "send_req", 7, 100, 4096, 0x2a, 1), // 같은 tag/opcode, 다른 LU
		mkUfsLun(3.0, "complete_rsp", 7, 100, 4096, 0x2a, 0),
		mkUfsLun(6.0, "complete_rsp", 7, 100, 4096, 0x2a, 1),
	})
	lun0 := findActionLun(t, out, "complete_rsp", 0)
	lun1 := findActionLun(t, out, "complete_rsp", 1)
	if !nearly(lun0.DtoC, 2000.0) {
		t.Errorf("lun0 dtoc = %v, want 2000", lun0.DtoC)
	}
	if !nearly(lun1.DtoC, 4000.0) {
		t.Errorf("lun1 dtoc = %v, want 4000", lun1.DtoC)
	}
}

// lun 복원 실패(0xff)일 때만 (tag,opcode) 폴백이 돈다.
func TestFsioUFSCompleteWithUnknownLunStillPairs(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkUfsLun(1.0, "send_req", 14, 6659, 4096, 0x28, 1),
		mkUfsLun(2.0, "complete_rsp", 14, 6659, 4096, 0x28, LunUnknown),
	})
	c := findAction(t, out, "complete_rsp")
	if !nearly(c.DtoC, 1000.0) {
		t.Errorf("dtoc = %v (짝 못 찾음)", c.DtoC)
	}
	// complete 행의 lun 은 send 쪽 값을 채택해야 LU 집계가 LU0 으로 쏠리지 않는다.
	if c.LUN != 1 {
		t.Errorf("lun = %d, want 1 (send 의 LU1 로 보정돼야 함)", c.LUN)
	}
}

// 정확 매칭이 폴백보다 우선한다.
func TestFsioUFSExactLunMatchPreferredOverFallback(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkUfsLun(1.0, "send_req", 7, 100, 4096, 0x2a, 0),
		mkUfsLun(2.0, "send_req", 7, 200, 4096, 0x2a, 1),
		mkUfsLun(3.0, "complete_rsp", 7, 200, 4096, 0x2a, 1), // LU1 정확 매칭
		mkUfsLun(6.0, "complete_rsp", 7, 100, 4096, 0x2a, 0), // LU0
	})
	l1 := findActionLun(t, out, "complete_rsp", 1)
	l0 := findActionLun(t, out, "complete_rsp", 0)
	if !nearly(l1.DtoC, 1000.0) {
		t.Errorf("LU1 dtoc = %v, want 1000", l1.DtoC)
	}
	if !nearly(l0.DtoC, 5000.0) {
		t.Errorf("LU0 dtoc = %v, want 5000", l0.DtoC)
	}
}

func TestFsioUFSContinuity(t *testing.T) {
	// LU 마다 LBA 주소공간이 독립이라 lba 가 이어져도 LU 가 다르면 연속이 아니다.
	across := ProcessFsioUFS([]FsioUfsEvent{
		mkUfsLun(1.0, "send_req", 1, 100, 4096, 0x2a, 0),
		mkUfsLun(2.0, "send_req", 2, 101, 4096, 0x2a, 1),
	})
	if across[1].Continuous {
		t.Error("다른 LU 는 연속으로 판정하면 안 됨")
	}

	within := ProcessFsioUFS([]FsioUfsEvent{
		mkUfsLun(1.0, "send_req", 1, 100, 4096, 0x2a, 1),
		mkUfsLun(2.0, "send_req", 2, 101, 4096, 0x2a, 1),
	})
	if !within[1].Continuous {
		t.Error("같은 LU 연속은 유지돼야 함")
	}
}

// mgmt 페어링 — query/tm 은 (kind, lun, tag).
func TestFsioUFSQueryAndTMPairByLunAndTag(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkMgmt(1.0, "upiu_query_req", 0, 5),
		mkMgmt(1.0, "upiu_tm_req", 0, 5), // 같은 lun/tag 지만 종류가 달라 안 섞임
		mkMgmt(2.0, "upiu_query_rsp", 0, 5),
		mkMgmt(3.0, "upiu_tm_rsp", 0, 5),
	})
	if q := findAction(t, out, "upiu_query_rsp"); !nearly(q.DtoC, 1000.0) {
		t.Errorf("query dtoc = %v, want 1000", q.DtoC)
	}
	if tm := findAction(t, out, "upiu_tm_rsp"); !nearly(tm.DtoC, 2000.0) {
		t.Errorf("tm dtoc = %v, want 2000", tm.DtoC)
	}
}

func TestFsioUFSQueryDoesNotPairAcrossLun(t *testing.T) {
	// LU 가 다르면 같은 tag 라도 다른 요청이다.
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkMgmt(1.0, "upiu_query_req", 0, 5),
		mkMgmt(2.0, "upiu_query_rsp", 1, 5),
	})
	if out[1].DtoC != 0 {
		t.Errorf("LU 를 건너뛰어 짝지었다: dtoc = %v", out[1].DtoC)
	}
}

// UIC 는 tag 가 없어 단일 슬롯으로 페어링한다
// (커널이 uic_cmd_mutex 로 호스트당 1개만 outstanding 을 보장).
func TestFsioUFSUICPairsViaSingleSlot(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkMgmt(1.0, "uic_send", 0, 0),
		mkMgmt(1.25, "uic_complete", 0, 0),
	})
	if !nearly(out[1].DtoC, 250.0) {
		t.Errorf("uic dtoc = %v, want 250", out[1].DtoC)
	}
}

func TestFsioUFSUICWithoutDirectionIsNotPaired(t *testing.T) {
	// 구 producer (dir 키 없음) → action "uic". 페어링 불가라 dtoc 0 유지.
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkMgmt(1.0, "uic", 0, 0),
		mkMgmt(2.0, "uic", 0, 0),
	})
	if out[0].DtoC != 0 || out[1].DtoC != 0 {
		t.Errorf("방향 미상 uic 가 짝지어졌다: %v, %v", out[0].DtoC, out[1].DtoC)
	}
}

// complete 는 요청 UPIU 메타가 없다 — 같은 tag 의 send 에서 복사해야 한다.
func TestFsioUFSUpiuMetaPairedSendToComplete(t *testing.T) {
	txn := uint8(0x01)
	flags := uint8(0x42)
	send := mkUfs(1.0, "send_req", 7, 1024, 4096, 0x2a)
	send.Txn = &txn
	send.UpiuFlags = &flags
	send.UpiuAttr = "Simple"

	out := ProcessFsioUFS([]FsioUfsEvent{send, mkUfs(2.0, "complete_rsp", 7, 1024, 4096, 0x2a)})
	c := findAction(t, out, "complete_rsp")
	if c.Txn == nil || *c.Txn != 0x01 {
		t.Errorf("txn 이 복사되지 않았다: %v", c.Txn)
	}
	if c.UpiuFlags == nil || *c.UpiuFlags != 0x42 {
		t.Errorf("upiu_flags 가 복사되지 않았다: %v", c.UpiuFlags)
	}
	if c.UpiuAttr != "Simple" {
		t.Errorf("upiu_attr = %q", c.UpiuAttr)
	}
}

// 짝을 못 찾은 complete 는 건드리지 않는다 — 없는 값을 지어내면 안 된다.
func TestFsioUFSUpiuMetaUnpairedCompleteStaysEmpty(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{mkUfs(2.0, "complete_rsp", 7, 1024, 4096, 0x2a)})
	c := findAction(t, out, "complete_rsp")
	if c.Txn != nil || c.UpiuFlags != nil {
		t.Errorf("짝 없는 complete 에 메타가 생겼다: txn=%v flags=%v", c.Txn, c.UpiuFlags)
	}
}

// complete 의 comm 은 IRQ 문맥의 swapper/kworker — 비어 있는 게 아니라 **틀린 값**이라
// send 값으로 덮어야 한다.
func TestFsioUFSCrossLayerMetaBackfilledSendToComplete(t *testing.T) {
	send := mkUfs(1.0, "send_req", 7, 1024, 4096, 0x2a)
	send.Comm, send.Process = "mysqld", "mysqld"
	send.Name = "/data/ibdata1"
	send.Ino = 983241

	complete := mkUfs(2.0, "complete_rsp", 7, 1024, 4096, 0x2a)
	complete.Comm, complete.Process = "swapper/0", "swapper/0"
	complete.Syscall = "-"
	complete.Name = ""
	complete.Ino = 0
	complete.PID, complete.TID = 0, 0

	out := ProcessFsioUFS([]FsioUfsEvent{send, complete})
	c := findAction(t, out, "complete_rsp")
	if c.Comm != "mysqld" {
		t.Errorf("comm = %q — IRQ 문맥 값이 남았다", c.Comm)
	}
	if c.Name != "/data/ibdata1" {
		t.Errorf("name = %q", c.Name)
	}
	if c.Ino != 983241 {
		t.Errorf("ino = %d", c.Ino)
	}
	if c.PID != 1 || c.TID != 1 {
		t.Errorf("pid/tid = %d/%d", c.PID, c.TID)
	}
	if c.Syscall != "vfs_write" {
		t.Errorf("syscall = %q — `-` 는 빈 값으로 봐야 한다", c.Syscall)
	}
}

// bpftrace 는 파일명을 못 얻으면 빈칸이 아니라 `ino:N` / `(라벨)` 을 채운다.
// 이건 "실제 값" 이 아니라 부재의 표현이라 send 의 진짜 경로로 덮어야 한다.
func TestFsioUFSNamelessPlaceholdersAreOverwrittenByRealPath(t *testing.T) {
	for _, placeholder := range []string{"", "-", "ino:983241", "(flush:barrier)", "(meta:journal)"} {
		send := mkUfs(1.0, "send_req", 7, 1024, 4096, 0x2a)
		send.Name = "/data/ibdata1"
		complete := mkUfs(2.0, "complete_rsp", 7, 1024, 4096, 0x2a)
		complete.Name = placeholder

		out := ProcessFsioUFS([]FsioUfsEvent{send, complete})
		c := findAction(t, out, "complete_rsp")
		if c.Name != "/data/ibdata1" {
			t.Errorf("complete 의 %q 가 send 의 실제 경로로 덮여야 한다: got %q", placeholder, c.Name)
		}
	}
}

// complete 가 진짜 경로를 갖고 있으면 그걸 유지한다.
func TestFsioUFSRealPathOnCompleteIsKept(t *testing.T) {
	send := mkUfs(1.0, "send_req", 7, 1024, 4096, 0x2a)
	send.Name = "/data/send.db"
	complete := mkUfs(2.0, "complete_rsp", 7, 1024, 4096, 0x2a)
	complete.Name = "/data/complete.db"

	out := ProcessFsioUFS([]FsioUfsEvent{send, complete})
	if c := findAction(t, out, "complete_rsp"); c.Name != "/data/complete.db" {
		t.Errorf("name = %q — complete 의 실제 경로를 잃었다", c.Name)
	}
}

// 시간이 거꾸로 가는 입력에서 음수 latency 를 만들지 않는다.
func TestFsioUFSNegativeTimeGuard(t *testing.T) {
	out := ProcessFsioUFS([]FsioUfsEvent{
		mkUfs(2.0, "send_req", 7, 100, 4096, 0x2a),
		mkUfs(2.0, "complete_rsp", 7, 100, 4096, 0x2a),
	})
	for _, e := range out {
		if e.DtoC < 0 || e.CtoC < 0 || e.CtoD < 0 {
			t.Errorf("음수 latency: dtoc=%v ctoc=%v ctod=%v", e.DtoC, e.CtoC, e.CtoD)
		}
	}
}
