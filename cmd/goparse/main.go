// goparse — Rust ↔ Go 파서 정합성 검증용 standalone CLI.
//
//	go run ./cmd/goparse <trace.log> <outputDir> <ufs|block|both|ufscustom|fsio_ufs|fsio_block>
//
// agent 본체와 동일한 trace/parser.RunParquetOnly 를 직접 호출해
// outputDir/result_<type>.parquet 을 생성한다. scripts/compare-parsers.sh 가
// 같은 trace.log 에 Rust tools/trace --parquet-only 와 이걸 둘 다 돌려
// DuckDB EXCEPT 로 row-by-row 비교한다.
package main

import (
	"fmt"
	"os"

	"agent/trace/parser"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: goparse <trace.log> <outputDir> <ufs|block|both|ufscustom|fsio_ufs|fsio_block>")
		os.Exit(2)
	}
	logFile, outputDir, traceType := os.Args[1], os.Args[2], os.Args[3]

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}

	progress := func(line string) { fmt.Fprintln(os.Stderr, line) }
	if err := parser.RunParquetOnly(logFile, outputDir, traceType, progress); err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}
}
