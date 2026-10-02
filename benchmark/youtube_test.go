package benchmark

import (
	"agent/macro"
	"context"
	"strings"
	"testing"
	"time"
)

func TestYoutubeAdClassification(t *testing.T) {
	for _, tc := range []struct {
		name          string
		els           []macro.UIElement
		ad, sponsored bool
	}{
		{"추천 광고는 재생 광고가 아님", []macro.UIElement{{ResourceID: "com.google.android.youtube:id/watch_player"}, {ContentDesc: "스폰서 - 광고 제목 - 동영상 재생"}}, false, true},
		{"제목의 광고 단어", []macro.UIElement{{ContentDesc: "광고 없이 음악 듣기 - 동영상 재생"}}, false, false},
		{"광고 패널", []macro.UIElement{{ContentDesc: "광고 패널 닫기"}}, true, false},
		{"Shorts 스폰서", []macro.UIElement{{ResourceID: "com.google.android.youtube:id/reel_play_pause_button"}, {Text: "Sponsored"}}, true, true},
		{"건너뛰기", []macro.UIElement{{ContentDesc: "광고 건너뛰기"}}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := inspectYoutube(tc.els)
			if s.Ad != tc.ad || s.Sponsored != tc.sponsored {
				t.Fatalf("%+v", s)
			}
		})
	}
}
func TestYoutubeMediaSessionScope(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want bool
	}{
		{"package=com.google.android.youtube\n active=true state=PlaybackState {state=PLAYING(3),", true},
		{"package=com.google.android.youtube\n active=true state=PlaybackState {state=3,", true},
		{"package=com.google.android.youtube\n active=true state=PlaybackState {state=PAUSED(2),", false},
		{"package=com.google.android.youtube\n active=false\npackage=other\n active=true state=PlaybackState {state=3,", false},
	} {
		if got := youtubePlaying(tc.s); got != tc.want {
			t.Fatalf("got %v for %s", got, tc.s)
		}
	}
}
func TestYoutubeMonitorExcludesAdsAndUnknown(t *testing.T) {
	n, skips := 0, 0
	states := []string{"ad", "ad", "unknown", "content", "ad", "content", "content", "content"}
	var segments []youtubeSegment
	m, err := monitorYoutube(context.Background(), 5*time.Millisecond, time.Second, time.Millisecond,
		func(context.Context) (youtubeSample, error) {
			i := n
			n++
			if i >= len(states) {
				i = len(states) - 1
			}
			s := youtubeSample{State: states[i]}
			if s.State == "ad" {
				s.Skip = &macro.UIElement{}
			}
			return s, nil
		},
		func(context.Context, *macro.UIElement) error { skips++; return nil }, func(s youtubeSegment) { segments = append(segments, s) })
	if err != nil {
		t.Fatal(err)
	}
	if m["ad_seconds"] <= 0 || m["unknown_seconds"] <= 0 || m["content_seconds"] < .005 || skips != 3 {
		t.Fatalf("metrics=%v skips=%d", m, skips)
	}
}
func TestYoutubeMonitorTimeoutAndCancel(t *testing.T) {
	sample := func(context.Context) (youtubeSample, error) { return youtubeSample{State: "unknown"}, nil }
	skip := func(context.Context, *macro.UIElement) error { return nil }
	_, err := monitorYoutube(context.Background(), 0, 5*time.Millisecond, time.Millisecond, sample, skip, func(youtubeSegment) {})
	if err == nil {
		t.Fatal("unknown 화면을 성공으로 처리")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = monitorYoutube(ctx, time.Second, time.Second, time.Millisecond, sample, skip, func(youtubeSegment) {})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("cancel: %v", err)
	}
}

func TestYoutubeOCRRejectsBackgroundAndLowConfidence(t *testing.T) {
	s := "5\t1\t1\t1\t1\t1\t20\t30\t100\t40\t98\t1080p\n" +
		"5\t1\t1\t1\t1\t2\t20\t130\t100\t40\t30\t720p\n" +
		"5\t1\t1\t1\t1\t3\t20\t230\t100\t40\t98\t360p\n"
	els := parseYoutubeOCR(s, 100)
	if len(els) != 1 || els[0].Text != "360p" || els[0].CenterY != 250 {
		t.Fatalf("%+v", els)
	}
}

func TestYoutubeShortsPlaybackControl(t *testing.T) {
	for _, tc := range []struct {
		label string
		want  bool
	}{
		{"동영상 일지중지", true},
		{"동영상 일시중지", true},
		{"Pause video", true},
		{"동영상 재생", false},
		{"Play video", false},
		{"", false},
	} {
		els := []macro.UIElement{{ResourceID: "com.google.android.youtube:id/reel_play_pause_button", ContentDesc: tc.label}}
		if youtubeShortsPlaying(els) != tc.want {
			t.Fatal(tc)
		}
	}
}

func TestYoutubeQualityFPSLabels(t *testing.T) {
	for _, label := range []string{"720p", "720p60", "720p50"} {
		if !youtubeQualityLabel(label, "720p") {
			t.Fatalf("지원 화질 누락: %s", label)
		}
	}
	for _, label := range []string{"1080p60", "자동 (720p60)", "720p Premium", "720p600"} {
		if youtubeQualityLabel(label, "720p") {
			t.Fatalf("다른 항목을 화질로 오인: %s", label)
		}
	}
}

func TestYoutubeVideoCandidate(t *testing.T) {
	for _, tc := range []struct {
		desc string
		want bool
	}{
		{"일반 영상 - 동영상 재생", true},
		{"스폰서 - 광고 영상 - 동영상 재생", false},
		{"일반 영상 - Shorts 동영상 재생", false},
		{"동영상 재생", false},
	} {
		e := macro.UIElement{ContentDesc: tc.desc, Bounds: [4]int{0, 300, 1200, 1400}, CenterY: 850}
		if youtubeVideoCandidate(e, "", 2700) != tc.want {
			t.Fatal(tc)
		}
	}
}

// TestYoutubeMonitorMergesSegments — 같은 상태가 이어지는 표본은 한 구간으로 합친다.
// 표본마다 구간을 내면 장시간 재생에서 Behavior 구간이 수백 개가 된다.
func TestYoutubeMonitorMergesSegments(t *testing.T) {
	n := 0
	states := []string{"ad", "ad", "ad", "unknown", "content", "content", "content", "content", "content"}
	var segments []youtubeSegment
	_, err := monitorYoutube(context.Background(), 4*time.Millisecond, time.Second, time.Millisecond,
		func(context.Context) (youtubeSample, error) {
			i := n
			n++
			if i >= len(states) {
				i = len(states) - 1
			}
			return youtubeSample{State: states[i]}, nil
		},
		func(context.Context, *macro.UIElement) error { return nil },
		func(s youtubeSegment) { segments = append(segments, s) })
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i, s := range segments {
		got = append(got, s.State)
		if i > 0 && segments[i-1].End != s.Start {
			t.Fatalf("구간 사이가 비거나 겹친다: %+v", segments)
		}
	}
	// ad·ad·ad → ad 하나, 경계 표본들 → unknown 하나, 나머지 → content 하나.
	if strings.Join(got, ",") != "ad,unknown,content" {
		t.Fatalf("합쳐진 구간 = %v", got)
	}
}

// TestYoutubeMonitorFlushesOnError — 에러로 끝나도 진행 중이던 구간을 잃지 않는다.
func TestYoutubeMonitorFlushesOnError(t *testing.T) {
	var segments []youtubeSegment
	_, err := monitorYoutube(context.Background(), time.Second, 5*time.Millisecond, time.Millisecond,
		func(context.Context) (youtubeSample, error) { return youtubeSample{State: "ad"}, nil },
		func(context.Context, *macro.UIElement) error { return nil },
		func(s youtubeSegment) { segments = append(segments, s) })
	if err == nil {
		t.Fatal("광고 한도 초과 에러가 나야 한다")
	}
	if len(segments) != 1 || segments[0].State != "ad" {
		t.Fatalf("마지막 구간이 기록되지 않았다: %+v", segments)
	}
}
