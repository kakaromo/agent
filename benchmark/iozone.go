package benchmark

import (
	"fmt"
	"strconv"
	"strings"
)

func buildIozoneCommand(remotePath string, params map[string]string) string {
	size := params["size"]
	if size == "" {
		size = "100m"
	}
	reclen := params["reclen"]
	if reclen == "" {
		reclen = "4k"
	}

	cmd := fmt.Sprintf("%s -a -s %s -r %s -i 0 -i 1 -i 2", remotePath, size, reclen)

	if threads := params["threads"]; threads != "" {
		cmd += " -t " + threads
	}
	if filepath := params["filepath"]; filepath != "" {
		cmd += " -f " + filepath
	} else {
		name := params["name"]
		if name == "" {
			name = "iozone_test"
		}
		cmd += " -f /data/local/tmp/test/" + name
	}

	return cmd
}

func parseIozoneResults(output string) map[string]float64 {
	metrics := make(map[string]float64)
	lines := strings.Split(output, "\n")

	// iozone outputs a table with headers, then data rows.
	// Look for lines with numeric data after the header.
	inResults := false
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Header line contains "kB" and "reclen"
		if strings.Contains(line, "kB") && strings.Contains(line, "reclen") {
			inResults = true
			continue
		}

		if inResults {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			// Check if first field is numeric
			if _, err := strconv.ParseFloat(fields[0], 64); err != nil {
				continue
			}
			// Fields: filesize reclen write rewrite read reread random_read random_write
			if len(fields) >= 5 {
				if v, err := strconv.ParseFloat(fields[2], 64); err == nil {
					metrics["write_kb_sec"] = v
				}
				if v, err := strconv.ParseFloat(fields[4], 64); err == nil {
					metrics["read_kb_sec"] = v
				}
			}
			if len(fields) >= 7 {
				if v, err := strconv.ParseFloat(fields[3], 64); err == nil {
					metrics["rewrite_kb_sec"] = v
				}
				if v, err := strconv.ParseFloat(fields[5], 64); err == nil {
					metrics["reread_kb_sec"] = v
				}
				if v, err := strconv.ParseFloat(fields[6], 64); err == nil {
					metrics["random_read_kb_sec"] = v
				}
			}
			if len(fields) >= 8 {
				if v, err := strconv.ParseFloat(fields[7], 64); err == nil {
					metrics["random_write_kb_sec"] = v
				}
			}
			break // Take first data row
		}
	}

	return metrics
}
