#!/usr/bin/env bash
# routes/agent/trace/ 사본이 portal 원본과 갈라졌는지 본다.
#
# 기준은 portal /trace 다. 차이가 나면 **portal 을 고치고 여기로 복사**한다
# (반대 방향으로 하면 두 화면이 또 갈라진다).
#
# portal 체크아웃이 없으면 조용히 통과한다 — 빌드를 막지 않는다.
set -u

PORTAL="${PORTAL_REPO:-$HOME/project/portal}/frontend/src/routes/trace"
HERE="$(cd "$(dirname "$0")/.." && pwd)/src/routes/agent/trace"

if [ ! -d "$PORTAL" ]; then
  echo "skip: portal 원본을 못 찾음 ($PORTAL). PORTAL_REPO 로 지정 가능."
  exit 0
fi

# 사본에 허용된 유일한 수정은 타입 import 경로다. 비교 전에 원본에 같은 치환을 걸어
# 그 한 줄 때문에 매번 DIFF 가 뜨는 걸 막는다.
normalize() {
  sed 's#\$lib/api/trace\.js#./types.js#; s#\$lib/utils/arrow-decoder\.js#./types.js#' "$1"
}

rc=0
for f in cmdColors.ts TraceChartView.svelte TraceStatsView.svelte BoundaryLegend.svelte; do
  if [ ! -f "$PORTAL/$f" ]; then echo "WARN 원본 없음: $f"; continue; fi
  if diff -q <(normalize "$PORTAL/$f") "$HERE/$f" >/dev/null; then
    echo "OK   $f"
  else
    echo "DIFF $f  — portal 이 앞서 있으면 복사할 것:"
    echo "     sed 's#\\\$lib/api/trace.js#./types.js#; s#\\\$lib/utils/arrow-decoder.js#./types.js#' \\"
    echo "       '$PORTAL/$f' > '$HERE/$f'"
    rc=1
  fi
done
exit $rc
