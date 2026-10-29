#!/bin/bash
# CI release entry: run on macOS; the Python builder owns all platform logic.
set -euo pipefail
cd "$(dirname "$0")/.."
exec python3 scripts/build.py release
