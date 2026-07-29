package benchmark

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
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

	if req.HasBranching && len(req.Edges) > 0 {
		// DAG 실행 모드 (조건 분기 포함)
		go o.executeScenarioDAG(jobCtx, job, deviceIDs, req.Steps, req.Edges, req.Repeat, policy)
	} else {
		// 기존 선형 실행 모드
		expanded := expandSteps(req.Steps, req.Loops, req.Repeat)
		go o.executeScenario(jobCtx, job, deviceIDs, expanded, policy)
	}
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
	completed, failed, cancelled := 0, 0, 0
	var failMsg string // 실패/취소 device 의 첫 에러 메시지 (DB error_message 로 전달)
	for _, ds := range job.DeviceStatuses {
		switch ds.State {
		case pb.JobState_JOB_STATE_COMPLETED:
			completed++
		case pb.JobState_JOB_STATE_FAILED:
			failed++
			if failMsg == "" {
				failMsg = ds.Message
			}
		case pb.JobState_JOB_STATE_CANCELLED:
			cancelled++
		}
	}
	total := len(job.DeviceStatuses)
	switch {
	case cancelled > 0:
		// 하나라도 취소됐으면 job 은 취소로 본다 (cancel 은 사용자 명시 의도 — COMPLETED 로 덮지 않는다).
		job.State = pb.JobState_JOB_STATE_CANCELLED
	case failed == total:
		job.State = pb.JobState_JOB_STATE_FAILED
	case failed > 0:
		job.State = pb.JobState_JOB_STATE_PARTIALLY_FAILED
	default:
		job.State = pb.JobState_JOB_STATE_COMPLETED
	}
	finalState := job.State
	job.mu.Unlock()

	slog.Info("scenario finished", "job_id", job.ID, "state", finalState)
	// SSE 구독 여부와 무관하게 최종 상태를 DB 에 반영한다 (cancel 시 running 잔존 버그 방지).
	// 실패면 어느 스텝이 왜 실패했는지 error_message 로 함께 저장한다.
	o.fireFinishHook(job.ID, jobStateHookString(finalState), failMsg)
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

	// 종료 시 active trace 정리
	defer func() {
		slog.Info("scenario defer cleanup", "activeTraceJobID", activeTraceJobID, "hasTraceMgr", o.traceMgr != nil)
		if activeTraceJobID != "" && o.traceMgr != nil {
			slog.Info("cleaning up active trace on scenario end", "trace_job", activeTraceJobID)
			if err := o.traceMgr.StopTrace(activeTraceJobID); err != nil {
				slog.Warn("trace cleanup stop failed", "error", err)
			} else {
				slog.Info("trace cleanup stop success", "trace_job", activeTraceJobID)
			}
		}
	}()

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

		prevTraceID := activeTraceJobID
		stepOut, stepMetrics, err := o.executeStep(ctx, job, md, es, i, stepFiles, deviceID, &activeTraceJobID)

		// trace 상태가 바뀌었으면 job에 등록/해제 (cancel 시 정리용)
		if activeTraceJobID != prevTraceID {
			if activeTraceJobID != "" {
				job.setActiveTrace(deviceID, activeTraceJobID)
			} else {
				job.clearActiveTrace(deviceID)
			}
		}

		if err != nil {
			// 실행 중인 스텝이 취소로 중단된 경우는 실패가 아니라 취소로 기록한다.
			// (스텝 경계의 ctx.Err() 체크는 실행 중 취소를 잡지 못하고 이 분기가 먼저 잡는다)
			if ctx.Err() != nil {
				slog.Info("scenario step cancelled", "job_id", job.ID, "device", deviceID, "step", es.stepIndex, "type", es.step.Type)
				o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", progress)
				o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, false, "cancelled", traceJobMappings...)
				return
			}
			errMsg := fmt.Sprintf("%s failed: %s", msg, err.Error())
			slog.Error("scenario step failed", "job_id", job.ID, "device", deviceID, "step", es.stepIndex, "type", es.step.Type, "error", err)
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

		// Send step completion with results (benchmark 결과 또는 trace 정보 포함)
		stepProgress := int32(float64(i+1) / float64(totalSteps) * 100)
		if len(stepMetrics) > 0 || strings.Contains(stepOut, "TRACE_STOP|") || strings.Contains(stepOut, "TRACE_START|") {
			o.updateDeviceStatusWithResult(job, deviceID, pb.JobState_JOB_STATE_RUNNING,
				msg+" completed", stepProgress, prefixedMetrics, stepOut)
		} else {
			o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_RUNNING,
				msg+" completed", stepProgress)
		}
	}

	// 루프 완주 후에도 cancel 여부를 최종 확인한다. 마지막 스텝이 cancel 로 깨어나 정상 리턴한 경우
	// (예: scroll pause 가 sleepCtx 로 중단됨) 스텝 경계 체크를 지나쳐 여기 도달할 수 있으므로,
	// COMPLETED 로 기록하기 전에 ctx 취소를 반영해 CANCELLED 로 마무리한다.
	if ctx.Err() != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", 0)
		o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, false, "cancelled", traceJobMappings...)
		return
	}

	o.updateDeviceStatusWithResult(job, deviceID, pb.JobState_JOB_STATE_COMPLETED, "done", 100, allMetrics, rawOutput.String())
	o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, true, "", traceJobMappings...)
}

const testDir = "/data/local/tmp/test"

func (o *Orchestrator) executeStep(ctx context.Context, job *Job, md *adb.ManagedDevice, es expandedStep, execIndex int, stepFiles map[int]string, deviceID string, activeTraceJobID *string) (string, map[string]float64, error) {
	step := es.step

	// Loop 변수 치환: 콤마 구분 리스트 값을 loop index로 선택
	// 예: bs="4k,8k,16k,32k" + loopIndex=2 → bs="8k"
	// repeat 변수도 지원: size="1G,2G,4G" + repeatIndex=2 → size="2G"
	if es.loopTotal > 0 || es.repeatTotal > 1 {
		resolvedParams := make(map[string]string, len(step.Params))
		for k, v := range step.Params {
			resolvedParams[k] = resolveLoopVariable(v, es.loopIndex, es.loopTotal, es.repeatIndex, es.repeatTotal)
		}
		// 원본 step을 변경하지 않고 복사본 사용.
		// type=app_macro / condition step 은 Macro / Condition 필드가 핵심이므로 함께 복사해야 한다
		// (예전엔 Type/Tool/Params 만 복사해서 macro 가 nil 이 되어 'missing macro config' 에러 발생).
		stepCopy := &pb.ScenarioStep{
			Type:      step.Type,
			Tool:      step.Tool,
			Params:    resolvedParams,
			Condition: step.Condition,
			Macro:     step.Macro,
		}
		step = stepCopy
		es.step = step
	}

	// Auto trace wrapping: if params has trace="on", wrap step with trace start/stop
	// Skip if trace is already active (manual trace_start/trace_stop in progress)
	if step.Params["trace"] == "on" && o.traceMgr != nil {
		if *activeTraceJobID != "" {
			// Trace already running, skip auto trace and notify user
			slog.Info("skipping auto trace: trace already active", "active_trace", *activeTraceJobID)
			out, metrics, err := o.executeStepInner(ctx, job, md, es, execIndex, stepFiles, deviceID, activeTraceJobID)
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

		// auto trace의 job ID를 activeTraceJobID에 저장 (defer cleanup용)
		*activeTraceJobID = traceJobID

		// Execute the actual step
		out, metrics, stepErr := o.executeStepInner(ctx, job, md, es, execIndex, stepFiles, deviceID, activeTraceJobID)

		// Stop trace regardless of step result or context cancellation
		slog.Info("stopping auto trace", "trace_job", traceJobID)
		if stopErr := o.traceMgr.StopTrace(traceJobID); stopErr != nil {
			slog.Warn("auto trace stop failed", "error", stopErr)
		}
		*activeTraceJobID = "" // 정리 완료

		// Prepend/append trace info to output
		traceStart := fmt.Sprintf("TRACE_START|loop=%d|step=%d|repeat=%d|job_id=%s|trace_type=%s",
			es.loopIndex, es.stepIndex, es.repeatIndex, traceJobID, traceType)
		traceStop := fmt.Sprintf("TRACE_STOP|loop=%d|step=%d|repeat=%d|job_id=%s|trace_type=%s",
			es.loopIndex, es.stepIndex, es.repeatIndex, traceJobID, traceType)
		fullOut := traceStart + "\n" + out + "\n" + traceStop

		return fullOut, metrics, stepErr
	}

	return o.executeStepInner(ctx, job, md, es, execIndex, stepFiles, deviceID, activeTraceJobID)
}

func (o *Orchestrator) executeStepInner(ctx context.Context, job *Job, md *adb.ManagedDevice, es expandedStep, execIndex int, stepFiles map[int]string, deviceID string, activeTraceJobID *string) (string, map[string]float64, error) {
	step := es.step

	switch step.Type {
	case "benchmark":
		return o.executeBenchmarkStep(ctx, md, step, es, execIndex, stepFiles)
	case "iotest":
		// iotest stderr 진행률을 IOTEST|... 메시지로 forward (frontend ScenarioCanvas 가 파싱).
		onProg := func(line string) {
			if job == nil {
				return
			}
			job.notify(&pb.JobProgress{
				JobId:    job.ID,
				DeviceId: deviceID,
				State:    pb.JobState_JOB_STATE_RUNNING,
				Message:  line,
			})
		}
		return o.executeIOTestStep(ctx, md, step, onProg)
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
		// ctx 취소를 존중하는 대기 — cancel 시 즉시 깨어난다(순수 time.Sleep 은 sec 를 다 채워 cancel 을 무시).
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(time.Duration(sec) * time.Second):
		}
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
	case "app_macro":
		if o.macroMgr == nil {
			return "", nil, fmt.Errorf("macro manager not configured")
		}
		macroConfig := step.Macro
		if macroConfig == nil {
			return "", nil, fmt.Errorf("app_macro step missing macro config")
		}
		// Build replay request
		replayReq := &pb.ReplayMacroRequest{
			DeviceId:     deviceID,
			Events:       macroConfig.Events,
			SourceWidth:  macroConfig.SourceWidth,
			SourceHeight: macroConfig.SourceHeight,
		}
		// Initialize device: wake up + home + unlock
		slog.Info("app_macro: initializing device", "device", deviceID)
		md.Device.Shell(ctx, "input keyevent KEYCODE_WAKEUP")    // 화면 깨우기
		time.Sleep(500 * time.Millisecond)
		md.Device.Shell(ctx, "input keyevent KEYCODE_HOME")      // 홈 화면
		time.Sleep(500 * time.Millisecond)
		md.Device.Shell(ctx, "input swipe 540 2000 540 800 300") // 잠금 화면 스와이프 해제
		time.Sleep(1 * time.Second)

		// Launch app if packageName specified
		if macroConfig.PackageName != "" {
			clearMode := macroConfig.ClearMode
			if clearMode == "" {
				clearMode = "force_stop" // 기본값
			}
			switch clearMode {
			case "clear":
				// pm clear: 앱 데이터 + 캐시 전체 초기화 (첫 실행 상태)
				slog.Info("app_macro: pm clear", "package", macroConfig.PackageName)
				md.Device.Shell(ctx, fmt.Sprintf("pm clear %s", macroConfig.PackageName))
				time.Sleep(1 * time.Second)
			case "force_stop":
				// 앱만 종료, 데이터 유지
				md.Device.Shell(ctx, fmt.Sprintf("am force-stop %s", macroConfig.PackageName))
				time.Sleep(500 * time.Millisecond)
			case "none":
				// 아무것도 안 함
			}
			md.Device.Shell(ctx, fmt.Sprintf("monkey -p %s -c android.intent.category.LAUNCHER 1", macroConfig.PackageName))
			time.Sleep(2 * time.Second)
		}
		resp, err := o.macroMgr.ReplayMacro(ctx, replayReq)
		if err != nil {
			return "", nil, fmt.Errorf("replay macro: %w", err)
		}
		// Collect metrics from OCR results
		metrics := make(map[string]float64)
		for k, v := range resp.Metrics {
			metrics[k] = v
		}
		var outParts []string
		outParts = append(outParts, fmt.Sprintf("APP_MACRO|name=%s|success=%t", macroConfig.MacroName, resp.Success))
		for k, v := range resp.OcrResults {
			outParts = append(outParts, fmt.Sprintf("OCR|%s=%s", k, v))
		}
		return strings.Join(outParts, "\n"), metrics, nil
	case "install_apk":
		if o.apkMgr == nil {
			return "", nil, fmt.Errorf("apk manager not configured")
		}
		filename := step.Params["apk_filename"]
		if filename == "" {
			return "", nil, fmt.Errorf("install_apk step missing 'apk_filename' param")
		}
		resp, err := o.apkMgr.Install(ctx, &pb.InstallApkRequest{
			DeviceId:                deviceID,
			ApkFilename:             filename,
			Reinstall:               true,
			GrantRuntimePermissions: step.Params["grant_permissions"] == "true",
		})
		if err != nil {
			msg := ""
			if resp != nil {
				msg = resp.Message
			}
			return msg, nil, fmt.Errorf("install_apk %s: %w", filename, err)
		}
		out := fmt.Sprintf("INSTALL_APK|filename=%s|package=%s\n%s", filename, resp.PackageName, resp.Message)
		return out, nil, nil
	case "uninstall_apk":
		if o.apkMgr == nil {
			return "", nil, fmt.Errorf("apk manager not configured")
		}
		pkg := step.Params["package_name"]
		if pkg == "" {
			return "", nil, fmt.Errorf("uninstall_apk step missing 'package_name' param")
		}
		resp, err := o.apkMgr.Uninstall(ctx, &pb.UninstallApkRequest{
			DeviceId:    deviceID,
			PackageName: pkg,
			KeepData:    step.Params["keep_data"] == "true",
		})
		if err != nil {
			msg := ""
			if resp != nil {
				msg = resp.Message
			}
			return msg, nil, fmt.Errorf("uninstall_apk %s: %w", pkg, err)
		}
		out := fmt.Sprintf("UNINSTALL_APK|package=%s\n%s", pkg, resp.Message)
		return out, nil, nil
	case "tap_element":
		// 요소 기반 탭 — 단일 MacroEvent 를 replay 로 실행 (요소 재탐색 + 좌표 폴백은 replayer 담당)
		if o.macroMgr == nil {
			return "", nil, fmt.Errorf("macro manager not configured")
		}
		ev := &pb.MacroEvent{
			Type:               "tap_element",
			ElementResourceId:  step.Params["element_resource_id"],
			ElementText:        step.Params["element_text"],
			ElementContentDesc: step.Params["element_content_desc"],
			ElementMatchMode:   step.Params["element_match_mode"],
			ElementContainerId: step.Params["element_container_id"],
		}
		if v, err := strconv.Atoi(step.Params["x"]); err == nil {
			ev.X = int32(v)
		}
		if v, err := strconv.Atoi(step.Params["y"]); err == nil {
			ev.Y = int32(v)
		}
		if v, err := strconv.Atoi(step.Params["element_index"]); err == nil {
			ev.ElementIndex = int32(v)
		}
		resp, err := o.macroMgr.ReplayMacro(ctx, &pb.ReplayMacroRequest{
			DeviceId: deviceID,
			Events:   []*pb.MacroEvent{ev},
		})
		if err != nil {
			return "", nil, fmt.Errorf("tap_element: %w", err)
		}
		metrics := make(map[string]float64)
		for k, v := range resp.Metrics {
			metrics[k] = v
		}
		// 요소를 못 찾고 폴백 좌표도 없었으면 스텝 실패로 처리한다.
		// (요소를 못 찾았는데 성공으로 넘어가는 silent failure 방지 — replayer 가 재탐색까지 한 뒤의 결과.)
		if metrics["tap_element_not_found"] > 0 {
			return "", nil, fmt.Errorf("tap_element: 요소를 찾을 수 없습니다 (resource_id=%q text=%q content_desc=%q). 재시도 후에도 화면에서 대상을 못 찾았습니다",
				step.Params["element_resource_id"], step.Params["element_text"], step.Params["element_content_desc"])
		}
		return fmt.Sprintf("TAP_ELEMENT|id=%s|text=%s|success=%t",
			step.Params["element_resource_id"], step.Params["element_text"], resp.Success), metrics, nil
	case "tap":
		// 절대 좌표 탭. 커스텀뷰(요소 미노출) 화면 — 삼성 노트 AI 메뉴, 게임 등 —
		// tap_element 로 못 잡는 버튼을 좌표로 직접 누른다. 스케일링 없이 raw 좌표 그대로.
		x, errX := strconv.Atoi(step.Params["x"])
		y, errY := strconv.Atoi(step.Params["y"])
		if errX != nil || errY != nil {
			return "", nil, fmt.Errorf("tap step needs valid 'x','y' params")
		}
		md.Device.Shell(ctx, fmt.Sprintf("input tap %d %d", x, y))
		return fmt.Sprintf("TAP|x=%d|y=%d", x, y), nil, nil
	case "text":
		// input text — 단일 MacroEvent 로 실행. submit=true 면 입력 후 Enter(66).
		if o.macroMgr == nil {
			return "", nil, fmt.Errorf("macro manager not configured")
		}
		ev := &pb.MacroEvent{Type: "text", InputText: step.Params["input_text"]}
		if step.Params["submit"] == "true" {
			ev.Keycode = 66 // KEYCODE_ENTER
		}
		resp, err := o.macroMgr.ReplayMacro(ctx, &pb.ReplayMacroRequest{
			DeviceId: deviceID,
			Events:   []*pb.MacroEvent{ev},
		})
		if err != nil {
			return "", nil, fmt.Errorf("text: %w", err)
		}
		return fmt.Sprintf("TEXT|input=%s|submit=%s|success=%t",
			step.Params["input_text"], step.Params["submit"], resp.Success), nil, nil
	case "scroll":
		// 유저처럼 피드 반복 스크롤 (워크로드 재현)
		if o.macroMgr == nil {
			return "", nil, fmt.Errorf("macro manager not configured")
		}
		ev := &pb.MacroEvent{Type: "scroll", Direction: step.Params["direction"]}
		if v, err := strconv.Atoi(step.Params["count"]); err == nil {
			ev.MaxScrolls = int32(v)
		}
		if v, err := strconv.Atoi(step.Params["pause"]); err == nil {
			ev.ScrollPause = int32(v)
		}
		if v, err := strconv.Atoi(step.Params["duration"]); err == nil {
			ev.Duration = int32(v)
		}
		resp, err := o.macroMgr.ReplayMacro(ctx, &pb.ReplayMacroRequest{
			DeviceId: deviceID,
			Events:   []*pb.MacroEvent{ev},
		})
		if err != nil {
			return "", nil, fmt.Errorf("scroll: %w", err)
		}
		return fmt.Sprintf("SCROLL|dir=%s|count=%d|success=%t",
			step.Params["direction"], ev.MaxScrolls, resp.Success), nil, nil
	case "key":
		// 범용 키 이벤트 — BACK(4)/HOME(3)/일시정지(85) 등 제어 동작.
		if o.macroMgr == nil {
			return "", nil, fmt.Errorf("macro manager not configured")
		}
		keycode := 0
		if v, err := strconv.Atoi(step.Params["keycode"]); err == nil {
			keycode = v
		}
		if keycode <= 0 {
			return "", nil, fmt.Errorf("key step missing valid 'keycode' param")
		}
		resp, err := o.macroMgr.ReplayMacro(ctx, &pb.ReplayMacroRequest{
			DeviceId: deviceID,
			Events:   []*pb.MacroEvent{{Type: "key", Keycode: int32(keycode)}},
		})
		if err != nil {
			return "", nil, fmt.Errorf("key: %w", err)
		}
		return fmt.Sprintf("KEY|keycode=%d|success=%t", keycode, resp.Success), nil, nil
	case "stop_app":
		// 앱을 완전히 종료한다 (am force-stop). 유튜브 PIP 처럼 뒤로가기로 안 멈추는
		// 백그라운드 재생까지 확실히 중단 — 워크로드 측정 종료용.
		// 패키지는 보통 UI serializer 가 앞선 launch_app 에서 자동 채움. 그래도 비면
		// 시나리오 전체를 실패시키지 않고 경고 후 skip (관대한 처리).
		pkg := step.Params["package_name"]
		if pkg == "" {
			slog.Warn("stop_app: package_name 없음 — skip (앞선 launch_app 이 없거나 패키지 미지정)")
			return "STOP_APP|skipped (no package)", nil, nil
		}
		md.Device.Shell(ctx, fmt.Sprintf("am force-stop %s", pkg))
		return fmt.Sprintf("STOP_APP|pkg=%s", pkg), nil, nil
	case "launch_app":
		// 앱을 지정 초기화 모드로 깨끗하게 시작한다 (AnTuTu 등 cold start 재현).
		pkg := step.Params["package_name"]
		if pkg == "" {
			return "", nil, fmt.Errorf("launch_app step missing 'package_name' param")
		}
		mode := step.Params["clear_mode"]
		if mode == "" {
			mode = "force_stop"
		}

		// 디바이스 준비: 깨우기 + 홈 + 잠금 해제
		md.Device.Shell(ctx, "input keyevent KEYCODE_WAKEUP")
		time.Sleep(500 * time.Millisecond)
		md.Device.Shell(ctx, "input keyevent KEYCODE_HOME")
		time.Sleep(500 * time.Millisecond)
		md.Device.Shell(ctx, "input swipe 540 2000 540 800 300")
		time.Sleep(1 * time.Second)

		switch mode {
		case "clear":
			// 데이터+캐시 전체 초기화 (첫 실행 상태)
			slog.Info("launch_app: pm clear", "package", pkg)
			md.Device.Shell(ctx, fmt.Sprintf("pm clear %s", pkg))
			time.Sleep(1 * time.Second)
		case "cache":
			// 캐시만 삭제, 데이터 유지 (SDK 지원 시)
			slog.Info("launch_app: pm clear --cache-only", "package", pkg)
			md.Device.Shell(ctx, fmt.Sprintf("pm clear --cache-only %s", pkg))
			time.Sleep(1 * time.Second)
		case "force_stop":
			md.Device.Shell(ctx, fmt.Sprintf("am force-stop %s", pkg))
			time.Sleep(500 * time.Millisecond)
		case "none":
			// 초기화 없이 실행
		}

		// 실행
		md.Device.Shell(ctx, fmt.Sprintf("monkey -p %s -c android.intent.category.LAUNCHER 1", pkg))

		// 실행 후 대기: wait_activity 지정 시 해당 activity 포커스까지, 아니면 고정 대기.
		waitActivity := step.Params["wait_activity"]
		waitSec := 3
		if v, err := strconv.Atoi(step.Params["wait_seconds"]); err == nil && v >= 0 {
			waitSec = v
		}
		if waitActivity != "" {
			deadline := time.Now().Add(time.Duration(waitSec) * time.Second)
			if waitSec <= 0 {
				deadline = time.Now().Add(30 * time.Second) // activity 대기 기본 상한
			}
			matched := false
			for time.Now().Before(deadline) {
				focus, _ := md.Device.Shell(ctx, "dumpsys window | grep mCurrentFocus")
				if strings.Contains(focus, waitActivity) {
					matched = true
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			slog.Info("launch_app: wait_activity", "pattern", waitActivity, "matched", matched)
		} else {
			time.Sleep(time.Duration(waitSec) * time.Second)
		}
		return fmt.Sprintf("LAUNCH_APP|pkg=%s|mode=%s", pkg, mode), nil, nil
	case "condition":
		// 선형 실행 모드에서 condition은 스킵 (DAG 모드에서만 처리)
		slog.Info("skipping condition step in linear mode", "step", es.stepIndex)
		return "CONDITION_SKIPPED (linear mode)", nil, nil
	default:
		return "", nil, fmt.Errorf("unknown step type: %s", step.Type)
	}
}

func (o *Orchestrator) executeBenchmarkStep(ctx context.Context, md *adb.ManagedDevice, step *pb.ScenarioStep, es expandedStep, execIndex int, stepFiles map[int]string) (string, map[string]float64, error) {
	tool := step.Tool
	toolName := o.resolveToolName(tool)
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
		// progress 메시지는 default 이름으로 — 파일명 override 가 있어도 UI 표시는 'fio' 등 표준명이 자연스럽다.
		toolName := defaultToolNameFor(es.step.Tool)
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
	case "iotest":
		stepDesc += ": iotest"
	case "sleep":
		stepDesc += fmt.Sprintf(": sleep %ss", es.step.Params["seconds"])
	case "install_apk":
		stepDesc += fmt.Sprintf(": install_apk(%s)", es.step.Params["apk_filename"])
	case "uninstall_apk":
		stepDesc += fmt.Sprintf(": uninstall_apk(%s)", es.step.Params["package_name"])
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

// ══════════════════════════════════════════════════════════════
// DAG Execution Mode (조건 분기 지원)
// ══════════════════════════════════════════════════════════════

func (o *Orchestrator) executeScenarioDAG(ctx context.Context, job *Job, deviceIDs []string,
	steps []*pb.ScenarioStep, edges []*pb.StepEdge, repeat int32, policy string) {
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
			o.runScenarioOnDeviceDAG(ctx, job, devID, steps, edges, repeat)
		}(deviceID)
	}
	wg.Wait()

	// Determine final state (same as linear mode)
	job.mu.Lock()
	completed, failed, cancelled := 0, 0, 0
	var failMsg string
	for _, ds := range job.DeviceStatuses {
		switch ds.State {
		case pb.JobState_JOB_STATE_COMPLETED:
			completed++
		case pb.JobState_JOB_STATE_FAILED:
			failed++
			if failMsg == "" {
				failMsg = ds.Message
			}
		case pb.JobState_JOB_STATE_CANCELLED:
			cancelled++
		}
	}
	total := len(job.DeviceStatuses)
	if cancelled > 0 {
		job.State = pb.JobState_JOB_STATE_CANCELLED
	} else if failed == total {
		job.State = pb.JobState_JOB_STATE_FAILED
	} else if failed > 0 {
		job.State = pb.JobState_JOB_STATE_PARTIALLY_FAILED
	} else {
		job.State = pb.JobState_JOB_STATE_COMPLETED
	}
	finalState := job.State
	job.mu.Unlock()

	slog.Info("scenario DAG finished", "job_id", job.ID, "state", finalState)
	o.fireFinishHook(job.ID, jobStateHookString(finalState), failMsg)
}

func (o *Orchestrator) runScenarioOnDeviceDAG(ctx context.Context, job *Job, deviceID string,
	steps []*pb.ScenarioStep, edges []*pb.StepEdge, repeat int32) {
	md, err := o.manager.GetDevice(deviceID)
	if err != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, err.Error(), 0)
		return
	}

	o.manager.SetState(deviceID, pb.DeviceState_DEVICE_STATE_BUSY)
	defer o.manager.SetState(deviceID, pb.DeviceState_DEVICE_STATE_ONLINE)

	startedAt := time.Now().UnixMilli()
	var rawOutput strings.Builder
	allMetrics := make(map[string]float64)
	stepFiles := make(map[int]string)
	var activeTraceJobID string
	var traceJobMappings []*pb.TraceJobMapping

	// 종료 시 active trace 정리
	defer func() {
		if activeTraceJobID != "" && o.traceMgr != nil {
			slog.Info("cleaning up active trace on DAG scenario end", "trace_job", activeTraceJobID)
			o.traceMgr.StopTrace(activeTraceJobID)
		}
	}()

	// Build adjacency: from_step -> [{to_step, label}]
	type edgeInfo struct {
		toStep int
		label  string
	}
	adj := make(map[int][]edgeInfo)
	for _, e := range edges {
		adj[int(e.FromStep)] = append(adj[int(e.FromStep)], edgeInfo{toStep: int(e.ToStep), label: e.Label})
	}

	if repeat <= 0 {
		repeat = 1
	}

	// Last benchmark metrics (for condition evaluation)
	lastBenchmarkMetrics := make(map[string]float64)

	totalRepeat := int(repeat)
	executedSteps := 0

	for ri := 1; ri <= totalRepeat; ri++ {
		// DAG walk starting from step 0
		currentStep := 0
		visited := make(map[int]int) // step -> visit count (for loop detection safety)

		for currentStep >= 0 && currentStep < len(steps) {
			if ctx.Err() != nil {
				o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", 0)
				o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, false, "cancelled", traceJobMappings...)
				return
			}

			visited[currentStep]++
			if visited[currentStep] > 1000 {
				errMsg := fmt.Sprintf("infinite loop detected at step %d", currentStep)
				o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, errMsg, 0)
				o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, false, errMsg, traceJobMappings...)
				return
			}

			step := steps[currentStep]
			executedSteps++

			msg := fmt.Sprintf("repeat %d/%d, step %d: %s", ri, totalRepeat, currentStep, step.Type)
			o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_RUNNING, msg, 0)

			// Condition node: evaluate and branch
			if step.Type == "condition" && step.Condition != nil {
				cond := step.Condition
				condResult := false
				condMsg := ""

				// 스토리지 메트릭 수집 (condition 평가 전 항상)
				dfOut, _ := md.Device.Shell(ctx, "df /data")
				if storageMetrics := parseDfOutput(dfOut); storageMetrics != nil {
					for k, v := range storageMetrics {
						lastBenchmarkMetrics[k] = v
					}
				}

				if len(cond.Rules) > 0 {
					// 복합 조건: rules + logic (and/or)
					condResult, condMsg = evaluateCompoundCondition(ctx, md, cond, lastBenchmarkMetrics)
				} else {
					// 단일 조건 (하위 호환)
					condResult, condMsg = evaluateSingleRule(ctx, md, cond.Source, cond.MetricKey, cond.Operator, cond.Threshold, cond.ThresholdString, cond.ShellCommand, cond.ExtractPattern, lastBenchmarkMetrics)
				}

				slog.Info("condition evaluated", "job_id", job.ID, "device", deviceID,
					"step", currentStep, "result", condResult, "msg", condMsg)

				rawOutput.WriteString(fmt.Sprintf("=== step %d: condition ===\n%s\n", currentStep, condMsg))

				if condResult {
					currentStep = int(cond.TrueBranchStep)
				} else {
					currentStep = int(cond.FalseBranchStep)
				}
				continue
			}

			// Regular step execution
			es := expandedStep{
				step:        step,
				stepIndex:   currentStep,
				repeatIndex: ri,
				repeatTotal: totalRepeat,
			}

			prevTraceID := activeTraceJobID
			stepOut, stepMetrics, execErr := o.executeStep(ctx, job, md, es, executedSteps-1, stepFiles, deviceID, &activeTraceJobID)

			// trace 상태 변경 → job에 등록/해제
			if activeTraceJobID != prevTraceID {
				if activeTraceJobID != "" {
					job.setActiveTrace(deviceID, activeTraceJobID)
				} else {
					job.clearActiveTrace(deviceID)
				}
			}

			if execErr != nil {
				// 실행 중 취소는 실패가 아니라 취소로 기록 (선형 모드와 동일)
				if ctx.Err() != nil {
					slog.Info("scenario step cancelled", "job_id", job.ID, "device", deviceID, "step", currentStep, "type", step.Type)
					o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", 0)
					o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, false, "cancelled", traceJobMappings...)
					return
				}
				errMsg := fmt.Sprintf("step %d (%s) failed: %s", currentStep, step.Type, execErr.Error())
				o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_FAILED, errMsg, 0)
				o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, false, errMsg, traceJobMappings...)
				return
			}

			// Collect trace mappings
			if strings.Contains(stepOut, "TRACE_STOP|") {
				for _, line := range strings.Split(stepOut, "\n") {
					if strings.HasPrefix(line, "TRACE_STOP|") {
						mapping := parseTraceMapping(line, es)
						if mapping != nil {
							traceJobMappings = append(traceJobMappings, mapping)
						}
					}
				}
			}

			if stepOut != "" {
				rawOutput.WriteString(fmt.Sprintf("=== %s ===\n%s\n", msg, stepOut))
			}

			// Store metrics
			prefixedMetrics := make(map[string]float64)
			for k, v := range stepMetrics {
				prefix := fmt.Sprintf("r%d_step%d_", ri, currentStep)
				key := prefix + k
				allMetrics[key] = v
				prefixedMetrics[key] = v
			}

			// Send step completion (benchmark 결과 또는 trace 정보)
			if len(stepMetrics) > 0 || strings.Contains(stepOut, "TRACE_STOP|") || strings.Contains(stepOut, "TRACE_START|") {
				o.updateDeviceStatusWithResult(job, deviceID, pb.JobState_JOB_STATE_RUNNING,
					msg+" completed", 0, prefixedMetrics, stepOut)
			}

			// Update last benchmark metrics for condition evaluation
			if step.Type == "benchmark" && len(stepMetrics) > 0 {
				lastBenchmarkMetrics = stepMetrics
			}

			// Find next step via edges
			nextStep := -1
			outEdges := adj[currentStep]
			if len(outEdges) > 0 {
				// For non-condition nodes, take the first (or unlabeled) edge
				nextStep = outEdges[0].toStep
			}

			if nextStep < 0 {
				break // No outgoing edges: end of DAG path
			}
			currentStep = nextStep
		}
	}

	// DAG 완주 후에도 cancel 여부 최종 확인 (선형 경로와 동일 — cancel 로 깨어난 스텝이 정상 리턴한 경우 대비).
	if ctx.Err() != nil {
		o.updateDeviceStatus(job, deviceID, pb.JobState_JOB_STATE_CANCELLED, "cancelled", 0)
		o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, false, "cancelled", traceJobMappings...)
		return
	}

	o.updateDeviceStatusWithResult(job, deviceID, pb.JobState_JOB_STATE_COMPLETED, "done", 100, allMetrics, rawOutput.String())
	o.storeResult(job, deviceID, startedAt, rawOutput.String(), allMetrics, true, "", traceJobMappings...)
}

// evaluateShellCondition evaluates a shell command output against a condition.
// Supports:
//   - 숫자 비교: extract_pattern으로 숫자 추출 → threshold과 비교 (>, <, >=, <=, ==, !=)
//   - 문자열 비교: contains/!contains로 threshold_string 포함 여부 확인
//   - 정규식 추출: extract_pattern에 캡처 그룹 → 첫 번째 그룹에서 숫자 추출
func evaluateShellCondition(output string, cond *pb.ConditionalBranch) (bool, string) {
	op := cond.Operator

	// 문자열 비교 (contains / !contains)
	if op == "contains" || op == "!contains" {
		target := cond.ThresholdString
		if target == "" {
			target = fmt.Sprintf("%.0f", cond.Threshold) // fallback
		}
		has := strings.Contains(output, target)
		result := (op == "contains" && has) || (op == "!contains" && !has)
		msg := fmt.Sprintf("shell: output %q %s %q = %v", truncate(output, 100), op, target, result)
		return result, msg
	}

	// 숫자 비교: extract_pattern이 있으면 정규식으로 추출
	var numStr string
	if cond.ExtractPattern != "" {
		re, err := regexp.Compile(cond.ExtractPattern)
		if err != nil {
			return false, fmt.Sprintf("shell: invalid extract_pattern: %s", err.Error())
		}
		matches := re.FindStringSubmatch(output)
		if len(matches) >= 2 {
			numStr = matches[1] // 첫 번째 캡처 그룹
		} else if len(matches) >= 1 {
			numStr = matches[0] // 전체 매치
		}
	} else {
		// 패턴 없으면 출력에서 첫 번째 숫자 추출
		re := regexp.MustCompile(`[-+]?\d*\.?\d+`)
		if m := re.FindString(output); m != "" {
			numStr = m
		}
	}

	if numStr == "" {
		return false, fmt.Sprintf("shell: no number found in output %q", truncate(output, 100))
	}

	val, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
	if err != nil {
		return false, fmt.Sprintf("shell: cannot parse number %q: %s", numStr, err.Error())
	}

	result := evaluateCondition(val, op, cond.Threshold)
	msg := fmt.Sprintf("shell: extracted %.2f from %q, %s %.2f = %v", val, truncate(output, 60), op, cond.Threshold, result)
	return result, msg
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// evaluateSingleRule evaluates one condition rule (metric or shell).
func evaluateSingleRule(ctx context.Context, md *adb.ManagedDevice, source, metricKey, operator string, threshold float64, thresholdString, shellCommand, extractPattern string, metrics map[string]float64) (bool, string) {
	if source == "" {
		source = "metric"
	}

	if source == "shell" {
		shellOut, shellErr := md.Device.Shell(ctx, shellCommand)
		if shellErr != nil {
			return false, fmt.Sprintf("shell error: %s", shellErr.Error())
		}
		shellOut = strings.TrimSpace(shellOut)
		return evaluateShellCondition(shellOut, &pb.ConditionalBranch{
			Operator: operator, Threshold: threshold, ThresholdString: thresholdString,
			ExtractPattern: extractPattern,
		})
	}

	// metric
	val, ok := metrics[metricKey]
	if !ok {
		return false, fmt.Sprintf("metric %s not found", metricKey)
	}
	result := evaluateCondition(val, operator, threshold)
	return result, fmt.Sprintf("metric: %s=%.2f %s %.2f → %v", metricKey, val, operator, threshold, result)
}

// evaluateCompoundCondition evaluates multiple rules with AND/OR logic.
func evaluateCompoundCondition(ctx context.Context, md *adb.ManagedDevice, cond *pb.ConditionalBranch, metrics map[string]float64) (bool, string) {
	logic := cond.Logic
	if logic == "" {
		logic = "and"
	}

	var msgs []string
	finalResult := logic == "and" // and: start true, or: start false

	for i, rule := range cond.Rules {
		result, msg := evaluateSingleRule(ctx, md,
			rule.Source, rule.MetricKey, rule.Operator,
			rule.Threshold, rule.ThresholdString,
			rule.ShellCommand, rule.ExtractPattern, metrics)

		label := fmt.Sprintf("[%d] %s", i+1, msg)
		msgs = append(msgs, label)

		if logic == "and" {
			finalResult = finalResult && result
		} else {
			finalResult = finalResult || result
		}
	}

	summary := fmt.Sprintf("%s(%s) → %v", strings.ToUpper(logic), strings.Join(msgs, "; "), finalResult)
	return finalResult, summary
}

func evaluateCondition(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case "<":
		return value < threshold
	case ">=":
		return value >= threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	case "!=":
		return value != threshold
	default:
		return false
	}
}

// ══════════════════════════════════════════════════════════════
// Loop Variable Resolution
// ══════════════════════════════════════════════════════════════

// resolveLoopVariable resolves comma-separated list values by loop/repeat index.
//
// Supported patterns:
//   - "4k,8k,16k,32k"    → loop index로 선택 (1-based, 인덱스 초과 시 마지막 값)
//   - "{loop}G"           → loop index 숫자로 치환
//   - "{repeat}G"         → repeat index 숫자로 치환
//   - "{loop*2}G"         → loop index × 2로 치환
//   - "4k"                → 변경 없음 (단일 값)
func resolveLoopVariable(value string, loopIndex, loopTotal, repeatIndex, repeatTotal int) string {
	// 1. {loop}, {repeat}, {loop*N} 템플릿 치환
	if strings.Contains(value, "{") {
		result := value
		result = strings.ReplaceAll(result, "{loop}", strconv.Itoa(loopIndex))
		result = strings.ReplaceAll(result, "{repeat}", strconv.Itoa(repeatIndex))
		result = strings.ReplaceAll(result, "{loop_total}", strconv.Itoa(loopTotal))
		result = strings.ReplaceAll(result, "{repeat_total}", strconv.Itoa(repeatTotal))

		// {loop*N} 패턴: 예를 들어 {loop*1024} → loopIndex * 1024
		for {
			start := strings.Index(result, "{loop*")
			if start < 0 {
				break
			}
			end := strings.Index(result[start:], "}")
			if end < 0 {
				break
			}
			expr := result[start+6 : start+end] // "1024" 부분
			multiplier, err := strconv.Atoi(strings.TrimSpace(expr))
			if err == nil {
				computed := loopIndex * multiplier
				result = result[:start] + strconv.Itoa(computed) + result[start+end+1:]
			} else {
				break
			}
		}

		// {repeat*N} 패턴
		for {
			start := strings.Index(result, "{repeat*")
			if start < 0 {
				break
			}
			end := strings.Index(result[start:], "}")
			if end < 0 {
				break
			}
			expr := result[start+8 : start+end]
			multiplier, err := strconv.Atoi(strings.TrimSpace(expr))
			if err == nil {
				computed := repeatIndex * multiplier
				result = result[:start] + strconv.Itoa(computed) + result[start+end+1:]
			} else {
				break
			}
		}

		return result
	}

	// 2. 콤마 구분 리스트 → loop index로 선택
	if strings.Contains(value, ",") {
		parts := strings.Split(value, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		idx := 0
		if loopIndex > 0 {
			idx = loopIndex - 1 // loopIndex는 1-based
		} else if repeatIndex > 0 {
			idx = repeatIndex - 1
		}
		if idx >= len(parts) {
			idx = len(parts) - 1 // 범위 초과 시 마지막 값
		}
		if idx < 0 {
			idx = 0
		}
		return parts[idx]
	}

	// 3. 단일 값 → 그대로
	return value
}

// parseDfOutput parses "df /data" output to extract storage metrics.
// Output format: "Filesystem  1K-blocks  Used  Available  Use%  Mounted on"
// Returns: data_usage_percent, data_used_gb, data_avail_gb, data_total_gb
func parseDfOutput(output string) map[string]float64 {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return nil
	}
	// 마지막 줄이 /data 마운트 정보
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// Use% 필드에서 % 제거
		usePct := strings.TrimSuffix(fields[4], "%")
		pct, err := strconv.ParseFloat(usePct, 64)
		if err != nil {
			continue
		}

		// 1K-blocks 단위
		totalKB, _ := strconv.ParseFloat(fields[1], 64)
		usedKB, _ := strconv.ParseFloat(fields[2], 64)
		availKB, _ := strconv.ParseFloat(fields[3], 64)

		return map[string]float64{
			"data_usage_percent": pct,
			"data_used_gb":      usedKB / (1024 * 1024),
			"data_avail_gb":     availKB / (1024 * 1024),
			"data_total_gb":     totalKB / (1024 * 1024),
		}
	}
	return nil
}
