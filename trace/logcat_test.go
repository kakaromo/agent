package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildLogcatArgs(t *testing.T) {
	// measure — `-s` 로 좁혀야 한다. 태그에 레벨이 없으면 :V 를 붙인다.
	got := buildLogcatArgs("SER1", LogcatFormatMonotonic, LogcatModeMeasure,
		[]string{"Genie", "QnnHtp:I", "  ", "io_stats"})
	want := []string{"-s", "SER1", "logcat", "-v", "monotonic",
		"-s", "Genie:V", "QnnHtp:I", "io_stats:V"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("measure args\n got: %v\nwant: %v", got, want)
	}

	// explore — 태그 제한이 없어야 한다. `-s` 필터가 붙으면 넓게 못 받는다.
	ex := buildLogcatArgs("SER1", LogcatFormatEpoch, LogcatModeExplore, []string{"Genie"})
	joined := strings.Join(ex, " ")
	if strings.Contains(joined, "Genie") {
		t.Errorf("explore 모드에 태그 필터가 붙었다: %v", ex)
	}
	if !strings.Contains(joined, "-v epoch") {
		t.Errorf("epoch 형식이 반영되지 않았다: %v", ex)
	}
}

// ⚠⚠ 이 테스트가 보안 가드다. output_dir 은 사무실 모드에서 인증 없는 0.0.0.0
// 바인딩 위로 들어온다. 허용 루트 밖 경로를 그대로 쓰면 임의 경로 쓰기가 된다.
func TestLogcatResolveOutputBase(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()
	m := NewLogcatManager(nil, base)
	m.AddSearchRoot(extra)

	// 빈 값 → 기본
	if got := m.resolveOutputBase(""); got != base {
		t.Errorf("빈 값: %q, 기대 %q", got, base)
	}
	// 허용 루트 밑 → 통과
	inside := filepath.Join(base, "jobs", "x")
	if got := m.resolveOutputBase(inside); got != inside {
		t.Errorf("허용 경로가 거부됐다: %q", got)
	}
	if got := m.resolveOutputBase(extra); got != extra {
		t.Errorf("등록한 searchRoot 가 거부됐다: %q", got)
	}

	// ⚠ 아래는 전부 **기본 위치로 되돌아가야** 한다.
	rejects := map[string]string{
		"상위 탈출":        filepath.Join(base, "..", "evil"),
		"절대 경로":        "/etc",
		"prefix 유사 경로": base + "-evil", // 문자열 prefix 비교였다면 통과해 버린다
	}
	for name, p := range rejects {
		if got := m.resolveOutputBase(p); got != base {
			t.Errorf("%s: %q 가 허용됐다 (기대: 기본 위치 %q)", name, p, base)
		}
	}
}

func TestCountLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.log")
	if err := os.WriteFile(p, []byte("a\nb\nc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if n, err := countLines(p); err != nil || n != 3 {
		t.Errorf("countLines = %d, %v — 기대 3", n, err)
	}
	// 빈 파일 = 0줄. "한 줄도 못 받았다" 진단의 근거라 정확해야 한다.
	empty := filepath.Join(dir, "empty.log")
	os.WriteFile(empty, nil, 0644)
	if n, err := countLines(empty); err != nil || n != 0 {
		t.Errorf("빈 파일 countLines = %d, %v — 기대 0", n, err)
	}
	if _, err := countLines(filepath.Join(dir, "nope.log")); err == nil {
		t.Error("없는 파일인데 에러가 안 났다")
	}
}
