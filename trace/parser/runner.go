package parser

import (
	"bufio"
	"fmt"
	"os"
)

// ProgressFunc — 진행 라인 콜백. tracer.go 의 SubscribeJobProgress 로 forward 된다.
type ProgressFunc func(line string)

// RunParquetOnly — trace.log 를 한 번에 파싱해 outputDir 에 result_<type>.parquet 을 만든다.
// traceType 은 "ufs", "block", "ufscustom", "both" 중 하나. "both" 는 UFS + Block 동시 수집.
//
// Rust `--parquet-only` 의 Go 대체. 실시간 윈도우링 없이 단일 패스로 처리하므로 정합성
// 검증이 단순하다 (같은 trace.log 에 Rust 결과 vs Go 결과 비교).
//
// progressFn 은 nil 이면 무시된다.
func RunParquetOnly(logFile, outputDir, traceType string, progressFn ProgressFunc) error {
	if traceType == "" {
		return fmt.Errorf("traceType is required")
	}
	wantUFS := traceType == "ufs" || traceType == "both"
	wantBlock := traceType == "block" || traceType == "both"
	wantUFSCustom := traceType == "ufscustom"

	if !wantUFS && !wantBlock && !wantUFSCustom {
		return fmt.Errorf("unknown traceType: %s", traceType)
	}

	f, err := os.Open(logFile)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer f.Close()

	report := func(line string) {
		if progressFn != nil {
			progressFn(line)
		}
	}
	report(fmt.Sprintf("parsing %s for trace_type=%s", logFile, traceType))

	scanner := bufio.NewScanner(f)
	// trace_pipe 라인이 비교적 길 수 있어 buffer 1MB 까지 허용
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	var ufsEvents []UFSEvent
	var blockEvents []BlockEvent
	var ufsCustomEvents []UFSCustomEvent

	var lineNo uint64
	const reportEvery = 100_000
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if line == "" {
			continue
		}

		if wantUFS {
			if ev, ok := parseUFSLine(line); ok {
				ev.LineNumber = lineNo
				ufsEvents = append(ufsEvents, ev)
				continue
			}
		}
		if wantBlock {
			if ev, ok := parseBlockLine(line); ok {
				ev.LineNumber = lineNo
				blockEvents = append(blockEvents, ev)
				continue
			}
		}
		if wantUFSCustom {
			if ev, ok := parseUFSCustomLine(line); ok {
				ev.LineNumber = lineNo
				ufsCustomEvents = append(ufsCustomEvents, ev)
				continue
			}
		}

		if lineNo%reportEvery == 0 {
			report(fmt.Sprintf("scanned %d lines (ufs=%d block=%d ufscustom=%d)",
				lineNo, len(ufsEvents), len(blockEvents), len(ufsCustomEvents)))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	report(fmt.Sprintf("scan done: %d lines, ufs=%d block=%d ufscustom=%d",
		lineNo, len(ufsEvents), len(blockEvents), len(ufsCustomEvents)))

	if wantUFS && len(ufsEvents) > 0 {
		report("processing ufs bottom-half...")
		ufsEvents = ProcessUFS(ufsEvents)
		report("writing result_ufs.parquet")
		if err := WriteUFSParquet(ufsEvents, outputDir); err != nil {
			return fmt.Errorf("write ufs parquet: %w", err)
		}
	}
	if wantBlock && len(blockEvents) > 0 {
		report("processing block bottom-half...")
		blockEvents = ProcessBlock(blockEvents)
		report("writing result_block.parquet")
		if err := WriteBlockParquet(blockEvents, outputDir); err != nil {
			return fmt.Errorf("write block parquet: %w", err)
		}
	}
	if wantUFSCustom && len(ufsCustomEvents) > 0 {
		report("processing ufscustom bottom-half...")
		ufsCustomEvents = ProcessUFSCustom(ufsCustomEvents)
		report("writing result_ufscustom.parquet")
		if err := WriteUFSCustomParquet(ufsCustomEvents, outputDir); err != nil {
			return fmt.Errorf("write ufscustom parquet: %w", err)
		}
	}

	report("done")
	return nil
}
