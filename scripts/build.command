#!/bin/bash
# Double-click in Finder, or run from Terminal. No DMG for development builds.
set -u
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:$PATH"
cd "$(dirname "$0")/.." || exit 1
status=0
if ! command -v python3 >/dev/null 2>&1; then
    printf 'Python 3.9+ is required. Install Python and retry.\n' >&2
    status=1
else
    python3 scripts/build.py dev || status=$?
fi
if [[ -t 0 ]]; then
    printf '\nPress Enter to close...'
    read -r _ || true
fi
exit "$status"
