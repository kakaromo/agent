package trace

import (
	"bufio"
	"context"
	"fmt"
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
	"agent/trace/parser"

	"github.com/google/uuid"
)

// TraceJob represents a running trace session.
//
// 흐름: StartTrace 가 ftrace 이벤트 enable + adb cat trace_pipe → trace.log append 를 시작.
// StopTrace 가 tracing 중지 + adb 종료 후 백그라운드에서 parquet-only 일괄 파싱 1회 실행
// (실시간 윈도우 분할은 폐기됨 → result_*.parquet 단일 생성). ReparseTrace 는 같은
// parquet-only 흐름을 보존된 trace.log 로 재실행한다.
type TraceJob struct {
	Mu         sync.Mutex
	ID         string
	DeviceID   string
	TraceType  string // "ufs", "block", "both"
	State      pb.JobState
	OutputDir  string // parquet 산출 디렉토리 (trace.log 와 result_*.parquet 모두 여기)
	LogFile    string
	TracingDir string
	StartedAt  int64
	FinishedAt int64
	Error      string

	// internal processes
	adbCancel    context.CancelFunc
	adbCmd       *exec.Cmd
	logFd        *os.File
	subscribers  []chan *pb.JobProgress
	lastProgress []*pb.JobProgress
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
	mu         sync.RWMutex
	jobs       map[string]*TraceJob
	adbMgr     *adb.Manager
	toolsDir   string
	outputBase string
}

func NewManager(adbMgr *adb.Manager, toolsDir, traceDir string) *Manager {
	if traceDir == "" {
		// 빈 문자열이면 outputDir 가 jobID 한 글자가 되어 cwd 에 trace.log 가 쌓인다.
		// config.Load 가 default 를 채워주지만, NewManager 직접 호출자나 home 디렉토리
		// 조회 실패 같은 엣지 케이스를 위한 마지막 방어선.
		traceDir = filepath.Join(os.TempDir(), "agent_trace")
		slog.Warn("trace dir empty, falling back to temp", "trace_dir", traceDir)
	}
	return &Manager{
		jobs:       make(map[string]*TraceJob),
		adbMgr:     adbMgr,
		toolsDir:   toolsDir,
		outputBase: traceDir,
	}
}

// StartTrace begins trace collection + realtime parsing on a device.
func (m *Manager) StartTrace(ctx context.Context, req *pb.StartTraceRequest) (string, error) {
	md, err := m.adbMgr.GetDevice(req.DeviceId)
	if err != nil {
		return "", err
	}

	traceType := req.TraceType
	if traceType == "" {
		traceType = "ufs"
	}
	isFsio := IsFsioTraceType(traceType)

	// fsio 는 ftrace 를 안 쓰므로 tracingDir 이 없어도 된다.
	tracingDir := md.TracingDir
	if tracingDir == "" && !isFsio {
		return "", fmt.Errorf("tracing directory not found on device %s", req.DeviceId)
	}

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer setupCancel()

	jobID := uuid.New().String()
	outputDir := filepath.Join(m.outputBase, jobID)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir output: %w", err)
	}

	logFile := filepath.Join(outputDir, "trace.log")

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

	// fsio 는 eBPF 바이너리를 push 해 실행한다 — ftrace 이벤트 조작을 하지 않는다.
	if isFsio {
		job.notify(&pb.JobProgress{
			JobId:    jobID,
			DeviceId: req.DeviceId,
			State:    pb.JobState_JOB_STATE_RUNNING,
			Message:  "preparing fsiotrace (push + root check)",
		})
		if err := prepareFsioDevice(setupCtx, md.Device, m.toolsDir); err != nil {
			m.mu.Lock()
			delete(m.jobs, jobID)
			m.mu.Unlock()
			return "", err
		}
	} else {
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

		enableFtraceEvents(setupCtx, md, tracingDir, traceType)

		// Start tracing
		md.Device.Shell(setupCtx, fmt.Sprintf("echo 1 > %s/tracing_on", tracingDir))
	}

	// Start collector → log file
	adbCtx, adbCancel := context.WithCancel(context.Background())
	logFd, err := os.Create(logFile)
	if err != nil {
		adbCancel()
		return "", fmt.Errorf("create log file: %w", err)
	}

	var adbCmd *exec.Cmd
	if isFsio {
		adbCmd, err = startFsioCollector(adbCtx, md.Serial, traceType, logFd)
	} else {
		adbCmd = exec.CommandContext(adbCtx, "adb", "-s", md.Serial, "shell",
			fmt.Sprintf("cat %s/trace_pipe", tracingDir))
		adbCmd.Stdout = logFd
		err = adbCmd.Start()
	}
	if err != nil {
		logFd.Close()
		adbCancel()
		return "", fmt.Errorf("start collector: %w", err)
	}

	return m.finishStart(job, adbCmd, adbCancel, logFd)
}

// enableFtraceEvents — ftrace 계열 trace_type 의 이벤트를 켠다.
func enableFtraceEvents(ctx context.Context, md *adb.ManagedDevice, tracingDir, traceType string) {
	switch traceType {
	case "ufs":
		md.Device.Shell(ctx, fmt.Sprintf("echo 1 > %s/events/ufs/ufshcd_command/enable", tracingDir))
	case "block":
		md.Device.Shell(ctx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_issue/enable", tracingDir))
		md.Device.Shell(ctx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_complete/enable", tracingDir))
	case "both":
		md.Device.Shell(ctx, fmt.Sprintf("echo 1 > %s/events/ufs/ufshcd_command/enable", tracingDir))
		md.Device.Shell(ctx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_issue/enable", tracingDir))
		md.Device.Shell(ctx, fmt.Sprintf("echo 1 > %s/events/block/block_rq_complete/enable", tracingDir))
	}
}

// finishStart — 수집 프로세스 등록 + 백그라운드 감시. ftrace/fsio 공통 뒷부분.
//
// 식별 정보는 전부 job 에 이미 들어 있으므로 따로 받지 않는다.
func (m *Manager) finishStart(job *TraceJob, adbCmd *exec.Cmd, adbCancel context.CancelFunc,
	logFd *os.File) (string, error) {

	jobID, deviceID := job.ID, job.DeviceID

	// Wait for log data to start flowing
	time.Sleep(500 * time.Millisecond)

	job.Mu.Lock()
	job.adbCancel = adbCancel
	job.adbCmd = adbCmd
	job.logFd = logFd
	job.Mu.Unlock()

	job.notify(&pb.JobProgress{
		JobId:    jobID,
		DeviceId: deviceID,
		State:    pb.JobState_JOB_STATE_RUNNING,
		Message:  "trace collecting",
	})

	slog.Info("trace started", "job_id", jobID, "device", deviceID, "type", job.TraceType,
		"output_dir", job.OutputDir)

	// Wait for adb process in background.
	// adbCancel 이 호출되면 exec.CommandContext 가 SIGKILL 을 보내므로 Wait 가 즉시 풀린다.
	// 그래도 adb 가 좀비/uninterruptible 상태로 빠질 가능성을 대비해 timeout 후 강제 Kill.
	go func() {
		done := make(chan struct{})
		go func() {
			adbCmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			slog.Warn("adb trace_pipe wait timeout, force killing", "job_id", jobID, "pid", adbCmd.Process.Pid)
			if adbCmd.Process != nil {
				adbCmd.Process.Kill()
			}
			<-done // 짧게는 반환된다 (Kill 후)
		}
		logFd.Close()
	}()

	return jobID, nil
}

// StopTrace stops trace collection. Device tracing off + adb kill은 동기,
// parquet-only 일괄 파싱은 백그라운드로 처리하여 즉시 리턴.
//
// 상태: RUNNING → COLLECTING (parsing) → COMPLETED. COLLECTING 동안에는
// result_*.parquet 가 아직 없으므로 GetTraceResult 등은 차단된다.
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
	deviceID := job.DeviceID
	tracingDir := job.TracingDir
	traceType := job.TraceType
	job.Mu.Unlock()

	const shellTimeout = 10 * time.Second

	// 1. 기기 측 수집 중지 (동기, 타임아웃)
	if md, err := m.adbMgr.GetDevice(deviceID); err == nil {
		shellCtx, shellCancel := context.WithTimeout(context.Background(), shellTimeout)
		if IsFsioTraceType(traceType) {
			// ⚠ pkill 이 **먼저**다. fsiotrace 는 SIGTERM 을 받으면 detach 후 ringbuf
			// 잔여 이벤트를 배수하고 끝내서 마지막 몇 건이 안 잘린다. adb 를 먼저
			// 죽이면 EPIPE 경로로 가는데 그건 fallback 이다 (fsio.go 주석 참고).
			stopFsioOnDevice(shellCtx, md.Device)
		} else {
			md.Device.Shell(shellCtx, fmt.Sprintf("echo 0 > %s/tracing_on", tracingDir))
			md.Device.Shell(shellCtx, fmt.Sprintf("echo 0 > %s/events/enable", tracingDir))
		}
		shellCancel()
	}

	// 2. 수집 프로세스 종료 (동기). fsio 는 위 pkill 로 이미 끝났을 수 있고,
	//    안 끝났으면 여기서 파이프가 닫혀 EPIPE 로 정리된다.
	if adbCancel != nil {
		adbCancel()
	}

	// 상태를 COLLECTING으로 변경 (parquet 생성 대기)
	job.Mu.Lock()
	job.State = pb.JobState_JOB_STATE_COLLECTING
	job.Mu.Unlock()

	slog.Info("trace collection stopped, parsing in background", "job_id", jobID)

	// 3. 백그라운드에서 parquet-only 일괄 파싱 1회 실행
	go m.finalizeTrace(job)

	return nil
}

// finalizeTrace — StopTrace 후 백그라운드에서 1회 호출. trace.log → result_*.parquet
// 일괄 파싱을 수행하고 상태를 COMPLETED 로 전이한다. 실패 시 FAILED.
func (m *Manager) finalizeTrace(job *TraceJob) {
	jobID := job.ID

	// adb 가 마지막 버퍼를 flush 하기까지 잠시 대기
	time.Sleep(1 * time.Second)

	if fi, err := os.Stat(job.LogFile); err == nil {
		slog.Info("trace log ready for parsing", "job_id", jobID, "size_bytes", fi.Size())
	} else {
		m.failJob(job, fmt.Sprintf("trace.log not found: %v", err))
		return
	}

	if err := m.runParquetOnly(job, pb.JobState_JOB_STATE_COLLECTING); err != nil {
		m.failJob(job, fmt.Sprintf("parquet-only failed: %v", err))
		return
	}

	job.Mu.Lock()
	job.State = pb.JobState_JOB_STATE_COMPLETED
	job.FinishedAt = time.Now().UnixMilli()
	deviceID := job.DeviceID
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

// runParquetOnly — trace.log 를 일괄 파싱해 OutputDir 에 result_*.parquet 을 생성한다.
// progressState 는 진행률 알림에 실어 보낼 JobState (COLLECTING 또는 REPARSING).
// 호출 전에 기존 result_*.parquet 가 있으면 삭제한다.
//
// AGENT_PARSER=go 환경변수가 설정되면 Go 내장 파서(`trace/parser`) 를 사용한다.
// 기본은 Rust `tools/trace --parquet-only` 자식 프로세스. 1단계 안정 운영 후 Go 파서로
// 정합성 검증을 거쳐 점진적으로 전환한다.
func (m *Manager) runParquetOnly(job *TraceJob, progressState pb.JobState) error {
	jobID := job.ID

	// 기존 결과 정리: result_*.parquet 만 제거 (trace.log 는 보존)
	if entries, err := os.ReadDir(job.OutputDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".parquet") {
				continue
			}
			if !strings.HasPrefix(name, "result_") && !strings.HasPrefix(name, "realtime_") {
				continue
			}
			_ = os.Remove(filepath.Join(job.OutputDir, name))
		}
	}
	// legacy realtime/ 서브폴더가 있으면 함께 정리 (이전 잡 호환)
	legacyDir := filepath.Join(job.OutputDir, "realtime")
	if entries, err := os.ReadDir(legacyDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".parquet") {
				_ = os.Remove(filepath.Join(legacyDir, e.Name()))
			}
		}
	}

	// Go 내장 파서 분기.
	//
	// fsio 는 **항상** Go 파서를 쓴다. 체크인된 tools/trace 바이너리에는 fsio 파싱이
	// 없어서(bpftrace 지원은 그 뒤에 추가됨) Rust 로 보내면 산출물이 조용히 0건이 된다.
	// AGENT_PARSER 설정과 무관하게 여기서 갈라야 한다.
	if os.Getenv("AGENT_PARSER") == "go" || IsFsioTraceType(job.TraceType) {
		slog.Info("using Go embedded parser", "job_id", jobID, "trace_type", job.TraceType)
		progressFn := func(line string) {
			job.notify(&pb.JobProgress{
				JobId:   jobID,
				State:   progressState,
				Message: line,
			})
		}
		return parser.RunParquetOnly(job.LogFile, job.OutputDir, job.TraceType, progressFn)
	}

	traceBin := filepath.Join(m.toolsDir, "trace")
	outputPrefix := filepath.Join(job.OutputDir, "result")
	cmd := exec.Command(traceBin, job.LogFile, outputPrefix, "--parquet-only")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start trace: %w", err)
	}

	// stdout 의 진행 라인을 SubscribeJobProgress 로 forward
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			slog.Debug("parquet-only output", "job_id", jobID, "line", line)
			job.notify(&pb.JobProgress{
				JobId:   jobID,
				State:   progressState,
				Message: line,
			})
		}
	}()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("trace parquet-only: %w", err)
	}
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
//
// 우선순위:
//  1. 메모리 (job.OutputDir, job.TraceType)
//  2. 디스크 base dir 직하 (parquet-only 산출물 위치)
//  3. legacy realtime 서브폴더 (이전 실시간 잡 호환)
func (m *Manager) GetTraceJobInfo(jobID string) (*TraceJobInfo, error) {
	if job, err := m.GetJob(jobID); err == nil {
		return &TraceJobInfo{Dir: job.OutputDir, TraceType: job.TraceType}, nil
	}
	baseDir := filepath.Join(m.outputBase, jobID)
	if fi, err := os.Stat(baseDir); err == nil && fi.IsDir() {
		// base dir 에 result_*.parquet 가 있으면 거기서, 없으면 legacy realtime/
		legacy := filepath.Join(baseDir, "realtime")
		if _, err := os.Stat(legacy); err == nil {
			if matches, _ := filepath.Glob(filepath.Join(baseDir, "*.parquet")); len(matches) == 0 {
				return &TraceJobInfo{Dir: legacy, TraceType: detectTraceTypeFromDir(legacy)}, nil
			}
		}
		return &TraceJobInfo{Dir: baseDir, TraceType: detectTraceTypeFromDir(baseDir)}, nil
	}
	return nil, fmt.Errorf("trace job not found: %s", jobID)
}

// ArchiveParquetEntry — archive 흐름에서 portal 에 보낼 parquet 파일 1개의 메타.
type ArchiveParquetEntry struct {
	LocalPath string // 절대 경로
	TraceType string // "ufs" | "block" | "ufscustom" — 파일명으로 판별
	Size      int64
	BaseName  string // 정렬 + portal 키 매핑용 (e.g. "result_ufs.parquet")
}

// GetArchiveFiles — Trace Archive 흐름에서 업로드할 파일 목록을 반환한다.
//
// parquet-only 흐름에서는 타입당 파일 1개씩 (result_ufs.parquet 등) 이지만, legacy
// realtime 산출물(realtime_*_NNN.parquet) 도 함께 인식해 호환을 유지한다. 정렬은
// 파일명 lexicographic — legacy 의 시퀀스 번호 순서와 일치한다.
func (m *Manager) GetArchiveFiles(jobID string) (rawPath string, rawSize int64, parquetFiles []ArchiveParquetEntry, err error) {
	info, ierr := m.GetTraceJobInfo(jobID)
	if ierr != nil {
		err = ierr
		return
	}

	// trace.log 위치 후보: info.Dir 가 base 일 수도, 그 부모일 수도 있다.
	candidates := []string{
		filepath.Join(info.Dir, "trace.log"),
		filepath.Join(filepath.Dir(info.Dir), "trace.log"),
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

	entries, rerr := os.ReadDir(info.Dir)
	if rerr != nil {
		err = fmt.Errorf("read parquet dir %s: %w", info.Dir, rerr)
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

// detectTraceTypeFromFilename — result_<type>.parquet / <type>.parquet / legacy realtime_<type>_NNN.parquet
// 패턴을 모두 인식. 매칭 안 되면 "unknown".
func detectTraceTypeFromFilename(name string) string {
	lower := strings.ToLower(name)
	switch {
	// ⚠ fsio_* 를 먼저 본다. substring 매칭이라 "result_fsio_ufs.parquet" 이
	// 아래 "ufs" 분기에 먼저 걸리면 ftrace UFS 로 오분류된다.
	case strings.Contains(lower, "fsio_ufs"):
		return "fsio_ufs"
	case strings.Contains(lower, "fsio_block"):
		return "fsio_block"
	case strings.Contains(lower, "ufscustom"):
		return "ufscustom"
	case strings.Contains(lower, "ufs"):
		return "ufs"
	case strings.Contains(lower, "block"):
		return "block"
	}
	return "unknown"
}

// detectTraceTypeFromDir — 디렉토리의 result_*.parquet 파일명으로 trace_type 을 추정한다.
//
// 메모리에 잡이 없을 때(agent 재시작 후) 쓰인다. 예전에는 무조건 "both" 로 뒀는데,
// 그러면 fsio 잡을 reparse 할 때 ftrace 파서가 돌아 **아무것도 안 나온다.**
// 산출물이 없으면(파싱 전) ftrace 기본값 "both" 로 폴백한다 — 기존 동작 유지.
func detectTraceTypeFromDir(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.parquet"))
	for _, m := range matches {
		if t := detectTraceTypeFromFilename(filepath.Base(m)); t != "unknown" {
			// fsio 는 단일 선택이라 하나만 나오면 그게 답이다. ftrace 계열은
			// ufs/block 이 섞일 수 있어 "both" 로 합쳐 읽는 기존 규칙을 따른다.
			if strings.HasPrefix(t, "fsio_") {
				return t
			}
		}
	}
	return "both"
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

		if _, err := os.Stat(logFile); err != nil {
			return fmt.Errorf("trace.log not found for job %s — cannot reparse", jobID)
		}

		job = &TraceJob{
			ID:    jobID,
			State: pb.JobState_JOB_STATE_COMPLETED,
			// 메모리에 잡이 없으므로 산출물 파일명으로 종류를 되찾는다.
			// "both" 로 고정하면 fsio 잡이 ftrace 파서를 타 아무것도 안 나온다.
			OutputDir: baseDir,
			LogFile:   logFile,
			TraceType: detectTraceTypeFromDir(baseDir),
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

	if _, err := os.Stat(job.LogFile); err != nil {
		return fmt.Errorf("trace.log not found: %s", job.LogFile)
	}

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

// doReparse — 보존된 trace.log 를 parquet-only 흐름으로 재처리한다. finalizeTrace 와
// 동일한 runParquetOnly 를 공유하며 다른 점은 진행률 state(REPARSING) 뿐이다.
func (m *Manager) doReparse(job *TraceJob) {
	jobID := job.ID

	job.notify(&pb.JobProgress{
		JobId:   jobID,
		State:   pb.JobState_JOB_STATE_REPARSING,
		Message: "reparse started",
	})

	if err := m.runParquetOnly(job, pb.JobState_JOB_STATE_REPARSING); err != nil {
		slog.Error("trace reparse failed", "job_id", jobID, "error", err)
		job.Mu.Lock()
		job.State = pb.JobState_JOB_STATE_FAILED
		job.Error = err.Error()
		job.FinishedAt = time.Now().UnixMilli()
		job.Mu.Unlock()
		errMsg := err.Error()
		job.notify(&pb.JobProgress{
			JobId:   jobID,
			State:   pb.JobState_JOB_STATE_FAILED,
			Message: errMsg,
			Error:   errMsg,
		})
		return
	}

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
