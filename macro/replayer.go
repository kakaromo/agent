package macro

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agent/adb"
	pb "agent/pb"
)

// Replayer replays macro events on a device using ADB input commands.
type Replayer struct {
	dev          *adb.Device
	sourceWidth  int
	sourceHeight int
}

func NewReplayer(dev *adb.Device, sourceWidth, sourceHeight int) *Replayer {
	if sourceWidth <= 0 {
		sourceWidth = 1080
	}
	if sourceHeight <= 0 {
		sourceHeight = 2400
	}
	return &Replayer{dev: dev, sourceWidth: sourceWidth, sourceHeight: sourceHeight}
}

// Replay executes all events in sequence, respecting timing.
func (r *Replayer) Replay(ctx context.Context, events []*pb.MacroEvent) (*pb.ReplayMacroResponse, error) {
	// Get target device resolution for coordinate scaling
	targetWidth, targetHeight := getDeviceResolution(ctx, r.dev.Serial)
	scaleX := float64(targetWidth) / float64(r.sourceWidth)
	scaleY := float64(targetHeight) / float64(r.sourceHeight)

	ocrResults := make(map[string]string)
	metrics := make(map[string]float64)

	var lastT int64

	for i, ev := range events {
		select {
		case <-ctx.Done():
			return &pb.ReplayMacroResponse{
				Success:    false,
				Message:    "cancelled",
				OcrResults: ocrResults,
				Metrics:    metrics,
			}, nil
		default:
		}

		// Wait for timing gap
		if ev.T > lastT && i > 0 {
			delay := time.Duration(ev.T-lastT) * time.Millisecond
			if delay > 0 && delay < 30*time.Minute {
				time.Sleep(delay)
			}
		}
		lastT = ev.T

		slog.Debug("replay event", "index", i, "type", ev.Type, "t", ev.T)

		switch ev.Type {
		case "tap":
			x := int(float64(ev.X) * scaleX)
			y := int(float64(ev.Y) * scaleY)
			r.dev.Shell(ctx, fmt.Sprintf("input tap %d %d", x, y))

		case "swipe":
			x1 := int(float64(ev.X) * scaleX)
			y1 := int(float64(ev.Y) * scaleY)
			x2 := int(float64(ev.X2) * scaleX)
			y2 := int(float64(ev.Y2) * scaleY)
			dur := ev.Duration
			if dur <= 0 {
				dur = 300
			}
			r.dev.Shell(ctx, fmt.Sprintf("input swipe %d %d %d %d %d", x1, y1, x2, y2, dur))

		case "key":
			r.dev.Shell(ctx, fmt.Sprintf("input keyevent %d", ev.Keycode))

		case "wait":
			sec := ev.Seconds
			if sec <= 0 {
				sec = 1
			}
			time.Sleep(time.Duration(sec) * time.Second)

		case "wait_until":
			if err := r.waitUntil(ctx, ev); err != nil {
				slog.Warn("wait_until failed", "error", err)
			}

		case "screenshot":
			name := ev.Name
			if name == "" {
				name = fmt.Sprintf("screenshot_%d", i)
			}
			// uiautomator dump 기반 텍스트 수집 (Tesseract 불필요)
			texts, err := r.dumpUITexts(ctx)
			if err != nil {
				slog.Warn("screenshot ui dump failed", "error", err)
				continue
			}
			fullText := strings.Join(texts, "\n")
			ocrResults[name] = fullText

			// 패턴 매칭으로 값 추출
			if ev.OcrPattern != "" {
				re, err := regexp.Compile(ev.OcrPattern)
				if err == nil {
					match := re.FindStringSubmatch(fullText)
					if len(match) > 1 {
						if v, err := strconv.ParseFloat(match[1], 64); err == nil {
							metrics[name] = v
						}
					} else if len(match) > 0 {
						if v, err := strconv.ParseFloat(match[0], 64); err == nil {
							metrics[name] = v
						}
					}
				}
			}

			// 항목명 → 숫자 쌍도 자동 추출
			for j := 0; j < len(texts)-1; j++ {
				if val, err := strconv.ParseFloat(texts[j+1], 64); err == nil && !isNumericString(texts[j]) && len(texts[j]) > 1 {
					metrics[texts[j]] = val
				}
			}

		case "scroll_capture":
			scrollOcrResults := r.scrollCapture(ctx, ev)
			for k, v := range scrollOcrResults {
				ocrResults[k] = v
				// 숫자값은 metrics에도 추가 (fio 방식과 동일)
				if k != "full_text" {
					if val, err := strconv.ParseFloat(v, 64); err == nil {
						metrics[k] = val
					} else {
						// "4269.8MB/s" → 4269.8 파싱
						metrics[k] = parseSpeedValue(v)
					}
				}
			}

		default:
			slog.Warn("unknown event type", "type", ev.Type)
		}
	}

	return &pb.ReplayMacroResponse{
		Success:    true,
		Message:    fmt.Sprintf("replayed %d events", len(events)),
		OcrResults: ocrResults,
		Metrics:    metrics,
	}, nil
}

// waitUntil polls for a condition until timeout.
// ui_text/activity: 텍스트가 이미 있으면 사라질 때까지 기다린 후, 다시 나타날 때까지 대기
func (r *Replayer) waitUntil(ctx context.Context, ev *pb.MacroEvent) error {
	timeout := time.Duration(ev.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	interval := time.Duration(ev.PollInterval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}

	deadline := time.Now().Add(timeout)
	pattern := ev.WaitPattern
	method := ev.WaitMethod

	// Phase 1: 텍스트가 이미 있으면 사라질 때까지 대기 (최대 30초)
	if method == "ui_text" || method == "activity" {
		alreadyPresent := false
		if method == "ui_text" {
			found, err := getDeviceUIText(ctx, r.dev, pattern)
			alreadyPresent = err == nil && found
		} else {
			focus, err := getDeviceActivityFocus(ctx, r.dev)
			alreadyPresent = err == nil && pattern != "" && strings.Contains(focus, pattern)
		}

		if alreadyPresent {
			slog.Info("wait_until: pattern already present, waiting for it to disappear first", "pattern", pattern)
			disappearDeadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(disappearDeadline) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				time.Sleep(2 * time.Second)

				stillPresent := false
				if method == "ui_text" {
					found, err := getDeviceUIText(ctx, r.dev, pattern)
					stillPresent = err == nil && found
				} else {
					focus, err := getDeviceActivityFocus(ctx, r.dev)
					stillPresent = err == nil && pattern != "" && strings.Contains(focus, pattern)
				}
				if !stillPresent {
					slog.Info("wait_until: pattern disappeared, now waiting for reappearance", "pattern", pattern)
					break
				}
			}
		}
	}

	// Phase 2: 텍스트가 나타날 때까지 대기
	// For screen_stable: track previous screenshot hash
	var prevScreenHash string

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("wait_until timeout (%s)", timeout)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		switch method {
		case "activity":
			focus, err := getDeviceActivityFocus(ctx, r.dev)
			if err == nil && pattern != "" && strings.Contains(focus, pattern) {
				slog.Info("wait_until: activity matched", "pattern", pattern, "focus", focus)
				return nil
			}

		case "ui_text":
			found, err := getDeviceUIText(ctx, r.dev, pattern)
			if err == nil && found {
				slog.Info("wait_until: UI text matched", "pattern", pattern)
				return nil
			}

		case "screen_stable":
			// Take screenshot and compute simple hash
			resp, err := CaptureScreenshot(ctx, r.dev)
			if err == nil {
				hash := fmt.Sprintf("%d", len(resp.ImageData))
				if prevScreenHash != "" && hash == prevScreenHash {
					slog.Info("wait_until: screen stable")
					return nil
				}
				prevScreenHash = hash
			}

		default:
			// Default: treat as activity
			focus, err := getDeviceActivityFocus(ctx, r.dev)
			if err == nil && pattern != "" && strings.Contains(focus, pattern) {
				return nil
			}
		}

		time.Sleep(interval)
	}
}

// dumpUITexts runs uiautomator dump and extracts non-empty text values.
func (r *Replayer) dumpUITexts(ctx context.Context) ([]string, error) {
	_, err := r.dev.Shell(ctx, "uiautomator dump /sdcard/ui.xml")
	if err != nil {
		return nil, fmt.Errorf("uiautomator dump: %w", err)
	}
	out, err := r.dev.Shell(ctx, "cat /sdcard/ui.xml")
	if err != nil {
		return nil, fmt.Errorf("cat ui.xml: %w", err)
	}

	// Extract text="..." values
	re := regexp.MustCompile(`text="([^"]+)"`)
	matches := re.FindAllStringSubmatch(out, -1)
	var texts []string
	for _, m := range matches {
		if len(m) > 1 && strings.TrimSpace(m[1]) != "" {
			texts = append(texts, m[1])
		}
	}
	return texts, nil
}

// scrollCapture performs scroll + uiautomator dump capture repeatedly.
func (r *Replayer) scrollCapture(ctx context.Context, ev *pb.MacroEvent) map[string]string {
	results := make(map[string]string)
	maxScrolls := int(ev.MaxScrolls)
	if maxScrolls <= 0 {
		maxScrolls = 10
	}
	scrollPause := time.Duration(ev.ScrollPause) * time.Second
	if scrollPause <= 0 {
		scrollPause = 1 * time.Second
	}

	var allText strings.Builder
	var prevTexts string
	seenMetrics := make(map[string]bool)

	for i := 0; i < maxScrolls; i++ {
		select {
		case <-ctx.Done():
			break
		default:
		}

		// Dump UI hierarchy (정확도 100%, OCR 불필요)
		texts, err := r.dumpUITexts(ctx)
		if err != nil {
			slog.Warn("scroll_capture dump failed", "scroll", i, "error", err)
			continue
		}

		currentTexts := strings.Join(texts, "|")
		if currentTexts == prevTexts {
			slog.Info("scroll_capture: no new content, end reached", "scroll", i)
			break
		}
		prevTexts = currentTexts

		// 항목명 → 점수 파싱
		// UI dump 순서: [항목명] [점수] [Speed/Read Speed/Write Speed] [속도값] ...
		var lastCategory string
		for j := 0; j < len(texts)-1; j++ {
			name := texts[j]
			value := texts[j+1]

			// 순수 숫자(점수)가 다음에 오면 현재가 항목명
			if _, err := strconv.Atoi(value); err == nil && !isNumericString(name) && len(name) > 1 {
				key := normalizeMetricKey(name) + "_score"
				if !seenMetrics[key] {
					results[key] = value
					seenMetrics[key] = true
					allText.WriteString(fmt.Sprintf("%s: %s\n", key, value))
				}
				lastCategory = name
			}

			// Speed 계열 → 카테고리 + speed 라벨
			if lastCategory != "" && (name == "Speed" || name == "Read Speed" || name == "Write Speed") {
				speedLabel := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
				key := normalizeMetricKey(lastCategory) + "_" + speedLabel + "_mbs"
				if !seenMetrics[key] {
					results[key] = value
					seenMetrics[key] = true
					allText.WriteString(fmt.Sprintf("%s: %s\n", key, value))
				}
			}
		}

		// Scroll
		targetWidth, targetHeight := getDeviceResolution(ctx, r.dev.Serial)
		centerX := targetWidth / 2
		startY := targetHeight * 3 / 4
		endY := targetHeight / 4
		if ev.Direction == "up" {
			startY, endY = endY, startY
		}
		r.dev.Shell(ctx, fmt.Sprintf("input swipe %d %d %d %d 300", centerX, startY, centerX, endY))
		time.Sleep(scrollPause)
	}

	results["full_text"] = allText.String()

	// Extract all numeric matches from full text
	if ev.OcrPattern != "" {
		re, err := regexp.Compile(ev.OcrPattern)
		if err == nil {
			matches := re.FindAllString(allText.String(), -1)
			for j, m := range matches {
				results[fmt.Sprintf("match_%d", j)] = m
			}
		}
	}

	return results
}

// isNumericString checks if a string is purely numeric.
func isNumericString(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// normalizeMetricKey converts display name to snake_case key.
func normalizeMetricKey(name string) string {
	// 긴 매칭 우선 (순서 중요)
	keyMap := []struct{ from, to string }{
		{"Mixed Multi-Random Access", "mixed_multi_random"},
		{"Mixed Random Access", "mixed_random"},
		{"Sequence Read", "seq_read"},
		{"Sequence Write", "seq_write"},
		{"Random Access", "random_access"},
		{"Multi-AI Read", "multi_ai_read"},
		{"Multi-AI Write", "multi_ai_write"},
		{"AI Read", "ai_read"},
		{"AI Write", "ai_write"},
	}
	for _, km := range keyMap {
		if name == km.from {
			return km.to
		}
	}
	// 변환 안 된 경우 소문자 + 공백→언더스코어
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "_"))
}

// parseSpeedValue extracts numeric value from speed strings like "4269.8MB/s", "103.8MB/s".
func parseSpeedValue(s string) float64 {
	re := regexp.MustCompile(`([\d.]+)`)
	match := re.FindString(s)
	if match != "" {
		v, err := strconv.ParseFloat(match, 64)
		if err == nil {
			return v
		}
	}
	return 0
}
