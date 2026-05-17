#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

CONFIG="${1:-config/devices.toml}"
BINARY="./agent"

echo "=== Building UI ==="
./build-ui.sh

echo "=== Building agent ==="
go build -o "$BINARY" .

echo "=== Starting agent (config: $CONFIG) ==="
exec "$BINARY" -config "$CONFIG"
