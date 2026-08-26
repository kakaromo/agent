package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "agent/pb"
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

// rwbs 는 `[RWDF]` + flag `[FSMA]` 조합이라 **첫 글자**로 갈라야 한다.
// 'F' 가 첫 자리면 FLUSH, 뒤에 오면 FUA 다 — "WF" 를 flush 로 보면 write 바이트를
// 통째로 잃는다. 실기기 수집에서 실제로 WF/WFS/WSMA 가 나온다.
func TestFsioRwbsClassifiedByFirstLetter(t *testing.T) {
	cases := []struct {
		rwbs string
		want string // "write" | "flush" | "discard" | "read"
	}{
		{"W", "write"}, {"WS", "write"}, {"WSM", "write"}, {"WSMA", "write"},
		{"WF", "write"}, {"WFS", "write"}, // F 가 뒤 = FUA, flush 아님
		{"F", "flush"}, {"FS", "flush"}, // F 가 앞 = FLUSH
		{"R", "read"}, {"RS", "read"},
		{"D", "discard"}, {"DS", "discard"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		logFile := filepath.Join(dir, "trace.log")
		// issue + complete 한 쌍, size 4096
		mk := func(ts, action, rwbs string) string {
			return ts + "\tBLK\t1\t1\t0\ttest\tvfs_write\t" + action +
				"\text4\t8\t0\t0\t4096\t1000\t\t0x0\trwbs=" + rwbs + "\n"
		}
		data := mk("1.0", "block_rq_issue", c.rwbs) + mk("2.0", "block_rq_complete", c.rwbs)
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
		var got string
		switch {
		case stats.WriteTotalBytes > 0:
			got = "write"
		case stats.ReadTotalBytes > 0:
			got = "read"
		case stats.DiscardTotalBytes > 0:
			got = "discard"
		default:
			got = "flush" // 합산 대상 아님
		}
		if got != c.want {
			t.Errorf("rwbs=%q → %s, want %s", c.rwbs, got, c.want)
		}
	}
}

// `time` 은 초 단위다. 예전엔 1000 으로 나눠 29초 트레이스가 0.029s 로 나왔다.
func TestFsioDurationIsSeconds(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "trace.log")
	data := "100.000000\tBLK\t1\t1\t0\ttest\tvfs_write\tblock_rq_issue\text4\t8\t0\t0\t4096\t1000\t\t0x0\trwbs=W\n" +
		"130.000000\tBLK\t1\t1\t0\ttest\tvfs_write\tblock_rq_complete\text4\t8\t0\t0\t4096\t1000\t\t0x0\trwbs=W\n"
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
	if stats.DurationSeconds != 30.0 {
		t.Errorf("duration = %v, want 30 (초 단위 그대로 — 1000 으로 나누면 안 된다)",
			stats.DurationSeconds)
	}
}

// TestFsioCmdStatsExcludesUnfinishedFromLatency — CMD 별 latency 모수에서
// dtoc=0 행(send 행 + 미완결 IO)이 빠지는지 고정한다.
//
// **이게 왜 조용히 틀리는가** — 파서는 complete 를 못 받은 send 를 IsUnfinished 로
// 표시하고 dtoc 를 0 으로 둔다. 0 은 "0ms" 가 아니라 **"모름"** 이다
// (trace/parser/fsio_inflight.go 참고). 그런데 집계가 그 0 을 모수에 넣으면
// avg/p99 가 아래로 끌려 내려간다. IO 가 몰릴수록 complete 누락률이 올라가므로
// **부하가 높을수록 latency 를 낮게 보고하는** 방향으로 틀린다.
//
// min 에만 가드가 있으면 min 만 맞고 나머지가 틀려서 표를 봐도 눈치채기 어렵다.
func TestFsioCmdStatsExcludesUnfinishedFromLatency(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")

	stats, err := ComputeStats([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	byCmd := map[string]*pb.CmdStats{}
	for _, c := range stats.CmdStats {
		byCmd[c.Cmd] = c
	}

	// 0x2a — send + complete 2행. dtoc 가 실린 건 complete 1행뿐이고 값은 0.275ms.
	// send 행의 dtoc=0 이 섞이면 avg 가 절반(0.1375)으로 내려간다.
	w := byCmd["0x2a"]
	if w == nil {
		t.Fatalf("cmd 0x2a 가 없다 (got %v)", byCmd)
	}
	const wantDtoc = 0.275
	if diff := w.Dtoc.Avg - wantDtoc; diff < -0.001 || diff > 0.001 {
		t.Errorf("0x2a dtoc avg = %.4f, want ~%.4f — send 행의 dtoc=0 이 모수에 섞였다",
			w.Dtoc.Avg, wantDtoc)
	}
	if diff := w.Dtoc.P99 - wantDtoc; diff < -0.001 || diff > 0.001 {
		t.Errorf("0x2a dtoc p99 = %.4f, want ~%.4f — 백분위 모수에 0 이 섞였다",
			w.Dtoc.P99, wantDtoc)
	}

	// 0x28 — 미완결 send 1행뿐. dtoc 를 아는 행이 **하나도 없으므로** 통계는
	// 0 이 아니라 "값 없음"(0 으로 남김)이어야 한다. 여기서 max/avg 가 0 이 아닌
	// 값을 내면 미완결 IO 를 완료된 것처럼 집계한 것이다.
	r := byCmd["0x28"]
	if r == nil {
		t.Fatalf("cmd 0x28 가 없다 (got %v)", byCmd)
	}
	if r.Dtoc.Max != 0 || r.Dtoc.Avg != 0 {
		t.Errorf("0x28 dtoc max/avg = %.4f/%.4f, want 0/0 — 미완결 IO 가 모수에 들어갔다",
			r.Dtoc.Max, r.Dtoc.Avg)
	}
}

// TestFsioAggregationExcludesMgmt — AI 집계 경로(RunAggregation)가 mgmt 행을
// 모수에서 빼는지 고정한다.
//
// ComputeStats 는 예전부터 mgmt 를 뺐지만 aggregate.go 는 안 뺐다. 이 결과는
// LLM 이 근거로 읽으므로, 오염되면 "0x00 이 N%" 같은 존재하지 않는 패턴을
// 그럴듯하게 해석한다. 에러가 아니라 조용히 틀린 답이라 눈치채기 어렵다.
func TestFsioAggregationExcludesMgmt(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")
	infos := []*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}

	res, err := RunAggregation(infos, AggCmdBreakdown, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	rows, ok := res.Data["commands"].([]map[string]any)
	if !ok {
		t.Fatalf("commands 행을 못 읽었다: %T", res.Data["commands"])
	}
	for _, r := range rows {
		// mgmt 행은 opcode 가 없어 0x00 으로 뭉친다. 데이터 IO 만 남아야 한다.
		if cmd, _ := r["cmd"].(string); cmd == "0x00" {
			t.Errorf("cmd_breakdown 에 mgmt 행(0x00)이 섞였다: %v", r)
		}
	}
	// 로그의 데이터 IO cmd 는 0x2a / 0x28 둘뿐이다.
	if len(rows) != 2 {
		t.Errorf("cmd 행 수 = %d, want 2 (got %v)", len(rows), rows)
	}

	// tail_latency 도 같은 모수를 써야 한다. mgmt 의 dtoc(query 7.7ms, uic 2.95ms)는
	// 데이터 IO(0.275ms)보다 훨씬 커서, 안 빼면 상위를 mgmt 가 독차지한다.
	tail, err := RunAggregation(infos, AggTailLatency, map[string]any{"n": 10})
	if err != nil {
		t.Fatal(err)
	}
	events, ok := tail.Data["events"].([]TailLatencyEvent)
	if !ok {
		t.Fatalf("events 를 못 읽었다: %T", tail.Data["events"])
	}
	for _, e := range events {
		if e.Cmd == "0x00" {
			t.Errorf("tail_latency 상위에 mgmt 행이 섞였다: %+v", e)
		}
	}
}

// TestMgmtExclusionRoundTrips — ExcludeMgmt 가 만든 조건을 stripMgmtExclusion 이
// 정확히 되돌리는지 고정한다.
//
// 둘은 같은 문자열에 의존하는 **쌍**이다. 리터럴이 어긋나면 strip 이 조용히
// no-op 이 되고, mgmt 집계 where 에 `is_mgmt = FALSE` 가 남아 결과가 0행이 된다.
// 그런데 화면에는 에러가 아니라 "mgmt 이벤트가 없었다" 로 보인다 — 조용히 틀린다.
func TestMgmtExclusionRoundTrips(t *testing.T) {
	ufs := fsioCols{schema: fsioSchema{isUFS: true}}

	cases := []struct{ name, in string }{
		{"빈 where", ""},
		{"기존 조건 있음", "WHERE time >= 1.0"},
		{"조건 여러 개", "WHERE time >= 1.0 AND lba > 100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			excluded := ufs.ExcludeMgmt(tc.in)
			if !strings.Contains(excluded, mgmtExclusion) {
				t.Fatalf("ExcludeMgmt(%q) = %q — 제외 조건이 안 붙었다", tc.in, excluded)
			}
			if got := stripMgmtExclusion(excluded); got != tc.in {
				t.Errorf("round-trip 실패: %q → %q → %q, want %q",
					tc.in, excluded, got, tc.in)
			}
		})
	}

	// fsio_block / ftrace 에는 is_mgmt 컬럼이 없다 — 조건을 붙이면 Binder Error.
	blk := fsioCols{schema: fsioSchema{isBlock: true}}
	if got := blk.ExcludeMgmt("WHERE time >= 1.0"); got != "WHERE time >= 1.0" {
		t.Errorf("fsio_block 에 mgmt 조건이 붙었다: %q", got)
	}
}

// TestRawDataNamesMgmtAndNullsNumerics — Raw Data 경로가 mgmt 행을
// (1) 이름으로 구분되게, (2) lba/size/qd 는 0 으로 남기는지 고정한다.
//
// 보정 전에는 mgmt 4행이 전부 cmd=`0x00` 이었다. SCSI 로는 TEST UNIT READY 라
// 클라이언트 분류기가 실제 IO 명령으로 오인하기까지 했다. Query 인지 hibern8
// 인지 Abort Task 인지가 Raw Data 에서 구분되지 않으면 "hibern8 도는 동안 IO 가
// 멈췄다" 를 볼 수가 없다 — mgmt 를 같은 타임라인에 남기는 이유가 사라진다.
func TestRawDataNamesMgmtAndNullsNumerics(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")

	resp, err := GetRawData([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Raw Data 는 mgmt 를 **일부러 남긴다** (통계와 반대). 7행 전부 나와야 한다.
	if resp.TotalEvents != 7 {
		t.Errorf("total_events = %d, want 7 — Raw Data 에서 mgmt 가 빠졌다", resp.TotalEvents)
	}

	var mgmtRows int
	for _, e := range resp.Events {
		isMgmt := strings.HasPrefix(e.Action, "upiu_") ||
			strings.HasPrefix(e.Action, "uic") || e.Action == "exception"
		if !isMgmt {
			continue
		}
		mgmtRows++
		if e.Cmd == "0x00" {
			t.Errorf("mgmt 행이 여전히 0x00 이다: action=%s", e.Action)
		}
		if e.Cmd == "" {
			t.Errorf("mgmt 행의 cmd 가 비었다: action=%s", e.Action)
		}
		// lba/size/qd 는 mgmt 에 의미가 없다 — NULL 로 와서 0 이어야 한다.
		if e.Lba != 0 || e.Size != 0 || e.Qd != 0 {
			t.Errorf("mgmt 행에 수치가 남았다: action=%s lba=%d size=%d qd=%d",
				e.Action, e.Lba, e.Size, e.Qd)
		}
	}
	if mgmtRows != 4 {
		t.Fatalf("mgmt 행 수 = %d, want 4", mgmtRows)
	}

	// 이름이 실제로 구분되는지 — Query 와 UIC 가 서로 다른 이름이어야 한다.
	names := map[string]bool{}
	for _, e := range resp.Events {
		if strings.HasPrefix(e.Action, "upiu_") || strings.HasPrefix(e.Action, "uic") {
			names[e.Cmd] = true
		}
	}
	if len(names) < 2 {
		t.Errorf("mgmt 이름이 구분되지 않는다: %v", names)
	}

	// 데이터 IO 는 그대로 hex 여야 한다 (mgmt 이름이 새어 들어오면 안 된다).
	for _, e := range resp.Events {
		if e.Action == "send_req" && !strings.HasPrefix(e.Cmd, "0x") {
			t.Errorf("데이터 IO 의 cmd 가 hex 가 아니다: %q", e.Cmd)
		}
	}
}

// TestRawDataCarriesMgmtDetail — mgmt 원본값과 미완결 플래그가 Raw Data 까지
// 실려 오는지 고정한다.
//
// cmd 에 mgmt_name 이 들어가도 "Query 가 **어느 IDN 을** 읽었나" 와 "TM 이
// 성공했나(resp/status)" 는 이름만으로 알 수 없다. 이 값들이 parquet 에만
// 있고 wire 로 안 나오면 행 단위 확인이 불가능하다.
func TestRawDataCarriesMgmtDetail(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")

	resp, err := GetRawData([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var sawQuery, sawUic, sawUnfinished bool
	for _, e := range resp.Events {
		switch {
		case strings.HasPrefix(e.Action, "upiu_query"):
			sawQuery = true
			if !e.IsMgmt {
				t.Errorf("query 행의 is_mgmt 가 false 다: %s", e.Action)
			}
			if e.MgmtName == "" {
				t.Errorf("query 행의 mgmt_name 이 비었다: %s", e.Action)
			}
			// 로그의 query 행은 qop=0x01(Read Descriptor), idn=0x07(Geometry).
			if e.QueryOpcode == nil || *e.QueryOpcode != 0x01 {
				t.Errorf("query_opcode = %v, want 0x01 (%s)", e.QueryOpcode, e.Action)
			}
			if e.QueryIdn == nil || *e.QueryIdn != 0x07 {
				t.Errorf("query_idn = %v, want 0x07 (%s)", e.QueryIdn, e.Action)
			}
		case strings.HasPrefix(e.Action, "uic"):
			sawUic = true
			// uic_cmd=0x17 = DME_HIBERNATE_EXIT.
			if e.UicCmd == nil || *e.UicCmd != 0x17 {
				t.Errorf("uic_cmd = %v, want 0x17 (%s)", e.UicCmd, e.Action)
			}
		}
		if e.IsUnfinished {
			sawUnfinished = true
			// 미완결 행의 dtoc 는 0 이어야 한다 — 지연을 지어내지 않는다.
			if e.Dtoc != 0 {
				t.Errorf("미완결 행에 dtoc 가 채워졌다: %f", e.Dtoc)
			}
		}
	}
	if !sawQuery {
		t.Error("query 행이 Raw Data 에 없다")
	}
	if !sawUic {
		t.Error("uic 행이 Raw Data 에 없다")
	}
	// 로그의 tag=12 send 는 complete 가 없다 → 파서가 IsUnfinished 로 닫아야 한다.
	if !sawUnfinished {
		t.Error("미완결 IO 가 is_unfinished 로 표시되지 않았다")
	}

	// 데이터 IO 행에는 mgmt 값이 새어 들어오면 안 된다.
	for _, e := range resp.Events {
		if e.Action == "send_req" || e.Action == "complete_rsp" {
			if e.IsMgmt || e.MgmtName != "" {
				t.Errorf("데이터 IO 행에 mgmt 값이 붙었다: %s is_mgmt=%v name=%q",
					e.Action, e.IsMgmt, e.MgmtName)
			}
		}
	}
}

// TestSampledPathHandlesMgmtNulls — 샘플링 경로에서도 mgmt 처리가 동작하는지.
//
// 샘플링 쿼리는 `b.` 별칭 + CTE 구조라 SELECT 절이 전혀 다르게 조립된다.
// 전체 조회만 테스트하면 이쪽이 조용히 깨진 채로 남는다 — 특히 타입 없는 NULL
// 은 "스캔된 행이 전부 mgmt" 같은 조건에서만 터져서 재현이 어렵다.
func TestSampledPathHandlesMgmtNulls(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")

	// 샘플링 경로 강제 — 로그가 7행이라 예산을 4로 내린다.
	// 1 로 내리면 LIMIT 1 이 되어 mgmt/데이터 구분 이전에 다 잘린다.
	orig := maxEventsForTest
	maxEventsForTest = 4
	defer func() { maxEventsForTest = orig }()

	resp, err := GetRawData([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil)
	if err != nil {
		t.Fatalf("샘플링 경로 실패: %v", err)
	}
	if !resp.IsSampled {
		t.Fatal("샘플링 경로를 안 탔다 — 테스트가 무의미하다")
	}

	var sawMgmt bool
	for _, e := range resp.Events {
		if !e.IsMgmt {
			continue
		}
		sawMgmt = true
		if e.Cmd == "0x00" || e.Cmd == "" {
			t.Errorf("샘플링 경로에서 mgmt cmd 가 안 붙었다: action=%s cmd=%q", e.Action, e.Cmd)
		}
		if e.Lba != 0 || e.Size != 0 || e.Qd != 0 {
			t.Errorf("샘플링 경로에서 mgmt 수치가 안 비었다: lba=%d size=%d qd=%d",
				e.Lba, e.Size, e.Qd)
		}
	}
	if !sawMgmt {
		t.Error("샘플링 결과에 mgmt 행이 없다")
	}
}

// writeSkewedFsioParquet — mgmt 가 압도적 다수인 트레이스. idle 구간에서
// hibern8 쌍이 계속 도는 상황을 흉내낸다.
func writeSkewedFsioParquet(t *testing.T, dataPairs, mgmtPairs int) string {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < dataPairs; i++ {
		ts := 100.0 + float64(i)*0.01
		b.WriteString(fmt.Sprintf("%.6f\tUFS\t100\t100\t0\tapp\tvfs_write\tufshcd_command:send_req\text4\t8\t32\t1\t4096\t%d\t/data/a\t0x2\tlun=0 tag=%d hwq=0 ufs_op=0x2a grp=0x0\n",
			ts, 1000+i, i%32))
		b.WriteString(fmt.Sprintf("%.6f\tUFS\t0\t0\t0\tswapper/0\t-\tufshcd_command:complete_rsp\t\t0\t0\t0\t4096\t%d\t\t0x2\tlun=0 tag=%d hwq=0 ufs_op=0x2a grp=0x0\n",
			ts+0.001, 1000+i, i%32))
	}
	for i := 0; i < mgmtPairs; i++ {
		ts := 100.0 + float64(i)*0.01
		b.WriteString(fmt.Sprintf("%.6f\tUFS\t0\t0\t0\tswapper/0\t-\tufshcd_uic_command\t\t0\t0\t0\t0\t0\t\t0x0\tuic_cmd=0x17 a1=0x0 a2=0x0 a3=0x0 dir=send\n", ts))
		b.WriteString(fmt.Sprintf("%.6f\tUFS\t0\t0\t0\tswapper/0\t-\tufshcd_uic_command\t\t0\t0\t0\t0\t0\t\t0x0\tuic_cmd=0x17 a1=0x0 a2=0x0 a3=0x0 dir=comp\n", ts+0.002))
	}
	logFile := filepath.Join(dir, "trace.log")
	if err := os.WriteFile(logFile, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(logFile, dir, "fsio_ufs", nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSampledPathSplitsMgmtBudget — mgmt 가 표본 예산을 독차지하지 않는지.
//
// rn 을 전체에 걸쳐 하나로 매기면 uniform 샘플링이 **행 수 비율대로** 표본을
// 나눠 준다. idle 구간처럼 mgmt 가 행의 99% 인 트레이스에서는 정작 드문
// 데이터 IO 가 차트에서 거의 사라진다.
//
// 실측(데이터 IO 20행 / mgmt 2000행, 예산 100):
//
//	분리 전: 데이터 IO 7행 (35%)
//	분리 후: 데이터 IO 20행 (100%) — 소수 그룹이 온전히 남는다
//
// ⚠ 나눗수를 **그룹마다** 계산하는 게 핵심이다. 합계 기준 하나(=21)를 쓰면
// 20행짜리 데이터 IO 는 rn % 21 = 0 이 한 번도 안 맞아 **0행**이 된다 —
// 분리를 하고도 소수 그룹이 통째로 빠진다.
func TestSampledPathSplitsMgmtBudget(t *testing.T) {
	dir := writeSkewedFsioParquet(t, 10, 1000) // 데이터 IO 20행, mgmt 2000행

	orig := maxEventsForTest
	maxEventsForTest = 100
	defer func() { maxEventsForTest = orig }()

	resp, err := GetRawData([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsSampled {
		t.Fatal("샘플링 경로를 안 탔다 — 테스트가 무의미하다")
	}

	var dataIO, mgmt int
	for _, e := range resp.Events {
		if e.IsMgmt {
			mgmt++
		} else {
			dataIO++
		}
	}
	t.Logf("표본: 데이터 IO %d행 (원본 20), mgmt %d행 (원본 2000)", dataIO, mgmt)

	// 소수 그룹이 예산 비율에 눌리지 않아야 한다. 분리 전 실측이 7행이었으므로
	// 그보다 확실히 나은 선을 건다.
	if dataIO < 15 {
		t.Errorf("데이터 IO 표본 %d행 — mgmt 가 예산을 독차지했다 (want >= 15)", dataIO)
	}
	// mgmt 도 살아 있어야 한다 (한쪽만 남기는 게 목적이 아니다).
	if mgmt == 0 {
		t.Error("mgmt 가 표본에서 전멸했다")
	}
	// 총량이 예산을 크게 넘지 않아야 한다 — 그룹별 예산의 합이 상한이다.
	if len(resp.Events) > maxEventsForTest {
		t.Errorf("표본 총량 %d > 예산 %d", len(resp.Events), maxEventsForTest)
	}
}

// TestMgmtStatsCarriesFullPercentiles — mgmt 의 DtoC 분포 지표가 실제로
// 채워지는지. UI 의 "DtoC 분포" 탭이 이 값들을 그린다.
//
// 컬럼만 만들고 값이 안 오면 표가 전부 0.000 으로 보이는데, 그건 "지연이
// 0" 으로 읽혀서 빈칸보다 나쁘다.
func TestMgmtStatsCarriesFullPercentiles(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")

	stats, err := ComputeStats([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.MgmtStats) == 0 {
		t.Fatal("mgmt 집계가 비었다")
	}

	var checked int
	for _, m := range stats.MgmtStats {
		if m.GetPairedCount() == 0 {
			continue // 짝이 없으면 분포가 없는 게 맞다
		}
		checked++
		d := m.GetDtoc()
		if d == nil {
			t.Fatalf("%s: dtoc 가 nil", m.GetName())
		}
		// 짝지어진 mgmt 는 실제 왕복 시간이 있다 — 전부 0 이면 안 된다.
		if d.GetMax() <= 0 {
			t.Errorf("%s: dtoc.max = %f, want > 0", m.GetName(), d.GetMax())
		}
		if d.GetMedian() <= 0 {
			t.Errorf("%s: dtoc.median = %f, want > 0 — 백분위가 안 채워졌다",
				m.GetName(), d.GetMedian())
		}
		if d.GetP99() <= 0 {
			t.Errorf("%s: dtoc.p99 = %f, want > 0", m.GetName(), d.GetP99())
		}
		if d.GetP999() <= 0 {
			t.Errorf("%s: dtoc.p999 = %f, want > 0", m.GetName(), d.GetP999())
		}
		// min <= median <= max 는 분포의 기본 성질이다.
		if d.GetMin() > d.GetMedian() || d.GetMedian() > d.GetMax() {
			t.Errorf("%s: min/median/max 순서가 깨졌다 (%f / %f / %f)",
				m.GetName(), d.GetMin(), d.GetMedian(), d.GetMax())
		}
	}
	if checked == 0 {
		t.Fatal("paired mgmt 가 하나도 없다 — 테스트가 무의미하다")
	}
}

// TestSampledPathNonDivisibleGroupSizes — 그룹 크기가 예산으로 딱 안 나눠지는
// 경우에도 표본이 나오는지.
//
// **이 테스트가 없어서 실제 버그를 놓쳤다.** grp_div 를 DuckDB 의 `/` 로 쓰면
// DOUBLE 나눗셈이라 1.6 같은 값이 나오고, `rn % 1.6 = 0` 은 정수 rn 에 거의
// 안 맞아 표본이 0행이 된다. 그런데 초기 테스트는 2000/50, 20/50 처럼 딱
// 떨어지는 값이라 통과했다 — 나눗수가 정수로 나와 버그가 안 드러났다.
//
// 여기서는 일부러 안 나눠지는 크기(97, 1103)를 쓴다.
func TestSampledPathNonDivisibleGroupSizes(t *testing.T) {
	// 데이터 IO 194행(97쌍), mgmt 2206행(1103쌍) — 어떤 예산으로도 딱 안 떨어진다.
	dir := writeSkewedFsioParquet(t, 97, 1103)

	orig := maxEventsForTest
	maxEventsForTest = 300 // 그룹당 150 → data 194//150=1, mgmt 2206//150=14
	defer func() { maxEventsForTest = orig }()

	resp, err := GetRawData([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsSampled {
		t.Fatal("샘플링 경로를 안 탔다")
	}

	var dataIO, mgmt int
	for _, e := range resp.Events {
		if e.IsMgmt {
			mgmt++
		} else {
			dataIO++
		}
	}
	t.Logf("표본: 데이터 IO %d행 (원본 194), mgmt %d행 (원본 2206)", dataIO, mgmt)

	// DOUBLE 나눗셈이면 여기서 한쪽이 0 이 된다.
	if dataIO == 0 {
		t.Error("데이터 IO 표본이 0행 — 나눗수가 정수가 아니라 modulo 가 안 맞는다")
	}
	if mgmt == 0 {
		t.Error("mgmt 표본이 0행 — 나눗수가 정수가 아니라 modulo 가 안 맞는다")
	}
	// 소수 그룹이 다수 그룹에 눌리지 않아야 한다.
	if dataIO < 50 {
		t.Errorf("데이터 IO 표본 %d행 — 예산 배분이 비율에 눌렸다", dataIO)
	}
}

// TestSampledPathMixedUfsBlock — fsio_ufs + fsio_block 동시 조회 + 샘플링.
//
// 이 조합에서 detectLbaColumn 은 컬럼이 아니라 **식**(`COALESCE(lba, sector)`)을
// 돌려준다. 별칭을 식 앞에 붙이면 `b.COALESCE(lba, sector)` 가 되어
// "Scalar Function with name coalesce does not exist" 로 터진다.
//
// checkMixedFamily 가 두 fsio 타입을 같은 계열로 허용하므로 실제로 도달 가능하고,
// **50만 행을 넘겨 샘플링 경로를 타야만** 드러난다 — 전체 조회는 prefix 가 ""
// 라서 멀쩡하다. 그래서 대용량 트레이스에서만 나타나는 종류다.
func TestSampledPathMixedUfsBlock(t *testing.T) {
	ufsDir := writeFsioParquet(t, "fsio_ufs")
	var blockDir string

	// BLK 행이 있는 별도 로그 — 공용 fixture 에는 BLK 가 없어 block parquet 이
	// 아예 안 생긴다 (그러면 lba/sector 가 같이 있는 상황이 안 만들어져서
	// 이 테스트가 무의미해진다).
	blockDir = t.TempDir()
	blockLog := filepath.Join(blockDir, "trace.log")
	var bb strings.Builder
	for i := 0; i < 4; i++ {
		ts := 12345.0 + float64(i)*0.01
		bb.WriteString(fmt.Sprintf("%.6f\tBLK\t4521\t4521\t3\tmysqld\tvfs_write\tblock_rq_issue\text4\t8\t32\t983241\t16384\t%d\t/data/ibdata1\t0x10000\trwbs=WS\n", ts, 8192000+i*32))
		bb.WriteString(fmt.Sprintf("%.6f\tBLK\t4521\t4521\t1\tmysqld\tvfs_write\tblock_rq_complete\text4\t8\t32\t983241\t16384\t%d\t/data/ibdata1\t0x10000\trwbs=WS\n", ts+0.002, 8192000+i*32))
	}
	if err := os.WriteFile(blockLog, []byte(bb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(blockLog, blockDir, "fsio_block", nil); err != nil {
		t.Fatal(err)
	}

	orig := maxEventsForTest
	maxEventsForTest = 4
	defer func() { maxEventsForTest = orig }()

	resp, err := GetRawData([]*TraceJobInfo{
		{Dir: ufsDir, TraceType: "fsio_ufs"},
		{Dir: blockDir, TraceType: "fsio_block"},
	}, nil)
	if err != nil {
		t.Fatalf("혼합 스키마 샘플링 실패: %v", err)
	}
	if len(resp.Events) == 0 {
		t.Error("혼합 스키마 샘플링 결과가 비었다")
	}
}

// TestBlockUnfinishedReachesWire — fsio_block 의 미완결 IO 플래그가 wire 까지
// 오는지.
//
// UI 에 "unfin" 열을 만들어 놓고 서버가 값을 안 실으면 **항상 빈칸**이라,
// DtoC 0 인 미완결 행이 "엄청 빠른 IO" 로 읽힌다 — UFS 에서 막으려던 바로 그
// 오독이 block 에서 그대로 일어난다. 열만 있고 값이 없는 게 더 나쁘다.
func TestBlockUnfinishedReachesWire(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "trace.log")
	var b strings.Builder
	// 짝이 맞는 1쌍 + complete 없는 issue 2건.
	b.WriteString("100.000000\tBLK\t1\t1\t0\tapp\tvfs_write\tblock_rq_issue\text4\t8\t32\t1\t4096\t1000\t/data/a\t0x10000\trwbs=WS\n")
	b.WriteString("100.001000\tBLK\t1\t1\t0\tapp\tvfs_write\tblock_rq_complete\text4\t8\t32\t1\t4096\t1000\t/data/a\t0x10000\trwbs=WS\n")
	b.WriteString("100.002000\tBLK\t1\t1\t0\tapp\tvfs_write\tblock_rq_issue\text4\t8\t32\t1\t4096\t2000\t/data/b\t0x10000\trwbs=WS\n")
	b.WriteString("100.003000\tBLK\t1\t1\t0\tapp\tvfs_read\tblock_rq_issue\text4\t8\t32\t1\t4096\t3000\t/data/c\t0x1\trwbs=R\n")
	// 시간 만료(5초)로 닫히도록 한참 뒤 행을 하나 더 둔다.
	b.WriteString("200.000000\tBLK\t1\t1\t0\tapp\tvfs_write\tblock_rq_issue\text4\t8\t32\t1\t4096\t4000\t/data/d\t0x10000\trwbs=WS\n")
	if err := os.WriteFile(logFile, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(logFile, dir, "fsio_block", nil); err != nil {
		t.Fatal(err)
	}

	resp, err := GetRawData([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_block"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var unfinished int
	for _, e := range resp.Events {
		if e.IsUnfinished {
			unfinished++
			if e.Dtoc != 0 {
				t.Errorf("미완결 행에 dtoc 가 채워졌다: %f", e.Dtoc)
			}
		}
	}
	if unfinished == 0 {
		t.Error("fsio_block 의 is_unfinished 가 wire 로 안 온다 — Raw Data 의 unfin 열이 항상 빈칸이 된다")
	}
	t.Logf("미완결 %d행 표시됨", unfinished)
}
