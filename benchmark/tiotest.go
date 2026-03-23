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

	cmd := fmt.Sprintf("%s -t %s -f %s -b %s -d %s", remotePath, threads, size, blocksize, dir)

	// -k N: skip test number N (can be used multiple times)
	// Test numbers: 0=seq_write, 1=rand_write, 2=seq_read, 3=rand_read
	if skip := params["skip"]; skip != "" {
		// Raw skip values: "0,1" or "0 1"
		for _, s := range strings.FieldsFunc(skip, func(r rune) bool { return r == ',' || r == ' ' }) {
			cmd += " -k " + strings.TrimSpace(s)
		}
	} else if test := params["test"]; test != "" {
		switch test {
		case "seq_write":
			cmd += " -k 1 -k 2 -k 3" // skip rand_write, seq_read, rand_read
		case "seq_read":
			cmd += " -k 0 -k 1 -k 3" // skip seq_write, rand_write, rand_read
		case "rand_write":
			cmd += " -k 0 -k 2 -k 3" // skip seq_write, seq_read, rand_read
		case "rand_read":
			cmd += " -k 0 -k 1 -k 2" // skip seq_write, rand_write, seq_read
		case "seq":
			cmd += " -k 1 -k 3" // skip rand_write, rand_read
		case "rand":
			cmd += " -k 0 -k 2" // skip seq_write, seq_read
		// "all" or empty = no skip
		}
	}

	// -u: do not unlink test files (useful for use_file_from_step)
	if params["keep_files"] == "true" || params["use_file_from_step"] != "" {
		cmd += " -u"
	}

	return cmd
}

func parseTiotestResults(output string) map[string]float64 {
	metrics := make(map[string]float64)
	lines := strings.Split(output, "\n")

	inPerf := false
	inLatency := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Detect sections
		if strings.Contains(line, "Tiotest results for") {
			inPerf = true
			inLatency = false
			continue
		}
		if strings.Contains(line, "Tiotest latency results") {
			inPerf = false
			inLatency = true
			continue
		}

		// Performance table:
		// | Write           1 MBs |    0.0 s |  90.835 MB/s |   0.5 %  |  58.9 % |
		// | Random Write  391 MBs |    0.3 s | 1408.952 MB/s |   1.4 %  |  96.6 % |
		// | Read            1 MBs |    0.0 s | 249.128 MB/s |   1.7 %  |  37.9 % |
		// | Random Read   391 MBs |    0.1 s | 3288.200 MB/s |   9.5 %  |  87.8 % |
		if inPerf && strings.HasPrefix(line, "|") {
			parseTiotestPerfLine(line, metrics)
		}

		// Latency table:
		// | Write        |        0.017 ms |        0.190 ms |  0.00000 |   0.00000 |
		if inLatency && strings.HasPrefix(line, "|") {
			parseTiotestLatencyLine(line, metrics)
		}
	}

	return metrics
}

func parseTiotestPerfLine(line string, metrics map[string]float64) {
	// Split by | and clean
	parts := strings.Split(line, "|")
	if len(parts) < 6 {
		return
	}

	item := strings.TrimSpace(parts[1])
	rateStr := strings.TrimSpace(parts[3])
	usrCPU := strings.TrimSpace(parts[4])
	sysCPU := strings.TrimSpace(parts[5])

	var prefix string
	switch {
	case strings.HasPrefix(item, "Random Write"):
		prefix = "rand_write"
	case strings.HasPrefix(item, "Random Read"):
		prefix = "rand_read"
	case strings.HasPrefix(item, "Write"):
		prefix = "seq_write"
	case strings.HasPrefix(item, "Read"):
		prefix = "seq_read"
	default:
		return
	}

	// Rate: "90.835 MB/s"
	rateFields := strings.Fields(rateStr)
	if len(rateFields) >= 1 {
		if v, err := strconv.ParseFloat(rateFields[0], 64); err == nil {
			metrics[prefix+"_mb_sec"] = v
		}
	}

	// Time: extract from parts[2] "0.0 s"
	timeStr := strings.TrimSpace(parts[2])
	timeFields := strings.Fields(timeStr)
	if len(timeFields) >= 1 {
		if v, err := strconv.ParseFloat(timeFields[0], 64); err == nil {
			metrics[prefix+"_time_sec"] = v
		}
	}

	// CPU
	if v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(usrCPU), "%"), 64); err == nil {
		metrics[prefix+"_usr_cpu_pct"] = v
	}
	if v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(sysCPU), "%"), 64); err == nil {
		metrics[prefix+"_sys_cpu_pct"] = v
	}
}

func parseTiotestLatencyLine(line string, metrics map[string]float64) {
	parts := strings.Split(line, "|")
	if len(parts) < 6 {
		return
	}

	item := strings.TrimSpace(parts[1])
	avgLat := strings.TrimSpace(parts[2])
	maxLat := strings.TrimSpace(parts[3])
	pct2s := strings.TrimSpace(parts[4])
	pct10s := strings.TrimSpace(parts[5])

	var prefix string
	switch {
	case strings.HasPrefix(item, "Random Write"):
		prefix = "rand_write"
	case strings.HasPrefix(item, "Random Read"):
		prefix = "rand_read"
	case strings.HasPrefix(item, "Write"):
		prefix = "seq_write"
	case strings.HasPrefix(item, "Read"):
		prefix = "seq_read"
	case strings.HasPrefix(item, "Total"):
		prefix = "total"
	default:
		return
	}

	// "0.017 ms"
	avgFields := strings.Fields(avgLat)
	if len(avgFields) >= 1 {
		if v, err := strconv.ParseFloat(avgFields[0], 64); err == nil {
			metrics[prefix+"_lat_avg_ms"] = v
		}
	}
	maxFields := strings.Fields(maxLat)
	if len(maxFields) >= 1 {
		if v, err := strconv.ParseFloat(maxFields[0], 64); err == nil {
			metrics[prefix+"_lat_max_ms"] = v
		}
	}
	if v, err := strconv.ParseFloat(pct2s, 64); err == nil {
		metrics[prefix+"_lat_pct_over_2s"] = v
	}
	if v, err := strconv.ParseFloat(pct10s, 64); err == nil {
		metrics[prefix+"_lat_pct_over_10s"] = v
	}
}
