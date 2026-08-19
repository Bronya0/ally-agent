# Code Graph: ally-agent

## Language and Build

- language: Go 1.25.5 (go.mod, module ally-dev)
- language: TypeScript / Vue 3 (frontend/package.json + Vite)
- build: Wails v3 (`wails3 build` / `wails3 dev`), GitHub Actions 发布构建 (.github/workflows/build.yml)

## Module Hierarchy

- [entry] main.go — Wails v3 应用入口；`NewApp()` 装配、嵌入 `frontend/dist` 资源、更新重启 helper、窗口选项
- [service] internal/app/ — Agent 核心编排（唯一后端包，也是 Wails 绑定面；按前缀分层：无前缀=核心、prov_=provider、host_=宿主桥接、orch_=工具编排、biz_=业务模块、infra_=工具基础设施）
  - app.go — 编排核心：`App` 长生命周期状态、`ConfigState`/`ChatRequest` 等 DTO、`runChat` 聊天循环、`executeTool` 分发、`StartChat`/`CancelRun`/会话生命周期、ask/wait、前端兼容绑定 [Lines: 2394]
  - prov_model.go — provider 流式适配：`streamOpenAIChat`/`streamOpenAIResponses`/`streamAnthropicMessages`、SSE 解析、重试与多 key 故障切换、`modelStreamEvent`/`modelStreamResult` [Lines: 1706]
  - prov_proxy.go / prov_proxy_windows.go / prov_proxy_darwin.go / prov_proxy_other.go / prov_proxy_scutil.go — 代理感知 HTTP client、系统代理检测、transport 缓存失效
  - host_desktop.go — Wails 生命周期、窗口创建、系统对话框、`wailsAppHandle` 注入
  - host_events.go — `eventSink` 事件边界（`App.emit` 唯一出口）、`fanoutEventSink` 广播
  - host_network.go — 网络事件出口：SSE `/events`、轮询 `/poll`、环形缓冲、WS 预留（默认关闭，`ALLY_NETWORK_EVENTS=1` 启用）
  - host_tray.go — 系统托盘
  - host_taskbar_windows.go / host_taskbar_other.go — 任务栏进度与窗口闪烁
  - host_process_windows.go / host_process_other.go — 子进程窗口与进程树控制
  - host_notifications.go — 桌面通知服务注入（`SetNotifier`）与任务完成/出错/取消提示音 `notifyCompletion`（Windows 内置 toast 事件音，其他平台默认音）
  - host_update_relaunch_darwin.go / host_update_relaunch_other.go — 更新后重启辅助
  - biz_config.go — 配置域：`mergeConfig`、`SaveConfig`/`ReloadConfig`、key 池归一化、`effectiveConfig`、`pathRuntime`、`appDataDir` [Lines: 512]
  - biz_sessions.go — 会话索引/快照/历史持久化：gzip 历史读写、裁剪、会话文件原子替换 [Lines: 1138]
  - biz_context.go — 请求消息组装：`buildMessages`、系统上下文、todo 状态、附件上下文、上下文 Token 统计与缓存 [Lines: 1071]
  - biz_prompt.go — 系统提示词管线：`buildSystemPromptParts`、skill 元数据、全局记忆索引、AGENTS/CLAUDE 加载
  - biz_workspace.go — 工作区文件列表、workspace map、路径搜索索引、gitignore 规则解析 [Lines: 908]
  - biz_workspace_editor.go — UI 文件浏览器专用的受限完整文本读写（2 MiB 上限、版本冲突校验、原子写入、与 Agent 文件操作共用锁）
  - biz_skills.go / biz_builtin_skills.go — skill 发现/加载/启停（目录、standalone md、内置嵌入）
  - biz_mcp.go — MCP 生命周期：`McpManager` 连接/重连/工具发现、前端绑定、MCP 工具执行 [Lines: 845]
  - biz_update.go — 自更新：发布检查（Atom feed）、下载、解压、应用、回滚、跳过列表 [Lines: 1462]
  - biz_stats.go — 异步 Token 统计落盘与查询
  - biz_project_context.go — AGENTS.md/CLAUDE.md 读取、CODEGRAPH 提示片段
  - orch_subagent.go — 子代理执行循环：`executeDelegate`、提示词构建、事件发射、记录清理 [Lines: 614]
  - orch_file_ops.go — create/delete/run-command 编排、shell 检测（Git Bash 发现）、危险路径保护、路径安全解析 [Lines: 886]
  - orch_http.go — HTTP/Web fetch 工具、每主机限速、结果清洗（`truncateRunes`/`normalizeWhitespace`）
  - orch_read.go — 批量读取、文档抽取、行预览渲染、run 级读取缓存
  - orch_edit.go — 模型编辑执行（原子提交/回滚）
  - orch_edit_plan.go — 编辑批次归一化（`planLocalEditBatch` 唯一边界）
  - orch_validation.go — edit/create 写入后的低成本语言校验（Python/Go/JS/TS/Vue/Java/JSON），以单个 `validation` 字符串回填模型
  - orch_batch_policy.go — 工具批次冲突/屏障策略（`detectToolBatchConflicts`、`isOrderedFileMutationTool`）
  - orch_command_safety.go — 命令安全边界（`checkCommandSafety`）
  - orch_git.go — git 状态/diff 工具编排
  - orch_remote.go — SSH 远程工具编排
  - orch_services.go — 后台服务进程管理
  - orch_scheduler.go — 计划任务调度（cron/interval/once）
  - orch_memory.go / orch_grep.go — 全局记忆与 grep 编排
  - infra_bridges.go — 类型别名（`CalculateRequest` 等）、`limitedBuffer`、路径/哈希薄包装 [Lines: 345]
  - infra_result.go — 工具结果信封 `toolResult`、模型压缩、摘要
  - infra_stream.go — 流式事件节流（`runStreamDeltaEmitter`、tool 进度跟踪）
  - infra_shell_env.go — 登录 shell 环境探测（macOS/Linux PATH 导入）
  - *_test.go — 单元/集成测试（app_test.go、orch_test.go、prov_model_keys_test.go 等）
- [infra] internal/tools/ — 工具纯算法层（不依赖 *App；每个子目录一个工具）
  - calculate/ — 数学表达式求值
  - command/ — 命令安全解析（重定向/路径/风险模式）
  - edit/ — 编辑 Diff 与变更范围算法
  - git/ — git porcelain 与 unified-diff 解析
  - grep/ — ripgrep 单次扫描与结果归一化（精确统计、Top-100 `fileCounts`、`offset` 翻页、`offsetExhausted` 越界信号与全仓库跳过策略）
  - memory/ — 记忆 Markdown frontmatter 解析
  - pathutil/ — 工作区路径解析与安全检查（Runtime 注入）
  - read/ — 文本读取、版本令牌、原子写入、文档文本抽取
  - scheduler/ — 计划任务调度解析与下次执行计算
  - service/ — 后台进程 rolling buffer 与长命令检测
  - shared/ — `CodedError` 与内置工具 schema（`Builtins()`）
- [infra] internal/builtin_skills/ — 内置 skill 嵌入资源（go:embed `skills/<name>/SKILL.md`）
- [ui] frontend/src/ — Vue 3 单页桌面 UI（Naive UI）
  - App.vue — 唯一主组件：状态、Wails 事件路由、工作区 Tab、流式缓冲、Mermaid 渲染 [Lines: 296KB]
  - components/ — AppHeader、ChatMessages、WorkspaceExplorer、SettingsModal、ToolCallCard、SubagentInlineCard、TaskCenterPanel、TokenStatsModal 等组件；WorkspaceExplorer 按需挂载，目录懒加载并在选择文件后覆盖内容区编辑/高亮预览
  - utils/ — sessionStore、toolPreview、diff、htmlRender、modelConfigIO、i18n 等纯函数模块（含 .test.mjs）
  - i18n.mjs — zh-CN / en-US 双语源
  - data/modelCatalog.json — 模型目录（400KB）
- [config] frontend/vite.config.js / Taskfile.yml — 构建配置
- [build] scripts/ — 模型目录生成、macOS 打开脚本
- [build] .github/workflows/ — 发布构建与产物上传

## Key Types / Interfaces

- [struct] App — 后端长生命周期状态：runs/histories/mcpManager/subRuns/todos/缓存与互斥
- [config] ConfigState — 全部用户配置 + 会话级 overlay（key 池、代理、模型、技能开关、背景等）
- [struct] ChatRequest — StartChat 请求：消息数组或单条消息 + 附件 + 配置 overlay
- [struct] modelStreamResult / modelStreamEvent — provider 流式结果与逐事件回调（content/reasoning/toolCalls/usage）
- [struct] toolResult — 工具结果信封 `{ok, data, error, errorCode, details}`
- [struct] McpManager — MCP 服务器连接/重连/工具发现/函数名映射
- [struct] scheduledTaskManager — 计划任务调度状态机（cron/interval/once、串行执行）
- [struct] statsRecorder — 异步 Token 统计队列与落盘
- [struct] SubagentRun — 子代理运行记录（状态、步数、工具事件、文件读写）
- [struct] TodoEntry — todo 列表条目
- [interface] eventSink — 事件边界（App.emit 唯一出口，测试可注入；Wails/网络均实现该接口）
- [interface] pathutil.Runtime — 路径工具的宿主抽象（AppDataDir 注入）
- [struct] limitedBuffer — 并发安全限长字节缓冲（命令输出捕获）
- [typedef] openai.Tool — LLM 工具 schema（OpenAI function 格式，全部内置工具）
- [struct] SkillDefinition — skill 元数据（name/description/whenToUse/source/embeddedContent）

## Call Flow / Data Flow

- [init] main() → backend.NewApp() // Wails 应用装配，注册 App 服务
  - [init] NewApp → ensureInitialized() // 加载 ~/.ally_agent/config.json，建 histories/sessions/memories 目录
  - [init] NewApp → NewMcpManager() // MCP 管理器装配
    - [ext] McpManager.StartAll → mcp-go client // stdio/SSE/streamable-HTTP 连接
- [call] frontend → app.StartChat(ChatRequest) → runChat() [service] // 用户发消息
  - [call] runChat → buildMessages() [service] // 系统提示 + 历史 + 当前消息
  - [call] runChat → streamModelResponse() → streamOpenAIChat/Responses/AnthropicMessages [adapter]
    - [ext] stream* → proxyHTTPClient().Do(req) // SSE 流式请求
    - [event] stream* → onEvent(modelStreamEvent) // 内容/推理/工具增量回调
  - [event] runChat → a.emit("run:stream" / "run:image" / "tool:result") // 经 eventSink → fanout → Wails/网络 → 前端
  - [call] runChat → executeTool() // 模型请求执行工具
    - [call] executeTool → orch_*.go / biz_*.go 编排 [service]
      - [ext] orch_read/orch_file_ops → internal/tools/* // 纯算法层
  - [data] 工具结果 → compactToolResultForModel() // 压缩后回填模型上下文
  - [call] runChat → saveHistory() [service] // 裁剪 + 清洗 → gzip 落盘
- [call] executeTool(subagent) → executeDelegate() [service] // 子代理独立循环
  - [call] executeDelegate → streamModelResponse() // 子代理 LLM 轮
  - [call] executeDelegate → executeTool() // 子代理工具（并发上限 4）
  - [event] executeDelegate → a.emit("sub:spawn"/"sub:step"/"sub:done")
- [call] scheduledTaskManager.run → executeDelegate() [service] // 计划任务隔离上下文
- [call] app.SaveConfig → mergeConfig() [service] // 配置合并 + key 归一化

## Inter-Module Dependencies

- main.go → internal/app // Wails 服务绑定与嵌入资源
- internal/app → internal/tools/* // 编排层调用纯算法（read/grep/edit/command/pathutil/shared 等）
- internal/app → internal/builtin_skills // go:embed 内置 skill 内容
- internal/app → github.com/wailsapp/wails/v3 // host_*.go 宿主桥接（仅这些文件）
- internal/app → github.com/sashabaranov/go-openai / openai-go / anthropic-sdk-go // prov_model.go provider 适配
- internal/app → github.com/mark3labs/mcp-go // biz_mcp.go MCP 客户端
- internal/app → github.com/robfig/cron // orch_scheduler.go
- frontend/src → internal/app // Wails bindings（StartChat/SaveConfig/GetConfig + run:* 事件）
- internal/app → internal/app // 同包跨文件调用（biz_context ↔ biz_sessions 等，无包边界）

## Hot Paths / Files to Focus

- [***] internal/app/app.go — 聊天循环与工具分发核心（重构后 2394 行，AGENTS.md 规定保留编排域）
- [***] frontend/src/App.vue — 前端唯一主组件（296KB），事件路由与全部 UI 状态
- [**] internal/app/prov_model.go — provider 适配与流式解析（1706 行，SSE 容错热点）
- [**] internal/app/biz_sessions.go — 会话/历史持久化（1138 行，gzip 读写 + 裁剪）
- [**] internal/app/biz_context.go — 消息组装与上下文统计（1071 行）
- [**] internal/app/orch_subagent.go — 子代理执行循环（614 行）
- [*] internal/app/orch_edit.go / orch_file_ops.go — 文件变更与命令安全边界
- [*] internal/app/biz_prompt.go — 系统提示词管线（skill/记忆/AGENTS 注入）
- [*] internal/app/biz_mcp.go — MCP 生命周期与工具暴露
- [*] internal/tools/read — 读取/哈希/原子写纯算法（read/edit 契约基础）
