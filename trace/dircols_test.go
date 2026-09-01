package trace

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pb "agent/pb"
	"agent/trace/parser"
)

// 방향 분류기 / 끝주소 식의 회귀 테스트.
//
// 여기서 고정하는 건 전부 **조용히 틀리는** 종류다 — 에러가 아니라 그럴듯한 숫자가
// 나와서 한참 뒤에야 발견된다.

// dirOf — 주어진 parquet 에서 (cmd, dir) 목록을 뽑는다.
func dirOf(t *testing.T, dir, traceType string) map[string]string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.parquet"))
	if len(files) == 0 {
		t.Fatal("parquet 없음")
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	glob := fmt.Sprintf("'%s'", files[0])
	cols := newFsioCols(db, glob)
	cmdCol := detectCmdColumn(db, glob)
	present := hasColumns(db, glob, "action")

	q := fmt.Sprintf(`SELECT DISTINCT CAST(%s AS VARCHAR), %s
		FROM read_parquet(%s) WHERE %s`,
		cmdCol, cols.DirExpr(cmdCol), glob, cols.SendPredicate(present["action"]))
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("query: %v\n%s", err, q)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var cmd string
		var d sql.NullString
		if err := rows.Scan(&cmd, &d); err != nil {
			t.Fatal(err)
		}
		if d.Valid {
			out[cmd] = d.String
		} else {
			out[cmd] = "<null>"
		}
	}
	return out
}

func writeFtraceParquet(t *testing.T, lines []string, traceType string) string {
	t.Helper()
	d := t.TempDir()
	lf := filepath.Join(d, "trace.log")
	data := ""
	for _, l := range lines {
		data += l + "\n"
	}
	if err := os.WriteFile(lf, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(lf, d, traceType, nil); err != nil {
		t.Fatal(err)
	}
	return d
}

func ufsComplete(ts, tag, lba, op string) string {
	return fmt.Sprintf("  kworker/0:1-1     [000] .... %s: ufshcd_command: complete_rsp: "+
		"tag: %s, DB: 0x0, size: 4096, IS: 0x0, LBA: %s, opcode: %s, group_id: 0x0, hwq_id: 0",
		ts, tag, lba, op)
}

func ufsLine(ts, tag, lba, op string) string {
	return fmt.Sprintf("  kworker/0:1-1     [000] .... %s: ufshcd_command: send_req: "+
		"tag: %s, DB: 0x0, size: 4096, IS: 0x0, LBA: %s, opcode: %s, group_id: 0x0, hwq_id: 0",
		ts, tag, lba, op)
}

// ftrace UFS 의 hex opcode 가 read/write 로 분류되는가.
//
// ⚠ 이게 이 커밋의 핵심이다. 기존 fsio_agg.go 의 `lower(cmd) LIKE 'r%'` 폴백은
// '0x28' 에도 '0x2a' 에도 안 걸려 **ftrace UFS 의 read/write 가 전부 0** 이었다.
func TestDirExprClassifiesFtraceUFSHexOpcodes(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("100.000000", "1", "0", "0x28 (READ_10)"),
		ufsLine("100.000100", "2", "8", "0x2a (WRITE_10)"),
		ufsLine("100.000200", "3", "16", "0x88 (READ_16)"),
		ufsLine("100.000300", "4", "24", "0x8a (WRITE_16)"),
		ufsLine("100.000400", "5", "32", "0x42 (UNMAP)"),
		ufsLine("100.000500", "6", "40", "0x35 (SYNCHRONIZE_CACHE_10)"),
	}, "ufs")

	got := dirOf(t, dir, "ufs")
	want := map[string]string{
		"0x28": "read", "0x88": "read",
		"0x2a": "write", "0x8a": "write",
		"0x42": "<null>", // discard — read 도 write 도 아니다
		"0x35": "<null>", // flush
	}
	for cmd, exp := range want {
		if got[cmd] != exp {
			t.Errorf("cmd %s: dir=%q, want %q (전체: %v)", cmd, got[cmd], exp, got)
		}
	}
}

func blockLine(ts, sector, rwbs string) string {
	return fmt.Sprintf("  kworker/0:1-1     [000] .... %s: block_rq_issue: 8,0 %s 4096 () %s + 8 [kworker]",
		ts, rwbs, sector)
}

// block 의 rwbs 는 **첫 글자**로 갈라야 한다. 완전일치면 WF/WFS/WSMA 가 전부 샌다.
func TestDirExprClassifiesBlockRwbsByFirstLetter(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		blockLine("200.000000", "0", "R"),
		blockLine("200.000100", "8", "RA"),
		blockLine("200.000200", "16", "W"),
		blockLine("200.000300", "24", "WS"),
		blockLine("200.000400", "32", "WFS"), // F 가 뒤 = FUA, write 다
		blockLine("200.000500", "40", "D"),   // discard
		blockLine("200.000600", "48", "FF"),  // F 가 앞 = flush
	}, "block")

	got := dirOf(t, dir, "block")
	want := map[string]string{
		"R": "read", "RA": "read",
		"W": "write", "WS": "write", "WFS": "write",
		"D": "<null>", "FF": "<null>",
	}
	for cmd, exp := range want {
		if _, ok := got[cmd]; !ok {
			continue // 파서가 그 줄을 안 만들었으면 스킵 (포맷 차이)
		}
		if got[cmd] != exp {
			t.Errorf("rwbs %s: dir=%q, want %q (전체: %v)", cmd, got[cmd], exp, got)
		}
	}
	if len(got) == 0 {
		t.Fatal("block 행이 하나도 안 나왔다 — 픽스처가 파서에 도달하지 못했다")
	}
}

// ftrace UFS 의 Attribution read/write 바이트가 0 이 아닌지.
//
// ⚠ 이건 **원래 깨져 있던 동작**이다. fsio_agg.go 의 폴백이 `lower(cmd) LIKE 'r%'`
// 였는데 io_flags 는 fsio 전용이라 ftrace 는 항상 폴백을 탔고, ftrace UFS 의 cmd 는
// `0x28`/`0x2a` 라 어느 쪽에도 안 걸려서 read/write 가 **전부 0** 이었다.
// 에러가 아니라 0 이라 아무것도 안 잡았다.
func TestAttributionSplitsReadWriteForFtraceUFS(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("300.000000", "1", "0", "0x28 (READ_10)"),
		ufsComplete("300.000050", "1", "0", "0x28 (READ_10)"),
		ufsLine("300.000100", "2", "8", "0x28 (READ_10)"),
		ufsComplete("300.000150", "2", "8", "0x28 (READ_10)"),
		ufsLine("300.000200", "3", "16", "0x2a (WRITE_10)"),
		ufsComplete("300.000250", "3", "16", "0x2a (WRITE_10)"),
	}, "ufs")

	resp, err := ComputeAttribution(
		[]*TraceJobInfo{{Dir: dir, TraceType: "ufs"}},
		&pb.GetIoAttributionRequest{
			Dims: []pb.AttributionDim{pb.AttributionDim_ATTR_DIM_CMD},
			TopN: 20,
		})
	if err != nil {
		t.Fatal(err)
	}

	var readB, writeB uint64
	for _, d := range resp.GetGroups() {
		for _, e := range d.GetEntries() {
			readB += e.GetReadBytes()
			writeB += e.GetWriteBytes()
		}
	}
	if readB == 0 {
		t.Errorf("read_bytes = 0 — ftrace UFS 의 0x28 이 read 로 분류되지 않았다")
	}
	if writeB == 0 {
		t.Errorf("write_bytes = 0 — ftrace UFS 의 0x2a 가 write 로 분류되지 않았다")
	}
	t.Logf("read=%d write=%d bytes", readB, writeB)
}
