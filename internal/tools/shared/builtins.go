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

	"ally-dev/internal/tools/screenshot"
)

const (
	// MaxWaitSeconds bounds the wait tool's pause. Exported because the runtime
	// clamp in app.go reads this same constant: the schema must never promise a
	// wider range than the tool accepts.
	MaxWaitSeconds = 3600
	// MaxToolStage bounds model-supplied stage numbers; gaps are allowed.
	MaxToolStage = 2147483647
	// MaxReadRangeLines bounds the read tool's per-file line range.
	MaxReadRangeLines = 10000
	MaxReadLineChars  = 2000
	// MaxRenderHTMLCharacters bounds the HTML snippet by Unicode code points.
	MaxRenderHTMLCharacters = 50000
	// maxCalculateExactInteger is the largest signed 64-bit integer. The calculate
	// engine's +, -, * are int64 arithmetic: exact up to this bound and silently
	// wrapping past it, so the tool description has to state the range.
	maxCalculateExactInteger = 1<<63 - 1
	// MaxRenderHTMLTitleChars bounds the render_html title, which the UI also
	// uses as the iframe title.
	MaxRenderHTMLTitleChars = 200
	// Suggest limits: the schema and the runtime check read these same constants,
	// so the promise "1-4 short texts" cannot drift from what is enforced.
	MaxSuggestItems     = 4
	MaxSuggestItemChars = 80
	// Plan bounds. Every plan call echoes the plan back into the conversation,
	// and a step title is what a later call names, so the number of steps and the
	// title length are both bounded here and checked against these same numbers
	// in the handler.
	MaxPlanSteps          = 50
	MaxPlanStepTitleChars = 200
	// MaxDelegateStepBudget bounds one delegated run: it is both the subagent
	// maxSteps maximum and the scheduled-task hard cap (scheduler.MaxSteps
	// aliases it). The literal lives here, in the leaf package every tool
	// package may import, so the declared maximum cannot drift from what the
	// runtimes enforce.
	MaxDelegateStepBudget = 1000
	// Legacy grace bounds are retained only for request compatibility. Stops now
	// immediately kill the owned tree. Previously a graceful stop waited before
	// force killing the process tree, and one read returns
	// DefaultServiceReadTailBytes of recent output; both are capped here because
	// the service schema declares the same numbers, and the runtime clamps in
	// orch_services.go read them from this block.
	DefaultServiceStopGraceSeconds = 3
	MaxServiceStopGraceSeconds     = 30
	DefaultServiceReadTailBytes    = 8 * 1024
	MaxServiceReadTailBytes        = 32 * 1024
	MaxServiceDescriptionChars     = 500
	// HTTP response-size bounds. internal/app's maxHTTPBodyBytes /
	// defaultHTTPMaxBody / defaultWebFetchBody and web_fetch's character bounds
	// read these same constants, so a declared range can never drift from what
	// the runtime enforces.
	MaxHTTPBodyBytes     = 50 * 1024 * 1024
	DefaultHTTPMaxBody   = 256 * 1024
	DefaultWebFetchBody  = 2 * 1024 * 1024
	DefaultWebFetchChars = 60000
	MaxWebFetchChars     = 200000
	// DeletePathListLimit bounds how many paths one delete call may carry. The
	// local `delete` tool and `remote_delete_path` both read it for their schema,
	// and the handler-side check reads the same number (the Wails API path never
	// passes through the gate), so a declared bound cannot drift from what the
	// runtime accepts.
	DeletePathListLimit = 20
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
		functionTool("list_files", "Inspect directory structure; use read directly for known files and grep to locate code by content. Lists up to 3 levels deep and 200 entries, with directories ending in '/'. Dotfiles, gitignored paths, VCS internals and heavy dependency/build directories are omitted. At most 50 direct files per directory are shown; '+N more files' marks omitted siblings. Narrow the path for depth/global truncation; narrowing to the same directory cannot reveal omitted siblings. An absent entry does not prove a path does not exist.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Workspace-relative directory path, or explicit absolute path for read-only listing. Empty means workspace root."},
			},
		}),
		// Description kept deliberately terse: the schema sits in every request
		// prefix, and the parts trimmed here (returned-field lists, platform
		// trivia, error-code enumeration, the "fail by title to discover
		// windows" meta-hint) were all either derivable from results or
		// restated by the runtime error text. The rules that change how the
		// model calls the tool — unique windowTitle match, mutually exclusive
		// targets, negative region coordinates, downscaling, the privacy
		// guard — are all still here.
		functionTool("screenshot", "Capture a screen/window image, or list visible windows with action=list. Default capture uses the primary display; region uses virtual-screen coordinates (negative values allowed). Prefer windowId from list; windowTitle must match exactly one window, otherwise the error includes candidates. windowId, windowTitle and region are mutually exclusive. Window capture excludes covering windows; display/region capture shows what is visible. focus=true restores and raises a window. Images are downscaled. Capture only when the user asks to see the screen or a UI element.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":      map[string]any{"type": "string", "enum": []string{"capture", "list"}, "description": "capture (default) or list (return visible windows instead)."},
				"windowId":    map[string]any{"type": "integer", "minimum": 1, "description": "Window id from action=list."},
				"windowTitle": map[string]any{"type": "string", "minLength": 1, "description": "Case-insensitive substring of one window title or process name; must match exactly one window."},
				"focus":       map[string]any{"type": "boolean", "description": "Restore and raise the target window before capturing (default false)."},
				"display":     map[string]any{"type": "integer", "minimum": 0, "description": "Monitor index (0 = primary). Capture only."},
				"title":       map[string]any{"type": "string", "description": "action=list: filter by window title or process name (case-insensitive)."},
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": screenshot.MaxListWindows, "description": fmt.Sprintf("action=list: row cap (default %d).", screenshot.MaxListWindows)},
				"region": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x":      map[string]any{"type": "integer", "description": "Left edge (virtual-screen px)."},
						"y":      map[string]any{"type": "integer", "description": "Top edge (virtual-screen px)."},
						"width":  map[string]any{"type": "integer", "minimum": 1, "maximum": screenshot.MaxRegionWidthPx, "description": "Width in pixels."},
						"height": map[string]any{"type": "integer", "minimum": 1, "maximum": screenshot.MaxRegionHeightPx, "description": "Height in pixels."},
					},
					"required": []string{"x", "y", "width", "height"},
				},
			},
		}),
		functionTool("edit", "Edit one workspace file per call; send parallel calls for multiple files. Read first and use its current version, or reuse the version from a successful edit. Each change uses oldText or lineRange, never both. Prefer small, unique oldText copied verbatim from read without line-number prefixes; replaceAll applies only to oldText. Use lineRange for large block replacements/deletions. All changes refer to the same original snapshot: never shift line numbers for earlier changes. Overlapping changes fail together; merge edits in the same region into one change whose newText is the final content. For insertions next to an edited line, include both in one change. Source/version/range errors may attach a complete read-format snapshot of a small text file: rebuild using its source and version; otherwise re-read the needed range before retrying.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Workspace-relative path of the single file to edit in this call."},
				"version": map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{6}$", "description": "Required 6-character current version from read or the preceding successful edit of this file. Comparison is case-insensitive."},
				// Same declaration as remote_edit; only the read tool it names differs.
				"changes": editChangesSchema("read"),
			},
			"required": []string{"path", "version", "changes"},
		}),
		functionTool("create", "Create a UTF-8 file and missing parent directories. Existing files require overwrite=true; directories, symlinks and existing non-text files are refused.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Workspace-relative path of the file to create; parent directories are created automatically."},
				"content":   map[string]any{"type": "string", "description": "Full UTF-8 text of the new file (may be empty). It is written as-is, so add a trailing newline yourself when you want one."},
				"overwrite": map[string]any{"type": "boolean", "description": "Allow replacing an existing text file; default false."},
			},
			"required": []string{"path", "content"},
		}),
		functionTool("delete", "Delete a file or directory in the workspace. Directories require recursive=true. Strictly prohibited from deleting drive roots, level 1 and level 2 system backbone directories (e.g. /etc, /var, /usr, /home/*, C:\\Windows, C:\\Users/*), VCS metadata (.git), or protected system targets (e.g. /dev, /proc, /sys, /etc/shadow, /var/local/libs)."+deleteBatchNote(), map[string]any{
			"type": "object",
			"properties": map[string]any{
				"paths":     deletePathsSchema("Workspace-relative or authorized path to delete."),
				"recursive": map[string]any{"type": "boolean", "description": "Required when deleting a directory; applies to every path in the call."},
			},
			"required": []string{"paths"},
		}),
		functionTool("command", "Run a shell command with cwd inside the workspace. Windows uses Git Bash when available, otherwise PowerShell; macOS/Linux use bash. Read-only outside inspection and new outside paths are allowed; modifying existing outside paths is refused. Use delete for unmanaged shell deletion; managed contexts such as git rm, docker rm and kubectl delete follow their own rules. Follow a rejection reason instead of retrying unchanged. Output is a bounded preview; use the full log path with read/grep when provided, rather than rerunning side effects. A timeout promotes the still-running process to a background service: use service read/stop with its returned id, never start a duplicate.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Shell command to run."},
				"cwd":     map[string]any{"type": "string", "description": "Working directory inside the workspace. Relative to the workspace root; an absolute path inside the workspace is also accepted and rebased. Empty means workspace root."},
				"timeout": commandTimeoutSchema(),
			},
			"required": []string{"command"},
		}),
		functionTool("service", "Manage long-running local processes in the current workspace without blocking. Sessions and sub-agents in the same workspace share services; list/read/stop cannot access other workspaces. The active service limit is global across workspaces. List reports activeCount for this workspace and globalActiveCount/maxActive for global capacity. A workspace is required for model service operations. start requires a description explaining the service purpose and returns an id; list defaults to starting/running services; includeFinished=true also lists recent finished services; read returns recent output, including from finished services; stop immediately force-kills the owned process tree and confirms exit. Model output keeps at most the last 8 KiB; reduced-from reports clipping. Avoid polling loops; read after a concrete condition or wait.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"includeFinished": map[string]any{"type": "boolean", "description": "For action=list: include finished services (default false). Output remains readable by id."},
				"action":          map[string]any{"type": "string", "enum": []string{"start", "stop", "list", "read"}, "description": "Start a new background process, stop one by id, list starting/running services (includeFinished=true adds recent finished ones), or read a bounded tail of one service's output (works on finished services for post-mortem diagnosis)."},
				"name":            map[string]any{"type": "string", "description": "Optional label such as frontend or backend. Used only with action=start."},
				"description":     map[string]any{"type": "string", "minLength": 1, "maxLength": MaxServiceDescriptionChars, "pattern": ".*\\S.*", "description": "Required with action=start: explain what the service does and why it is needed. Shown to the user in service cards and the task center."},
				"command":         map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Long-running command. Required with action=start."},
				"cwd":             map[string]any{"type": "string", "description": "Workspace-relative working directory. Empty means workspace root. Used only with action=start."},
				"id":              map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Service id returned by action=start. Required with action=stop and action=read."},
				"graceSeconds": map[string]any{
					"type": "integer", "minimum": 0, "maximum": MaxServiceStopGraceSeconds,
					"description": fmt.Sprintf("Deprecated compatibility field (legacy default %d, maximum %d); ignored. Stop always immediately force-kills the owned process tree.", DefaultServiceStopGraceSeconds, MaxServiceStopGraceSeconds),
				},
				"tailBytes": map[string]any{
					"type": "integer", "minimum": 0, "maximum": MaxServiceReadTailBytes,
					"description": fmt.Sprintf("Bytes of the most recent output to return (default %d, max %d; omit or send 0 for the default, larger requests are clamped). Used only with action=read; the body that reaches you is still capped at the last 8 KiB, and a `reduced-from` longer than that tells you it was cut.", DefaultServiceReadTailBytes, MaxServiceReadTailBytes),
				},
			},
			"required": []string{"action"},
		}),
		functionTool("wait", "Pause the current agent run for a short, cancellable delay (1-3600 seconds) with a reason. To wait after other operations, assign a later stage; wait has no implicit ordering and never ends the run. A delay does not verify readiness: check the expected condition afterward.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxWaitSeconds, "description": "Delay in whole seconds, from 1 to 3600."},
				"reason":  map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "pattern": ".*\\S.*", "description": "Short reason for the pause; the user reads it, so state plainly what you are waiting for."},
			},
			"required": []string{"seconds", "reason"},
		}),
		functionTool("ask", "Ask user decisions together in one card. Each question needs concise options with labels and non-empty descriptions; recommended is optional. Omit ids to have them assigned automatically. This must be the only tool call in the response.", map[string]any{
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
		functionTool("scheduled_task", "Create, list, or delete persistent scheduled tasks that survive Ally restarts. Recurring schedules resume from startup without catching up missed fires. The backend also accepts future RFC3339 one-shot times; past-due one-shots are reported as missed and never run late. For agent-created tasks, create only when the user explicitly requests scheduled or recurring automation. Task content must be exactly one of instruction (an LLM agent runs with fresh context each fire, on the model that was active when the task was created) or command (a shell command runs in the task workspace through command safety checks, max 600s per run).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"includeFinished": map[string]any{"type": "boolean", "description": "For action=list: include completed, missed, or invalid tasks with no next run (default false). Running tasks and tasks with a next run are always listed."},
				"action":          map[string]any{"type": "string", "enum": []string{"create", "list", "delete"}, "description": "Create, list active tasks, or delete a scheduled task. Use includeFinished=true to list all retained tasks."},
				"id":              map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Task id required for delete."},
				"name":            map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Short task name required for create."},
				"instruction":     map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Self-contained instruction executed by an LLM agent with fresh context on every run. Mutually exclusive with command; provide either instruction or command for action=create."},
				"command":         map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Shell command executed in the task workspace on every run (same safety rules as the command tool; not for long-running services). Mutually exclusive with instruction; provide either instruction or command for action=create."},
				"schedule":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Required for create: future RFC3339 one-shot time, Go duration such as 30m/2h (at least 1m), or a standard five-field cron expression. @-descriptors such as @daily are rejected: use a duration for intervals."},
			},
			"required": []string{"action"},
		}),
		functionTool("http_request", "Make a single HTTP/HTTPS request with method, headers, query, body or JSON.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method":             map[string]any{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9!#$%&'*+.^_`|~-]*$", "description": "HTTP method token. Default GET; normalized to uppercase before sending."},
				"url":                map[string]any{"type": "string", "minLength": 1, "pattern": `^https?://\S+$`, "description": "Absolute http:// or https:// URL."},
				"headers":            map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Request headers. User-Agent defaults to AllyAgent unless provided."},
				"query":              map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Query parameters merged into the URL."},
				"body":               map[string]any{"type": "string", "description": "Raw request body text. Mutually exclusive with json; provide either body or json, not both."},
				"json":               map[string]any{"type": "object", "additionalProperties": true, "description": "JSON object to encode as the request body. Mutually exclusive with body; provide either body or json, not both. Sets Content-Type to application/json unless provided."},
				"saveTo":             map[string]any{"type": "string", "description": "Optional workspace-relative download path for large responses; parent directories are created automatically."},
				"timeout":            httpTimeoutSchema(),
				"maxBytes":           map[string]any{"type": "integer", "minimum": 0, "maximum": MaxHTTPBodyBytes, "description": fmt.Sprintf("Response body cap in bytes (default %d, max %d; omit or send 0 for the default, larger requests are clamped). saveTo raises the default to the max.", DefaultHTTPMaxBody, MaxHTTPBodyBytes)},
				"followRedirects":    map[string]any{"type": "boolean", "description": "Follow redirects (at most 5). Default true; set false to return the first response as-is."},
				"insecureSkipVerify": map[string]any{"type": "boolean", "description": "Skip TLS verification. Default false; only for debugging or trusted self-signed services."},
			},
			"required": []string{"url"},
		}),
		functionTool("web_fetch", "Fetch a web page and return readable text, title, and links.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":                map[string]any{"type": "string", "minLength": 1, "pattern": `^https?://\S+$`, "description": "Absolute http:// or https:// URL."},
				"format":             map[string]any{"type": "string", "enum": []string{"readable", "raw", ""}, "description": "Output mode. readable (default): Readability-extracted main-content text. raw: bounded page source without extraction, not byte-exact; use when readable extraction fails or source markup matters. An empty string means the default, exactly as the runtime reads it."},
				"headers":            map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Request headers (e.g. Authorization, Cookie, Accept-Language). User-Agent defaults to AllyAgent unless provided."},
				"timeout":            httpTimeoutSchema(),
				"maxBytes":           map[string]any{"type": "integer", "minimum": 0, "maximum": MaxHTTPBodyBytes, "description": fmt.Sprintf("Download cap in bytes (default %d, max %d; omit or send 0 for the default, larger requests are clamped).", DefaultWebFetchBody, MaxHTTPBodyBytes)},
				"maxChars":           map[string]any{"type": "integer", "minimum": 0, "maximum": MaxWebFetchChars, "description": fmt.Sprintf("Character cap for the returned text (default %d, max %d; omit or send 0 for the default). Text longer than this is truncated and the result is flagged truncated.", DefaultWebFetchChars, MaxWebFetchChars)},
				"insecureSkipVerify": map[string]any{"type": "boolean", "description": "Skip TLS verification. Default false; only for debugging or trusted self-signed services."},
			},
			"required": []string{"url"},
		}),
		functionTool("remote_read", "Read one or more text files on a remote SSH workspace (same contract as read: bounded line-numbered preview + 6-char version for remote_edit; UTF-16 LE/BE transcoded; no document extraction). Prefer targeted startLine/endLine ranges or tailLines for logs; follow the startLine continuation hint when truncated. Pass needed files in the files array to read in parallel.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": sshTargetSchema(),
				"files":  batchReadFilesSchema(),
			},
			"required": []string{"target", "files"},
		}),
		functionTool("remote_edit", "Edit one file on a remote SSH workspace, using the same changes contract as edit. Send parallel calls for multiple files. target selects an SSH host and absolute non-root workspace (e.g. dev:/srv/app); path is relative to it. Use the current version from remote_read or a successful remote_edit. changes must be a JSON array, not a quoted string. Re-read if the snapshot changed.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":  sshTargetSchema(),
				"path":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Relative path of the single file to edit in this call. Absolute paths under the workspace root are also accepted and rebased (the root itself is not)."},
				"version": map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{6}$", "description": "Required 6-char current version from remote_read."},
				"changes": editChangesSchema("remote_read"),
			},
			"required": []string{"target", "path", "version", "changes"},
		}),
		functionTool("remote_create_file", "Create a UTF-8 file and missing parents on a remote SSH workspace. Directories are refused. Existing files require overwrite=true and user approval.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    sshTargetSchema(),
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Remote path of the file to create, relative to the SSH workspace root; absolute paths under that root are accepted and rebased."},
				"content":   map[string]any{"type": "string", "description": "Full UTF-8 text of the new file (may be empty), written as-is."},
				"overwrite": map[string]any{"type": "boolean", "description": "Set true to replace an existing file; the overwrite is approved by the user first."},
			},
			"required": []string{"target", "path", "content"},
		}),
		functionTool("remote_delete_path", "Delete one or more files or directories in a remote SSH workspace. Refuses the workspace root, VCS metadata, and the protected system locations listed below; directories require recursive=true. Strictly prohibited from deleting filesystem roots, level 1 and level 2 system backbone directories (e.g. /etc, /var, /usr, /home/*), or protected system targets (e.g. /dev, /proc, /sys, /etc/shadow, /var/local/libs). Use remote deletion cautiously; it is destructive. Prefer this over remote_run_command deletion."+deleteBatchNote(), map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    sshTargetSchema(),
				"paths":     deletePathsSchema("Remote path to delete, relative to the SSH workspace root; absolute paths under that root are accepted and rebased."),
				"recursive": map[string]any{"type": "boolean", "description": "Required for deleting directories; applies to every path in the call. System level 1/2 directories are always blocked."},
			},
			"required": []string{"target", "paths"},
		}),
		functionTool("remote_run_command", "Run a non-interactive shell command on a remote SSH workspace. Use remote_delete_path for unmanaged deletion; managed contexts such as git rm, docker rm and kubectl delete follow their own rules. Modifying existing paths outside the workspace is refused. A timeout kills the process group. Output keeps its real head and tail within 128 KiB, without a full-output file; narrow output when possible.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":  sshTargetSchema(),
				"command": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Shell command to run on the remote host."},
				"shell":   map[string]any{"type": "string", "enum": []string{"/bin/bash", "/bin/sh", "/usr/bin/bash", "/usr/bin/sh", "/bin/zsh", "/usr/bin/zsh", ""}, "description": "Optional absolute path of the remote shell; an empty string means the helper chooses one itself. Omit to let the remote helper choose one (bash, then sh; cmd.exe on Windows)."},
				"cwd":     map[string]any{"type": "string", "description": "Working directory inside the remote workspace. Relative to the workspace root; an absolute path equal to or under the root is also accepted and rebased. Empty means workspace root."},
				"timeout": commandTimeoutSchema(),
			},
			"required": []string{"target", "command"},
		}),
		functionTool("remote_transfer", "Transfer files or directories between the local workspace and a remote SSH workspace using raw binary streaming with bounded O(1) memory.\n"+
			"- action='upload': streams localPath (file or directory) to remotePath on the remote workspace.\n"+
			"- action='download': streams remotePath (file or directory) from the remote workspace to localPath.\n"+
			"- Preserves file modes, permissions, and directory structures.\n"+
			"- Both localPath and remotePath must stay within their respective workspaces (strict workspace fences enforced).\n"+
			"- Existing destinations require overwrite=true with user approval; version-control metadata (.git) is strictly protected.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":     sshTargetSchema(),
				"action":     map[string]any{"type": "string", "enum": []string{"upload", "download"}, "description": "Transfer direction: 'upload' (local to remote) or 'download' (remote to local)."},
				"localPath":  map[string]any{"type": "string", "minLength": 1, "description": "Workspace-relative local file or directory path."},
				"remotePath": map[string]any{"type": "string", "minLength": 1, "description": "Remote workspace-relative destination or source file/directory path."},
				"overwrite":  map[string]any{"type": "boolean", "description": "Allow overwriting an existing destination file or directory (subject to user confirmation)."},
			},
			"required": []string{"target", "action", "localPath", "remotePath"},
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
		}),
		functionTool("grep", "Locate code by content when its location is unknown; prefer this over shell grep/rg. Once the target code file is known, use read to understand its relevant functions or logic before editing; do not repeatedly grep that file to piece together its contents. Single-file searches suit specific match queries, log filtering or counting. Searches UTF-8 content; default lines mode groups matching lines by file with line numbers and text previews capped at 500 characters each. Totals are exact within the searched scope. Use the returned next-offset to paginate available entries, or narrow path/glob. Hidden files are skipped during directory traversal; dependency/build directories are excluded unless path points inside them. An empty path also skips files over 10 MB.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":        map[string]any{"type": "string", "minLength": 1, "pattern": `.*\S.*`, "description": "Rust regex pattern; look-around and backreferences are unsupported. Escape regex metacharacters when matching literal text."},
				"outputMode":     map[string]any{"type": "string", "enum": []string{"lines", "count_matches", ""}, "description": "lines (default, also for an empty string) returns matching lines with capped text previews. count_matches returns occurrence counts for only the top 100 files by count, 20 files per page; pagination cannot retrieve files beyond that top 100."},
				"path":           map[string]any{"type": "string", "description": "File or directory, relative to the workspace root or absolute. Empty means workspace root. For reading known code, use read instead."},
				"glob":           map[string]any{"type": "string", "description": "Optional glob filter, e.g. *.go or frontend/**/*.vue."},
				"includeIgnored": map[string]any{"type": "boolean", "description": "Include files ignored by .gitignore/.ignore. Default false. Does not enable hidden-directory traversal or remove dependency/build directory exclusions."},
				"caseSensitive":  map[string]any{"type": "boolean", "description": "Match case exactly. Default false (case-insensitive)."},
				"offset":         map[string]any{"type": "integer", "minimum": 0, "description": "Skip first N matching lines/files for pagination."},
			},
			"required": []string{"pattern"},
		}),
		functionTool("read", "Read known files directly to understand code or prepare edits. For small files, omit line ranges; for large files, read ranges covering complete relevant functions or logic rather than piecing them together through repeated grep calls or tiny reads. Batch independent files in one files array. Supports text and images (jpg, png, gif, webp; bmp returns a text notice). Text returns bounded `<ally-file path=… version=…>` previews with 1-based line numbers; edit needs that version. Use tailLines for log tails. Follow the startLine continuation hint only when more content is needed. Missing paths and directories are omitted; other failures carry an error attribute. reused means unchanged content is already in context. truncated-lines marks lines with missing tails: edit refuses lineRange over those lines, so use oldText instead.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files": batchReadFilesSchema(),
			},
			"required": []string{"files"},
		}),

		functionTool("calculate", fmt.Sprintf("Evaluate a deterministic math or logical expression using expr syntax (e.g. 144 + 2**3, sqrt(16), sin(pi/2), sum([1,2,3]), 10 > 5 ? 100 : 200); `^` means the same as `**`. There is no integer-division operator: `/` yields a fraction, and `//` starts a comment that silently drops the rest of the expression, so write floor(13 / 5) or 13 / 5. Integer +, -, * are exact up to ±%d and wrap silently past it; write an operand as a decimal (9999999999.0 * 9999999999.0) to compute in floating point instead.", maxCalculateExactInteger), map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Expression to evaluate (supports arithmetic, math functions, logic, ternary, and array aggregations)."},
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
		functionTool("plan", "Maintain the plan for the session: the short, ordered list of steps of a multi-step task, shown to the user as a progress panel. steps starts or replaces the plan (step titles in order; the first one starts immediately). finish reports where the work got to: a step title means that step and every step before it are done — the step after it becomes current, and naming the last step ends the plan with every step done. Call with neither of them to read the current plan back. Carry at most one of steps and finish per call — a call that gives both is refused, because the order of intent would be anyone's guess. Every result names the plan's state (empty, running or closed) and echoes the whole plan with its progress and the call that moves it forward, so there is nothing to remember between calls. A step title is how a report names that step, so keep the titles unique and tell the steps apart at a glance, and report a title the plan actually carries: an unknown one is refused instead of added. Statuses are never written by hand and exactly one step is current at a time. Start a plan when the work has several steps; skip the tool for single-step requests.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"steps": map[string]any{
					"type":     "array",
					"maxItems": MaxPlanSteps,
					"items":    map[string]any{"type": "string", "minLength": 1, "maxLength": MaxPlanStepTitleChars, "pattern": ".*\\S.*"},
					"description": fmt.Sprintf("The whole plan, in order, as step titles: the first step starts immediately and the rest wait their turn. "+
						"A title is how a later call names a step, so keep every title unique (titles that differ only by case or surrounding spaces count as the same) and within %d characters, and the whole plan within %d steps. "+
						"Send it to start a plan or to replace one — including after the plan drifted from the work; a replacement starts over, so it does not carry progress across. An empty array clears the plan.", MaxPlanStepTitleChars, MaxPlanSteps),
				},
				"finish": map[string]any{
					"type":      "string",
					"maxLength": MaxPlanStepTitleChars,
					"description": "Where the work got to: the title of the step the work reached. That step and every step before it are done, and the step after it becomes current — so naming the last step ends the plan with every step done. " +
						"The title is matched ignoring case and surrounding spaces; a title the plan does not carry is refused, and an empty value counts as not sending it.",
				},
			},
		}),
		functionTool("subagent", "Delegate a task to a child agent with its own tool loop. The child uses built-in and MCP tools, but never the tools that belong to the parent session or to the visible user: no ask, no subagent, no plan, no skill and no scheduled_task; only its final summary is returned. You must set `maxSteps` (the child's tool-call-round budget) based on task difficulty.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":         map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "The task for the child agent. Be specific — include file paths and expected outcomes."},
				"role":         map[string]any{"type": "string", "minLength": 1, "maxLength": 80, "pattern": ".*\\S.*", "description": "What the sub-agent is, as a short role name such as \"researcher\", \"code reviewer\", or \"tester\", not a sentence about the task. It is injected into the sub-agent's system prompt."},
				"maxSteps":     map[string]any{"type": "integer", "minimum": 1, "maximum": MaxDelegateStepBudget, "description": "Required tool-call-round budget, chosen by task difficulty: small lookups ~5-10, normal tasks ~15-30, large multi-file work ~40-80. The child is warned as it runs low and must output a report on the final round."},
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
		functionTool("suggest", "By default, end replies with 1-4 useful next actions; omit when none are useful. Order by relevance and prefix each item with its ordinal (1., 2., ...). Items become user messages verbatim, so write self-contained instructions the user could send. This must be the only tool call in the response and ends the run; finish your reply first.", map[string]any{
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
					"description": "1-4 short texts, ordered by relevance (most recommended first). Begin each one with its ordinal (\"1. …\", \"2. …\"), counting from 1 in array order so the number matches its position, then the instruction itself. Each is sent as-is as the user's next message: write a complete, self-contained request the user could send verbatim, ordinal prefix included in the length limit.",
				},
			},
			"required": []string{"items"},
		}),
	}
}

var builtinToolExamples = map[string]string{
	"edit":               `{"path":"app.go","version":"9k3m7x","changes":[{"oldText":"const oldName = oldValue","newText":"const newName = newValue"}]}; lineRange: {"path":"app.go","version":"9k3m7x","changes":[{"lineRange":"40-72","newText":"replacement block"}]}`,
	"command":            `{"command":"go test ./...","cwd":".","timeout":120}`,
	"service":            `start: {"action":"start","name":"frontend","description":"Run the frontend dev server for browser previews and hot reload","command":"npm run dev","cwd":"frontend"}; stop: {"action":"stop","id":"svc_..."}; list: {"action":"list"}; read: {"action":"read","id":"svc_...","tailBytes":16384}`,
	"ask":                `{"questions":[{"question":"Which database should we use?","options":[{"label":"SQLite","description":"Simple local storage.","recommended":true},{"label":"PostgreSQL","description":"Production database."}]}]}`,
	"remote_read":        `{"target":"my-dev:/srv/app","files":[{"path":"main.go"}]}`,
	"remote_edit":        `{"target":"my-dev:/srv/app","path":"main.go","version":"9k3m7x","changes":[{"oldText":"func old() {}","newText":"func new() {}"}]}`,
	"remote_run_command": `{"target":"my-dev:/srv/app","command":"go test ./..."}`,
	"remote_transfer":    `upload: {"target":"my-dev:/srv/app","action":"upload","localPath":"dist","remotePath":"public","overwrite":true}; download: {"target":"my-dev:/srv/app","action":"download","remotePath":"logs","localPath":"debug_logs"}`,
	"ssh_cluster":        `list: {"action":"list"}; add: {"action":"add","alias":"dev-node","host":"192.168.1.10","username":"root","description":"Development worker node","reason":"Deploy worker service"}`,
	"grep":               `{"pattern":"TODO|FIXME","path":"frontend/src","glob":"*.vue"}`,
	"read":               `one file: {"files":[{"path":"app.go"}]}; multiple files: {"files":[{"path":"app.go"},{"path":"main.go"}]}; range: {"files":[{"path":"services.go","startLine":1,"endLine":200}]}; tail: {"files":[{"path":"server.log","tailLines":200}]}`,
	"render_html":        `{"html":"<div id=\"chart\" style=\"width:100%;height:350px;\"></div><script>const c=echarts.init(document.getElementById('chart'),'dark');c.setOption({title:{text:'Metrics'},xAxis:{data:['Mon','Tue','Wed','Thu','Fri']},yAxis:{},series:[{type:'bar',data:[12,34,56,78,90]}]});</script>"}`,
	"screenshot":         `list: {"action":"list"}; a window by id: {"windowId":131076}; by unique title: {"windowTitle":"ally-agent"}; region: {"region":{"x":0,"y":0,"width":1280,"height":800}}`,
	"subagent":           `{"task":"Inspect the authentication module and report concrete security issues.","role":"code reviewer","maxSteps":20,"description":"Review authentication"}`,
	"plan":               `start: {"steps":["Inspect code","Run tests"]}; report progress: {"finish":"Inspect code"}; read: {}`,
}

func functionTool(name, description string, parameters map[string]any) openai.Tool {
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		properties = map[string]any{}
		parameters["properties"] = properties
	}
	properties["stage"] = map[string]any{
		"type": "integer", "minimum": 0, "maximum": MaxToolStage,
		"description": "Execution stage (default 0): equal stages run concurrently; stages ascend; failure blocks later stages.",
	}

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
				"endLine":   map[string]any{"type": "integer", "minimum": 0, "description": "Optional inclusive last line; omit or send 0 to request through the end of the file, subject to output limits. A value below startLine is normalized into an ascending range instead of being rejected."},
				"tailLines": map[string]any{"type": "integer", "minimum": 0, "maximum": MaxReadRangeLines, "description": "Optional: read only the last N lines (e.g. the end of a log); omit or send 0 to disable tail reading and use the normal line range. Do not combine a positive value with nonzero startLine or endLine. All reads remain subject to output limits."},
			},
			"required": []string{"path"},
		},
		"description": "Required array of file request objects for reading one or more files in parallel.",
	}
}

// editLineRangeOptionalPattern is what the change object's own lineRange property
// declares: a real range, or a blank value. The runtime picks the source by
// effective value (strings.TrimSpace(change.LineRange) != "", tools/edit/apply.go).
const editLineRangeOptionalPattern = `^(?:\s*|[1-9][0-9]*-[1-9][0-9]*)$`

// sshTargetSchema is the `target` argument every remote_* tool takes: the alias
// plus the absolute workspace path on that host. One declaration keeps the five
// remote tools from spelling the same pattern two different ways.
func sshTargetSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "pattern": `.*\S.*`, "description": "Explicit SSH target plus an absolute, non-root workspace path, e.g. my-dev:/srv/app. The workspace path / is rejected."}
}

// commandTimeoutSchema bounds the shell tools' timeout (command,
// remote_run_command).
func commandTimeoutSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "maximum": 600, "description": "Timeout in seconds; omit or send 0 for the default (120), max 600."}
}

// httpTimeoutSchema bounds the HTTP tools' request timeout (http_request,
// web_fetch).
func httpTimeoutSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "maximum": 120, "description": "Request timeout in seconds; omit or send 0 for the default (60), max 120."}
}

// deleteBatchNote is the batch note the delete declarations carry: the
// pre-validation promise, the per-path result and the absent case are one policy,
// so the wording is written once and appended by both declarations.
func deleteBatchNote() string {
	return fmt.Sprintf(" One call may delete several entries: `paths` takes at most %d. Every path is validated before anything is deleted (one refused path means nothing is deleted), and the result reports each path separately. Naming the same path twice is harmless, and listing a directory together with a path inside it is handled for you: each identity is deleted once, deepest first. A path that does not exist is not an error: it comes back as an `absent` slot with nothing deleted, so there is no need to retry it.", DeletePathListLimit)
}

// deletePathsSchema is the batch spelling both delete tools declare, built once
// for the two of them: bounds and item shape live here so the declarations
// cannot drift apart, and itemDescription is the only thing they say
// differently.
func deletePathsSchema(itemDescription string) map[string]any {
	return map[string]any{
		"type":        "array",
		"minItems":    1,
		"maxItems":    DeletePathListLimit,
		"items":       map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": itemDescription},
		"description": fmt.Sprintf("Paths to delete in one call, at most %d. One refused path refuses the whole call before anything is deleted.", DeletePathListLimit),
	}
}

// editChangesSchema builds the `changes` array both edit tools declare. sourceTool
// names the read tool the model copies source text from — `read` locally,
// `remote_read` over SSH — and is the only thing the two declarations say
// differently: bounds and shape live here once, so a local and a remote edit
// cannot drift apart.
func editChangesSchema(sourceTool string) map[string]any {
	return map[string]any{
		"type":        "array",
		"minItems":    1,
		"maxItems":    50,
		"description": fmt.Sprintf("All changes for this one file (max 50). Every change names exactly one source plus `newText` and matches against the same original `%s` snapshot; overlapping source regions fail the whole call.", sourceTool),
		"items":       editChangeSchema(sourceTool),
	}
}

func editChangeSchema(sourceTool string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"oldText": map[string]any{
				"type":        "string",
				"description": fmt.Sprintf("Small exact unique source snippet copied exactly from the `%s` result, without the displayed line number prefixes; preferred over `lineRange`. Mutually exclusive with lineRange; provide either oldText or lineRange.", sourceTool),
			},
			"replaceAll": map[string]any{
				"type":        "boolean",
				"description": "Optional; defaults to false. Only meaningful with `oldText`, where true replaces every non-overlapping exact occurrence in the original snapshot; do not use with `lineRange`.",
			},
			"lineRange": map[string]any{
				"type": "string", "pattern": editLineRangeOptionalPattern,
				"description": fmt.Sprintf("Inclusive whole-line A-B range from `%s`'s numbered output, for a large block replacement or deletion. Mutually exclusive with oldText; provide either oldText or lineRange. Align the range with the exact first and last lines you mean — a closing brace inside the range must be included, one outside stays untouched. All ranges use the original `%s` version, so never adjust for earlier changes.", sourceTool, sourceTool),
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "Replacement text without line prefixes. Empty deletes the selected source.",
			},
		},
		"required": []string{"newText"},
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

// protectedToolNames is the set of local file/command tools that must never
// leave the model's toolset: without read/edit/create/delete/command the
// agent cannot inspect or change the workspace at all, so Settings never
// offers them for disabling and the filter refuses them as a backstop.
var protectedToolNames = map[string]bool{
	"read":    true,
	"edit":    true,
	"create":  true,
	"delete":  true,
	"command": true,
}

// IsProtectedTool reports whether the named tool is one of the core local
// file/command tools that must stay enabled.
func IsProtectedTool(name string) bool {
	return protectedToolNames[NormalizeName(name)]
}

// IsBuiltinTool reports whether name is a built-in tool's canonical name.
// Legacy aliases (e.g. document_read) normalize like their canonical name,
// which is what BuiltinSchema keys on, so a disabled-tools entry never needs
// the alias spelling.
func IsBuiltinTool(name string) bool {
	_, ok := BuiltinSchema(name)
	return ok
}

// FilterTools drops the disabled non-protected builtins from a tool list.
// Disabled names are normalized (lower/trim); unknown names and protected
// tools are ignored — the config may carry stale entries after upgrades, and
// the core five must survive any config content.
func FilterTools(tools []openai.Tool, disabled []string) []openai.Tool {
	if len(disabled) == 0 || len(tools) == 0 {
		return tools
	}
	off := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		name = NormalizeName(name)
		if name != "" && !protectedToolNames[name] {
			off[name] = true
		}
	}
	if len(off) == 0 {
		return tools
	}
	filtered := make([]openai.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Function != nil && off[NormalizeName(tool.Function.Name)] {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

// SanitizeDisabledTools normalizes a disabled-tools list for persistence:
// trims, lower-cases, dedupes, and drops entries that are not toggleable
// builtins (protected tools and unknown names). Order is not meaningful.
func SanitizeDisabledTools(names []string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = NormalizeName(name)
		if name == "" || seen[name] || protectedToolNames[name] || !IsBuiltinTool(name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
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
