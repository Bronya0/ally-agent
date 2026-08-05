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
		functionTool("edit", "Validate and apply exact replacements across multiple workspace files in one call. Read returns numbered text; the numeric `N: ` prefixes are display-only and must never be copied into newText. For each change choose exactly one source. Prefer `lineRange` in inclusive `A-B` form for whole-line replacements (default for multi-line blocks; no need to reproduce the original text). Use `oldText` only for an in-line edit inside one line, or when the target line is extremely long (e.g. minified JSON) and a line range is impractical. Never pass both. Every lineRange in one file refers to the original line numbers from the read that produced `version`; do not adjust later ranges after earlier edits because Ally locates every source on the same snapshot and applies changes backwards. Each oldText must occur exactly once, source regions cannot overlap, and all files are validated before any write. Reuse the returned version only for a follow-up edit whose exact current source is known; otherwise re-read. Error codes: E_BAD_EDIT (malformed request, overlap, non-unique oldText, too many changes), E_VERSION_MISMATCH (file changed since read; re-read all affected files and retry with the new version), E_PATH_OUTSIDE.", map[string]any{
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
							"version": map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{12}$", "description": "Required 12-character current version from read or the preceding successful edit. Comparison is case-insensitive."},
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
		functionTool("create_file", "Create a new UTF-8 text file inside the workspace (or an additional session-level extra root). Parent directories are created automatically. Does not overwrite unless overwrite is true. Refuses symlink targets and non-text overwrites. Error codes: E_PATH_OUTSIDE, E_EXISTS, E_TARGET_IS_DIRECTORY, E_SYMLINK_PATH, E_TEXT_OVERWRITE.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"content":   map[string]any{"type": "string"},
				"overwrite": map[string]any{"type": "boolean"},
			},
			"required": []string{"path", "content"},
		}),
		functionTool("delete_path", "Delete a file, symlink, or directory in the workspace (or an additional session-level extra root). Directories require recursive=true. Refuses any allowed root, VCS metadata (.git, .svn, .hg), and OS-sensitive paths. Symlink parents are resolved for workspace safety; deleting a final symlink removes the link itself, not its target. Returns path, kind, and removed item counts. Error codes: E_PATH_OUTSIDE, E_PATH_NOT_FOUND, E_DIR_REQUIRES_RECURSIVE, E_DELETE_BLOCKED.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"recursive": map[string]any{"type": "boolean"},
			},
			"required": []string{"path"},
		}),
		functionTool("run_command", "Run a shell command with cwd confined to the workspace. On Windows, bash (from Git for Windows) is used when available, falling back to PowerShell; on macOS/Linux, bash is used. Commands may inspect outside paths, redirect to null devices, and create new outside paths. Modifying/deleting existing outside paths, explicit deletion commands, unsafe cwd symlinks, and long-running services are refused. The current session may also allow writes inside additional extra roots (the E_PATH_OUTSIDE error lists all allowed roots). If E_PATH_OUTSIDE is returned, read the Chinese reason and detected target: do not retry unchanged; use a new/workspace/extra-root target. Only literal existing outside write targets are blocked; dynamic targets (variables, globs, heredoc content) are allowed. Error codes: E_COMMAND_BLOCKED, E_PATH_OUTSIDE, E_CWD_INVALID, E_LONG_RUNNING_COMMAND.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":        map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*"},
				"cwd":            map[string]any{"type": "string", "description": "Relative working directory. Empty means workspace root."},
				"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 600, "description": "Default 120, max 600."},
			},
			"required": []string{"command"},
		}),
		functionTool("background_process", "Run, inspect, and stop long-running local processes (frontend/backend dev servers, Wails/Vite/Django/uvicorn, workers) without blocking the agent loop. The process starts in the Ally Task Center, where the user can inspect its live rolling output buffer (latest 512 KiB). action=start returns immediately with the process id and current status; the process keeps running in the background while the agent continues other work. action=list returns metadata for all tracked services without their output (call read to inspect a specific service). action=read returns a bounded tail of one service's output (default 8 KiB, max 32 KiB) plus byte accounting so the model can decide whether to read more. action=stop terminates a service by id. Use list/read sparingly: avoid polling loops, and prefer a single read after a concrete condition (e.g. wait + read) rather than repeated reads. Error codes: E_BAD_COMMAND, E_SERVICE_LIMIT, E_BAD_BACKGROUND_ACTION, E_BAD_SERVICE_ID, E_SERVICE_NOT_FOUND.", map[string]any{
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
		functionTool("ask", "Pause the current visible agent run and ask the user 1–5 decision questions. Every question must provide 2–6 concise, reasonable options with unique ids, labels, useful descriptions, and exactly one recommended option. The UI supports selecting multiple answers and automatically appends a final custom-answer choice, so do not include an Other/Custom option yourself. Call ask as the only tool in the model response. Error codes: E_BAD_ASK, E_ASK_CANCELLED, E_ASK_BATCH_CONFLICT.", map[string]any{
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
				"action":      map[string]any{"type": "string", "enum": []string{"create", "list", "delete"}},
				"id":          map[string]any{"type": "string", "minLength": 1, "description": "Task id required for delete."},
				"name":        map[string]any{"type": "string", "minLength": 1, "description": "Short task name required for create."},
				"instruction": map[string]any{"type": "string", "minLength": 1, "description": "Self-contained instruction executed with fresh context on every run."},
				"schedule":    map[string]any{"type": "string", "description": "RFC3339 time, Go duration such as 30m/2h, or standard five-field cron expression."},
				"timezone":    map[string]any{"type": "string", "description": "IANA timezone for cron, such as Asia/Shanghai. Defaults to local timezone."},
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
				"method":         map[string]any{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9!#$%&'*+.^_`|~-]*$", "description": "HTTP method token. Default GET; normalized to uppercase before sending."},
				"url":            map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Absolute http:// or https:// URL."},
				"headers":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Request headers. User-Agent defaults to AllyAgent unless provided."},
				"query":          map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Query parameters merged into the URL."},
				"body":           map[string]any{"type": "string", "description": "Raw request body. Mutually exclusive with json."},
				"json":           jsonValueSchema("JSON value to encode as the request body. Sets Content-Type to application/json unless provided."),
				"saveTo":         map[string]any{"type": "string", "description": "Optional workspace-relative download path. Parent directories are created automatically."},
				"maxBytes":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxHTTPBodyBytes, "description": "Maximum decoded response bytes. Default 262144; use saveTo for large downloads."},
				"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "description": "Request timeout. Default 60 seconds."},
			},
			"required": []string{"url"},
			"not":      map[string]any{"required": []string{"body", "json"}},
		}),
		functionTool("web_fetch", "Fetch a web page and return readable text, title, and links. Use for ordinary page reading instead of curl. Safe defaults include a bounded size, timeout, redirect limit, per-host rate limit, and clear User-Agent. Private/local network access follows the app's allowPrivateNetwork setting, which is enabled by default. robots.txt is not checked unless explicitly requested.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":            map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Absolute http:// or https:// URL."},
				"maxBytes":       map[string]any{"type": "integer", "minimum": 1, "maximum": maxHTTPBodyBytes, "description": "Maximum decoded source bytes read before text extraction. Default 2097152."},
				"maxChars":       map[string]any{"type": "integer", "minimum": 1, "maximum": 200000, "description": "Maximum readable text characters. Default 60000, max 200000."},
				"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "description": "Request timeout. Default 60 seconds."},
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
		functionTool("remote_read_file", "Read raw UTF-8 text from a remote SSH workspace. The returned content is directly copyable into remote_edit oldText, and its version is required by remote_edit. Omit startLine/endLine to read the whole file. With only startLine, read from that line through the end; with only endLine, read lines 1 through that inclusive range. Positive startLine values select an inclusive range; a negative startLine reads the last N lines (absolute value max 10000).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Explicit SSH target plus workspace root, e.g. my-dev:/srv/app."},
				"path":      map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Relative file path inside the remote workspace."},
				"startLine": map[string]any{"type": "integer", "minimum": -MaxReadRangeLines, "description": "Optional inclusive 1-based start line. Positive values start from the beginning; negative values read the last N lines, with absolute value at most 10000."},
				"endLine":   map[string]any{"type": "integer", "minimum": 1, "description": "Optional inclusive 1-based end line. Must be omitted when startLine is negative."},
			},
			"required": []string{"target", "path"},
		}),
		functionTool("remote_edit", "Validate and apply replacements across multiple files under one remote SSH target. remote_read_file returns numbered text. In each change choose exactly one source. Prefer an inclusive whole-line `lineRange` in `A-B` form for whole-line replacements; use a small unique `oldText` only for an in-line edit inside one line, or when the target line is extremely long (e.g. minified JSON); never both. All ranges use the original read version's line numbers and need no offset adjustment between changes.", map[string]any{
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
		functionTool("remote_run_command", "Run a non-interactive shell command in a remote SSH workspace. Uses system ssh BatchMode=yes and remote python3. Cwd defaults to the target workspace. Explicit deletion commands (rm, unlink, rmdir, del, erase, rd, remove-item) are refused with E_COMMAND_BLOCKED; use remote_delete_path for deletion. Other error codes: E_PATH_OUTSIDE, E_CWD_INVALID, E_LONG_RUNNING_COMMAND.", map[string]any{
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
		functionTool("grep_files", "Search UTF-8 text file contents using ripgrep (`rg`). Search is case-insensitive by default. Release packages bundle `rg`; development builds also search PATH. Workspace-relative paths are resolved under the workspace; explicit absolute paths are allowed for read-only search subject to safety checks. Returns match samples plus exact stats: count, occurrences, files, statsExact, samplesTruncated. `count` is matching lines; `occurrences` is total regex hits and should be used for questions like \"how many times does X appear\". `samplesTruncated=true` means only returned match samples were truncated; stats remain exact when statsExact=true. Error results include errorCode values such as E_GREP_REGEX, E_GREP_GLOB, E_GREP_TIMEOUT, E_GREP_PATH, E_SEARCH_ROOT_BLOCKED, and E_RIPGREP_NOT_FOUND. Skips binary/large/heavy directories. Use glob for basename patterns like *.go or relative path patterns like frontend/src/*.vue.", map[string]any{
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
			},
			"required": []string{"pattern"},
		}),
		functionTool("read", "Read 1–20 files. Always pass a top-level files array, even for one file. Do not pass top-level path, a string array, offset, or lineCount. Missing paths and directories are silently omitted from the returned files array; other per-file read failures remain visible. UTF-8 text content is prefixed with display-only 1-based `N: ` line numbers and includes a 12-character version for edit; do not copy the prefixes into edit text. Omit startLine/endLine for the whole file; either or both positive values define an inclusive range. For text files, a negative startLine reads the last N lines (absolute value max 10000) and must not be combined with endLine. Each displayed line is capped at 2000 Unicode characters for stable output; `truncatedLines` reports line numbers whose content was shortened. Large files are auto-truncated and the content ends with a `[Showing lines A-B of N. Use startLine=C to continue.]` marker; follow it to page through the rest. Supported document formats (.docx, .pptx, .xlsx, .pdf) return non-editable extracted text; .xlsx optionally accepts a sheet name or 1-based index.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files": batchReadFilesSchema(),
			},
			"required": []string{"files"},
		}),
		functionTool("memory_read", "Read one full global memory Markdown file from ~/.ally_agent/memories. Use this when a memory index description matches the task; do not rely on the index alone for detailed facts.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Path from the memory index, or a relative .md path under ~/.ally_agent/memories."},
			},
			"required": []string{"path"},
		}),
		functionTool("memory_write", "Create or update a global memory Markdown file. Existing memories require version from memory_read.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "Optional relative .md path under ~/.ally_agent/memories, or absolute path inside that directory. If omitted, a slug is generated from description."},
				"description": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Short searchable summary used in the memory index."},
				"content":     map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "Full Markdown memory body, without YAML frontmatter."},
				"version":     map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{12}$", "description": "Required version from memory_read when updating an existing memory. Comparison is case-insensitive."},
			},
			"required": []string{"description", "content"},
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
		functionTool("todo_write", "Create or update a visible task list only when longer work genuinely benefits from progress tracking. Do not use it for trivial tasks or merely to demonstrate activity. State machine discipline: keep at most one item `in_progress` at a time; mark the current item `done` before advancing the next; do not jump a `pending` item straight to `done`.", map[string]any{
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
				"description":  map[string]any{"type": "string", "description": "Short 3-5 word description for UI display."},
				"cleanContext": map[string]any{"type": "boolean", "description": "If true, skip workspace environment injection. Use for tasks that do not depend on project structure (e.g. write a standalone algorithm). Default false."},
				"model":        map[string]any{"type": "string", "description": "Optional model override. Default uses current model."},
			},
			"required": []string{"task"},
		}),
		functionTool("create_goal", "Create a tracked goal. The system will continue running turns until the goal is completed or blocked.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objective":           map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "The objective to pursue. Must have a verifiable end state."},
				"completionCriterion": map[string]any{"type": "string", "description": "How to verify the goal is complete. Optional."},
				"maxTurns":            map[string]any{"type": "integer", "minimum": 1, "description": "Maximum turns allowed. Default 10."},
			},
			"required": []string{"objective"},
		}),
		functionTool("update_goal", "Update current goal status: complete, blocked, or paused.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "enum": []any{"complete", "blocked", "paused"}, "description": "New goal status."},
				"reason": map[string]any{"type": "string", "description": "Optional reason for the status change."},
			},
			"required": []string{"status"},
		}),
		functionTool("get_goal", "Get the current goal status, progress and budget. Returns null if no goal is set.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		functionTool("skill", "Invoke a registered skill from the current skill listing. Use when the user wants to call a skill, or when you need instructions for a specific task covered by a skill.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill": map[string]any{"type": "string", "minLength": 1, "pattern": ".*\\S.*", "description": "The exact name of the skill to invoke, spelled as it appears in the current skill listing (e.g. \"codebase-design\", \"diagnosing-bugs\")."},
				"args":  map[string]any{"type": "string", "description": "Optional argument string for the skill, written like a command line (e.g. `-m \"fix bug\"`, `123`). Omit it for skills that take no arguments."},
			},
			"required": []string{"skill"},
		}),
	}
}

var builtinToolExamples = map[string]string{
	"list_files":         `{"path":"frontend/src","maxDepth":2,"limit":200}`,
	"edit":               `preferred whole-line change: {"files":[{"path":"app.go","version":"9k3m7x2p4t6w","changes":[{"lineRange":"40-72","newText":"func replacement() {\n}\n"}]}]}; in-line edit (only when a line range is impractical): {"files":[{"path":"app.go","version":"9k3m7x2p4t6w","changes":[{"oldText":"const oldName = \"ally\"","newText":"const newName = \"ally\""}]}]}; multiple original-snapshot ranges (no offset adjustment): {"files":[{"path":"app.go","version":"9k3m7x2p4t6w","changes":[{"lineRange":"10-15","newText":"first replacement"},{"lineRange":"80-95","newText":"second replacement"}]}]}`,
	"create_file":        `{"path":"notes/example.md","content":"# Example\n","overwrite":false}`,
	"delete_path":        `{"path":"tmp/generated","recursive":true}`,
	"run_command":        `{"command":"go test ./...","cwd":".","timeoutSeconds":120}`,
	"background_process": `start: {"action":"start","name":"frontend","command":"npm run dev","cwd":"frontend"}; stop: {"action":"stop","id":"svc_..."}; list: {"action":"list"}; read: {"action":"read","id":"svc_...","tailBytes":8192}`,
	"wait":               `{"seconds":5,"reason":"Wait for the development server to become ready"}`,
	"ask":                `{"questions":[{"id":"database","question":"Which database should we use?","options":[{"id":"sqlite","label":"SQLite","description":"Simple local storage.","recommended":true},{"id":"postgres","label":"PostgreSQL","description":"Production database.","recommended":false}]}]}`,
	"scheduled_task":     `create: {"action":"create","name":"daily check","instruction":"Run tests and summarize failures.","schedule":"0 9 * * *","timezone":"Asia/Shanghai"}; list: {"action":"list"}; delete: {"action":"delete","id":"task_..."}`,
	"http_request":       `{"url":"https://api.example.com/items","method":"GET","query":{"limit":"10"},"timeoutSeconds":60}`,
	"web_fetch":          `{"url":"https://example.com/docs","maxChars":60000}`,
	"remote_list_files":  `{"target":"ubuntu@example.com:/srv/app","path":"src","maxDepth":2}`,
	"remote_read_file":   `{"target":"ubuntu@example.com:/srv/app","path":"main.go","startLine":1,"endLine":200}`,
	"remote_edit":        `{"target":"ubuntu@example.com:/srv/app","files":[{"path":"main.go","version":"9k3m7x2p4t6w","changes":[{"lineRange":"12-30","newText":"func replacement() {\n}\n"}]}]}`,
	"remote_create_file": `{"target":"ubuntu@example.com:/srv/app","path":"notes.txt","content":"hello\n","overwrite":false}`,
	"remote_delete_path": `{"target":"ubuntu@example.com:/srv/app","path":"tmp/output","recursive":true}`,
	"remote_run_command": `{"target":"ubuntu@example.com:/srv/app","command":"go test ./...","cwd":".","timeoutSeconds":120}`,
	"grep_files":         `{"pattern":"TODO|FIXME","path":"frontend/src","glob":"*.vue","maxMatches":100}`,
	"read":               `one file: {"files":[{"path":"app.go"}]}; range: {"files":[{"path":"services.go","startLine":1,"endLine":200}]}; tail: {"files":[{"path":"server.log","startLine":-200}]}`,
	"memory_read":        `{"path":"coding-conventions.md"}`,
	"memory_write":       `{"path":"coding-conventions.md","description":"Project coding conventions","content":"Use focused changes and run tests."}`,
	"calculate":          `{"expression":"sqrt(144) + 2^3"}`,
	"render_html":        `{"title":"Interactive counter","html":"<button id='counter'>0</button><script>const button=document.getElementById('counter');button.onclick=()=>button.textContent=String(Number(button.textContent)+1)</script>"}`,
	"todo_write":         `update: {"todos":[{"title":"Inspect implementation","status":"in_progress"},{"title":"Run tests","status":"pending"}]}; read current: {}`,
	"subagent":           `{"task":"Inspect the authentication module and report concrete security issues.","description":"Review authentication","cleanContext":false}`,
	"create_goal":        `{"objective":"Make all tests pass","completionCriterion":"go test ./... exits successfully","maxTurns":10}`,
	"update_goal":        `{"status":"complete","reason":"All required tests pass."}`,
	"get_goal":           `{}`,
	"skill":              `{"skill":"review","args":"main"}`,
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
				"maxChars":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200000, "description": "Maximum extracted characters for this file's document extraction."},
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
			"version": map[string]any{"type": "string", "pattern": "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{12}$"},
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
				"description": "Small exact unique source snippet without read's numeric line prefixes. Use only for an in-line edit inside one line, or when the target line is extremely long (e.g. minified JSON). Choose this OR lineRange, never both.",
			},
			"lineRange": map[string]any{
				"type": "string", "pattern": "^[1-9][0-9]*-[1-9][0-9]*$",
				"description": "Inclusive original-snapshot whole-line range in A-B form, copied from read's displayed line numbers. Preferred source for whole-line replacements (default for multi-line blocks). Choose this OR oldText; all ranges in the file use the same read version, so never adjust for earlier changes.",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "Replacement text without numeric line prefixes. Empty deletes the selected source. Do not include unchanged source text outside the selected oldText or lineRange.",
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
