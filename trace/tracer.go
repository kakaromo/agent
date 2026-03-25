package trace

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agent/adb"
	pb "agent/pb"

	"github.com/google/uuid"
)

// TraceJob represents a running trace session.
type TraceJob struct {
	Mu           sync.Mutex
	ID           string
	DeviceID     string
	TraceType    string // "ufs", "block", "both"
	State        pb.JobState
	OutputDir    string // base dir for this job
	RealtimeDir  string // realtime parquet output dir
	LogFile      string
	TracingDir   string
	TraceGrpcJobID string // job_id from trace gRPC server (for --client stop)
	StartedAt    int64
	FinishedAt   int64
	Error        string

	// internal processes
	adbCancel      context.CancelFunc
	adbCmd         *exec.Cmd
	logFd          *os.File
	realtimeCmd    *exec.Cmd
	realtimeCancel context.CancelFunc
	subscribers    []chan *pb.JobProgress
	lastProgress   []*pb.JobProgress
}

func (j *TraceJob) notify(progress *pb.JobProgress) {
	j.Mu.Lock()
	defer j.Mu.Unlock()
	j.lastProgress = append(j.lastProgress, progress)
	for _, ch := range j.subscribers {
		select {
		case ch <- progress:
		default:
		}
	}
}

func (j *TraceJob) closeSubscribers() {
	j.Mu.Lock()
	defer j.Mu.Unlock()
	for _, ch := range j.subscribers {
		close(ch)
	}
	j.subscribers = nil
}

// Manager manages trace sessions.
type Manager struct {
	mu            sync.RWMutex
	jobs          map[string]*TraceJob
	adbMgr        *adb.Manager
	toolsDir      string
	outputBase    string
	traceGrpcPort int
	traceServer   *exec.Cmd
	traceServerCancel context.CancelFunc
}

func NewManager(adbMgr *adb.Manager, toolsDir, traceDir string, traceGrpcPort int) *Manager {
	return &Manager{
		jobs:          make(map[string]*TraceJob),
		adbMgr:        adbMgr,
		toolsDir:      toolsDir,
		outputBase:    traceDir,
		traceGrpcPort: traceGrpcPort,
	}
}

// StartTraceServer starts the trace gRPC server as a background process.
func (m *Manager) StartTraceServer() error {
	traceBin := filepath.Join(m.toolsDir, "trace")
	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, traceBin,
		"--grpc-server",
		"--port", fmt.Sprintf("%d", m.traceGrpcPort),
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start trace grpc server: %w", err)
	}

	m.traceServer = cmd
	m.traceServerCancel = cancel

	time.Sleep(1 * time.Second)

	slog.Info("trace gRPC server started", "port", m.traceGrpcPort, "pid", cmd.Process.Pid)
	return nil
}

// StopTraceServer stops the trace gRPC server.
func (m *Manager) StopTraceServer() {
	if m.traceServerCancel != nil {
		m.traceServerCancel()
	}
	if m.traceServer != nil {
		m.traceServer.Wait()
	}
	slog.Info("trace gRPC server stopped")
}

// StartTrace begins trace collection + realtime parsing on a device.
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
	realtimeDir := filepath.Join(outputDir, "realtime")
	if err := os.MkdirAll(realtimeDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir output: %w", err)
	}

	logFile := filepath.Join(outputDir, "trace.log")
	traceType := req.TraceType
	if traceType == "" {
		traceType = "ufs"
	}
	windowSec := req.WindowSeconds
	if windowSec <= 0 {
		windowSec = 1
	}

	job := &TraceJob{
		ID:          jobID,
		DeviceID:    req.DeviceId,
		TraceType:   traceType,
		State:       pb.JobState_JOB_STATE_RUNNING,
		OutputDir:   outputDir,
		RealtimeDir: realtimeDir,
		LogFile:     logFile,
		TracingDir:  tracingDir,
		StartedAt:   time.Now().UnixMilli(),
	}

	m.mu.Lock()
	m.jobs[jobID] = job
	m.mu.Unlock()

	// Stop tracing and clear trace buffer
	md.Device.Shell(bgCtx, fmt.Sprintf("echo 0 > %s/tracing_on", tracingDir))
	md.Device.Shell(bgCtx, fmt.Sprintf("echo > %s/trace", tracingDir))

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

	// Wait for log data to start flowing
	time.Sleep(500 * time.Millisecond)

	// Start realtime parsing via trace CLI client
	realtimeCtx, realtimeCancel := context.WithCancel(bgCtx)
	traceBin := filepath.Join(m.toolsDir, "trace")
	realtimeCmd := exec.CommandContext(realtimeCtx, traceBin,
		"--client", "realtime",
		"--server", fmt.Sprintf("localhost:%d", m.traceGrpcPort),
		"--source-path", logFile,
		"--output-dir", realtimeDir,
		"--log-type", traceType,
		"--window", fmt.Sprintf("%d", windowSec),
	)

	// Capture stdout to parse trace gRPC job_id
	realtimeStdout, err := realtimeCmd.StdoutPipe()
	if err != nil {
		adbCancel()
		logFd.Close()
		realtimeCancel()
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	realtimeCmd.Stderr = io.Discard

	if err := realtimeCmd.Start(); err != nil {
		adbCancel()
		logFd.Close()
		realtimeCancel()
		return "", fmt.Errorf("start realtime parsing: %w", err)
	}

	job.Mu.Lock()
	job.adbCancel = adbCancel
	job.adbCmd = adbCmd
	job.logFd = logFd
	job.realtimeCmd = realtimeCmd
	job.realtimeCancel = realtimeCancel
	job.Mu.Unlock()

	// Parse trace gRPC job_id from realtime client stdout in background
	go func() {
		scanner := bufio.NewScanner(realtimeStdout)
		for scanner.Scan() {
			line := scanner.Text()
			// Output format: [파일 #0001] /path/realtime_000001.parquet (52340 events, ...) job_id=xxx
			if idx := strings.Index(line, "job_id="); idx >= 0 {
				traceJobID := strings.TrimSpace(line[idx+7:])
				job.Mu.Lock()
				if job.TraceGrpcJobID == "" {
					job.TraceGrpcJobID = traceJobID
					slog.Info("captured trace gRPC job_id", "agent_job", jobID, "trace_job", traceJobID)
				}
				job.Mu.Unlock()
			}
		}
	}()

	job.notify(&pb.JobProgress{
		JobId:    jobID,
		DeviceId: req.DeviceId,
		State:    pb.JobState_JOB_STATE_RUNNING,
		Message:  "trace collecting + realtime parsing",
	})

	slog.Info("trace started", "job_id", jobID, "device", req.DeviceId, "type", traceType,
		"realtime_dir", realtimeDir, "window", windowSec)

	// Wait for adb process in background
	go func() {
		adbCmd.Wait()
		logFd.Close()
	}()

	return jobID, nil
}

// StopTrace stops trace collection and gracefully stops realtime parsing.
func (m *Manager) StopTrace(jobID string) error {
	job, err := m.GetJob(jobID)
	if err != nil {
		return err
	}

	job.Mu.Lock()
	if job.State != pb.JobState_JOB_STATE_RUNNING {
		job.Mu.Unlock()
		return fmt.Errorf("job not running: %s", jobID)
	}
	adbCancel := job.adbCancel
	realtimeCancel := job.realtimeCancel
	deviceID := job.DeviceID
	tracingDir := job.TracingDir
	traceGrpcJobID := job.TraceGrpcJobID
	job.Mu.Unlock()

	// 1. Disable tracing on device
	if md, err := m.adbMgr.GetDevice(deviceID); err == nil {
		md.Device.Shell(context.Background(), fmt.Sprintf("echo 0 > %s/tracing_on", tracingDir))
		md.Device.Shell(context.Background(), fmt.Sprintf("echo 0 > %s/events/enable", tracingDir))
	}

	// 2. Stop adb trace_pipe (so realtime parser gets no more data)
	if adbCancel != nil {
		adbCancel()
	}

	// Wait for remaining data to flush to log file
	time.Sleep(1 * time.Second)

	// 3. Gracefully stop realtime parsing via trace --client stop
	if traceGrpcJobID != "" {
		traceBin := filepath.Join(m.toolsDir, "trace")
		stopCmd := exec.Command(traceBin,
			"--client", "stop",
			"--server", fmt.Sprintf("localhost:%d", m.traceGrpcPort),
			"--job-id", traceGrpcJobID,
		)
		stopCmd.Stdout = io.Discard
		stopCmd.Stderr = io.Discard
		if err := stopCmd.Run(); err != nil {
			slog.Warn("trace --client stop failed, forcing", "error", err, "trace_job", traceGrpcJobID)
			// Fallback: force kill
			if realtimeCancel != nil {
				realtimeCancel()
			}
		} else {
			slog.Info("trace realtime parsing stopped gracefully", "trace_job", traceGrpcJobID)
		}
	} else {
		// No trace gRPC job_id captured, force kill
		slog.Warn("no trace gRPC job_id, forcing realtime parser stop")
		if realtimeCancel != nil {
			realtimeCancel()
		}
	}

	// Wait for realtime parser to finish writing final parquet
	time.Sleep(1 * time.Second)

	// Delete raw trace log (large file, parquet is sufficient)
	if fi, err := os.Stat(job.LogFile); err == nil {
		slog.Info("deleting trace log", "job_id", jobID, "size_bytes", fi.Size())
		os.Remove(job.LogFile)
	}

	job.Mu.Lock()
	job.State = pb.JobState_JOB_STATE_COMPLETED
	job.FinishedAt = time.Now().UnixMilli()
	job.Mu.Unlock()

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
	job.Mu.Lock()
	job.State = pb.JobState_JOB_STATE_FAILED
	job.Error = errMsg
	job.FinishedAt = time.Now().UnixMilli()
	job.Mu.Unlock()

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

	job.Mu.Lock()
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
	job.Mu.Unlock()

	return ch, nil
}

// TraceJobInfo holds directory and type info for parquet file lookup.
type TraceJobInfo struct {
	Dir       string
	TraceType string // "ufs", "block", "both"
}

// GetTraceJobInfo returns the parquet directory and trace type for a job.
func (m *Manager) GetTraceJobInfo(jobID string) (*TraceJobInfo, error) {
	// Try memory first
	if job, err := m.GetJob(jobID); err == nil {
		return &TraceJobInfo{Dir: job.RealtimeDir, TraceType: job.TraceType}, nil
	}
	// Fallback: check realtime dir on disk (type unknown, use "both" to read all)
	dir := filepath.Join(m.outputBase, jobID, "realtime")
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return &TraceJobInfo{Dir: dir, TraceType: "both"}, nil
	}
	// Fallback: check base dir (for old --parquet-only jobs)
	dir = filepath.Join(m.outputBase, jobID)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return &TraceJobInfo{Dir: dir, TraceType: "both"}, nil
	}
	return nil, fmt.Errorf("trace job not found: %s", jobID)
}

// DeleteJob deletes a completed/failed trace job and its output files.
func (m *Manager) DeleteJob(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if job, ok := m.jobs[jobID]; ok {
		job.Mu.Lock()
		state := job.State
		job.Mu.Unlock()
		if state == pb.JobState_JOB_STATE_RUNNING {
			return fmt.Errorf("cannot delete running trace job: %s", jobID)
		}
		delete(m.jobs, jobID)
	}

	dir := filepath.Join(m.outputBase, jobID)
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("failed to remove trace dir: %w", err)
		}
	}

	slog.Info("trace job deleted", "job_id", jobID)
	return nil
}
