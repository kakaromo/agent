package parser

import (
	"strconv"
	"strings"
)

// VFS read 종료 요약 row (`vfs_read:exit` / `readv:exit`) 파싱 + page-cache 분류.
//
// Rust `../trace/src/parsers/bpftrace_tsv.rs::build_fsio_read` 와
// `../trace/src/processors/fsio_read.rs` 의 포팅.
//
// ## 분류 규칙의 정본은 여기(그리고 Rust)다 — bpftrace 의 cls= 가 아니다
//
// OUTPUT_FORMAT.md: "정본은 fill/sync_ra/async_ra 원시값이고, 분석기는 거기서 직접
// 계산하는 것이 원칙". BPF 의 1차 판정은 BpfCls 에 보존해 대조에 쓴다.

// cache 분류 라벨. parquet cache_class 컬럼에 그대로 들어간다.
const (
	CacheClassHit     = "CACHE_HIT_INFERRED"
	CacheClassMiss    = "CACHE_MISS"
	CacheClassDirect  = "DIRECT_IO"
	CacheClassEOF     = "EOF"
	CacheClassError   = "ERROR"
	CacheClassUnknown = "UNKNOWN"
)

// CountsTowardRatio — hit ratio 분모에 들어가는 class 인가.
//
// DIRECT_IO / EOF / ERROR / UNKNOWN 은 "캐시를 맞췄나" 라는 질문 자체가 성립하지
// 않으므로 분모에서 뺀다.
func CountsTowardRatio(cacheClass string) bool {
	return cacheClass == CacheClassHit || cacheClass == CacheClassMiss
}

// FsioReadActionMmap — mmap page fault 종료 요약 action.
//
// SQL 리터럴로도 쓰이므로(집계에서 분모 분리) 상수로 둔다.
const FsioReadActionMmap = "mmap_fault:exit"

// isFsioReadAction — 이 action 이 VFS read 종료 요약인가.
//
// ⚠ mmap_fault:exit 은 hit 계산식이 다르다 — fault-around 때문에 캐시에 있는
//
//	페이지는 fault 를 아예 안 낸다. 즉 mmap 에서 hit 은 "행이 나오는 것" 이 아니라
//	**"행이 없는 것"** 이다. 그래서 파싱은 같이 하되 통계에서 read 와 분모를 섞지
//	않는다 (ComputeFsioReadStats 의 mmap 분리 참조).
//
//	섞으면 캐시는 그대로인데 mmap 을 쓴다는 이유만으로 적중률이 깎인다 —
//	실측 fio_mmap_sample.log 기준 74.22% → 59.63% (14.6%p 가 근거 없이 사라진다).
func isFsioReadAction(a string) bool {
	return a == "vfs_read:exit" || a == "readv:exit" || a == FsioReadActionMmap
}

// IsMmapFault — 이 행이 mmap page fault 인가. read 와 **모집단이 다르다.**
func (e *FsioReadEvent) IsMmapFault() bool {
	return e.Action == FsioReadActionMmap
}

// parseFsioReadLine — layer="VFS" 의 read 종료 요약 한 줄 → FsioReadEvent.
//
// ⚠ 여기서는 **원시 증거만 채운다.** cache 분류(CacheClass/Coverage)는
// ClassifyFsioRead 가 FSIO_SUMMARY 와 함께 계산한다 — 파서는 로그 전역
// 정보(coverage)를 모르기 때문이다.
func parseFsioReadLine(line string) (FsioReadEvent, bool) {
	c, ok := splitFsioCols(line)
	if !ok || c.layer != "VFS" || !isFsioReadAction(c.rawAction) {
		return FsioReadEvent{}, false
	}

	// ret 는 **부호 있음** — 음수는 -errno, 0 은 EOF.
	// 키가 없으면 0 이 되는데 그건 EOF 와 구분이 안 되므로 아래에서 사유를 남긴다.
	rawResult, _ := strconv.ParseInt(c.extra["ret"], 10, 64)
	requested := atoiU64(c.extra["req"])
	offset := atoiU64(c.extra["off"])

	fillUnits := atoiU32(c.extra["fill"])
	syncRa := atoiU32(c.extra["sync_ra"])
	asyncRa := atoiU32(c.extra["async_ra"])
	fillPages := atoiU32(c.extra["fill_pg"])
	readID := atoiU32(c.extra["rid"])

	// 수집 품질 사유 — 파서 단계에서 알 수 있는 것만. coverage 는 분류기가 덧붙인다.
	// `evr=` 는 문제가 있을 때만 등장하므로 없는 게 정상이다.
	var reasons []string
	if evr, has := c.extra["evr"]; has {
		// 값은 "ovf," / "noent," / "nest," 의 조합 (trailing comma 포함).
		if containsToken(evr, "ovf") {
			reasons = append(reasons, "overflow")
		}
		if containsToken(evr, "noent") {
			reasons = append(reasons, "no_evidence_ctx")
		}
		if containsToken(evr, "nest") {
			reasons = append(reasons, "nested")
		}
	}
	// 판정 필수 키 누락 — 이 행은 신뢰할 수 없다.
	if _, has := c.extra["ret"]; !has {
		reasons = append(reasons, "missing_ret")
	}
	if _, has := c.extra["req"]; !has {
		reasons = append(reasons, "missing_req")
	}

	ev := FsioReadEvent{
		Time:       c.time,
		LineNumber: 0, // 호출부가 채운다
		Pid:        c.pid,
		Tid:        c.tid,
		CPU:        c.cpu,
		Comm:       c.comm,
		Syscall:    c.syscall,
		Action:     c.rawAction,
		ReadID:     readID,

		Fs:       c.fs,
		DevMajor: c.devMajor,
		DevMinor: c.devMinor,
		Ino:      c.ino,
		Name:     c.name,

		Offset:         offset,
		RequestedBytes: requested,
		RawResult:      rawResult,

		IoFlags:    c.ioFlags,
		IsBuffered: c.flags.IsBuffered,
		// O_DIRECT(bit 9) 와 DIRECT_IO(bit 33) 중 하나라도 서면 direct.
		IsDirect:     c.flags.IsODirect || c.flags.IsDirectIO,
		FillUnits:    fillUnits,
		SyncRaUnits:  syncRa,
		AsyncRaUnits: asyncRa,
		FillPages:    fillPages,
		EvidenceFs:   c.extra["evfs"],

		// FS page-fill/read 증거가 있었나 — miss 의 근거.
		FsReadSeen: fillUnits > 0 || syncRa > 0,
		// 분류기가 FSIO_SUMMARY 로 채운다. 그 전까지는 "모름".
		Coverage:      "unknown",
		CacheClass:    CacheClassUnknown,
		QualityReason: joinReasons(reasons),
		BpfCls:        c.extra["cls"],
	}
	// 음수(error)면 0. 진짜 값은 RawResult 에 있다.
	if rawResult > 0 {
		ev.ReturnedBytes = uint64(rawResult)
	}
	// dur_ns 키가 없으면 nil — 0 으로 채우면 "정말 0ns" 와 구분이 안 된다.
	if v, has := c.extra["dur_ns"]; has {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			ev.DurationNs = &n
		}
	}
	if len(reasons) == 0 {
		ev.Quality = "ok"
	} else {
		ev.Quality = "suspect"
	}
	return ev, true
}

// containsToken — evr= 값에 사유가 들어 있나.
//
// Rust 는 `evr.contains("ovf")` 로 단순 substring 검사를 한다. 값이 "ovf,noent," 처럼
// trailing comma 를 포함한 조합이라 토큰 분리보다 substring 이 안전하다
// (구 producer 가 구분자를 바꿔도 계속 걸린다). 같은 의미를 유지한다.
func containsToken(evr, token string) bool {
	return strings.Contains(evr, token)
}

// joinReasons — 사유를 쉼표로 잇는다. 없으면 빈 문자열.
func joinReasons(reasons []string) string {
	return strings.Join(reasons, ",")
}

// appendReason — 기존 사유에 하나를 덧붙인다 (중복은 넣지 않는다).
func appendReason(existing, reason string) string {
	if existing == "" {
		return reason
	}
	for _, r := range strings.Split(existing, ",") {
		if r == reason {
			return existing
		}
	}
	return existing + "," + reason
}

// ClassifyFsioRead — 각 행에 Coverage / CacheClass / Quality 를 채운다.
//
// summary 가 nil 이면(로그에 FSIO_SUMMARY 가 없음) 모든 행이 coverage 를 알 수 없으므로
// **UNKNOWN 으로 강등**된다. 이게 안전한 방향이다 — 모르는 상태에서 hit 이라고 말하면
// 훅 미부착을 캐시 적중으로 보고하게 된다.
func ClassifyFsioRead(list []FsioReadEvent, summary *FsioSummary) []FsioReadEvent {
	// ra_sync_hook 이 없으면 demand miss(sync_ra)와 선반입(async_ra)을 가를 수 없다.
	// 그러면 hit/miss 판정 자체가 성립하지 않으므로 전 행을 UNKNOWN 으로 둔다.
	raSplitAvailable := summary != nil && summary.RaSyncHook

	for i := range list {
		ev := &list[i]

		cov := "unknown"
		if summary != nil {
			if summary.CoverageOK(ev.Fs) {
				cov = "ok"
			} else {
				cov = "missing"
			}
		}
		ev.Coverage = cov

		class, extraReason := classifyOneFsioRead(ev, cov, raSplitAvailable)
		ev.CacheClass = class
		if extraReason != "" {
			ev.QualityReason = appendReason(ev.QualityReason, extraReason)
		}
		// 사유가 하나라도 붙었으면 이 행의 수치를 그대로 믿으면 안 된다.
		if ev.QualityReason == "" {
			ev.Quality = "ok"
		} else {
			ev.Quality = "suspect"
		}
	}
	return list
}

// classifyOneFsioRead — 한 행의 분류. 두 번째 반환값은 덧붙일 quality 사유(있으면).
//
// **우선순위가 곧 분석 의도다. 순서를 바꾸지 말 것.**
//
// ⚠ BPF 의 1차 판정(fsiotrace.bpf.c 의 emit_read_exit)은 **DIRECT 를 ERROR 보다 먼저**
//
//	본다. 여기서는 의도적으로 다르다 — 실패한 read 는 O_DIRECT 였든 아니든 먼저
//	"실패" 로 보고돼야 한다. 원본 판정은 BpfCls 에 보존돼 있어 대조 가능하다.
func classifyOneFsioRead(ev *FsioReadEvent, coverage string, raSplitAvailable bool) (string, string) {
	// 1. ERROR — 음수 반환값은 -errno.
	if ev.RawResult < 0 {
		return CacheClassError, ""
	}

	// 2. EOF — 요청은 있었는데 0 바이트를 받았다. 오류가 아닌 정상 0 반환.
	if ev.RawResult == 0 && ev.RequestedBytes > 0 {
		return CacheClassEOF, ""
	}

	// 3. DIRECT_IO — page cache 를 우회하므로 캐시 증거가 무의미하다.
	if ev.IsDirect {
		return CacheClassDirect, ""
	}

	// 4. UNKNOWN — 증거를 신뢰할 수 없는 모든 경우.
	//    ⚠ 이 단계가 hit 보다 위에 있어야 한다. 아래로 내리면 "증거가 없다" 가
	//      "훅이 안 불렸다(=hit)" 로 둔갑한다.
	if ev.QualityReason != "" {
		// 파서가 이미 evr(ovf/noent/nest)나 필수 키 누락을 기록했다.
		return CacheClassUnknown, ""
	}
	if coverage != "ok" {
		// 이 fs 의 증거 훅이 안 붙었거나, 요약 자체가 없어 알 수 없다.
		return CacheClassUnknown, "coverage_missing"
	}
	if !raSplitAvailable {
		// sync/async readahead 를 못 가르면 demand miss 와 선반입이 섞인다.
		return CacheClassUnknown, "ra_hook_missing"
	}
	if !ev.IsBuffered {
		// buffered 도 direct 도 아니다 — 어느 경로인지 모른다.
		return CacheClassUnknown, "not_buffered"
	}

	// 5. CACHE_MISS — FS page-fill/read 증거가 있었다.
	if ev.FsReadSeen {
		// fill 은 있는데 sync_ra 가 없고 async_ra 가 있으면, 그 fill 이 demand 에서
		// 왔는지 선반입에서 왔는지 이 계층에서는 못 가른다(bpftrace 의 miss_ra).
		// miss 로 세되 모호하다는 사실은 남긴다.
		if ev.SyncRaUnits == 0 && ev.FillUnits > 0 && ev.AsyncRaUnits > 0 {
			return CacheClassMiss, "ambiguous_readahead"
		}
		return CacheClassMiss, ""
	}

	// 6. CACHE_HIT_INFERRED — buffered, 반환 바이트 있음, coverage ok, 증거 0.
	//    async_ra > 0 이어도 hit 이다 — 반환 바이트는 캐시에서 왔고 다음 창을
	//    미리 채웠을 뿐이다. 이걸 miss 로 세면 순차 read 의 hit 이 통째로 뒤집힌다.
	return CacheClassHit, ""
}
