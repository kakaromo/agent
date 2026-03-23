package trace

import (
	"database/sql"
	"fmt"

	pb "agent/pb"

	_ "github.com/marcboeker/go-duckdb"
)

const maxEvents = 500000

// GetRawData returns trace events, sampling if over maxEvents.
func GetRawData(dirs []string, filter *pb.TraceFilter) (*pb.GetTraceRawDataResponse, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	glob := buildGlobList(dirs)
	where := buildFilterWhere(filter)
	cmdCol := detectCmdColumn(db, glob)
	lbaCol := detectLbaColumn(db, glob)

	// Count total events
	q := fmt.Sprintf("SELECT count(*) FROM read_parquet(%s) %s", glob, where)
	var total int64
	if err := db.QueryRow(q).Scan(&total); err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	resp := &pb.GetTraceRawDataResponse{
		TotalEvents: total,
	}

	if total == 0 {
		return resp, nil
	}

	if total <= maxEvents {
		// No sampling needed
		events, err := queryAllEvents(db, glob, where, cmdCol, lbaCol)
		if err != nil {
			return nil, err
		}
		resp.Events = events
		resp.SampledEvents = int64(len(events))
		resp.IsSampled = false
		return resp, nil
	}

	// Sampling required
	events, err := querySampledEvents(db, glob, where, cmdCol, lbaCol, total)
	if err != nil {
		return nil, err
	}
	resp.Events = events
	resp.SampledEvents = int64(len(events))
	resp.IsSampled = true
	return resp, nil
}

func queryAllEvents(db *sql.DB, glob, where, cmdCol, lbaCol string) ([]*pb.TraceEvent, error) {
	q := fmt.Sprintf(`SELECT time, %s, qd, cpu, dtoc, ctod, ctoc, %s, size, continuous
		FROM read_parquet(%s) %s ORDER BY time`, lbaCol, cmdCol, glob, where)
	return scanEvents(db, q)
}

func querySampledEvents(db *sql.DB, glob, where, cmdCol, lbaCol string, total int64) ([]*pb.TraceEvent, error) {
	// Strategy:
	// 1. Divide time range into buckets
	// 2. From each bucket, pick min/max rows for lba, qd, dtoc, ctod, ctoc
	// 3. Fill remaining slots with uniform sampling
	// Use a single SQL with UNION ALL approach

	filterCond := where
	if filterCond == "" {
		filterCond = "WHERE 1=1"
	}

	// The key insight: we use row_number + modulo for uniform sampling,
	// then UNION with min/max extremes per time bucket, then DISTINCT + LIMIT
	q := fmt.Sprintf(`
WITH base AS (
  SELECT *, row_number() OVER (ORDER BY time) as rn,
    NTILE(%d) OVER (ORDER BY time) as bucket
  FROM read_parquet(%s) %s
),
-- Min/max events per bucket (must include)
extremes AS (
  SELECT DISTINCT ON (bucket, metric) rn FROM (
    SELECT bucket, rn, 'lba_min' as metric, %s as val, rank() OVER (PARTITION BY bucket ORDER BY %s ASC) as r FROM base
    UNION ALL
    SELECT bucket, rn, 'lba_max', %s, rank() OVER (PARTITION BY bucket ORDER BY %s DESC) FROM base
    UNION ALL
    SELECT bucket, rn, 'qd_min', qd, rank() OVER (PARTITION BY bucket ORDER BY qd ASC) FROM base
    UNION ALL
    SELECT bucket, rn, 'qd_max', qd, rank() OVER (PARTITION BY bucket ORDER BY qd DESC) FROM base
    UNION ALL
    SELECT bucket, rn, 'dtoc_max', dtoc, rank() OVER (PARTITION BY bucket ORDER BY dtoc DESC) FROM base
    UNION ALL
    SELECT bucket, rn, 'dtoc_min', dtoc, rank() OVER (PARTITION BY bucket ORDER BY dtoc ASC) FROM base WHERE dtoc > 0
    UNION ALL
    SELECT bucket, rn, 'ctod_max', ctod, rank() OVER (PARTITION BY bucket ORDER BY ctod DESC) FROM base
    UNION ALL
    SELECT bucket, rn, 'ctod_min', ctod, rank() OVER (PARTITION BY bucket ORDER BY ctod ASC) FROM base WHERE ctod > 0
    UNION ALL
    SELECT bucket, rn, 'ctoc_max', ctoc, rank() OVER (PARTITION BY bucket ORDER BY ctoc DESC) FROM base
    UNION ALL
    SELECT bucket, rn, 'ctoc_min', ctoc, rank() OVER (PARTITION BY bucket ORDER BY ctoc ASC) FROM base WHERE ctoc > 0
  ) sub WHERE r = 1
),
-- Uniform sampling for remaining slots
sampled AS (
  SELECT rn FROM base WHERE rn %% %d = 0
),
-- Combine
combined AS (
  SELECT rn FROM extremes
  UNION
  SELECT rn FROM sampled
)
SELECT b.time, b.%s, b.qd, b.cpu, b.dtoc, b.ctod, b.ctoc, b.%s, b.size, b.continuous
FROM base b
JOIN combined c ON b.rn = c.rn
ORDER BY b.time
LIMIT %d`,
		maxEvents/10, // ~50k buckets for extremes
		glob, filterCond,
		lbaCol, lbaCol, lbaCol, lbaCol,
		int(total)/maxEvents+1, // modulo for uniform sampling
		lbaCol, cmdCol,
		maxEvents)

	return scanEvents(db, q)
}

func scanEvents(db *sql.DB, q string) ([]*pb.TraceEvent, error) {
	rows, err := db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []*pb.TraceEvent
	for rows.Next() {
		e := &pb.TraceEvent{}
		if err := rows.Scan(&e.Time, &e.Lba, &e.Qd, &e.Cpu, &e.Dtoc, &e.Ctod, &e.Ctoc, &e.Cmd, &e.Size, &e.Continuous); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, e)
	}
	return events, nil
}

func detectLbaColumn(db *sql.DB, glob string) string {
	q := fmt.Sprintf(`SELECT column_name FROM (DESCRIBE SELECT * FROM read_parquet(%s) LIMIT 0)
		WHERE column_name IN ('lba', 'sector')`, glob)
	rows, err := db.Query(q)
	if err != nil {
		return "lba"
	}
	defer rows.Close()

	hasLba := false
	hasSector := false
	for rows.Next() {
		var col string
		rows.Scan(&col)
		if col == "lba" {
			hasLba = true
		}
		if col == "sector" {
			hasSector = true
		}
	}
	if hasLba && hasSector {
		return "COALESCE(lba, sector)"
	}
	if hasLba {
		return "lba"
	}
	if hasSector {
		return "sector"
	}
	return "lba"
}
