# 02. 빠른 시작

## 사전 준비

1. **Go 1.25+** — `go version` 확인
2. **Node 18+** — `node -v` 확인 (UI 빌드용)
3. **adb** — `adb devices` 가 동작해야 함
4. **Android 디바이스** USB 연결, **USB 디버깅 ON**

```bash
adb devices
# List of devices attached
# R3CY10SD7RE     device
```

## 빌드

처음이면 UI 의존성 설치까지 포함해 한 번에:

```bash
cd /Users/songhyun/project/agent

# UI 빌드 (npm install + vite build)
./build-ui.sh

# Go 빌드 (//go:embed all:ui/build 로 UI 가 바이너리에 들어감)
go build -o agent .

# 또는 둘 다 한 번에
./run.sh
```

산출물 크기 참고:
- 사무실 모드만 빌드: ~63 MB
- standalone(UI 임베드) 빌드: **~78 MB**

## 첫 실행

### Standalone (브라우저 UI)

```bash
./agent --standalone -config config/devices.toml
```

로그가 다음과 같이 나오면 성공:

```
INFO standalone mode enabled — localhost bind, UI served, Go trace parser forced
INFO config loaded port=50051 standalone=true
INFO sqlite opened path=/Users/songhyun/.agent-standalone/agent.db
INFO local server seeded id=1 host=localhost port=50051
INFO scanning connected devices...
INFO device discovered device_id=2-1.1.2 serial=R3CY10SD7RE model="SM S938N" android=16
INFO listening addr=127.0.0.1:50051
INFO archive base path=/Users/songhyun/.agent-standalone/archive
INFO agent starting port=50051 services="gRPC + WebSocket(screen)"
```

브라우저로 http://127.0.0.1:50051 접속:
- 자동으로 `/agent` 로 리다이렉트
- 좌측 패널에 `localhost (this agent:50051)` 서버 자동 등록되어 있음
- 디바이스 목록에 연결된 단말 표시
- 중앙에 7개 모드 탭: **Benchmark / Scenario / Trace / IOTest / Macro / Schedule / Results**

### 사무실 모드 (헤드리스 gRPC)

```bash
./agent -config config/devices.toml
```

로그:
```
INFO config loaded port=50051 standalone=false
INFO listening addr=:50051
INFO agent starting port=50051 services="gRPC + WebSocket(screen)"
```

브라우저로 접속하면 SPA 가 안 떠 있어서 404. gRPC 클라이언트(portal 등)에서 호출해야 함.

## 첫 벤치마크 실행해 보기 (브라우저)

1. http://127.0.0.1:50051/agent 접속
2. 좌측 패널에서 디바이스 체크 (예: `R3CY10SD7RE`)
3. 중앙에서 **Benchmark** 탭 선택
4. Tool: `FIO`, params:
   ```
   rw=randread
   bs=4k
   size=32m
   runtime=5
   ```
5. **실행** 버튼 → 우상단 floating job card 에 진행률 표시
6. 완료 후 **Results** 탭으로 이동, 잡 클릭 시 metrics (IOPS, latency 백분위 등) 표시

## 첫 벤치마크 실행해 보기 (CLI)

```bash
# 잡 시작
JOB=$(curl -s -X POST 'http://127.0.0.1:50051/api/agent/benchmark/run?serverId=1' \
  -H 'Content-Type: application/json' \
  -d '{
    "deviceIds":["2-1.1.2"],
    "tool":"FIO",
    "params":{"rw":"randread","bs":"4k","size":"32m","runtime":"5"},
    "jobName":"cli-test"
  }')
JID=$(echo "$JOB" | python3 -c "import sys,json; print(json.load(sys.stdin)['jobId'])")
echo "job_id=$JID"

# SSE 진행률 (5~6초 후 completed)
curl -sN "http://127.0.0.1:50051/api/agent/benchmark/progress?serverId=1&jobId=$JID"

# 결과
curl -s "http://127.0.0.1:50051/api/agent/benchmark/result?serverId=1&jobId=$JID" | python3 -m json.tool
```

## config/devices.toml

기본 설정:

```toml
[server]
port = 50051
tools_dir = "./tools"

[standalone]
enabled = false          # CLI --standalone 으로 override 가능
# db_path = ""           # 미지정 시 $HOME/.agent-standalone/agent.db
# archive_base = ""      # 미지정 시 $HOME/.agent-standalone/archive

[minio]                  # 사무실 archive 업로드용. standalone 에선 무시
endpoint = "localhost:9000"
access_key = "admin"
secret_key = "********"
bucket = "agent"
use_ssl = false
```

## 자주 쓰는 CLI 플래그

| 플래그 | 의미 |
|---|---|
| `-config <path>` | 설정 파일 경로 (default: `config/devices.toml`) |
| `--standalone` | standalone 모드 강제 활성 (config 값 override) |
| `--db-path <path>` | SQLite 파일 경로 override |

## 종료

```bash
# foreground 라면 Ctrl+C (graceful shutdown 5초)
# background 라면
pkill -f 'agent --standalone'
```

graceful shutdown 동안:
- 실행 중이던 잡은 cancel 처리
- SQLite WAL flush
- ScheduleRunner Stop
- gRPC GracefulStop (5초 타임아웃)

## 다음

- 내부 동작 이해 → [03-architecture.md](03-architecture.md)
- 모든 endpoint 보기 → [05-rest-api.md](05-rest-api.md)
- 문제 발생 → [12-troubleshooting.md](12-troubleshooting.md)
