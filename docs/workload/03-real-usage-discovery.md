# 실사용 워크로드 확인

시나리오를 조립하기 전에 답할 질문 — **"유저가 실제로 뭘, 얼마나 자주 하는가"**.
이건 넘겨짚을 게 아니라 데이터로 확인하는 일이며, HCI·모바일 시스템 연구와 제품 분석 기업들이
정립한 방법이 있다. 대규모 정량 로깅부터 소규모 정성 관찰까지 네 층.

## 방법의 스펙트럼

```
정량 · 대규모 · 자동  ←───────────────────────────→  정성 · 소규모 · 맥락
   ① 제품 텔레메트리    ② in-the-wild 로깅    ③ IO trace 데이터셋    ④ ESM · 다이어리
```

## 01. 제품 텔레메트리 — 기업이 실사용을 보는 방법

가장 대규모·자동화된 층. 기업은 앱 안의 모든 행동을 *이벤트* 로 계측하고 그 패턴을 분석해 "유저가 실제로 뭘 하는가" 에 답한다.

- **Mixpanel** — 모든 상호작용을 rich property 붙은 event 로 잡는 granular 접근(event-centric).
- **Amplitude** — 세그먼트·코호트·리텐션 중심(user-centric).

공통 질문은 하나 — "유저가 제품 안에서 실제로 무엇을 하는가". 자주 쓰이는 흐름(top funnel)이 곧 우리가 재현할 CUJ 후보.

!!! tip "우리 적용"
    사내 앱이면 이벤트 로그의 top-N 플로우를, 외부 앱이면 공개된 사용 통계를 시나리오 우선순위로.

- [Amplitude vs Mixpanel 방법론 비교](https://www.statsig.com/perspectives/amplitude-vs-mixpanel-which-analytics-tool-is-right-for-you)

## 02. In-the-wild 로깅 — 학술 실측의 고전

실험실이 아닌 *실제 생활 속* 에서 기기에 로거를 심어 장기간 사용을 측정하는 방법.

**LiveLab** (Rice대) — 재프로그래밍 가능한 in-device 로거로 34명을 6~12개월 추적한 첫 대규모 종단 연구. 기기 사용을 연속 수집하고 정기 인터뷰로 보강. "Smartphone usage in the wild" 류 후속 연구가 앱·컨텍스트 분포를 대규모로 특성화했다.

!!! tip "우리 적용"
    우리 도구의 `monitor`/trace 수집이 곧 소규모 in-device 로거다 — 대상 기기에서 실사용 세션을 며칠 돌려 앱·구간 빈도를 뽑고, 그 top 세션을 시나리오화.

- [LiveLab (MobiSys)](https://www.semanticscholar.org/paper/LiveLab:-measuring-wireless-networks-and-smartphone-Shepard-Rahmati/5892b9314971e90e32d8bf81ca4e7dcbecb5ef8f) · [Tales of 34 iPhone Users (arXiv)](https://arxiv.org/pdf/1106.5100) · [Smartphone Usage in the Wild](https://www.researchgate.net/publication/221052737_Smartphone_usage_in_the_wild_A_large-scale_analysis_of_applications_and_context)

## 03. 공개 IO trace 데이터셋 — 우리 층과 정확히 같은 데이터

가장 직접적인 소스. 이미 실유저에게서 *블록·UFS 레벨 IO 를 캡처한 트레이스* 가 공개돼 있고, 스마트폰 앱별 IO 특성을 분석한 연구가 있다.

스마트폰 앱 IO 를 캡처한 연구는 *Mail(로그인·동기화·이메일) 3분, Facebook(피드·프로필 갱신) 180초* 처럼 구체 워크로드 구간을 트레이스로 남긴다 — 우리 `trace_start/stop` 구간 설계의 직접 참고. **SNIA** 는 storage IO trace 의 대표 공개 저장소다.

!!! tip "우리 적용"
    공개 트레이스의 앱·구간 목록(로그인·피드로드·미디어저장 등)을 시나리오 체크리스트로 삼고, 우리 기기에서 실측해 비교.

- [Storage on Your Smartphone (HotStorage'17)](https://www.cs.utexas.edu/~vijay/papers/hotstorage17-energy.pdf) · [Exploring Public Storage Traces (SNIA 등)](https://towardsdatascience.com/exploring-public-storage-traces-16ef7ac9e038/)

## 04. ESM · 다이어리 — 맥락과 동기를 캐는 정성 층

로그는 "무엇을" 은 알려주지만 "*왜*" 는 못 준다. 경험표집법(ESM)·다이어리는 현장에서 유저에게 직접 물어 *왜 그 순간 그 앱을 썼는지*, 어떤 맥락이 사용을 유발했는지를 캔다 — 로그가 놓친 *드물지만 중요한* 워크로드를 발굴하는 데 유용.

ESM 은 *현상이 일어나는 그 장소·시각에* 사용자를 표집(랜덤/스케줄/이벤트 트리거)해 자기보고를 받는 방법. 정량 로그로 빈도를 잡고, ESM 으로 *왜·언제* 를 보강하는 혼합이 표준.

!!! tip "우리 적용"
    배너의 `workload_note` 가 바로 ESM 의 축소판 — 실행할 때 "이 순간 유저는 왜 이걸 하나" 를 메모로 남겨 시나리오 근거를 축적.

- [The ESM on Mobile Devices (ACM CSUR)](https://dl.acm.org/doi/10.1145/3123988) · [Diary Studies (UX 가이드)](https://www.userinterviews.com/ux-research-field-guide-chapter/diary-studies)

## 한눈에 — 어떤 방법을 언제

| 방법 | 알려주는 것 | 비용/규모 | 우리 도구에서 |
|---|---|---|---|
| ① 제품 텔레메트리 | 무엇을 얼마나 자주 (빈도·퍼널) | 낮음 · 대규모 | top 플로우 → CUJ 우선순위 |
| ② in-the-wild 로깅 | 실생활 앱·컨텍스트 분포 | 중간 · 종단 | monitor/trace 며칠 수집 → top 세션 |
| ③ IO trace 데이터셋 | 앱별 블록/UFS IO 구간 | 낮음 · 공개 | trace_start/stop 구간 체크리스트 |
| ④ ESM · 다이어리 | 왜·언제 (동기·맥락) | 높음 · 소규모 | workload_note 로 근거 축적 |

## 실전 순서 (깔때기)

네 방법은 경쟁이 아니라 깔때기로 쓴다. 큰 데이터로 후보를 넓게 잡고, 작은 관찰로 좁혀 확정:

1. **빈도부터 (①/②)** — 사내 앱은 텔레메트리 top-N, 외부 앱은 우리 기기에 `monitor` 를 며칠 걸어 실제 앱·세션 빈도를 확보. "자주 쓰는" 을 **숫자로** 정의.
2. **IO 구간 매핑 (③)** — 공개 trace 연구의 앱별 워크로드 목록을 체크리스트로, 우리 top 세션과 대조해 측정 구간 확정.
3. **맥락 보강 (④)** — 상위 워크로드마다 "왜 이 순간 이걸 하나" 를 `workload_note` 로 기록.
4. **시나리오로 승격** — 확정된 top 워크로드를 요소 스텝으로 조립 → [발굴 루프](02-scenario-discovery.md).

핵심: **"자주" 를 감이 아니라 빈도 데이터로 정의**하는 게 1번. 우리 도구의 monitor·trace·workload_note 가 이미 소규모 실측 로거 역할을 할 수 있으니, 외부 데이터셋 없이도 대상 기기에서 자체 실측이 가능하다.
