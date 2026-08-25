package trace

import (
	"database/sql"
	"fmt"
	"strings"

	pb "agent/pb"

	_ "github.com/marcboeker/go-duckdb/v2"
)

const maxEvents = 500000

// maxEventsForTest — 샘플링 경로를 테스트에서 강제하기 위한 훅.
// 운영에서는 maxEvents 와 같다.
var maxEventsForTest = maxEvents

// GetRawData returns trace events, sampling if over maxEvents.
func GetRawData(infos []*TraceJobInfo, filter *pb.TraceFilter) (*pb.GetTraceRawDataResponse, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	defer db.Close()

	if err := checkMixedFamily(infos); err != nil {
		return nil, err
	}

	glob := buildGlobList(infos)
	cmdCol := detectCmdColumn(db, glob)
	lbaCol := detectLbaColumn(db, glob)
	// fsio 산출물이면 cross-layer 컬럼을 함께 싣는다 — Raw Data 표가 "이 IO 를 누가/
	// 어느 파일에" 를 행 단위로 보여주기 위해 필요하다. ftrace 는 기존 11컬럼 그대로.
	fsio := detectFsioSchema(db, glob)
	where := buildFilterWhereCols(filter, lbaCol, cmdCol, filterPresentCols(db, glob))

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

	if total <= int64(maxEventsForTest) {
		// No sampling needed
		events, err := queryAllEvents(db, glob, where, cmdCol, lbaCol, fsio)
		if err != nil {
			return nil, err
		}
		resp.Events = events
		resp.SampledEvents = int64(len(events))
		resp.IsSampled = false
		return resp, nil
	}

	// Sampling required
	events, err := querySampledEvents(db, glob, where, cmdCol, lbaCol, total, fsio)
	if err != nil {
		return nil, err
	}
	resp.Events = events
	resp.SampledEvents = int64(len(events))
	resp.IsSampled = true
	return resp, nil
}

func queryAllEvents(db *sql.DB, glob, where, cmdCol, lbaCol string, fsio fsioSchema) ([]*pb.TraceEvent, error) {
	q := fmt.Sprintf(`SELECT time, %s, qd, cpu, dtoc, ctod, ctoc, %s, size, continuous, action%s
		FROM read_parquet(%s) %s ORDER BY time`,
		lbaCol, cmdCol, fsioExtraSelect(fsio), glob, where)
	if fsio.any() {
		return scanEventsFsio(db, q, fsio)
	}
	return scanEvents(db, q)
}

func querySampledEvents(db *sql.DB, glob, where, cmdCol, lbaCol string, total int64, fsio fsioSchema) ([]*pb.TraceEvent, error) {
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
SELECT b.time, %s, b.qd, b.cpu, b.dtoc, b.ctod, b.ctoc, %s, b.size, b.continuous, b.action%s
FROM base b
JOIN combined c ON b.rn = c.rn
ORDER BY b.time
LIMIT %d`,
		maxEvents/10, // ~50k buckets for extremes
		glob, filterCond,
		lbaCol, lbaCol, lbaCol, lbaCol,
		int(total)/maxEvents+1, // modulo for uniform sampling
		lbaCol, cmdCol, fsioExtraSelectPrefixed(fsio, "b."),
		maxEvents)

	if fsio.any() {
		return scanEventsFsio(db, q, fsio)
	}
	return scanEvents(db, q)
}

// fsioExtraSelectPrefixed — 샘플링 쿼리처럼 테이블 별칭이 필요한 곳용.
func fsioExtraSelectPrefixed(f fsioSchema, prefix string) string {
	cols := fsioExtraColsFor(f)
	if len(cols) == 0 {
		return ""
	}
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = prefix + c
	}
	return ", " + strings.Join(out, ", ")
}

// fsioExtraCols — fsio 산출물에만 있는 확장 컬럼. Raw Data 표가 "이 IO 를 누가/
// 어느 파일에" 를 행 단위로 보여주기 위해 필요하다.
//
// 39개 is_* 불리언은 싣지 않는다 — io_flags 원본 하나면 클라이언트가 같은 비트
// 정의로 풀 수 있고, 전송량도 작고 비트가 늘어도 안 깨진다.
//
// 순서는 scanEventsFsio 의 Scan 순서와 **정확히 일치해야 한다** (positional scan).
var fsioUfsExtraCols = []string{
	"aligned", "line_number", "pid", "tid", "comm", "syscall", "fs", "ino", "name", "io_flags",
	"tag", "opcode", "lun", "groupid", "hwqid",
	"txn", "upiu_flags", "upiu_func", "upiu_attr", "upiu_cp",
}

var fsioBlockExtraCols = []string{
	"aligned", "line_number", "pid", "tid", "comm", "syscall", "fs", "ino", "name", "io_flags",
	"devmajor", "devminor", "rwbs", "flags", "extra",
}

// fsioExtraSelect — 확장 컬럼의 SELECT 절 조각. fsio 가 아니면 빈 문자열.
func fsioExtraSelect(f fsioSchema) string {
	cols := fsioExtraColsFor(f)
	if len(cols) == 0 {
		return ""
	}
	return ", " + strings.Join(cols, ", ")
}

func fsioExtraColsFor(f fsioSchema) []string {
	switch {
	case f.isUFS:
		return fsioUfsExtraCols
	case f.isBlock:
		return fsioBlockExtraCols
	default:
		return nil
	}
}

// scanEventsFsio — 기본 11컬럼 + fsio 확장 컬럼을 읽는다.
//
// nullable 컬럼(UPIU 헤더)은 sql.Null* 로 받아 **값이 없으면 proto 필드를 안 채운다**.
// 0 으로 채우면 "txn=0x00 인 요청" 과 "UPIU 를 못 얻은 행" 이 구분되지 않는다.
func scanEventsFsio(db *sql.DB, q string, f fsioSchema) ([]*pb.TraceEvent, error) {
	rows, err := db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []*pb.TraceEvent
	for rows.Next() {
		e := &pb.TraceEvent{}
		var action, comm, syscall, fsName, name sql.NullString
		var aligned sql.NullBool
		var lineNo, ino, ioFlags sql.NullInt64

		dest := []any{&e.Time, &e.Lba, &e.Qd, &e.Cpu, &e.Dtoc, &e.Ctod, &e.Ctoc,
			&e.Cmd, &e.Size, &e.Continuous, &action,
			&aligned, &lineNo, &e.Pid, &e.Tid, &comm, &syscall, &fsName, &ino, &name, &ioFlags}

		var tag, opcode, lun, groupid sql.NullInt64
		var hwqid sql.NullInt64
		var txn, upiuFlags, upiuFunc, upiuCp sql.NullInt64
		var upiuAttr sql.NullString
		var devmajor, devminor, extra sql.NullInt64
		var rwbs, flags sql.NullString

		if f.isUFS {
			dest = append(dest, &tag, &opcode, &lun, &groupid, &hwqid,
				&txn, &upiuFlags, &upiuFunc, &upiuAttr, &upiuCp)
		} else if f.isBlock {
			dest = append(dest, &devmajor, &devminor, &rwbs, &flags, &extra)
		}

		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan fsio event: %w", err)
		}

		e.Action = action.String
		e.Aligned = aligned.Bool
		e.LineNumber = uint64(lineNo.Int64)
		e.Comm = comm.String
		e.Syscall = syscall.String
		e.Fs = fsName.String
		e.Ino = uint64(ino.Int64)
		e.Name = name.String
		e.IoFlags = uint64(ioFlags.Int64)

		if f.isUFS {
			e.Tag = uint32(tag.Int64)
			e.Opcode = uint32(opcode.Int64)
			e.Lun = uint32(lun.Int64)
			e.Groupid = uint32(groupid.Int64)
			e.Hwqid = int32(hwqid.Int64)
			e.UpiuAttr = upiuAttr.String
			// UPIU 헤더는 없으면 안 채운다 (0 과 "없음" 을 구분).
			if txn.Valid {
				v := uint32(txn.Int64)
				e.Txn = &v
			}
			if upiuFlags.Valid {
				v := uint32(upiuFlags.Int64)
				e.UpiuFlags = &v
			}
			if upiuFunc.Valid {
				v := uint32(upiuFunc.Int64)
				e.UpiuFunc = &v
			}
			if upiuCp.Valid {
				v := uint32(upiuCp.Int64)
				e.UpiuCp = &v
			}
		} else if f.isBlock {
			e.Devmajor = uint32(devmajor.Int64)
			e.Devminor = uint32(devminor.Int64)
			e.Rwbs = rwbs.String
			e.Flags = flags.String
			e.Extra = uint32(extra.Int64)
		}

		events = append(events, e)
	}
	return events, nil
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
		var action sql.NullString
		if err := rows.Scan(&e.Time, &e.Lba, &e.Qd, &e.Cpu, &e.Dtoc, &e.Ctod, &e.Ctoc, &e.Cmd, &e.Size, &e.Continuous, &action); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if action.Valid {
			e.Action = action.String
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
