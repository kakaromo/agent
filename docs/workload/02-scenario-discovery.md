# 시나리오 발굴 방법론

우리 도구는 요소 기반 스텝으로 실앱 워크로드를 **재현**하고 그 구간의 UFS/Block I/O 를 **측정·해석**한다.
남은 질문은 하나 — **무엇을 실행할지**. 시나리오 발굴은 즉흥이 아니라 방법론이 있는 활동이며,
아래 다섯 소스는 각각 확립된 성능공학·SRE·시스템 연구에 근거를 둔다.

## 발굴 → 우리 도구 파이프라인

```
관찰/가설 → 요소 스텝 조립 → trace_start/stop 구간 지정 → 실행
   → 워크로드 배너로 해석 → scenario_template 저장 → (↻) 커버리지 매트릭스 갱신
```

## 01. 실사용 행동에서 역산 — Critical User Journey

가장 신뢰도 높은 소스. "사용자가 실제로 뭘 하는가" 를 관찰해 스텝으로 옮긴다. SRE 의 **CUJ** 는 이걸 정식 방법론으로 만든 것 — 제품에서 가장 가치 있는 핵심 흐름을 골라 태스크 시퀀스로 모델링.

우리 요소 스텝(`launch_app → scroll → tap_element → text`)이 CUJ 를 그대로 코드화한다.

!!! question "발굴 질문"
    이 앱에서 스토리지를 가장 많이 건드리는 순간은 언제인가? — 보통 cold start, 미디어 저장, 캐시 생성.

- [SRE Workbook — Implementing SLOs](https://sre.google/workbook/implementing-slos/) · [Product-focused Reliability](https://sre.google/resources/practices-and-processes/product-focused-reliability-for-sre/)

## 02. IO 특성 축에서 역산 — 커버리지 매트릭스

사용자 관점이 아니라 **UFS/Block 관점**에서 짜는, 스토리지 벤치마크 도구만의 고유 발굴법. 스토리지 트레이스 특성화 연구는 워크로드를 *R/W 비율·요청 크기·정렬·동시성(QD)·응답시간·오프셋* 이라는 표준 파라미터로 기술한다.

| 특성 축 | 극단 A | 극단 B | 유발 시나리오 예 |
|---|---|---|---|
| R/W 비율 | Read 지배 | Write 지배 | 앱 cold 로딩(R) vs 연속 촬영·녹화(W) |
| IO 크기 | 4K 랜덤 | 순차 대용량 | DB·설정 접근 vs 동영상 파일 저장 |
| 큐 뎁스 (QD) | QD1 직렬 | QD≥16 병렬 | UI 단일 반응 vs 백그라운드 플러시·다운로드 |
| DISCARD | 없음 | TRIM 폭주 | 정상 조작 vs 대량 삭제·캐시 정리 |
| Cold / Warm | Cold | Warm | force_stop 후 재실행 vs 연속 실행 |

각 축의 극단을 유발하는 실앱 시나리오를 하나씩 찾으면 커버리지 세트가 완성된다. 트레이스 특성화는 평균·분산만으로 부족하고 **히스토그램(분포)** 으로 표현해야 한다는 게 정설 — 배너/차트가 latency·QD 를 분포로 보여주는 이유다.

- [Characterization of Storage Workload Traces (IISWC'08)](https://iiswc.org/iiswc2008/Papers/012.pdf) · [Cloud Block Storage Workloads (arXiv)](https://arxiv.org/pdf/2203.10766)

## 03. 표준 벤치마크를 실앱으로 번역 — Trace-driven

합성 벤치마크(fio·PCMark·AndroBench)가 이미 정의한 워크로드 패턴을 **실앱 재현으로 승격**한다. 워크로드 특성화는 *model-driven*(합성)과 *trace-driven*(실측 재현) 두 갈래로 나뉘며, 우리 도구는 후자에 강점이 있다.

fio 의 "70/30 mixed randrw" 가 어떤 앱 행동에서 나오는지 역추적하거나, PCMark "사진 편집·저장" 을 실제 갤러리 앱 매크로 + trace 로 승격. 합성 결과와 실앱 결과를 나란히 놓고 배너로 판독.

- [TraceTracker: I/O Workload Reconstruction (arXiv)](https://arxiv.org/pdf/1709.04806) · [2DIO: Cache-Accurate Trace Generation (EuroSys'25)](https://dl.acm.org/doi/10.1145/3767295.3769391)

## 04. 이상 구간을 재현 시나리오로 승격 — Trace → Scenario

이미 수집한 trace 에서 **비정상 구간을 발견**하고 그걸 반복 재현 시나리오로 만든다. record-and-replay 는 "버그·비정상 행동을 결정적으로 재현" 하는 확립된 기법 — 우리는 그 대상을 UI 버그가 아니라 **IO 이상**으로 확장한다.

워크로드 배너가 `p99 warn`·`DISCARD 폭주` 를 잡으면 그 job 을 `scenario_template` 으로 저장 → Gregg 의 drill-down(이상을 반복 재현해 원인 격리)을 도구로 구현.

- [RANDR: Record and Replay for Android](https://seclab.bu.edu/papers/sahin19-randr.pdf) · [R&R: are we there yet? (FSE'17)](https://dl.acm.org/doi/10.1145/3106237.3117769)

## 05. 대표 시나리오를 정량적으로 추린다 — 커버리지 선정

시나리오를 많이 모으면 다음 질문은 "어느 걸 정규 세트로 남길까". SPEC 의 벤치마크 선정이 정확히 이 문제를 푼다 — 중복 워크로드는 쳐내고, 서로 다른 코드 경로·병목을 exercise 하는 것만 남긴다.

SPEC 은 워크로드를 특성 벡터로 클러스터링하고 각 클러스터의 *medoid*(대표)만 골라 그룹당 4~5개로 전체 거동의 96~99% 를 복원한다. 우리 식으로는: IO 특성 매트릭스로 시나리오를 클러스터링해 셀당 대표 하나만 정규 세트로 승격.

- [SPEC CPU2026: Representativeness (arXiv)](https://arxiv.org/pdf/2605.03713) · [SPEC CPU: The Next Generation (arXiv)](https://arxiv.org/pdf/2605.01575)

## 실전 발굴 루프

다섯 소스를 순서대로 쓰면 하나의 발굴 루프가 된다:

1. **CUJ 로 넓게 시작** — 앱별 핵심 여정 3~5개를 요소 스텝으로 조립.
2. **IO 매트릭스로 빈칸 확인** — 5개 축 극단 중 아직 안 건드린 셀을 찾아 시나리오 추가.
3. **합성 벤치마크와 짝짓기** — fio/PCMark 패턴에 대응하는 실앱 시나리오로 비교 가능하게.
4. **이상을 승격** — 배너가 warn 잡은 job 을 scenario_template 으로 고정, 반복 재현.
5. **medoid 로 정규 세트 확정** — 특성 유사 시나리오는 대표 하나만 남겨 회귀 스위트로.

발굴은 일회성이 아니라 순환이다. 우리 도구는 이미 이 루프의 모든 단계(요소 스텝 · trace 구간 · 워크로드 배너 · scenario_template)를 갖췄고, 방법론은 그걸 **어떤 순서로 채울지**를 준다.
