#!/usr/bin/env python3
"""Run the single complete performance and retained-heap suite."""
from __future__ import annotations

import json
import os
from pathlib import Path
import re
import signal
import statistics
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[2]
SUITE = Path(__file__).resolve().parent
ITERATIONS = 30
REPEATS = 3
CPU = 4
TIMEOUT = 300
BENCH_LINE = re.compile(r"^(BenchmarkPerformance\S+)\s+(\d+)\s+(.+)$")
NUMBER_UNIT = re.compile(r"(-?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)\s+([A-Za-z0-9/_-]+)")


def parse_go_output(text: str) -> dict:
    output, tests, metrics, benchmarks = [], {}, [], {}
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            output.append(line + "\n")
            continue
        if not isinstance(event, dict):
            continue
        if isinstance(event.get("Output"), str):
            output.append(event["Output"])
        if event.get("Test") and event.get("Action") in {"pass", "fail", "skip"}:
            name = event["Test"]
            if tests.get(name) != "fail":
                tests[name] = event["Action"]
    combined = "".join(output)
    for line in combined.splitlines():
        if "PERF_METRIC " in line:
            metric = json.loads(line.split("PERF_METRIC ", 1)[1])
            if not isinstance(metric, dict) or not isinstance(metric.get("scenario"), str):
                raise ValueError("invalid PERF_METRIC record")
            metrics.append(metric)
        match = BENCH_LINE.match(line)
        if match:
            name = re.sub(r"-\d+$", "", match[1])
            sample = {unit: float(number) for number, unit in NUMBER_UNIT.findall(match[3])}
            if not {"ns/op", "B/op", "allocs/op"} <= sample.keys():
                raise ValueError(f"incomplete benchmark sample: {name}")
            sample["iterations"] = int(match[2])
            benchmarks.setdefault(name, []).append(sample)
    summary = {}
    for name, samples in benchmarks.items():
        units = set().union(*(s.keys() for s in samples)) - {"iterations"}
        summary[name] = {
            "samples": samples,
            "median": {u: statistics.median(s[u] for s in samples if u in s) for u in sorted(units)},
            "min_ns": min(s["ns/op"] for s in samples),
            "max_ns": max(s["ns/op"] for s in samples),
        }
    return {"tests": tests, "metrics": metrics, "benchmarks": summary, "output": combined}


def fixture_overlay() -> dict[str, str]:
    mapping = {}
    for source in sorted((SUITE / "desktop").glob("*_test.go")):
        stem = source.stem.removesuffix("_test")
        target = ROOT / "desktop" / f"zz_ally_performance_{stem}_test.go"
        if target.exists():
            raise ValueError(f"overlay 目标已存在，拒绝遮盖: {target}")
        mapping[str(target)] = str(source)
    if not mapping:
        raise ValueError("没有发现性能测试 fixture")
    return mapping


def write_json(path: Path, value):
    with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent, prefix=".json-", delete=False) as stream:
        temporary = Path(stream.name)
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    try:
        os.replace(temporary, path)
    finally:
        if temporary.exists():
            temporary.unlink()


def terminate_owned_process(process: subprocess.Popen):
    if process.poll() is not None:
        return
    if os.name == "nt":
        subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
    else:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    process.wait()


def execute(command: list[str], log: Path, env: dict, timeout: int) -> tuple[int, float]:
    started = time.monotonic()
    with log.open("w", encoding="utf-8", newline="\n") as output:
        process = subprocess.Popen(command, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT,
                                   start_new_session=os.name != "nt")
        try:
            code = process.wait(timeout=timeout)
        except (subprocess.TimeoutExpired, KeyboardInterrupt):
            terminate_owned_process(process)
            raise
    return code, time.monotonic() - started


def format_time(ns):
    if ns < 1000: return f"{ns:.2f} ns"
    if ns < 1_000_000: return f"{ns/1000:.2f} μs"
    return f"{ns/1_000_000:.2f} ms"


def report_text(result: dict) -> str:
    top = {name: state for name, state in result["tests"].items() if name.startswith("TestPerformance") and "/" not in name}
    env = result["environment"]
    lines = ["# 完整性能与内存测试报告", "", f"- 状态：**{result['status']}**",
             f"- 运行时间：{result['started_at']}", f"- Git HEAD：`{result['git_head']}`；dirty={result['dirty']}",
             f"- Go：{env.get('GOVERSION', '?')}；{env.get('GOOS', '?')}/{env.get('GOARCH', '?')}；GOMAXPROCS={CPU}",
             f"- MyGo：{env.get('mygo', '?')}；CPU：{env.get('cpu_model', '未采集')}",
             f"- 固定执行全部场景；基准 {ITERATIONS} 次 × {REPEATS} 轮，取中位数。",
             f"- 压力测试：通过 {list(top.values()).count('pass')}，失败 {list(top.values()).count('fail')}，跳过 {list(top.values()).count('skip')}。", ""]
    for error in result["errors"]: lines.append(f"> 错误：{error}")
    lines += ["", "## 性能基准", "", "| 场景 | 中位耗时 | B/op | allocs/op | 样本数 |", "| --- | ---: | ---: | ---: | ---: |"]
    for name, value in sorted(result["benchmarks"].items()):
        m = value["median"]
        unit = "ns/lookup" if "ns/lookup" in m else "ns/op"
        suffix = " / lookup" if unit == "ns/lookup" else ""
        lines.append(f"| `{name}` | {format_time(m[unit])}{suffix} | {m['B/op']:,.0f} | {m['allocs/op']:,.0f} | {len(value['samples'])} |")
    lines += ["", "## GC 后存活内存及行为指标", ""]
    for metric in result["metrics"]:
        parts = []
        for key, value in metric.items():
            if key == "scenario": continue
            if key.endswith("bytes") and isinstance(value, (int, float)):
                parts.append(f"{key}={value/1048576:.3f} MiB")
            else:
                parts.append(f"{key}={value}")
        lines.append(f"- **{metric['scenario']}**：" + "；".join(parts))
    lines += ["", "## 测量边界", "", "- 仅使用合成数据，不调用真实保存、模型、MCP、SSH 或网络。",
              "- HOME/USERPROFILE 在测试子进程隔离；中间文件清理，报告和日志保留。",
              "- UI 基准包含离屏构建、布局和软件绘制，不等于实际 GPU 帧时间。",
              "- 存活堆在 fixture 保持存活时经过两次 GC 测量，累计分配不能当成泄漏。",
              "- 快照排队是合成保留场景，不代表实际保存必然积压同样数量。",
              "- 耗时只记录，不使用依赖机器速度的硬断言。", "", "原始证据见同目录 `stress.jsonl`、`bench.jsonl`、`results.json`。", ""]
    return "\n".join(lines)


def main(argv=None) -> int:
    if argv is None:
        argv = sys.argv[1:]
    if argv:
        print("只保留一套完整测试，无需参数：python tests/performance/run.py", file=sys.stderr)
        return 2
    artifacts = ROOT / ".tmp" / "performance"
    artifacts.mkdir(parents=True, exist_ok=True)
    run_dir = Path(tempfile.mkdtemp(prefix=datetime.now().strftime("%Y%m%d-%H%M%S-"), dir=artifacts))
    result = {"schema_version": 2, "status": "running", "started_at": datetime.now(timezone.utc).isoformat(),
              "git_head": "unknown", "dirty": False, "environment": {},
              "settings": {"iterations": ITERATIONS, "repeats": REPEATS, "cpu": CPU, "timeout": TIMEOUT},
              "tests": {}, "benchmarks": {}, "metrics": [], "commands": [], "errors": []}
    code = 0
    try:
        result["git_head"] = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
        result["dirty"] = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT))
        result["environment"] = json.loads(subprocess.check_output(["go", "env", "-json", "GOOS", "GOARCH", "GOVERSION"], cwd=ROOT))
        result["environment"]["mygo"] = subprocess.check_output(["go", "list", "-m", "-f", "{{.Version}}", "github.com/egoist/mygo"], cwd=ROOT, text=True).strip()
        caches = json.loads(subprocess.check_output(["go", "env", "-json", "GOPATH", "GOMODCACHE", "GOCACHE"], cwd=ROOT))
        with tempfile.TemporaryDirectory(prefix="scratch-", dir=run_dir) as scratch_name:
            scratch = Path(scratch_name)
            home = scratch / "isolated-home"
            home.mkdir()
            env = os.environ.copy()
            # Resolve Go caches before HOME isolation; never serialize their paths.
            env.update(caches)
            env.update({"CGO_ENABLED": "0", "HOME": str(home), "USERPROFILE": str(home)})
            env.pop("ALLY_DATA_DIR", None)
            env.pop("ALLY_PERF_CONFIG", None)
            overlay = scratch / "overlay.json"
            write_json(overlay, {"Replace": fixture_overlay()})
            for label, run_pattern, bench_pattern, repeat in (
                ("stress", "^TestPerformance", "^$", 1),
                ("bench", "^$", "^BenchmarkPerformance", REPEATS),
            ):
                log = run_dir / f"{label}.jsonl"
                binary = scratch / (label + (".test.exe" if os.name == "nt" else ".test"))
                command = ["go", "test", "-json", "-overlay", str(overlay), "-tags=ally_performance", "-p", "1", "./desktop",
                           "-run", run_pattern, "-bench", bench_pattern, "-benchmem", f"-benchtime={ITERATIONS}x", f"-count={repeat}", f"-cpu={CPU}", f"-timeout={TIMEOUT}s", "-o", str(binary)]
                print(f"[{label}] 开始；日志：{log.relative_to(ROOT)}", flush=True)
                exit_code, seconds = execute(command, log, env, TIMEOUT + 30)
                result["commands"].append({"stage": label, "exit_code": exit_code, "seconds": seconds, "log": log.name})
                parsed = parse_go_output(log.read_text(encoding="utf-8", errors="replace"))
                result["tests"].update(parsed["tests"])
                result["benchmarks"].update(parsed["benchmarks"])
                result["metrics"].extend(parsed["metrics"])
                for line in parsed["output"].splitlines():
                    if line.startswith("cpu: "):
                        result["environment"]["cpu_model"] = line.removeprefix("cpu: ")
                if exit_code:
                    raise RuntimeError(f"{label} 失败（exit={exit_code}），请查看 {log.name}")
                if label == "stress" and not any(name.startswith("TestPerformance") for name in parsed["tests"]):
                    raise RuntimeError("未执行压力测试，拒绝报告成功")
                if label == "bench" and (not parsed["benchmarks"] or any(len(b["samples"]) != REPEATS for b in parsed["benchmarks"].values())):
                    raise RuntimeError("基准未执行或样本不完整，拒绝报告成功")
                print(f"[{label}] 完成，{seconds:.1f}s", flush=True)
        result["status"] = "passed"
    except KeyboardInterrupt:
        result["status"], code = "cancelled", 130
        result["errors"].append("用户取消；测试进程树已停止")
    except Exception as exc:
        result["status"], code = "failed", 1
        result["errors"].append(str(exc))
    write_json(run_dir / "results.json", result)
    (run_dir / "report.md").write_text(report_text(result), encoding="utf-8")
    write_json(artifacts / "latest.json", {"status": result["status"], "results": str((run_dir / "results.json").relative_to(ROOT)), "report": str((run_dir / "report.md").relative_to(ROOT))})
    print(f"状态：{result['status']}；报告：{(run_dir / 'report.md').relative_to(ROOT)}", flush=True)
    for error in result["errors"]: print(error, file=sys.stderr)
    return code


if __name__ == "__main__":
    raise SystemExit(main())
