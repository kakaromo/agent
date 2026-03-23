package benchmark

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agent/adb"
	pb "agent/pb"

	"github.com/google/uuid"
)

const remoteToolDir = "/data/local/tmp"

// Job represents a benchmark job across one or more devices.
type Job struct {
	mu             sync.Mutex
	ID             string
	Name           string
	Tool           pb.BenchmarkTool
	Params         map[string]string
	State          pb.JobState
	DeviceStatuses map[string]*pb.DeviceJobStatus
	Results        map[string]*pb.BenchmarkResult
	subscribers    []chan *pb.JobProgress
	lastProgress   []*pb.JobProgress // history of all progress messages
}

func (j *Job) addSubscriber(ch chan *pb.JobProgress) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.subscribers = append(j.subscribers, ch)
}

func (j *Job) notify(progress *pb.JobProgress) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lastProgress = append(j.lastProgress, progress)
	for _, ch := range j.subscribers {
		select {
		case ch <- progress:
		default:
		}
	}
}

func (j *Job) closeSubscribers() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, ch := range j.subscribers {
		close(ch)
	}
	j.subscribers = nil
}

// TraceController interface to avoid circular imports with trace package.
type TraceController interface {
	StartTrace(ctx context.Context, req *pb.StartTraceRequest) (string, error)
	StopTrace(jobID string) error
}

// Orchestrator manages benchmark job execution.
type Orchestrator struct {
	mu       sync.RWMutex
	jobs     map[string]*Job
	manager  *adb.Manager
	toolsDir string
	traceMgr TraceController
}

func NewOrchestrator(manager *adb.Manager, toolsDir string) *Orchestrator {
	return &Orchestrator{
		jobs:     make(map[string]*Job),
		manager:  manager,
		toolsDir: toolsDir,
	}
}

// SetTraceController sets the trace controller for scenario trace_start/trace_stop steps.
func (o *Orchestrator) SetTraceController(tc TraceController) {
	o.traceMgr = tc
}

// RunBenchmark starts a new benchmark job and returns immediately with a job ID.
func (o *Orchestrator) RunBenchmark(ctx context.Context, req *pb.RunBenchmarkRequest) (string, error) {
	deviceIDs := req.DeviceIds
	if len(deviceIDs) == 0 {
		deviceIDs = o.manager.GetOnlineDevices()
	}
	if len(deviceIDs) == 0 {
		return "", fmt.Errorf("no online devices available")
	}

	jobID := uuid.New().String()
	job := &Job{
		ID:             jobID,
		Name:           req.JobName,
		Tool:           req.Tool,
		Params:         req.Params,
		State:          pb.JobState_JOB_STATE_QUEUED,
		DeviceStatuses: make(map[string]*pb.DeviceJobStatus),
		Results:        make(map[string]*pb.BenchmarkResult),
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

	// Use background context so the job outlives the RPC call
	go o.executeJob(context.Background(), job, deviceIDs)
	return jobID, nil
}

func (o *Orchestrator) executeJob(ctx context.Context, job *Job, deviceIDs []string) {
	defer job.closeSubscribers()

	var wg sync.WaitGroup
	for _, deviceID := range deviceIDs {
		wg.Add(1)
		go func(devID string) {
			defer wg.Done()
			o.runOnDevice(ctx, job, devID)
		}(deviceID)
	}
	wg.Wait()

	// Determine overall job state
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

	slog.Info("job finished", "job_id", job.ID, "state", job.State, "completed", completed, "failed", failed)
}

func (o *Orchestrator) runOnDevice(ctx context.Context, job *Job, deviceID string) {
	md, err := o.manager.GetDevice(deviceID)
	if err != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, err.Error(), 0)
		return
	}

	o.manager.SetState(deviceID, pb.DeviceState_DEVICE_STATE_BUSY)
	defer o.manager.SetState(deviceID, pb.DeviceState_DEVICE_STATE_ONLINE)

	startedAt := time.Now().UnixMilli()

	// Push tool
	toolName := toolNameFor(job.Tool)
	if toolName == "" {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, "unknown tool", 0)
		return
	}

	o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_PUSHING_TOOLS, "pushing "+toolName, 10)

	localPath := filepath.Join(o.toolsDir, toolName)
	remotePath := remoteToolDir + "/" + toolName

	if err := pushToolIfNeeded(ctx, md.Device, localPath, remotePath); err != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, "push failed: "+err.Error(), 10)
		return
	}

	if _, err := md.Device.Shell(ctx, "chmod 755 "+remotePath); err != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, "chmod failed: "+err.Error(), 15)
		return
	}

	// Ensure test directory exists
	if _, err := md.Device.Shell(ctx, "mkdir -p /data/local/tmp/test"); err != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, "mkdir failed: "+err.Error(), 15)
		return
	}

	// Run benchmark
	o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_RUNNING, "running "+toolName, 20)

	cmdStr := buildCommand(job.Tool, remotePath, job.Params)
	out, err := md.Device.Shell(ctx, cmdStr)
	if err != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, "execution failed: "+err.Error(), 50)
		o.storeResult(job, deviceID, startedAt, "", nil, false, err.Error())
		return
	}

	// Collect results
	o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_COLLECTING, "parsing results", 80)

	metrics := parseResults(job.Tool, out)
	o.storeResult(job, deviceID, startedAt, out, metrics, true, "")

	o.updateDeviceStatusWithResult(job, deviceID, pb.JobState_JOB_STATE_COMPLETED, "done", 100, metrics, out)
}

func (o *Orchestrator) updateDeviceStatus(job *Job, deviceID string, state pb.JobState, msg string, progress int32) {
	o.updateDeviceStatusWithResult(job, deviceID, state, msg, progress, nil, "")
}

func (o *Orchestrator) updateDeviceStatusWithResult(job *Job, deviceID string, state pb.JobState, msg string, progress int32, metrics map[string]float64, rawOutput string) {
	job.mu.Lock()
	if ds, ok := job.DeviceStatuses[deviceID]; ok {
		ds.State = state
		ds.Message = msg
		ds.ProgressPercent = progress
	}
	job.mu.Unlock()

	job.notify(&pb.JobProgress{
		JobId:           job.ID,
		DeviceId:        deviceID,
		State:           state,
		Message:         msg,
		ProgressPercent: progress,
		Metrics:         metrics,
		RawOutput:       rawOutput,
	})
}

func (o *Orchestrator) storeResult(job *Job, deviceID string, startedAt int64, rawOutput string, metrics map[string]float64, success bool, errMsg string) {
	result := &pb.BenchmarkResult{
		DeviceId:   deviceID,
		Tool:       job.Tool,
		RawOutput:  rawOutput,
		Metrics:    metrics,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UnixMilli(),
		Success:    success,
		Error:      errMsg,
	}
	job.mu.Lock()
	job.Results[deviceID] = result
	job.mu.Unlock()
}

// GetJob returns a job by ID.
func (o *Orchestrator) GetJob(jobID string) (*Job, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	job, ok := o.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	return job, nil
}

// DeleteJob deletes a completed/failed job. Returns error if job is still running.
func (o *Orchestrator) DeleteJob(jobID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	job, ok := o.jobs[jobID]
	if !ok {
		return fmt.Errorf("job not found: %s", jobID)
	}
	job.mu.Lock()
	state := job.State
	job.mu.Unlock()
	if state == pb.JobState_JOB_STATE_QUEUED || state == pb.JobState_JOB_STATE_RUNNING ||
		state == pb.JobState_JOB_STATE_PUSHING_TOOLS || state == pb.JobState_JOB_STATE_COLLECTING {
		return fmt.Errorf("cannot delete running job: %s", jobID)
	}
	delete(o.jobs, jobID)
	return nil
}

// SubscribeJobProgress returns a channel that receives progress updates.
// Replays all past progress messages first, then streams new ones.
func (o *Orchestrator) SubscribeJobProgress(jobID string) (chan *pb.JobProgress, error) {
	job, err := o.GetJob(jobID)
	if err != nil {
		return nil, err
	}
	ch := make(chan *pb.JobProgress, 256)

	job.mu.Lock()
	// Replay history
	for _, p := range job.lastProgress {
		ch <- p
	}
	// If job already finished, close immediately after replay
	finished := job.State == pb.JobState_JOB_STATE_COMPLETED ||
		job.State == pb.JobState_JOB_STATE_FAILED ||
		job.State == pb.JobState_JOB_STATE_PARTIALLY_FAILED
	if finished {
		close(ch)
	} else {
		job.subscribers = append(job.subscribers, ch)
	}
	job.mu.Unlock()

	return ch, nil
}

// GetJobStatus returns the current status of a job.
func (o *Orchestrator) GetJobStatus(jobID string) (*pb.GetJobStatusResponse, error) {
	job, err := o.GetJob(jobID)
	if err != nil {
		return nil, err
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	resp := &pb.GetJobStatusResponse{
		JobId:        job.ID,
		State:        job.State,
		TotalDevices: int32(len(job.DeviceStatuses)),
	}

	for _, ds := range job.DeviceStatuses {
		resp.DeviceStatuses = append(resp.DeviceStatuses, ds)
		switch ds.State {
		case pb.JobState_JOB_STATE_COMPLETED:
			resp.CompletedDevices++
		case pb.JobState_JOB_STATE_FAILED:
			resp.FailedDevices++
		}
	}
	return resp, nil
}

// GetBenchmarkResults returns results for a job, optionally filtered by device.
func (o *Orchestrator) GetBenchmarkResults(jobID, deviceID string) ([]*pb.BenchmarkResult, error) {
	job, err := o.GetJob(jobID)
	if err != nil {
		return nil, err
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	var results []*pb.BenchmarkResult
	if deviceID != "" {
		if r, ok := job.Results[deviceID]; ok {
			results = append(results, r)
		}
	} else {
		for _, r := range job.Results {
			results = append(results, r)
		}
	}
	return results, nil
}

// ==================== Helpers ====================

func toolNameFor(tool pb.BenchmarkTool) string {
	switch tool {
	case pb.BenchmarkTool_BENCHMARK_TOOL_FIO:
		return "fio"
	case pb.BenchmarkTool_BENCHMARK_TOOL_IOZONE:
		return "iozone"
	case pb.BenchmarkTool_BENCHMARK_TOOL_TIOTEST:
		return "tiotest"
	default:
		return ""
	}
}

func pushToolIfNeeded(ctx context.Context, dev *adb.Device, localPath, remotePath string) error {
	// Check if tool already exists on device
	out, err := dev.Shell(ctx, "ls "+remotePath+" 2>/dev/null && echo EXISTS")
	if err == nil && strings.Contains(out, "EXISTS") {
		return nil
	}
	return dev.Push(ctx, localPath, remotePath)
}

func buildCommand(tool pb.BenchmarkTool, remotePath string, params map[string]string) string {
	switch tool {
	case pb.BenchmarkTool_BENCHMARK_TOOL_FIO:
		return buildFioCommand(remotePath, params)
	case pb.BenchmarkTool_BENCHMARK_TOOL_IOZONE:
		return buildIozoneCommand(remotePath, params)
	case pb.BenchmarkTool_BENCHMARK_TOOL_TIOTEST:
		return buildTiotestCommand(remotePath, params)
	default:
		return remotePath
	}
}

func parseResults(tool pb.BenchmarkTool, output string) map[string]float64 {
	switch tool {
	case pb.BenchmarkTool_BENCHMARK_TOOL_FIO:
		return parseFioResults(output)
	case pb.BenchmarkTool_BENCHMARK_TOOL_IOZONE:
		return parseIozoneResults(output)
	case pb.BenchmarkTool_BENCHMARK_TOOL_TIOTEST:
		return parseTiotestResults(output)
	default:
		return nil
	}
}
