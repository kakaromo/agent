# 발굴 워크북 (how-to)

방법론은 [시나리오 발굴](02-scenario-discovery.md)에서 다뤘으니, 여기선 **실제 손을 움직이는 절차** 다.
후보 워크로드를 정하고 → 요소 스텝으로 조립하고 → trace 구간을 감싸고 → 실행·해석·저장까지.

!!! note "스텝 구조 주의 (실제 코드 기준)"
    요소 조작(`tap_element`/`scroll_capture`/`text`/`key`)은 **최상위 `ScenarioStep.type` 이 아니다.**
    이들은 `app_macro` 스텝의 `macro.events` 배열 안에 들어간다. 최상위 스텝 타입은
    `benchmark / shell / cleanup / sleep / trace_start / trace_stop / condition / app_macro / install_apk / uninstall_apk` 뿐이다.
    (proto: `proto/agent.proto` `ScenarioStep` / `MacroEvent`, 매핑: `server/rest_macro.go`)

## 1. 후보를 고른다 — 어디서 가져오나

감으로 정하지 말고 세 소스 중 하나에서 후보를 끌어온다 — 빈도(실측) / 표준(벤치마크 정의) / IO 특성(매트릭스 빈칸).

**공개 참조 — 기존 스토리지 벤치마크의 워크로드 정의**

바닥부터 만들 필요 없다. 기존 벤치마크가 워크로드를 이미 정의해뒀다:

- **AndroBench** — 순차/랜덤 R·W + SQLite insert/update/delete
- **MobiBench / AndroStep** — 파일 IO + SQLite 트랜잭션 (워크로드 생성기 + 트레이스 분석기 + **리플레이어**, 우리 도구와 같은 구조)
- **PCMark** — 실앱 기반 워크로드 표준

이 목록을 실앱 행동으로 번역하면 후보가 나온다 — "SQLite insert 폭주" → 채팅앱 메시지 수신, "순차 write 대용량" → 동영상 저장.

- [AndroBench (논문)](https://link.springer.com/content/pdf/10.1007%2F978-3-642-27552-4_89.pdf) · [MobiBench / AndroStep (GitHub)](https://github.com/ESOS-Lab/Mobibench) · [Android I/O Stack 분석 프레임워크](https://www.mdpi.com/1999-5903/5/4/591)

## 2. 스텝으로 조립한다 — 우리 팔레트

후보 워크로드를 우리 도구의 요소 기반 스텝으로 옮긴다. 좌표가 아니라 resource-id/text 로 요소를 잡으므로 화면이 조금 달라도 재현이 안정적이다.

**최상위 스텝** (`ScenarioStep.type`)

| 스텝 | 하는 일 | 발굴에서의 쓰임 |
|---|---|---|
| `app_macro` | 실앱 UX 워크로드 재현 (packageName + clearMode + events) | 실앱 행동 전체 |
| `trace_start` / `trace_stop` | UFS/Block ftrace 구간 감쌈 (params: `trace_type`) | **측정 구간 지정** |
| `benchmark` | fio/iozone/tiotest/iotest | 합성 워크로드 |
| `sleep` / `shell` / `cleanup` | 대기 / 셸 / 정리 | 구간 사이 조정 |

**app_macro 안의 이벤트** (`macro.events[].type`)

| 이벤트 | 필드 (camelCase) | 쓰임 |
|---|---|---|
| `tap` | `x`, `y` | 커스텀뷰·게임 좌표 |
| `tap_element` | `elementText` / `elementResourceId` / `elementContentDesc` / `elementMatchMode` / `elementIndex` / `elementContainerId` (+폴백 `x`,`y`) | 동적 콘텐츠 조작 |
| `text` | `inputText` | 검색·메시지 입력 |
| `swipe` | `x`,`y` → `x2`,`y2`, `duration` | 드래그·플링 |
| `scroll_capture` | `direction`(down/up), `maxScrolls`, `scrollPause` | 피드 로딩(read) |
| `key` | `keycode` (BACK=4, HOME=3, MEDIA_STOP=86) | 종료·정지 |
| `wait` | `seconds` (초 단위) | 로딩 대기 |
| `wait_until` | `waitMethod`(activity/ui_text/screen_stable), `waitPattern`, `timeout`, `pollInterval` | 조건 대기 |
| `screenshot` | `name`, `ocrRegion`, `ocrPattern` | 캡처·OCR |

- 좌표 단위 필드(`x`/`y`/`x2`/`y2`)는 `sourceWidth`/`sourceHeight` 기준으로 녹화되고 재생 시 대상 해상도로 스케일된다.
- `elementMatchMode`: `exact`(기본, 완전일치 후 부분일치 폴백) · `contains` · `prefix` · `suffix` · `regex`.
- 앱 실행은 별도 이벤트가 아니라 `app_macro.macro` 의 `packageName` + `clearMode`(none/force_stop/clear)가 담당한다.
- 전체 필드 정의: [`schemas/scenario.schema.json`](../schemas/scenario.schema.json) (proto `MacroEvent` / `server/rest_macro.go` `macroEventToMap` 기준).

## 3. 측정 구간을 감싼다 — trace 위치가 전부

가장 흔한 실수가 여기서 난다. 측정하려는 워크로드가 trace 구간 **안** 에 있어야 한다.

!!! warning "measurement 이 워크로드 밖에 놓이는 실수"
    `app_macro → trace_start → trace_stop` (❌ 워크로드가 trace 앞) 가 아니라
    `trace_start → app_macro → trace_stop` (✅) 여야 한다. 측정 대상 행동을 **감싸는** 위치에 trace_start/stop 을 둘 것.

!!! warning "warm-up 을 구간에 포함시키는 실수"
    cold start 를 재려면 `clearMode: "force_stop"` 을 trace **안** 에, 정상 조작만 재려면 앱 로딩을 warm-up 으로 두고 이후 행동만 감싼다. 무엇을 재는지 먼저 정하고 경계를 긋는다.

## 4. 바로 쓰는 워크로드 카탈로그

IO 특성 매트릭스의 셀을 채우는 시작 세트. 아래 5종 모두 실행 가능한 완전한 JSON 이 [`examples/`](../examples/) 에 있고, 스키마 검증을 통과한다.

| 앱 · 워크로드 | IO 성격 | 흐름 (trace 로 감쌈) | 파일 |
|---|---|---|---|
| 유튜브 홈피드 소비 | READ 지배 | `app_macro`(scroll_capture ×8 → tap_element → wait → key MEDIA_STOP) | [youtube-homefeed](../examples/youtube-homefeed.scenario.json) |
| 갤러리 연속 촬영/저장 | WRITE 지배 + DISCARD | `app_macro`(tap 셔터 ×5 → key BACK) | [gallery-capture](../examples/gallery-capture.scenario.json) |
| 노트 AI 요약 저장 | MIXED (R+W+SQLite) | `app_macro`(tap 문서 → tap_element AI요약 → wait_until) — 커스텀뷰라 `tap` 좌표 의존 | [note-ai-summary](../examples/note-ai-summary.scenario.json) |
| 앱 cold start ×N | READ · 높은 QD | loop{ `app_macro`(clearMode=force_stop → wait_until activity) } ×5 | [cold-start-repeat](../examples/cold-start-repeat.scenario.json) |
| 캐시 정리 / 대량 삭제 | DISCARD (TRIM) | `app_macro`(설정 → 저장공간 → 캐시 지우기) | [cache-clear-discard](../examples/cache-clear-discard.scenario.json) |

!!! note "좌표 예제는 실기기 재확인 필요"
    `tap` 좌표(셔터·메뉴 위치)와 일부 `elementText`(OneUI 버전별 라벨)는 기기·앱 버전마다 다르다.
    예제의 `x`/`y` 는 1080×2340 기준 **예시값** 이며, 각 파일 `description` 에 그 취지가 적혀 있다.
    실제 실행 전 dry-run 으로 좌표·요소를 맞춘 뒤 `sourceWidth`/`sourceHeight` 와 함께 확정한다.

완전한 예제: [youtube-homefeed.scenario.json](../examples/youtube-homefeed.scenario.json) — 실제 이벤트 필드명(`scroll_capture`/`maxScrolls`/`elementText`/`seconds`)으로 작성돼 스키마 검증을 통과한다.

## 5. 조립 함정 — 이미 밟아본 것들

실기기에서 겪고 문서화한 실전 함정.

!!! warning "유튜브 플레이어 컨트롤 자동 fade-out"
    재생 중 컨트롤이 사라져 `tap_element` 가 요소를 못 잡음 → 직전 위치 `tap`(좌표)로 대체하거나 fade 전에 조작.

!!! warning "삼성 노트 등 전체 커스텀뷰"
    요소가 uiautomator 에 안 노출 → `tap` 좌표 의존. 탭마다 툴팁 팝업이 뜨니 `wait` 를 넉넉히.

!!! warning "유튜브는 BACK 으로 안 멈춘다 (PIP)"
    BACK 시 PIP 로 재생 지속 → 진짜 종료는 `key` keycode 86(MEDIA_STOP) 또는 `stop_app`. 세션 경계가 흐려지면 trace 가 오염됨.

## 발굴 1건 완주 체크리스트

- [ ] **측정 목표 1문장** — "이 시나리오는 무슨 IO 를 재는가"
- [ ] **소스 확인** — 실측 빈도 / 벤치마크 정의 / 매트릭스 빈칸 중 어디서 왔나
- [ ] **스텝 조립** — 요소 우선(`tap_element`), 커스텀뷰만 `tap`
- [ ] **trace 경계** — 측정 대상을 `trace_start/stop` 이 감싸는지, warm-up 포함 여부 확정
- [ ] **1회 dry-run** — 실기기 실행, 요소 미스·fade-out·PIP 함정 확인
- [ ] **배너로 검증** — 자동 해석이 의도한 IO 성격(READ/WRITE/QD/DISCARD)을 잡는가
- [ ] **템플릿 저장** — 가치 있으면 `scenario_template` 으로 고정, 매트릭스 셀 갱신

핵심: 발굴은 **"측정 목표 1문장 → trace 경계 → 배너 검증"** 의 반복이다. 공개 벤치마크 정의로 후보를 얻고, 우리 스텝으로 실앱에 번역하고, 배너로 의도대로 됐는지 확인하면 한 건이 완주된다 — 이걸 매트릭스가 찰 때까지 돌린다.
