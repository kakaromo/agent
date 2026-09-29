package trace

// DRAM 대역폭(dram_bw) 시계열 + 요약 집계.
//
// 정본은 Rust `../trace/src/output/dram_bw_duckdb.rs` — SQL 과 의미를 그대로 옮겼고
// 응답 shape 은 portal `/api/trace/dram-bw` 와 같다(UI 컴포넌트를 그대로 쓰려고).
//
// 지키는 불변식 (bpftrace/docs/DRAM_BW.md):
//   - mbps 는 이미 MiB/s — SQL 어디서도 곱하거나 나누지 않는다.
//   - us 는 설정값이라 가중하지 않는다. 평균은 샘플 단순 평균, 간격은 time 차이(LAG).
//   - IO 필터는 적용하지 않는다 — 시간 조건만. cpu 는 kworker CPU 라 의미가 다르다.
//
// 범위의 의미:
//   - 시계열: 줌(start/end)만. 구간(spans)은 적용하지 않는다 — 밴드와 겹쳐 보려고.
//   - 요약: 줌 ∩ 켜진 구간 OR.   - 구간별: 각 구간 ∩ 줌.
//   - 샘플 간격: 줌 범위에서 잰다. 구간 합집합에서 재면 뺀 구간 경계가 공백으로 잡힌다.

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	dramTargetPointsDefault = 2000
	dramTargetPointsMax     = 20000
)

// DramSpan — 시간 구간 하나 (초, parquet time 과 같은 축).
type DramSpan struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// DramBwQuery — 조회 조건. Start/End 가 둘 다 0 이면 전체.
type DramBwQuery struct {
	Start, End   float64
	Spans        []DramSpan
	TargetPoints int
}

// DramBwSummary — portal 응답의 summary 와 같은 shape. 없는 값은 nil (0 으로 채우지 않는다).
type DramBwSummary struct {
	Samples          uint64   `json:"samples"`
	AvgMibps         *float64 `json:"avgMibps"`
	MinMibps         *uint32  `json:"minMibps"`
	P50Mibps         *uint32  `json:"p50Mibps"`
	P95Mibps         *uint32  `json:"p95Mibps"`
	P99Mibps         *uint32  `json:"p99Mibps"`
	MaxMibps         *uint32  `json:"maxMibps"`
	IntervalMedianMs *float64 `json:"intervalMedianMs"`
	IntervalMaxMs    *float64 `json:"intervalMaxMs"`
	TimeStart        *float64 `json:"timeStart"`
	TimeEnd          *float64 `json:"timeEnd"`
}

type DramBwSpanStats struct {
	Start   float64        `json:"start"`
	End     float64        `json:"end"`
	Summary *DramBwSummary `json:"summary"`
}

// DramBwResult — portal `/api/trace/dram-bw` 응답과 같은 shape.
type DramBwResult struct {
	Time            []float64         `json:"time"`
	AvgMibps        []float64         `json:"avgMibps"`
	MaxMibps        []uint32          `json:"maxMibps"`
	Bucketed        bool              `json:"bucketed"`
	BucketSec       float64           `json:"bucketSec"`
	Summary         *DramBwSummary    `json:"summary"`
	Spans           []DramBwSpanStats `json:"spans"`
	QualityWarnings []string          `json:"qualityWarnings"`
	Devs            []string          `json:"devs"`
	// 이 잡들에 DRAM parquet 이 있나 — 없으면 화면이 탭/차트를 숨긴다 (에러 아님).
	Available bool `json:"available"`
}

// FindDramBwParquets — 잡들에 딸린 result_dram_bw.parquet 경로. 없으면 빈 슬라이스.
func FindDramBwParquets(infos []*TraceJobInfo) []string {
	var found []string
	for _, info := range infos {
		for _, p := range dramBwParquetPatterns {
			if m, err := filepath.Glob(filepath.Join(info.Dir, p)); err == nil {
				found = append(found, m...)
			}
		}
	}
	return found
}

// ComputeDramBw — DRAM parquet 이 없으면 Available=false 인 빈 결과 (에러 아님).
func ComputeDramBw(infos []*TraceJobInfo, q DramBwQuery) (*DramBwResult, error) {
	res := &DramBwResult{Time: []float64{}, AvgMibps: []float64{}, MaxMibps: []uint32{},
		Spans: []DramBwSpanStats{}, QualityWarnings: []string{}, Devs: []string{}}
	files := FindDramBwParquets(infos)
	if len(files) == 0 {
		return res, nil
	}
	res.Available = true

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()
	quoted := make([]string, 0, len(files))
	for _, f := range files {
		quoted = append(quoted, "'"+strings.ReplaceAll(f, "'", "''")+"'")
	}
	from := "read_parquet([" + strings.Join(quoted, ",") + "], union_by_name=true)"

	zoom := dramZoomCond(q.Start, q.End)
	zoomWhere := whereOf(zoom)
	target := q.TargetPoints
	if target <= 0 {
		target = dramTargetPointsDefault
	}
	if target > dramTargetPointsMax {
		target = dramTargetPointsMax
	}
	if target < 2 {
		target = 2
	}

	if err := dramSeries(db, from, zoomWhere, target, res); err != nil {
		return nil, err
	}

	spanCond := dramSpansCond(q.Spans)
	summaryWhere := whereOf(zoom, spanCond)
	if res.Summary, err = dramSummary(db, from, summaryWhere); err != nil {
		return nil, err
	}
	if len(q.Spans) > 0 {
		zs, err := dramSummary(db, from, zoomWhere)
		if err != nil {
			return nil, err
		}
		res.Summary.IntervalMedianMs = zs.IntervalMedianMs
		res.Summary.IntervalMaxMs = zs.IntervalMaxMs
	}
	for _, sp := range q.Spans {
		s, err := dramSummary(db, from, whereOf(zoom, dramSpansCond([]DramSpan{sp})))
		if err != nil {
			return nil, err
		}
		res.Spans = append(res.Spans, DramBwSpanStats{Start: sp.Start, End: sp.End, Summary: s})
	}

	res.Devs = queryStrings(db, fmt.Sprintf("SELECT DISTINCT dev FROM %s %s ORDER BY dev", from, summaryWhere))
	res.QualityWarnings = dramQualityWarnings(db, from, summaryWhere, res.Summary)
	return res, nil
}

func dramZoomCond(start, end float64) string {
	var c []string
	if start > 0 {
		c = append(c, fmt.Sprintf("time >= %v", start))
	}
	if end > 0 {
		c = append(c, fmt.Sprintf("time <= %v", end))
	}
	return strings.Join(c, " AND ")
}

// dramSpansCond — 구간 OR. ⚠ min~max 로 뭉개면 가운데 뺀 구간이 도로 포함된다.
// 구간을 줬는데 전부 무효면 FALSE — 조건을 빼면 요청보다 느슨해져 전체가 나온다.
func dramSpansCond(spans []DramSpan) string {
	if len(spans) == 0 {
		return ""
	}
	var parts []string
	for _, s := range spans {
		if s.End >= s.Start {
			parts = append(parts, fmt.Sprintf("(time >= %v AND time <= %v)", s.Start, s.End))
		}
	}
	if len(parts) == 0 {
		return "FALSE"
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func whereOf(conds ...string) string {
	var c []string
	for _, x := range conds {
		if x != "" {
			c = append(c, x)
		}
	}
	if len(c) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(c, " AND ")
}

func dramSeries(db *sql.DB, from, where string, target int, res *DramBwResult) error {
	var n int64
	var t0, t1 sql.NullFloat64
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*), MIN(time), MAX(time) FROM %s %s", from, where)).
		Scan(&n, &t0, &t1); err != nil {
		return fmt.Errorf("dram series range: %w", err)
	}
	if !t0.Valid || !t1.Valid {
		return nil
	}
	var q string
	if int(n) <= target || t1.Float64 <= t0.Float64 {
		q = fmt.Sprintf("SELECT time, CAST(mbps AS DOUBLE), CAST(mbps AS UINTEGER) FROM %s %s ORDER BY time, line_number", from, where)
	} else {
		// 균등 시간 버킷 — 마지막 샘플이 target 번째 버킷으로 넘치지 않게 LEAST 로 접는다.
		w := (t1.Float64 - t0.Float64) / float64(target)
		res.Bucketed, res.BucketSec = true, w
		q = fmt.Sprintf(`SELECT AVG(time), AVG(mbps), CAST(MAX(mbps) AS UINTEGER) FROM (
			SELECT time, mbps, LEAST(CAST(FLOOR((time - %v) / %v) AS BIGINT), %d) AS b FROM %s %s
		) GROUP BY b ORDER BY b`, t0.Float64, w, target-1, from, where)
	}
	rows, err := db.Query(q)
	if err != nil {
		return fmt.Errorf("dram series: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t, a float64
		var m uint32
		if err := rows.Scan(&t, &a, &m); err != nil {
			return err
		}
		res.Time = append(res.Time, t)
		res.AvgMibps = append(res.AvgMibps, a)
		res.MaxMibps = append(res.MaxMibps, m)
	}
	return rows.Err()
}

// dramSummary — 백분위는 quantile_disc (실제 샘플값, 보간 없음) — Rust 와 같다.
func dramSummary(db *sql.DB, from, where string) (*DramBwSummary, error) {
	q := fmt.Sprintf(`WITH base AS (SELECT time, mbps, line_number FROM %s %s),
		iv AS (SELECT (time - LAG(time) OVER (ORDER BY time, line_number)) * 1000.0 AS d FROM base)
		SELECT (SELECT COUNT(*) FROM base),
		       (SELECT AVG(mbps) FROM base),
		       (SELECT CAST(MIN(mbps) AS UINTEGER) FROM base),
		       (SELECT CAST(quantile_disc(mbps, 0.50) AS UINTEGER) FROM base),
		       (SELECT CAST(quantile_disc(mbps, 0.95) AS UINTEGER) FROM base),
		       (SELECT CAST(quantile_disc(mbps, 0.99) AS UINTEGER) FROM base),
		       (SELECT CAST(MAX(mbps) AS UINTEGER) FROM base),
		       (SELECT median(d) FROM iv WHERE d IS NOT NULL),
		       (SELECT MAX(d) FROM iv WHERE d IS NOT NULL),
		       (SELECT MIN(time) FROM base),
		       (SELECT MAX(time) FROM base)`, from, where)
	var n int64
	var avg, ivMed, ivMax, ts, te sql.NullFloat64
	var mn, p50, p95, p99, mx sql.NullInt64
	if err := db.QueryRow(q).Scan(&n, &avg, &mn, &p50, &p95, &p99, &mx, &ivMed, &ivMax, &ts, &te); err != nil {
		return nil, fmt.Errorf("dram summary: %w", err)
	}
	return &DramBwSummary{
		Samples: uint64(n), AvgMibps: f64p(avg), MinMibps: u32p(mn), P50Mibps: u32p(p50),
		P95Mibps: u32p(p95), P99Mibps: u32p(p99), MaxMibps: u32p(mx),
		IntervalMedianMs: f64p(ivMed), IntervalMaxMs: f64p(ivMax), TimeStart: f64p(ts), TimeEnd: f64p(te),
	}, nil
}

// dramQualityWarnings — 문구는 Rust 와 같게 둔다 (두 화면이 같은 말을 해야 한다).
func dramQualityWarnings(db *sql.DB, from, where string, s *DramBwSummary) []string {
	out := []string{}
	if us := queryStrings(db, fmt.Sprintf("SELECT DISTINCT CAST(us AS VARCHAR) FROM %s %s", from, where)); len(us) > 1 {
		out = append(out, fmt.Sprintf("샘플 창 길이(us)가 일정하지 않아요 (%d종). 커널의 SW 샘플 경로 값이 섞인 것으로 보여요 — "+
			"평균은 샘플 단순 평균이라 창 길이가 달라도 보정되지 않아요.", len(us)))
	}
	if s != nil && s.Samples > 0 {
		var wake int64
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s %s", from, whereAppend(where, "wake <> 0"))).Scan(&wake); err == nil && wake > 0 {
			out = append(out, fmt.Sprintf("임계값 교차(wake≠0) 샘플이 %d개 있어요. 주기 샘플이 아닌 인터럽트 경로 값이라 간격이 고르지 않을 수 있어요.", wake))
		}
	}
	if s != nil && s.IntervalMedianMs != nil && s.IntervalMaxMs != nil {
		med, mx := *s.IntervalMedianMs, *s.IntervalMaxMs
		if med > 0 && mx > med*5 {
			out = append(out, fmt.Sprintf("샘플 간격이 벌어진 곳이 있어요 (보통 %.1fms, 최대 %.0fms). "+
				"그 구간은 수집이 끊겼거나 기기가 잠들었던 것일 수 있어요 — 차트에서 선이 끊겨 보여요.", med, mx))
		}
	}
	return out
}

func whereAppend(where, cond string) string {
	if where == "" {
		return "WHERE " + cond
	}
	return where + " AND " + cond
}

func queryStrings(db *sql.DB, q string) []string {
	out := []string{}
	rows, err := db.Query(q)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s sql.NullString
		if rows.Scan(&s) == nil && s.Valid {
			out = append(out, s.String)
		}
	}
	return out
}

func f64p(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	x := v.Float64
	return &x
}

func u32p(v sql.NullInt64) *uint32 {
	if !v.Valid {
		return nil
	}
	x := uint32(v.Int64)
	return &x
}
