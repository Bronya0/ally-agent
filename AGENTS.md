# Ally — AGENTS.md

This file is read by AI coding agents. Keep it current when the app architecture, tool contracts, prompt pipeline, or UI workflows change.

---

## Build & Run Commands

| Command | Description |
|---------|-------------|
| `wails3 dev` | Run the desktop app in development mode with hot reload |
| `wails3 build` | Build a distributable desktop binary (also the only verification command) |

## Git Convention

When developing Ally itself, push with the author set to `ally agent`:

```powershell
git add -A
git -c user.name="ally agent" -c user.email="ally@agent.dev" commit -m "..."
git push origin main
```

This uses `-c` to override the commit author per-command only, leaving the developer's global Git config unchanged.

---

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
│   ├── builtin_skills/       # 内置 skill 嵌入资源（go:embed）
│   ├── host/                 # 宿主事件适配（eventSink → Wails v3）
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
| `host_` | Host 桥接 | Wails 生命周期、窗口、对话框、eventSink 边界、系统托盘、子进程与任务栏控制 |
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
- `command/` — 命令安全解析：重定向/路径/风险模式匹配
- `edit/` — 编辑 Diff、变更范围
- `git/` — git porcelain / unified-diff 解析
- `grep/` — ripgrep 封装与结果归一化
- `memory/` — 记忆 Markdown frontmatter 解析 + 编排（Runtime 注入）
- `pathutil/` — 工作区路径解析与安全检查（Runtime 注入）
- `read/` — 文本读取、版本令牌、原子写入、文档文本抽取
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
3. `buildMessages()` constructs the request context: core system prompt, workspace map, goal context, persisted history, current user message, and attachments.
4. `buildToolsWithMcp()` combines static built-in tools with connected MCP tools.
5. `streamModelResponse()` dispatches to the configured provider adapter.
6. Streaming deltas and tool-call updates are emitted to the frontend through runtime events.
7. Non-file tool calls run concurrently with a max concurrency of 4; built-in file mutations run afterward in `toolCallIndex` order. `wait` must be the only call in its tool batch.
8. Tool results are appended to same-turn model context and the loop repeats until no tool calls remain or `maxAgentSteps` is reached.
9. Saved session history excludes raw tool calls/results and stores compact tool activity summaries.

Connected MCP tools are sorted by server, tool name, and function name before being exposed so request tool ordering remains deterministic across turns.

---

## Backend State

`App` in `app.go` owns the long-lived process state:

- `config`: active `ConfigState`
- `configPath`: `~/.ally_agent/config.json`
- `runs`: run ID to cancel function
- `runSessions`: run ID to session ID, used to reject cleanup of active sessions
- `histories`: backend model history by session ID
- `sessionsDir`: local session index and per-session UI snapshots under `~/.ally_agent/sessions/`
- `sessionMu`: serializes local session index/snapshot file operations
- `pendingAsks`: blocking main-session questions waiting for frontend submission
- `disabledSkills`: persisted list of skill names disabled by the user
- `mcpManager`: active MCP manager
- `goalStates`: active goal mode state by session
- `todos`: session-local todo list
- `todoRevisions`: monotonic todo update revisions emitted with `todo:update`
- `subRuns`: sub-agent progress records
- `services`: active background-service records; at most 8 run concurrently, each keeps a rolling 512 KiB buffer, and terminal records are removed immediately
- `scheduled`: process-local scheduled-task manager; definitions and results are cleared whenever Ally closes or starts
- `fileOpsMu`: serializes local write/edit/delete operations
- `gitDiffMu`: serializes/cancels git diff work
- workspace token/context caches
- `stats`: 有界非阻塞 Token 统计队列、按天持久化记录和异步刷新器；启动加载文件和记录数有界，关闭时等待队列 drain/flush 完成

`ConfigState` is stored in `~/.ally_agent/config.json` and includes:

- provider fields: `providerName`, `apiFormat`, `baseUrl`, `apiKey`, `apiKeys` (ordered multi-key pool; first entry is highest priority), `model`, `reasoningEffort`
- runtime fields: `workspace`, `temperature`, `maxTokens`, `contextWindow`
- tray fields: `closeToTray` (close window hides to system tray instead of quitting; legacy config without the field defaults to on)
- network fields: `proxyMode` (`off`/`system`/`manual`), `proxyUrl`, `proxyNoProxy`
- prompt fields: `systemPrompt`, `customPrompt`
- model presets: `models`
- skill settings: `disabledSkills`

Important config behavior:

- `mergeConfig()` preserves existing config values when overlay fields are zero/empty. `apiKeys` is replaced as a whole when non-nil; a legacy `apiKey`-only overlay replaces the pool with a single key.
- `apiKey` and `apiKeys` stay in sync: the pool is the source of truth and `apiKey` mirrors its first entry; a legacy config with only `apiKey` is loaded as a single-key pool.
- Multi-key requests use strict priority failover: the first usable (not cooling down) key is always tried first; on auth/quota errors the key enters a 60s in-memory cooldown, on transient errors a 10s cooldown, then the next key is tried, up to a global attempt cap (`maxMultiKeyAttempts`) that replaces per-key adapter retries. Failover only happens before any stream output is emitted. Cooldown state is process-local and never persisted.
- `disabledSkills` is normalized and persisted.
- Skill enable/disable operations update both memory and `config.json`.
- Frontend `defaultConfig()` also includes `disabledSkills` so saving settings does not erase skill state.
- `reasoningTag` defaults to `reasoning_content` in both backend and frontend model drafts.
- `reasoningEffort` defaults to `auto` (send no thinking-strength parameter) in both backend and frontend model drafts; supported levels are `low`/`medium`/`high`/`xhigh`/`max`. `normalizeReasoningEffort()` collapses unknown/stale values back to `auto` so a bad config never injects an unsupported parameter. The OpenAI Chat adapter sends it as top-level `reasoning_effort`, the Responses adapter as `reasoning.effort`, and the Anthropic adapter as `output_config.effort` (without enabling thinking blocks). A recognized selection is sent unchanged by every adapter, including `xhigh` and `max`; provider compatibility errors are surfaced instead of silently changing the user's choice.
- On Windows, an auto-detected valid Git Bash path is returned in config so Settings can show the executable actually in use.
- Proxy mode is explicit and fail-closed: `off` ignores inherited proxy variables, `system` detects Windows WinINET fixed proxy settings with environment fallback, and `manual` accepts HTTP/HTTPS/SOCKS5 URLs. PAC URLs are reported but not executed yet.

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

### OpenAI Chat Completions

- Uses `github.com/sashabaranov/go-openai`.
- Sends `max_completion_tokens` by default.
- Falls back to legacy `max_tokens` if a compatible provider rejects `max_completion_tokens`.
- Sends `stream_options.include_usage=true`, and retries without `stream_options` if unsupported.
- Merges streaming tool-call deltas with `mergeToolCallDeltas()`.
- Reads `ReasoningContent` deltas when providers expose them.

### OpenAI Responses

- Uses `github.com/openai/openai-go/responses`.
- Converts system messages to `instructions`.
- Converts chat messages to `ResponseInputItemParam`.
- Assistant tool calls are converted to `function_call` items with both:
  - `call_id`: original tool-call ID
  - `id`: stable `fc_<call_id>` item ID
- Tool results are converted to `function_call_output` by `call_id`.
- Streams output text, reasoning summary text, function-call argument deltas, completion usage, failures, and incomplete responses.
- On the official OpenAI Responses endpoint, exposes the native image-generation tool and streams generated images through `run:image`; custom compatible endpoints do not receive that provider-specific tool.
- Tool definitions are converted with `ToolParamOfFunction(..., strict=false)` for provider compatibility.

### Anthropic Messages

- Uses `github.com/anthropics/anthropic-sdk-go`.
- Converts system messages to `params.System`.
- Converts user/assistant messages to Anthropic content blocks.
- Assistant tool calls become `tool_use` blocks.
- Consecutive tool-result messages become one user message with `tool_result` blocks.
- Image attachments are accepted only as valid `data:image/...;base64,...` URLs.
- Tool schemas are mapped to `ToolInputSchemaParam`; `properties` and `required` are first-class fields, while JSON Schema constraints such as `additionalProperties`, `anyOf`, and `not` are preserved through `ExtraFields`.
- Stream `stop_reason` values are preserved. `max_tokens`, `refusal`, `pause_turn`, context-window overflow, and unknown reasons stop the run with an explicit error instead of being treated as normal completion.
- Tool result envelopes with `ok:false` are sent back as Anthropic `tool_result` blocks with `is_error:true`.
- Anthropic Base URLs ending in `/v1` are normalized because the official SDK appends `/v1/messages`; non-positive Max Tokens default to 8192 for this format.
- Requests sent to the official Anthropic base URL enable a top-level five-minute prompt-cache breakpoint; custom compatible endpoints do not receive this field.
- Thinking strength, when the user picks a level, is sent as `output_config.effort` (`low`/`medium`/`high`/`xhigh`/`max`); thinking blocks are deliberately not enabled (adaptive/enabled `thinking` would emit blocks whose signatures must be replayed verbatim in the next tool turn, and `buildAnthropicMessages` cannot do that losslessly yet). On models where adaptive thinking is already the default, effort alone tunes that existing thinking; on older models the parameter may be ignored or rejected, which the user opts into by picking a level.

---

## Chat Loop

`StartChat()` registers a run and starts `runChat()` in a goroutine.

`runChat()`:

- builds messages with `buildMessages()`
- builds static + MCP tools with `buildToolsWithMcp()`
- streams model output and emits:
  - `run:start`
  - `run:llm_wait`
  - `run:delta`
  - `run:reasoning`
  - tool events
  - `run:done` / `run:error`
- tracks usage with provider-reported usage when available, otherwise estimates
- executes non-file tool calls concurrently with a semaphore cap of 4
- rejects mixed tool batches containing `wait`; a valid `wait` batch contains exactly one call
- rejects same-path mutation groups, then executes remaining built-in file mutations in `toolCallIndex` order under `fileOpsMu`
- appends compact model-facing tool results
- loops until no tool calls remain

Tool result channels:

- Frontend receives full JSON via `tool:result` / `tool:error`.
- Model context receives compacted JSON from `compactToolResultForModel()`.
- `read` content is intentionally not compacted so exact raw snippets remain copyable into edit changes.

`saveHistory()`:

- drops system messages
- turns tool calls and tool results into concise assistant summaries
- converts multi-content messages to text summaries for persistence
- keeps only the last 40 saved messages

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

Skill metadata:

- Only enabled skills are listed in the system prompt.
- Full skill Markdown is not injected by default.
- Disabled skills are omitted from metadata and cannot be loaded through the `skill` tool.

Global memory metadata:

- `~/.ally_agent/memories/` is created on startup.
- Markdown files under that directory with YAML frontmatter `description` are scanned into a separate "全局记忆索引" system prompt part.
- The index contains only file paths and descriptions. Full memory content is loaded only through `memory_read`.
- Durable cross-project knowledge should be created or updated through `memory_write`.

---

## Skills Architecture

Skills are inspired by Kimi Code.

### Discovery

`ListSkills()` scans:

- user skills: `~/.agents/skills/`, `~/.claude/skills/`
- project skills: `<workspace>/.agents/skills/`, `<workspace>/.kimi-code/skills/`, `<workspace>/.claude/skills/`
- built-in skills: embedded into the binary via `go:embed` under `internal/builtin_skills/skills/<name>/SKILL.md`

The `.claude/skills/` paths follow the Agent Skills open standard (agentskills.io) and Claude Code convention, so skills dropped into those directories by other tools are discovered by Ally without changes. Ally-native paths (`.agents/skills`, `.kimi-code/skills`) are scanned first, so on a name conflict the Ally-native skill wins via the `seen`-map dedup; the Claude-convention path is a fallback. Scope precedence overall: project > user > extra > builtin, matching `buildSkillListingMeta`. Built-in skills ship with Ally, require no files on disk, cannot be deleted by the user, and are still subject to `disabledSkills` (can be turned off in Settings → Skills).

Supported layouts:

- directory skill: `skill-dir/SKILL.md`
- standalone Markdown skill: `skill-name.md`
- built-in skill: `internal/builtin_skills/skills/<name>/SKILL.md` (embedded, parsed via `parseSkillContent`)

`parseSkillFile()` reads YAML frontmatter fields:

- `name`
- `description`
- `type`
- `whenToUse` (Ally-native) or `when_to_use` (Agent Skills open standard / Claude Code) — both accepted, Ally-native spelling wins when both are present

Other Agent Skills fields (`disable-model-invocation`, `user-invocable`, `allowed-tools`, `context`, `paths`, etc.) are ignored — they encode Claude Code-specific behaviors Ally does not implement. If no usable frontmatter is found, the filename or parent directory becomes the skill name.

Built-in skill loading:

- `builtinSkillEntries()` in `internal/app/builtin_skills.go` walks the embedded `skills/` tree exposed by the leaf package `ally-dev/internal/builtin_skills` and returns `SkillDefinition` entries with `Source="builtin"` and `embeddedContent` populated.
- `readSkillContent()` returns `embeddedContent` directly for built-in skills; user/project skills still go through `os.ReadFile`.
- Adding a built-in skill only requires creating `internal/builtin_skills/skills/<name>/SKILL.md` with YAML frontmatter — no other change is needed.

Currently shipped built-in skills:

- `playwright-cli` — drives a real browser through the `playwright-cli` npm package via `run_command`. On first use it checks for `playwright-cli` on PATH, asks the user before running `npm install -g @playwright/cli@latest`. Browser selection prefers system-installed browsers to avoid extra downloads: `--browser=msedge` on Windows (system Edge), and on macOS/Linux it probes for system Chrome/Edge before falling back to `ask` + `playwright-cli install-browser` chromium. The skill deliberately defers command/parameter details to `playwright-cli --help` and only documents Ally-specific integration conventions and the install/browser-detection workflow.
- `review` — automatically reviews an explicitly named range, current workspace changes, or the latest relevant commit without asking the user to identify a requirement source. It derives expectations from the latest concrete request first, then uses task text, commit context, related docs, existing behavior, and tests as evidence. Large reviews may use parallel internal checks, but the final report is concise, plain Chinese with each real issue explained by its location, impact, and concrete fix; missing standalone requirements never pause the review.
- `codegraph` — generates or incrementally updates `CODEGRAPH.md` at the workspace root as an LLM-optimized code graph for downstream AI coding agents. Detects language/build from root config files, walks the module tree with `list_files`, and writes six fixed H2 sections (`Language and Build`, `Module Hierarchy`, `Key Types / Interfaces`, `Call Flow / Data Flow`, `Inter-Module Dependencies`, `Hot Paths / Files to Focus`) using workspace-relative paths and semantic tags. When `CODEGRAPH.md` already exists it updates only changed sections in place via `edit` rather than regenerating from scratch; output is plain-text bullets/indented arrows (no Mermaid/tables), capped at 800 lines, and verified by read-back.

### Enable/Disable

Skill settings are controlled by `disabledSkills` in `ConfigState`.

- Default state: all discovered skills are enabled.
- `DeactivateSkill(name)`: adds the skill name to `disabledSkills` and writes config.
- `ActivateSkill(name)`: removes the skill name from `disabledSkills`, reads the skill file, and returns a rendered `<kimi-skill-loaded>` block.
- `ClearSkills()`: disables all currently discovered skills and writes config.
- `GetActiveSkills()`: returns the currently enabled skill names.

Disabled skills remain visible in Settings but:

- are not injected into the system prompt metadata
- are not available to the model through the `skill` tool
- are marked off in the Settings → Skills page

### Full Skill Loading

Full skill content is loaded only when explicitly requested:

- user slash command: `/<skillname>` or `/skill:<name>`
- model tool call: `Skill({skill, args})`, only if enabled

The loaded content is wrapped as:

```xml
<kimi-skill-loaded name="..." source="..." dir="..." args="...">
...
</kimi-skill-loaded>
```

Frontend behavior:

- Settings → Skills toggles enable/disable state only; turning a switch on does not inject full skill content into chat.
- Slash command activation explicitly loads the full skill and starts a model turn with that loaded block.

---

## Tool Architecture

Built-in tools are declared in `chatTools()` as OpenAI function tools.

Key rules:

- Built-in schemas use strict object schemas with `additionalProperties: false`.
- `executeTool()` decodes JSON with `DisallowUnknownFields()` so typoed parameters fail loudly.
- MCP tools are appended dynamically and keep their upstream schemas.
- Plan mode blocks side-effectful tools and MCP tools.
- `wait` requires `seconds` and a short user-visible `reason`, is cancellable through the active run context, and must be the only tool call in its batch.
- Tool results use `{ok, data, error}`.

Built-in model-facing tools:

| Tool | Purpose |
|------|---------|
| `list_files` | List files/directories with depth and limit controls |
| `read` | Read one or many local files; text returns raw copyable content, documents return extracted text |
| `edit` | Atomically apply one or many exact replacements to one local file |
| `create_file` | Create/overwrite text files |
| `delete_path` | Delete files/directories |
| `grep_files` | Regex search through bundled ripgrep, with PATH fallback in development |
| `run_command` | Shell command execution with safety checks |
| `wait` | Pause the current agent run for a cancellable 1–3600 second delay |
| `http_request` | Bounded HTTP/HTTPS API request |
| `web_fetch` | Bounded webpage fetch and readable-text extraction |
| `remote_*` | SSH remote list/read/edit/create/delete/run commands |
| `calculate` | Deterministic local math expression evaluator |
| `ask` | Pause the visible main Agent session for one or more user questions |
| `todo_write` | Session todo management; at most one `in_progress` item at a time, mark done before advancing |
| `memory_read` | Read one full global memory Markdown file |
| `memory_write` | Create/update one global memory Markdown file |
| `subagent` | Spawn a sub-agent for a scoped task |
| `scheduled_task` | Create, list, or delete temporary isolated Agent tasks for the current Ally process |
| `create_goal`, `update_goal`, `get_goal` | Goal mode lifecycle |
| `skill` | Load an enabled skill |

MCP tools are named:

```text
mcp__<serverName>__<toolName>
```

---

## Read/Edit Architecture

`read` is the only model-facing local read tool.

Accepted read forms:

- Model-facing calls use only `files`: an array of one or more `{path, startLine?, endLine?, sheet?}` requests.
- Backend compatibility fields may still accept the older `path`, `paths`, and shared-range forms, but they are not exposed in the tool schema.

Text files:

- must be UTF-8-ish text
- reject binary/NUL content
- return raw LF-normalized text that can be copied directly into `edit.changes[].oldText`
- missing paths and directory targets are silently omitted from the returned `files` array (an ignored-only batch succeeds with an empty array); other partial failures stay in the corresponding file result with `errorCode` when known
- include metadata: `startLine`, `endLine`, `nextStartLine`, `totalLines`, `truncated`, `version`, `lineEnding`; `version` is a 12-character lowercase Crockford Base32 prefix derived from SHA-256 content and is compared case-insensitively

Document files:

- `.docx`, `.pptx`, `.xlsx`, `.pdf`
- return extracted text
- are marked non-editable
- extraction algorithms live in `internal/tools/read` (pure stdlib, no App coupling)

Range semantics for model-facing reads:

- omit both `startLine` and `endLine` to read the whole file
- provide only `startLine` to read from that line through the end of the file
- provide only `endLine` to read from line 1 through that inclusive line
- provide both for an inclusive range
- `lineCount`, `contextBefore`, `contextAfter`, and `offset` are not model-facing parameters
- hard output limits may still set `truncated` and `nextStartLine`; the model should request another explicit range only when the remaining content is actually needed
- plain-text range previews count and locate lines with bounded-memory linear scans instead of materializing one string entry per file line, so tiny reads remain safe on million-line files and very long single lines stay UTF-8/budget bounded

The model-facing `edit` tool has one cross-file batch exact-replacement mode. Line-range and legacy exact-string helpers remain backend compatibility APIs and are not exposed to the model.

`planLocalEditBatch()` in `orch_edit_plan.go` is the **only** normalization boundary for local model-facing edits. Both `orch_batch_policy.go` conflict detection and `orch_edit.go` execution must consume that plan; do not independently parse, canonicalize, or merge `edit.files` in either layer. Pure diff and changed-range algorithms live in `internal/tools/edit`; `orch_edit.go` is the app-owned execution boundary for local edits.

Edit parameters:

- `files` (1–20 items)
- each file contains `path`, required `version` from `read`, and 1–50 `changes`
- each change contains non-empty `oldText` and `newText`; one call permits at most 200 total changes

Important edit contract:

- Read the file first with `read`.
- `version` is mandatory for model-facing local and remote edits. It is a short optimistic-concurrency token; a stale value fails with `E_VERSION_MISMATCH`, and malformed values fail with `E_BAD_VERSION`.
- Successful edits return the new `version` per file. It may be reused directly for a follow-up edit when the exact current `oldText` is already known; re-read only when content is unknown, external modification is possible, or a version/match error occurs.
- Every non-no-op `oldText` is matched against the same original version snapshot and must occur exactly once. Ambiguous exact matches return optional structured `details` with at most three raw UTF-8 candidate previews, clipping flags, line ranges, and recovery guidance; the detail JSON is capped at 4 KiB so callers can issue a narrow `read` without receiving the full file.
- For a multi-line whole-line block only, exact-match failure may fall back to ignoring leading spaces/tabs on each line. The fallback succeeds only for one unique candidate and safely rebases `newText` to the file's actual base indentation; body text is never fuzzy-matched.
- Changes whose normalized `oldText` and `newText` are identical are ignored and reported as warnings. An all-no-op local batch succeeds without writing the file.
- Matches must not overlap. The backend locates all effective matches first, applies them from the end of the file backward, and writes once.
- Repeated local `files` entries resolving to the same physical path and using the same version are merged into one original-snapshot edit plan; conflicting versions fail before writing.
- All files are validated before writes. Any invalid, missing, ambiguous, overlapping, or stale effective change fails the entire call without modifying any file.
- Empty `newText` deletes `oldText`; insertion replaces a unique anchor with the anchor plus inserted content.
- Put all independent changes across affected files in one call to minimize model round trips. Each file is written once; a later commit failure triggers best-effort rollback of earlier writes.
- `remote_edit` uses `{target, files}` and the same per-file `version`/`changes` contract as local edit.
- Multiple separate file-mutation tool calls targeting the same normalized local or remote path in one tool-call batch are all rejected with `E_WRITE_BATCH_CONFLICT`; no mutation for that path is executed. Repeated entries within one local `edit` call are instead merged as described above.
- Built-in file mutations execute in `toolCallIndex` order after non-file tools complete.
- backend compatibility APIs may continue using SHA-256 and exact-string helpers internally

---

## MCP Architecture

`mcp.go` owns MCP lifecycle.

Network MCP transports use Ally's proxy-aware HTTP client. Stdio MCP servers inherit the normalized proxy environment. Saving changed proxy settings reconnects MCP servers so the new transport takes effect.

Config path:

```text
~/.ally_agent/mcp.json
```

Settings page:

- Settings → MCP edits raw JSON
- Save reconnects all MCP servers
- Server status is shown with connected/connecting/failed states

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

State management:

- Vue 3 `<script setup>`
- plain `ref()` / `reactive()`
- no Vuex/Pinia
- prompt history remains in `localStorage`; session index and completed UI snapshots are persisted by the backend in local files

Major UI regions:

- header with workspace tabs, history dropdown, plan indicator, settings, window controls
- chat message area
- command menu (`/`)
- session switcher
- todo panel
- composer
- `ComposerInfoBar`
- settings modal

Settings pages:

- General: custom prompt, close-to-tray toggle (`closeToTray`, default on), launch-at-login switch (`GetAutostartEnabled`/`SetAutostartEnabled`)
- Models: provider/model presets and active model selection
- Skills: enable/disable discovered skills; persisted through `disabledSkills`
- MCP: raw MCP config editor and server status
- About: GPLv3 notice, warranty disclaimer, and source repository link
- Network: proxy off/system/manual selection, fixed system-proxy detection, bypass list, redacted status, and a bounded connection test

The composer task-center button opens `TaskCenterPanel`, whose controlled tabs separate temporary scheduled tasks from managed background services. Cards show bounded previews; full scheduled output and service buffers open in a large scrollable modal. Active service buffers refresh while the modal is open.

The composer statistics button opens `TokenStatsModal`, which queries `GetTokenStats()` on every open and renders dependency-free SVG charts for provider, model, source, workspace, daily/hourly usage, input/output volume, request/session/activity counts, and cache hit rate.

Runtime events are registered through Wails `Events.On()` and routed by `sessionId` and `runId`.

Frontend-specific rendering:

- MarkdownIt for Markdown
- highlight.js with the Darcula theme and compact line counts for code blocks
- Markdown HTTP(S)/mailto links are intercepted and opened through Wails `Browser.OpenURL` instead of navigating the embedded WebView
- bounded render cache for non-streaming Markdown
- streaming messages bypass cache
- Streaming text deltas are batched to roughly 20 FPS so the active Markdown tree is not reparsed and replaced every display frame.
- Mermaid diagrams render sequentially during browser idle slots only when they approach the viewport. Rendered SVG DOM is unloaded again beyond the viewport retention margin and restored from a bounded 16-entry / 2M-character LRU when available; evicted diagrams rerender on demand. Drag and wheel transforms are coalesced to one animation-frame write, and GPU `will-change` promotion is held only briefly while transforming. Diagrams use a warm Darcula-derived base theme and remain click-activated viewports supporting cursor-centered wheel zoom, pointer dragging, and double-click reset. Escape or a pointer press outside the diagram clears its interaction focus before other Escape actions run.
- `displaySourceMessages` inserts archive placeholders for large histories without mutating true session messages
- tool card components render read groups, diffs, command output, MCP tools, and sub-agent progress
- `render_html` keeps the iframe unmounted while arguments stream, then mounts one script-enabled, origin-isolated `srcdoc` iframe after completion. A `postMessage` bridge reports height while CSP blocks external resources.
- `AskToolCard` renders one question per Tab, supports multiple selections and custom answers, and submits all answers together

UI internationalization:

- `frontend/src/i18n.mjs` is the source of truth for UI translations and locale helpers.
- Only `zh-CN` and `en-US` are supported. The primary `navigator.languages` / `navigator.language` entry decides the locale at startup: values beginning with `zh` use Chinese; all others use English.
- The root Naive UI `NConfigProvider` and discrete APIs must receive the matching component locale and date locale.
- New user-facing UI text must be added to both locale tables and referenced through `t()` / `$t()`; do not translate model output, file contents, command output, or raw tool results.
- `AppHeader` always shows a GitHub repository button that opens the Ally project through the system browser. Startup performs one best-effort latest-release check; when a newer semantic version exists, that same button changes into the green update icon.

---

## Commands

| Command | Description |
|---------|-------------|
| `/new` | Create a new session |
| `/sessions` | Show sessions; `/switch N` switches session |
| `/init` | Explore project and generate AGENTS.md |
| `/goal` | Start goal mode |
| `/skills` | Show discovered skills and enabled/disabled status |
| `/clearskills` | Disable all discovered skills |
| `/<skillname>` | Explicitly load a skill |
| `/skill:<name>` | Alternate explicit skill loading syntax |
| `/note` | Save durable project knowledge |
| `/remember` | Compatibility alias for `/note` |
| `/compact` | Compact the current session history |
| `/reload` | Reload model config file |

---

## Sessions, Context, And Token Accounting

The backend owns the session index at `~/.ally_agent/sessions/index.json` and stores each completed UI snapshot as a separate gzip JSON file under `~/.ally_agent/sessions/<escaped-session-id>.json.gz`. The frontend loads only index metadata at startup and imports one snapshot when the user selects a session; inactive session messages are released from frontend memory. Only successful `run:done` turns and explicit successful compaction update a snapshot. Failed, cancelled, or still-streaming turns remain process-local and are excluded from later model requests and snapshots. Closing the window waits for queued local-file writes. Legacy `localStorage`/IndexedDB session data is migrated once and then cleared.

Backend model histories are separate process-memory histories keyed by session ID and persisted as gzip-compressed JSON (`~/.ally_agent/histories/<escaped-session-id>.json.gz`). Loading remains compatible with legacy uncompressed `.json` files. On restart, backend history remains the model-context source of truth; frontend history contributes only a detected new tail. Alignment uses the longest overlap between the backend visible-history suffix and a contiguous frontend range, so an older frontend snapshot prefix is not duplicated. When backend compaction intentionally removes textual overlap, only the latest frontend request item is appended. Saved histories are bounded by an estimated-token budget rather than a fixed message count and are trimmed only at user-message boundaries, preserving assistant tool-call/result protocol groups. Failed and cancelled runs do not replace the last completed backend history.

Backend session cleanup distinguishes `ReleaseSession`, which frees in-memory history/goal/todo/context state while preserving the persisted history file, from `DeleteSession`, which also removes both compressed and legacy history files. Frontend explicit deletion uses `DeleteSession`; runtime session eviction uses `ReleaseSession`.

Workspace/session invariants:

- Every workspace Tab owns a valid `sessionId`; creating or selecting a session immediately updates that link, including sessions without a selected workspace.
- Selecting a session already owned by another Tab activates that Tab instead of silently rebinding a different Tab.
- Runtime events with an explicit `sessionId` never fall back to the currently visible session. Terminal events are accepted only when their `runId` still matches the session's current run.
- `Ctrl/Cmd+Left/Right` workspace navigation is disabled inside inputs, textareas, selects, and contenteditable elements.
- `CancelRun` cancels the context immediately but retains `runs`/`runSessions` registration until `runChat` actually exits, so session release/deletion cannot race a cancelling run.

Context accounting:

- `GetContextBreakdown()` reports system prompt parts, history, current session, tools, and workspace context.
- System prompt parts are shown separately in the context popover, including AGENTS.md/project instructions.
- Global memory index is shown as its own system prompt part in the context popover.
- Workspace token usage accumulates provider-reported usage when available, with estimates as fallback.
- Historical token statistics retain 90 local days under `~/.ally_agent/stats/`; a bounded non-blocking queue keeps recording and persistence off the chat hot path, startup loading enforces file/record/value limits, and shutdown waits for the final flush before Wails exits. `GetTokenStats()` aggregates provider/model/source/workspace/day/hour/cache/session dimensions from an in-memory snapshot.

Long-render optimization:

- Frontend may archive visible old messages for rendering performance.
- Visual archiving does not mutate `session.messages`; completed sessions are separately pruned by the runtime retention limits below.
- Backend context construction receives the retained conversation history, capped by `MAX_MODEL_HISTORY_MESSAGES`.
- Normal rendering is bounded to 180 messages / 220k estimated characters; expanded archives are still bounded to 360 messages / 440k characters rather than mounting the full conversation.
- Completed frontend sessions retain the latest 400 conversation messages and 260 renderable messages in memory, with at most 200 unpinned sessions. Running, active, and workspace-linked sessions are protected from session eviction.
- Backend UI snapshots retain the same bounded 400 conversation / 260 renderable window used by runtime archiving; large tool previews, edit arguments, attachment payloads, and Diffs are stripped or individually truncated before gzip JSON serialization. The frontend keeps only active, running, and workspace-linked session messages in memory.
- Media previews use revocable Blob URLs. Images render from a bounded thumbnail while the original Base64 payload is retained only when it is eligible for model input.
- Diff rendering uses exact LCS only below a fixed matrix budget. Larger replacements use a linear-memory prefix/suffix fallback, and multi-file edit cards stay collapsed until explicitly expanded.
- Workspace history is normalized and deduplicated, retains the latest 30 paths, and is displayed in a fixed-height scrollable dropdown.

---

## Sub-Agents And Goal Mode

`subagent` starts a child agent loop without an artificial step or wall-clock limit. Cancellation still follows the parent run or an explicit stop request.

Sub-agents receive connected MCP tools and share the manager's invalid-session reconnect path. Interactive/nested tools such as `ask` and `subagent`, plus parent-owned goal/todo/scheduled/memory-write state, remain excluded.

Completed sub-agent records release their cancel function, keep at most 100 tool events each, and are globally pruned to the latest 50 completed records. Running records are never pruned.

Sub-agent UI:

- backend emits `sub:*` events
- `sub:spawn` includes the parent tool-call identity so the frontend upgrades the original `subagent` card in place
- `tool:result` / `tool:error` finalize that same inline sub-agent card instead of appending raw JSON result cards
- frontend displays a lightweight inline sub-agent progress row

Goal mode:

- `create_goal` stores an active goal with objective, optional completion criterion, and turn budget.
- `runChat()` can continue turns automatically while the goal remains active.
- `update_goal` marks the goal `complete`, `blocked`, or `paused`.
- Stable goal objective/rules remain in the system context; dynamic status and turn counters are appended near the request tail as `<ally-goal-progress>` to preserve provider prefix-cache reuse.

Interactive ask behavior:

- `ask` is available to the visible main Agent session in YOLO and GRILL modes, but is excluded from sub-agents and scheduled tasks.
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

Legacy config fallback:

```text
%APPDATA%/KimiAgentLab/config.json
```

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
- `run_command` keeps cwd inside the workspace, permits outside reads, null-device redirection, and creation of new outside paths, but refuses commands that may modify/delete existing outside paths or perform explicit deletion.
- Command safety uses lightweight shell-aware invocation parsing in `internal/tools/command`: quoted/search data is not treated as executable syntax, nested shell payloads and command substitutions are inspected, managed deletions are allowed only when every deletion in the compound command is managed, and source/destination-aware mutation analysis checks explicit write targets rather than every referenced path.
- `checkCommandSafety()` in `orch_command_safety.go` is the app-owned boundary for workspace roots, path existence, `E_COMMAND_BLOCKED` / `E_PATH_OUTSIDE`, and user-facing explanations; it must consume the command package's semantic analysis instead of adding independent full-string risk regexes.
- The model-facing system prompt and `run_command` schema explain how to recover from `E_PATH_OUTSIDE`: read the Chinese reason/target, avoid unchanged retries, choose a new or workspace target, and replace dynamic redirections with literal paths.
- Prefer `delete_path` / `remote_delete_path` over shell deletion.
- `readTextFile` rejects binary files using NUL checks.
- Release packages bundle ripgrep under the executable's `tools/` directory; development builds fall back to `rg` from `PATH`.
- On Windows, `run_command` and `background_process` prefer **bash** from Git for Windows over PowerShell. Detection order: a validated `gitBashPath` setting (manual override) → Git for Windows common install paths → derive Git Bash from `git.exe` on `PATH` → a `bash.exe` on `PATH` only when it belongs to the same Git for Windows installation → fallback to PowerShell (`pwsh.exe` → `powershell.exe`). Arbitrary `bash.exe` launchers such as `C:\Windows\System32\bash.exe` (legacy WSL) are rejected because their Linux PATH and argument forwarding break Windows tool discovery and shell expansion. When Git Bash is not detected, startup warns the user to set `gitBashPath` in Settings. The system prompt dynamically reflects which shell is active so the model generates correct syntax. Linux/macOS always use `bash -c` and ignore `gitBashPath`.
- On macOS/Linux, `infra_shell_env.go` probes the user's login shell once (`$SHELL -l -c /usr/bin/env`, with an OS-account shell fallback), appends only missing absolute `PATH` entries to the inherited environment, and shares that environment with `run_command` and `background_process`. Probe failures leave the original environment unchanged; other profile variables such as `GOPATH` and `NVM_DIR` are not imported.
- When bash is active on Windows, safety checks detect both Windows-style (`C:\...`) and MSYS2-style (`/c/...`) absolute paths outside the workspace.
- Tool output is capped by `maxToolOutput`.
- HTTP tools use bounded response sizes, timeouts, redirect limits, and clear user agent defaults.
- macOS/Linux command subprocesses use the one-time login-shell `PATH` enrichment from `internal/app/infra_shell_env.go`; actual commands remain non-interactive `bash -c` invocations.
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
- Events: lowercase with colon separators, e.g. `run:delta`, `tool:result`, `mcp:status`.
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
- Keep each cross-layer contract at one source of truth: `chatTools()` for schemas, `executeTool()` for strict dispatch/decoding, `orch_edit_plan.go` for local edit normalization, `orch_batch_policy.go` for batch barriers/conflicts, `orch_edit.go` for app-owned edit execution, `internal/tools/edit` for pure diff/range algorithms, `internal/tools/pathutil` for workspace path resolution and safety checks, `infra_result.go` for result envelopes/compaction, and `infra_stream.go` for stream throttling.
- Do not independently parse, canonicalize, deduplicate, or infer the same request in multiple layers. If a lower layer produces a normalized plan, upstream policy checks and downstream execution must consume that plan.
- Workspace path resolution and boundary checks live in `internal/tools/pathutil`. The app package keeps only thin package-level wrappers (delegating to pathutil through a host-neutral `Runtime` interface) so existing call sites stay unchanged; do not re-implement `insideRoot`, `safeJoin`, `insideWriteRoot`, or `resolveReadablePath` in app or any other tool package.
- Keep the project in one Go package unless a package boundary has a clear dependency direction and Wails binding impact has been verified. Mechanical same-package extraction is preferred before introducing new packages.

### Cross-layer change procedure

- Before changing a tool or event contract, trace the complete path: schema → strict decoder → batch policy → executor → result envelope → frontend event/card rendering → tests → `AGENTS.md`/`CODEGRAPH.md` when the architecture changes.
- When a request contains nested mutations, normalize it once before conflict detection. Repeated entries inside one local `edit` call may merge into one physical target; separate mutation tool calls targeting the same normalized path must still conflict.
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

## Current Architectural Notes

- `prov_model.go` is the provider boundary; avoid leaking provider-specific request shapes into Agent orchestration.
- `app.go` owns Agent orchestration and long-lived state, but must not import Wails runtime. Desktop lifecycle/dialog/window behavior belongs in `host_desktop.go`; all UI event publication goes through `eventSink` in `host_events.go`.
- Modify each cross-layer contract at its unique boundary: edit normalization in `orch_edit_plan.go`, pure edit algorithms in `internal/tools/edit`, tool-batch policy in `orch_batch_policy.go`, app-owned edit execution in `orch_edit.go`, workspace path resolution in `internal/tools/pathutil`, result envelopes/compaction in `infra_result.go`, and stream throttling in `infra_stream.go`.
- Tool implementations are split: pure algorithms live in `internal/tools/<name>/` (no `*App` receiver, no ConfigState dependency), while app-owned orchestration (workspace resolution, serialization guards, event sinks, lifecycle) stays in `internal/app/<name>.go` as thin wrappers that inject `*App` as the host-neutral `Runtime`. The app package keeps package-level lowercase wrappers (e.g. `safeJoin`, `workspaceRoot`, `insideRoot`) that delegate to pathutil so existing call sites stay unchanged; do not duplicate these helpers elsewhere.
- `chatTools()` is the source of truth for built-in LLM-facing schemas; `executeTool()` remains the dispatch and strict JSON decoding boundary.
- The model-facing `background_process` tool supports four actions: `start` (returns immediately with the service id, no readiness wait), `stop` (terminate by id), `list` (metadata for all tracked services, no output tails), and `read` (bounded tail of one service's output, default 8 KiB, max 32 KiB). This lets agents run frontend/backend dev processes without blocking the agent loop and inspect their output on demand without overloading the model context. `StartService`, `StopService`, and `ListServices` remain available as Wails/backend APIs for the Task Center UI.
- Background-process state contains active processes only. Records are removed after `cmd.Wait()` completes, and the backend rejects starts beyond the 8-process active limit.
- The model-facing `wait` tool is for short, concrete asynchronous delays only. It is limited to 3600 seconds, disabled in grill mode, and displayed in the UI with a local countdown.
- Grill mode is session-local and request-scoped through `ChatRequest.grillMode`. The backend enforces an `ask`-only interview protocol: plain-text questions are retried, while a marked no-questions-left summary ends the mode and returns the session to YOLO. Side-effectful and MCP tools remain filtered and execution-guarded. Users may switch from an active Grill ask back to YOLO, which cancels the pending run.
- The composer footer owns a two-option run-mode switch: YOLO is the default execution mode and GRILL is the session-local interview mode. Its model picker groups presets by normalized provider, sorts providers and model IDs naturally, and opens with the active provider expanded while other groups remain collapsible.
- `render_html` completion updates the original tool card with one sandboxed iframe rather than appending a second result card.
- `web_fetch` keeps the default readable-page payload intact for model context. HTTP/web results use a larger dedicated model cap and include explicit reduction metadata only when that cap is exceeded.
- The scheduled-task drawer is opened from `ComposerInfoBar`, displays full task state and latest output, and supports manual deletion/cancellation. Model-facing `scheduled_task.list` returns bounded metadata without stored output and must not be polled.
- `compactToolResultForModel()` is the source of truth for model-side tool-result reduction.
- `read` is the model-facing local read tool; `read_file` and the legacy `batch_read` alias may exist for backend compatibility but should not be exposed to the model as primary names.
- Skills are default-enabled metadata only; disabled skills persist through `disabledSkills`.
- Full skill Markdown is loaded only by explicit user slash command or enabled `skill` tool call.
- Settings → Skills manages enable/disable state and does not inject full skill content.
- Settings → MCP manages raw MCP JSON and reconnects servers.
- Settings → Models owns provider presets and the current active provider/model. Known provider/model quick setup is generated from `docs/model_api.json` into a compact, lazily loaded frontend catalog; only Ally-compatible text-output models with tool calling are included, while custom configuration remains available. Model presets can be exported as unencrypted JSON (including API keys) and incrementally imported; normalized `providerName + model` is the identity, matching entries are replaced, and unrelated presets are retained.
- The model editor's connection test sends one isolated minimal request using the unsaved form values; it does not mutate or persist the active configuration.
- macOS self-update relaunches through a detached argv/pipe helper instead of shell interpolation and retains the previous `.app.bak` through a startup grace period.
- The context popover should keep system prompt parts separate, especially AGENTS.md/project instructions.

---

## Wails Event Emission Map

后端模块只调用 `App.emit()`；该方法与 `eventSink` 位于 `host_events.go`，Wails v3 适配器（`wailsEventSink`）再调用 `app.Event.Emit`。Wails 启动、窗口和系统对话框位于 `host_desktop.go`。前端在 `App.vue` 的 `bindRuntimeEvents()` 中通过 `Events.On()` 统一注册，`runtimeEventOffs` 跟踪卸载。Agent/runtime 模块不得直接调用 Wails 事件 API。

### 高频流（双层节流，仅这两条）

| 事件 | 后端节流位置 | 前端缓冲位置 |
|------|--------------|--------------|
| `run:delta` / `run:reasoning` | `runStreamDeltaEmitter` (`infra_stream.go`, 32ms / 512B)，流末 `flush()` 兜底；`run:image` 发送前先 flush | `queueStreamDelta` + `setTimeout(48ms) → requestAnimationFrame` (`App.vue`) |
| `tool:update` | `toolCallProgressTracker.eventsWithForce` (`infra_stream.go`, 200ms / 2048B)，超阈值且在窗口内早 continue；`forceEvents()` 流末绕过节流。`run_command` 约每 120ms 发布累计输出并在结束前强制最终快照 | `bufferToolUpdate` + `setTimeout(120ms) → requestAnimationFrame`；命令卡短输出按内容收缩，最多约六行后滚动，并自动跟随末尾 |

### 生命周期事件（天然低频，无需节流）

**Chat loop (`app.go` `runChat`)**：
- `run:start` / `run:llm_wait` / `run:done` / `run:error` — 每轮 1 次
- `run:compact` / `run:compacted` — 压缩时
- `run:image` — 图片 delta
- `tool:result` / `tool:error` — 每个工具调用 1 次 (`app.go`)
- `tokens:update` — `recordWorkspaceTokenUsage` 每个 LLM step 1 次 (`app.go`)
- `token stats`: 每个主 Agent / 子 Agent LLM step 投递一次，后台异步落盘到 `~/.ally_agent/stats/<date>.json`
- `tokens:reset` — `ResetWorkspaceTokenUsage` (`app.go`)

**Ask (`app.go`)**：`ask:ready` / `ask:closed` — 每次 ask 1 次

**Goal (`app.go`)**：`goal:update` — `recordGoalTurn` / `updateGoal` 每轮 1 次

**Todo (`app.go`)**：`todo:update` — `emitTodoUpdate` 写入时

**MCP (`app.go`)**：`mcp:status` — 服务器状态变更时（一次发全部 server 状态）

**Dependencies/Config (`app.go`)**：`dependency:missing` / `config:warning` — 罕见，前端按 tool 去重

**Services (`orch_services.go`)**：`service:update` — 仅 `startServiceWithConfig` / `finalizeService` / `stopService` 三处，不随输出滚动触发

**Scheduled tasks (`orch_scheduler.go`)**：`scheduled:update` / `scheduled:run_start` / `scheduled:run_done` / `scheduled:run_error` — 调度生命周期点

### 子代理 / 调度任务路径

`app.go` `executeDelegate` 调用 `streamModelResponse(ctx, cfg, model, messages, tools, nil)` —— **传入 `nil` onEvent**，子代理和调度任务不发任何 `run:delta` / `tool:update` 流式事件。只发 step 级事件：
- `sub:spawn` / `sub:done` / `sub:error` — 生命周期
- `sub:tool:start` / `sub:tool:result` / `sub:tool:error` — 每工具调用 1 次
- `sub:step` — 每 LLM step 1 次

### 事件命名约定

lowercase + 冒号分隔，如 `run:delta`、`tool:result`、`mcp:status`、`sub:step`、`scheduled:update`、`service:update`、`todo:update`、`goal:update`、`ask:ready`、`tokens:update`、`dependency:missing`、`config:warning`。

---

## Performance And Memory Notes (Ally-specific)

- `tool:update` 事件在 `toolCallProgressTracker.eventsWithForce` (`app.go`) 中带 `toolUpdateThrottle = 200ms` + `toolUpdateThreshold = 2048` 字节节流。累计 `arguments` 超过阈值且仍在窗口内时早 continue，跳过 O(len(args)) 的 state 构造。`forceEvents()` 在流结束后绕过节流，保证最终参数状态送达。`run_command` 执行时复用同一事件，以约 120ms 间隔发送有变化的累计 stdout/stderr，并在命令结束前发送最终输出快照；前端固定高度展示并跟随末尾。测试见 `TestToolCallProgressTrackerThrottlesLargeUpdates`。
- 前端 `App.vue` 用 `toolUpdateBuffers` Map + `setTimeout(120ms) → requestAnimationFrame` 批量 flush `tool:update`，并在 `tool:result` / `tool:error` / `run:done` / `run:error` / `run:cancelled` 处理前显式 `flushToolUpdateBuffer()`。`streamBuffers` 走同样的 `queueStreamDelta` 模式处理 `run:delta`。
- 单 session `localStorage` 预算 240KB，大 tool 预览 / edit 参数 / 附件 Base64 / Diff 在序列化前剥除或截断。
- 前端 Mermaid SVG DOM 视口外卸载，16 条 / 2M 字符 LRU；`render_html` 流式期间不挂载 iframe，完成后再挂一个 sandboxed iframe。
- `run_command` / `background_process` 后端 rolling buffer 512KB / 进程，最多 8 个活动进程；服务停止或退出后立即清理记录。
- 媒体预览用 revocable Blob URL，session 驱逐时 `URL.revokeObjectURL`；原图 Base64 仅在需要 model 输入时保留。
- `AppHeader` 容器 `--wails-draggable: drag`，内部所有按钮 / 输入 / 下拉必须 `no-drag`；`ComposerInfoBar` 所有交互 span/button `no-drag`，否则下拉/点击会被拖拽逻辑吞掉。
- 后台 goroutine（chat run / MCP / scheduled task / service process）都派生自 `app.ctx`，窗口关闭后随 ctx 取消。

