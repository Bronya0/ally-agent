# Ally — AGENTS.md

This file is read by AI coding agents. Keep it current when the app architecture, tool contracts, prompt pipeline, or UI workflows change.

> 本文件是**指导性文档**：记录"应该如何修改"的规范与边界。当前系统状态（目录结构、模块层级、调用流、数据流）以根目录 `CODEGRAPH.md` 和源码为准；本文件只保留规范类内容，状态描述一律指向 `CODEGRAPH.md` 或具体源文件，避免双源漂移。

---

## Build & Run Commands

| Command | Description |
|---------|-------------|
| `wails3 dev` | Run the desktop app in development mode with hot reload |
| `wails3 build` | Build a distributable desktop binary (also the only verification command). On macOS this emits only the raw `bin/Ally` executable — the Dock/Finder icon lives in the `.app` bundle, so use `wails3 task darwin:package` (or `darwin:package:universal`) to produce `bin/Ally.app` with `Contents/Resources/icons.icns`; Windows embeds the icon into the `.exe` via the generated `.syso`, so `wails3 build` alone already carries the icon there |

## Git Convention

When developing Ally itself, push with the author set to `ally agent`:

```powershell
git add -A
git -c user.name="ally agent" -c user.email="ally@agent.dev" commit -m "..."
git push origin main
```

This uses `-c` to override the commit author per-command only, leaving the developer's global Git config unchanged.

### Committing partial file changes from the agent shell

When splitting one file's diff across multiple commits (e.g. `i18n.mjs` containing strings for two features), do NOT drive `git add -p` through piped stdin — the agent shell is non-interactive and the piped answers are swallowed, leaving the index in a partial state. Instead, pre-split hunks with a patch file and apply them straight to the index:

1. `git diff -- <file> > full.patch`, then split hunks with a small Python script (or keep/drop whole hunks; no offset fixing needed when dropping tail hunks).
2. `git apply --cached --check split.patch` to validate, then `git apply --cached split.patch` to stage. The workspace file already contains the full change, so the patch applies to the index (HEAD state), never to the working tree — omitting `--cached` fails with `patch does not apply`.
3. Commit, then repeat for the next feature's hunks, and `git add` whole files that belong to a single feature.

Shell gotcha: in this environment the `command` tool's heredoc layer eats backslash escapes, so a Python script containing a backslash character literal (e.g. a line-ending check written as an escape sequence) arrives with an unterminated string and raises `SyntaxError`. Prefer backslash-free formulations (e.g. test `line[0]` against `+` or a space, or use `chr(92)`), or write the script to a file first and run `python file` so the script body is never passed through a heredoc.

## Release Process

Git tags and GitHub Releases are the source of truth for Ally versions. Release tags use `vMAJOR.MINOR.PATCH`; `.github/workflows/build.yml` injects the published tag through `ALLY_BUILD_VERSION`. Do not treat `frontend/package.json`'s `0.0.0` as the app release version and do not change it only for a release.

1. Synchronize and identify the current release:
   - Require a clean worktree on `main` and make sure it matches `origin/main`.
   - Run `git fetch origin --tags --prune`.
   - Inspect `git tag --sort=-v:refname` and the latest GitHub Release before choosing the next semantic version.
2. Choose the next version and prepare the release notes:
   - Use a patch bump for compatible fixes or maintenance, a minor bump for backward-compatible features, and a major bump for breaking changes.
   - Summarize user-visible changes from `git log <previous-tag>..HEAD`; do not claim changes that are not present in that range.
   - End the notes with `**Full Changelog**: https://github.com/Bronya0/ally-agent/compare/<previous-tag>...<new-tag>`.
3. Verify the exact commit that will be released:
   - `wails3 build`
4. Commit the release-related repository changes and push `main` to `origin`. Recheck that the worktree is clean and local `HEAD` equals `origin/main`.
5. Publish a non-draft GitHub Release targeting `main`, with tag `<new-tag>`, title `Ally <new-tag>`, and the prepared notes. Authentication must come from GitHub CLI login or a `GITHUB_TOKEN` environment variable with repository contents write permission. Never place a token value in repository files, release notes, scripts, or copied command text.

Publishing the Release triggers `.github/workflows/build.yml`, which builds and attaches the Windows x64, Linux x64, and macOS universal packages.

---

## Repository Layout

```
├── main.go                   # Wails v3 应用入口、窗口选项和 App 绑定
├── internal/
│   ├── app/                  # Agent 核心与编排（见下方文件命名约定）
│   ├── game/                 # 与 Agent 隔离的局域网游戏 WS 服务与房间生命周期
│   ├── builtin_skills/       # 内置 skill 嵌入资源（go:embed）
│   ├── host/                 # 宿主事件适配（eventSink → Wails v3 / 网络）
│   ├── provider/             # Provider 格式、Base URL 和 token 参数归一化
│   ├── platform/process/     # 跨平台子进程窗口与进程树控制
│   └── tools/                # 工具纯算法层（无 *App / ConfigState 依赖）
├── frontend/                 # Vue 3 前端和生成的 Wails bindings
├── scripts/                  # 构建与打包脚本
├── third_party/              # 第三方许可证文件
└── build/                    # 构建资源和平台元数据
```

### `internal/app/` 文件命名约定

`internal/app/` 下的文件按层级前缀命名，前缀直接表明文件的职责归属：

| 前缀 | 层级 | 职责 |
|------|------|------|
| 无 | 核心 | `app.go` 持有 chat loop、`*App` 长生命周期状态和 `executeTool()` dispatch |
| `prov_` | Provider 适配 | OpenAI/Anthropic 流式适配、代理配置 |
| `host_` | Host 桥接 | Wails 生命周期、窗口、对话框、eventSink 边界、网络事件出口（SSE/轮询/WS 预留）、系统托盘、子进程与任务栏控制、桌面通知（`host_notifications.go`，任务完成/出错/取消提示音） |
| `orch_` | 工具编排 | 绑定 `internal/tools/` 纯算法到 `*App` 状态：路径解析、并行调度、互斥锁、原子写入、批次策略、安全边界 |
| `infra_` | 工具基础设施 | 跨编排共享：命令环境、结果信封与压缩、流式节流、DTO 别名与归一化 |
| `biz_` | 业务模块 | 独立功能：skills 发现与加载、系统提示词构建、项目上下文、MCP 生命周期、版本检查、异步 Token 统计 |

约定：
- `orch_<name>.go` 对应 `internal/tools/<name>/` 的纯算法，两者构成一个工具的完整实现。
- 测试文件跟随被测文件：`<prefix>_<name>_test.go` 或跨编排的 `orch_test.go`。
- 新增工具编排写 `orch_<name>.go`；新增 host 能力写 `host_<name>.go`；新增独立业务写 `biz_<name>.go`。

### `internal/tools/` 纯算法层

每个子目录是一个工具的纯算法实现，不依赖 `*App`、`ConfigState` 或任何 app 包符号：

- `calculate/` — 纯数学计算
- `command/` — 命令安全解析：以 `mvdan.cc/sh/v3` Bash AST 为主，兼容 PowerShell/cmd、路径与风险语义
- `edit/` — 编辑 Diff、变更范围
- `git/` — git porcelain / unified-diff 解析
- `grep/` — ripgrep 封装与结果归一化
- `memory/` — 记忆 Markdown frontmatter 解析 + 编排（Runtime 注入）
- `pathutil/` — 工作区路径解析与安全检查（Runtime 注入）
- `read/` — 文本读取、版本令牌、原子写入（不解析办公/PDF 文档）
- `scheduler/` — 计划任务调度解析、校验与下次执行计算
- `service/` — 后台进程 rolling buffer 与长命令检测
- `shared/` — 跨工具编码错误（`CodedError`）与内置工具 schema

需要访问 `*App` 状态的编排逻辑放在 `internal/app/orch_*.go`，纯算法放在这里。

---

## High-Level Architecture

Ally is a Wails v3 desktop AI coding agent.

The backend is a Go application bound into the frontend through Wails. The frontend is a Vue 3 single-page desktop UI using Naive UI. The LLM-facing core is provider-neutral: `internal/app/prov_model.go` owns the provider wire adapters, while `internal/provider` owns provider-format and default-value normalization.

Core runtime flow:

1. Frontend calls `StartChat(ChatRequest)` through Wails.
2. Backend creates a cancellable run and starts `runChat()`.
3. `buildMessages()` constructs the request context: core system prompt, workspace map, persisted history, current user message, and attachments. The workspace map is frozen per session at the first request (labeled as a snapshot; `list_files` returns live state) so the request prefix stays byte-stable and provider prompt caches survive across runs. The context-budget tail injection is disabled (cache-first); if re-enabled it is rebuilt each request and never persisted, and the Anthropic adapter places its prompt-cache breakpoints before transient tail items so they cannot invalidate the cached prefix. The todo list is not injected per step: every `plan` tool call returns the full updated list in its model-facing tool result, which is persisted into history and stays visible across steps and turns. When a user starts a new turn, the current in-memory plan is attached once to the first model request as transient context; subsequent tool-loop requests do not repeat it, and it is not persisted.
4. `buildToolsWithMcp()` combines static built-in tools with connected MCP tools.
5. `streamModelResponse()` dispatches to the configured provider adapter.
6. Streaming deltas and tool-call updates are emitted to the frontend through runtime events.
7. Non-file tool calls run concurrently with a max concurrency of 4; built-in file mutations run afterward in `toolCallIndex` order. `wait`, `ask`, and `suggest` must each be the only call in their tool batch (see batch policy).
8. Tool results are appended to same-turn model context and the loop repeats until no tool calls remain or `maxAgentSteps` (9999) is reached; a single successful `suggest` call also ends the run.
9. Saved session history keeps tool calls and their model-facing tool results verbatim; system messages and image payloads are dropped (see `sanitizeHistoryMessages()`).

Connected MCP tools are sorted by server, tool name, and function name before being exposed so request tool ordering remains deterministic across turns.

---

## Backend State

`App` 长生命周期状态、`ConfigState` 字段定义、关键配置行为（`mergeConfig` 合并规则、多 key 优先级故障转移、代理 fail-closed、reasoningEffort 归一化）见 `CODEGRAPH.md`「核心数据流」与源码 `app.go` / `prov_model.go`。修改配置行为时以 `mergeConfig()` 与 `normalizeAPIFormat()` / `normalizeReasoningEffort()` 为唯一边界。

---

## Model Provider Layer

Provider adaptation lives in `prov_model.go`.

Supported API formats:

| `apiFormat` | Adapter | Default Base URL |
|-------------|---------|------------------|
| `openai_chat` | `streamOpenAIChat` | app default OpenAI-compatible base URL |
| `openai_responses` | `streamOpenAIResponses` | `https://api.openai.com/v1` |
| `anthropic_messages` | `streamAnthropicMessages` | `https://api.anthropic.com` |

`normalizeAPIFormat()` accepts common aliases such as `chat`, `responses`, `anthropic`, and `claude_messages`.

各适配器的请求/响应细节（token 参数、tool-call 合并、stop_reason 处理、prompt cache、thinking 参数）见 `prov_model.go` 内注释与 `prov_model_test.go`；修改时保持 `prov_model.go` 为唯一 provider 边界，不得把 provider 特有形状泄漏进 `app.go` 或工具编排层。

### 模型预设目录（Model Catalog）

“添加模型”弹窗的 provider/模型预设来自以下链路：

```text
https://models.dev/api.json  →  docs/model_api.json →（scripts/generate-model-catalog.mjs）→  frontend/src/data/modelCatalog.json
```

- `docs/model_api.json` 是 **models.dev（AI SDK 官方模型注册表）的静态快照**，约 192 个 provider，3-4 MB。格式即 models.dev 的 API 格式（provider 含 `npm`/`api`/`name`/`doc`/`models`；模型含 `tool_call`/`limit`/`modalities`/`reasoning_options` 等）。
- `frontend/src/data/modelCatalog.json` 是**生成产物**，不要手改（下一轮生成会覆盖）；生成脚本只保留 `npm` 为 `@ai-sdk/openai-compatible` / `@ai-sdk/openai` / `@ai-sdk/anthropic` 的 provider，且模型须未废弃、支持工具调用、支持文本输出。
- 前端在 `SettingsModal.vue` 的 `ensureModelCatalog()` 中懒加载 `modelCatalog.json`，仅用于填充 providerName/apiFormat/baseUrl/contextWindow/maxTokens/reasoningTag；API Key 仍需用户自填。
- **更新方式**：下载最新源并重新生成：

```bash
curl -o docs/model_api.json https://models.dev/api.json
node scripts/generate-model-catalog.mjs
```

> 注意：`models.dev` 在国内网络可能超时，需代理访问。

---

## Chat Loop

`StartChat()` registers a run and starts `runChat()` in a goroutine.

`runChat()`:

- builds messages with `buildMessages()`
- builds static + MCP tools with `buildToolsWithMcp()`
- streams model output and emits: `run:start`, `run:llm_wait`, `run:stream` (merged content + reasoning deltas), tool events, `run:done` / `run:error`
- tracks usage with provider-reported usage when available, otherwise estimates
- executes non-file tool calls concurrently with a semaphore cap of 4
- rejects mixed tool batches containing `wait`; a valid `wait` batch contains exactly one call
- keeps the first same-path mutation group entry (later same-path calls are rejected with `E_WRITE_BATCH_CONFLICT`), then executes the surviving built-in file mutations in `toolCallIndex` order under `fileOpsMu`
- appends compact model-facing tool results
- replaces tool-call arguments that are not valid JSON (stream cut off mid-arguments) with the `truncatedToolCallArguments` marker in `normalizeToolCalls()`, and `sanitizeHistoryMessages()` applies the same repair when loading histories written before the fix — providers that parse `tool_calls` arguments server-side (DeepSeek among them) otherwise reject every request for the session with 400
- collapses tool-call function names that are whole-number repetitions of a shorter unit (`collapseRepeatedName()` in `normalizeToolCalls()` and in `sanitizeHistoryMessages()`): relays that re-send the full function name in every streaming delta produced names like `http_request` repeated N times, which failed dispatch as `unknown tool` and then poisoned the session the same way once replayed. `mergeToolCallDeltas()` now dedupes re-sent id/name deltas (`mergeRepeatedStringDelta`) and skips exactly duplicated argument chunks, so the artifact is no longer produced in the first place
- enforces the tool-call pairing invariant through `repairDanglingToolCalls()` inside `sanitizeHistoryMessages()`: every persisted assistant `tool_calls` entry must be answered by exactly one tool message carrying its ID. Unanswered (dangling) tool_calls are stripped with the assistant text kept, and orphan or duplicate tool messages are dropped — a run that panicked between appending the assistant tool_calls message and its tool results would otherwise poison the saved history with a 400-per-request session. `executeTool()` additionally converts panics into `E_TOOL_PANIC` tool errors (logging the stack) so a handler crash neither kills the desktop process nor unwinds `runChat` into the dangling state
- loops until no tool calls remain

Tool result channels:

- Frontend receives full JSON via `tool:result` / `tool:error`.
- Model context receives compacted JSON from `compactToolResultForModel()`.
- `read` content is intentionally not compacted so its displayed line numbers remain available for `edit.lineRange` selection.

`saveHistory()`:

- drops system messages
- keeps tool calls and their model-facing tool results verbatim
- converts multi-content messages to text summaries for persistence
- trims history by an estimated-token budget at user-message boundaries (see `trimSavedHistory()`), not a fixed message count

Turn-level retry and interruption（`runChat` 每个 LLM step 的循环）:

- 每个 step 最多重试 3 次（`maxTurnRetries`）；遇到 `shouldRetryLLMError()` 判定的可重试错误（超时/断流/5xx/常见网络中断串/空模型响应）且未达上限时，按 `llmRetryDelay()`（500ms×2^n，上限 10s）退避后整轮重发，并发射 `run:retry` 事件（payload 含 `attempt/maxAttempts/error/waitMs/reason:"stream_interrupted"/discardCurrentResponse:true`）。
- OpenAI-compatible Chat 的 `finish_reason` 会保留在 `modelStreamResult.StopReason`；`length` 和 `content_filter` 不会被当作正常完成，而是以明确错误结束。一个没有正文、工具调用或图片的空响应也不会直接 `run:done`，而会进入上述 step 重试。
- provider 返回 400 且本 step 未 sanitize 过：`sanitizeHistoryMessages(messages)` 修复上下文（若缩短则重建 requestMessages，含重新附加 plan），发 `run:retry {reason:"context sanitized after provider 400"}` 后 continue（不占重试次数）。
- 重试等待期间 ctx 被取消：追加 `cancelledTurnMarker()` 并以 `run:error`(kind=cancelled) 返回。
- provider 适配器内还有一层 pre-first-event 重试（`shouldRetryLLMError` + `emitLLMRetryEvent`，多 key 时被关闭），其 `run:retry` 事件带 `keyIndex/totalKeys`。
- `run:inject` 事件在排队用户消息进入上下文时发射（run 工作期间用户发消息，当前工具批完成后注入，下一模型请求可见）。
- 图片生成（`run:image`）通过图片注入 user 消息进入模型上下文，仅存活单轮。

中断/取消语义：ESC 或 Stop 取消时 `ctx` 被取消，`runChat` 通过 deferred 检查点保存已完成的工具调用/结果与当前提问（见 `cancelledTurnMarker()`），并在历史中追加 `<ally-cancelled>` user-role 控制标记，让下一轮模型能区分"用户手动中断"与 provider 报错。流式中断（`stream_interrupted`）整轮重试时会丢弃当前回合的流式输出（前端 `discardInterruptedResponse`）；400-sanitize 修复路径保留已流出的 assistant 文本。

---

## System Prompt Pipeline

`defaultSystemPrompt()` delegates to `buildSystemPromptParts()`.

Prompt parts include:

- core agent rules and tool usage policy
- enabled skill metadata
- global memory index from `~/.ally_agent/memories/*.md`
- project/user instructions loaded from AGENTS/CLAUDE files
- custom prompt from settings

AGENTS/CLAUDE loading:

1. User-level: `~/.agents/AGENTS.md`, fallback `~/.agents/agents.md`
2. Workspace-level: `<workspace>/AGENTS.md`, `CLAUDE.md`, `agents.md`, `claude.md`
3. Files are concatenated with `<!-- From: path -->` headers and deduplicated by absolute path.

Skill metadata: only enabled skills are listed; full skill Markdown is not injected by default; disabled skills are omitted from metadata and cannot be loaded through the `skill` tool.

Global memory metadata: `~/.ally_agent/memories/` is created on startup; files with YAML frontmatter `description` are scanned into a separate "全局记忆索引" system prompt part; the index contains only paths and descriptions, full content is loaded through the regular `read` tool (`~/.ally_agent` is a whitelisted write root, so `create`/`edit`/`delete` manage memory files directly).

---

## Skills Architecture

### Discovery

`ListSkills()` scans:

- user skills: `~/.agents/skills/`, `~/.claude/skills/`
- project skills: `<workspace>/.agents/skills/`, `<workspace>/.claude/skills/`
- built-in skills: embedded into the binary via `go:embed` under `internal/builtin_skills/skills/<name>/SKILL.md`

The `.claude/skills/` paths follow the Agent Skills open standard (agentskills.io) and Claude Code convention, so skills dropped into those directories by other tools are discovered by Ally without changes. The Ally-native path (`.agents/skills`) is scanned first, so on a name conflict the Ally-native skill wins via the `seen`-map dedup; the Claude-convention path is a fallback. Scope precedence overall: project > user > extra > builtin, matching `buildSkillListingMeta`. Built-in skills ship with Ally, require no files on disk, cannot be deleted by the user, and are still subject to `disabledSkills`.

Supported layouts:

- directory skill: `skill-dir/SKILL.md`
- standalone Markdown skill: `skill-name.md`
- built-in skill: `internal/builtin_skills/skills/<name>/SKILL.md` (embedded, parsed via `parseSkillContent`)

`parseSkillFile()` reads YAML frontmatter fields:

- `name`, `description`, `type`
- `whenToUse` (Ally-native) or `when_to_use` (Agent Skills open standard / Claude Code) — both accepted, Ally-native spelling wins when both are present

Other Agent Skills fields (`disable-model-invocation`, `user-invocable`, `allowed-tools`, `context`, `paths`, etc.) are ignored. If no usable frontmatter is found, the filename or parent directory becomes the skill name.

Built-in skill loading:

- `builtinSkillEntries()` in `internal/app/builtin_skills.go` walks the embedded `skills/` tree and returns `SkillDefinition` entries with `Source="builtin"` and `embeddedContent` populated.
- `readSkillContent()` returns `embeddedContent` directly for built-in skills; user/project skills still go through `os.ReadFile`.
- Adding a built-in skill only requires creating `internal/builtin_skills/skills/<name>/SKILL.md` with YAML frontmatter — no other change is needed.

Currently shipped built-in skills (details in each SKILL.md):

- `playwright-cli` — drives a real browser through the `playwright-cli` npm package via `command`; defers command/parameter details to `playwright-cli --help`.
- `codegraph` — generates or incrementally updates `CODEGRAPH.md` as a per-file feature index (feature→file lookup table, one line per file with key symbols, parallel subagent analysis for large projects, capped at 800 lines, in-place incremental updates via `edit`).

### Enable/Disable

Skill settings are controlled by `disabledSkills` in `ConfigState`.

- Default state: all discovered skills are enabled.
- `DeactivateSkill(name)`: adds the skill name to `disabledSkills` and writes config.
- `ActivateSkill(name)`: removes the skill name from `disabledSkills`, reads the skill file, and returns a rendered `<skill-loaded>` block.
- `ClearSkills()`: disables all currently discovered non-builtin skills and writes config. Built-in skills are always-on (the Settings UI locks their toggles), so the sweep must not disable them; replacing the list also re-enables builtin entries left behind by older configs.
- `GetActiveSkills()`: returns the currently enabled skill names.

Disabled skills remain visible in Settings but are not injected into the system prompt metadata, are not available through the `skill` tool, and are marked off in Settings → Skills.

### Full Skill Loading

Full skill content is loaded only when explicitly requested:

- user slash command: `/<skillname>` or `/skill:<name>`
- model tool call: `Skill({skill, args})`, only if enabled

The loaded content is wrapped as:

```xml
<skill-loaded name="..." source="..." dir="..." args="...">
...
</skill-loaded>
```

Settings → Skills toggles enable/disable only; slash-command activation loads the full skill and starts a model turn with that loaded block.

---

## Tool Architecture

Built-in tools are declared in `chatTools()` as OpenAI function tools.

Key rules:

- Built-in schemas use strict object schemas with `additionalProperties: false`.
- `executeTool()` decodes JSON leniently at the top level: unknown argument keys do not fail the call; they are collected and returned as a warning on the successful result envelope. Missing/invalid required parameters still fail loudly through per-tool validation.
- MCP tools are appended dynamically and keep their upstream schemas.
- Plan mode blocks side-effectful tools and MCP tools.
- `wait` requires `seconds` and a short user-visible `reason`, is cancellable through the active run context, and must be the only tool call in its batch (`ask` and `suggest` have the same single-call barrier).
- Tool results use `{ok, data, error, errorCode, details, warnings}`; `warnings` carries non-fatal notices (ignored unknown arguments) on success and is never set on errors.

Built-in model-facing tools:

| Tool | Purpose |
|------|---------|
| `list_files` | List files/directories with depth and limit controls |
| `read` | Read one or many local files; text returns numbered line previews; only plain text and images are supported |
| `edit` | Atomically apply exact-source or whole-line-range replacements to local files; after writing, returns a concise `validation` string with low-cost language checks |
| `create` | Create/overwrite text files; after writing, returns a concise `validation` string with low-cost language checks |
| `delete` | Delete files/directories |
| `grep` | Regex search through bundled ripgrep, with PATH fallback in development; exact stats + per-file hotspot counts (`fileCounts`) + `offset`/`nextOffset` pagination (`offsetExhausted` marks an offset past the end); workspace-wide skip policies are returned in `skipped`, while explicit paths bypass broad generated-directory and 10 MB exclusions |
| `command` | Shell command execution with safety checks |
| `service` | Run/inspect/stop long-lived local processes (dev servers, workers); unified three-platform stop = best-effort graceful termination, bounded grace wait (`graceSeconds`, default 3, max 30), then force kill of the whole process tree — escalation and kill failures are reported in the result `error` field |
| `wait` | Pause the current agent run for a cancellable 1–3600 second delay |
| `http_request` | Bounded HTTP/HTTPS API request |
| `web_fetch` | Bounded webpage fetch and readable-text extraction |
| `remote_*` | SSH remote read/edit/create/delete/run commands; use `remote_run_command` for directory discovery |
| `calculate` | Deterministic local math expression evaluator |
| `ask` | Pause the visible main Agent session for one or more user questions |
| `plan` | Session plan management; at most one `in_progress` item at a time, mark done before advancing |
| `subagent` | Spawn a sub-agent for a scoped task (requires a `role` name, shown as the card label and injected into the sub-agent system prompt) |
| `scheduled_task` | Create, list, or delete temporary isolated Agent tasks for the current Ally process |
| `skill` | Load an enabled skill |
| `render_html` | Render a self-contained HTML snippet inline in the chat UI as a sandboxed srcdoc iframe (max 50k chars); mounted only after the tool completes so arguments can stream first |
| `suggest` | Suggest 1–4 click-to-send follow-up chips; hidden tool (no running card), must be the only call in its batch, a single successful call ends the run |

MCP tools are named:

```text
mcp__<serverName>__<toolName>
```

---

## Read/Edit Architecture

`read` is the only model-facing local read tool.

Accepted read forms:

- Model-facing calls use only `files`: an array of one or more `{path, startLine?, endLine?}` requests.
- Backend compatibility fields may still accept the older `path`, `paths`, and shared-range forms, but they are not exposed in the tool schema.

Text files:

- must be text: UTF-8 returned as-is; UTF-16 LE/BE (with or without a BOM) is transcoded to UTF-8 for reading; reject binary content (non-text UTF-16 and files with NUL bytes are still `E_BINARY_FILE`)
- return LF-normalized text with display-only 1-based `N: ` prefixes; omit those prefixes from `oldText`/`newText`, and use the displayed numbers in `lineRange`
- missing paths and directory targets are silently omitted from the returned `files` array (an ignored-only batch succeeds with an empty array); other partial failures stay in the corresponding file result with `errorCode` when known
- include metadata: `startLine`, `endLine`, `nextStartLine`, `totalLines`, `truncated`, `truncatedLines`, `truncatedLinesOmitted`, `version`, `lineEnding`; `version` is a 6-character lowercase Crockford Base32 prefix derived from SHA-256 content and is compared case-insensitively

Office/PDF documents (`.docx`, `.pptx`, `.xlsx`, `.pdf`, etc.) are deliberately not parsed: read rejects them with a coded `E_DOCUMENT_UNSUPPORTED` error that points at the anydoc skill, whose conversion produces the Markdown the model then reads. Images keep their DataURL injection path.

Range semantics for model-facing reads:

- omit both `startLine` and `endLine` to read the whole file
- positive `startLine` without `endLine` reads from that line through the end; only `endLine` reads lines 1 through that inclusive line; both positive values give an inclusive range
- negative `startLine` reads the last N lines (absolute value limited to 10000) and must not be combined with `endLine`
- each displayed text line is capped at 2000 Unicode characters; `truncatedLines` identifies lines shortened by that cap, and `truncatedLinesOmitted` indicates the bounded line-number list omitted additional entries
- `truncated` is set both when a hard output limit cut the range (then `nextStartLine` points at the rest) and when individual lines were shortened; request another explicit range only when the remaining content is actually needed
- plain-text range previews use bounded-memory linear scans (no per-line materialization), so tiny reads remain safe on million-line files; very long single lines stay UTF-8/budget bounded

The model-facing `edit` tool edits exactly ONE file per call with a flat argument shape: top-level `path`, `version`, and `changes`, where each change uses a small exact `oldText` (preferred) or `lineRange` for larger whole-line replacements. Multi-file changes are expressed as parallel `edit` calls in one model response — one call per file. Legacy top-level string and line helpers remain backend compatibility APIs and are not exposed to the model.

`planLocalEditBatch()` in `orch_edit_plan.go` is the **only** normalization boundary for local model-facing edits. Both `orch_batch_policy.go` conflict detection and `orch_edit.go` execution must consume that plan; do not independently parse, canonicalize, or merge `edit.files` in either layer. Pure diff and changed-range algorithms live in `internal/tools/edit`; `orch_edit.go` is the app-owned execution boundary for local edits.

Edit parameters (one file per call):

- top-level `path`, required `version` from `read`, and 1–50 `changes`
- each change contains `newText` plus exactly one source: a small exact `oldText` copied from the `read` snapshot, or `lineRange` in inclusive `A-B` form only when replacing a larger whole-line block or when reproducing the exact source is impractical; optional `replace_all` defaults to `false`, replaces every non-overlapping exact occurrence when used with `oldText`, and is ignored with `lineRange` while a warning is returned
- the flat shape is the only accepted form; the legacy nested `{"files":[...]}` arguments used before this schema fail with `E_BAD_EDIT` — there is no compatibility path, matching every other built-in tool's single canonical shape

Important edit contract:

- Read the file first with `read`.
- `version` is mandatory for model-facing local and remote edits. It is a short optimistic-concurrency token; a stale value fails with `E_VERSION_MISMATCH`, and malformed values fail with `E_BAD_VERSION`.
- Each change chooses exactly one source: prefer a small, unique, unnumbered `oldText` copied exactly from the original `read` snapshot for precise replacements, including focused multi-line snippets when enough surrounding context makes it unique. Use `lineRange` in inclusive `A-B` form only for larger whole-line replacements or when reproducing the exact source is impractical (for example, an extremely long single line). By default `oldText` must occur exactly once; optional `replace_all: true` replaces every non-overlapping exact occurrence in the original snapshot, while it is ignored with `lineRange` and reported as a warning. All ranges in a file use the original numbered read snapshot and need no offset adjustment for earlier changes. Ambiguous default exact matches return optional structured `details` with at most three raw UTF-8 candidate previews, clipping flags, line ranges, and recovery guidance; the detail JSON is capped at 4 KiB so callers can issue a narrow `read` without receiving the full file.
- Successful edits return the new `version` per file. Reuse it only when the current source is known exactly; otherwise re-read numbered content before another `oldText` or `lineRange` edit.
- With `lineRange`, `newText` replaces exactly the selected whole lines; lines outside the range stay untouched, so never re-emit a closing brace or other code that sits outside the range — and if the brace is inside the range, `newText` must include it.
- For a multi-line whole-line block only, exact-match failure may fall back to ignoring leading spaces/tabs on each line. The fallback succeeds only for one unique candidate and safely rebases `newText` to the file's actual base indentation; body text is never fuzzy-matched.
- Exact changes whose normalized `oldText` and `newText` are identical are ignored and reported as warnings. An all-no-op edit batch (local or remote) succeeds without writing the file.
- Source regions must not overlap. The backend locates all exact snippets and line ranges on the original snapshot, applies them from the end of the file backward, and writes once.
- Repeated entries resolving to the same physical path inside one internal edit plan are merged into one original-snapshot edit plan; conflicting versions fail before writing. Model-facing flat calls carry exactly one file, so the merge is only exercised by internal callers and tests.
- All files are validated before writes. Any invalid, missing, ambiguous, overlapping, or stale effective change fails the entire call without modifying any file.
- When the provider stream cuts off mid-arguments and the decoded JSON is truncated, there is no salvage or partial execution: `prepareToolCallsForExecution()` rewrites the truncated arguments to the explicit `truncatedToolCallArguments` marker for both provider replay and execution, the executor turns the marker into `E_TRUNCATED_ARGS`, and the model resends the complete call. Single-file edit calls keep arguments small, so a full resend is cheap; the marker also keeps histories valid for providers that parse `tool_calls` arguments server-side.
- Empty `newText` deletes the selected source; exact-source insertion replaces a unique anchor with that anchor plus the inserted content.
- Put all independent changes for one file into that file's single `edit` call, and edit several files by sending parallel `edit` calls in the same response. The file is written once; a commit failure leaves it unchanged. The executor still validates the whole plan before writing and rolls back earlier committed files when a later commit fails, which only multi-file internal plans can trigger.
- Validation batching: when one tool batch carries several edit/create calls, directory-level checks (go vet per package, tsc/vue-tsc per tsconfig project) run once per validation unit instead of once per call — on the last batch call touching the unit, with the expanded path set of every call sharing it so output filters stay complete. A call whose paths were all absorbed returns an empty `validation` field. Single-write batches and calls outside a planned batch validate immediately as before (`planBatchValidation` in `orch_validation.go`).
- `remote_edit` uses the same flat single-file contract as local edit — top-level `target` (SSH target plus workspace root), `path`, `version`, and `changes`, exactly one file per call with multi-file changes expressed as parallel `remote_edit` calls — and returns the same `MultiEditResult` shape with exactly one file. Remote reads share the local text-encoding boundary (`read.DecodeTextBytes`), so UTF-16 LE/BE files are read and edited remotely exactly like locally, with UTF-8 write-back. `remote_edit` shares `edit`'s model-facing result compaction and truncation handling.
- Multiple separate file-mutation tool calls targeting the same normalized local or remote path in one tool-call batch are deduplicated: the first call in tool-call index order executes, and the rest are rejected with `E_WRITE_BATCH_CONFLICT` without executing — the model sees which call won and re-sends skipped changes in a later response.
- Built-in file mutations execute in `toolCallIndex` order after non-file tools complete.
- backend compatibility APIs may continue using SHA-256 and exact-string helpers internally.

---

## MCP Architecture

`mcp.go` owns MCP lifecycle.

Network MCP transports use Ally's proxy-aware HTTP client. Stdio MCP servers inherit the normalized proxy environment. Saving changed proxy settings reconnects MCP servers so the new transport takes effect.

Config path:

```text
~/.ally_agent/mcp.json
```

Settings page: Settings → MCP edits the server list or the raw JSON; applying the config reconciles instead of restarting everything (`ReconcileMcpServers`): only added, removed, or changed servers are (re)connected, unchanged servers keep their live connections and tool registrations. Server status is shown with connected/connecting/failed/disabled states (disabled = configured with `enabled:false`, never connected). A broken `mcp.json` surfaces a `config:warning` instead of silently unloading servers; MCP tool results are clamped to the same model-context cap as built-in tools. Saving changed proxy settings still restarts every MCP server (`RestartMcpServers`) so the new transport reaches all of them.

Manager flow:

1. Load `mcpServers` config.
2. Connect configured servers through stdio, SSE, or Streamable HTTP clients.
3. `Initialize()`.
4. `ListTools()`.
5. Sanitize tool names into OpenAI function names.
6. Map sanitized names back to real server/tool names during calls.

MCP status is emitted through `mcp:status`.

---

## Frontend Architecture

The frontend is centered on `frontend/src/App.vue`.

State management: Vue 3 `<script setup>` with plain `ref()` / `reactive()`, no Vuex/Pinia. Prompt history stays in `localStorage`; session index and completed UI snapshots are persisted by the backend in local files.

Major UI regions: header (controlled Naive UI workspace tabs, running indicators, drag ordering, history dropdown, plan indicator, settings, window controls), chat message area (one permanently mounted `ChatMessages` instance per open workspace Tab; content panes use `display-directive="show"` so switching only hides panes and preserves native DOM scroll state), per-Tab plan panel and workspace explorer/editor, command menu (`/`), session switcher, composer, `ComposerInfoBar`, settings modal.

Settings pages:

- General: custom prompt, launch-at-login switch; the retained close-to-tray setting is currently hidden
- Models: provider/model presets and active model selection
- Skills: enable/disable discovered skills; persisted through `disabledSkills`
- MCP: raw MCP config editor and server status
- About: GPLv3 notice, warranty disclaimer, and source repository link
- Network: proxy off/system/manual selection, fixed system-proxy detection, bypass list, redacted status, and a bounded connection test

The composer task-center button opens `TaskCenterPanel` (controlled tabs separate temporary scheduled tasks from managed background services; bounded previews; full output/service buffers open in a large scrollable modal). The composer statistics button opens `TokenStatsModal`, which queries `GetTokenStats()` on every open and renders dependency-free SVG charts.

Runtime events are registered through Wails `Events.On()` and routed by `sessionId` and `runId`.

Frontend-specific rendering:

- MarkdownIt for Markdown; highlight.js with the Darcula theme and compact line counts for code blocks
- Markdown HTTP(S)/mailto links are intercepted and opened through Wails `Browser.OpenURL`
- bounded render cache for non-streaming Markdown; streaming messages bypass cache
- Streaming text deltas are batched to roughly 20 FPS so the active Markdown tree is not reparsed and replaced every display frame
- Mermaid diagrams render sequentially during browser idle slots only when they approach the viewport; rendered SVG DOM is unloaded beyond the viewport retention margin and restored from a bounded 16-entry / 2M-character LRU; drag/wheel transforms are coalesced to one animation-frame write, with brief GPU `will-change` promotion; diagrams use a warm Darcula-derived base theme with click-activated viewports (cursor-centered wheel zoom, pointer dragging, double-click reset); Escape or a pointer press outside clears interaction focus
- `displaySourceMessages` inserts archive placeholders for large histories without mutating true session messages
- tool card components render read groups, diffs, command output, MCP tools, and sub-agent progress
- `render_html` keeps the iframe unmounted while arguments stream, then mounts one script-enabled, origin-isolated `srcdoc` iframe after completion; a `postMessage` bridge reports height while CSP blocks external resources
- `AskToolCard` renders one question per Tab, supports multiple selections and custom answers, and submits all answers together

UI internationalization:

- `frontend/src/i18n.mjs` is the source of truth for UI translations and locale helpers.
- Only `zh-CN` and `en-US` are supported. The primary `navigator.languages` / `navigator.language` entry decides the locale at startup: values beginning with `zh` use Chinese; all others use English.
- The root Naive UI `NConfigProvider` and discrete APIs must receive the matching component locale and date locale.
- New user-facing UI text must be added to both locale tables and referenced through `t()` / `$t()`; do not translate model output, file contents, command output, or raw tool results.
- `AppHeader` always shows a GitHub repository button that opens the Ally project through the system browser. Startup performs one best-effort latest-release check; when a newer semantic version exists, that same button changes into the green update icon.

Tool card verb / "Used \<Name\>" troubleshooting:

A tool card showing "Used \<Name\>" (or "Using \<Name\>" while running) instead of a real verb (Read / Edited / Ran / Grep / …) means `toolVerbLabel()` missed the `TOOL_VERBS` lookup and fell back to the `'Used'` / `'Using'` default. Every correctly registered tool shows its own verb, so a single tool regressing to "Used" is almost always a missing or mis-cased `TOOL_VERBS` key. Three files to check, in order:

- `frontend/src/utils/toolVerb.mjs` — `TOOL_VERBS` maps the **raw backend tool name** (the model-facing function name) to `[inProgress, done, noun]`. A missing key here is the usual cause. `toolVerbLabel(name, kind, status)` returns the verb; `hasNamedVerb(name)` tells the card the verb already names the action. `KIND_VERBS` only covers `mcp` as a kind-level fallback; any other unknown name yields `'Used'` / `'Using'`.
- `frontend/src/components/ToolCallCard.vue` — verb span = `toolVerb()`, name span = `toolDisplayName()`. When `hasNamedVerb(msg.name)` is false the name span falls back to `formatToolName(msg.name)`, which is exactly why a missing entry renders "Used Grep" rather than "Grep" (the verb defaults to "Used", the name defaults to the raw tool name).
- `frontend/src/App.vue` — `toolKind(name)` derives `msg.kind`. The verb table is keyed by name (not kind) on purpose, so check the `TOOL_VERBS` key first; `toolKind` is rarely the culprit.

`msg.name` is streamed straight from the backend (`tool:start` / `tool:update` payload `name` = `call.Function.Name`), with no case-folding anywhere in the pipeline. Casing is deliberate and must match the backend registration in `internal/tools/shared/builtins.go` (currently all built-in tool names are lowercase, e.g. `grep`; `Glob` was removed). Add a tool, rename one, or change its casing and forget the matching `TOOL_VERBS` key → the card reverts to "Used \<Name\>". (grep regressed exactly this way in `4dbbbdd`, where the verb entry was deleted leaving only a `toolDisplayName` special-case that fixed the name span but not the verb span; restored in `a08106e` by re-adding `grep: ['Grep','Grep','Grep']`.)

---

## WebView2 / Wails Frontend Constraints

Ally runs inside Wails v3 WebView2 / WKWebView / WebKitGTK, not a normal browser tab. Violations reproduce as "works in dev, broken in packaged app". See also `.ally/lessons.md` [webview] 2026-08-20 and [css] 2026-08-17.

### 1. Window drag region is decided at pointerdown

- `.app-header` is `--wails-draggable: drag`. Every interactive element inside (buttons, inputs, dropdowns, `n-tabs-tab`, `ComposerInfoBar` spans) must be `no-drag`, otherwise clicks/dropdowns are swallowed as window drag.
- When toggling drag/no-drag during a gesture (e.g. tab reorder), write `element.style.setProperty('--wails-draggable','no-drag')` synchronously and restore with `removeProperty`. Do not rely on reactive `:class` alone: it applies next frame, but WebView2 decides at pointerdown whether the gesture drags the window or the element.
- Restore with `removeProperty`, not writing `drag` back, so CSS cascade takes over.

### 2. Never use HTML5 Drag-and-Drop - use Pointer Events

- WebView2/Wails does not reliably support `draggable` / `dragover` / `drop` / `DataTransfer` inside the page. Do not use native DnD for tab reorder, list reorder, file drop, etc.
- Unified pattern: `@pointerdown` -> `setPointerCapture(pointerId)` -> `window` `pointermove`/`pointerup`/`pointercancel`. Call `preventDefault()` in `pointerdown` and `pointermove`, and add `touch-action: none` + `user-select: none` to the host while dragging.
- Gate dragging behind a threshold (e.g. `hypot < 4px`). Once exceeded set `hasDragged` and show ghost/shift feedback immediately; do not wait for a drop target. On drop emit reorder, then suppress the synthetic click with `@click.capture` + `stopPropagation`.
- Ghost must be `<Teleport to="body">` + `position: fixed` + `translateZ(0)` + `will-change` to avoid clipping by `overflow: hidden` and to promote to compositor. Original tab stays as a dashed placeholder; clear content is shown by the ghost, middle tabs shift with `translateX(var(--offset))`.

### 3. Naive UI stacking pitfalls

- `n-tabs` (`inheritAttrs: false` but manually merges `$attrs`) forwards `data-*` / `draggable` / `class` to `.n-tabs-tab`, but `tabClass`/internal styles can override custom selectors - use `:deep()` with sufficient specificity.
- `n-tabs-bar` 1px rendering is unstable on HiDPI; Ally hides it (`display: none`) and renders the active underline via `::after` 2px solid. Keep this replacement when changing tab styles.
- `n-dropdown` / `n-modal` leave focus on the trigger - blur the trigger synchronously in `open*` handlers, otherwise the button stays highlighted after close.

### 4. General WebView2 frontend rules

- Do not rely on browser extensions, `file://` drops, or `window.open` popups. `render_html` is a single sandboxed `srcdoc` iframe with CSP blocking external resources.
- `pointerId` can be `undefined` for synthetic events - guard with `event.pointerId !== undefined && event.pointerId !== dragPointerId`.
- Avoid synchronous layout thrashing in `pointermove`; heavy re-render already stays behind `setTimeout -> requestAnimationFrame` throttles for `run:stream` / `tool:update`.
- `cursor: grab/grabbing` is hint only and never drives drag logic.

---

## Commands

| Command | Description |
|---------|-------------|
| `/new` | Create a new session |
| `/sessions` | Show sessions; `/switch N` switches session |
| `/init` | Explore project and generate AGENTS.md |
| `/skills` | Show discovered skills and enabled/disabled status |
| `/clearskills` | Disable all discovered non-builtin skills |
| `/<skillname>` | Explicitly load a skill |
| `/skill:<name>` | Alternate explicit skill loading syntax |
| `/note` | Save durable project knowledge |
| `/remember` | Compatibility alias for `/note` |
| `/lesson` | Review the conversation and update `.ally/lessons.md` with reusable pitfalls |
| `/compact` | Compact the current session history |
| `/reload` | Reload model config file |

---

## Sessions, Context, And Token Accounting

Backend 会话/历史/上下文核算的完整说明（索引与 gzip 快照、前后端历史对齐、`ReleaseSession` vs `DeleteSession`、Token 统计落盘、长渲染优化边界）见 `CODEGRAPH.md`「会话与上下文」与 `biz_sessions.go` / `biz_stats.go`。关键不变式：

- 每个工作区 Tab 持有有效 `sessionId`；创建或切换会话立即更新该链接。
- Header 与内容区 Tabs 共享受控 `activeWorkspaceId`；`Ctrl/Cmd+Left/Right` 切换，拖拽排序操作同一 `workspaceTabs` 数组。
- 每个打开的 Tab 常驻一个 `ChatMessages` + `n-tab-pane`（`display-directive="show"`）；该 pane 拥有会话计划面板；`WorkspaceExplorer` 树/编辑器挂在 n-tabs **外层**（每个 Tab 一个常驻实例 + `v-show`，编辑草稿不因切 Tab 丢失）。切换不得共享计划/编辑器状态、卸载已打开 Tab 的消息或引入合成滚动锚点。
- 会话列表只显示当前工作区的会话；选择历史会话不会切换到其他工作区或静默重绑到别的 Tab。
- 带显式 `sessionId` 的运行时事件绝不回退到当前可见会话；终止事件仅在 `runId` 仍匹配该会话当前 run 时接受。
- 输入框/文本域/下拉/可编辑元素内禁用 `Ctrl/Cmd+Left/Right` 工作区导航。
- `CancelRun` 立即取消 context，但保留 `runs`/`runSessions` 注册直到 `runChat` 实际退出，避免会话释放/删除与取消中的 run 竞态。

---

## Sub-Agents

`subagent` starts a child agent loop bounded by a hard 1000-step cap (shared with the scheduled-task maximum); wall-clock is otherwise unlimited. Cancellation still follows the parent run or an explicit stop request.

Sub-agents receive connected MCP tools and share the manager's invalid-session reconnect path. Interactive/nested tools such as `ask` and `subagent` (`agent_delegate` alias), plus `plan`, `skill`, `scheduled_task`, remain excluded (`subagentTools`). The default step budget is 25 (`defaultSubagentSteps`, range 1–1000); when a sub-agent exhausts its budget it is forced into a final report-only turn (`forceSubagentFinalReport`, status `timed_out`).

Completed sub-agent records release their cancel function, keep at most 100 tool events each, and are globally pruned to the latest 50 completed records. Running records are never pruned.

Sub-agent UI: backend emits `sub:*` events; `sub:spawn` includes the parent tool-call identity so the frontend upgrades the original `subagent` card in place; `tool:result` / `tool:error` finalize that same inline sub-agent card; frontend displays a lightweight inline sub-agent progress row.

Interactive ask behavior:

- `ask` is available to the visible main Agent session, but is excluded from sub-agents and scheduled tasks.
- The tool blocks until the frontend submits every question or the run is cancelled. Cancelling emits `ask:closed`, removes the pending request, and returns `E_ASK_CANCELLED`.

---

## Configuration Files

Main app config:

```text
~/.ally_agent/config.json
```

MCP config:

```text
~/.ally_agent/mcp.json
```

Legacy scheduled-task file (deleted on startup and no longer written):

```text
~/.ally_agent/scheduled_tasks.json
```

Scheduled tasks exist only for the current Ally process. Each execution uses fresh isolated context and a fixed workspace. Runs are globally serialized, cannot overlap with the same task, and retain only the latest bounded summary/error. Per-run defaults are 100 steps and one hour, configurable up to 1000 steps and 24 hours.

Legacy completed background-service history (cleaned on startup and no longer written):

```text
~/.ally_agent/service_history/
```

Service processes are stopped when Ally exits and are not auto-restarted. Terminal service records are removed immediately and are not retained across restarts.

Example MCP config:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    }
  }
}
```

---

## Security And Safety

- Workspace write operations are confined to the configured workspace, except `~/.ally_agent` is also allowed for Ally global config and memories.
- Read-only local tools may inspect explicit absolute paths outside the workspace subject to safety checks.
- `command` keeps cwd inside the workspace, permits outside reads, null-device redirection, and creation of new outside paths, but refuses commands that may modify/delete existing outside paths or perform explicit deletion.
- Command safety uses `mvdan.cc/sh/v3`'s Bash AST in `internal/tools/command` for shell invocations, nested commands, command substitutions, heredocs, and redirections; a narrow legacy scanner remains only for parser-rejected/incomplete foreign-shell fragments. Quoted/search data is not treated as executable syntax, managed deletions are allowed only when every deletion in the compound command is managed, and source/destination-aware mutation analysis checks explicit write targets rather than every referenced path. Heredoc bodies and here-strings are skipped as data (quoted delimiters are fully literal; unquoted bodies still have command substitutions inspected).
- `checkCommandSafety()` in `orch_command_safety.go` is the app-owned boundary for workspace roots, path existence, `E_COMMAND_BLOCKED` / `E_PATH_OUTSIDE`, and user-facing explanations; it must consume the command package's AST-backed semantic analysis instead of adding independent full-string risk regexes. Redirection/mutation targets that cannot be statically resolved (variables, globs, heredoc artifacts) are allowed through permissively; only literal existing outside paths are blocked.
- The `command` schema explains how to recover from `E_PATH_OUTSIDE`: read the Chinese reason/target, avoid unchanged retries, choose a new or workspace target, and replace literal outside targets with workspace paths. The model-facing system prompt keeps only the first two recovery steps (read the returned Chinese explanation and detected target; do not retry the unchanged command) and points to the schema for the rest.
- Prefer `delete` / `remote_delete_path` over shell deletion.
- `readTextFile` rejects binary files using NUL checks, after transcoding UTF-16 LE/BE (with or without a BOM) to UTF-8; edited UTF-16 files are written back as UTF-8.
- Release packages bundle ripgrep under the executable's `tools/` directory; development builds fall back to `rg` from `PATH`.
- On Windows, `command` and `service` prefer **bash** from Git for Windows over PowerShell. Detection order: a validated `gitBashPath` setting (manual override) → Git for Windows common install paths → derive Git Bash from `git.exe` on `PATH` → a `bash.exe` on `PATH` only when it belongs to the same Git for Windows installation → fallback to PowerShell (`pwsh.exe` → `powershell.exe`). Arbitrary `bash.exe` launchers such as `C:\Windows\System32\bash.exe` (legacy WSL) are rejected because their Linux PATH and argument forwarding break Windows tool discovery and shell expansion. When Git Bash is not detected, startup warns the user to set `gitBashPath` in Settings. The system prompt dynamically reflects which shell is active so the model generates correct syntax. Linux/macOS always use `bash -c` and ignore `gitBashPath`.
- On macOS, system proxy detection uses the built-in `/usr/sbin/scutil --proxy` command without cgo, with a bounded timeout; Ally parses fixed HTTP/HTTPS/SOCKS settings, bypass entries, and PAC metadata. Linux continues using proxy environment variables, and PAC-only configurations remain explicitly unsupported until PAC evaluation is implemented. Proxy fail-closed means `proxyForConfig()` returns an error (request fails) when proxy resolution errors out or system proxy is not enabled — never silently bypasses to direct. All outbound dials go through `guardedDialContext`, which rejects private IPs when `allowPrivateNetwork` is off (SSRF guard, applies to NO_PROXY direct paths too); the proxy host itself is always allowed.
- On macOS/Linux, `infra_shell_env.go` probes the user's login shell once (`$SHELL -l -c /usr/bin/env`, with an OS-account shell fallback), appends only missing absolute `PATH` entries to the inherited environment, and shares that environment with `command` and `service`. Probe failures leave the original environment unchanged; other profile variables such as `GOPATH` and `NVM_DIR` are not imported.
- When bash is active on Windows, safety checks detect both Windows-style (`C:\...`) and MSYS2-style (`/c/...`) absolute paths outside the workspace.
- Tool output is capped by `maxToolOutput`; HTTP tools use bounded response sizes, timeouts, redirect limits, and clear user agent defaults.
- API keys are stored in the OS user config directory without encryption.
- MCP servers are spawned as subprocesses from user-controlled config.

---

## Tests And Verification

`wails3 build` is the only build and verification command. Run it when a change touches the Go backend or the Wails bridge; it compiles the backend, builds the Vue frontend, and produces the desktop binary. Pure frontend changes that do not affect bindings do not require verification.

---

## Code Style Guidelines

- Go: standard `gofmt`, tabs, explicit `if err != nil` handling.
- Vue: Composition API with `<script setup>`.
- Components: PascalCase file/component names.
- CSS: one dark theme, semantic class names, no preprocessor.
- Events: lowercase with colon separators, e.g. `run:stream`, `tool:result`, `mcp:status`.
- JSON fields: camelCase for Go struct tags and user-facing tool parameters.
- Wails bindings: `wails3 build` regenerates bindings automatically; no manual binding step needed.
- Avoid broad refactors while changing tool contracts or provider adapters.

---

## Development And Refactoring Rules

These rules are required for future changes. They exist to keep Agent behavior discoverable, prevent cross-layer contract drift, and preserve a future path to extracting the Agent core from Wails.

### Module ownership and unique modification boundaries

- Treat `app.go` as orchestration only: chat-loop control flow, run/session state, and coordination between domain modules belong there. New domain logic should move to the owning module instead of enlarging `app.go`.
- Keep the Agent core host-neutral. Core/runtime files must not import Wails runtime packages or call window APIs, dialogs, browser APIs, or OS desktop integration directly.
- Put Wails lifecycle, window management, directory/file-manager integration, and other desktop behavior in `host_desktop.go`.
- Publish UI/runtime events only through the `eventSink` boundary in `host_events.go`; preserve event names, payload shapes, session routing, and terminal-event rules when changing implementations.
- Keep provider-specific request/response types behind `prov_model.go`; do not leak OpenAI, Responses, or Anthropic wire details into `app.go` or generic tool orchestration.
- Keep each cross-layer contract at one source of truth: `chatTools()` for schemas, `executeTool()` for dispatch/decoding (top-level lenient decode with unknown-argument warnings), `orch_edit_plan.go` for local edit normalization, `orch_batch_policy.go` for batch barriers/conflicts, `orch_edit.go` for app-owned edit execution, `internal/tools/edit` for pure diff/range algorithms, `internal/tools/pathutil` for workspace path resolution and safety checks, `infra_result.go` for result envelopes/compaction, and `infra_stream.go` for stream throttling.
- Do not independently parse, canonicalize, deduplicate, or infer the same request in multiple layers. If a lower layer produces a normalized plan, upstream policy checks and downstream execution must consume that plan.
- Workspace path resolution and boundary checks live in `internal/tools/pathutil`. The app package keeps only thin package-level wrappers (delegating to pathutil through a host-neutral `Runtime` interface) so existing call sites stay unchanged; do not re-implement `insideRoot`, `safeJoin`, `insideWriteRoot`, or `resolveReadablePath` in app or any other tool package.
- Keep the project in one Go package unless a package boundary has a clear dependency direction and Wails binding impact has been verified. Mechanical same-package extraction is preferred before introducing new packages.

### Cross-layer change procedure

- Before changing a tool or event contract, trace the complete path: schema → lenient decoder (unknown keys → envelope warning) → batch policy → executor → result envelope → frontend event/card rendering → tests → `AGENTS.md`/`CODEGRAPH.md` when the architecture changes.
- When a request contains nested mutations, normalize it once before conflict detection (`planLocalEditBatch` is the shared boundary; the model-facing `edit` shape is flat and single-file). Repeated internal plan entries that resolve to one physical path merge into one plan entry; separate mutation tool calls targeting the same normalized path must still conflict.
- Add at least one end-to-end or boundary test that enters through the same layer as production. A unit test for `executeTool()` alone is insufficient when the scheduler or batch policy can reject the request first.
- For changes that affect both core and host behavior, use a capture/no-op `eventSink` test so the Agent can be tested without Wails and the event payload contract remains explicit.
- Do not fix a failing cross-layer test by weakening a valid safety assertion. First identify which layer owns the rule, then move the rule to the shared boundary or update the test to the intended contract.
- Keep compatibility aliases and legacy backend APIs separate from model-facing schemas; do not expose a compatibility shape merely to avoid updating the canonical boundary.

### Safety and verification requirements

- Preserve optimistic concurrency for local and remote edits. `E_VERSION_MISMATCH` must not trigger an automatic reread, guessed rebase, or blind retry; the model must reread and regenerate changes when needed.
- File mutations must validate the complete normalized batch before any write. Maintain single-snapshot matching, non-overlap checks, atomic commit, and best-effort rollback semantics.
- Keep safety-sensitive fallbacks narrowly proven: unique indentation-only matching may rebase indentation, but never fuzzy-match code bodies or choose among multiple candidates.
- Keep bounded output, concurrency caps, timeouts, cancellation, and redirect limits intact unless the change includes an explicit resource-safety rationale and regression tests.
- For backend changes, run `wails3 build` and `git diff --check`. For pure frontend changes, `wails3 build` is only needed when the Wails bridge or generated bindings are affected.
- Inspect `git diff` and `git status` after formatting/builds. Do not include generated binaries, unrelated formatting changes, or line-ending-only changes in a functional commit.
- Update `AGENTS.md` and `CODEGRAPH.md` in the same change when module responsibilities, public workflows, event flow, tool contracts, or major data flow changes.

---

## Wails Event Emission Map

后端模块只调用 `App.emit()`；该方法与 `eventSink` 位于 `host_events.go`，默认 Wails v3 适配器（`wailsEventSink`）再调用 `app.Event.Emit`。另支持可选网络出口：`host_network.go` 的 `networkEventSink`（SSE `/events`、轮询 `/poll`、`/healthz`），由 `ALLY_NETWORK_EVENTS=1` 环境变量启用（默认关闭），与 Wails 通过 `fanoutEventSink` 串行广播（panic 隔离、互不影响）。Wails 启动、窗口和系统对话框位于 `host_desktop.go`。前端在 `App.vue` 的 `bindRuntimeEvents()` 中通过 `Events.On()` 统一注册，`runtimeEventOffs` 跟踪卸载。Agent/runtime 模块不得直接调用 Wails 事件 API。

### 高频流（双层节流，仅这两条）

| 事件 | 后端节流位置 | 前端缓冲位置 |
|------|--------------|--------------|
| `run:stream` (content+reasoning 合并；reasoning 只发字符数 `reasoningLen`，不发正文) | `runStreamDeltaEmitter` (`infra_stream.go`, **64ms 纯时间节流，无字节阈值**)，流末 `flush()` 兜底；`run:image` 发送前先 flush | `queueStreamDelta` + **纯 `requestAnimationFrame` 合帧（~16ms）** (`App.vue`；48ms setTimeout 链已废弃) |
| `tool:update` | `toolCallProgressTracker.eventsWithForce` (`infra_stream.go`, 200ms / 2048B)，超阈值且在窗口内早 continue；`forceEvents()` 流末绕过节流。`command` 约每 120ms 发布累计输出并在结束前强制最终快照 | `bufferToolUpdate` + `setTimeout(120ms) → requestAnimationFrame`；命令卡短输出按内容收缩，最多约六行后滚动，并自动跟随末尾 |

### 生命周期事件（天然低频，无需节流）

**Chat loop (`app.go` `runChat`)**：`run:start` / `run:llm_wait` / `run:done` / `run:error`（每轮 1 次）、`run:retry`（重试时，含 adapter 内 key 切换与 turn-level 重试）、`run:inject`（排队消息进上下文）、`run:compact` / `run:compacted`（压缩时）、`run:image`（图片 delta）、`tool:result` / `tool:error`（每工具调用 1 次）、`tokens:update`（每 LLM step 1 次，**provider 无 usage 时静默跳过**）、`tokens:reset`（`ResetWorkspaceTokenUsage`）

**Ask (`app.go`)**：`ask:ready` / `ask:closed` — 每次 ask 1 次

**Plan (`app.go`)**：`plan:update` — 写入时

**MCP (`app.go`)**：`mcp:status` — 服务器状态变更时（一次发全部状态）

**Dependencies/Config (`app.go`)**：`dependency:missing` / `config:warning` — 罕见，前端按 tool 去重

**更新 (`biz_update.go`)**：`update:progress` / `update:ready` / `update:applied` / `update:error` / `update:cancelled` — 自更新生命周期点

**Services (`orch_services.go`)**：`service:update` — 仅 `startServiceWithConfig` / `finalizeService` / `stopService` 三处，不随输出滚动触发

**Scheduled tasks (`orch_scheduler.go`)**：`scheduled:update` / `scheduled:run_start` / `scheduled:run_done` / `scheduled:run_error` — 调度生命周期点

### 子代理 / 调度任务路径

`app.go` `executeDelegate` 调用 `streamModelResponse(ctx, cfg, model, messages, tools, nil)` —— **传入 `nil` onEvent**，子代理和调度任务不发任何 `run:stream` / `tool:update` 流式事件。只发 step 级事件：

- `sub:spawn` / `sub:done` / `sub:error` — 生命周期
- `sub:tool:start` / `sub:tool:result` / `sub:tool:error` — 每工具调用 1 次
- `sub:step` — 每 LLM step 1 次

### 事件命名约定

lowercase + 冒号分隔，如 `run:stream`、`tool:result`、`mcp:status`、`sub:step`、`scheduled:update`、`service:update`、`plan:update`、`ask:ready`、`tokens:update`、`dependency:missing`、`config:warning`。

---

## Performance And Memory Notes (Ally-specific)

- `tool:update` 事件在 `toolCallProgressTracker.eventsWithForce` 中带 `toolUpdateThrottle = 200ms` + `toolUpdateThreshold = 2048` 字节节流；`forceEvents()` 在流结束后绕过节流。`command` 执行时以约 120ms 间隔发送有变化的累计 stdout/stderr，并在命令结束前发送最终输出快照。测试见 `TestToolCallProgressTrackerThrottlesLargeUpdates`。
- 前端 `App.vue` 用 `toolUpdateBuffers` Map + `setTimeout(120ms) → requestAnimationFrame` 批量 flush `tool:update`，并在终端事件处理前显式 `flushToolUpdateBuffer()`；`streamBuffers` 走 `queueStreamDelta` + 纯 rAF 合帧处理 `run:stream`（48ms setTimeout 链已废弃）。
- 单 session `localStorage` 预算 240KB，大 tool 预览 / edit 参数 / 附件 Base64 / Diff 在序列化前剥除或截断。
- 前端 Mermaid SVG DOM 视口外卸载，16 条 / 2M 字符 LRU；`render_html` 流式期间不挂载 iframe，完成后再挂一个 sandboxed iframe。
- `command` / `service` 后端 rolling buffer 512KB / 进程，最多 8 个活动进程；服务停止或退出后立即清理记录。
- 媒体预览用 revocable Blob URL，session 驱逐时 `URL.revokeObjectURL`；原图 Base64 仅在需要 model 输入时保留。
- `AppHeader` 容器 `--wails-draggable: drag`，内部所有按钮 / 输入 / 下拉必须 `no-drag`；`ComposerInfoBar` 所有交互 span/button `no-drag`，否则下拉/点击会被拖拽逻辑吞掉。
- 后台 goroutine（chat run / MCP / scheduled task / service process）都派生自 `app.ctx`，窗口关闭后随 ctx 取消。
