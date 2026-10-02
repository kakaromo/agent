package benchmark

import (
	"agent/adb"
	"agent/macro"
	"agent/pb"
	"context"
	"os"
	"testing"
	"time"
)

// 명시적으로 지정한 테스트 기기의 현재 일반 영상에서만 실행한다.
func TestYoutubeDeviceQuality(t *testing.T) {
	serial := os.Getenv("YOUTUBE_TEST_DEVICE")
	if serial == "" {
		t.Skip("실기기 검증은 YOUTUBE_TEST_DEVICE 지정 시에만 실행")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dev := adb.NewDevice(serial)
	watch := func(seconds string) {
		o := &Orchestrator{}
		_, metrics, err := o.executeYoutube(ctx, nil, &adb.ManagedDevice{Device: dev}, expandedStep{step: &pb.ScenarioStep{Type: "youtube", Params: map[string]string{"action": "watch", "seconds": seconds, "ad_timeout": "180"}}}, serial, "")
		if err != nil {
			t.Fatal("watch", err)
		}
		t.Log("watch", metrics)
	}
	watch("0")
	for _, q := range []string{"360p", "720p", "1080p"} {
		out, _, err := setYoutubeQuality(ctx, dev, q)
		if err != nil {
			t.Fatal(q, err)
		}
		t.Log(out)
		watch("6")
	}
}

func TestYoutubeDeviceMenuOCR(t *testing.T) {
	serial := os.Getenv("YOUTUBE_TEST_DEVICE")
	if serial == "" {
		t.Skip("실기기 지정 필요")
	}
	dev := adb.NewDevice(serial)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	els, err := macro.DumpUIElements(ctx, dev, false)
	if err != nil {
		t.Fatal(err)
	}
	words, err := youtubeOCRMenu(ctx, dev, els)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range words {
		t.Log(w.Text, w.CenterX, w.CenterY)
	}
}

func TestYoutubeDeviceControls(t *testing.T) {
	serial := os.Getenv("YOUTUBE_TEST_DEVICE")
	if serial == "" {
		t.Skip("실기기 지정 필요")
	}
	dev := adb.NewDevice(serial)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	for _, target := range []string{"play", "pause", "fullscreen", "inline", "play"} {
		out, _, err := youtubeControl(ctx, dev, target)
		if err != nil {
			t.Fatal(target, err)
		}
		t.Log(out)
	}
}

func TestYoutubeDevicePanels(t *testing.T) {
	serial := os.Getenv("YOUTUBE_TEST_DEVICE")
	if serial == "" {
		t.Skip("실기기 지정 필요")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dev := adb.NewDevice(serial)
	for _, target := range []string{"comments", "close_panel", "description", "close_panel"} {
		out, _, err := youtubePanel(ctx, dev, target)
		if err != nil {
			t.Fatal(target, err)
		}
		t.Log(out)
	}
}
