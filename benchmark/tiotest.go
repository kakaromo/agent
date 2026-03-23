package benchmark

import (
	"fmt"
	"strconv"
	"strings"
)

func buildTiotestCommand(remotePath string, params map[string]string) string {
	size := params["size"]
	if size == "" {
		size = "100"
	}
	threads := params["threads"]
	if threads == "" {
		threads = "4"
	}
	blocksize := params["blocksize"]
	if blocksize == "" {
		blocksize = "4096"
	}
	dir := params["directory"]
	if dir == "" {
		dir = "/data/local/tmp/test"
	}

	return fmt.Sprintf("%s -t %s -f %s -b %s -d %s", remotePath, threads, size, blocksize, dir)
}

func parseTiotestResults(output string) map[string]float64 {
	metrics := make(map[string]float64)
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// tiotest outputs lines like:
		// "Sequential Read:  XX.XX MB/s"
		// "Sequential Write: XX.XX MB/s"
		// "Random Read:      XX.XX MB/s"
		// "Random Write:     XX.XX MB/s"
		// Also may output in table format with columns
		parseTiotestLine(line, "Sequential Read", "seq_read_mb_sec", metrics)
		parseTiotestLine(line, "Sequential Write", "seq_write_mb_sec", metrics)
		parseTiotestLine(line, "Random Read", "random_read_mb_sec", metrics)
		parseTiotestLine(line, "Random Write", "random_write_mb_sec", metrics)

		// Table format: Thread  Read Rate  Write Rate ...
		if strings.HasPrefix(line, "Total") || strings.HasPrefix(line, "Average") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if v, err := strconv.ParseFloat(f, 64); err == nil {
					switch {
					case i == 1:
						metrics["total_read_mb_sec"] = v
					case i == 2:
						metrics["total_write_mb_sec"] = v
					}
				}
			}
		}
	}

	return metrics
}

func parseTiotestLine(line, prefix, metricKey string, metrics map[string]float64) {
	if !strings.Contains(line, prefix) {
		return
	}
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return
	}
	fields := strings.Fields(parts[1])
	if len(fields) >= 1 {
		if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
			metrics[metricKey] = v
		}
	}
}
