package parser

import (
	"strings"
	"testing"
)

// VFS read 종료 요약 파싱 + page-cache 분류.
//
// 정본은 Rust `../trace/src/processors/fsio_read.rs` 이고, 같은 fixture
// (`../trace/tests/fixtures/fsio_cache_sample.log`) 로 33컬럼 row-by-row 대조를 마쳤다.
// 여기서는 그 대조가 지켜야 할 **규칙**을 고정한다 — fixture 없이도 깨지면 잡히도록.

// rdLine — VFS read 종료 요약 한 줄 조립. extra 만 바꿔 가며 쓴다.
func rdLine(action, syscall, fs, ioFlags, extra string) string {
	cols := []string{
		"3.082789", "VFS", "118", "118", "1", "dd", syscall, action, fs,
		"8", "0", "33", "65536", "0", "/data/ex/a.bin", ioFlags, extra,
	}
	return strings.Join(cols, "\t")
}

// io_flags — bit 32 = IO_BUFFERED, bit 9 = IO_O_DIRECT.
const (
	flagsBuffered = "0x0000000100000000"
	flagsDirect   = "0x0000000000000200"
	flagsNone     = "0x0"
)

func summaryOK() *FsioSummary {
	return &FsioSummary{
		Coverage:     map[string]bool{"ext4": true, "f2fs": true},
		RaSyncHook:   true,
		RaAsyncHook:  true,
		ReadExitHook: true,
		IterExitHook: true,
	}
}

// classify1 — 한 줄을 파싱해 분류까지 끝낸 결과.
func classify1(t *testing.T, line string, s *FsioSummary) FsioReadEvent {
	t.Helper()
	ev, ok := parseFsioReadLine(line)
	if !ok {
		t.Fatalf("parse 실패: %q", line)
	}
	return ClassifyFsioRead([]FsioReadEvent{ev}, s)[0]
}

func TestFsioReadAcceptsBothExitActions(t *testing.T) {
	// vfs_read:exit 과 readv:exit 둘 다 종료 요약이다. 하나만 받으면 readv/preadv
	// 경로의 read 가 통째로 사라진다.
	for _, action := range []string{"vfs_read:exit", "readv:exit"} {
		if _, ok := parseFsioReadLine(rdLine(action, "vfs_read", "ext4", flagsBuffered, "ret=4096 req=4096")); !ok {
			t.Errorf("%s 를 받지 못했다", action)
		}
	}
	// 진입 row(:exit 아님)는 종료 요약이 아니다 — 받으면 한 read 가 두 번 세어진다.
	for _, action := range []string{"vfs_read", "readv", "vfs_write"} {
		if _, ok := parseFsioReadLine(rdLine(action, "vfs_read", "ext4", flagsBuffered, "ret=4096 req=4096")); ok {
			t.Errorf("%s 는 종료 요약이 아닌데 받았다", action)
		}
	}
}

// 분류 우선순위가 곧 분석 의도다. 순서가 바뀌면 조용히 틀린 값이 나온다.
func TestFsioReadClassifyPriority(t *testing.T) {
	tests := []struct {
		name   string
		flags  string
		extra  string
		want   string
		reason string
	}{
		// ERROR 는 DIRECT 보다 먼저다 — 실패한 read 는 O_DIRECT 였든 아니든 "실패".
		// ⚠ BPF 의 1차 판정(cls=)은 반대 순서라 여기서 의도적으로 갈린다.
		{"error 가 direct 를 이긴다", flagsDirect, "ret=-5 req=4096 cls=direct", CacheClassError, ""},
		{"음수 반환은 ERROR", flagsBuffered, "ret=-22 req=4096", CacheClassError, ""},
		{"0 반환 + 요청 있음은 EOF", flagsBuffered, "ret=0 req=4096", CacheClassEOF, ""},
		{"O_DIRECT 는 DIRECT_IO", flagsDirect, "ret=4096 req=4096", CacheClassDirect, ""},
		// 증거 0 + buffered + coverage ok → hit.
		{"증거 없으면 hit", flagsBuffered, "ret=4096 req=4096 fill=0 sync_ra=0 async_ra=0", CacheClassHit, ""},
		// async_ra 가 있어도 hit 이다. 이걸 miss 로 세면 순차 read 의 hit 이 통째로 뒤집힌다.
		{"async_ra 만 있으면 여전히 hit", flagsBuffered, "ret=4096 req=4096 fill=0 sync_ra=0 async_ra=3", CacheClassHit, ""},
		{"fill 있으면 miss", flagsBuffered, "ret=4096 req=4096 fill=2 sync_ra=1 async_ra=0", CacheClassMiss, ""},
		// fill 은 있는데 sync_ra 가 없고 async_ra 가 있으면 출처를 못 가른다.
		{"모호한 readahead 는 miss + 사유", flagsBuffered, "ret=4096 req=4096 fill=1 sync_ra=0 async_ra=1", CacheClassMiss, "ambiguous_readahead"},
		// buffered 도 direct 도 아니면 어느 경로인지 모른다 — hit 이라고 하면 안 된다.
		{"buffered 아니면 UNKNOWN", flagsNone, "ret=4096 req=4096 fill=0", CacheClassUnknown, "not_buffered"},
		// ⚠ ret 누락은 UNKNOWN 이 아니다. ret 이 없으면 raw_result 가 0 이라 EOF 규칙(2번)에
		// 먼저 걸린다 — 우선순위가 UNKNOWN(4번)보다 위이기 때문. Rust 도 같은 결과를 낸다
		// (EOF / suspect / missing_ret 로 실측 대조 완료). class 는 못 믿더라도 quality=suspect
		// 와 사유가 남아 소비 측이 걸러낼 수 있다.
		{"ret 없으면 EOF + suspect", flagsBuffered, "req=4096 fill=0", CacheClassEOF, "missing_ret"},
		{"evr=ovf 면 UNKNOWN", flagsBuffered, "ret=4096 req=4096 fill=0 evr=ovf,", CacheClassUnknown, "overflow"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := classify1(t, rdLine("vfs_read:exit", "vfs_read", "ext4", tc.flags, tc.extra), summaryOK())
			if ev.CacheClass != tc.want {
				t.Errorf("cache_class = %q, want %q", ev.CacheClass, tc.want)
			}
			if tc.reason != "" && !strings.Contains(ev.QualityReason, tc.reason) {
				t.Errorf("quality_reason = %q, want %q 포함", ev.QualityReason, tc.reason)
			}
			// 사유가 붙었으면 quality 는 suspect 여야 한다.
			wantQ := "ok"
			if ev.QualityReason != "" {
				wantQ = "suspect"
			}
			if ev.Quality != wantQ {
				t.Errorf("quality = %q, want %q (reason=%q)", ev.Quality, wantQ, ev.QualityReason)
			}
		})
	}
}

// ⚠ 이 테스트가 이 기능의 핵심 안전장치다.
//
// CACHE_HIT_INFERRED 는 "훅이 안 불렸다" 는 음성 증거다. coverage 를 모르는 채
// hit 이라고 하면 **훅 미부착이 전부 캐시 적중으로 둔갑한다.**
func TestFsioReadNeverClaimsHitWithoutCoverage(t *testing.T) {
	line := rdLine("vfs_read:exit", "vfs_read", "ext4", flagsBuffered, "ret=4096 req=4096 fill=0 sync_ra=0 cls=hit")

	// 1. FSIO_SUMMARY 자체가 없다 → 알 수 없다.
	if ev := classify1(t, line, nil); ev.CacheClass != CacheClassUnknown {
		t.Errorf("summary 없음: %q, want UNKNOWN (bpf 는 hit 이라 해도)", ev.CacheClass)
	}
	// 2. 이 fs 의 coverage 가 꺼져 있다.
	if ev := classify1(t, line, &FsioSummary{Coverage: map[string]bool{"ext4": false}, RaSyncHook: true}); ev.CacheClass != CacheClassUnknown {
		t.Errorf("coverage=0: %q, want UNKNOWN", ev.CacheClass)
	}
	// 3. 요약에 없는 fs (rootfs 등) — 모르면 신뢰하지 않는다. 실측 fixture 에 나오는 사례다.
	rootfs := rdLine("vfs_read:exit", "vfs_read", "rootfs", flagsBuffered, "ret=4096 req=4096 fill=0 cls=hit")
	ev := classify1(t, rootfs, summaryOK())
	if ev.CacheClass != CacheClassUnknown || ev.Coverage != "missing" {
		t.Errorf("모르는 fs: class=%q coverage=%q, want UNKNOWN/missing", ev.CacheClass, ev.Coverage)
	}
	// BPF 의 1차 판정은 대조용으로 남아 있어야 한다.
	if ev.BpfCls != "hit" {
		t.Errorf("bpf_cls = %q, want %q (대조용 보존)", ev.BpfCls, "hit")
	}
	// 4. ra_sync_hook 이 없으면 demand miss 와 선반입을 못 가른다.
	noRa := &FsioSummary{Coverage: map[string]bool{"ext4": true}, RaSyncHook: false}
	if ev := classify1(t, line, noRa); ev.CacheClass != CacheClassUnknown {
		t.Errorf("ra_sync_hook 없음: %q, want UNKNOWN", ev.CacheClass)
	}
}

// dur_ns 가 없으면 nil 이어야 한다. 0 으로 채우면 "정말 0ns" 와 구분이 안 된다.
func TestFsioReadDurationAbsentIsNil(t *testing.T) {
	ev := classify1(t, rdLine("vfs_read:exit", "vfs_read", "ext4", flagsBuffered, "ret=4096 req=4096"), summaryOK())
	if ev.DurationNs != nil {
		t.Errorf("dur_ns 없음인데 %v, want nil", *ev.DurationNs)
	}
	ev = classify1(t, rdLine("vfs_read:exit", "vfs_read", "ext4", flagsBuffered, "ret=4096 req=4096 dur_ns=1087375"), summaryOK())
	if ev.DurationNs == nil || *ev.DurationNs != 1087375 {
		t.Errorf("dur_ns 파싱 실패: %v", ev.DurationNs)
	}
}

// 음수 ret 은 returned_bytes 0, raw_result 에 원값 보존.
func TestFsioReadNegativeReturnedBytes(t *testing.T) {
	ev := classify1(t, rdLine("vfs_read:exit", "vfs_read", "ext4", flagsBuffered, "ret=-5 req=4096"), summaryOK())
	if ev.ReturnedBytes != 0 {
		t.Errorf("returned_bytes = %d, want 0", ev.ReturnedBytes)
	}
	if ev.RawResult != -5 {
		t.Errorf("raw_result = %d, want -5 (원값 보존)", ev.RawResult)
	}
}

// hit ratio 분모 — "캐시를 맞췄나" 가 성립하는 class 만 센다.
func TestCountsTowardRatio(t *testing.T) {
	in := []string{CacheClassHit, CacheClassMiss}
	out := []string{CacheClassDirect, CacheClassEOF, CacheClassError, CacheClassUnknown}
	for _, c := range in {
		if !CountsTowardRatio(c) {
			t.Errorf("%s 는 분모에 들어가야 한다", c)
		}
	}
	for _, c := range out {
		if CountsTowardRatio(c) {
			t.Errorf("%s 는 분모에서 빠져야 한다", c)
		}
	}
}

func TestParseFsioSummaryLine(t *testing.T) {
	line := "# FSIO_SUMMARY v=1 events=3300 rb_drop=0 read_enter=363 read_exit=363 pair_lost=0 " +
		"nested=0 evid_noent=0 evid_ovf=0 ext4_cache_coverage=1 f2fs_cache_coverage=0 " +
		"ra_sync_hook=1 ra_async_hook=1 read_exit_hook=1 iter_exit_hook=1"
	s := ParseFsioSummaryLine(line)
	if s == nil {
		t.Fatal("파싱 실패")
	}
	if !s.CoverageOK("ext4") {
		t.Error("ext4 coverage 는 ok 여야")
	}
	if s.CoverageOK("f2fs") {
		t.Error("f2fs 는 coverage=0 이라 false 여야")
	}
	// 요약에 없는 fs / 빈 이름은 모르므로 false.
	if s.CoverageOK("rootfs") || s.CoverageOK("") {
		t.Error("모르는 fs 는 false 여야 (모르면 신뢰하지 않는다)")
	}
	if !s.RaSyncHook {
		t.Error("ra_sync_hook 이 1")
	}
	// coverage 에 0 이 하나라도 있으면 화면에 경고를 띄워야 한다.
	if !s.HasQualityWarning() {
		t.Error("f2fs coverage=0 이면 경고 대상")
	}
	// FSIO_SUMMARY 줄이 아니면 nil.
	if ParseFsioSummaryLine("3.08\tVFS\t1\t1") != nil {
		t.Error("TSV 줄을 요약으로 읽었다")
	}
	// nil 요약은 "알 수 없음" 이므로 경고 대상이다.
	var none *FsioSummary
	if !none.HasQualityWarning() || none.CoverageOK("ext4") {
		t.Error("nil 요약은 경고 대상이고 coverage 는 false")
	}
}

// pair_lost 키가 없는 구 producer 는 read_enter - read_exit 로 복원한다.
func TestFsioSummaryPairLostFallback(t *testing.T) {
	s := ParseFsioSummaryLine("# FSIO_SUMMARY v=1 read_enter=400 read_exit=363 ra_sync_hook=1")
	if s == nil || s.PairLost != 37 {
		t.Fatalf("pair_lost 복원 실패: %+v", s)
	}
	// 음수가 될 상황에서 언더플로가 나면 안 된다 (uint64).
	s = ParseFsioSummaryLine("# FSIO_SUMMARY v=1 read_enter=10 read_exit=20 ra_sync_hook=1")
	if s == nil || s.PairLost != 0 {
		t.Fatalf("언더플로 방지 실패: %+v", s)
	}
}

// 깨진 줄에 panic 하면 수집 전체가 날아간다 (파싱은 StopTrace 후 1회).
func TestFsioReadSurvivesMalformedInput(t *testing.T) {
	cases := []string{
		"", "\t", strings.Repeat("\t", 16),
		"3.08\tVFS\tx\ty\tz\tdd\tvfs_read\tvfs_read:exit\text4\t\t\t\t\t\t\tnothex\tret=abc req=xyz fill=-1",
		"3.08\tVFS\t1\t1\t0\tdd\tvfs_read\tvfs_read:exit\text4\t0\t0\t0\t0\t0\t\t0x0\t",
		"3.08\tVFS\t1\t1\t0\tdd\tvfs_read\tvfs_read:exit\text4\t0\t0\t0\t0\t0\t\t0x0\tret=99999999999999999999",
	}
	for i, line := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("case %d panic: %v\n  line=%q", i, r, line)
				}
			}()
			if ev, ok := parseFsioReadLine(line); ok {
				ClassifyFsioRead([]FsioReadEvent{ev}, summaryOK())
			}
		}()
	}
}
