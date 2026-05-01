package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ProgressEvent is a single line of stderr JSONL output.
type ProgressEvent struct {
	Thread   string `json:"thread"`
	Step     int    `json:"step"`
	Op       string `json:"op"`
	Status   string `json:"status"` // "ok", "error", "running", "skipped"
	Error    string `json:"error,omitempty"`
	Duration int64  `json:"duration_ns,omitempty"`

	// Optional fields depending on op
	Path     string `json:"path,omitempty"`
	Offset   int64  `json:"offset,omitempty"`
	BS       int64  `json:"bs,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	Value    string `json:"value,omitempty"`
	Progress int    `json:"progress,omitempty"` // for create_files/delete_pattern
	Total    int    `json:"total,omitempty"`    // for create_files/delete_pattern/loop

	// Loop specific
	Iter    int    `json:"iter,omitempty"`
	OpInner string `json:"op_inner,omitempty"` // inner op for loop progress
}

var progressMu sync.Mutex

// emitProgress writes a progress event to stderr as a single JSON line.
func emitProgress(evt ProgressEvent) {
	progressMu.Lock()
	defer progressMu.Unlock()
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stderr, string(data))
}
