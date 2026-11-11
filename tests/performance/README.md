# 完整性能与内存测试套件

**只保留一套完整测试，一个命令：**

```bash
python tests/performance/run.py
```

固定执行全部压力测试、存活内存测量和性能基准，无快速版、模块筛选、Git 对照或 profile 选项。基准固定 30 次 × 3 轮，取中位数；GOMAXPROCS=4，每阶段超时 300 秒。

## 覆盖范围

- 1 千、1 万、10 万条合成消息下的历史索引、分页、工具追加和搜索。
- 子消息先到、父卡后到、旧记录修改、删除后补回、同数量替换及压缩。
- 连续翻 160 页的 Markdown、代码搜索缓存、导航 Handle 复用和压缩后释放。
- MCP/read/remote_read/command/create/remote_create_file 的大结果保留：每种最多 128 个约 1 MiB 独立结果。
- 错误、取消、耗时和后台证据隔离；create 预览字节上限与 UTF-8 完整性。
- 完整快照的耗时、分配、深拷贝隔离，以及多份快照保留和释放。
- 12 次会话生命周期后存活堆、对象数量和累计分配。
- 巨大未闭合代码块的流式更新、稳定页渲染和事件投影。

## 文件与结果

测试源码保留在 `tests/performance/desktop/`，执行器为 `run.py`；`test_runner.py` 只验证执行器自身的结果解析和失败处理，不是另一套性能测试。

每次运行生成一个独立的 `.tmp/performance/日期时间-随机后缀/` 目录，统一保存：

- `report.md`：完整报告。
- `results.json`：环境、设置、原始样本、中位数和内存指标。
- `stress.jsonl`、`bench.jsonl`：原始 Go 测试日志。

`.tmp/performance/latest.json` 指向最近完成的报告。overlay、隔离 HOME 和测试可执行文件自动清理，报告和测试源码保留。

## 边界与判定

- 仅使用合成数据，不读取真实会话，不调用模型、MCP、SSH、网络或真实保存，不修改用户数据。
- HOME/USERPROFILE 只在测试子进程隔离，保留原 Go 缓存路径，不设置全局 `ALLY_DATA_DIR`。
- 普通 `go test ./...` 和应用构建不包含这些压力测试。不要直接用 `go test -tags ally_performance ./...`；脚本通过 overlay 调用桌面层内部实现。
- 正确性、缓存越界、明显的大结果保留、UTF-8 损坏、执行失败或基准样本不完整：退出非零，报告失败。超时/取消会停止测试进程树，不能把未执行的场景算作通过。
- 耗时、B/op、allocs/op 和存活堆都记录在报告中，不把机器相关的耗时写成硬断言。
- 存活堆在 fixture 保持存活时经过两次 GC 测量；累计分配不能当成泄漏。快照队列是模拟保留场景，不代表实际保存必然积压同样数量。
- UI 测量是离屏构建、布局和软件绘制，不代表实际 GPU 帧时间；本套件不覆盖 GPU/WebView、真实模型上下文或进程工作集。

## 后续维护

新增功能直接往现有 `stress_test.go` / `bench_test.go` 添加场景，不新增另一套入口或运行模式。保留 `ally_performance` build tag，测试名以 `TestPerformance`、基准名以 `BenchmarkPerformance` 开头。合成数据及固定规模集中在 `fixtures_test.go`。

基准输入和预热放在计时前；工具 ID 索引先初始化，流式输入使用 Builder。测存活堆时明确 `runtime.KeepAlive()`，避免因 fixture 被提前释放测出虚假的低内存。

要求 Python 3.10+ 和项目要求的 Go/MyGo 依赖；无需额外 Python 包。以后直接说“执行性能和内存测试套件”，就运行上面的唯一命令。
