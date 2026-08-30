package parser

import (
	"strconv"
	"strings"
)

// FSIO_SUMMARY — bpftrace fsiotrace 의 기계 판독용 수집 품질 요약 파서.
//
// 명세: `../bpftrace/docs/OUTPUT_FORMAT.md` §"수집 품질 — FSIO_SUMMARY".
// Rust `../trace/src/parsers/fsio_summary.rs` 의 포팅.
//
// ## 왜 필요한가 — 이게 없으면 cache 판정이 조용히 틀린다
//
// CACHE_HIT_INFERRED 는 "FS page-fill 훅이 한 번도 안 불렸다" 는 **음성 증거**다.
// 그래서 "훅이 안 불렸다" 와 "훅이 애초에 안 붙었다" 를 갈라야 하는데, 그 정보가
// 여기 `<fs>_cache_coverage` 에만 있다. **모르면 훅 미부착이 전부 hit 으로 둔갑한다.**
//
// ## 어디에 있나
//
// `fsiotrace > fsio.log` 로 받으면 이벤트 스트림 안에 `# FSIO_SUMMARY ...` 주석 줄로
// 들어온다 (실기기에서 가장 흔한 형태). `#` 로 시작해 quickFsioCheck 에 안 걸리므로
// TSV 파서가 자연히 버린다 — 그래서 **별도 스캔**으로 집는다.

// FsioSummary — FSIO_SUMMARY 한 줄을 분해한 수집 품질 정보.
type FsioSummary struct {
	// Coverage — filesystem 이름 → 증거 훅이 전부 붙었나. 키는 ext4/f2fs/erofs/iomap.
	Coverage map[string]bool
	// RbDrop — ringbuf 유실. >0 이면 이 로그의 집계는 **하한값**이다.
	RbDrop uint64
	// PairLost — read_enter - read_exit. >0 이면 그만큼 요약이 안 나왔다.
	PairLost uint64
	// EvidNoent — 증거 훅이 컨텍스트를 못 찾은 횟수.
	EvidNoent uint64
	// EvidOvf — 증거 카운터 포화. 해당 row 는 UNKNOWN.
	EvidOvf uint64
	// RaSyncHook — page_cache_sync_ra 훅이 붙었나. 0 이면 demand/prefetch 구분 불가.
	RaSyncHook  bool
	RaAsyncHook bool
	// ReadExitHook — vfs_read kretprobe. 0 이면 종료 요약 자체가 안 나온다.
	ReadExitHook bool
	IterExitHook bool
}

// CoverageOK — 이 fs 의 hit 판정을 믿어도 되나.
//
// 알 수 없는 fs 이름(요약에 없는 fs)은 **false** — 모르면 신뢰하지 않는다.
// 빈 fs 이름도 false (어느 훅 집합이 필요한지 알 수 없다).
func (s *FsioSummary) CoverageOK(fs string) bool {
	if s == nil || fs == "" {
		return false
	}
	return s.Coverage[fs]
}

// HasQualityWarning — 로그 전체 수준의 문제. 있으면 통계 상단에 경고해야 한다.
func (s *FsioSummary) HasQualityWarning() bool {
	if s == nil {
		return true
	}
	if s.RbDrop > 0 || s.PairLost > 0 || s.EvidOvf > 0 || s.EvidNoent > 0 || !s.RaSyncHook {
		return true
	}
	for _, ok := range s.Coverage {
		if !ok {
			return true
		}
	}
	return false
}

// ParseFsioSummaryLine — `FSIO_SUMMARY v=1 events=... key=value ...` 한 줄 파싱.
//
// `# ` 접두사 유무 양쪽을 받는다 (로그 안 주석 줄 / .summary 파일).
// 해당 줄이 아니면 nil.
func ParseFsioSummaryLine(line string) *FsioSummary {
	body := strings.TrimLeft(line, " \t")
	body = strings.TrimLeft(strings.TrimPrefix(body, "#"), " \t")
	if !strings.HasPrefix(body, "FSIO_SUMMARY") {
		return nil
	}

	// 순서에 의존하지 않는 key=value 파싱 — 알 수 없는 key 는 자연히 무시된다.
	kv := make(map[string]string, 24)
	for _, tok := range strings.Fields(body) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			kv[k] = v
		}
	}

	num := func(k string) uint64 {
		v, _ := strconv.ParseUint(kv[k], 10, 64)
		return v
	}
	// 훅 플래그는 **없으면 false** — 모르면 신뢰하지 않는 방향으로 기운다.
	flag := func(k string) bool { return kv[k] == "1" }

	coverage := make(map[string]bool, 4)
	for _, fs := range []string{"ext4", "f2fs", "erofs", "iomap"} {
		// 키가 아예 없으면(구버전 producer) 등록하지 않는다 →
		// CoverageOK() 가 false 를 돌려 UNKNOWN 으로 강등된다.
		if v, ok := kv[fs+"_cache_coverage"]; ok {
			coverage[fs] = v == "1"
		}
	}

	// pair_lost 는 producer 가 직접 준다. 없으면 read_enter - read_exit 로 복원.
	var pairLost uint64
	if _, ok := kv["pair_lost"]; ok {
		pairLost = num("pair_lost")
	} else if e, x := num("read_enter"), num("read_exit"); e > x {
		pairLost = e - x
	}

	return &FsioSummary{
		Coverage:     coverage,
		RbDrop:       num("rb_drop"),
		PairLost:     pairLost,
		EvidNoent:    num("evid_noent"),
		EvidOvf:      num("evid_ovf"),
		RaSyncHook:   flag("ra_sync_hook"),
		RaAsyncHook:  flag("ra_async_hook"),
		ReadExitHook: flag("read_exit_hook"),
		IterExitHook: flag("iter_exit_hook"),
	}
}
