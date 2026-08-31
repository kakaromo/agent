# 06. SQLite 스키마 + 영속화

standalone 모드에서만 사용. `storage/sqlitedb/` 패키지가 담당.

## 드라이버

[modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — **pure Go** SQLite 구현. CGO 불필요 → cross-compile 그대로 동작 (Windows 후속 빌드에도 같은 코드 사용 가능).

DSN:
```
{path}?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)
```

- **foreign_keys=ON**: 미래 FK 추가 시 위해 (현재 8 테이블에 FK 는 없으나 enable)
- **busy_timeout=5s**: 동시 쓰기 시 락 대기
- **journal_mode=WAL**: 읽기/쓰기 동시성 향상, crash 안전

Connection pool: MaxOpen=4, MaxIdle=2 (단일 standalone 프로세스라 작게).

## 8 테이블

### `agent_servers`

agent gRPC 서버 등록. standalone 부팅 시 localhost 가 자동 seed.

```sql
CREATE TABLE agent_servers (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL UNIQUE,
  host        TEXT NOT NULL,
  port        INTEGER NOT NULL DEFAULT 50051,
  enabled     INTEGER NOT NULL DEFAULT 1,
  description TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
```

portal `portal_agent_servers` 와 동일 구조 (table prefix 만 제거).

### `job_executions`

벤치마크/시나리오/트레이스 잡 실행 이력. **핵심 영속화 테이블**.

```sql
CREATE TABLE job_executions (
  id                     INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id                 TEXT NOT NULL UNIQUE,
  server_id              INTEGER NOT NULL,
  server_name            TEXT,
  type                   TEXT NOT NULL,             -- benchmark | scenario | trace
  tool                   TEXT,                       -- FIO | IOZONE | ufs | block | ...
  job_name               TEXT,
  device_ids             TEXT,                       -- JSON array string
  state                  TEXT NOT NULL DEFAULT 'running',
  config                 TEXT,                       -- JSON
  result_summary         TEXT,                       -- JSON (Phase 10)
  scheduled_job_id       INTEGER,                    -- nullable: 스케줄 fire 잡 추적
  retry_attempt          INTEGER NOT NULL DEFAULT 0,
  error_message          TEXT,
  started_at             TEXT,
  completed_at           TEXT,
  created_at             TEXT NOT NULL,
  -- Trace archive 메타 (옵션 — portal 호환만 유지, standalone 에선 거의 미사용)
  trace_raw_key          TEXT,
  trace_raw_format       TEXT,
  trace_raw_size         INTEGER,
  trace_raw_uploaded_at  TEXT,
  trace_parquet_keys     TEXT,                       -- JSON
  trace_parsed_at        TEXT,
  trace_parse_state      TEXT,                       -- IDLE|UPLOADING|UPLOADED|PARSING|PARSED|PARSE_FAILED
  trace_parse_error      TEXT
);

CREATE INDEX idx_job_executions_server_id  ON job_executions(server_id);
CREATE INDEX idx_job_executions_state      ON job_executions(state);
CREATE INDEX idx_job_executions_created_at ON job_executions(created_at DESC);
```

**state 가능값:**
- `queued` (잡 시작 직후)
- `pushing_tools` (fio/iozone 등을 디바이스에 push)
- `running` (실제 실행 중)
- `collecting` (trace 만: 수집 종료 후 parquet 파싱 중)
- `completed` (정상 종료)
- `failed` (실패)
- `partially_failed` (multi-device 중 일부만 실패)
- `cancelled` (사용자가 cancel)
- `reparsing` (trace ReparseTrace 진행 중)

**result_summary 예시 (benchmark):**
```json
{
  "jobId": "...",
  "devices": [{
    "deviceId": "2-1.1.2",
    "tool": "fio",
    "success": true,
    "error": "",
    "startedAt": 1779056559492,
    "finishedAt": 1779056563043,
    "metrics": {
      "read_iops": 281289.33,
      "read_bw_kb": 1125157,
      "read_clat_ns_mean": 2197.22,
      "read_clat_ns_p99.000000": 3696,
      "read_clat_ns_p99.900000": 9920,
      "write_iops": 0,
      "write_bw_kb": 0,
      "write_clat_ns_mean": 0,
      "job_runtime_ms": 3000
    }
  }]
}
```

**result_summary 예시 (trace):**
```json
{
  "jobId":"...",
  "totalEvents":8212,
  "durationSeconds":0.001,
  "readTotalBytes":16801792,
  "writeTotalBytes":311296,
  "continuousRatio":0.804,
  "alignedRatio":...,
  "sendCount":4104,
  "dtoc":{"min":...,"max":...,"avg":...,"p99":...,...},
  "ctod":{...},"ctoc":{...},
  "cmdTop":[{"cmd":"0x28","count":8198,"ratio":99.83}]
}
```

(raw output, latency histogram 풀, cmd 분포 전체는 생략 — Result 페이지 첫 화면용 정도만)

### `benchmark_presets`

FIO/IOZONE/TIOTEST/IOTEST 의 params 프리셋.

```sql
CREATE TABLE benchmark_presets (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL,
  description TEXT,
  tool        TEXT NOT NULL,
  params_json TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
```

### `iotest_presets`

```sql
CREATE TABLE iotest_presets (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL,
  description TEXT,
  category    TEXT NOT NULL,        -- Basic I/O | Random/Stress | Data Integrity | ...
  config_json TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
```

### `scenario_templates`

```sql
CREATE TABLE scenario_templates (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT NOT NULL,
  description  TEXT,
  repeat_count INTEGER NOT NULL DEFAULT 1,
  steps_json   TEXT NOT NULL,      -- JSON array of ScenarioStep
  loops_json   TEXT,                -- JSON array of ScenarioLoop (nullable)
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
```

steps_json 구조 예:
```json
[
  {"type":"shell","cmd":"sync"},
  {"type":"benchmark","tool":"BENCHMARK_TOOL_FIO","params":{"rw":"randread"}},
  {"type":"trace_start","traceType":"ufs"},
  {"type":"sleep","seconds":5},
  {"type":"trace_stop"},
  {"type":"cleanup"}
]
```

### `app_macros`

scrcpy 녹화 결과 (tap/swipe/key/wait_until/screenshot/scroll_capture/ocr).

```sql
CREATE TABLE app_macros (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT NOT NULL,
  description   TEXT,
  package_name  TEXT,
  events_json   TEXT NOT NULL,
  device_width  INTEGER,
  device_height INTEGER,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
```

events_json 구조 예:
```json
[
  {"t":0,"type":"tap","x":500,"y":1000},
  {"t":150,"type":"swipe","x":100,"y":1000,"x2":900,"y2":1000,"duration":300},
  {"t":500,"type":"wait_until","waitMethod":"ui_text","waitPattern":"Loaded","timeout":30,"pollInterval":1},
  {"t":600,"type":"screenshot","name":"loaded_state","ocrPattern":"version: ([\\d.]+)","ocrRegion":{"x":0,"y":2200,"width":1080,"height":80}}
]
```

### `scheduled_jobs`

cron 자동 실행 잡.

```sql
CREATE TABLE scheduled_jobs (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  name                TEXT NOT NULL,
  description         TEXT,
  enabled             INTEGER NOT NULL DEFAULT 1,
  type                TEXT NOT NULL,         -- benchmark | scenario
  server_id           INTEGER NOT NULL,
  device_ids          TEXT NOT NULL,         -- JSON array string
  config              TEXT NOT NULL,         -- JSON
  cron_expression     TEXT NOT NULL,
  busy_policy         TEXT DEFAULT 'reject',
  retry_count         INTEGER NOT NULL DEFAULT 0,
  retry_delay_seconds INTEGER NOT NULL DEFAULT 60,
  notify_on_failure   INTEGER NOT NULL DEFAULT 0,
  notify_on_success   INTEGER NOT NULL DEFAULT 0,
  notify_webhook_url  TEXT,
  last_run_at         TEXT,
  last_run_status     TEXT,
  next_run_at         TEXT,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL
);
```

`cron_expression`: 표준 5-field (분 시 일 월 요일) + robfig descriptor (`@every 5m`, `@daily` 등).

### `ai_log_profiles`

on-device AI(LLM) 의 TTFT/TPOT 를 logcat 문구에서 뽑기 위한 정규식 묶음. 런타임이 찍는
문자열이 AP·세트·버전마다 달라 코드에 박을 수 없어 프리셋으로 둔다.

```sql
CREATE TABLE ai_log_profiles (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT NOT NULL,
  description   TEXT,
  runtime       TEXT NOT NULL,   -- qnn | llamacpp | vendor ... (조회 필터)
  soc           TEXT,            -- 빈 값 = 런타임 공용
  patterns_json TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
```

`runtime`/`soc` 만 컬럼으로 뺀 이유: 기기가 붙었을 때 "이 AP 에 맞는 프로파일" 을 고르려면
**조회 조건**이 돼야 한다. JSON 안에 있으면 못 거른다. 나머지가 `patterns_json` 인 이유는
패턴 종류가 런타임마다 늘어나는데 컬럼이면 매번 마이그레이션이 필요해서다
(`benchmark_presets.params_json` 과 같은 판단).

⚠ 측정 **결과**는 여기 안 들어간다. 프로파일은 "어떻게 읽을지" 이고 측정값은 잡 산출물이라
`job_executions.result_summary` 로 간다. 섞으면 프로파일 하나를 여러 잡이 쓸 때 꼬인다.

`patterns_json` 예:

```json
{
  "tags": ["Genie", "QnnHtp"],
  "minPriority": "I",
  "marks":  [ { "key": "prefill_begin", "regex": "prefill begin" } ],
  "series": [ { "key": "ttft", "regex": "TTFT ([0-9.]+) ms", "unit": "ms" },
              { "key": "tpot", "regex": "decode ([0-9.]+) ms/tok", "unit": "ms" } ]
}
```

`marks` 는 걸린 줄의 **시각만** 써서 구간 경계로 쓰고, `series` 는 캡처 그룹에서 **숫자**를
뽑아 시계열이 된다. 성격이 달라 분리했다 — 뭉치면 파싱이 지저분해진다.

⚠ `ValidatePatternsJSON` 이 저장 시점에 막는 것들 (전부 "통과시키면 측정 시점에 조용히
틀리는" 종류): 잘못된 정규식 / series 에 캡처 그룹 없음 / key 중복 / 패턴 0개.

## Repository API

각 파일이 1 entity 의 CRUD 를 담당:

### `repo_server.go`

```go
ListServers(ctx) ([]*AgentServer, error)
FindServer(ctx, id) (*AgentServer, error)
CreateServer(ctx, *AgentServer) (*AgentServer, error)
UpdateServer(ctx, id, *AgentServer) (*AgentServer, error)
DeleteServer(ctx, id) error
```

### `repo_execution.go`

```go
SaveJobExecution(ctx, *JobExecution) (*JobExecution, error)        // ON CONFLICT(job_id) DO NOTHING
UpdateJobExecutionState(ctx, jobID, state, errMsg) error
UpdateJobExecutionResultSummary(ctx, jobID, jsonStr) error
FindJobExecutionByJobID(ctx, jobID) (*JobExecution, error)
FindJobExecution(ctx, id) (*JobExecution, error)
ListJobExecutions(ctx, JobExecutionFilter) ([]*JobExecution, totalCount, error)
DeleteJobExecution(ctx, id) error
GetExecutionStats(ctx, serverID *int64) (*ExecutionStats, error)
UpdateTraceArchiveMeta(ctx, jobID, TraceArchiveMeta) error
MarkStaleRunningAsFailed(ctx, reason) (int64, error)               // 부팅 시 자동 호출
```

### `repo_preset.go`

BenchmarkPreset / IOTestPreset / ScenarioTemplate / AILogProfile CRUD.

### `repo_macro_schedule.go`

AppMacro CRUD + ScheduledJob CRUD + `ToggleScheduledJobEnabled` + `UpdateScheduledJobLastRun` + `UpdateScheduledJobNextRun`.

## 마이그레이션

`db.go::migrate()` 가 `Open()` 시 자동 호출. 모든 statement 는 `CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS` 라 idempotent.

스키마 변경 추가 시:
1. 새 컬럼이면 `ALTER TABLE` 을 migrate stmts 에 append
2. 새 테이블이면 `CREATE TABLE IF NOT EXISTS` append
3. **기존 컬럼 변경/삭제**는 까다로움 (SQLite 의 제한적인 ALTER 지원). 보통 새 컬럼 추가 + 코드에서 fallback 처리

## 직접 쿼리 (디버깅)

```bash
sqlite3 ~/.agent-standalone/agent.db

.tables
# agent_servers       benchmark_presets   job_executions      scheduled_jobs
# ai_log_profiles     app_macros          iotest_presets      scenario_templates

.schema job_executions

SELECT type, state, COUNT(*) FROM job_executions GROUP BY type, state;

# 최근 실패한 잡
SELECT job_id, type, tool, error_message, completed_at
FROM job_executions
WHERE state = 'failed'
ORDER BY created_at DESC
LIMIT 10;

# 잡 result_summary 풀 보기
SELECT result_summary FROM job_executions WHERE job_id = '...';
```

## Hook 흐름 (JobExecutionRecorder)

`server/rest_hook.go` 의 `dbRecorder` 가 SQLite 쓰기를 담당. portal Spring `JobExecutionService` 와 동일 역할.

```go
type JobExecutionRecorder interface {
    OnStart(ctx, jobID, jobType, tool, jobName, deviceIDs, body)
    OnState(ctx, jobID, state, errMsg)
    OnResult(ctx, agent, jobID, jobType)
}
```

호출 시점:

| 시점 | hook | DB 동작 |
|---|---|---|
| `POST /benchmark/run` 응답 직후 | OnStart | INSERT (state=running, config, started_at) |
| `POST /trace/start` 응답 직후 | OnStart | 동일 |
| `POST /scenario/run` 응답 직후 | OnStart | 동일 |
| Schedule cron fire 직후 | (runner.go 자체 INSERT) | INSERT + scheduled_job_id 매핑 |
| SSE progress 의 terminal state | OnState + OnResult | UPDATE state, completed_at, result_summary |
| SSE channel close | OnState + OnResult | 동일 |
| `GET /benchmark/status` 가 terminal 응답 | OnState + OnResult | 동일 (polling 경로 호환) |
| `GET /benchmark/status` 가 404 | OnState | UPDATE state=failed, error_message |
| `GET /benchmark/result` 가 404 | OnState | 동일 |

**OnResult 가 실제로 데이터를 저장할 수 있는 조건**: agent 메모리에 잡 결과가 살아있어야 함. 재시작 직후 → agent 메모리 없음 → `GetBenchmarkResult` 가 404 → summary 빈 문자열 → DB skip. 즉 **잡 종료 직후의 한 번만 저장 기회**가 있음.

## 부팅 시 stale 정리

main.go:
```go
n, _ := sqliteDB.MarkStaleRunningAsFailed(ctx, "agent restarted before completion")
```

SQL:
```sql
UPDATE job_executions
SET state='failed',
    error_message=COALESCE(error_message, 'agent restarted before completion'),
    completed_at=<now>
WHERE state IN ('running','queued','pushing_tools','collecting','reparsing');
```

`COALESCE(error_message, ?)` 의 의도: 이미 error_message 가 있으면 보존, 없으면 default 메시지.

## 백업 / 출장 패키지

SQLite 파일 + archive 폴더만 백업하면 됨:

```bash
tar czf agent-data-backup.tgz \
  ~/.agent-standalone/agent.db \
  ~/.agent-standalone/agent.db-wal \
  ~/.agent-standalone/archive/
```

복원도 동일 경로에 풀어두면 끝. 다른 머신에서도 `./agent --standalone` 으로 같은 DB 사용 가능.

WAL 파일 (`-wal`) 은 활성 transaction 의 변경사항이므로 백업 시 agent 종료 후 또는 `PRAGMA wal_checkpoint(FULL)` 후 복사하는 게 안전.

## 다음

- Trace 흐름 → [07-trace.md](07-trace.md)
- Benchmark/Scenario → [08-benchmark-scenario.md](08-benchmark-scenario.md)
- cron 자세히 → [10-cron-schedule.md](10-cron-schedule.md)
