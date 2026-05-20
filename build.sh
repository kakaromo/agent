#!/bin/bash
# 멀티 플랫폼 빌드. 각 타겟은 cgo 가 필요하며 (go-duckdb 의존), 해당 OS 의 cross-compiler 가
# 호스트에 없으면 그 타겟만 skip + 경고. host native 타겟은 별도 cross-compiler 없이 cgo 동작.
#
# 호스트별 cross-compiler 설치 가이드:
#   macOS:  brew install mingw-w64          # Windows 빌드
#           Linux/macOS amd64 는 native 호스트에서 빌드 권장 (CI / VM)
#   Ubuntu: sudo apt install mingw-w64      # Windows
#           sudo update-alternatives --config x86_64-w64-mingw32-gcc  ← posix
#           sudo apt install gcc-aarch64-linux-gnu  # linux arm64

set -u  # set -e 는 안 씀 — 한 타겟 실패가 나머지를 막지 않도록

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
DIST_DIR="dist"
mkdir -p "$DIST_DIR"

HOST_OS=$(go env GOOS)
HOST_ARCH=$(go env GOARCH)

echo "=== Host: $HOST_OS/$HOST_ARCH ==="
echo "=== Building UI (standalone embed) ==="
./build-ui.sh || { echo "UI build 실패" >&2; exit 1; }

echo "=== Building agent v${VERSION} ==="

# 한 타겟을 빌드한다. 인자: GOOS GOARCH 출력경로 [CC] [CXX] [LDFLAGS]
# CC 가 비어있고 host != target 이면 skip.
build_target() {
    local goos="$1" goarch="$2" out="$3" cc="${4:-}" cxx="${5:-}" ldflags="${6:-}"
    local is_native=false
    if [ "$goos" = "$HOST_OS" ] && [ "$goarch" = "$HOST_ARCH" ]; then
        is_native=true
    fi

    if [ "$is_native" = false ] && [ -z "$cc" ]; then
        echo "  ⊘ $goos/$goarch — cross-compiler 미설정/미발견, skip"
        return 0
    fi

    echo "  → $out (GOOS=$goos GOARCH=$goarch ${cc:+CC=$cc})"
    if [ -n "$cc" ]; then
        CGO_ENABLED=1 CC="$cc" CXX="$cxx" CGO_LDFLAGS="$ldflags" \
            GOOS="$goos" GOARCH="$goarch" \
            go build -o "$out" . || {
            echo "  ✗ $goos/$goarch 빌드 실패" >&2
            return 1
        }
    else
        # native — cgo 활성 (go-duckdb 필요), CC 는 호스트 기본
        CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" \
            go build -o "$out" . || {
            echo "  ✗ $goos/$goarch 빌드 실패" >&2
            return 1
        }
    fi
}

# macOS ARM64
build_target darwin arm64 "${DIST_DIR}/agent-darwin-arm64"

# macOS AMD64 — host 가 arm64 라면 cgo cross 가 까다로워 native 일 때만 빌드
build_target darwin amd64 "${DIST_DIR}/agent-darwin-amd64"

# Linux AMD64 — host 가 linux/amd64 이거나 cross-compiler 있을 때만
LINUX_AMD64_CC=""
if command -v x86_64-linux-gnu-gcc >/dev/null 2>&1; then
    LINUX_AMD64_CC="x86_64-linux-gnu-gcc"
fi
build_target linux amd64 "${DIST_DIR}/agent-linux-amd64" "$LINUX_AMD64_CC" "${LINUX_AMD64_CC/gcc/g++}"

# Linux ARM64
LINUX_ARM64_CC=""
if command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; then
    LINUX_ARM64_CC="aarch64-linux-gnu-gcc"
fi
build_target linux arm64 "${DIST_DIR}/agent-linux-arm64" "$LINUX_ARM64_CC" "${LINUX_ARM64_CC/gcc/g++}"

# Windows AMD64 — MinGW gcc 필요. thread model = posix 권장.
WIN_CC=""
if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
    THREAD_MODEL=$(x86_64-w64-mingw32-gcc -v 2>&1 | grep -oE 'Thread model: [a-z0-9]+' | awk '{print $NF}' || echo unknown)
    if [ "$THREAD_MODEL" != "posix" ]; then
        echo "  WARN: MinGW thread model = '$THREAD_MODEL' (posix 권장). 빌드 실패 시:"
        echo "        sudo update-alternatives --config x86_64-w64-mingw32-gcc"
    fi
    WIN_CC="x86_64-w64-mingw32-gcc"
fi
build_target windows amd64 "${DIST_DIR}/agent-windows-amd64.exe" \
    "$WIN_CC" "${WIN_CC/gcc/g++}" "-static -lpthread"

# ── iotest (Android arm64 전용) — pure Go, cgo 무관 ──
echo ""
echo "=== Building iotest (Android arm64) ==="
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "tools/iotest" ./cmd/iotest/ \
    && echo "  tools/iotest built ($(du -h tools/iotest | cut -f1))" \
    || echo "  ✗ iotest 빌드 실패" >&2

echo ""
echo "=== Build complete ==="
ls -lh "${DIST_DIR}/" 2>/dev/null
echo ""
if [ -f tools/iotest ]; then
    echo "=== Tools ==="
    ls -lh tools/iotest
fi
