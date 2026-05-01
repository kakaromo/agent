package main

// FinalResult is the stdout JSON output.
type FinalResult struct {
	Threads       []ThreadResult `json:"threads"`
	TotalDuration int64          `json:"total_duration_ns"`
}

// ThreadResult is the result for one thread.
type ThreadResult struct {
	Name       string     `json:"name"`
	TotalBytes int64      `json:"total_bytes"`
	TotalOps   int        `json:"total_ops"`
	Duration   int64      `json:"duration_ns"`
	Throughput float64    `json:"throughput_bps"`
	IOPS       float64    `json:"iops"`
	Errors     int        `json:"errors"`
	Ops        []OpResult `json:"ops"`
}

// OpResult is the result for one operation.
type OpResult struct {
	Op       string `json:"op"`
	Status   string `json:"status"` // "ok", "error", "skipped"
	Error    string `json:"error,omitempty"`
	Duration int64  `json:"duration_ns,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	Offset   int64  `json:"offset,omitempty"`
	BS       int64  `json:"bs,omitempty"`
	Count    int    `json:"count,omitempty"`
	Path     string `json:"path,omitempty"`
	Value    string `json:"value,omitempty"`
	Pattern  string `json:"pattern,omitempty"`

	// Actual resolved values (when random/seq was used)
	// Shows what was actually picked, e.g. "bs=16k,offset=28672,count=4"
	Actual string `json:"actual,omitempty"`

	// Loop results
	AvgNs     int64      `json:"avg_ns,omitempty"`
	MinNs     int64      `json:"min_ns,omitempty"`
	MaxNs     int64      `json:"max_ns,omitempty"`
	LoopOps   []OpResult `json:"iters,omitempty"`
	LoopBytes int64      `json:"total_bytes,omitempty"`
}
