// Package schedule 은 standalone 모드의 cron 잡 실행기.
//
// portal Spring ScheduledJobService 와 동일한 의도:
//   - DB 의 enabled=1 ScheduledJob 들을 cron expression 으로 등록
//   - fire 시점에 type 에 따라 RunBenchmark / RunScenario gRPC 호출
//   - 결과를 ScheduledJob.lastRunAt / lastRunStatus 에 기록 + JobExecution 에 영속화
//   - CRUD 변경 시 Reload() 호출로 cron 스케줄 재구성
//
// portal Spring @Scheduled 와 다른 점:
//   - 분산 락 없음 (단일 노드)
//   - 권한 체크 없음 (standalone localhost 전제)
package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	pb "agent/pb"
	"agent/storage/sqlitedb"
)

func nullStrSched(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func sqlNullInt64(n int64) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: n, Valid: true}
}

// JobRunner — RunBenchmark/RunScenario 를 시간 약속에 따라 호출할 수 있는 인터페이스.
// server.DeviceAgentServer 가 이 시그니처를 만족한다.
type JobRunner interface {
	RunBenchmark(ctx context.Context, req *pb.RunBenchmarkRequest) (*pb.RunBenchmarkResponse, error)
	RunScenario(ctx context.Context, req *pb.RunScenarioRequest) (*pb.RunScenarioResponse, error)
}

// Runner — cron 라이브러리 + DB + JobRunner 를 묶는다.
type Runner struct {
	db     *sqlitedb.DB
	agent  JobRunner
	parser cron.Parser

	mu       sync.Mutex
	cronImpl *cron.Cron
	entries  map[int64]cron.EntryID // scheduledJob.id → cron entry id
}

// New — Runner 생성. Start() 호출 전엔 cron 동작 안 함.
func New(db *sqlitedb.DB, agent JobRunner) *Runner {
	// portal 의 Spring CronExpression 은 6-field (초까지) 지원 + 표준 5-field 도 가능.
	// 표준 5-field 만 사용해도 충분 — 둘 다 허용하려면 Hour|Minute|Dom|Month|Dow 형식.
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	return &Runner{
		db:      db,
		agent:   agent,
		parser:  parser,
		entries: make(map[int64]cron.EntryID),
	}
}

// Start — 백그라운드 cron 시작 + 초기 reload.
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	if r.cronImpl == nil {
		r.cronImpl = cron.New(cron.WithParser(r.parser))
	}
	r.cronImpl.Start()
	r.mu.Unlock()
	r.Reload(ctx)
}

// Stop — graceful shutdown. ctx 가 timeout 되면 강제 종료.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cronImpl == nil {
		return
	}
	stopCtx := r.cronImpl.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(3 * time.Second):
	}
	r.cronImpl = nil
	r.entries = make(map[int64]cron.EntryID)
}

// Reload — DB 에서 enabled ScheduledJob 들을 다시 읽어 cron 스케줄 재구성.
// CRUD 변경 시 매번 호출. 단순화를 위해 모두 제거 후 재등록.
func (r *Runner) Reload(ctx context.Context) {
	jobs, err := r.db.ListScheduledJobs(ctx)
	if err != nil {
		slog.Warn("schedule reload list failed", "error", err)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cronImpl == nil {
		return
	}

	for _, entryID := range r.entries {
		r.cronImpl.Remove(entryID)
	}
	r.entries = make(map[int64]cron.EntryID)

	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		job := j // capture
		entry, err := r.cronImpl.AddFunc(j.CronExpression, func() {
			r.fire(context.Background(), job)
		})
		if err != nil {
			slog.Warn("schedule add failed", "id", j.ID, "expr", j.CronExpression, "error", err)
			continue
		}
		r.entries[j.ID] = entry
		// next_run_at 만 기록 (last_run_* 은 건드리지 않음)
		next := r.cronImpl.Entry(entry).Next
		if !next.IsZero() {
			_ = r.db.UpdateScheduledJobNextRun(ctx, j.ID, &next)
		}
	}
}

// Trigger — 수동 실행 (portal trigger 와 동일). 새 잡 ID 반환.
func (r *Runner) Trigger(ctx context.Context, scheduleID int64) (string, error) {
	job, err := r.db.FindScheduledJob(ctx, scheduleID)
	if err != nil {
		return "", err
	}
	return r.dispatch(ctx, job)
}

// fire — cron tick. 단일 노드라 락 없이 직접 dispatch.
func (r *Runner) fire(ctx context.Context, j *sqlitedb.ScheduledJob) {
	jobID, err := r.dispatch(ctx, j)
	status := "success"
	if err != nil {
		status = "failed"
		slog.Warn("scheduled job dispatch failed", "schedule_id", j.ID, "name", j.Name, "error", err)
	} else {
		slog.Info("scheduled job dispatched", "schedule_id", j.ID, "name", j.Name, "job_id", jobID)
	}
	// next 계산
	r.mu.Lock()
	var next *time.Time
	if r.cronImpl != nil {
		if eid, ok := r.entries[j.ID]; ok {
			n := r.cronImpl.Entry(eid).Next
			if !n.IsZero() {
				next = &n
			}
		}
	}
	r.mu.Unlock()
	_ = r.db.UpdateScheduledJobLastRun(ctx, j.ID, status, next)
}

// dispatch — type 에 따라 RunBenchmark / RunScenario gRPC 호출.
// 성공 시 JobExecution row 도 저장 (portal AgentController.runBenchmark 의 jobExecutionService.save 와 동일).
func (r *Runner) dispatch(ctx context.Context, j *sqlitedb.ScheduledJob) (string, error) {
	var deviceIDs []string
	if err := json.Unmarshal([]byte(j.DeviceIDs), &deviceIDs); err != nil {
		return "", fmt.Errorf("parse deviceIds: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(j.Config), &cfg); err != nil {
		return "", fmt.Errorf("parse config: %w", err)
	}

	switch j.Type {
	case "benchmark":
		toolStr, _ := cfg["tool"].(string)
		params, _ := cfg["params"].(map[string]any)
		paramsStr := make(map[string]string, len(params))
		for k, v := range params {
			if s, ok := v.(string); ok {
				paramsStr[k] = s
			} else {
				paramsStr[k] = fmt.Sprintf("%v", v)
			}
		}
		jobName, _ := cfg["jobName"].(string)
		if jobName == "" {
			jobName = j.Name
		}
		req := &pb.RunBenchmarkRequest{
			DeviceIds:  deviceIDs,
			Tool:       parseBenchmarkToolStr(toolStr),
			Params:     paramsStr,
			JobName:    jobName,
			BusyPolicy: j.BusyPolicy,
		}
		resp, err := r.agent.RunBenchmark(ctx, req)
		if err != nil {
			return "", err
		}
		r.saveExecution(ctx, resp.GetJobId(), "benchmark", toolStr, jobName, j, deviceIDs, cfg)
		return resp.GetJobId(), nil
	case "scenario":
		stepsRaw, _ := cfg["stepsJson"].(string)
		loopsRaw, _ := cfg["loopsJson"].(string)
		repeat := int32(1)
		if v, ok := cfg["repeatCount"].(float64); ok {
			repeat = int32(v)
		}
		// 단순화: ScenarioStep / ScenarioLoop 풀 파싱은 portal Spring 측의 일이고,
		// standalone 에서 scenario 자체는 stepsJson / loopsJson 만 RunScenarioRequest 에 직접 매핑할 수 없으니
		// 현재 phase 에서는 placeholder — Phase 7 이후 ScenarioStep proto 구조에 맞춰 변환.
		_ = stepsRaw
		_ = loopsRaw
		_ = repeat
		return "", fmt.Errorf("scenario dispatch not yet implemented (Phase 7 후속)")
	default:
		return "", fmt.Errorf("unknown schedule type: %s", j.Type)
	}
}

// saveExecution — schedule fire 결과 JobExecution row 생성.
// scheduledJobId 를 함께 기록해 nantoss 잡 추적 가능.
func (r *Runner) saveExecution(ctx context.Context, jobID, jobType, tool, jobName string, j *sqlitedb.ScheduledJob, deviceIDs []string, cfg map[string]any) {
	if jobID == "" {
		return
	}
	deviceIDsJSON, _ := json.Marshal(deviceIDs)
	configJSON, _ := json.Marshal(cfg)
	exec := &sqlitedb.JobExecution{
		JobID:          jobID,
		ServerID:       j.ServerID,
		Type:           jobType,
		State:          "running",
		DeviceIDs:      nullStrSched(string(deviceIDsJSON)),
		Config:         nullStrSched(string(configJSON)),
		Tool:           nullStrSched(tool),
		JobName:        nullStrSched(jobName),
		ScheduledJobID: sqlNullInt64(j.ID),
	}
	if _, err := r.db.SaveJobExecution(ctx, exec); err != nil {
		slog.Warn("schedule save execution failed", "jobId", jobID, "error", err)
	}
}

// parseBenchmarkToolStr — server/rest_convert.go 의 같은 함수와 동일 매핑.
// schedule 패키지가 server 를 import 하면 cycle 이므로 여기 복제.
func parseBenchmarkToolStr(s string) pb.BenchmarkTool {
	switch s {
	case "FIO", "fio", "BENCHMARK_TOOL_FIO":
		return pb.BenchmarkTool_BENCHMARK_TOOL_FIO
	case "IOZONE", "iozone", "BENCHMARK_TOOL_IOZONE":
		return pb.BenchmarkTool_BENCHMARK_TOOL_IOZONE
	case "TIOTEST", "tiotest", "BENCHMARK_TOOL_TIOTEST":
		return pb.BenchmarkTool_BENCHMARK_TOOL_TIOTEST
	case "IOTEST", "iotest", "BENCHMARK_TOOL_IOTEST":
		return pb.BenchmarkTool_BENCHMARK_TOOL_IOTEST
	default:
		return pb.BenchmarkTool_BENCHMARK_TOOL_UNSPECIFIED
	}
}
