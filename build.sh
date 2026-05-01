#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
DIST_DIR="dist"
mkdir -p "$DIST_DIR"

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
echo "Building Windows AMD64..."
if command -v x86_64-w64-mingw32-gcc &>/dev/null; then
    CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 go build -o "${DIST_DIR}/agent-windows-amd64.exe" .
else
    echo "  MinGW not found, building without CGO (DuckDB disabled)..."
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o "${DIST_DIR}/agent-windows-amd64.exe" .
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
