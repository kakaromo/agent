# 시나리오 예제

이식 포맷(`schemaVersion`/`kind`/`steps`) 으로 된 자체완결 시나리오 파일.
스키마는 [`../schemas/scenario.schema.json`](../schemas/scenario.schema.json),
설계 배경은 [ADR 0001](../adr/0001-scenario-portability.md).

## 불러오기

```bash
curl -X POST http://127.0.0.1:50051/api/agent/scenario-templates/import \
  -H 'Content-Type: application/json' \
  -d @docs/examples/clock-offset-verify-fsio-ufs.scenario.json
```

Scenario 탭의 템플릿 목록에 뜬다. 같은 내용을 다시 넣으면 `contentHash` 로
걸러져 `skipped` 로 돌아온다(중복 생성 안 됨).

## UX 워크로드

실앱 조작을 재현해 앱별 IO 프로파일을 보는 용도.

| 파일 | 내용 |
|---|---|
| `cold-start-repeat` | force_stop 후 앱 재실행 반복 — 콜드 로딩 순차 read |
| `youtube-homefeed` | 홈피드 스크롤 — 영상 프리페치 |
| `browser-multitab` | 탭 여러 개 전환 |
| `gallery-capture` | 촬영·저장 — write 우위 (콜드=read 우위 법칙의 예외) |
| `cache-clear-discard` | pm clear 후 재실행 — discard/trim |
| `note-ai-summary` | 노트 앱 AI 요약 |

## clock offset 검증

**behavior 구간 분할이 실제로 맞는지 확인하는 용도.** 다른 예제와 목적이 다르다 —
앱 IO 프로파일이 아니라 **도구 자체를 검증**한다.

| 파일 | 경로 |
|---|---|
| `clock-offset-verify-fsio-ufs` | fsio UFS (eBPF, root 필요) — 메인 |
| `clock-offset-verify-fsio-block` | fsio Block |
| `clock-offset-verify-ftrace-ufs` | ftrace UFS |

구조: `trace_start → sleep 5s → dd(64MB, fsync) → sleep 5s → rm → trace_stop`

**왜 필요한가.** 스텝 경계는 호스트 wall clock 인데 parquet `time` 은 기기 시각이라,
둘을 잇는 offset 이 틀리면 **구간이 통째로 밀려도 그래프는 정상으로 보인다.**
눈으로 못 걸러내므로 알려진 시각에 IO 를 일으켜 확인하는 수밖에 없다.

**보는 법.** 실행 후 Trace 결과의 **Behavior 탭**에서:

- ✅ 64MB write 가 **dd 스텝 행**에 잡힌다
- ❌ 옆 구간(sleep)에 잡히거나 전 구간에 퍼져 있다 → offset 이 틀렸다

⚠ **판단 기준은 sleep 구간이 "0" 이냐가 아니다.** 백그라운드 앱이나 f2fs GC 가
IO 를 낼 수 있다. **64MB write 가 dd 행에 잡히느냐**를 본다.

⚠ Behavior 탭이 아예 안 보이거나 Charts 에 "구간을 표시할 수 없습니다" 배너가 뜨면
clock offset 측정 자체가 실패한 것이다. 원인은
`POST /api/agent/trace/clocksync` 로 확인한다(`reason` 필드).

**ftrace 판만 어긋난다면** `trace_clock` 설정이 안 먹은 것이다. ftrace 기본 clock
`local` 은 suspend 중 멈춰서 호스트 측정(`/proc/uptime`, BOOTTIME)과 축이 달라진다.
agent 가 시작 시 `boot` 로 바꾸는데, 실패하면 부팅 로그에 경고가 남는다.
