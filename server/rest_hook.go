package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"

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
func installJobExecutionHook(_ *DeviceAgentServer, db *sqlitedb.DB) {
	if db == nil {
		return
	}
	recorderRef.Store(JobExecutionRecorder(&dbRecorder{db: db}))
}

// dbRecorder — SQLite 백엔드 구현.
type dbRecorder struct {
	db *sqlitedb.DB

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
	summary, err := buildResultSummary(ctx, agent, jobID, jobType)
	if err != nil || summary == "" {
		return
	}
	if err := r.db.UpdateJobExecutionResultSummary(ctx, jobID, summary); err != nil {
		slog.Debug("update job execution result_summary failed", "jobId", jobID, "error", err)
	}
}

// nullStr — empty 면 invalid sql.NullString.
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
