package parser

import (
	"bufio"
	"fmt"
	"os"
)

// ProgressFunc — 진행 라인 콜백. tracer.go 의 SubscribeJobProgress 로 forward 된다.
type ProgressFunc func(line string)

// RunParquetOnly — trace.log 를 한 번에 파싱해 outputDir 에 result_<type>.parquet 을 만든다.
//
// traceType:
//   - ftrace 계열: "ufs", "block", "both"(UFS+Block 동시), "ufscustom"
//   - bpftrace(fsiotrace) 계열: "fsio_ufs", "fsio_block"
//
// fsio_* 는 입력 포맷 자체가 다르다 (ftrace 텍스트가 아니라 TAB 17컬럼 TSV).
// 수집 시 `--only ufs` / `--only blk` 로 한 레이어만 받으므로 단일 선택이다.
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
	wantFsioUFS := traceType == "fsio_ufs"
	wantFsioBlock := traceType == "fsio_block"

	if !wantUFS && !wantBlock && !wantUFSCustom && !wantFsioUFS && !wantFsioBlock {
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
	var fsioUfsEvents []FsioUfsEvent
	var fsioBlockEvents []FsioBlockEvent

	var lineNo uint64
	const reportEvery = 100_000
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if line == "" {
			continue
		}

		// fsio 를 먼저 판정한다. ftrace UFS 라인 검사는 `ufshcd_command:` substring 이라
		// bpftrace TSV 도 걸리는데, 포맷이 달라 그대로 넘기면 오파싱된다.
		if wantFsioUFS || wantFsioBlock {
			if quickFsioCheck(line) {
				if wantFsioUFS {
					if ev, ok := parseFsioUfsLine(line); ok {
						ev.LineNumber = lineNo
						fsioUfsEvents = append(fsioUfsEvents, ev)
					}
				} else {
					if ev, ok := parseFsioBlockLine(line); ok {
						ev.LineNumber = lineNo
						fsioBlockEvents = append(fsioBlockEvents, ev)
					}
				}
			}
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
			report(fmt.Sprintf("scanned %d lines (ufs=%d block=%d ufscustom=%d fsio_ufs=%d fsio_block=%d)",
				lineNo, len(ufsEvents), len(blockEvents), len(ufsCustomEvents),
				len(fsioUfsEvents), len(fsioBlockEvents)))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	report(fmt.Sprintf("scan done: %d lines, ufs=%d block=%d ufscustom=%d fsio_ufs=%d fsio_block=%d",
		lineNo, len(ufsEvents), len(blockEvents), len(ufsCustomEvents),
		len(fsioUfsEvents), len(fsioBlockEvents)))

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

	if wantFsioUFS && len(fsioUfsEvents) > 0 {
		report("processing fsio_ufs bottom-half...")
		fsioUfsEvents = ProcessFsioUFS(fsioUfsEvents)
		report(fmt.Sprintf("writing result_fsio_ufs.parquet (%s)", fsioUnfinishedNote(countUnfinishedUfs(fsioUfsEvents), len(fsioUfsEvents))))
		if err := WriteFsioUfsParquet(fsioUfsEvents, outputDir); err != nil {
			return fmt.Errorf("write fsio_ufs parquet: %w", err)
		}
	}
	if wantFsioBlock && len(fsioBlockEvents) > 0 {
		report("processing fsio_block bottom-half...")
		fsioBlockEvents = ProcessFsioBlock(fsioBlockEvents)
		report(fmt.Sprintf("writing result_fsio_block.parquet (%s)", fsioUnfinishedNote(countUnfinishedBlock(fsioBlockEvents), len(fsioBlockEvents))))
		if err := WriteFsioBlockParquet(fsioBlockEvents, outputDir); err != nil {
			return fmt.Errorf("write fsio_block parquet: %w", err)
		}
	}

	report("done")
	return nil
}

// 미완결 건수는 숨기지 않고 진행 로그에 드러낸다.
//
// producer 의 구조적 한계(tp_btf 재진입 가드)로 complete 가 극소수 누락되는데,
// 실측 0.036% 라 지금은 무해하다. 다만 이 비율이 커지면 **보정할 게 아니라 원인을
// 봐야 한다는 신호**라 값이 보여야 한다.
func fsioUnfinishedNote(unfinished, total int) string {
	if unfinished == 0 {
		return fmt.Sprintf("%d events", total)
	}
	pct := float64(unfinished) * 100 / float64(total)
	return fmt.Sprintf("%d events, 미완결 %d 건 (%.3f%%) — complete 를 못 받은 send",
		total, unfinished, pct)
}

func countUnfinishedUfs(events []FsioUfsEvent) int {
	n := 0
	for i := range events {
		if events[i].IsUnfinished {
			n++
		}
	}
	return n
}

func countUnfinishedBlock(events []FsioBlockEvent) int {
	n := 0
	for i := range events {
		if events[i].IsUnfinished {
			n++
		}
	}
	return n
}
