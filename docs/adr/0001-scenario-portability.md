# ADR-0001: 시나리오 이식성 — DB 유지 + export/import 계층

- **상태:** 제안됨 (Proposed)
- **날짜:** 2026-07-26
- **관련:** [시나리오 이식성 설계](../workload/06-portability.md), [`schemas/scenario.schema.json`](../schemas/scenario.schema.json)

## 맥락 (Context)

시나리오를 다른 환경(다른 노트북·팀원·git repo)에서 불러와 재사용하고 싶다는 요구가 있었다.
초기 제안은 "시나리오를 DB 대신 JSON 파일로 저장" 이었다.

확인 결과, 시나리오 내용은 **이미 JSON** 이다:

- `ScenarioTemplate.StepsJSON` — `string` (JSON)
- `ScenarioTemplate.LoopsJSON` — `sql.NullString` (JSON)

SQLite 는 이 JSON 문자열을 담는 컨테이너일 뿐이다. 따라서 진짜 문제는 저장 포맷이 아니라
**이식성** — "그 JSON 을 하나의 자체완결 파일로 꺼내고 다시 넣는 문" 이 없다는 것.

## 결정 (Decision)

**SQLite 저장소는 그대로 두고, export/import 이식 계층만 추가한다.**

1. 이식 포맷은 자체완결 JSON (`.scenario.json` / `.scenariopack.json`).
2. `steps` 는 proto shape(`ScenarioStep`/`MacroEvent`, camelCase)을 **가공 없이** 담는다.
3. 이식성의 핵심은 `requirements` 메타데이터(설치앱·해상도·trace 타입)와 `schemaVersion`.
4. API 는 기존 `rest_preset.go` 의 scenario-templates CRUD 옆에 3개 엔드포인트로 추가.

## 근거 (Rationale)

DB 를 파일 기반으로 전환하면 DB 가 **무료로 주던 것을 전부 재구현** 해야 한다:

| DB 가 지금 주는 것 | 파일 전환 시 |
|---|---|
| 정렬·페이징·필터 (executions 목록) | 직접 구현 |
| 동시 쓰기 안전 (cron + UI 동시) | 파일 락 직접 관리 |
| 부팅 시 stale 정리 · 트랜잭션 | 재구현 |
| JobExecution 연결 (scheduled_job_id 등) | 참조 무결성 수동 |

시나리오 내용은 이미 JSON 이므로, 부족한 건 "이식 가능한 파일로 꺼내는 문" 하나뿐이다.
저장소를 갈아엎을 이유가 없다.

`schemaVersion` 을 export 도입 **전에** 넣는 이유: 나중에 스텝 구조가 바뀌어도 옛 파일을 읽을 수 있게
하려면 첫 파일부터 버전이 박혀 있어야 한다. 사후에 붙이면 버전 없는 파일들이 이미 퍼진 뒤라 늦다.

`requirements` 가 없으면 다른 해상도·미설치 앱 환경에서 좌표 tap 이 어긋나 **조용히 깨진다.**
import 시 이 블록을 대조해 경고를 띄우는 게 이식성의 실질적 핵심이다.

## 대안 (Alternatives considered)

- **A. 시나리오를 DB 대신 JSON 파일 디렉토리로 저장** — 기각. 위 표의 재구현 비용. 이식성 이득은 export/import 로 동일하게 얻음.
- **B. 별도 시나리오 공유 서버** — 기각. git repo 커밋이 더 단순하고 버전 관리·리뷰가 공짜.
- **C. steps 를 이식용 자체 포맷으로 재설계** — 기각. proto shape 을 그대로 쓰면 변환 버그가 없다.

## 결과 (Consequences)

**긍정**

- 기존 DB 인프라(쿼리·동시성·부팅정리·JobExecution 연결) 그대로 유지.
- 파일로 내보내 git 에 커밋 → 팀이 버전 관리·PR 리뷰. 별도 서버 불필요.
- `requirements` + dry-validate 로 "조용히 깨지는 이식" 방지.

**부정 / 유의**

- export/import 는 얇지만 새 코드 표면이다 — schemaVersion 마이그레이션을 계속 관리해야 함.
- proto shape 을 그대로 노출하므로, 스텝 구조 변경 시 이식 포맷도 함께 버전업해야 함(그래서 schemaVersion 필수).
- REST 핸들러는 gRPC interceptor 를 우회하므로, 향후 auth 도입 시 import 엔드포인트에 별도 검증 필요.

## 구현 순서

1. `GET .../{id}/export` (읽기 전용, 위험 없음)
2. `schemaVersion` + `POST .../import` (검증·경고·contentHash 스킵)
3. `GET .../export-all` (bundle)
4. 카탈로그 예제를 `docs/examples/` 에 커밋 (스키마 검증 완료)
