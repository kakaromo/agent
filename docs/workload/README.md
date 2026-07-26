# 워크로드 방법론

job 상세의 "무엇이 돌았고 왜 이렇게 동작했나" 워크로드 컨텍스트에서 출발해,
**어떤 워크로드를 측정할지 발굴하고 → 실사용을 확인하고 → 자동 실행하고 → 시나리오로 조립·이식**하는
전 과정의 방법론과 근거를 정리한 섹션이다.

## 문서 구성

| 문서 | 성격 | 무엇 |
|---|---|---|
| [배너 설계 근거](01-context-banner-grounding.md) | 근거 | 워크로드 컨텍스트 배너가 선 학술·업계 방법론 |
| [시나리오 발굴 방법론](02-scenario-discovery.md) | 근거 | 측정할 가치 있는 워크로드를 찾는 5가지 소스 |
| [실사용 워크로드 확인](03-real-usage-discovery.md) | 근거 | "자주 쓰는"을 데이터로 정의하는 4층 실측 방법 |
| [자동 실행](04-automation.md) | 근거 | 사람이 정의한 워크로드를 자동 반복·순회하는 방법 |
| [발굴 워크북 (how-to)](05-authoring-howto.md) | 구현 | 우리 도구로 시나리오를 직접 조립하는 절차 |
| [시나리오 이식성 설계](06-portability.md) | 구현 | export/import 로 다른 환경·팀·git 에서 재사용 |

## 리서치 문서(외부 링크·비교표 중심)

`01`~`04` 는 외부 학술·업계 근거를 매핑한 리서치 문서다. 시각적 비교가 필요한 원본은
Artifact 로도 발행돼 있으며(팀 공유용), 이 마크다운은 repo 내 영속 사본이다.

## 관련 코드

- 규칙 엔진: `ui/src/routes/agent/agent-result/workloadContext.ts`
- 배너: `ui/src/routes/agent/agent-result/WorkloadContextBanner.svelte`
- 시나리오 실행: `benchmark/scenario.go`, 스텝 proto: `proto/agent.proto` (`ScenarioStep`/`MacroEvent`)
- 이식 포맷: [`schemas/scenario.schema.json`](../schemas/scenario.schema.json), 예제: [`examples/`](../examples/)
- 결정 기록: [ADR-0001 시나리오 이식성](../adr/0001-scenario-portability.md)

관련 기존 문서: [Benchmark / Scenario](../08-benchmark-scenario.md), [Schedule (cron)](../10-cron-schedule.md), [SQLite 스키마](../06-sqlite-schema.md)
