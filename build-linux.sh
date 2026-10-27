#!/usr/bin/env bash
# Build Ally Native for Linux (amd64, plain binary, no packaging).
# Output: dist/ally-native-linux-amd64.
# Cross-compiles cleanly: pure Go, no cgo (Skill mygo-api §25.2).
set -euo pipefail
cd "$(dirname "$0")"
if [[ $# -ne 0 ]]; then
	printf 'Usage: bash build-linux.sh\n' >&2
	exit 2
fi
mkdir -p dist
output="dist/ally-native-linux-amd64"
GOMAXPROCS=2 GOGC=30 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -p 1 -trimpath -tags mygo_noinspector -ldflags '-s -w' -o "$output" .
printf 'Built: %s\n' "$output"
