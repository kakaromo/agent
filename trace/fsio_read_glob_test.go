package trace

import (
	"os"
	"path/filepath"
	"testing"
)

// ⚠ fsio_read parquet 이 일반 통계 조회에 섞이면 안 된다.
//
// 스키마가 33컬럼으로 전혀 달라 union_by_name 으로 붙으면 행 수가 통째로 부푼다
// (실측: fsio_ufs 471 + fsio_read 363 = 834). 에러 없이 **모든 통계가 조용히
// 틀리는** 종류라 특히 위험하다. trace_type 이 "both"/"" 인 잡은 `*.parquet`
// 와일드카드를 쓰므로 실제로 도달하는 경로다.
func TestFsioReadParquetExcludedFromNormalGlob(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"result_fsio_ufs.parquet",
		"result_fsio_read.parquet",
		"result_ufs.parquet",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 와일드카드로 도는 타입들 — fsio_read 가 절대 안 나와야 한다.
	for _, tt := range []string{"", "both", "unknown_type"} {
		for _, f := range findParquetFiles(dir, tt) {
			if isFsioReadParquet(f) {
				t.Errorf("traceType=%q 에 fsio_read 가 섞였다: %s", tt, filepath.Base(f))
			}
		}
	}
	// 명시적 타입도 마찬가지.
	for _, tt := range []string{"ufs", "fsio_ufs", "fsio_block"} {
		for _, f := range findParquetFiles(dir, tt) {
			if isFsioReadParquet(f) {
				t.Errorf("traceType=%q 에 fsio_read 가 섞였다", tt)
			}
		}
	}
	// 그래도 일반 parquet 은 정상적으로 잡혀야 한다 (과잉 차단 방지).
	if len(findParquetFiles(dir, "ufs")) != 1 {
		t.Error("result_ufs.parquet 을 못 찾았다 — 필터가 과했다")
	}

	// 전용 조회는 반대로 찾아야 한다.
	got := FindFsioReadParquets([]*TraceJobInfo{{Dir: dir, TraceType: "fsio_ufs"}})
	if len(got) != 1 || filepath.Base(got[0]) != "result_fsio_read.parquet" {
		t.Errorf("FindFsioReadParquets = %v, want [result_fsio_read.parquet]", got)
	}
	// 없는 잡은 빈 결과 (Page Cache 탭을 숨길 근거가 된다).
	if len(FindFsioReadParquets([]*TraceJobInfo{{Dir: t.TempDir(), TraceType: "fsio_ufs"}})) != 0 {
		t.Error("fsio_read 없는 잡인데 결과가 나왔다")
	}
}
