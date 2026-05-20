# 01. 프로젝트 개요

## 무엇을 하는 도구인가

Android 디바이스를 USB로 노트북에 연결한 뒤, ADB를 통해 디바이스의 **스토리지 성능을 측정하고 분석**하는 에이전트.

핵심 기능:

1. **벤치마크 실행** — fio, iozone, tiotest, iotest 를 디바이스에 push 후 실행, 결과 metrics(IOPS, BW, latency 백분위 등) 수집
2. **커널 트레이스 수집** — UFS / Block layer 의 ftrace 이벤트를 실시간 capture, parquet 으로 변환 후 DuckDB 로 통계 계산
3. **디바이스 모니터링** — CPU, Memory, Disk I/O, 파일시스템 사용량을 1초 단위로 스트리밍
4. **시나리오 실행** — multi-step 실행 (loop, condition, shell, cleanup, sleep, trace_start/stop, app_macro, install_apk/uninstall_apk 등)
5. **앱 매크로** — 디바이스 UI 자동화 (tap/swipe/key, OCR 기반 추출, scrcpy 스크린 스트리밍)
6. **APK 설치/제거** — `tools/apks/` 폴더에 둔 .apk 를 디바이스에 push/install, 또는 설치된 앱 uninstall (벤치마크 앱 사전 준비용)

## 두 가지 운영 모드

### 사무실 모드 (default)

```bash
./agent -config config/devices.toml
```

- 0.0.0.0 바인딩, 외부 클라이언트(예: portal 웹포털, esportal)가 gRPC로 연결
- 노트북/서버는 디바이스 평가 인프라의 한 컴포넌트로만 동작
- UI 없음 (헤드리스 gRPC 서버)

### Standalone 모드 (출장용)

```bash
./agent --standalone -config config/devices.toml
# → 브라우저 http://127.0.0.1:50051 로 접속
```

- 127.0.0.1 만 바인딩 (외부 LAN 차단). 사내망에서 동료와 공유하려면 `--bind 0.0.0.0` (또는 특정 IP)
- portal 의 풀 UI 를 바이너리에 임베드 (Scenario DAG 빌더, deck.gl scatter, scrcpy 스트리밍 포함)
- SQLite 로 잡 이력/프리셋/스케줄 영속화
- 외부 의존성 없음 (MinIO 불필요, portal Spring 백엔드 불필요)
- 진짜 **단일 바이너리**로 출장지 노트북에서 평가 가능

두 모드 모두 동일한 바이너리이고, 같은 포트(50051)에서 gRPC + HTTP + WebSocket 을 [cmux](https://github.com/soheilhy/cmux) 로 다중화한다. standalone 시 HTTP 분기에 REST/SSE/WS + Svelte SPA 가 추가로 마운트될 뿐.

## 두 모드의 차이 한눈에

| 항목 | 사무실 | Standalone |
|---|---|---|
| 바인딩 (기본) | 0.0.0.0:50051 | 127.0.0.1:50051 — `--bind` 로 override |
| gRPC | ✓ (원격 클라이언트 사용) | ✓ (호환 유지) |
| HTTP REST `/api/agent/*` | × | ✓ |
| HTTP SSE `/api/agent/.../stream` | × | ✓ |
| Svelte SPA `/agent`, `/_app/*` | × | ✓ (//go:embed) |
| Trace 파서 | tools/trace (Rust) | Go 내장 (`trace/parser`) |
| 영속화 | × (메모리) | SQLite (`~/.agent-standalone/agent.db`) |
| Archive | MinIO | 로컬 디스크 (`~/.agent-standalone/archive/`) |
| Cron 스케줄 | × | ✓ (robfig/cron v3) |
| 인증 | 없음 (interceptor 부재) | 없음 (localhost 전제) |

## 디바이스/도구 의존성

**필수:**
- `adb` (Android Debug Bridge) — PATH 에 있어야 함. SDK Platform Tools 설치 권장
- Android 디바이스 (USB 연결, USB 디버깅 ON)

**push 되는 외부 바이너리** (`tools/` 디렉토리):
- `fio`, `iozone`, `tiotest` — Android arm64 ELF. 파일명이 다르면 `[tools]` 섹션으로 override (예: `fio = "fio-3.36"`)
- `iotest` — `cmd/iotest` 에서 Go 빌드 (Android arm64)
- `scrcpy-server` — 스크린 스트리밍용 JAR
- `trace` — **사무실 모드 전용** Rust 트레이스 파서. standalone 에서는 Go 파서가 강제되어 사용 안 함
- `apks/*.apk` — 디바이스에 설치할 벤치마크 앱 (antutu 등). UI / 시나리오에서 자동 노출. 자세한 정책은 [`tools/apks/README.md`](../tools/apks/README.md)

**옵션:**
- MinIO (사무실 모드의 archive 업로드용. standalone 에서는 불필요)

## 시스템 요구사항

- Go 1.25+
- Node 18+ (UI 빌드용)
- macOS / Linux 우선 지원, Windows 는 후속 (계획상)
- 디스크: trace 출력은 한 잡당 수십 ~ 수백 MB (long-running 의 경우 더 큼)
- 메모리: agent 자체는 ~50-100MB, fio 등 외부 도구는 별도

## 다음 읽을 페이지

- 처음 실행해 보고 싶다면 → [02-quickstart.md](02-quickstart.md)
- 내부 구조가 궁금하다면 → [03-architecture.md](03-architecture.md)
- standalone 자세히 → [04-standalone-mode.md](04-standalone-mode.md)
