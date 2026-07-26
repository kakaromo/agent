# 자동 실행

"사람이 조작해야 하지만, 알아서 동작하게" — 이건 소프트웨어 자동화의 정확한 정의다.
사람은 **무엇을 할지 한 번 정의**하고, 시스템이 **그걸 반복·순회·검증**한다.
UI 자동화·크롤러·continuous benchmarking·트래픽 리플레이는 이 문제를 각기 다른 규모에서 이미 풀었다.

## 자동화 성숙도 사다리 — 우리 위치

| 레벨 | 단계 | 상태 |
|---|---|---|
| L0 | 수동 실행 (조립 + 실행 버튼) | ✅ 있음 |
| L1 | 반복 자동 — cron 스케줄 (benchmark) | ✅ 있음 |
| **L1½** | **UX 시나리오 자동 dispatch** | ⚠️ 막힘 |
| L2 | 순회 자동 — 배터리 | 다음 |
| L3 | 적응 자동 — 회귀·이상 대응 (반자동) | 다음 |

## 지금 막힌 곳 — 딱 하나의 구멍

benchmark 는 이미 cron 이 자동 실행한다. 요소 기반 **UX 시나리오** 의 자동 dispatch 만 placeholder 로 비어 있다 (`schedule/runner.go`):

```go
case "benchmark":
    resp, err := r.agent.RunBenchmark(ctx, req)   // ✅ 자동 실행됨
    ...
case "scenario":
    return "", fmt.Errorf("scenario dispatch not yet implemented")  // ❌ 여기
```

!!! warning "필요한 작업"
    `stepsJson/loopsJson → RunScenarioRequest` 변환. 수동 실행 경로(`grpc.go`)에 이미 이 변환이 있으니 재사용 → 작은 작업으로 UX 시나리오 cron 자동화 완성.

## 01. 사람 조작을 코드로 재생 — UI 자동화

"사람이 할 일을 기계가 대신 조작" 의 가장 성숙한 층. 특히 **UIAutomator** 는 실유저 행동을 시뮬레이션하도록 설계됐고 앱 경계를 넘는 시스템 조작(권한 팝업·알림·다중 앱)까지 자동화한다 — 우리 요소 기반 빌더가 쓰는 바로 그 도구.

둘(UIAutomator + Espresso)을 결합하면 *supervised monkey*(무작위가 아니라 정해진 흐름을 자동 반복)를 구현할 수 있다 — 우리 `tap_element / scroll / text` 스텝의 자동 재생이 정확히 이 범주.

!!! note "우리 위치"
    이미 uiautomator dump 로 요소를 잡아 재생 중 — L0/L1 은 이 기반 위에 있다. 자동화는 "재생 능력" 이 아니라 "언제 재생할지" 의 문제로 좁혀진다.

- [UIAutomator 가이드](https://getautonoma.com/blog/android-ui-automator-testing-guide) · [Espresso UI Automation](https://www.browserstack.com/guide/espresso-android-testing)

## 02. 스스로 앱을 탐색 — GUI 크롤러 & 모델 기반

사람이 스텝을 안 짜도 시스템이 *스스로 화면을 순회* 하며 워크로드를 만들어내는 연구 계보. Monkey(무작위) → GUI Ripper(모델 구축) → 최근 RL·VLM(human-like 시퀀스)로 진화했다.

GUIRipper/MobiGUITAR 는 앱을 크롤링해 *화면=상태, 이벤트=전이* 인 FSM 모델을 자동 구축. 최근엔 강화학습·비전 모델(VLM-Fuzz)로 의미 있는 human-like 경로를 학습해 커버리지를 높인다.

!!! note "우리 위치"
    크롤러를 붙이면 "발굴" 단계까지 자동화 가능 — 단, human-like 재현성은 요소 기반 접근이 더 안정적이라 **크롤러=발견, 요소스텝=정규측정** 분업이 현실적.

- [GUI Ripping (ASE'12)](https://dl.acm.org/doi/10.1145/2351676.2351717) · [RL 기반 크롤링·테스팅](https://www.mdpi.com/2076-3417/16/2/1093) · [VLM-Fuzz](https://link.springer.com/article/10.1007/s10664-026-10816-4)

## 03. 매일 알아서 벤치마크 — Continuous Benchmarking

우리 cron 러너가 하려는 것의 정확한 이름. *정기적으로 자동 벤치마크를 돌려 성능 회귀를 가능한 빨리 감지* 하는 개발 관행.

팀이 최소 매일 자동 빌드로 벤치마크를 검증해 회귀를 조기 감지. production-like 워크로드 모델로 기준선 대비 pass/fail 판정을 자동화하고, 실패 시 릴리스를 막는 gate 로 쓴다.

!!! note "우리 위치"
    cron 러너 + JobExecution 영속 + 배너 해석이 이미 continuous benchmarking 의 뼈대. 남은 건 "기준선 비교/판정" 로직 — L3 로 가는 다리.

- [Continuous Benchmarking (Bencher)](https://bencher.dev/docs/explanation/continuous-benchmarking/) · [코드 레벨 성능 변화 자동 식별 (arXiv)](https://arxiv.org/pdf/2303.14256)

## 04. 실측을 자동 재생 — 워크로드 리플레이 & 신세틱 모니터링

운영 시스템에서 실측한 워크로드를 *테스트 시스템에 자동 재생* 하고 성능을 관측하는 업계 기법. "실사용 워크로드를 알아서 순회" 의 대규모 대응.

production 트래픽을 캡처해 재생(GoReplay·Kraken)하거나, 핵심 유저 여정을 *합성 모니터(신세틱)* 로 주기 실행해 가용성·성능을 지속 검증. 성능 테스트 자동화의 핵심은 *반복 가능하고 일관된 프로세스*.

!!! note "우리 위치"
    우리는 "실측 세션 → 요소 스텝" 으로 워크로드를 record & replay 하는 셈. 이를 야간 cron 배터리로 돌리면 스마트폰 스토리지판 신세틱 모니터링이 된다.

- [Automated Performance Testing (k6)](https://grafana.com/docs/k6/latest/testing-guides/automated-performance-testing/) · [Synthetic Monitoring (MS Playbook)](https://microsoft.github.io/code-with-engineering-playbook/automated-testing/synthetic-monitoring-tests/)

## 결론 — 답은 "예", 그리고 거의 다 왔다

워크로드 UX 자동 실행은 가능하고, 우리 인프라는 사다리의 L0·L1 을 이미 갖췄다. 진행 순서는:

1. **L1½ 구멍 메우기** — `runner.go` scenario dispatch 구현. 수동 경로의 변환을 재사용하는 작은 작업.
2. **L2 배터리** — 발굴된 top 워크로드 세트를 한 스케줄로 묶어 야간 순회 (신세틱 모니터링 패턴).
3. **L3 적응** — 배너 지표를 기준선과 비교, 회귀 시 자동 재실행·플래그 (continuous benchmarking gate). 사람 판단이 필요하니 반자동으로.

핵심: 자동화는 **"재생 능력" 이 아니라 "언제·무엇을 재생할지" 의 문제** 로 이미 좁혀졌다. 재생 엔진(요소 스텝)·스케줄러(cron)·해석(배너)·영속(DB)이 다 있으니, 남은 건 이들을 잇는 얇은 배선이다.
