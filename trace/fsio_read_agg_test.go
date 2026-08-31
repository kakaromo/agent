package trace

import (
	"os"
	"path/filepath"
	"testing"

	pb "agent/pb"
)

// fsio_read 집계.
//
// 정본은 Rust `../trace/src/output/fsio_read_stats_duckdb.rs` 이고, 실제 fixture
// (`../trace/tests/fixtures/fsio_cache_sample.log` → parquet) 로 class/스칼라/top-files
// 전부 대조를 마쳤다. 여기서는 fixture 없이도 지켜야 할 **계약**을 고정한다.

// fixtureDir — fsio_read parquet 이 있는 디렉토리. 없으면 스킵한다.
// 생성: go run ./cmd/goparse <fsio 로그> <dir> fsio_ufs
func fixtureDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("FSIO_READ_FIXTURE_DIR")
	if d == "" {
		t.Skip("FSIO_READ_FIXTURE_DIR 미설정 — parquet fixture 필요")
	}
	if _, err := os.Stat(filepath.Join(d, "result_fsio_read.parquet")); err != nil {
		t.Skipf("fixture parquet 없음: %v", err)
	}
	return d
}

// fsio_read 가 없는 잡은 **에러가 아니라 빈 응답**이다.
// 호출부가 이걸로 Page Cache 탭을 숨긴다 — 에러로 만들면 정상 job 에 빨간 배너가 뜬다.
func TestFsioReadStatsEmptyWhenNoSibling(t *testing.T) {
	resp, err := ComputeFsioReadStats(
		[]*TraceJobInfo{{Dir: t.TempDir(), TraceType: "fsio_ufs"}},
		&pb.GetFsioReadStatsRequest{},
	)
	if err != nil {
		t.Fatalf("형제 parquet 이 없는 건 에러가 아니어야 한다: %v", err)
	}
	if resp.TotalRequests != 0 || len(resp.ByClass) != 0 {
		t.Errorf("빈 응답이어야 하는데 total=%d classes=%d", resp.TotalRequests, len(resp.ByClass))
	}
	// 분모가 없으면 비율은 nil 이다 — 0.0 으로 채우면 "전부 miss" 로 오독된다.
	if resp.RequestHitRatio != nil || resp.RequestMissRatio != nil || resp.UnknownRatio != nil {
		t.Error("판정 대상이 없으면 비율은 nil 이어야 한다")
	}
	if resp.SchemaVersion != fsioReadSchemaVersion {
		t.Errorf("schema_version = %q, want %q", resp.SchemaVersion, fsioReadSchemaVersion)
	}
}

// 실 데이터 계약 — Rust 와 대조한 값들이 유지되는지.
func TestFsioReadStatsAgainstFixture(t *testing.T) {
	dir := fixtureDir(t)
	resp, err := ComputeFsioReadStats(
		[]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}},
		&pb.GetFsioReadStatsRequest{TopN: 5},
	)
	if err != nil {
		t.Fatal(err)
	}

	// class 별 requests 합 = total. 어긋나면 어느 한쪽 쿼리의 where 가 다른 것이다.
	var sum uint64
	for _, c := range resp.ByClass {
		sum += c.Requests
	}
	if sum != resp.TotalRequests {
		t.Errorf("class 합 %d != total %d — 두 쿼리의 필터가 갈렸다", sum, resp.TotalRequests)
	}

	// hit ratio 의 분모는 hit+miss 만이다 (DIRECT/EOF/ERROR/UNKNOWN 제외).
	var hit, miss uint64
	for _, c := range resp.ByClass {
		switch c.CacheClass {
		case fsioClassHit:
			hit = c.Requests
		case fsioClassMiss:
			miss = c.Requests
		}
	}
	if hit+miss > 0 {
		if resp.RequestHitRatio == nil {
			t.Fatal("판정 대상이 있는데 hit ratio 가 nil")
		}
		want := float64(hit) / float64(hit+miss)
		if got := *resp.RequestHitRatio; got != want {
			t.Errorf("hit ratio = %v, want %v (분모는 hit+miss 만)", got, want)
		}
		// 분모에 total 을 쓰면 이 값보다 작아진다 — 흔한 실수라 명시적으로 막는다.
		if *resp.RequestHitRatio == float64(hit)/float64(resp.TotalRequests) && resp.TotalRequests != hit+miss {
			t.Error("hit ratio 분모가 total 이다 — DIRECT/EOF/UNKNOWN 이 섞였다")
		}
	}

	// duration 표본이 있으면 백분위가 채워져야 한다.
	// (전부 nil 이면 duration_ns 컬럼 감지가 깨진 것이다 — 실제로 한 번 겪었다:
	//  filterPresentCols 는 필터 대상 컬럼만 훑어서 duration_ns 를 늘 false 로 준다.)
	for _, c := range resp.ByClass {
		if c.DurationSamples > 0 && c.DurationP50Ns == nil {
			t.Errorf("%s: 표본 %d 인데 p50 이 nil — duration_ns 감지 실패", c.CacheClass, c.DurationSamples)
		}
		if c.DurationSamples == 0 && c.DurationAvgNs != nil {
			t.Errorf("%s: 표본 0 인데 avg 가 있다 (0 으로 채우면 '0ns 였다' 가 된다)", c.CacheClass)
		}
		if c.DurationSamples > c.Requests {
			t.Errorf("%s: 표본(%d) > 요청(%d)", c.CacheClass, c.DurationSamples, c.Requests)
		}
	}

	// top_files 는 총 소요 시간 내림차순. TopN 을 넘지 않는다.
	if len(resp.TopFiles) > 5 {
		t.Errorf("top_files %d개 — TopN=5 를 넘었다", len(resp.TopFiles))
	}
	for i := 1; i < len(resp.TopFiles); i++ {
		if resp.TopFiles[i-1].TotalDurationNs < resp.TopFiles[i].TotalDurationNs {
			t.Error("top_files 가 소요 시간 내림차순이 아니다")
			break
		}
	}
	// 파일별 hit+miss+unknown 은 그 파일 요청 수를 넘지 않는다.
	for _, f := range resp.TopFiles {
		if f.HitRequests+f.MissRequests+f.UnknownRequests > f.Requests {
			t.Errorf("%s: 분류 합이 요청 수를 넘었다", f.Key)
		}
	}

	// coverage 가 안 되는 행이 있으면 **경고를 숨기지 않는다.**
	if len(resp.QualityWarnings) == 0 {
		var unknown uint64
		for _, c := range resp.ByClass {
			if c.CacheClass == fsioClassUnknown {
				unknown = c.Requests
			}
		}
		if unknown > 0 {
			t.Error("UNKNOWN 이 있는데 품질 경고가 없다 — 숨기면 안 된다")
		}
	}
}

// TopN clamp — 서버가 상한을 건다.
func TestFsioReadStatsTopNClamp(t *testing.T) {
	dir := fixtureDir(t)
	resp, err := ComputeFsioReadStats(
		[]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}},
		&pb.GetFsioReadStatsRequest{TopN: 99999},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.TopFiles) > fsioReadMaxTopN {
		t.Errorf("top_files %d개 — %d 로 clamp 돼야", len(resp.TopFiles), fsioReadMaxTopN)
	}
}

// ⚠ mmap 을 read 분모에 섞으면 안 된다.
//
// fault-around 때문에 캐시에 있는 페이지는 fault 를 아예 안 낸다 — mmap 에서 hit 은
// "행이 나오는 것" 이 아니라 **"행이 없는 것"** 이다. 그래서 mmap row 는 사실상 miss 만
// 모인 모집단이고, read 와 같은 분모에 넣으면 캐시는 그대로인데 mmap 을 쓴다는 이유만으로
// 적중률이 깎인다.
//
// 실측(FSIO_MMAP_FIXTURE_DIR = fio_mmap_sample.log 파싱 결과):
//
//	분리함  74.22%  ← 맞음
//	섞음    59.63%  ← 14.6%p 가 근거 없이 사라진다
//
// ⚠ 이 테스트는 **mmap 행이 있는 parquet** 이라야 의미가 있다. mmap 0건인 fixture 로
// 돌리면 분리를 빼도 통과한다 — Rust 쪽이 이 함정으로 버그를 오래 못 잡았다.
func TestFsioReadMmapExcludedFromHitRatio(t *testing.T) {
	dir := os.Getenv("FSIO_MMAP_FIXTURE_DIR")
	if dir == "" {
		t.Skip("FSIO_MMAP_FIXTURE_DIR 미설정 — mmap 행이 있는 parquet 필요")
	}
	if _, err := os.Stat(filepath.Join(dir, "result_fsio_read.parquet")); err != nil {
		t.Skipf("fixture parquet 없음: %v", err)
	}

	resp, err := ComputeFsioReadStats(
		[]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}},
		&pb.GetFsioReadStatsRequest{TopN: 5},
	)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Mmap == nil || resp.Mmap.Requests == 0 {
		t.Fatal("mmap 집계가 비었다 — 파서가 mmap_fault:exit 을 안 받고 있을 수 있다")
	}

	// 1. class 별 합 = total. mmap 이 어느 쪽에도 섞이면 안 된다.
	var sum uint64
	for _, c := range resp.ByClass {
		sum += c.Requests
	}
	if sum != resp.TotalRequests {
		t.Errorf("class 합 %d != total %d", sum, resp.TotalRequests)
	}

	// 2. **핵심** — 분모에 mmap 이 섞였는지. 섞이면 hit ratio 가 낮아진다.
	var hit, miss uint64
	for _, c := range resp.ByClass {
		switch c.CacheClass {
		case fsioClassHit:
			hit = c.Requests
		case fsioClassMiss:
			miss = c.Requests
		}
	}
	if hit+miss == 0 {
		t.Fatal("판정 대상이 0")
	}
	want := float64(hit) / float64(hit+miss)
	if resp.RequestHitRatio == nil || *resp.RequestHitRatio != want {
		t.Fatalf("hit ratio = %v, want %v", resp.RequestHitRatio, want)
	}

	// ⚠ 여기서 "mmap 을 섞은 값과 다른가" 로 검사하면 **안 된다.**
	//
	// 처음에 그렇게 썼다가 분리를 빼도 테스트가 통과했다: 분리가 빠지면 hit/miss 자체가
	// 이미 오염돼 있어서, 그 오염된 값으로 다시 "섞은 값" 을 만들면 또 달라지기 때문이다
	// (59.63% vs 50.00%). 자기 자신을 기준으로 삼으면 무엇과도 비교가 안 된다.
	//
	// 그래서 **fixture 의 알려진 정답**에 고정한다. 파서가 mmap 을 받고 집계가 분리하는
	// 두 조건이 다 맞아야만 이 값이 나온다.
	const (
		wantHit    = 95 // fio_mmap_sample.log 실측 (Rust CLI 대조)
		wantMiss   = 33
		wantMmap   = 33
		wantMmapMs = 32
	)
	if hit != wantHit || miss != wantMiss {
		t.Errorf("read 모집단 hit/miss = %d/%d, want %d/%d — mmap 이 섞였을 수 있다",
			hit, miss, wantHit, wantMiss)
	}
	if resp.Mmap.Requests != wantMmap || resp.Mmap.MissRequests != wantMmapMs {
		t.Errorf("mmap = %d건/miss %d, want %d/%d",
			resp.Mmap.Requests, resp.Mmap.MissRequests, wantMmap, wantMmapMs)
	}
	// 74.22% — 소수점 둘째 자리까지 고정. 섞이면 59.63% 가 된다.
	if got := float64(int(*resp.RequestHitRatio*10000+0.5)) / 100; got != 74.22 {
		t.Errorf("hit ratio = %.2f%%, want 74.22%% (섞이면 59.63%% 가 된다)", got)
	}
	t.Logf("hit ratio %.2f%% (read %d/%d) · mmap %d건 miss %d — 섞으면 59.63%%",
		*resp.RequestHitRatio*100, hit, hit+miss, resp.Mmap.Requests, resp.Mmap.MissRequests)

	// 3. mmap 은 대부분 miss 다 (fault-around). 이게 뒤집히면 판정이 이상한 것이다.
	if resp.Mmap.MissRequests*2 < resp.Mmap.Requests {
		t.Errorf("mmap miss 가 %d/%d — fault-around 전제와 어긋난다",
			resp.Mmap.MissRequests, resp.Mmap.Requests)
	}

	// 4. top_files 도 read 모집단이어야 한다 — 전체와 파일별이 다른 모집단이면
	//    같은 화면에서 합이 안 맞는다.
	for _, f := range resp.TopFiles {
		if f.HitRequests+f.MissRequests+f.UnknownRequests > f.Requests {
			t.Errorf("%s: 분류 합이 요청 수를 넘었다", f.Key)
		}
	}
}
