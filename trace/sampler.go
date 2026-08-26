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
//
// ⚠ 샘플링 쿼리의 **나눗수·버킷 수·LIMIT 이 전부 이 값을 따른다.** 예전엔
// 임계값만 훅이고 나머지는 maxEvents 상수를 직접 썼는데, 그러면 테스트가
// 샘플링 경로에 들어가도 나눗수가 1 이라 **아무것도 솎이지 않아서** 솎기
// 동작을 검증할 수 없었다 (통과하지만 아무것도 증명 못 하는 테스트).
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
	timeCol := detectTimeColumn(db, glob)
	lbaCol := detectLbaColumn(db, glob)
	// fsio 산출물이면 cross-layer 컬럼을 함께 싣는다 — Raw Data 표가 "이 IO 를 누가/
	// 어느 파일에" 를 행 단위로 보여주기 위해 필요하다. ftrace 는 기존 11컬럼 그대로.
	cols := newFsioCols(db, glob)
	where := buildFilterWhereCols(filter, lbaCol, cmdCol, timeCol, filterPresentCols(db, glob))

	// ⚠ Raw Data 는 **mgmt 행을 일부러 남긴다.** 통계와 반대다.
	//
	// 통계는 데이터 IO 의 모수를 지키려고 mgmt 를 빼지만, Raw Data 는 "그 시각에
	// 무슨 일이 있었나" 를 보는 화면이라 hibern8 이 도는 동안 IO 가 멈춘 게 같은
	// 타임라인에 보여야 한다. 대신 아래 두 가지를 해 준다:
	//   - cmd 를 mgmt_name 으로 바꿔 종류가 `0x00` 한 덩어리로 뭉치지 않게
	//   - lba/size/qd 를 NULL 로 만들어 Y=0 가짜 가로줄이 안 생기게

	// Count total events
	q := fmt.Sprintf("SELECT count(*) FROM read_parquet(%s) %s", glob, where)
	var total int64
	if err := db.QueryRow(q).Scan(&total); err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	resp := &pb.GetTraceRawDataResponse{
		TotalEvents: total,
		// 프론트가 컬럼 세트/UI 노출을 정하는 데 쓴다. 단독 trace 실행에는
		// mappings 가 없어 이 값 말고는 타입을 알 길이 없다.
		TraceType: primaryTraceType(infos),
	}

	if total == 0 {
		return resp, nil
	}

	if total <= int64(maxEventsForTest) {
		// No sampling needed
		events, err := queryAllEvents(db, glob, where, cols, cmdCol, lbaCol)
		if err != nil {
			return nil, err
		}
		resp.Events = events
		resp.SampledEvents = int64(len(events))
		resp.IsSampled = false
		return resp, nil
	}

	// Sampling required
	events, err := querySampledEvents(db, glob, where, cols, cmdCol, lbaCol, total)
	if err != nil {
		return nil, err
	}
	resp.Events = events
	resp.SampledEvents = int64(len(events))
	resp.IsSampled = true
	return resp, nil
}

func queryAllEvents(db *sql.DB, glob, where string, cols fsioCols, cmdCol, lbaCol string) ([]*pb.TraceEvent, error) {
	fsio := cols.schema
	q := fmt.Sprintf(`SELECT time, %s, %s, cpu, dtoc, ctod, ctoc, %s, %s, continuous, action%s
		FROM read_parquet(%s) %s ORDER BY time`,
		cols.rawLbaExpr(lbaCol, ""), cols.mgmtNullExpr("qd", "UINTEGER", ""),
		cols.rawCmdExpr(cmdCol, ""), cols.mgmtNullExpr("size", "UINTEGER", ""),
		fsioExtraSelect(fsio), glob, where)
	if fsio.any() {
		return scanEventsFsio(db, q, fsio)
	}
	return scanEvents(db, q)
}

func querySampledEvents(db *sql.DB, glob, where string, cols fsioCols, cmdCol, lbaCol string, total int64) ([]*pb.TraceEvent, error) {
	fsio := cols.schema
	// Strategy:
	// 1. Divide time range into buckets
	// 2. From each bucket, pick min/max rows for lba, qd, dtoc, ctod, ctoc
	// 3. Fill remaining slots with uniform sampling
	// Use a single SQL with UNION ALL approach

	filterCond := where
	if filterCond == "" {
		filterCond = "WHERE 1=1"
	}

	// ⚠ mgmt 와 데이터 IO 를 **각자 예산으로** 샘플링한다.
	//
	// rn 은 전체 행에 걸친 하나의 연속 번호라, uniform 샘플링(`rn %% N = 0`)이
	// 두 그룹에 **행 수 비율대로** 표본을 나눠 준다. idle 구간처럼 mgmt 가 행의
	// 90%% 인 트레이스에서는 표본의 90%% 를 mgmt 가 가져가고, 정작 드문 데이터 IO 가
	// 차트에서 사라진다. 반대로 IO 폭주 구간에서는 mgmt 가 통째로 없어져
	// "hibern8 이 안 돌았다" 로 잘못 읽힌다.
	//
	// PARTITION BY 로 rn 을 그룹별로 따로 매기면 각 그룹이 독립적으로 1/N 로
	// 솎이므로 둘 다 온전한 해상도로 남는다. mgmt 가 없는 스키마에서는
	// grpExpr 이 상수라 기존 동작과 완전히 같다.
	//
	// ⚠ LIMIT 은 time 순 정렬 뒤에 걸리므로 한쪽이 통째로 잘리지는 않는다
	// (Rust 는 그룹별 결과를 이어 붙인 뒤 다시 target 으로 깎아서 뒤에 붙은 mgmt 가
	//  전멸하는 문제가 있었다 — chart_rpc_duckdb.rs:268 참고. 여기 구조는 그 함정이
	//  없지만, 대신 총량이 예산의 최대 2배가 될 수 있어 maxEvents 로 상한을 둔다).
	//
	// 정본: Rust `../trace/src/output/chart_rpc_duckdb.rs` 의 decimate_split_mgmt.
	grpExpr := "0"
	groups := 1
	if cols.schema.isUFS {
		grpExpr = "CASE WHEN COALESCE(is_mgmt, FALSE) THEN 1 ELSE 0 END"
		groups = 2
	}
	// 그룹당 예산 — 전체 예산을 그룹 수로 나눈다. 두 그룹이 각자 이 예산에 맞춰
	// 솎이므로 합계는 대략 maxEventsForTest 를 유지한다.
	groupBudget := maxEventsForTest / groups
	if groupBudget < 1 {
		groupBudget = 1
	}

	// The key insight: we use row_number + modulo for uniform sampling,
	// then UNION with min/max extremes per time bucket, then DISTINCT + LIMIT
	q := fmt.Sprintf(`
WITH base AS (
  SELECT *, row_number() OVER (PARTITION BY %s ORDER BY time) as rn,
    %s as grp,
    -- ⚠ 나눗수는 **그룹마다 따로** 계산한다.
    --
    -- 합계 기준 하나로 쓰면 소수 그룹이 통째로 빠진다: 데이터 IO 20행에
    -- 나눗수 21 이면 rn %% 21 = 0 이 **한 번도 안 맞아 0행**이 된다.
    -- 그룹 크기에 비례한 나눗수를 써야 각 그룹이 자기 안에서 1/N 로 솎인다.
    -- greatest(...,1) — 그룹이 예산보다 작으면 나눗수 1(전량 유지).
    --
    -- ⚠⚠ **정수 나눗셈(//) 이어야 한다.** DuckDB 의 / 는 DOUBLE 나눗셈이라
    -- 800000/500000 = 1.6 이 되고, rn %% 1.6 = 0 은 정수 rn 에 **거의 안 맞아
    -- 표본이 0행**이 된다. 게다가 그룹마다 소수부가 달라 결과가 뒤집힌다 —
    -- 실측: 데이터 IO 900k(div 3.6)→0행, mgmt 100k(div 0.4→greatest→1.0)→전량.
    -- 즉 mgmt 를 살리려던 분리가 **데이터 IO 를 전멸시키는** 정반대 결과가 됐다.
    -- (초기 테스트는 2000/50, 20/50 이 딱 떨어지는 값이라 통과했다.)
    greatest(count(*) OVER (PARTITION BY %s) // %d, 1) as grp_div,
    NTILE(%d) OVER (ORDER BY time) as bucket
  FROM read_parquet(%s) %s
),
-- Min/max events per bucket (must include)
-- ⚠ rn 은 grp 안에서만 유일하다 (위 PARTITION BY). 아래 모든 곳에서
-- **(grp, rn) 을 한 쌍으로** 다뤄야 한다. rn 만 쓰면 두 그룹의 같은 번호가
-- 섞여 엉뚱한 행이 딸려 온다.
extremes AS (
  SELECT DISTINCT ON (bucket, metric) grp, rn FROM (
    SELECT bucket, grp, rn, 'lba_min' as metric, %s as val, rank() OVER (PARTITION BY bucket ORDER BY %s ASC) as r FROM base
    UNION ALL
    SELECT bucket, grp, rn, 'lba_max', %s, rank() OVER (PARTITION BY bucket ORDER BY %s DESC) FROM base
    UNION ALL
    SELECT bucket, grp, rn, 'qd_min', qd, rank() OVER (PARTITION BY bucket ORDER BY qd ASC) FROM base
    UNION ALL
    SELECT bucket, grp, rn, 'qd_max', qd, rank() OVER (PARTITION BY bucket ORDER BY qd DESC) FROM base
    UNION ALL
    SELECT bucket, grp, rn, 'dtoc_max', dtoc, rank() OVER (PARTITION BY bucket ORDER BY dtoc DESC) FROM base
    UNION ALL
    SELECT bucket, grp, rn, 'dtoc_min', dtoc, rank() OVER (PARTITION BY bucket ORDER BY dtoc ASC) FROM base WHERE dtoc > 0
    UNION ALL
    SELECT bucket, grp, rn, 'ctod_max', ctod, rank() OVER (PARTITION BY bucket ORDER BY ctod DESC) FROM base
    UNION ALL
    SELECT bucket, grp, rn, 'ctod_min', ctod, rank() OVER (PARTITION BY bucket ORDER BY ctod ASC) FROM base WHERE ctod > 0
    UNION ALL
    SELECT bucket, grp, rn, 'ctoc_max', ctoc, rank() OVER (PARTITION BY bucket ORDER BY ctoc DESC) FROM base
    UNION ALL
    SELECT bucket, grp, rn, 'ctoc_min', ctoc, rank() OVER (PARTITION BY bucket ORDER BY ctoc ASC) FROM base WHERE ctoc > 0
  ) sub WHERE r = 1
),
-- Uniform sampling for remaining slots.
-- rn 이 grp 별로 1부터 다시 시작하므로, 이 modulo 는 **각 그룹을 독립적으로**
-- 1/N 로 솎는다 — 이게 분리 샘플링의 핵심이다.
sampled AS (
  SELECT grp, rn FROM base WHERE rn %% grp_div = 0
),
-- Combine
combined AS (
  SELECT grp, rn FROM extremes
  UNION
  SELECT grp, rn FROM sampled
)
SELECT b.time, %s, %s, b.cpu, b.dtoc, b.ctod, b.ctoc, %s, %s, b.continuous, b.action%s
FROM base b
JOIN combined c ON b.grp = c.grp AND b.rn = c.rn
ORDER BY b.time
LIMIT %d`,
		grpExpr,               // PARTITION BY — 그룹별로 rn 을 따로 매긴다
		grpExpr,               // grp 컬럼
		grpExpr,               // grp_div 의 PARTITION BY (그룹 크기)
		groupBudget,           // 그룹당 표본 예산
		maxEventsForTest/10+1, // ~50k buckets for extremes (테스트에선 훅을 따른다)
		glob, filterCond,
		lbaCol, lbaCol, lbaCol, lbaCol,
		cols.rawLbaExpr(detectLbaColumnPrefixed(db, glob, "b."), "b."), cols.mgmtNullExpr("b.qd", "UINTEGER", "b."),
		cols.rawCmdExpr(cmdCol, "b."), cols.mgmtNullExpr("b.size", "UINTEGER", "b."),
		fsioExtraSelectPrefixed(fsio, "b."),
		maxEventsForTest)

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

// primaryTraceType — 조회 대상의 대표 trace_type.
// 여러 잡을 합쳐 조회해도 계열 혼합은 막혀 있으므로(checkMixedFamily) 첫 값이면 충분하다.
func primaryTraceType(infos []*TraceJobInfo) string {
	for _, i := range infos {
		if i.TraceType != "" {
			return i.TraceType
		}
	}
	return ""
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
	// mgmt 원본값 — cmd 에는 이미 mgmt_name 이 들어가지만, Query 가 어느 IDN 을
	// 건드렸는지 / TM 이 성공했는지(resp/status)는 이름만으로 알 수 없다.
	"is_mgmt", "mgmt_name",
	"upiu_resp", "upiu_status",
	"query_opcode", "query_idn", "query_index", "query_selector", "uic_cmd",
	// 미완결 IO — 이 행의 dtoc=0 은 "0ms" 가 아니라 "모름" 이다.
	"is_unfinished",
}

var fsioBlockExtraCols = []string{
	"aligned", "line_number", "pid", "tid", "comm", "syscall", "fs", "ino", "name", "io_flags",
	"devmajor", "devminor", "rwbs", "flags", "extra",
	// block 도 미완결 IO 가 있다 — (dev, sector, rwbs) 는 재사용 신호가 없어
	// 시간 만료로 닫는다 (trace/parser/fsio_block.go). UFS 쪽만 넣고 여기를
	// 빠뜨리면 Raw Data 의 "unfin" 열이 **항상 빈칸**이라, DtoC 0 인 미완결 행이
	// "엄청 빠른 IO" 로 읽힌다 — UFS 에서 막으려던 바로 그 오독이다.
	"is_unfinished",
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
		// lba/qd/size 는 mgmt 행에서 NULL 로 온다 (fsio_cols.go 의 mgmtNullExpr).
		// proto 필드에 직접 스캔하면 "converting NULL to uint64 is unsupported"
		// 로 터지므로 Null* 로 받아 값이 있을 때만 채운다. 안 채우면 0 인데,
		// **여기서의 0 은 wire 상 필드 부재와 같아** 클라이언트가 구분할 수 있다.
		var lba, qd, size sql.NullInt64

		dest := []any{&e.Time, &lba, &qd, &e.Cpu, &e.Dtoc, &e.Ctod, &e.Ctoc,
			&e.Cmd, &size, &e.Continuous, &action,
			&aligned, &lineNo, &e.Pid, &e.Tid, &comm, &syscall, &fsName, &ino, &name, &ioFlags}

		var tag, opcode, lun, groupid sql.NullInt64
		var hwqid sql.NullInt64
		var txn, upiuFlags, upiuFunc, upiuCp sql.NullInt64
		var upiuAttr sql.NullString
		var devmajor, devminor, extra sql.NullInt64
		var rwbs, flags sql.NullString
		var isMgmt, isUnfinished sql.NullBool
		var mgmtName sql.NullString
		var upiuResp, upiuStatus sql.NullInt64
		var qOpcode, qIdn, qIndex, qSelector, uicCmd sql.NullInt64

		if f.isUFS {
			dest = append(dest, &tag, &opcode, &lun, &groupid, &hwqid,
				&txn, &upiuFlags, &upiuFunc, &upiuAttr, &upiuCp,
				&isMgmt, &mgmtName, &upiuResp, &upiuStatus,
				&qOpcode, &qIdn, &qIndex, &qSelector, &uicCmd, &isUnfinished)
		} else if f.isBlock {
			dest = append(dest, &devmajor, &devminor, &rwbs, &flags, &extra, &isUnfinished)
		}

		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan fsio event: %w", err)
		}

		e.Action = action.String
		// mgmt 행은 NULL → 0 으로 남긴다. lba/size/qd 가 0 인 mgmt 행은
		// 클라이언트가 그 값을 안 찍어야 한다 (차트 Y=0 가짜 가로줄 방지).
		if lba.Valid {
			e.Lba = uint64(lba.Int64)
		}
		if qd.Valid {
			e.Qd = uint32(qd.Int64)
		}
		if size.Valid {
			e.Size = uint32(size.Int64)
		}
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
			e.IsMgmt = isMgmt.Bool
			e.MgmtName = mgmtName.String
			e.IsUnfinished = isUnfinished.Bool
			// mgmt 원본값도 같은 규칙 — 없으면 안 채운다. 0 은 유효값이다
			// (query_idn 0x00 = bBootLunEn, uic_cmd 는 0 이 없지만 resp/status 는
			// 0 이 "성공" 이라 부재와 반드시 구분돼야 한다).
			setOptU32(&e.UpiuResp, upiuResp)
			setOptU32(&e.UpiuStatus, upiuStatus)
			setOptU32(&e.QueryOpcode, qOpcode)
			setOptU32(&e.QueryIdn, qIdn)
			setOptU32(&e.QueryIndex, qIndex)
			setOptU32(&e.QuerySelector, qSelector)
			setOptU32(&e.UicCmd, uicCmd)
		} else if f.isBlock {
			e.Devmajor = uint32(devmajor.Int64)
			e.Devminor = uint32(devminor.Int64)
			e.Rwbs = rwbs.String
			e.Flags = flags.String
			e.Extra = uint32(extra.Int64)
			e.IsUnfinished = isUnfinished.Bool
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
	return detectLbaColumnPrefixed(db, glob, "")
}

// detectLbaColumnPrefixed — 테이블 별칭(`b.`)을 붙인 lba 식.
//
// ⚠ 별칭을 **식 전체 앞에 붙이면 안 된다.** 두 스키마가 섞이면 반환값이
// `COALESCE(lba, sector)` 라는 식이라, 앞에 붙이면 `b.COALESCE(lba, sector)` 가
// 되어 "Scalar Function with name coalesce does not exist" 로 터진다.
// 컬럼 이름 각각에 붙여야 한다.
//
// 이 조합(fsio_ufs + fsio_block 동시 조회)은 checkMixedFamily 가 같은 계열로
// 허용하므로 실제로 도달 가능하고, **50만 행을 넘겨 샘플링 경로를 타야만**
// 드러난다 (전체 조회는 prefix 가 "" 라 멀쩡하다).
func detectLbaColumnPrefixed(db *sql.DB, glob, prefix string) string {
	q := fmt.Sprintf(`SELECT column_name FROM (DESCRIBE SELECT * FROM read_parquet(%s) LIMIT 0)
		WHERE column_name IN ('lba', 'sector')`, glob)
	rows, err := db.Query(q)
	if err != nil {
		return prefix + "lba"
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
		return fmt.Sprintf("COALESCE(%slba, %ssector)", prefix, prefix)
	}
	if hasLba {
		return prefix + "lba"
	}
	if hasSector {
		return prefix + "sector"
	}
	return prefix + "lba"
}

// setOptU32 — nullable 정수를 proto optional 필드에 옮긴다.
//
// 0 과 "값 없음" 을 구분해야 하는 필드 전용이다. upiu_status 는 **0 이 성공**이라
// 부재와 섞이면 실패한 TM 을 성공으로 읽는다.
func setOptU32(dst **uint32, v sql.NullInt64) {
	if !v.Valid {
		return
	}
	u := uint32(v.Int64)
	*dst = &u
}
