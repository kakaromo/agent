# CLAUDE.md

상세 문서는 **[docs/](docs/README.md)** 참고. 이 파일은 빠른 참조용 요약.

## Project Overview

Go 기반 Android 디바이스 에이전트. ADB를 통해 디바이스에 연결하여 벤치마크 실행, 커널 트레이스 수집, 디바이스 모니터링을 gRPC 서비스로 제공합니다.

두 가지 모드:
- **사무실 모드** (default): 0.0.0.0 바인딩, gRPC-only 헤드리스. portal 등 원격 클라이언트가 연결
- **Standalone 모드** (`--standalone`): 127.0.0.1 바인딩(기본), portal UI 임베드, SQLite 영속화. 출장 시 노트북 단독 사용. LAN 공유는 `--bind 0.0.0.0` 또는 `[server] bind = "..."` (신뢰망 한정)

각 모드의 자세한 내용은 [docs/01-overview.md](docs/01-overview.md), [docs/04-standalone-mode.md](docs/04-standalone-mode.md).

## Build & Run

```bash
./build-ui.sh              # Svelte UI 빌드 (ui/build → //go:embed 대상)
go build -o agent .        # Go 빌드 (UI 임베드 포함)
./run.sh                   # UI + Go 빌드 + 실행 (config/devices.toml)
./agent -config config/devices.toml             # 사무실 (기존 gRPC, 0.0.0.0)
./agent --standalone -config config/devices.toml # 출장 (UI, 127.0.0.1)
```

Proto 컴파일: `protoc --go_out=. --go-grpc_out=. proto/agent.proto`

### Standalone 모드 (출장용 원바이너리)

`--standalone` 플래그 또는 `[standalone] enabled=true` 시 활성. 출장 시 노트북 단독으로
portal 의 풀 UX 를 그대로 재현한다. 사무실 모드(standalone=false)는 기존 gRPC-only 동작 유지.

**활성 효과:**
- **127.0.0.1 바인딩 (기본)** — 외부 LAN 접근 차단 (인증 없음 전제). `--bind 0.0.0.0` 또는 `--bind <IP>` 로 override 가능, 사용 시 부팅 로그에 명시적 경고
- **Svelte SPA 서빙** — `//go:embed all:ui/build` 로 바이너리에 임베드 (`/` SPA, `/_app/...` 자산, 미존재 경로는 `index.html` fallback)
- **`AGENT_PARSER=go` 자동 setenv** — `tools/trace` 외부 바이너리 미사용 → Windows 후속 빌드 시 trace.exe 불필요
- **SQLite 영속화** — `$HOME/.agent-standalone/agent.db` (config `[standalone] db_path` 로 override). 7 테이블 (agent_servers, job_executions, benchmark_presets, iotest_presets, scenario_templates, app_macros, scheduled_jobs)
- **로컬 archive 폴더** — `$HOME/.agent-standalone/archive` (MinIO 미사용). `[standalone] archive_base` 또는 `--archive-base` 로 override
- **Cron 러너** — robfig/cron v3. enabled ScheduledJob 자동 fire, 결과 JobExecution 영구 저장
- 부팅 시 stale running 잡 자동 `failed` 정리 (메모리 휘발 호환)
- 잡 종료 시 metrics summary 가 `job_executions.result_summary` 에 영구 저장됨 → agent 재시작 후에도 Result 페이지에서 IOPS/latency 등 조회 가능

**기존 gRPC 는 그대로 유지** (cmux 같은 포트). 사무실 원격 클라이언트와 동일 바이너리에서 공존.

#### UI

`ui/` 디렉토리는 `portal/frontend` 의 agent UI 를 통째로 복사 + 인증 스텁화 한 SvelteKit 앱이다.
- `routes/agent` — 메인 페이지 (3 패널 + 7 모드 탭: Benchmark / Scenario / Trace / IOTest / Macro / Schedule / Results)
- `routes/agent/scenario-canvas/*` — @xyflow/svelte 기반 시각적 DAG 빌더
- `lib/components/data-table`, `ui/*` — shadcn-svelte primitives (bits-ui 기반)
- `lib/stores/auth.svelte.ts` — **stub**. 항상 ADMIN 인증 상태로 응답 (시그니처는 portal 동일)
- `lib/api/client.ts` — 404 + `{state:"failed"}` 응답은 정상 데이터로 처리 (만료된 잡 호환)
- 사용 사이즈 프리셋: `compact` (`resize-ui.sh compact` 적용 완료, body text-xs)
- 의존성: deck.gl (GPU scatter), jmuxer (H.264 디코드), echarts, @xyflow/svelte, bits-ui, paneforge 등 portal 와 동일

portal 의 `/trace` 페이지는 **MinIO archive 파싱 흐름이라 standalone 에선 의미 없어 제거**.
Trace 결과는 `/agent` 모드의 AgentTraceResultSheet 안에서 즉시 시각화한다.

#### REST/SSE/WS endpoints (portal `/api/agent/*` 호환)

응답 shape 와 path 모두 portal AgentController/ScheduledJobController/JobExecutionController 와 1:1 일치.
모든 endpoint 는 `serverId` 쿼리 파라미터를 받지만 standalone 에선 무시 (self).

**Device & Server (8)**
- `GET    /api/agent/devices` — ListDevices
- `POST   /api/agent/devices/{serial}/connect|disconnect`
- `GET    /api/agent/servers` — DB CRUD (localhost 자기 자신 자동 seed)
- `POST   /api/agent/servers`, `PUT/DELETE /api/agent/servers/{id}`
- `POST   /api/agent/servers/test`, `/api/agent/servers/{id}/test|reconnect`, `GET /servers/{id}/status` (TCP reachable)

**Benchmark (5)**
- `POST   /api/agent/benchmark/run` — RunBenchmark + JobExecution 저장 hook
- `GET    /api/agent/benchmark/status?jobId=` — terminal 도달 시 DB state + result_summary 동기화
- `GET    /api/agent/benchmark/result?jobId=&deviceId=` — 만료 잡은 404 + `{state:"failed", results:[]}`
- `DELETE /api/agent/jobs/{jobId}`, `POST /api/agent/jobs/{jobId}/cancel`
- `SSE    /api/agent/benchmark/progress?jobId=` — 명명 이벤트: `progress`, `complete`, `error`. portal frontend addEventListener 호환

**Scenario (1)**
- `POST   /api/agent/scenario/run` (Phase 7 UI 측 호출은 frontend 가, 백엔드 dispatch 는 schedule runner 에서 placeholder — RunScenario 매핑은 grpc.go 에 이미 존재)

**Trace (5)**
- `POST   /api/agent/trace/start`
- `POST   /api/agent/trace/{jobId}/stop|reparse`
- `POST   /api/agent/trace/result` body `{jobIds, filter, latencyRangesMs}` — portal `toTraceStatsMap` 와 동일 shape
- `POST   /api/agent/trace/raw` body `{jobIds, filter}`

**Monitoring SSE**
- `GET    /api/agent/monitoring/stream?deviceIds=A&deviceIds=B&interval=1` — 명명 이벤트 `metrics`

**Macro (12)**
- DB CRUD: `GET/POST /api/agent/app-macros`, `GET/PUT/DELETE /api/agent/app-macros/{id}`, `POST /api/agent/app-macros/{id}/duplicate`
- gRPC 위임: `GET /api/agent/macro/installed-apps?deviceId=`, `POST /api/agent/macro/start-recording|stop-recording|replay|screenshot|ocr`

**APK (3)**
- `GET    /api/agent/apks` — 호스트 `tools/apks/*.apk` 목록 (filename, sizeBytes, modifiedAt)
- `POST   /api/agent/apks/install` body `{deviceId, apkFilename, grantPermissions?}` — `adb install -r [-g]`
- `POST   /api/agent/apks/uninstall` body `{deviceId, packageName, keepData?}` — `adb uninstall [-k]`
- scenario step `install_apk` / `uninstall_apk` 로도 호출 가능

**Preset / Template (13)**
- BenchmarkPreset CRUD 4, IOTestPreset CRUD 4, ScenarioTemplate CRUD 5 (duplicate 포함)

**Schedule (7)**
- `GET    /api/agent/schedules`, `GET /api/agent/schedules/{id}`
- `POST/PUT/DELETE /api/agent/schedules`, `POST /api/agent/schedules/{id}/trigger|enable`
- robfig/cron v3 — CRUD 변경 시 자동 Reload. fire 결과 JobExecution `scheduled_job_id` 로 추적

**Execution history (5)**
- `GET    /api/agent/executions?serverId=&type=&state=&page=&size=` — Spring Page<T> 호환 `{content, totalElements, totalPages, page, size}`
- `GET    /api/agent/executions/{id}|by-job-id/{jobId}`
- `DELETE /api/agent/executions/{id}`
- `GET    /api/agent/executions/stats?serverId=`

**Archive (2)**
- `POST   /api/agent/upload/trace` body `{jobIds, remotePath}` — 로컬 `archive_base/{remotePath}/{jobId}/` 로 parquet + trace.log 복사
- `POST   /api/agent/upload/benchmark` body `{jobId, remotePath}` — `{deviceId}_result.json` 로컬 저장

**Screen WebSocket**
- `WS     /api/agent/screen/{deviceId}` — portal frontend `getScreenWebSocketUrl` 호환 (legacy `/ws/screen/{id}` 로 내부 라우팅)

#### 직렬화 / enum

- JSON: portal `LinkedHashMap` 동일 shape. `server/rest_convert.go` 가 proto → map 변환
- enum 문자열 변환: portal `toJobStateString` / `toDeviceStateString` / `toBenchmarkToolString` 와 정확히 일치 (`completed`, `online`, `fio` 등 소문자)
- summary 저장: `server/rest_summary.go` — benchmark 는 IOPS/BW/latency p99 등 핵심 metrics, trace 는 totalEvents/dtoc latency 등 발췌
- REST 핸들러는 gRPC interceptor 를 우회하므로 향후 auth interceptor 도입 시 REST 측 별도 적용 필요

## Architecture

### Module Structure

- **`main.go`** — gRPC 서버 시작, 매니저 초기화, graceful shutdown, `--standalone` 플래그, SQLite 초기화, cron 러너, stale 잡 부팅 정리
- **`embed.go`** — `//go:embed all:ui/build` (standalone UI 임베드)
- **`ui/`** — SvelteKit SPA (portal/frontend agent UI 풀 복사 + 인증 스텁화)
- **`server/grpc.go`** — gRPC 서비스 구현 (DeviceAgent)
- **`server/http.go`** — cmux HTTP 분기용 라우터. standalone 시 DB-backed CRUD 모듈 마운트
- **`server/rest.go`** — Device/Benchmark/Trace REST. portal 호환 path + JobExecution hook
- **`server/rest_convert.go`** — proto → map 변환, enum 문자열화, TraceFilter 빌더
- **`server/rest_server.go`** — AgentServer CRUD + TCP reachable 테스트
- **`server/rest_execution.go`** — JobExecution history (Spring Page<T> 호환)
- **`server/rest_macro.go`** — AppMacro DB CRUD + gRPC 위임 (recording/replay/OCR/screenshot)
- **`server/rest_apk.go`** — APK 관리 REST (list + install + uninstall, gRPC 위임)
- **`server/rest_preset.go`** — BenchmarkPreset / IOTestPreset / ScenarioTemplate CRUD
- **`server/rest_schedule.go`** — ScheduledJob CRUD + trigger/enable
- **`server/rest_archive.go`** — `/api/agent/upload/*` 로컬 디스크 archive (MinIO 미사용)
- **`server/rest_hook.go`** — `JobExecutionRecorder` 인터페이스 + dbRecorder (OnStart/OnState/OnResult)
- **`server/rest_summary.go`** — terminal 잡의 metrics summary 추출 → DB 영구 저장
- **`server/sse.go`** — `/api/agent/benchmark/progress`, `/api/agent/monitoring/stream` (portal EventSource 호환)
- **`server/ws.go`** — 보조 WebSocket (`/ws/jobs/{id}/progress`, `/ws/monitor`)
- **`schedule/runner.go`** — robfig/cron v3 기반 cron 실행기
- **`storage/sqlitedb/`** — modernc.org/sqlite (pure Go) 영속화. 7 entity CRUD
- **`adb/`** — ADB 디바이스 관리 (검색, 연결, 셸 명령, install/uninstall)
- **`apkmgr/`** — `tools/apks/*.apk` 목록 + 디바이스 push/install/uninstall (경로 traversal 가드)
- **`benchmark/`** — 벤치마크 오케스트레이터 (시나리오 실행, trace 연동)
- **`monitor/`** — 디바이스 메트릭 수집 (스트리밍)
- **`trace/`** — 트레이스 관리
  - `tracer.go` — 트레이스 세션 시작/중지 (adb trace_pipe → 로그 파일 → Parquet 변환)
  - `stats.go` — DuckDB로 Parquet 통계 계산 (latency, QD, histogram, cmd별 분석)
  - `sampler.go` — 대용량 데이터 샘플링 (50만 이벤트 초과 시 extremes + uniform 샘플링)
- **`tools/`** — 외부 바이너리 (trace 파서 등)
- **`config/`** — 설정 파일 (`devices.toml`)
- **`pb/`** — 생성된 protobuf Go 코드
- **`proto/agent.proto`** — gRPC 서비스 정의

### gRPC Services (port: config에서 설정, 기본 50051)

- **Device**: `ListDevices`, `ConnectDevice`, `DisconnectDevice`
- **Benchmark**: `RunBenchmark`, `RunScenario`, `GetJobStatus`, `SubscribeJobProgress`, `GetBenchmarkResult`, `DeleteJob`, `CancelJob`
- **Trace**: `StartTrace`, `StopTrace`, `ReparseTrace`, `GetTraceResult`, `GetTraceRawData`
- **Monitor**: `MonitorDevices` (스트리밍)
- **Macro**: `ListInstalledApps`, `StartRecording`, `StopRecording`, `ReplayMacro`, `TakeScreenshot`, `ScreenshotOcr`
- **APK**: `ListBundledApks`, `InstallApk`, `UninstallApk`
- **Upload**: `UploadTraceToMinio`, `UploadBenchmarkToMinio`, `UploadTraceArchive` (streaming)

## Trace 수집 흐름

### 현재 구현 (parquet-only 단일화)

수집 중 윈도우 단위 실시간 파싱(`--client realtime` + 50053 gRPC) 은 폐기됐다.
StopTrace 후 보존된 trace.log 를 `tools/trace --parquet-only` 로 한 번에 파싱해
`result_<type>.parquet` 을 생성한다. ReparseTrace 는 같은 경로를 재호출한다.

```
[StartTrace]
   ├─ ftrace 이벤트 enable + tracing_on=1
   └─ adb shell cat trace_pipe → trace.log (append, append 만 함)

[StopTrace]
   ├─ tracing_on=0, adb 프로세스 종료 (동기)
   ├─ 상태 COLLECTING 으로 전환 후 즉시 RPC 리턴
   └─ 백그라운드: runParquetOnly
        ├─ trace <log> <OutputDir>/result --parquet-only
        ├─ stdout 진행률 → SubscribeJobProgress forward
        └─ 완료 시 상태 COMPLETED, trace.log 는 보존

[GetTraceResult] / [GetTraceRawData]
   ├─ RUNNING/COLLECTING/REPARSING 상태면 명시적 차단
   └─ DuckDB 가 result_*.parquet 을 읽어 통계/raw 반환

[ReparseTrace]
   └─ runParquetOnly 재호출 (기존 result_*.parquet 정리 후 재생성)
```

- `StartTrace`: 디바이스 ftrace 이벤트 활성화 → `adb shell cat trace_pipe` → trace.log.
  파싱 자식 프로세스는 띄우지 않는다.
- `StopTrace`: tracing 중지 + adb 종료(동기) → COLLECTING → 백그라운드에서
  parquet-only 1회 → COMPLETED. **trace.log 는 삭제하지 않고 보존** (ReparseTrace 용).
- trace_type: `ufs`, `block`, `both` 지원

### runParquetOnly (`trace/tracer.go`)

`finalizeTrace` 와 `doReparse` 가 공유하는 단일 함수. `tools/trace <log> <prefix> --parquet-only`
를 호출해 산출 prefix(`OutputDir/result`) 로 `result_<type>.parquet` 을 만든다.
호출 전 기존 `result_*.parquet` / legacy `realtime_*.parquet` 를 정리한다.

### Go 내장 파서 (`trace/parser/`)

`AGENT_PARSER=go` 환경변수가 설정되면 Rust 자식 프로세스 대신 Go 내장 파서를 사용한다.
정합성 검증/A-B 비교용 분기이며, 안정화 후 기본값을 Go 로 전환할 예정.

```bash
AGENT_PARSER=go ./agent -config config/devices.toml
```

같은 trace.log 로 두 파서를 각각 돌려 DuckDB `EXCEPT` 로 row-by-row 비교 (Rust 결과
ground truth). 차이가 0 이 되면 Rust 의존 제거를 검토한다.

### 운영 영향

- 수집 도중 조회 불가: COLLECTING 동안 `GetTraceResult` 호출 시 명시적 에러.
- stop 후 parquet 생성까지 latency: trace.log 크기에 비례한 일괄 파싱 시간 만큼 대기.
- agent 기동 시 자식 프로세스 없음: 50053 gRPC 서버, `--client realtime` 모두 사용 안 함.
- `config.devices.toml` 의 `trace_grpc_port` 는 deprecated 필드 (toml 호환 위해 남김, 무시됨).

### 통계 조회 시 parquet 매칭

`trace/stats.go` 는 다음 파일명 패턴을 인식 (legacy 잡 호환):
  - 현재:    `result_ufs.parquet` / `result_block.parquet` / `result_ufscustom.parquet`
  - merged:  `ufs.parquet` / `block.parquet` / `ufscustom.parquet`
  - legacy:  `realtime_ufs_NNNNNN.parquet` / `realtime_block_*` / `realtime_ufscustom_*`

mixed schema (UFS + Block 동시 수집) 는 DuckDB `union_by_name=true` 로 합쳐 읽는다.

### Parquet 스키마 (trace 바이너리 출력)

- **UFS**: time, lba, size, opcode, qd, cpu, dtoc, ctoc, ctod, continuous, aligned
- **Block**: time, sector, size, io_type, qd, cpu, dtoc, ctoc, ctod, continuous
- **UFSCUSTOM**: time, lba, size, opcode, qd, cpu, dtoc, ctoc, ctod, continuous

DuckDB에서 mixed schema 읽기: `union_by_name=true` 옵션 사용 (stats.go 참고)

## Language

코드 주석과 커밋 메시지는 한국어로 작성합니다.
