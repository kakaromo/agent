package trace

import (
	"database/sql"
	"fmt"
	"strings"

	pb "agent/pb"
	"agent/trace/parser"
)

// fsio_read page-cache 통계 집계.
//
// 정본은 Rust `../trace/src/output/fsio_read_stats_duckdb.rs` 다 — SQL 을 그대로 옮겼고
// 응답 shape 도 portal UI(`TraceCacheView.svelte`)와 맞춘다.
//
// ## ⚠ 0 으로 채우지 않는다
//
// 비율 3종과 지연 백분위 4종은 **없을 수 있다**(분모 0, 표본 0). 0 으로 채우면:
//   - 비율: "전부 miss" 와 "판정할 게 없음" 이 구분되지 않는다
//   - 지연: "0ns 였다" 로 오독된다
// proto 의 optional 로 내보내 nil 을 유지한다.

// fsioReadSchemaVersion — 응답에 실어 클라이언트가 스키마를 인지하게 한다.
// Rust `fsio_read_stats_duckdb.rs` 와 같은 값이어야 한다.
const fsioReadSchemaVersion = "fsio_read-v1"

// class 라벨 — parser 패키지의 상수와 같은 값 (SQL 리터럴로 써야 해서 여기 둔다).
const (
	fsioClassHit     = "CACHE_HIT_INFERRED"
	fsioClassMiss    = "CACHE_MISS"
	fsioClassUnknown = "UNKNOWN"
)

const (
	fsioReadDefaultTopN = 20
	fsioReadMaxTopN     = 200
)

// fsioMmapPred — mmap page fault 행을 가리키는 SQL 조건.
//
// ⚠ 캐시에 있는 페이지는 fault 를 안 내므로(fault-around) mmap row 는 사실상 miss 만
// 모인 모집단이다. read 와 같은 분모에 섞으면 캐시는 그대로인데 mmap 을 쓴다는 이유만으로
// 적중률이 깎인다 — 실측 fio_mmap_sample.log 74.22% → 59.63% (14.6%p).
// 그래서 본 집계에서 빼고 아래에서 따로 센다.
const fsioMmapPred = "action = '" + parser.FsioReadActionMmap + "'"

// addWhere — 기존 where 절에 조건 하나를 AND 로 붙인다.
func addWhere(where, cond string) string {
	if cond == "" {
		return where
	}
	if where == "" {
		return "WHERE " + cond
	}
	return where + " AND " + cond
}

// fsioReadFileKeyExpr — 파일 식별 키 SQL.
// 폴백 순서는 Rust `FILE_KEY_EXPR` 와 **같아야** 한다: 경로 → ino:N → (meta:fs) → (unknown).
const fsioReadFileKeyExpr = `CASE
	WHEN name IS NOT NULL AND name <> '' THEN name
	WHEN ino <> 0 THEN 'ino:' || CAST(ino AS VARCHAR)
	WHEN fs IS NOT NULL AND fs <> '' THEN '(meta:' || fs || ')'
	ELSE '(unknown)' END`

// ComputeFsioReadStats — fsio_read parquet 에서 page-cache 통계를 낸다.
//
// infos 는 조회 대상 잡들. 그 안의 fsio_read 형제 parquet 만 본다 — 없으면
// 빈 응답(TotalRequests=0)을 돌려준다. 에러가 아니다: "이 잡에는 Page Cache 로
// 볼 게 없다" 는 정상 상태이고, 호출부가 탭을 숨기는 근거로 쓴다.
func ComputeFsioReadStats(infos []*TraceJobInfo, req *pb.GetFsioReadStatsRequest) (*pb.GetFsioReadStatsResponse, error) {
	resp := &pb.GetFsioReadStatsResponse{SchemaVersion: fsioReadSchemaVersion}

	files := FindFsioReadParquets(infos)
	if len(files) == 0 {
		return resp, nil
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	quoted := make([]string, 0, len(files))
	for _, f := range files {
		quoted = append(quoted, fmt.Sprintf("'%s'", f))
	}
	glob := "[" + strings.Join(quoted, ",") + "]"

	// fsio_read 는 단일 스키마라 lba/cmd 축이 없다. time 필터만 의미가 있고,
	// 나머지(sector/dtoc/qd 등)는 컬럼이 없어 present 검사에서 자연히 빠진다.
	present := filterPresentCols(db, glob)
	where := buildFilterWhereCols(req.GetFilter(), "", "", "time", present)

	// duration_ns 가 없는 구버전 parquet 방어 — 전부 NULL 로 취급한다(0 아님).
	//
	// ⚠ filterPresentCols 로는 알 수 없다. 그건 필터 대상 컬럼(filterCols)만 훑는
	// 고정 목록이라 duration_ns 가 들어 있지 않아 **항상 false** 가 나온다.
	// 그대로 두면 실제 값이 다 있는데도 전부 NULL 로 버려서 지연 통계가 통째로 빈다.
	durExpr := "NULL::UBIGINT"
	if hasColumns(db, glob, "duration_ns")["duration_ns"] {
		durExpr = "duration_ns"
	}

	// ⚠ 본 집계에서 mmap 을 뺀다. 이 한 줄이 빠지면 화면 hit ratio 가 조용히 낮아진다
	// (fsioMmapPred 주석 참조). mmap 은 아래에서 따로 집계한다.
	//
	// action 컬럼이 없는 구버전 parquet 은 mmap 행 자체가 존재할 수 없으므로
	// (mmap_fault:exit 은 action 이 생긴 뒤 도입) 조건을 걸지 않는다.
	hasAction := hasColumns(db, glob, "action")["action"]
	readWhere := where
	if hasAction {
		readWhere = addWhere(where, "NOT ("+fsioMmapPred+")")
	}

	if err := fsioReadClassStats(db, glob, readWhere, durExpr, resp); err != nil {
		return nil, err
	}
	if err := fsioReadScalars(db, glob, readWhere, durExpr, resp); err != nil {
		return nil, err
	}

	topN := int(req.GetTopN())
	if topN <= 0 {
		topN = fsioReadDefaultTopN
	}
	if topN > fsioReadMaxTopN {
		topN = fsioReadMaxTopN
	}
	// 파일별도 read 모집단 기준이다 — 전체(74.22%)와 파일별이 다른 모집단이면
	// 같은 화면에서 합이 안 맞는다.
	if err := fsioReadTopFiles(db, glob, readWhere, durExpr, topN, resp); err != nil {
		return nil, err
	}

	// mmap page fault — 별도 모집단. **적중률은 만들지 않는다**(분모 부재).
	if hasAction {
		if err := fsioReadMmapStats(db, glob, addWhere(where, fsioMmapPred), durExpr, resp); err != nil {
			return nil, err
		}
	}

	fsioReadRatios(resp)
	resp.QualityWarnings = fsioReadQualityWarnings(db, glob, readWhere)
	return resp, nil
}

// fsioReadMmapStats — mmap page fault 집계.
//
// ⚠ **적중률을 만들지 않는다.** fault-around 때문에 캐시에 있는 페이지는 fault 를 안
// 내므로 이 모집단은 사실상 miss 만 모인다. hit/(hit+miss) 를 구하면 구조적으로 0% 에
// 가깝게 나오고, 그걸 "mmap 이 캐시를 못 맞춘다" 로 읽으면 완전히 틀린 결론이 된다.
// 진짜 분모(접근한 페이지 수)는 이 계층에 없다 — bpftrace 가 fault 만 보고 접근 전체를
// 못 본다. 건수와 지연만 보여주고 해석은 화면 문구가 맡는다.
func fsioReadMmapStats(db *sql.DB, glob, mmapWhere, durExpr string, resp *pb.GetFsioReadStatsResponse) error {
	q := fmt.Sprintf(`SELECT COUNT(*) AS reqs,
		COUNT(*) FILTER (WHERE cache_class = '%[4]s') AS misses,
		COALESCE(SUM(fill_units), 0) AS fill,
		COUNT(%[1]s) AS dur_n,
		CAST(FLOOR(AVG(%[1]s)) AS UBIGINT) AS dur_avg,
		quantile_disc(%[1]s, 0.50) AS p50,
		quantile_disc(%[1]s, 0.99) AS p99
	FROM read_parquet(%[2]s) %[3]s`, durExpr, glob, mmapWhere, fsioClassMiss)

	var m pb.FsioReadMmapStats
	var avg, p50, p99 sql.NullInt64
	err := db.QueryRow(q).Scan(&m.Requests, &m.MissRequests, &m.FillUnits,
		&m.DurationSamples, &avg, &p50, &p99)
	if err != nil {
		return fmt.Errorf("fsio_read mmap query: %w", err)
	}
	m.DurationAvgNs = nullU64(avg)
	m.DurationP50Ns = nullU64(p50)
	m.DurationP99Ns = nullU64(p99)
	resp.Mmap = &m
	return nil
}

// fsioReadClassStats — class 별 집계 + 지연 백분위.
//
// ⚠ 평균은 **FLOOR** 로 정수화한다. DuckDB 의 CAST(… AS UBIGINT) 는 half-to-even
// 반올림이라(5.5 → 6) Rust 의 정수 나눗셈(5.5 → 5)과 갈린다. 두 구현이 다른 숫자를
// 내면 안 되므로 truncate 로 맞춘다 — Rust 쪽 parity 테스트가 이걸 잡았다.
//
// quantile_disc 는 nearest-rank 다 (보간하지 않고 실제 표본값을 고른다). NULL 을
// 무시하므로 표본이 0 이면 NULL → nil 이 된다.
func fsioReadClassStats(db *sql.DB, glob, where, durExpr string, resp *pb.GetFsioReadStatsResponse) error {
	q := fmt.Sprintf(`SELECT cache_class,
		COUNT(*) AS requests,
		COALESCE(SUM(requested_bytes), 0) AS req_b,
		COALESCE(SUM(returned_bytes), 0) AS ret_b,
		COUNT(%[1]s) AS dur_n,
		CAST(FLOOR(AVG(%[1]s)) AS UBIGINT) AS dur_avg,
		quantile_disc(%[1]s, 0.50) AS p50,
		quantile_disc(%[1]s, 0.95) AS p95,
		quantile_disc(%[1]s, 0.99) AS p99
	FROM read_parquet(%[2]s) %[3]s
	GROUP BY cache_class ORDER BY requests DESC`, durExpr, glob, where)

	rows, err := db.Query(q)
	if err != nil {
		return fmt.Errorf("fsio_read class query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var c pb.FsioReadClassStats
		var avg, p50, p95, p99 sql.NullInt64
		if err := rows.Scan(&c.CacheClass, &c.Requests, &c.RequestedBytes, &c.ReturnedBytes,
			&c.DurationSamples, &avg, &p50, &p95, &p99); err != nil {
			return fmt.Errorf("fsio_read class scan: %w", err)
		}
		// 표본이 없으면 nil 을 유지한다 — 0 으로 채우면 "0ns 였다" 가 된다.
		c.DurationAvgNs = nullU64(avg)
		c.DurationP50Ns = nullU64(p50)
		c.DurationP95Ns = nullU64(p95)
		c.DurationP99Ns = nullU64(p99)
		resp.ByClass = append(resp.ByClass, &c)
	}
	return rows.Err()
}

// fsioReadScalars — 전역 스칼라. short read 는 0 < returned < requested (EOF 는 아니다).
func fsioReadScalars(db *sql.DB, glob, where, durExpr string, resp *pb.GetFsioReadStatsResponse) error {
	q := fmt.Sprintf(`SELECT COUNT(*) AS total,
		COALESCE(SUM(fill_units), 0) AS fill,
		COALESCE(SUM(sync_ra_units), 0) AS sra,
		COALESCE(SUM(async_ra_units), 0) AS ara,
		COUNT(*) FILTER (WHERE async_ra_units > 0) AS ra_reqs,
		COUNT(*) FILTER (WHERE returned_bytes > 0 AND returned_bytes < requested_bytes) AS shorts,
		COUNT(*) FILTER (WHERE %[1]s IS NULL) AS dur_unknown
	FROM read_parquet(%[2]s) %[3]s`, durExpr, glob, where)

	err := db.QueryRow(q).Scan(&resp.TotalRequests, &resp.FillUnits, &resp.SyncRaUnits,
		&resp.ReadaheadUnits, &resp.ReadaheadRequests, &resp.ShortReads, &resp.DurationUnknown)
	if err != nil {
		return fmt.Errorf("fsio_read scalar query: %w", err)
	}
	return nil
}

// fsioReadTopFiles — 파일별 top-N, 총 VFS read 시간 순.
//
// dur 이 없는 행은 0 으로 더해진다 — 미상이 많으면 과소평가된다(문서화된 한계).
func fsioReadTopFiles(db *sql.DB, glob, where, durExpr string, topN int, resp *pb.GetFsioReadStatsResponse) error {
	q := fmt.Sprintf(`SELECT %[1]s AS k,
		COUNT(*) AS reqs,
		COUNT(*) FILTER (WHERE cache_class = '%[5]s') AS hits,
		COUNT(*) FILTER (WHERE cache_class = '%[6]s') AS misses,
		COUNT(*) FILTER (WHERE cache_class = '%[7]s') AS unks,
		COALESCE(SUM(requested_bytes), 0) AS req_b,
		COALESCE(SUM(returned_bytes), 0) AS ret_b,
		COALESCE(SUM(fill_units), 0) AS fill,
		COUNT(*) FILTER (WHERE async_ra_units > 0) AS ra_reqs,
		COALESCE(SUM(async_ra_units), 0) AS ra_units,
		COALESCE(SUM(%[2]s), 0) AS dur_sum
	FROM read_parquet(%[3]s) %[4]s
	GROUP BY k ORDER BY dur_sum DESC, k ASC LIMIT %[8]d`,
		fsioReadFileKeyExpr, durExpr, glob, where,
		fsioClassHit, fsioClassMiss, fsioClassUnknown, topN)

	rows, err := db.Query(q)
	if err != nil {
		return fmt.Errorf("fsio_read file query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var f pb.FsioReadFileStats
		if err := rows.Scan(&f.Key, &f.Requests, &f.HitRequests, &f.MissRequests, &f.UnknownRequests,
			&f.RequestedBytes, &f.ReturnedBytes, &f.FillUnits, &f.ReadaheadRequests,
			&f.ReadaheadUnits, &f.TotalDurationNs); err != nil {
			return fmt.Errorf("fsio_read file scan: %w", err)
		}
		resp.TopFiles = append(resp.TopFiles, &f)
	}
	return rows.Err()
}

// fsioReadRatios — 비율 3종. **분모가 0 이면 nil 을 유지한다.**
//
// hit/miss 분모는 판정 가능한 것(hit+miss)만이다 — DIRECT_IO/EOF/ERROR/UNKNOWN 은
// "캐시를 맞췄나" 라는 질문 자체가 성립하지 않는다.
func fsioReadRatios(resp *pb.GetFsioReadStatsResponse) {
	cnt := func(name string) uint64 {
		for _, c := range resp.ByClass {
			if c.CacheClass == name {
				return c.Requests
			}
		}
		return 0
	}
	hit, miss, unknown := cnt(fsioClassHit), cnt(fsioClassMiss), cnt(fsioClassUnknown)
	if classifiable := hit + miss; classifiable > 0 {
		h := float64(hit) / float64(classifiable)
		m := float64(miss) / float64(classifiable)
		resp.RequestHitRatio, resp.RequestMissRatio = &h, &m
	}
	if resp.TotalRequests > 0 {
		u := float64(unknown) / float64(resp.TotalRequests)
		resp.UnknownRatio = &u
	}
}

// fsioReadQualityWarnings — parquet 행에서 뽑을 수 있는 품질 경고.
//
// **숨기지 않는다** — 부족한 근거를 모른 채 hit ratio 를 읽으면 cache 판정에서
// 특히 위험하다. 문구는 Rust 와 같은 뜻을 유지한다.
func fsioReadQualityWarnings(db *sql.DB, glob, where string) []string {
	q := fmt.Sprintf(`SELECT
		COUNT(*) FILTER (WHERE coverage <> 'ok') AS cov_bad,
		COUNT(*) FILTER (WHERE quality = 'suspect') AS suspect,
		COUNT(*) AS total
	FROM read_parquet(%s) %s`, glob, where)

	var covBad, suspect, total uint64
	if err := db.QueryRow(q).Scan(&covBad, &suspect, &total); err != nil {
		// 경고를 못 만든다고 통계 전체를 실패시키지 않는다.
		return nil
	}

	var out []string
	if covBad > 0 {
		out = append(out, fmt.Sprintf(
			"이 파일시스템은 캐시에서 읽었는지 확인할 방법이 없어요 — %d건은 '알 수 없음'으로 뒀습니다. hit 으로 세지 않았어요.", covBad))
	}
	if suspect > 0 {
		out = append(out, fmt.Sprintf(
			"%d건은 판정 근거가 약해요 (수치가 한계를 넘었거나, 근거를 못 찾았거나, 미리읽기인지 애매한 경우) — 이 건들의 hit/miss 는 참고만 해주세요.", suspect))
	}
	if total > 0 {
		if pct := float64(covBad) / float64(total) * 100; pct > 50 {
			out = append(out, fmt.Sprintf(
				"전체의 %.1f%% 가 확인 불가라서, 아래 hit/miss 숫자는 전체를 대표하지 못해요.", pct))
		}
	}
	return out
}

// nullU64 — NULL 이면 nil. 0 으로 채우지 않는다 ("표본 없음" ≠ "0ns").
func nullU64(v sql.NullInt64) *uint64 {
	if !v.Valid {
		return nil
	}
	u := uint64(v.Int64)
	if v.Int64 < 0 {
		u = 0
	}
	return &u
}
