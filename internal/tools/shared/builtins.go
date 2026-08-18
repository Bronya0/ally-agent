// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package shared

import (
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

const (
	maxWaitSeconds    = 3600
	maxHTTPBodyBytes  = 50 * 1024 * 1024
	MaxReadRangeLines = 10000
	MaxReadLineChars  = 2000
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
				"includeHidden":  map[string]any{"type": "boolean", "description": "Include dotfiles and dot-directories. Default false."},
				"includeIgnored": map[string]any{"type": "boolean", "description": "Include heavy ignored directories such as .git, node_modules, dist, build. Default false."},
			},
		}),
		functionTool("edit", "Validate and apply exact replacements across multiple workspace files in one call. Each file requires the current 6-char `version` from read; a stale one fails with E_VERSION_MISMATCH (re-read all affected files and retry). Prefer a small exact unique `oldText` per change; `replace_all` replaces every non-overlapping exact occurrence; `lineRange` (A-B form) replaces larger whole-line blocks — choose oldText OR lineRange, never both. If exact matching fails, Ally auto-retries once normalizing invisible differences (trailing spaces, smart/Unicode quotes and dashes to ASCII); an ambiguous normalized match fails with E_MULTI_MATCH — add surrounding context rather than re-reading the whole file. All changes in a file share the original read version, so no offset adjustment between changes. Error codes: E_BAD_EDIT, E_VERSION_MISMATCH, E_PATH_OUTSIDE.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files": map[string]any{
					"type":        "array",
					"minItems":    1,
					"maxItems":    20,
					"description": "Files to edit in this call. Put all independent changes for the same file in one changes array when possible (max 50). Repeated normalized paths with the same version are merged against one original snapshot; total changes across all files must not exceed 200.",
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
		functionTool("create", "Create a new UTF-8 text file inside the workspace (or an additional session-level extra root). Parent directories are created automatically. Does not overwrite unless overwrite is true. Refuses symlink targets and non-text overwrites. Error codes: E_PATH_OUTSIDE, E_EXISTS, E_TARGET_IS_DIRECTORY, E_SYMLINK_PATH, E_TEXT_OVERWRITE.", map[string]any{
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
		functionTool("command", "Run a shell command with cwd confined to the workspace. On Windows the shell is Git Bash (bash) when available, otherwise PowerShell; on macOS/Linux, bash. Commands may inspect outside paths, redirect to null devices, and create new outside paths; modifying or deleting existing outside paths, explicit deletion commands, unsafe cwd symlinks, and long-running services are refused. The session may also allow writes inside extra roots (the E_PATH_OUTSIDE error lists all allowed roots) — on E_PATH_OUTSIDE, read the returned reason and switch target rather than retrying unchanged. When output exceeds the capture limit it is truncated and an `outputFilePath` points to the full output; read it if earlier output matters.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"cwd":            map[string]any{"type": "string", "description": "Relative working directory. Empty means workspace root."},
				"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 600, "description": "Default 120, max 600."},
			},
			"required": []string{"command"},
		}),
		functionTool("service", "Run, inspect, and stop long-running local processes (frontend/backend dev servers, Wails/Vite/Django/uvicorn, workers) without blocking the agent loop. action=start launches a process and returns its id; action=list shows tracked services; action=read returns a bounded tail of one service's output (default 8 KiB, max 32 KiB) plus byte accounting; action=stop terminates a service. Processes appear in the Task Center with a live rolling output buffer. Use list/read sparingly: avoid polling loops; prefer a single read after a concrete condition (e.g. wait + read). Error codes: E_BAD_COMMAND, E_SERVICE_LIMIT, E_BAD_BACKGROUND_ACTION, E_BAD_SERVICE_ID, E_SERVICE_NOT_FOUND.", map[string]any{
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
		functionTool("ask", "Pause the current visible agent run and ask the user decision questions. Every question must provide concise, reasonable options with unique ids, labels, useful descriptions, and exactly one recommended option. The UI supports selecting multiple answers and automatically appends a final custom-answer choice, so do not include an Other/Custom option yourself. Call ask as the only tool in the model response. Error codes: E_BAD_ASK, E_ASK_CANCELLED, E_ASK_BATCH_CONFLICT.", map[string]any{
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
		functionTool("scheduled_task", "Create, list, or delete temporary scheduled Agent tasks for the current Ally process. Create tasks only when the user explicitly requests scheduled or recurring automation. Scheduled executions receive the normal workspace tool set, including commands, file operations, network tools, MCP, and delegation; only scheduled_task itself is withheld to prevent recursive task creation. Tasks use isolated fresh context on every execution and are cleared whenever Ally closes or starts. Use action=list only when the user asks to inspect tasks or an id is needed; never poll it. Future task results are shown in the Task Center UI and are not injected into the current conversation.", map[string]any{
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
				"insecureSkipVerify": map[string]any{"type": "boolean", "description": "Skip TLS certificate verification. Default false. Use only for debugging or trusted internal services with self-signed certificates; enabling it weakens transport security."},
			},
			"required": []string{"url"},
			"not":      map[string]any{"required": []string{"body", "json"}},
		}),
		functionTool("web_fetch", "Fetch a web page and return readable text, title, and links. Use for ordinary page reading instead of curl. Safe defaults include a bounded size, timeout, redirect limit, per-host rate limit, and clear User-Agent. Private/local network access follows the app's allowPrivateNetwork setting, which is enabled by default. robots.txt is not checked unless explicitly requested.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":                map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Absolute http:// or https:// URL."},
				"maxBytes":           map[string]any{"type": "integer", "minimum": 1, "maximum": maxHTTPBodyBytes, "description": "Maximum decoded source bytes read before text extraction. Default 2097152."},
				"maxChars":           map[string]any{"type": "integer", "minimum": 1, "maximum": 200000, "description": "Maximum readable text characters. Default 60000, max 200000."},
				"timeoutSeconds":     map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "description": "Request timeout. Default 60 seconds."},
				"insecureSkipVerify": map[string]any{"type": "boolean", "description": "Skip TLS certificate verification. Default false. Use only for debugging or trusted internal services with self-signed certificates; enabling it weakens transport security."},
			},
			"required": []string{"url"},
		}),
		functionTool("remote_list_files", "List files on a remote SSH workspace. Target is explicit: host:/absolute/workspace or ssh://user@host:port/absolute/workspace. To inspect /home, use target host:/home with empty path; do not use host:/ for broad root listing. Uses system ssh with BatchMode=yes and remote python3.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus workspace root, e.g. my-dev:/srv/app or ubuntu@10.0.1.20:/home/ubuntu/project."},
				"path":          map[string]any{"type": "string", "description": "Relative directory path inside the remote workspace. Empty means workspace root."},
				"maxDepth":      map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "Maximum recursion depth. Default 3, max 20."},
				"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Maximum entries returned. Default 200, max 1000."},
				"includeHidden": map[string]any{"type": "boolean", "description": "Include dotfiles and dot-directories. Default false."},
			},
			"required": []string{"target"},
		}),
		functionTool("remote_read_file", "Read raw text from a remote SSH workspace. UTF-8 is returned as-is; UTF-16 LE/BE (with or without a BOM) is transcoded to UTF-8 for reading. The returned content is directly copyable into remote_edit oldText, and its version is required by remote_edit. Omit startLine/endLine to read the whole file. With only startLine, read from that line through the end; with only endLine, read lines 1 through that inclusive range. Positive startLine values select an inclusive range; a negative startLine reads the last N lines (absolute value max 10000).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus workspace root, e.g. my-dev:/srv/app."},
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Relative file path inside the remote workspace."},
				"startLine": map[string]any{"type": "integer", "minimum": -MaxReadRangeLines, "description": "Optional inclusive 1-based start line. Positive values start from the beginning; negative values read the last N lines, with absolute value at most 10000."},
				"endLine":   map[string]any{"type": "integer", "minimum": 1, "description": "Optional inclusive 1-based end line. Must be omitted when startLine is negative."},
			},
			"required": []string{"target", "path"},
		}),
		functionTool("remote_edit", "Validate and apply replacements across multiple files under one remote SSH target. Each file requires the `version` from remote_read_file. In each change choose exactly one source: a small exact `oldText` copied from remote_read_file (preferred, with enough surrounding context to be unique), or an inclusive whole-line `lineRange` in `A-B` form for larger blocks. `replace_all` (default false) replaces every non-overlapping exact occurrence; valid only with oldText, ignored with lineRange. All ranges use the original read version's line numbers, so no offset adjustment between changes.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus workspace root, e.g. my-dev:/srv/app."},
				"files":  editFilesSchema(),
			},
			"required": []string{"target", "files"},
		}),
		functionTool("remote_create_file", "Create or overwrite a UTF-8 text file in a remote SSH workspace. Uses single-shot write on the remote host.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"content":   map[string]any{"type": "string"},
				"overwrite": map[string]any{"type": "boolean"},
			},
			"required": []string{"target", "path", "content"},
		}),
		functionTool("remote_delete_path", "Delete a file or directory in a remote SSH workspace. Refuses workspace root, .git, and OS-sensitive paths. Prefer this over remote_run_command deletion.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"recursive": map[string]any{"type": "boolean"},
			},
			"required": []string{"target", "path"},
		}),
		functionTool("remote_run_command", "Run a non-interactive shell command in a remote SSH workspace. Uses system ssh BatchMode=yes and remote python3. Cwd defaults to the target workspace. Explicit deletion commands (rm, unlink, rmdir, del, erase, rd, remove-item) are refused with E_COMMAND_BLOCKED; use remote_delete_path for deletion. Use `grep -rn 'pattern' src/` to search remote code. Other error codes: E_PATH_OUTSIDE, E_CWD_INVALID, E_LONG_RUNNING_COMMAND.", map[string]any{
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
		functionTool("grep", "Search UTF-8 file contents with ripgrep (`rg`); returns exact match counts plus sample match lines grouped by file, sorted by relevance. Leaving `path` empty searches the whole workspace in one call with exact stats — do not repeat the search directory by directory. Workspace-wide searches report their skip policy in `skipped` (ignored files, heavy generated directories, and files over 10 MB); an explicit `path` search intentionally bypasses those broad exclusions. Case-insensitive by default; use `caseSensitive: true` or prefix the pattern with `(?-i)` for exact-case. `glob`: no slash matches the basename (`*.go`), with slash matches a relative path (`frontend/src/*.vue`). `contextBefore`/`contextAfter` (0-50) add surrounding context lines, often avoiding a separate `read`; model compaction budgets real matches and context lines separately. On sample truncation, page with the returned `nextOffset` as `offset`; count/occurrence/file stats stay exact. Re-read a file before using a sampled line as edit source text, since long lines may be truncated. Workspace-relative paths resolve under the workspace; explicit absolute paths allowed for read-only search.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "ripgrep regex pattern to search for."},
				"path":           map[string]any{"type": "string", "description": "Optional workspace-relative subdirectory, or explicit absolute path for read-only search. Empty means workspace root."},
				"glob":           map[string]any{"type": "string", "description": "Optional glob filter. No slash means match basename (e.g. *.go); slash means match relative path."},
				"maxFiles":       map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Maximum matching files to include in returned match samples. Default 50. Exact count/occurrence/file stats still scan all matches."},
				"maxMatches":     map[string]any{"type": "integer", "minimum": 1, "maximum": 5000, "description": "Maximum matching lines to include in returned match samples. Default maxFiles*10, max 5000. Exact count/occurrence/file stats still scan all matches."},
				"maxDepth":       map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Maximum directory depth. Default 20, max 100."},
				"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "description": "Overall grep timeout across exact stats and sample collection. Default 30, max 120."},
				"includeIgnored": map[string]any{"type": "boolean", "description": "Include files ignored by .gitignore/.ignore. Default false."},
				"caseSensitive":  map[string]any{"type": "boolean", "description": "Match case exactly. Default false (case-insensitive)."},
				"offset":         map[string]any{"type": "integer", "minimum": 0, "description": "Skip the first N matching lines before collecting samples. Pass the previous result's nextOffset to page through large result sets; a result with offsetExhausted: true means the offset skipped past the end, so reset it to 0. Default 0."},
				"contextBefore":  map[string]any{"type": "integer", "minimum": 0, "maximum": 50, "description": "Include N context lines before each match (rg -B). Context lines are marked `context: true` and never count toward stats or offsets. Default 0."},
				"contextAfter":   map[string]any{"type": "integer", "minimum": 0, "maximum": 50, "description": "Include N context lines after each match (rg -A). Context lines are marked `context: true` and never count toward stats or offsets. Default 0."},
			},
			"required": []string{"pattern"},
		}),
		functionTool("read", "Read 1-20 files via a top-level `files` array (even for one file). Do not pass top-level path/paths fields, and never use a string array; every `files` item must be an object with `path`. Missing paths and directories are silently omitted; other per-file failures remain visible. UTF-8 text is prefixed with display-only 1-based `N: ` line numbers and a 6-char `version` for edit — do not copy the line prefixes into edit text. Omit startLine/endLine for the whole file; positive values define an inclusive range; a negative startLine reads the last N lines (max 10000) and must not combine with endLine. Large files are auto-truncated and end with a `[Showing lines A-B of N. Use startLine=C to continue.]` marker — follow it to page. Document formats (.docx, .pptx, .xlsx, .pdf) return non-editable extracted text; .xlsx accepts a sheet name or 1-based index.", map[string]any{
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
		functionTool("render_html", "Render a self-contained HTML snippet inline in the chat UI. Use ONLY for interactive widgets or custom visualizations that Mermaid and Markdown cannot express — interactive calculators, dynamic data explorers, styled mockups, custom animated SVG. Do NOT use for diagrams, flowcharts, pie charts, or tables (use Mermaid fenced blocks and Markdown tables instead). Rendered in a sandboxed iframe with a dark theme. Maximum 50,000 characters. No external resources. Return a short text summary in your response explaining what was rendered.", map[string]any{
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
		functionTool("subagent", "Delegate a task to a child agent with its own tool loop (no step or wall-clock limit). The child can use built-in and MCP tools but cannot ask the user or delegate nested agents; only its final summary is returned. See the Delegation section in the system prompt for when to use this tool.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":         map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "The task for the child agent. Be specific — include file paths and expected outcomes."},
				"role":         map[string]any{"type": "string", "minLength": 1, "maxLength": 80, "pattern": ".*\\S.*", "description": "The role of the sub-agent, e.g. \"researcher\", \"code reviewer\", \"tester\". Shown as the card label in the UI and injected into the sub-agent's system prompt."},
				"description":  map[string]any{"type": "string", "description": "Short 3-5 word description for UI display."},
				"cleanContext": map[string]any{"type": "boolean", "description": "If true, skip workspace environment injection. Use for tasks that do not depend on project structure (e.g. write a standalone algorithm). Default false."},
				"model":        map[string]any{"type": "string", "description": "Optional model override. Default uses current model."},
			},
			"required": []string{"task", "role"},
		}),
		functionTool("skill", "Invoke a registered skill from the current skill listing. Use when the user wants to call a skill, or when you need instructions for a specific task covered by a skill.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "The exact name of the skill to invoke, spelled as it appears in the current skill listing (e.g. \"codebase-design\", \"diagnosing-bugs\")."},
				"args":  map[string]any{"type": "string", "description": "Optional argument string for the skill, written like a command line (e.g. `-m \"fix bug\"`, `123`). Omit it for skills that take no arguments."},
			},
			"required": []string{"skill"},
		}),
		functionTool("suggest", "Suggest 1-4 follow-up actions as clickable chips below your last reply, ordered by relevance from most to least recommended. Each label is sent as-is as the user's next message when clicked. Call this only when the user might genuinely benefit from a concrete next step; if no useful follow-up exists, do not call it. Must be the only tool call in its batch. Error codes: E_BAD_SUGGEST, E_SUGGEST_BATCH_CONFLICT.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type":        "array",
					"minItems":    1,
					"maxItems":    4,
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
	"edit":               `preferred exact-string change: {"files":[{"path":"app.go","version":"9k3m7x","changes":[{"oldText":"const oldName = oldValue","newText":"const newName = newValue"}]}]}; replace every exact match: {"files":[{"path":"app.go","version":"9k3m7x","changes":[{"oldText":"oldValue","newText":"newValue","replace_all":true}]}]}; larger whole-line change (when exact source is impractical to reproduce): {"files":[{"path":"app.go","version":"9k3m7x","changes":[{"lineRange":"40-72","newText":"replacement block"}]}]}; multiple original-snapshot changes (no offset adjustment): {"files":[{"path":"app.go","version":"9k3m7x","changes":[{"oldText":"const first = 1","newText":"const first = 2"},{"oldText":"const second = 1","newText":"const second = 2"}]}]}`,
	"create":             `{"path":"notes/example.md","content":"# Example\n","overwrite":false}`,
	"delete":             `{"path":"tmp/generated","recursive":true}`,
	"command":            `{"command":"go test ./...","cwd":".","timeoutSeconds":120}`,
	"service":            `start: {"action":"start","name":"frontend","command":"npm run dev","cwd":"frontend"}; stop: {"action":"stop","id":"svc_..."}; list: {"action":"list"}; read: {"action":"read","id":"svc_...","tailBytes":8192}`,
	"wait":               `{"seconds":5,"reason":"Wait for the development server to become ready"}`,
	"ask":                `{"questions":[{"id":"database","question":"Which database should we use?","options":[{"id":"sqlite","label":"SQLite","description":"Simple local storage.","recommended":true},{"id":"postgres","label":"PostgreSQL","description":"Production database.","recommended":false}]}]}`,
	"scheduled_task":     `create: {"action":"create","name":"daily check","instruction":"Run tests and summarize failures.","schedule":"0 9 * * *"}; list: {"action":"list"}; delete: {"action":"delete","id":"task_..."}`,
	"http_request":       `{"url":"https://api.example.com/items","method":"GET","query":{"limit":"10"},"timeoutSeconds":60}`,
	"web_fetch":          `{"url":"https://example.com/docs","maxChars":60000}`,
	"remote_list_files":  `{"target":"ubuntu@example.com:/srv/app","path":"src","maxDepth":2}`,
	"remote_read_file":   `{"target":"ubuntu@example.com:/srv/app","path":"main.go","startLine":1,"endLine":200}`,
	"remote_edit":        `{"target":"ubuntu@example.com:/srv/app","files":[{"path":"main.go","version":"9k3m7x","changes":[{"oldText":"func oldName() {}","newText":"func newName() {}","replace_all":true}]}]}`,
	"remote_create_file": `{"target":"ubuntu@example.com:/srv/app","path":"notes.txt","content":"hello\n","overwrite":false}`,
	"remote_delete_path": `{"target":"ubuntu@example.com:/srv/app","path":"tmp/output","recursive":true}`,
	"remote_run_command": `{"target":"ubuntu@example.com:/srv/app","command":"go test ./...","cwd":".","timeoutSeconds":120}`,
	"grep":               `{"pattern":"TODO|FIXME","path":"frontend/src","glob":"*.vue","maxMatches":100}`,
	"read":               `one file: {"files":[{"path":"app.go"}]}; range: {"files":[{"path":"services.go","startLine":1,"endLine":200}]}; tail: {"files":[{"path":"server.log","startLine":-200}]}`,
	"calculate":          `{"expression":"sqrt(144) + 2^3"}`,
	"render_html":        `{"title":"Interactive counter","html":"<button id='counter'>0</button><script>const button=document.getElementById('counter');button.onclick=()=>button.textContent=String(Number(button.textContent)+1)</script>"}`,
	"plan":               `update: {"todos":[{"title":"Inspect implementation","status":"in_progress"},{"title":"Run tests","status":"pending"}]}; read current: {}`,
	"subagent":           `{"task":"Inspect the authentication module and report concrete security issues.","role":"code reviewer","description":"Review authentication","cleanContext":false}`,
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
				"startLine": map[string]any{"type": "integer", "minimum": -MaxReadRangeLines, "description": "Optional inclusive 1-based start line. Positive values start from the beginning; negative values read the last N lines, with absolute value at most 10000. Omit endLine when using a negative value."},
				"endLine":   map[string]any{"type": "integer", "minimum": 1, "description": "Optional inclusive 1-based end line. If omitted, reading continues to the final line; omit it when startLine is negative."},
				"sheet":     map[string]any{"type": "string", "description": "Xlsx sheet name or 1-based sheet index for this file."},
				"maxChars":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200000, "description": "Maximum extracted characters for document extraction (.docx/.pptx/.xlsx/.pdf) only; for text files use startLine/endLine to bound the read."},
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
				"description": "Small exact unique source snippet without read's numeric line prefixes. Preferred source for precise replacements, including focused multi-line snippets when enough surrounding context makes it unique. Copy it exactly from the read result. Choose this OR lineRange, never both.",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Optional; defaults to false. With oldText, true replaces every non-overlapping exact occurrence in the original snapshot. With lineRange, it is ignored and reported as a warning.",
			},
			"lineRange": map[string]any{
				"type": "string", "pattern": "^[1-9][0-9]*-[1-9][0-9]*$",
				"description": "Inclusive original-snapshot whole-line range in A-B form, copied from read's displayed line numbers. Use as a fallback for larger whole-line replacements or when reproducing the exact source is impractical (for example, an extremely long single line). The range replaces exactly those whole lines; lines outside it (e.g. a closing brace on the next line) stay untouched, so newText must not re-emit them. Choose this OR oldText; replace_all is ignored and reported as a warning with lineRange; all ranges in the file use the same read version, so never adjust for earlier changes.",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "Replacement text without numeric line prefixes. Empty deletes the selected source. Do not include unchanged source text outside the selected oldText or lineRange. For lineRange, newText replaces the whole selected block exactly: a closing brace outside the range stays in the file (do not add it), while one inside the range must be included.",
			},
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
