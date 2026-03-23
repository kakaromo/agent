package trace

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	pb "agent/pb"

	_ "github.com/marcboeker/go-duckdb"
)

var defaultLatencyRanges = []float64{0.1, 0.5, 1, 5, 10, 50, 100, 500, 1000}

// buildGlobList builds a DuckDB-compatible glob list from multiple directories.
// Uses union_by_name=true to handle mixed schemas (ufs vs block parquet).
func buildGlobList(dirs []string) string {
	if len(dirs) == 1 {
		return fmt.Sprintf("'%s', union_by_name=true", filepath.Join(dirs[0], "*.parquet"))
	}
	var parts []string
	for _, d := range dirs {
		parts = append(parts, fmt.Sprintf("'%s'", filepath.Join(d, "*.parquet")))
	}
	return "[" + strings.Join(parts, ", ") + "], union_by_name=true"
}

// ComputeStats computes trace statistics from parquet files across multiple directories.
func ComputeStats(dirs []string, filter *pb.TraceFilter, customRanges []float64) (*pb.TraceStats, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	glob := buildGlobList(dirs)
	where := buildFilterWhere(filter)

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

	// 6. Cmd + size counts
	stats.CmdSizeCounts, err = queryCmdSizeCounts(db, glob, where)
	if err != nil {
		return nil, fmt.Errorf("cmd size counts: %w", err)
	}

	// 7. Continuity stats (aligned may not exist in all schemas)
	q = fmt.Sprintf(`SELECT
		count(*) FILTER (WHERE continuous = true),
		count(*) FILTER (WHERE continuous = true) * 100.0 / count(*)
	FROM read_parquet(%s) %s`, glob, where)
	db.QueryRow(q).Scan(&stats.ContinuousCount, &stats.ContinuousRatio)

	// Try aligned stats separately (column may not exist)
	q = fmt.Sprintf(`SELECT
		count(*) FILTER (WHERE aligned = true),
		count(*) FILTER (WHERE aligned = true) * 100.0 / count(*)
	FROM read_parquet(%s) %s`, glob, where)
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
		percentile_cont(0.99) WITHIN GROUP (ORDER BY qd)
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

		if err := rows.Scan(
			&cs.Cmd, &cs.Count, &cs.Ratio,
			&dtocMin, &dtocMax, &dtocAvg, &dtocStd, &dtocP99, &dtocP999,
			&ctodMin, &ctodMax, &ctodAvg, &ctodStd, &ctodP99, &ctodP999,
			&ctocMin, &ctocMax, &ctocAvg, &ctocStd, &ctocP99, &ctocP999,
			&qdMin, &qdMax, &qdAvg, &qdStd, &qdP99,
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

// ==================== Helpers ====================

func detectCmdColumn(db *sql.DB, glob string) string {
	// With union_by_name=true, both columns may exist (NULL for missing rows).
	// Check which columns are present.
	q := fmt.Sprintf(`SELECT column_name FROM (DESCRIBE SELECT * FROM read_parquet(%s) LIMIT 0)
		WHERE column_name IN ('opcode', 'io_type')`, glob)
	rows, err := db.Query(q)
	if err != nil {
		return "opcode"
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		rows.Scan(&col)
		cols = append(cols, col)
	}

	// Both exist (mixed ufs+block) → use COALESCE
	hasOpcode := false
	hasIoType := false
	for _, c := range cols {
		if c == "opcode" {
			hasOpcode = true
		}
		if c == "io_type" {
			hasIoType = true
		}
	}
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

func buildFilterWhere(f *pb.TraceFilter) string {
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
		conds = append(conds, fmt.Sprintf("COALESCE(lba, sector) >= %d", f.StartLba))
	}
	if f.EndLba > 0 {
		conds = append(conds, fmt.Sprintf("COALESCE(lba, sector) <= %d", f.EndLba))
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
		conds = append(conds, fmt.Sprintf("COALESCE(opcode, io_type) IN (%s)", strings.Join(vals, ",")))
	}
	if len(f.SizeList) > 0 {
		vals := make([]string, len(f.SizeList))
		for i, v := range f.SizeList {
			vals[i] = fmt.Sprintf("%d", v)
		}
		conds = append(conds, fmt.Sprintf("size IN (%s)", strings.Join(vals, ",")))
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
