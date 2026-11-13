#!/usr/bin/env python3
"""按需运行的界面检查：文件树和侧栏的规模与键盘交互。

不进入 `go test ./...`，也不进应用构建。测试源码在 tests/explorer/desktop/，
由 overlay 挂到 desktop 包里运行，仓库里的 desktop/ 不会被写入。
"""

import json
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SUITE = Path(__file__).resolve().parent
TAG = "ally_explorer"


def overlay() -> dict[str, str]:
    mapping: dict[str, str] = {}
    for source in sorted((SUITE / "desktop").glob("*_test.go")):
        target = ROOT / "desktop" / f"zz_ally_explorer_{source.stem}_test.go"
        if target.exists():
            raise SystemExit(f"overlay 目标已存在，拒绝遮盖: {target}")
        mapping[str(target)] = str(source)
    return mapping


def main() -> int:
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as file:
        json.dump({"Replace": overlay()}, file)
        overlay_path = file.name
    run = sys.argv[1] if len(sys.argv) > 1 else "TestOnDemand"
    try:
        return subprocess.call(
            ["go", "test", "-overlay", overlay_path, "-tags", TAG, "-count=1", "-v", "-run", run, "./desktop"],
            cwd=ROOT,
        )
    finally:
        Path(overlay_path).unlink()


if __name__ == "__main__":
    raise SystemExit(main())
