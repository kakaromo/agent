# 07. Trace 수집/파싱/시각화

## 전체 흐름

```
[StartTrace]
   ├─ 디바이스 ftrace 이벤트 enable + tracing_on=1
   └─ adb shell cat trace_pipe → trace.log (append, append 만 함)

[StopTrace]
   ├─ tracing_on=0, adb 프로세스 종료 (동기)
   ├─ 상태 COLLECTING 으로 전환 후 즉시 RPC 리턴
   └─ 백그라운드: runParquetOnly
        ├─ trace <log> <OutputDir>/result --parquet-only  (사무실)
        ├─ 또는 trace/parser/ Go 내장 파서                  (standalone)
        ├─ stdout 진행률 → SubscribeJobProgress forward
        └─ 완료 시 상태 COMPLETED, trace.log 는 보존

[GetTraceResult / GetTraceRawData]
   ├─ RUNNING/COLLECTING/REPARSING 상태면 명시적 차단
   └─ DuckDB 가 result_*.parquet 을 읽어 통계/raw 반환

[ReparseTrace]
   └─ runParquetOnly 재호출 (기존 result_*.parquet 정리 후 재생성)
```

## Trace type

- **ufs**: UFS layer ftrace 이벤트 (send_req, complete_rsp). `dtoc`(driver→complete) latency
- **block**: Block layer (block_rq_issue, block_rq_complete). 일반 디바이스 I/O 트레이스
- **both**: UFS + Block 동시. parquet 두 개 생성
- **ufscustom**: UFS 의 변형 (complete_rsp 만, CPU 정보 없음)

## 파서 선택

### 사무실 모드 (default)

`tools/trace` Rust 바이너리 실행.

```
trace <log path> <output prefix> --parquet-only
```

stdout 으로 진행률 % 출력. agent 가 line-by-line 으로 SubscribeJobProgress forward.

### Standalone 모드

`AGENT_PARSER=go` 자동 설정 → `trace/parser/` Go 내장 파서 사용.

```go
// trace/tracer.go:345
if os.Getenv("AGENT_PARSER") == "go" {
    slog.Info("using Go embedded parser", "job_id", jobID, "trace_type", job.TraceType)
    return parseGo(...)
}
```

Go 파서는 같은 parquet 스키마를 생성하므로 stats.go 의 DuckDB 쿼리는 두 파서 모두 호환.

## Parquet 스키마

### UFS
```
time         DOUBLE   -- ftrace timestamp (초)
lba          UINT64   -- Logical Block Address
size         UINT32   -- 4096-byte sector count
opcode       VARCHAR  -- "0x28" (READ10), "0x2a" (WRITE10), "0x35" (SYNC_CACHE)
qd           UINT32   -- Queue Depth at time
cpu          UINT32   -- CPU id (send_req only)
dtoc         DOUBLE   -- Driver → Complete latency (ms)
ctoc         DOUBLE   -- Complete → next Complete (ms, write 시)
ctod         DOUBLE   -- Complete → next Dispatch (ms)
continuous   BOOL     -- 이전 IO 와 인접 LBA
aligned      BOOL     -- size 가 정렬됨
action       VARCHAR  -- "send_req" | "complete_rsp"
```

### Block
```
time         DOUBLE
sector       UINT64   -- 512-byte sector
size         UINT32
io_type      VARCHAR  -- "READ" | "WRITE" | "DISCARD"
qd           UINT32
cpu          UINT32
dtoc, ctoc, ctod  DOUBLE
continuous   BOOL
action       VARCHAR  -- "block_rq_issue" | "block_rq_complete"
```

### UFSCUSTOM
UFS 와 비슷하나 `cpu` 없고 `action`이 `complete_rsp` 만.

## stats 계산 (DuckDB)

`trace/stats.go::ComputeStats` 가 parquet 들을 DuckDB 로 집계.

### 인식하는 파일명 패턴 (legacy 호환)

- 현재: `result_ufs.parquet` / `result_block.parquet` / `result_ufscustom.parquet`
- merged: `ufs.parquet` / `block.parquet` / `ufscustom.parquet`
- legacy: `realtime_ufs_NNNNNN.parquet` 등

mixed schema (UFS + Block 동시) 는 DuckDB `union_by_name=true` 로 합쳐 읽음.

### 집계 결과 (TraceStats)

- **totalEvents**, **durationSeconds**
- **dtoc / ctod / ctoc / qd** 각각 LatencyStats (`min, max, avg, stddev, median, p99, p999, p9999, p99999, p999999`)
- **cmdStats**: cmd 별 count, ratio, latency stats, totalSizeBytes, continuousCount, continuousRatio, sendCount
- **latencyHistograms**: cmd × latency_type 별 bucket 분포
- **cmdSizeCounts**: (cmd, size) 별 count
- **continuousCount/Ratio**, **alignedCount/Ratio**
- **readTotalBytes / writeTotalBytes / discardTotalBytes**, **sendCount**

filter 가 있으면 DuckDB WHERE 절로 적용.

## raw events (샘플링)

`trace/sampler.go` — 50만 이벤트 초과 시 자동 샘플링.

알고리즘:
1. **extremes 추출** — top-K (가장 큰 dtoc 등) 보존
2. **uniform 샘플링** — 시간축 균등 분포
3. 두 set 을 union → sampledEvents

`isSampled=true` + `sampledEvents` 가 응답에 포함됨. portal UI 의 deck.gl scatter chart 가 이 정보를 표시.

## 운영 영향

- 수집 도중 조회 불가: COLLECTING/RUNNING/REPARSING 동안 `GetTraceResult`/`GetTraceRawData` 가 명시적 에러
- Stop 후 parquet 생성까지 지연: trace.log 크기에 비례 (5초 * 풀 부하 ≈ 10~30 MB log → ~1-3초 파싱)
- trace.log 는 **삭제하지 않고 보존** (ReparseTrace 용)
- `config.devices.toml` 의 `trace_grpc_port` 는 deprecated (이전 실시간 파서 경로 잔재)

## standalone 의 특이점

### Go 파서 강제

`AGENT_PARSER=go` 가 자동 설정되므로 `tools/trace` Rust 바이너리 불필요. Windows 후속 빌드 시 `trace.exe` 별도 빌드 안 해도 됨.

### 메모리 만료 호환

Trace 잡도 다른 잡과 마찬가지로 메모리에 들고 있다가 agent 재시작 시 휘발. 그러나 **parquet 파일 자체는 디스크에 영구 보존**.

`trace/tracer.go::GetTraceJobInfo` 의 fallback:
1. 메모리에 job 있으면 반환
2. 없으면 `outputBase/{jobID}/result_*.parquet` 확인 → `TraceType="both"` 추정 반환
3. 못 찾으면 에러

이 덕분에 재시작 후에도 `GetTraceResult` 가 일부 동작 가능. 다만 portal frontend 는 trace_type 메타 정확도가 필요할 수 있어 100% 호환은 아님.

### Archive 영구 보존

`POST /api/agent/upload/trace` 호출 시 `archive_base/{remotePath}/{jobId}/` 로 parquet + trace.log 복사. 이게 진짜 영구 보존:

```
~/.agent-standalone/archive/
└── my-experiment/
    └── 9eacdb23-3f28.../
        ├── result_ufs.parquet     (1.2 MB)
        └── trace.log              (15 MB)
```

원본 `~/agent_trace/{jobId}/` 는 같은 잡 ID 재사용 시 덮어쓸 수 있으므로, **분석 후 archive 호출하는 게 안전**.

## DuckDB CLI 로 직접 분석

agent 가 사용하는 DuckDB 와 동일한 raw parquet 을 분석 가능:

```bash
duckdb
> SELECT cmd, COUNT(*) AS count, AVG(dtoc) AS avg_dtoc, MAX(dtoc) AS max_dtoc
  FROM '~/agent_trace/{jobId}/result_ufs.parquet'
  WHERE action='complete_rsp'
  GROUP BY cmd
  ORDER BY count DESC;
```

mixed schema (UFS + Block) 같이 읽기:
```sql
SELECT * FROM read_parquet([
  '~/agent_trace/{jobId}/result_ufs.parquet',
  '~/agent_trace/{jobId}/result_block.parquet'
], union_by_name=true);
```

## UI 시각화 (portal 동일)

`ui/src/routes/agent/AgentTraceResultSheet.svelte` + `ui/src/routes/agent/TraceScatterChart.svelte`:

- **6 종 ECharts scatter**: LBA × time, QD × time, CPU × time, DtoC × time, CtoD × time, CtoC × time
- **action tabs**: send_req / complete_rsp / block_rq_issue / block_rq_complete / all
- **filter UI**: 시간 / LBA / dtoc / ctoc / ctod / QD / CPU / cmd / size / action 모두

deckgl 컴포넌트는 portal/frontend 에 있으나 standalone 의 trace 결과는 AgentTraceResultSheet 안의 ECharts 만 사용 (portal `/trace` 페이지의 deckgl 풀 화면은 의도적으로 제외 — MinIO archive 흐름이라).

## 다음

- Benchmark/Scenario 흐름 → [08-benchmark-scenario.md](08-benchmark-scenario.md)
- UI 구조 → [09-ui.md](09-ui.md)
