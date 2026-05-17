# 08. Benchmark / Scenario 실행 흐름

## 지원 도구

| Tool | proto enum | 설명 | 바이너리 |
|---|---|---|---|
| FIO | `BENCHMARK_TOOL_FIO` | Flexible I/O Tester. 가장 풍부한 옵션 | `tools/fio` (4.5MB Android arm64) |
| IOZONE | `BENCHMARK_TOOL_IOZONE` | 파일시스템 throughput | `tools/iozone` (1.1MB) |
| TIOTEST | `BENCHMARK_TOOL_TIOTEST` | thread I/O test | `tools/tiotest` (28KB) |
| IOTEST | `BENCHMARK_TOOL_IOTEST` | 자체 syscall-level I/O 테스트 (Go) | `tools/iotest` (4.2MB) |

## 실행 단계

### 1. 잡 시작 (RunBenchmark)

`benchmark.Orchestrator.RunBenchmark` 진입점.

```
[run]                                 [agent 백그라운드 goroutine]
─────                                 ──────────────────────────
POST /api/agent/benchmark/run
    │
    ├─ jobId 생성 (uuid)
    ├─ JobExecution INSERT (state=running)
    ├─ 응답 {jobId}
    │
    └─ go orchestrator.execute(...)
                                       state = QUEUED
                                       progress: pushing_tools (10%)
                                       │
                                       ├─ adb push <tool binary> /data/local/tmp/
                                       │
                                       state = RUNNING
                                       progress: running (20%)
                                       │
                                       ├─ adb shell .../{tool} <params>
                                       │      (stdout 실시간 capture)
                                       │
                                       state = COLLECTING
                                       progress: parsing (80%)
                                       │
                                       ├─ rawOutput 파싱 → metrics 추출
                                       │
                                       state = COMPLETED
                                       progress: done (100%, metrics 포함)
                                       │
                                       └─ progress 채널 close
                                          (SSE 구독자에게 'complete' 시그널)
```

각 단계에서 `JobProgress` 메시지를 channel 에 push → SSE / WS subscriber 에게 전송.

### 2. 결과 조회

`GetBenchmarkResult` 는 메모리에 있는 `BenchmarkResult[]` 반환:

```go
type BenchmarkResult struct {
    DeviceId   string
    Tool       BenchmarkTool
    RawOutput  string                // fio JSON 등 풀 출력
    Metrics    map[string]float64    // 파싱된 핵심 수치
    StartedAt  int64                 // unix ms
    FinishedAt int64
    Success    bool
    Error      string
    TraceJobs  []TraceJobMapping     // scenario 에서 trace_start 가 만든 잡들
}
```

### 3. SSE Progress

`SubscribeJobProgress` → 채널 fan-out → SSE handler `event: progress` 이벤트.

## FIO 핵심 params

| key | 의미 | 예 |
|---|---|---|
| `rw` | I/O 패턴 | `read`, `write`, `randread`, `randwrite`, `randrw` |
| `bs` | block size | `4k`, `8k`, `1m` |
| `size` | 작업 크기 | `32m`, `1g` |
| `runtime` | 시간 (초) | `5`, `60` |
| `iodepth` | I/O depth | `1`, `32` |
| `numjobs` | thread 수 | `1`, `4` |
| `ioengine` | engine | `libaio`, `psync` |
| `direct` | O_DIRECT | `1`, `0` |

agent 가 자동 추가하는 옵션:
- `name=benchmark`
- `directory=/data/local/tmp/test`
- `time_based` (runtime 지정 시)
- `output-format=json` (파싱용)

## FIO 메트릭 매핑

`benchmark.parseFio` 가 fio JSON 의 `jobs[0].read|write|trim|sync` 에서 추출:

| metrics key | fio path |
|---|---|
| `read_iops`, `write_iops` | `read.iops` |
| `read_bw_kb`, `write_bw_kb` | `read.bw` |
| `read_bw_bytes`, `write_bw_bytes` | `read.bw_bytes` |
| `read_clat_ns_mean` etc | `read.clat_ns.mean` |
| `read_clat_ns_p99.000000` | `read.clat_ns.percentile["99.000000"]` |
| `read_clat_ns_p99.900000` | `read.clat_ns.percentile["99.900000"]` |
| ... 등 16개 백분위 모두 | |
| `read_io_bytes`, `write_io_bytes` | `read.io_bytes` |
| `read_total_ios`, `write_total_ios` | `read.total_ios` |
| `job_runtime_ms`, `ctx_switches` | `job_runtime`, `ctx` |
| `usr_cpu_pct`, `sys_cpu_pct` | `usr_cpu`, `sys_cpu` |

전체 backplate fio JSON 은 `rawOutput` 에 보존.

## IOTEST (자체 도구)

`cmd/iotest/main.go` 가 자체 Go 바이너리. multi-thread, syscall-level I/O.

config JSON 구조:
```json
{
  "threads": [
    {"op":"read", "blocksize":"4k", "rw":"randread", "iodepth":1},
    {"op":"write", "blocksize":"4k", "rw":"randwrite", "iodepth":1}
  ],
  "duration_seconds": 10,
  "sync_start": true
}
```

다양한 카테고리:
- Basic I/O
- Random/Stress
- Data Integrity
- File Management
- Concurrent
- Device Control

UI: `ui/src/routes/agent/iotest/IOTestForm.svelte` + `IOTestEditor.svelte` (thread 목록 편집) + `IOTestProgressView.svelte` (실행 timeline).

## Scenario 실행 (RunScenario)

multi-step 실행. portal `AgentScenarioBuilder` 의 시각적 DAG → proto `ScenarioStep[] + ScenarioLoop[]` → agent 가 순차/loop 실행.

### Step type

| type | 동작 |
|---|---|
| `benchmark` | tool/params 받아 RunBenchmark 와 동일 실행 |
| `iotest` | benchmark + tool=IOTEST + config |
| `shell` | adb shell <cmd> 실행 |
| `cleanup` | 디바이스의 /data/local/tmp/test 파일 정리 |
| `sleep` | seconds 만큼 대기 |
| `trace_start` | StartTrace 호출, 결과 traceJobId 를 step 결과로 첨부 |
| `trace_stop` | 진행 중 trace 종료 |
| `condition` | metric 값 또는 shell 결과로 분기 |
| `app_macro` | AppMacro 재생 |

### Loop

```json
"loops": [
  {"startStep": 1, "endStep": 4, "count": 3}
]
```

steps[1]~steps[4] 를 3번 반복. nested loop 가능 (loops 가 여러 개).

### Branching (DAG 모드)

`hasBranching=true` 면 `edges: [{from, to, condition?}]` 로 임의 DAG 구성. condition node 가 두 출력 (true/false branch).

### Scenario 실행 결과

`BenchmarkResult[]` (step 마다 하나) 가 반환됨. 각 result 의 `traceJobs` 에 step 안의 trace_start 가 만든 잡 ID 매핑:

```json
"traceJobs": [{
  "traceJobId": "uuid-...",
  "stepIndex": 2,
  "loopIndex": 0,
  "repeatIndex": 1,
  "traceType": "ufs"
}]
```

이걸로 UI 가 "loop 1 회차의 trace 결과 보기" 같은 drill-down 가능.

## Cancel / Delete

```
POST /api/agent/jobs/{jobId}/cancel       # 진행 중 중지
DELETE /api/agent/jobs/{jobId}             # 메모리에서 제거 (+ trace 잡까지)
```

DeleteJob 은 benchmark 결과의 rawOutput 에서 TRACE_START/TRACE_STOP 라인을 파싱해 연관된 trace 잡 ID 들도 함께 제거.

## standalone 영속화

- **OnStart**: 잡 시작 시 INSERT (config 전체 JSON, deviceIds, tool 등)
- **OnState**: terminal 시 UPDATE state, completed_at, error_message
- **OnResult**: terminal 시 `GetBenchmarkResult` → summary 추출 → UPDATE result_summary

`result_summary` 에는 raw output 제외 + 핵심 metrics 11개 추출 (`server/rest_summary.go::buildBenchmarkSummary`):
- read_iops, write_iops
- read_bw_kb, write_bw_kb
- read_clat_ns_mean, write_clat_ns_mean
- read_clat_ns_p99.000000, write_clat_ns_p99.000000
- read_clat_ns_p99.900000, write_clat_ns_p99.900000
- job_runtime_ms

전체 metrics 가 필요하면 `result_summary` 가 아니라 `rawOutput` 이 살아있는 동안(agent 메모리) `/benchmark/result` 호출.

## 자주 쓰는 시나리오 예

### 단순 randread 100k IOPS 측정

```json
{
  "deviceIds":["2-1.1.2"],
  "tool":"FIO",
  "params":{"rw":"randread","bs":"4k","size":"100m","runtime":"10","iodepth":"16"}
}
```

### Sequential write throughput

```json
{
  "tool":"FIO",
  "params":{"rw":"write","bs":"1m","size":"500m","numjobs":"1"}
}
```

### 시나리오: warmup → trace 동반 fio 3회 반복

```json
{
  "scenarioName":"warmup-then-traced-fio",
  "steps":[
    {"type":"shell","cmd":"sync"},                                        // step 0: warmup
    {"type":"trace_start","traceType":"ufs"},                              // step 1
    {"type":"benchmark","tool":"BENCHMARK_TOOL_FIO","params":{...}},      // step 2
    {"type":"trace_stop"},                                                // step 3
    {"type":"sleep","seconds":5}                                          // step 4
  ],
  "loops":[{"startStep":1,"endStep":4,"count":3}]
}
```

→ trace_start step 1 이 매 loop iteration 마다 새 traceJobId 발급, 총 3 trace 잡 생성.

## 다음

- Schedule(cron) → [10-cron-schedule.md](10-cron-schedule.md)
- UI 구조 → [09-ui.md](09-ui.md)
