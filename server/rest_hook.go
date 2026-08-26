package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"agent/artifacts"
	pb "agent/pb"
	"agent/storage/sqlitedb"
)

// JobExecutionRecorder — REST 핸들러가 잡 시작/종료/결과를 DB 에 기록하는 진입점.
//
// rest.go 는 sqlitedb 를 직접 import 하지 않는다 (Phase 0 시점엔 DB 없었음).
// 대신 패키지 전역 hook 으로 등록해서 rest.go 의 핸들러가 DB 유무에 무관하게 동작하도록 한다.
// installJobExecutionHook(http.go) 가 standalone 모드 시 hook 을 활성화한다.
//
// portal AgentController 의 jobExecutionService.save / updateState 호출 패턴을 동일하게 따라간다.
type JobExecutionRecorder interface {
	OnStart(ctx context.Context, jobID, jobType, tool, jobName string, deviceIDs []string, body map[string]any)
	OnState(ctx context.Context, jobID, state, errMsg string)
	// OnResult — 잡이 terminal state 일 때 호출. agent 의 GetBenchmarkResult 응답을
	// summary JSON 으로 만들어 job_executions.result_summary 에 저장한다.
	// agent 재시작 후에도 Result 페이지에서 metrics 를 조회할 수 있도록 보존.
	OnResult(ctx context.Context, agent *DeviceAgentServer, jobID, jobType string)
}

var recorderRef atomic.Value // holds JobExecutionRecorder (interface), or nil

// currentRecorder — rest.go 핸들러가 호출. nil 이면 no-op.
func currentRecorder() JobExecutionRecorder {
	v := recorderRef.Load()
	if v == nil {
		return nil
	}
	r, _ := v.(JobExecutionRecorder)
	return r
}

// installJobExecutionHook — DB 있을 때 호출. portal 의 jobExecutionService 역할 단방향 등록.
// archiveBase 가 비어있지 않으면 잡 종료 시 benchmark 결과 JSON 을 디스크에도 자동 저장한다.
func installJobExecutionHook(agent *DeviceAgentServer, db *sqlitedb.DB, archiveBase string) {
	if db == nil {
		return
	}
	rec := &dbRecorder{db: db, archiveBase: archiveBase}
	recorderRef.Store(JobExecutionRecorder(rec))

	// job 종료 시 DB state 를 직접 갱신한다 (SSE 구독 여부와 무관 — cancel 시 running 잔존 버그 방지).
	// SSE 경로(sse.go)는 UI 가 progress 를 구독할 때만 동작하므로, 그와 별개로 항상 반영되도록 orchestrator hook 을 건다.
	if agent != nil && agent.orchestrator != nil {
		agent.orchestrator.SetJobFinishHook(func(jobID, state, errMsg string) {
			ctx := context.Background()
			rec.OnState(ctx, jobID, state, errMsg)
			rec.OnResult(ctx, agent, jobID, inferJobTypeFromAgent(agent, jobID))
		})
	}
}

// dbRecorder — SQLite 백엔드 구현.
type dbRecorder struct {
	db          *sqlitedb.DB
	archiveBase string // benchmark 자동 JSON archive 의 루트. 빈 문자열이면 파일 저장 skip.

	// serverID 자동 매핑 — standalone 에서는 localhost 가 id=1 이지만 안전하게 한 번 조회 캐시.
	once     sync.Once
	cached   int64
	cachedNm string
}

func (r *dbRecorder) resolveServerID(ctx context.Context) (int64, string) {
	r.once.Do(func() {
		servers, err := r.db.ListServers(ctx)
		if err != nil || len(servers) == 0 {
			return
		}
		r.cached = servers[0].ID
		r.cachedNm = servers[0].Name
	})
	return r.cached, r.cachedNm
}

func (r *dbRecorder) OnStart(ctx context.Context, jobID, jobType, tool, jobName string, deviceIDs []string, body map[string]any) {
	if jobID == "" {
		return
	}
	serverID, serverName := r.resolveServerID(ctx)
	deviceIDsJSON, _ := json.Marshal(deviceIDs)
	configJSON, _ := json.Marshal(body)
	exec := &sqlitedb.JobExecution{
		JobID:      jobID,
		ServerID:   serverID,
		Type:       jobType,
		State:      "running",
		DeviceIDs:  nullStr(string(deviceIDsJSON)),
		Config:     nullStr(string(configJSON)),
		Tool:       nullStr(tool),
		JobName:    nullStr(jobName),
		ServerName: nullStr(serverName),
	}
	if _, err := r.db.SaveJobExecution(ctx, exec); err != nil {
		slog.Warn("save job execution failed", "jobId", jobID, "error", err)
	}
}

func (r *dbRecorder) OnState(ctx context.Context, jobID, state, errMsg string) {
	if jobID == "" || state == "" {
		return
	}
	if err := r.db.UpdateJobExecutionState(ctx, jobID, state, errMsg); err != nil {
		slog.Debug("update job execution state failed", "jobId", jobID, "error", err)
	}
}

// OnResult — terminal state 도달 시 agent 메모리에서 결과 fetch → DB result_summary 저장.
// portal Spring 의 JobExecutionService.updateResultSummary 와 동일한 목적.
//
// benchmark / scenario: GetBenchmarkResult 응답을 summary 로 압축 (device 별 핵심 metrics).
// trace: GetTraceResult 의 stats 일부를 summary 로 (totalEvents, durationSeconds, 주요 latency).
func (r *dbRecorder) OnResult(ctx context.Context, agent *DeviceAgentServer, jobID, jobType string) {
	if jobID == "" || agent == nil {
		return
	}

	// benchmark/scenario 는 summary·traceJobs·autoArchive 셋 다 GetBenchmarkResult 를 쓰므로
	// 한 번만 fetch 해서 공유한다 (terminal 잡마다 동일 RPC 3회 방지).
	if jobType == "benchmark" || jobType == "scenario" {
		resp, err := agent.GetBenchmarkResult(ctx, &pb.GetBenchmarkResultRequest{JobId: jobID})
		if err != nil || len(resp.GetResults()) == 0 {
			return // 메모리에 없으면 skip
		}
		if summary := buildBenchmarkSummaryFrom(jobID, resp); summary != "" {
			if err := r.db.UpdateJobExecutionResultSummary(ctx, jobID, summary); err != nil {
				slog.Debug("update job execution result_summary failed", "jobId", jobID, "error", err)
			}
		}
		// trace job 매핑 영속화 — 만료된 job 도 job 상세에서 기존 trace UI 로 진입 가능하게.
		if tj := collectTraceJobsFrom(resp); tj != "" {
			if err := r.db.UpdateJobExecutionTraceJobs(ctx, jobID, tj); err != nil {
				slog.Debug("update job execution trace_jobs failed", "jobId", jobID, "error", err)
			}
		}
		// 스텝 구간 영속화 — 같은 이유다. 구간이 메모리에만 있으면 잡 만료 후
		// parquet 은 남는데 Behavior 탭만 사라진다.
		if sb := collectStepBoundariesFrom(resp); sb != "" {
			if err := r.db.UpdateJobExecutionStepBoundaries(ctx, jobID, sb); err != nil {
				slog.Debug("update job execution step_boundaries failed", "jobId", jobID, "error", err)
			}
		}
		// archive_base 가 설정돼 있으면 풀 결과 JSON 도 디스크에 자동 저장.
		if r.archiveBase != "" {
			r.autoArchiveBenchmarkFrom(agent, jobID, jobType, resp)
		}
		return
	}

	// trace 등 그 외 타입: 별도 RPC(GetTraceResult) 경로.
	summary, err := buildResultSummary(ctx, agent, jobID, jobType)
	if err == nil && summary != "" {
		if err := r.db.UpdateJobExecutionResultSummary(ctx, jobID, summary); err != nil {
			slog.Debug("update job execution result_summary failed", "jobId", jobID, "error", err)
		}
	}
}

// autoArchiveBenchmarkFrom — 이미 fetch 한 결과를
// 잡 산출물 폴더(jobs/<이름>/)에 {deviceId}_result.json 으로 저장 — trace 와 같은 곳.
// 수동 upload (`/api/agent/upload/benchmark`) 와 폴더 분리.
func (r *dbRecorder) autoArchiveBenchmarkFrom(agent *DeviceAgentServer, jobID, jobType string, resp *pb.GetBenchmarkResultResponse) {
	if resp == nil || len(resp.GetResults()) == 0 {
		return
	}
	// 결과 JSON 을 **trace 와 같은 잡 폴더**에 둔다.
	//
	// 예전엔 archive/auto/<날짜>/<jobId>/ 였는데, trace 는 agent_trace/<traceJobId>/ 라
	// 같은 잡이 두 트리로, 그것도 **서로 다른 ID 이름**으로 갈라졌다. 폴더째 넘기는
	// 것만으로 재현·공유가 되게 한곳에 모은다.
	dstDir := r.jobArtifactDir(agent, jobID, jobType)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		slog.Warn("auto archive mkdir failed", "jobId", jobID, "error", err)
		return
	}
	for _, br := range resp.GetResults() {
		fileName := fmt.Sprintf("%s_result.json", sanitizeFileSegment(br.GetDeviceId()))
		fullPath := filepath.Join(dstDir, fileName)
		data, err := json.MarshalIndent(benchmarkResultToMap(br), "", "  ")
		if err != nil {
			slog.Warn("auto archive marshal failed", "jobId", jobID, "deviceId", br.GetDeviceId(), "error", err)
			continue
		}
		if err := os.WriteFile(fullPath, data, 0o644); err != nil {
			slog.Warn("auto archive write failed", "jobId", jobID, "path", fullPath, "error", err)
			continue
		}
		slog.Info("benchmark result auto-archived", "jobId", jobID, "deviceId", br.GetDeviceId(), "path", fullPath)
	}
}

// jobArtifactDir — 이 잡의 산출물 폴더. DB 에서 시각·타입·이름을 읽어 이름을 만든다.
//
// 잡이 DB 에 없으면(드묾) 기존 방식으로 폴백한다 — 저장을 아예 못 하는 것보다 낫다.
func (r *dbRecorder) jobArtifactDir(agent *DeviceAgentServer, jobID, jobType string) string {
	// ⚠ **잡이 이미 정한 폴더를 그대로 쓴다.** 각자 계산하면 시각이 갈린다 —
	// 폴더 이름은 초 단위라, 시나리오가 첫 trace_start 에 닿기까지 1초만 걸려도
	// result.json 과 trace 가 다른 폴더로 떨어진다. 이 기능이 없애려던 바로 그 증상이다.
	if agent != nil && agent.orchestrator != nil {
		if job, err := agent.orchestrator.GetJob(jobID); err == nil {
			if dir := job.ArtifactDir(); dir != "" {
				return dir
			}
			// trace 를 안 쓴 잡은 폴더가 아직 없다 — 잡 시각으로 만든다(같은 규칙).
			return artifacts.JobArtifactDir(r.archiveBase, job.StartedAt(), jobType, job.Name, jobID)
		}
	}

	// 잡이 메모리에 없으면(만료 등) DB 시각으로 폴백한다.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e, err := r.db.FindJobExecutionByJobID(ctx, jobID); err == nil && e != nil {
		started := e.CreatedAt
		if e.StartedAt.Valid {
			started = e.StartedAt.Time
		}
		return artifacts.JobArtifactDir(r.archiveBase, started, e.Type, e.JobName.String, jobID)
	}
	slog.Debug("job execution 조회 실패 — 폴백 경로 사용", "jobId", jobID)
	return filepath.Join(r.archiveBase, "jobs", artifacts.JobDirName(time.Now(), "", "", jobID))
}

// collectStepBoundariesFrom — 결과에서 스텝 구간을 모아 JSON array 문자열로 반환.
// FE StepBoundary shape 과 동일 (rest_convert.go 의 benchmarkResultToMap 와 같은 키).
// 구간이 없으면 빈 문자열 — 단독 trace 나 구버전 잡이 여기 해당한다.
func collectStepBoundariesFrom(resp *pb.GetBenchmarkResultResponse) string {
	if resp == nil || len(resp.GetResults()) == 0 {
		return ""
	}
	out := make([]map[string]any, 0)
	for _, br := range resp.GetResults() {
		for _, b := range br.GetStepBoundaries() {
			out = append(out, map[string]any{
				"stepIndex":    b.GetStepIndex(),
				"loopIndex":    b.GetLoopIndex(),
				"repeatIndex":  b.GetRepeatIndex(),
				"type":         b.GetType(),
				"label":        b.GetLabel(),
				"startedAt":    b.GetStartedAt(),
				"finishedAt":   b.GetFinishedAt(),
				"startedMono":  b.GetStartedMono(),
				"finishedMono": b.GetFinishedMono(),
				"success":      b.GetSuccess(),
				"error":        b.GetError(),
			})
		}
	}
	if len(out) == 0 {
		return ""
	}
	data, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(data)
}

// collectTraceJobsFrom — 이미 fetch 한 결과에서 trace job 매핑을 모아 JSON array 문자열로 반환.
// FE TraceJobMapping shape ({traceJobId, stepIndex, loopIndex, repeatIndex, traceType}) 와 동일.
// trace 가 없으면 빈 문자열.
func collectTraceJobsFrom(resp *pb.GetBenchmarkResultResponse) string {
	if resp == nil || len(resp.GetResults()) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	jobs := make([]map[string]any, 0)
	for _, br := range resp.GetResults() {
		for _, tj := range br.GetTraceJobs() {
			id := tj.GetTraceJobId()
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			jobs = append(jobs, map[string]any{
				"traceJobId":  id,
				"stepIndex":   tj.GetStepIndex(),
				"loopIndex":   tj.GetLoopIndex(),
				"repeatIndex": tj.GetRepeatIndex(),
				"traceType":   tj.GetTraceType(),
			})
		}
	}
	if len(jobs) == 0 {
		return ""
	}
	data, err := json.Marshal(jobs)
	if err != nil {
		return ""
	}
	return string(data)
}

// nullStr — empty 면 invalid sql.NullString.
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
