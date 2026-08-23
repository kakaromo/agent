package trace

import (
	"os"
	"path/filepath"
	"testing"

	"agent/trace/parser"
)

// bpftrace TSV → parquet → 통계까지의 통합 회귀 테스트.
//
// 여기서 고정하는 것은 fsio 스키마가 기존 ftrace 경로와 다른 **세 지점**이다.
// 셋 다 조용히 틀리는 종류라 (에러가 아니라 그럴듯한 숫자가 나온다) 테스트가 없으면
// 한참 뒤에야 발견된다.

// fsioTestLog — 데이터 IO 1쌍 + mgmt 4행 + 미완결 send 1행.
var fsioTestLog = []string{
	// send/complete 한 쌍 — WRITE_10(0x2a), 16384 bytes
	"12345.678935\tUFS\t4521\t4521\t3\tmysqld\tvfs_write\tufshcd_command:send_req\text4\t8\t32\t983241\t16384\t1024000\t/data/ibdata1\t0x0000080040002102\tlun=0 tag=7 hwq=0 ufs_op=0x2a grp=0x0 txn=0x01 flags=0x42 func=0x00 attr=Simple cp=0",
	"12345.679210\tUFS\t0\t0\t1\tswapper/0\t-\tufshcd_command:complete_rsp\t\t0\t0\t0\t16384\t1024000\t\t0x0000080040002102\tlun=0 tag=7 hwq=0 ufs_op=0x2a grp=0x0",
	// mgmt — Query UPIU 왕복
	"12346.000500\tUFS\t0\t0\t2\tkworker/2:1H\t-\tufshcd_upiu:query_req\t\t0\t0\t0\t0\t0\t\t0x0\ttxn=0x16 lun=0 tag=7 flags=0x00 func=0x00 resp=0x00 status=0x00 dir=send qop=0x01 idn=0x07 qidx=0 qsel=0",
	"12346.008200\tUFS\t0\t0\t2\tkworker/2:1H\t-\tufshcd_upiu:query_rsp\t\t0\t0\t0\t0\t0\t\t0x0\ttxn=0x26 lun=0 tag=7 flags=0x00 func=0x00 resp=0x00 status=0x00 dir=comp qop=0x01 idn=0x07 qidx=0 qsel=0",
	// mgmt — UIC hibern8
	"12346.010000\tUFS\t0\t0\t0\tswapper/0\t-\tufshcd_uic_command\t\t0\t0\t0\t0\t0\t\t0x0\tuic_cmd=0x17 a1=0x0 a2=0x0 a3=0x0 dir=send",
	"12346.012950\tUFS\t0\t0\t0\tswapper/0\t-\tufshcd_uic_command\t\t0\t0\t0\t0\t0\t\t0x0\tuic_cmd=0x17 a1=0x0 a2=0x0 a3=0x0 dir=comp",
	// VFS 행 — 버려져야 한다
	"12347.100000\tVFS\t4521\t4521\t3\tmysqld\tvfs_write\tvfs_write\text4\t8\t32\t983241\t16384\t0\t/data/ibdata1\t0x0000010040002102\t",
	// complete 없는 send — READ_10(0x28), 4096 bytes
	"12350.000000\tUFS\t4521\t4521\t3\tmysqld\tvfs_read\tufshcd_command:send_req\text4\t8\t32\t555\t4096\t2048000\t/data/x.db\t0x0000080000000101\tlun=1 tag=12 hwq=0 ufs_op=0x28 grp=0x0",
}

func writeFsioParquet(t *testing.T, traceType string) string {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "trace.log")
	data := ""
	for _, l := range fsioTestLog {
		data += l + "\n"
	}
	if err := os.WriteFile(logFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(logFile, dir, traceType, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFsioStatsExcludesMgmtAndKeepsByteUnits(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")

	stats, err := ComputeStats([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// ① mgmt 행 제외 — parquet 에는 7행(데이터 3 + mgmt 4)이 있지만 통계 모수는 3이다.
	// idle 구간에서는 mgmt 가 행의 대부분이라 이게 안 되면 분모가 통째로 흔들린다.
	if stats.TotalEvents != 3 {
		t.Errorf("total_events = %d, want 3 (mgmt 4행이 섞였다)", stats.TotalEvents)
	}

	// ② cmd 축 정규화 — fsio 의 opcode 는 UInt8 숫자라 그대로 두면 "42"/"40" 이 된다.
	// hex 문자열이어야 read/write 분류기가 맞는다.
	got := map[string]int64{}
	for _, c := range stats.CmdStats {
		got[c.Cmd] = c.Count
	}
	if got["0x2a"] != 2 {
		t.Errorf("cmd 0x2a count = %d, want 2 — cmd 축이 hex 로 정규화되지 않았다 (got %v)",
			got["0x2a"], got)
	}
	if got["0x28"] != 1 {
		t.Errorf("cmd 0x28 count = %d, want 1 (got %v)", got["0x28"], got)
	}

	// ③ size 단위 — bpftrace 는 이미 bytes 로 준다. ftrace UFS 의 ×4096 을 태우면
	// 4096배 부풀어 오른다. 로그의 send_req size 합계(16384 + 4096)와 정확히 같아야 한다.
	if stats.WriteTotalBytes != 16384 {
		t.Errorf("write_total_bytes = %d, want 16384 (×4096 이 잘못 적용됐다)",
			stats.WriteTotalBytes)
	}
	if stats.ReadTotalBytes != 4096 {
		t.Errorf("read_total_bytes = %d, want 4096", stats.ReadTotalBytes)
	}
}

func TestFsioBlockStatsUsesRwbsAsCmdAxis(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "trace.log")
	// BLK 한 쌍 — rwbs=WS, 16384 bytes
	data := "12345.678920\tBLK\t4521\t4521\t3\tmysqld\tvfs_write\tblock_rq_issue\text4\t8\t32\t983241\t16384\t8192000\t/data/ibdata1\t0x0000010040002102\trwbs=WS\n" +
		"12345.679230\tBLK\t4521\t4521\t1\tmysqld\tvfs_write\tblock_rq_complete\text4\t8\t32\t983241\t16384\t8192000\t/data/ibdata1\t0x0000010040002102\trwbs=WS\n"
	if err := os.WriteFile(logFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(logFile, dir, "fsio_block", nil); err != nil {
		t.Fatal(err)
	}

	stats, err := ComputeStats([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_block"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// io_type 은 파서 정책상 항상 빈 값이라 cmd 축으로 쓸 수 없다 — rwbs 를 써야 한다.
	// 안 그러면 cmd 가 통째로 "" 가 되고 read/write 분류가 전부 default 로 샌다.
	if len(stats.CmdStats) == 0 || stats.CmdStats[0].Cmd != "WS" {
		t.Fatalf("cmd 축이 rwbs 가 아니다: %+v", stats.CmdStats)
	}
	// rwbs 는 조합값("WS")이라 첫 글자로 분류해야 write 로 잡힌다. 그리고 계수는 1.
	if stats.WriteTotalBytes != 16384 {
		t.Errorf("write_total_bytes = %d, want 16384", stats.WriteTotalBytes)
	}
}

// 파일명 substring 매칭이라 순서가 중요하다 — fsio_ufs 가 ufs 로 오분류되면
// reparse 가 ftrace 파서를 태워 아무것도 안 나온다.
func TestDetectTraceTypeFromFilenameFsioFirst(t *testing.T) {
	cases := map[string]string{
		"result_fsio_ufs.parquet":   "fsio_ufs",
		"result_fsio_block.parquet": "fsio_block",
		"result_ufs.parquet":        "ufs",
		"result_block.parquet":      "block",
		"result_ufscustom.parquet":  "ufscustom",
	}
	for name, want := range cases {
		if got := detectTraceTypeFromFilename(name); got != want {
			t.Errorf("%s → %s, want %s", name, got, want)
		}
	}
}

func TestDetectTraceTypeFromDir(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")
	if got := detectTraceTypeFromDir(dir); got != "fsio_ufs" {
		t.Errorf("fsio 디렉토리 판정 = %q, want fsio_ufs", got)
	}
	// 산출물이 없으면 기존 동작(ftrace "both")으로 폴백한다.
	if got := detectTraceTypeFromDir(t.TempDir()); got != "both" {
		t.Errorf("빈 디렉토리 판정 = %q, want both", got)
	}
}
