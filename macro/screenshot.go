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

const (
	screenshotRemotePath = "/sdcard/macro_screenshot.png"
)

// CaptureScreenshot takes a screenshot from the device via ADB.
func CaptureScreenshot(ctx context.Context, dev *adb.Device) (*pb.TakeScreenshotResponse, error) {
	// Take screenshot on device
	_, err := dev.Shell(ctx, "screencap -p "+screenshotRemotePath)
	if err != nil {
		return &pb.TakeScreenshotResponse{Success: false}, fmt.Errorf("screencap: %w", err)
	}

	// Pull to temp file
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("macro_screenshot_%s_%d.png", dev.Serial, time.Now().UnixMilli()))
	defer os.Remove(tmpFile)

	if err := dev.Pull(ctx, screenshotRemotePath, tmpFile); err != nil {
		return &pb.TakeScreenshotResponse{Success: false}, fmt.Errorf("pull screenshot: %w", err)
	}

	// Read file
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return &pb.TakeScreenshotResponse{Success: false}, fmt.Errorf("read screenshot: %w", err)
	}

	// Get dimensions
	width, height := 0, 0
	img, err := png.Decode(bytes.NewReader(data))
	if err == nil {
		bounds := img.Bounds()
		width = bounds.Dx()
		height = bounds.Dy()
	}

	// Clean up remote file
	dev.Shell(ctx, "rm -f "+screenshotRemotePath)

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
