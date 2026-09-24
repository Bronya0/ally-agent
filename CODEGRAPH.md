# Code Graph: ally-agent — 文件级功能速查

> 一行一模块/文件的功能索引，供 AI 快速定位"某个功能在哪个文件"。规范类内容见 `AGENTS.md`。

## 功能 → 文件速查表

| 想找什么 | 去哪里 |
|---|---|
| 聊天循环、工具分发（含 command 工具执行）、运行/会话编排 | `internal/app/app.go`（runChat / executeTool / chatTools） |
| 模型流式适配、多 key 故障切换 | `internal/app/prov_model.go`（调度/多 key）+ `prov_adapter_*.go`（三协议适配器） |
| 代理检测 / 代理 HTTP 客户端 | `internal/app/prov_proxy*.go`（含 darwin/scutil/windows 平台探测） |
| 事件出口（后端 emit） | `internal/app/host_events.go`；前端路由在 `App.vue` `bindRuntimeEvents()` |
| 桌面生命周期、窗口几何、系统对话框 | `internal/app/host_desktop.go` / `host_window_state.go` |
| http_request / web_fetch 工具（含 SSRF 防护） | `internal/app/orch_http.go` |
| 文件变更后校验 / 批次内校验规划 | `internal/app/orch_validation.go`（validateChangedFiles / planBatchValidation） |
| 工具批次策略（文件变更串行、写冲突检测） | `internal/app/orch_batch_policy.go` |
| memory 工具（记忆列表/搜索） | `internal/app/orch_memory.go` + `internal/tools/memory/` |
| git 状态查看 | `internal/app/orch_git.go` + `internal/tools/git/` |
| 模型列表拉取 | `internal/app/biz_modellist.go`（FetchModelList） |
| AGENTS.md / CODEGRAPH.md 注入、背景图 | `internal/app/biz_project_context.go`（loadAgentsMd / loadCodeGraph） |
| 临时工作区（创建/清理） | `internal/app/biz_temp_workspace.go` |
| 内置技能清单 | `internal/app/biz_builtin_skills.go` |
| 错误日志轮转 | `internal/app/infra_log.go`（InitErrorLogger） |
| 命令环境 / 登录 shell PATH 探测 | `internal/app/infra_shell_env.go`（commandEnvironment / warmCommandEnvironment） |
| 在终端打开工作区路径 | `internal/app/host_terminal*.go` |
| 内置工具 schema 定义 | `internal/tools/shared/builtins.go` |
| 某工具的执行逻辑 | `internal/app/orch_<name>.go`（编排）+ `internal/tools/<name>/`（纯算法） |
| edit 读写契约 / 原子写 / 冲突合并 | `orch_edit_plan.go` → `orch_edit.go` → `tools/edit`、`tools/read` |
| 命令安全拦截与路径校验 | `orch_command_safety.go` + `internal/tools/command` |
| 文件基础读写与删除防护 | `internal/app/orch_file_ops.go` |
| 受保护路径判定（VCS 元数据 / 路径别名归一） | `internal/tools/pathutil/pathutil.go`（`CanonicalPath` / `VCSMetadataReason`；写、删、命令目标三条入口共用） |
| 会话/历史持久化（坏数据协议修复在 `prov_history_hygiene.go`） | `internal/app/biz_sessions.go` |
| 系统提示词组装（核心规则 / 技能名片 / 记忆索引 / 用户档案 USER.md / AGENTS.md / 代码图谱 / 项目教训 LESSONS.md / 自定义提示词） | `internal/app/biz_prompt.go` |
| 请求消息与上下文 Token 核算（口径：请求前缀 + provider 实测锚点） | `internal/app/biz_context.go`（`sessionPrefixBreakdown` / `contextAnchor` / `finalizeSessionBreakdown`） |
| 配置合并 / key 池管理 | `internal/app/biz_config.go` |
| 技能发现与加载 | `internal/app/biz_skills.go` |
| MCP 客户端生命周期 | `internal/app/biz_mcp.go` |
| 计划任务 / 后台服务 / 命令超时收编 | `orch_scheduler.go` / `orch_services.go`（promoteTimedOutCommand）+ `TaskCenterPanel.vue` |
| 远程 SSH 工具与审批闸门（首次连接 / 危险命令 / 覆盖 / 集群登记；主机指纹不一致直接换记录重连） | `orch_remote.go` / `orch_ssh_credential.go`（按解析端点缓存并区分认证模式）+ `internal/tools/sshclient/`（纯 Go 传输、密钥认证、known_hosts 固化与不一致时替换） |
| SSH 集群清单与工作区授权（`ssh_clusters.json` 落盘、别名/端点两种写法、默认拒绝） | `internal/app/biz_ssh_cluster.go` + `orch_ssh_cluster.go` + `SSHClusterPanel.vue` + `ComposerInfoBar.vue` |
| 知识库模式（KB 提示词 / sources/ 只读） | `internal/app/orch_kb.go` + `ModeSider.vue` + `App.vue` |
| 对外本地 HTTP API 服务 | `internal/app/biz_api.go` |
| 应用自更新 | `internal/app/biz_update.go` |
| 前端唯一主组件与全局状态 | `frontend/src/App.vue` |
| 技能 / MCP / 模型 / SSH 集群管理内联页 | `SkillsPanel.vue` / `McpPanel.vue` / `ModelsPanel.vue` / `SSHClusterPanel.vue` |
| 文件树 / 编辑器 UI | `WorkspaceExplorer.vue` + `biz_workspace_editor.go` |
| 工具卡动词 "Used X" 标签 | `frontend/src/utils/toolVerb.mjs`（TOOL_VERBS 表） |
| 思考回放（reasoning_content / reasoning / reasoning_text / signature / encrypted_content） | `internal/app/prov_reasoning.go` + 三适配器（`prov_adapter_*.go`） |
| 提示词缓存（断点位置 / 命中与写入口径；保留时长不发送，用供应商默认） | `prov_model.go`（三适配器断点与 usage）+ `biz_stats.go`（落库与汇总）；身份：会话/子代理 lane 用 `responsesPromptCacheKey`，回放台账用 `reasoningScope` |
| 模型输入能力（视觉）降级 / 图片占位 | `internal/app/biz_context.go`（`buildMessages` 单一收口） + 目录字段 `visionCapable`（`frontend/src/data/modelCatalog.json`，由 `scripts/generate-model-catalog.mjs` 生成） |
| 流终止判定（finish_reason / `[DONE]` 哨兵）与 tool_calls 增量归并 | `internal/app/prov_model.go`（`sseDoneWatcher`、`toolCallAccumulator`） |
| 工具 schema 修补（$ref 内联 / 补 type / 矛盾类型修复） | `internal/tools/schemautil/` |
| 工具调用 ID 规范化、参数解码、截断参数标记 | `internal/tools/toolcall/` |

## 核心调用流

`main()` → `NewApp()` → Wails 装配 → 前端 `StartChat()` → `app.runChat()`: `buildMessages()`（biz_context）→ `buildToolsWithMcp()`（biz_mcp）→ `streamModelResponse()`（prov_model）→ 流式事件经 `host_events` 到前端 → 工具分发 `executeTool()`（并发 4，文件变更串行：`orch_batch_policy.go` 定序，变更后校验走 `orch_validation.go`）→ 结果回填循环 → `saveHistory()`（biz_sessions）。子代理/调度任务走 `executeDelegate()`（orch_subagent）。

每个 step 开头汇总上下文用量并决定是否 auto-compact：`breakdownAcc.update()`（消息估算）+ `sessionPrefixBreakdown()`（系统提示词 / 工作区地图）+ `finalizeSessionBreakdown()`（provider 实测锚点）→ `bd.Total` 与阈值比较。footer 与自动压缩读同一个数。阈值触发时先做微压缩（`microcompactMessages`：只清掉较旧的工具结果、消息条数不变，同时丢弃 provider 实测锚点并失效本次 run 的读缓存），仍在阈值之上再走 `compactRunHistory(reason)`：`threshold` 为阈值触发（失败不致命），`overflow` 为请求被判定上下文超长后的强制压缩并重试（`llmErrorKindContextTooLong`）；压缩也失败才报出可操作提示。

## 后端分层架构

### `internal/app/`
- `app.go`: Agent 编排核心（聊天循环 runChat、工具分发 executeTool、内建工具 chatTools、生命周期 StartChat/CancelRun；command 工具执行也在此分发）。
- `prov_*`: 模型与网络适配（协议脏代码唯一聚集区）。`prov_model.go`（多 key 池调度、流式归并、错误分类；工具声明与工具调用 ID 的纯规则已下沉到 `internal/tools/schemautil`、`internal/tools/toolcall`）；`prov_adapter_chat.go` / `prov_adapter_responses.go` / `prov_adapter_anthropic.go`（三家协议独立适配器：请求构造、工具/消息转换、流解析与 Usage 提取）；`prov_reasoning.go`（思考回放台账与请求体改写；台账 scope 按 run 隔离，见 `reasoningScope`）；`prov_wire_config.go`（思考档位 wire 拼写 `reasoningWireForAdapter`、API 格式归一化、端点/token 参数默认值——档位拼写的唯一收口）；`prov_history_hygiene.go`（历史消息协议卫生：内存/磁盘双 profile、tool_call/tool_result 配对修复、悬空调用剥离、运行中微压缩 `microcompactMessages`）；`prov_proxy*.go`（代理探测、SSRF 守卫客户端与模型请求的流空闲超时 `streamIdleTimeoutTransport`）。
- `host_*`: 桌面与宿主桥（唯一允许 import Wails）。`host_desktop.go`（桌面桥与对话框）；`host_events.go`（emit 统一出口）；`host_window_state.go`（窗口位置持久化）；`host_notifications.go`（桌面通知音）；`host_network.go`（网络事件环与订阅分发）；`host_terminal*.go`（在终端打开工作区路径）；另有按平台分文件：`host_clipboard_windows.go`/`_other.go`、`host_filemanager_windows.go`/`_other.go`、`host_process_windows.go`/`_other.go`、`host_update_relaunch_darwin.go`/`_other.go`。
- `infra_*`: 共享基础设施。`infra_bridges.go`（类型别名与原子写）；`infra_result.go`（结果信封与模型端压缩）；`infra_stream.go`（流式节流）；`infra_output_encoding.go`（控制台编码与 UTF-8/GBK 转码）；`infra_log.go`（错误日志按日轮转）；`infra_shell_env.go`（登录 shell PATH 探测与命令环境组装）。
- `biz_*`: 独立业务模块。`biz_config.go`（配置）；`biz_context.go`（上下文与 Token 核算）；`biz_compact.go`（历史压缩：手动/自动/溢出恢复三入口、阈值与超时归一、`CompactSession`/`CancelCompaction`）；`biz_prompt.go`（系统提示词组装：各段原料的读取与预算、用户档案与项目教训的注入、提示词缓存）；`biz_sessions.go`（会话持久化与清理）；`biz_workspace*.go`（文件列表/搜索/编辑器/文件信息，fileinfo 按平台分文件）；`biz_ssh_cluster.go`（SSH 集群清单落盘、端点/别名解析、工作区白名单与会话级连接放行；写盘后 emit `ssh:clusters-changed`）；`biz_skills.go`（技能发现/加载）；`biz_builtin_skills.go`（内置技能条目）；`biz_mcp.go`（MCP 生命周期）；`biz_api.go`（本地 HTTP API）；`biz_modellist.go`（拉取模型列表）；`biz_project_context.go`（AGENTS.md / CODEGRAPH.md 读取注入、背景图管理）；`biz_temp_workspace.go`（临时工作区创建与退出清理）；`biz_update.go`（自更新）；`biz_stats.go`（Token 统计）。
- `orch_*`: 工具编排（绑定纯算法到 `*App` 状态）。`orch_edit_plan.go` / `orch_edit.go`（编辑批次规划与原子提交）；`orch_command_safety.go`（命令安全拦截）；`orch_batch_policy.go`（文件变更工具定序与写批次冲突检测）；`orch_validation.go`（文件变更后校验与批次校验规划）；`orch_file_ops.go`（文件读写删与危险路径拦截）；`orch_read.go`（读取，含图片与去重哈希）；`orch_grep.go`（ripgrep 搜索）；`orch_http.go`（http_request / web_fetch 编排）；`orch_git.go`（git 状态）；`orch_memory.go`（memory 工具编排）；`orch_remote*.go`（SSH 远端操作：读写/编辑/删除/跑命令共用一个解析并授权入口、以及多道审批闸门（首次连接 / 危险命令 / 覆盖 / 集群登记；主机指纹不一致时不经询问直接更新记录并重连））；`orch_ssh_cluster.go`（模型侧 `ssh_cluster` 工具：列清单、登记新节点、对已登记节点只申请授权不覆盖）；`orch_scheduler.go`（计划任务）；`orch_services.go`（后台服务）；`orch_subagent.go`（子代理：lane 稳定的缓存路由 + run 局部回放 scope）；`orch_kb.go`（知识库 sources/ 读写保护）。

### `internal/tools/`（纯算法层，绝不依赖 `*App`/`ConfigState`）
- `calculate/`（数学求值）· `command/`（Bash AST 安全解析与目标提取）· `edit/`（LCS diff 与范围替换）· `git/`（porcelain 解析）· `grep/`（ripgrep 封装）· `memory/`（记忆条目存储与运行时接口）· `pathutil/`（路径安全解析）· `read/`（文本读取与版本计算）· `scheduler/`（调度表达式解析）· `schemautil/`（工具 JSON-Schema 修补：$ref 内联与属性 type 推断）· `service/`（rolling buffer 与长进程判定）· `shared/`（CodedError 与内置 schema）· `sshclient/`（纯 Go SSH 连接、密钥/Agent 认证、主机指纹校验）· `toolcall/`（工具调用 ID 规范化、参数解码、截断参数标记）。

### `internal/game/`
（已移除：局域网联机中继服务下线，游戏区仅保留人机对战；模型调用经 `internal/app/biz_game_ai.go`。）

## 前端核心结构 (`frontend/src/`)

- `App.vue`: 前端根组件（状态流转、Wails 事件监听、工作区 Tab 与模式切换、流式合帧渲染）。
- `i18n.mjs`: 双语文本源（在 src 根，不在 utils/ 下）。
- `composables/`: `useToolEvents.mjs`（工具事件流分组）、`sakuraBreeze.mjs`（樱花动效）、`digitalWave.mjs`（数码波浪的模块级单例入口，特效层挂载时注册、触发点调 burstDigitalWave）。
- `data/`: `modelCatalog.json`（模型目录，脚本生成）、`eyeLines.mjs`。
- `games/`: `GamePanel.vue`（人机对战面板）与规则/玩法逻辑（`rules.mjs`）；文本棋盘协议与走法解析在 `ai.mjs`（后端单次模型调用绑定 `internal/app/biz_game_ai.go`，返回 usage 供面板显示 token 用量）。
- `components/`:
  - 核心视图与面板: `AppHeader.vue` (顶部栏与 Tab 拖拽), `ModeSider.vue` (左侧模式栏), `ChatMessages.vue` (消息流), `ComposerInfoBar.vue` (输入栏), `ModelMenu.vue` (共享模型选择下拉，输入栏与游戏区共用), `WorkspaceExplorer.vue` (文件树与编辑器), `SkillsPanel.vue` (技能管理), `McpPanel.vue` (MCP 管理), `ModelsPanel.vue` (模型管理), `SSHClusterPanel.vue` (SSH 集群清单管理：增删改/导入导出), `TaskCenterPanel.vue` (任务中心), `SettingsModal.vue` (系统设置), `TokenStatsModal.vue` (Token 统计), `CommandMenu.vue` (命令面板), `WelcomeMessage.vue` (欢迎页), `GamePanel.vue` (游戏面板, 在 games/)。
  - 工具卡与消息渲染: `ToolCallCard.vue` (通用工具卡与 diff), `AskToolCard.vue` (交互提问), `SubagentInlineCard.vue` (子代理卡), `DiffView.vue` (Diff 渲染), `HtmlRenderCard.vue` (HTML 渲染), `ReadGroupCard.vue` / `ReadGrepGroupCard.vue` (read/grep 合并卡), `TerminalOutputView.vue` (终端输出), `CodeView.vue`, `ToolStatusIcon.vue`, `RenderBoundary.vue`, `StreamingMarkdownBody.vue` (流式 Markdown 缓动渲染), `MessageAttachments.vue`, `ContextUsageInline.vue` (上下文用量)。
  - 弹层与小组件: `ToolsPopover.vue` / `McpStatusPopover.vue` / `SkillsPopover.vue` (欢迎页触发), `GitDiffModal.vue`, `FileInfoModal.vue`, `FileMentionMenu.vue`, `TokenPieChart.vue`, `AllyAvatar.vue`, `AllyWordmark.vue`, `SakuraBreeze.vue`, `DigitalWave.vue` (数码波浪全屏特效层), `RunSpinner.vue` (输入栏状态行的“在跑”三根条，纯 CSS 关键帧 + scaleY，标签为空时挂载)。
- `utils/`: 工具卡相关（`toolVerb.mjs` 动词表、`toolEventState.mjs` 事件状态机、`toolFormat.mjs`、`toolPreview.mjs`、`toolCardSignature.mjs`、`toolError.mjs`）；模型与配置（`modelConfigIO.mjs` 归一收口、`modelProviderCatalog.mjs`、`modelLabel.mjs`、`modelUsage.mjs`、`config.mjs`）；会话与输入（`sessionStore.mjs`、`sessionState.mjs`、`promptHistoryStore.mjs` 有界历史、`planPanel.mjs`）；渲染（`diff.js`、`ansi.mjs`、`shellHighlight.mjs`、`htmlRender.mjs`、`markdownPreview.mjs`、`mermaidShared.mjs`、`streamEase.mjs` 流式缓动）；SSH 集群（`sshClusterIO.mjs` 导入导出载荷的归一与校验）；平台（`clipboard.mjs`、`download.mjs`、`theme.mjs`、`fileInfo.mjs`、`format.mjs`、`versionCheck.mjs`、`buildVersion.js`、`skills.mjs`）。
