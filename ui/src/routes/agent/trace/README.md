# `routes/agent/trace/` — portal `/trace` 사본

이 폴더의 `.svelte` / `cmdColors.ts` 는 **portal 레포에서 복사해 온 사본**이다.

- 원본: `~/project/portal/frontend/src/routes/trace/`
- 복사 시점 portal 커밋: **`af02795`** (2026-08-28)

## 기준은 원본이다

Trace 분석 화면은 세 벌 존재하고(**portal `/trace`**, portal `/agent`, 이 standalone),
**기준은 portal `/trace`** 다. 화면·표 구성을 고칠 일이 생기면

> **portal 을 먼저 고치고, 여기로 복사한다.**

여기서 직접 고치면 두 화면이 또 갈라진다. 실제로 그렇게 갈라진 적이 있다.

## 복사본에 허용되는 유일한 수정

**타입 import 경로 재작성.** 원본은 타입을 `$lib/api/trace.js`(portal 전용 REST 계층)와
`$lib/utils/arrow-decoder.js` 에서 가져오는데, standalone 은 그 백엔드를 쓰지 않는다
(데이터는 `$lib/api/agent.ts` 로 온다). 그래서 그 import 줄만 `./types.js` 로 바꾼다.

```diff
- import type { StepBoundary } from '$lib/api/trace.js';
+ import type { StepBoundary } from './types.js';
```

그 외에는 **한 줄도 고치지 않는다.** 재복사할 때 이 치환만 다시 적용하면 된다.

## 파일 목록

| 파일 | 원본 | import 치환 |
|---|---|---|
| `cmdColors.ts` | `routes/trace/cmdColors.ts` | 없음 (import 자체가 없다) |
| `TraceChartView.svelte` | `routes/trace/TraceChartView.svelte` | `StepBoundary`, `ChartMeta` |
| `TraceStatsView.svelte` | `routes/trace/TraceStatsView.svelte` | `Stats*` |
| `BoundaryLegend.svelte` | `routes/trace/BoundaryLegend.svelte` | `StepBoundary` |
| `types.ts` | (사본 아님 — standalone 전용) | — |

## 드리프트 확인

```bash
P=~/project/portal/frontend/src/routes/trace
for f in cmdColors.ts TraceChartView.svelte TraceStatsView.svelte BoundaryLegend.svelte; do
  diff <(sed 's#\$lib/api/trace.js#./types.js#; s#\$lib/utils/arrow-decoder.js#./types.js#' "$P/$f") "$f" \
    > /dev/null && echo "OK   $f" || echo "DIFF $f"
done
```

## 여기에 없는 것 (의도적)

`TraceRawDataView` / `TraceBehaviorView` / `TraceAttributionView` / `deckgl/` /
`TraceAiChatSheet` 는 **가져오지 않았다.** portal 의 parquet 페이징·MinIO·job 레지스트리를
전제하거나(standalone 엔 그 백엔드가 없다), standalone 쪽 구현이 이 데이터엔 더 낫기
때문이다. 자세한 이유는 이 작업의 계획 문서 "범위 제외" 절 참고.
