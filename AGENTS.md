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
