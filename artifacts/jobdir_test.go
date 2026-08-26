package artifacts

import (
	"strings"
	"testing"
	"time"
)

func TestJobDirName(t *testing.T) {
	at := time.Date(2026, 8, 26, 22, 28, 41, 0, time.UTC)

	tests := []struct {
		name             string
		typ, jobName, id string
		want             string
	}{
		{"이름 있음", "scenario", "UI 검증", "48b10aec-5aad", "20260826-222841_scenario_UI_검증"},
		{"이름 없으면 생략", "scenario", "", "48b10aec-5aad", "20260826-222841_scenario"},
		{"타입·이름 다 없으면 id 로 구분", "", "", "48b10aec-5aad", "20260826-222841_48b10aec"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := JobDirName(at, tt.typ, tt.jobName, tt.id); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ⚠ jobName 은 사용자 입력이다. 경로 조작이 통하면 archiveBase 밖에 쓰게 된다.
func TestJobDirNameRejectsTraversal(t *testing.T) {
	at := time.Date(2026, 8, 26, 22, 28, 41, 0, time.UTC)
	for _, bad := range []string{"../../etc/passwd", "a/b/c", "..", "....//", "a\\b"} {
		got := JobDirName(at, "scenario", bad, "id")
		if strings.Contains(got, "/") || strings.Contains(got, "\\") || strings.Contains(got, "..") {
			t.Errorf("경로 조작이 남았다: %q → %q", bad, got)
		}
	}
}

func TestJobDirNameKeepsKorean(t *testing.T) {
	at := time.Date(2026, 8, 26, 22, 28, 41, 0, time.UTC)
	got := JobDirName(at, "scenario", "유튜브 콜드실행", "id")
	if !strings.Contains(got, "유튜브") {
		t.Errorf("한글이 깎였다: %q", got)
	}
}

// ⚠ 존이 달라도 같은 순간이면 같은 이름이어야 한다. 아니면 시나리오(로컬)와
// hook(UTC)이 같은 잡을 두 폴더에 쓴다 — 실제로 KST 에서 9시간 차로 갈라졌다.
func TestJobDirNameIsTimezoneStable(t *testing.T) {
	utc := time.Date(2026, 8, 26, 14, 4, 1, 0, time.UTC)
	kst := utc.In(time.FixedZone("KST", 9*3600))
	a := JobDirName(utc, "scenario", "x", "id")
	b := JobDirName(kst, "scenario", "x", "id")
	if a != b {
		t.Errorf("존에 따라 이름이 갈렸다: %q vs %q", a, b)
	}
}

func TestJobDirNameZeroTime(t *testing.T) {
	// 시각이 없으면 이름을 못 만드는 게 아니라 현재 시각을 쓴다.
	if got := JobDirName(time.Time{}, "trace", "x", "id"); got == "" {
		t.Error("빈 이름이 나왔다")
	}
}

// ⚠ Windows 는 디렉토리 이름 끝의 '.' 을 조용히 버린다. 자른 자리가 '.' 이면
// 그 뒤만 다른 두 잡 이름이 **한 폴더로 합쳐진다.** (windows-amd64 를 빌드해 배포한다)
func TestJobDirNameNoTrailingDotAfterTruncation(t *testing.T) {
	at := time.Date(2026, 8, 26, 22, 28, 41, 0, time.UTC)
	// 40번째 글자가 '.' 이 되도록 만든다
	name := strings.Repeat("a", 39) + "." + "evil"
	got := JobDirName(at, "scenario", name, "id")
	if strings.HasSuffix(got, ".") {
		t.Errorf("이름이 '.' 로 끝난다 (Windows 에서 충돌): %q", got)
	}
}
