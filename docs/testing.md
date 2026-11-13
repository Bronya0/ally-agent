# 全量测试

运行 `python scripts/test.py`，对 `./...` 的全部包运行测试。脚本设置 `CGO_ENABLED=0`、`-count=1`，保留 Go 构建缓存，不复用测试结果缓存。

不再拆分测试包或使用额外启动器。`-p 1` 限制包的构建和测试并发，默认 `GOMAXPROCS=2`、`-parallel 2`。需要更低占用时运行 `python scripts/test.py --jobs 1`；`--jobs` 同时控制 CPU 和包内测试并行度。未显式设置 `GOMEMLIMIT` 时使用 `512MiB`，这是单进程 Go 运行时的软内存目标，不是整个命令的硬内存上限。

测试直接交给 Go 枚举、执行和汇总，包含子测试、Example 和 Fuzz 种子；失败返回非零退出码。TestMain 和 t.TempDir 继续负责环境与临时文件隔离。最后一行报告包含构建和链接的总耗时。

日常改动只执行必要的对应检查，不默认运行全套。MyGo UI 检查为 `go tool mygo vet .`，不要与全套测试并行运行。较重的性能和资源管理器检查分别位于 `tests/performance/run.py` 和 `tests/explorer/run.py`，按需单独执行。
