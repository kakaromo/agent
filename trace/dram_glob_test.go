package trace

import (
	"os"
	"path/filepath"
	"testing"
)

// ⚠ DRAM parquet 이 IO 통계 조회에 섞이면 안 된다 — fsio_read 와 같은 함정.
// trace_type 이 "both"/"" 인 잡은 `*.parquet` 와일드카드라 union_by_name 으로 붙어
// 행 수가 부풀고 모든 통계가 **에러 없이** 틀린다.
func TestDramBwParquetExcludedFromNormalGlob(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"result_ufs.parquet", "result_block.parquet", "result_dram_bw.parquet"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []string{"", "both", "ufs", "block", "fsio_ufs"} {
		for _, f := range findParquetFiles(dir, tt) {
			if isDramBwParquet(f) {
				t.Errorf("traceType=%q 에 dram_bw 가 섞였다: %s", tt, filepath.Base(f))
			}
		}
	}
	if got := findParquetFiles(dir, "both"); len(got) != 2 {
		t.Errorf("both = %v, want ufs+block 2개 (과잉 차단 확인)", got)
	}
}
