#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "=== Building iotest ==="

# Android arm64 (기본 타겟)
echo "Building Android arm64..."
GOOS=linux GOARCH=arm64 go build -o "tools/iotest" ./cmd/iotest/
echo "  tools/iotest ($(du -h tools/iotest | cut -f1))"

# 로컬 테스트용 (현재 OS/ARCH)
if [[ "$1" == "--local" ]]; then
    echo "Building local ($(go env GOOS)/$(go env GOARCH))..."
    go build -o "tools/iotest-local" ./cmd/iotest/
    echo "  tools/iotest-local ($(du -h tools/iotest-local | cut -f1))"
fi

echo ""
echo "=== Done ==="
echo "Android 디바이스에 배포: adb push tools/iotest /data/local/tmp/iotest"
