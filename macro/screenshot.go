package macro

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agent/adb"
	pb "agent/pb"
)

// pngSignature — PNG 파일의 첫 8바이트. screencap 실패 시 에러 문구가 오는 것을 거른다.
var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// CaptureScreenshot takes a screenshot from the device via ADB.
//
// `exec-out screencap -p` 로 PNG 를 stdout 으로 바로 받는다. 예전엔
// /sdcard/macro_screenshot.png 에 쓰고 pull 했는데, 측정 중(YouTube OCR 등)에는
// 그 write 가 trace 에 섞이고, 중간에 실패하면 파일이 기기에 남았다.
func CaptureScreenshot(ctx context.Context, dev *adb.Device) (*pb.TakeScreenshotResponse, error) {
	data, err := dev.ExecOut(ctx, "screencap -p")
	if err != nil {
		return &pb.TakeScreenshotResponse{Success: false}, fmt.Errorf("screencap: %w", err)
	}
	if !bytes.HasPrefix(data, pngSignature) {
		return &pb.TakeScreenshotResponse{Success: false},
			fmt.Errorf("screencap: PNG 가 아니다: %.80q", data)
	}

	// Get dimensions
	width, height := 0, 0
	img, err := png.Decode(bytes.NewReader(data))
	if err == nil {
		bounds := img.Bounds()
		width = bounds.Dx()
		height = bounds.Dy()
	}

	return &pb.TakeScreenshotResponse{
		Success:   true,
		ImageData: data,
		Width:     int32(width),
		Height:    int32(height),
	}, nil
}

// RunScreenshotOcr captures a screenshot, optionally crops a region, and runs Tesseract OCR.
func RunScreenshotOcr(ctx context.Context, dev *adb.Device, region *pb.OcrRegion, extractPattern string) (*pb.ScreenshotOcrResponse, error) {
	// Take screenshot
	resp, err := CaptureScreenshot(ctx, dev)
	if err != nil || !resp.Success {
		return &pb.ScreenshotOcrResponse{Success: false}, err
	}

	imageData := resp.ImageData

	// Crop region if specified
	if region != nil && region.Width > 0 && region.Height > 0 {
		cropped, err := cropPNG(imageData, int(region.X), int(region.Y), int(region.Width), int(region.Height))
		if err == nil {
			imageData = cropped
		} else {
			slog.Warn("crop failed, using full image", "error", err)
		}
	}

	// Run OCR
	fullText, err := runTesseract(ctx, imageData)
	if err != nil {
		return &pb.ScreenshotOcrResponse{
			Success:   false,
			ImageData: resp.ImageData,
		}, fmt.Errorf("tesseract: %w", err)
	}

	// Extract value with pattern
	var extractedValue string
	if extractPattern != "" {
		re, err := regexp.Compile(extractPattern)
		if err == nil {
			match := re.FindStringSubmatch(fullText)
			if len(match) > 1 {
				extractedValue = match[1] // first capture group
			} else if len(match) > 0 {
				extractedValue = match[0]
			}
		}
	}

	return &pb.ScreenshotOcrResponse{
		Success:        true,
		FullText:       fullText,
		ExtractedValue: extractedValue,
		ImageData:      resp.ImageData,
	}, nil
}

// runTesseract executes tesseract on the given PNG image data.
func runTesseract(ctx context.Context, imageData []byte) (string, error) {
	if !tesseractAvailable() {
		return "", fmt.Errorf("tesseract not installed (brew install tesseract)")
	}

	// Write image to temp file
	tmpIn := filepath.Join(os.TempDir(), fmt.Sprintf("ocr_in_%d.png", time.Now().UnixNano()))
	tmpOut := filepath.Join(os.TempDir(), fmt.Sprintf("ocr_out_%d", time.Now().UnixNano()))
	defer os.Remove(tmpIn)
	defer os.Remove(tmpOut + ".txt")

	if err := os.WriteFile(tmpIn, imageData, 0644); err != nil {
		return "", fmt.Errorf("write temp image: %w", err)
	}

	// Run tesseract
	cmd := exec.CommandContext(ctx, "tesseract", tmpIn, tmpOut, "-l", "eng+kor", "--psm", "6")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Try without Korean if kor traineddata not available
		cmd2 := exec.CommandContext(ctx, "tesseract", tmpIn, tmpOut, "-l", "eng", "--psm", "6")
		if err2 := cmd2.Run(); err2 != nil {
			return "", fmt.Errorf("tesseract failed: %w (%s)", err2, stderr.String())
		}
	}

	// Read output
	data, err := os.ReadFile(tmpOut + ".txt")
	if err != nil {
		return "", fmt.Errorf("read OCR output: %w", err)
	}

	return strings.TrimSpace(string(data)), nil
}

// cropPNG crops a PNG image to the specified region.
func cropPNG(pngData []byte, x, y, w, h int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}

	bounds := img.Bounds()
	// Clamp region to image bounds
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x+w > bounds.Dx() {
		w = bounds.Dx() - x
	}
	if y+h > bounds.Dy() {
		h = bounds.Dy() - y
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid crop region")
	}

	cropped := image.NewRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			cropped.Set(dx, dy, img.At(x+dx, y+dy))
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, cropped); err != nil {
		return nil, fmt.Errorf("encode cropped: %w", err)
	}
	return buf.Bytes(), nil
}
