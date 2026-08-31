package trace

import (
	"strings"
	"testing"
)

// TestFsioOnlyLayerIncludesVFS — fsio_read(page cache) 를 보려면 vfs 가 붙어야 한다.
//
// ⚠ 이게 이 옵션의 존재 이유다. fsio_read row(`vfs_read:exit`/`readv:exit`/
// `mmap_fault:exit`)는 **VFS 레이어 행 자체**라, `--only ufs` 로만 수집하면
// print 필터에서 걸려 로그에 아예 안 남는다 → Page Cache 통계와 mmap 집계가
// 통째로 빈다. 파서가 layer=="VFS" 로 거르는 것과 짝이다.
func TestFsioOnlyLayerIncludesVFS(t *testing.T) {
	tests := []struct {
		traceType  string
		includeVFS bool
		want       string
	}{
		{"fsio_ufs", false, "ufs"},
		{"fsio_ufs", true, "ufs,vfs"},
		{"fsio_block", false, "blk"},
		{"fsio_block", true, "blk,vfs"},
	}
	for _, tt := range tests {
		if got := fsioOnlyLayer(tt.traceType, tt.includeVFS); got != tt.want {
			t.Errorf("fsioOnlyLayer(%q, %v) = %q, want %q",
				tt.traceType, tt.includeVFS, got, tt.want)
		}
	}
}

// TestBuildFsioCommandIncludeVFS — 실제 커맨드 문자열까지 확인한다.
func TestBuildFsioCommandIncludeVFS(t *testing.T) {
	got := buildFsioCommand("fsio_ufs", true)
	if !strings.Contains(got, "--only ufs,vfs") {
		t.Errorf("include_vfs=true 인데 vfs 가 빠졌다: %q", got)
	}
	if off := buildFsioCommand("fsio_ufs", false); strings.Contains(off, "vfs") {
		t.Errorf("include_vfs=false 인데 vfs 가 붙었다: %q", off)
	}
}
