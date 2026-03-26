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

### 기본 흐름 (현재 구현)

```
StartTrace → adb trace_pipe → trace.log 파일 수집
StopTrace  → trace 바이너리로 일괄 파싱 (--parquet-only) → Parquet 생성
GetTraceResult → DuckDB로 Parquet 읽어서 통계 반환
```

- `StartTrace`: 디바이스의 ftrace 이벤트 활성화 → `adb shell cat trace_pipe` → 로그 파일 기록
- `StopTrace`: tracing 비활성화 → adb 프로세스 종료 → `tools/trace --parquet-only <log> <output>` 실행
- trace_type: `ufs`, `block`, `both` 지원

### 실시간 파싱 모드 (trace 서버 기능)

trace 바이너리(`tools/trace`)에는 실시간 파싱 기능이 있어, 로그가 쌓이는 동안 실시간으로 Parquet를 생성할 수 있습니다.

#### 실시간 파싱 아키텍처

```
[adb trace_pipe] → trace.log (계속 append)
                      ↓
              [trace gRPC 서버] tail -f 방식 감시
                      ↓
              윈도우(기본 1초)마다 Parquet 생성
                realtime_000001.parquet (완료)
                realtime_000002.parquet (완료)
                realtime_000003.parquet (작성 중)
```

#### 실시간 파싱 사용법

trace 바이너리의 gRPC 서버를 별도로 실행해야 합니다:

```bash
# 1. trace gRPC 서버 실행 (MinIO 환경변수 필요)
export MINIO_ENDPOINT=http://localhost:9000
export MINIO_ACCESS_KEY=admin
export MINIO_SECRET_KEY=<secret>
export MINIO_BUCKET=trace
./tools/trace --grpc-server --port 50053

# 2. 실시간 파싱 시작 (다른 터미널)
./tools/trace --client realtime \
  --server localhost:50053 \
  --source-path /tmp/agent_trace/<job_id>/trace.log \
  --output-dir /tmp/agent_trace/<job_id>/realtime \
  --log-type ufs \
  --window 1

# 3. 실시간 파싱 중지
./tools/trace --client stop --server localhost:50053 --job-id <REALTIME_JOB_ID>
```

#### 실시간 파싱 핵심 특성

- **윈도우별 파일 생성**: 지정 시간(기본 1초)마다 새 Parquet 파일 생성 (`realtime_NNNNNN.parquet`)
- **Atomic 쓰기**: `.tmp`에 먼저 쓰고 완료 후 `.parquet`로 rename → 읽기 충돌 없음
- **Bottom Half 증분 처리**: UFS/Block의 send↔complete 매칭을 윈도우 간에 상태 유지
- **DuckDB 호환**: `read_parquet('realtime_*.parquet')` 글로브 패턴으로 전체 조회 가능
- log_type: `ufs`, `block`, `ufscustom` 지원

#### 중단 시 Parquet 병합

실시간 파싱 중단(`stop`) 후 윈도우별 파일들을 하나로 병합해야 합니다. trace 바이너리에는 자동 merge 기능이 없으므로 DuckDB로 직접 병합:

```sql
COPY (
  SELECT * FROM read_parquet('/tmp/agent_trace/<job_id>/realtime/realtime_*.parquet')
  ORDER BY time
) TO '/tmp/agent_trace/<job_id>/result.parquet' (FORMAT PARQUET);
```

또는 Go 코드에서 DuckDB를 이용해 병합:
```go
db.Exec(`COPY (SELECT * FROM read_parquet(?) ORDER BY time) TO ? (FORMAT PARQUET)`,
    filepath.Join(outputDir, "realtime_*.parquet"),
    filepath.Join(outputDir, "result.parquet"))
```

#### agent에서 활용 시나리오

1. `StartTrace`로 adb trace_pipe 수집 시작
2. trace gRPC 서버의 realtime 모드로 로그 파일을 실시간 파싱
3. 실시간으로 생성되는 Parquet를 DuckDB로 읽어 라이브 통계 제공
4. `StopTrace` 시 realtime 파싱도 중지 → DuckDB로 윈도우 파일 병합 → 단일 Parquet로 결과 조회

### Parquet 스키마 (trace 바이너리 출력)

- **UFS**: time, lba, size, opcode, qd, cpu, dtoc, ctoc, ctod, continuous, aligned
- **Block**: time, sector, size, io_type, qd, cpu, dtoc, ctoc, ctod, continuous
- **UFSCUSTOM**: time, lba, size, opcode, qd, cpu, dtoc, ctoc, ctod, continuous

DuckDB에서 mixed schema 읽기: `union_by_name=true` 옵션 사용 (stats.go 참고)

## Language

코드 주석과 커밋 메시지는 한국어로 작성합니다.
