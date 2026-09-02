package trace

import (
	"archive/zip"
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

// ExcelMaxDataRows — CSV 한 파일에 담는 최대 **데이터** 행 수.
//
// Excel 시트 한계가 1,048,576 행인데 **첫 줄이 헤더**라 데이터는 하나 적다.
// 이 값을 넘으면 파일을 _1, _2 로 나누고 ZIP 으로 묶는다.
const ExcelMaxDataRows = 1_048_575

// rowsPerFile — 테스트에서 갈아끼우기 위한 간접 참조.
// 100만 행 픽스처를 만들면 테스트가 수십 초 걸려서, 분할 로직만 작은 값으로 검증한다.
var rowsPerFile int64 = ExcelMaxDataRows

// rawCSVColumns — ftrace 공통 11 컬럼. fsio 는 cross-layer 컬럼이 뒤에 붙는다.
var rawCSVColumns = []string{
	"time", "lba", "qd", "cpu", "dtoc", "ctod", "ctoc", "cmd", "size", "continuous", "action",
}

// RawCSVResult — 내보내기 결과. 호출부가 로그·검증에 쓴다.
type RawCSVResult struct {
	Rows      int64 // 헤더 제외 데이터 행 수
	FileCount int   // 나눠 쓴 CSV 개수 (1 이면 분할 없음)
	Zipped    bool  // ZIP 으로 감쌌는가
}

// CountRawRows — 필터를 적용한 raw 이벤트 행 수.
//
// 내보내기 **전에** 부른다. 분할 여부(=ZIP 여부)에 따라 Content-Type 과 파일명이
// 달라지는데, 스트리밍은 첫 바이트를 쓰고 나면 헤더를 못 바꾸기 때문이다.
// count(*) 는 parquet 메타만 읽어 싸다 — 95만 행에서 수 ms.
func CountRawRows(infos []*TraceJobInfo, filter *pb.TraceFilter) (int64, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return 0, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	if err := checkMixedFamily(infos); err != nil {
		return 0, err
	}
	glob := buildGlobList(infos)
	where := buildFilterWhereCols(filter,
		detectLbaColumn(db, glob), detectCmdColumn(db, glob), detectTimeColumn(db, glob),
		filterPresentCols(db, glob))

	var n int64
	q := fmt.Sprintf("SELECT count(*) FROM read_parquet(%s) %s", glob, where)
	if err := db.QueryRow(q).Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// ExportRawCSV — raw 이벤트 전체를 w 에 쓴다.
//
// split=false 면 CSV 하나를 그대로 흘려보낸다.
// split=true  면 `<baseName>_1.csv`, `_2.csv` … 로 나눠 **ZIP 하나**로 감싼다.
//
// ⚠ 분할 파일은 **각각 첫 줄에 헤더를 다시 넣는다.** 두 번째 파일만 열었을 때
// 컬럼을 알 수 없으면 그 파일은 혼자서는 쓸모가 없다.
//
// split 판단은 호출부가 CountRawRows 로 미리 한다 (스트리밍이라 중간에 못 바꾼다).
func ExportRawCSV(w io.Writer, infos []*TraceJobInfo, filter *pb.TraceFilter,
	baseName string, split bool,
) (RawCSVResult, error) {
	res := RawCSVResult{Zipped: split}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return res, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	if err := checkMixedFamily(infos); err != nil {
		return res, err
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
		return res, fmt.Errorf("raw csv query: %w", err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return res, err
	}
	header := rawCSVHeader(colNames)

	vals := make([]any, len(colNames))
	ptrs := make([]any, len(colNames))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	rec := make([]string, len(colNames))

	// 한 행을 rec 로 읽어 오는 클로저 — 두 경로가 같이 쓴다.
	scanRow := func() error {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		for i, v := range vals {
			rec[i] = csvCell(v)
		}
		return nil
	}

	if !split {
		buf := bufio.NewWriterSize(w, 64*1024)
		cw := csv.NewWriter(buf)
		if err := cw.Write(header); err != nil {
			return res, err
		}
		for rows.Next() {
			if err := scanRow(); err != nil {
				return res, err
			}
			if err := cw.Write(rec); err != nil {
				return res, err
			}
			res.Rows++
		}
		if err := rows.Err(); err != nil {
			return res, err
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return res, err
		}
		res.FileCount = 1
		return res, buf.Flush()
	}

	// ── 분할 + ZIP ──
	zw := zip.NewWriter(w)
	var cw *csv.Writer
	var inFile int64 // 현재 파일에 쓴 데이터 행 수

	// newPart — 다음 조각 파일을 열고 헤더를 쓴다.
	newPart := func() error {
		if cw != nil {
			cw.Flush()
			if err := cw.Error(); err != nil {
				return err
			}
		}
		res.FileCount++
		f, err := zw.Create(fmt.Sprintf("%s_%d.csv", baseName, res.FileCount))
		if err != nil {
			return err
		}
		cw = csv.NewWriter(f)
		inFile = 0
		// ⚠ 조각마다 헤더를 다시 넣는다 — 두 번째 파일만 열어도 컬럼을 알아야 한다.
		return cw.Write(header)
	}

	if err := newPart(); err != nil {
		return res, err
	}
	for rows.Next() {
		if inFile >= rowsPerFile {
			if err := newPart(); err != nil {
				return res, err
			}
		}
		if err := scanRow(); err != nil {
			return res, err
		}
		if err := cw.Write(rec); err != nil {
			return res, err
		}
		res.Rows++
		inFile++
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return res, err
	}
	return res, zw.Close()
}

// rawCSVHeader — DuckDB 컬럼명을 우리가 정한 이름으로 바꾼다.
//
// SELECT 식이 `CASE WHEN ...` 이라 컬럼명이 식 전체가 되는 경우가 있어 그대로 못 쓴다.
func rawCSVHeader(colNames []string) []string {
	h := make([]string, len(colNames))
	copy(h, colNames)
	for i := range h {
		if i < len(rawCSVColumns) {
			h[i] = rawCSVColumns[i]
		}
	}
	return h
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
