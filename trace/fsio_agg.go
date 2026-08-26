package trace

import (
	"database/sql"
	"fmt"
	"strings"

	pb "agent/pb"
)

// fsio 전용 집계 2종 — mgmt 통계와 I/O 귀속.
//
// 둘 다 기존 `GROUP BY cmd` 통계로는 답이 안 나오는 질문을 다룬다.
// Rust `../trace/src/output/{stats_rpc_duckdb,attribution_rpc_duckdb}.rs` 의 SQL 을
// 그대로 옮겼다 — 응답 shape 도 portal UI 와 맞춘다.

// ==================== mgmt 통계 ====================

// mgmtKindExpr — mgmt action 접두어로 종류를 가르는 SQL 식.
//
// UI 그룹핑용이며 파서가 action 을 `upiu_*` / `uic_*` / `exception` 으로 정규화해 둔
// 덕분에 문자열 접두어만 보면 된다.
const mgmtKindExpr = `CASE
	WHEN action LIKE 'upiu_query%' THEN 'query'
	WHEN action LIKE 'upiu_tm%'    THEN 'tm'
	WHEN action LIKE 'uic%'        THEN 'uic'
	ELSE 'other' END`

// queryMgmtStats — mgmt 이벤트를 표시 이름(mgmt_name) 별로 집계한다.
//
// **핵심 지표는 total_time_ms (dtoc 합계) 다.** mgmt 는 데이터 전송이 아니라 링크
// 점유라 "hibern8 에 몇 초 있었나" 가 실질적인 질문이고, 그건 합계 없이는 안 나온다.
// idle 구간에서는 데이터 IO 가 거의 없고 mgmt 가 행의 대부분이라 이 집계가 그 구간의
// 사실상 유일한 산출물이 된다.
//
// 집계 축이 cmd 가 아니라 name 인 이유 — mgmt 는 SCSI opcode 가 없어 cmd 축에서는
// 전부 '0x00' 으로 뭉친다.
// where 는 현재 화면 필터. 이걸 안 받으면 사용자가 구간을 확대해도 mgmt 는 전체
// 구간 합계를 유지해서, UI 가 `totalTimeMs / (durationSeconds*1000)` 으로 계산하는
// "관측 기간의 N%" 가 100% 를 훌쩍 넘긴다(분자만 전체, 분모는 축소된 구간).
func queryMgmtStats(db *sql.DB, glob, where string) ([]*pb.MgmtStats, error) {
	// mgmt 행만 고른다. 호출부 where 는 데이터 IO 기준으로 만들어졌고
	// COALESCE(is_mgmt,FALSE)=FALSE 가 들어 있을 수 있으므로 그 조건만 뺀다.
	mgmtWhere := stripMgmtExclusion(where)
	cond := "COALESCE(is_mgmt, FALSE) = TRUE AND mgmt_name IS NOT NULL AND mgmt_name != ''"
	if mgmtWhere != "" {
		cond = strings.TrimPrefix(mgmtWhere, "WHERE ") + " AND " + cond
	}
	// dtoc 는 complete 쪽 행에만 채워진다(send 는 0). 짝지어진 건수 = dtoc > 0 인 행.
	q := fmt.Sprintf(`SELECT
		mgmt_name,
		any_value(%s) AS kind,
		count(*)::BIGINT AS cnt,
		count(*) FILTER (WHERE dtoc > 0)::BIGINT AS paired,
		COALESCE(sum(dtoc) FILTER (WHERE dtoc > 0), 0) AS total_ms,
		min(dtoc) FILTER (WHERE dtoc > 0),
		max(dtoc) FILTER (WHERE dtoc > 0),
		avg(dtoc) FILTER (WHERE dtoc > 0),
		stddev_pop(dtoc) FILTER (WHERE dtoc > 0),
		percentile_cont(0.5) WITHIN GROUP (ORDER BY dtoc) FILTER (WHERE dtoc > 0),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY dtoc) FILTER (WHERE dtoc > 0),
		percentile_cont(0.999) WITHIN GROUP (ORDER BY dtoc) FILTER (WHERE dtoc > 0)
	FROM read_parquet(%s)
	WHERE %s
	GROUP BY mgmt_name
	ORDER BY total_ms DESC, cnt DESC`, mgmtKindExpr, glob, cond)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*pb.MgmtStats
	for rows.Next() {
		var name, kind string
		var cnt, paired int64
		var totalMs float64
		var mn, mx, avg, std, med, p99, p999 sql.NullFloat64
		if err := rows.Scan(&name, &kind, &cnt, &paired, &totalMs,
			&mn, &mx, &avg, &std, &med, &p99, &p999); err != nil {
			return nil, err
		}
		out = append(out, &pb.MgmtStats{
			Name:        name,
			Kind:        kind,
			Count:       cnt,
			PairedCount: paired,
			TotalTimeMs: totalMs,
			Dtoc: &pb.LatencyStats{
				Min: mn.Float64, Max: mx.Float64, Avg: avg.Float64,
				Stddev: std.Float64, Median: med.Float64,
				P99: p99.Float64, P999: p999.Float64,
			},
		})
	}
	return out, rows.Err()
}

// stripMgmtExclusion — where 에서 mgmt 제외 조건만 걷어낸다.
//
// 데이터 IO 집계용 where 에는 `COALESCE(is_mgmt, FALSE) = FALSE` 가 붙어 있는데,
// mgmt 집계에 그걸 그대로 쓰면 아무것도 안 남는다. 나머지 조건(시간/귀속 등)은
// 유지해야 확대 구간과 모수가 맞는다.
func stripMgmtExclusion(where string) string {
	const excl = "COALESCE(is_mgmt, FALSE) = FALSE"
	if !strings.Contains(where, excl) {
		return where
	}
	out := strings.ReplaceAll(where, " AND "+excl, "")
	out = strings.ReplaceAll(out, excl+" AND ", "")
	out = strings.ReplaceAll(out, "WHERE "+excl, "")
	if strings.TrimSpace(out) == "WHERE" || strings.TrimSpace(out) == "" {
		return ""
	}
	return out
}

// ==================== I/O 귀속 ====================

// flowClassExpr — io_flags(u64) → 단일 flow 라벨. 39개 불리언을 읽을 수 있는 한 줄로 접는다.
//
// ⚠ 비트값은 `trace/parser/fsio_line.go` 의 f* 상수와 **중복**이다. 같이 고칠 것.
//
// 우선순위 순서가 분석 의도를 인코딩한다 — GC/checkpoint/journal 은 앱이 요청하지 않은
// **백그라운드** 작업이라 data/metadata 분류를 가린다. GC 이면서 DATA 인 행은
// "GC" 로 읽혀야 actionable 하다 ("데이터 쓰기" 라고 하면 앱 탓으로 오독된다).
const flowClassExpr = `CASE
	WHEN (io_flags & 16777216)    != 0 THEN 'GC'
	WHEN (io_flags & 8388608)     != 0 THEN 'Checkpoint'
	WHEN (io_flags & 4194304)     != 0 THEN 'Journal'
	WHEN (io_flags & 34359738368) != 0 THEN 'Writeback(kworker)'
	WHEN (io_flags & 68719476736) != 0 THEN 'fsync'
	WHEN (io_flags & 8589934592)  != 0 THEN 'DirectIO'
	WHEN (io_flags & 17179869184) != 0 THEN 'mmap-writeback'
	WHEN (io_flags & 131072)      != 0 THEN 'Metadata'
	WHEN (io_flags & 4294967296)  != 0 THEN 'Buffered(app)'
	WHEN (io_flags & 65536)       != 0 THEN 'Data'
	ELSE 'Other' END`

// attrDimSpec — 축별 SQL 식과 그게 요구하는 parquet 컬럼.
//
// 식은 전부 상수다 — 클라이언트 문자열이 SQL 식별자가 될 수 없게 한다.
type attrDimSpec struct {
	expr     string
	needCols []string
}

func attrDimSQL(d pb.AttributionDim) (attrDimSpec, bool) {
	switch d {
	case pb.AttributionDim_ATTR_DIM_COMM:
		// 빈 comm 은 '(none)' 으로 — NULL 그룹이 조용히 사라지지 않게.
		return attrDimSpec{"CASE WHEN comm IS NULL OR comm = '' THEN '(none)' ELSE comm END",
			[]string{"comm"}}, true
	case pb.AttributionDim_ATTR_DIM_PID:
		return attrDimSpec{"CAST(pid AS VARCHAR)", []string{"pid"}}, true
	case pb.AttributionDim_ATTR_DIM_TID:
		return attrDimSpec{"CAST(tid AS VARCHAR)", []string{"tid"}}, true
	case pb.AttributionDim_ATTR_DIM_SYSCALL:
		// bpftrace 는 VFS 를 안 거친 행의 syscall 을 '-' 로 준다 — 그대로 노출(의미 있음).
		return attrDimSpec{"CASE WHEN syscall IS NULL OR syscall = '' THEN '(none)' ELSE syscall END",
			[]string{"syscall"}}, true
	case pb.AttributionDim_ATTR_DIM_FS:
		return attrDimSpec{"CASE WHEN fs IS NULL OR fs = '' THEN '(none)' ELSE fs END",
			[]string{"fs"}}, true
	case pb.AttributionDim_ATTR_DIM_FILE:
		// File 축은 **이름을 아는 파일만** 센다. 나머지는 (파일 아님) 하나로 묶는다.
		// 제외 대상 셋:
		//  - 빈 값 / '-'   : 파일이 없는 IO
		//  - '(...)' 라벨  : bpftrace 가 "왜 파일이 없는지" 로 채운 값
		//                    (flush:barrier / meta:journal / gc:segment …)
		//  - 'ino:N'       : dentry 를 못 얻어 inode 만 아는 경우. f2fs node/저널처럼
		//                    **애초에 파일이 아닌 것**도 같은 모양이라 문자열로는 구분이
		//                    안 된다 → 파일 순위에 섞으면 오독을 부른다.
		return attrDimSpec{`CASE WHEN name IS NULL OR name = '' OR name = '-'
			OR name LIKE '(%)' OR name LIKE 'ino:%'
			THEN '(파일 아님)' ELSE name END`, []string{"name"}}, true
	case pb.AttributionDim_ATTR_DIM_INO:
		return attrDimSpec{"CAST(ino AS VARCHAR)", []string{"ino"}}, true
	case pb.AttributionDim_ATTR_DIM_FLOW:
		return attrDimSpec{flowClassExpr, []string{"io_flags"}}, true
	case pb.AttributionDim_ATTR_DIM_CMD:
		// cmd 는 호출부가 만들어 주는 **파생 별칭**이라 raw parquet 컬럼 요구가 없다.
		// 여기에 "cmd" 를 넣으면 모든 trace_type 에서 cmd 축이 unsupported 로 잘못 보고된다.
		return attrDimSpec{"__CMD__", nil}, true
	case pb.AttributionDim_ATTR_DIM_LUN:
		return attrDimSpec{"'LU' || CAST(lun AS VARCHAR)", []string{"lun"}}, true
	case pb.AttributionDim_ATTR_DIM_DEVICE:
		return attrDimSpec{"CAST(devmajor AS VARCHAR) || ':' || CAST(devminor AS VARCHAR)",
			[]string{"devmajor", "devminor"}}, true
	}
	return attrDimSpec{}, false
}

func attrSortExpr(s pb.AttributionSort) string {
	switch s {
	case pb.AttributionSort_ATTR_SORT_BYTES:
		return "total_bytes"
	case pb.AttributionSort_ATTR_SORT_LATENCY_SUM:
		return "dtoc_sum"
	default:
		return "cnt"
	}
}

const (
	attrDefaultTopN = 20
	attrMaxTopN     = 200
)

// ComputeAttribution — "이 IO 를 누가/무엇이 만들었나" 를 축별 top-N 으로 집계한다.
//
// 기존 stats 는 GROUP BY cmd 하나로 고정이라 프로세스·파일·경로 질문에 답할 수 없다.
// bpftrace 가 각 행에 붙인 cross-layer 메타를 집계 축으로 승격시킨 것.
//
// parquet 에 없는 축은 에러가 아니라 unsupported 로 돌려준다 — ftrace 산출물로 호출하면
// 대부분의 축이 여기 해당한다.
func ComputeAttribution(infos []*TraceJobInfo, req *pb.GetIoAttributionRequest) (*pb.GetIoAttributionResponse, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	if err := checkMixedFamily(infos); err != nil {
		return nil, err
	}

	glob := buildGlobList(infos)
	lbaCol := detectLbaColumn(db, glob)
	cmdCol := detectCmdColumn(db, glob)
	fsio := detectFsioSchema(db, glob)
	timeCol := detectTimeColumn(db, glob)
	where := buildFilterWhereCols(req.GetFilter(), lbaCol, cmdCol, timeCol, filterPresentCols(db, glob))
	if fsio.isUFS {
		where = addCondition(where, "COALESCE(is_mgmt, FALSE) = FALSE")
	}

	// action 이름과 size 단위는 계열·레이어마다 다르다.
	//
	//   fsio_ufs   : send_req/complete_rsp,        size = bytes            → ×1
	//   fsio_block : block_rq_issue/complete,      size = bytes            → ×1
	//   ftrace ufs : send_req/complete_rsp,        size = 4KB LBA 단위     → ×4096
	//   ftrace blk : block_rq_issue/complete,      size = 512B sector 단위 → ×512
	//
	// 예전엔 `!fsio.any()` 면 무조건 4096 이라 **ftrace block 의 바이트가 8배**로
	// 부풀었다. cmd 축은 needCols 가 없어 항상 지원되므로 ftrace block 조회로
	// 실제 도달하는 경로였다.
	isBlockLayer := fsio.isBlock
	if !fsio.any() {
		// ftrace 는 sector 컬럼 유무로 block 을 가른다 (ufs 는 lba).
		isBlockLayer = hasColumns(db, glob, "sector")["sector"]
	}
	reqAction, compAction := "send_req", "complete_rsp"
	if isBlockLayer {
		reqAction, compAction = "block_rq_issue", "block_rq_complete"
	}
	sectorBytes := 1
	if !fsio.any() {
		if isBlockLayer {
			sectorBytes = 512
		} else {
			sectorBytes = 4096
		}
	}

	resp := &pb.GetIoAttributionResponse{}

	// 전체 이벤트 수 — ratio 분모.
	var total int64
	if err := db.QueryRow(fmt.Sprintf(`SELECT count(*) FROM read_parquet(%s) %s`, glob, where)).
		Scan(&total); err != nil {
		return nil, fmt.Errorf("total: %w", err)
	}
	resp.TotalEvents = total
	if total == 0 {
		return resp, nil
	}

	present := hasColumns(db, glob,
		"comm", "pid", "tid", "syscall", "fs", "name", "ino", "io_flags", "lun", "devmajor", "devminor")

	topN := int(req.GetTopN())
	if topN <= 0 {
		topN = attrDefaultTopN
	}
	if topN > attrMaxTopN {
		topN = attrMaxTopN
	}

	// read/write 분해는 io_flags 비트가 있으면 그걸로 (가장 정확), 없으면 cmd 분류로 폴백.
	readPred, writePred := "lower(cmd) LIKE 'r%'", "lower(cmd) LIKE 'w%'"
	if present["io_flags"] {
		readPred, writePred = "(io_flags & 1) != 0", "(io_flags & 2) != 0"
	}

	for _, dim := range req.GetDims() {
		spec, ok := attrDimSQL(dim)
		if !ok {
			continue
		}
		missing := false
		for _, c := range spec.needCols {
			if !present[c] {
				missing = true
				break
			}
		}
		if missing {
			resp.UnsupportedDims = append(resp.UnsupportedDims, dim)
			continue
		}
		expr := spec.expr
		if expr == "__CMD__" {
			expr = cmdCol
		}

		group, err := queryAttrGroup(db, glob, where, expr, cmdCol, readPred, writePred,
			reqAction, compAction, sectorBytes, topN, dim, present, total,
			attrSortExpr(req.GetSortBy()))
		if err != nil {
			return nil, fmt.Errorf("attribution dim %v: %w", dim, err)
		}
		resp.Groups = append(resp.Groups, group)
	}
	return resp, nil
}

func queryAttrGroup(db *sql.DB, glob, where, expr, cmdCol, readPred, writePred,
	reqAction, compAction string, sectorBytes, topN int, dim pb.AttributionDim,
	present map[string]bool, total int64, sortExpr string) (*pb.AttributionGroup, error) {

	g := &pb.AttributionGroup{Dim: dim}

	// top-N 자르기 **전** 전체 카디널리티. UI 가 "전체 N개 중 상위 20개" 를 표시해
	// 롤업 사실을 숨기지 않게 한다.
	if err := db.QueryRow(fmt.Sprintf(
		`SELECT count(DISTINCT %s) FROM (SELECT %s AS cmd, * FROM read_parquet(%s) %s)`,
		expr, cmdCol, glob, where)).Scan(&g.DistinctKeys); err != nil {
		return nil, fmt.Errorf("distinct: %w", err)
	}

	// count(DISTINCT name) 은 비싸다 — "이 프로세스가 몇 개 파일을 건드렸나" 가
	// 의미 있는 축에서만 계산한다 (File/Ino 축에선 자명하게 1).
	wantFiles := present["name"] && (dim == pb.AttributionDim_ATTR_DIM_COMM ||
		dim == pb.AttributionDim_ATTR_DIM_PID || dim == pb.AttributionDim_ATTR_DIM_TID)
	filesCol := "NULL::BIGINT"
	if wantFiles {
		filesCol = "count(DISTINCT name)::BIGINT"
	}

	// percentile 은 p50/p99 2개만 — 기존 stats 의 6개는 10행짜리 cmd 그룹엔 맞지만
	// 1만행 name 그룹엔 sort 비용이 과하다.
	q := fmt.Sprintf(`WITH base AS (SELECT %s AS cmd, * FROM read_parquet(%s) %s),
	agg AS (
	  SELECT %s AS k,
	         count(*)::BIGINT AS cnt,
	         count(*) FILTER (WHERE action = '%s')::BIGINT AS sc,
	         COALESCE(sum(CASE WHEN action = '%s' AND %s THEN CAST(size AS BIGINT) * %d ELSE 0 END), 0)::BIGINT AS read_b,
	         COALESCE(sum(CASE WHEN action = '%s' AND %s THEN CAST(size AS BIGINT) * %d ELSE 0 END), 0)::BIGINT AS write_b,
	         COALESCE(sum(CASE WHEN action = '%s' THEN CAST(size AS BIGINT) * %d ELSE 0 END), 0)::BIGINT AS total_bytes,
	         COALESCE(sum(dtoc) FILTER (WHERE action = '%s' AND dtoc > 0), 0) AS dtoc_sum,
	         avg(dtoc) FILTER (WHERE action = '%s' AND dtoc > 0) AS dtoc_avg,
	         quantile_disc(dtoc, 0.5) FILTER (WHERE action = '%s' AND dtoc > 0) AS dtoc_p50,
	         quantile_disc(dtoc, 0.99) FILTER (WHERE action = '%s' AND dtoc > 0) AS dtoc_p99,
	         COALESCE(max(dtoc) FILTER (WHERE action = '%s'), 0) AS dtoc_max,
	         %s AS distinct_files
	  FROM base GROUP BY 1
	),
	ranked AS (SELECT *, row_number() OVER (ORDER BY %s DESC, k ASC) AS rn FROM agg)
	SELECT k, cnt, sc, read_b, write_b, total_bytes, dtoc_sum,
	       dtoc_avg, dtoc_p50, dtoc_p99, dtoc_max, distinct_files, FALSE AS is_other
	FROM ranked WHERE rn <= %d
	UNION ALL
	SELECT '(other)', COALESCE(sum(cnt),0), COALESCE(sum(sc),0),
	       COALESCE(sum(read_b),0), COALESCE(sum(write_b),0), COALESCE(sum(total_bytes),0),
	       COALESCE(sum(dtoc_sum),0), NULL, NULL, NULL,
	       COALESCE(max(dtoc_max),0), NULL, TRUE
	FROM ranked WHERE rn > %d
	HAVING count(*) > 0`,
		cmdCol, glob, where,
		expr,
		reqAction,
		compAction, readPred, sectorBytes,
		compAction, writePred, sectorBytes,
		compAction, sectorBytes,
		compAction, compAction, compAction, compAction, compAction,
		filesCol,
		sortExpr, topN, topN)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var cnt, sc, readB, writeB, totalB int64
		var dtocSum, dtocMax float64
		var dtocAvg, dtocP50, dtocP99 sql.NullFloat64
		var distinctFiles sql.NullInt64
		var isOther bool
		if err := rows.Scan(&key, &cnt, &sc, &readB, &writeB, &totalB, &dtocSum,
			&dtocAvg, &dtocP50, &dtocP99, &dtocMax, &distinctFiles, &isOther); err != nil {
			return nil, err
		}
		e := &pb.AttributionEntry{
			Key: key, Count: cnt, SendCount: sc,
			Ratio:      float64(cnt) * 100 / float64(total),
			ReadBytes:  uint64(readB),
			WriteBytes: uint64(writeB),
			TotalBytes: uint64(totalB),
			DtocSumMs:  dtocSum,
			DtocMaxMs:  dtocMax,
			IsOther:    isOther,
		}
		// (other) 롤업 행의 percentile 은 **null 로 남긴다.**
		// 0 으로 채우면 "0ms = 빠름" 으로 읽혀 unknown 의 정반대 의미가 된다.
		if dtocAvg.Valid {
			e.DtocAvgMs = &dtocAvg.Float64
		}
		if dtocP50.Valid {
			e.DtocP50Ms = &dtocP50.Float64
		}
		if dtocP99.Valid {
			e.DtocP99Ms = &dtocP99.Float64
		}
		if distinctFiles.Valid {
			e.DistinctFiles = &distinctFiles.Int64
		}
		g.Entries = append(g.Entries, e)
	}
	return g, rows.Err()
}
