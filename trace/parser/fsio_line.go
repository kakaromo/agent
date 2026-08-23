package parser

import (
	"strconv"
	"strings"
)

// bpftrace(fsiotrace) TSV 한 줄 파싱.
//
// 명세: `../bpftrace/docs/OUTPUT_FORMAT.md`. TAB 구분 **17컬럼 고정**, 헤더 없음.
// Rust `../trace/src/parsers/bpftrace_tsv.rs` 의 포팅.
//
//	1 ts  2 layer  3 pid  4 tid  5 cpu  6 comm  7 syscall  8 action  9 fs
//	10 dev_major  11 dev_minor  12 ino  13 size(bytes)  14 sec  15 name
//	16 io_flags  17 extra
//
// BLK / UFS row 만 취하고 VFS / FS row 는 버린다.

// io_flags 비트 마스크 (OUTPUT_FORMAT.md §5 + fsiotrace.h).
const (
	fRead             uint64 = 0x1
	fWrite            uint64 = 0x2
	fDiscard          uint64 = 0x4
	fFlush            uint64 = 0x8
	fTrim             uint64 = 0x10
	fOSync            uint64 = 0x100
	fODirect          uint64 = 0x200
	fOAppend          uint64 = 0x400
	fODsync           uint64 = 0x800
	fSyncPath         uint64 = 0x1000
	fReqSync          uint64 = 0x2000
	fReqPrio          uint64 = 0x4000
	fReqRahead        uint64 = 0x8000
	fData             uint64 = 0x10000
	fMetadata         uint64 = 0x20000
	fInode            uint64 = 0x40000
	fBitmap           uint64 = 0x80000
	fDirent           uint64 = 0x100000
	fXattr            uint64 = 0x200000
	fJournal          uint64 = 0x400000
	fCheckpoint       uint64 = 0x800000
	fGC               uint64 = 0x1000000
	fExtentAlloc      uint64 = 0x2000000
	fExtentFree       uint64 = 0x4000000
	fBmap             uint64 = 0x8000000
	fBuffered         uint64 = 0x100000000
	fDirectIO         uint64 = 0x200000000
	fMmapWriteback    uint64 = 0x400000000
	fWritebackKworker uint64 = 0x800000000
	fFsyncTriggered   uint64 = 0x1000000000
	fSawVfs           uint64 = 0x10000000000
	fF2FSNodeWrite    uint64 = 0x1000000000000
	fF2FSDataWrite    uint64 = 0x2000000000000
	fF2FSMetaWrite    uint64 = 0x4000000000000
	fF2FSNodeGC       uint64 = 0x8000000000000
	fF2FSDataGC       uint64 = 0x10000000000000
	fF2FSHotData      uint64 = 0x20000000000000
	fF2FSWarmData     uint64 = 0x40000000000000
	fF2FSColdData     uint64 = 0x80000000000000
)

// quickFsioCheck — 정밀 파싱 전 값싼 선별.
//
// 첫 컬럼이 `\d+\.\d+`, 둘째가 BLK/UFS/VFS/FS 면 fsio 라인이다.
// ftrace UFS 라인도 `ufshcd_command:` 를 포함하므로 **fsio 판정을 먼저** 해야
// 두 포맷이 섞이지 않는다 (Rust 도 같은 순서).
func quickFsioCheck(line string) bool {
	t1 := strings.IndexByte(line, '\t')
	if t1 <= 0 {
		return false
	}
	// 첫 컬럼: 숫자 + '.' + 숫자
	ts := line[:t1]
	dot := strings.IndexByte(ts, '.')
	if dot <= 0 || dot == len(ts)-1 {
		return false
	}
	for i := 0; i < len(ts); i++ {
		if i == dot {
			continue
		}
		if ts[i] < '0' || ts[i] > '9' {
			return false
		}
	}
	rest := line[t1+1:]
	t2 := strings.IndexByte(rest, '\t')
	if t2 < 0 {
		return false
	}
	switch rest[:t2] {
	case "BLK", "UFS", "VFS", "FS":
		return true
	}
	return false
}

// hexPrefix — hex 값 앞부분만 떼어낸다.
//
// producer 는 진단을 돕기 위해 값 뒤에 이름을 괄호로 붙인다:
// `qop=0x05(read_flag)`, `uic_cmd=0x17(DME_HIBER_ENTER)`.
// 통째로 ParseUint 에 넣으면 실패해서 **조용히 0 이 된다** — 값이 0 인 것과
// 파싱 실패가 구분이 안 되는 게 특히 나쁘다. 그래서 hex 자리까지만 먹고 멈춘다.
// 이름은 소비자가 자체 테이블(ufs_names.go)로 푼다 — producer 문자열에 의존하지 않기 위해서.
func hexPrefix(s string) string {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	for i := 0; i < len(s); i++ {
		c := s[i]
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return s[:i]
		}
	}
	return s
}

func parseHexU64(s string) uint64 {
	v, _ := strconv.ParseUint(hexPrefix(s), 16, 64)
	return v
}

func parseHexU8(s string) uint8 {
	v, _ := strconv.ParseUint(hexPrefix(s), 16, 8)
	return uint8(v)
}

func parseHexU16(s string) uint16 {
	v, _ := strconv.ParseUint(hexPrefix(s), 16, 16)
	return uint16(v)
}

func parseHexU32(s string) uint32 {
	v, _ := strconv.ParseUint(hexPrefix(s), 16, 32)
	return uint32(v)
}

func atoiU32(s string) uint32 {
	v, _ := strconv.ParseUint(s, 10, 32)
	return uint32(v)
}

func atoiU64(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

// parseFsioExtra — `key=value` 공백 구분 토큰을 map 으로.
func parseFsioExtra(extra string) map[string]string {
	if extra == "" {
		return nil
	}
	m := make(map[string]string, 12)
	for _, tok := range strings.Fields(extra) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			m[k] = v
		}
	}
	return m
}

// fsioFlags — io_flags u64 를 39개 boolean 으로 분해한 것.
// 필드 순서는 Rust `DecodedFlags` 와 동일.
type fsioFlags struct {
	IsRead, IsWrite, IsDiscard, IsFlush, IsTrim                               bool
	IsOSync, IsODirect, IsOAppend, IsODsync                                   bool
	IsSyncPath, IsReqSync, IsReqPrio, IsReqRahead                             bool
	IsData, IsMetadata, IsInode, IsBitmap, IsDirent, IsXattr                  bool
	IsJournal, IsCheckpoint, IsGC, IsExtentAlloc, IsExtentFree, IsBmap        bool
	IsBuffered, IsDirectIO, IsMmapWriteback, IsWritebackKworker               bool
	IsFsyncTriggered, IsSawVfs                                                bool
	IsF2FSNodeWrite, IsF2FSDataWrite, IsF2FSMetaWrite                         bool
	IsF2FSNodeGC, IsF2FSDataGC, IsF2FSHotData, IsF2FSWarmData, IsF2FSColdData bool
}

func decodeFsioFlags(f uint64) fsioFlags {
	return fsioFlags{
		IsRead:             f&fRead != 0,
		IsWrite:            f&fWrite != 0,
		IsDiscard:          f&fDiscard != 0,
		IsFlush:            f&fFlush != 0,
		IsTrim:             f&fTrim != 0,
		IsOSync:            f&fOSync != 0,
		IsODirect:          f&fODirect != 0,
		IsOAppend:          f&fOAppend != 0,
		IsODsync:           f&fODsync != 0,
		IsSyncPath:         f&fSyncPath != 0,
		IsReqSync:          f&fReqSync != 0,
		IsReqPrio:          f&fReqPrio != 0,
		IsReqRahead:        f&fReqRahead != 0,
		IsData:             f&fData != 0,
		IsMetadata:         f&fMetadata != 0,
		IsInode:            f&fInode != 0,
		IsBitmap:           f&fBitmap != 0,
		IsDirent:           f&fDirent != 0,
		IsXattr:            f&fXattr != 0,
		IsJournal:          f&fJournal != 0,
		IsCheckpoint:       f&fCheckpoint != 0,
		IsGC:               f&fGC != 0,
		IsExtentAlloc:      f&fExtentAlloc != 0,
		IsExtentFree:       f&fExtentFree != 0,
		IsBmap:             f&fBmap != 0,
		IsBuffered:         f&fBuffered != 0,
		IsDirectIO:         f&fDirectIO != 0,
		IsMmapWriteback:    f&fMmapWriteback != 0,
		IsWritebackKworker: f&fWritebackKworker != 0,
		IsFsyncTriggered:   f&fFsyncTriggered != 0,
		IsSawVfs:           f&fSawVfs != 0,
		IsF2FSNodeWrite:    f&fF2FSNodeWrite != 0,
		IsF2FSDataWrite:    f&fF2FSDataWrite != 0,
		IsF2FSMetaWrite:    f&fF2FSMetaWrite != 0,
		IsF2FSNodeGC:       f&fF2FSNodeGC != 0,
		IsF2FSDataGC:       f&fF2FSDataGC != 0,
		IsF2FSHotData:      f&fF2FSHotData != 0,
		IsF2FSWarmData:     f&fF2FSWarmData != 0,
		IsF2FSColdData:     f&fF2FSColdData != 0,
	}
}

// fsioCols — 17컬럼 공통 필드를 한 번만 파싱해 둔 것.
type fsioCols struct {
	time      float64
	layer     string
	pid, tid  uint32
	cpu       uint32
	comm      string
	syscall   string
	rawAction string
	fs        string
	devMajor  uint32
	devMinor  uint32
	ino       uint64
	size      uint32
	sec       uint64
	name      string
	ioFlags   uint64
	extra     map[string]string
	flags     fsioFlags
}

// splitFsioCols — 공통 17컬럼 분해. 컬럼 수가 모자라면 ok=false.
//
// `-x`(--decode) 를 켜면 18번째 컬럼(io_flags 비트 풀이)이 붙는데, 그건 무시하고
// 앞 17컬럼만 쓴다 — 그래서 `>= 17` 검사다.
func splitFsioCols(line string) (fsioCols, bool) {
	cols := strings.Split(line, "\t")
	if len(cols) < 17 {
		return fsioCols{}, false
	}
	t, err := strconv.ParseFloat(cols[0], 64)
	if err != nil {
		return fsioCols{}, false
	}
	ioFlags := parseHexU64(cols[15])
	return fsioCols{
		time:      t,
		layer:     cols[1],
		pid:       atoiU32(cols[2]),
		tid:       atoiU32(cols[3]),
		cpu:       atoiU32(cols[4]),
		comm:      cols[5],
		syscall:   cols[6],
		rawAction: cols[7],
		fs:        cols[8],
		devMajor:  atoiU32(cols[9]),
		devMinor:  atoiU32(cols[10]),
		ino:       atoiU64(cols[11]),
		size:      atoiU32(cols[12]),
		sec:       atoiU64(cols[13]),
		name:      cols[14],
		ioFlags:   ioFlags,
		extra:     parseFsioExtra(cols[16]),
		flags:     decodeFsioFlags(ioFlags),
	}, true
}

// parseFsioBlockLine — layer="BLK" 한 줄 → FsioBlockEvent.
func parseFsioBlockLine(line string) (FsioBlockEvent, bool) {
	c, ok := splitFsioCols(line)
	if !ok || c.layer != "BLK" || c.rawAction == "" {
		return FsioBlockEvent{}, false
	}
	// io_type 은 채우지 않는다 (파서 정책) — 분류는 rwbs / io_flags 로.
	return FsioBlockEvent{
		Time:               c.time,
		Process:            c.comm,
		CPU:                c.cpu,
		Flags:              "",
		Action:             c.rawAction,
		DevMajor:           c.devMajor,
		DevMinor:           c.devMinor,
		IOType:             "",
		Extra:              0,
		Sector:             c.sec,
		Size:               c.size,
		Comm:               c.comm,
		Aligned:            isBlockAligned(c.sec),
		PID:                c.pid,
		TID:                c.tid,
		Syscall:            c.syscall,
		FS:                 c.fs,
		Ino:                c.ino,
		Name:               c.name,
		RWBS:               c.extra["rwbs"],
		IOFlags:            c.ioFlags,
		IsRead:             c.flags.IsRead,
		IsWrite:            c.flags.IsWrite,
		IsDiscard:          c.flags.IsDiscard,
		IsFlush:            c.flags.IsFlush,
		IsTrim:             c.flags.IsTrim,
		IsOSync:            c.flags.IsOSync,
		IsODirect:          c.flags.IsODirect,
		IsOAppend:          c.flags.IsOAppend,
		IsODsync:           c.flags.IsODsync,
		IsSyncPath:         c.flags.IsSyncPath,
		IsReqSync:          c.flags.IsReqSync,
		IsReqPrio:          c.flags.IsReqPrio,
		IsReqRahead:        c.flags.IsReqRahead,
		IsData:             c.flags.IsData,
		IsMetadata:         c.flags.IsMetadata,
		IsInode:            c.flags.IsInode,
		IsBitmap:           c.flags.IsBitmap,
		IsDirent:           c.flags.IsDirent,
		IsXattr:            c.flags.IsXattr,
		IsJournal:          c.flags.IsJournal,
		IsCheckpoint:       c.flags.IsCheckpoint,
		IsGC:               c.flags.IsGC,
		IsExtentAlloc:      c.flags.IsExtentAlloc,
		IsExtentFree:       c.flags.IsExtentFree,
		IsBmap:             c.flags.IsBmap,
		IsBuffered:         c.flags.IsBuffered,
		IsDirectIO:         c.flags.IsDirectIO,
		IsMmapWriteback:    c.flags.IsMmapWriteback,
		IsWritebackKworker: c.flags.IsWritebackKworker,
		IsFsyncTriggered:   c.flags.IsFsyncTriggered,
		IsSawVfs:           c.flags.IsSawVfs,
		IsF2FSNodeWrite:    c.flags.IsF2FSNodeWrite,
		IsF2FSDataWrite:    c.flags.IsF2FSDataWrite,
		IsF2FSMetaWrite:    c.flags.IsF2FSMetaWrite,
		IsF2FSNodeGC:       c.flags.IsF2FSNodeGC,
		IsF2FSDataGC:       c.flags.IsF2FSDataGC,
		IsF2FSHotData:      c.flags.IsF2FSHotData,
		IsF2FSWarmData:     c.flags.IsF2FSWarmData,
		IsF2FSColdData:     c.flags.IsF2FSColdData,
	}, true
}

// normalizeUfsAction — UFS row 의 action 문자열을 정규화하고 mgmt 여부를 가른다.
//
// UFS row 는 데이터 IO(`ufshcd_command:*`) 와 management 이벤트(Query/TM UPIU, UIC,
// exception) 두 갈래다. 둘 다 같은 stream 으로 받고 IsMgmt 로 구분한다 —
// 분석가가 한 타임라인에서 같이 봐야 하기 때문.
//
// action 접두어(`upiu_`/`uic_`)는 데이터 IO 의 send_req/complete_rsp 와 충돌을 막고,
// 차트의 substring 필터에서 "프로토콜 트래픽만" 토글이 된다.
func normalizeUfsAction(raw string, extra map[string]string) (action string, isMgmt, ok bool) {
	switch {
	case strings.HasPrefix(raw, "ufshcd_command:"):
		s := strings.TrimPrefix(raw, "ufshcd_command:")
		if s == "" {
			return "", false, false
		}
		return s, false, true
	case strings.HasPrefix(raw, "ufshcd_upiu:"):
		s := strings.TrimPrefix(raw, "ufshcd_upiu:")
		if s == "" {
			return "", false, false
		}
		return "upiu_" + s, true, true
	case raw == "ufshcd_uic_command":
		// dir 은 producer 가 나중에 추가한 키 — 구 producer 는 안 준다.
		// 방향을 모르면 send/complete 페어링이 불가하므로 "uic" 로 남겨
		// processor 가 latency 계산을 건너뛰게 한다.
		switch extra["dir"] {
		case "send":
			return "uic_send", true, true
		case "comp":
			return "uic_complete", true, true
		default:
			return "uic", true, true
		}
	case raw == "ufshcd_exception_event":
		return "exception", true, true
	default:
		// 그 외 UFS action 은 아직 모르는 종류 — 조용히 버린다.
		return "", false, false
	}
}

// optU8 / optU8Dec / optU32 — extra 에 키가 있을 때만 값을 채운다.
// nil 은 "그 값이 없었다" 는 뜻이고 0 과 구분된다.
func optU8(extra map[string]string, key string) *uint8 {
	s, ok := extra[key]
	if !ok {
		return nil
	}
	v := parseHexU8(s)
	return &v
}

func optU8Dec(extra map[string]string, key string) *uint8 {
	s, ok := extra[key]
	if !ok {
		return nil
	}
	n, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		return nil
	}
	v := uint8(n)
	return &v
}

func optU32(extra map[string]string, key string) *uint32 {
	s, ok := extra[key]
	if !ok {
		return nil
	}
	v := parseHexU32(s)
	return &v
}

// parseFsioUfsLine — layer="UFS" 한 줄 → FsioUfsEvent.
func parseFsioUfsLine(line string) (FsioUfsEvent, bool) {
	c, ok := splitFsioCols(line)
	if !ok || c.layer != "UFS" {
		return FsioUfsEvent{}, false
	}
	action, isMgmt, ok := normalizeUfsAction(c.rawAction, c.extra)
	if !ok {
		return FsioUfsEvent{}, false
	}

	// LU 마다 LBA 주소공간이 독립이고 tag 도 LU 별로 재사용되므로 반드시 보존한다.
	// bpftrace 는 lun 을 못 얻으면 `lun=?` 로 내보낸다. 0 으로 폴백하면
	// **실제 LU0 과 구분이 안 되므로** 미상은 미상으로 남긴다.
	lun := LunUnknown
	if s, has := c.extra["lun"]; has && s != "?" {
		if n, err := strconv.ParseUint(s, 10, 8); err == nil {
			lun = uint8(n)
		}
	}

	var tag uint32
	if s, has := c.extra["tag"]; has {
		tag = atoiU32(s)
	}
	var hwq int32
	if s, has := c.extra["hwq"]; has {
		if n, err := strconv.ParseInt(s, 10, 32); err == nil {
			hwq = int32(n)
		}
	}
	var opcode uint8
	if s, has := c.extra["ufs_op"]; has {
		opcode = parseHexU8(s)
	}
	// grp 은 커널의 __u16 (fsiotrace.h ufs_group_id) — u8 로 받으면 0x100 이상이 잘린다.
	var grp uint16
	if s, has := c.extra["grp"]; has {
		grp = parseHexU16(s)
	}

	txn := optU8(c.extra, "txn")
	upiuFlags := optU8(c.extra, "flags")
	upiuFunc := optU8(c.extra, "func")
	upiuCp := optU8Dec(c.extra, "cp")
	// mgmt 전용 키 — 전부 optional. 구 producer 는 안 주므로 없으면 nil.
	upiuResp := optU8(c.extra, "resp")
	upiuStatus := optU8(c.extra, "status")
	queryOpcode := optU8(c.extra, "qop")
	queryIdn := optU8(c.extra, "idn")
	queryIndex := optU8Dec(c.extra, "qidx")
	querySelector := optU8Dec(c.extra, "qsel")
	uicCmd := optU32(c.extra, "uic_cmd")

	// 표시 이름은 파싱 시점에 구워 parquet 컬럼으로 저장한다.
	// SQL 쪽에서 이름 테이블을 다시 구현하지 않게 하는 게 목적.
	mgmtName := ""
	if isMgmt {
		switch {
		case uicCmd != nil:
			mgmtName = UICDisplay(*uicCmd)
		case txn != nil && (*txn == 0x04 || *txn == 0x24):
			// TM UPIU — 종류는 func 가 가른다.
			var fn uint8
			if upiuFunc != nil {
				fn = *upiuFunc
			}
			mgmtName = TMDisplay(fn)
		case queryOpcode != nil:
			var idn uint8
			if queryIdn != nil {
				idn = *queryIdn
			}
			mgmtName = QueryDisplay(*queryOpcode, idn)
		default:
			// 종류를 특정할 정보가 없으면 action 그대로 (nop_out/rtt/exception 등).
			mgmtName = action
		}
	}

	return FsioUfsEvent{
		Time:               c.time,
		Process:            c.comm,
		CPU:                c.cpu,
		Action:             action,
		Tag:                tag,
		Opcode:             opcode,
		LUN:                lun,
		LBA:                c.sec,
		Size:               c.size,
		GroupID:            uint32(grp),
		HwqID:              hwq,
		Aligned:            isUFSAligned(c.sec),
		PID:                c.pid,
		TID:                c.tid,
		Comm:               c.comm,
		Syscall:            c.syscall,
		FS:                 c.fs,
		Ino:                c.ino,
		Name:               c.name,
		IOFlags:            c.ioFlags,
		IsRead:             c.flags.IsRead,
		IsWrite:            c.flags.IsWrite,
		IsDiscard:          c.flags.IsDiscard,
		IsFlush:            c.flags.IsFlush,
		IsTrim:             c.flags.IsTrim,
		IsOSync:            c.flags.IsOSync,
		IsODirect:          c.flags.IsODirect,
		IsOAppend:          c.flags.IsOAppend,
		IsODsync:           c.flags.IsODsync,
		IsSyncPath:         c.flags.IsSyncPath,
		IsReqSync:          c.flags.IsReqSync,
		IsReqPrio:          c.flags.IsReqPrio,
		IsReqRahead:        c.flags.IsReqRahead,
		IsData:             c.flags.IsData,
		IsMetadata:         c.flags.IsMetadata,
		IsInode:            c.flags.IsInode,
		IsBitmap:           c.flags.IsBitmap,
		IsDirent:           c.flags.IsDirent,
		IsXattr:            c.flags.IsXattr,
		IsJournal:          c.flags.IsJournal,
		IsCheckpoint:       c.flags.IsCheckpoint,
		IsGC:               c.flags.IsGC,
		IsExtentAlloc:      c.flags.IsExtentAlloc,
		IsExtentFree:       c.flags.IsExtentFree,
		IsBmap:             c.flags.IsBmap,
		IsBuffered:         c.flags.IsBuffered,
		IsDirectIO:         c.flags.IsDirectIO,
		IsMmapWriteback:    c.flags.IsMmapWriteback,
		IsWritebackKworker: c.flags.IsWritebackKworker,
		IsFsyncTriggered:   c.flags.IsFsyncTriggered,
		IsSawVfs:           c.flags.IsSawVfs,
		IsF2FSNodeWrite:    c.flags.IsF2FSNodeWrite,
		IsF2FSDataWrite:    c.flags.IsF2FSDataWrite,
		IsF2FSMetaWrite:    c.flags.IsF2FSMetaWrite,
		IsF2FSNodeGC:       c.flags.IsF2FSNodeGC,
		IsF2FSDataGC:       c.flags.IsF2FSDataGC,
		IsF2FSHotData:      c.flags.IsF2FSHotData,
		IsF2FSWarmData:     c.flags.IsF2FSWarmData,
		IsF2FSColdData:     c.flags.IsF2FSColdData,
		Txn:                txn,
		UpiuFlags:          upiuFlags,
		UpiuFunc:           upiuFunc,
		UpiuAttr:           c.extra["attr"],
		UpiuCp:             upiuCp,
		IsMgmt:             isMgmt,
		UpiuResp:           upiuResp,
		UpiuStatus:         upiuStatus,
		QueryOpcode:        queryOpcode,
		QueryIdn:           queryIdn,
		QueryIndex:         queryIndex,
		QuerySelector:      querySelector,
		UicCmd:             uicCmd,
		MgmtName:           mgmtName,
	}, true
}
