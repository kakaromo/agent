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

// hexOpcodeExpr — fsio_ufs 의 opcode(UInt8)를 `0x2a` 꼴 hex 문자열로.
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
const hexOpcodeExpr = "CASE WHEN typeof(opcode) = 'VARCHAR' " +
	"THEN lower(CAST(opcode AS VARCHAR)) " +
	"ELSE '0x' || lpad(lower(to_hex(CAST(opcode AS UTINYINT))), 2, '0') END"

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
	if !c.schema.any() {
		return ""
	}
	base := hexOpcodeExpr
	if c.schema.isUFS && includeMgmt {
		// mgmt_name 이 빈 문자열인 행(종류 특정 실패)은 hex 로 폴백한다 —
		// 빈 cmd 는 차트 범례에서 이름 없는 계열이 되어 더 나쁘다.
		base = fmt.Sprintf(
			"CASE WHEN COALESCE(is_mgmt, FALSE) AND COALESCE(mgmt_name, '') != '' "+
				"THEN mgmt_name ELSE %s END", base)
	}
	switch {
	case c.schema.isUFS && c.schema.isBlock:
		// 여러 잡을 합쳐 두 스키마가 섞인 경우 — 있는 쪽을 쓴다.
		return fmt.Sprintf("COALESCE(%s, rwbs)", base)
	case c.schema.isUFS:
		return base
	default:
		return "rwbs"
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
func (c fsioCols) mgmtNullExpr(col, typ string) string {
	if !c.schema.isUFS {
		return col
	}
	return fmt.Sprintf(
		"CASE WHEN COALESCE(is_mgmt, FALSE) THEN NULL::%s ELSE %s END", typ, col)
}
