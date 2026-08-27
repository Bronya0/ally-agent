# Code Graph: ally-agent — 文件级功能速查

> 用途：一行一文件的功能索引，供 AI/开发者**快速定位**"某个功能在哪个文件"。按目录与 `internal/app/` 前缀分层分组；测试文件列于各组尾部。
> 本文件由 `biz_project_context.go` 注入系统提示词，请保持精炼；增删文件或重大架构变更后同步更新。规范类内容（"应该如何改"）见 `AGENTS.md`。
> 平台分叉约定：文件后缀 `_windows` / `_darwin` / `_linux` / `_other` 为对应平台的实现（或空实现），构建标签隔离。

## 功能 → 文件速查表

| 想找什么 | 去哪里 |
|------|------|
| 聊天循环、工具分发、运行/会话编排 | `internal/app/app.go`（runChat / executeTool / chatTools） |
| 模型流式适配、多 key 故障切换 | `internal/app/prov_model.go` |
| 代理检测 / 代理 HTTP 客户端 | `internal/app/prov_proxy*.go` |
| 事件出口（后端 emit） | `internal/app/host_events.go`；前端路由在 `App.vue` `bindRuntimeEvents()` |
| 内置工具 schema 定义 | `internal/tools/shared/builtins.go` |
| 某工具的执行逻辑 | `internal/app/orch_<name>.go`（编排）+ `internal/tools/<name>/`（纯算法） |
| edit 读写契约 / 原子写 | `orch_edit_plan.go` → `orch_edit.go` → `internal/tools/edit`、`internal/tools/read` |
| 命令安全拦截 | `orch_command_safety.go` + `internal/tools/command` |
| 会话/历史持久化 | `internal/app/biz_sessions.go` |
| 系统提示词组装 | `internal/app/biz_prompt.go` |
| 配置合并 / key 池 | `internal/app/biz_config.go` |
| MCP 生命周期 | `internal/app/biz_mcp.go` |
| 应用自更新 | `internal/app/biz_update.go` |
| Token 统计 | `internal/app/biz_stats.go` + `TokenStatsModal.vue` |
| 工作区文件列表 / workspace map | `internal/app/biz_workspace.go` |
| 文件树/编辑器 UI | `WorkspaceExplorer.vue` + `biz_workspace_editor.go` |
| 设置界面 | `SettingsModal.vue` |
| 工具卡动词 "Used X" 问题 | `frontend/src/utils/toolVerb.mjs`（TOOL_VERBS 表） |
| 子代理 | `internal/app/orch_subagent.go` + `SubagentInlineCard.vue` |
| 计划任务 / 后台服务 | `orch_scheduler.go` / `orch_services.go` + `TaskCenterPanel.vue` |
| 游戏休息区 | `internal/game/service.go` + `frontend/src/games/` |

## 核心调用流（简要）

`main()` → `backend.NewApp()`（加载配置、建目录）→ Wails 装配 → 前端 `StartChat(ChatRequest)` → `app.runChat()`：`buildMessages()`（biz_context）→ `buildToolsWithMcp()`（biz_mcp）→ `streamModelResponse()`（prov_model）→ 流式事件经 `eventSink`（host_events）到前端 → 工具调用经 `executeTool()` 分发到 `orch_*`/`biz_*`（批次策略见 orch_batch_policy，并发 4，文件变更串行）→ 结果压缩（infra_result）回填模型 → 循环至无工具调用 → `saveHistory()`（biz_sessions）。子代理/计划任务走 `executeDelegate()`（orch_subagent）。

---

## main.go（入口）

- main.go — Wails v3 应用入口：嵌入前端资源与图标、先处理更新重启 helper、装配 App/game/通知三个 Service、无边框深色主窗口 + 单实例聚焦；关键: main, NewApp, SingleInstanceOptions

## internal/app/ — 核心

- app.go — Agent 编排核心：App 长生命周期状态、约 55 个 DTO（ChatRequest/EditRequest/AskRequest/Remote*/Service* 等）、runChat 聊天循环（含 turn 级重试/取消/400-sanitize）、executeTool 工具分发与宽容解码、chatTools 注册、ask/suggest/会话绑定；关键: App, ConfigState, runChat, executeTool, chatTools, StartChat, CancelRun, normalizeToolCalls, executeAsk, SubmitAskResponse

## internal/app/ — prov_ 前缀（Provider 适配）

- prov_model.go — Provider 流式适配唯一边界：openai_chat / openai_responses / anthropic_messages 三适配器、SSE 解析、usage 汇总、tool-call 增量合并与修复、多 key 池故障切换（冷却/探测）；关键: streamModelResponse, streamOpenAIChat, streamOpenAIResponses, streamAnthropicMessages, mergeToolCallDeltas, shouldRetryLLMError, markAnthropicPromptCacheBreakpoints
- prov_proxy.go — 代理感知 HTTP 客户端：fail-closed 代理解析、transport 缓存、SSRF 守卫拨号（guardedDialContext）、状态脱敏、代理环境变量生成；关键: proxyForConfig, proxyHTTPClient, guardedDialContext, sanitizeProxyStatus
- prov_proxy_darwin.go — macOS 系统代理检测入口（scutil）
- prov_proxy_windows.go — Windows 系统代理检测（注册表 Internet Settings）
- prov_proxy_other.go — Linux 等平台代理检测（环境变量）
- prov_proxy_scutil.go — macOS `scutil --proxy` 输出解析（HTTP/HTTPS/SOCKS/PAC 标记）；关键: parseScutilProxyOutput
- prov_model_test.go — 测试 Anthropic 缓存断点、Responses 提示缓存、tool-call 名称折叠/去重合并、400 识别
- prov_model_keys_test.go / prov_model_keys_integration_test.go — 测试 key 池归一化、冷却选择与隔离、多 key 故障切换集成
- prov_proxy_test.go / prov_proxy_fix_test.go / prov_proxy_scutil_test.go — 测试手动代理 fail-closed 与脱敏、SSRF 放行代理主机/绕过列表、scutil 解析

## internal/app/ — host_ 前缀（宿主桥接，唯一可 import Wails 的文件群）

- host_desktop.go — Wails 生命周期与桌面桥：ServiceStartup/Shutdown、事件 sink 适配、窗口/对话框（选工作区、选背景图、导出文件）、打开文件管理器、自启动；关键: ServiceStartup, wailsEventSink, SelectWorkspace, ExportTextFile
- host_events.go — 事件出口边界：`eventSink` 接口、`App.emit()` 唯一发射点、panic 隔离的 fanout 广播（Wails + 网络双出口）；关键: eventSink, emit, fanoutEventSink
- host_network.go — 可选网络事件出口（`ALLY_NETWORK_EVENTS=1` 启用）：token 鉴权 SSE `/events`、轮询 `/poll`、`/healthz`、环形历史缓冲；关键: networkEventSink, handleSSE, handlePoll, eventRing
- host_network_test.go — 测试环形缓冲、fanout 广播与 panic 隔离、非回环拒绝、SSE/轮询/鉴权/历史、大负载截断
- host_notifications.go — 桌面通知服务注入与任务完成/出错/取消提示音（仅窗口最小化时，700ms 冷却）；关键: SetNotifier, notifyCompletion
- host_process_windows.go — Windows 子进程窗口隐藏（CREATE_NO_WINDOW）与 Job Object 进程树终止；关键: hideCommandWindow, stopProcessTree
- host_process_other.go — 非 Windows 对应实现（SIGKILL 进程组）
- host_taskbar_windows.go — Windows 任务栏进度与未激活窗口闪烁（手写 taskbarlist3 COM vtable）
- host_taskbar_other.go — 非 Windows 任务栏进度空实现
- host_filemanager_windows.go — Windows 用 explorer.exe 打开路径（CmdLine 转义防注入）；关键: explorerCommand
- host_filemanager_other.go — 非 Windows 文件管理器命令构造（xdg-open 等）
- host_tray.go — 系统托盘（图标缩放、菜单、close-to-tray 钩子；当前发布已禁用调用）；关键: setupSystemTray
- host_update_relaunch_darwin.go — macOS 更新后重启 helper（子进程等待父进程管道）；关键: RunUpdateRelaunchHelper
- host_update_relaunch_other.go — 其他平台更新重启空实现

## internal/app/ — infra_ 前缀（工具基础设施，跨编排共享）

- infra_bridges.go — 工具基础设施桥：类型别名、provider 参数/格式/reasoningEffort 归一化、limitedBuffer、路径与哈希薄包装、原子写；关键: normalizeAPIFormat, normalizeReasoningEffort, limitedBuffer, resolveReadablePath, safeWriteFile
- infra_result.go — 工具结果信封与模型压缩：`{ok,data,error,errorCode,details,warnings}`、CodedError 码提取、按工具压缩 data/文本输出；关键: toolResult, compactToolResultForModel, toolResultSummary
- infra_stream.go — 流式节流：`run:stream` 64ms 纯时间节流发射器、`tool:update` 200ms/2048B 进度门控与流末强制快照；关键: runStreamDeltaEmitter, toolCallProgressTracker, eventsWithForce
- infra_shell_env.go — 登录 shell 环境探测（macOS/Linux PATH 导入）、命令环境构造；关键: commandEnvironment, probeLoginShellPath
- infra_bridges_test.go / infra_shell_env_test.go — 测试 reasoningEffort 归一化、mergeConfig 适配器取值、登录 shell PATH 探测

## internal/app/ — biz_ 前缀（业务模块）

- biz_config.go — 配置域：默认值、mergeConfig 覆盖合并、key 池归一化与故障转移配置、代理网络配置、模型连通测试；关键: mergeConfig, SaveConfig, effectiveConfig, TestModelConnection, normalizeAPIKeys
- biz_context.go — 请求消息组装与上下文核算：buildMessages（系统提示+历史+当前消息+附件）、plan/todo 状态机、ContextBreakdown token 估算与静态缓存；关键: buildMessages, handleTodoList, getContextBreakdown, appendUserMessageWithAttachments
- biz_prompt.go — 系统提示词管线：buildSystemPromptParts、优先级声明、共享编辑规则、skill 元数据、全局记忆索引、项目 lessons（.ally/lessons.md）上下文；关键: buildSystemPromptParts, buildSkillListingMeta, buildMemoryIndexContext, sharedEditRules
- biz_project_context.go — AGENTS/CLAUDE 项目指令加载（用户级+工作区级，去重拼接）、CODEGRAPH.md 提示片段、聊天背景图文件管理；关键: loadAgentsMd, loadCodeGraph, buildCodeGraphPromptPart
- biz_sessions.go — 会话持久化三类文件（`sessions/index.json` + 会话快照 gz + `histories/<id>.json.gz`）：原子写、索引降级重建与最旧淘汰、历史裁剪（token 预算）、sanitize 修复（dangling tool_calls / 截断 JSON / 重复函数名）；关键: ListSessions, saveHistory, sanitizeHistoryMessages, repairDanglingToolCalls, trimSavedHistory, TruncateSessionHistory
- biz_workspace.go — 工作区文件列表与 workspace map：list_files、按会话冻结 map（前缀字节稳定保 prompt cache）、路径搜索索引（rg/walk 双路径+TTL 缓存）、根 .gitignore 匹配；关键: listFilesWithConfig, sessionWorkspaceMap, searchWorkspacePaths
- biz_workspace_editor.go — UI 文件浏览器专用的受限文本/媒体读写：16MiB 上限、版本冲突校验、原子保存、图片/视频/PDF 预览；关键: ReadWorkspaceFile, SaveWorkspaceFile, WorkspaceImageContent
- biz_workspace_fileinfo.go — 文件详情：元数据 + 单遍多哈希（MD5/SHA1/SHA256/SHA512/CRC32）、目录有界递归汇总；关键: GetWorkspaceFileInfo
- biz_skills.go — skill 发现/加载/启停：多目录扫描（.agents > .claude > builtin 去重）、frontmatter 解析（whenToUse/when_to_use 兼容）、disabledSkills 管理、`/skill` 加载渲染；关键: ListSkills, ActivateSkill, DeactivateSkill, parseSkillContent, handleSkillToolCall
- biz_builtin_skills.go — 枚举 go:embed 内置 skill 资源为 SkillDefinition（embeddedContent 免磁盘 IO）；关键: builtinSkillEntries
- biz_mcp.go — MCP 生命周期与工具桥接：加载 mcp.json、stdio/SSE/Streamable-HTTP 连接、schema 归一化、`mcp__<server>__<tool>` 函数名映射、失效会话重连；关键: McpManager, StartAll, CallToolByFunctionName, buildToolsWithMcp
- biz_modellist.go — 调 OpenAI 标准 GET /models 返回模型 ID 列表（走代理、15s 超时、8MB 上限）；关键: FetchModelList
- biz_stats.go — 异步 Token 统计：有界非阻塞队列、按天落盘 `stats/<date>.json`（90 天保留、备份恢复）、聚合查询；关键: statsRecorder, recordTokenStats, GetTokenStats
- biz_update.go — 应用自更新：Atom 发布检查、平台资产下载、暂存校验、Windows/macOS 原子应用与回滚、跳过版本；关键: CheckForUpdates, DownloadUpdate, ApplyUpdate, rollbackReplacedResources
- biz_workspacedir_windows.go — Windows 默认工作区（SHGetKnownFolderPath Documents，尊重 OneDrive 重定向）
- biz_workspacedir_other.go — macOS/Linux 默认工作区（~/Documents、XDG user-dirs）
- biz_workspace_fileinfo_windows.go / _darwin.go / _linux.go — 各平台 stat 时间与块分配实现
- biz_workspace_fileinfo_times.go — 平台无关 stat 时间集合类型
测试:
- app_test.go — 测试 runChat 重试/取消/空响应、mergeConfig、历史持久化、系统提示词、todo、节流、更新清理
- app_run_input_test.go — 测试 run 期间排队注入消息（校验/满队列/注入顺序）
- app_movepath_test.go — 测试 MovePath 工作区内移动
- app_orch_git_test.go — 测试 git status --porcelain=v2 解析
- biz_sessions_test.go / biz_sessions_truncate_test.go — 测试会话索引/快照、修复、截断
- biz_workspace_map_test.go — 测试 workspace map 折叠/大小/忽略规则/剪枝
- biz_workspace_editor_test.go — 测试编辑器读写/过期拒绝/超大拒绝
- biz_project_context_test.go — 测试 gitignore 规则、map 缓存、AGENTS 注入
- biz_prompt_lessons_test.go — 测试 lessons 上下文渲染
- biz_skills_test.go / biz_skills_claude_compat_test.go — 测试 skill 解析/扫描/.claude 兼容
- biz_builtin_skills_test.go — 测试内置 skill 枚举
- biz_mcp_test.go — 测试 MCP schema 归一化/函数名/排序/invalid session
- biz_stats_test.go — 测试 token 统计聚合/落盘/备份/淘汰（16 例）
- biz_update_atom_test.go / biz_update_rollback_test.go — 测试 Atom 解析与回滚

## internal/app/ — orch_ 前缀（工具编排：绑定纯算法到 App 状态）

- orch_file_ops.go — create/delete/run-command 编排：路径安全解析、Git Bash 检测（findWindowsBash）、危险删除路径守卫；关键: createFileWithConfig, deletePathWithConfig, runCommandWithConfig, isDangerousDeletePath
- orch_read.go — 批量读取编排：并行读取、有界行预览（2000 字符/行）、run 级读取缓存、图片 DataURL 注入、办公/PDF 文档拒绝（指路 anydoc）；关键: BatchReadFiles, runReadCache, readImageWithConfig
- orch_edit_plan.go — 本地编辑批次**唯一归一化边界**（planLocalEditBatch）：合并同路径、冲突检测、执行器与批次策略共用该计划；关键: planLocalEditBatch
- orch_edit.go — 本地 edit 执行：原子提交/回滚、版本校验、流截断参数抢救（salvageEditRequest）；关键: editFilesWithConfig, salvageEditRequest
- orch_batch_policy.go — 工具批次冲突/屏障策略：同路径写互斥（E_WRITE_BATCH_CONFLICT）、wait/ask/suggest 单调用屏障；关键: detectToolBatchConflicts, detectWriteBatchConflicts
- orch_validation.go — edit/create 写入后低成本语言校验（Python/Go/JS/TS/Vue/Java/JSON），单个 validation 字符串回填模型；关键: attachValidation, validateChangedFiles
- orch_command_safety.go — 命令安全边界：绑定 command AST 语义分析到工作区根与路径存在性，产出 E_COMMAND_BLOCKED/E_PATH_OUTSIDE；关键: checkCommandSafety, validateRemoteCommandSafety
- orch_git.go — git status/diff 编排：porcelain V2 解析、TTL 缓存、diff 序列化与取消；关键: getGitStatus, parseGitStatusV2, GetGitDiff, CancelGitDiff
- orch_grep.go — grep 编排：ripgrep 封装绑定工作区解析与安全检查、rg/git-bash 缺失事件；关键: GrepFiles, grepFilesWithConfig
- orch_http.go — http_request/web_fetch 编排：重定向敏感头剥离、压缩体解码、每主机限速、Readability 正文抽取、URL 访问校验；关键: httpRequestToolWithConfig, webFetchToolWithConfig, htmlExtractContent
- orch_remote.go — SSH 远程工具编排：向远程 stdin 注入内嵌 Python helper 完成远程读/写/编辑/删除/命令；关键: invokeRemotePython, remoteReadFile, remoteEdit, buildRemoteScript
- orch_services.go — 后台服务编排：512KB rolling buffer 输出、进程树控制、有界 tail、service:update 事件（仅生命周期三处）；关键: StartService, finalizeService, readServiceOutput
- orch_scheduler.go — 计划任务调度管理：cron/interval/once 解析校验、全局串行执行、scheduled:* 事件；关键: scheduledTaskManager, executeScheduledTaskTool, safeTrigger
- orch_subagent.go — 子代理执行循环：步数预算（默认 25，上限 1000）、耗尽强制汇报轮、独立系统提示与工具排除集（无 ask/subagent/plan/skill/scheduled_task）；关键: executeDelegate, subagentSystemPrompt, subagentTools, forceSubagentFinalReport
- orch_memory.go — 全局记忆工具薄封装（注入 memory.Runtime，逻辑在 internal/tools/memory）；关键: memoriesRuntime, listMemories
测试:
- orch_test.go — 测试跨编排集成：批次冲突/去重、命令安全、edit 全契约、read 预览、grep 统计、服务与调度任务（百余用例）
- orch_bom_test.go — 测试 BOM 端到端保留与 stale version 拒绝
- orch_json_repair_test.go — 测试工具参数 JSON 修复经 executeTool 生效
- orch_read_image_test.go — 测试图片读取 MIME/DataURL/历史清洗
- orch_remote_test.go — 测试远程脚本传输不变量与 payload 占位符注入
- orch_webfetch_test.go — 测试 web_fetch readable/raw 双模式

## internal/game/（局域网休息区服务，与 Agent 隔离）

- service.go — 受限 WS 房间中继：启停 HTTP 房间服务器、本机私有 IPv4 枚举、token 鉴权、Origin 白名单、受限广播（不含游戏规则）；关键: Service, StartServer/StopServer, hub, deriveAccessToken, allowedOrigin
- service_test.go — 测试 token 派生与 Origin 白名单安全

## internal/builtin_skills/（go:embed 内置 skill 资源）

- embed.go — 嵌入整棵 skills/ 目录并暴露根前缀；关键: FS, Root
- skills/anydoc/SKILL.md — Office/PDF → Markdown 转换 skill（read 拒绝文档后指路）
- skills/codegraph/SKILL.md — 生成/更新 CODEGRAPH.md 代码图谱 skill
- skills/playwright-cli/SKILL.md — 经 command 驱动 playwright-cli 操作浏览器 skill

## internal/tools/（工具纯算法层，不依赖 *App/ConfigState）

- shared/builtins.go — 全部内置工具的 OpenAI function schema（strict object）与名称归一化；关键: Builtins(), NormalizeName, enforceStrictSchema
- shared/errors.go — 跨工具 CodedError 与错误码提取；关键: CodedError, New, Code
- pathutil/pathutil.go — 工作区路径解析与安全检查唯一实现（Runtime 注入 AppDataDir）；关键: Runtime, SafeJoin, ResolveReadable, InsideWriteRoot, InsideAllyAgentDir
- read/read.go — 文本读取/编码（UTF-8/UTF-16 转码、BOM）、LF 归一化、SHA-256 版本令牌、原子写；关键: ReadTextFile, DecodeTextBytes, HashVersion, SafeWriteFile
- edit/apply.go — 编辑 Diff 应用纯算法：单快照定位、非重叠校验、倒序应用、缩进不敏感回退；关键: 55 个符号（apply 变更主逻辑）
- edit/diff.go — 变更行范围计算与 unified diff 预览生成；关键: ComputeChangedLineRange, GenerateEditDiffPreview
- edit/fuzzy.go — 精确匹配失败时的保守 Unicode 归一化回退（智能引号/全角/破折号，仅定位不改写区域外字节）；关键: NormalizeForFuzzyMatch
- command/ast.go — 基于 mvdan.cc/sh/v3 的 Bash AST 调用提取与重定向目标解析；关键: astInvocations, astShellRedirectionTargets
- command/semantic.go — 命令语义分析：嵌套调用、命令替换、heredoc、删除/写入目标识别、风险模式；关键: Invocations, MutationPathTargets, invocationRisk, scanHeredoc
- command/command.go — 旧式回退扫描与字面量路径工具：外部修改判定、重定向目标、写目标分类；关键: MayModifyOutsidePath, LiteralWriteTargets, IsShellNullDevice
- grep/grep.go — ripgrep 单次扫描封装：精确统计、Top-100 fileCounts 堆、offset 翻页、跳过策略；关键: Search, Find, NormalizeError, fileCountHeap
- grep/hide_windows.go / hide_other.go — ripgrep 子进程窗口隐藏（平台分叉）
- git/git.go — git porcelain/unified-diff 解析：StatusEntry、按路径拆分 diff、diff 统计、未跟踪文件合成 diff；关键: ParseStatusZ, SplitUnifiedDiffByPath, SynthesizeUntrackedDiff
- memory/memory.go — 记忆 Markdown frontmatter 解析/生成；关键: ParseMarkdown, FormatMarkdown
- memory/runtime.go — 记忆列表/读写/路径解析编排（Runtime 注入）+ 索引缓存；关键: Runtime, List, Read, Write, indexCache
- calculate/calculate.go — 确定性数学表达式求值（手写递归下降解析器）；关键: Evaluate
- scheduler/scheduler.go — 计划任务调度解析/校验/下次执行计算与限额常量；关键: ParseSchedule, DefaultSteps/MaxSteps
- service/service.go — 后台服务 rolling buffer 与长命令检测；关键: RollingBuffer, LooksLikeLongRunningService
测试:
- edit/apply_test.go / diff_test.go / fuzzy_test.go、command/semantic_test.go、grep/grep_test.go、read/read_test.go、service/service_test.go — 各自测试同目录纯算法

---

## frontend/src/ — 入口与全局

- main.js — Vue 应用入口：createApp、注册 $t、initTheme 后挂载
- App.vue — **前端唯一主组件**（~7900 行）：全局状态（无 Pinia）、Wails 事件路由（bindRuntimeEvents 按 sessionId/runId 分发）、工作区 Tab 管理、`run:stream` rAF 合帧缓冲、tool:update 120ms 缓冲、Markdown/Mermaid 渲染与 LRU 缓存、/命令系统、附件、会话持久化、任务中心、版本更新、全局快捷键；关键: bindRuntimeEvents, queueStreamDelta/flushStreamBuffer, addWorkspaceTab/closeWorkspaceTab, markdownRenderCache
- i18n.mjs — zh-CN/en-US 双语翻译源与语言工具（唯一 UI 文案来源）；关键: LOCALE_ZH_CN/LOCALE_EN_US, detectLocale, t, naiveLocale
- style.css — 全局主样式（~3700 行）：字体、CSS 变量（字号/主题 accent 色系）
- app.css — Wails 模板遗留样式，未被引用（残留）
- index.html / vite.config.js / package.json — Vite 入口 HTML；构建配置（Naive UI 自动引入、注入 `__ALLY_BUILD_VERSION__`）；依赖清单
- check_vue.mjs — 用 vue/compiler-sfc 试解析 App.vue 的诊断脚本

## frontend/src/composables/

- useToolEvents.mjs — 从 App.vue 抽出的工具卡子系统：tool:result/tool:error 处理与按工具名分发结果适配器；关键: useToolEvents(ctx), toolResultAdapters
- sakuraBreeze.mjs — 樱花特效全局开关（模块级单例 ref）；关键: useSakuraBreeze, toggleSakura

## frontend/src/components/（31 个 Vue 组件）

- AppHeader.vue — 顶部标题栏：工作区 Tab 拖拽排序（Pointer Events 模拟，非原生 DnD）、历史下拉、更新按钮、窗口控制；`--wails-draggable` 拖拽区；关键: onWorkspacePointerDown
- ChatMessages.vue — 聊天消息列表：v-memo 渲染缓存、autoFollow 自动滚动（程序化滚动落点匹配防误关）、跳底按钮、长消息折叠；关键: handleScroll, messageRenderMemo
- ComposerInfoBar.vue — 输入区信息条：模型分组下拉（按使用频次排序）、reasoning effort、git 徽标、上下文明细+compact、任务中心/文件树开关、会话导出；关键: modelGroups, exportFullSession
- SettingsModal.vue — 设置中心（General/Models/Skills/MCP/Network/About）：模型增删改+目录预设懒加载+连通测试+导入导出、MCP JSON/表单双向同步、代理检测与测试；关键: ensureModelCatalog, syncFormToJson, testModelConnection
- WorkspaceExplorer.vue — 工作区侧栏文件树+编辑器：目录懒加载、多选+右键菜单、Ace 编辑器（语法校验、MD 图片加载、预览切换）、拖拽调宽；关键: openFile, saveFile, initAceEditor, onContextMenuSelect
- ToolCallCard.vue — 通用工具调用卡：动宾动词标签、edit 多文件 split diff、create 代码预览、命令高亮、错误码本地化、validation 警告条；关键: toolVerb, highlightCommand
- AskToolCard.vue — ask 工具卡：多问题 Tab、多选+自定义回答、一次性提交；关键: answerState, submitAnswers
- SubagentInlineCard.vue — 子代理内联进度卡：步数/令牌/时长、最近 8 条子工具行；关键: recentTools
- TaskCenterPanel.vue — 任务中心：调度任务/后台服务双 Tab、buffer 预览、服务日志弹窗（1.5s 轮询）；关键: openServiceLog, refreshServiceLog
- TokenStatsModal.vue — Token 统计弹窗：概览卡、30 天堆叠柱状图、月度热力图、模型/工作区饼图（每次打开查 GetTokenStats）；关键: load, dailyBars, heatmapCells
- TokenPieChart.vue — 无依赖 SVG 环形占比图+图例；关键: segments
- DiffView.vue — diff 渲染：unified/split 双布局、聚类折叠、三来源（diffText/old+new/diffLines）；关键: parseDiffText, displayRows
- GitDiffModal.vue — git diff 弹窗：文件树+目录折叠、DiffView 预览、按 branch/状态缓存；关键: loadDiff, buildTreeRows
- ReadGroupCard.vue — read 多文件分组卡：树形前缀、行区间 chip；关键: entryChip, treePrefix
- HtmlRenderCard.vue — render_html 卡片：流式尾预览，完成后挂 sandboxed srcdoc iframe + postMessage 高度上报；关键: handleFrameMessage
- WelcomeMessage.vue — 欢迎消息：头像+版本号+工具/MCP 信息表
- AllyAvatar.vue — 品牌"竖瞳"眼睛头像：眨眼动画、随机搭话气泡、点击开樱花特效；关键: toggleSakura
- AllyWordmark.vue — "Ally" 渐变文字标（纯样式）
- SakuraBreeze.vue — 全屏樱花+草叶飘落特效层（Teleport body、单例接管）；关键: buildPetals, openEffect/closeEffect
- SplashScreen.vue — 启动闪屏：入场动画、2.8s 自动结束、点击跳过
- MessageAttachments.vue — 消息附件网格：图片/视频/音频/文本预览
- McpStatusPopover.vue — MCP 服务器状态弹层（状态点/transport/工具数/错误）
- ToolsPopover.vue — 内置工具分组弹层；关键: groupBuiltinTools, BUILTIN_TOOL_GROUPS
- FileMentionMenu.vue — 输入 `@` 文件提及候选菜单
- CommandMenu.vue — 输入 `/` 斜杠命令候选下拉
- CodeView.vue — 代码高亮预览（hljs + 32 条 LRU）；关键: highlightCached
- StreamingMarkdownBody.vue — 独立渲染作用域的 Markdown 消息体（流式不拖累整列表）；关键: html
- RenderBoundary.vue — 渲染错误边界（onErrorCaptured + 重试）
- ToolStatusIcon.vue — 工具状态 SVG 图标（✓/✕/脉冲点）
- ContextUsageInline.vue — 行内上下文百分比纯展示
- FileInfoModal.vue — 文件元信息弹窗（GetWorkspaceFileInfo + 分区展示）

## frontend/src/utils/（纯函数模块；同名 .test.mjs 为对应测试）

- toolVerb.mjs — **工具卡动词表机制**：`TOOL_VERBS` 以后端原始工具名为键映射 [进行中,完成,名词] 动词；`scheduled_task`/`service` 按 args.action 细分；查不到回落 "Using/Used"（"Used X" 问题查这里）；关键: toolVerbLabel, hasNamedVerb
- toolEventState.mjs — 工具卡事件定位与状态机：确定性 eventId、状态归一化唯一写入口；关键: toolEventId, commitToolEventMessage, setToolStatus
- toolPreview.mjs — 工具卡预览：代码截窗、HTTP 标题、大历史归档占位（displaySourceMessages）；关键: codePreviewWindow, displaySourceMessages
- toolCardSignature.mjs — 工具卡 v-memo 渲染签名（字段变化才重渲染）；关键: toolCardRenderSignature
- toolError.mjs — 工具错误正文格式化（剥离面向模型的回显）
- toolFormat.mjs — read 行范围 chip 文本
- diff.js — LCS 行级 diff、变更簇分组与 +N/-N 统计；关键: computeDiffLines, computeEditStats
- sessionStore.mjs — IndexedDB 会话快照存取与串行写队列；关键: loadSessionSnapshots, createSerialWriteQueue
- sessionState.mjs — 会话/Tab 查找、runId 终止事件接受判断、可编辑元素导航判定；关键: findSessionWorkspaceTab, shouldAcceptRunTerminal
- config.mjs — 前端配置默认值与后端配置归一化合并；关键: defaultConfig, assignConfig
- theme.mjs — 强调色主题管理（7 主题 + localStorage）；关键: initTheme, setTheme
- modelConfigIO.mjs — 模型配置导入导出：校验/去重合并/token 参数归一化；关键: buildModelConfigExport, mergeModelConfigs
- modelProviderCatalog.mjs — 模型预设目录（modelCatalog.json）查询与预设回填；关键: applyCatalogPreset, providerCatalogOptions
- modelUsage.mjs — localStorage 记录模型使用频次（下拉排序）
- planPanel.mjs — 计划面板条目编号与 in_progress 居中滚动量
- fileInfo.mjs — 文件信息分区构建（大小/时间/哈希），弹框与面板共用
- shellHighlight.mjs — 命令输出 shell 语法高亮；关键: highlightShellCommand
- htmlRender.mjs — render_html 的 srcdoc 文档构建（CSP+postMessage 高度）与钳制
- markdownPreview.mjs — Markdown 内本地图片相对路径解析
- versionCheck.mjs — 语义化版本比较；关键: isNewerReleaseVersion
- wailsEvent.mjs — 解包 Wails v3 Events.On 回调外壳；关键: unwrapWailsEvent
- download.mjs — 文本导出（Wails 保存对话框，回退 blob）
- clipboard.mjs — 统一剪贴板复制（API 失败回退 execCommand）
- format.mjs — token 数/整数紧凑格式化（fmtTokens/fmtNum）
- buildVersion.js — 读取注入的构建版本号（缺省 dev）
- ascii.js — 5 行块状字符 ASCII 横幅渲染

## frontend/src/games/（协作休息区 H5 游戏）

- GamePanel.vue — 游戏弹窗 UI：建房/加入、IP 选择、邀请码复制、棋盘/牌桌渲染；房主权威模式（本机 applyAction 后脱敏 syncState 广播）；关键: GetNetworkInfo, StartServer
- rules.mjs — 纯规则引擎：五子棋/围棋/象棋/斗地主状态创建与走子校验；关键: createState, applyAction, GAME_META
- connection.mjs — 联机连接层：`ALLY-GAME-1|host|port|roomId|secret` 邀请串、GameConnection WS 客户端（seq 去重、AES-GCM 加密）；关键: GameConnection, parseInvite
- crypto.mjs — 联机加密基元：SHA-256 派生房间 AES-GCM 密钥、加解密 JSON 包、访问令牌；关键: deriveKey, encryptJSON, accessToken

## frontend/src/data/

- modelCatalog.json — 模型目录生成产物（勿手改）：models.dev → docs/model_api.json → scripts/generate-model-catalog.mjs
- eyeLines.mjs — Ally 眼球搭话台词池（EYE_STYLES 按风格分组）

---

## 构建与脚本

- Taskfile.yml — wails3 任务定义
- scripts/generate-model-catalog.mjs — 从 docs/model_api.json 生成前端模型目录
- scripts/open-ally-macos.command — macOS 开发启动脚本
- .github/workflows/build.yml — 发布构建（注入 ALLY_BUILD_VERSION，产出三平台包）
- build/ — 各平台打包资源（图标、Info.plist、nsis/msix/nfpm/appimage 配置）
- docs/ — 官网落地页与截图；docs/model_api.json 为 models.dev 静态快照
- frontend/check_vue.mjs — SFC 诊断脚本（见上）
