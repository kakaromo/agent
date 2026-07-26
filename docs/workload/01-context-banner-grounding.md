# 배너 설계 근거

job 상세의 워크로드 컨텍스트 배너 — **raw 수치 · 규칙 자동 해석 · 사용자 메모 · trace 진입** —
는 새 발명이 아니라 **데이터 서사화 · 워크로드 특성화 · 자동 인사이트** 세 갈래의 확립된 방법론을
IO 벤치마크 도메인에 맞춰 조립한 것이다. 아래는 배너의 각 구성 요소를 학술·업계 선례에 1:1로 매핑한 근거다.

## 01. 데이터 옆에 해석을 붙인다 — 서사형 시각화

숫자만 던지지 않고 *왜 중요한지*를 같은 화면에 두는 배너의 뼈대는 **narrative visualization** 계보에 놓인다.

**Narrative Visualization: Telling Stories with Data** (Segel & Heer, IEEE TVCG 2010, DOI 10.1109/TVCG.2010.179)

시각화에 서사를 결합하는 장르를 분류하고 *author-driven ↔ reader-driven* 축을 제시한 표준 참조(2,000+ 인용).

| 우리 코드 | 대응 |
|---|---|
| `deriveInsights()` | author-driven 서사 — 시스템이 미리 해석을 서술 |
| `workload_note` | reader-driven 주석 — 사용자가 맥락을 보강 |

- [Stanford PDF](http://vis.stanford.edu/files/2010-Narrative-InfoVis.pdf) · [ACM DL](https://dl.acm.org/doi/10.1109/TVCG.2010.179) · [dblp](https://dblp.org/rec/journals/tvcg/SegelH10.html)

## 02. "누가·무엇을·왜" 부하를 만드는가 — 워크로드 특성화

배너가 궁극적으로 하려는 일(실행이 시스템에 무슨 부하를 줬는지 사람이 읽게 특성화)은 시스템 성능공학의 교과서적 절차다.

**Systems Performance — Workload Characterization · USE Method** (Brendan Gregg, Netflix, Prentice Hall 2판 2020)

> "튜닝을 시작하기 전에 누가 요청하고, 무엇을, 왜 요청하는지부터 식별하라."

| 우리 코드 | 대응 |
|---|---|
| `describeWorkload()` | who / what / why — 워크로드가 무엇을 실행했나 |
| QD≥16 → 병렬 | Saturation — 큐 뎁스로 포화 판정 |
| p99 good / warn | Latency Analysis — 지연 임계 판정 |

- [USE Method (Gregg)](https://www.brendangregg.com/Slides/FISL13_USE_Method/) · [USENIX LISA12](https://www.usenix.org/conference/lisa12/performance-analysis-methodology) · [Systems Performance](https://www.goodreads.com/book/show/18058001-systems-performance)

## 03. 규칙으로 문장을 뽑는다 — 자동 인사이트 (업계 제품)

LLM 없이 규칙 테이블로 "p99가 평소보다 높음" 같은 문장을 생성하는 방식은 **automated insights** 라는 이름으로 상용 관측 제품에 표준화돼 있다.

- **Datadog Watchdog / Watchdog Explains** — 알림 설정 없이 이상을 자동 감지·설명, 기여 태그 surface. `deriveStepInsights()` 와 같은 패턴.
    - [Watchdog Insights](https://docs.datadoghq.com/watchdog/insights/) · [Watchdog Explains](https://docs.datadoghq.com/dashboards/graph_insights/watchdog_explains/)
- **Power BI Quick Insights** — 리포트를 열면 상관·이상·추세를 설명과 함께 자동 생성. 배너 "수치 해석(자동)" 과 목적 동일.
    - [Find Insights](https://learn.microsoft.com/en-us/power-bi/create-reports/insights) · [Quick Insights](https://learn.microsoft.com/en-us/power-bi/create-reports/service-insights)

## 04. 메트릭 위에 이벤트 맥락을 얹는다 — 주석 & 이벤트 마커

배너의 "메모" 와 "trace 진입 버튼" 은 시계열 위에 사람이 만든 맥락을 영속화하는 **annotation** 계보다.

**Grafana Dashboard Annotations** — 메트릭을 배포·장애 등 의미 있는 이벤트와 상관시켜 "수치 뒤의 완전한 이야기" 를 만든다. 주석 텍스트에 상세 시스템 링크를 넣을 수 있음.

| 우리 코드 | 대응 |
|---|---|
| `workload_note` | annotation description + tags |
| `trace_jobs` → CTA | annotation link to detail system |

- [Annotate visualizations](https://grafana.com/docs/grafana/latest/visualizations/dashboards/build-dashboards/annotate-visualizations/)

## 05. IO trace 를 사람이 읽게 그린다 — 도메인 선례

특히 **UFS/Block IO trace + fio** 도메인의 선례. "전체 trace 확인" 이 여는 `AgentTraceResultSheet`(패턴·QD·CPU·latency)의 직접 선조.

**iowatcher** (Chris Mason, Meta; seekwatcher 후속) — block IO trace 를 그래프/무비로. `AgentTraceResultSheet` 의 도메인 원형.

- [iowatcher(1)](https://man7.org/linux/man-pages/man1/iowatcher.1.html) · [iowatcher-ng](https://github.com/sbates130272/iowatcher-ng)

## 종합

배너의 네 요소는 각각 확립된 방법론에 대응한다:

- **무엇이 돌았나** = 워크로드 특성화 (Gregg)
- **자동 해석** = 서사형 시각화 (Segel & Heer) × 자동 인사이트 (Datadog · Power BI)
- **메모 + trace 진입** = 주석 (Grafana)
- **trace 시각화** = IO 도메인 선례 (iowatcher)

차별점은 **결정적 규칙 엔진**(LLM 없이 오프라인 standalone 동작) + **IO trace 도메인 특화 해석**(READ 웜스타트 / QD 병렬 / DISCARD TRIM 등)을 job 상세라는 **단일 진입점**에 조립한 점. 즉 "발명" 이 아니라 "올바른 조립" 이며, 위 선례들이 그 설계 판단을 뒷받침한다.
