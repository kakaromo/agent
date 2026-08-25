package parser

import "testing"

// 픽스처는 `../bpftrace/docs/OUTPUT_FORMAT.md` §3·§4 의 예시 라인과
// Rust `../trace/src/parsers/bpftrace_tsv.rs` 의 테스트에서 그대로 가져왔다.
// 두 구현이 같은 입력을 보게 하는 것이 목적이라 임의로 바꾸지 말 것.

const (
	blkIssueLine = "12345.678920\tBLK\t4521\t4521\t3\tmysqld\tvfs_write\tblock_rq_issue\text4\t8\t32\t983241\t16384\t8192000\t/data/ibdata1\t0x0000010040002102\trwbs=WS"

	ufsSendLine = "12345.678935\tUFS\t4521\t4521\t3\tmysqld\tvfs_write\tufshcd_command:send_req\text4\t8\t32\t983241\t16384\t1024000\t/data/ibdata1\t0x0000080040002102\tlun=0 tag=7 hwq=0 ufs_op=0x2a grp=0x0 txn=0x01 flags=0x42 func=0x00 attr=Simple cp=0"

	// complete_rsp 는 응답 UPIU 를 stash 하지 않아 txn/flags/func/attr/cp 가 빠진다.
	ufsCompleteLine = "12345.679210\tUFS\t4521\t4521\t1\tmysqld\tvfs_write\tufshcd_command:complete_rsp\text4\t8\t32\t983241\t16384\t1024000\t/data/ibdata1\t0x0000080040002102\tlun=0 tag=7 hwq=0 ufs_op=0x2a grp=0x0"
)

func TestParseFsioBlockLine(t *testing.T) {
	ev, ok := parseFsioBlockLine(blkIssueLine)
	if !ok {
		t.Fatal("BLK 행 파싱 실패")
	}
	if ev.Time != 12345.678920 {
		t.Errorf("time = %v", ev.Time)
	}
	if ev.Action != "block_rq_issue" {
		t.Errorf("action = %q", ev.Action)
	}
	if ev.DevMajor != 8 || ev.DevMinor != 32 {
		t.Errorf("dev = %d:%d", ev.DevMajor, ev.DevMinor)
	}
	if ev.Sector != 8192000 || ev.Size != 16384 {
		t.Errorf("sector=%d size=%d", ev.Sector, ev.Size)
	}
	if ev.RWBS != "WS" {
		t.Errorf("rwbs = %q", ev.RWBS)
	}
	// io_type 은 파서 정책상 항상 빈 값 — 분류는 rwbs/io_flags 로 한다.
	if ev.IOType != "" {
		t.Errorf("io_type 은 비어 있어야 한다: %q", ev.IOType)
	}
	// cross-layer 메타가 살아 있어야 bpftrace 를 쓰는 의미가 있다.
	if ev.PID != 4521 || ev.Comm != "mysqld" || ev.Syscall != "vfs_write" {
		t.Errorf("cross-layer: pid=%d comm=%q syscall=%q", ev.PID, ev.Comm, ev.Syscall)
	}
	if ev.FS != "ext4" || ev.Ino != 983241 || ev.Name != "/data/ibdata1" {
		t.Errorf("fs=%q ino=%d name=%q", ev.FS, ev.Ino, ev.Name)
	}
	if ev.IOFlags != 0x0000010040002102 {
		t.Errorf("io_flags = %#x", ev.IOFlags)
	}
	// 0x0000010040002102 = WRITE(0x2) | O_SYNC(0x100) | REQ_SYNC(0x2000) | SAW_VFS(bit40)
	if !ev.IsWrite || !ev.IsOSync || !ev.IsReqSync || !ev.IsSawVfs {
		t.Errorf("켜져야 할 비트가 꺼졌다: io_flags=%#x", ev.IOFlags)
	}
	// 이 값에 없는 비트는 꺼져 있어야 한다 — 마스크가 밀리면 여기서 잡힌다.
	if ev.IsRead || ev.IsData || ev.IsJournal || ev.IsGC || ev.IsWritebackKworker {
		t.Errorf("꺼져야 할 비트가 켜졌다: io_flags=%#x", ev.IOFlags)
	}
}

func TestParseFsioUfsSendAndComplete(t *testing.T) {
	send, ok := parseFsioUfsLine(ufsSendLine)
	if !ok {
		t.Fatal("send_req 파싱 실패")
	}
	// action 은 `ufshcd_command:` prefix 가 벗겨진다.
	if send.Action != "send_req" {
		t.Errorf("action = %q", send.Action)
	}
	if send.IsMgmt {
		t.Error("데이터 IO 는 IsMgmt=false 여야 한다")
	}
	if send.LUN != 0 || send.Tag != 7 || send.Opcode != 0x2a {
		t.Errorf("lun=%d tag=%d opcode=%#x", send.LUN, send.Tag, send.Opcode)
	}
	if send.LBA != 1024000 || send.Size != 16384 {
		t.Errorf("lba=%d size=%d", send.LBA, send.Size)
	}
	// send_req 에만 요청 UPIU 헤더가 붙는다.
	if send.Txn == nil || *send.Txn != 0x01 {
		t.Errorf("txn = %v", send.Txn)
	}
	if send.UpiuFlags == nil || *send.UpiuFlags != 0x42 {
		t.Errorf("upiu_flags = %v", send.UpiuFlags)
	}
	if send.UpiuAttr != "Simple" {
		t.Errorf("upiu_attr = %q", send.UpiuAttr)
	}
	if send.UpiuCp == nil || *send.UpiuCp != 0 {
		t.Errorf("upiu_cp = %v", send.UpiuCp)
	}

	comp, ok := parseFsioUfsLine(ufsCompleteLine)
	if !ok {
		t.Fatal("complete_rsp 파싱 실패")
	}
	if comp.Action != "complete_rsp" {
		t.Errorf("action = %q", comp.Action)
	}
	// 응답 UPIU 는 stash 하지 않으므로 nil 이어야 한다 — 0 이 아니다.
	if comp.Txn != nil || comp.UpiuFlags != nil || comp.UpiuFunc != nil || comp.UpiuCp != nil {
		t.Errorf("complete_rsp 는 UPIU 헤더가 없어야 한다: txn=%v flags=%v",
			comp.Txn, comp.UpiuFlags)
	}
}

// `lun=?` 를 0 으로 폴백하면 실제 LU0 과 섞인다. 미상은 미상으로 남아야 한다.
func TestParseFsioUfsUnknownLun(t *testing.T) {
	line := "12399.123456\tUFS\t0\t0\t0\tswapper/0\t-\tufshcd_command:send_req\t\t8\t0\t0\t4096\t2048000\t\t0x0000080000000001\tlun=? tag=12 hwq=0 ufs_op=0x28 grp=0x0"
	ev, ok := parseFsioUfsLine(line)
	if !ok {
		t.Fatal("파싱 실패")
	}
	if ev.LUN != LunUnknown {
		t.Errorf("lun = %d, want LunUnknown(0xff)", ev.LUN)
	}
	// UFS_TAG_CTX miss 라 cross-layer 정보가 비는 것은 정상이다.
	if ev.Name != "" || ev.Ino != 0 {
		t.Errorf("miss 행은 cross-layer 가 비어야 한다: name=%q ino=%d", ev.Name, ev.Ino)
	}
}

// `-x`(--decode) 는 18번째 컬럼을 덧붙인다. 앞 17컬럼만 읽으면 무해해야 한다.
func TestParseFsioTolerates18thColumn(t *testing.T) {
	line := blkIssueLine + "\t[WRITE|O_SYNC|DATA]"
	ev, ok := parseFsioBlockLine(line)
	if !ok {
		t.Fatal("18컬럼 행 파싱 실패")
	}
	if ev.RWBS != "WS" {
		t.Errorf("rwbs = %q — 18번째 컬럼이 extra 를 침범했다", ev.RWBS)
	}
}

// 16컬럼 이하는 거절해야 한다.
func TestParseFsioRejectsShortLine(t *testing.T) {
	short := "12345.678920\tBLK\t4521\t4521\t3\tmysqld"
	if _, ok := parseFsioBlockLine(short); ok {
		t.Error("컬럼이 모자란 행을 받아들였다")
	}
}

// VFS / FS row 는 버린다 (BLK/UFS 만 대상).
func TestParseFsioRejectsVfsAndFsLayers(t *testing.T) {
	vfs := "12345.678900\tVFS\t4521\t4521\t3\tmysqld\tvfs_write\tvfs_write\text4\t8\t32\t983241\t16384\t0\t/data/ibdata1\t0x0000010040002102\t"
	if _, ok := parseFsioBlockLine(vfs); ok {
		t.Error("VFS 행이 BLK 로 파싱됐다")
	}
	if _, ok := parseFsioUfsLine(vfs); ok {
		t.Error("VFS 행이 UFS 로 파싱됐다")
	}
}

// mgmt 행 — Query UPIU. mgmt_name 이 파싱 시점에 구워져야 한다.
func TestParseFsioUfsMgmtQuery(t *testing.T) {
	line := "1000.000500\tUFS\t0\t0\t2\tkworker/2:1H\t-\tufshcd_upiu:query_req\t\t0\t0\t0\t0\t0\t\t0x0000000000000000\ttxn=0x16 lun=0 tag=7 flags=0x00 func=0x00 resp=0x00 status=0x00 dir=send qop=0x01 idn=0x07 qidx=0 qsel=0"
	ev, ok := parseFsioUfsLine(line)
	if !ok {
		t.Fatal("query_req 파싱 실패")
	}
	if !ev.IsMgmt {
		t.Error("IsMgmt 가 켜져야 한다")
	}
	if ev.Action != "upiu_query_req" {
		t.Errorf("action = %q — upiu_ 접두어가 붙어야 데이터 IO 와 안 섞인다", ev.Action)
	}
	// qop=0x01(Read Descriptor) + idn=0x07(geometry)
	if ev.MgmtName != "Read Descriptor(geometry)" {
		t.Errorf("mgmt_name = %q", ev.MgmtName)
	}
	if ev.QueryOpcode == nil || *ev.QueryOpcode != 0x01 {
		t.Errorf("query_opcode = %v", ev.QueryOpcode)
	}
	if ev.QueryIdn == nil || *ev.QueryIdn != 0x07 {
		t.Errorf("query_idn = %v", ev.QueryIdn)
	}
}

// mgmt 행 — UIC. dir 로 send/complete 를 가른다.
func TestParseFsioUfsMgmtUIC(t *testing.T) {
	send := "1000.010000\tUFS\t0\t0\t0\tswapper/0\t-\tufshcd_uic_command\t\t0\t0\t0\t0\t0\t\t0x0\tuic_cmd=0x17 a1=0x0 a2=0x0 a3=0x0 dir=send"
	comp := "1000.012950\tUFS\t0\t0\t0\tswapper/0\t-\tufshcd_uic_command\t\t0\t0\t0\t0\t0\t\t0x0\tuic_cmd=0x17 a1=0x0 a2=0x0 a3=0x0 dir=comp"

	s, ok := parseFsioUfsLine(send)
	if !ok {
		t.Fatal("uic send 파싱 실패")
	}
	if s.Action != "uic_send" {
		t.Errorf("action = %q", s.Action)
	}
	if s.MgmtName != "DME_HIBER_ENTER" {
		t.Errorf("mgmt_name = %q", s.MgmtName)
	}

	c, ok := parseFsioUfsLine(comp)
	if !ok {
		t.Fatal("uic comp 파싱 실패")
	}
	if c.Action != "uic_complete" {
		t.Errorf("action = %q", c.Action)
	}

	// dir 이 없는 구 producer 는 "uic" 로 남아 페어링에서 빠진다.
	noDir := "1000.010000\tUFS\t0\t0\t0\tswapper/0\t-\tufshcd_uic_command\t\t0\t0\t0\t0\t0\t\t0x0\tuic_cmd=0x17"
	n, ok := parseFsioUfsLine(noDir)
	if !ok {
		t.Fatal("dir 없는 uic 파싱 실패")
	}
	if n.Action != "uic" {
		t.Errorf("dir 없으면 action=%q 여야 페어링을 건너뛴다", n.Action)
	}
}

// producer 가 값 뒤에 이름을 괄호로 붙여도 hex 가 조용히 0 이 되면 안 된다.
func TestHexPrefixStopsAtNonHex(t *testing.T) {
	if got := parseHexU8("0x05(read_flag)"); got != 0x05 {
		t.Errorf("괄호 이름이 붙은 hex 파싱 실패: %#x", got)
	}
	if got := parseHexU32("0x17(DME_HIBER_ENTER)"); got != 0x17 {
		t.Errorf("uic_cmd 파싱 실패: %#x", got)
	}
	if got := parseHexU8("0x2a"); got != 0x2a {
		t.Errorf("평범한 hex 파싱 실패: %#x", got)
	}
}

// grp 은 커널 __u16 — u8 로 받으면 0x100 이상이 잘린다.
func TestParseFsioUfsGroupIDIs16Bit(t *testing.T) {
	line := "1000.0\tUFS\t1\t1\t0\tx\t-\tufshcd_command:send_req\t\t0\t0\t0\t4096\t100\t\t0x0\tlun=0 tag=1 ufs_op=0x2a grp=0x123"
	ev, ok := parseFsioUfsLine(line)
	if !ok {
		t.Fatal("파싱 실패")
	}
	if ev.GroupID != 0x123 {
		t.Errorf("groupid = %#x, want 0x123 (u8 로 받으면 0x23 으로 잘린다)", ev.GroupID)
	}
}

func TestQuickFsioCheck(t *testing.T) {
	if !quickFsioCheck(blkIssueLine) {
		t.Error("BLK 행을 걸렀다")
	}
	if !quickFsioCheck(ufsSendLine) {
		t.Error("UFS 행을 걸렀다")
	}
	// ftrace UFS 라인은 fsio 가 아니다 — 두 포맷이 섞이면 안 된다.
	ftrace := "  kworker/0:1H-123   [000] ....  1234.567890: ufshcd_command: send_req: ..."
	if quickFsioCheck(ftrace) {
		t.Error("ftrace 라인을 fsio 로 오인했다")
	}
	if quickFsioCheck("") {
		t.Error("빈 줄을 받아들였다")
	}
}
