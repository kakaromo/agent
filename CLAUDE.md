# CLAUDE.md

## Project Overview

Go 기반 Android 디바이스 에이전트. ADB를 통해 디바이스에 연결하여 벤치마크 실행, 커널 트레이스 수집, 디바이스 모니터링을 gRPC 서비스로 제공합니다.

## Build & Run

```bash
go build -o agent .        # 빌드
./run.sh                   # 빌드 + 실행 (config/devices.toml 사용)
./agent -config config/devices.toml  # 직접 실행
```

Proto 컴파일: `protoc --go_out=. --go-grpc_out=. proto/agent.proto`

## Architecture

### Module Structure

- **`main.go`** — gRPC 서버 시작, 매니저 초기화, graceful shutdown
- **`server/grpc.go`** — gRPC 서비스 구현 (DeviceAgent)
- **`adb/`** — ADB 디바이스 관리 (검색, 연결, 셸 명령)
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
- **Benchmark**: `RunBenchmark`, `RunScenario`, `GetJobStatus`, `SubscribeJobProgress`, `GetBenchmarkResult`, `DeleteJob`
- **Trace**: `StartTrace`, `StopTrace`, `GetTraceResult`, `GetTraceRawData`
- **Monitor**: `MonitorDevices` (스트리밍)

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
