package benchmark

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent/adb"
	pb "agent/pb"

	"github.com/google/uuid"
)

// expandedStep holds a step with its loop/repeat context for progress reporting.
type expandedStep struct {
	step       *pb.ScenarioStep
	stepIndex  int // original step index
	loopIndex  int // current loop iteration (1-based, 0 = no loop)
	loopTotal  int // total loop count (0 = no loop)
	repeatIndex int // current repeat iteration (1-based)
	repeatTotal int
}

// expandSteps expands steps with loop info into a flat execution list.
func expandSteps(steps []*pb.ScenarioStep, loops []*pb.ScenarioLoop, repeat int32) []expandedStep {
	if repeat <= 0 {
		repeat = 1
	}

	// Build loop map: for each step index, which loop does it belong to?
	type loopInfo struct {
		startStep int
		endStep   int
		count     int
	}
	loopMap := make(map[int]*loopInfo) // stepIndex -> loopInfo
	var loopInfos []*loopInfo
	for _, l := range loops {
		li := &loopInfo{
			startStep: int(l.StartStep),
			endStep:   int(l.EndStep),
			count:     int(l.Count),
		}
		loopInfos = append(loopInfos, li)
		for i := li.startStep; i <= li.endStep && i < len(steps); i++ {
			loopMap[i] = li
		}
	}

	var result []expandedStep

	for r := int32(1); r <= repeat; r++ {
		processed := make(map[int]bool)

		for i := 0; i < len(steps); i++ {
			if processed[i] {
				continue
			}

			if li, ok := loopMap[i]; ok && i == li.startStep {
				// This is the start of a loop range
				for lc := 1; lc <= li.count; lc++ {
					for si := li.startStep; si <= li.endStep && si < len(steps); si++ {
						result = append(result, expandedStep{
							step:        steps[si],
							stepIndex:   si,
							loopIndex:   lc,
							loopTotal:   li.count,
							repeatIndex: int(r),
							repeatTotal: int(repeat),
						})
						processed[si] = true
					}
				}
				// Skip to after the loop range
				i = li.endStep
			} else {
				result = append(result, expandedStep{
					step:        steps[i],
					stepIndex:   i,
					repeatIndex: int(r),
					repeatTotal: int(repeat),
				})
				processed[i] = true
			}
		}
	}

	return result
}

// RunScenario starts a scenario job with multiple steps.
func (o *Orchestrator) RunScenario(ctx context.Context, req *pb.RunScenarioRequest) (string, error) {
	deviceIDs := req.DeviceIds
	if len(deviceIDs) == 0 {
		deviceIDs = o.manager.GetOnlineDevices()
	}
	if len(deviceIDs) == 0 {
		return "", fmt.Errorf("no online devices available")
	}
	if len(req.Steps) == 0 {
		return "", fmt.Errorf("no steps defined")
	}

	policy := req.BusyPolicy
	for _, id := range deviceIDs {
		if err := o.checkDeviceBusy(id, policy); err != nil {
			return "", err
		}
	}

	jobCtx, jobCancel := context.WithCancel(context.Background())
	jobID := uuid.New().String()
	job := &Job{
		ID:             jobID,
		Name:           req.ScenarioName,
		State:          pb.JobState_JOB_STATE_QUEUED,
		DeviceStatuses: make(map[string]*pb.DeviceJobStatus),
		Results:        make(map[string]*pb.BenchmarkResult),
		cancelFunc:     jobCancel,
	}
	for _, id := range deviceIDs {
		job.DeviceStatuses[id] = &pb.DeviceJobStatus{
			DeviceId: id,
			State:    pb.JobState_JOB_STATE_QUEUED,
		}
	}

	o.mu.Lock()
	o.jobs[jobID] = job
	o.mu.Unlock()

	expanded := expandSteps(req.Steps, req.Loops, req.Repeat)

	go o.executeScenario(jobCtx, job, deviceIDs, expanded, policy)
	return jobID, nil
}

func (o *Orchestrator) executeScenario(ctx context.Context, job *Job, deviceIDs []string, steps []expandedStep, policy string) {
	defer job.closeSubscribers()

	var wg sync.WaitGroup
	for _, deviceID := range deviceIDs {
		wg.Add(1)
		go func(devID string) {
			defer wg.Done()
			if policy == "wait" {
				lock := o.getDeviceLock(devID)
				lock.Lock()
				defer lock.Unlock()
			}
			if ctx.Err() != nil {
				o.updateDeviceStatus(job, devID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", 0)
				return
			}
			o.runScenarioOnDevice(ctx, job, devID, steps)
		}(deviceID)
	}
	wg.Wait()

	// Determine final state
	job.mu.Lock()
	completed, failed := 0, 0
	for _, ds := range job.DeviceStatuses {
		switch ds.State {
		case pb.JobState_JOB_STATE_COMPLETED:
			completed++
		case pb.JobState_JOB_STATE_FAILED:
			failed++
		}
	}
	total := len(job.DeviceStatuses)
	if failed == total {
		job.State = pb.JobState_JOB_STATE_FAILED
	} else if failed > 0 {
		job.State = pb.JobState_JOB_STATE_PARTIALLY_FAILED
	} else {
		job.State = pb.JobState_JOB_STATE_COMPLETED
	}
	job.mu.Unlock()

	slog.Info("scenario finished", "job_id", job.ID, "state", job.State)
}

func (o *Orchestrator) runScenarioOnDevice(ctx context.Context, job *Job, deviceID string, steps []expandedStep) {
	md, err := o.manager.GetDevice(deviceID)
	if err != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, err.Error(), 0)
		return
	}

	o.manager.SetState(deviceID, pb.DeviceState_DEVICE_STATE_BUSY)
	defer o.manager.SetState(deviceID, pb.DeviceState_DEVICE_STATE_ONLINE)

	startedAt := time.Now().UnixMilli()
	totalSteps := len(steps)
	var rawOutput strings.Builder
	allMetrics := make(map[string]float64)
	// Track file paths generated by each original step index for use_file_from_step
	stepFiles := make(map[int]string)
	// Track active trace job ID for trace_start/trace_stop
	var activeTraceJobID string
	// Collect trace job mappings
	var traceJobMappings []*pb.TraceJobMapping

	for i, es := range steps {
		// Check cancellation
		if ctx.Err() != nil {
			o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", 0)
			o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, false, "cancelled", traceJobMappings...)
			return
		}
		progress := int32(float64(i) / float64(totalSteps) * 100)
		msg := formatStepMessage(es, totalSteps)

		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_RUNNING, msg, progress)

		stepOut, stepMetrics, err := o.executeStep(ctx, md, es, i, stepFiles, deviceID, &activeTraceJobID)
		if err != nil {
			errMsg := fmt.Sprintf("%s failed: %s", msg, err.Error())
			o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, errMsg, progress)
			o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, false, errMsg, traceJobMappings...)
			return
		}

		// Collect trace job mapping from TRACE_STOP output
		if strings.Contains(stepOut, "TRACE_STOP|") {
			for _, line := range strings.Split(stepOut, "\n") {
				if !strings.HasPrefix(line, "TRACE_STOP|") {
					continue
				}
				mapping := parseTraceMapping(line, es)
				if mapping != nil {
					traceJobMappings = append(traceJobMappings, mapping)
				}
			}
		}

		if stepOut != "" {
			rawOutput.WriteString(fmt.Sprintf("=== %s ===\n", msg))
			rawOutput.WriteString(stepOut)
			rawOutput.WriteString("\n")
		}

		// Store metrics with prefix and send progress with step results
		prefixedMetrics := make(map[string]float64)
		for k, v := range stepMetrics {
			var prefix string
			if es.loopTotal > 0 {
				prefix = fmt.Sprintf("r%d_loop%d_step%d_", es.repeatIndex, es.loopIndex, es.stepIndex)
			} else {
				prefix = fmt.Sprintf("r%d_step%d_", es.repeatIndex, es.stepIndex)
			}
			key := prefix + k
			allMetrics[key] = v
			prefixedMetrics[key] = v
		}

		// Send step completion with results if benchmark step
		if len(stepMetrics) > 0 {
			stepProgress := int32(float64(i+1) / float64(totalSteps) * 100)
			o.updateDeviceStatusWithResult(job, deviceID, pb.JobState_JOB_STATE_RUNNING,
				msg+" completed", stepProgress, prefixedMetrics, stepOut)
		}
	}

	o.updateDeviceStatusWithResult(job, deviceID, pb.JobState_JOB_STATE_COMPLETED, "done", 100, allMetrics, rawOutput.String())
	o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, true, "", traceJobMappings...)
}

const testDir = "/data/local/tmp/test"

func (o *Orchestrator) executeStep(ctx context.Context, md *adb.ManagedDevice, es expandedStep, execIndex int, stepFiles map[int]string, deviceID string, activeTraceJobID *string) (string, map[string]float64, error) {
	step := es.step

	// Auto trace wrapping: if params has trace="on", wrap step with trace start/stop
	// Skip if trace is already active (manual trace_start/trace_stop in progress)
	if step.Params["trace"] == "on" && o.traceMgr != nil {
		if *activeTraceJobID != "" {
			// Trace already running, skip auto trace and notify user
			slog.Info("skipping auto trace: trace already active", "active_trace", *activeTraceJobID)
			out, metrics, err := o.executeStepInner(ctx, md, es, execIndex, stepFiles, deviceID, activeTraceJobID)
			warnMsg := fmt.Sprintf("TRACE_SKIPPED|step=%d|reason=trace already active (%s)", es.stepIndex, *activeTraceJobID)
			return warnMsg + "\n" + out, metrics, err
		}

		traceType := step.Params["trace_type"]
		if traceType == "" {
			traceType = "ufs"
		}
		windowSec := int32(1)
		if ws := step.Params["window_seconds"]; ws != "" {
			if v, err := strconv.Atoi(ws); err == nil && v > 0 {
				windowSec = int32(v)
			}
		}

		traceJobID, err := o.traceMgr.StartTrace(ctx, &pb.StartTraceRequest{
			DeviceId:      deviceID,
			TraceType:     traceType,
			WindowSeconds: windowSec,
		})
		if err != nil {
			return "", nil, fmt.Errorf("auto trace start: %w", err)
		}

		// Execute the actual step
		out, metrics, stepErr := o.executeStepInner(ctx, md, es, execIndex, stepFiles, deviceID, activeTraceJobID)

		// Stop trace regardless of step result
		if stopErr := o.traceMgr.StopTrace(traceJobID); stopErr != nil {
			slog.Warn("auto trace stop failed", "error", stopErr)
		}

		// Prepend/append trace info to output
		traceStart := fmt.Sprintf("TRACE_START|loop=%d|step=%d|repeat=%d|job_id=%s|trace_type=%s",
			es.loopIndex, es.stepIndex, es.repeatIndex, traceJobID, traceType)
		traceStop := fmt.Sprintf("TRACE_STOP|loop=%d|step=%d|repeat=%d|job_id=%s|trace_type=%s",
			es.loopIndex, es.stepIndex, es.repeatIndex, traceJobID, traceType)
		fullOut := traceStart + "\n" + out + "\n" + traceStop

		return fullOut, metrics, stepErr
	}

	return o.executeStepInner(ctx, md, es, execIndex, stepFiles, deviceID, activeTraceJobID)
}

func (o *Orchestrator) executeStepInner(ctx context.Context, md *adb.ManagedDevice, es expandedStep, execIndex int, stepFiles map[int]string, deviceID string, activeTraceJobID *string) (string, map[string]float64, error) {
	step := es.step

	switch step.Type {
	case "benchmark":
		return o.executeBenchmarkStep(ctx, md, step, es, execIndex, stepFiles)
	case "shell":
		cmd := step.Params["cmd"]
		if cmd == "" {
			return "", nil, fmt.Errorf("shell step missing 'cmd' param")
		}
		out, err := md.Device.Shell(ctx, cmd)
		return out, nil, err
	case "cleanup":
		if refs := step.Params["delete_files_from_steps"]; refs != "" {
			var files []string
			for _, s := range strings.Split(refs, ",") {
				idx, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil {
					continue
				}
				if f, ok := stepFiles[idx]; ok {
					files = append(files, f)
				}
			}
			if len(files) == 0 {
				return "", nil, nil
			}
			out, err := md.Device.Shell(ctx, "rm -f "+strings.Join(files, " "))
			return out, nil, err
		}
		path := step.Params["path"]
		if path == "" {
			path = testDir
		}
		out, err := md.Device.Shell(ctx, "rm -rf "+path)
		return out, nil, err
	case "sleep":
		seconds := step.Params["seconds"]
		if seconds == "" {
			seconds = "1"
		}
		sec, _ := strconv.Atoi(seconds)
		if sec <= 0 {
			sec = 1
		}
		time.Sleep(time.Duration(sec) * time.Second)
		return "", nil, nil
	case "trace_start":
		if o.traceMgr == nil {
			return "", nil, fmt.Errorf("trace manager not configured")
		}
		traceType := step.Params["trace_type"]
		if traceType == "" {
			traceType = "ufs"
		}
		windowSec := int32(1)
		if ws := step.Params["window_seconds"]; ws != "" {
			if v, err := strconv.Atoi(ws); err == nil && v > 0 {
				windowSec = int32(v)
			}
		}
		traceJobID, err := o.traceMgr.StartTrace(ctx, &pb.StartTraceRequest{
			DeviceId:      deviceID,
			TraceType:     traceType,
			WindowSeconds: windowSec,
		})
		if err != nil {
			return "", nil, fmt.Errorf("start trace: %w", err)
		}
		*activeTraceJobID = traceJobID
		// Output in structured format: TRACE_START|loop|step|job_id
		out := fmt.Sprintf("TRACE_START|loop=%d|step=%d|job_id=%s", es.loopIndex, es.stepIndex, traceJobID)
		return out, nil, nil
	case "trace_stop":
		if o.traceMgr == nil || *activeTraceJobID == "" {
			return "", nil, fmt.Errorf("no active trace to stop")
		}
		stoppedID := *activeTraceJobID
		traceType := step.Params["trace_type"]
		if traceType == "" {
			traceType = "ufs"
		}
		if err := o.traceMgr.StopTrace(stoppedID); err != nil {
			return "", nil, fmt.Errorf("stop trace: %w", err)
		}
		*activeTraceJobID = ""
		out := fmt.Sprintf("TRACE_STOP|loop=%d|step=%d|repeat=%d|job_id=%s|trace_type=%s",
			es.loopIndex, es.stepIndex, es.repeatIndex, stoppedID, traceType)
		return out, nil, nil
	default:
		return "", nil, fmt.Errorf("unknown step type: %s", step.Type)
	}
}

func (o *Orchestrator) executeBenchmarkStep(ctx context.Context, md *adb.ManagedDevice, step *pb.ScenarioStep, es expandedStep, execIndex int, stepFiles map[int]string) (string, map[string]float64, error) {
	tool := step.Tool
	toolName := toolNameFor(tool)
	if toolName == "" {
		return "", nil, fmt.Errorf("unknown benchmark tool")
	}

	localPath := filepath.Join(o.toolsDir, toolName)
	remotePath := remoteToolDir + "/" + toolName

	if err := pushToolIfNeeded(ctx, md.Device, localPath, remotePath); err != nil {
		return "", nil, fmt.Errorf("push %s: %w", toolName, err)
	}
	if _, err := md.Device.Shell(ctx, "chmod 755 "+remotePath); err != nil {
		return "", nil, fmt.Errorf("chmod: %w", err)
	}

	// Ensure test directory exists
	if _, err := md.Device.Shell(ctx, "mkdir -p "+testDir); err != nil {
		return "", nil, fmt.Errorf("mkdir: %w", err)
	}

	// Copy params so we don't mutate the original
	params := make(map[string]string)
	for k, v := range step.Params {
		params[k] = v
	}

	// Handle use_file_from_step: reuse the file from a previous step
	if refStr := params["use_file_from_step"]; refStr != "" {
		refStep, err := strconv.Atoi(refStr)
		if err != nil {
			return "", nil, fmt.Errorf("invalid use_file_from_step: %s", refStr)
		}
		prevFile, ok := stepFiles[refStep]
		if !ok {
			return "", nil, fmt.Errorf("use_file_from_step %d: no file recorded for that step", refStep)
		}
		// For fio: set filename directly instead of directory+name
		params["filename"] = prevFile
		delete(params, "use_file_from_step")
	}

	if params["directory"] == "" && params["filename"] == "" {
		params["directory"] = testDir
	}

	// Generate unique name based on loop/step/exec index
	if params["name"] == "" {
		if es.loopTotal > 0 {
			params["name"] = fmt.Sprintf("r%d_loop%d_step%d", es.repeatIndex, es.loopIndex, es.stepIndex)
		} else {
			params["name"] = fmt.Sprintf("r%d_step%d_exec%d", es.repeatIndex, es.stepIndex, execIndex)
		}
	}

	// Record the file path this step will use (for use_file_from_step reference)
	if params["filename"] != "" {
		stepFiles[es.stepIndex] = params["filename"]
	} else {
		dir := params["directory"]
		// fio creates files as: directory/name.job_index.thread_index
		stepFiles[es.stepIndex] = dir + "/" + params["name"] + ".0.0"
	}

	cmdStr := buildCommand(tool, remotePath, params)
	out, err := md.Device.Shell(ctx, cmdStr)
	if err != nil {
		return out, nil, fmt.Errorf("run %s: %w", toolName, err)
	}

	metrics := parseResults(tool, out)
	return out, metrics, nil
}

func formatStepMessage(es expandedStep, totalSteps int) string {
	var parts []string

	if es.repeatTotal > 1 {
		parts = append(parts, fmt.Sprintf("repeat %d/%d", es.repeatIndex, es.repeatTotal))
	}
	if es.loopTotal > 0 {
		parts = append(parts, fmt.Sprintf("loop %d/%d", es.loopIndex, es.loopTotal))
	}

	stepDesc := fmt.Sprintf("step %d", es.stepIndex)
	switch es.step.Type {
	case "benchmark":
		toolName := toolNameFor(es.step.Tool)
		rw := es.step.Params["rw"]
		if rw != "" {
			stepDesc += fmt.Sprintf(": %s %s", toolName, rw)
		} else {
			stepDesc += fmt.Sprintf(": %s", toolName)
		}
	case "shell":
		cmd := es.step.Params["cmd"]
		if len(cmd) > 40 {
			cmd = cmd[:40] + "..."
		}
		stepDesc += fmt.Sprintf(": shell(%s)", cmd)
	case "cleanup":
		stepDesc += ": cleanup"
	case "sleep":
		stepDesc += fmt.Sprintf(": sleep %ss", es.step.Params["seconds"])
	}

	parts = append(parts, stepDesc)
	return strings.Join(parts, ", ")
}

// parseTraceMapping parses a TRACE_STOP line into a TraceJobMapping.
// Format: TRACE_STOP|loop=1|step=2|repeat=1|job_id=abc-123|trace_type=ufs
func parseTraceMapping(line string, es expandedStep) *pb.TraceJobMapping {
	m := &pb.TraceJobMapping{
		StepIndex:   int32(es.stepIndex),
		LoopIndex:   int32(es.loopIndex),
		RepeatIndex: int32(es.repeatIndex),
	}
	for _, part := range strings.Split(line, "|") {
		if strings.HasPrefix(part, "job_id=") {
			m.TraceJobId = strings.TrimSpace(strings.TrimPrefix(part, "job_id="))
		} else if strings.HasPrefix(part, "trace_type=") {
			m.TraceType = strings.TrimSpace(strings.TrimPrefix(part, "trace_type="))
		}
	}
	if m.TraceJobId == "" {
		return nil
	}
	return m
}
