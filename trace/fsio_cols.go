package trace

import (
	"database/sql"
	"fmt"
)

// fsio(bpftrace) 산출물 전용 SQL 컬럼식 모음.
//
// # 왜 파일을 나눴나
//
// fsio 는 기존 ftrace 스키마와 컬럼 이름·타입·의미가 전부 다르다. 그런데 조회
// 경로(stats/sampler/aggregate)는 두 계열을 **같은 SQL 로** 처리해야 한다 —
// 여러 잡을 합쳐 조회할 수 있어서 호출부가 종류를 확실히 알지 못한다.
//
// 그래서 "런타임 컬럼 탐지 + CASE 분기" 라는 구조가 필연인데, 이 분기가 각
// 호출부에 흩어지면 한 곳만 고치고 나머지를 빠뜨린다. 실제로 그렇게 됐다 —
// mgmt 제외 조건이 stats.go 에만 있고 aggregate.go 에는 없어서 AI 집계가
// 오염됐다(2e1e8dd). 여기로 모아 **한 군데서만 고치게** 한다.
//
// 정본은 Rust `../trace/src/output/{stats,chart}_rpc_duckdb.rs` 다.

// fsioCols — 한 번의 스키마 탐지 결과로 만들어지는 컬럼식 묶음.
//
// detectCmdColumn 은 호출마다 DESCRIBE 쿼리를 돌린다. 예전엔 한 요청에서
// 8번까지 반복 호출됐다. 여기서 한 번 만들어 돌려 쓴다.
type fsioCols struct {
	schema fsioSchema
}

func newFsioCols(db *sql.DB, glob string) fsioCols {
	return fsioCols{schema: detectFsioSchema(db, glob)}
}

// mgmtExclusion — 데이터 IO 집계에서 mgmt 행을 빼는 조건.
//
// COALESCE 인 이유 — 다른 trace_type parquet 과 union 하면 is_mgmt 가 NULL 이다.
const mgmtExclusion = "COALESCE(is_mgmt, FALSE) = FALSE"

// ExcludeMgmt — where 에 mgmt 제외 조건을 붙인다. fsio_ufs 가 아니면 그대로 둔다.
//
// mgmt(Query/TM UPIU, UIC) 는 데이터 전송이 아니라 **링크 점유**다. 데이터 IO
// 통계에 섞이면 분모가 통째로 흔들린다 — **idle 구간에서는 mgmt 가 행의
// 대부분**이기 때문이다.
func (c fsioCols) ExcludeMgmt(where string) string {
	if !c.schema.isUFS {
		return where
	}
	return addCondition(where, mgmtExclusion)
}

// CmdExpr — cmd 축으로 쓸 SQL 식.
//
// fsio 는 기존 ftrace 스키마와 타입/의미가 달라 그대로 못 쓴다:
//   - fsio_ufs `opcode` 는 **UInt8 숫자**다. 기존 ftrace UFS 는 `0x2a` **문자열**
//     이라 그대로 두면 cmd 축이 `42` 같은 10진수로 나와 분류기가 전부 빗나간다.
//   - fsio_block `io_type` 은 파서 정책상 **항상 빈 문자열**이라 cmd 축이 통째로
//     빈다. 대신 `rwbs`("WS"/"R"/"D")가 분류 정보를 갖고 있다.
//
// mgmt 를 포함해 조회할 때(includeMgmt=true)는 mgmt 행의 cmd 를 `mgmt_name`
// 으로 바꾼다. mgmt 는 SCSI opcode 가 없어 opcode=0 이라, 그대로 hex 로 찍으면
// **종류가 전부 `0x00` 한 덩어리로 뭉쳐** Query 인지 hibern8 인지 Abort Task
// 인지 구분이 안 된다. 이름은 파싱 시점에 구워둔 값이라 SQL 이 이름 테이블을
// 재구현하지 않는다.
//
// 정본: Rust `../trace/src/output/chart_rpc_duckdb.rs:362`.
func (c fsioCols) CmdExpr(includeMgmt bool) string {
	return c.CmdExprPrefixed(includeMgmt, "")
}

// CmdExprPrefixed — CmdExpr 의 테이블 별칭 버전 (`b.` 등). 샘플링 쿼리처럼
// JOIN 이 있는 곳은 컬럼을 한정하지 않으면 ambiguous 로 터진다.
func (c fsioCols) CmdExprPrefixed(includeMgmt bool, prefix string) string {
	if !c.schema.any() {
		return ""
	}
	col := func(n string) string { return prefix + n }
	// opcode(UInt8) → `0x2a` 꼴 hex.
	//
	// ⚠ **ftrace 잡과 union 되면 opcode 가 VARCHAR 로 승격된다** (ftrace UFS 는
	// opcode 가 '0x2a' 문자열). 그 상태에서 to_hex 를 태우면 숫자가 아니라
	// **ASCII 바이트**를 인코딩한다 — '42' → '3432' → lpad(...,2) 로 잘려 '0x34'
	// 라는 엉뚱한 cmd 가 나오고, 분류기가 전부 default 로 새서 read/write 바이트가
	// 0 이 된다. typeof 로 갈라 이미 문자열이면 그대로 쓴다.
	//
	// ⚠ CASE 의 두 갈래는 **둘 다 타입 검사를 통과해야 한다**. opcode 가 UTINYINT
	// 일 때 lower(opcode) 는 함수 매칭에 실패하므로 양쪽 모두 VARCHAR 로 캐스팅해
	// 둔다. to_hex(VARCHAR) 는 ASCII 를 인코딩하지만 그 갈래는 실행되지 않는다.
	base := fmt.Sprintf("CASE WHEN typeof(%s) = 'VARCHAR' "+
		"THEN lower(CAST(%s AS VARCHAR)) "+
		"ELSE '0x' || lpad(lower(to_hex(CAST(%s AS UTINYINT))), 2, '0') END",
		col("opcode"), col("opcode"), col("opcode"))
	if c.schema.isUFS && includeMgmt {
		// mgmt_name 이 빈 문자열인 행(종류 특정 실패)은 hex 로 폴백한다 —
		// 빈 cmd 는 차트 범례에서 이름 없는 계열이 되어 더 나쁘다.
		base = fmt.Sprintf(
			"CASE WHEN COALESCE(%s, FALSE) AND COALESCE(%s, '') != '' "+
				"THEN %s ELSE %s END",
			col("is_mgmt"), col("mgmt_name"), col("mgmt_name"), base)
	}
	switch {
	case c.schema.isUFS && c.schema.isBlock:
		// 여러 잡을 합쳐 두 스키마가 섞인 경우 — 있는 쪽을 쓴다.
		return fmt.Sprintf("COALESCE(%s, %s)", base, col("rwbs"))
	case c.schema.isUFS:
		return base
	default:
		return col("rwbs")
	}
}

// mgmtNullExpr — mgmt 행에서 의미 없는 수치 컬럼을 NULL 로 만드는 식.
//
// **0 이 아니라 NULL 이어야 한다.** mgmt 는 데이터 전송이 아니라 lba/size/qd 가
// 애초에 없는 개념인데, 0 으로 두면 클라이언트가 그걸 유효값으로 찍어
// **Y=0 에 가짜 가로줄**이 생기고 자동 스케일 축이 0 까지 늘어나 실제 LBA
// 분포가 납작해진다. NULL 이면 그 계열에서 조용히 빠진다.
//
// ⚠ NULL 에 **반드시 타입을 붙인다.** 그냥 `THEN NULL` 이면 DuckDB 가 분기
// 타입을 합칠 때 결과를 무타입 NULL 로 추론하는 경우가 있다(스캔된 행이 전부
// mgmt 이거나 필터로 non-mgmt 가 다 걸러졌을 때). 그러면 바깥 SELECT 의
// 캐스팅이 "Invalid column type NULL" 로 터진다.
//
// 정본: Rust `../trace/src/output/chart_rpc_duckdb.rs:358-360`.
func (c fsioCols) mgmtNullExpr(col, typ, prefix string) string {
	if !c.schema.isUFS {
		return col
	}
	return fmt.Sprintf(
		"CASE WHEN COALESCE(%sis_mgmt, FALSE) THEN NULL::%s ELSE %s END",
		prefix, typ, col)
}

// rawCmdExpr — Raw Data / 차트용 cmd 식. mgmt 를 이름으로 살린다.
//
// 통계용(CmdExpr(false))과 갈라 두는 이유 — 통계는 ExcludeMgmt 로 mgmt 를
// 아예 빼므로 이름이 필요 없고, 오히려 cmd 축에 mgmt 이름이 섞이면 read/write
// 분류기가 오작동한다. Raw Data 는 반대로 mgmt 를 남기므로 이름이 필수다.
//
// fsio 가 아니면 호출부가 넘긴 기존 cmdCol 을 그대로 쓴다 — ftrace 는
// opcode/io_type 이름이 스키마마다 달라 여기서 지어낼 수 없다.
func (c fsioCols) rawCmdExpr(cmdCol, prefix string) string {
	if !c.schema.any() {
		return cmdCol
	}
	return c.CmdExprPrefixed(true, prefix)
}

// rawLbaExpr — Raw Data / 차트용 lba 식. mgmt 행은 NULL.
//
// ⚠ lbaExpr 은 **이미 별칭이 적용된 완성 식**이어야 한다 (detectLbaColumnPrefixed).
// 여기서 prefix 를 앞에 덧붙이면 두 스키마가 섞였을 때
// `b.COALESCE(lba, sector)` 가 되어 SQL 이 터진다. prefix 는 mgmt 판정
// 컬럼(is_mgmt)을 한정하는 데만 쓴다.
func (c fsioCols) rawLbaExpr(lbaExpr, prefix string) string {
	if !c.schema.isUFS {
		return lbaExpr
	}
	return c.mgmtNullExpr(lbaExpr, "UBIGINT", prefix)
}

// ============================================================================
// 방향(read/write) × 주소 연속성 — 공용 컬럼식
//
// 판정 축은 **send 순서**다. 주소 연속성은 "발행 순서상 직전 요청에 이어지는가" 의
// 성질이지 완료 순서의 성질이 아니다.
//
// parquet 의 `continuous` 컬럼을 안 쓰고 조회 시 다시 계산하는 이유 —
// 그 컬럼은 **방향 구분 없이 직전 send 1개**와만 비교한다. read 와 write 가 섞여
// 나가면 read 스트림 자체는 완벽히 순차인데도 중간의 write 때문에 끊긴 것으로
// 집계된다. 여기서는 read 는 직전 read 와, write 는 직전 write 와 비교한다.
// ============================================================================

// DirExpr — send 행의 방향을 'read'/'write'/NULL 로 내는 SQL 식.
//
// **NULL 이 핵심이다.** discard/flush/unmap 은 read 도 write 도 아니다. 'other' 로
// 뭉뚱그려 분모에 남기면 비율이 조용히 오염된다 — 특히 f2fs 는 discard 량이 커서
// write 에 섞으면 눈에 띄게 부푼다. 호출부가 `dir IS NOT NULL` 로 버린다.
//
// 판정 우선순위:
//  1. fsio: `is_read`/`is_write` 불리언. 파서가 io_flags 비트를 이미 푼 결과라
//     `is_discard`/`is_flush` 와 어긋날 수 없다. union 으로 NULL 이 될 수 있어 COALESCE.
//  2. ftrace UFS / ufscustom: opcode hex. 0x28/0x88=READ, 0x2a/0x8a=WRITE.
//     (stats.go 의 Go 분류기와 같은 목록 — 거기가 정본이다)
//  3. block / fsio_block: io_type / rwbs 의 **첫 글자**.
//
// ⚠ 3번은 반드시 첫 글자여야 한다. 완전일치로 하면 실기기에 실제로 나오는
// `WF`/`WFS`/`WSMA` 가 전부 빠진다 (F 가 뒤에 오면 FUA 지 flush 가 아니다).
// TestFsioRwbsClassifiedByFirstLetter 가 이걸 고정하고 있다.
//
// ⚠ `fsio_agg.go` 의 `lower(cmd) LIKE 'r%'` 를 재사용하면 안 된다 — ftrace UFS 의
// cmd 는 `0x28`/`0x2a` 라 r%/w% 어느 쪽에도 안 걸려 read/write 가 전부 0 이 된다.
func (c fsioCols) DirExpr(cmdExpr string) string {
	return c.DirExprWith(cmdExpr, true)
}

// DirExprWith — hasRWFlags 로 `is_read`/`is_write` 컬럼 존재를 명시한다.
//
// ⚠ 스키마 판정(`schema.any()`)은 `is_mgmt`/`mgmt_name`/`rwbs` 로 하는데 여기서
// 참조하는 건 `is_read`/`is_write` 라 **탐지 컬럼과 참조 컬럼이 다르다.** 지금은
// 두 producer(Go/Rust) 모두 함께 내보내지만, 한쪽만 있는 parquet 이 들어오면
// 쿼리가 통째로 터진다. 호출부가 확인해 넘길 수 있게 열어 둔다.
func (c fsioCols) DirExprWith(cmdExpr string, hasRWFlags bool) string {
	if c.schema.any() && hasRWFlags {
		// fsio 는 파서가 구워둔 불리언이 가장 정확하다 — io_flags 비트를 이미 푼
		// 결과라 is_discard/is_flush 와 어긋날 수 없다.
		//
		// ⚠ 단, 불리언이 **둘 다 false 일 수 있다.** producer 가 io_flags 를 안 준
		// 경우(구버전 bpftrace, UFS_TAG_CTX miss)가 그렇다. 그때 그냥 포기하면
		// 데이터가 멀쩡히 있는데도 집계가 통째로 빈다. opcode/rwbs 로 폴백한다.
		fb := ""
		if cmdExpr != "" {
			l := fmt.Sprintf("lower(CAST(%s AS VARCHAR))", cmdExpr)
			fb = fmt.Sprintf(
				" WHEN %s IN ('0x28','0x88') THEN 'read'"+
					" WHEN %s IN ('0x2a','0x8a') THEN 'write'"+
					" WHEN upper(left(%s, 1)) = 'R' THEN 'read'"+
					" WHEN upper(left(%s, 1)) = 'W' THEN 'write'", l, l, l, l)
		}
		return "CASE WHEN COALESCE(is_read, FALSE) THEN 'read' " +
			"WHEN COALESCE(is_write, FALSE) THEN 'write'" + fb + " END"
	}
	if cmdExpr == "" {
		return "NULL"
	}
	lower := fmt.Sprintf("lower(CAST(%s AS VARCHAR))", cmdExpr)
	// ftrace 는 union 으로 ufs(opcode) + block(io_type) 이 섞일 수 있어 양쪽을 다 본다.
	// hex 를 먼저 보고, 안 걸리면 첫 글자로 간다.
	return fmt.Sprintf(
		"CASE WHEN %s IN ('0x28','0x88') THEN 'read' "+
			"WHEN %s IN ('0x2a','0x8a') THEN 'write' "+
			"WHEN upper(left(%s, 1)) = 'R' THEN 'read' "+
			"WHEN upper(left(%s, 1)) = 'W' THEN 'write' END",
		lower, lower, lower, lower)
}

// SendPredicate — 이 행이 요청(send/issue)인가.
//
// ⚠ **ufscustom 에는 `action` 컬럼이 아예 없다** (trace/parser/types.go 의
// UFSCustomEvent). 기존 `action IN ('send_req','block_rq_issue')` 를 그대로 쓰면
// ufscustom 은 **조용히 0 행**이 되어 "데이터 없음" 처럼 보인다. ufscustom 은 한 행이
// 곧 한 요청이라 TRUE 가 맞다.
func (c fsioCols) SendPredicate(hasAction bool) string {
	if !hasAction {
		return "TRUE"
	}
	// ⚠ `Q` 는 block_rq_issue 의 별칭이다. 파서가 둘 다 받고(block.go:96,
	// fsio_block.go:64) 원문을 그대로 parquet 에 넣으므로(fsio_line.go:263
	// `rawAction`) 여기서도 둘 다 봐야 한다. 빠뜨리면 `Q` 로 기록된 트레이스는
	// send 가 0 건이 되어 **에러 없이 화면이 통째로 빈다.**
	return "action IN ('send_req', 'block_rq_issue', 'Q')"
}

// CompletePredicate — 이 행이 완료(complete) 인가. SendPredicate 의 짝.
//
// `C` 는 block_rq_complete 의 별칭이다 (SendPredicate 의 `Q` 와 같은 이유).
func (c fsioCols) CompletePredicate(hasAction bool) string {
	if !hasAction {
		return "TRUE"
	}
	return "action IN ('complete_rsp', 'block_rq_complete', 'C')"
}

// EndAddrExpr — 이 요청의 끝 주소. 다음 요청의 시작 주소가 이 값과 같으면 연속이다.
//
// size 단위가 소스마다 다르다:
//   - ftrace(ufs/ufscustom/block): 주소와 size 가 **같은 단위**라 그냥 더한다
//     (ufs 는 둘 다 4KB LBA, block 은 둘 다 512B sector — 파서가 이미 정규화)
//   - fsio(ufs/block): size 가 **bytes** 라 주소 단위로 올림 나눗셈해야 한다
//     (파서 fsio_ufs.go / fsio_block.go 의 ceilDiv64 와 같은 계산)
//
// ⚠ 올림 나눗셈에 **`//` 를 쓴다.** DuckDB 의 `/` 는 정수끼리도 **부동소수 나눗셈**이다.
// 예: size=4096 이면 `(4096+4095)/4096` = 1.9998 이 그대로 남는다(`//` 면 1).
// 그러면 끝주소가 정수가 아니게 되어
// `addr = lag(end_addr)` 동등 비교가 **영원히 안 맞고** 연속이 항상 0% 로 나온다.
// 에러가 아니라 그럴듯한 0 이라 알아채기 어렵다.
func (c fsioCols) EndAddrExpr(lbaCol string) string {
	switch {
	case c.schema.isUFS && c.schema.isBlock:
		// fsio 두 스키마가 섞인 경우 — 행마다 있는 쪽으로 간다.
		return "CASE WHEN lba IS NOT NULL " +
			"THEN CAST(lba AS HUGEINT) + (CAST(size AS HUGEINT) + 4095) // 4096 " +
			"ELSE CAST(sector AS HUGEINT) + (CAST(size AS HUGEINT) + 511) // 512 END"
	case c.schema.isUFS:
		return "CAST(lba AS HUGEINT) + (CAST(size AS HUGEINT) + 4095) // 4096"
	case c.schema.isBlock:
		return "CAST(sector AS HUGEINT) + (CAST(size AS HUGEINT) + 511) // 512"
	default:
		return fmt.Sprintf("CAST(%s AS HUGEINT) + CAST(size AS HUGEINT)", lbaCol)
	}
}

// ContiguityPartition — 주소 공간이 독립인 단위. PARTITION BY 에 덧붙일 컬럼 목록
// (앞에 콤마 포함, 없으면 빈 문자열).
//
// ⚠ 이게 빠지면 **서로 다른 LU/디바이스의 요청이 거짓으로 이어져** 연속 비율이
// 100% 쪽으로 부푼다. "훌륭한 워크로드" 처럼 보이는 가장 그럴듯한 오답이다.
// 파서도 같은 이유로 페어링 키에 넣는다 (fsio_ufs.go lun, fsio_block.go dev).
//
// ⚠ ftrace UFS/ufscustom 은 **LU 컬럼이 스키마에 없어** 구분할 방법이 자체가 없다.
// 소스의 한계지 이 기능의 한계가 아니다 — multi-LU 트레이스면 연속 비율이
// 과대평가된다.
func (c fsioCols) ContiguityPartition(present map[string]bool) []string {
	if c.schema.isUFS && present["lun"] {
		// 255 = LunUnknown. 미상끼리 이어지는 건 파서 동작(ev.LUN == prevLun)과 같다.
		return []string{"COALESCE(lun, 255)"}
	}
	if present["devmajor"] && present["devminor"] {
		return []string{"COALESCE(devmajor, 0)", "COALESCE(devminor, 0)"}
	}
	return nil
}

// SectorBytes — size 1 단위가 몇 바이트인가. 집계값을 bytes 로 환산할 때 쓴다.
//
//	fsio        : 1    (bpftrace 가 이미 bytes 로 준다)
//	ftrace block: 512  (sector)
//	ftrace ufs  : 4096 (4KB LBA)
//
// ⚠ 기존 stats.go 는 이 계수를 **cmd 문자열로 행마다** 골랐다. 방향으로 묶는 집계는
// 그 방법을 못 쓰므로 스키마에서 한 번 정한다. ftrace 는 `sector` 컬럼 유무로 block 을
// 가른다 — `fsio_agg.go` 의 isBlockLayer 와 같은 판정이다. (예전에 여기가 무조건
// 4096 이라 ftrace block 바이트가 8배로 부푼 적이 있다.)
func (c fsioCols) SectorBytes(hasSector bool) uint64 {
	if c.schema.any() {
		return 1
	}
	if hasSector {
		return 512
	}
	return 4096
}
