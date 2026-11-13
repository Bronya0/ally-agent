#!/usr/bin/env python3
"""Run all Go tests with a single package process and bounded parallelism."""
import argparse
import os
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--jobs", type=int, default=2,
                        help="CPU and in-package test parallelism (default: 2)")
    args = parser.parse_args()
    if args.jobs < 1:
        parser.error("--jobs must be at least 1")
    env = dict(os.environ, CGO_ENABLED="0", GOMAXPROCS=str(args.jobs))
    env.setdefault("GOMEMLIMIT", "512MiB")
    started = time.perf_counter()
    result = subprocess.call(
        ["go", "test", "-p", "1", "-parallel", str(args.jobs), "-count=1",
         "-timeout=60s", "./..."], cwd=ROOT, env=env,
    )
    print(f"Full Go tests: {time.perf_counter() - started:.2f}s", flush=True)
    return result


if __name__ == "__main__":
    raise SystemExit(main())
