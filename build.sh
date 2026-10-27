#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
output="${1:-dist/ally-native-v2.exe}"
mkdir -p "$(dirname "$output")"
GOMAXPROCS=2 GOGC=30 CGO_ENABLED=0 go build -p 1 -trimpath -tags mygo_noinspector -ldflags '-H windowsgui -s -w' -o "$output" .
printf 'Built: %s\n' "$output"
