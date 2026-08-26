package trace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agent/adb"

	"github.com/google/uuid"
)

// logcat 수집기 — trace 와 **같은 층위의 부가 수집**이다.
//
// 시나리오 스텝이 아니라 잡에 딸린 옵션으로 붙는다. 스텝으로 만들면
// scenario/steptypes.go 가 단일 진실 소스라 프롬프트·검증·UI 파생물이 전부 늘어난다.
//
// 구조는 tracer.go 를 그대로 미러링한다 (신규 설계 없음):
//   - adb 자식 프로세스를 context.Background() 파생 컨텍스트로 띄운다
//     (수집기가 RPC 보다 오래 살아야 한다)
//   - cancel → CommandContext 가 SIGKILL, 30초 워치독으로 좀비 대비
//   - RUNNING → COLLECTING(flush) → COMPLETED
//
// trace 와 다른 점: 파싱 단계가 없다. 원본 로그를 남기는 것이 목적이고,
// 패턴 매칭은 나중에 프로파일을 바꿔가며 몇 번이고 다시 돌릴 수 있다.

// LogcatFormat — `adb logcat -v <fmt>`.
//
// ⚠ 시계축이 여기서 갈린다:
//   - monotonic: CLOCK_MONOTONIC (suspend 제외)
//   - epoch:     wall clock (호스트와 직접 대조 가능)
//
// `/proc/uptime`(BOOTTIME) 과 monotonic 은 **누적 suspend 만큼 어긋난다**
// (실기기 실측 120.5초). IO 트레이스와 겹치려면 축 변환이 필요하다 —
// 자세한 내용은 clockoffset.go 의 MeasureClockOffset 주석 참고.
type LogcatFormat string

const (
	LogcatFormatMonotonic LogcatFormat = "monotonic"
	LogcatFormatEpoch     LogcatFormat = "epoch"
)

// LogcatFileName — 산출물 이름. trace.log 와 나란히 놓인다.
const LogcatFileName = "logcat.log"

// LogcatJob — 수집 세션 하나.
type LogcatJob struct {
	Mu       sync.Mutex
	ID       string
	DeviceID string
	State    JobStateLite
	// Mode — measure(좁게) 또는 explore(넓게). 아래 LogcatMode 참고.
	Mode      LogcatMode
	Format    LogcatFormat
	Tags      []string
	OutputDir string
	LogFile   string

	StartedAt, FinishedAt int64
	Error                 string

	// Lines — 수집된 줄 수 (종료 후 채워진다). 0 이면 "한 줄도 못 받았다" 는
	// 진단 근거가 된다 — 패턴 문제와 수집 문제를 가르는 데 쓴다.
	Lines int

	adbCancel context.CancelFunc
	adbCmd    *exec.Cmd
	logFd     *os.File
}

// JobStateLite — logcat 은 pb 의존 없이 단순 상태만 쓴다.
// (trace 는 gRPC 진행률을 스트리밍하지만 logcat 은 그럴 필요가 없다.)
type JobStateLite string

const (
	LogcatStateRunning    JobStateLite = "running"
	LogcatStateCollecting JobStateLite = "collecting"
	LogcatStateCompleted  JobStateLite = "completed"
	LogcatStateFailed     JobStateLite = "failed"
)

// LogcatMode — 수집 범위.
type LogcatMode string

const (
	// LogcatModeMeasure — 프로파일이 정한 태그만 (`-s tag1 tag2`).
	// 실측정용. 부하를 최소화한다.
	LogcatModeMeasure LogcatMode = "measure"
	// LogcatModeExplore — 태그 제한 없이 넓게. 형식을 모를 때 1회성으로만 쓴다.
	//
	// ⚠ 전체 수집은 그 자체가 IO/CPU 를 써서 측정 대상을 흔든다. 수백 ms 단위
	// TTFT 에선 무시 못 한다. **실측정에는 쓰지 않는다.**
	LogcatModeExplore LogcatMode = "explore"
)

// LogcatManager — 수집 세션 관리.
type LogcatManager struct {
	mu          sync.RWMutex
	jobs        map[string]*LogcatJob
	adbMgr      *adb.Manager
	outputBase  string
	searchRoots []string
}

func NewLogcatManager(adbMgr *adb.Manager, outputBase string) *LogcatManager {
	if outputBase == "" {
		outputBase = filepath.Join(os.TempDir(), "agent_logcat")
	}
	return &LogcatManager{
		jobs:       make(map[string]*LogcatJob),
		adbMgr:     adbMgr,
		outputBase: outputBase,
	}
}

// AddSearchRoot — 시나리오가 자기 잡 폴더에 쓰므로 조회 루트를 등록한다.
// resolveOutputBase 의 허용 목록도 겸한다.
func (m *LogcatManager) AddSearchRoot(dir string) {
	if dir == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.searchRoots {
		if r == dir {
			return
		}
	}
	m.searchRoots = append(m.searchRoots, dir)
}

// StartLogcatRequest — 수집 시작 입력.
type StartLogcatRequest struct {
	DeviceID  string
	Mode      LogcatMode
	Format    LogcatFormat
	Tags      []string
	OutputDir string
	// BufferSizeKB — `logcat -G`. 0 이면 건드리지 않는다.
	// 탐색 모드에서 로그가 밀려 잘리는 것을 막는다.
	BufferSizeKB int
}

// StartLogcat — 수집을 시작한다.
func (m *LogcatManager) StartLogcat(ctx context.Context, req StartLogcatRequest) (string, error) {
	if req.DeviceID == "" {
		return "", fmt.Errorf("deviceId required")
	}
	md, err := m.adbMgr.GetDevice(req.DeviceID)
	if err != nil {
		return "", fmt.Errorf("device: %w", err)
	}
	mode := req.Mode
	if mode == "" {
		mode = LogcatModeMeasure
	}
	format := req.Format
	if format == "" {
		// 기본은 monotonic — 기존 trace 산출물과 형태가 같아 사람이 읽기 쉽다.
		// IO 와 겹칠 때의 축 변환은 상위에서 정한다.
		format = LogcatFormatMonotonic
	}
	// ⚠ measure 모드인데 태그가 비어 있으면 **전체를 받게 된다.** 이건 조용히
	// 부하를 키우는 길이라(측정 대상을 흔든다) 명시적으로 막는다.
	if mode == LogcatModeMeasure && len(req.Tags) == 0 {
		return "", fmt.Errorf("measure 모드에는 태그가 필요하다 " +
			"(전체 수집은 측정 대상을 흔든다). 태그를 모르면 explore 모드를 쓸 것")
	}

	jobID := uuid.New().String()
	outBase := m.resolveOutputBase(req.OutputDir)
	outputDir := filepath.Join(outBase, jobID)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	logFile := filepath.Join(outputDir, LogcatFileName)

	job := &LogcatJob{
		ID: jobID, DeviceID: req.DeviceID,
		State: LogcatStateRunning, Mode: mode, Format: format,
		Tags:      append([]string(nil), req.Tags...),
		OutputDir: outputDir, LogFile: logFile,
		StartedAt: time.Now().UnixMilli(),
	}
	m.mu.Lock()
	m.jobs[jobID] = job
	m.mu.Unlock()

	// 버퍼를 키우고(선택) 기존 내용을 비운다.
	//
	// ⚠ `-c` 를 먼저 하지 않으면 **수집 시작 전의 오래된 줄**이 섞여 들어와
	// 구간 판정이 틀어진다. 실패해도 수집은 진행한다 (기기에 따라 거부될 수 있다).
	setupCtx, cancelSetup := context.WithTimeout(ctx, 10*time.Second)
	if req.BufferSizeKB > 0 {
		if _, err := md.Device.Shell(setupCtx,
			fmt.Sprintf("logcat -G %dK", req.BufferSizeKB)); err != nil {
			slog.Warn("logcat 버퍼 크기 설정 실패 — 기본값으로 진행",
				"job_id", jobID, "error", err)
		}
	}
	if _, err := md.Device.Shell(setupCtx, "logcat -c"); err != nil {
		slog.Warn("logcat 버퍼 비우기 실패 — 이전 줄이 섞일 수 있다",
			"job_id", jobID, "error", err)
	}
	cancelSetup()

	// ⚠ 수집기 컨텍스트는 **요청 ctx 가 아니라** Background 파생이다.
	// RPC 가 끝나도 수집은 계속돼야 한다 (tracer.go:190 과 같은 이유).
	adbCtx, adbCancel := context.WithCancel(context.Background())
	logFd, err := os.Create(logFile)
	if err != nil {
		adbCancel()
		m.dropJob(jobID)
		return "", fmt.Errorf("create log file: %w", err)
	}

	args := buildLogcatArgs(md.Serial, format, mode, req.Tags)
	adbCmd := exec.CommandContext(adbCtx, "adb", args...)
	adbCmd.Stdout = logFd
	if err := adbCmd.Start(); err != nil {
		logFd.Close()
		adbCancel()
		m.dropJob(jobID)
		return "", fmt.Errorf("start logcat: %w", err)
	}

	// 데이터가 흐르기 시작할 여유를 준다 (tracer.go:283 과 같다).
	time.Sleep(500 * time.Millisecond)

	job.Mu.Lock()
	job.adbCancel = adbCancel
	job.adbCmd = adbCmd
	job.logFd = logFd
	job.Mu.Unlock()

	slog.Info("logcat started", "job_id", jobID, "device", req.DeviceID,
		"mode", mode, "format", format, "tags", req.Tags, "output_dir", outputDir)

	// adb 프로세스 회수 — cancel 이면 SIGKILL 이라 Wait 가 즉시 풀리지만,
	// 좀비/uninterruptible 대비로 30초 워치독을 둔다 (tracer.go:304-320 복사).
	go func() {
		done := make(chan struct{})
		go func() {
			adbCmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			slog.Warn("logcat wait timeout, force killing", "job_id", jobID)
			if adbCmd.Process != nil {
				adbCmd.Process.Kill()
			}
			<-done
		}
		logFd.Close()
	}()

	return jobID, nil
}

// buildLogcatArgs — adb 인자를 조립한다.
//
// ⚠ measure 모드는 `-s <tags>` 로 좁힌다. `-s` 는 "silent 기본 + 나열한 태그만"
// 이라 소음을 확실히 자른다.
func buildLogcatArgs(serial string, format LogcatFormat, mode LogcatMode, tags []string) []string {
	args := []string{"-s", serial, "logcat", "-v", string(format)}
	if mode == LogcatModeMeasure && len(tags) > 0 {
		args = append(args, "-s")
		for _, t := range tags {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			// 이미 `Tag:Level` 형태면 그대로, 아니면 모든 레벨을 받는다.
			if !strings.Contains(t, ":") {
				t += ":V"
			}
			args = append(args, t)
		}
	}
	return args
}

// StopLogcat — 수집을 멈춘다. adb kill 은 동기, 줄 수 집계는 백그라운드.
func (m *LogcatManager) StopLogcat(jobID string) error {
	m.mu.RLock()
	job := m.jobs[jobID]
	m.mu.RUnlock()
	if job == nil {
		return fmt.Errorf("logcat job not found: %s", jobID)
	}

	job.Mu.Lock()
	if job.State != LogcatStateRunning {
		st := job.State
		job.Mu.Unlock()
		return fmt.Errorf("logcat job not running: %s (state=%s)", jobID, st)
	}
	cancel := job.adbCancel
	job.State = LogcatStateCollecting
	job.Mu.Unlock()

	if cancel != nil {
		cancel()
	}

	go m.finalizeLogcat(job)
	return nil
}

// finalizeLogcat — flush 를 기다렸다 줄 수를 세고 COMPLETED 로 넘긴다.
func (m *LogcatManager) finalizeLogcat(job *LogcatJob) {
	// adb 가 마지막 버퍼를 내보낼 시간 (tracer.go:438 과 같은 이유).
	time.Sleep(1 * time.Second)

	n, err := countLines(job.LogFile)
	job.Mu.Lock()
	defer job.Mu.Unlock()
	job.FinishedAt = time.Now().UnixMilli()
	if err != nil {
		job.State = LogcatStateFailed
		job.Error = err.Error()
		slog.Warn("logcat finalize 실패", "job_id", job.ID, "error", err)
		return
	}
	job.Lines = n
	job.State = LogcatStateCompleted
	// ⚠ 0줄이면 경고로 남긴다. 나중에 "패턴이 안 맞았다" 와 "수집 자체가 실패했다"
	// 를 가르는 근거가 된다 — 원인이 완전히 달라 진단이 갈려야 한다.
	if n == 0 {
		slog.Warn("logcat 이 한 줄도 수집되지 않았다 — 태그/권한/기기 연결을 확인할 것",
			"job_id", job.ID, "mode", job.Mode, "tags", job.Tags)
	} else {
		slog.Info("logcat stopped", "job_id", job.ID, "lines", n, "file", job.LogFile)
	}
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 256*1024)
	n := 0
	for {
		c, err := f.Read(buf)
		for _, b := range buf[:c] {
			if b == '\n' {
				n++
			}
		}
		if err != nil {
			break
		}
	}
	return n, nil
}

// GetLogcatJob — 조회.
func (m *LogcatManager) GetLogcatJob(jobID string) (*LogcatJob, error) {
	m.mu.RLock()
	job := m.jobs[jobID]
	m.mu.RUnlock()
	if job == nil {
		return nil, fmt.Errorf("logcat job not found: %s", jobID)
	}
	return job, nil
}

func (m *LogcatManager) dropJob(jobID string) {
	m.mu.Lock()
	delete(m.jobs, jobID)
	m.mu.Unlock()
}

// resolveOutputBase — 요청이 준 산출물 루트를 검증한다.
//
// ⚠⚠ tracer.go:608 과 **같은 이유로 반드시 필요하다.** output_dir 은 사무실 모드에서
// 인증 없는 0.0.0.0 바인딩 위로 들어온다. 이 검사를 빼고 spawn 코드만 베끼면
// **임의 경로 쓰기**가 생긴다.
//
// filepath.Rel 을 쓰는 이유: 문자열 prefix 비교는 "/data" 와 "/data-evil" 을
// 구분하지 못한다.
func (m *LogcatManager) resolveOutputBase(requested string) string {
	if requested == "" {
		return m.outputBase
	}
	abs, err := filepath.Abs(filepath.Clean(requested))
	if err != nil {
		slog.Warn("logcat output_dir 해석 실패 — 기본 위치 사용",
			"requested", requested, "error", err)
		return m.outputBase
	}

	m.mu.RLock()
	allowed := append([]string{m.outputBase}, m.searchRoots...)
	m.mu.RUnlock()

	for _, root := range allowed {
		if root == "" {
			continue
		}
		rootAbs, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return abs
		}
	}
	slog.Warn("logcat output_dir 이 허용된 루트 밖이다 — 기본 위치 사용",
		"requested", requested, "allowed", allowed)
	return m.outputBase
}

// StartLogcatForJob — benchmark.LogcatController 구현.
//
// 시나리오가 잡 옵션으로 수집을 켤 때 쓰는 진입점이다. 태그가 있으면 measure(좁게),
// 없으면 explore(넓게)로 간다 — 태그를 모르는 탐색 단계에서도 그대로 쓸 수 있다.
//
// ⚠ 탐색 모드는 부하가 크므로 **실측정에는 태그를 반드시 지정**해야 한다.
// 여기서 막지 않는 이유는 탐색 자체가 정당한 사용이기 때문이다 — 대신 로그로 남긴다.
func (m *LogcatManager) StartLogcatForJob(ctx context.Context, deviceID string,
	tags []string, outputDir string) (string, error) {

	mode := LogcatModeMeasure
	if len(tags) == 0 {
		mode = LogcatModeExplore
		slog.Warn("logcat 태그가 없어 explore(전체) 모드로 수집한다 — "+
			"부하가 크므로 실측정에는 태그를 지정할 것", "device", deviceID)
	}
	return m.StartLogcat(ctx, StartLogcatRequest{
		DeviceID:  deviceID,
		Mode:      mode,
		Tags:      tags,
		OutputDir: outputDir,
	})
}

// IsAllowedPath — 이 경로가 logcat 산출물 루트 안인지 본다.
//
// ⚠ REST 가 임의 경로를 읽지 못하게 하는 가드다. 없으면 서버의 아무 파일이나
// 노출된다 (`/etc/passwd` 같은 것). resolveOutputBase 와 같은 filepath.Rel
// 기법을 쓴다 — 문자열 prefix 비교는 "/data" 와 "/data-evil" 을 구분 못 한다.
func (m *LogcatManager) IsAllowedPath(abs string) bool {
	m.mu.RLock()
	allowed := append([]string{m.outputBase}, m.searchRoots...)
	m.mu.RUnlock()

	for _, root := range allowed {
		if root == "" {
			continue
		}
		rootAbs, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
