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
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

const (
	maxWaitSeconds    = 3600
	maxHTTPBodyBytes  = 50 * 1024 * 1024
	MaxReadRangeLines = 10000
	MaxReadLineChars  = 2000
	// maxDelegateStepBudget bounds the subagent maxSteps parameter. Kept in
	// sync with the app-side hard cap (scheduler.MaxSteps).
	maxDelegateStepBudget = 1000
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

func chatToolsUncached() []openai.Tool {
	return []openai.Tool{
		functionTool("list_files", "List files and directories. Workspace-relative paths are resolved under the workspace; explicit absolute paths are allowed for read-only inspection subject to safety checks. Returns {entries,count,truncated}.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           map[string]any{"type": "string", "description": "Workspace-relative directory path, or explicit absolute path for read-only listing. Empty means workspace root."},
				"maxDepth":       map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Maximum recursion depth. Default 3, max 50."},
				"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Maximum entries returned. Default 200, max 1000. Check truncated."},
				"includeHidden":  map[string]any{"type": "boolean", "description": "Include dotfiles and dot-directories; VCS internals like .git are always excluded. Default false."},
				"includeIgnored": map[string]any{"type": "boolean", "description": "Include gitignored paths and dependency directories such as node_modules, __pycache__; VCS internals like .git are always excluded. Default false."},
			},
		}),
		functionTool("edit", "Validate and apply exact replacements across multiple workspace files in one call.\n"+
			"- Read each affected file first; every file requires the current 6-character `version` from `read`. A stale version fails with `E_VERSION_MISMATCH`; re-read affected files and retry.\n"+
			"- The top-level `files` value must be a JSON array (`[...]`), never a quoted string.\n"+
			"- Each `files` item must be an object containing its own `path`, `version`, and `changes`; `path` and `version` never go at the top level, and missing required fields fail the whole call.\n"+
			"- Each file's `changes` value must be a JSON array (`[...]`), never a quoted string.\n"+
			"- Prefer a small exact unique `oldText` per change; `replace_all` replaces every non-overlapping exact occurrence; `lineRange` (A-B form) replaces larger whole-line blocks.\n"+
			"- If exact matching fails, Ally retries once after normalizing invisible differences such as trailing spaces and smart/Unicode quotes and dashes. An ambiguous match fails with `E_MULTI_MATCH`; add surrounding context.\n"+
			"- All changes in a file share the original read version, so no offset adjustment is needed between changes.\n"+
			"- After writing, `validation` contains a concise syntax/compile check. The file is already written if validation fails; fix the reported issue with another edit.\n"+
			"- Error codes include `E_BAD_EDIT`, `E_VERSION_MISMATCH`, and `E_PATH_OUTSIDE`.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files": map[string]any{
					"type":        "array",
					"minItems":    1,
					"maxItems":    20,
					"description": "Files to edit in this call (1-20):\n- `files` is an array of file objects, never a quoted string.\n- Each item carries its own `path`, `version`, and `changes`; `path` and `version` must not be hoisted to the top level.\n- Put all independent changes for the same file in one `changes` array when possible (max 50); repeated normalized paths with the same version merge; total changes across all files must not exceed 200.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
							"version": map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{6}$", "description": "Required 6-character current version from read or the preceding successful edit. Comparison is case-insensitive."},
							"changes": map[string]any{
								"type":     "array",
								"minItems": 1,
								"maxItems": 50,
								"items":    editChangeSchema(),
							},
						},
						"required": []string{"path", "version", "changes"},
					},
				},
			},
			"required": []string{"files"},
		}),
		functionTool("create", "Create a new UTF-8 text file inside the workspace (or an additional session-level extra root). Parent directories are created automatically. Does not overwrite unless overwrite is true. Refuses symlink targets and non-text overwrites. After writing, `validation` contains a concise automatic syntax/compile check; if it reports a failure, the file is already written and should be fixed with another edit. Error codes: E_PATH_OUTSIDE, E_EXISTS, E_TARGET_IS_DIRECTORY, E_SYMLINK_PATH, E_TEXT_OVERWRITE.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"content":   map[string]any{"type": "string"},
				"overwrite": map[string]any{"type": "boolean"},
			},
			"required": []string{"path", "content"},
		}),
		functionTool("delete", "Delete a file, symlink, or directory in the workspace (or an additional session-level extra root). Directories require recursive=true. Refuses any allowed root, VCS metadata (.git, .svn, .hg), and OS-sensitive paths. Symlink parents are resolved for workspace safety; deleting a final symlink removes the link itself, not its target. Returns path, kind, and removed item counts. Error codes: E_PATH_OUTSIDE, E_PATH_NOT_FOUND, E_DIR_REQUIRES_RECURSIVE, E_DELETE_BLOCKED.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"recursive": map[string]any{"type": "boolean"},
			},
			"required": []string{"path"},
		}),
		functionTool("command", "Run a shell command with cwd confined to the workspace. On Windows the shell is Git Bash when available, otherwise PowerShell; on macOS/Linux, bash. Commands may inspect outside paths, redirect to null devices, and create new outside paths; modifying/deleting existing outside paths, explicit deletion commands, unsafe cwd symlinks, and long-running services are refused. On E_PATH_OUTSIDE, read the returned reason and switch target rather than retrying unchanged. When output exceeds the capture limit it is truncated and `outputFilePath` points to the full output.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"cwd":            map[string]any{"type": "string", "description": "Relative working directory. Empty means workspace root."},
				"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 600, "description": "Default 120, max 600."},
			},
			"required": []string{"command"},
		}),
		functionTool("service", "Run, inspect, and stop long-running local processes (dev servers, workers) without blocking the agent loop. action=start launches a process and returns its id; list shows tracked services; read returns a bounded output tail (default 8 KiB, max 32 KiB); stop terminates one. Use list/read sparingly (no polling loops); prefer a single read after a concrete condition (e.g. wait + read). Error codes: E_BAD_COMMAND, E_SERVICE_LIMIT, E_BAD_BACKGROUND_ACTION, E_BAD_SERVICE_ID, E_SERVICE_NOT_FOUND.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":    map[string]any{"type": "string", "enum": []string{"start", "stop", "list", "read"}, "description": "Start a new background process, stop one by id, list all tracked services, or read a bounded tail of one service's output."},
				"name":      map[string]any{"type": "string", "description": "Optional label such as frontend or backend. Used only with action=start."},
				"command":   map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Long-running command. Required with action=start."},
				"cwd":       map[string]any{"type": "string", "description": "Workspace-relative working directory. Empty means workspace root. Used only with action=start."},
				"id":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Service id returned by action=start. Required with action=stop and action=read."},
				"tailBytes": map[string]any{"type": "integer", "minimum": 1, "maximum": 32768, "description": "Maximum bytes of output to return with action=read. Default 8192, max 32768. Ignored by other actions."},
			},
			"required": []string{"action"},
			"oneOf": []any{
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "start"}}, "required": []string{"command"}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "stop"}}, "required": []string{"id"}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "read"}}, "required": []string{"id"}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "list"}}},
			},
		}),
		functionTool("wait", "Pause the current agent run for a short, cancellable delay after an asynchronous operation has started or while a concrete external condition is expected to change. Call wait as the only tool in the model response, then verify the condition after it completes. Do not use it for user input or long schedules. Error codes: E_BAD_WAIT, E_WAIT_CANCELLED, E_WAIT_BATCH_CONFLICT.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": maxWaitSeconds, "description": "Delay in whole seconds, from 1 to 3600."},
				"reason":  map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "pattern": ".*\\S.*", "description": "Short user-visible reason for waiting."},
			},
			"required": []string{"seconds", "reason"},
		}),
		functionTool("ask", "Pause the current visible agent run and ask the user decision questions. Every question needs concise options with unique ids, labels, useful descriptions, and exactly one recommended option. The UI supports multiple selections and appends a custom-answer choice, so do not add an Other/Custom option. Call ask as the only tool in the model response. Error codes: E_BAD_ASK, E_ASK_CANCELLED, E_ASK_BATCH_CONFLICT.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 5,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":       map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "pattern": "^[A-Za-z0-9_-]+$"},
							"question": map[string]any{"type": "string", "minLength": 1, "maxLength": 500, "pattern": ".*\\S.*"},
							"options": map[string]any{
								"type": "array", "minItems": 2, "maxItems": 6,
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"id":          map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "pattern": "^[A-Za-z0-9_-]+$"},
										"label":       map[string]any{"type": "string", "minLength": 1, "maxLength": 120, "pattern": ".*\\S.*"},
										"description": map[string]any{"type": "string", "minLength": 1, "maxLength": 400, "pattern": ".*\\S.*"},
										"recommended": map[string]any{"type": "boolean"},
									},
									"required": []string{"id", "label", "description", "recommended"},
								},
							},
						},
						"required": []string{"id", "question", "options"},
					},
				},
			},
			"required": []string{"questions"},
		}),
		functionTool("scheduled_task", "Create, list, or delete temporary scheduled Agent tasks for the current Ally process. Create only when the user explicitly requests scheduled or recurring automation. Scheduled runs get the normal tool set (commands, file ops, network, MCP, delegation) except scheduled_task itself. Tasks use fresh isolated context each run and are cleared when Ally restarts. Use action=list only when asked or an id is needed; never poll. Results appear in the Task Center UI, not the current conversation.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":      map[string]any{"type": "string", "enum": []string{"create", "list", "delete"}, "description": "Create, list, or delete a scheduled task."},
				"id":          map[string]any{"type": "string", "minLength": 1, "description": "Task id required for delete."},
				"name":        map[string]any{"type": "string", "minLength": 1, "description": "Short task name required for create."},
				"instruction": map[string]any{"type": "string", "minLength": 1, "description": "Self-contained instruction executed with fresh context on every run."},
				"schedule":    map[string]any{"type": "string", "description": "RFC3339 time, Go duration such as 30m/2h, or standard five-field cron expression."},
			},
			"required": []string{"action"},
			"oneOf": []any{
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "create"}}, "required": []string{"name", "instruction", "schedule"}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "list"}}},
				map[string]any{"properties": map[string]any{"action": map[string]any{"const": "delete"}}, "required": []string{"id"}},
			},
		}),
		functionTool("http_request", "Make a single HTTP/HTTPS request with custom method, headers, query, body or JSON. Use for APIs, webhooks, internal services, and precise protocol debugging. Safe defaults include a bounded response size, timeout, redirect limit, per-host rate limit, and clear User-Agent. Private/local network access follows the app's allowPrivateNetwork setting, which is enabled by default.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method":             map[string]any{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9!#$%&'*+.^_`|~-]*$", "description": "HTTP method token. Default GET; normalized to uppercase before sending."},
				"url":                map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Absolute http:// or https:// URL."},
				"headers":            map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Request headers. User-Agent defaults to AllyAgent unless provided."},
				"query":              map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Query parameters merged into the URL."},
				"body":               map[string]any{"type": "string", "description": "Raw request body. Mutually exclusive with json."},
				"json":               jsonValueSchema("JSON value to encode as the request body. Sets Content-Type to application/json unless provided."),
				"saveTo":             map[string]any{"type": "string", "description": "Optional workspace-relative download path. Parent directories are created automatically."},
				"maxBytes":           map[string]any{"type": "integer", "minimum": 1, "maximum": maxHTTPBodyBytes, "description": "Maximum decoded response bytes. Default 262144; use saveTo for large downloads."},
				"timeoutSeconds":     map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "description": "Request timeout. Default 60 seconds."},
				"insecureSkipVerify": map[string]any{"type": "boolean", "description": "Skip TLS verification. Default false; only for debugging or trusted self-signed services."},
			},
			"required": []string{"url"},
			"not":      map[string]any{"required": []string{"body", "json"}},
		}),
		functionTool("web_fetch", "Fetch a web page and return readable text, title, and links. Use for ordinary page reading instead of curl. Pass format:\"raw\" to get a bounded decoded page source without extraction, e.g. when readable mode fails or you need the original markup. Safe defaults include a bounded size, timeout, redirect limit, per-host rate limit, and clear User-Agent. Private/local network access follows the app's allowPrivateNetwork setting, which is enabled by default.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":                map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Absolute http:// or https:// URL."},
				"format":             map[string]any{"type": "string", "enum": []string{"readable", "raw"}, "description": "Output mode. readable (default): Readability-extracted main-content text. raw: bounded page source without extraction (still capped by maxBytes/maxChars), not byte-exact; use when readable fails (E_WEB_FETCH_EXTRACT) or source markup matters."},
				"maxBytes":           map[string]any{"type": "integer", "minimum": 1, "maximum": maxHTTPBodyBytes, "description": "Maximum decoded source bytes read before text extraction. Default 2097152."},
				"maxChars":           map[string]any{"type": "integer", "minimum": 1, "maximum": 200000, "description": "Maximum readable text characters. Default 60000, max 200000."},
				"timeoutSeconds":     map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "description": "Request timeout. Default 60 seconds."},
				"insecureSkipVerify": map[string]any{"type": "boolean", "description": "Skip TLS verification. Default false; only for debugging or trusted self-signed services."},
			},
			"required": []string{"url"},
		}),
		functionTool("remote_read_file", "Read a text file on a remote SSH workspace (same contract as read: line-numbered preview + 6-char version for remote_edit; UTF-16 LE/BE transcoded; no document extraction). Omit startLine/endLine for the whole file; positive startLine without endLine reads to EOF; only endLine reads lines 1..endLine; negative startLine reads the last N lines (max 10000), not with endLine.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus workspace root, e.g. my-dev:/srv/app."},
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Relative file path inside the remote workspace."},
				"startLine": map[string]any{"type": "integer", "minimum": -MaxReadRangeLines, "description": "Optional 1-based start line; negative reads last N lines (max 10000)."},
				"endLine":   map[string]any{"type": "integer", "minimum": 1, "description": "Optional inclusive end line; omit when startLine is negative."},
			},
			"required": []string{"target", "path"},
		}),
		functionTool("remote_edit", "Validate and apply replacements across multiple files on one remote SSH target.\n"+
			"- Each file requires the current 6-character version from `remote_read_file`; `E_VERSION_MISMATCH` means re-read before editing.\n"+
			"- The `files` value must be a JSON array of file objects, never a quoted string.\n"+
			"- Each `files` item carries its own `path`, `version`, and `changes`; each item's `changes` value must be a JSON array (`[...]`), never a quoted string.\n"+
			"- Each change chooses exactly one source: a small exact unique `oldText` copied from `remote_read_file` (preferred), or an inclusive whole-line `lineRange` in A-B form for larger blocks.\n"+
			"- `replace_all` works only with `oldText`.\n"+
			"- `newText` is required.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus workspace root, e.g. my-dev:/srv/app."},
				"files":  remoteEditFilesSchema(),
			},
			"required": []string{"target", "files"},
		}),
		functionTool("remote_create_file", "Create or overwrite a UTF-8 text file in a remote SSH workspace (same contract as create); single-shot write.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"content":   map[string]any{"type": "string"},
				"overwrite": map[string]any{"type": "boolean"},
			},
			"required": []string{"target", "path", "content"},
		}),
		functionTool("remote_delete_path", "Delete a file or directory in a remote SSH workspace (same contract as delete; refuses root, .git, OS-sensitive paths). Prefer this over remote_run_command deletion.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"recursive": map[string]any{"type": "boolean"},
			},
			"required": []string{"target", "path"},
		}),
		functionTool("remote_run_command", "Run a non-interactive shell command on a remote SSH workspace (same contract as command; explicit deletion commands are refused — use remote_delete_path). Cwd defaults to the workspace root. Use find, ls, or other shell commands for remote directory discovery. Search remote code with grep -rn 'pattern' src/. Error codes: E_PATH_OUTSIDE, E_CWD_INVALID, E_LONG_RUNNING_COMMAND.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":         map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus workspace root, e.g. my-dev:/srv/app."},
				"command":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"cwd":            map[string]any{"type": "string", "description": "Relative working directory inside the remote workspace. Empty means workspace root."},
				"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 600, "description": "Default 120, max 600."},
				"shell":          map[string]any{"type": "string", "description": "Remote shell executable. Default /bin/bash if available, otherwise /bin/sh."},
			},
			"required": []string{"target", "command"},
		}),
		functionTool("grep", "Search UTF-8 file contents with ripgrep: returns exact match counts plus sample match lines grouped by file, sorted by relevance. Leaving `path` empty searches the whole workspace in one call with exact stats — do not repeat the search directory by directory. Workspace-wide searches report their skip policy in `skipped` (ignored files, heavy generated directories, files over 10 MB); an explicit `path` search intentionally bypasses those broad exclusions. Case-insensitive by default; use `caseSensitive: true` or prefix `(?-i)` for exact-case. `glob`: no slash matches the basename (`*.go`), with slash matches a relative path (`frontend/src/*.vue`). `contextBefore`/`contextAfter` (0-50) add surrounding context lines; context never counts toward stats or offsets. On sample truncation, page with the returned `nextOffset` as `offset`; all stats stay exact; `offsetExhausted: true` means the offset skipped past the end, so reset to 0. Re-read a file before using a sampled line as edit source, since long lines may be truncated.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "ripgrep regex pattern to search for."},
				"path":           map[string]any{"type": "string", "description": "Optional subdirectory, or explicit absolute path for read-only search. Empty means workspace root."},
				"glob":           map[string]any{"type": "string", "description": "Optional glob filter. No slash = basename (e.g. *.go); slash = relative path."},
				"maxFiles":       map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Maximum matching files in samples. Default 50. Exact stats still scan all matches."},
				"maxMatches":     map[string]any{"type": "integer", "minimum": 1, "maximum": 5000, "description": "Maximum sample lines. Default maxFiles*10, max 5000. Exact stats still scan all matches."},
				"maxDepth":       map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Maximum directory depth. Default 20, max 100."},
				"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "description": "Overall grep timeout across stats and samples. Default 30, max 120."},
				"includeIgnored": map[string]any{"type": "boolean", "description": "Include files ignored by .gitignore/.ignore. Default false."},
				"caseSensitive":  map[string]any{"type": "boolean", "description": "Match case exactly. Default false (case-insensitive)."},
				"offset":         map[string]any{"type": "integer", "minimum": 0, "description": "Skip the first N matching lines before collecting samples. Pass the previous nextOffset to page. A result with offsetExhausted: true means the offset skipped past the end, so reset to 0. Default 0."},
				"contextBefore":  map[string]any{"type": "integer", "minimum": 0, "maximum": 50, "description": "N context lines before each match (rg -B), marked `context: true`, never counted in stats or offsets. Default 0."},
				"contextAfter":   map[string]any{"type": "integer", "minimum": 0, "maximum": 50, "description": "N context lines after each match (rg -A), marked `context: true`, never counted in stats or offsets. Default 0."},
			},
			"required": []string{"pattern"},
		}),
		functionTool("read", "Read 1-20 files via a top-level `files` array (even for one file). Never use a top-level path/paths field or a string array; every item is an object with `path`. Missing paths and directories are silently omitted; other per-file failures stay visible. UTF-8 text is prefixed with display-only 1-based `N: ` line numbers and a 6-char `version` for edit — do not copy the prefixes into edit text. Omit startLine/endLine for the whole file; positive values give an inclusive range; a negative startLine reads the last N lines (max 10000) and must not combine with endLine. Large files auto-truncate with a `[Showing lines A-B of N. Use startLine=C to continue.]` marker — follow it to page. Only plain text files are supported: office/PDF documents (.docx/.pptx/.xlsx/.pdf, etc.) return E_DOCUMENT_UNSUPPORTED — convert them to Markdown with the anydoc skill first (e.g. npx -y @firecrawl/anydoc file.docx -o file.md), then read the converted .md. Images (.png/.jpg/.gif/.webp) are injected as visual input.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files": batchReadFilesSchema(),
			},
			"required": []string{"files"},
		}),

		functionTool("calculate", "Evaluate a deterministic math expression without shelling out. Supports + - * / % ^, parentheses, constants pi/e, and functions sqrt, abs, sin, cos, tan, asin, acos, atan, log, ln, exp, floor, ceil, round, min, max.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Math expression to evaluate."},
			},
			"required": []string{"expression"},
		}),
		functionTool("render_html", "Render a self-contained HTML snippet inline in the chat UI. Use ONLY for interactive widgets or custom visualizations Mermaid and Markdown cannot express (calculators, data explorers, styled mockups, animated SVG). Do NOT use for diagrams, flowcharts, pie charts, or tables (use Mermaid and Markdown tables). Rendered in a sandboxed dark-theme iframe. Max 50,000 characters. No external resources.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"html": map[string]any{
					"type":        "string",
					"minLength":   1,
					"maxLength":   50000,
					"description": "Self-contained HTML snippet with inline CSS. No external scripts or resources. Use inline <style> tags for styling. SVG is supported for diagrams.",
				},
				"title": map[string]any{
					"type":        "string",
					"maxLength":   200,
					"description": "Optional short title for the rendered content.",
				},
			},
			"required": []string{"html"},
		}),
		functionTool("plan", "Create or update a visible task list only when longer work genuinely benefits from progress tracking. Do not use it for trivial tasks or merely to demonstrate activity. State machine discipline: keep at most one item `in_progress` at a time; mark the current item `done` before advancing the next; do not jump a `pending` item straight to `done`.", map[string]any{
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
		functionTool("subagent", "Delegate a task to a child agent with its own tool loop. The child uses built-in and MCP tools but cannot ask the user or nest sub-agents; only its final summary is returned. You must set `maxSteps` (the child's tool-call-round budget) based on task difficulty.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":         map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "The task for the child agent. Be specific — include file paths and expected outcomes."},
				"role":         map[string]any{"type": "string", "minLength": 1, "maxLength": 80, "pattern": ".*\\S.*", "description": "The role of the sub-agent, e.g. \"researcher\", \"code reviewer\", \"tester\". Shown as the card label in the UI and injected into the sub-agent's system prompt."},
				"maxSteps":     map[string]any{"type": "integer", "minimum": 1, "maximum": maxDelegateStepBudget, "description": "Required tool-call-round budget, chosen by task difficulty: small lookups ~5-10, normal tasks ~15-30, large multi-file work ~40-80. The child is warned as it runs low and must output a report on the final round."},
				"description":  map[string]any{"type": "string", "description": "Short 3-5 word description for UI display."},
				"cleanContext": map[string]any{"type": "boolean", "description": "If true, skip workspace environment injection. Use for tasks that do not depend on project structure (e.g. write a standalone algorithm). Default false."},
				"model":        map[string]any{"type": "string", "description": "Optional model override. Default uses current model."},
			},
			"required": []string{"task", "role", "maxSteps"},
		}),
		functionTool("skill", "Invoke a registered skill from the current skill listing. Use when the user wants to call a skill, or when you need instructions for a specific task covered by a skill.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "The exact name of the skill to invoke, spelled as it appears in the current skill listing (e.g. \"codebase-design\", \"diagnosing-bugs\")."},
				"args":  map[string]any{"type": "string", "description": "Optional argument string for the skill, written like a command line (e.g. `-m \"fix bug\"`, `123`). Omit it for skills that take no arguments."},
			},
			"required": []string{"skill"},
		}),
		functionTool("suggest", "Suggest 1-4 follow-up actions as clickable chips below your last reply, ordered by relevance from most to least recommended. Each label is sent as-is as the user's next message when clicked. Call this only when the user might genuinely benefit from a concrete next step; if no useful follow-up exists, do not call it. Must be the only tool call in its batch, and a successful call ends the turn — emit it only after your reply content is complete. Error codes: E_BAD_SUGGEST, E_SUGGEST_BATCH_CONFLICT.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": 4,
					"items": map[string]any{
						"type":      "string",
						"minLength": 1,
						"maxLength": 80,
						"pattern":   ".*\\S.*",
					},
					"description": "1-4 short chip texts, ordered by relevance (most recommended first). Each is sent as-is as the user's next message when clicked.",
				},
			},
			"required": []string{"items"},
		}),
	}
}

var builtinToolExamples = map[string]string{
	"list_files":         `{"path":"frontend/src","maxDepth":2,"limit":200}`,
	"edit":               `{"files":[{"path":"app.go","version":"9k3m7x","changes":[{"oldText":"const oldName = oldValue","newText":"const newName = newValue"}]}]}; lineRange: {"files":[{"path":"app.go","version":"9k3m7x","changes":[{"lineRange":"40-72","newText":"replacement block"}]}]}`,
	"create":             `{"path":"notes/example.md","content":"# Example\n","overwrite":false}`,
	"delete":             `{"path":"tmp/generated","recursive":true}`,
	"command":            `{"command":"go test ./...","cwd":".","timeoutSeconds":120}`,
	"service":            `start: {"action":"start","name":"frontend","command":"npm run dev","cwd":"frontend"}; stop: {"action":"stop","id":"svc_..."}; list: {"action":"list"}; read: {"action":"read","id":"svc_...","tailBytes":8192}`,
	"wait":               `{"seconds":5,"reason":"Wait for the development server to become ready"}`,
	"ask":                `{"questions":[{"id":"database","question":"Which database should we use?","options":[{"id":"sqlite","label":"SQLite","description":"Simple local storage.","recommended":true},{"id":"postgres","label":"PostgreSQL","description":"Production database.","recommended":false}]}]}`,
	"scheduled_task":     `create: {"action":"create","name":"daily check","instruction":"Run tests and summarize failures.","schedule":"0 9 * * *"}; list: {"action":"list"}; delete: {"action":"delete","id":"task_..."}`,
	"http_request":       `{"url":"https://api.example.com/items","method":"GET","query":{"limit":"10"},"timeoutSeconds":60}`,
	"web_fetch":          `{"url":"https://example.com/docs","maxChars":60000}`,
	"remote_read_file":   `{"target":"my-dev:/srv/app","path":"main.go"}`,
	"remote_edit":        `{"target":"my-dev:/srv/app","files":[{"path":"main.go","version":"9k3m7x","changes":[{"oldText":"func old() {}","newText":"func new() {}"}]}]}`,
	"remote_create_file": `{"target":"my-dev:/srv/app","path":"notes.txt","content":"hello"}`,
	"remote_delete_path": `{"target":"my-dev:/srv/app","path":"tmp/output","recursive":true}`,
	"remote_run_command": `{"target":"my-dev:/srv/app","command":"go test ./..."}`,
	"grep":               `{"pattern":"TODO|FIXME","path":"frontend/src","glob":"*.vue","maxMatches":100}`,
	"read":               `one file: {"files":[{"path":"app.go"}]}; range: {"files":[{"path":"services.go","startLine":1,"endLine":200}]}; tail: {"files":[{"path":"server.log","startLine":-200}]}`,
	"calculate":          `{"expression":"sqrt(144) + 2^3"}`,
	"render_html":        `{"title":"Interactive counter","html":"<button id='counter'>0</button><script>const button=document.getElementById('counter');button.onclick=()=>button.textContent=String(Number(button.textContent)+1)</script>"}`,
	"plan":               `update: {"todos":[{"title":"Inspect implementation","status":"in_progress"},{"title":"Run tests","status":"pending"}]}; read current: {}`,
	"subagent":           `{"task":"Inspect the authentication module and report concrete security issues.","role":"code reviewer","maxSteps":20,"description":"Review authentication"}`,
	"skill":              `{"skill":"codegraph","args":"main"}`,
	"suggest":            `{"items":["Run go build to verify","Add unit tests"]}`,
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
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "File path to read."},
				"startLine": map[string]any{"type": "integer", "minimum": -MaxReadRangeLines, "description": "Optional 1-based start line. Positive reads from that line; negative reads the last N lines (max 10000) and must omit endLine."},
				"endLine":   map[string]any{"type": "integer", "minimum": 1, "description": "Optional inclusive end line; omit to read through EOF, and omit when startLine is negative."},
			},
			"required": []string{"path"},
		},
		"description": "Required array of file request objects. Example: [{\"path\":\"app.go\"}]. Each item must be an object with path; never use a string array. Missing paths and directories are silently omitted from results.",
	}
}

func editFilesSchema() map[string]any {
	return map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{
		"type": "object", "properties": map[string]any{
			"path":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
			"version": map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{6}$"},
			"changes": map[string]any{"type": "array", "minItems": 1, "maxItems": 50, "items": editChangeSchema()},
		}, "required": []string{"path", "version", "changes"},
	}}
}

func editChangeSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"oldText": map[string]any{
				"type": "string", "minLength": 1,
				"description": "Small exact unique source snippet copied exactly from the read result, without `N: ` prefixes; preferred over lineRange.",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Optional; defaults to false. With oldText, true replaces every non-overlapping exact occurrence in the original snapshot; with lineRange it is ignored.",
			},
			"lineRange": map[string]any{
				"type": "string", "pattern": "^[1-9][0-9]*-[1-9][0-9]*$",
				"description": "Inclusive whole-line A-B range from read's displayed line numbers, for larger blocks; replaces exactly those lines — a closing brace inside the range must be included, one outside stays untouched. All ranges use the original read version, so never adjust for earlier changes.",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "Replacement text without line prefixes. Empty deletes the selected source.",
			},
		},
		"required": []string{"newText"},
		"oneOf": []any{
			map[string]any{"required": []string{"oldText"}, "not": map[string]any{"required": []string{"lineRange"}}},
			map[string]any{"required": []string{"lineRange"}, "not": map[string]any{"required": []string{"oldText"}}},
		},
	}
}

// remoteEditFilesSchema / remoteEditChangeSchema 与本地 edit 的结构完全一致
// （键名、pattern、oneOf 与 DTO 解码对齐），仅描述精简：完整规则见本地
// edit 工具描述——两者每轮同场发送，远程描述只需指向它。
func remoteEditFilesSchema() map[string]any {
	return map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{
		"type": "object", "properties": map[string]any{
			"path":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
			"version": map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{6}$", "description": "Required 6-char version from remote_read_file."},
			"changes": map[string]any{"type": "array", "minItems": 1, "maxItems": 50, "items": remoteEditChangeSchema()},
		}, "required": []string{"path", "version", "changes"},
	}}
}

func remoteEditChangeSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"oldText":     map[string]any{"type": "string", "minLength": 1},
			"replace_all": map[string]any{"type": "boolean"},
			"lineRange":   map[string]any{"type": "string", "pattern": "^[1-9][0-9]*-[1-9][0-9]*$"},
			"newText":     map[string]any{"type": "string"},
		},
		"required": []string{"newText"},
		"oneOf": []any{
			map[string]any{"required": []string{"oldText"}, "not": map[string]any{"required": []string{"lineRange"}}},
			map[string]any{"required": []string{"lineRange"}, "not": map[string]any{"required": []string{"oldText"}}},
		},
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

// toolNameAliases maps deprecated tool names to their canonical names.
// Add an entry here when renaming a built-in tool so historical sessions
// (whose persisted tool_call names are the old spelling) keep dispatching
// correctly. The map is consulted after lower-casing, so aliases must use
// lowercase keys. MCP tool names (mcp__*) are never aliased here.
var toolNameAliases = map[string]string{
	"batch_read": "read", // legacy name; kept so historical sessions keep dispatching
}

// normalizeToolName lower-cases the incoming tool name and resolves any
// deprecated alias to its canonical name. It is the single entry point for
// tool-name normalization in executeTool.
func NormalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := toolNameAliases[name]; ok {
		return canonical
	}
	return name
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
