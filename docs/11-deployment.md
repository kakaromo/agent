# 11. 배포 / 출장 패키지

## 일반 빌드

```bash
./build-ui.sh         # npm install + vite build → ui/build/
go build -o agent .   # //go:embed all:ui/build → 78MB 바이너리
```

호스트 OS / 아키텍처 자동 사용. mac arm64 에서 빌드하면 mac arm64 바이너리.

## 멀티 플랫폼 빌드 (`build.sh`)

```bash
./build.sh
```

내부적으로 다음을 모두 빌드:

```
GOOS=darwin  GOARCH=arm64 go build -o dist/agent-darwin-arm64
GOOS=darwin  GOARCH=amd64 go build -o dist/agent-darwin-amd64
GOOS=linux   GOARCH=amd64 go build -o dist/agent-linux-amd64
GOOS=linux   GOARCH=arm64 go build -o dist/agent-linux-arm64
GOOS=windows GOARCH=amd64 go build -o dist/agent-windows-amd64.exe  # CGO 분기
```

각 산출물 약 70~80MB.

UI 빌드는 build.sh 시작 부분에서 한 번만 (`./build-ui.sh`).

### CGO 분기

Windows 빌드는 DuckDB 의존 때문에 CGO 필요. mingw 가 설치되어 있으면 CGO_ENABLED=1, 없으면 CGO_ENABLED=0 으로 fallback (DuckDB 기능 제한적):

```bash
if command -v x86_64-w64-mingw32-gcc &>/dev/null; then
    CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 go build ...
else
    echo "MinGW not found, building without CGO (DuckDB disabled)..."
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ...
fi
```

**참고**: standalone 모드는 DuckDB 가 통계 계산에 필수 (trace `GetTraceResult`). Windows 에서 CGO 없이 빌드하면 trace 통계 조회 시 에러 가능. mingw 설치 권장.

## 출장 패키지 구성

USB / 외장 SSD 에 다음 디렉토리 통째로 복사:

```
agent-portable/
├── agent (또는 agent.exe)        # standalone 바이너리 (78MB)
├── config/
│   └── devices.toml              # [standalone] enabled=true 권장
├── tools/                         # Android push 도구
│   ├── fio                        # Android arm64 ELF — `[tools] fio = "..."` 로 파일명 override 가능
│   ├── iozone
│   ├── tiotest
│   ├── iotest                     # Go 자체 빌드
│   ├── apks/                      # 디바이스에 설치할 .apk (optional, README 참고)
│   └── scrcpy-server              # JAR
└── platform-tools/                # (옵션) ADB 동봉
    ├── adb
    ├── adb.exe                    # Windows
    └── ...
```

**tools/trace 는 standalone 에선 미사용** (Go 내장 파서가 강제) — 출장 패키지에 안 넣어도 됨. 사무실 모드도 같이 쓰려면 포함.

권장 `config/devices.toml`:

```toml
[server]
port = 50051
tools_dir = "./tools"

[standalone]
enabled = true                          # 출장용 기본 ON
# db_path = "./standalone/agent.db"     # 미지정 시 $HOME/.agent-standalone/
# archive_base = "./standalone/archive"

[minio]
# 모두 빈 값 또는 주석 — standalone 에선 미사용

# 도구 파일명이 다른 경우 (예: tools/fio-3.36) override
# [tools]
# fio = "fio-3.36"
# iozone = "iozone-3.506"
# tiotest = "tiotest-0.4.3"
```

`db_path` / `archive_base` 를 상대 경로로 두면 USB 안에서 모든 게 self-contained. 다른 노트북에 꽂아도 같은 DB 사용.

## 실행 (출장지에서)

```bash
# 1. ADB 가 PATH 에 있어야 함 (또는 platform-tools/ 를 PATH 에 추가)
export PATH="$(pwd)/platform-tools:$PATH"   # 옵션

# 2. 디바이스 USB 연결, USB 디버깅 ON 후 한 번 인증
adb devices

# 3. agent 시작
./agent

# 4. 브라우저로 접속
open http://127.0.0.1:50051       # mac
xdg-open http://127.0.0.1:50051   # Linux
start http://127.0.0.1:50051      # Windows
```

## 상시 실행 (옵션)

### macOS launchd

`~/Library/LaunchAgents/com.example.agent.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.example.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/path/to/agent-portable/agent</string>
    <string>--standalone</string>
    <string>-config</string>
    <string>/path/to/agent-portable/config/devices.toml</string>
  </array>
  <key>WorkingDirectory</key><string>/path/to/agent-portable</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/agent.log</string>
  <key>StandardErrorPath</key><string>/tmp/agent.err</string>
</dict>
</plist>
```

`launchctl load ~/Library/LaunchAgents/com.example.agent.plist`

### Linux systemd

`/etc/systemd/user/agent.service`:

```ini
[Unit]
Description=Agent Standalone
After=network.target

[Service]
ExecStart=/path/to/agent-portable/agent --standalone -config /path/to/agent-portable/config/devices.toml
WorkingDirectory=/path/to/agent-portable
Restart=on-failure

[Install]
WantedBy=default.target
```

`systemctl --user enable --now agent`

## 백업 / 데이터 이동

가장 중요한 데이터:

```
~/.agent-standalone/         # standalone 데이터 디렉토리
├── agent.db                 # SQLite (모든 잡 이력, preset, schedule)
├── agent.db-wal             # WAL log
└── archive/                 # 명시적으로 archive 한 잡들

~/agent_trace/               # 활성 trace 잡 (parquet + trace.log)
```

backup:

```bash
# agent 종료 후 (WAL flush 위해)
pkill -f 'agent --standalone'

tar czf agent-data-$(date +%Y%m%d).tgz \
  ~/.agent-standalone/ \
  ~/agent_trace/
```

다른 머신으로 이동 시 동일 경로에 풀어두면 그대로 사용 가능.

## Windows 후속 지원

현재 상태:
- ✅ Go 코드는 OS 비의존 (modernc/sqlite pure Go, embed.go OS 무관)
- ✅ Go 내장 trace parser 라 standalone 은 외부 trace.exe 불필요
- ⚠️ **DuckDB CGO 필요** — Windows 빌드 시 mingw 또는 CGO_ENABLED=0 으로 fallback
- ⚠️ **adb.exe** PATH 에 있어야 함 (또는 platform-tools 동봉)
- ⚠️ **Windows Defender 첫 실행 경고** — 한 번 허용하면 끝
- ⚠️ **scrcpy-server.apk** — Android 디바이스 push 라 OS 무관

검증 미완. 출장 시 첫 Windows 노트북에서 다음 점검 필요:
1. `adb devices` 동작
2. `./agent.exe --standalone` 기동 로그 정상
3. 브라우저 http://127.0.0.1:50051 SPA 로딩
4. benchmark 1회 실행 → 결과 metrics 표시
5. trace 1회 → parquet 생성 + 통계 차트
6. scrcpy 스트리밍 ← (이 부분이 Windows 에서 가장 가능성 낮은 영역)

## 단일 바이너리 검증

빌드 후:

```bash
ls -lh agent
# -rwxr-xr-x ... 78M ... agent

# 단일 바이너리만으로 동작하는지 (tools/ 외 모든 외부 의존성 zero 확인)
mkdir /tmp/test && cp agent /tmp/test/ && cp -R tools /tmp/test/ && cp -R config /tmp/test/
cd /tmp/test
./agent --standalone -config config/devices.toml
# → 로그에 sqlite opened, listening, agent starting 정상

# 다른 디렉토리 (tools 없이) 에서 standalone 모드 → benchmark 만 안 됨
# (trace 는 Go parser 라 동작, scrcpy 는 tools/scrcpy-server.apk 필요)
```

## 보안

출장 시 외부 노출 위험:
- standalone 은 127.0.0.1 만 listen → 외부 LAN 접근 차단
- 같은 머신의 다른 user 가 SSH 접속 가능하면 그 user 도 접근 가능 (loopback 은 user 격리 안 됨)
- **신뢰할 수 없는 사용자가 있는 머신에서는 standalone 비추천**
- 카페/공항 등 공용 WiFi 환경에서도 127.0.0.1 이라 안전

외부 노출이 필요하면 standalone 비활성 + 별도 인증 reverse proxy (nginx + Basic Auth 등) 필요.

## 다음

- 문제 발생 시 → [12-troubleshooting.md](12-troubleshooting.md)
