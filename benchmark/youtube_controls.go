package benchmark

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"agent/adb"
	"agent/macro"
)

// 재생 버튼의 현재 동작 이름으로 상태를 구분한다. toggle 키를 맹목적으로 누르지 않는다.
func youtubePaused(label string) bool {
	return label == "동영상 재생" || label == "Play video" || label == "다시 재생" || label == "Replay video"
}

func youtubePauseControl(label string) bool {
	return label == "동영상 일시중지" || label == "동영상 일시 중지" || label == "동영상 일지중지" || label == "Pause video"
}

func youtubeFullscreenExit(label string) bool {
	return strings.Contains(label, "전체화면 종료") || strings.Contains(label, "전체 화면 종료") || strings.Contains(strings.ToLower(label), "exit full")
}

var youtubePausedStateRE = regexp.MustCompile(`state=PlaybackState\s*\{state=(?:PAUSED\()?2\)?[, ]`)

func youtubeMediaPaused(out string) bool {
	for _, section := range strings.Split(out, "package=")[1:] {
		if (strings.HasPrefix(section, "com.google.android.youtube\n") || strings.HasPrefix(section, "com.google.android.youtube\r")) && strings.Contains(section, "active=true") && youtubePausedStateRE.MatchString(section) {
			return true
		}
	}
	return false
}

func youtubeControl(ctx context.Context, dev *adb.Device, target string) (out string, metrics map[string]float64, resultErr error) {
	ids := map[string]string{"play": "player_control_play_pause_replay_button", "pause": "player_control_play_pause_replay_button", "fullscreen": "fullscreen_button", "inline": "fullscreen_button", "next": "player_control_next_button", "previous": "player_control_previous_button", "minimize": "player_collapse_button"}
	id, ok := ids[target]
	if !ok {
		return "", nil, fmt.Errorf("지원하지 않는 YouTube control: %s", target)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	focus, err := dev.Shell(ctx, "dumpsys window | grep mCurrentFocus")
	if err != nil || !strings.Contains(focus, "com.google.android.youtube/") {
		return "", nil, fmt.Errorf("YouTube가 전면 앱이 아닙니다")
	}
	media, _ := dev.Shell(ctx, "dumpsys media_session")
	if target == "play" || target == "pause" {
		if target == "play" && youtubePlaying(media) || target == "pause" && youtubeMediaPaused(media) {
			return "YOUTUBE_CONTROL_VERIFIED|" + target, nil, nil
		}
		if youtubePlaying(media) || youtubeMediaPaused(media) {
			if _, err := dev.Shell(ctx, "cmd media_session dispatch "+target); err != nil {
				return "", nil, err
			}
			for attempt := 0; attempt < 3; attempt++ {
				if err := youtubeWait(ctx, 500*time.Millisecond); err != nil {
					return "", nil, err
				}
				media, _ = dev.Shell(ctx, "dumpsys media_session")
				if target == "play" && youtubePlaying(media) || target == "pause" && youtubeMediaPaused(media) {
					return "YOUTUBE_CONTROL_VERIFIED|" + target, nil, nil
				}
			}
		}
	} else if youtubePlaying(media) {
		// 움직이는 영상의 UI idle 실패를 피하고 메뉴 조작 뒤 재생을 복원한다.
		if _, err := dev.Shell(ctx, "cmd media_session dispatch pause"); err != nil {
			return "", nil, err
		}
		defer func() {
			restore, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := dev.Shell(restore, "cmd media_session dispatch play"); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("재생 복원 실패: %w", err))
			}
		}()
	}
	tapped := false
	for attempt := 0; attempt < 5; attempt++ {
		probe, probeCancel := context.WithTimeout(ctx, 12*time.Second)
		els, err := macro.DumpUIElements(probe, dev, false)
		probeCancel()
		if err != nil {
			if ctx.Err() != nil || attempt == 4 {
				return "", nil, err
			}
			continue
		}
		var player, button *macro.UIElement
		for i := range els {
			e := &els[i]
			if e.ResourceID == "com.google.android.youtube:id/watch_player" || e.ResourceID == "com.google.android.youtube:id/reel_watch_player" {
				player = e
			}
			if e.ResourceID == "com.google.android.youtube:id/"+id || ((target == "play" || target == "pause") && e.ResourceID == "com.google.android.youtube:id/reel_play_pause_button") {
				button = e
			}
		}
		if button != nil {
			label := button.ContentDesc
			done := target == "play" && youtubePauseControl(label) || target == "pause" && youtubePaused(label) || target == "fullscreen" && youtubeFullscreenExit(label) || target == "inline" && !youtubeFullscreenExit(label) && label != ""
			if done {
				return "YOUTUBE_CONTROL_VERIFIED|" + target, nil, nil
			}
			if (target == "play" || target == "pause") && !youtubePaused(label) && !youtubePauseControl(label) {
				return "", nil, fmt.Errorf("재생 컨트롤 상태를 확인할 수 없습니다: %s", label)
			}
			if !tapped {
				if _, err = dev.Shell(ctx, fmt.Sprintf("input tap %d %d", button.CenterX, button.CenterY)); err != nil {
					return "", nil, err
				}
				tapped = true
				if target == "next" || target == "previous" || target == "minimize" {
					return "YOUTUBE_CONTROL_REQUESTED|" + target, nil, nil
				}
			}
		} else if player != nil {
			if _, err = dev.Shell(ctx, fmt.Sprintf("input tap %d %d", player.Bounds[0]+(player.Bounds[2]-player.Bounds[0])/3, player.CenterY)); err != nil {
				return "", nil, err
			}
		} else {
			return "", nil, fmt.Errorf("YouTube 플레이어가 없습니다: %s", target)
		}
		if err := youtubeWait(ctx, 500*time.Millisecond); err != nil {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("YouTube 컨트롤 적용 확인 실패: %s", target)
}

func youtubeWait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type youtubeShell func(context.Context, string) (string, error)

// 연결 복구는 취소된 실행 컨텍스트와 분리한다. 원래 꺼져 있던 망은 켜지 않는다.
func youtubeNetworkCycle(ctx context.Context, shell youtubeShell, duration time.Duration) (err error) {
	wifi, err := shell(ctx, "settings get global wifi_on")
	if err != nil {
		return err
	}
	data, err := shell(ctx, "settings get global mobile_data")
	if err != nil {
		return err
	}
	wifi, data = strings.TrimSpace(wifi), strings.TrimSpace(data)
	if (wifi != "0" && wifi != "1") || (data != "0" && data != "1") {
		return fmt.Errorf("네트워크 원래 상태 확인 실패 (wifi=%s, data=%s)", wifi, data)
	}
	if wifi == "0" && data == "0" {
		return fmt.Errorf("연결 복구 테스트에는 켜진 네트워크가 필요합니다")
	}
	defer func() {
		restore, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, state := range []struct{ service, enabled string }{{"wifi", wifi}, {"data", data}} {
			if state.enabled == "1" {
				if _, e := shell(restore, "svc "+state.service+" enable"); e != nil {
					err = errors.Join(err, fmt.Errorf("%s 복원 실패: %w", state.service, e))
				}
			}
		}
	}()
	if wifi == "1" {
		if _, err = shell(ctx, "svc wifi disable"); err != nil {
			return err
		}
	}
	if data == "1" {
		if _, err = shell(ctx, "svc data disable"); err != nil {
			return err
		}
	}
	return youtubeWait(ctx, duration)
}
