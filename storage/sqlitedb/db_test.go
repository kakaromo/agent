package sqlitedb

import (
	"context"
	"path/filepath"
	"testing"
)

// 임시 디렉토리에 SQLite 파일을 만들어 풀 CRUD 검증.
// 단순한 happy path + ErrNotFound 만 본다 — 단위 테스트 무거워지지 않게.

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSeedLocalServer(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id1, err := db.SeedLocalServer("localhost", 50051)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if id1 == 0 {
		t.Fatalf("expected non-zero id, got %d", id1)
	}

	// 두 번째 호출은 같은 row 반환 (UNIQUE host+port 가정)
	id2, err := db.SeedLocalServer("localhost", 50051)
	if err != nil {
		t.Fatalf("seed 2nd: %v", err)
	}
	if id1 != id2 {
		t.Errorf("seed should be idempotent, got id1=%d id2=%d", id1, id2)
	}

	servers, err := db.ListServers(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}
	if servers[0].Host != "localhost" || servers[0].Port != 50051 {
		t.Errorf("unexpected server: %+v", servers[0])
	}
}

func TestServerCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	s, err := db.CreateServer(ctx, &AgentServer{
		Name: "remote-1", Host: "10.0.0.5", Port: 50051, Enabled: true,
		Description: "office agent",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if s.ID == 0 {
		t.Fatal("ID 0")
	}

	found, err := db.FindServer(ctx, s.ID)
	if err != nil || found.Name != "remote-1" {
		t.Fatalf("find: %v %+v", err, found)
	}

	found.Description = "updated"
	updated, err := db.UpdateServer(ctx, s.ID, found)
	if err != nil || updated.Description != "updated" {
		t.Fatalf("update: %v %+v", err, updated)
	}

	if err := db.DeleteServer(ctx, s.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.FindServer(ctx, s.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestJobExecutionLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	saved, err := db.SaveJobExecution(ctx, &JobExecution{
		JobID:    "job-1",
		ServerID: 1,
		Type:     "benchmark",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.State != "running" {
		t.Errorf("default state=running, got %s", saved.State)
	}

	// 중복 SaveJobExecution 은 IGNORE — 같은 row 반환.
	again, err := db.SaveJobExecution(ctx, &JobExecution{JobID: "job-1", ServerID: 1, Type: "benchmark"})
	if err != nil {
		t.Fatalf("save dup: %v", err)
	}
	if again.ID != saved.ID {
		t.Errorf("duplicate save should not create new row")
	}

	if err := db.UpdateJobExecutionState(ctx, "job-1", "completed", ""); err != nil {
		t.Fatalf("update state: %v", err)
	}
	completed, _ := db.FindJobExecutionByJobID(ctx, "job-1")
	if completed.State != "completed" {
		t.Errorf("state=%s, want completed", completed.State)
	}
	if !completed.CompletedAt.Valid {
		t.Errorf("completed_at should be set")
	}

	if err := db.UpdateJobExecutionResultSummary(ctx, "job-1", `{"iops":417000}`); err != nil {
		t.Fatalf("update result: %v", err)
	}

	// 워크로드 컨텍스트 메모 round-trip (신규 컬럼 workload_note).
	if err := db.UpdateJobExecutionWorkloadNote(ctx, "job-1", "warm start, 모델 이미 로드됨"); err != nil {
		t.Fatalf("update workload note: %v", err)
	}
	noted, _ := db.FindJobExecutionByJobID(ctx, "job-1")
	if !noted.WorkloadNote.Valid || noted.WorkloadNote.String != "warm start, 모델 이미 로드됨" {
		t.Errorf("workload note round-trip failed: %+v", noted.WorkloadNote)
	}
	// 빈 문자열 저장 시 NULL 로 되돌림 (자동 해석 복귀).
	if err := db.UpdateJobExecutionWorkloadNote(ctx, "job-1", ""); err != nil {
		t.Fatalf("clear workload note: %v", err)
	}
	cleared, _ := db.FindJobExecutionByJobID(ctx, "job-1")
	if cleared.WorkloadNote.Valid {
		t.Errorf("empty note should clear to NULL, got %q", cleared.WorkloadNote.String)
	}

	// trace job 매핑 영속화 round-trip (신규 컬럼 trace_jobs).
	tjJSON := `[{"traceJobId":"t-1","stepIndex":1,"loopIndex":0,"repeatIndex":1,"traceType":"ufs"}]`
	if err := db.UpdateJobExecutionTraceJobs(ctx, "job-1", tjJSON); err != nil {
		t.Fatalf("update trace jobs: %v", err)
	}
	tjRec, _ := db.FindJobExecutionByJobID(ctx, "job-1")
	if !tjRec.TraceJobs.Valid || tjRec.TraceJobs.String != tjJSON {
		t.Errorf("trace_jobs round-trip failed: %+v", tjRec.TraceJobs)
	}
	// 빈 문자열은 기존 값 보존 (갱신 안 함).
	if err := db.UpdateJobExecutionTraceJobs(ctx, "job-1", ""); err != nil {
		t.Fatalf("empty trace jobs update: %v", err)
	}
	kept, _ := db.FindJobExecutionByJobID(ctx, "job-1")
	if !kept.TraceJobs.Valid || kept.TraceJobs.String != tjJSON {
		t.Errorf("empty trace_jobs should preserve existing, got %+v", kept.TraceJobs)
	}

	stats, err := db.GetExecutionStats(ctx, nil)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 1 || stats.Completed != 1 {
		t.Errorf("stats: %+v", stats)
	}

	list, total, err := db.ListJobExecutions(ctx, JobExecutionFilter{Limit: 10})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list: total=%d len=%d err=%v", total, len(list), err)
	}
}

func TestBenchmarkPresetCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	p, err := db.CreateBenchmarkPreset(ctx, &BenchmarkPreset{
		Name: "fio-randread", Tool: "FIO", ParamsJSON: `{"rw":"randread"}`,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	list, _ := db.ListBenchmarkPresets(ctx)
	if len(list) != 1 {
		t.Errorf("len=%d", len(list))
	}

	p.Description = "updated desc"
	if _, err := db.UpdateBenchmarkPreset(ctx, p.ID, p); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := db.DeleteBenchmarkPreset(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestScenarioTemplateCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	tpl, err := db.CreateScenarioTemplate(ctx, &ScenarioTemplate{
		Name:        "warmup-then-fio",
		StepsJSON:   `[{"type":"shell","cmd":"sync"},{"type":"benchmark"}]`,
		RepeatCount: 3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tpl.RepeatCount != 3 {
		t.Errorf("repeat=%d", tpl.RepeatCount)
	}

	if err := db.DeleteScenarioTemplate(ctx, tpl.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.FindScenarioTemplate(ctx, tpl.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound")
	}
}

func TestScheduledJobCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	s, err := db.CreateScheduledJob(ctx, &ScheduledJob{
		Name:           "every-hour-noop",
		Type:           "benchmark",
		ServerID:       1,
		DeviceIDs:      `["dev1"]`,
		Config:         `{"tool":"FIO"}`,
		CronExpression: "0 * * * *",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !s.Enabled {
		t.Error("expected enabled=true")
	}

	updated, err := db.ToggleScheduledJobEnabled(ctx, s.ID, false)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if updated.Enabled {
		t.Error("expected enabled=false after toggle")
	}
}

// TestStepBoundariesPersist — 스텝 구간이 DB 에 남고 다시 읽히는가.
//
// 이게 없으면 잡이 만료된 뒤 parquet 은 남는데 Behavior 탭만 조용히 사라진다
// (trace_jobs 를 영속화한 것과 정확히 같은 이유).
func TestStepBoundariesPersist(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	exec, err := db.SaveJobExecution(ctx, &JobExecution{
		JobID: "job-sb", Type: "scenario", State: "running",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	const payload = `[{"stepIndex":0,"type":"scroll","startedMono":1005.5,"finishedMono":1007.25,"success":true}]`
	if err := db.UpdateJobExecutionStepBoundaries(ctx, "job-sb", payload); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := db.FindJobExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.StepBoundaries.Valid || got.StepBoundaries.String != payload {
		t.Errorf("StepBoundaries = %q (valid=%v), want %q",
			got.StepBoundaries.String, got.StepBoundaries.Valid, payload)
	}

	// 빈 문자열은 no-op — 구간 없는 잡(단독 trace)이 기존 값을 지우면 안 된다.
	if err := db.UpdateJobExecutionStepBoundaries(ctx, "job-sb", ""); err != nil {
		t.Fatalf("empty update: %v", err)
	}
	again, _ := db.FindJobExecution(ctx, exec.ID)
	if again.StepBoundaries.String != payload {
		t.Error("빈 문자열 업데이트가 기존 값을 지웠다")
	}
}
