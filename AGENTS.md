# Ally — AGENTS.md

本文档仅记录 **开发 Ally 本身时不可违反的架构边界、开发规范与高危避坑铁律**。
日常功能代码位置查阅 `CODEGRAPH.md`，标准 Go/Vue 惯用法与工具参数无需在此赘述。

---

## 1. 构建与验证 (Build & Test)

| 命令 | 用途 |
|---|---|
| `wails3 dev` | 启动桌面端热重载开发模式 |
| `wails3 build` | 构建并校验桌面端二进制（修改 Go 或 Wails 绑定的唯一验证命令） |
| `go test ./...` | 执行全套后端测试（测试前必须确保满足隔离规则） |
| `gofmt -l .` | 格式化自查，期望输出为空（2026-09-15 已统一 25 个文件，此前长期不全绿） |

## 2. Git 提交规范 (Git Convention)

Ally 本身开发提交时，严格按如下命令临时指定作者身份：

```bash
git add -A
git -c user.name="ally agent" -c user.email="ally@agent.dev" commit -m "..."
git push origin main
```

## 3. 分层架构与唯一边界 (Architectural Boundaries)

修改或添加功能时必须遵守各层职责边界，严禁跨层泄露：

1. **宿主隔离 (Host Neutrality)**:
   - Agent 核心逻辑保持宿主中立。`main.go` 与 `internal/app/host_*.go` 是**唯一**允许 import Wails v3 runtime 的代码；`app.go`、`orch_*`、`biz_*` 严禁直接调用 Wails 窗口、事件、对话框或原生 API。
   - UI 运行时事件统一经 `App.emit()`（`host_events.go`）抛出。
2. **模型适配边界 (Provider Boundary)**:
   - `internal/app/prov_model.go` 是 Provider 协议适配（OpenAI/Anthropic/Responses wire 结构）的**唯一**边界，不得把 provider 特有数据结构外泄至 `app.go` 或工具编排层。
3. **工具分层 (Tools Pure vs Orch)**:
   - `internal/tools/<name>/`: 纯算法层，**绝对禁止**依赖 `*App`、`ConfigState` 或任何 `internal/app` 符号。
   - `internal/app/orch_<name>.go`: 工具编排层，负责绑定纯算法到 `*App` 状态（路径校验、工作区上下文、并发锁、审计日志）。
4. **文件命名约定**:
   - `prov_`: 模型协议适配
   - `host_`: Wails/窗口/桌面桥
   - `orch_`: 工具编排（对应 `internal/tools/`）
   - `infra_`: 跨编排共享（流式节流、结果信封、环境）
   - `biz_`: 独立业务（配置、会话、上下文、提示词、技能、MCP、自更新）

## 4. 关键避坑铁律 (Critical Gotchas)

以下规则触犯会导致死锁、数据损坏或崩溃：

1. **`App` 结构体锁与并发安全**:
   - 新增 `map` 或引用类型字段时，**必须**在 `NewApp()` 初始化，且在持有 `a.mu` 写入前做 nil 懒加载兜底。持有锁期间写入未初始化的 nil map 会引发不可恢复的 panic，导致互斥锁永久无法释放，造成全局死锁。
2. **多 Tab 与工作区路由**:
   - 工作区解析必须调用 `tabOwnsWorkspace(tab)` / `runWorkspaceForTab(tab)`，禁止将 Tab 路径与 `config.workspace` 盲目比对。
   - 知识库 Tab（`kind:kb`）和临时 Tab（`kind:temp`）绝不向 `config.workspace` 写回持久化路径。
3. **前端 WebView2 交互区**:
   - `.app-header` 声明了 `--wails-draggable: drag`；其内部所有可交互元素（Tab 标签、按钮、输入框、下拉菜单）必须显式标记 `--wails-draggable: no-drag`，否则点击/拖拽手势会被 WebView2 窗口拖拽劫持。
4. **单测安全隔离**:
   - 单元测试**绝对禁止**读写或删除真实磁盘路径（`~`、`~/.ally_agent`、源码树、系统目录）。所有文件系统操作必须在 `t.TempDir()` 沙箱内完成，读取用户主目录的逻辑需通过 `t.Setenv("HOME", t.TempDir())` 隔离。
5. **本地编辑契约 (Edit Protocol)**:
   - 模型侧修改文件必须先 `read` 获取 `version` token，再调用 `edit`（单次调用修改单一文件；跨文件修改在同轮返回中并行调用 `edit`，由后端执行原子校验与批次写入）。
6. **本地文本缓存必须结构性有界**:
   - 输入历史一类本地文本缓存只有两种合法形态：结构性有界（每桶条数上限 + 单条长度上限 + 全库字符预算 + 工作区移除时显式弃桶，收口在 `frontend/src/utils/promptHistoryStore.mjs`）或明说重启即丢。只做一半 = 配额慢性泄露或丢用户数据。

## 5. 模型协议铁律 (Model Protocol Rules)

对接 provider 的约定，违反即静默降级或线上 400：

1. **思考档位**:
   - “关闭思考”（`off`）是独立档位，不得归一到 `auto`：auto = 交给供应商决定（Ally 侧按“在思考”处理，只是不发等级），off = 明确要求别思考。
   - 同一档位在不同协议上拼写不同，映射**必须收口在一处**：Chat/Responses 走 `reasoningWireForAdapter`（`reasoningWirePlan`），Anthropic 走 `configureAnthropicThinking`；禁止适配器各自拼写。UI 下拉只产 `reasoningEffortLevels` 里的规范值，别名容错只归后端。
2. **回填判据是端点，不是模型名单**:
   - 思考字段（`reasoning_content` 等）按“每条助手消息”校验（DeepSeek/Kimi）：除 OpenAI 官方端点外一律回填显式空串（`chatReasoningBackfillKey` + `chatRequestRewriteTransport`）；除官方端点之外还有一处例外——“关闭思考”档位（请求本身要求对方别思考，无内容可回传）。
   - 供应商校验的是字段**存在性**，而 `omitempty` 会丢空串键，所以只能请求侧改写；改写必须确定且幂等，否则破坏 prompt/KV 前缀缓存。
3. **内部 LLM 调用不得改写用户的思考档位**:
   - 压缩总结等内部调用原样传 `cfg`。写死档位会与设置静默漂移，且强推“关闭思考”会让必思考模型直接 400。
4. **瞬态注入只放最新用户消息之前**:
   - 计划快照只在本次 run 的首个请求、于最新用户消息**之前**附带一次，永不落盘；没有未完成计划就不注入。插入位置稳定才不破坏供应商前缀缓存。
5. **工具调用身份以 id 为准，index 只做定位**:
   - 流式 tool_calls 归并：带 id 的碎片未命中已有调用即新调用（同一调用不会换 id）；不带 id 时才用 index 定位续片、并用名字兜底判断是否另一个调用。**禁止把 index 当身份**——中转常把一个 index 复用给多个调用，index 优先会把两个调用焊成一个（名字与参数一起拼坏）。id 唯一性由 `toolcall.CallIDFoundry` 集中铸造（重复变 `id__2`、缺失则铸造、按对话历史播种）；Responses 侧同理：item id 是身份，output_index 只定位，命中 index 时不得回写 item 表。
