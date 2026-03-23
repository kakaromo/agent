package benchmark

import (
	"encoding/json"
	"fmt"
	"strings"
)

func buildFioCommand(remotePath string, params map[string]string) string {
	bs := params["bs"]
	if bs == "" {
		bs = "4k"
	}
	size := params["size"]
	if size == "" {
		size = "100m"
	}
	rw := params["rw"]
	if rw == "" {
		rw = "randread"
	}
	name := params["name"]
	if name == "" {
		name = "benchmark"
	}
	numjobs := params["numjobs"]
	if numjobs == "" {
		numjobs = "1"
	}
	runtime := params["runtime"]
	filename := params["filename"]
	directory := params["directory"]
	if filename == "" && directory == "" {
		directory = "/data/local/tmp/test"
	}

	var cmd string
	if filename != "" {
		// Use specific file (reuse from previous step)
		cmd = fmt.Sprintf("%s --name=%s --rw=%s --bs=%s --size=%s --numjobs=%s --filename=%s --output-format=json",
			remotePath, name, rw, bs, size, numjobs, filename)
	} else {
		cmd = fmt.Sprintf("%s --name=%s --rw=%s --bs=%s --size=%s --numjobs=%s --directory=%s --output-format=json",
			remotePath, name, rw, bs, size, numjobs, directory)
	}
	if runtime != "" {
		cmd += " --runtime=" + runtime + " --time_based"
	}
	return cmd
}

// fio JSON structs for full parsing

type fioResult struct {
	Jobs []fioJob `json:"jobs"`
}

type fioJob struct {
	JobName    string      `json:"jobname"`
	Read       fioRWStats  `json:"read"`
	Write      fioRWStats  `json:"write"`
	Trim       fioRWStats  `json:"trim"`
	JobRuntime int64       `json:"job_runtime"`
	UsrCPU     float64     `json:"usr_cpu"`
	SysCPU     float64     `json:"sys_cpu"`
	Ctx        int64       `json:"ctx"`
}

type fioRWStats struct {
	IOBytes   int64   `json:"io_bytes"`
	IOKbytes  int64   `json:"io_kbytes"`
	BwBytes   float64 `json:"bw_bytes"`
	Bw        float64 `json:"bw"`
	Iops      float64 `json:"iops"`
	Runtime   int64   `json:"runtime"`
	TotalIOs  int64   `json:"total_ios"`
	SlatNs    fioLat  `json:"slat_ns"`
	ClatNs    fioLat  `json:"clat_ns"`
	LatNs     fioLat  `json:"lat_ns"`
	BwMin     float64 `json:"bw_min"`
	BwMax     float64 `json:"bw_max"`
	BwMean    float64 `json:"bw_mean"`
	BwDev     float64 `json:"bw_dev"`
	IopsMin   float64 `json:"iops_min"`
	IopsMax   float64 `json:"iops_max"`
	IopsMean  float64 `json:"iops_mean"`
	IopsDev   float64 `json:"iops_stddev"`
}

type fioLat struct {
	Min    float64            `json:"min"`
	Max    float64            `json:"max"`
	Mean   float64            `json:"mean"`
	Stddev float64            `json:"stddev"`
	N      int64              `json:"N"`
	Pct    map[string]float64 `json:"percentile"`
}

func parseFioResults(output string) map[string]float64 {
	metrics := make(map[string]float64)

	idx := strings.Index(output, "{")
	if idx < 0 {
		return metrics
	}
	jsonStr := output[idx:]

	var result fioResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return metrics
	}

	if len(result.Jobs) == 0 {
		return metrics
	}

	job := result.Jobs[0]

	// CPU & runtime
	metrics["job_runtime_ms"] = float64(job.JobRuntime)
	metrics["usr_cpu_pct"] = job.UsrCPU
	metrics["sys_cpu_pct"] = job.SysCPU
	metrics["ctx_switches"] = float64(job.Ctx)

	// Read stats
	extractRWMetrics("read", &job.Read, metrics)

	// Write stats
	extractRWMetrics("write", &job.Write, metrics)

	// Trim stats (if any)
	if job.Trim.TotalIOs > 0 {
		extractRWMetrics("trim", &job.Trim, metrics)
	}

	return metrics
}

func extractRWMetrics(prefix string, rw *fioRWStats, metrics map[string]float64) {
	p := prefix + "_"

	// Throughput
	metrics[p+"io_bytes"] = float64(rw.IOBytes)
	metrics[p+"bw_bytes"] = rw.BwBytes
	metrics[p+"bw_kb"] = rw.Bw
	metrics[p+"iops"] = rw.Iops
	metrics[p+"runtime_ms"] = float64(rw.Runtime)
	metrics[p+"total_ios"] = float64(rw.TotalIOs)

	// IOPS stats
	metrics[p+"iops_min"] = rw.IopsMin
	metrics[p+"iops_max"] = rw.IopsMax
	metrics[p+"iops_mean"] = rw.IopsMean
	metrics[p+"iops_stddev"] = rw.IopsDev

	// Bandwidth stats
	metrics[p+"bw_min_kb"] = rw.BwMin
	metrics[p+"bw_max_kb"] = rw.BwMax
	metrics[p+"bw_mean_kb"] = rw.BwMean
	metrics[p+"bw_stddev_kb"] = rw.BwDev

	// Submission latency (slat)
	metrics[p+"slat_ns_min"] = rw.SlatNs.Min
	metrics[p+"slat_ns_max"] = rw.SlatNs.Max
	metrics[p+"slat_ns_mean"] = rw.SlatNs.Mean
	metrics[p+"slat_ns_stddev"] = rw.SlatNs.Stddev

	// Completion latency (clat)
	metrics[p+"clat_ns_min"] = rw.ClatNs.Min
	metrics[p+"clat_ns_max"] = rw.ClatNs.Max
	metrics[p+"clat_ns_mean"] = rw.ClatNs.Mean
	metrics[p+"clat_ns_stddev"] = rw.ClatNs.Stddev

	// Completion latency percentiles
	for pctKey, pctVal := range rw.ClatNs.Pct {
		metrics[fmt.Sprintf("%sclat_ns_p%s", p, pctKey)] = pctVal
	}

	// Total latency (lat = slat + clat)
	metrics[p+"lat_ns_min"] = rw.LatNs.Min
	metrics[p+"lat_ns_max"] = rw.LatNs.Max
	metrics[p+"lat_ns_mean"] = rw.LatNs.Mean
	metrics[p+"lat_ns_stddev"] = rw.LatNs.Stddev
}
