# Code Graph: ally-agent — 功能导航

> 用于定位主要功能的文件与入口，可能过期，编辑前核实源码。模块新增、入口迁移或职责变化时更新；规范见 `AGENTS.md`，详细设计见 `docs/`。全文保持在 8 KiB 内。

## 功能 → 文件速查表

| 想找什么 | 去哪里 |
|---|---|
| 聊天循环、工具分发、运行取消 | `internal/app/app.go`：`StartChat` / `runChat` / `executeTool` |
| 模型协议、多 key、流式响应 | `internal/app/prov_model.go` + `internal/app/prov_adapter_*.go` |
| 思考与 Responses 原生回放、历史修复 | `internal/app/prov_reasoning.go` / `prov_responses_history.go` / `prov_history_hygiene.go` |
| 上下文核算、前缀冻结、压缩 | `internal/app/biz_context.go` / `biz_context_lock.go` / `biz_compact.go` / `biz_compact_fast.go`；`docs/agent-context.md` |
| 提示词、项目指令、代码地图、教训 | `internal/app/biz_prompt.go` + `biz_project_context.go`；内置地图技能 `internal/builtin_skills/skills/codegraph/SKILL.md` |
| 配置事务、模型身份、数据目录 | `internal/app/biz_config.go`：`commitConfig` / `AppDataDir`；`biz_config_state.go` |
| 会话存储、索引、启动加载 | `internal/app/biz_sessions.go` + `biz_lifecycle.go`；前端 `frontend/src/utils/startupTasks.mjs` |
| 工具阶段调度、写冲突、变更校验 | `internal/app/orch_tool_stages.go` / `orch_batch_policy.go` / `orch_validation.go` |
| 文件读写、版本校验、搜索 | `internal/app/orch_read.go` / `orch_edit.go` / `orch_grep.go` / `orch_file_ops.go` + `internal/tools/` 对应目录 |
| 命令与路径安全、沙箱接线 | `internal/app/orch_fence.go` / `orch_sandbox.go` + `internal/tools/pathutil/` / `internal/tools/command/` + `internal/sandbox/`；`docs/safety-fence.md` |
| 内置工具 schema、入参校验、调用 ID | `internal/tools/shared/builtins.go` + `internal/tools/schemautil/` / `internal/tools/toolcall/` |
| 子代理、计划任务、后台服务 | `internal/app/orch_subagent.go` / `orch_scheduler.go` / `orch_services.go`；计划工具在 `biz_context.go` |
| HTTP 请求、网页、截图 | `internal/app/orch_http.go` / `orch_screenshot.go`；截图子进程入口 `main_screenshot.go` |
| SSH 清单、授权、远端工具 | `internal/app/biz_ssh_cluster.go` / `orch_ssh_cluster.go` / `orch_remote*.go` / `remote_helper.py` + `internal/tools/sshclient/` |
| 技能、记忆、MCP | `internal/app/biz_skills.go` / `orch_memory.go` / `biz_mcp.go`；内置技能在 `internal/builtin_skills/skills/` |
| ChatGPT 登录与订阅模型 | `internal/chatgpt/` + `internal/app/biz_chatgpt.go` / `host_chatgpt.go` / `prov_chatgpt.go`；`docs/chatgpt-auth.md` |
| 插件安装、资源、页面 API | `internal/app/orch_plugin*.go` + `internal/tools/plugin/` + `frontend/src/utils/pluginHost.mjs`；`docs/plugin-system.md` |
| 桌面生命周期、窗口、事件出口 | `internal/app/host_desktop.go` / `host_events.go`；流式节流 `infra_stream.go` |
| 无头服务、本地 HTTP API | `main_server.go` + `internal/app/host_server.go` / `biz_server_options.go` / `biz_api.go`；`docs/headless.md` / `docs/api.md` |
| 自更新、完整性、原子替换 | `internal/app/biz_update.go` + `biz_update_guard.go` / `biz_update_integrity.go` / `biz_update_files.go` / `biz_update_validate.go` / `host_update_bundle_*.go` |
| 前端工作区、Tab、会话与事件路由 | `frontend/src/App.vue`：`repointTabSession` / `bindRuntimeEvents`；`frontend/src/utils/workspacePath.mjs` / `sessionStore.mjs` |
| 消息、工具卡、流式 Markdown | `frontend/src/components/ChatMessages.vue` / `ToolCallCard.vue` / `StreamingMarkdownBody.vue` + `frontend/src/composables/useToolEvents.mjs` |
| 工具状态、卡片数据、计划展示 | `frontend/src/utils/toolKind.mjs` / `toolEventState.mjs` / `toolPreview.mjs` / `planPanel.mjs` + `frontend/src/components/CardGrid.vue` |
| 输入栏统计、文件树、设置与管理页 | `frontend/src/components/ComposerInfoBar.vue` / `WorkspaceExplorer.vue` / `SettingsModal.vue`；技能、MCP、模型、SSH、插件各有 Panel 组件 |
| 主题、动效、国际化、本地历史 | `frontend/src/style.css` / `i18n.mjs`；`frontend/src/utils/theme.mjs` / `naiveTheme.mjs` / `promptHistoryStore.mjs`；动效在 `frontend/src/composables/` |
| 三协议实际请求字段、发送条件、思考/缓存/回放契约 | [docs/model-protocol-fields.md](docs/model-protocol-fields.md)（实现规则唯一文档入口）；供应商资料快照保留在 [docs/provider-api-fields.md](docs/provider-api-fields.md) |
| 缓存架构与订阅零命中排查 | [docs/agent-prompt-cache.md](docs/agent-prompt-cache.md)（冻结/回放/统计）+ [docs/chatgpt-cache-diagnostics.md](docs/chatgpt-cache-diagnostics.md)（实验条件/证据边界/诊断采集） |

## 核心调用流

- 桌面入口 `main.go` 装配 `App` 与 Wails；无头入口 `main_server.go` 复用业务生命周期与 API。
- 前端 `App.vue` → `StartChat` / `runChat`（`internal/app/app.go`）→ 消息与上下文（`biz_context.go`）、MCP 工具（`biz_mcp.go`）→ `streamModelResponse`（`prov_model.go`）→ 对应协议适配器。
- 模型工具调用 → 阶段调度（`orch_tool_stages.go`）→ `executeTool` → `orch_*` 编排与 `internal/tools/` 算法 → 工具结果回填，继续模型循环 → `saveHistory`（`biz_sessions.go`）。
- UI 事件经 `host_events.go` 的 `App.emit` 发出，`App.vue` 路由到会话与消息展示；流式节流在 `infra_stream.go`。
- 上下文压缩在 `biz_compact.go` / `biz_compact_fast.go`；前缀快照的冻结与刷新在 `biz_context.go`，缓存说明见 `docs/agent-prompt-cache.md`。

## 模块职责

- `internal/app/`：应用状态与业务编排。`prov_` 模型协议，`host_` 宿主桥，`orch_` 工具编排，`infra_` 共享设施，`biz_` 独立业务；边界以 `AGENTS.md` 为准。
- `internal/tools/`：工具算法与共享规则；按工具名分目录。`internal/sandbox/` 提供平台沙箱策略，接入状态查源码。
- `internal/chatgpt/`：OAuth、账号与凭据存储；`internal/builtin_skills/`：随二进制嵌入的技能内容。
- `frontend/src/App.vue`：工作台状态与交互路由；`frontend/src/components/`：页面与展示组件；`frontend/src/composables/`：共享行为；`frontend/src/utils/`：状态、协议、格式与存储规则。
- `frontend/src/games/`：棋盘玩法与模型走法解析；模型桥接在 `internal/app/biz_game_ai.go`。知识库模式入口在 `internal/app/orch_kb.go` 与 `App.vue`。
- `frontend/src/data/modelCatalog.json`：生成的模型目录，来源 `scripts/generate-model-catalog.mjs`；`frontend/bindings/`：Wails 生成绑定。
- `Taskfile.yml` / `build/` / `.github/workflows/build.yml`：构建、打包、发布；前端任务见 `frontend/package.json`，验证要求见 `AGENTS.md`。
- `docs/agent-architecture.md`：架构说明；`docs/agent-core-loop.md`：运行循环；`docs/agent-tools.md`：工具机制；`docs/update-security-review.md`：更新安全审查。
