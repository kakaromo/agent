package trace

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"agent/adb"
	pb "agent/pb"

	"github.com/google/uuid"
)

// TraceJob represents a running trace session.
type TraceJob struct {
	mu           sync.Mutex
	ID           string
	DeviceID     string
	TraceType    string // "ufs", "block", "both"
	State        pb.JobState
	OutputDir    string
	LogFile      string
	TracingDir   string // /sys/kernel/tracing or /sys/kernel/debug/tracing
	StartedAt    int64
	FinishedAt   int64
	Error        string

	// internal
	adbCancel    context.CancelFunc
	adbCmd       *exec.Cmd
	logFd        *os.File
	subscribers  []chan *pb.JobProgress
	lastProgress []*pb.JobProgress
}

func (j *TraceJob) notify(progress *pb.JobProgress) {
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

func (j *TraceJob) closeSubscribers() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, ch := range j.subscribers {
		close(ch)
	}
	j.subscribers = nil
}

// Manager manages trace sessions.
type Manager struct {
	mu         sync.RWMutex
	jobs       map[string]*TraceJob
	adbMgr     *adb.Manager
	toolsDir   string
	outputBase string
}

func NewManager(adbMgr *adb.Manager, toolsDir, traceDir string) *Manager {
	return &Manager{
		jobs:       make(map[string]*TraceJob),
		adbMgr:     adbMgr,
		toolsDir:   toolsDir,
		outputBase: traceDir,
	}
}

// findTracingDir finds the tracing directory on the device.
// StartTrace begins trace log collection on a device.
func (m *Manager) StartTrace(ctx context.Context, req *pb.StartTraceRequest) (string, error) {
	md, err := m.adbMgr.GetDevice(req.DeviceId)
	if err != nil {
		return "", err
	}

	tracingDir := md.TracingDir
	if tracingDir == "" {
		return "", fmt.Errorf("tracing directory not found on device %s", req.DeviceId)
	}

	bgCtx := context.Background()

	jobID := uuid.New().String()
	outputDir := filepath.Join(m.outputBase, jobID)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir output: %w", err)
	}

	logFile := filepath.Join(outputDir, "trace.log")
	traceType := req.TraceType
	if traceType == "" {
		traceType = "ufs"
	}

	job := &TraceJob{
		ID:         jobID,
		DeviceID:   req.DeviceId,
		TraceType:  traceType,
		State:      pb.JobState_JOB_STATE_RUNNING,
		OutputDir:  outputDir,
		LogFile:    logFile,
		TracingDir: tracingDir,
		StartedAt:  time.Now().UnixMilli(),
	}

	m.mu.Lock()
	m.jobs[jobID] = job
	m.mu.Unlock()

	// Clear trace buffer before starting
	md.Device.Shell(bgCtx, fmt.Sprintf("echo 0 > %s/tracing_on", tracingDir))
	md.Device.Shell(bgCtx, fmt.Sprintf("echo 0 > %s/trace", tracingDir))

	// Enable selected events
	job.notify(&pb.JobProgress{
		JobId:    jobID,
		DeviceId: req.DeviceId,
		State:    pb.JobState_JOB_STATE_RUNNING,
		Message:  fmt.Sprintf("enabling %s events", traceType),
	})

	switch traceType {
	case "ufs":
		md.Device.Shell(bgCtx, fmt.Sprintf("echo 1 > %s/events/ufs/ufshcd_command/enable", tracingDir))
	case "block":
		md.Device.Shell(bgCtx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_issue/enable", tracingDir))
		md.Device.Shell(bgCtx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_complete/enable", tracingDir))
	case "both":
		md.Device.Shell(bgCtx, fmt.Sprintf("echo 1 > %s/events/ufs/ufshcd_command/enable", tracingDir))
		md.Device.Shell(bgCtx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_issue/enable", tracingDir))
		md.Device.Shell(bgCtx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_complete/enable", tracingDir))
	}

	// Start tracing
	md.Device.Shell(bgCtx, fmt.Sprintf("echo 1 > %s/tracing_on", tracingDir))

	// Start adb shell cat trace_pipe → log file
	adbCtx, adbCancel := context.WithCancel(bgCtx)
	logFd, err := os.Create(logFile)
	if err != nil {
		adbCancel()
		return "", fmt.Errorf("create log file: %w", err)
	}

	adbCmd := exec.CommandContext(adbCtx, "adb", "-s", md.Serial, "shell",
		fmt.Sprintf("cat %s/trace_pipe", tracingDir))
	adbCmd.Stdout = logFd
	if err := adbCmd.Start(); err != nil {
		logFd.Close()
		adbCancel()
		return "", fmt.Errorf("start trace_pipe: %w", err)
	}

	job.mu.Lock()
	job.adbCancel = adbCancel
	job.adbCmd = adbCmd
	job.logFd = logFd
	job.mu.Unlock()

	job.notify(&pb.JobProgress{
		JobId:    jobID,
		DeviceId: req.DeviceId,
		State:    pb.JobState_JOB_STATE_RUNNING,
		Message:  "trace collecting",
	})

	slog.Info("trace started", "job_id", jobID, "device", req.DeviceId, "type", traceType, "tracing_dir", tracingDir)

	// Wait for adb process in background
	go func() {
		adbCmd.Wait()
		logFd.Close()
	}()

	return jobID, nil
}

// StopTrace stops trace collection, then runs trace tool to generate parquet.
func (m *Manager) StopTrace(jobID string) error {
	job, err := m.GetJob(jobID)
	if err != nil {
		return err
	}

	job.mu.Lock()
	if job.State != pb.JobState_JOB_STATE_RUNNING {
		job.mu.Unlock()
		return fmt.Errorf("job not running: %s", jobID)
	}
	adbCancel := job.adbCancel
	deviceID := job.DeviceID
	tracingDir := job.TracingDir
	job.mu.Unlock()

	// Disable tracing on device
	if md, err := m.adbMgr.GetDevice(deviceID); err == nil {
		md.Device.Shell(context.Background(), fmt.Sprintf("echo 0 > %s/tracing_on", tracingDir))
		md.Device.Shell(context.Background(), fmt.Sprintf("echo 0 > %s/events/enable", tracingDir))
	}

	// Stop adb trace_pipe collection
	if adbCancel != nil {
		adbCancel()
	}

	// Wait for file to flush
	time.Sleep(500 * time.Millisecond)

	// Check log file size
	if fi, err := os.Stat(job.LogFile); err == nil {
		slog.Info("trace log collected", "job_id", jobID, "size_bytes", fi.Size())
	}

	job.notify(&pb.JobProgress{
		JobId:    jobID,
		DeviceId: deviceID,
		State:    pb.JobState_JOB_STATE_COLLECTING,
		Message:  "parsing trace log to parquet",
	})

	// Run trace tool: ./tools/trace --parquet-only <log_file> <output_prefix>
	traceBin := filepath.Join(m.toolsDir, "trace")
	outputPrefix := filepath.Join(job.OutputDir, "result")

	var stdout, stderr bytes.Buffer
	traceCmd := exec.Command(traceBin, "--parquet-only", job.LogFile, outputPrefix)
	traceCmd.Stdout = &stdout
	traceCmd.Stderr = &stderr

	slog.Info("running trace tool", "cmd", traceCmd.String())
	if err := traceCmd.Run(); err != nil {
		errMsg := fmt.Sprintf("trace tool failed: %v: %s", err, stderr.String())
		slog.Error(errMsg)
		m.failJob(job, errMsg)
		return fmt.Errorf(errMsg)
	}

	slog.Info("trace tool completed", "job_id", jobID)

	job.mu.Lock()
	job.State = pb.JobState_JOB_STATE_COMPLETED
	job.FinishedAt = time.Now().UnixMilli()
	job.mu.Unlock()

	job.notify(&pb.JobProgress{
		JobId:    jobID,
		DeviceId: deviceID,
		State:    pb.JobState_JOB_STATE_COMPLETED,
		Message:  "trace completed",
	})
	job.closeSubscribers()

	slog.Info("trace stopped", "job_id", jobID)
	return nil
}

func (m *Manager) failJob(job *TraceJob, errMsg string) {
	job.mu.Lock()
	job.State = pb.JobState_JOB_STATE_FAILED
	job.Error = errMsg
	job.FinishedAt = time.Now().UnixMilli()
	job.mu.Unlock()

	job.notify(&pb.JobProgress{
		JobId:    job.ID,
		DeviceId: job.DeviceID,
		State:    pb.JobState_JOB_STATE_FAILED,
		Error:    errMsg,
	})
	job.closeSubscribers()
}

// GetJob returns a trace job by ID.
func (m *Manager) GetJob(jobID string) (*TraceJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("trace job not found: %s", jobID)
	}
	return job, nil
}

// SubscribeProgress subscribes to trace job progress.
func (m *Manager) SubscribeProgress(jobID string) (chan *pb.JobProgress, error) {
	job, err := m.GetJob(jobID)
	if err != nil {
		return nil, err
	}
	ch := make(chan *pb.JobProgress, 256)

	job.mu.Lock()
	for _, p := range job.lastProgress {
		ch <- p
	}
	finished := job.State == pb.JobState_JOB_STATE_COMPLETED ||
		job.State == pb.JobState_JOB_STATE_FAILED
	if finished {
		close(ch)
	} else {
		job.subscribers = append(job.subscribers, ch)
	}
	job.mu.Unlock()

	return ch, nil
}

// GetParquetDir returns the output directory for a trace job's parquet files.
// Falls back to checking disk if job is not in memory (e.g. after agent restart).
func (m *Manager) GetParquetDir(jobID string) (string, error) {
	// Try memory first
	if job, err := m.GetJob(jobID); err == nil {
		return job.OutputDir, nil
	}
	// Fallback: check if directory exists on disk
	dir := filepath.Join(m.outputBase, jobID)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return dir, nil
	}
	return "", fmt.Errorf("trace job not found: %s", jobID)
}

// DeleteJob deletes a completed/failed trace job and its output files.
func (m *Manager) DeleteJob(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check memory for running job
	if job, ok := m.jobs[jobID]; ok {
		job.mu.Lock()
		state := job.State
		job.mu.Unlock()
		if state == pb.JobState_JOB_STATE_RUNNING {
			return fmt.Errorf("cannot delete running trace job: %s", jobID)
		}
		delete(m.jobs, jobID)
	}

	// Remove output directory (works even if job not in memory)
	dir := filepath.Join(m.outputBase, jobID)
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("failed to remove trace dir: %w", err)
		}
	}

	slog.Info("trace job deleted", "job_id", jobID)
	return nil
}
