# 03. 아키텍처

## 전체 그림

```
┌─────────────────────────────────────────────────────────────────────┐
│                          agent 바이너리 (~78MB)                     │
│                                                                     │
│   ┌─────────────────────────────────────────────────────────────┐   │
│   │  main.go: graceful start/stop, config load, cmux setup      │   │
│   └───────────┬───────────────────────────────────────────┬─────┘   │
│               │                                           │         │
│   ┌───────────▼───────────────┐         ┌─────────────────▼────────┐│
│   │   cmux (port 50051)       │         │  ScheduleRunner          ││
│   │   ─────────────────       │         │  (standalone only)       ││
│   │  HTTP/2 + content-type:   │         │  robfig/cron v3          ││
│   │   application/grpc        │         │  → fire RunBenchmark     ││
│   │       ↓                   │         └──────────────────────────┘│
│   │   gRPC server             │                                     │
│   │       ↓                   │         ┌──────────────────────────┐│
│   │   DeviceAgentServer       │         │  SQLite (standalone)     ││
│   │   (server/grpc.go)        │         │  7 tables                ││
│   │                           │         │  (storage/sqlitedb/)     ││
│   │  HTTP/1.1 (cmux.Any)      │         └──────────────────────────┘│
│   │       ↓                   │                    ↑                │
│   │   NewHTTPRouter           │                    │                │
│   │   (server/http.go)        │                    │                │
│   │   ├─ /api/agent/* (REST)──┼───→ server/rest_*.go (handler)      │
│   │   │   └─ JobExecutionRecorder hook ─────────────┘               │
│   │   ├─ /api/agent/.../stream (SSE) ──→ server/sse.go              │
│   │   ├─ /ws/screen/{id}, /api/agent/screen/{id} → screen/handler.go│
│   │   ├─ /ws/* (보조 WS) ─────→ server/ws.go                        │
│   │   ├─ /health                                                    │
│   │   └─ / → //go:embed all:ui/build (SPA fallback)                 │
│   └───────────────────────────┘                                     │
│                                                                     │
│   ┌─────────────────────────────────────────────────────────────┐   │
│   │  Domain Managers (모든 모드 공통)                            │   │
│   │  ─────────────────────────────                              │   │
│   │  adb.Manager           — 디바이스 검색/연결/셸                │   │
│   │  benchmark.Orchestrator— fio/iozone/iotest/tiotest 실행      │   │
│   │  trace.Manager         — ftrace 수집 + parquet 파싱           │   │
│   │  monitor.Collector     — CPU/MEM/Disk 1초 streaming           │   │
│   │  macro.Manager         — 녹화/재생/OCR                        │   │
│   │  apkmgr.Manager        — tools/apks/*.apk push/install        │   │
│   │  screen.Manager        — scrcpy session                      │   │
│   │  storage.MinioClient   — (사무실 모드 archive)                │   │
│   └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
                ↓ ADB                              ↓ HTTP/WS
   ┌──────────────────────┐              ┌──────────────────────┐
   │  Android Device      │              │  Browser (standalone)│
   │  (USB)               │              │  Svelte SPA          │
   │  /data/local/tmp/    │              │  /agent route        │
   │   ├─ fio             │              └──────────────────────┘
   │   ├─ iozone          │
   │   └─ /sys/kernel/    │              ┌──────────────────────┐
   │      tracing/        │              │  Remote gRPC Client  │
   └──────────────────────┘              │  (portal Spring 등)  │
                                         └──────────────────────┘
```

## 핵심 디자인 결정

### 1. 단일 포트 + cmux 다중화

gRPC, HTTP REST, SSE, WebSocket 모두 같은 50051 포트.

```go
// main.go
m := cmux.New(lis)
grpcLis := m.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"))
httpLis := m.Match(cmux.Any())
```

- **gRPC 분기**: HTTP/2 + `content-type: application/grpc` 헤더
- **HTTP 분기**: 나머지 모두 (HTTP/1.1, WebSocket Upgrade, SSE 등)

**왜 이렇게 했나:** 출장 시나리오에서 방화벽 설정을 단순화 (포트 하나만). 사무실 원격 gRPC 클라이언트와 standalone 브라우저가 같은 포트에 공존.

**주의:** cmux 분기 순서가 중요. gRPC 가 먼저 match 되어야 HTTP/2 frame 이 HTTP 분기로 새지 않음. (이전에 cmux 분기 잘못으로 WebSocket Upgrade 가 502 나는 버그 가능성 있어 회귀 테스트 필수)

### 2. REST 어댑터는 gRPC 메서드 직접 호출

```go
// server/rest.go 예시
resp, err := agent.RunBenchmark(r.Context(), req)
```

- 네트워크/직렬화 오버헤드 zero
- gRPC 핸들러 로직 그대로 재사용
- portal Spring 의 `AgentGrpcClient` 가 하는 일을 standalone 에선 Go in-process call 로 대체
- 단, gRPC interceptor 는 우회됨 → 향후 auth interceptor 도입 시 REST 측 별도 적용 필요

### 3. portal 호환 응답 shape

portal frontend 코드를 그대로 가져왔으므로, REST 응답이 portal Spring AgentController 의 `LinkedHashMap` 직렬화 결과와 정확히 일치해야 함.

- enum → 소문자 문자열 (`completed`, `online`, `fio`)
- 필드명: camelCase (`deviceId`, `jobId`, `progressPercent`)
- portal `toJobStateString`, `toBenchmarkToolString` 등을 `server/rest_convert.go` 에 1:1 복제

### 4. SSE for progress/monitoring (portal 호환)

portal frontend 가 `EventSource` 로 progress/monitoring 을 받음 → 우리도 SSE 가 primary.
- `event: progress\ndata: {...}\n\n` 형식
- 명명 이벤트 (`progress`, `complete`, `error`, `metrics`)
- 30초 keepalive comment 라인 (`: keepalive ...`)
- 보조로 WebSocket (`/ws/jobs/{id}/progress`, `/ws/monitor`)도 유지 (gRPC 외 다른 클라이언트 호환용)

### 5. SQLite 영속화는 standalone 에서만

```go
// main.go
if cfg.Standalone.Enabled {
    sqliteDB, err = sqlitedb.Open(dbPath)
    // ...
    routerOpts.DB = sqliteDB
}
```

- 사무실 모드는 portal Spring PostgreSQL 이 영속화 담당 (agent 는 메모리만)
- standalone 은 자체 SQLite 영속화 (잡 이력, 프리셋, 매크로, 스케줄)

### 6. 부팅 시 stale 잡 정리

agent 재시작 = 메모리 잡 휘발. DB의 `running/queued/...` state 는 stale 이므로 부팅 직후 일괄 `failed` 처리.

```go
sqliteDB.MarkStaleRunningAsFailed(ctx, "agent restarted before completion")
```

이게 없으면 Results 페이지에 "영원히 running" 잡들이 쌓임.

### 7. Trace 파서 standalone 강제 Go 내장

`AGENT_PARSER=go` 환경변수가 켜지면 `tools/trace` Rust 바이너리 대신 `trace/parser/` Go 내장 파서를 사용.
standalone 부팅 시 자동 setenv:

```go
os.Setenv("AGENT_PARSER", "go")
```

이유: 출장 시 외부 바이너리 의존성 zero → 단일 바이너리 + 디바이스 push 도구만으로 운영 가능. Windows 후속 빌드 시 trace.exe 별도 빌드 불필요.

## 컴포넌트 책임 분리

### `main.go`

- CLI flag 파싱 (`--standalone`, `-config`, `--db-path`)
- config 로드
- standalone 시 SQLite open + local server seed + stale 잡 정리
- 도메인 매니저 초기화 (adb, benchmark.Orchestrator, monitor.Collector, trace.Manager, macro.Manager, screen.Manager, storage.MinioClient)
- cmux + gRPC server + HTTP server 시작
- ScheduleRunner Start
- SIGINT/SIGTERM 신호 → graceful shutdown

### `server/`

| 파일 | 책임 |
|---|---|
| `grpc.go` | gRPC `DeviceAgentServer` 구현 (50+ RPC) |
| `http.go` | cmux HTTP 분기 라우터, SPA fallback |
| `rest.go` | Device/Benchmark/Trace REST endpoints |
| `rest_server.go` | AgentServer CRUD + TCP reachable test |
| `rest_execution.go` | JobExecution history + stats |
| `rest_macro.go` | AppMacro CRUD + gRPC 위임 (recording/replay/OCR/screenshot) |
| `rest_apk.go` | APK 관리 (list + install + uninstall, gRPC 위임) |
| `rest_preset.go` | BenchmarkPreset / IOTestPreset / ScenarioTemplate CRUD |
| `rest_schedule.go` | ScheduledJob CRUD + trigger/enable |
| `rest_archive.go` | `/api/agent/upload/*` 로컬 디스크 복사 |
| `rest_convert.go` | proto → map 변환, enum 문자열화, TraceFilter 빌더 |
| `rest_hook.go` | JobExecutionRecorder 인터페이스 + dbRecorder |
| `rest_summary.go` | terminal 잡의 metrics summary 추출 |
| `rest_tcp.go` | TCP reachable 헬퍼 |
| `sse.go` | `/api/agent/benchmark/progress`, `/api/agent/monitoring/stream` |
| `ws.go` | 보조 WebSocket (`/ws/jobs/*/progress`, `/ws/monitor`) |
| `protojson.go` | protojson 옵션, parseFloat64 헬퍼 |

### `storage/sqlitedb/`

| 파일 | 책임 |
|---|---|
| `db.go` | `Open()`, 마이그레이션, `SeedLocalServer`, `DefaultPath` |
| `models.go` | 7 entity 구조체 (camelCase JSON 매칭) |
| `repo_server.go` | AgentServer CRUD |
| `repo_execution.go` | JobExecution CRUD + filter + stats + MarkStaleRunningAsFailed |
| `repo_preset.go` | BenchmarkPreset / IOTestPreset / ScenarioTemplate CRUD |
| `repo_macro_schedule.go` | AppMacro / ScheduledJob CRUD + toggle |
| `db_test.go` | 6 단위 테스트 |

### `schedule/`

| 파일 | 책임 |
|---|---|
| `runner.go` | robfig/cron v3 기반 cron 실행기, Reload/Trigger API |

### `apkmgr/`

| 파일 | 책임 |
|---|---|
| `manager.go` | `tools/apks/*.apk` 목록 + `adb install -r` push/install + `adb uninstall` |

### `trace/`

| 파일 | 책임 |
|---|---|
| `tracer.go` | ftrace 활성화, trace_pipe 캡처, parquet 파싱 |
| `stats.go` | DuckDB 로 parquet 통계 계산 |
| `sampler.go` | 대용량 raw events 샘플링 |
| `parser/` | Go 내장 파서 (`AGENT_PARSER=go` 시 사용) |

### `ui/`

| 디렉토리 | 책임 |
|---|---|
| `src/routes/agent/*` | portal agent 메인 페이지 + 32 컴포넌트 |
| `src/routes/agent/scenario-canvas/*` | @xyflow/svelte 시각적 DAG 빌더 |
| `src/routes/agent/iotest/*` | I/O Test 폼 + 진행 표시 |
| `src/lib/api/agent.ts` | typed REST 클라이언트 (portal 와 동일) |
| `src/lib/components/*` | shadcn-svelte UI primitives, DataTable, perf-chart |
| `src/lib/stores/auth.svelte.ts` | **stub** — 항상 ADMIN 인증 상태 |
| `build/` | Vite 산출물 (//go:embed 대상) |

## 데이터 흐름 예시

### Benchmark 실행 (standalone 모드, 브라우저에서)

```
[브라우저]                                          [agent Go process]
─────────                                          ──────────────────
사용자가 Benchmark 폼 작성 → 실행 클릭

  POST /api/agent/benchmark/run?serverId=1 ──→  rest.go handler
       body: {deviceIds, tool, params}              ↓
                                                 RunBenchmark gRPC 메서드 호출
                                                 (in-process)
                                                    ↓
                                                 orchestrator.RunBenchmark
                                                    ↓
                                                 jobID 생성 → goroutine 시작
                                                    ↓
                                                 [hook] currentRecorder.OnStart
                                                    └→ SQLite job_executions INSERT
  ←── {jobId}

  EventSource('/api/agent/benchmark/progress')
                                              ──→ sse.go handler
                                                 orchestrator.SubscribeJobProgress
                                                    ↓ (채널)
                                                 progress 채널에서 event 받을 때마다
                                                    ↓
                                                 SSE 'event: progress\ndata: {...}\n\n' 전송
                                                    ↓ (잡 진행 중 백그라운드)
                                              adb push fio → adb shell ./fio ...
                                                    ↓ stdout 파싱
                                                 progress 채널에 push
                                                    ↓
                                                 fio 종료 → metrics 파싱 → 결과 저장
                                                    ↓
                                                 channel close
  ←── event: complete\ndata: {}\n\n              [hook] OnState(completed) + OnResult
                                                    └→ SQLite UPDATE state, result_summary

  사용자가 Results 탭 클릭
  GET /api/agent/executions?serverId=1 ───────→  rest_execution.go
                                                 sqliteDB.ListJobExecutions
  ←── {content: [...], totalElements, ...}

  잡 클릭
  GET /api/agent/executions/by-job-id/{jid}──→   rest_execution.go
                                                 sqliteDB.FindJobExecutionByJobID
  ←── {jobId, state, resultSummary: "{...}"}    (result_summary 는 JSON 문자열)

  UI 가 resultSummary 파싱 → IOPS/BW/latency 표시
```

## 동시성 / 누수 방지

코드베이스 메모리 (`feedback-gorilla-websocket-write`, `feedback-goroutine-cleanup`, `feedback-lock-holding`) 에 명시된 패턴을 따른다:

### 동시 WebSocket Write 직렬화

`screen/handler.go:100`, `server/sse.go`, `server/ws.go` 에서 `wsWriteMu` 로 직렬화:

```go
var writeMu sync.Mutex
write := func(...) {
    writeMu.Lock()
    defer writeMu.Unlock()
    return ws.WriteMessage(...)
}
```

video frame + control 응답 + ping 등 여러 goroutine 이 같은 conn 에 쓰는 흔한 버그 방지.

### Goroutine 누수 방지 4단계

monitor SSE/WS 핸들러에서:

```go
ctx, cancel := context.WithCancel(r.Context())
ch := make(chan ..., len(deviceIDs)*4)
var wg sync.WaitGroup

for _, id := range deviceIDs {
    wg.Add(1)
    go func() {
        defer wg.Done()
        collector.StreamMetrics(ctx, id, interval, ch)
    }()
}

defer func() {
    cancel()                                    // 1. ctx cancel
    drainDone := make(chan struct{})
    go func() {                                 // 2. drain (다른 goroutine 이 ch 에 쓰다 hang 방지)
        for range ch {}
        close(drainDone)
    }()
    wg.Wait()                                   // 3. 모든 producer 종료 대기
    close(ch)                                   // 4. drain 종료
    <-drainDone
}()
```

### 락 보유 시간 최소화 (3-phase)

cron/SQLite 접근에서 락 안에 외부 I/O 금지. portal 의 `JobExecutionService.updateState` 와 동일 의도.

## 다음

- standalone 자세히 → [04-standalone-mode.md](04-standalone-mode.md)
- REST endpoints 전체 표 → [05-rest-api.md](05-rest-api.md)
- DB 스키마 → [06-sqlite-schema.md](06-sqlite-schema.md)
