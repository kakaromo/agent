# 12. 문제 해결

## 부팅 / 기동

### `failed to listen error="listen tcp :50051: bind: address already in use"`

50051 포트가 이미 점유 중. 다른 agent 인스턴스 또는 다른 프로세스가 쓰는 중.

```bash
# 점유 프로세스 확인
lsof -iTCP:50051 -sTCP:LISTEN

# 안전하게 종료
pkill -f 'agent --standalone'
# 또는 PID 직접
kill <pid>

# 강제 (graceful 안 될 때)
pkill -9 -f agent
```

### `sqlite open failed`

DB 파일 권한 또는 디렉토리 없음.

```bash
# 권한 확인
ls -la ~/.agent-standalone/

# 직접 생성
mkdir -p ~/.agent-standalone/
chmod 700 ~/.agent-standalone/
```

다른 경로 사용:
```bash
./agent --standalone --db-path /custom/path/agent.db
```

### `scanning connected devices...` 후 디바이스 안 보임

```bash
# 1. adb 자체 동작 확인
adb devices
# List of devices attached
# (비어있으면 USB/USB디버깅 문제)

# 2. agent 가 adb 를 찾는지
which adb
# (agent 가 PATH 에서 adb 를 찾으므로 export PATH 확인)

# 3. 디바이스에서 USB 디버깅 허용 팝업 수락했는지

# 4. unauthorized 라면 디바이스에서 첫 인증 팝업 처리
```

### `init tracing cmd failed ... Permission denied`

ftrace 권한 부족. 일반 사용자 ADB 로는 `/sys/kernel/tracing/` 쓰기 불가.
**무시해도 됨** — agent 가 root 권한 필요한 일부 ftrace 셋업을 시도하지만 실패해도 trace_pipe capture 자체는 동작 (init 단계의 nop tracer 설정 등은 nice-to-have).

만약 trace 자체가 안 되면 디바이스를 `adb root` 가능한 user/eng build 로 띄우거나, 일반 product build 에서는 capture 가능한 이벤트만 사용.

## 브라우저 UI

### "화면 연결이 끊겼습니다 / 재연결"

scrcpy WebSocket 이 일찍 종료된 경우.

원인 후보:
1. **portal-style path** `/api/agent/screen/{deviceId}` 호출이 agent 의 `/ws/screen/{deviceId}` 로 매핑 안 됨 → 양쪽 다 등록 확인 (server/http.go)
2. **디바이스 OFFLINE 또는 unauthorized** — `curl http://127.0.0.1:50051/api/agent/devices` 로 state 확인
3. **scrcpy-server.apk 디바이스 push 실패** — `tools/scrcpy-server.apk` 존재 확인
4. **MSE SourceBuffer 에러** — 디바이스 모델에 따라 H.264 profile 미지원 (드물지만 발생). 브라우저 console 의 `SourceBuffer error` 메시지 확인

agent 로그에서 `screen session ended` 가 즉시 보이면 scrcpy 자체 실패. `scrcpy session started` 후 한참 뒤 ended 면 정상 진행 중 끊긴 것.

### "API Error [GET] /agent/benchmark/status?... 404"

만료된 잡 (agent 메모리에서 사라진 잡) 을 polling 중.

- 이건 정상 동작 — client.ts 가 404 + state body 를 정상 데이터로 처리
- 콘솔 에러 로그만 보이고 UI 는 "failed" 로 표시되어야 함
- 만약 UI 에 "실행 중 오류 발생" 토스트가 뜨면 client.ts 의 404 처리가 빠진 것 → `ui/src/lib/api/client.ts` 점검

### "실행 중 오류가 발생했습니다" 토스트

`AgentResultsView.svelte` 의 `loadExecutions` 또는 `loadStats` 가 throw.

```bash
# 직접 확인
curl -s 'http://127.0.0.1:50051/api/agent/executions?serverId=1' | python3 -m json.tool
curl -s 'http://127.0.0.1:50051/api/agent/executions/stats?serverId=1' | python3 -m json.tool
```

둘 다 200 OK 면 frontend 캐시 문제 (Cmd+Shift+R 강력 새로고침). 둘 중 하나가 5xx 면 agent 로그 확인.

### Monitor 그래프가 안 그려짐 / "재연결"

SSE 스트림이 끊겼거나 첫 메시지를 못 받음.

```bash
# 직접 검증 — 3초 동안 metrics 가 들어와야 정상
curl -sN --max-time 3 'http://127.0.0.1:50051/api/agent/monitoring/stream?serverId=1&deviceIds=2-1.1.2&interval=1'
```

`event: metrics` 줄이 안 보이면:
1. deviceIds 가 실제 deviceId(USB path 형식) 인지 확인. serial(`R3CY10SD7RE`) 이 아니라 `2-1.1.2` 같은 형식
2. 디바이스 ONLINE 상태인지

frontend 가 OFFLINE 잠시 직후 SSE 즉시 close → "재연결" 표시는 정상. 디바이스 다시 ONLINE 되면 사용자가 재연결 버튼 클릭.

### NodePalette / 컴포넌트가 큼

`./resize-ui.sh compact` 로 사이즈 프리셋을 한 단계 작게. 적용 후:
```bash
cd ui && npm run build
cd .. && go build -o agent .
pkill -f 'agent --standalone'
./agent --standalone -config config/devices.toml
```

브라우저는 Cmd+Shift+R (강력 새로고침) 필수.

## 잡 / 데이터

### Results 에 잡이 너무 많이 쌓임 (stale running)

stale 잡 정리는 부팅 시 자동이지만, agent 가 계속 떠 있는 동안에는 cron 으로 쌓일 수 있음. 일괄 정리:

```bash
sqlite3 ~/.agent-standalone/agent.db "DELETE FROM job_executions WHERE state IN ('failed','completed') AND created_at < datetime('now','-7 days')"
```

또는 전체 삭제 (테스트 데이터일 때):
```bash
sqlite3 ~/.agent-standalone/agent.db "DELETE FROM job_executions"
```

### agent 재시작 후 잡 결과 안 보임 (Phase 10 이전 잡)

`result_summary` 컬럼이 비어있는 잡은 재시작 후 metrics 조회 불가.

Phase 10 (자동 result_summary 저장) 도입 후 만든 잡들은 영구 보존. 이전 잡은:
- 다시 실행해서 새 잡으로 만들거나
- 옛 잡의 trace 가 archive 됐다면 `archive/` 폴더의 parquet 직접 확인

### SQLite DB 손상

WAL 모드라 일반적으로 안전하나 강제 종료 시 드물게 문제:

```bash
# 무결성 검사
sqlite3 ~/.agent-standalone/agent.db "PRAGMA integrity_check"

# 문제 있으면 dump → 새 DB 로 재구축
sqlite3 ~/.agent-standalone/agent.db ".dump" > /tmp/dump.sql
mv ~/.agent-standalone/agent.db ~/.agent-standalone/agent.db.broken
sqlite3 ~/.agent-standalone/agent.db < /tmp/dump.sql
```

## Trace

### `RUNNING/COLLECTING/REPARSING 상태면 명시적 차단`

trace 잡이 수집/파싱 중일 때 result 조회 요청. parquet 이 아직 없어서 차단됨.

```bash
# 잡 상태 확인
curl -s 'http://127.0.0.1:50051/api/agent/benchmark/status?serverId=1&jobId=<TRACE_JID>' | python3 -m json.tool

# COMPLETED 까지 대기 후 재시도
```

### Trace 데이터가 너무 적음 / 0 events

- ftrace 이벤트가 enable 안 됐을 가능성 → 디바이스 root 권한 필요
- 트레이스 동안 디바이스에 I/O 부하 없었음 → fio 같은 부하 잡 동시 실행
- USB 연결이 너무 느려 trace_pipe 가 timeout → `windowSeconds` 늘리기

### Go parser vs Rust parser 결과 차이

검증 시:
```bash
# Rust (사무실 모드)
AGENT_PARSER= ./agent ...

# Go (standalone)
AGENT_PARSER=go ./agent ...
```

같은 trace.log 로 두 파서 결과 (parquet) 가 다르면:
```sql
duckdb
> SELECT * FROM '<rust>.parquet' EXCEPT SELECT * FROM '<go>.parquet';
```

차이 row 있으면 Go parser 버그. 메모리 [[project-trace-go-migration]] 참고.

## Build

### `failed to embed: pattern all:ui/build: cannot embed file ... no such file or directory`

`ui/build/` 가 비어있음. UI 빌드 안 됨.

```bash
./build-ui.sh
ls ui/build/index.html       # 존재 확인
go build -o agent .
```

### `npm install` 매우 느림 / 멈춤

처음 install 은 portal 의존성이 많아 5분 정도 소요 가능 (397 packages). 또는:

```bash
cd ui
npm cache clean --force
rm -rf node_modules package-lock.json
npm install
```

### `go: warning: "./..." matched no packages`

cwd 가 잘못된 디렉토리. agent 루트로 이동:
```bash
cd /Users/songhyun/project/agent
go build ./...
```

### Windows MinGW 없음

CGO_ENABLED=0 fallback 으로 빌드:
```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o agent.exe .
```

→ DuckDB 비활성. trace 통계 조회 시 에러.
MinGW 설치 권장: `brew install mingw-w64` (mac), `apt install mingw-w64` (Debian).

## gRPC (사무실 모드)

### `grpcurl` 으로 list 가 안 됨

reflection 활성화는 확인. main.go:
```go
reflection.Register(grpcServer)
```

```bash
grpcurl -plaintext localhost:50051 list
# agent.DeviceAgent
# grpc.reflection.v1alpha.ServerReflection
# ...
```

플러그인 없으면:
```bash
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
```

## 그 외

### 로그를 더 자세히 보고 싶다

agent 는 `log/slog` 사용. 환경변수로 레벨 조정 가능 (코드에 아직 노출 안 됐을 수 있음 — main.go 확인).

```bash
# 가장 자세히 (Debug 레벨 — 코드에 따라)
GOLOG=debug ./agent --standalone ...
```

직접 코드 수정으로 강제:
```go
slog.SetLogLoggerLevel(slog.LevelDebug)
```

### Cron 잡이 안 떠 / fire 안 됨

```bash
# 1. enabled 확인
sqlite3 ~/.agent-standalone/agent.db "SELECT id, name, enabled, cron_expression, next_run_at FROM scheduled_jobs"

# 2. next_run_at 이 과거면 Reload 안 된 것 — agent 재시작 또는 enable 토글
curl -X POST http://127.0.0.1:50051/api/agent/schedules/<id>/enable

# 3. cron_expression 검증 (crontab.guru 등)

# 4. agent 로그에 "scheduled job dispatched" 라인 보이는지
grep scheduled /tmp/agent.log
```

### 메모리 / 디스크 사용량

- agent: 50~150MB (idle ~ 잡 진행 중)
- SQLite DB: 잡 수에 비례. 100 잡 ≈ 500KB
- Trace parquet: 한 잡당 수십 MB ~ 수백 MB (5초 long-running 부하 = ~10-30MB)
- trace.log (원본): parquet 의 5~10배 크기. 보존 정책 필요시 수동 삭제

장기 운영 시:
```bash
# 30일 이상 된 trace 디렉토리 삭제
find ~/agent_trace/ -maxdepth 1 -type d -mtime +30 -exec rm -rf {} \;

# DB 의 30일 이상 잡 삭제
sqlite3 ~/.agent-standalone/agent.db "DELETE FROM job_executions WHERE created_at < datetime('now','-30 days')"
sqlite3 ~/.agent-standalone/agent.db "VACUUM"
```

### 다음

이 문서에서 답을 못 찾으면:
1. agent 로그 확인 (slog 출력)
2. SQLite 직접 쿼리
3. 브라우저 DevTools Network/Console 탭
4. 관련 메모리 ([[project-standalone-mode]], [[project-trace-realtime-only]] 등)
5. portal 원본 코드 비교 (`portal/src/main/java/com/samsung/move/agent/`)
