#!/usr/bin/env bash
# Rust tools/trace --parquet-only 와 Go trace/parser 의 결과를 비교한다.
#
# 사용법:
#   scripts/compare-parsers.sh <trace.log> <ufs|block|both|ufscustom>
#
# 두 파서 각각 임시 디렉토리에 result_*.parquet 을 만든 후,
# DuckDB 로 양방향 EXCEPT (Rust\Go 와 Go\Rust) 행 수를 출력한다.
# 두 값이 모두 0 이면 row 수준에서 정합.
#
# 필요한 도구: ./tools/trace (Rust), ./goparse (go build ./cmd/goparse), duckdb CLI.

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <trace.log> <ufs|block|both|ufscustom|fsio_ufs|fsio_block>" >&2
  exit 2
fi

LOG="$1"
TYPE="$2"

if [[ ! -f "$LOG" ]]; then
  echo "trace log not found: $LOG" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUST_BIN="$ROOT/tools/trace"
GO_BIN="$ROOT/goparse"

for bin in "$RUST_BIN" "$GO_BIN"; do
  if [[ ! -x "$bin" ]]; then
    echo "binary missing or not executable: $bin" >&2
    echo "  - Rust:  prebuilt at tools/trace" >&2
    echo "  - Go:    run 'go build ./cmd/goparse' from repo root" >&2
    exit 1
  fi
done
if ! command -v duckdb >/dev/null; then
  echo "duckdb CLI not on PATH" >&2
  exit 1
fi

WORK="$(mktemp -d -t compare-parsers.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
RUST_DIR="$WORK/rust"
GO_DIR="$WORK/go"
mkdir -p "$RUST_DIR" "$GO_DIR"

echo "[1/4] Rust --parquet-only → $RUST_DIR" >&2
# Rust 는 output_prefix 인자라 result 접두사를 명시한다.
"$RUST_BIN" --parquet-only "$LOG" "$RUST_DIR/result" >/dev/null

echo "[2/4] Go RunParquetOnly → $GO_DIR" >&2
"$GO_BIN" "$LOG" "$GO_DIR" "$TYPE" >/dev/null

# 비교할 parquet 종류. 입력 trace_type 하나가 산출물 여러 개를 낼 수 있다.
#
# fsio_ufs / fsio_block 은 result_fsio_read.parquet 을 **함께** 낸다 — VFS read 종료
# 요약은 같은 로그에 섞여 오는 형제 산출물이라 독립 trace_type 이 아니다.
# page-cache 판정(hit/miss)이 Rust 와 어긋나면 여기서 잡힌다.
resolve_types() {
  case "$1" in
    ufs)         echo "ufs" ;;
    block)       echo "block" ;;
    both)        echo "ufs block" ;;
    ufscustom)   echo "ufscustom" ;;
    fsio_ufs)    echo "fsio_ufs fsio_read" ;;
    fsio_block)  echo "fsio_block fsio_read" ;;
    *) echo "unknown trace type: $1" >&2; exit 1 ;;
  esac
}

echo "[3/4] DuckDB row-by-row 비교" >&2
status=0
for t in $(resolve_types "$TYPE"); do
  rust_pq="$RUST_DIR/result_${t}.parquet"
  go_pq="$GO_DIR/result_${t}.parquet"

  if [[ ! -f "$rust_pq" || ! -f "$go_pq" ]]; then
    echo "  [$t] parquet 누락 — rust=$([[ -f $rust_pq ]] && echo ok || echo MISS) go=$([[ -f $go_pq ]] && echo ok || echo MISS)"
    status=1
    continue
  fi

  # 행 수 + 양방향 EXCEPT 카운트. union_by_name 없이 컬럼 순서/이름까지 같아야 한다.
  duckdb -c "
    CREATE TEMP VIEW r AS SELECT * FROM read_parquet('$rust_pq');
    CREATE TEMP VIEW g AS SELECT * FROM read_parquet('$go_pq');
    SELECT
      '$t' AS type,
      (SELECT COUNT(*) FROM r) AS rust_rows,
      (SELECT COUNT(*) FROM g) AS go_rows,
      (SELECT COUNT(*) FROM (SELECT * FROM r EXCEPT SELECT * FROM g)) AS rust_only,
      (SELECT COUNT(*) FROM (SELECT * FROM g EXCEPT SELECT * FROM r)) AS go_only;
  "
done

echo "[4/4] done (work dir: $WORK)" >&2
exit "$status"
