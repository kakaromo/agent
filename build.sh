#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
DIST_DIR="dist"
mkdir -p "$DIST_DIR"

echo "=== Building UI (standalone embed) ==="
./build-ui.sh

echo "=== Building agent v${VERSION} ==="

# macOS ARM64
echo "Building macOS ARM64..."
GOOS=darwin GOARCH=arm64 go build -o "${DIST_DIR}/agent-darwin-arm64" .

# macOS AMD64
echo "Building macOS AMD64..."
GOOS=darwin GOARCH=amd64 go build -o "${DIST_DIR}/agent-darwin-amd64" .

# Linux AMD64
echo "Building Linux AMD64..."
GOOS=linux GOARCH=amd64 go build -o "${DIST_DIR}/agent-linux-amd64" .

# Linux ARM64
echo "Building Linux ARM64..."
GOOS=linux GOARCH=arm64 go build -o "${DIST_DIR}/agent-linux-arm64" .

# Windows AMD64
# go-duckdb 는 cgo 필수 (cgo 없으면 `undefined: Conn` 컴파일 에러).
# MinGW gcc 가 없으면 그냥 skip — Windows exe 만 빠진다.
# pthread_mutex_* undefined → MinGW 가 win32 스레드 모델이거나 winpthread 미링크.
# update-alternatives 로 posix 모델 선택 + CGO_LDFLAGS 로 명시적 -lpthread 필요.
echo "Building Windows AMD64..."
if command -v x86_64-w64-mingw32-gcc &>/dev/null; then
    # MinGW threading model 확인 — posix 가 아니면 pthread 심볼 못 찾음
    THREAD_MODEL=$(x86_64-w64-mingw32-gcc -v 2>&1 | grep -oE 'Thread model: [a-z0-9]+' | awk '{print $NF}' || echo unknown)
    if [ "$THREAD_MODEL" != "posix" ]; then
        echo "  WARN: MinGW thread model = '$THREAD_MODEL' (posix 권장)"
        echo "        sudo update-alternatives --config x86_64-w64-mingw32-gcc 로 posix 선택"
    fi
    CGO_ENABLED=1 \
        CC=x86_64-w64-mingw32-gcc \
        CXX=x86_64-w64-mingw32-g++ \
        CGO_LDFLAGS="-static -lpthread" \
        GOOS=windows GOARCH=amd64 \
        go build -o "${DIST_DIR}/agent-windows-amd64.exe" .
else
    echo "  MinGW (x86_64-w64-mingw32-gcc) 미발견 — Windows exe 빌드 skip"
    echo "  Ubuntu: sudo apt install mingw-w64 && sudo update-alternatives --config x86_64-w64-mingw32-gcc (posix 선택)"
    echo "  macOS:  brew install mingw-w64"
fi

# ── iotest (Android arm64 전용) ──
echo ""
echo "=== Building iotest (Android arm64) ==="
GOOS=linux GOARCH=arm64 go build -o "tools/iotest" ./cmd/iotest/
echo "  tools/iotest built ($(du -h tools/iotest | cut -f1))"

echo ""
echo "=== Build complete ==="
ls -lh "${DIST_DIR}/"
echo ""
echo "=== Tools ==="
ls -lh tools/iotest
