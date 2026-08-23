package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pb "agent/pb"
	"agent/trace/parser"
)

// mgmt 집계 — 링크 점유 시간(total_time_ms)이 핵심 지표다.
// idle 구간에서는 데이터 IO 가 거의 없고 mgmt 가 행의 대부분이라 이게 유일한 산출물이 된다.
func TestMgmtStatsAggregatesByDisplayName(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")
	stats, err := ComputeStats([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]*pb.MgmtStats{}
	for _, m := range stats.MgmtStats {
		got[m.Name] = m
	}
	// 픽스처: query 왕복 7.7ms, hibern8 enter 2.95ms
	q := got["Read Descriptor(geometry)"]
	if q == nil {
		t.Fatalf("query mgmt 가 없다: %+v", stats.MgmtStats)
	}
	if q.Kind != "query" {
		t.Errorf("kind = %q, want query", q.Kind)
	}
	if q.Count != 2 || q.PairedCount != 1 {
		t.Errorf("count=%d paired=%d, want 2/1 (send+complete 2행, 짝지어진 건 1)", q.Count, q.PairedCount)
	}
	if d := q.TotalTimeMs - 7.7; d > 1e-6 || d < -1e-6 {
		t.Errorf("total_time_ms = %v, want 7.7", q.TotalTimeMs)
	}

	u := got["DME_HIBER_ENTER"]
	if u == nil {
		t.Fatalf("uic mgmt 가 없다: %+v", stats.MgmtStats)
	}
	if u.Kind != "uic" {
		t.Errorf("kind = %q, want uic", u.Kind)
	}
	if d := u.TotalTimeMs - 2.95; d > 1e-6 || d < -1e-6 {
		t.Errorf("total_time_ms = %v, want 2.95", u.TotalTimeMs)
	}

	// mgmt 는 데이터 IO 통계 모수에 섞이면 안 된다.
	if stats.TotalEvents != 3 {
		t.Errorf("데이터 IO total = %d, want 3 (mgmt 가 섞였다)", stats.TotalEvents)
	}
}

// ftrace 산출물은 cross-layer 컬럼이 없다 — 에러가 아니라 unsupported 로 알려야 한다.
func TestAttributionReportsUnsupportedDims(t *testing.T) {
	// ftrace UFS 스키마를 흉내낸 parquet (comm/name 없음)
	dir := t.TempDir()
	logFile := filepath.Join(dir, "trace.log")
	// 기존 ftrace UFS 라인 형식
	data := "  kworker/0:1H-123   [000] ....  1000.000000: ufshcd_command: send_req: 0:0:0:0: tag: 1, DB: 0x0, size: 4096, IS: 0, LBA: 100, opcode: 0x2a (WRITE_10), group_id: 0x0, hwq_id: 0\n" +
		"  kworker/0:1H-123   [000] ....  1000.001000: ufshcd_command: complete_rsp: 0:0:0:0: tag: 1, DB: 0x0, size: 4096, IS: 0, LBA: 100, opcode: 0x2a (WRITE_10), group_id: 0x0, hwq_id: 0\n"
	if err := os.WriteFile(logFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(logFile, dir, "ufs", nil); err != nil {
		t.Fatal(err)
	}

	resp, err := ComputeAttribution([]*TraceJobInfo{{Dir: dir, TraceType: "ufs"}},
		&pb.GetIoAttributionRequest{
			Dims: []pb.AttributionDim{
				pb.AttributionDim_ATTR_DIM_COMM,
				pb.AttributionDim_ATTR_DIM_FILE,
				pb.AttributionDim_ATTR_DIM_CMD,
			},
			TopN: 5,
		})
	if err != nil {
		t.Fatalf("에러가 아니라 unsupported 로 알려야 한다: %v", err)
	}
	if len(resp.UnsupportedDims) != 2 {
		t.Errorf("unsupported = %v, want [COMM FILE]", resp.UnsupportedDims)
	}
	// cmd 축은 파생 별칭이라 raw 컬럼 요구가 없다 — 여기서 unsupported 로 빠지면 안 된다.
	if len(resp.Groups) != 1 || resp.Groups[0].Dim != pb.AttributionDim_ATTR_DIM_CMD {
		t.Errorf("cmd 축이 살아 있어야 한다: %+v", resp.Groups)
	}
}

// (other) 롤업 행의 percentile 은 nil 이어야 한다.
// 0 으로 채우면 "0ms = 빠름" 으로 읽혀 unknown 의 정반대 의미가 된다.
func TestAttributionOtherRowHasNullPercentiles(t *testing.T) {
	// comm 이 여러 개여야 롤업이 생긴다 — 픽스처는 단일 프로세스라 직접 만든다.
	dir := t.TempDir()
	logFile := filepath.Join(dir, "trace.log")
	data := ""
	for i, comm := range []string{"appA", "appB", "appC"} {
		ts := float64(i)*10 + 1
		mk := func(t float64, action string) string {
			return fmt.Sprintf("%.6f\tUFS\t%d\t%d\t0\t%s\tvfs_write\tufshcd_command:%s"+
				"\text4\t8\t0\t0\t4096\t%d\t/data/%s.db\t0x10000\tlun=0 tag=%d ufs_op=0x2a\n",
				t, i+1, i+1, comm, action, 100+i*10, comm, i+1)
		}
		data += mk(ts, "send_req") + mk(ts+0.5, "complete_rsp")
	}
	if err := os.WriteFile(logFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(logFile, dir, "fsio_ufs", nil); err != nil {
		t.Fatal(err)
	}
	resp, err := ComputeAttribution([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}},
		&pb.GetIoAttributionRequest{
			Dims: []pb.AttributionDim{pb.AttributionDim_ATTR_DIM_COMM},
			TopN: 1, // 강제 롤업
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Groups) == 0 {
		t.Fatal("그룹이 없다")
	}
	var other *pb.AttributionEntry
	for _, e := range resp.Groups[0].Entries {
		if e.IsOther {
			other = e
		}
	}
	if other == nil {
		t.Fatalf("(other) 행이 없다 — 3개 comm 중 top-1 만 남겼으니 롤업이 있어야 한다: %+v",
			resp.Groups[0].Entries)
	}
	if other.DtocP99Ms != nil || other.DtocAvgMs != nil || other.DtocP50Ms != nil {
		t.Error("롤업 행의 percentile 은 nil 이어야 한다 (0 이면 '빠름' 으로 오독)")
	}
	// 합계는 살아 있어야 한다 — 롤업이지 누락이 아니다.
	if other.Count == 0 {
		t.Error("롤업 행의 count 가 0")
	}
}

// flow 축은 백그라운드 작업(GC/journal)이 data/metadata 를 **가려야** 한다.
// GC 이면서 DATA 인 행을 "데이터 쓰기" 로 읽으면 앱 탓으로 오독된다.
func TestAttributionFlowPrioritizesBackgroundWork(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "trace.log")
	// io_flags: GC(0x1000000) | DATA(0x10000) 둘 다 켜진 행
	mk := func(ts, action, flags string) string {
		return ts + "\tUFS\t1\t1\t0\ttest\tvfs_write\tufshcd_command:" + action +
			"\text4\t8\t0\t0\t4096\t100\t\t" + flags + "\tlun=0 tag=1 ufs_op=0x2a\n"
	}
	data := mk("1.0", "send_req", "0x0000000001010000") + // GC|DATA
		mk("2.0", "complete_rsp", "0x0000000001010000")
	if err := os.WriteFile(logFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(logFile, dir, "fsio_ufs", nil); err != nil {
		t.Fatal(err)
	}
	resp, err := ComputeAttribution([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}},
		&pb.GetIoAttributionRequest{
			Dims: []pb.AttributionDim{pb.AttributionDim_ATTR_DIM_FLOW}, TopN: 5,
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Groups) == 0 || len(resp.Groups[0].Entries) == 0 {
		t.Fatal("flow 그룹이 비었다")
	}
	if got := resp.Groups[0].Entries[0].Key; got != "GC" {
		t.Errorf("flow = %q, want GC (백그라운드 작업이 DATA 를 가려야 한다)", got)
	}
}

// Raw Data 표에 fsio cross-layer 컬럼이 실려야 한다.
// 이게 없으면 표가 기존 11컬럼과 똑같아져 Attribution 탭과 중복만 된다.
func TestRawDataCarriesFsioColumns(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")
	resp, err := GetRawData([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Events) == 0 {
		t.Fatal("이벤트 없음")
	}
	// 픽스처의 send_req 행 — cross-layer 가 채워져 있어야 한다.
	var send *pb.TraceEvent
	for _, e := range resp.Events {
		if e.GetAction() == "send_req" && e.GetComm() != "" {
			send = e
			break
		}
	}
	if send == nil {
		t.Fatalf("cross-layer 가 채워진 send 행이 없다")
	}
	if send.GetComm() != "mysqld" || send.GetSyscall() != "vfs_write" || send.GetFs() != "ext4" {
		t.Errorf("cross-layer: comm=%q syscall=%q fs=%q", send.GetComm(), send.GetSyscall(), send.GetFs())
	}
	if send.GetName() != "/data/ibdata1" {
		t.Errorf("name = %q", send.GetName())
	}
	if send.GetLun() != 0 || send.GetTag() != 7 {
		t.Errorf("lun=%d tag=%d", send.GetLun(), send.GetTag())
	}
	if send.GetLineNumber() == 0 {
		t.Error("line_number 가 0 — 원본 로그 라인 추적이 안 된다")
	}
	// UPIU 헤더는 send_req 에만 붙는다.
	if send.Txn == nil || send.GetTxn() != 0x01 {
		t.Errorf("txn = %v, want 0x01", send.Txn)
	}
	// io_flags 원본이 살아 있어야 클라이언트가 39비트를 푼다.
	if send.GetIoFlags() == 0 {
		t.Error("io_flags 가 0")
	}
}

// ftrace 산출물은 확장 컬럼이 없다 — 기존 11컬럼 경로가 그대로 동작해야 한다.
func TestRawDataFtracePathUnchanged(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "trace.log")
	data := "  kworker/0:1H-123   [000] ....  1000.000000: ufshcd_command: send_req: 0:0:0:0: tag: 1, DB: 0x0, size: 4096, IS: 0, LBA: 100, opcode: 0x2a (WRITE_10), group_id: 0x0, hwq_id: 0\n" +
		"  kworker/0:1H-123   [000] ....  1000.001000: ufshcd_command: complete_rsp: 0:0:0:0: tag: 1, DB: 0x0, size: 4096, IS: 0, LBA: 100, opcode: 0x2a (WRITE_10), group_id: 0x0, hwq_id: 0\n"
	if err := os.WriteFile(logFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(logFile, dir, "ufs", nil); err != nil {
		t.Fatal(err)
	}
	resp, err := GetRawData([]*TraceJobInfo{{Dir: dir, TraceType: "ufs"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(resp.Events))
	}
	// 기본 컬럼은 살아 있고 fsio 확장은 비어 있어야 한다.
	e := resp.Events[0]
	if e.GetCmd() == "" || e.GetTime() == 0 {
		t.Errorf("기본 컬럼이 비었다: cmd=%q time=%v", e.GetCmd(), e.GetTime())
	}
	if e.GetComm() != "" || e.GetIoFlags() != 0 || e.GetLineNumber() != 0 {
		t.Errorf("ftrace 에 fsio 확장이 채워졌다: comm=%q io_flags=%d line=%d",
			e.GetComm(), e.GetIoFlags(), e.GetLineNumber())
	}
}
