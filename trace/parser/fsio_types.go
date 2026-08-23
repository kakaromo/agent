package parser

// bpftrace(fsiotrace) TSV 파생 이벤트 모델.
//
// 입력 명세: `../bpftrace/docs/OUTPUT_FORMAT.md` (TAB 17컬럼).
// 스키마 정본: `../trace/src/output/fsio_{ufs,block}_parquet.rs` 의 Field 순서.
// **컬럼명·순서·타입이 Rust 산출물과 1:1 이어야** DuckDB 가 두 산출물을 같이 읽는다.
//
// 기존 ftrace 계열(UFSEvent/BlockEvent) 과 분리한 이유 — bpftrace 가 흡수한
// cross-layer 메타(pid/tid/comm/syscall/fs/ino/name)와 io_flags, UPIU 헤더를
// 살려두기 위함. 입력 소스도 필드도 달라서 parquet 을 따로 낸다.

// LunUnknown — lun 미상 sentinel. bpftrace `UFS_LUN_UNKNOWN`(src/fsiotrace.h) 과 같은 값이며
// TSV 에는 `lun=?` 로 나온다. 0 으로 폴백하면 실제 LU0 과 섞이므로 별도 값으로 둔다.
const LunUnknown uint8 = 0xff

// FsioUfsEvent — bpftrace UFS row 한 줄.
//
// 데이터 IO(`ufshcd_command:*`) 와 management 이벤트(Query/TM UPIU, UIC, exception) 를
// 같은 stream 으로 담고 IsMgmt 로 구분한다 — 분석가가 한 타임라인에서 같이 봐야
// "hibern8 도는 동안 IO 가 멈췄다" 가 보이기 때문.
//
// Option 계열(*uint8/*uint32)은 UFS_TAG_CTX miss / 구 producer 에서 빠질 수 있는 값이다.
// nil 은 "그 값이 없었다" 는 뜻이고 0 과 구분된다.
type FsioUfsEvent struct {
	Time               float64 `parquet:"time"`
	Process            string  `parquet:"process"` // bpftrace 는 comm 만 주지만 일관성 위해 process 로도 노출 (comm 과 동일 값)
	CPU                uint32  `parquet:"cpu"`
	Action             string  `parquet:"action"` // "send_req" | "complete_rsp" | "upiu_*" | "uic_send"/"uic_complete" | "exception"
	Tag                uint32  `parquet:"tag"`
	Opcode             uint8   `parquet:"opcode"`  // SCSI opcode (0x28=READ_10, 0x2a=WRITE_10 등). ftrace UFSEvent.Opcode 는 string 이니 혼동 주의
	LUN                uint8   `parquet:"lun"`     // UFS logical unit. LU 마다 LBA 주소공간이 독립 → 페어링/연속성 키에 필수. 미상은 LunUnknown(0xff)
	LBA                uint64  `parquet:"lba"`     // bpftrace 의 `sec` (UFS layer 에서는 LBA, LU 별 독립)
	Size               uint32  `parquet:"size"`    // bytes — 이미 바이트라 통계에서 곱셈 계수는 1 이다
	GroupID            uint32  `parquet:"groupid"` // bpftrace 의 `grp` (커널 __u16)
	HwqID              int32   `parquet:"hwqid"`   // bpftrace 의 `hwq` (-1 가능)
	QD                 uint32  `parquet:"qd"`
	DtoC               float64 `parquet:"dtoc"`
	CtoC               float64 `parquet:"ctoc"`
	CtoD               float64 `parquet:"ctod"`
	Continuous         bool    `parquet:"continuous"`
	Aligned            bool    `parquet:"aligned"`
	LineNumber         uint64  `parquet:"line_number"`
	PID                uint32  `parquet:"pid"`
	TID                uint32  `parquet:"tid"`
	Comm               string  `parquet:"comm"`
	Syscall            string  `parquet:"syscall"`
	FS                 string  `parquet:"fs"`
	Ino                uint64  `parquet:"ino"`
	Name               string  `parquet:"name"`
	IOFlags            uint64  `parquet:"io_flags"`
	IsRead             bool    `parquet:"is_read"`
	IsWrite            bool    `parquet:"is_write"`
	IsDiscard          bool    `parquet:"is_discard"`
	IsFlush            bool    `parquet:"is_flush"`
	IsTrim             bool    `parquet:"is_trim"`
	IsOSync            bool    `parquet:"is_o_sync"`
	IsODirect          bool    `parquet:"is_o_direct"`
	IsOAppend          bool    `parquet:"is_o_append"`
	IsODsync           bool    `parquet:"is_o_dsync"`
	IsSyncPath         bool    `parquet:"is_sync_path"`
	IsReqSync          bool    `parquet:"is_req_sync"`
	IsReqPrio          bool    `parquet:"is_req_prio"`
	IsReqRahead        bool    `parquet:"is_req_rahead"`
	IsData             bool    `parquet:"is_data"`
	IsMetadata         bool    `parquet:"is_metadata"`
	IsInode            bool    `parquet:"is_inode"`
	IsBitmap           bool    `parquet:"is_bitmap"`
	IsDirent           bool    `parquet:"is_dirent"`
	IsXattr            bool    `parquet:"is_xattr"`
	IsJournal          bool    `parquet:"is_journal"`
	IsCheckpoint       bool    `parquet:"is_checkpoint"`
	IsGC               bool    `parquet:"is_gc"`
	IsExtentAlloc      bool    `parquet:"is_extent_alloc"`
	IsExtentFree       bool    `parquet:"is_extent_free"`
	IsBmap             bool    `parquet:"is_bmap"`
	IsBuffered         bool    `parquet:"is_buffered"`
	IsDirectIO         bool    `parquet:"is_direct_io"`
	IsMmapWriteback    bool    `parquet:"is_mmap_writeback"`
	IsWritebackKworker bool    `parquet:"is_writeback_kworker"`
	IsFsyncTriggered   bool    `parquet:"is_fsync_triggered"`
	IsSawVfs           bool    `parquet:"is_saw_vfs"`
	IsF2FSNodeWrite    bool    `parquet:"is_f2fs_node_write"`
	IsF2FSDataWrite    bool    `parquet:"is_f2fs_data_write"`
	IsF2FSMetaWrite    bool    `parquet:"is_f2fs_meta_write"`
	IsF2FSNodeGC       bool    `parquet:"is_f2fs_node_gc"`
	IsF2FSDataGC       bool    `parquet:"is_f2fs_data_gc"`
	IsF2FSHotData      bool    `parquet:"is_f2fs_hot_data"`
	IsF2FSWarmData     bool    `parquet:"is_f2fs_warm_data"`
	IsF2FSColdData     bool    `parquet:"is_f2fs_cold_data"`
	Txn                *uint8  `parquet:"txn,optional"`
	UpiuFlags          *uint8  `parquet:"upiu_flags,optional"`
	UpiuFunc           *uint8  `parquet:"upiu_func,optional"`
	UpiuAttr           string  `parquet:"upiu_attr"`
	UpiuCp             *uint8  `parquet:"upiu_cp,optional"`
	IsMgmt             bool    `parquet:"is_mgmt"`
	UpiuResp           *uint8  `parquet:"upiu_resp,optional"`
	UpiuStatus         *uint8  `parquet:"upiu_status,optional"`
	QueryOpcode        *uint8  `parquet:"query_opcode,optional"`
	QueryIdn           *uint8  `parquet:"query_idn,optional"`
	QueryIndex         *uint8  `parquet:"query_index,optional"`
	QuerySelector      *uint8  `parquet:"query_selector,optional"`
	UicCmd             *uint32 `parquet:"uic_cmd,optional"`
	MgmtName           string  `parquet:"mgmt_name"` // 표시 이름을 파싱 시점에 구워 둔다 — SQL 쪽에서 이름 테이블 재구현을 막는다
	IsUnfinished       bool    `parquet:"is_unfinished"`
}

// FsioBlockEvent — bpftrace BLK row 한 줄.
//
// ⚠ IsUnfinished 의 위치가 FsioUfsEvent 와 다르다 (여기는 RWBS 다음, 저기는 맨 끝).
// Rust 스키마가 그렇게 굳어 있어 의도적으로 맞추지 않는다.
type FsioBlockEvent struct {
	Time               float64 `parquet:"time"`
	Process            string  `parquet:"process"` // bpftrace 는 comm 만 주지만 일관성 위해 process 로도 노출 (comm 과 동일 값)
	CPU                uint32  `parquet:"cpu"`
	Flags              string  `parquet:"flags"`  // ftrace 의 trace flag string. bpftrace 출력엔 없어 빈 문자열 유지
	Action             string  `parquet:"action"` // "send_req" | "complete_rsp" | "upiu_*" | "uic_send"/"uic_complete" | "exception"
	DevMajor           uint32  `parquet:"devmajor"`
	DevMinor           uint32  `parquet:"devminor"`
	IOType             string  `parquet:"io_type"` // 파서 정책상 항상 빈 값 — 분류는 RWBS / IOFlags 사용
	Extra              uint32  `parquet:"extra"`   // bpftrace 출력엔 없음 — 0 유지
	Sector             uint64  `parquet:"sector"`  // 512B sector
	Size               uint32  `parquet:"size"`    // bytes — 이미 바이트라 통계에서 곱셈 계수는 1 이다
	Comm               string  `parquet:"comm"`
	QD                 uint32  `parquet:"qd"`
	DtoC               float64 `parquet:"dtoc"`
	CtoC               float64 `parquet:"ctoc"`
	CtoD               float64 `parquet:"ctod"`
	Continuous         bool    `parquet:"continuous"`
	Aligned            bool    `parquet:"aligned"`
	LineNumber         uint64  `parquet:"line_number"`
	PID                uint32  `parquet:"pid"`
	TID                uint32  `parquet:"tid"`
	Syscall            string  `parquet:"syscall"`
	FS                 string  `parquet:"fs"`
	Ino                uint64  `parquet:"ino"`
	Name               string  `parquet:"name"`
	RWBS               string  `parquet:"rwbs"` // "WS"/"R"/"D"... block layer rwbs. io_type 보다 상세 (조합 의미 보존)
	IsUnfinished       bool    `parquet:"is_unfinished"`
	IOFlags            uint64  `parquet:"io_flags"`
	IsRead             bool    `parquet:"is_read"`
	IsWrite            bool    `parquet:"is_write"`
	IsDiscard          bool    `parquet:"is_discard"`
	IsFlush            bool    `parquet:"is_flush"`
	IsTrim             bool    `parquet:"is_trim"`
	IsOSync            bool    `parquet:"is_o_sync"`
	IsODirect          bool    `parquet:"is_o_direct"`
	IsOAppend          bool    `parquet:"is_o_append"`
	IsODsync           bool    `parquet:"is_o_dsync"`
	IsSyncPath         bool    `parquet:"is_sync_path"`
	IsReqSync          bool    `parquet:"is_req_sync"`
	IsReqPrio          bool    `parquet:"is_req_prio"`
	IsReqRahead        bool    `parquet:"is_req_rahead"`
	IsData             bool    `parquet:"is_data"`
	IsMetadata         bool    `parquet:"is_metadata"`
	IsInode            bool    `parquet:"is_inode"`
	IsBitmap           bool    `parquet:"is_bitmap"`
	IsDirent           bool    `parquet:"is_dirent"`
	IsXattr            bool    `parquet:"is_xattr"`
	IsJournal          bool    `parquet:"is_journal"`
	IsCheckpoint       bool    `parquet:"is_checkpoint"`
	IsGC               bool    `parquet:"is_gc"`
	IsExtentAlloc      bool    `parquet:"is_extent_alloc"`
	IsExtentFree       bool    `parquet:"is_extent_free"`
	IsBmap             bool    `parquet:"is_bmap"`
	IsBuffered         bool    `parquet:"is_buffered"`
	IsDirectIO         bool    `parquet:"is_direct_io"`
	IsMmapWriteback    bool    `parquet:"is_mmap_writeback"`
	IsWritebackKworker bool    `parquet:"is_writeback_kworker"`
	IsFsyncTriggered   bool    `parquet:"is_fsync_triggered"`
	IsSawVfs           bool    `parquet:"is_saw_vfs"`
	IsF2FSNodeWrite    bool    `parquet:"is_f2fs_node_write"`
	IsF2FSDataWrite    bool    `parquet:"is_f2fs_data_write"`
	IsF2FSMetaWrite    bool    `parquet:"is_f2fs_meta_write"`
	IsF2FSNodeGC       bool    `parquet:"is_f2fs_node_gc"`
	IsF2FSDataGC       bool    `parquet:"is_f2fs_data_gc"`
	IsF2FSHotData      bool    `parquet:"is_f2fs_hot_data"`
	IsF2FSWarmData     bool    `parquet:"is_f2fs_warm_data"`
	IsF2FSColdData     bool    `parquet:"is_f2fs_cold_data"`
}
