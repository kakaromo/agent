package benchmark

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"agent/adb"
	"agent/macro"
)

// YouTube의 NAF 메뉴는 텍스트 접근성을 제공하지 않아 스크린샷 OCR로 읽는다.
// 메뉴 핸들 아래만 후보로 삼아 배경 영상의 글자를 누르지 않는다.
func youtubeOCRMenu(ctx context.Context, dev *adb.Device, elements []macro.UIElement) ([]macro.UIElement, error) {
	top := -1
	for _, e := range elements {
		if e.ContentDesc == "드래그 핸들" || e.ContentDesc == "Drag handle" {
			top = e.Bounds[3]
			break
		}
	}
	if top < 0 {
		return nil, fmt.Errorf("화질 메뉴 패널을 확인하지 못했습니다")
	}
	words, err := youtubeOCRScreen(ctx, dev, top)
	if err != nil {
		return nil, err
	}
	filtered := words[:0]
	for _, w := range words {
		// 메뉴 제목의 현재 화질 값은 선택 항목이 아니다.
		if (youtubeQualityLabel(w.Text, "360p") || youtubeQualityLabel(w.Text, "720p") || youtubeQualityLabel(w.Text, "1080p")) && w.CenterY < top+168 {
			continue
		}
		filtered = append(filtered, w)
	}
	return filtered, nil
}

func youtubeOCRScreen(ctx context.Context, dev *adb.Device, top int) ([]macro.UIElement, error) {
	exe, err := exec.LookPath("tesseract")
	if err != nil {
		candidate := filepath.Join(os.Getenv("ProgramFiles"), "Tesseract-OCR", "tesseract.exe")
		if _, e := os.Stat(candidate); e != nil {
			return nil, fmt.Errorf("화질 메뉴 OCR에 Tesseract가 필요합니다")
		}
		exe = candidate
	}
	shot, err := macro.CaptureScreenshot(ctx, dev)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", "youtube-menu-*.png")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	defer os.Remove(path)
	img, decodeErr := png.Decode(bytes.NewReader(shot.ImageData))
	if decodeErr != nil {
		f.Close()
		return nil, decodeErr
	}
	if top >= img.Bounds().Dy() {
		f.Close()
		return nil, fmt.Errorf("잘못된 메뉴 경계")
	}
	cropped := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()-top))
	draw.Draw(cropped, cropped.Bounds(), img, image.Pt(0, top), draw.Src)
	if err = png.Encode(f, cropped); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, exe, path, "stdout", "-l", "kor+eng", "--psm", "6", "tsv").Output()
	if err != nil {
		return nil, fmt.Errorf("화질 OCR: %w", err)
	}
	result := parseYoutubeOCR(string(out), 0)
	// 해상도 숫자 뒤 p를 한글 모델이 0으로 읽을 수 있어 영문 모델도 사용한다.
	english, err := exec.CommandContext(ctx, exe, path, "stdout", "-l", "eng", "--psm", "11", "tsv").Output()
	if err == nil {
		result = append(result, parseYoutubeOCR(string(english), 0)...)
	}
	for i := range result {
		result[i].CenterY += top
		result[i].Bounds[1] += top
		result[i].Bounds[3] += top
	}
	return result, nil
}

func parseYoutubeOCR(tsv string, top int) []macro.UIElement {
	var elements []macro.UIElement
	for _, line := range strings.Split(tsv, "\n") {
		cols := strings.SplitN(strings.TrimSuffix(line, "\r"), "\t", 12)
		if len(cols) != 12 || cols[0] != "5" {
			continue
		}
		confidence, _ := strconv.ParseFloat(cols[10], 64)
		if confidence < 60 {
			continue
		}
		x, e1 := strconv.Atoi(cols[6])
		y, e2 := strconv.Atoi(cols[7])
		w, e3 := strconv.Atoi(cols[8])
		h, e4 := strconv.Atoi(cols[9])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || y < top || w <= 0 || h <= 0 {
			continue
		}
		elements = append(elements, macro.UIElement{Text: strings.TrimSpace(cols[11]), CenterX: x + w/2, CenterY: y + h/2, Bounds: [4]int{x, y, x + w, y + h}})
	}
	return elements
}
