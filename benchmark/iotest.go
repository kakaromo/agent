package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"agent/adb"
	pb "agent/pb"
)

// iotestProgressEvent — iotest 바이너리의 stderr JSONL 한 줄 (cmd/iotest/progress.go ProgressEvent 와 동일).
type iotestProgressEvent struct {
	Thread   string `json:"thread"`
	Step     int    `json:"step"`
	Op       string `json:"op"`
	Status   string `json:"status"`
	Iter     int    `json:"iter,omitempty"`
	Total    int    `json:"total,omitempty"`
	OpInner  string `json:"op_inner,omitempty"`
}

// iotestThreadState — agent 측에서 thread별 누적 상태. completed 가 한 번 들어오면 done.
type iotestThreadState struct {
	maxStep   int
	current   int
	currentOp string
	iter      int
	iterTotal int
	status    string // "running" | "completed" | "failed"
}

// onIOTestProgress 콜백 시그니처 — scenario 측이 IOTEST|... 메시지로 변환해 SSE 에 forward.
type onIOTestProgress func(line string)

func (o *Orchestrator) executeIOTestStep(ctx context.Context, md *adb.ManagedDevice, step *pb.ScenarioStep, onProgress onIOTestProgress) (string, map[string]float64, error) {
	toolName := o.resolveToolName(pb.BenchmarkTool_BENCHMARK_TOOL_IOTEST)
	localPath := filepath.Join(o.toolsDir, toolName)
	remotePath := remoteToolDir + "/" + toolName

	if err := pushToolIfNeeded(ctx, md.Device, localPath, remotePath); err != nil {
		return "", nil, fmt.Errorf("push %s: %w", toolName, err)
	}
	if _, err := md.Device.Shell(ctx, "chmod 755 "+remotePath); err != nil {
		return "", nil, fmt.Errorf("chmod: %w", err)
	}

	cmdStr := buildIOTestCommand(remotePath, step.Params)

	// thread별 상태를 누적해 IOTEST|... 메시지로 변환.
	var stateMu sync.Mutex
	states := map[string]*iotestThreadState{}

	onStderr := func(line string) {
		var evt iotestProgressEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			return
		}
		if evt.Thread == "" {
			return
		}
		stateMu.Lock()
		st, ok := states[evt.Thread]
		if !ok {
			st = &iotestThreadState{status: "running"}
			states[evt.Thread] = st
		}
		// step 은 monotonic — 가장 큰 값을 totalSteps 추정에 사용.
		if evt.Step > st.maxStep {
			st.maxStep = evt.Step
		}
		st.current = evt.Step
		if evt.OpInner != "" {
			st.currentOp = evt.OpInner
		} else if evt.Op != "" {
			st.currentOp = evt.Op
		}
		if evt.Iter > 0 {
			st.iter = evt.Iter
		}
		if evt.Total > 0 {
			st.iterTotal = evt.Total
		}
		switch evt.Status {
		case "completed":
			st.status = "completed"
		case "error", "failed":
			st.status = "failed"
		case "running", "ok", "":
			// keep
		}
		// snapshot 직렬화는 lock 안에서 끝.
		msg := formatIOTestMessage(evt.Thread, st)
		stateMu.Unlock()
		if onProgress != nil {
			onProgress(msg)
		}
	}

	output, err := md.Device.ShellStream(ctx, cmdStr, nil, onStderr)
	if err != nil {
		return output, nil, fmt.Errorf("iotest exec: %w", err)
	}

	metrics := parseIOTestResults(output)
	return output, metrics, nil
}

// formatIOTestMessage — frontend ScenarioCanvas.extractIOTestThreadProgresses 와 메시지 패턴 동기화.
// 패턴: `IOTEST|thread=NAME|completed=N|total=N|status=S|op=...|iter=N|iterTotal=N`
func formatIOTestMessage(thread string, st *iotestThreadState) string {
	var b strings.Builder
	b.WriteString("IOTEST|thread=")
	b.WriteString(thread)
	fmt.Fprintf(&b, "|completed=%d|total=%d|status=%s",
		st.current, max(st.maxStep, st.current), st.status)
	if st.currentOp != "" {
		b.WriteString("|op=")
		b.WriteString(st.currentOp)
	}
	if st.iter > 0 {
		fmt.Fprintf(&b, "|iter=%d", st.iter)
	}
	if st.iterTotal > 0 {
		fmt.Fprintf(&b, "|iterTotal=%d", st.iterTotal)
	}
	return b.String()
}

func buildIOTestCommand(remotePath string, params map[string]string) string {
	configJSON := params["config"]
	if configJSON == "" {
		return remotePath
	}
	// Write config to a temp file on device, then run iotest with -f
	// The config is passed inline via echo | iotest (stdin)
	// Escape single quotes in JSON for shell
	escaped := strings.ReplaceAll(configJSON, "'", "'\\''")
	return fmt.Sprintf("echo '%s' | %s", escaped, remotePath)
}

func parseIOTestResults(output string) map[string]float64 {
	metrics := make(map[string]float64)

	// Find the JSON output (stdout) — may be mixed with other output
	jsonStart := strings.Index(output, "{")
	if jsonStart < 0 {
		return metrics
	}

	// Find the matching closing brace
	jsonStr := output[jsonStart:]
	depth := 0
	jsonEnd := -1
	for i, c := range jsonStr {
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				jsonEnd = i + 1
				break
			}
		}
	}
	if jsonEnd < 0 {
		return metrics
	}
	jsonStr = jsonStr[:jsonEnd]

	var result struct {
		Threads []struct {
			Name       string  `json:"name"`
			TotalBytes int64   `json:"total_bytes"`
			TotalOps   int     `json:"total_ops"`
			Duration   int64   `json:"duration_ns"`
			Throughput float64 `json:"throughput_bps"`
			IOPS       float64 `json:"iops"`
			Errors     int     `json:"errors"`
		} `json:"threads"`
		TotalDuration int64 `json:"total_duration_ns"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return metrics
	}

	for _, t := range result.Threads {
		prefix := "thread_" + t.Name + "_"
		metrics[prefix+"bytes"] = float64(t.TotalBytes)
		metrics[prefix+"ops"] = float64(t.TotalOps)
		metrics[prefix+"duration_ns"] = float64(t.Duration)
		metrics[prefix+"throughput_bps"] = t.Throughput
		metrics[prefix+"iops"] = t.IOPS
		metrics[prefix+"errors"] = float64(t.Errors)
	}

	metrics["total_duration_ns"] = float64(result.TotalDuration)

	// Calculate totals
	var totalBytes float64
	var totalOps float64
	for _, t := range result.Threads {
		totalBytes += float64(t.TotalBytes)
		totalOps += float64(t.TotalOps)
	}
	metrics["total_bytes"] = totalBytes
	metrics["total_ops"] = totalOps
	if result.TotalDuration > 0 {
		metrics["total_throughput_bps"] = totalBytes / (float64(result.TotalDuration) / 1e9)
		metrics["total_iops"] = totalOps / (float64(result.TotalDuration) / 1e9)
	}

	return metrics
}
