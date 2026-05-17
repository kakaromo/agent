#!/bin/bash
# UI 빌드 — Go 빌드 전에 실행해야 //go:embed all:ui/build 가 산출물을 임베드한다.
# 누락 시 build/.gitkeep 만 임베드되어 SPA 가 동작하지 않으므로 명시적으로 실패시킨다.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/ui"

if [ ! -d node_modules ]; then
	echo "=== ui/ npm install ==="
	npm ci --no-audit --no-fund
fi

echo "=== ui/ build ==="
npm run build

if [ ! -f build/index.html ]; then
	echo "ERROR: ui/build/index.html not generated" >&2
	exit 1
fi
echo "  ui build OK ($(du -sh build | cut -f1))"
