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
	"sort"
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

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer setupCancel()

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
	md.Device.Shell(setupCtx, fmt.Sprintf("echo 0 > %s/tracing_on", tracingDir))
	md.Device.Shell(setupCtx, fmt.Sprintf("echo > %s/trace", tracingDir))

	// Enable selected events
	job.notify(&pb.JobProgress{
		JobId:    jobID,
		DeviceId: req.DeviceId,
		State:    pb.JobState_JOB_STATE_RUNNING,
		Message:  fmt.Sprintf("enabling %s events", traceType),
	})

	switch traceType {
	case "ufs":
		md.Device.Shell(setupCtx, fmt.Sprintf("echo 1 > %s/events/ufs/ufshcd_command/enable", tracingDir))
	case "block":
		md.Device.Shell(setupCtx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_issue/enable", tracingDir))
		md.Device.Shell(setupCtx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_complete/enable", tracingDir))
	case "both":
		md.Device.Shell(setupCtx, fmt.Sprintf("echo 1 > %s/events/ufs/ufshcd_command/enable", tracingDir))
		md.Device.Shell(setupCtx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_issue/enable", tracingDir))
		md.Device.Shell(setupCtx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_complete/enable", tracingDir))
	}

	// Start tracing
	md.Device.Shell(setupCtx, fmt.Sprintf("echo 1 > %s/tracing_on", tracingDir))

	// Start adb shell cat trace_pipe → log file
	adbCtx, adbCancel := context.WithCancel(context.Background())
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
	realtimeCtx, realtimeCancel := context.WithCancel(context.Background())
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

// StopTrace stops trace collection. Device tracing off + adb kill은 동기,
// parquet 병합/정리는 백그라운드로 처리하여 즉시 리턴.
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

	const shellTimeout = 10 * time.Second

	// 1. Disable tracing on device (동기, 타임아웃)
	if md, err := m.adbMgr.GetDevice(deviceID); err == nil {
		shellCtx, shellCancel := context.WithTimeout(context.Background(), shellTimeout)
		md.Device.Shell(shellCtx, fmt.Sprintf("echo 0 > %s/tracing_on", tracingDir))
		md.Device.Shell(shellCtx, fmt.Sprintf("echo 0 > %s/events/enable", tracingDir))
		shellCancel()
	}

	// 2. Stop adb trace_pipe (동기)
	if adbCancel != nil {
		adbCancel()
	}

	// 상태를 COLLECTING으로 변경 (분석은 가능하지만 아직 정리 중)
	job.Mu.Lock()
	job.State = pb.JobState_JOB_STATE_COLLECTING
	job.Mu.Unlock()

	slog.Info("trace collection stopped, finalizing in background", "job_id", jobID)

	// 3. 나머지는 백그라운드에서 처리 (parquet 병합, 로그 삭제)
	go m.finalizeTrace(job, jobID, deviceID, traceGrpcJobID, realtimeCancel)

	return nil
}

// finalizeTrace handles parquet merge and cleanup in background.
func (m *Manager) finalizeTrace(job *TraceJob, jobID, deviceID, traceGrpcJobID string, realtimeCancel context.CancelFunc) {
	const stopTimeout = 10 * time.Second

	// flush 대기
	time.Sleep(1 * time.Second)

	// graceful stop realtime parser
	if traceGrpcJobID != "" {
		traceBin := filepath.Join(m.toolsDir, "trace")
		stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTimeout)

		stopCmd := exec.CommandContext(stopCtx, traceBin,
			"--client", "stop",
			"--server", fmt.Sprintf("localhost:%d", m.traceGrpcPort),
			"--job-id", traceGrpcJobID,
		)
		stopCmd.Stdout = io.Discard
		stopCmd.Stderr = io.Discard

		done := make(chan error, 1)
		go func() { done <- stopCmd.Run() }()

		select {
		case err := <-done:
			if err != nil {
				slog.Warn("trace --client stop failed", "error", err, "trace_job", traceGrpcJobID)
			} else {
				slog.Info("trace realtime parsing stopped gracefully", "trace_job", traceGrpcJobID)
			}
		case <-stopCtx.Done():
			slog.Warn("trace --client stop timed out, forcing", "timeout", stopTimeout, "trace_job", traceGrpcJobID)
			if stopCmd.Process != nil {
				stopCmd.Process.Kill()
			}
		}
		stopCancel()
	}

	// force kill if still running
	if realtimeCancel != nil {
		realtimeCancel()
	}

	// parquet 병합 대기
	time.Sleep(2 * time.Second)

	// trace.log 보존 (reparsing 용)
	if fi, err := os.Stat(job.LogFile); err == nil {
		slog.Info("trace log preserved for reparse", "job_id", jobID, "size_bytes", fi.Size())
	}

	// 완료 상태
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

	slog.Info("trace finalized", "job_id", jobID)
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

// ArchiveParquetEntry — archive 흐름에서 portal 에 보낼 realtime parquet 파일 1개의 메타.
type ArchiveParquetEntry struct {
	LocalPath string // 절대 경로
	TraceType string // "ufs" | "block" | "ufscustom" — 파일명 prefix 로 판별
	Size      int64
	BaseName  string // 정렬 + portal 키 매핑용 (e.g. "realtime_ufs_001.parquet")
}

// GetArchiveFiles — Trace Archive 흐름에서 업로드할 파일 목록을 반환한다.
//
// 반환:
//   - rawPath: trace.log 절대 경로 (없으면 에러)
//   - rawSize: trace.log 바이트 수
//   - parquetFiles: realtime parquet 파일 (시퀀스 번호 순으로 정렬됨 → 시간순과 일치하도록 agent 가 부여)
//
// 정렬 정책: 파일명 lexicographic = 시퀀스 번호 순 = 시간순 (Portal 의 read_parquet 호출자 정렬 책임).
func (m *Manager) GetArchiveFiles(jobID string) (rawPath string, rawSize int64, parquetFiles []ArchiveParquetEntry, err error) {
	info, ierr := m.GetTraceJobInfo(jobID)
	if ierr != nil {
		err = ierr
		return
	}

	// trace.log: realtime dir 의 부모 = base dir 안에 있음. dir 이 base 일 수도 있어 두 케이스 다 시도.
	candidates := []string{
		filepath.Join(filepath.Dir(info.Dir), "trace.log"),
		filepath.Join(info.Dir, "trace.log"),
		filepath.Join(m.outputBase, jobID, "trace.log"),
	}
	var logPath string
	var logFi os.FileInfo
	for _, c := range candidates {
		if fi, e := os.Stat(c); e == nil && !fi.IsDir() {
			logPath = c
			logFi = fi
			break
		}
	}
	if logPath == "" {
		err = fmt.Errorf("trace.log not found for job %s (tried: %v)", jobID, candidates)
		return
	}
	rawPath = logPath
	rawSize = logFi.Size()

	// realtime parquet 수집
	entries, rerr := os.ReadDir(info.Dir)
	if rerr != nil {
		err = fmt.Errorf("read realtime dir %s: %w", info.Dir, rerr)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".parquet") {
			continue
		}
		name := e.Name()
		traceType := detectTraceTypeFromFilename(name)
		fp := filepath.Join(info.Dir, name)
		fi, ferr := os.Stat(fp)
		if ferr != nil {
			continue
		}
		parquetFiles = append(parquetFiles, ArchiveParquetEntry{
			LocalPath: fp,
			TraceType: traceType,
			Size:      fi.Size(),
			BaseName:  name,
		})
	}
	sort.Slice(parquetFiles, func(i, j int) bool {
		return parquetFiles[i].BaseName < parquetFiles[j].BaseName
	})
	return
}

// detectTraceTypeFromFilename — "realtime_ufs_001.parquet" / "realtime_block_..." / "realtime_ufscustom_..."
// 패턴이 아니면 "unknown" 반환 (호출자가 거부).
func detectTraceTypeFromFilename(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "_ufscustom_"):
		return "ufscustom"
	case strings.Contains(lower, "_ufs_"):
		return "ufs"
	case strings.Contains(lower, "_block_"):
		return "block"
	}
	return "unknown"
}

// DeleteJob deletes a completed/failed trace job and its output files.
func (m *Manager) DeleteJob(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if job, ok := m.jobs[jobID]; ok {
		job.Mu.Lock()
		state := job.State
		job.Mu.Unlock()
		if state == pb.JobState_JOB_STATE_RUNNING || state == pb.JobState_JOB_STATE_REPARSING {
			return fmt.Errorf("cannot delete %s trace job: %s", state.String(), jobID)
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

// ReparseTrace re-parses a completed trace job's raw log file to regenerate parquet files.
func (m *Manager) ReparseTrace(jobID string) error {
	m.mu.Lock()
	job, ok := m.jobs[jobID]
	m.mu.Unlock()

	if !ok {
		// 디스크에서 복원 시도
		baseDir := filepath.Join(m.outputBase, jobID)
		logFile := filepath.Join(baseDir, "trace.log")
		realtimeDir := filepath.Join(baseDir, "realtime")

		if _, err := os.Stat(logFile); err != nil {
			return fmt.Errorf("trace.log not found for job %s — cannot reparse", jobID)
		}
		if _, err := os.Stat(realtimeDir); err != nil {
			os.MkdirAll(realtimeDir, 0755)
		}

		job = &TraceJob{
			ID:          jobID,
			State:       pb.JobState_JOB_STATE_COMPLETED,
			OutputDir:   baseDir,
			RealtimeDir: realtimeDir,
			LogFile:     logFile,
			TraceType:   "both",
		}
		m.mu.Lock()
		m.jobs[jobID] = job
		m.mu.Unlock()
	}

	job.Mu.Lock()
	state := job.State
	job.Mu.Unlock()

	switch state {
	case pb.JobState_JOB_STATE_RUNNING, pb.JobState_JOB_STATE_COLLECTING, pb.JobState_JOB_STATE_REPARSING:
		return fmt.Errorf("job %s is in state %s, cannot reparse", jobID, state.String())
	}

	// trace.log 존재 확인
	if _, err := os.Stat(job.LogFile); err != nil {
		return fmt.Errorf("trace.log not found: %s", job.LogFile)
	}

	// 상태 전이
	job.Mu.Lock()
	job.State = pb.JobState_JOB_STATE_REPARSING
	job.FinishedAt = 0
	job.Error = ""
	job.subscribers = nil
	job.Mu.Unlock()

	slog.Info("starting trace reparse", "job_id", jobID, "log_file", job.LogFile)
	go m.doReparse(job)
	return nil
}

func (m *Manager) doReparse(job *TraceJob) {
	jobID := job.ID

	job.notify(&pb.JobProgress{
		JobId:   jobID,
		State:   pb.JobState_JOB_STATE_REPARSING,
		Message: "reparse started",
	})

	// 1. 기존 parquet 삭제
	entries, _ := os.ReadDir(job.RealtimeDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".parquet") {
			os.Remove(filepath.Join(job.RealtimeDir, e.Name()))
		}
	}
	slog.Info("cleared old parquet files", "job_id", jobID, "dir", job.RealtimeDir)

	// 2. trace --parquet-only 실행
	// 사용법: ./trace <log_file> <output_prefix> --parquet-only
	traceBin := filepath.Join(m.toolsDir, "trace")
	outputPrefix := filepath.Join(job.RealtimeDir, "realtime")
	cmd := exec.Command(traceBin, job.LogFile, outputPrefix, "--parquet-only")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.failReparse(job, fmt.Sprintf("stdout pipe: %v", err))
		return
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		m.failReparse(job, fmt.Sprintf("start trace: %v", err))
		return
	}

	// stdout 파싱으로 진행률 보고
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			slog.Debug("reparse output", "job_id", jobID, "line", line)
			job.notify(&pb.JobProgress{
				JobId:   jobID,
				State:   pb.JobState_JOB_STATE_REPARSING,
				Message: line,
			})
		}
	}()

	if err := cmd.Wait(); err != nil {
		m.failReparse(job, fmt.Sprintf("trace reparse failed: %v", err))
		return
	}

	// 3. 완료
	job.Mu.Lock()
	job.State = pb.JobState_JOB_STATE_COMPLETED
	job.FinishedAt = time.Now().UnixMilli()
	job.Mu.Unlock()

	job.notify(&pb.JobProgress{
		JobId:   jobID,
		State:   pb.JobState_JOB_STATE_COMPLETED,
		Message: "reparse completed",
	})

	slog.Info("trace reparse completed", "job_id", jobID)
}

func (m *Manager) failReparse(job *TraceJob, errMsg string) {
	slog.Error("trace reparse failed", "job_id", job.ID, "error", errMsg)
	job.Mu.Lock()
	job.State = pb.JobState_JOB_STATE_FAILED
	job.Error = errMsg
	job.FinishedAt = time.Now().UnixMilli()
	job.Mu.Unlock()

	job.notify(&pb.JobProgress{
		JobId:   job.ID,
		State:   pb.JobState_JOB_STATE_FAILED,
		Message: errMsg,
		Error:   errMsg,
	})
}
