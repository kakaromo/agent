package trace

import (
	"bufio"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	pb "agent/pb"
)

// Raw 이벤트 CSV 내보내기.
//
// # 왜 GetRawData 를 안 쓰나
//
// GetRawData 는 50만 건이 넘으면 **샘플링한다**(sampler.go). 그 샘플은 균등 추출이
// 아니라 시간 버킷마다 lba/qd/dtoc/ctod/ctoc 의 **최소·최대 행을 일부러 끼워 넣는다** —
// 차트 외곽선을 살리려는 의도다. 즉 극단값 쪽으로 치우친 표본이라, 그걸 CSV 로 내보내면
// 받는 사람이 평균·합계를 계산했을 때 **그럴듯하게 틀린 값**이 나온다.
//
// 내보내기는 "이 데이터로 다시 계산하겠다" 는 용도이므로 샘플링을 타면 안 된다.
// 여기서는 parquet 을 직접 읽어 **전체 행**을 스트리밍한다. 메모리에 다 올리지도
// 않는다 (357k 행이 JSON 으로 55MB 였다).
//
// 컬럼 식은 sampler.go 의 queryAllEvents 와 **같은 것을 쓴다** — 화면 표와 CSV 가
// 다른 값을 보이면 안 되기 때문이다 (mgmt 행의 cmd 이름, lba/size/qd NULL 처리 포함).

// rawCSVColumns — ftrace 공통 11 컬럼. fsio 는 아래에서 cross-layer 컬럼을 덧붙인다.
var rawCSVColumns = []string{
	"time", "lba", "qd", "cpu", "dtoc", "ctod", "ctoc", "cmd", "size", "continuous", "action",
}

// ExportRawCSV — 필터를 적용한 raw 이벤트 전체를 CSV 로 w 에 스트리밍한다.
//
// 반환값은 쓴 데이터 행 수(헤더 제외). 호출부가 로그/검증에 쓴다.
func ExportRawCSV(w io.Writer, infos []*TraceJobInfo, filter *pb.TraceFilter) (int64, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return 0, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	if err := checkMixedFamily(infos); err != nil {
		return 0, err
	}

	glob := buildGlobList(infos)
	cmdCol := detectCmdColumn(db, glob)
	timeCol := detectTimeColumn(db, glob)
	lbaCol := detectLbaColumn(db, glob)
	cols := newFsioCols(db, glob)
	where := buildFilterWhereCols(filter, lbaCol, cmdCol, timeCol, filterPresentCols(db, glob))

	// ⚠ Raw 는 mgmt 행을 **일부러 남긴다** (통계와 반대). queryAllEvents 와 같은 정책이다 —
	// "그 시각에 무슨 일이 있었나" 를 보는 데이터라 hibern8 구간도 타임라인에 있어야 한다.
	sel := fmt.Sprintf(`SELECT time, %s, %s, cpu, dtoc, ctod, ctoc, %s, %s, continuous, action%s
		FROM read_parquet(%s) %s ORDER BY time`,
		cols.rawLbaExpr(lbaCol, ""), cols.mgmtNullExpr("qd", "UINTEGER", ""),
		cols.rawCmdExpr(cmdCol, ""), cols.mgmtNullExpr("size", "UINTEGER", ""),
		fsioExtraSelect(cols.schema), glob, where)

	rows, err := db.Query(sel)
	if err != nil {
		return 0, fmt.Errorf("raw csv query: %w", err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return 0, err
	}

	// bufio 로 감싸 매 행 syscall 을 피한다 — 35만 행이면 차이가 크다.
	buf := bufio.NewWriterSize(w, 64*1024)
	cw := csv.NewWriter(buf)
	// 헤더는 DuckDB 가 돌려준 이름이 아니라 우리가 정한 이름을 쓴다 — SELECT 식이
	// `CASE WHEN ...` 이라 컬럼명이 식 전체가 되는 경우가 있다.
	header := make([]string, len(colNames))
	copy(header, colNames)
	for i := range header {
		if i < len(rawCSVColumns) {
			header[i] = rawCSVColumns[i]
		}
	}
	if err := cw.Write(header); err != nil {
		return 0, err
	}

	vals := make([]any, len(colNames))
	ptrs := make([]any, len(colNames))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	var n int64
	rec := make([]string, len(colNames))
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		for i, v := range vals {
			rec[i] = csvCell(v)
		}
		if err := cw.Write(rec); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return n, err
	}
	return n, buf.Flush()
}

// csvCell — DuckDB 값 하나를 CSV 셀 문자열로.
//
// ⚠ **NULL 은 빈 칸으로 둔다. 0 으로 바꾸면 안 된다.**
// mgmt 행의 lba/size/qd 는 개념 자체가 없어서 NULL 이고, dtoc 0 은 "0ms" 가 아니라
// "아직 모름"(미완결 IO) 이다. 0 으로 채우면 받는 쪽에서 유효값으로 읽어 평균이 내려간다.
func csvCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		// %g 는 1e+06 같은 지수 표기를 만들어 스프레드시트에서 다시 파싱하기 나쁘다.
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", t), "0"), ".")
	default:
		return fmt.Sprintf("%v", t)
	}
}
