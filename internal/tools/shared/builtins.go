// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package shared

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

const (
	// MaxWaitSeconds bounds the wait tool's pause. Exported because the runtime
	// clamp in app.go reads this same constant: the schema must never promise a
	// wider range than the tool accepts.
	MaxWaitSeconds = 3600
	// MaxReadRangeLines bounds the read tool's per-file line range.
	MaxReadRangeLines = 10000
	MaxReadLineChars  = 2000
	// MaxRenderHTMLCharacters bounds the HTML snippet by Unicode code points.
	MaxRenderHTMLCharacters = 50000
	// MaxRenderHTMLTitleChars bounds the render_html title, which the UI also
	// uses as the iframe title.
	MaxRenderHTMLTitleChars = 200
	// Suggest limits: the schema and the runtime check read these same constants,
	// so the promise "1-4 short texts" cannot drift from what is enforced.
	MaxSuggestItems     = 4
	MaxSuggestItemChars = 80
	// maxDelegateStepBudget bounds the subagent maxSteps parameter. Kept in
	// sync with the app-side hard cap (scheduler.MaxSteps).
	maxDelegateStepBudget = 1000
	// Service bounds. A graceful stop waits DefaultServiceStopGraceSeconds before
	// force killing the process tree, and one read returns
	// DefaultServiceReadTailBytes of recent output; both are capped here because
	// the service schema declares the same numbers, and the runtime clamps in
	// orch_services.go read them from this block.
	DefaultServiceStopGraceSeconds = 3
	MaxServiceStopGraceSeconds     = 30
	DefaultServiceReadTailBytes    = 8 * 1024
	MaxServiceReadTailBytes        = 32 * 1024
	// HTTP response-size bounds. internal/app's maxHTTPBodyBytes /
	// defaultHTTPMaxBody / defaultWebFetchBody and web_fetch's character bounds
	// read these same constants, so a declared range can never drift from what
	// the runtime enforces.
	MaxHTTPBodyBytes     = 50 * 1024 * 1024
	DefaultHTTPMaxBody   = 256 * 1024
	DefaultWebFetchBody  = 2 * 1024 * 1024
	DefaultWebFetchChars = 60000
	MaxWebFetchChars     = 200000
)

// chatToolsCache memoizes the built-in tool list. The schema is pure static
// text built from functionTool() + enforceStrictSchema(), all literals — no
// runtime state leaks in. cache is built once per process and shared across
// every chatTools() caller (runChat, ListTools, buildToolsForConfig,
// getContextBreakdown). Callers must not mutate the returned slice or any
// element's Parameters map — they are shared. The only place that today
// mutates Parameters is normalizeSchemaNode's recursion during construction,
// which runs once inside the cached build.
var chatToolsCache = struct {
	once  sync.Once
	tools []openai.Tool
}{}

func Builtins() []openai.Tool {
	chatToolsCache.once.Do(func() {
		chatToolsCache.tools = chatToolsUncached()
	})
	return chatToolsCache.tools
}

// builtinSchemaCache indexes the cached declarations by tool name for the
// argument gate in executeTool.
var builtinSchemaCache = struct {
	once   sync.Once
	byName map[string]map[string]any
}{}

// BuiltinSchema returns the argument schema declared for a built-in tool — the
// exact object the model was shown. ok is false for a name with no built-in
// declaration (MCP tools such as mcp__server__tool, and legacy aliases carried
// for old histories), which callers must read as "nothing to validate against"
// rather than as an argument error. The returned map is the shared cached one:
// callers must not mutate it.
func BuiltinSchema(name string) (map[string]any, bool) {
	builtinSchemaCache.once.Do(func() {
		byName := make(map[string]map[string]any)
		for _, tool := range Builtins() {
			if tool.Function == nil {
				continue
			}
			parameters, ok := tool.Function.Parameters.(map[string]any)
			if !ok {
				continue
			}
			byName[NormalizeName(tool.Function.Name)] = parameters
		}
		builtinSchemaCache.byName = byName
	})
	schema, ok := builtinSchemaCache.byName[name]
	return schema, ok
}

func chatToolsUncached() []openai.Tool {
	return []openai.Tool{
		functionTool("list_files", "List files and directories within a workspace path. Directories end with '/'. Hidden entries (dotfiles, gitignored paths, VCS internals) are never listed. Depth and entry count are bounded automatically; when the result is truncated, narrow the path instead of asking for more entries.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Workspace-relative directory path, or explicit absolute path for read-only listing. Empty means workspace root."},
			},
		}),
		functionTool("edit", "Validate and apply exact replacements to one workspace file per call.\n"+
			"- Edit one file per call: `path`, `version`, and `changes` sit at the top level. When editing multiple files, emit parallel edit calls in the same turn.\n"+
			"- Read the file first: `version` is the required current 6-character token from `read`.\n"+
			"- Prefer a small unique `oldText` per change; `replace_all` replaces all exact occurrences; `lineRange` (A-B form) replaces whole-line blocks.\n"+
			"- All changes in one call match against the same original snapshot in reverse line order.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Workspace-relative path of the single file to edit in this call."},
				"version": map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{6}$", "description": "Required 6-character current version from read or the preceding successful edit of this file. Comparison is case-insensitive."},
				"changes": map[string]any{
					"type":        "array",
					"minItems":    1,
					"maxItems":    50,
					"description": "All changes for this one file (max 50). Every change matches against the same original read snapshot; overlapping source regions fail the whole call.",
					"items":       editChangeSchema(),
				},
			},
			"required": []string{"path", "version", "changes"},
		}),
		functionTool("create", "Create a UTF-8 text file in the workspace. Parent directories are created automatically. An existing file is never replaced unless `overwrite` is true; without it an existing file fails with E_EXISTS, and an existing directory or symlink is rejected outright. A file that cannot be read as text is refused even with `overwrite` (E_TEXT_OVERWRITE).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Workspace-relative path of the file to create; parent directories are created automatically."},
				"content":   map[string]any{"type": "string", "description": "Full UTF-8 text of the new file (may be empty). It is written as-is, so add a trailing newline yourself when you want one."},
				"overwrite": map[string]any{"type": "boolean", "description": "Set true to replace an existing file; otherwise an existing file is rejected with E_EXISTS. A file that cannot be read as text is refused even with overwrite=true (E_TEXT_OVERWRITE)."},
			},
			"required": []string{"path", "content"},
		}),
		functionTool("delete", "Delete a file or directory in the workspace. Directories require recursive=true. Strictly prohibited from deleting drive roots, level 1 and level 2 system backbone directories (e.g. /etc, /var, /usr, /home/*, C:\\Windows, C:\\Users/*), VCS metadata (.git), or protected system targets (e.g. /dev, /proc, /sys, /etc/shadow, /var/local/libs).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Workspace-relative or authorized path to delete. Refuses filesystem root, level 1/2 directories, and sensitive system targets."},
				"recursive": map[string]any{"type": "boolean", "description": "Required when deleting a directory."},
			},
			"required": []string{"path"},
		}),
		functionTool("command", "Run a shell command with cwd confined to the workspace. On Windows the shell is Git Bash when available, otherwise PowerShell; on macOS/Linux, bash. Commands may inspect outside paths, redirect to null devices, and create new outside paths; modifying/deleting existing outside paths, explicit deletion commands, and unsafe cwd symlinks are refused. Unmanaged shell deletion commands (e.g. rm, find -delete, rsync --delete) are refused; use the delete tool for workspace files. Recognized managed deletion contexts (e.g. git rm, docker rm, kubectl delete) follow their own rules. On E_PATH_OUTSIDE, read the returned reason and switch target rather than retrying unchanged. When output exceeds the capture limit it is truncated and the result's `full` attribute points to the full output (readable via read), so never re-run a side-effecting command just to see more output; pipe through tail/head yourself when you only need part of a large output. When a command does not exit within its timeout it is NOT killed: it is promoted to a background service and keeps running (ports stay bound); the result carries the `timed-out` and `promoted-to-service` flags and its output names the new service id — continue with the service tool (read/stop) instead of re-running the command.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Shell command to run."},
				"cwd":     map[string]any{"type": "string", "description": "Working directory inside the workspace. Relative to the workspace root; an absolute path inside the workspace is also accepted and rebased. Empty means workspace root."},
				"timeout": map[string]any{"type": "integer", "minimum": 0, "maximum": 600, "description": "Timeout in seconds; omit or send 0 for the default (120), max 600."},
			},
			"required": []string{"command"},
		}),
		functionTool("service", "Run, inspect, and stop long-running local processes (dev servers, workers) without blocking the agent loop. action=start launches a process and returns its id; list shows tracked services including the most recent finished ones (status exited/stopped, with the exit code when non-zero and the error — read their final output to diagnose why a service died); read returns the tail of a service's output — the body that reaches you is capped at the last 8 KiB and `reduced-from` names the longer tail that was cut, while `tailBytes` sets how much of the rolling buffer that tail is drawn from — and works on finished services too (byte accounting rides on the opening tag: `returned`/`buffer`/`total`, plus `from-byte`); stop first tries graceful termination for a grace window (default 3s; `graceSeconds` raises it for services that must flush state on exit), then force kills the whole process tree; a force-kill writes its reason to the result's `error` attribute. Use list/read sparingly (no polling loops); prefer a single read after a concrete condition (e.g. wait + read). Error codes: E_BAD_COMMAND, E_SERVICE_LIMIT, E_BAD_BACKGROUND_ACTION, E_BAD_SERVICE_ID, E_SERVICE_NOT_FOUND.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":  map[string]any{"type": "string", "enum": []string{"start", "stop", "list", "read"}, "description": "Start a new background process, stop one by id, list all tracked services (running plus the most recent finished ones), or read a bounded tail of one service's output (works on finished services for post-mortem diagnosis)."},
				"name":    map[string]any{"type": "string", "description": "Optional label such as frontend or backend. Used only with action=start."},
				"command": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Long-running command. Required with action=start."},
				"cwd":     map[string]any{"type": "string", "description": "Workspace-relative working directory. Empty means workspace root. Used only with action=start."},
				"id":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Service id returned by action=start. Required with action=stop and action=read."},
				"graceSeconds": map[string]any{
					"type": "integer", "minimum": 0, "maximum": MaxServiceStopGraceSeconds,
					"description": fmt.Sprintf("Seconds to wait for a graceful stop before the process tree is force killed (default %d, max %d; omit or send 0 for the default). Used only with action=stop; raise it for services that must flush state on exit.", DefaultServiceStopGraceSeconds, MaxServiceStopGraceSeconds),
				},
				"tailBytes": map[string]any{
					"type": "integer", "minimum": 0, "maximum": MaxServiceReadTailBytes,
					"description": fmt.Sprintf("Bytes of the most recent output to return (default %d, max %d; omit or send 0 for the default, larger requests are clamped). Used only with action=read; the body that reaches you is still capped at the last 8 KiB, and a `reduced-from` longer than that tells you it was cut.", DefaultServiceReadTailBytes, MaxServiceReadTailBytes),
				},
			},
			"required": []string{"action"},
			"oneOf": []any{
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "start"}}, "required": []string{"command"}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "stop"}}, "required": []string{"id"}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "read"}}, "required": []string{"id"}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "list"}}},
			},
		}),
		functionTool("wait", "Pause the current agent run for a short, cancellable delay (1-3600 seconds) with a reason. This must be the only tool call in that model response: Ally rejects the whole batch (E_WAIT_BATCH_CONFLICT) when anything else rides along.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxWaitSeconds, "description": "Delay in whole seconds, from 1 to 3600."},
				"reason":  map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "pattern": ".*\\S.*", "description": "Short reason for the pause; the user reads it, so state plainly what you are waiting for."},
			},
			"required": []string{"seconds", "reason"},
		}),
		functionTool("ask", "Ask the user decision questions. Each question needs concise options with a label and a non-empty description; recommended is optional. Do not invent ids: every question and option is identified automatically. This must be the only tool call in that model response: Ally rejects the whole batch (E_ASK_BATCH_CONFLICT) when anything else rides along.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 5,
					"description": "One to five questions; all of them are shown in a single card and answered together.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": map[string]any{"type": "string", "minLength": 1, "maxLength": 500, "pattern": ".*\\S.*", "description": "Question text shown to the user, 1-500 characters."},
							"id":       map[string]any{"type": "string", "maxLength": 64, "description": "Optional. Omit it and Ally assigns one; only supply it when you need to reference this question yourself. At most 64 characters."},
							"options": map[string]any{
								"type": "array", "minItems": 2, "maxItems": 6,
								"description": "Two to six options the user can pick from.",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"label":       map[string]any{"type": "string", "minLength": 1, "maxLength": 120, "pattern": ".*\\S.*", "description": "Short label shown on the option, 1-120 characters."},
										"id":          map[string]any{"type": "string", "maxLength": 64, "description": "Optional. Omit it and Ally assigns one; only supply it when you need to reference this option yourself. At most 64 characters."},
										"description": map[string]any{"type": "string", "minLength": 1, "maxLength": 400, "pattern": ".*\\S.*", "description": "Required non-empty details for this option."},
										"recommended": map[string]any{"type": "boolean", "description": "Optional flag marking a recommended option."},
									},
									"required": []string{"label", "description"},
								},
							},
						},
						"required": []string{"question", "options"},
					},
				},
			},
			"required": []string{"questions"},
		}),
		functionTool("scheduled_task", "Create, list, or delete persistent scheduled tasks that survive Ally restarts. Recurring schedules resume from startup without catching up missed fires. The backend also accepts future RFC3339 one-shot times; past-due one-shots are reported as missed and never run late. For agent-created tasks, create only when the user explicitly requests recurring automation. Task content must be exactly one of instruction (an LLM agent runs with fresh context each fire) or command (a shell command runs in the task workspace through command safety checks, max 600s per run).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":      map[string]any{"type": "string", "enum": []string{"create", "list", "delete"}, "description": "Create, list, or delete a scheduled task."},
				"id":          map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Task id required for delete."},
				"name":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Short task name required for create."},
				"instruction": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Self-contained instruction executed by an LLM agent with fresh context on every run. Mutually exclusive with command."},
				"command":     map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Shell command executed in the task workspace on every run (same safety rules as the command tool; not for long-running services). Mutually exclusive with instruction."},
				"schedule":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Future RFC3339 one-shot time, Go duration such as 30m/2h (at least 1m), or a standard five-field cron expression. @-descriptors such as @daily are rejected: use a duration for intervals."},
			},
			"required": []string{"action"},
			"oneOf": []any{
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "create"}}, "required": []string{"name", "schedule"}, "oneOf": []any{
					map[string]any{"required": []string{"instruction"}},
					map[string]any{"required": []string{"command"}},
				}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "list"}}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "delete"}}, "required": []string{"id"}},
			},
		}),
		functionTool("http_request", "Make a single HTTP/HTTPS request with method, headers, query, body or JSON.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method":             map[string]any{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9!#$%&'*+.^_`|~-]*$", "description": "HTTP method token. Default GET; normalized to uppercase before sending."},
				"url":                map[string]any{"type": "string", "minLength": 1, "pattern": `^https?://\S+$`, "description": "Absolute http:// or https:// URL."},
				"headers":            map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Request headers. User-Agent defaults to AllyAgent unless provided."},
				"query":              map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Query parameters merged into the URL."},
				"body":               map[string]any{"type": "string", "description": "Raw request body. Mutually exclusive with json."},
				"json":               jsonValueSchema("JSON value to encode as the request body. Sets Content-Type to application/json unless provided."),
				"saveTo":             map[string]any{"type": "string", "description": "Optional workspace-relative download path for large responses; parent directories are created automatically."},
				"timeout":            map[string]any{"type": "integer", "minimum": 0, "maximum": 120, "description": "Request timeout in seconds; omit or send 0 for the default (60), max 120."},
				"maxBytes":           map[string]any{"type": "integer", "minimum": 0, "maximum": MaxHTTPBodyBytes, "description": fmt.Sprintf("Response body cap in bytes (default %d, max %d; omit or send 0 for the default, larger requests are clamped). saveTo raises the default to the max.", DefaultHTTPMaxBody, MaxHTTPBodyBytes)},
				"followRedirects":    map[string]any{"type": "boolean", "description": "Follow redirects (at most 5). Default true; set false to return the first response as-is."},
				"insecureSkipVerify": map[string]any{"type": "boolean", "description": "Skip TLS verification. Default false; only for debugging or trusted self-signed services."},
			},
			"required": []string{"url"},
			// Judged on the effective value, like the runtime (req.Body != ""): an
			// explicit empty body means "not provided", so padding the field must
			// not read as "body and json were both sent".
			"not": map[string]any{
				"required":   []string{"body", "json"},
				"properties": map[string]any{"body": map[string]any{"type": "string", "minLength": 1}},
			},
		}),
		functionTool("web_fetch", "Fetch a web page and return readable text, title, and links.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":                map[string]any{"type": "string", "minLength": 1, "pattern": `^https?://\S+$`, "description": "Absolute http:// or https:// URL."},
				"format":             map[string]any{"type": "string", "enum": []string{"readable", "raw", ""}, "description": "Output mode. readable (default): Readability-extracted main-content text. raw: bounded page source without extraction, not byte-exact; use when readable fails (E_WEB_FETCH_EXTRACT) or source markup matters. An empty string means the default, exactly as the runtime reads it."},
				"headers":            map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Request headers (e.g. Authorization, Cookie, Accept-Language). User-Agent defaults to AllyAgent unless provided."},
				"timeout":            map[string]any{"type": "integer", "minimum": 0, "maximum": 120, "description": "Request timeout in seconds; omit or send 0 for the default (60), max 120."},
				"maxBytes":           map[string]any{"type": "integer", "minimum": 0, "maximum": MaxHTTPBodyBytes, "description": fmt.Sprintf("Download cap in bytes (default %d, max %d; omit or send 0 for the default, larger requests are clamped).", DefaultWebFetchBody, MaxHTTPBodyBytes)},
				"maxChars":           map[string]any{"type": "integer", "minimum": 0, "maximum": MaxWebFetchChars, "description": fmt.Sprintf("Character cap for the returned text (default %d, max %d; omit or send 0 for the default). Text longer than this is truncated and the result is flagged truncated.", DefaultWebFetchChars, MaxWebFetchChars)},
				"insecureSkipVerify": map[string]any{"type": "boolean", "description": "Skip TLS verification. Default false; only for debugging or trusted self-signed services."},
			},
			"required": []string{"url"},
		}),
		functionTool("remote_read", "Read one or more text files on a remote SSH workspace (same contract as read: line-numbered preview + 6-char version for remote_edit; UTF-16 LE/BE transcoded; no document extraction). Omit startLine/endLine to read the whole file when needed, or specify startLine/endLine (or `tailLines` for the end of one) for targeted ranges in larger files. Pass needed files in the files array to read in parallel.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "minLength": 1, "pattern": `.*\S.*`, "description": "Explicit SSH target plus an absolute, non-root workspace path, e.g. my-dev:/srv/app. The workspace path / is rejected."},
				"files":  batchReadFilesSchema(),
			},
			"required": []string{"target", "files"},
		}),
		functionTool("remote_edit", "Validate and apply exact replacements to ONE file per call in a remote SSH workspace (same flat contract as edit; to change several files, send parallel remote_edit calls in one response).\n"+
			"- `target` selects the SSH target plus an absolute, non-root workspace path, e.g. my-dev:/srv/app; `/` is rejected, and `path` is relative to that root.\n"+
			"- Requires the current 6-character `version` from `remote_read`; `E_VERSION_MISMATCH` means re-read before editing.\n"+
			"- `changes` must be a JSON array (`[...]`), not a quoted JSON string (a quoted string is auto-repaired, but do not rely on it).\n"+
			"- Each change chooses exactly one source: a small exact unique `oldText` copied from `remote_read` (preferred), or an inclusive whole-line `lineRange` in A-B form for larger blocks.\n"+
			"- `replace_all` works only with `oldText`. `newText` is required.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":  map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus an absolute, non-root workspace path, e.g. my-dev:/srv/app. The workspace path / is rejected."},
				"path":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Relative path of the single file to edit in this call. Absolute paths under the workspace root are also accepted and rebased (the root itself is not)."},
				"version": map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{6}$", "description": "Required 6-char current version from remote_read."},
				"changes": remoteEditChangesSchema(),
			},
			"required": []string{"target", "path", "version", "changes"},
		}),
		functionTool("remote_create_file", "Create a UTF-8 text file in a remote SSH workspace; single-shot write. A path that is already a directory is rejected. An existing file needs `overwrite`, and that overwrite asks the user to approve it first; without `overwrite` the write fails with a plain \"file already exists\" message that carries no E_EXISTS code.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus an absolute, non-root workspace path, e.g. my-dev:/srv/app. The workspace path / is rejected."},
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Remote path of the file to create, relative to the SSH workspace root; absolute paths under that root are accepted and rebased."},
				"content":   map[string]any{"type": "string", "description": "Full UTF-8 text of the new file (may be empty), written as-is."},
				"overwrite": map[string]any{"type": "boolean", "description": "Set true to replace an existing file; the overwrite is approved by the user first."},
			},
			"required": []string{"target", "path", "content"},
		}),
		functionTool("remote_delete_path", "Delete a file or directory in a remote SSH workspace. Refuses the workspace root, its immediate child directories (root-level folders), VCS metadata, and OS-sensitive paths; other directories require recursive=true, while root-level files remain deletable. Strictly prohibited from deleting filesystem roots, level 1 and level 2 system backbone directories (e.g. /etc, /var, /usr, /home/*), or protected system targets (e.g. /dev, /proc, /sys, /etc/shadow, /var/local/libs). Use remote deletion cautiously; it is destructive. Prefer this over remote_run_command deletion.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus an absolute, non-root workspace path, e.g. my-dev:/srv/app. The workspace path / is rejected."},
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Remote path to delete. Refuses filesystem root, level 1/2 directories, and sensitive system targets."},
				"recursive": map[string]any{"type": "boolean", "description": "Required for deleting directories below the workspace root. Immediate child directories of the workspace root and system level 1/2 directories are always blocked."},
			},
			"required": []string{"target", "path"},
		}),
		functionTool("remote_run_command", "Run a non-interactive shell command on a remote SSH workspace. Unmanaged shell deletion commands (e.g. rm, find -delete, rsync --delete) are refused; use remote_delete_path to delete workspace files. Recognized managed deletion contexts (e.g. git rm, docker rm, kubectl delete) follow their own rules. Unlike the local command tool, a remote timeout kills the process group instead of promoting it to a background service, output is capped at 128 KiB with no full-output file, and a literal write target outside the workspace fails with E_PATH_OUTSIDE.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":  map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus an absolute, non-root workspace path, e.g. my-dev:/srv/app. The workspace path / is rejected."},
				"command": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Shell command to run on the remote host."},
				"shell":   map[string]any{"type": "string", "enum": []string{"/bin/bash", "/bin/sh", "/usr/bin/bash", "/usr/bin/sh", "/bin/zsh", "/usr/bin/zsh", ""}, "description": "Optional absolute path of the remote shell; an empty string means the helper chooses one itself. Omit to let the remote helper choose one (bash, then sh; cmd.exe on Windows)."},
				"cwd":     map[string]any{"type": "string", "description": "Working directory inside the remote workspace. Relative to the workspace root; an absolute path equal to or under the root is also accepted and rebased. Empty means workspace root."},
				"timeout": map[string]any{"type": "integer", "minimum": 0, "maximum": 600, "description": "Timeout in seconds; omit or send 0 for the default (120), max 600."},
			},
			"required": []string{"target", "command"},
		}),
		functionTool("ssh_cluster", "Inspect or register SSH cluster servers for the current workspace. action=list shows servers authorized for this workspace; action=add asks the user to approve registering a new node — or, when the alias is already registered, only to authorize that existing node for the workspace (stored nodes and credentials are never overwritten). Credentials (password / private key path) are configured by the user in the SSH cluster manager; this tool cannot store them. An alias that is not registered yet also needs host, username, description and reason in the same call — the schema can only require `alias`, because the already-registered path ignores the other four.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":      map[string]any{"type": "string", "enum": []string{"list", "add"}, "description": "list: Show servers authorized for the current workspace; add: Request to register a new server node, or to authorize an already-registered one."},
				"alias":       map[string]any{"type": "string", "minLength": 1, "pattern": `^[a-zA-Z0-9_\-\.]+$`, "description": "Short friendly identifier, e.g. dev-api or staging-db. Required for add. An alias that is already registered is never modified: only workspace authorization is requested and the fields below are ignored."},
				"host":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Server IP address or hostname. Required when registering a new alias."},
				"port":        map[string]any{"type": "integer", "minimum": 0, "maximum": 65535, "description": "SSH port; omit or send 0 for the default (22)."},
				"username":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "SSH login user. Required when registering a new alias."},
				"description": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Required when registering a new alias: human-readable description of server role, environment, or purpose (e.g. 'staging redis cluster node', 'build runner')."},
				"reason":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explanation to the user why this server is needed. Required when registering a new alias."},
			},
			"required": []string{"action"},
			"oneOf": []any{
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "list"}}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "add"}}, "required": []string{"alias"}},
			},
		}),
		functionTool("grep", "Search UTF-8 file contents with ripgrep. Returns a `<ally-grep>` tag block whose opening tag carries the explicit `mode` (`lines`/`count_matches`), the match/file totals (`hits` appears only when it differs from `matched`), `truncated`/`offset-exhausted` flags, and `next-offset` while more entries remain. `lines` mode (default) groups matches by file — one bare path row per file, then one indented `line: text` row per matching line (text preview trimmed, max 500 chars; no colon means the preview was dropped for budget) — so a separate read is only needed for surrounding context; `count_matches` returns one `path: count=N` row per file. Result size is bounded automatically; paginate with `offset` using `next-offset` (it resumes right after the last row shown) or narrow path/glob instead of asking for more entries.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":        map[string]any{"type": "string", "minLength": 1, "pattern": `.*\S.*`, "description": "Search regex pattern."},
				"outputMode":     map[string]any{"type": "string", "enum": []string{"lines", "count_matches", ""}, "description": "Output shape: lines (default, line numbers plus capped text previews) or count_matches. An empty string means the default, exactly as the runtime reads it."},
				"path":           map[string]any{"type": "string", "description": "Subdirectory or explicit absolute path. Empty means workspace root."},
				"glob":           map[string]any{"type": "string", "description": "Optional glob filter, e.g. *.go or frontend/**/*.vue."},
				"includeIgnored": map[string]any{"type": "boolean", "description": "Include files ignored by .gitignore/.ignore. Default false."},
				"caseSensitive":  map[string]any{"type": "boolean", "description": "Match case exactly. Default false (case-insensitive)."},
				"offset":         map[string]any{"type": "integer", "minimum": 0, "description": "Skip first N matching lines/files for pagination."},
			},
			"required": []string{"pattern"},
		}),
		functionTool("read", "Read one or more file contents. Supports text files and images (jpg, png, gif, webp; bmp is read as a text notice instead, not as image input). For text files, each readable file is returned as a `<ally-file path=… version=…>` block (plus `lines=A-B`/`total=N` when known) whose body carries 1-based line numbers; that version is what edit needs. A path that does not exist and a directory are dropped without a block (when nothing else remains, the whole result is `(no readable files returned)`); every other failure still returns a block carrying an `error` attribute. The `truncated`/`reused`/`image`/`error` attributes flag anything else. Omit startLine/endLine to read the whole file when needed, or specify startLine/endLine for a targeted range in larger files (or `tailLines` for the end of one, e.g. a log) to save context. Pass needed files in the files array to read in parallel.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files": batchReadFilesSchema(),
			},
			"required": []string{"files"},
		}),

		functionTool("calculate", "Evaluate a deterministic math expression (e.g. 144 + 2^3, sqrt(16), sin(pi/2)).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Math expression to evaluate."},
			},
			"required": []string{"expression"},
		}),
		functionTool("render_html", fmt.Sprintf("Render a self-contained HTML snippet inline in the chat UI. Use for interactive widgets, data explorers, styled mockups, animated SVG, or rich charts. Apache ECharts (global `echarts`) is pre-installed in the environment—do not fetch external scripts or styles. Rendered in a sandboxed dark-theme iframe. Max %d characters.", MaxRenderHTMLCharacters), map[string]any{
			"type": "object",
			"properties": map[string]any{
				"html": map[string]any{
					"type":        "string",
					"minLength":   1,
					"maxLength":   MaxRenderHTMLCharacters,
					"description": "Self-contained HTML snippet with inline CSS and JS. Global `echarts` is pre-loaded for rich data visualizations (e.g. line, bar, pie, scatter, radar, heatmap). Give chart containers explicit dimensions (e.g. style=\"width:100%;height:350px;\"). No external scripts or resources.",
				},
				"title": map[string]any{
					"type":        "string",
					"maxLength":   MaxRenderHTMLTitleChars,
					"description": "Optional short title for the rendered content.",
				},
			},
			"required": []string{"html"},
		}),
		functionTool("plan", "Manage the session task list. Sets or updates the whole todo list (pending, in_progress, done), or pass an empty array to clear. Omit todos to read the current plan. At most one todo may be in_progress; if the list you send contains none, the first pending entry is promoted to in_progress, and the result shows the list as stored.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title":  map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Short, actionable title for the todo."},
							"status": map[string]any{"type": "string", "enum": []any{"pending", "in_progress", "done"}, "description": "Current status of the todo. At most one todo may be in_progress at a time."},
						},
						"required": []string{"title", "status"},
					},
					"description": "The updated todo list. Omit to read current. Pass empty array to clear.",
				},
			},
		}),
		functionTool("subagent", "Delegate a task to a child agent with its own tool loop. The child uses built-in and MCP tools, but never the tools that belong to the parent session or to the visible user: no ask, no subagent, no plan, no skill and no scheduled_task; only its final summary is returned. You must set `maxSteps` (the child's tool-call-round budget) based on task difficulty.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":         map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "The task for the child agent. Be specific — include file paths and expected outcomes."},
				"role":         map[string]any{"type": "string", "minLength": 1, "maxLength": 80, "pattern": ".*\\S.*", "description": "What the sub-agent is, as a short role name such as \"researcher\", \"code reviewer\", or \"tester\", not a sentence about the task. It is injected into the sub-agent's system prompt."},
				"maxSteps":     map[string]any{"type": "integer", "minimum": 1, "maximum": maxDelegateStepBudget, "description": "Required tool-call-round budget, chosen by task difficulty: small lookups ~5-10, normal tasks ~15-30, large multi-file work ~40-80. The child is warned as it runs low and must output a report on the final round."},
				"description":  map[string]any{"type": "string", "description": "Short 3-5 word summary of what the delegated agent is working on."},
				"cleanContext": map[string]any{"type": "boolean", "description": "If true, skip workspace environment injection. Use for tasks that do not depend on project structure (e.g. write a standalone algorithm). Default false."},
				"model":        map[string]any{"type": "string", "description": "Optional model override. Default uses current model."},
			},
			"required": []string{"task", "role", "maxSteps"},
		}),
		functionTool("skill", "Invoke a registered skill from the current skill listing. Use when the user wants to call a skill, or when you need instructions for a specific task covered by a skill.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "The name of the skill to invoke, as listed (e.g. \"codebase-design\", \"diagnosing-bugs\"); matching is case-insensitive, and a disabled skill reports not found."},
				"args":  map[string]any{"type": "string", "description": "Optional argument string for the skill, written like a command line (e.g. `-m \"fix bug\"`, `123`). Omit it for skills that take no arguments."},
			},
			"required": []string{"skill"},
		}),
		functionTool("suggest", "Suggest 1-4 follow-up actions the user is most likely to take next. Order them by relevance, most recommended first. Each item is sent as-is as the user's next message, so phrase it as an instruction the user would send rather than a note to yourself. This must be the only tool call in that model response: Ally rejects the whole batch (E_SUGGEST_BATCH_CONFLICT) when anything else rides along.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": MaxSuggestItems,
					"items": map[string]any{
						"type":      "string",
						"minLength": 1,
						"maxLength": MaxSuggestItemChars,
						"pattern":   ".*\\S.*",
					},
					"description": "1-4 short texts, ordered by relevance (most recommended first). Each is sent as-is as the user's next message: write a complete, self-contained request the user could send verbatim.",
				},
			},
			"required": []string{"items"},
		}),
	}
}

var builtinToolExamples = map[string]string{
	"edit":               `{"path":"app.go","version":"9k3m7x","changes":[{"oldText":"const oldName = oldValue","newText":"const newName = newValue"}]}; lineRange: {"path":"app.go","version":"9k3m7x","changes":[{"lineRange":"40-72","newText":"replacement block"}]}`,
	"command":            `{"command":"go test ./...","cwd":".","timeout":120}`,
	"service":            `start: {"action":"start","name":"frontend","command":"npm run dev","cwd":"frontend"}; stop: {"action":"stop","id":"svc_...","graceSeconds":10}; list: {"action":"list"}; read: {"action":"read","id":"svc_...","tailBytes":16384}`,
	"ask":                `{"questions":[{"question":"Which database should we use?","options":[{"label":"SQLite","description":"Simple local storage.","recommended":true},{"label":"PostgreSQL","description":"Production database."}]}]}`,
	"remote_read":        `{"target":"my-dev:/srv/app","files":[{"path":"main.go"}]}`,
	"remote_edit":        `{"target":"my-dev:/srv/app","path":"main.go","version":"9k3m7x","changes":[{"oldText":"func old() {}","newText":"func new() {}"}]}`,
	"remote_run_command": `{"target":"my-dev:/srv/app","command":"go test ./..."}`,
	"ssh_cluster":        `list: {"action":"list"}; add: {"action":"add","alias":"dev-node","host":"192.168.1.10","username":"root","description":"Development worker node","reason":"Deploy worker service"}`,
	"grep":               `{"pattern":"TODO|FIXME","path":"frontend/src","glob":"*.vue"}`,
	"read":               `one file: {"files":[{"path":"app.go"}]}; multiple files: {"files":[{"path":"app.go"},{"path":"main.go"}]}; range: {"files":[{"path":"services.go","startLine":1,"endLine":200}]}; tail: {"files":[{"path":"server.log","tailLines":200}]}`,
	"render_html":        `{"html":"<div id=\"chart\" style=\"width:100%;height:350px;\"></div><script>const c=echarts.init(document.getElementById('chart'),'dark');c.setOption({title:{text:'Metrics'},xAxis:{data:['Mon','Tue','Wed','Thu','Fri']},yAxis:{},series:[{type:'bar',data:[12,34,56,78,90]}]});</script>"}`,
	"subagent":           `{"task":"Inspect the authentication module and report concrete security issues.","role":"code reviewer","maxSteps":20,"description":"Review authentication"}`,
	"plan":               `{"todos":[{"title":"Inspect code","status":"in_progress"},{"title":"Run tests","status":"pending"}]}; clear: {"todos":[]}`,
}

func functionTool(name, description string, parameters map[string]any) openai.Tool {
	if example := builtinToolExamples[name]; example != "" {
		description = strings.TrimSpace(description) + " Canonical JSON example(s): " + example
	}
	return RawFunction(name, description, enforceStrictSchema(parameters))
}

func RawFunction(name, description string, parameters map[string]any) openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		},
	}
}

func batchReadFilesSchema() map[string]any {
	return map[string]any{
		"type":     "array",
		"minItems": 1,
		"maxItems": 20,
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": `.*\S.*`, "description": "File path to read."},
				"startLine": map[string]any{"type": "integer", "minimum": 0, "description": "Optional 1-based first line to read; omit or send 0 to start at the beginning."},
				"endLine":   map[string]any{"type": "integer", "minimum": 0, "description": "Optional inclusive last line; omit or send 0 for the end of the file. A value below startLine is normalized into an ascending range instead of being rejected."},
				"tailLines": map[string]any{"type": "integer", "minimum": 0, "maximum": MaxReadRangeLines, "description": "Optional: read only the last N lines (e.g. the end of a log); omit or send 0 for a full read. Cannot be combined with startLine/endLine."},
			},
			"required": []string{"path"},
			// Mutual exclusion is judged on the effective value, exactly as the
			// runtime folds tailLines<=0 into the plain startLine path: an explicit
			// 0 means "not set" (the description says so), so padding it must not
			// read as "both forms were requested".
			"oneOf": []any{
				map[string]any{"not": map[string]any{
					"required":   []string{"tailLines"},
					"properties": map[string]any{"tailLines": map[string]any{"type": "integer", "minimum": 1}},
				}},
				map[string]any{"required": []string{"tailLines"}, "properties": map[string]any{
					"tailLines": map[string]any{"type": "integer", "minimum": 1},
					"startLine": map[string]any{"type": "integer", "maximum": 0},
					"endLine":   map[string]any{"type": "integer", "maximum": 0},
				}},
			},
		},
		"description": "Required array of file request objects for reading one or more files in parallel.",
	}
}

// editLineRangePattern is the whole-line "A-B" form shared by the local edit
// tool's changes[] and remote_edit's; tools/edit.ParseLineRange accepts the same
// shape.
const editLineRangePattern = `^[1-9][0-9]*-[1-9][0-9]*$`

// editLineRangeOptionalPattern is what the change object's own lineRange property
// declares: a real range, or a blank value. The runtime picks the source by
// effective value (strings.TrimSpace(change.LineRange) != "", tools/edit/apply.go),
// so a padded "lineRange": "" beside a real oldText means "not provided" and must
// not be refused — the same rule editSourceOneOf already applies. The strict
// editLineRangePattern stays on the oneOf branches, where "lineRange is the
// source" is decided: a blank lineRange with no oldText matches no branch and is
// still refused.
const editLineRangeOptionalPattern = `^(?:\s*|[1-9][0-9]*-[1-9][0-9]*)$`

// editSourceOneOf expresses "exactly one source per change" over the effective
// value of each source instead of the presence of its key: oldText is a source
// only at minLength 1 and lineRange only when it matches editLineRangePattern.
// Key presence would report a model that pads an unused source with an empty
// string as "both sources given", while the runtime treats that same empty
// string as "not provided" (tools/edit/apply.go).
func editSourceOneOf() []any {
	return []any{
		map[string]any{
			"required":   []string{"oldText"},
			"properties": map[string]any{"oldText": map[string]any{"minLength": 1}},
			"not": map[string]any{
				"required":   []string{"lineRange"},
				"properties": map[string]any{"lineRange": map[string]any{"pattern": editLineRangePattern}},
			},
		},
		map[string]any{
			"required":   []string{"lineRange"},
			"properties": map[string]any{"lineRange": map[string]any{"pattern": editLineRangePattern}},
			"not": map[string]any{
				"required":   []string{"oldText"},
				"properties": map[string]any{"oldText": map[string]any{"minLength": 1}},
			},
		},
	}
}

func editChangeSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"oldText": map[string]any{
				"type": "string",
				// No minLength here: a blank source is the runtime's "not provided"
				// (change.OldText != "", tools/edit/apply.go), so this property must not
				// refuse a padded empty string — editSourceOneOf decides which source the
				// change uses, and its branches carry the minLength 1 that makes a blank
				// oldText useless.
				"description": "Small exact unique source snippet copied exactly from the read result, without `N: ` prefixes; preferred over lineRange.",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Optional; defaults to false. With oldText, true replaces every non-overlapping exact occurrence in the original snapshot; with lineRange it is ignored.",
			},
			"lineRange": map[string]any{
				"type": "string", "pattern": editLineRangeOptionalPattern,
				"description": "Inclusive whole-line A-B range from read's displayed line numbers, for larger blocks; replaces exactly those lines — a closing brace inside the range must be included, one outside stays untouched. All ranges use the original read version, so never adjust for earlier changes.",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "Replacement text without line prefixes. Empty deletes the selected source.",
			},
		},
		"required": []string{"newText"},
		"oneOf":    editSourceOneOf(),
	}
}

// remoteEditChangesSchema / remoteEditChangeSchema 与本地 edit 的 change 结构
// 完全一致（键名、pattern、oneOf 与 DTO 解码对齐），仅描述精简：完整规则见
// 本地 edit 工具描述——两者每轮同场发送，远程描述只需指向它。
func remoteEditChangesSchema() map[string]any {
	return map[string]any{
		"type": "array", "minItems": 1, "maxItems": 50,
		"description": "One to fifty changes for this single file. Each change takes exactly one source (oldText or lineRange) plus newText — the same shape the local edit tool's changes[] uses.",
		"items":       remoteEditChangeSchema(),
	}
}

func remoteEditChangeSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"oldText":     map[string]any{"type": "string", "description": "Exact unique source snippet copied from remote_read; use this instead of lineRange for small edits."},
			"replace_all": map[string]any{"type": "boolean", "description": "With oldText, true replaces every non-overlapping exact occurrence in the original snapshot."},
			"lineRange":   map[string]any{"type": "string", "pattern": editLineRangeOptionalPattern, "description": "Inclusive whole-line A-B range from remote_read's displayed line numbers, e.g. \"40-72\"."},
			"newText":     map[string]any{"type": "string", "description": "Replacement text without line prefixes; empty deletes the selected source."},
		},
		"required": []string{"newText"},
		"oneOf":    editSourceOneOf(),
	}
}

func jsonValueSchema(description string) map[string]any {
	return map[string]any{
		"description": description,
		"anyOf": []any{
			map[string]any{"type": "object", "additionalProperties": true},
			map[string]any{"type": "array", "items": map[string]any{}},
			map[string]any{"type": "string"},
			map[string]any{"type": "number"},
			map[string]any{"type": "boolean"},
		},
	}
}

func enforceStrictSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	}
	normalizeSchemaNode(schema)
	return schema
}

func normalizeSchemaNode(node map[string]any) {
	if node == nil {
		return
	}
	if node["type"] == "object" {
		if _, ok := node["additionalProperties"]; !ok {
			node["additionalProperties"] = false
		}
		if _, ok := node["properties"]; !ok {
			node["properties"] = map[string]any{}
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for _, raw := range props {
			if child, ok := raw.(map[string]any); ok {
				normalizeSchemaNode(child)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		normalizeSchemaNode(items)
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := node[key].([]any); ok {
			for _, raw := range variants {
				if child, ok := raw.(map[string]any); ok {
					normalizeSchemaNode(child)
				}
			}
		}
	}
	if notSchema, ok := node["not"].(map[string]any); ok {
		normalizeSchemaNode(notSchema)
	}
	if additional, ok := node["additionalProperties"].(map[string]any); ok {
		normalizeSchemaNode(additional)
	}
}

// NormalizeName lower-cases the incoming tool name and trims surrounding
// whitespace. It is the single entry point for tool-name normalization in
// executeTool; there is no alias table behind it.
func NormalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ParseFrontmatterField reads a single `field: value` line from YAML
// frontmatter. It is the single shared implementation for Ally's minimal
// frontmatter needs (skill metadata and memory descriptions); full YAML
// parsing is intentionally avoided.
func ParseFrontmatterField(line, field string) string {
	prefix := field + ":"
	prefixAlt := field + " :"
	if strings.HasPrefix(line, prefix) || strings.HasPrefix(line, prefixAlt) {
		idx := strings.Index(line, ":")
		if idx < 0 {
			idx = strings.Index(line, ": ")
		}
		if idx < 0 {
			return ""
		}
		v := strings.TrimSpace(line[idx+1:])
		if strings.HasPrefix(v, `"`) {
			var decoded string
			if err := json.Unmarshal([]byte(v), &decoded); err == nil {
				return decoded
			}
		}
		v = strings.Trim(v, `"'`)
		return v
	}
	return ""
}
