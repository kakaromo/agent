package benchmark

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestYoutubeNetworkRestore(t *testing.T) {
	for _, tc := range []struct {
		name, data, fail string
		cancel           bool
	}{
		{"wifi_only", "0", "", false},
		{"both", "1", "", false},
		{"partial_failure", "1", "svc data disable", false},
		{"cancel", "1", "", true},
		{"restore_failure", "1", "svc wifi enable", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var commands []string
			shell := func(callCtx context.Context, cmd string) (string, error) {
				commands = append(commands, cmd)
				if strings.HasSuffix(cmd, " enable") && callCtx.Err() != nil {
					t.Fatal("복원에 취소된 컨텍스트 사용")
				}
				if cmd == "settings get global wifi_on" {
					return "1", nil
				}
				if cmd == "settings get global mobile_data" {
					return tc.data, nil
				}
				if cmd == tc.fail {
					return "", fmt.Errorf("실패 주입")
				}
				if cmd == "svc wifi disable" && tc.cancel {
					cancel()
				}
				return "", nil
			}
			err := youtubeNetworkCycle(ctx, shell, time.Millisecond)
			if (tc.fail != "" || tc.cancel) != (err != nil) {
				t.Fatalf("err=%v", err)
			}
			joined := strings.Join(commands, ";")
			if !strings.Contains(joined, "svc wifi enable") {
				t.Fatal(joined)
			}
			if strings.Contains(joined, "svc data enable") != (tc.data == "1") {
				t.Fatal(joined)
			}
		})
	}
}

func TestYoutubeNetworkRejectsUnknownState(t *testing.T) {
	var commands []string
	err := youtubeNetworkCycle(context.Background(), func(_ context.Context, cmd string) (string, error) {
		commands = append(commands, cmd)
		return "null", nil
	}, time.Second)
	if err == nil || len(commands) != 2 {
		t.Fatalf("%v %v", err, commands)
	}
}

func TestYoutubeControlLabels(t *testing.T) {
	if !youtubePaused("동영상 재생") || youtubePaused("동영상 일시중지") || !youtubePauseControl("동영상 일지중지") || !youtubeFullscreenExit("전체화면 종료") || youtubeFullscreenExit("전체화면으로 전환") {
		t.Fatal("컨트롤 상태 판별 실패")
	}
}
