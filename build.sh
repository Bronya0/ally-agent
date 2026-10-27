#!/usr/bin/env bash
# Cross-compile helper for CI (macOS/Linux runners) and Git Bash.
# Native Windows builds use build.bat instead.
#   macOS:   bash build-macos.sh      -> dist/ally-native-darwin-{arm64,amd64}
#   Linux:   bash build-linux.sh      -> dist/ally-native-linux-amd64
# Plain `go build` only: no packaging, no signing (see Skill mygo-api §25).
set -euo pipefail
cd "$(dirname "$0")"
if [[ $# -ne 0 ]]; then
	printf 'Usage: bash build.sh (output: dist/ally-native.exe)\n' >&2
	exit 2
fi
output="dist/ally-native.exe"
mkdir -p "$(dirname "$output")"
GOMAXPROCS=2 GOGC=30 CGO_ENABLED=0 go build -p 1 -trimpath -tags mygo_noinspector -ldflags '-H windowsgui -s -w' -o "$output" .
printf 'Built: %s\n' "$output"
