#!/usr/bin/env bash
# Build Ally Native for macOS (arm64 + amd64, plain binaries, no signing).
# Output: dist/ally-native-darwin-arm64, dist/ally-native-darwin-amd64.
# Cross-compiles cleanly: pure Go, no cgo (Skill mygo-api §25.2).
set -euo pipefail
cd "$(dirname "$0")"
if [[ $# -ne 0 ]]; then
	printf 'Usage: bash build-macos.sh\n' >&2
	exit 2
fi
mkdir -p dist
for arch in arm64 amd64; do
	output="dist/ally-native-darwin-${arch}"
	GOMAXPROCS=2 GOGC=30 CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
		go build -p 1 -trimpath -tags mygo_noinspector -ldflags '-s -w' -o "$output" .
	printf 'Built: %s\n' "$output"
done
