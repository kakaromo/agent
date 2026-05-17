# 10. Schedule (cron 자동 실행)

## 개요

`schedule/runner.go` — [robfig/cron v3](https://github.com/robfig/cron) 기반 cron 잡 실행기. standalone 모드에서만 동작.

기능:
- DB 의 `scheduled_jobs` 에서 `enabled=1` row 들을 cron 등록
- fire 시점에 `RunBenchmark` / `RunScenario` gRPC 호출
- 결과를 `JobExecution` row 로 영구 기록 (scheduled_job_id 로 부모 추적)
- ScheduledJob CRUD 시 자동 Reload

## 사용 예 (5분마다 fio randread)

```bash
curl -X POST http://127.0.0.1:50051/api/agent/schedules \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"5min-randread",
    "type":"benchmark",
    "serverId":1,
    "deviceIds":"[\"2-1.1.2\"]",
    "config":"{\"tool\":\"FIO\",\"params\":{\"rw\":\"randread\",\"bs\":\"4k\",\"size\":\"32m\",\"runtime\":\"10\"}}",
    "cronExpression":"*/5 * * * *",
    "enabled":true
  }'
```

이후 매 5분마다 fio 자동 실행, JobExecution 에 row 1개씩 쌓임.

## Cron 표현식

robfig/cron v3 가 받는 형식:

### 표준 5-field
```
* * * * *
│ │ │ │ │
│ │ │ │ └── 요일 (0=일요일, 1=월, ..., 6=토)
│ │ │ └──── 월 (1-12)
│ │ └────── 일 (1-31)
│ └──────── 시 (0-23)
└────────── 분 (0-59)
```

예:
- `*/1 * * * *` — 매 분
- `0 * * * *` — 매 시 정각
- `30 9 * * 1-5` — 평일 오전 9:30
- `0 0 * * 0` — 매주 일요일 자정

### Descriptor
- `@every 5m`
- `@every 1h30m`
- `@hourly`
- `@daily`
- `@weekly`
- `@monthly`
- `@reboot` (지원 안 함 — 부팅 시 별도 처리 필요)

robfig 의 `cron.Descriptor` 가 켜져 있어 두 형식 모두 사용 가능.

## Runner 라이프사이클

### 시작 (main.go)

```go
if cfg.Standalone.Enabled {
    scheduleRunner = schedule.New(sqliteDB, agentServer)
    scheduleRunner.Start(ctx)
    routerOpts.ScheduleRunner = scheduleRunner
    defer scheduleRunner.Stop()
}
```

`Start()` 가 호출하는 일:
1. `cron.New()` + `Start()` — 백그라운드 ticker 시작
2. `Reload(ctx)` — DB 의 enabled ScheduledJob 들 등록

### Reload (CRUD 변경 시)

```go
func (r *Runner) Reload(ctx context.Context) {
    jobs := r.db.ListScheduledJobs(ctx)

    // 기존 entry 모두 제거
    for _, entryID := range r.entries {
        r.cronImpl.Remove(entryID)
    }
    r.entries = make(map[int64]cron.EntryID)

    // 새로 등록
    for _, j := range jobs {
        if !j.Enabled { continue }
        entryID, err := r.cronImpl.AddFunc(j.CronExpression, func() {
            r.fire(context.Background(), job)
        })
        r.entries[j.ID] = entryID
        // next_run_at 도 DB 에 기록
    }
}
```

호출 시점:
- `POST /api/agent/schedules` (생성 후)
- `PUT /api/agent/schedules/{id}` (수정 후)
- `DELETE /api/agent/schedules/{id}` (삭제 후)
- `POST /api/agent/schedules/{id}/enable` (토글 후)

### Fire

```go
func (r *Runner) fire(ctx context.Context, j *ScheduledJob) {
    jobID, err := r.dispatch(ctx, j)
    status := "success"
    if err != nil { status = "failed" }
    r.db.UpdateScheduledJobLastRun(ctx, j.ID, status, &nextRun)
}
```

### Dispatch (type 별)

```go
switch j.Type {
case "benchmark":
    req := &pb.RunBenchmarkRequest{
        DeviceIds: deviceIDs,
        Tool:      parseBenchmarkToolStr(toolStr),
        Params:    paramsStr,
        JobName:   jobName,
        BusyPolicy: j.BusyPolicy,
    }
    resp, err := agent.RunBenchmark(ctx, req)
    saveExecution(ctx, resp.GetJobId(), "benchmark", ...)  // JobExecution INSERT
    return resp.GetJobId(), err

case "scenario":
    // (Phase 7 후속 — stepsJson → ScenarioStep proto 변환 미완)
    return "", fmt.Errorf("scenario dispatch not yet implemented")
}
```

### Manual Trigger

```bash
curl -X POST http://127.0.0.1:50051/api/agent/schedules/1/trigger
```

→ `runner.Trigger(scheduleID)` → 동일한 dispatch 호출. cron 안 기다리고 즉시.

응답: `{"success":true,"jobId":"new-uuid"}`

### Stop

```go
func (r *Runner) Stop() {
    stopCtx := r.cronImpl.Stop()
    select {
    case <-stopCtx.Done():
    case <-time.After(3 * time.Second):
    }
}
```

main.go 의 `defer scheduleRunner.Stop()` 이 graceful shutdown 시 호출. 진행 중 fire 가 있으면 3초까지 대기.

## scheduled_jobs 테이블

[06-sqlite-schema.md#scheduled_jobs](06-sqlite-schema.md#scheduled_jobs) 참고.

핵심 컬럼:
- `enabled` — 0/1 (Reload 시 false 면 등록 안 함)
- `type` — `benchmark | scenario`
- `server_id` — 어느 agent 서버에서 실행할지 (standalone 은 보통 1)
- `device_ids` — JSON array string
- `config` — JSON
- `cron_expression`
- `busy_policy`, `retry_count`, `retry_delay_seconds`
- `notify_on_*`, `notify_webhook_url` — (현재 미구현, 향후 추가 예정)
- `last_run_at`, `last_run_status`, `next_run_at` — Reload/fire 가 갱신

## JobExecution 추적

cron fire 가 만든 잡은 `scheduled_job_id` 컬럼으로 부모 ScheduledJob 과 연결됨.

```sql
-- 특정 cron 잡이 만든 모든 실행 이력
SELECT je.job_id, je.state, je.created_at
FROM job_executions je
WHERE je.scheduled_job_id = 1
ORDER BY je.created_at DESC;
```

UI 의 Results 페이지에서 schedule 컬럼 필터 추가하면 cron 잡만 모아볼 수 있음 (portal 에는 이 필터 없음 — 후속 개선 후보).

## Tip / 주의

### 1. 디바이스 OFFLINE 시 fire 동작

cron 은 시간만 보고 fire 하므로 디바이스 연결 안 되어 있어도 dispatch 시도. `RunBenchmark` 가 디바이스 못 찾아 실패 → JobExecution state=failed. 이게 의도된 동작 (실패 이력도 남기는 게 디버깅에 유용).

원하지 않으면 ScheduledJob 의 enabled 를 토글하거나 별도 pre-check 추가 필요.

### 2. 동시 fire (잡 이미 실행 중일 때)

`busyPolicy` 옵션:
- `reject` (기본): 이미 실행 중이면 RunBenchmark 가 에러 반환 → state=failed
- `wait`: 이전 잡 끝날 때까지 대기
- `force`: 강제 동시 실행

cron 빈도가 잡 실행 시간보다 짧으면 (예: `*/1 * * * *` + 잡이 1분 30초 걸림) busyPolicy 신중히 선택.

### 3. agent 재시작 후 동작

`scheduled_jobs` 자체는 DB 에 영구 보존. agent 재시작 시 `Start()` → `Reload()` 로 자동 복구. **놓친 fire 는 보충 실행 안 함** (cron 표준 동작).

예: 매시 정각 실행 cron 이 있는데 12:30~13:30 사이 agent 가 죽어있었다면, 13:00 fire 는 영원히 놓침.

### 4. next_run_at 의 정확도

- `Reload()` 시점에 다음 fire 시간을 계산해 DB 에 기록
- fire 후에도 다음 fire 시간 다시 기록
- 그 사이에 사용자가 cron_expression 수정 → Reload 가 다시 갱신

따라서 next_run_at 은 **마지막 Reload/fire 시점 기준** 추정값. 매우 정확하진 않으나 UX 표시용으로 충분.

### 5. 미구현 영역

- **Scenario dispatch**: schedule type=scenario 는 placeholder. stepsJson/loopsJson 을 ScenarioStep proto 로 매핑해야 함 (Phase 7 후속 작업)
- **webhook notification**: notifyOnFailure/Success 컬럼은 있으나 호출 코드 없음
- **분산 락**: 단일 standalone 노드라 불필요. 만약 여러 standalone 인스턴스가 같은 DB 를 공유한다면 (출장 시나리오엔 없음) 락 필요

## 디버깅

### 등록된 cron entry 확인

```bash
sqlite3 ~/.agent-standalone/agent.db
> SELECT id, name, enabled, cron_expression, next_run_at, last_run_at, last_run_status
  FROM scheduled_jobs
  WHERE enabled=1
  ORDER BY next_run_at;
```

### 로그 확인

```
INFO scheduled job dispatched schedule_id=1 name=smoke-fio job_id=ec7600db-...
INFO job finished job_id=ec7600db-... state=JOB_STATE_COMPLETED
```

dispatch 실패 시:
```
WARN scheduled job dispatch failed schedule_id=1 name=... error="device not connected"
```

### cron 표현식 검증

robfig 의 파서가 받아들이지 않으면 ScheduledJob CRUD 자체가 실패 (400). 일반적인 cron 검증 사이트(crontab.guru 등)로 미리 확인.

## 다음

- 배포 패키지 → [11-deployment.md](11-deployment.md)
- 문제 해결 → [12-troubleshooting.md](12-troubleshooting.md)
