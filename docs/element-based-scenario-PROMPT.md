# 작업 프롬프트: 요소 기반(Element-based) 시나리오 빌더 추가

> 이 문서를 agent 프로젝트에서 작업하는 Claude 세션에 그대로 붙여넣으세요.
> 사전 조사(코드 위치·훅·주의점)가 이미 담겨 있으니, 다시 전체 탐색하지 말고 이 지점들을 **먼저 열어 확인**한 뒤 착수하면 됩니다.

---

## 배경 / 목표

이 프로젝트(`/Users/songhyun/project/agent`)는 Android 디바이스에서 **사용자 행동을 재현하고 그 행동이 UFS/Block 저장장치 I/O에 주는 영향을 커널 트레이스로 측정**하는 Go 기반 에이전트다. 시나리오 실행 엔진(`benchmark/scenario.go`)·매크로 재생(`macro/`)·트레이스(`trace/`)·시각적 DAG 빌더(`ui/.../scenario-canvas`)가 이미 존재하고 동작한다.

**현재 문제:** 매크로/시나리오의 탭이 **절대좌표 기반**(`input tap x y`)이라, (1) 만들 때 좌표를 일일이 찍어야 하고 (2) 앱 UI가 바뀌면 깨진다.

**목표:** 좌표 대신 **요소(resource-id·text·content-desc)를 선택**해서 스텝을 만들고, 재생 시 그 요소를 화면에서 다시 찾아 중심을 탭하는 **요소 기반 방식**을 추가한다. 최종적으로는 "라이브 화면에서 요소를 클릭 → 캔버스에 요소 탭 블록이 추가"되는 흐름을 만든다.

**중요 원칙:**
- 기존 좌표 기반 tap은 **제거하지 말고 그대로 둔다**(폴백으로도 쓴다). 요소 기반은 **추가**다.
- 새 이벤트 타입 문자열은 `tap_element`로 한다(기존 `tap`과 구분).
- 좌표 폴백: 요소를 못 찾으면(게임·DRM 등 uiautomator 트리가 빈 화면) 저장해 둔 좌표로 탭하도록 폴백 필드를 함께 저장한다.

---

## 사전 확인된 코드 지점 (먼저 열어볼 것)

착수 전 아래 파일들을 읽어 현재 구현을 눈으로 확인하라. (라인 번호는 조사 시점 기준, 다소 어긋날 수 있으니 함수명으로 찾을 것.)

### 저수준 / 재생
- `adb/device.go` — `func (d *Device) Shell(ctx, cmd string) (string, error)` : 모든 `input`/`uiautomator` 명령의 관문. **재사용.**
- `macro/replayer.go`
  - `Replay()`의 `switch ev.Type` — case들이 서로 격리돼 있음. 여기에 `case "tap_element":` 추가가 진입점.
  - `dumpUITexts()` — 현재 `uiautomator dump /sdcard/ui.xml` 실행 후 `cat`, 그리고 정규식 `text="([^"]+)"`로 **text만** 추출. **bounds·resource-id·content-desc는 안 뽑음** → 여기가 신규 파서가 필요한 이유.
  - `getDeviceResolution()` (또는 `macro/recorder.go`) — 해상도 조회. 재사용 가능.
- `macro/recorder.go` — `getDeviceUIText()` : `strings.Contains`로 XML 원문에 패턴 존재만 확인(bool). dump 실행 패턴 참고용.
- `macro/screenshot.go` — Tesseract OCR. **OCR은 좌표를 반환하지 않으므로 요소 기반엔 쓰지 않는다.** uiautomator XML의 `bounds` 파싱이 정공법.

### 데이터 모델 (proto)
- `proto/agent.proto`
  - `message MacroEvent` — 필드 1~19 사용 중, **다음 번호 20**. reserved/gap 없음. 여기에 셀렉터 필드 추가.
  - `message AppMacroConfig` — `repeated MacroEvent events` 이므로 MacroEvent에 필드 추가하면 자동 상속. **수정 불필요.**
  - `message ScenarioStep` — `type`은 문자열, `params`는 `map<string,string>`. 셀렉터는 MacroEvent 레벨 관심사라 **ScenarioStep도 수정 불필요.**
  - 재생성 명령(CLAUDE.md 참고): `protoc --go_out=. --go-grpc_out=. proto/agent.proto` → **`pb/agent.pb.go`가 갱신됨** (실제 사용처). `proto/agent.pb.go`는 stale 중복이니 무시.
- `pb/agent.pb.go` — import는 전부 `pb "agent/pb"`. 재생성 대상은 `pb/`.

### JSON 변환 (수동 매핑 — 실수 주의)
- `server/rest_macro.go`
  - `macroEventToMap()` — proto → camelCase JSON. **필드마다 `if` 한 줄 수동.**
  - `buildMacroEvent()` — JSON → proto. **필드마다 수동.** replay 요청 + 시나리오 hydrate 둘 다 여기 통과.
  - ⚠️ 이 둘은 protojson 자동이 아니다. 새 필드를 **두 함수 짝으로** 추가하지 않으면 컴파일 에러 없이 조용히 누락된다.
- `server/rest_scenario.go` — `hydrateMacroSteps()` 가 `macroId`만 온 스텝을 DB events로 채움. 셀렉터 필드는 `buildMacroEvent` 재사용하므로 자동 커버.

### DB
- 매크로 이벤트는 `sqlitedb.AppMacro.EventsJSON`에 **JSON 문자열 통째로** 저장. **스키마 마이그레이션 불필요.** 새 셀렉터 필드는 JSON에 자연히 들어감.

### UI (SvelteKit, `ui/`)
- `ui/src/routes/agent/scenario-canvas/NodePalette.svelte` — `stepTypes` 배열이 팔레트 블록 목록의 단일 진실 소스. 새 블록 한 줄 추가.
- `ui/src/routes/agent/scenario-canvas/types.ts` — `STEP_TYPE_COLORS`, `stepSummary()` 에 새 타입 case 추가.
- `ui/src/routes/agent/scenario-canvas/ScenarioCanvas.svelte` — `onDrop` / `createDefaultStep(type)` : 일반 스텝 생성 경로. 기본 필드 초기화 추가.
- `ui/src/routes/agent/scenario-canvas/serializer.ts` — `canvasToProto()` / `buildStepParams()` : 노드 → step JSON. `app_macro`가 `macroId` 등 top-level 필드 붙이는 선례를 따를 것. 역방향은 `protoToCanvas()`.
- `ui/src/routes/agent/AgentStepEditDialog.svelte` — `StepForm` 인터페이스에 옵셔널 필드(`elementText?`, `elementResourceId?`, `elementX?`, `elementY?` 등) 추가.
- `ui/src/routes/agent/AgentMacroRecorder.svelte` **와** `AgentScreenSheet.svelte` — 라이브 H.264 미러링. `getVideoRect()`(레터박스 보정)·`sendTouch()`(화면좌표→디바이스픽셀 변환)가 이미 있음 → **요소 오버레이 박스 배치의 기반으로 재사용.**
- `ui/src/lib/api/agent.ts` — 매크로 API 클라이언트(`replayMacro`, `takeScreenshot`, `screenshotOcr` 등). 새 "요소 목록" API 클라이언트 함수를 여기 추가.
- ⚠️ 확인된 사실: `ui/src` 어디에도 `uiautomator`/`bounds=`/`resource-id`/요소 트리 소비 코드가 **없다.** 요소 목록을 받아 화면에 박스로 그리는 UI는 **전부 신규.** 정적 스크린샷+박스 오버레이 참고 렌더러도 없음.

---

## 작업 항목 (권장 순서: 백엔드 먼저)

백엔드(①②④⑤)를 먼저 완성해 **실기기에서 "요소로 탭이 실제로 되는지"를 UI 없이 검증**한 뒤, UI(③)를 얹는다. 이 아이디어의 성패는 요소 인식·재생이 실제로 되느냐이므로 그걸 먼저 못박는다.

### ① [Go] XML 요소 파서 (핵심 신규)
`uiautomator dump` 결과 XML을 `encoding/xml`로 노드 파싱해, 각 노드의
`bounds="[x1,y1][x2,y2]"`, `resource-id`, `text`, `content-desc`, `class`, `clickable` 를 뽑고 **중심 좌표**를 계산하는 헬퍼를 만든다.
- 새 파일 예: `macro/uihierarchy.go`
- 반환 구조 예: `type UIElement struct { ResourceID, Text, ContentDesc, Class string; Clickable bool; CenterX, CenterY int; Bounds [4]int }`
- `bounds` 문자열 파서(`[x1,y1][x2,y2]` → 정수 4개, 중심 = 중점) 포함.
- dump 실행은 기존 패턴 재사용: `dev.Shell(ctx, "uiautomator dump /sdcard/ui.xml")` → `dev.Shell(ctx, "cat /sdcard/ui.xml")`.

### ② [Go/proto] 요소 목록 API
현재 화면의 클릭 가능 요소 목록(①의 결과)을 프론트로 반환하는 gRPC/REST 엔드포인트 + `agent.ts` 클라이언트 함수를 `rest_macro.go` 매크로 API 옆에 추가.
- REST 예: `GET /api/agent/macro/ui-elements?deviceId=` → `[]UIElement`.
- gRPC 추가 시 proto에 메시지/RPC 추가 후 재생성.

### ④ [Go/proto] 요소 셀렉터 필드 + 재생 case
- `proto/agent.proto` `MacroEvent`에 필드 추가(번호 20~): `string element_resource_id`, `string element_text`, `string element_content_desc` (그리고 폴백용 좌표는 기존 `x`,`y` 재사용). `protoc` 재생성.
- `server/rest_macro.go` `macroEventToMap` + `buildMacroEvent` **둘 다** 새 필드 매핑 추가(짝으로!).
- `macro/replayer.go` `Replay()` switch에 `case "tap_element":` 추가:
  1. ①로 현재 화면 요소 목록 획득
  2. 셀렉터 우선순위(resource-id → text → content-desc)로 매칭
  3. 찾으면 중심 좌표 `input tap`, **못 찾으면 저장된 `x`,`y` 좌표로 폴백**(폴백 시 로그/메트릭에 표시)
- ⑤ 배선은 ④ 안에서 rest_macro 두 함수로 함께 처리됨.

### ③ [Svelte] 클릭 오버레이 UI (마지막, 리스크 큼)
- `AgentMacroRecorder.svelte`(또는 신규 컴포넌트)에서 라이브 `<video>` 위에 절대위치 오버레이 레이어를 만들고, ② API로 받은 요소 bounds를 `getVideoRect()` 기준으로 박스 배치.
- 박스 클릭 → 해당 요소의 셀렉터로 `tap_element` 스텝을 만들어 캔버스에 추가.
- 포인터 이벤트 우선순위 주의(`<video>` 위 클릭 가능 오버레이). "요소 선택 모드" 토글로 평소 미러링 조작과 분리 권장.
- 팔레트/직렬화/StepForm(위 UI 지점)에 `element_tap` 블록 대응 추가.

---

## 검증 방법

- **①②④ 백엔드 완료 후 (UI 전에):** 실기기 연결 → 아무 앱 열고 → ② API로 요소 목록이 실제 뜨는지 확인 → `tap_element` 이벤트를 담은 매크로를 직접 replay 요청(REST)으로 던져 **요소가 실제로 눌리는지** 확인. 폴백도 일부러 안 잡히는 셀렉터로 테스트.
- **③ UI 완료 후:** 라이브 화면에서 요소 클릭 → 캔버스 블록 생성 → 저장 → 재생까지 전 흐름.
- **회귀:** 기존 좌표 기반 `tap` 매크로가 여전히 동작하는지(제거 금지 원칙) 반드시 확인.

## 주의 / 함정 (조사에서 확인됨)

1. **JSON 변환은 수동.** `macroEventToMap`↔`buildMacroEvent` 짝으로 안 고치면 조용히 누락. 컴파일 에러 안 남.
2. **proto 재생성은 빌드 스크립트에 통합돼 있지 않다.** `protoc ...`를 수동 실행해야 하고, 갱신 대상은 `pb/`(‌`proto/`의 .pb.go는 stale).
3. **uiautomator 트리가 빈 화면이 존재**(게임·DRM 영상 등). 좌표 폴백 필수.
4. **주석/커밋 메시지는 한국어**로 작성(프로젝트 규칙, CLAUDE.md).
5. `dumpUITexts`는 기존 벤치마크 metric 파싱에도 쓰이니 **시그니처를 바꾸지 말고** 새 파서를 별도 함수로 추가.

## 참고: 완성 후 유튜브 시나리오 예시 흐름
`앱 실행(youtube) → tap_element(text="검색") → text 입력("lofi") → tap_element(첫 결과) → wait(재생) → swipe(추천 스크롤) → tap_element(다음 영상)` 을 `trace_start`/`trace_stop`으로 감싸 재생 구간 UFS I/O 측정.
(참고: 텍스트 입력 `input text` 액션도 현재 없음 — 필요하면 이 작업 중 `case "text":`로 함께 추가 가능. 별도 갭이나 요소 탭과 궁합이 좋음.)

### 구현 완료 (2026-07-24) — 실기기 검증됨
백엔드 + UI 전 항목 구현·커밋(`6500c1d`) 후 실기기(SM-S938N, Android 16)에서 유튜브 실앱으로 검증 완료.
검색→영상 재생→다른 영상 전환→Shorts 전환→BACK 중단까지 요소 기반 탭만으로 동작 확인.
셀렉터 3종(resource-id / text / content-desc) + 부분일치 + 좌표 폴백 모두 실증.

**함정 — 유튜브 플레이어 컨트롤 자동 fade-out:**
유튜브 시청 화면의 재생/일시정지·다음·전체화면 등 **플레이어 컨트롤 버튼은 몇 초 후 자동으로 숨김(fade-out)** 된다.
컨트롤이 숨겨진 상태에서 `uiautomator dump` 를 뜨면 해당 버튼이 트리에 없어 `tap_element` 가 못 찾고 좌표 폴백으로 떨어진다.
→ 시나리오에서 플레이어 컨트롤 버튼을 요소로 탭하려면, **직전에 플레이어 영역을 좌표 `tap` 한 번(컨트롤 표시)** 넣고 나서 `tap_element` 를 배치하는 게 안정적이다.
예: `tap(화면중앙) → wait(0.5s) → tap_element(desc="일시중지")`.
이는 유튜브 UI 특성이며 파서/재생 로직 문제가 아니다. (일반 앱 버튼은 항상 트리에 있어 이 처리가 불필요.)
