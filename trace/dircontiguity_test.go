package trace

import (
	"fmt"
	"testing"

	pb "agent/pb"
)

// 방향별 주소 연속성 집계의 회귀 테스트.
//
// 여기서 고정하는 건 전부 **에러 없이 그럴듯하게 틀리는** 종류다.

// dcOf — (방향, 연속여부) → 항목.
func dcOf(s *pb.TraceStats) map[string]*pb.DirectionContiguityStats {
	m := map[string]*pb.DirectionContiguityStats{}
	for _, e := range s.GetDirectionContiguity() {
		k := e.GetDirection()
		if e.GetContiguous() {
			k += "/cont"
		} else {
			k += "/disc"
		}
		m[k] = e
	}
	return m
}

func statsOf(t *testing.T, dir, traceType string) *pb.TraceStats {
	t.Helper()
	s, err := ComputeStats([]*TraceJobInfo{{Dir: dir, TraceType: traceType}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// ⚠ 집계 SQL 이 실패하면 slog.Warn 만 남기고 빈 슬라이스가 온다. 그대로 두면
	// "0건" 을 기대하는 단언이 **조용히 통과**한다 — 실제로 그렇게 놓칠 뻔했다
	// (COALESCE 안의 콤마까지 잘려 SQL 이 깨진 건). 여기서 막는다.
	if len(s.GetDirectionContiguity()) == 0 {
		t.Fatal("direction_contiguity 가 비었다 — 집계 SQL 이 실패했을 수 있다 (로그의 WARN 확인)")
	}
	return s
}

// ⭐ 이 기능의 존재 이유. read/write 가 인터리빙될 때 방향별 체인이
// **기존 continuous 컬럼과 다른 답**을 내는지 명시적으로 고정한다.
//
// send 순서: R(0,+1) W(100,+1) R(1,+1) W(101,+1)
//
//	기존 continuous : 전부 false (직전이 반대 방향이라)
//	방향별 체인      : 3·4번째 true
//
// 이 차이는 버그가 아니라 **의도**다. 나중에 "왜 두 숫자가 다르냐" 로 되돌려지지
// 않도록 두 값을 한 테스트에서 같이 검증한다.
func TestDirContiguityChainsPerDirection(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("400.000000", "1", "0", "0x28 (READ_10)"),
		ufsLine("400.000100", "2", "100", "0x2a (WRITE_10)"),
		ufsLine("400.000200", "3", "1", "0x28 (READ_10)"),    // R1 끝(0+1)에 이어짐
		ufsLine("400.000300", "4", "101", "0x2a (WRITE_10)"), // W1 끝(100+1)에 이어짐
	}, "ufs")

	s := statsOf(t, dir, "ufs")

	// 기존 전역 continuous 는 0 이어야 한다 (방향이 계속 바뀌므로).
	if s.GetContinuousCount() != 0 {
		t.Errorf("기존 continuous_count = %d, want 0 — 전역 체인은 방향이 바뀌면 끊긴다",
			s.GetContinuousCount())
	}

	m := dcOf(s)
	if got := m["read/cont"].GetCount(); got != 1 {
		t.Errorf("read/cont = %d, want 1 (R2 는 R1 에 이어진다)", got)
	}
	if got := m["write/cont"].GetCount(); got != 1 {
		t.Errorf("write/cont = %d, want 1 (W2 는 W1 에 이어진다)", got)
	}
	if got := m["read/cont"].GetRatioWithinDirection(); got != 50 {
		t.Errorf("read 연속 비율 = %.1f%%, want 50%%", got)
	}
}

// 각 체인의 첫 send 는 비연속으로 세어야 한다.
// COALESCE 를 빼면 LAG 가 NULL 이라 count(*) FILTER 가 **양쪽 다 건너뛴다** →
// 네 칸 합이 send 수에 못 미치고 비율이 100% 가 안 된다.
func TestDirContiguityFirstSendCountedAsDiscontiguous(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("500.000000", "1", "0", "0x28 (READ_10)"),
	}, "ufs")

	s := statsOf(t, dir, "ufs")
	m := dcOf(s)
	if len(m) != 1 {
		t.Fatalf("항목 %d개, want 1 — 첫 send 가 사라졌다면 COALESCE 누락이다: %v", len(m), m)
	}
	e := m["read/disc"]
	if e == nil || e.GetCount() != 1 {
		t.Fatalf("read/disc 1건이어야 한다: %v", m)
	}
	if e.GetRatioWithinDirection() != 100 {
		t.Errorf("비율 = %.1f%%, want 100%%", e.GetRatioWithinDirection())
	}
}

// discard/flush 는 read 도 write 도 아니라 어느 칸에도 안 들어가야 한다.
// 섞이면 write 바이트가 discard 만큼 부푼다 (f2fs 에서 특히 크다).
func TestDirContiguityExcludesDiscardAndFlush(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("600.000000", "1", "0", "0x28 (READ_10)"),
		ufsLine("600.000100", "2", "8", "0x42 (UNMAP)"),
		ufsLine("600.000200", "3", "16", "0x35 (SYNCHRONIZE_CACHE_10)"),
		ufsLine("600.000300", "4", "24", "0x2a (WRITE_10)"),
	}, "ufs")

	s := statsOf(t, dir, "ufs")
	if got := s.GetClassifiedSendCount(); got != 2 {
		t.Errorf("classified_send_count = %d, want 2 (read 1 + write 1; unmap/flush 제외)", got)
	}
	if s.GetSendCount() != 4 {
		t.Errorf("send_count = %d, want 4 — 분모는 전체 send 여야 한다", s.GetSendCount())
	}
	for k, e := range dcOf(s) {
		if e.GetCount() != 1 {
			t.Errorf("%s = %d건, want 1", k, e.GetCount())
		}
	}
}

// ratio_of_sends 4칸 합이 100% 인지.
func TestDirContiguityRatiosSumTo100(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("700.000000", "1", "0", "0x28 (READ_10)"),
		ufsLine("700.000100", "2", "1", "0x28 (READ_10)"),
		ufsLine("700.000200", "3", "50", "0x28 (READ_10)"),
		ufsLine("700.000300", "4", "100", "0x2a (WRITE_10)"),
		ufsLine("700.000400", "5", "101", "0x2a (WRITE_10)"),
	}, "ufs")

	s := statsOf(t, dir, "ufs")
	var sum float64
	for _, e := range s.GetDirectionContiguity() {
		sum += e.GetRatioOfSends()
	}
	if d := sum - 100; d > 0.01 || d < -0.01 {
		t.Errorf("ratio_of_sends 합 = %.4f%%, want 100%% — 항목이 새고 있다", sum)
	}
}

// 바이트 단위 환산 — ftrace UFS 는 size 1 = 4096 bytes.
func TestDirContiguityConvertsSizeToBytes(t *testing.T) {
	dir := writeFtraceParquet(t, []string{
		ufsLine("800.000000", "1", "0", "0x28 (READ_10)"),
	}, "ufs")

	s := statsOf(t, dir, "ufs")
	e := dcOf(s)["read/disc"]
	if e == nil {
		t.Fatal("read 항목 없음")
	}
	// 픽스처 size=4096 bytes → 파서가 4KB LBA 단위로 1 → ×4096 = 4096 bytes
	if got := e.GetTotalBytes(); got != 4096 {
		t.Errorf("total_bytes = %d, want 4096", got)
	}
	if got := e.GetAvgRequestBytes(); got != 4096 {
		t.Errorf("avg_request_bytes = %.0f, want 4096", got)
	}
}

// LU 가 다르면 주소가 이어져도 연속이 아니다 (fsio_ufs).
// 이게 빠지면 연속 비율이 100% 쪽으로 부푼다 — 가장 그럴듯한 오답이다.
func TestDirContiguityPartitionsByLun(t *testing.T) {
	// LU 를 섞어 세면 **한 체인으로 이어지도록** 주소를 배치한다.
	//   lun0: lba 0 (+1)   lun1: lba 1 (+1)   lun0: lba 2 (+1)   lun1: lba 3
	// 전역 체인이면 0→1→2→3 이 전부 이어져 cont 3건이 나온다.
	// LU 로 갈라 보면 lun0 은 0,2 / lun1 은 1,3 이라 **어느 쪽도 안 이어진다** → cont 0건.
	// TSV 컬럼: 12=size(bytes), 13=sec(=LBA). io_flags(15) 0x1 = read.
	// size 4096B = 1 LBA 라 끝주소는 lba+1 이다.
	lines := []string{}
	for i, tc := range []struct{ lun, lba int }{
		{0, 0}, {1, 1}, {0, 2}, {1, 3},
	} {
		lines = append(lines, fmt.Sprintf(
			"900.00%04d\tUFS\t100\t100\t0\tapp\tvfs_read\tufshcd_command:send_req\text4\t8\t32\t555\t4096\t%d\t/d\t0x0000000000000001\tlun=%d tag=%d hwq=0 ufs_op=0x28 grp=0x0",
			i, tc.lba, tc.lun, i))
	}
	dir := writeFsioLines(t, lines, "fsio_ufs")

	s := statsOf(t, dir, "fsio_ufs")
	m := dcOf(s)

	if e := m["read/cont"]; e != nil {
		t.Errorf("read/cont = %d건, want 0 — LU 파티션이 빠져 서로 다른 LU 의 주소가 이어졌다",
			e.GetCount())
	}
	if e := m["read/disc"]; e == nil || e.GetCount() != 4 {
		t.Errorf("read/disc = %v, want 4건", e)
	}
}

// fsio 는 size 가 bytes 라 끝주소에 올림 나눗셈이 들어간다. 그 나눗셈이 정수여야 한다.
//
// ⚠ DuckDB 의 `/` 는 정수끼리도 **부동소수 나눗셈**이다. size=4096 이면
// `(4096+4095)/4096` 이 1.9998 로 남아(`//` 면 1) 끝주소가 정수가 아니게 되고,
// `addr = lag(end_addr)` 동등 비교가
// **영원히 안 맞아 연속이 항상 0%** 로 나온다. 에러가 아니라 그럴듯한 0 이다.
// 정수 나눗셈은 `//` 다.
func TestDirContiguityFsioUsesIntegerCeilDiv(t *testing.T) {
	// lba 0(+1) → 1(+1) → 2 : size 4096B = 1 LBA 라 셋이 한 체인으로 이어져야 한다.
	lines := []string{}
	for i, lba := range []int{0, 1, 2} {
		lines = append(lines, fmt.Sprintf(
			"950.00%04d\tUFS\t100\t100\t0\tapp\tvfs_read\tufshcd_command:send_req\text4\t8\t32\t555\t4096\t%d\t/d\t0x0000000000000001\tlun=0 tag=%d hwq=0 ufs_op=0x28 grp=0x0",
			i, lba, i))
	}
	dir := writeFsioLines(t, lines, "fsio_ufs")

	s := statsOf(t, dir, "fsio_ufs")
	m := dcOf(s)
	e := m["read/cont"]
	if e == nil || e.GetCount() != 2 {
		t.Fatalf("read/cont = %v, want 2건 — 끝주소가 소수라 동등 비교가 안 맞으면 0 이 된다", e)
	}
}

// fsio_block 은 `Q` 를 block_rq_issue 의 별칭으로 쓴다 (파서가 둘 다 받고 원문을
// 그대로 저장한다). send predicate 가 이걸 빠뜨리면 **에러 없이 화면이 통째로 빈다** —
// send 0건, 항목 0개. 조회 자체는 성공하므로 아무도 못 알아챈다.
func TestDirContiguityAcceptsQActionAlias(t *testing.T) {
	lines := []string{}
	for i, sec := range []int{0, 8, 16} { // 4096B = 8 sector 라 이어진다
		lines = append(lines, fmt.Sprintf(
			"800.00%04d\tBLK\t100\t100\t0\tapp\tvfs_read\tQ\text4\t8\t0\t555\t4096\t%d\t/d\t0x0000000000000001\trwbs=R",
			i, sec))
	}
	dir := writeFsioLines(t, lines, "fsio_block")

	s := statsOf(t, dir, "fsio_block")
	if got := s.GetClassifiedSendCount(); got != 3 {
		t.Errorf("classified = %d, want 3 — `Q` 를 send 로 안 세면 화면이 빈다", got)
	}
	m := dcOf(s)
	if e := m["read/cont"]; e == nil || e.GetCount() != 2 {
		t.Errorf("read/cont = %v, want 2건", e)
	}
}

// 시간 컬럼이 없으면(detectTimeColumn 이 "" 을 돌려주는 계약) 깨진 SQL 을 만들지 말고
// 조용히 건너뛴다. 정렬 축이 없으면 연속성 판정 자체가 성립하지 않는다.
func TestDirContiguitySkipsWhenNoTimeColumn(t *testing.T) {
	got, n, err := queryDirContiguity(nil, "'x'", "", fsioCols{}, "lba", "opcode", "",
		map[string]bool{"action": true})
	if err != nil {
		t.Fatalf("에러가 아니라 조용히 건너뛰어야 한다: %v", err)
	}
	if got != nil || n != 0 {
		t.Errorf("빈 결과여야 한다: %v, %d", got, n)
	}
}

// **기존 통계**(sendCount / continuousRatio / cmdStats.sendCount)도 `Q` 를 세야 한다.
//
// 이건 이번 기능과 별개인 **기존 버그**다. stats.go 에 send 판정 문자열이 네 군데
// 박혀 있었고 전부 `Q` 를 빠뜨려서, `Q` 로 기록된 트레이스는 sendCount 가 0 이었다.
// 판정을 SendPredicate 하나로 모으면서 같이 고쳤다.
func TestSendCountAcceptsQActionAlias(t *testing.T) {
	lines := []string{}
	for i, sec := range []int{0, 8, 16} {
		lines = append(lines, fmt.Sprintf(
			"810.00%04d\tBLK\t100\t100\t0\tapp\tvfs_read\tQ\text4\t8\t0\t555\t4096\t%d\t/d\t0x0000000000000001\trwbs=R",
			i, sec))
	}
	dir := writeFsioLines(t, lines, "fsio_block")

	s, err := ComputeStats([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_block"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GetSendCount(); got != 3 {
		t.Errorf("sendCount = %d, want 3 — `Q` 가 send 로 안 세지면 0 이 된다", got)
	}
	var cmdSend int64
	for _, c := range s.GetCmdStats() {
		cmdSend += c.GetSendCount()
	}
	if cmdSend != 3 {
		t.Errorf("cmdStats sendCount 합 = %d, want 3", cmdSend)
	}
}
