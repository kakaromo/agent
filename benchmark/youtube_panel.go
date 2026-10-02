package benchmark

import (
	"context"
	"errors"
	"fmt"
	"time"

	"agent/adb"
	"agent/macro"
)

func youtubePanelTitle(els []macro.UIElement) string {
	for _, e := range els {
		if e.ResourceID == "com.google.android.youtube:id/modern_title" && (e.Text == "댓글" || e.Text == "Comments" || e.Text == "설명" || e.Text == "Description") {
			return e.Text
		}
	}
	return ""
}

// 영상 영역을 제외한 메뉴 문구를 찾고, 열린 패널 제목까지 확인한다.
func youtubePanel(ctx context.Context, dev *adb.Device, target string) (out string, metrics map[string]float64, resultErr error) {
	if target != "comments" && target != "description" && target != "close_panel" {
		return "", nil, fmt.Errorf("지원하지 않는 패널: %s", target)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	media, _ := dev.Shell(ctx, "dumpsys media_session")
	if youtubePlaying(media) {
		if _, err := dev.Shell(ctx, "cmd media_session dispatch pause"); err != nil {
			return "", nil, err
		}
		defer func() {
			restore, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := dev.Shell(restore, "cmd media_session dispatch play"); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}()
	}
	els, err := macro.DumpUIElements(ctx, dev, false)
	if err != nil {
		return "", nil, err
	}
	// 시작 광고가 끝나도 광고 패널이 남는 버전은 명시적인 접기 버튼만 누른다.
	if target != "close_panel" {
		for _, e := range els {
			if e.ContentDesc == "광고 접기" || e.ContentDesc == "광고 패널 닫기" || e.ContentDesc == "Close ad panel" {
				if _, err = dev.Shell(ctx, fmt.Sprintf("input tap %d %d", e.CenterX, e.CenterY)); err != nil {
					return "", nil, err
				}
				els, err = macro.DumpUIElements(ctx, dev, false)
				if err != nil {
					return "", nil, err
				}
				break
			}
		}
	}
	title := youtubePanelTitle(els)
	if target == "close_panel" {
		if title == "" {
			return "", nil, fmt.Errorf("닫을 댓글·설명 패널이 없습니다")
		}
		for _, e := range els {
			if e.ResourceID == "com.google.android.youtube:id/close_button" {
				if _, err = dev.Shell(ctx, fmt.Sprintf("input tap %d %d", e.CenterX, e.CenterY)); err != nil {
					return "", nil, err
				}
				break
			}
		}
	} else {
		if title != "" {
			return "", nil, fmt.Errorf("먼저 열린 패널을 닫아야 합니다: %s", title)
		}
		top := 0
		for _, e := range els {
			if e.ResourceID == "com.google.android.youtube:id/watch_player" {
				top = e.Bounds[3]
			}
		}
		if top == 0 {
			return "", nil, fmt.Errorf("일반 영상의 세로 플레이어가 없습니다")
		}
		match := func(e macro.UIElement) bool {
			if e.CenterY <= top {
				return false
			}
			for _, v := range []string{e.Text, e.ContentDesc} {
				if target == "comments" && (v == "댓글" || v == "Comments") || target == "description" && (v == "더보기" || v == "more" || v == "More") {
					return true
				}
			}
			return false
		}
		var candidate *macro.UIElement
		for i := range els {
			if match(els[i]) {
				candidate = &els[i]
				break
			}
		}
		if candidate == nil {
			words, err := youtubeOCRScreen(ctx, dev, top)
			if err != nil {
				return "", nil, err
			}
			for i := range words {
				if match(words[i]) {
					candidate = &words[i]
					break
				}
			}
		}
		if candidate == nil {
			return "", nil, fmt.Errorf("패널 열기 문구를 찾지 못했습니다: %s", target)
		}
		if _, err = dev.Shell(ctx, fmt.Sprintf("input tap %d %d", candidate.CenterX, candidate.CenterY)); err != nil {
			return "", nil, err
		}
	}
	if err = youtubeWait(ctx, 500*time.Millisecond); err != nil {
		return "", nil, err
	}
	els, err = macro.DumpUIElements(ctx, dev, false)
	if err != nil {
		return "", nil, err
	}
	title = youtubePanelTitle(els)
	if target == "close_panel" && title == "" || target == "comments" && (title == "댓글" || title == "Comments") || target == "description" && (title == "설명" || title == "Description") {
		return "YOUTUBE_PANEL_VERIFIED|" + target, nil, nil
	}
	return "", nil, fmt.Errorf("패널 상태 확인 실패: %s (%s)", target, title)
}
