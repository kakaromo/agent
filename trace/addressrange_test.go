package trace

import (
	"os"
	"path/filepath"
	"testing"

	pb "agent/pb"
	"agent/trace/parser"
)

// 주소 범위(min/max/span) 집계의 회귀 테스트.
//
// 이 집계가 틀리면 화면에 **그럴듯한 숫자**가 뜬다 — 에러가 안 나므로 눈으로는
// 못 잡는다. 여기서 고정하는 건 전부 그 종류다.

// arOf — 방향 → 항목.
func arOf(s *pb.TraceStats) map[string]*pb.AddressRangeStats {
	m := map[string]*pb.AddressRangeStats{}
	for _, e := range s.GetAddressRange() {
		m[e.GetDirection()] = e
	}
	return m
}

// rangeOf — ComputeStats 를 돌리고 address_range 가 실제로 채워졌는지까지 확인한다.
//
// ⚠ 집계 SQL 이 실패하면 slog.Warn 만 남기고 빈 슬라이스가 온다. 그대로 두면
// 뒤의 단언들이 nil 맵을 훑으며 **조용히 통과**한다. 픽스처가 이 경로에 실제로
// 닿는지를 여기서 막는다.
func rangeOf(t *testing.T, dir, traceType string) map[string]*pb.AddressRangeStats {
	t.Helper()
	s, err := ComputeStats([]*TraceJobInfo{{Dir: dir, TraceType: traceType}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.GetAddressRange()) == 0 {
		t.Fatal("address_range 가 비었다 — 집계 SQL 이 실패했을 수 있다 (로그의 WARN 확인)")
	}
	return arOf(s)
}

// ⭐ 기본 동작. 전체/read/write 가 각각 자기 대역의 min/max 를 낸다.
//
// read 는 0~16, write 는 100~108. 전체는 0~108 이라 **read 범위도 write 범위도
// 아니다** — 방향별로 안 나누면 답할 수 없는 질문이라는 게 이 기능의 요지다.
func TestAddressRangeSplitsByDirection(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("400.000000", "1", "0", "0x28 (READ_10)"),
		ufsLine("400.000100", "2", "100", "0x2a (WRITE_10)"),
		ufsLine("400.000200", "3", "16", "0x28 (READ_10)"),
		ufsLine("400.000300", "4", "108", "0x2a (WRITE_10)"),
	}, "ufs")

	m := rangeOf(t, dir, "ufs")

	for _, c := range []struct {
		dir          string
		lo, hi, span uint64
		count        int64
	}{
		{"all", 0, 108, 108, 4},
		{"read", 0, 16, 16, 2},
		{"write", 100, 108, 8, 2},
	} {
		e := m[c.dir]
		if e == nil {
			t.Fatalf("%s 행이 없다 (전체: %v)", c.dir, m)
		}
		if e.GetMinAddr() != c.lo || e.GetMaxAddr() != c.hi {
			t.Errorf("%s: min/max = %d/%d, want %d/%d",
				c.dir, e.GetMinAddr(), e.GetMaxAddr(), c.lo, c.hi)
		}
		if e.GetSpan() != c.span {
			t.Errorf("%s: span = %d, want %d", c.dir, e.GetSpan(), c.span)
		}
		if e.GetCount() != c.count {
			t.Errorf("%s: count = %d, want %d reqs", c.dir, e.GetCount(), c.count)
		}
	}

	// UFS 는 주소 1단위가 4KB. 이걸 안 내면 화면이 Block(512B) 과 같은 축으로
	// 비교해 8배 틀린 결론을 낸다.
	if got := m["all"].GetUnitBytes(); got != 4096 {
		t.Errorf("unit_bytes = %d, want 4096 (UFS)", got)
	}
}

// ⭐ complete 행이 모수를 부풀리지 않는가.
//
// send 2건 + complete 2건인데 count 가 4 면 SendPredicate 가 안 걸린 것이다.
// 범위 자체는 같아서 min/max 만 보면 **못 잡는다** — count 로 잡는다.
func TestAddressRangeCountsSendsOnly(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("410.000000", "1", "0", "0x28 (READ_10)"),
		ufsComplete("410.000050", "1", "0", "0x28 (READ_10)"),
		ufsLine("410.000100", "2", "64", "0x28 (READ_10)"),
		ufsComplete("410.000150", "2", "64", "0x28 (READ_10)"),
	}, "ufs")

	m := rangeOf(t, dir, "ufs")
	if got := m["all"].GetCount(); got != 2 {
		t.Errorf("count = %d, want 2 reqs — complete 행까지 셌다 (SendPredicate 미적용)", got)
	}
}

// ⭐ discard/flush 는 방향이 없지만 주소는 있다.
//
// 그래서 **전체 count > read+write count** 가 정상이다. 이걸 모르면 화면에서
// "합이 안 맞는다" 를 버그로 신고하게 된다. 전체 범위에도 들어가야 한다.
func TestAddressRangeIncludesDirectionlessRequests(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("420.000000", "1", "10", "0x28 (READ_10)"),
		ufsLine("420.000100", "2", "20", "0x2a (WRITE_10)"),
		ufsLine("420.000200", "3", "900", "0x42 (UNMAP)"), // discard — 방향 없음
	}, "ufs")

	m := rangeOf(t, dir, "ufs")
	if got := m["all"].GetCount(); got != 3 {
		t.Errorf("전체 count = %d, want 3 reqs — discard 가 전체에서 빠졌다", got)
	}
	// discard 주소(900)가 전체 max 여야 한다.
	if got := m["all"].GetMaxAddr(); got != 900 {
		t.Errorf("전체 max = %d, want 900 — 방향 없는 요청의 주소가 범위에서 빠졌다", got)
	}
	if r, w := m["read"].GetCount(), m["write"].GetCount(); r+w != 2 {
		t.Errorf("read+write = %d reqs, want 2 — discard 가 방향 행에 샜다", r+w)
	}
}

// ⭐ block 의 `Q` 별칭.
//
// SendPredicate 없이 문자열을 박으면 `Q` 로 기록된 트레이스가 **에러 없이 0 reqs**
// 가 된다 (PR#23 이 고친 버그). ftrace block_rq_issue 는 Q 로 나온다.
func TestAddressRangeHandlesBlockQAlias(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		blockLine("430.000000", "0", "R"),
		blockLine("430.000100", "2048", "W"),
	}, "block")

	m := rangeOf(t, dir, "block")
	if got := m["all"].GetCount(); got != 2 {
		t.Fatalf("count = %d, want 2 reqs — Q 별칭이 send 로 안 잡혔다", got)
	}
	if got := m["all"].GetMaxAddr(); got != 2048 {
		t.Errorf("max = %d, want 2048", got)
	}
	// Block 은 sector 단위 512B — UFS 의 4096 과 달라야 한다.
	if got := m["all"].GetUnitBytes(); got != 512 {
		t.Errorf("unit_bytes = %d, want 512 (block sector)", got)
	}
}

// ⭐ 혼합 조회(lba + sector)는 **아예 내지 않는다.**
//
// detectLbaColumn 이 COALESCE(lba, sector) 를 돌려주면 4KB 단위와 512B 단위 주소가
// 한 min/max 에 섞인다. 그 결과는 에러가 아니라 **그럴듯하게 틀린 범위**라 화면에서
// 못 알아본다. 빈 값으로 두고 UI 가 "—" 를 그리게 한다.
func TestAddressRangeSkippedOnMixedSchema(t *testing.T) {
	ufsDir := writeFtraceParquet(t, []string{
		ufsLine("440.000000", "1", "0", "0x28 (READ_10)"),
	}, "ufs")
	blockDir := writeFtraceParquet(t, []string{
		blockLine("440.000100", "2048", "W"),
	}, "block")

	s, err := ComputeStats([]*TraceJobInfo{
		{Dir: ufsDir, TraceType: "ufs"},
		{Dir: blockDir, TraceType: "block"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s.GetAddressRange()); n != 0 {
		t.Errorf("혼합 조회에서 address_range 가 %d행 나왔다 — 4KB/512B 단위가 섞인다", n)
	}
	// 픽스처가 실제로 두 스키마를 합쳐 읽었는지 확인 — 이게 0 이면 위 단언은
	// "조회 자체가 실패해서" 통과한 것이라 아무것도 검증하지 못한다.
	if s.GetTotalEvents() == 0 {
		t.Fatal("total_events = 0 — 혼합 조회가 아예 실패했다 (테스트가 무의미)")
	}
}

// ⭐ mgmt 행이 min 을 0 으로 끌어내리지 않는가.
//
// ⚠ 함정: fsio_ufs 의 Query/UIC 행은 parquet 에서 lba 가 **NULL 이 아니라 0** 이다
// (rawLbaExpr 의 NULL 화는 Raw Data/차트 경로 전용이라 이 집계엔 안 걸린다).
// 그래서 mgmt 를 안 거르면 min 이 조용히 0 이 되고, "0번지부터 썼다" 는 틀린
// 사실이 화면에 뜬다. 막아주는 건 ComputeStats 의 ExcludeMgmt 다.
//
// 픽스처(fsioTestLog)의 데이터 IO 는 write 1024000, read 2048000 두 건이고
// 나머지 4행은 mgmt(lba=0), 1행은 VFS 다.
func TestAddressRangeExcludesMgmtZeroAddresses(t *testing.T) {
	dir := writeFsioParquet(t, "fsio_ufs")

	m := rangeOf(t, dir, "fsio_ufs")
	all := m["all"]
	if got := all.GetCount(); got != 2 {
		t.Errorf("count = %d, want 2 reqs — mgmt 행까지 셌다", got)
	}
	if all.GetMinAddr() != 1024000 || all.GetMaxAddr() != 2048000 {
		t.Errorf("min/max = %d/%d, want 1024000/2048000 — min 이 0 이면 mgmt(lba=0)가 샜다",
			all.GetMinAddr(), all.GetMaxAddr())
	}
	// ⚠ fsio 도 **주소는 4KB 단위**다. bytes 로 오는 건 `size` 지 주소가 아니다
	// (EndAddrExpr 이 fsio_ufs 에서도 size 를 4096 으로 나눠 더하는 게 근거).
	// 여기서 1 을 기대하면 화면이 범위를 4096배 작게 그리는 걸 고정해 버린다.
	if got := all.GetUnitBytes(); got != 4096 {
		t.Errorf("unit_bytes = %d, want 4096 (fsio_ufs 도 주소는 4KB 단위)", got)
	}
	// 방향 분해도 살아 있어야 한다 (is_read/is_write 불리언 경로).
	if r := m["read"]; r == nil || r.GetMinAddr() != 2048000 {
		t.Errorf("read 행 = %v, want min 2048000", r)
	}
	if w := m["write"]; w == nil || w.GetMinAddr() != 1024000 {
		t.Errorf("write 행 = %v, want min 1024000", w)
	}
}

// ⭐ fsio_block 의 주소 단위는 512B 다.
//
// ⚠ 이 테스트의 존재 이유 — 처음 구현이 `SectorBytes` 를 썼는데 그건 **size** 1
// 단위의 바이트라 fsio 에서 1 을 돌려준다. 주소에 그걸 쓰면 범위가 512~4096배
// 작게 나온다(실측: 3.91 GB 가 0.98 MB 로). 에러가 아니라 그럴듯한 숫자라
// 화면에서 못 알아본다. AddrUnitBytes 와 SectorBytes 를 다시 헷갈리면 여기서 잡힌다.
func TestAddressRangeFsioBlockUnitIsSector(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "trace.log")
	// fsio_block 한 줄 — 태그는 BLK 다(BLOCK 이 아니라). rwbs=WS, 16384 bytes.
	line := "12345.678920\tBLK\t4521\t4521\t3\tmysqld\tvfs_write\tblock_rq_issue\text4\t8\t32\t983241\t16384\t8192000\t/data/ibdata1\t0x0000010040002102\trwbs=WS"
	if err := os.WriteFile(logFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parser.RunParquetOnly(logFile, dir, "fsio_block", nil); err != nil {
		t.Fatal(err)
	}

	m := rangeOf(t, dir, "fsio_block")
	if got := m["all"].GetUnitBytes(); got != 512 {
		t.Errorf("unit_bytes = %d, want 512 — fsio 라고 1 을 쓰면 범위가 512배 작아진다", got)
	}
}

// ⭐ 필터가 범위에 반영되는가.
//
// 화면은 필터를 걸면 stats 를 같은 필터로 재조회한다. 범위가 안 따라 줄어들면
// 표와 차트가 어긋난다.
func TestAddressRangeRespectsFilter(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("450.000000", "1", "0", "0x28 (READ_10)"),
		ufsLine("450.000100", "2", "500", "0x28 (READ_10)"),
		ufsLine("450.000200", "3", "1000", "0x28 (READ_10)"),
	}, "ufs")

	s, err := ComputeStats([]*TraceJobInfo{{Dir: dir, TraceType: "ufs"}},
		&pb.TraceFilter{EndLba: 600}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := arOf(s)
	if m["all"] == nil {
		t.Fatal("address_range 가 비었다")
	}
	if got := m["all"].GetMaxAddr(); got != 500 {
		t.Errorf("max = %d, want 500 — 필터가 범위에 반영되지 않았다", got)
	}
	if got := m["all"].GetCount(); got != 2 {
		t.Errorf("count = %d, want 2 reqs", got)
	}
}
