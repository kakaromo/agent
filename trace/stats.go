package trace

import (
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	pb "agent/pb"

	_ "github.com/marcboeker/go-duckdb/v2"
)

var defaultLatencyRanges = []float64{0.1, 0.5, 1, 5, 10, 50, 100, 500, 1000}

// parquetGlobPatterns returns file glob patterns for a trace type.
//
// parquet-only 단일화 후 산출물은 result_<type>.parquet 한 개. legacy 잡과의 호환을
// 위해 merged 명(<type>.parquet) 과 윈도우 분할(realtime_<type>_*.parquet) 도 인식한다.
func parquetGlobPatterns(traceType string) []string {
	switch traceType {
	case "ufs":
		return []string{"result_ufs.parquet", "ufs.parquet", "realtime_ufs_*.parquet"}
	case "block":
		return []string{"result_block.parquet", "block.parquet", "realtime_block_*.parquet"}
	case "ufscustom":
		return []string{"result_ufscustom.parquet", "ufscustom.parquet", "realtime_ufscustom_*.parquet"}
	// bpftrace(fsiotrace) 계열 — 수집 시 `--only` 로 한 레이어만 받으므로 단일 파일이다.
	case "fsio_ufs":
		return []string{"result_fsio_ufs.parquet", "fsio_ufs.parquet"}
	case "fsio_block":
		return []string{"result_fsio_block.parquet", "fsio_block.parquet"}
	default:
		return []string{"*.parquet"}
	}
}

// findParquetFiles finds actual parquet files in a directory matching the trace type.
func findParquetFiles(dir, traceType string) []string {
	patterns := parquetGlobPatterns(traceType)
	var found []string
	for _, p := range patterns {
		matches, err := filepath.Glob(filepath.Join(dir, p))
		if err == nil && len(matches) > 0 {
			found = append(found, matches...)
		}
	}
	return found
}

// buildGlobList builds a DuckDB-compatible glob from multiple TraceJobInfos.
// Only includes patterns that actually have matching files.
func buildGlobList(infos []*TraceJobInfo) string {
	needsUnion := false
	var parts []string
	for _, info := range infos {
		files := findParquetFiles(info.Dir, info.TraceType)
		for _, f := range files {
			parts = append(parts, fmt.Sprintf("'%s'", f))
		}
		if info.TraceType == "both" || info.TraceType == "" {
			needsUnion = true
		}
	}
	if len(parts) == 0 {
		return "''" // will produce empty result
	}
	if len(parts) == 1 {
		if needsUnion {
			return parts[0] + ", union_by_name=true"
		}
		return parts[0]
	}
	glob := "[" + strings.Join(parts, ", ") + "]"
	if needsUnion {
		glob += ", union_by_name=true"
	}
	return glob
}

// ComputeStats computes trace statistics from parquet files.
func ComputeStats(infos []*TraceJobInfo, filter *pb.TraceFilter, customRanges []float64) (*pb.TraceStats, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	glob := buildGlobList(infos)
	lbaCol := detectLbaColumn(db, glob)
	cmdCol := detectCmdColumn(db, glob)
	fsio := detectFsioSchema(db, glob)
	where := buildFilterWhere(filter, lbaCol, cmdCol)
	// mgmt(Query/TM UPIU, UIC) 행 제외.
	//
	// 집계 대부분은 action = send_req/block_rq_issue 로 걸러져 mgmt 가 자동 제외되지만,
	// total_events(count(*)) 와 duration 은 필터가 없어 mgmt 가 섞인다. **idle 구간에서는
	// mgmt 가 행의 대부분**이라 데이터 IO 의 분모가 통째로 흔들린다.
	// COALESCE 인 이유 — 다른 trace_type parquet 과 union 하면 is_mgmt 가 NULL 이다.
	if fsio.isUFS {
		where = addCondition(where, "COALESCE(is_mgmt, FALSE) = FALSE")
	}

	stats := &pb.TraceStats{}

	// 1. Total events + duration
	q := fmt.Sprintf(`SELECT count(*), min(time), max(time) FROM read_parquet(%s) %s`, glob, where)
	var total int64
	var minTime, maxTime sql.NullFloat64
	if err := db.QueryRow(q).Scan(&total, &minTime, &maxTime); err != nil {
		return nil, fmt.Errorf("total query: %w", err)
	}
	stats.TotalEvents = total
	if minTime.Valid && maxTime.Valid {
		stats.DurationSeconds = (maxTime.Float64 - minTime.Float64) / 1000.0
	}

	if total == 0 {
		return stats, nil
	}

	// 2. Overall latency stats (dtoc, ctod, ctoc)
	for _, col := range []string{"dtoc", "ctod", "ctoc"} {
		ls, err := queryLatencyStats(db, glob, where, col)
		if err != nil {
			return nil, fmt.Errorf("latency stats %s: %w", col, err)
		}
		switch col {
		case "dtoc":
			stats.Dtoc = ls
		case "ctod":
			stats.Ctod = ls
		case "ctoc":
			stats.Ctoc = ls
		}
	}

	// 3. QD stats
	stats.Qd, err = queryLatencyStats(db, glob, where, "qd")
	if err != nil {
		return nil, fmt.Errorf("qd stats: %w", err)
	}

	// 4. Cmd stats
	stats.CmdStats, err = queryCmdStats(db, glob, where)
	if err != nil {
		return nil, fmt.Errorf("cmd stats: %w", err)
	}

	// 5. Latency histograms
	ranges := customRanges
	if len(ranges) == 0 {
		ranges = defaultLatencyRanges
	}
	for _, latType := range []string{"dtoc", "ctod", "ctoc"} {
		histograms, err := queryLatencyHistograms(db, glob, where, latType, ranges)
		if err != nil {
			return nil, fmt.Errorf("histogram %s: %w", latType, err)
		}
		stats.LatencyHistograms = append(stats.LatencyHistograms, histograms...)
	}

	// 5.5 Compute read/write/discard totals from cmd stats
	// UFS: size unit = 4096 bytes (1 LBA = 4KB)
	// Block: size unit = 512 bytes (1 sector = 512B)
	//
	// ⚠ **fsio 는 계수가 1 이다.** bpftrace 는 size 를 이미 bytes 로 내려 보낸다
	// (OUTPUT_FORMAT.md col 13). 아래 ×4096/×512 를 그대로 태우면 read/write 바이트가
	// 4096배/512배 부풀어 오른다. Rust 도 fsio 일 때만 sector_bytes=1 로 둔다.
	// fsio 는 1, ftrace UFS 는 4096(1 LBA), ftrace Block 은 512(1 sector).
	ufsUnit, blockUnit := uint64(4096), uint64(512)
	if fsio.any() {
		ufsUnit, blockUnit = 1, 1
	}
	for _, cs := range stats.CmdStats {
		cmd := strings.ToUpper(cs.Cmd)
		switch {
		// UFS data-transfer opcodes
		case cmd == "0X28" || cmd == "0X88": // READ_10, READ_16
			cs.TotalSizeBytes *= ufsUnit
			stats.ReadTotalBytes += cs.TotalSizeBytes
		case cmd == "0X2A" || cmd == "0X8A": // WRITE_10, WRITE_16
			cs.TotalSizeBytes *= ufsUnit
			stats.WriteTotalBytes += cs.TotalSizeBytes
		case cmd == "0X42": // UNMAP (discard)
			cs.TotalSizeBytes *= ufsUnit
			stats.DiscardTotalBytes += cs.TotalSizeBytes
		// UFS/SCSI control-plane opcodes — 데이터 전송 없거나 작아서 read/write/discard
		// 합산 대상 아님. 단위 변환도 하지 않는다 (size 가 의미적으로 LBA 가 아님).
		// 0x35 SYNC_CACHE_10, 0x91 SYNC_CACHE_16  — flush
		// 0x1B START_STOP_UNIT
		// 0x00 TEST_UNIT_READY, 0x12 INQUIRY
		// 0x25 READ_CAPACITY_10, 0x9E SERVICE_ACTION_IN_16 (READ_CAPACITY_16)
		// 0xA0 REPORT_LUNS, 0x1A MODE_SENSE_6, 0x5A MODE_SENSE_10
		case cmd == "0X35" || cmd == "0X91" ||
			cmd == "0X1B" || cmd == "0X00" || cmd == "0X12" ||
			cmd == "0X25" || cmd == "0X9E" ||
			cmd == "0XA0" || cmd == "0X1A" || cmd == "0X5A":
			// 합산 안 함, 단위 변환 안 함.
		// Block io_type / fsio rwbs — 첫 글자로 분류한다.
		// 완전일치로 하면 rwbs 의 조합값("WS"/"RS"/"DS")이 전부 default 로 샌다.
		case strings.HasPrefix(cmd, "R"):
			cs.TotalSizeBytes *= blockUnit
			stats.ReadTotalBytes += cs.TotalSizeBytes
		case strings.HasPrefix(cmd, "W"):
			cs.TotalSizeBytes *= blockUnit
			stats.WriteTotalBytes += cs.TotalSizeBytes
		case strings.HasPrefix(cmd, "D"):
			cs.TotalSizeBytes *= blockUnit
			stats.DiscardTotalBytes += cs.TotalSizeBytes
		default:
			// 분류 못한 cmd — UFS 단위 추정만 하고 합산은 보류.
			// 새 opcode 가 자주 보이면 위 switch 에 추가해야 한다.
			slog.Warn("unknown trace cmd opcode (not classified into read/write/discard)",
				"cmd", cs.Cmd, "count", cs.Count, "raw_size_total", cs.TotalSizeBytes)
			cs.TotalSizeBytes *= ufsUnit
		}
	}

	// 6. Cmd + size counts
	stats.CmdSizeCounts, err = queryCmdSizeCounts(db, glob, where)
	if err != nil {
		return nil, fmt.Errorf("cmd size counts: %w", err)
	}

	// 7. Continuity stats — only from send/issue events (address continuity is send-side only)
	// UFS: action='send_req', Block: action='block_rq_issue'
	sendFilter := addCondition(where, "action IN ('send_req', 'block_rq_issue')")
	q = fmt.Sprintf(`SELECT
		count(*),
		count(*) FILTER (WHERE continuous = true),
		count(*) FILTER (WHERE continuous = true) * 100.0 / NULLIF(count(*), 0)
	FROM read_parquet(%s) %s`, glob, sendFilter)
	db.QueryRow(q).Scan(&stats.SendCount, &stats.ContinuousCount, &stats.ContinuousRatio)

	// Try aligned stats separately (column may not exist, also send-side only)
	q = fmt.Sprintf(`SELECT
		count(*) FILTER (WHERE aligned = true),
		count(*) FILTER (WHERE aligned = true) * 100.0 / NULLIF(count(*), 0)
	FROM read_parquet(%s) %s`, glob, sendFilter)
	db.QueryRow(q).Scan(&stats.AlignedCount, &stats.AlignedRatio) // ignore error if column missing

	return stats, nil
}

func queryLatencyStats(db *sql.DB, glob, where, col string) (*pb.LatencyStats, error) {
	// Filter out 0 values for latency columns (not for qd)
	extraFilter := ""
	if col != "qd" {
		if where == "" {
			extraFilter = fmt.Sprintf("WHERE %s > 0", col)
		} else {
			extraFilter = fmt.Sprintf("%s AND %s > 0", where, col)
		}
	} else {
		extraFilter = where
	}

	q := fmt.Sprintf(`SELECT
		min(%s), max(%s), avg(%s), stddev_pop(%s),
		percentile_cont(0.5) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.999) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.9999) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.99999) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.999999) WITHIN GROUP (ORDER BY %s)
	FROM read_parquet(%s) %s`,
		col, col, col, col, col, col, col, col, col, col, glob, extraFilter)

	ls := &pb.LatencyStats{}
	var minV, maxV, avgV, stdV, medV, p99, p999, p9999, p99999, p999999 sql.NullFloat64
	if err := db.QueryRow(q).Scan(&minV, &maxV, &avgV, &stdV, &medV, &p99, &p999, &p9999, &p99999, &p999999); err != nil {
		return ls, err
	}
	if minV.Valid {
		ls.Min = minV.Float64
	}
	if maxV.Valid {
		ls.Max = maxV.Float64
	}
	if avgV.Valid {
		ls.Avg = avgV.Float64
	}
	if stdV.Valid {
		ls.Stddev = stdV.Float64
	}
	if medV.Valid {
		ls.Median = medV.Float64
	}
	if p99.Valid {
		ls.P99 = p99.Float64
	}
	if p999.Valid {
		ls.P999 = p999.Float64
	}
	if p9999.Valid {
		ls.P9999 = p9999.Float64
	}
	if p99999.Valid {
		ls.P99999 = p99999.Float64
	}
	if p999999.Valid {
		ls.P999999 = p999999.Float64
	}
	return ls, nil
}

func queryCmdStats(db *sql.DB, glob, where string) ([]*pb.CmdStats, error) {
	// Detect cmd column: opcode (ufs) or io_type (block)
	cmdCol := detectCmdColumn(db, glob)

	q := fmt.Sprintf(`SELECT
		%s as cmd, count(*) as cnt,
		count(*) * 100.0 / sum(count(*)) OVER () as ratio,
		min(CASE WHEN dtoc > 0 THEN dtoc END), max(dtoc), avg(dtoc), stddev_pop(dtoc),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY dtoc),
		percentile_cont(0.999) WITHIN GROUP (ORDER BY dtoc),
		min(CASE WHEN ctod > 0 THEN ctod END), max(ctod), avg(ctod), stddev_pop(ctod),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY ctod),
		percentile_cont(0.999) WITHIN GROUP (ORDER BY ctod),
		min(CASE WHEN ctoc > 0 THEN ctoc END), max(ctoc), avg(ctoc), stddev_pop(ctoc),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY ctoc),
		percentile_cont(0.999) WITHIN GROUP (ORDER BY ctoc),
		min(qd), max(qd), avg(qd), stddev_pop(qd),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY qd),
		COALESCE(sum(CAST(size AS BIGINT)) FILTER (WHERE action IN ('send_req', 'block_rq_issue')), 0) as total_size,
		count(*) FILTER (WHERE continuous = true) as cont_count,
		count(*) FILTER (WHERE continuous = true) * 100.0 / NULLIF(count(*) FILTER (WHERE action IN ('send_req', 'block_rq_issue')), 0) as cont_ratio,
		count(*) FILTER (WHERE action IN ('send_req', 'block_rq_issue')) as send_count
	FROM read_parquet(%s) %s GROUP BY %s ORDER BY cnt DESC`,
		cmdCol, glob, where, cmdCol)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*pb.CmdStats
	for rows.Next() {
		cs := &pb.CmdStats{
			Dtoc: &pb.LatencyStats{},
			Ctod: &pb.LatencyStats{},
			Ctoc: &pb.LatencyStats{},
			Qd:   &pb.LatencyStats{},
		}
		var dtocMin, dtocMax, dtocAvg, dtocStd, dtocP99, dtocP999 sql.NullFloat64
		var ctodMin, ctodMax, ctodAvg, ctodStd, ctodP99, ctodP999 sql.NullFloat64
		var ctocMin, ctocMax, ctocAvg, ctocStd, ctocP99, ctocP999 sql.NullFloat64
		var qdMin, qdMax, qdAvg, qdStd, qdP99 sql.NullFloat64

		var contRatio sql.NullFloat64
		if err := rows.Scan(
			&cs.Cmd, &cs.Count, &cs.Ratio,
			&dtocMin, &dtocMax, &dtocAvg, &dtocStd, &dtocP99, &dtocP999,
			&ctodMin, &ctodMax, &ctodAvg, &ctodStd, &ctodP99, &ctodP999,
			&ctocMin, &ctocMax, &ctocAvg, &ctocStd, &ctocP99, &ctocP999,
			&qdMin, &qdMax, &qdAvg, &qdStd, &qdP99,
			&cs.TotalSizeBytes,
			&cs.ContinuousCount, &contRatio,
			&cs.SendCount,
		); err != nil {
			return nil, err
		}
		assignNullable(cs.Dtoc, dtocMin, dtocMax, dtocAvg, dtocStd, dtocP99, dtocP999)
		assignNullable(cs.Ctod, ctodMin, ctodMax, ctodAvg, ctodStd, ctodP99, ctodP999)
		assignNullable(cs.Ctoc, ctocMin, ctocMax, ctocAvg, ctocStd, ctocP99, ctocP999)
		if qdMin.Valid {
			cs.Qd.Min = qdMin.Float64
		}
		if qdMax.Valid {
			cs.Qd.Max = qdMax.Float64
		}
		if qdAvg.Valid {
			cs.Qd.Avg = qdAvg.Float64
		}
		if qdStd.Valid {
			cs.Qd.Stddev = qdStd.Float64
		}
		if qdP99.Valid {
			cs.Qd.P99 = qdP99.Float64
		}
		if contRatio.Valid {
			cs.ContinuousRatio = contRatio.Float64
		}
		results = append(results, cs)
	}
	return results, nil
}

func assignNullable(ls *pb.LatencyStats, min, max, avg, std, p99, p999 sql.NullFloat64) {
	if min.Valid {
		ls.Min = min.Float64
	}
	if max.Valid {
		ls.Max = max.Float64
	}
	if avg.Valid {
		ls.Avg = avg.Float64
	}
	if std.Valid {
		ls.Stddev = std.Float64
	}
	if p99.Valid {
		ls.P99 = p99.Float64
	}
	if p999.Valid {
		ls.P999 = p999.Float64
	}
}

func queryLatencyHistograms(db *sql.DB, glob, where, latCol string, ranges []float64) ([]*pb.LatencyHistogram, error) {
	cmdCol := detectCmdColumn(db, glob)

	// Build CASE expression from ranges
	caseExpr := buildRangeCaseExpr(latCol, ranges)

	q := fmt.Sprintf(`SELECT %s as cmd, %s as bucket_id, count(*) as cnt
		FROM read_parquet(%s) %s %s
		GROUP BY cmd, bucket_id ORDER BY cmd, bucket_id`,
		cmdCol, caseExpr, glob,
		addCondition(where, fmt.Sprintf("%s > 0", latCol)),
		"")

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Group by cmd
	histMap := make(map[string]*pb.LatencyHistogram)
	for rows.Next() {
		var cmd string
		var bucketID int
		var count int64
		if err := rows.Scan(&cmd, &bucketID, &count); err != nil {
			return nil, err
		}
		h, ok := histMap[cmd]
		if !ok {
			h = &pb.LatencyHistogram{Cmd: cmd, LatencyType: latCol}
			histMap[cmd] = h
		}
		start, end := rangeBounds(ranges, bucketID)
		h.Buckets = append(h.Buckets, &pb.LatencyBucket{
			RangeStartMs: start,
			RangeEndMs:   end,
			Count:        count,
		})
	}

	var result []*pb.LatencyHistogram
	for _, h := range histMap {
		result = append(result, h)
	}
	return result, nil
}

func queryCmdSizeCounts(db *sql.DB, glob, where string) ([]*pb.CmdSizeCount, error) {
	cmdCol := detectCmdColumn(db, glob)
	q := fmt.Sprintf(`SELECT %s as cmd, size, count(*) as cnt
		FROM read_parquet(%s) %s GROUP BY cmd, size ORDER BY cmd, size`, cmdCol, glob, where)
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*pb.CmdSizeCount
	for rows.Next() {
		c := &pb.CmdSizeCount{}
		if err := rows.Scan(&c.Cmd, &c.Size, &c.Count); err != nil {
			return nil, err
		}
		results = append(results, c)
	}
	return results, nil
}

// ==================== AI 해석용 다각도 집계 ====================
//
// LLM 이 trace 결과를 더 깊이 해석하도록, 기존 요약 하나 외에 여러 각도의 집계 slice 를 제공한다.
// LLM 은 SQL 을 만들지 않고 여기서 DuckDB 로 뽑은 결과만 해석한다.
//
// 각 함수는 best-effort — 컬럼/파일 없음 등으로 실패해도 상위(buildTraceSummary)에서 해당 slice 만
// 빠지고 나머지는 진행되도록 에러를 그대로 반환한다.

// TailLatencyEvent — dtoc 기준 가장 느린 요청 1건.
type TailLatencyEvent struct {
	Time float64 `json:"time"`
	Cmd  string  `json:"cmd"`
	Size int64   `json:"size"`
	Dtoc float64 `json:"dtoc"`
	Qd   int64   `json:"qd"`
}

// TimeBinLatency — 전체 duration 을 여러 bin 으로 나눈 구간별 latency 추이 1개 bin.
type TimeBinLatency struct {
	Bin int `json:"bin"`
	// 구간 경계. parquet 의 time 컬럼 원값이며 **초 단위**다(ms 아님 — 실측: span 29.6 이
	// 29.6초에 해당). LLM 프롬프트에 그대로 들어가므로 이름이 단위를 정확히 말해야 한다.
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
	Count    int64   `json:"count"`
	AvgDtoc  float64 `json:"avgDtoc"`
	P99Dtoc  float64 `json:"p99Dtoc"`
}

// ComputeAIExtras — AI 해석용 다각도 집계를 한 번에 계산해 map 으로 반환.
//
// tail top-N / 구간추이는 pb.TraceStats 에 대응 필드가 없어 proto 를 건드리지 않고 여기서 계산한다.
// 반환 map 은 buildTraceSummary 가 LLM 요약 map 에 그대로 병합한다.
//
// best-effort: 개별 slice 계산이 실패하면 해당 키만 생략하고 나머지는 채운다.
// 전부 실패하거나 파일이 없어도 빈 map + nil 을 반환해 상위 summary 조달을 죽이지 않는다.
func ComputeAIExtras(infos []*TraceJobInfo, filter *pb.TraceFilter, tailN, timeBins int) (map[string]any, error) {
	if tailN <= 0 {
		tailN = 10
	}
	if timeBins <= 0 {
		timeBins = 20
	}
	out := make(map[string]any)

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return out, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	glob := buildGlobList(infos)
	lbaCol := detectLbaColumn(db, glob)
	cmdCol := detectCmdColumn(db, glob)
	timeCol := detectTimeColumn(db, glob) // UFSCUSTOM 등 time 컬럼 부재 대비
	where := buildFilterWhere(filter, lbaCol, cmdCol)

	// 1. tail latency top-N (dtoc 기준 가장 느린 요청)
	if tail, err := queryTailLatency(db, glob, where, cmdCol, timeCol, tailN); err != nil {
		slog.Warn("AI extras: tail latency 실패", "err", err)
	} else if len(tail) > 0 {
		out["tailLatencyTop"] = tail
	}

	// 2. 시간 구간별 latency 추이 (time 컬럼 없으면 skip)
	if timeCol != "" {
		if series, err := queryTimeSeriesLatency(db, glob, where, timeCol, timeBins); err != nil {
			slog.Warn("AI extras: 시간 구간별 latency 추이 실패", "err", err)
		} else if len(series) > 0 {
			out["latencyOverTime"] = series
		}
	}

	return out, nil
}

// detectTimeColumn — 시간축 컬럼명을 감지. 대부분 'time' 이지만 일부 스키마(UFSCUSTOM 등)는
// 'start_time' 을 쓸 수 있다. 둘 다 없으면 빈 문자열 → 시간 기반 집계 skip.
func detectTimeColumn(db *sql.DB, glob string) string {
	q := fmt.Sprintf(`SELECT column_name FROM (DESCRIBE SELECT * FROM read_parquet(%s) LIMIT 0)
		WHERE column_name IN ('time', 'start_time')`, glob)
	rows, err := db.Query(q)
	if err != nil {
		// 정상 경로는 컬럼이 없으면 "" 를 반환해 시간 기반 집계를 skip 한다.
		// 에러 경로도 같은 계약을 따른다 — "time 이 있다"고 낙관하면 상위 쿼리가
		// SQL 에러로 죽는다(skip 이 조용한 오답보다 낫다).
		return ""
	}
	defer rows.Close()

	hasTime := false
	hasStart := false
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			continue
		}
		if col == "time" {
			hasTime = true
		}
		if col == "start_time" {
			hasStart = true
		}
	}
	switch {
	case hasTime && hasStart:
		return "COALESCE(time, start_time)"
	case hasTime:
		return "time"
	case hasStart:
		return "start_time"
	default:
		return ""
	}
}

// queryTailLatency — dtoc 기준 내림차순 상위 N 이벤트.
// DuckDB top-N 최적화로 500만+ 이벤트도 가볍다.
// timeCol 은 detectTimeColumn 결과(존재하는 컬럼만) — 없으면 "" 라 NULL 상수로 대체.
// size/qd 는 UFSCUSTOM 혼입 시 NULL 일 수 있어 COALESCE 로 방어한다.
func queryTailLatency(db *sql.DB, glob, where, cmdCol, timeCol string, n int) ([]TailLatencyEvent, error) {
	timeExpr := "NULL"
	if timeCol != "" {
		timeExpr = timeCol
	}
	// dtoc > 0 인 요청만 (완료 latency 없는 이벤트 제외).
	w := addCondition(where, "dtoc > 0")
	q := fmt.Sprintf(`SELECT
		%s AS t,
		%s AS cmd,
		COALESCE(size, 0) AS size,
		dtoc,
		COALESCE(qd, 0) AS qd
	FROM read_parquet(%s) %s
	ORDER BY dtoc DESC LIMIT %d`, timeExpr, cmdCol, glob, w, n)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []TailLatencyEvent
	for rows.Next() {
		var e TailLatencyEvent
		var t, dtoc sql.NullFloat64
		var cmd sql.NullString
		var size, qd sql.NullInt64
		if err := rows.Scan(&t, &cmd, &size, &dtoc, &qd); err != nil {
			return nil, err
		}
		e.Time = t.Float64
		e.Cmd = cmd.String
		e.Size = size.Int64
		e.Dtoc = dtoc.Float64
		e.Qd = qd.Int64
		events = append(events, e)
	}
	return events, rows.Err()
}

// queryTimeSeriesLatency — 전체 duration 을 bins 개로 나눠 bin 별 이벤트 수 / avg / p99 dtoc.
// "특정 구간에서 latency 튐" 을 LLM 이 짚을 수 있게 한다.
//
// GROUP BY floor((time-min)/binwidth) 단순 방식. timeCol 은 detectTimeColumn 이 감지한
// 실존 컬럼(호출 측이 ""면 skip). dtoc > 0 인 요청만 집계한다.
func queryTimeSeriesLatency(db *sql.DB, glob, where, timeCol string, bins int) ([]TimeBinLatency, error) {
	// 시간 범위 먼저 파악.
	rangeW := addCondition(where, timeCol+" IS NOT NULL")
	rq := fmt.Sprintf(`SELECT min(%s), max(%s)
		FROM read_parquet(%s) %s`, timeCol, timeCol, glob, rangeW)
	var minT, maxT sql.NullFloat64
	if err := db.QueryRow(rq).Scan(&minT, &maxT); err != nil {
		return nil, err
	}
	if !minT.Valid || !maxT.Valid || maxT.Float64 <= minT.Float64 {
		return nil, nil // 시간축 없음(UFSCUSTOM 단독 등) 또는 단일 시점 — 추이 무의미
	}
	span := maxT.Float64 - minT.Float64
	binWidth := span / float64(bins)

	// bin index = floor((t-min)/binWidth), 마지막 경계(t==max)는 bins-1 로 clamp.
	w := addCondition(where, "dtoc > 0")
	q := fmt.Sprintf(`SELECT
		LEAST(CAST(floor((%s - %f) / %f) AS BIGINT), %d) AS bin,
		count(*) AS cnt,
		avg(dtoc) AS avg_dtoc,
		percentile_cont(0.99) WITHIN GROUP (ORDER BY dtoc) AS p99_dtoc
	FROM read_parquet(%s) %s
	GROUP BY bin ORDER BY bin`,
		timeCol, minT.Float64, binWidth, bins-1, glob, w)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var series []TimeBinLatency
	for rows.Next() {
		var bin sql.NullInt64
		var cnt sql.NullInt64
		var avgD, p99D sql.NullFloat64
		if err := rows.Scan(&bin, &cnt, &avgD, &p99D); err != nil {
			return nil, err
		}
		b := int(bin.Int64)
		series = append(series, TimeBinLatency{
			Bin:      b,
			StartSec: minT.Float64 + float64(b)*binWidth,
			EndSec:   minT.Float64 + float64(b+1)*binWidth,
			Count:    cnt.Int64,
			AvgDtoc:  avgD.Float64,
			P99Dtoc:  p99D.Float64,
		})
	}
	return series, rows.Err()
}

// ==================== Helpers ====================

// hasColumns — glob 대상 parquet 에 존재하는 컬럼 집합을 조회한다.
func hasColumns(db *sql.DB, glob string, names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, "'"+n+"'")
	}
	q := fmt.Sprintf(`SELECT column_name FROM (DESCRIBE SELECT * FROM read_parquet(%s) LIMIT 0)
		WHERE column_name IN (%s)`, glob, strings.Join(quoted, ", "))
	rows, err := db.Query(q)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err == nil {
			out[col] = true
		}
	}
	return out
}

// fsioSchema — 이 parquet 이 bpftrace(fsiotrace) 산출물인가.
//
// trace_type 문자열이 아니라 **스키마로** 판정한다. 통계 경로는 여러 잡을 합쳐
// 조회할 수 있어 호출부가 종류를 확실히 알지 못하는 경우가 있기 때문이다.
// `is_mgmt` 는 fsio_ufs 에만, `rwbs` 는 fsio_block 에만 있다.
type fsioSchema struct {
	isUFS   bool // fsio_ufs
	isBlock bool // fsio_block
}

func (f fsioSchema) any() bool { return f.isUFS || f.isBlock }

func detectFsioSchema(db *sql.DB, glob string) fsioSchema {
	c := hasColumns(db, glob, "is_mgmt", "rwbs", "mgmt_name")
	return fsioSchema{
		isUFS:   c["is_mgmt"] || c["mgmt_name"],
		isBlock: c["rwbs"],
	}
}

// detectCmdColumn — cmd 축으로 쓸 SQL 식.
//
// fsio 는 기존 ftrace 스키마와 타입/의미가 달라 그대로 못 쓴다 (Rust
// `output/stats_rpc_duckdb.rs` 가 같은 문제를 같은 방식으로 푼다):
//   - fsio_ufs `opcode` 는 **UInt8 숫자**다. 기존 ftrace UFS 는 `0x2a` **문자열**이라
//     그대로 두면 cmd 축이 `42` 같은 10진수로 나와 read/write 분류기가 전부 빗나간다.
//   - fsio_block `io_type` 은 파서 정책상 **항상 빈 문자열**이라 cmd 축이 통째로 빈다.
//     대신 `rwbs`("WS"/"R"/"D")가 분류 정보를 갖고 있다.
func detectCmdColumn(db *sql.DB, glob string) string {
	if f := detectFsioSchema(db, glob); f.any() {
		switch {
		case f.isUFS && f.isBlock:
			// 여러 잡을 합쳐 두 스키마가 섞인 경우 — 있는 쪽을 쓴다.
			return "COALESCE('0x' || lpad(lower(to_hex(opcode)), 2, '0'), rwbs)"
		case f.isUFS:
			return "'0x' || lpad(lower(to_hex(opcode)), 2, '0')"
		default:
			return "rwbs"
		}
	}

	// With union_by_name=true, both columns may exist (NULL for missing rows).
	cols := hasColumns(db, glob, "opcode", "io_type")
	hasOpcode, hasIoType := cols["opcode"], cols["io_type"]

	// Both exist (mixed ufs+block) → use COALESCE
	if hasOpcode && hasIoType {
		return "COALESCE(opcode, io_type)"
	}
	if hasOpcode {
		return "opcode"
	}
	if hasIoType {
		return "io_type"
	}
	return "opcode"
}

func buildFilterWhere(f *pb.TraceFilter, lbaCol, cmdCol string) string {
	if f == nil {
		return ""
	}
	var conds []string
	if f.StartTime > 0 {
		conds = append(conds, fmt.Sprintf("time >= %f", f.StartTime))
	}
	if f.EndTime > 0 {
		conds = append(conds, fmt.Sprintf("time <= %f", f.EndTime))
	}
	if f.StartLba > 0 {
		conds = append(conds, fmt.Sprintf("%s >= %d", lbaCol, f.StartLba))
	}
	if f.EndLba > 0 {
		conds = append(conds, fmt.Sprintf("%s <= %d", lbaCol, f.EndLba))
	}
	if f.MinDtoc > 0 {
		conds = append(conds, fmt.Sprintf("dtoc >= %f", f.MinDtoc))
	}
	if f.MaxDtoc > 0 {
		conds = append(conds, fmt.Sprintf("dtoc <= %f", f.MaxDtoc))
	}
	if f.MinCtoc > 0 {
		conds = append(conds, fmt.Sprintf("ctoc >= %f", f.MinCtoc))
	}
	if f.MaxCtoc > 0 {
		conds = append(conds, fmt.Sprintf("ctoc <= %f", f.MaxCtoc))
	}
	if f.MinCtod > 0 {
		conds = append(conds, fmt.Sprintf("ctod >= %f", f.MinCtod))
	}
	if f.MaxCtod > 0 {
		conds = append(conds, fmt.Sprintf("ctod <= %f", f.MaxCtod))
	}
	if f.MinQd > 0 {
		conds = append(conds, fmt.Sprintf("qd >= %d", f.MinQd))
	}
	if f.MaxQd > 0 {
		conds = append(conds, fmt.Sprintf("qd <= %d", f.MaxQd))
	}
	if len(f.CpuList) > 0 {
		vals := make([]string, len(f.CpuList))
		for i, v := range f.CpuList {
			vals[i] = fmt.Sprintf("%d", v)
		}
		conds = append(conds, fmt.Sprintf("cpu IN (%s)", strings.Join(vals, ",")))
	}
	if len(f.CmdList) > 0 {
		vals := make([]string, len(f.CmdList))
		for i, v := range f.CmdList {
			vals[i] = fmt.Sprintf("'%s'", v)
		}
		conds = append(conds, fmt.Sprintf("%s IN (%s)", cmdCol, strings.Join(vals, ",")))
	}
	if len(f.SizeList) > 0 {
		vals := make([]string, len(f.SizeList))
		for i, v := range f.SizeList {
			vals[i] = fmt.Sprintf("%d", v)
		}
		conds = append(conds, fmt.Sprintf("size IN (%s)", strings.Join(vals, ",")))
	}
	if len(f.ActionList) > 0 {
		vals := make([]string, len(f.ActionList))
		for i, v := range f.ActionList {
			vals[i] = fmt.Sprintf("'%s'", v)
		}
		conds = append(conds, fmt.Sprintf("action IN (%s)", strings.Join(vals, ",")))
	}
	if len(conds) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conds, " AND ")
}

func buildRangeCaseExpr(col string, ranges []float64) string {
	var parts []string
	for i, r := range ranges {
		parts = append(parts, fmt.Sprintf("WHEN %s <= %f THEN %d", col, r, i))
	}
	parts = append(parts, fmt.Sprintf("ELSE %d", len(ranges)))
	return "CASE " + strings.Join(parts, " ") + " END"
}

func rangeBounds(ranges []float64, bucketID int) (float64, float64) {
	if bucketID == 0 {
		return 0, ranges[0]
	}
	if bucketID >= len(ranges) {
		return ranges[len(ranges)-1], 0 // 0 = unbounded
	}
	return ranges[bucketID-1], ranges[bucketID]
}

func addCondition(where, cond string) string {
	if where == "" {
		return "WHERE " + cond
	}
	return where + " AND " + cond
}
