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

### CGO 분기 — Windows 는 cgo 필수

Windows 빌드는 DuckDB 의존 때문에 **반드시 CGO**. `go-duckdb` v1.8.5 는 cgo 없이는 컴파일 자체가 실패한다 (`undefined: Conn` — `connection.go` 가 cgo 가드 안에 있는데 `transaction.go` 가 그 타입을 참조).

CGO 빌드 — MinGW gcc 필요:

```bash
CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 go build ...
```

build.sh (Linux/macOS 호스트) 는 MinGW 미발견 시 CGO_ENABLED=0 으로 시도하지만 **이건 동작하지 않는다**. 위 이유로 컴파일 에러. 실제 동작하는 Windows 바이너리를 만들려면 MinGW 가 필수.

#### Windows 호스트에서 MinGW 설치

```powershell
# 1) MSYS2 설치
winget install MSYS2.MSYS2

# 2) MSYS2 UCRT64 셸 열고 gcc 설치
pacman -S --needed mingw-w64-ucrt-x86_64-gcc

# 3) Windows 시스템 PATH 에 추가
#    제어판 → 환경 변수 → Path 에 다음 한 줄 추가:
#    C:\msys64\ucrt64\bin

# 4) 새 PowerShell 열고 확인
gcc --version
# → gcc (Rev1, Built by MSYS2 project) 14.x.x ... 출력되면 OK
```

설치 후 `.\build.ps1` 또는 `.\run.ps1` 가 자동으로 MinGW gcc 를 감지해 CGO 빌드.

#### 방화벽 환경 — pacman 사용 불가일 때 수동 설치

사내 방화벽으로 `pacman` 의 패키지 다운로드가 막힌 경우, MSYS2 패키지는 단순한 zstd
tarball (`.pkg.tar.zst`) 이라 사외에서 받아서 옮길 수 있다.

1. **사외 환경**에서 MSYS2 인스톨러와 필요한 패키지를 다운로드 (mirror: `https://repo.msys2.org/mingw/ucrt64/`):

   - `msys2-x86_64-<date>.exe` — MSYS2 본체 인스톨러
   - UCRT64 toolchain 패키지들 (최신 버전, 정확한 파일명은 mirror 의 인덱스 참조):
     - `mingw-w64-ucrt-x86_64-gcc-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-gcc-libs-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-binutils-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-crt-git-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-headers-git-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-libwinpthread-git-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-winpthreads-git-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-gmp-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-mpfr-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-mpc-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-isl-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-zlib-*.pkg.tar.zst`
     - `mingw-w64-ucrt-x86_64-zstd-*.pkg.tar.zst`

2. **사내 PC** 로 USB/공유 폴더 등으로 옮긴다.

3. **MSYS2 본체 설치** — 인스톨러 실행, 기본 경로 `C:\msys64`. 본체 설치 자체는 네트워크 불필요.

4. **시작 메뉴 → "MSYS2 UCRT64"** 셸을 열고 로컬 파일로 설치 (네트워크 사용 안 함):

   ```bash
   # 받은 .pkg.tar.zst 들을 한 폴더에 모아둔 뒤
   pacman -U /c/path/to/pkgs/*.pkg.tar.zst
   # pacman 이 의존성 순서를 알아서 해결한다.
   ```

5. **검증**:

   ```bash
   which gcc                                # /ucrt64/bin/gcc
   gcc --version                            # 14.x.x ...
   gcc -v 2>&1 | grep "Thread model"        # posix
   gcc -print-file-name=libstdc++.a         # /ucrt64/.../libstdc++.a 존재 확인
   ```

6. **Windows 시스템 PATH 에 `C:\msys64\ucrt64\bin` 추가** 후 새 PowerShell 에서:

   ```powershell
   gcc --version
   .\build.ps1                              # → dist\agent-windows-amd64.exe
   ```

##### 더 무거운 옵션: pacman repo 통째 미러

오프라인 풀 미러를 원하면 사외에서 `https://repo.msys2.org/mingw/ucrt64/` 와 `msys/x86_64/` 를 wget 으로 받아 사내 mirror 로 쓴다. `/etc/pacman.d/mirrorlist.ucrt64` 의 Server 를 로컬 경로로 바꿔주면 `pacman -Syu` 가 그대로 동작. 다운로드 양이 ~2GB 이라 단순 toolchain 만 필요하면 위 수동 설치가 가볍다.

#### Linux/macOS 호스트에서 Windows cross-build

```bash
# macOS:    brew install mingw-w64
# Ubuntu:   sudo apt install mingw-w64
#           sudo update-alternatives --config x86_64-w64-mingw32-gcc  ← posix 선택
#           sudo update-alternatives --config x86_64-w64-mingw32-g++  ← posix 선택

CGO_ENABLED=1 \
  CC=x86_64-w64-mingw32-gcc \
  CXX=x86_64-w64-mingw32-g++ \
  CGO_LDFLAGS="-static -lpthread" \
  GOOS=windows GOARCH=amd64 \
  go build -o agent-windows-amd64.exe .

# 검증 — PE 바이너리인지 확인
file agent-windows-amd64.exe
# → PE32+ executable (console) x86-64, for MS Windows ... 가 정상
```

##### Ubuntu 에서 "undefined reference to pthread_mutex_unlock"

증상: 빌드 도중 링커가 pthread 심볼을 못 찾고 멈춤. 그 결과 exe 가 안 만들어지거나 손상된 파일.

원인: Ubuntu 의 mingw-w64 기본 thread model 이 `win32` 인 경우가 흔함. DuckDB(C++/pthread) 가 동작하려면 `posix` 모델이어야 함.

해결:

```bash
# 1) thread model 확인
x86_64-w64-mingw32-gcc -v 2>&1 | grep "Thread model"
# → Thread model: win32  ← 문제. posix 가 필요.

# 2) posix 변형 선택 (gcc + g++ 둘 다)
sudo update-alternatives --config x86_64-w64-mingw32-gcc
sudo update-alternatives --config x86_64-w64-mingw32-g++

# 3) 다시 확인
x86_64-w64-mingw32-gcc -v 2>&1 | grep "Thread model"
# → Thread model: posix

# 4) 빌드 시 CGO_LDFLAGS 에 winpthread 명시 (build.sh 가 자동 처리)
CGO_LDFLAGS="-static -lpthread" 
```

`build.sh` 가 thread model 을 자동 감지해 win32 면 경고 출력. 그래도 빌드는 시도하지만 링커 에러가 날 가능성이 높아 위 절차로 posix 로 바꾸세요.

### Windows 에서 직접 빌드 (PowerShell / CMD)

`build.sh` / `run.sh` 는 bash 라 Windows 에선 동작 안 함. 동등한 PowerShell + .bat wrapper 제공:

```powershell
# UI + agent.exe — CGO 자동 판단 (MinGW gcc 가 PATH 에 있으면 사용, 없으면 OFF)
.\build.ps1

# 빌드 + 실행 (UI build 도 자동)
.\run.ps1 -Standalone -Bind 0.0.0.0

# 이미 빌드된 exe 만 실행
.\run.ps1 -SkipBuild -Standalone

# 다른 OS 까지 cross-build (CGO OFF — MinGW 는 Windows 전용)
.\build.ps1 -All
```

CMD 또는 더블클릭은 `.bat` 사용 — 내부적으로 `-ExecutionPolicy Bypass` 로 ps1 호출:

```cmd
build.bat -SkipUI
run.bat -Standalone -Bind 0.0.0.0
```

CGO 빌드를 원하면 [MSYS2](https://www.msys2.org) 설치 후 MinGW 64-bit gcc(`x86_64-w64-mingw32-gcc` 또는 `gcc`) 를 PATH 에 등록.

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
