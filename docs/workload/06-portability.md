# 시나리오 이식성 설계

시나리오를 자체완결 파일로 내보내 **다른 노트북·팀·git 에서 불러와 그대로 실행**하는 설계.
결정 배경과 대안 비교는 [ADR-0001](../adr/0001-scenario-portability.md) 참고. 이 문서는 실무 요약이다.

## 핵심 사실: 시나리오는 이미 JSON

`ScenarioTemplate.StepsJSON` / `LoopsJSON` 이 이미 JSON 문자열이고, SQLite 는 그걸 담는 상자일 뿐이다.
그래서 목표는 저장 방식 변경이 아니라 **이식성** — DB 는 그대로 두고 export/import 계층만 얹는다.

!!! success "결정"
    **SQLite 유지 + export/import 추가.** 파일 기반 전환은 쿼리·페이징·동시성·부팅 정리를 다시 짜야 해 손해. 이식성은 "파일 하나 내보내기" 로 충분히 풀린다. → [ADR-0001](../adr/0001-scenario-portability.md)

## 이식 포맷 — 메타데이터가 이식성의 전부

steps 만 내보내면 다른 환경에서 조용히 깨진다. 우리 시나리오는 좌표 `tap` 과 요소 패턴을 섞어 쓰므로
*해상도·설치앱·trace 타입* 이 다르면 재현이 어긋난다. 이걸 막는 게 `requirements` 블록.

전체 스키마: [`schemas/scenario.schema.json`](../schemas/scenario.schema.json)

| 필드 | 필수 | 역할 |
|---|---|---|
| `schemaVersion` | MUST | 포맷 버전. import 호환 판정·마이그레이션 기준 |
| `name` / `steps` | MUST | 시나리오 본체 (steps 는 proto shape 그대로) |
| `requirements.packages` | MUST | 설치 필요 앱. import 시 미설치면 경고 |
| `requirements.sourceWidth/Height` | SHOULD | 좌표 tap 기준 해상도. 다르면 경고 |
| `requirements.traceType` | SHOULD | ufs / block / both |
| `origin.contentHash` | MAY | 중복 수입 스킵·변경 감지 |

!!! note "steps 는 proto shape 을 그대로 쓴다 (재발명 금지)"
    export 는 `StepsJSON` 을 가공 없이 담고 `requirements` 만 위에 덧씌운다 — 파싱/변환 버그 여지를 없앰.
    요소 조작은 `app_macro.macro.events` 안에 들어간다([발굴 워크북](05-authoring-howto.md) 참고).

## API — 기존 preset CRUD 패턴 재사용

`server/rest_preset.go` 의 scenario-templates CRUD 옆에 3개 엔드포인트만 추가. 새 인프라 없이 같은 mux·DB·변환 헬퍼를 쓴다.

| 메서드 | 경로 | 하는 일 |
|---|---|---|
| `GET` | `/api/agent/scenario-templates/{id}/export` | DB row → requirements 덧씌운 자체완결 JSON 다운로드. packages/해상도는 app_macro 에서 자동 수집 |
| `POST` | `/api/agent/scenario-templates/import` | JSON 업로드 → schemaVersion 검증 → requirements 대조(미설치앱·해상도 불일치 경고 리스트 반환) → DB insert. contentHash 같으면 스킵 |
| `GET` | `/api/agent/scenario-templates/export-all` | 여러 시나리오를 한 `.scenariopack.json` 으로 (팀 배포·git). import 는 배열도 수용 |

## 더 나은 아이디어 — 이식성을 넘어서

| 아이디어 | 왜 좋은가 |
|---|---|
| git 친화 포맷 | 파일로 내보내면 시나리오를 repo 에 커밋 → 버전 관리·PR 리뷰. 별도 서버 불필요 |
| schemaVersion 지금 도입 | 스텝 구조가 바뀌어도 옛 파일 read 가능. export 풀기 전에 넣어야 무료 |
| import 시 dry-validate | 실행 전에 미설치앱·해상도·trace 타입 경고 → "조용히 깨지는 이식" 방지 |
| 민감정보 스크럽 | export 시 디바이스 시리얼 등 제거/익명화 → 팀 공유 안전 |
| bundle 에 워크로드 메모 포함 | 시나리오 + `workload_note` 를 함께 이식 → "왜 이걸 재나" 까지 전달 |

## 구현 순서

1. **export 먼저** — `GET .../{id}/export`. StepsJSON 을 가공 없이 담고 requirements 덧씌움. 읽기 전용이라 위험 없음.
2. **schemaVersion + import** — 검증·경고 리스트·contentHash 스킵. dry-validate 로 "조용한 깨짐" 차단.
3. **bundle export-all** — 팀 배포·git 커밋용 다중 시나리오 파일.
4. **카탈로그를 실제 파일로** — [`examples/`](../examples/) 에 커밋. macroEventToMap 매핑 대조 검증 완료(스키마 통과).

핵심: **저장소는 그대로, 문 하나만 낸다.** 진짜 설계는 `requirements` 메타데이터 — 이게 있어야 이식이 "조용히 깨지지" 않는다.
