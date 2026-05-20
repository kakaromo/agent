# 04. Standalone 모드 상세

## 활성화 방법

세 가지 동일한 결과:

```bash
# 1. CLI 플래그
./agent --standalone -config config/devices.toml

# 2. config 에 명시
[standalone]
enabled = true

# 3. config + CLI 동시 (CLI override)
./agent --standalone --db-path /custom/path.db
```

부팅 로그에 다음 한 줄이 보이면 standalone 모드:

```
INFO standalone mode enabled — UI served, Go trace parser forced (bind 기본 127.0.0.1, --bind 로 override)
```

## 활성 시 자동으로 일어나는 일

`main.go` 의 standalone 진입 블록 (`if cfg.Standalone.Enabled`):

### 1. `AGENT_PARSER=go` 환경변수 자동 설정

```go
os.Setenv("AGENT_PARSER", "go")
```

이게 켜지면 `trace/tracer.go:345` 에서 `tools/trace` Rust 바이너리 대신 `trace/parser/` Go 내장 파서를 사용. 출장 시 외부 바이너리 의존성을 줄이는 핵심.

### 2. SQLite 영속화 활성

```go
sqliteDB, err = sqlitedb.Open(dbPath)
```

- 기본 경로: `$HOME/.agent-standalone/agent.db`
- override: `--db-path` 또는 `[standalone] db_path = "..."`
- 7 테이블 자동 마이그레이션 (CREATE TABLE IF NOT EXISTS)
- WAL 모드, busy_timeout 5초, foreign_keys ON

### 3. 자기 자신 localhost agent 자동 등록

```go
sqliteDB.SeedLocalServer("localhost", cfg.Server.Port)
```

`agent_servers` 테이블에 row 한 개 자동 INSERT (이미 있으면 idempotent):

```
id=1
name="localhost (this agent:50051)"
host="localhost"
port=50051
enabled=true
description="Auto-registered local standalone agent"
```

portal frontend 의 `AgentContextPanel` 좌측 패널이 비어있지 않도록.

### 4. 부팅 시 stale 잡 정리

```go
n, err := sqliteDB.MarkStaleRunningAsFailed(ctx, "agent restarted before completion")
```

이전 실행에서 종료되지 않은 잡들 (`state IN ('running','queued','pushing_tools','collecting','reparsing')`) 을 모두 `failed` + `completed_at=now` 로 일괄 업데이트. agent 가 죽으면 메모리는 휘발하니까 DB 의 running 상태는 무조건 stale.

로그:
```
INFO stale running jobs cleaned count=3
```

### 5. ScheduleRunner 시작

```go
scheduleRunner = schedule.New(sqliteDB, agentServer)
scheduleRunner.Start(ctx)
```

`scheduled_jobs` 테이블의 `enabled=1` row 들을 robfig/cron v3 에 등록. CRUD 변경 시 Reload 호출되어 자동 재구성.

### 6. 127.0.0.1 바인딩 (기본) — LAN 공유는 `--bind` 로 override

```go
bindHost := cfg.Server.Bind   // toml [server] bind 또는 --bind 플래그
if bindHost == "" {
    bindHost = "127.0.0.1"    // standalone 기본
    if !cfg.Standalone.Enabled {
        bindHost = "0.0.0.0"  // 사무실 기본
    }
}
```

**기본 동작 (안전):** standalone 은 127.0.0.1 만 듣는다. 인증이 없으므로 보안 경계는 OS 의 loopback.

**LAN 공유 (출장지에서 동료 노트북도 접속):**

```bash
# 모든 인터페이스로 노출
./agent --standalone --bind 0.0.0.0 -config config/devices.toml

# 특정 IP 만 노출 (멀티 NIC 환경)
./agent --standalone --bind 192.168.1.10

# 또는 config 에 영속:
# [server]
# bind = "0.0.0.0"
```

⚠️ 외부 바인딩은 **신뢰된 사내망에서만 사용**. UI 의 auth store 가 스텁이라 같은 네트워크의 누구나 디바이스에 `adb shell`, `pm install/uninstall`, 벤치마크 실행이 가능하다. 외부 노출 시 부팅 로그에 명시적 경고가 찍힌다 (`standalone 모드에서 외부 바인딩 사용 ...`).

### 7. UI 서빙 활성

```go
routerOpts.UIFS = uiFS()
routerOpts.EnableUI = true
```

`embed.go` 의 `//go:embed all:ui/build` 가 SvelteKit SPA 산출물을 바이너리 안에 묶음. `/`, `/agent`, `/trace/...` 모두 SPA fallback.

### 8. Archive base 경로 결정

벤치마크 결과 JSON 과 trace parquet 사본이 저장되는 폴더. 결정 우선순위:

1. `--archive-base /path/to/archive` CLI 플래그 (가장 우선)
2. `[standalone] archive_base = "/path/..."` toml
3. 기본값: `$HOME/.agent-standalone/archive`

저장 레이아웃:
```
<archive_base>/
└── <remotePath>/        # /api/agent/upload/* 의 remotePath 인자 그대로
    └── <jobId>/
        ├── {deviceId}_result.json   # benchmark archive
        ├── result_ufs.parquet       # trace archive
        └── trace.log
```

MinIO 미사용 → `/api/agent/upload/*` 가 이 경로로 로컬 복사. UI 의 "결과 archive 업로드" 버튼이 이걸 호출.

사무실 모드는 archive 엔드포인트 자체가 마운트되지 않는다 (MinIO 가 별도 책임).

## 디렉토리 구조 (런타임)

```
$HOME/
└── .agent-standalone/
    ├── agent.db                    # SQLite (7 tables)
    ├── agent.db-shm                # WAL shared memory
    ├── agent.db-wal                # WAL log
    └── archive/                    # /api/agent/upload/* 산출물
        ├── {remotePath}/{jobId}/
        │   ├── 2-1.1.2_result.json # benchmark archive
        │   ├── result_ufs.parquet  # trace archive
        │   └── trace.log
        └── ...

$HOME/agent_trace/                  # 활성 trace 잡 (cfg.Server.TraceDir)
└── {jobId}/
    ├── trace.log
    └── result_ufs.parquet          # Go 파서 산출물 (standalone)
```

## standalone vs 사무실 차이 한눈에

| 항목 | 사무실 | Standalone |
|---|---|---|
| bind 기본 | 0.0.0.0 | 127.0.0.1 (override: `--bind` / `[server] bind`) |
| SQLite | × | ✓ |
| cron runner | × | ✓ |
| UI 서빙 | × | ✓ |
| AGENT_PARSER | (unset → Rust) | go (강제) |
| JobExecution 영속화 | × | ✓ (OnStart/OnState/OnResult hook) |
| stale 잡 정리 | × | ✓ (부팅 시) |
| archive 저장 | MinIO | 로컬 디스크 |
| Server 다중 등록 | × | ✓ (DB CRUD) |
| Preset/Template/Macro | × | ✓ (DB CRUD) |
| Schedule | × | ✓ |

## JobExecution Hook 라이프사이클

잡 진행에 따라 `job_executions` 테이블이 다음 시점에 업데이트됨:

```
[POST /api/agent/benchmark/run]
    │
    ├─ orchestrator.RunBenchmark → jobId 반환
    │
    └─ JobExecutionRecorder.OnStart(jobId, "benchmark", tool, jobName, deviceIds, body)
           └→ INSERT INTO job_executions
                  (state='running', config=<body JSON>, started_at=now)

[잡 진행 중]
    │
    ├─ SSE 구독자가 있으면 sse.go 가 progress 채널 listen
    │   └─ 매 progress 마다 portal-style 'event: progress' 전송
    │
    └─ 잡 종료 시 (orchestrator 가 채널 close)
           │
           ├─ SSE: 'event: complete' 전송
           ├─ OnState(jobId, "completed", "")
           │      └→ UPDATE job_executions SET state='completed', completed_at=now
           └─ OnResult(agent, jobId, "benchmark")
                  └→ GetBenchmarkResult → summary 추출
                     UPDATE job_executions SET result_summary=<JSON>

[SSE 없이 GET /api/agent/benchmark/status 폴링한 경우]
    │
    └─ rest.go status handler 가 terminal state 감지 시 동일 hook 발화
           OnState + OnResult
```

이 흐름 덕분에:
- agent 재시작 후에도 Results 페이지에서 metrics 조회 가능
- 모든 잡 시도가 SQLite 에 기록됨 (실패 잡도)
- portal frontend 가 SSE 또는 polling 어느 쪽이든 호환

## Schedule (cron) 흐름

```
[POST /api/agent/schedules]
    │
    └─ sqliteDB.CreateScheduledJob → row 생성
       runner.Reload(ctx)
           ├─ ListScheduledJobs (enabled=1만)
           ├─ 기존 cron entry 모두 Remove
           └─ 각 ScheduledJob 을 cron.AddFunc(expr, fire)
              + next_run_at 컬럼 업데이트

[cron 시간 도달]
    │
    └─ fire(ctx, scheduledJob)
           ├─ dispatch(ctx, job)
           │      └─ type=benchmark → agent.RunBenchmark(req)
           │         agent 응답 jobId 받음
           │      └─ saveExecution: job_executions INSERT
           │         (scheduled_job_id = 부모 ScheduledJob.id)
           └─ UpdateScheduledJobLastRun(last_run_at, status, next_run_at)

[POST /api/agent/schedules/{id}/trigger]
    │
    └─ 동일 dispatch (cron 안 기다리고 즉시)

[POST /api/agent/schedules/{id}/enable]
    │
    └─ ToggleScheduledJobEnabled → Reload
```

## 종료 흐름 (graceful shutdown)

```
SIGINT 또는 SIGTERM 수신
    │
    ├─ context cancel (defer cancel 발화)
    │
    ├─ scheduleRunner.Stop()
    │      └─ cron.Stop(ctx) 5초 timeout
    │
    ├─ grpcServer.GracefulStop() 5초 timeout
    │      └─ 진행 중 RPC 마무리, 새 RPC 거부
    │
    ├─ httpServer.Shutdown(ctx)
    │      └─ 진행 중 HTTP/SSE/WS 마무리
    │
    └─ sqliteDB.Close() (deferred)
           └─ WAL checkpoint
```

다음 부팅 시 stale 잡 정리가 도는 이유: graceful 끝나도 잡 자체는 cancel 되며, agent 메모리에서 사라지지만 DB state 가 'running' 으로 남을 수 있어서.

## 보안 노트

- **인증 없음**. 기본 127.0.0.1 바인딩이 유일한 경계
- 다른 사용자가 같은 머신에 SSH 접속 가능하다면 그 사용자도 agent 에 접근 가능 (localhost loopback)
- 신뢰할 수 없는 사용자가 있는 머신에서는 standalone 비추천
- CSRF 보호 없음 (XSRF 헤더는 client.ts 가 보내지만 Go 핸들러는 무시)
- `--bind 0.0.0.0` / `--bind <IP>` 로 LAN 공유 가능하지만 **신뢰된 사내망 한정**. 같은 네트워크의 누구나 디바이스에 접근 가능 — 외부 인증이 필요한 환경이면 사무실 모드 + portal/proxy 앞단에 두는 게 맞음

## 검증 체크리스트

새 environment 에서 standalone 첫 실행 시 다음 모두 동작해야 정상:

```bash
# 1. 기동 + agent 등록 + 디바이스 인식
./agent --standalone -config config/devices.toml
# 로그에 sqlite opened, local server seeded, device discovered 확인

# 2. /agent 페이지 200
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:50051/agent
# → 200

# 3. /api/agent/servers — 자동 seed 된 row 1개
curl -s http://127.0.0.1:50051/api/agent/servers | python3 -m json.tool
# → [{"id":1,"name":"localhost (...)","host":"localhost",...}]

# 4. /api/agent/devices — 실제 단말
curl -s 'http://127.0.0.1:50051/api/agent/devices?serverId=1' | python3 -m json.tool

# 5. benchmark 한 사이클
JOB=$(curl -s -X POST 'http://127.0.0.1:50051/api/agent/benchmark/run?serverId=1' \
  -H 'Content-Type: application/json' \
  -d '{"deviceIds":["YOUR_DEVICE_ID"],"tool":"FIO","params":{"rw":"randread","bs":"4k","size":"8m","runtime":"3"}}')
JID=$(echo "$JOB" | python3 -c "import sys,json; print(json.load(sys.stdin)['jobId'])")
curl -sN --max-time 10 "http://127.0.0.1:50051/api/agent/benchmark/progress?serverId=1&jobId=$JID"
# → event: progress 4번 + event: complete

# 6. 잡 완료 후 result_summary 가 DB 에 저장됐는지
sqlite3 ~/.agent-standalone/agent.db "SELECT state, length(result_summary) FROM job_executions WHERE job_id='$JID'"
# → completed | 400 정도

# 7. agent 재시작 후에도 result_summary 가 살아있는지
pkill -f 'agent --standalone'; sleep 2
./agent --standalone -config config/devices.toml &
sleep 2
curl -s "http://127.0.0.1:50051/api/agent/executions/by-job-id/$JID" | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'resultSummary len={len(d[\"resultSummary\"])}')"
# → 400+
```

## 다음

- 모든 endpoint 표 → [05-rest-api.md](05-rest-api.md)
- SQLite 스키마 상세 → [06-sqlite-schema.md](06-sqlite-schema.md)
- Schedule 자세히 → [10-cron-schedule.md](10-cron-schedule.md)
