# Ally — AGENTS.md

This file is read by AI coding agents. Keep it current when the app architecture, tool contracts, prompt pipeline, or UI workflows change.

---

## Build & Run Commands

| Command | Description |
|---------|-------------|
| `wails dev` | Run the desktop app in development mode with hot reload |
| `wails build` | Build a distributable desktop binary |
| `go build ./...` | Compile the Go backend |
| `go test ./...` | Run Go tests |
| `cd frontend && npm install` | Install frontend dependencies |
| `cd frontend && npx vite build` | Build the Vue frontend |
| `cd frontend && npm test` | Run frontend unit tests when present |
| `wails generate module` | Regenerate Go-to-JS bindings after adding or changing exported Go methods |

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
   - `npm --prefix frontend ci`
   - `npm --prefix frontend run build`
   - `go test ./...`
   - `go build ./...`
   - `wails build -clean -s -skipbindings`
4. Commit the release-related repository changes and push `main` to `origin`. Recheck that the worktree is clean and local `HEAD` equals `origin/main`.
5. Publish a non-draft GitHub Release targeting `main`, with tag `<new-tag>`, title `Ally <new-tag>`, and the prepared notes. Authentication must come from GitHub CLI login or a `GITHUB_TOKEN` environment variable with repository contents write permission. Never place a token value in repository files, release notes, scripts, or copied command text.

Publishing the Release triggers `.github/workflows/build.yml`, which builds and attaches the Windows x64, Linux x64, and macOS universal packages.

---

## Repository Layout

```
├── app.go                    # Main Wails-bound backend: config, chat loop, tools, sessions, skills, context accounting
├── model_provider.go         # Model provider adapter layer: OpenAI Chat, OpenAI Responses, Anthropic Messages
├── mcp.go                    # MCP manager: config loading, process lifecycle, tool discovery, tool dispatch
├── scheduler.go              # Persistent scheduled Agent tasks powered by robfig/cron
├── edit_helpers.go           # Read range, changed-line, and diff helpers for text edits
├── main.go                   # Wails app entry point, window options, app binding
├── procattr_windows.go       # Windows process attributes for hidden shell windows
├── procattr_other.go         # Non-Windows process attribute shim
├── *_test.go                 # Go tests for provider, tools, MCP, prompt/config behavior
├── wails.json                # Wails project/build config
├── frontend/
│   ├── src/
│   │   ├── App.vue           # Main Vue app: layout, settings, sessions, command handling, runtime events
│   │   ├── style.css         # Global dark theme styles, settings, tool cards, chat, modals
│   │   ├── app.css           # App entry CSS
│   │   ├── main.js           # Vue mount
│   │   ├── components/
│   │   │   ├── AllyWordmark.vue
│   │   │   ├── AskToolCard.vue
│   │   │   ├── CodeView.vue
│   │   │   ├── ComposerInfoBar.vue
│   │   │   ├── ContextUsageInline.vue
│   │   │   ├── DiffView.vue
│   │   │   ├── GitDiffModal.vue
│   │   │   ├── MessageAttachments.vue
│   │   │   ├── ReadGroupCard.vue
│   │   │   ├── RenderBoundary.vue
│   │   │   ├── ScheduledTasksPanel.vue
│   │   │   ├── SplashScreen.vue
│   │   │   ├── SubagentInlineCard.vue
│   │   │   ├── ToolCallCard.vue
│   │   │   └── WelcomeMessage.vue
│   │   └── utils/
│   │       ├── ascii.js
│   │       ├── config.mjs
│   │       ├── diff.js
│   │       └── toolPreview.mjs
│   ├── wailsjs/              # Generated Wails JS/TS bindings
│   ├── package.json
│   └── vite.config.js
└── build/                    # Build assets and platform metadata
```

---

## High-Level Architecture

Ally is a Wails v2 desktop AI coding agent.

The backend is a Go application bound into the frontend through Wails. The frontend is a Vue 3 single-page desktop UI using Naive UI. The LLM-facing core is provider-neutral: the app stores conversation state as `go-openai`-style `ChatCompletionMessage` values, then adapts those messages to the selected provider format in `model_provider.go`.

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
- `histories`: backend session history by session ID
- `pendingAsks`: blocking main-session questions waiting for frontend submission
- `disabledSkills`: persisted list of skill names disabled by the user
- `mcpManager`: active MCP manager
- `goalStates`: active goal mode state by session
- `todos`: session-local todo list
- `todoRevisions`: monotonic todo update revisions emitted with `todo:update`
- `subRuns`: sub-agent progress records
- `services`: active background processes only; exited processes are removed and at most 8 may run concurrently
- `scheduled`: persistent scheduled-task manager; definitions are stored in `~/.ally_agent/scheduled_tasks.json`
- `fileOpsMu`: serializes local write/edit/delete operations
- `gitDiffMu`: serializes/cancels git diff work
- workspace token/context caches

`ConfigState` is stored in `~/.ally_agent/config.json` and includes:

- provider fields: `providerName`, `apiFormat`, `baseUrl`, `apiKey`, `model`
- runtime fields: `workspace`, `temperature`, `maxTokens`, `contextWindow`, `planMode`
- prompt fields: `systemPrompt`, `customPrompt`
- model presets: `models`
- skill settings: `disabledSkills`

Important config behavior:

- `mergeConfig()` preserves existing config values when overlay fields are zero/empty.
- `disabledSkills` is normalized and persisted.
- Skill enable/disable operations update both memory and `config.json`.
- Frontend `defaultConfig()` also includes `disabledSkills` so saving settings does not erase skill state.

---

## Model Provider Layer

Provider adaptation lives in `model_provider.go`.

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
- Extended Thinking is not exposed as a configurable feature until thinking signatures and redacted-thinking blocks can be replayed losslessly across tool turns.

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
- `batch_read` content is intentionally not compacted so exact raw snippets remain copyable into edit changes.

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
- plan mode restrictions when enabled
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
- Disabled skills are omitted from metadata and cannot be loaded through the `Skill` tool.

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

- user skills: `~/.agents/skills/`
- project skills: `<workspace>/.agents/skills/`
- project Kimi-style skills: `<workspace>/.kimi-code/skills/`

Supported layouts:

- directory skill: `skill-dir/SKILL.md`
- standalone Markdown skill: `skill-name.md`

`parseSkillFile()` reads YAML frontmatter fields:

- `name`
- `description`
- `type`
- `whenToUse`

If no usable frontmatter is found, the filename or parent directory becomes the skill name.

### Enable/Disable

Skill settings are controlled by `disabledSkills` in `ConfigState`.

- Default state: all discovered skills are enabled.
- `DeactivateSkill(name)`: adds the skill name to `disabledSkills` and writes config.
- `ActivateSkill(name)`: removes the skill name from `disabledSkills`, reads the skill file, and returns a rendered `<kimi-skill-loaded>` block.
- `ClearSkills()`: disables all currently discovered skills and writes config.
- `GetActiveSkills()`: returns the currently enabled skill names.

Disabled skills remain visible in Settings but:

- are not injected into the system prompt metadata
- are not available to the model through the `Skill` tool
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
| `batch_read` | Read one or many local files; text returns raw copyable content, documents return extracted text |
| `edit` | Atomically apply one or many exact replacements to one local file |
| `create_file` | Create/overwrite text files |
| `delete_path` | Delete files/directories |
| `grep_files` | Regex search through bundled ripgrep, with PATH fallback in development |
| `run_command` | Shell command execution with safety checks |
| `wait` | Pause the current agent run for a cancellable 1–600 second delay |
| `http_request` | Bounded HTTP/HTTPS API request |
| `web_fetch` | Bounded webpage fetch and readable-text extraction |
| `remote_*` | SSH remote list/read/edit/create/delete/run commands |
| `calculate` | Deterministic local math expression evaluator |
| `ask` | Pause the visible main Agent session for one or more user questions |
| `todo_write` | Session todo management |
| `memory_read` | Read one full global memory Markdown file |
| `memory_write` | Create/update one global memory Markdown file |
| `agent_delegate` | Spawn a sub-agent for a scoped task |
| `scheduled_task` | Create, list, or delete persistent isolated Agent tasks |
| `create_goal`, `update_goal`, `get_goal` | Goal mode lifecycle |
| `Skill` | Load an enabled skill |

MCP tools are named:

```text
mcp__<serverName>__<toolName>
```

---

## Read/Edit Architecture

`batch_read` is the only model-facing local read tool.

Accepted read forms:

- Model-facing calls use only `files`: an array of one or more `{path, startLine?, endLine?, sheet?}` requests.
- Backend compatibility fields may still accept the older `path`, `paths`, and shared-range forms, but they are not exposed in the tool schema.

Text files:

- must be UTF-8-ish text
- reject binary/NUL content
- return raw LF-normalized text that can be copied directly into `edit.changes[].oldText`
- include metadata: `startLine`, `endLine`, `nextStartLine`, `totalLines`, `truncated`, `md5`, `lineEnding`

Document files:

- `.docx`, `.pptx`, `.xlsx`, `.pdf`
- return extracted text
- are marked non-editable

Range semantics for model-facing reads:

- omit both `startLine` and `endLine` to read the whole file
- provide only `startLine` to read from that line through the end of the file
- provide only `endLine` to read from line 1 through that inclusive line
- provide both for an inclusive range
- `lineCount`, `contextBefore`, `contextAfter`, and `offset` are not model-facing parameters
- hard output limits may still set `truncated` and `nextStartLine`; the model should request another explicit range only when the remaining content is actually needed

The model-facing `edit` tool has one cross-file batch exact-replacement mode. Line-range and legacy exact-string helpers remain backend compatibility APIs and are not exposed to the model.

Edit parameters:

- `files` (1–20 items)
- each file contains `path`, required `expectedMd5` from `batch_read`, and 1–50 `changes`
- each change contains non-empty `oldText` and `newText`; one call permits at most 200 total changes

Important edit contract:

- Read the file first with `batch_read`.
- `expectedMd5` is mandatory for model-facing local and remote edits. It is a short optimistic-concurrency token, not a security digest; a stale value fails with `E_VERSION_MISMATCH`.
- Successful edits return `afterMd5` per file. It may be reused directly for a follow-up edit when the exact current `oldText` is already known; re-read only when content is unknown, external modification is possible, or a version/match error occurs.
- Every `oldText` is matched against the same original MD5 snapshot and must occur exactly once.
- Matches must not overlap. The backend locates all matches first, applies them from the end of the file backward, and writes once.
- All files are validated before writes. Any invalid, missing, ambiguous, overlapping, or stale change fails the entire call without modifying any file.
- Empty `newText` deletes `oldText`; insertion replaces a unique anchor with the anchor plus inserted content.
- Put all independent changes across affected files in one call to minimize model round trips. Each file is written once; a later commit failure triggers best-effort rollback of earlier writes.
- `remote_edit` uses `{target, files}` and the same per-file `expectedMd5`/`changes` contract as local edit.
- Multiple file mutations targeting the same normalized local or remote path in one tool-call batch are all rejected with `E_WRITE_BATCH_CONFLICT`; no mutation for that path is executed.
- Built-in file mutations execute in `toolCallIndex` order after non-file tools complete.
- backend compatibility APIs may continue using SHA-256 and exact-string helpers internally

---

## MCP Architecture

`mcp.go` owns MCP lifecycle.

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
2. Spawn configured servers with stdio clients.
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
- sessions and prompt history are persisted in `localStorage`

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

- General: custom prompt
- Models: provider/model presets and active model selection
- Skills: enable/disable discovered skills; persisted through `disabledSkills`
- MCP: raw MCP config editor and server status
- About: GPLv3 notice, warranty disclaimer, and source repository link

Runtime events are registered through Wails `EventsOn()` and routed by `sessionId` and `runId`.

Frontend-specific rendering:

- MarkdownIt for Markdown
- highlight.js for code blocks
- bounded render cache for non-streaming Markdown
- streaming messages bypass cache
- `displaySourceMessages` inserts archive placeholders for large histories without mutating true session messages
- tool card components render read groups, diffs, command output, MCP tools, and sub-agent progress
- `AskToolCard` renders one question per Tab, supports multiple selections and custom answers, and submits all answers together

UI internationalization:

- `frontend/src/i18n.mjs` is the source of truth for UI translations and locale helpers.
- Only `zh-CN` and `en-US` are supported. The primary `navigator.languages` / `navigator.language` entry decides the locale at startup: values beginning with `zh` use Chinese; all others use English.
- The root Naive UI `NConfigProvider` and discrete APIs must receive the matching component locale and date locale.
- New user-facing UI text must be added to both locale tables and referenced through `t()` / `$t()`; do not translate model output, file contents, command output, or raw tool results.
- Startup performs one best-effort GitHub latest-release check. A newer semantic version shows a green update icon in `AppHeader`; clicking it opens the Ally GitHub repository in the system browser.

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

Frontend sessions are local UI records stored in `localStorage`.

Backend histories are separate process-memory histories keyed by session ID. They are rebuilt from frontend-supplied messages when needed.

Backend session cleanup distinguishes `ReleaseSession`, which frees in-memory history/goal/todo/context state while preserving the persisted history file, from `DeleteSession`, which also removes the persisted history. Saved backend histories are bounded to the latest 40 valid messages. Frontend explicit deletion uses `DeleteSession`; runtime session eviction uses `ReleaseSession`.

Context accounting:

- `GetContextBreakdown()` reports system prompt parts, history, current session, tools, and workspace context.
- System prompt parts are shown separately in the context popover, including AGENTS.md/project instructions.
- Global memory index is shown as its own system prompt part in the context popover.
- Workspace token usage accumulates provider-reported usage when available, with estimates as fallback.

Long-render optimization:

- Frontend may archive visible old messages for rendering performance.
- Visual archiving does not mutate `session.messages`; completed sessions are separately pruned by the runtime retention limits below.
- Backend context construction receives the retained conversation history, capped by `MAX_MODEL_HISTORY_MESSAGES`.
- Normal rendering is bounded to 180 messages / 220k estimated characters; expanded archives are still bounded to 360 messages / 440k characters rather than mounting the full conversation.
- Completed frontend sessions retain the latest 400 conversation messages and 260 renderable messages in memory, with at most 30 unpinned sessions. Running, active, and workspace-linked sessions are protected from session eviction.
- Persisted sessions have a 240k-character budget each; large tool previews, edit arguments, attachment payloads, and Diffs are removed or truncated before `localStorage` serialization.
- Media previews use revocable Blob URLs. Images render from a bounded thumbnail while the original Base64 payload is retained only when it is eligible for model input.
- Diff rendering uses exact LCS only below a fixed matrix budget. Larger replacements use a linear-memory prefix/suffix fallback, and multi-file edit cards stay collapsed until explicitly expanded.

---

## Sub-Agents And Goal Mode

`agent_delegate` starts a child agent loop with its own limited step budget.

Completed sub-agent records release their cancel function, keep at most 100 tool events each, and are globally pruned to the latest 50 completed records. Running records are never pruned.

Sub-agent UI:

- backend emits `sub:*` events
- frontend removes the temporary `agent_delegate` tool card on `sub:spawn`
- frontend displays a lightweight inline sub-agent progress row

Goal mode:

- `create_goal` stores an active goal with objective, optional completion criterion, and turn budget.
- `runChat()` can continue turns automatically while the goal remains active.
- `update_goal` marks the goal `complete`, `blocked`, or `paused`.
- Stable goal objective/rules remain in the system context; dynamic status and turn counters are appended near the request tail as `<ally-goal-progress>` to preserve provider prefix-cache reuse.

Interactive ask behavior:

- `ask` is available to the visible main Agent session in YOLO, PLAN, and GRILL modes, but is excluded from sub-agents and scheduled tasks.
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

Scheduled tasks:

```text
~/.ally_agent/scheduled_tasks.json
```

Scheduled tasks run only while Ally is open. Each execution uses fresh isolated context and a fixed workspace. Runs are globally serialized, cannot overlap with the same task, and retain only the latest bounded summary/error. Tasks persist until manually deleted; per-run defaults are 100 steps and one hour, configurable up to 1000 steps and 24 hours.

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
- `run_command` refuses explicit absolute paths outside the workspace and `~/.ally_agent`, and refuses explicit deletion commands.
- Prefer `delete_path` / `remote_delete_path` over shell deletion.
- `readTextFile` rejects binary files using NUL checks.
- Release packages bundle ripgrep under the executable's `tools/` directory; development builds fall back to `rg` from `PATH`.
- Tool output is capped by `maxToolOutput`.
- HTTP tools use bounded response sizes, timeouts, redirect limits, and clear user agent defaults.
- Plan mode disables write/edit/delete/run/network/delegate tools and MCP tools.
- API keys are stored in the OS user config directory without encryption.
- MCP servers are spawned as subprocesses from user-controlled config.

---

## Tests And Verification

There is no single full integration suite. Use focused checks:

- `go test ./...`
- `go build ./...`
- `cd frontend && npx vite build`
- `cd frontend && npm test` when frontend tests are relevant
- `wails dev` for manual UI verification
- `wails build` for release build validation

Provider tests currently cover:

- API format normalization
- OpenAI Chat stream edge cases
- OpenAI Chat `max_completion_tokens` fallback detection
- OpenAI Responses function-call round trips
- Anthropic Messages tool use/tool result conversion
- Anthropic JSON Schema field preservation

Frontend utility tests cover:

- config utilities
- bounded Diff behavior
- tool preview utilities

---

## Code Style Guidelines

- Go: standard `gofmt`, tabs, explicit `if err != nil` handling.
- Vue: Composition API with `<script setup>`.
- Components: PascalCase file/component names.
- CSS: one dark theme, semantic class names, no preprocessor.
- Events: lowercase with colon separators, e.g. `run:delta`, `tool:result`, `mcp:status`.
- JSON fields: camelCase for Go struct tags and user-facing tool parameters.
- Wails bindings: regenerate after adding/changing exported Go methods.
- Avoid broad refactors while changing tool contracts or provider adapters.

---

## Current Architectural Notes

- `model_provider.go` is the provider boundary; avoid leaking provider-specific request shapes into `app.go`.
- `app.go` is large by design today and owns most backend features; keep changes locally scoped unless deliberately splitting modules.
- `chatTools()` is the source of truth for built-in LLM-facing tools.
- `executeTool()` is the source of truth for built-in tool dispatch and JSON argument validation.
- The model-facing `background_process` tool supports only `start` and `stop` so agents can run frontend/backend dev processes without blocking. Do not expose service list/status polling actions; `StartService`, `StopService`, and `ListServices` remain available as Wails/backend APIs.
- Background-process state contains active processes only. Records are removed after `cmd.Wait()` completes, and the backend rejects starts beyond the 8-process active limit.
- The model-facing `wait` tool is for short, concrete asynchronous delays only. It is limited to 600 seconds, disabled in plan/grill modes, and displayed in the UI with a local countdown.
- Grill mode is session-local and request-scoped through `ChatRequest.grillMode`. Its instruction is injected as a transient system message, side-effectful and MCP tools are filtered and execution-guarded, and active goals must wait for the user's next answer instead of auto-continuing.
- The composer footer owns one three-option run-mode switch: YOLO is the default execution mode, PLAN is read-only analysis, and GRILL is the session-local read-only interview mode. Mode options use names only and do not have command-menu entries.
- The scheduled-task drawer is opened from `ComposerInfoBar`, displays full task state and latest output, and supports manual deletion/cancellation. Model-facing `scheduled_task.list` returns bounded metadata without stored output and must not be polled.
- `compactToolResultForModel()` is the source of truth for model-side tool-result reduction.
- `batch_read` is the model-facing local read tool; `read_file` may exist for backend compatibility but should not be exposed to the model.
- Skills are default-enabled metadata only; disabled skills persist through `disabledSkills`.
- Full skill Markdown is loaded only by explicit user slash command or enabled `Skill` tool call.
- Settings → Skills manages enable/disable state and does not inject full skill content.
- Settings → MCP manages raw MCP JSON and reconnects servers.
- Settings → Models owns provider presets and the current active provider/model.
- The model editor's connection test sends one isolated minimal request using the unsaved form values; it does not mutate or persist the active configuration.
- The context popover should keep system prompt parts separate, especially AGENTS.md/project instructions.
