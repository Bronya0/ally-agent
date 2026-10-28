#!/usr/bin/env bash
# Shared by all local and CI entry points. Works from any current directory.
set -euo pipefail
ALLY_BUILD_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
run_build() {
    local target="$1"
    shift
    if [[ $# -ne 0 ]]; then
        printf 'This build entry point takes no arguments.
' >&2
        return 2
    fi
    local python_cmd
    for python_cmd in python3 python; do
        if "$python_cmd" -c 'import sys; assert sys.version_info >= (3, 9)' >/dev/null 2>&1; then
            "$python_cmd" "$ALLY_BUILD_ROOT/scripts/build.py" "$target"
            return
        fi
    done
    printf 'Python 3.9+ is required for packaging.
' >&2
    return 1
}
