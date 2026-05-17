// Package parser 는 ftrace `trace_pipe` 로그를 직접 파싱해 parquet 으로 변환한다.
// Rust `tools/trace` 바이너리의 parquet-only 경로를 Go 로 이관한 결과물이며,
// 1단계에서 정해진 단일 파일 산출 규약(`result_<type>.parquet`) 을 따른다.
//
// 스키마는 Rust 산출물과 1:1 호환을 목표로 한다 — `trace/stats.go` 의 DuckDB
// glob 이 두 산출물을 union 으로 읽을 수 있어야 한다.
package parser

// MilliSeconds — 초 단위 timestamp 차이를 latency(ms) 로 변환할 때 곱하는 상수.
// Rust: `crate::utils::constants::MILLISECONDS`.
const MilliSeconds float64 = 1000.0

// UFS bottom-half 알고리즘에서 사용하는 가드 상수 (Rust 와 동일).
const (
	UFSDebugLBA     uint64 = 2305843009213693951
	MaxValidUFSLBA  uint64 = 1 << 48
	DefaultAlignKB  uint64 = 64 // 기본 alignment 64KB
	UFSAlignUnit4KB uint64 = DefaultAlignKB / 4
	BlockAlignSect  uint64 = (DefaultAlignKB * 1024) / 512
)

// UFSEvent — Rust `models::UFS` 와 동일한 스키마. parquet 컬럼명/타입은 Rust 산출물과
// 일치해야 한다 (DuckDB union 읽기 호환).
type UFSEvent struct {
	Time       float64 `parquet:"time"`
	Process    string  `parquet:"process"`
	CPU        uint32  `parquet:"cpu"`
	Action     string  `parquet:"action"`
	Tag        uint32  `parquet:"tag"`
	Opcode     string  `parquet:"opcode"`
	LBA        uint64  `parquet:"lba"`
	Size       uint32  `parquet:"size"`
	GroupID    uint32  `parquet:"groupid"`
	HWQID      int32   `parquet:"hwqid"`
	QD         uint32  `parquet:"qd"`
	DtoC       float64 `parquet:"dtoc"`
	CtoC       float64 `parquet:"ctoc"`
	CtoD       float64 `parquet:"ctod"`
	Continuous bool    `parquet:"continuous"`
	Aligned    bool    `parquet:"aligned"`
	LineNumber uint64  `parquet:"line_number"`
}

// BlockEvent — Rust `models::Block` 와 동일한 스키마.
type BlockEvent struct {
	Time       float64 `parquet:"time"`
	Process    string  `parquet:"process"`
	CPU        uint32  `parquet:"cpu"`
	Flags      string  `parquet:"flags"`
	Action     string  `parquet:"action"`
	DevMajor   uint32  `parquet:"devmajor"`
	DevMinor   uint32  `parquet:"devminor"`
	IOType     string  `parquet:"io_type"`
	Extra      uint32  `parquet:"extra"`
	Sector     uint64  `parquet:"sector"`
	Size       uint32  `parquet:"size"`
	Comm       string  `parquet:"comm"`
	QD         uint32  `parquet:"qd"`
	DtoC       float64 `parquet:"dtoc"`
	CtoC       float64 `parquet:"ctoc"`
	CtoD       float64 `parquet:"ctod"`
	Continuous bool    `parquet:"continuous"`
	Aligned    bool    `parquet:"aligned"`
	LineNumber uint64  `parquet:"line_number"`
}

// UFSCustomEvent — Rust `models::UFSCUSTOM` 와 동일.
type UFSCustomEvent struct {
	Opcode     string  `parquet:"opcode"`
	LBA        uint64  `parquet:"lba"`
	Size       uint32  `parquet:"size"`
	StartTime  float64 `parquet:"start_time"`
	EndTime    float64 `parquet:"end_time"`
	DtoC       float64 `parquet:"dtoc"`
	StartQD    uint32  `parquet:"start_qd"`
	EndQD      uint32  `parquet:"end_qd"`
	CtoC       float64 `parquet:"ctoc"`
	CtoD       float64 `parquet:"ctod"`
	Continuous bool    `parquet:"continuous"`
	Aligned    bool    `parquet:"aligned"`
	LineNumber uint64  `parquet:"line_number"`
}

// isUFSAligned — Rust `utils::is_ufs_aligned`. LBA 가 4KB units 의 배수인지.
func isUFSAligned(lba uint64) bool {
	if UFSAlignUnit4KB == 0 {
		return true
	}
	return lba%UFSAlignUnit4KB == 0
}

// isBlockAligned — Rust `utils::is_block_aligned`. sector 가 alignment sectors 의 배수인지.
func isBlockAligned(sector uint64) bool {
	if BlockAlignSect == 0 {
		return true
	}
	return sector%BlockAlignSect == 0
}

// normalizeSector — Rust `parsers::log_common::normalize_sector`. Flush 계열/size=0/sentinel
// 케이스를 0 으로 정규화한다.
func normalizeSector(rawSector uint64, size uint32, ioType string) uint64 {
	const u64Max = ^uint64(0)
	if rawSector == u64Max {
		return 0
	}
	if size == 0 {
		return 0
	}
	if len(ioType) > 0 && ioType[0] == 'F' {
		return 0
	}
	return rawSector
}
