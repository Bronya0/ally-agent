# Code Graph: ally-agent — 文件级功能速查

> 一行一模块/文件的功能索引，供 AI 快速定位"某个功能在哪个文件"。规范类内容见 `AGENTS.md`。

## 功能 → 文件速查表

| 想找什么 | 去哪里 |
|---|---|
| 聊天循环、工具分发、运行/会话编排 | `internal/app/app.go`（runChat / executeTool / chatTools） |
| 模型流式适配、多 key 故障切换 | `internal/app/prov_model.go`（调度/多 key）+ `prov_adapter_*.go`（三协议适配器） |
| 代理检测 / 代理 HTTP 客户端 | `internal/app/prov_proxy*.go` |
| 事件出口（后端 emit） | `internal/app/host_events.go`；前端路由在 `App.vue` `bindRuntimeEvents()` |
| 桌面生命周期、窗口几何、系统对话框 | `internal/app/host_desktop.go` / `host_window_state.go` |
| 内置工具 schema 定义 | `internal/tools/shared/builtins.go` |
| 某工具的执行逻辑 | `internal/app/orch_<name>.go`（编排）+ `internal/tools/<name>/`（纯算法） |
| edit 读写契约 / 原子写 / 冲突合并 | `orch_edit_plan.go` → `orch_edit.go` → `tools/edit`、`tools/read` |
| 命令安全拦截与路径校验 | `orch_command_safety.go` + `internal/tools/command` |
| 文件基础读写与删除防护 | `internal/app/orch_file_ops.go` |
| 受保护路径判定（VCS 元数据 / 路径别名归一） | `internal/tools/pathutil/pathutil.go`（`CanonicalPath` / `VCSMetadataReason`；写、删、命令目标三条入口共用） |
| 会话/历史持久化（坏数据协议修复在 `prov_history_hygiene.go`） | `internal/app/biz_sessions.go` |
| 系统提示词组装 | `internal/app/biz_prompt.go` |
| 请求消息与上下文 Token 核算（口径：请求前缀 + provider 实测锚点） | `internal/app/biz_context.go`（`sessionPrefixBreakdown` / `contextAnchor` / `finalizeSessionBreakdown`） |
| 配置合并 / key 池管理 | `internal/app/biz_config.go` |
| 技能发现与加载 | `internal/app/biz_skills.go` |
| MCP 客户端生命周期 | `internal/app/biz_mcp.go` |
| 计划任务 / 后台服务 / 命令超时收编 | `orch_scheduler.go` / `orch_services.go`（promoteTimedOutCommand）+ `TaskCenterPanel.vue` |
| 远程 SSH 工具与凭证 | `orch_remote.go` / `orch_ssh_credential.go` |
| 知识库模式（KB 提示词 / sources/ 只读） | `internal/app/orch_kb.go` + `ModeSider.vue` + `App.vue` |
| 对外本地 HTTP API 服务 | `internal/app/biz_api.go` |
| 应用自更新 | `internal/app/biz_update.go` |
| 前端唯一主组件与全局状态 | `frontend/src/App.vue` |
| 技能 / MCP / 模型管理内联页 | `SkillsPanel.vue` / `McpPanel.vue` / `ModelsPanel.vue` |
| 文件树 / 编辑器 UI | `WorkspaceExplorer.vue` + `biz_workspace_editor.go` |
| 工具卡动词 "Used X" 标签 | `frontend/src/utils/toolVerb.mjs`（TOOL_VERBS 表） |
| 思考回放（reasoning_content / reasoning / reasoning_text / signature / encrypted_content） | `internal/app/prov_reasoning.go` + 三适配器（`prov_adapter_*.go`） |
| 提示词缓存（断点位置 / 命中与写入口径；保留时长不发送，用供应商默认） | `prov_model.go`（三适配器断点与 usage）+ `biz_stats.go`（落库与汇总）；身份：会话/子代理 lane 用 `responsesPromptCacheKey`，回放台账用 `reasoningScope` |
| 模型输入能力（视觉）降级 / 图片占位 | `internal/app/biz_context.go`（`buildMessages` 单一收口） + 目录字段 `visionCapable`（`scripts/generate-model-catalog.mjs`） |
| 流终止判定（finish_reason / `[DONE]` 哨兵）与 tool_calls 增量归并 | `internal/app/prov_model.go`（`sseDoneWatcher`、`toolCallAccumulator`） |
| 工具 schema 修补（$ref 内联 / 补 type / 矛盾类型修复） | `internal/tools/schemautil/` |
| 工具调用 ID 规范化、参数解码、截断参数标记 | `internal/tools/toolcall/` |

## 核心调用流

`main()` → `NewApp()` → Wails 装配 → 前端 `StartChat()` → `app.runChat()`: `buildMessages()`（biz_context）→ `buildToolsWithMcp()`（biz_mcp）→ `streamModelResponse()`（prov_model）→ 流式事件经 `host_events` 到前端 → 工具分发 `executeTool()`（并发 4，文件变更串行）→ 结果回填循环 → `saveHistory()`（biz_sessions）。子代理/调度任务走 `executeDelegate()`（orch_subagent）。

每个 step 开头汇总上下文用量并决定是否 auto-compact：`breakdownAcc.update()`（消息估算）+ `sessionPrefixBreakdown()`（系统提示词 / 工作区地图）+ `finalizeSessionBreakdown()`（provider 实测锚点）→ `bd.Total` 与阈值比较。footer 与自动压缩读同一个数。压缩统一走 `compactRunHistory(reason)`：`threshold` 为阈值触发（失败不致命），`overflow` 为请求被判定上下文超长后的强制压缩并重试（`llmErrorKindContextTooLong`）；压缩也失败才报出可操作提示。

## 后端分层架构

### `internal/app/`
- `app.go`: Agent 编排核心（聊天循环 runChat、工具分发 executeTool、内建工具 chatTools、生命周期 StartChat/CancelRun）。
- `prov_*`: 模型与网络适配（协议脏代码唯一聚集区）。`prov_model.go`（多 key 池调度、流式归并、错误分类；工具声明与工具调用 ID 的纯规则已下沉到 `internal/tools/schemautil`、`internal/tools/toolcall`）；`prov_adapter_chat.go` / `prov_adapter_responses.go` / `prov_adapter_anthropic.go`（三家协议独立适配器：请求构造、工具/消息转换、流解析与 Usage 提取）；`prov_reasoning.go`（思考回放台账与请求体改写；台账 scope 按 run 隔离，见 `reasoningScope`）；`prov_wire_config.go`（思考档位 wire 拼写 `reasoningWireForAdapter`、API 格式归一化、端点/token 参数默认值——档位拼写的唯一收口）；`prov_history_hygiene.go`（历史消息协议卫生：内存/磁盘双 profile、tool_call/tool_result 配对修复、悬空调用剥离）；`prov_proxy*.go`（代理探测与 SSRF 守卫客户端）。
- `host_*`: 桌面与宿主桥（唯一允许 import Wails）。`host_desktop.go`（桌面桥与对话框）；`host_events.go`（emit 统一出口）；`host_window_state.go`（窗口位置持久化）；`host_notifications.go`（桌面通知音）。
- `infra_*`: 共享基础设施。`infra_bridges.go`（类型别名与原子写）；`infra_result.go`（结果信封与模型端压缩）；`infra_stream.go`（流式节流）；`infra_output_encoding.go`（控制台编码与 UTF-8/GBK 转码）。
- `biz_*`: 独立业务模块。`biz_config.go`（配置）；`biz_context.go`（上下文与 Token 核算）；`biz_compact.go`（历史压缩：手动/自动/溢出恢复三入口、阈值与超时归一、`CompactSession`/`CancelCompaction`）；`biz_prompt.go`（系统提示词组装）；`biz_sessions.go`（会话持久化与清理）；`biz_workspace*.go`（文件列表/搜索/编辑器）；`biz_skills.go`（技能发现/加载）；`biz_mcp.go`（MCP 生命周期）；`biz_api.go`（本地 HTTP API）；`biz_update.go`（自更新）；`biz_stats.go`（Token 统计）。
- `orch_*`: 工具编排（绑定纯算法到 `*App` 状态）。`orch_edit_plan.go` / `orch_edit.go`（编辑批次规划与原子提交）；`orch_command.go` / `orch_command_safety.go`（命令执行与安全拦截）；`orch_file_ops.go`（文件读写删与危险路径拦截）；`orch_grep.go`（ripgrep 搜索）；`orch_remote*.go`（SSH 远端操作与凭证）；`orch_scheduler.go`（计划任务）；`orch_services.go`（后台服务）；`orch_subagent.go`（子代理：lane 稳定的缓存路由 + run 局部回放 scope）；`orch_kb.go`（知识库 sources/ 读写保护）。

### `internal/tools/`（纯算法层，绝不依赖 `*App`/`ConfigState`）
- `calculate/`（数学求值）· `command/`（Bash AST 安全解析与目标提取）· `edit/`（LCS diff 与范围替换）· `git/`（porcelain 解析）· `grep/`（ripgrep 封装）· `pathutil/`（路径安全解析）· `read/`（文本读取与版本计算）· `scheduler/`（调度表达式解析）· `schemautil/`（工具 JSON-Schema 修补：$ref 内联与属性 type 推断）· `service/`（rolling buffer 与长进程判定）· `shared/`（CodedError 与内置 schema）· `toolcall/`（工具调用 ID 规范化、参数解码、截断参数标记）。

## 前端核心结构 (`frontend/src/`)

- `App.vue`: 前端根组件（状态流转、Wails 事件监听、工作区 Tab 与模式切换、流式合帧渲染）。
- `components/`:
  - 核心视图与面板: `AppHeader.vue` (顶部栏与 Tab 拖拽), `ModeSider.vue` (左侧模式栏), `ChatMessages.vue` (消息流), `ComposerInfoBar.vue` (输入栏与模型选择), `WorkspaceExplorer.vue` (文件树与编辑器), `SkillsPanel.vue` (技能管理), `McpPanel.vue` (MCP 管理), `ModelsPanel.vue` (模型管理), `TaskCenterPanel.vue` (任务中心), `SettingsModal.vue` (系统设置), `TokenStatsModal.vue` (Token 统计)。
  - 工具卡组件: `ToolCallCard.vue` (通用工具卡与 diff), `AskToolCard.vue` (交互提问), `SubagentInlineCard.vue` (子代理卡), `DiffView.vue` (Diff 渲染), `HtmlRenderCard.vue` (HTML 渲染)。
- `utils/`: `toolVerb.mjs` (工具卡动词表), `toolEventState.mjs` (事件状态机), `diff.js` (Diff 计算), `config.mjs` (配置处理), `i18n.mjs` (双语文本源)。
