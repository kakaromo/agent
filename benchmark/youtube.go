package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agent/adb"
	"agent/macro"
	"agent/pb"
)

// 광고 문구는 영상 제목의 부분 문자열로 판정하지 않는다. 추천 광고 카드와
// 재생 광고를 구분하고, 건너뛰기는 명시적인 광고 버튼만 누른다.
type youtubeScreen struct {
	Player, Shorts, Ad, Sponsored bool
	Skip                          *macro.UIElement
}

func inspectYoutube(els []macro.UIElement) youtubeScreen {
	s := youtubeScreen{}
	for i := range els {
		e := &els[i]
		if e.ResourceID == "com.google.android.youtube:id/watch_player" {
			s.Player = true
		}
		if e.ResourceID == "com.google.android.youtube:id/reel_play_pause_button" {
			s.Player = true
			s.Shorts = true
		}
		for _, v := range []string{e.Text, e.ContentDesc} {
			if strings.HasPrefix(v, "스폰서 - ") || v == "스폰서" || v == "Sponsored" {
				s.Sponsored = true
			}
			switch v {
			case "광고주 페이지 방문", "광고 패널 닫기", "Visit advertiser", "Close ad panel", "광고", "Ad":
				s.Ad = true
			case "광고 건너뛰기", "건너뛰기", "Skip ad", "Skip ads":
				s.Ad = true
				s.Skip = e
			}
		}
		if strings.HasPrefix(e.ResourceID, "com.google.android.youtube:id/") && (strings.HasSuffix(e.ResourceID, "ad_skip_button") || strings.HasSuffix(e.ResourceID, "skip_ad_button")) {
			s.Ad = true
			s.Skip = e
		}
	}
	if s.Shorts && s.Sponsored {
		s.Ad = true
	}
	return s
}

var youtubePlayingRE = regexp.MustCompile(`state=PlaybackState\s*\{state=(?:PLAYING\()?3\)?[, ]`)

func youtubePlaying(out string) bool {
	// 다른 앱의 재생 상태를 YouTube 상태로 취급하지 않는다.
	for _, section := range strings.Split(out, "package=")[1:] {
		if strings.HasPrefix(section, "com.google.android.youtube\n") || strings.HasPrefix(section, "com.google.android.youtube\r") {
			if strings.Contains(section, "active=true") && youtubePlayingRE.MatchString(section) {
				return true
			}
		}
	}
	return false
}

func youtubeShortsPlaying(els []macro.UIElement) bool {
	for _, e := range els {
		if e.ResourceID != "com.google.android.youtube:id/reel_play_pause_button" {
			continue
		}
		// 일부 버전의 실제 접근성 라벨에는 '일지중지' 오자가 있다.
		switch e.ContentDesc {
		case "동영상 일시중지", "동영상 일시 중지", "동영상 일지중지", "Pause video":
			return true
		}
	}
	return false
}

func youtubeVideoCandidate(e macro.UIElement, title string, height int) bool {
	v := strings.TrimSpace(e.ContentDesc)
	return strings.HasSuffix(v, " - 동영상 재생") && !strings.HasPrefix(v, "스폰서") && !strings.HasPrefix(v, "Sponsored") && !strings.Contains(v, "Shorts 동영상") && e.Bounds[3]-e.Bounds[1] >= 180 && e.CenterY <= height-150 && (title == "" || strings.HasPrefix(strings.ToLower(v), strings.ToLower(title)))
}

func youtubeSeconds(p map[string]string, key string, fallback, max int) (time.Duration, error) {
	v := fallback
	if p[key] != "" {
		n, e := strconv.Atoi(p[key])
		if e != nil {
			return 0, fmt.Errorf("%s: 정수가 필요합니다", key)
		}
		v = n
	}
	if v < 0 || v > max {
		return 0, fmt.Errorf("%s: 0~%d 범위가 필요합니다", key, max)
	}
	return time.Duration(v) * time.Second, nil
}

type youtubeSample struct {
	State string
	Skip  *macro.UIElement
}
type youtubeSegment struct {
	State string `json:"state"`
	Start int64  `json:"startedAt"`
	End   int64  `json:"finishedAt"`
}

// 순수 감시 루프를 기기 접근과 분리해 광고 전환·취소·시간 초과를 검증한다.
func monitorYoutube(ctx context.Context, target, budget, poll time.Duration,
	sample func(context.Context) (youtubeSample, error), skip func(context.Context, *macro.UIElement) error,
	emit func(youtubeSegment)) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, target+budget)
	defer cancel()

	// 같은 상태가 이어지는 표본 구간은 하나로 합쳐서 내보낸다.
	//
	// 표본마다(약 3초) 구간을 기록하면 30분 재생에서 Behavior 구간이 수백 개가 되어
	// 타임라인·범례를 읽을 수 없다. 상태가 바뀔 때만 끊고, 어떤 경로로 끝나든
	// (성공·에러·취소) 마지막 구간은 defer 로 내보낸다.
	var pending *youtubeSegment
	flush := func() {
		if pending != nil {
			emit(*pending)
			pending = nil
		}
	}
	defer flush()
	push := func(seg youtubeSegment) {
		if pending != nil && pending.State == seg.State && pending.End == seg.Start {
			pending.End = seg.End
			return
		}
		flush()
		pending = &seg
	}

	metrics := map[string]float64{"content_seconds": 0, "ad_seconds": 0, "unknown_seconds": 0, "ad_skips": 0}
	var prev string
	var previousError string
	var last time.Time
	started := time.Now()
	for {
		if ctx.Err() != nil {
			return metrics, fmt.Errorf("YouTube 광고/재생 확인 대기 시간 초과 또는 취소: %w", ctx.Err())
		}
		s, err := sample(ctx)
		now := time.Now()
		if err != nil {
			s.State = "unknown"
			if err.Error() != previousError {
				slog.Warn("YouTube 화면 확인 실패", "error", err)
				previousError = err.Error()
			}
		} else {
			previousError = ""
		}
		if !last.IsZero() {
			// 양 끝이 모두 본영상 재생일 때만 관측 시간을 더한다. 경계 구간은 unknown.
			state := s.State
			if prev != state {
				state = "unknown"
			}
			metrics[state+"_seconds"] += now.Sub(last).Seconds()
			push(youtubeSegment{state, last.UnixMilli(), now.UnixMilli()})
		}
		prev = s.State
		last = now
		if s.State == "ad" && s.Skip != nil {
			if err := skip(ctx, s.Skip); err != nil {
				return metrics, err
			}
			metrics["ad_skips"]++
		}
		if s.State == "content" && ((target == 0 && metrics["content_seconds"] > 0) || metrics["content_seconds"] >= target.Seconds() && target > 0) {
			return metrics, nil
		}
		if time.Since(started).Seconds()-metrics["content_seconds"] > budget.Seconds() {
			return metrics, fmt.Errorf("YouTube 광고 또는 재생 미확인 대기 한도 초과")
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return metrics, ctx.Err()
		case <-timer.C:
		}
	}
}

func (o *Orchestrator) executeYoutube(ctx context.Context, job *Job, md *adb.ManagedDevice, es expandedStep, deviceID, traceID string) (string, map[string]float64, error) {
	p := es.step.Params
	action := p["action"]
	if action == "" {
		action = "watch"
	}
	var segments []youtubeSegment
	emit := func(seg youtubeSegment) {
		segments = append(segments, seg)
		if job == nil {
			return
		}
		sub := es
		label := map[string]string{"content": "YouTube 본영상 재생 관측", "ad": "YouTube 광고 관측", "feed": "YouTube 피드 탐색", "ad_card": "YouTube 광고 카드 표시", "unknown": "YouTube 재생 미확인"}[seg.State]
		sub.step = &pb.ScenarioStep{Type: "youtube", Params: map[string]string{"label": label}}
		o.recordStepBoundary(job, deviceID, sub, traceID, seg.Start, seg.End, nil)
	}
	if action == "inspect" {
		started := time.Now().UnixMilli()
		els, err := macro.DumpUIElements(ctx, md.Device, false)
		if err != nil {
			return "", nil, err
		}
		s := inspectYoutube(els)
		n := 0.0
		if s.Ad || s.Sponsored {
			n = 1
			emit(youtubeSegment{"ad", started, time.Now().UnixMilli()})
		}
		raw, _ := json.Marshal(s)
		return "YOUTUBE_INSPECT|" + string(raw), map[string]float64{"ad_visible": n}, nil
	}
	if action == "quality" {
		return setYoutubeQuality(ctx, md.Device, p["quality"])
	}
	if action == "control" {
		return youtubeControl(ctx, md.Device, p["target"])
	}
	if action == "panel" {
		return youtubePanel(ctx, md.Device, p["target"])
	}
	if action == "network_cycle" {
		duration, err := youtubeSeconds(p, "seconds", 10, 120)
		if err != nil {
			return "", nil, err
		}
		err = youtubeNetworkCycle(ctx, md.Device.Shell, duration)
		return "YOUTUBE_NETWORK_CYCLE|네트워크 끊김·복원", nil, err
	}
	if action == "select_video" {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		for attempt := 0; attempt < 8; attempt++ {
			els, err := macro.DumpUIElements(ctx, md.Device, false)
			if err != nil {
				return "", nil, err
			}
			width, height := 0, 0
			for _, e := range els {
				if e.Bounds[2] > width {
					width = e.Bounds[2]
				}
				if e.Bounds[3] > height {
					height = e.Bounds[3]
				}
			}
			for _, e := range els {
				v := e.ContentDesc
				if !youtubeVideoCandidate(e, p["title"], height) {
					continue
				}
				if _, err := md.Device.Shell(ctx, fmt.Sprintf("input tap %d %d", e.CenterX, e.CenterY)); err != nil {
					return "", nil, err
				}
				return "YOUTUBE_VIDEO_SELECTED|" + v, nil, nil
			}
			if width == 0 || height == 0 {
				return "", nil, fmt.Errorf("YouTube 목록 크기를 확인할 수 없습니다")
			}
			if _, err := md.Device.Shell(ctx, fmt.Sprintf("input swipe %d %d %d %d 400", width/2, height*3/4, width/2, height/3)); err != nil {
				return "", nil, err
			}
		}
		return "", nil, fmt.Errorf("8개 화면에서 광고·Shorts를 제외한 일반 영상을 찾지 못했습니다")
	}
	if action == "feed" {
		if o.macroMgr == nil {
			return "", nil, fmt.Errorf("macro manager not configured")
		}
		countText := p["count"]
		if countText == "" {
			countText = "1"
		}
		count, err := strconv.Atoi(countText)
		if err != nil || count < 1 || count > 100 {
			return "", nil, fmt.Errorf("feed count: 1~100 필요")
		}
		direction := p["direction"]
		if direction == "" {
			direction = "down"
		}
		if direction != "up" && direction != "down" {
			return "", nil, fmt.Errorf("feed direction: up/down 필요")
		}
		ads := 0.0
		for i := 0; i < count; i++ {
			start := time.Now().UnixMilli()
			els, err := macro.DumpUIElements(ctx, md.Device, false)
			if err != nil {
				return "", nil, err
			}
			s := inspectYoutube(els)
			state := "feed"
			if s.Ad || s.Sponsored {
				ads++
				state = "ad_card"
			}
			resp, err := o.macroMgr.ReplayMacro(ctx, &pb.ReplayMacroRequest{DeviceId: deviceID, Events: []*pb.MacroEvent{{Type: "scroll", Direction: direction, MaxScrolls: 1, ScrollPause: 1, Duration: 400}}})
			if err != nil {
				return "", nil, err
			}
			if !resp.Success {
				return "", nil, fmt.Errorf("feed scroll failed: %s", resp.Message)
			}
			emit(youtubeSegment{state, start, time.Now().UnixMilli()})
		}
		raw, _ := json.Marshal(segments)
		return "YOUTUBE_FEED|" + string(raw), map[string]float64{"ad_card_observations": ads}, nil
	}
	if action != "watch" {
		return "", nil, fmt.Errorf("unknown youtube action %q", action)
	}
	target, err := youtubeSeconds(p, "seconds", 60, 3600)
	if err != nil {
		return "", nil, err
	}
	budget, err := youtubeSeconds(p, "ad_timeout", 180, 900)
	if err != nil || budget == 0 {
		return "", nil, fmt.Errorf("ad_timeout: 1~900초 필요")
	}
	ocrSamples := 0.0
	sample := func(ctx context.Context) (youtubeSample, error) {
		probe, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		// 일반 영상은 뷰 트리(dumpsys activity top)로 먼저 판정한다. 재생 중 uiautomator 는
		// idle 을 못 잡아 12초를 기다린 뒤 실패해 표본 간격이 ~17초로 벌어졌다.
		// 판정할 수 없으면(플레이어 없음·백그라운드·Shorts) 아래 기존 경로로 간다.
		if p["surface"] != "shorts" {
			if top, err := md.Device.Shell(probe, "dumpsys activity top"); err == nil {
				if views, ok := parseYoutubeViewTree(top); ok {
					switch state, skipBtn := youtubeTreeState(views); state {
					case "ad":
						return youtubeSample{"ad", skipBtn}, nil
					case "player":
						media, err := md.Device.Shell(probe, "dumpsys media_session")
						if err != nil {
							return youtubeSample{}, err
						}
						if youtubePlaying(media) {
							return youtubeSample{"content", nil}, nil
						}
						return youtubeSample{"unknown", nil}, nil
					}
				}
			}
		}
		dumpCtx, dumpCancel := context.WithTimeout(probe, 12*time.Second)
		els, err := macro.DumpUIElements(dumpCtx, md.Device, false)
		dumpCancel()
		if err != nil {
			// 움직이는 화면은 UiAutomation idle 검사를 통과하지 못할 수 있다.
			// OCR과 전면 앱/미디어 상태를 함께 확인한다. OCR 자체가 실패하면 미확인이다.
			words, ocrErr := youtubeOCRScreen(probe, md.Device, 0)
			if ocrErr == nil && len(words) > 0 {
				ocrSamples++
				s := inspectYoutube(words)
				if p["surface"] == "shorts" && s.Sponsored {
					if err := swipeYoutubeShorts(probe, md.Device); err != nil {
						return youtubeSample{}, err
					}
					return youtubeSample{"ad", nil}, nil
				}
				if s.Ad {
					return youtubeSample{"ad", s.Skip}, nil
				}
				focus, focusErr := md.Device.Shell(probe, "dumpsys window | grep mCurrentFocus")
				media, mediaErr := md.Device.Shell(probe, "dumpsys media_session")
				if focusErr == nil && mediaErr == nil && strings.Contains(focus, "com.google.android.youtube/") && youtubePlaying(media) {
					return youtubeSample{"content", nil}, nil
				}
			}
			return youtubeSample{}, err
		}
		s := inspectYoutube(els)
		if s.Ad {
			if s.Shorts && s.Skip == nil {
				// Shorts 광고 카드는 다음 카드로 넘긴다. 외부 링크는 누르지 않는다.
				for _, e := range els {
					if e.ResourceID == "com.google.android.youtube:id/reel_play_pause_button" {
						b := e.Bounds
						w, h := b[2]-b[0], b[3]-b[1]
						if w > 0 && h > 0 {
							_, err := md.Device.Shell(probe, fmt.Sprintf("input swipe %d %d %d %d 400", e.CenterX, b[1]+h*3/4, e.CenterX, b[1]+h/4))
							if err != nil {
								return youtubeSample{}, err
							}
						}
						break
					}
				}
			}
			return youtubeSample{"ad", s.Skip}, nil
		}
		if !s.Player {
			return youtubeSample{"unknown", nil}, nil
		}
		for _, e := range els {
			if strings.HasSuffix(e.ResourceID, "play_pause_replay_button") || strings.HasSuffix(e.ResourceID, "reel_play_pause_button") {
				if e.ContentDesc == "동영상 재생" || e.ContentDesc == "Play video" || e.ContentDesc == "다시 재생" {
					return youtubeSample{"unknown", nil}, nil
				}
			}
		}
		// Shorts는 재생 중에도 미디어 세션이 STOPPED인 버전이 있다.
		// 광고 검사를 통과했고 화면에 일시중지 동작이 제공될 때 재생으로 관측한다.
		if s.Shorts && youtubeShortsPlaying(els) {
			return youtubeSample{"content", nil}, nil
		}
		media, err := md.Device.Shell(probe, "dumpsys media_session")
		if err != nil {
			return youtubeSample{}, err
		}
		if youtubePlaying(media) {
			return youtubeSample{"content", nil}, nil
		}
		return youtubeSample{"unknown", nil}, nil
	}
	skip := func(ctx context.Context, e *macro.UIElement) error {
		_, err := md.Device.Shell(ctx, fmt.Sprintf("input tap %d %d", e.CenterX, e.CenterY))
		return err
	}
	metrics, err := monitorYoutube(ctx, target, budget, 3*time.Second, sample, skip, emit)
	metrics["ocr_samples"] = ocrSamples
	raw, _ := json.Marshal(segments)
	return "YOUTUBE_SEGMENTS|" + string(raw), metrics, err
}

func swipeYoutubeShorts(ctx context.Context, dev *adb.Device) error {
	out, err := dev.Shell(ctx, "wm size")
	if err != nil {
		return err
	}
	matches := regexp.MustCompile(`(\d+)x(\d+)`).FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		return fmt.Errorf("Shorts 광고 스크롤: 화면 크기 없음")
	}
	m := matches[len(matches)-1]
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	_, err = dev.Shell(ctx, fmt.Sprintf("input swipe %d %d %d %d 400", w/2, h*3/4, w/2, h/4))
	return err
}

// 관측된 메뉴 라벨을 통해 선택한다. 지원하지 않는 화질은 자동으로 낮추지 않는다.
func youtubeQualityLabel(label, quality string) bool {
	// 60fps 영상은 메뉴에 720p60 / 1080p60으로 표시된다.
	return label == quality || regexp.MustCompile(`^`+regexp.QuoteMeta(quality)+`(?:24|25|30|48|50|60|120)$`).MatchString(label)
}

func setYoutubeQuality(ctx context.Context, dev *adb.Device, quality string) (string, map[string]float64, error) {
	if quality != "360p" && quality != "720p" && quality != "1080p" {
		return "", nil, fmt.Errorf("지원 화질: 360p,720p,1080p")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if _, err := dev.Shell(ctx, "cmd media_session dispatch pause"); err != nil {
		return "", nil, err
	}
	tap := func(match func(macro.UIElement) bool) error {
		els, err := macro.DumpUIElements(ctx, dev, false)
		if err != nil {
			return err
		}
		for _, e := range els {
			if match(e) {
				_, err = dev.Shell(ctx, fmt.Sprintf("input tap %d %d", e.CenterX, e.CenterY))
				return err
			}
		}
		ocr, ocrErr := youtubeOCRMenu(ctx, dev, els)
		if ocrErr == nil {
			for _, e := range ocr {
				if match(e) {
					_, err = dev.Shell(ctx, fmt.Sprintf("input tap %d %d", e.CenterX, e.CenterY))
					return err
				}
			}
		}
		return fmt.Errorf("YouTube 화질 메뉴 요소를 찾지 못했습니다 (요청 %s)", quality)
	}
	// 컨트롤이 사라지는 문제를 피하려고 재생 버튼을 통해 일시정지한다.
	els, err := macro.DumpUIElements(ctx, dev, false)
	if err != nil {
		return "", nil, err
	}
	if inspectYoutube(els).Ad {
		return "", nil, fmt.Errorf("광고 중에는 화질을 변경하지 않습니다. watch 준비 단계를 먼저 실행하세요")
	}
	var player, control *macro.UIElement
	for i := range els {
		e := &els[i]
		if e.ResourceID == "com.google.android.youtube:id/watch_player" {
			player = e
		}
		if strings.HasSuffix(e.ResourceID, "player_control_play_pause_replay_button") {
			control = e
		}
	}
	if player == nil {
		return "", nil, fmt.Errorf("YouTube 일반 영상 플레이어가 없습니다")
	}
	click := func(x, y int) error { _, e := dev.Shell(ctx, fmt.Sprintf("input tap %d %d", x, y)); return e }
	if control == nil {
		// 미디어 세션에 정지를 요청한 뒤 숨겨진 컨트롤만 표시한다.
		if err = click(player.Bounds[0]+(player.Bounds[2]-player.Bounds[0])/3, player.CenterY); err != nil {
			return "", nil, err
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	} else if control.ContentDesc != "동영상 재생" && control.ContentDesc != "Play video" {
		if err = click(control.CenterX, control.CenterY); err != nil {
			return "", nil, err
		}
	}
	for menuIndex, match := range []func(macro.UIElement) bool{
		func(e macro.UIElement) bool {
			return e.ContentDesc == "설정" || e.ContentDesc == "Settings" || e.ResourceID == "com.google.android.youtube:id/player_overflow_button"
		},
		func(e macro.UIElement) bool {
			return e.Text == "화질" || strings.HasPrefix(e.ContentDesc, "화질") || e.Text == "Quality" || strings.HasPrefix(e.ContentDesc, "Quality")
		},
		func(e macro.UIElement) bool {
			return e.Text == "고급" || e.ContentDesc == "고급" || e.Text == "Advanced" || e.ContentDesc == "Advanced" || e.Text == "구체적인"
		},
		func(e macro.UIElement) bool {
			return youtubeQualityLabel(e.Text, quality) || youtubeQualityLabel(e.ContentDesc, quality)
		},
	} {
		if err := tap(match); err != nil {
			return "", nil, fmt.Errorf("화질 메뉴 단계 %d: %w", menuIndex+1, err)
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	// 메뉴를 다시 열어 요청 화질이 적용됐는지 확인한다. OCR 오인식도 여기서 실패시킨다.
	if err := tap(func(e macro.UIElement) bool {
		return e.ResourceID == "com.google.android.youtube:id/player_overflow_button"
	}); err != nil {
		return "", nil, err
	}
	check, err := macro.DumpUIElements(ctx, dev, false)
	if err != nil {
		return "", nil, err
	}
	verified := false
	for _, e := range check {
		v := e.Text + e.ContentDesc
		if (strings.HasPrefix(v, "화질") || strings.HasPrefix(v, "Quality")) && strings.Contains(v, quality) && !strings.Contains(v, "자동") && !strings.Contains(v, "Auto") {
			verified = true
		}
	}
	if !verified {
		return "", nil, fmt.Errorf("요청 화질 %s 적용을 확인하지 못했습니다", quality)
	}
	if _, err = dev.Shell(ctx, "input keyevent 4"); err != nil {
		return "", nil, err
	}
	if err = tap(func(e macro.UIElement) bool {
		return strings.HasSuffix(e.ResourceID, "player_control_play_pause_replay_button") && (e.ContentDesc == "동영상 재생" || e.ContentDesc == "Play video")
	}); err != nil {
		return "", nil, err
	}
	return "YOUTUBE_QUALITY_VERIFIED|" + quality, nil, nil
}
