package trace

import (
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
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
	// fsio_read 는 여기서 돌려주지 않는다 — 독립 trace_type 이 아니라
	// fsio_ufs/fsio_block 의 **형제**다. Page Cache 조회는 FindFsioReadParquets 를 쓴다.
	default:
		return []string{"*.parquet"}
	}
}

// fsioReadParquetPatterns — VFS read 종료 요약 parquet 파일 패턴.
var fsioReadParquetPatterns = []string{"result_fsio_read.parquet", "fsio_read.parquet"}

// isFsioReadParquet — 이 경로가 fsio_read 산출물인가.
//
// ⚠ `*.parquet` 와일드카드(trace_type 이 "both"/"" 인 잡)가 이 파일을 **빨아들이면
// 안 된다.** 스키마가 33컬럼으로 전혀 달라 union_by_name 으로 붙으면 행 수가
// 통째로 부풀고(실측 471 → 834) 모든 통계가 조용히 틀린다.
func isFsioReadParquet(path string) bool {
	base := filepath.Base(path)
	for _, p := range fsioReadParquetPatterns {
		if base == p {
			return true
		}
	}
	return false
}

// FindFsioReadParquets — 이 잡들에 딸린 fsio_read parquet 경로.
//
// 없으면 빈 슬라이스. 호출부는 이걸로 "Page Cache 를 보여줄 수 있는 잡인가" 를 판단한다
// — portal 은 parquet 레지스트리에서 형제를 찾지만 standalone 은 디렉토리를 본다.
func FindFsioReadParquets(infos []*TraceJobInfo) []string {
	var found []string
	for _, info := range infos {
		for _, p := range fsioReadParquetPatterns {
			matches, err := filepath.Glob(filepath.Join(info.Dir, p))
			if err == nil {
				found = append(found, matches...)
			}
		}
	}
	return found
}

// findParquetFiles finds actual parquet files in a directory matching the trace type.
func findParquetFiles(dir, traceType string) []string {
	patterns := parquetGlobPatterns(traceType)
	var found []string
	for _, p := range patterns {
		matches, err := filepath.Glob(filepath.Join(dir, p))
		if err != nil {
			continue
		}
		for _, m := range matches {
			// ⚠ fsio_read 는 스키마가 달라 섞이면 행 수가 부풀고 통계가 조용히 틀린다.
			// `*.parquet` 와일드카드로 들어오는 경로를 여기서 막는다.
			if isFsioReadParquet(m) {
				continue
			}
			found = append(found, m)
		}
	}
	return found
}

// buildGlobList builds a DuckDB-compatible glob from multiple TraceJobInfos.
// Only includes patterns that actually have matching files.
func buildGlobList(infos []*TraceJobInfo) string {
	// union_by_name 이 필요한 경우 = **스키마가 섞일 수 있는 경우**.
	//
	//   - "both"/"" : 한 잡 안에 ufs + block parquet 이 같이 있다
	//   - 서로 다른 trace_type 의 잡을 함께 조회 (예: fsio_ufs + fsio_block).
	//     이건 UI 가 jobIds 를 여러 개 넘길 수 있어 실제로 도달한다. 예전에는
	//     "both"/"" 만 봐서, 타입이 다른 두 잡을 고르면 DuckDB 가
	//     `schema mismatch in glob` 로 조회 자체를 거부했다.
	//
	// 같은 타입만 여러 개면 스키마가 동일하므로 union 이 필요 없다(성능상 안 붙인다).
	needsUnion := false
	seenTypes := make(map[string]bool)
	var parts []string
	for _, info := range infos {
		files := findParquetFiles(info.Dir, info.TraceType)
		for _, f := range files {
			parts = append(parts, fmt.Sprintf("'%s'", f))
		}
		if info.TraceType == "both" || info.TraceType == "" {
			needsUnion = true
		}
		seenTypes[info.TraceType] = true
	}
	if len(seenTypes) > 1 {
		needsUnion = true
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

	if err := checkMixedFamily(infos); err != nil {
		return nil, err
	}

	glob := buildGlobList(infos)
	lbaCol := detectLbaColumn(db, glob)
	cols := newFsioCols(db, glob)
	fsio := cols.schema
	cmdCol := detectCmdColumn(db, glob)
	timeCol := detectTimeColumn(db, glob)
	where := buildFilterWhereCols(filter, lbaCol, cmdCol, timeCol, filterPresentCols(db, glob))
	// mgmt(Query/TM UPIU, UIC) 행 제외 — 조건 정의는 fsio_cols.go 에 모여 있다.
	//
	// 집계 대부분은 action = send_req/block_rq_issue 로 걸러져 mgmt 가 자동 제외되지만,
	// total_events(count(*)) 와 duration 은 필터가 없어 mgmt 가 섞인다. **idle 구간에서는
	// mgmt 가 행의 대부분**이라 데이터 IO 의 분모가 통째로 흔들린다.
	where = cols.ExcludeMgmt(where)

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
		// `time` 은 **초** 단위다 (ftrace 는 커널 monotonic clock 의 sec.usec,
		// bpftrace 는 bpf_ktime_get_ns() 를 sec.usec 로 찍는다). 예전엔 여기서
		// 1000 으로 나눠 29초짜리 트레이스가 0.029s 로 나왔다 — UI 가 이 값을
		// 못 믿고 raw 이벤트로 직접 계산하는 우회를 두고 있었을 정도다
		// (AgentTraceResultSheet 의 rawDurationSeconds).
		// Rust 도 `max(time) - min(time)` 를 그대로 쓴다 (나누지 않는다).
		stats.DurationSeconds = maxTime.Float64 - minTime.Float64
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
	//
	// ⚠ 이 판정은 **쿼리 단위**라, fsio 잡과 ftrace 잡을 함께 조회하면 한쪽이 틀린다.
	// 행별로 가르려면 집계 SQL 자체를 바꿔야 하는데, 두 계열을 섞어 보는 것 자체가
	// 의미가 옅어서(단위·의미가 다른 값을 한 표에 합산) 그 조합은 **명시적으로 막고**
	// 계수는 단일 계열 기준으로 둔다. mixedFamily 는 위에서 검사한다.
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
		// F 로 시작하면 flush (rwbs 는 [RWDF] + flag [FSMA] 조합이라 'F' 가 **첫 자리**면
		// FLUSH, 뒤에 오면 FUA 다). flush 는 데이터를 안 나르므로 size 는 0 이고
		// read/write/discard 합산 대상이 아니다 — 단위 변환도 하지 않는다.
		// 이 분기가 없으면 실기기 수집마다 flush 행이 default 로 새서
		// "unknown trace cmd opcode" 경고가 쏟아진다.
		case strings.HasPrefix(cmd, "F"):
			// 합산 안 함, 단위 변환 안 함.
		default:
			// 분류 못한 cmd — UFS 단위 추정만 하고 합산은 보류.
			// 새 opcode 가 자주 보이면 위 switch 에 추가해야 한다.
			slog.Warn("unknown trace cmd opcode (not classified into read/write/discard)",
				"cmd", cs.Cmd, "count", cs.Count, "raw_size_total", cs.TotalSizeBytes)
			cs.TotalSizeBytes *= ufsUnit
		}
	}

	// 5.6 mgmt 집계 (fsio_ufs 전용).
	//
	// 데이터 IO 통계에서는 mgmt 를 빼지만(위 not_mgmt 조건), 링크 점유 시간은
	// 그 자체로 답해야 할 질문이라 별도 축으로 낸다. idle 구간에서는 이게 사실상
	// 유일한 산출물이다.
	if fsio.isUFS {
		stats.MgmtStats, err = queryMgmtStats(db, glob, where)
		if err != nil {
			return nil, fmt.Errorf("mgmt stats: %w", err)
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

	// count(col) 는 위 extraFilter(0 제외 + 사용자 필터)를 그대로 탄 뒤의 행 수다.
	// 화면이 건수를 따로 짐작하지 않아도 되도록 같은 쿼리에서 함께 얻는다 —
	// 별도 쿼리로 세면 필터가 어긋날 여지가 생긴다.
	q := fmt.Sprintf(`SELECT
		min(%s), max(%s), avg(%s), stddev_pop(%s),
		percentile_cont(0.5) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.999) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.9999) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.99999) WITHIN GROUP (ORDER BY %s),
		percentile_cont(0.999999) WITHIN GROUP (ORDER BY %s),
		count(%s)
	FROM read_parquet(%s) %s`,
		col, col, col, col, col, col, col, col, col, col, col, glob, extraFilter)

	ls := &pb.LatencyStats{}
	var minV, maxV, avgV, stdV, medV, p99, p999, p9999, p99999, p999999 sql.NullFloat64
	var cnt sql.NullInt64
	if err := db.QueryRow(q).Scan(&minV, &maxV, &avgV, &stdV, &medV, &p99, &p999, &p9999, &p99999, &p999999, &cnt); err != nil {
		return ls, err
	}
	if cnt.Valid {
		ls.Count = cnt.Int64
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

	// ⚠ latency 3종은 **모든 지표에** `> 0` 가드를 건다 (min 만이 아니라).
	//
	// dtoc/ctod/ctoc 가 0 인 행은 두 종류이고 **둘 다 "0ms" 가 아니라 "모름"** 이다:
	//   1. 짝의 반대편 행 — dtoc 는 complete 행에만, ctod 는 send 행에만 실린다.
	//   2. 미완결 IO — complete 를 끝내 못 받아 파서가 IsUnfinished 로 닫은 send
	//      (trace/parser/fsio_inflight.go). bpftrace 는 IRQ 재진입 가드 때문에
	//      complete 를 구조적으로 소수 놓치고, **IO 가 몰릴수록 그 비율이 오른다.**
	//
	// 이 0 들을 모수에 넣으면 avg/p99 가 아래로 끌려 내려간다 — 즉 **부하가 높을수록
	// latency 를 낮게 보고하는** 방향으로 조용히 틀린다. 실측으로 avg 가 정확히
	// 절반이 되는 경우를 확인했다 (TestFsioCmdStatsExcludesUnfinishedFromLatency).
	//
	// 예전엔 min 에만 `CASE WHEN dtoc > 0` 가드가 있었다. min 만 맞고 max/avg/stddev/
	// 백분위가 틀려서 표를 봐도 눈치채기 어려웠다.
	//
	// Rust 쪽도 동일하다 — `../trace/src/output/stats_rpc_duckdb.rs:713` 의
	// `dtoc_w = "action = '{comp}' AND dtoc > 0"` 가 모든 지표에 걸린다.
	// overview 경로(queryLatencyStats)도 같은 조건을 WHERE 로 이미 걸고 있다.
	//
	// FILTER 를 쓰는 이유 — 여기는 GROUP BY cmd 라 WHERE 로 걸면 latency 가 없는
	// cmd 의 행 자체가 사라져 count/ratio/size 까지 같이 틀어진다. 집계별로
	// 모수를 따로 거는 FILTER 가 맞다.

	q := fmt.Sprintf(`SELECT
		%s as cmd, count(*) as cnt,
		count(*) * 100.0 / sum(count(*)) OVER () as ratio,
		min(dtoc) FILTER (WHERE dtoc > 0), max(dtoc) FILTER (WHERE dtoc > 0),
		avg(dtoc) FILTER (WHERE dtoc > 0), stddev_pop(dtoc) FILTER (WHERE dtoc > 0),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY dtoc) FILTER (WHERE dtoc > 0),
		percentile_cont(0.999) WITHIN GROUP (ORDER BY dtoc) FILTER (WHERE dtoc > 0),
		min(ctod) FILTER (WHERE ctod > 0), max(ctod) FILTER (WHERE ctod > 0),
		avg(ctod) FILTER (WHERE ctod > 0), stddev_pop(ctod) FILTER (WHERE ctod > 0),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY ctod) FILTER (WHERE ctod > 0),
		percentile_cont(0.999) WITHIN GROUP (ORDER BY ctod) FILTER (WHERE ctod > 0),
		min(ctoc) FILTER (WHERE ctoc > 0), max(ctoc) FILTER (WHERE ctoc > 0),
		avg(ctoc) FILTER (WHERE ctoc > 0), stddev_pop(ctoc) FILTER (WHERE ctoc > 0),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY ctoc) FILTER (WHERE ctoc > 0),
		percentile_cont(0.999) WITHIN GROUP (ORDER BY ctoc) FILTER (WHERE ctoc > 0),
		min(qd), max(qd), avg(qd), stddev_pop(qd),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY qd),
		-- 각 latency 의 모수. 위 집계들과 **같은 FILTER** 를 걸어야 "이 수치가 몇 건
		-- 기준인지" 가 실제로 맞는다. count(*) 를 쓰면 0 인 행까지 세어 표가 거짓말한다.
		count(dtoc) FILTER (WHERE dtoc > 0),
		count(ctod) FILTER (WHERE ctod > 0),
		count(ctoc) FILTER (WHERE ctoc > 0),
		count(qd),
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
		var dtocCnt, ctodCnt, ctocCnt, qdCnt sql.NullInt64

		var contRatio sql.NullFloat64
		if err := rows.Scan(
			&cs.Cmd, &cs.Count, &cs.Ratio,
			&dtocMin, &dtocMax, &dtocAvg, &dtocStd, &dtocP99, &dtocP999,
			&ctodMin, &ctodMax, &ctodAvg, &ctodStd, &ctodP99, &ctodP999,
			&ctocMin, &ctocMax, &ctocAvg, &ctocStd, &ctocP99, &ctocP999,
			&qdMin, &qdMax, &qdAvg, &qdStd, &qdP99,
			&dtocCnt, &ctodCnt, &ctocCnt, &qdCnt,
			&cs.TotalSizeBytes,
			&cs.ContinuousCount, &contRatio,
			&cs.SendCount,
		); err != nil {
			return nil, err
		}
		assignNullable(cs.Dtoc, dtocMin, dtocMax, dtocAvg, dtocStd, dtocP99, dtocP999)
		assignNullable(cs.Ctod, ctodMin, ctodMax, ctodAvg, ctodStd, ctodP99, ctodP999)
		assignNullable(cs.Ctoc, ctocMin, ctocMax, ctocAvg, ctocStd, ctocP99, ctocP999)
		assignCount(cs.Dtoc, dtocCnt)
		assignCount(cs.Ctod, ctodCnt)
		assignCount(cs.Ctoc, ctocCnt)
		assignCount(cs.Qd, qdCnt)
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

// assignCount — 해당 통계의 모수. NULL(집계 대상 0건)이면 0 이 맞다.
func assignCount(ls *pb.LatencyStats, cnt sql.NullInt64) {
	if cnt.Valid {
		ls.Count = cnt.Int64
	}
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
	where := buildFilterWhereCols(filter, lbaCol, cmdCol, timeCol, filterPresentCols(db, glob))

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
	// fsio 계열의 컬럼식은 fsio_cols.go 가 정본이다.
	if c := newFsioCols(db, glob); c.schema.any() {
		// 데이터 IO 통계용 — mgmt 는 어차피 ExcludeMgmt 로 빠지므로 hex 만 쓴다.
		return c.CmdExpr(false)
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

// traceFamily — trace_type 이 속한 계열. 섞이면 단위·의미가 달라 합산이 성립하지 않는다.
//
//   - "fsio"   : bpftrace 산출물 (size 가 bytes, opcode 가 UInt8)
//   - "ftrace" : ufs/block/both/ufscustom (size 가 LBA/sector 단위, opcode 가 문자열)
func traceFamily(traceType string) string {
	if strings.HasPrefix(traceType, "fsio_") {
		return "fsio"
	}
	return "ftrace"
}

// checkMixedFamily — fsio 잡과 ftrace 잡을 함께 조회하려 하면 에러.
//
// 조용히 합치면 **틀린 숫자가 그럴듯하게 나온다**:
//   - size 계수가 쿼리 단위라 한쪽이 4096배 어긋난다
//   - union 으로 opcode 가 VARCHAR 로 승격돼 cmd 축이 깨진다
//
// 둘 다 에러가 아니라 그럴듯한 값이라 사용자가 알아채기 어렵다. 애초에 단위와
// 의미가 다른 값을 한 표에 합산하는 것 자체가 성립하지 않으므로 명시적으로 막는다.
func checkMixedFamily(infos []*TraceJobInfo) error {
	fams := make(map[string]bool)
	for _, info := range infos {
		fams[traceFamily(info.TraceType)] = true
	}
	if len(fams) > 1 {
		return fmt.Errorf("fsio 잡과 ftrace 잡은 함께 조회할 수 없습니다 — " +
			"size 단위(bytes vs LBA/sector)와 cmd 표현이 달라 합산이 성립하지 않습니다. " +
			"같은 계열끼리 선택하세요")
	}
	return nil
}

// filterCols — cross-layer 필터가 참조하는 컬럼. 존재 검사용.
var filterCols = []string{"comm", "pid", "syscall", "fs", "name", "ino", "lun",
	"devmajor", "devminor", "io_flags"}

// filterPresentCols — 이 parquet 에 있는 필터 대상 컬럼 집합.
func filterPresentCols(db *sql.DB, glob string) map[string]bool {
	return hasColumns(db, glob, filterCols...)
}

// sqlQuote — SQL 문자열 리터럴로 안전하게 감싼다.
//
// 값의 출처가 UI 선택지만이 아니다 — 파일명(name)과 프로세스명(comm)은 **기기 위
// 앱이 정하는 값**이라 작은따옴표가 들어올 수 있다. 이스케이프 없이 문자열을
// 이어붙이면 쿼리가 깨지거나(정상 케이스) SQL 이 주입된다(악성 케이스).
func sqlQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// quotedList — 문자열 목록을 IN (...) 절 값으로.
func quotedList(vs []string) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = sqlQuote(v)
	}
	return strings.Join(out, ",")
}

// parseMaskString — io_flags 마스크 문자열 → u64. 빈 문자열/파싱 실패는 0(미적용).
//
// 문자열로 받는 이유: u64 를 JSON number 로 실으면 2^53 넘는 f2fs 힌트 비트가
// 조용히 반올림된다.
func parseMaskString(s string) uint64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// hasStartTime / hasEndTime — 시간 범위가 설정됐는가.
//
// ⚠ proto3 scalar 라 "미설정" 과 "0" 을 구분하지 못한다. 여기서는 **0 = 미설정**
// 으로 본다 — 기존 UI(Time min/max 입력)가 빈 칸을 0 으로 보내오기 때문이다.
//
// 스텝 구간 분할에서 이게 문제가 되지 않는 이유: parquet `time` 은 트레이스 시작
// 기준 상대초가 아니라 **기기 부팅 기준 monotonic 절대초**다(파서가 TSV 0번 컬럼을
// 그대로 싣는다 — parser/fsio_line.go). 그래서 실제 구간 경계는 수천~수만 초대이지
// 0 근처가 아니다. 0 이 유효 경계가 되는 건 부팅 직후 트레이스뿐인데, 그 경우에도
// 하한 생략은 결과를 바꾸지 않는다(그 앞에 데이터가 없다).
//
// 만약 나중에 축을 트레이스 상대초로 바꾸면 이 전제가 깨진다 — 그때는 음수 센티넬이나
// optional 필드로 "미설정" 을 따로 표현해야 한다.
func hasStartTime(f *pb.TraceFilter) bool { return f.StartTime > 0 }
func hasEndTime(f *pb.TraceFilter) bool   { return f.EndTime > 0 }

// buildFilterWhereCols — TraceFilter → WHERE 절.
//
// present 는 이 parquet 에 실제로 있는 컬럼 집합이다. cross-layer 필터는 fsio 에만
// 있는 컬럼을 참조하므로, 없는 컬럼 조건은 **조용히 건너뛴다** — 넣으면 쿼리 자체가
// 깨져서 ftrace 산출물 조회가 통째로 실패한다. nil 이면 검사를 생략한다(하위 호환).
//
// timeCol 은 시간축 컬럼식(detectTimeColumn 결과). 빈 문자열이면 시간 조건을 건너뛴다
// — 예전엔 `time` 을 리터럴로 박아서 `start_time` 만 있는 스키마(UFSCUSTOM 등)에
// 시간 필터를 걸면 DuckDB Binder Error 로 조회가 통째로 깨졌다. lbaCol/cmdCol 이
// 이미 인자로 들어오는 것과 같은 이유다.
func buildFilterWhereCols(f *pb.TraceFilter, lbaCol, cmdCol, timeCol string, present map[string]bool) string {
	if f == nil {
		return ""
	}
	// has — 컬럼 존재 여부. present 가 nil 이면 전부 있다고 본다.
	has := func(col string) bool { return present == nil || present[col] }
	var conds []string
	// 시간 범위. 컬럼명은 감지된 것을 쓴다 (timeCol 주석 참고).
	// 0 을 "미설정" 으로 보는 근거는 hasStartTime/hasEndTime 에 적어 두었다.
	if timeCol != "" {
		if hasStartTime(f) {
			conds = append(conds, fmt.Sprintf("%s >= %f", timeCol, f.StartTime))
		}
		if hasEndTime(f) {
			conds = append(conds, fmt.Sprintf("%s <= %f", timeCol, f.EndTime))
		}
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
		conds = append(conds, fmt.Sprintf("%s IN (%s)", cmdCol, quotedList(f.CmdList)))
	}
	if len(f.SizeList) > 0 {
		vals := make([]string, len(f.SizeList))
		for i, v := range f.SizeList {
			vals[i] = fmt.Sprintf("%d", v)
		}
		conds = append(conds, fmt.Sprintf("size IN (%s)", strings.Join(vals, ",")))
	}
	if len(f.ActionList) > 0 {
		conds = append(conds, fmt.Sprintf("action IN (%s)", quotedList(f.ActionList)))
	}
	// ── fsio cross-layer 필터 ──
	// 없는 컬럼은 건너뛴다 (ftrace 산출물에서 쿼리가 깨지지 않게).
	if len(f.CommList) > 0 && has("comm") {
		conds = append(conds, fmt.Sprintf("comm IN (%s)", quotedList(f.CommList)))
	}
	if len(f.PidList) > 0 && has("pid") {
		vals := make([]string, len(f.PidList))
		for i, v := range f.PidList {
			vals[i] = fmt.Sprintf("%d", v)
		}
		conds = append(conds, fmt.Sprintf("pid IN (%s)", strings.Join(vals, ",")))
	}
	if len(f.SyscallList) > 0 && has("syscall") {
		conds = append(conds, fmt.Sprintf("syscall IN (%s)", quotedList(f.SyscallList)))
	}
	if len(f.FsList) > 0 && has("fs") {
		conds = append(conds, fmt.Sprintf("fs IN (%s)", quotedList(f.FsList)))
	}
	if len(f.NameList) > 0 && has("name") {
		conds = append(conds, fmt.Sprintf("name IN (%s)", quotedList(f.NameList)))
	}
	if f.NameContains != "" && has("name") {
		// LIKE 특수문자(%, _)도 리터럴로 취급 — 파일명에 흔히 들어간다.
		esc := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(f.NameContains)
		conds = append(conds, fmt.Sprintf("name LIKE %s ESCAPE '\\'", sqlQuote("%"+esc+"%")))
	}
	if len(f.InoList) > 0 && has("ino") {
		vals := make([]string, len(f.InoList))
		for i, v := range f.InoList {
			vals[i] = fmt.Sprintf("%d", v)
		}
		conds = append(conds, fmt.Sprintf("ino IN (%s)", strings.Join(vals, ",")))
	}
	if len(f.LunList) > 0 && has("lun") {
		vals := make([]string, len(f.LunList))
		for i, v := range f.LunList {
			vals[i] = fmt.Sprintf("%d", v)
		}
		conds = append(conds, fmt.Sprintf("lun IN (%s)", strings.Join(vals, ",")))
	}
	if len(f.DevList) > 0 && has("devmajor") && has("devminor") {
		// "major:minor" 문자열 — 두 컬럼을 이어 비교한다.
		conds = append(conds, fmt.Sprintf(
			"(CAST(devmajor AS VARCHAR) || ':' || CAST(devminor AS VARCHAR)) IN (%s)",
			quotedList(f.DevList)))
	}
	if has("io_flags") {
		if m := parseMaskString(f.IoFlagsAny); m != 0 {
			conds = append(conds, fmt.Sprintf("(io_flags & %d) != 0", m))
		}
		if m := parseMaskString(f.IoFlagsAll); m != 0 {
			conds = append(conds, fmt.Sprintf("(io_flags & %d) = %d", m, m))
		}
		if m := parseMaskString(f.IoFlagsNone); m != 0 {
			conds = append(conds, fmt.Sprintf("(io_flags & %d) = 0", m))
		}
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
