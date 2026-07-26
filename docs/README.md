# Agent 프로젝트 문서

Go 기반 Android 디바이스 평가 에이전트의 상세 문서.

## 빠른 시작

처음 보는 분은 아래 순서로 읽으면 됩니다.

1. **[프로젝트 개요](01-overview.md)** — 무엇을 하는 도구인지, 두 가지 모드(사무실 / 출장)
2. **[빌드 & 실행](02-quickstart.md)** — `./build-ui.sh && go build && ./agent --standalone`
3. **[아키텍처](03-architecture.md)** — 컴포넌트 구조, cmux 다중화, 데이터 흐름

## 주제별 문서

### Standalone (출장용 원바이너리)
- **[Standalone 모드](04-standalone-mode.md)** — `--standalone` 플래그가 켜는 모든 동작
- **[REST/SSE/WS API 레퍼런스](05-rest-api.md)** — portal 호환 47+ endpoints
- **[SQLite 스키마](06-sqlite-schema.md)** — 7 테이블, 영속화 hook, stale 정리

### 핵심 기능
- **[Trace 수집/파싱](07-trace.md)** — ftrace → parquet → 통계/raw 조회
- **[Benchmark / Scenario](08-benchmark-scenario.md)** — fio/iozone/iotest 실행 흐름
- **[Schedule (cron)](10-cron-schedule.md)** — robfig/cron 기반 자동 실행

### UI
- **[Svelte UI 구조](09-ui.md)** — portal/frontend 포팅 노트, 인증 stub, 사이즈 프리셋

### 워크로드 방법론
- **[워크로드 방법론 개요](workload/README.md)** — 발굴 → 실사용 확인 → 자동 실행 → 조립·이식 전 과정
- **[배너 설계 근거](workload/01-context-banner-grounding.md)** — 워크로드 컨텍스트 배너가 선 학술·업계 방법론
- **[시나리오 발굴 방법론](workload/02-scenario-discovery.md)** — 측정할 워크로드를 찾는 5가지 소스
- **[실사용 워크로드 확인](workload/03-real-usage-discovery.md)** — "자주 쓰는"을 데이터로 정의하는 4층 실측
- **[자동 실행](workload/04-automation.md)** — 사람이 정의한 워크로드를 자동 반복·순회
- **[발굴 워크북 (how-to)](workload/05-authoring-howto.md)** — 우리 도구로 시나리오를 직접 조립하는 절차
- **[시나리오 이식성 설계](workload/06-portability.md)** — export/import 로 다른 환경·팀·git 재사용
- ADR: **[0001 시나리오 이식성](adr/0001-scenario-portability.md)** · 스키마: [`schemas/scenario.schema.json`](schemas/scenario.schema.json) · 예제: [`examples/`](examples/)

### 운영
- **[배포](11-deployment.md)** — 출장 패키지 만들기, Windows 후속 지원
- **[문제 해결](12-troubleshooting.md)** — 자주 나오는 에러와 해결

## 빠른 참조

| 항목 | 위치 |
|---|---|
| 메인 바이너리 | `./agent` (78MB, UI 임베드 포함) |
| 설정 파일 | `config/devices.toml` |
| SQLite DB | `$HOME/.agent-standalone/agent.db` |
| Archive 폴더 | `$HOME/.agent-standalone/archive/` |
| Trace parquet | `$HOME/agent_trace/{jobId}/result_*.parquet` |
| UI 소스 | `ui/src/` |
| 임베드 대상 | `ui/build/` |
| gRPC proto | `proto/agent.proto` |
| 생성 코드 | `pb/agent.pb.go`, `pb/agent_grpc.pb.go` |
| 단일 포트 | 50051 (gRPC + HTTP + WS 다중화) |

## 관련 프로젝트

- `/Users/songhyun/project/portal` — agent gRPC 를 호출하는 Spring Boot 웹포털. **standalone UI 의 원본**.
- `/Users/songhyun/project/esportal` — HEAD TCP 기반 별개 시스템 (agent 와 무관, 혼동 주의)

## 코드 컨벤션

- 코드 주석과 커밋 메시지는 한국어
- 함수/변수명은 영어 (camelCase / PascalCase)
- TypeScript/Svelte 도 동일 규칙
- 새 RPC 추가 시 [05-rest-api.md](05-rest-api.md) 의 매핑 표 업데이트 필수
