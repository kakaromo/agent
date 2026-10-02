package benchmark

import (
	"os"
	"strings"
	"testing"
)

// 실기기(SM-S938N, 로그아웃 계정) 재생 중 수집한 dumpsys activity top — 화면과 대조한 정답.
// 각 픽스처 앞에는 visible ad_progress_text 를 가진 다른 앱 activity 를 붙여,
// YouTube activity 만 고르는지도 함께 검사한다.
func TestYoutubeTreeStateFixtures(t *testing.T) {
	for _, tc := range []struct {
		file, want string
		skip       bool
	}{
		{"01_loading.txt", "player", false},
		{"03_ad_noskip.txt", "ad", false},
		{"05_ad_skip.txt", "ad", true},
		{"10_ad_second_noskip.txt", "ad", false}, // skip_ad_button_container 가 V 로 남아 있는 상태
		{"14_content.txt", "player", false},
	} {
		b, err := os.ReadFile("testdata/youtube_activity_top/" + tc.file)
		if err != nil {
			t.Fatal(err)
		}
		views, ok := parseYoutubeViewTree(string(b))
		if !ok {
			t.Fatalf("%s: YouTube 뷰 트리를 못 찾았다", tc.file)
		}
		got, skip := youtubeTreeState(views)
		if got != tc.want || (skip != nil) != tc.skip {
			t.Errorf("%s: state=%q skip=%v, want %q skip=%v", tc.file, got, skip != nil, tc.want, tc.skip)
		}
	}
}

// TestYoutubeTreeSkipAbsolute — 건너뛰기 좌표는 조상 오프셋을 더한 화면 절대좌표다.
// 부모 기준(1098,450-1440,630)을 그대로 쓰면 플레이어 높이만큼 위를 누른다.
func TestYoutubeTreeSkipAbsolute(t *testing.T) {
	b, err := os.ReadFile("testdata/youtube_activity_top/05_ad_skip.txt")
	if err != nil {
		t.Fatal(err)
	}
	views, _ := parseYoutubeViewTree(string(b))
	_, skip := youtubeTreeState(views)
	if skip == nil || skip.Bounds != [4]int{1098, 578, 1440, 758} {
		t.Fatalf("skip=%+v", skip)
	}
}

// TestYoutubeTreeAncestorVisibility — 자기 플래그가 V 여도 조상이 GONE 이면 안 보인다.
func TestYoutubeTreeAncestorVisibility(t *testing.T) {
	out := strings.Join([]string{
		"  ACTIVITY com.google.android.youtube/.app.honeycomb.Shell$HomeActivity 1 pid=1",
		"      mResumed=true mStopped=false mFinished=false",
		"    View Hierarchy:",
		"      com.android.internal.policy.DecorView{1 V.E...... R....... 0,0-1440,3120}",
		"        android.widget.FrameLayout{2 VFE...... ........ 0,128-1440,938 #7f0b18ba app:id/watch_player}",
		"          android.widget.FrameLayout{3 G.E...... ......I. 0,0-1440,810 #1 app:id/ad_overlay}",
		"            android.widget.TextView{4 V.ED..... ........ 15,15-202,150 #7f0b00f8 app:id/ad_progress_text}",
		"          82116979(0,0,1440,810)",
		"    Looper (main, tid 2) {f95bf5d}",
	}, "\n")
	views, ok := parseYoutubeViewTree(out)
	if !ok {
		t.Fatal("트리를 못 읽었다")
	}
	if got, _ := youtubeTreeState(views); got != "player" {
		t.Fatalf("GONE 조상 아래 광고 표시를 광고로 판정했다: %q", got)
	}
}

// TestYoutubeTreeRequiresResumed — 백그라운드(stopped) YouTube 의 뷰 트리로는 판정하지 않는다.
func TestYoutubeTreeRequiresResumed(t *testing.T) {
	b, err := os.ReadFile("testdata/youtube_activity_top/14_content.txt")
	if err != nil {
		t.Fatal(err)
	}
	stopped := strings.Replace(string(b), "mResumed=true", "mResumed=false", 1)
	if _, ok := parseYoutubeViewTree(stopped); ok {
		t.Fatal("stopped 상태의 뷰 트리를 판정에 썼다")
	}
}
