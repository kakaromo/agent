package trace

import (
	"path/filepath"
	"testing"
)

// TestResolveOutputBase — output_dir 은 gRPC 로 들어오는 경로다.
//
// 사무실 모드는 0.0.0.0 바인딩에 인증이 없어, 검사 없이 쓰면 임의 위치에 디렉토리를
// 만들고 로그를 쓰게 된다.
func TestResolveOutputBase(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()
	m := &Manager{outputBase: base}
	m.AddSearchRoot(extra)

	t.Run("빈 값은 기본 위치", func(t *testing.T) {
		if got := m.resolveOutputBase(""); got != base {
			t.Errorf("got %q, want %q", got, base)
		}
	})

	t.Run("허용 루트 밑은 통과", func(t *testing.T) {
		want := filepath.Join(base, "jobs", "x", "trace")
		if got := m.resolveOutputBase(want); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("등록된 검색 루트도 통과", func(t *testing.T) {
		want := filepath.Join(extra, "jobs", "y", "trace")
		if got := m.resolveOutputBase(want); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("루트 밖은 거부 — 기본 위치로", func(t *testing.T) {
		for _, bad := range []string{"/etc", "/tmp/somewhere-else", filepath.Join(base, "..", "escape")} {
			if got := m.resolveOutputBase(bad); got != base {
				t.Errorf("%q 를 받아들였다: %q", bad, got)
			}
		}
	})

	t.Run("이름이 비슷한 형제 디렉토리는 거부", func(t *testing.T) {
		// 문자열 prefix 비교였다면 통과해 버린다.
		if got := m.resolveOutputBase(base + "-evil"); got != base {
			t.Errorf("형제 디렉토리를 받아들였다: %q", got)
		}
	})
}
