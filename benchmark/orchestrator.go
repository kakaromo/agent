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
	"agent/artifacts"
	pb "agent/pb"

	"github.com/google/uuid"
)

const remoteToolDir = "/data/local/tmp"

// Job represents a benchmark job across one or more devices.
type Job struct {
	mu                sync.Mutex
	ID                string
	Name              string
	Tool              pb.BenchmarkTool
	Params            map[string]string
	State             pb.JobState
	DeviceStatuses    map[string]*pb.DeviceJobStatus
	Results           map[string]*pb.BenchmarkResult
	subscribers       []chan *pb.JobProgress
	lastProgress      []*pb.JobProgress
	cancelFunc        context.CancelFunc
	RetryCount        int32
	RetryDelaySeconds int32
	activeTraceIDs    map[string]string // deviceID → trace job ID
	activeLogcatIDs   map[string]string // deviceID → logcat job ID

	// stepBoundaries — deviceID → 스텝 실행 구간 목록.
	//
	// behavior 구간별 IO 분석의 시간 축이다. activeTraceIDs 와 같은 패턴으로 Job 에
	// 달아 두는 이유: storeResult 호출부가 10곳 가까이 되는데(성공/실패/취소 경로가
	// 제각각) 인자로 넘기면 한 곳만 빠뜨려도 그 경로에서 조용히 구간이 사라진다.
	stepBoundaries map[string][]*pb.StepBoundary

	// startedAt — 잡 생성 시각. 산출물 폴더 이름의 기준이다.
	//
	// ⚠ time.Now() 를 쓰면 안 된다 — 폴더 이름을 만드는 곳이 둘(여기, rest_hook)인데
	// 시나리오가 첫 trace_start 에 닿기까지 1초만 걸려도 초 단위 포맷에서 갈린다.
	startedAt time.Time

	// artifactDir — 이 잡의 산출물 폴더. 한 번 정하면 고정한다.
	//
	// ⚠ trace_start 마다 새로 계산하면 시각이 달라져 **같은 시나리오의 trace 가 서로
	// 다른 폴더로 흩어진다.**
	artifactDir string
}

// ensureArtifactDir — 이 잡의 산출물 폴더를 (처음 한 번만) 정해 돌려준다.
func (j *Job) ensureArtifactDir(base, jobType string) string {
	if base == "" {
		return ""
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.artifactDir == "" {
		j.artifactDir = artifacts.JobArtifactDir(base, j.startedAt, jobType, j.Name, j.ID)
	}
	return j.artifactDir
}

// StartedAt — 잡 생성 시각. 산출물 폴더 이름의 기준이다.
func (j *Job) StartedAt() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.startedAt
}

// ArtifactDir — 이미 정해진 이 잡의 산출물 폴더 (없으면 빈 문자열).
//
// 결과 JSON 을 쓰는 쪽(rest_hook)이 **같은 폴더**를 쓰게 하려고 노출한다. 각자
// 계산하면 시각이 달라져(초 단위 포맷) 폴더가 갈린다 — 이 기능이 없애려던 바로 그 증상.
func (j *Job) ArtifactDir() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.artifactDir
}

// appendStepBoundary — 스텝 하나의 실행 구간을 기록한다.
func (j *Job) appendStepBoundary(deviceID string, b *pb.StepBoundary) {
	if b == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.stepBoundaries == nil {
		j.stepBoundaries = make(map[string][]*pb.StepBoundary)
	}
	j.stepBoundaries[deviceID] = append(j.stepBoundaries[deviceID], b)
}

// takeStepBoundaries — 기록된 구간을 꺼내 **비운다** (take 그대로의 의미).
//
// ⚠ 비우는 것이 핵심이다. runOnDeviceWithRetry 는 실패 시 같은 디바이스로 재실행하고
// 시도마다 storeResult 를 부르는데, 비우지 않으면 **2회차 결과에 1회차 구간이 섞인다**
// (스텝이 두 벌씩 나오고, 실패한 시도의 시간 범위가 그대로 남는다).
//
// 복사본을 돌려주는 이유는 호출자가 슬라이스를 들고 나가서다 — 내부 상태와 공유하지 않는다.
func (j *Job) takeStepBoundaries(deviceID string) []*pb.StepBoundary {
	j.mu.Lock()
	defer j.mu.Unlock()
	src := j.stepBoundaries[deviceID]
	if len(src) == 0 {
		return nil
	}
	out := make([]*pb.StepBoundary, len(src))
	copy(out, src)
	delete(j.stepBoundaries, deviceID)
	return out
}

func (j *Job) setActiveTrace(deviceID, traceJobID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.activeTraceIDs == nil {
		j.activeTraceIDs = make(map[string]string)
	}
	j.activeTraceIDs[deviceID] = traceJobID
}

func (j *Job) clearActiveTrace(deviceID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.activeTraceIDs, deviceID)
}

func (j *Job) getActiveTraceIDs() map[string]string {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := make(map[string]string, len(j.activeTraceIDs))
	for k, v := range j.activeTraceIDs {
		cp[k] = v
	}
	return cp
}

// logcat 수집도 trace 와 같은 방식으로 추적한다.
//
// ⚠ 따로 두는 이유: logcat 은 trace 와 **독립적으로** 켜고 끌 수 있다(trace 없이
// logcat 만, 또는 그 반대). 하나의 map 에 섞으면 한쪽을 멈출 때 다른 쪽까지
// 지워진다. 취소 경로에서 조용히 수집이 남는 것이 이 분리의 이유다.
func (j *Job) setActiveLogcat(deviceID, logcatJobID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.activeLogcatIDs == nil {
		j.activeLogcatIDs = make(map[string]string)
	}
	j.activeLogcatIDs[deviceID] = logcatJobID
}

func (j *Job) clearActiveLogcat(deviceID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.activeLogcatIDs, deviceID)
}

func (j *Job) getActiveLogcatIDs() map[string]string {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := make(map[string]string, len(j.activeLogcatIDs))
	for k, v := range j.activeLogcatIDs {
		cp[k] = v
	}
	return cp
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

	// HostToDeviceMonotonic — 호스트 wall clock(ms)을 그 trace 잡의 기기 monotonic
	// 초로 옮긴다. parquet `time` 과 같은 축이라 스텝 경계를 구간 질의에 바로 쓸 수 있다.
	//
	// ok=false 면 offset 을 못 쟀거나 못 믿을 값이라는 뜻 — 그 경우 **구간 분할을
	// 하지 않는다.** 틀린 offset 으로 나눈 구간은 통째로 밀려도 그래프가 정상으로
	// 보여서 검증에서 안 걸러진다 (trace/clockoffset.go 참고).
	HostToDeviceMonotonic(traceJobID string, hostMillis int64) (float64, bool)

	// WriteBoundaryMarker — ftrace trace_marker 로 경계를 **기기 축에 직접** 찍고
	// 그 시각(boot clock 초)을 돌려준다. HostToDeviceMonotonic 이 실패했을 때의 폴백.
	//
	// ⚠ 폴백인 이유: 기기에 쓰기가 발생하고 `tracing_on=1` 일 때만 된다. 대신 커널이
	// 자기 시계로 찍으므로 **adb 왕복이 오차에 안 들어간다** — offset 방식이 RTT 때문에
	// 비활성화되는 느린 기기에서 구간 분할을 살리는 유일한 수단이다.
	WriteBoundaryMarker(ctx context.Context, traceJobID string,
		kind string, label string) (float64, bool)

	// TraceTypeOf — 그 잡이 실제로 어떤 trace_type 으로 돌고 있는지.
	//
	// trace_stop 스텝은 자기 params 에 trace_type 이 없는 게 보통이라(그 선택은
	// trace_start 에서 한다) 스텝 값을 믿으면 "ufs" 로 폴백한다. 그 값이 그대로
	// TRACE_STOP 라인에 실려 프론트의 trace_type 판정을 뒤집으므로, 잡에서 읽는다.
	// 모르는 잡이면 빈 문자열.
	TraceTypeOf(traceJobID string) string
}

// LogcatController — logcat 수집기. trace 와 같은 이유로 인터페이스로 끊는다
// (benchmark 가 trace 를 import 하면 순환).
//
// ⚠ trace 와 **같은 층위의 부가 수집**이다. 시나리오 스텝이 아니라 잡 옵션으로
// 붙으므로 scenario/steptypes.go 파생물(프롬프트·검증·UI)이 늘지 않는다.
type LogcatController interface {
	// StartLogcatForJob — 수집을 시작하고 잡 ID 를 준다.
	//
	// tags 가 비면 explore(넓게), 있으면 measure(좁게) 로 동작한다.
	// outputDir 은 잡 산출물 폴더 밑을 가리킨다 — 구현이 허용 루트를 검증한다.
	StartLogcatForJob(ctx context.Context, deviceID string, tags []string, outputDir string) (string, error)
	StopLogcat(jobID string) error
}

// MacroController interface to avoid circular imports with macro package.
type MacroController interface {
	ReplayMacro(ctx context.Context, req *pb.ReplayMacroRequest) (*pb.ReplayMacroResponse, error)
}

// ApkController abstracts apkmgr so scenario steps can install/uninstall APKs without
// importing apkmgr directly (avoid cycles).
type ApkController interface {
	Install(ctx context.Context, req *pb.InstallApkRequest) (*pb.InstallApkResponse, error)
	Uninstall(ctx context.Context, req *pb.UninstallApkRequest) (*pb.UninstallApkResponse, error)
}

// JobFinishHook — job 이 terminal 상태로 종료될 때 호출된다(SSE 구독 여부와 무관).
// server 레이어가 DB 영속화를 붙이는 용도. state 는 "completed"/"failed"/"cancelled"/"partially_failed".
type JobFinishHook func(jobID, state, errMsg string)

// Orchestrator manages benchmark job execution.
type Orchestrator struct {
	mu        sync.RWMutex
	jobs      map[string]*Job
	manager   *adb.Manager
	toolsDir  string
	toolNames map[pb.BenchmarkTool]string // 도구별 파일명 override (nil/빈 항목은 기본명 사용)
	traceMgr  TraceController
	logcatMgr LogcatController

	// artifactBase — 잡 산출물 루트. 설정되면 시나리오가 trace 를 자기 잡 폴더
	// 안에 쓰게 한다(결과 JSON 과 한곳에 모으기 위함). 비면 기존 동작(trace_dir).
	artifactBase string
	macroMgr     MacroController
	apkMgr       ApkController
	deviceLocks  map[string]*sync.Mutex // per-device lock for "wait" policy
	finishHook   JobFinishHook          // nil 가능 (사무실 모드 등 DB 없을 때)
}

// SetJobFinishHook — job 종료 시 호출될 콜백 등록. standalone 에서 DB 영속화 연결용.
func (o *Orchestrator) SetJobFinishHook(h JobFinishHook) {
	o.mu.Lock()
	o.finishHook = h
	o.mu.Unlock()
}

// fireFinishHook — job 종료 시 등록된 hook 을 안전하게 호출한다.
func (o *Orchestrator) fireFinishHook(jobID, state, errMsg string) {
	o.mu.RLock()
	h := o.finishHook
	o.mu.RUnlock()
	if h != nil {
		h(jobID, state, errMsg)
	}
}

// jobStateHookString — pb.JobState 를 DB/REST 호환 소문자 문자열로 변환.
//
// server.jobStateString 과 같은 매핑이지만 패키지 의존 방향(server → benchmark) 때문에
// 공유하지 않는다. terminal 4개만 다루므로 값이 갈릴 여지는 작다.
//
// default 가 "failed" 인 것은 의도적이다. 호출자는 모두 확정된 terminal 상태를 넘기므로
// 여기 도달하면 안 되지만, 만약 새 상태가 추가돼 매핑이 빠지면 **성공으로 기록되는 쪽이
// 훨씬 위험하다** — 실패가 조용히 성공으로 남는다. 모르면 실패로 기록하고 로그를 남긴다.
func jobStateHookString(s pb.JobState) string {
	switch s {
	case pb.JobState_JOB_STATE_COMPLETED:
		return "completed"
	case pb.JobState_JOB_STATE_FAILED:
		return "failed"
	case pb.JobState_JOB_STATE_PARTIALLY_FAILED:
		return "partially_failed"
	case pb.JobState_JOB_STATE_CANCELLED:
		return "cancelled"
	default:
		slog.Warn("매핑되지 않은 job 상태 — failed 로 기록", "state", s)
		return "failed"
	}
}

func NewOrchestrator(manager *adb.Manager, toolsDir string) *Orchestrator {
	return &Orchestrator{
		jobs:        make(map[string]*Job),
		manager:     manager,
		toolsDir:    toolsDir,
		deviceLocks: make(map[string]*sync.Mutex),
	}
}

// getDeviceLock returns a per-device mutex for sequential execution.
//
// 호출될 때 adb manager 에 더이상 존재하지 않는 디바이스의 락은 map 에서 제거한다
// (Mutex 자체는 in-flight job 이 들고 있을 수 있어 GC 에 맡김). 이렇게 하지 않으면
// 디바이스 ID 가 바뀔 때마다 entry 가 누적되어 long-lived agent 에서 무한 증가한다.
func (o *Orchestrator) getDeviceLock(deviceID string) *sync.Mutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	for id := range o.deviceLocks {
		if id == deviceID {
			continue
		}
		if _, err := o.manager.GetDevice(id); err != nil {
			delete(o.deviceLocks, id)
		}
	}
	if _, ok := o.deviceLocks[deviceID]; !ok {
		o.deviceLocks[deviceID] = &sync.Mutex{}
	}
	return o.deviceLocks[deviceID]
}

// checkDeviceBusy checks if a device is busy and applies the busy policy.
// Returns error if policy is "reject" and device is busy.
func (o *Orchestrator) checkDeviceBusy(deviceID, policy string) error {
	md, err := o.manager.GetDevice(deviceID)
	if err != nil {
		return err
	}
	if md.State != pb.DeviceState_DEVICE_STATE_BUSY {
		return nil
	}
	switch policy {
	case "force":
		return nil // allow concurrent execution
	case "wait":
		return nil // will be serialized via device lock
	default: // "reject" or empty
		return fmt.Errorf("device %s is busy (use busy_policy='wait' or 'force')", deviceID)
	}
}

// SetTraceController sets the trace controller for scenario trace_start/trace_stop steps.
func (o *Orchestrator) SetTraceController(tc TraceController) {
	o.traceMgr = tc
}

// SetLogcatController sets the logcat collector (optional side-car).
func (o *Orchestrator) SetLogcatController(lc LogcatController) {
	o.logcatMgr = lc
}

// SetMacroController sets the macro controller for app_macro steps.
func (o *Orchestrator) SetMacroController(mc MacroController) {
	o.macroMgr = mc
}

// SetApkController sets the APK controller for install_apk/uninstall_apk steps.
func (o *Orchestrator) SetApkController(ac ApkController) {
	o.apkMgr = ac
}

// SetToolName overrides the file name used for a given benchmark tool when pushing
// to the device. Empty value → 기본명을 사용. 호출자(main)는 config 로딩 직후 한 번
// 호출. push 가 시작되기 전이라 lock 필요 없음.
func (o *Orchestrator) SetToolName(tool pb.BenchmarkTool, name string) {
	if name == "" {
		return
	}
	if o.toolNames == nil {
		o.toolNames = make(map[pb.BenchmarkTool]string)
	}
	o.toolNames[tool] = name
}

// resolveToolName 은 override 가 있으면 그걸, 없으면 기본명을 돌려준다.
func (o *Orchestrator) resolveToolName(tool pb.BenchmarkTool) string {
	if n, ok := o.toolNames[tool]; ok && n != "" {
		return n
	}
	return defaultToolNameFor(tool)
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

	policy := req.BusyPolicy
	// Check busy status for all devices
	for _, id := range deviceIDs {
		if err := o.checkDeviceBusy(id, policy); err != nil {
			return "", err
		}
	}

	jobCtx, jobCancel := context.WithCancel(context.Background())
	jobID := uuid.New().String()
	job := &Job{
		ID:                jobID,
		Name:              req.JobName,
		Tool:              req.Tool,
		Params:            req.Params,
		State:             pb.JobState_JOB_STATE_QUEUED,
		DeviceStatuses:    make(map[string]*pb.DeviceJobStatus),
		Results:           make(map[string]*pb.BenchmarkResult),
		cancelFunc:        jobCancel,
		RetryCount:        req.RetryCount,
		RetryDelaySeconds: req.RetryDelaySeconds,
		startedAt:         time.Now(),
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

	go o.executeJob(jobCtx, job, deviceIDs, policy)
	return jobID, nil
}

func (o *Orchestrator) executeJob(ctx context.Context, job *Job, deviceIDs []string, policy string) {
	defer job.closeSubscribers()

	var wg sync.WaitGroup
	for _, deviceID := range deviceIDs {
		wg.Add(1)
		go func(devID string) {
			defer wg.Done()
			// If wait policy, acquire device lock for sequential execution
			if policy == "wait" {
				lock := o.getDeviceLock(devID)
				lock.Lock()
				defer lock.Unlock()
			}
			// Check cancellation before starting
			if ctx.Err() != nil {
				o.updateDeviceStatus(job, devID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", 0)
				return
			}
			o.runOnDeviceWithRetry(ctx, job, devID)
		}(deviceID)
	}
	wg.Wait()

	// Determine overall job state
	job.mu.Lock()
	completed, failed, cancelled := 0, 0, 0
	for _, ds := range job.DeviceStatuses {
		switch ds.State {
		case pb.JobState_JOB_STATE_COMPLETED:
			completed++
		case pb.JobState_JOB_STATE_FAILED:
			failed++
		case pb.JobState_JOB_STATE_CANCELLED:
			cancelled++
		}
	}
	total := len(job.DeviceStatuses)
	if cancelled == total {
		job.State = pb.JobState_JOB_STATE_CANCELLED
	} else if failed == total {
		job.State = pb.JobState_JOB_STATE_FAILED
	} else if failed > 0 || cancelled > 0 {
		job.State = pb.JobState_JOB_STATE_PARTIALLY_FAILED
	} else {
		job.State = pb.JobState_JOB_STATE_COMPLETED
	}
	finalState := job.State
	job.mu.Unlock()

	slog.Info("job finished", "job_id", job.ID, "state", finalState, "completed", completed, "failed", failed, "cancelled", cancelled)
	// SSE 구독 여부와 무관하게 최종 상태를 DB 에 반영 (scenario 경로와 동일).
	o.fireFinishHook(job.ID, jobStateHookString(finalState), "")
}

func (o *Orchestrator) runOnDeviceWithRetry(ctx context.Context, job *Job, deviceID string) {
	maxAttempts := int(job.RetryCount) + 1
	delay := time.Duration(job.RetryDelaySeconds) * time.Second
	if delay <= 0 {
		delay = 60 * time.Second
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", 0)
			return
		}

		if attempt > 0 {
			slog.Info("retrying device", "job_id", job.ID, "device_id", deviceID, "attempt", attempt+1, "max", maxAttempts)
			// Reset device status for retry
			o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_QUEUED, fmt.Sprintf("retry %d/%d", attempt+1, maxAttempts), 0)
			time.Sleep(delay)
		}

		o.runOnDevice(ctx, job, deviceID)

		// Check if device succeeded
		job.mu.Lock()
		ds := job.DeviceStatuses[deviceID]
		succeeded := ds != nil && ds.State == pb.JobState_JOB_STATE_COMPLETED
		job.mu.Unlock()

		if succeeded {
			return
		}

		// If this was the last attempt, keep the failed status
		if attempt >= maxAttempts-1 {
			return
		}
	}
}

func (o *Orchestrator) runOnDevice(ctx context.Context, job *Job, deviceID string) {
	if ctx.Err() != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", 0)
		return
	}
	md, err := o.manager.GetDevice(deviceID)
	if err != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, err.Error(), 0)
		return
	}

	o.manager.SetState(deviceID, pb.DeviceState_DEVICE_STATE_BUSY)
	defer o.manager.SetState(deviceID, pb.DeviceState_DEVICE_STATE_ONLINE)

	startedAt := time.Now().UnixMilli()

	// Push tool
	toolName := o.resolveToolName(job.Tool)
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

func (o *Orchestrator) storeResult(job *Job, deviceID string, startedAt int64, rawOutput string, metrics map[string]float64, success bool, errMsg string, traceJobs ...*pb.TraceJobMapping) {
	result := &pb.BenchmarkResult{
		DeviceId:   deviceID,
		Tool:       job.Tool,
		RawOutput:  rawOutput,
		Metrics:    metrics,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UnixMilli(),
		Success:    success,
		Error:      errMsg,
		TraceJobs:  traceJobs,
		// 구간은 인자가 아니라 Job 에서 꺼낸다 — 호출부가 많아 인자로 넘기면
		// 빠뜨린 경로에서 조용히 사라진다.
		StepBoundaries: job.takeStepBoundaries(deviceID),
	}
	job.mu.Lock()
	job.Results[deviceID] = result
	job.mu.Unlock()
}

// GetJob returns a job by ID.
// SetArtifactBase — 잡 산출물 루트를 지정한다 (standalone 전용).
func (o *Orchestrator) SetArtifactBase(dir string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.artifactBase = dir
}

func (o *Orchestrator) getArtifactBase() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.artifactBase
}

func (o *Orchestrator) GetJob(jobID string) (*Job, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	job, ok := o.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	return job, nil
}

// CancelJob cancels a running job.
func (o *Orchestrator) CancelJob(jobID string) error {
	job, err := o.GetJob(jobID)
	if err != nil {
		return err
	}

	// Active trace 먼저 정리 (cancel 성공 여부와 관계없이)
	traceIDs := job.getActiveTraceIDs()
	for deviceID, traceID := range traceIDs {
		if o.traceMgr != nil && traceID != "" {
			slog.Info("stopping trace on job cancel", "job_id", jobID, "device", deviceID, "trace_job", traceID)
			if stopErr := o.traceMgr.StopTrace(traceID); stopErr != nil {
				slog.Warn("trace stop on cancel failed", "trace_job", traceID, "error", stopErr)
			}
			job.clearActiveTrace(deviceID)
		}
	}

	// logcat 도 같이 정리한다.
	//
	// ⚠ 빠뜨리면 취소 후에도 adb logcat 자식 프로세스가 계속 살아 로그를 쌓는다.
	// 화면상으로는 잡이 취소돼 정상처럼 보이므로 **조용히 새는 경로**가 된다.
	for deviceID, logcatID := range job.getActiveLogcatIDs() {
		if o.logcatMgr != nil && logcatID != "" {
			slog.Info("stopping logcat on job cancel",
				"job_id", jobID, "device", deviceID, "logcat_job", logcatID)
			if stopErr := o.logcatMgr.StopLogcat(logcatID); stopErr != nil {
				slog.Warn("logcat stop on cancel failed", "logcat_job", logcatID, "error", stopErr)
			}
			job.clearActiveLogcat(deviceID)
		}
	}

	job.mu.Lock()
	defer job.mu.Unlock()
	if job.State != pb.JobState_JOB_STATE_QUEUED && job.State != pb.JobState_JOB_STATE_RUNNING &&
		job.State != pb.JobState_JOB_STATE_PUSHING_TOOLS && job.State != pb.JobState_JOB_STATE_COLLECTING {
		return fmt.Errorf("job not running: %s", jobID)
	}
	if job.cancelFunc != nil {
		job.cancelFunc()
	}
	slog.Info("job cancel requested", "job_id", jobID)
	return nil
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
	// CANCELLED 도 터미널 상태다. 빠지면 취소로 끝난 잡에 재구독 시 채널이 닫히지
	// 않고 subscriber 로 등록돼 complete 이벤트를 못 받아 카드가 running 으로 hang 된다.
	finished := job.State == pb.JobState_JOB_STATE_COMPLETED ||
		job.State == pb.JobState_JOB_STATE_FAILED ||
		job.State == pb.JobState_JOB_STATE_PARTIALLY_FAILED ||
		job.State == pb.JobState_JOB_STATE_CANCELLED
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

// defaultToolNameFor 은 config override 가 없을 때 쓰는 기본 파일명.
// orchestrator.resolveToolName 을 거쳐 호출하세요 — 직접 호출하지 말 것.
func defaultToolNameFor(tool pb.BenchmarkTool) string {
	switch tool {
	case pb.BenchmarkTool_BENCHMARK_TOOL_FIO:
		return "fio"
	case pb.BenchmarkTool_BENCHMARK_TOOL_IOZONE:
		return "iozone"
	case pb.BenchmarkTool_BENCHMARK_TOOL_TIOTEST:
		return "tiotest"
	case pb.BenchmarkTool_BENCHMARK_TOOL_IOTEST:
		return "iotest"
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
	case pb.BenchmarkTool_BENCHMARK_TOOL_IOTEST:
		return buildIOTestCommand(remotePath, params)
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
	case pb.BenchmarkTool_BENCHMARK_TOOL_IOTEST:
		return parseIOTestResults(output)
	default:
		return nil
	}
}
