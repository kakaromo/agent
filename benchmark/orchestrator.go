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

	// stepBoundaries — deviceID → 스텝 실행 구간 목록.
	//
	// behavior 구간별 IO 분석의 시간 축이다. activeTraceIDs 와 같은 패턴으로 Job 에
	// 달아 두는 이유: storeResult 호출부가 10곳 가까이 되는데(성공/실패/취소 경로가
	// 제각각) 인자로 넘기면 한 곳만 빠뜨려도 그 경로에서 조용히 구간이 사라진다.
	stepBoundaries map[string][]*pb.StepBoundary
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

// takeStepBoundaries — 기록된 구간을 복사해 돌려준다 (호출자가 슬라이스를 들고
// 나가므로 내부 상태와 공유하지 않는다).
func (j *Job) takeStepBoundaries(deviceID string) []*pb.StepBoundary {
	j.mu.Lock()
	defer j.mu.Unlock()
	src := j.stepBoundaries[deviceID]
	if len(src) == 0 {
		return nil
	}
	out := make([]*pb.StepBoundary, len(src))
	copy(out, src)
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
	mu          sync.RWMutex
	jobs        map[string]*Job
	manager     *adb.Manager
	toolsDir    string
	toolNames   map[pb.BenchmarkTool]string // 도구별 파일명 override (nil/빈 항목은 기본명 사용)
	traceMgr    TraceController
	macroMgr    MacroController
	apkMgr      ApkController
	deviceLocks map[string]*sync.Mutex // per-device lock for "wait" policy
	finishHook  JobFinishHook          // nil 가능 (사무실 모드 등 DB 없을 때)
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
