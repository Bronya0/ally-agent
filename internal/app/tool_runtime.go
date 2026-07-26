package app

// tool_runtime.go consolidates the App-owned tool orchestration that used to
// live in read.go, read_bridge.go, grep.go, scheduler.go, services.go,
// git_tools.go, memory.go, command_safety.go, file_edit.go,
// file_edit_plan.go, and batch_policy.go. These shims bind the pure
// algorithms under internal/tools/ to App state, ConfigState, and the event
// sink; they are not tool implementations themselves and intentionally stay
// in package app so they can reference unexported App helpers.
//
// Section layout:
//   1. Imports & re-exports
//   2. Command safety (was command_safety.go)
//   3. Git tools (was git_tools.go)
//   4. Grep (was grep.go)
//   5. Memory (was memory.go)
//   6. Read (was read.go + read_bridge.go)
//   7. Scheduler (was scheduler.go)
//   8. Services (was services.go)
//   9. Edit (was file_edit.go)
//  10. Edit batch plan (was file_edit_plan.go)
//  11. Tool batch policy (was batch_policy.go)

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	goruntime "runtime"

	"github.com/robfig/cron/v3"
	openai "github.com/sashabaranov/go-openai"

	"ally-dev/internal/tools/command"
	"ally-dev/internal/tools/edit"
	"ally-dev/internal/tools/git"
	"ally-dev/internal/tools/grep"
	"ally-dev/internal/tools/memory"
	"ally-dev/internal/tools/read"
	"ally-dev/internal/tools/scheduler"
	"ally-dev/internal/tools/service"
)

// ───────────────────────── Section 1: Re-exports ─────────────────────────

// rollingBuffer is the alias re-exported from the service tool package so
// app-side managedService can hold a reference without importing the tool
// package at call sites.
type rollingBuffer = service.RollingBuffer

func newRollingBuffer(limit int) *rollingBuffer {
	return service.NewRollingBuffer(limit)
}

func tailString(s string, limit int) string {
	return service.TailString(s, limit)
}

func normalizeServiceCommand(command string) string {
	return service.NormalizeCommand(command)
}

// looksLikeLongRunningService only blocks an explicit whitelist of known
// dev-server commands. Anything else continues to the normal run_command
// safety checks/timeouts.
func looksLikeLongRunningService(command string) bool {
	return service.LooksLikeLongRunningService(command)
}

func longRunningCommandError(command string) error {
	return service.LongRunningCommandError(command)
}

// ─────────────────────── Section 2: Command safety ───────────────────────
// (was command_safety.go)

// checkCommandSafety inspects commands for high-risk patterns and routes
// explicit deletion through delete_path, where workspace and OS guards apply.
// roots[0] 是主工作区（命令的默认 cwd），其余为会话级附加根目录。
func checkCommandSafety(req CommandRequest, roots []string) error {
	cmd := req.Command
	if command.ContainsExplicitDeleteCommand(cmd) && !command.IsAllowedDeleteContext(cmd) {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("安全围栏已拦截：run_command 不允许直接执行文件删除命令。\n原因：shell 删除命令可能绕过工作区边界、系统目录和 .git 保护。\n处理方式：请改用 delete_path 工具，由专用工具检查目标路径和递归范围。\n被拦截的命令：%s", cmd))
	}
	if risk := firstExistingOutsideMutationTarget(cmd, roots); risk != nil {
		return codedToolError("E_PATH_OUTSIDE", fmt.Errorf("安全围栏已拦截：命令可能修改工作区外的受保护目标。\n原因：%s。\n检测到的目标：%s\n允许的操作：读取工作区外路径、写入 /dev/null 等空设备、创建不存在的新路径。\n禁止的操作：覆盖、追加、移动、改权限或以其他方式修改已经存在的工作区外文件或目录。\n允许写入的根目录：\n%s\n被拦截的命令：%s", risk.Reason, risk.Path, formatAllowedRoots(roots), cmd))
	}
	if risk := command.MatchRiskPattern(cmd); risk != nil {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("高危命令拒绝: 检测到%s - 命令已被安全围栏拦截。\n如需执行此操作，请手动在终端中执行。\n被拦截的命令: %s", risk.Reason, cmd))
	}
	return nil
}

// outsideMutationRisk describes an outside-write risk that the UI can explain
// directly. Creating a new literal outside path is allowed, while changing an
// existing path or using an unresolved redirection target remains blocked.
// Harmless sinks such as /dev/null are ignored.
type outsideMutationRisk struct {
	Path   string
	Reason string
}

func firstExistingOutsideMutationTarget(commandLine string, roots []string) *outsideMutationRisk {
	if len(roots) == 0 {
		return nil
	}
	primaryRoot := roots[0]
	for _, target := range command.ShellRedirectionTargets(commandLine) {
		if command.IsShellNullDevice(target) {
			continue
		}
		path, ok := command.ResolveCommandLiteralPath(target, primaryRoot)
		if !ok {
			return &outsideMutationRisk{
				Path:   target,
				Reason: "重定向目标包含变量、通配符或命令替换，执行前无法确认真实写入位置",
			}
		}
		if insideAnyRoot(roots, path) || insideAllyAgentDir(path) {
			continue
		}
		if command.PathExists(path) {
			return &outsideMutationRisk{
				Path:   filepath.ToSlash(path),
				Reason: "重定向目标已经存在，继续执行可能覆盖或追加其内容",
			}
		}
	}

	if !command.MayModifyOutsidePath(commandLine) {
		return nil
	}
	for _, candidate := range command.AbsolutePathCandidates(commandLine) {
		if command.IsShellNullDevice(candidate) {
			continue
		}
		clean := filepath.Clean(candidate)
		if insideAnyRoot(roots, clean) || insideAllyAgentDir(clean) {
			continue
		}
		if command.PathExists(clean) {
			return &outsideMutationRisk{
				Path:   filepath.ToSlash(clean),
				Reason: "命令包含写入、移动、改权限或原地修改操作，并引用了已经存在的工作区外路径",
			}
		}
	}
	return nil
}

func validateRemoteCommandSafety(cmd string) error {
	if risk := command.MatchRiskPattern(cmd); risk != nil {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("高危命令拒绝: 检测到%s - 命令已被安全围栏拦截。\n如需执行此操作，请手动在终端中执行。\n被拦截的命令: %s", risk.Reason, cmd))
	}
	return nil
}

func firstAbsolutePathOutsideWorkspace(commandLine string, workspaceRoot string) string {
	root := filepath.Clean(workspaceRoot)
	for _, candidate := range command.AbsolutePathCandidates(commandLine) {
		if candidate == "" {
			continue
		}
		clean := filepath.Clean(candidate)
		if !insideRoot(root, clean) && !insideAllyAgentDir(clean) {
			return filepath.ToSlash(clean)
		}
	}
	return ""
}

// ───────────────────────── Section 3: Git tools ─────────────────────────
// (was git_tools.go)

func (a *App) GetGitStatus() GitStatus {
	workspace, err := workspaceRoot(a.config)
	if err != nil {
		return GitStatus{IsRepo: false}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root, err := gitRepoRoot(ctx, workspace)
	if err != nil {
		return GitStatus{IsRepo: false}
	}

	branchOut, _, err := runGitLimited(ctx, root, 16*1024, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return GitStatus{IsRepo: false}
	}
	branch := strings.TrimSpace(string(branchOut))

	out, _, err := runGitLimited(ctx, root, 256*1024, "status", "--porcelain=v1", "-z")
	if err != nil {
		return GitStatus{IsRepo: false}
	}
	st := GitStatus{IsRepo: true, Branch: branch}
	for _, entry := range git.ParseStatusZ(out) {
		switch entry.Status {
		case "modified", "renamed", "copied":
			st.Modified++
		case "added", "untracked":
			st.Added++
		case "deleted":
			st.Deleted++
		}
	}
	return st
}

func (a *App) GetGitDiff() GitDiffResult {
	workspace, err := workspaceRoot(a.config)
	if err != nil {
		return GitDiffResult{IsRepo: false, Error: err.Error()}
	}

	ctx, cancel, runID := a.beginGitDiffRequest()
	defer a.endGitDiffRequest(runID, cancel)

	root, err := gitRepoRoot(ctx, workspace)
	if err != nil {
		return GitDiffResult{IsRepo: false, Error: err.Error()}
	}

	branchOut, _, err := runGitLimited(ctx, root, 16*1024, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return GitDiffResult{IsRepo: false, Error: err.Error()}
	}
	result := GitDiffResult{IsRepo: true, Branch: strings.TrimSpace(branchOut)}

	statusOut, statusTruncated, err := runGitLimited(ctx, root, 256*1024, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Truncated = statusTruncated

	entries := git.ParseStatusZ(statusOut)
	const maxFiles = 80
	const maxTotalDiffBytes = 512 * 1024
	const maxDiffBytesPerFile = 96 * 1024
	const maxAggregateDiffBytes = maxFiles * maxDiffBytesPerFile
	// Fetch tracked changes in two repository-wide calls. The previous
	// implementation spawned two git processes per file, which is especially
	// expensive on Windows and could approach the request's 10-second timeout.
	unstagedOut, unstagedTruncated, unstagedErr := runGitLimited(ctx, root, maxAggregateDiffBytes, "diff", "--no-ext-diff", "--find-renames", "--find-copies")
	stagedOut, stagedTruncated, stagedErr := runGitLimited(ctx, root, maxAggregateDiffBytes, "diff", "--cached", "--no-ext-diff", "--find-renames", "--find-copies")
	unstagedByPath := git.SplitUnifiedDiffByPath(unstagedOut)
	stagedByPath := git.SplitUnifiedDiffByPath(stagedOut)
	if unstagedTruncated || stagedTruncated {
		result.Truncated = true
	}
	if unstagedErr != nil || stagedErr != nil {
		var errs []string
		if unstagedErr != nil {
			errs = append(errs, unstagedErr.Error())
		}
		if stagedErr != nil {
			errs = append(errs, stagedErr.Error())
		}
		result.Error = strings.Join(errs, "; ")
		return result
	}
	totalBytes := 0
	for _, entry := range entries {
		if len(result.Files) >= maxFiles {
			result.Truncated = true
			break
		}
		if totalBytes >= maxTotalDiffBytes {
			result.Truncated = true
			break
		}
		remaining := maxTotalDiffBytes - totalBytes
		fileLimit := maxDiffBytesPerFile
		if remaining < fileLimit {
			fileLimit = remaining
		}

		file := GitDiffFile{Path: entry.Path, Status: entry.Status}
		if entry.Untracked {
			file.Diff, file.Truncated, file.Binary, file.Error = synthesizeUntrackedDiffApp(root, entry.Path, fileLimit)
		} else {
			sections := make([]string, 0, 2)
			if staged := stagedByPath[entry.Path]; staged != "" {
				sections = append(sections, staged)
			}
			if unstaged := unstagedByPath[entry.Path]; unstaged != "" {
				sections = append(sections, unstaged)
			}
			combined := strings.TrimRight(strings.Join(sections, "\n"), "\n")
			if len(combined) > fileLimit {
				combined = combined[:fileLimit]
				file.Truncated = true
			}
			file.Diff = combined
			file.Binary = git.LooksLikeBinaryDiff(file.Diff)
		}
		file.Added, file.Deleted = git.CountUnifiedDiffStats(file.Diff)
		if file.Truncated {
			result.Truncated = true
		}
		totalBytes += len(file.Diff)
		result.Files = append(result.Files, file)
	}

	return result
}

func gitRepoRoot(ctx context.Context, workspace string) (string, error) {
	out, _, err := runGitLimited(ctx, workspace, 64*1024, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("git repository root is empty")
	}
	abs, err := filepath.Abs(filepath.FromSlash(root))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("git repository root is not a directory: %s", abs)
	}
	return filepath.Clean(abs), nil
}

func (a *App) CancelGitDiff() {
	a.gitDiffMu.Lock()
	defer a.gitDiffMu.Unlock()
	if a.gitDiffCancel != nil {
		a.gitDiffCancel()
		a.gitDiffCancel = nil
	}
	a.gitDiffRunID++
}

func (a *App) beginGitDiffRequest() (context.Context, context.CancelFunc, int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	a.gitDiffMu.Lock()
	if a.gitDiffCancel != nil {
		a.gitDiffCancel()
	}
	a.gitDiffRunID++
	runID := a.gitDiffRunID
	a.gitDiffCancel = cancel
	a.gitDiffMu.Unlock()
	return ctx, cancel, runID
}

func (a *App) endGitDiffRequest(runID int64, cancel context.CancelFunc) {
	cancel()
	a.gitDiffMu.Lock()
	if a.gitDiffRunID == runID {
		a.gitDiffCancel = nil
	}
	a.gitDiffMu.Unlock()
}

// synthesizeUntrackedDiffApp reads the untracked file from disk and delegates
// diff synthesis to git. File IO and workspace path resolution stay here;
// pure diff construction lives in internal/tools/git.
func synthesizeUntrackedDiffApp(root, rel string, limit int) (string, bool, bool, string) {
	fullPath, err := safeJoin([]string{root}, rel)
	if err != nil {
		return "", false, false, err.Error()
	}
	data, _, err := readTextFile(fullPath)
	if err != nil {
		binary := strings.Contains(strings.ToLower(err.Error()), "binary")
		return git.SynthesizeUntrackedDiff(rel, "", err, binary, limit)
	}
	text, _ := normalizeText(data)
	return git.SynthesizeUntrackedDiff(rel, text, nil, false, limit)
}

func runGitLimited(ctx context.Context, root string, limit int, args ...string) (string, bool, error) {
	if limit < 1 {
		limit = 1
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	hideCommandWindow(cmd)
	buf := git.NewLimitedBuffer(limit)
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return buf.String(), buf.Truncated(), err
}

// ───────────────────────── Section 4: Grep ─────────────────────────
// (was grep.go)

func (a *App) GrepFiles(req GrepRequest) (*GrepResult, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.grepFilesWithConfig(ctx, a.effectiveConfig(ConfigState{}), req)
}

func (a *App) grepFilesWithConfig(ctx context.Context, cfg ConfigState, req GrepRequest) (*GrepResult, error) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return nil, codedToolError("E_GREP_WORKSPACE", err)
	}
	if strings.TrimSpace(req.Pattern) == "" {
		return nil, codedToolError("E_GREP_BAD_PATTERN", errors.New("grep_files requires a non-empty pattern"))
	}
	searchRoot := root
	if strings.TrimSpace(req.Path) != "" {
		searchRoot, err = resolveReadablePath(cfg, req.Path)
		if err != nil {
			return nil, codedToolError("E_GREP_PATH", err)
		}
	}
	if _, err := os.Stat(searchRoot); err != nil {
		return nil, codedToolError("E_GREP_PATH", err)
	}

	// Safety: block broad/system searches only outside the selected workspace.
	if !insideRoot(root, searchRoot) {
		if blocked, reason := isDangerousSearchRoot(searchRoot); blocked {
			return nil, codedToolError("E_SEARCH_ROOT_BLOCKED", fmt.Errorf("%s\n\nThis search has been blocked for safety. If you need to search this path, do it manually.", reason))
		}
	}

	rgPath, err := grep.Find()
	if err != nil {
		a.emitRipgrepMissingIfNeeded()
		return nil, grep.MissingError()
	}

	timeoutSeconds := grep.TimeoutSeconds(req)
	grepCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	result, err := grep.Search(grepCtx, rgPath, root, searchRoot, req)
	if err != nil {
		return nil, grep.NormalizeError(err, timeoutSeconds)
	}
	return result, nil
}

func displayPathForConfig(cfg ConfigState, p string) string {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(p))
	}
	return grep.DisplayPathForRoot(root, p)
}

func (a *App) emitRipgrepMissingIfNeeded() {
	if _, err := grep.Find(); err == nil {
		return
	}
	a.emit("dependency:missing", map[string]any{
		"tool":         "rg",
		"name":         "ripgrep",
		"message":      "grep_files requires ripgrep (`rg`), but it was not found. Install ripgrep and restart Ally.",
		"installSteps": grep.InstallInstructions(),
	})
}

func (a *App) emitGitBashMissingIfNeeded() {
	if goruntime.GOOS != "windows" {
		return
	}
	cfg, err := a.getConfig()
	if err != nil {
		return
	}
	if _, bashName := findWindowsBash(cfg.GitBashPath); bashName != "" {
		return
	}
	a.emit("dependency:missing", map[string]any{
		"tool":       "bash",
		"name":       "Git Bash",
		"messageKey": "app.dependency.gitBashMissing",
		"message":    "Git Bash was not found. run_command will fall back to PowerShell. Install Git for Windows, or set the Git Bash path in Settings → General → Git Bash Path.",
		"installStepKeys": []string{
			"app.dependency.gitBashDownload",
			"app.dependency.gitBashConfigure",
		},
		"installSteps": []string{
			"Download from https://git-scm.com/download/win",
			"Or set the path manually in Settings, e.g. C:\\Program Files\\Git\\bin\\bash.exe",
		},
	})
}

// ───────────────────────── Section 5: Memory ─────────────────────────
// (was memory.go)

type memoryIndexCacheType struct {
	sync.Mutex
	result    MemoryListResult
	dirMtime  time.Time
	populated bool
}

var memoryIndexCache = memoryIndexCacheType{}

func (c *memoryIndexCacheType) lookup() (MemoryListResult, bool) {
	c.Lock()
	defer c.Unlock()
	if !c.populated {
		return MemoryListResult{}, false
	}
	if info, err := os.Stat(memoriesDir()); err == nil {
		if info.ModTime().After(c.dirMtime) {
			return MemoryListResult{}, false
		}
	}
	return c.result, true
}

func (c *memoryIndexCacheType) store(result MemoryListResult) {
	dir := memoriesDir()
	mtime := time.Time{}
	if info, err := os.Stat(dir); err == nil {
		mtime = info.ModTime()
	}
	c.Lock()
	c.result = result
	c.dirMtime = mtime
	c.populated = true
	c.Unlock()
}

func (c *memoryIndexCacheType) invalidate() {
	c.Lock()
	c.populated = false
	c.result = MemoryListResult{}
	c.Unlock()
}

func listMemories() (MemoryListResult, error) {
	dir := memoriesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return MemoryListResult{}, err
	}
	entries := []MemoryIndexEntry{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		data, info, err := readTextFile(path)
		if err != nil {
			return nil
		}
		desc, _ := memory.ParseMarkdown(string(data))
		if strings.TrimSpace(desc) == "" {
			return nil
		}
		entries = append(entries, MemoryIndexEntry{
			Path:        filepath.ToSlash(path),
			Description: desc,
			SHA256:      hashBytes(data),
			Size:        info.Size(),
		})
		return nil
	})
	if err != nil {
		return MemoryListResult{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Path) < strings.ToLower(entries[j].Path)
	})
	return MemoryListResult{Dir: filepath.ToSlash(dir), Memories: entries, Count: len(entries)}, nil
}

func resolveMemoryPath(p string) (string, error) {
	root := memoriesDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("memory path is required")
	}
	var target string
	if filepath.IsAbs(p) {
		target = p
	} else {
		target = filepath.Join(root, filepath.Clean(p))
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	rootAbs = filepath.Clean(rootAbs)
	if !insideRoot(rootAbs, abs) || strings.ToLower(filepath.Ext(abs)) != ".md" {
		return "", fmt.Errorf("memory path must be a .md file under %s", filepath.ToSlash(rootAbs))
	}
	return abs, nil
}

func defaultMemoryPath(description string) string {
	return memory.DefaultPath(description)
}

func (a *App) memoryRead(req MemoryReadRequest) (MemoryReadResult, error) {
	path, err := resolveMemoryPath(req.Path)
	if err != nil {
		return MemoryReadResult{}, err
	}
	data, info, err := readTextFile(path)
	if err != nil {
		return MemoryReadResult{}, err
	}
	desc, body := memory.ParseMarkdown(string(data))
	return MemoryReadResult{
		Path:        filepath.ToSlash(path),
		Description: desc,
		Content:     body,
		SHA256:      hashBytes(data),
		Version:     hashVersion(data),
		Size:        info.Size(),
	}, nil
}

func (a *App) memoryWrite(req MemoryWriteRequest) (MemoryWriteResult, error) {
	if strings.TrimSpace(req.Description) == "" {
		return MemoryWriteResult{}, errors.New("memory_write requires a non-empty description")
	}
	if strings.TrimSpace(req.Content) == "" {
		return MemoryWriteResult{}, errors.New("memory_write requires non-empty content")
	}
	pathValue := req.Path
	if strings.TrimSpace(pathValue) == "" {
		pathValue = defaultMemoryPath(req.Description)
	}
	path, err := resolveMemoryPath(pathValue)
	if err != nil {
		return MemoryWriteResult{}, err
	}
	before := []byte{}
	created := true
	if existing, _, err := readTextFile(path); err == nil {
		before = existing
		created = false
		if req.Version == "" {
			return MemoryWriteResult{}, fmt.Errorf("memory already exists: %s; pass version from memory_read", filepath.ToSlash(path))
		}
		if err := validateVersion(req.Version); err != nil {
			return MemoryWriteResult{}, err
		}
		currentVersion := hashVersion(existing)
		if !strings.EqualFold(req.Version, currentVersion) {
			return MemoryWriteResult{}, fmt.Errorf("[E_VERSION_MISMATCH] version %s does not match current memory version %s", req.Version, currentVersion)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return MemoryWriteResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return MemoryWriteResult{}, err
	}
	data := []byte(memory.FormatMarkdown(req.Description, req.Content))
	if err := safeWriteFile(path, data, 0o644); err != nil {
		return MemoryWriteResult{}, err
	}
	memoryIndexCache.invalidate()
	return MemoryWriteResult{
		Path:         filepath.ToSlash(path),
		Description:  strings.TrimSpace(req.Description),
		SHA256:       hashBytes(data),
		Version:      hashVersion(data),
		Size:         int64(len(data)),
		Created:      created,
		UpdatedIndex: !bytes.Equal(before, data),
	}, nil
}

// ───────────────────────── Section 6: Read ─────────────────────────
// (was read.go + read_bridge.go)

// Read-range types shared between app.go's read preview helpers and the
// model-facing read tool dispatcher. They live here (not in internal/tools/read)
// because they describe the app-owned bounded-preview result shape used by the
// chat loop, not the pure file-reading algorithm in internal/tools/read.

type readRangeRequest struct {
	StartLine     int
	EndLine       int
	LineCount     int
	ContextBefore int
	ContextAfter  int
}

const (
	maxReadRangeLines          = 10000
	changedLineMaxOutputLines  = 12
	changedLineTextBudgetBytes = 50 * 1024
)

type readPreviewResult struct {
	Content       string
	RawContent    string
	TotalLines    int
	StartLine     int
	EndLine       int
	NextStartLine int
	Truncated     bool
	RangeStatus   string
	EmptyRange    bool
}

func (a *App) BatchReadFiles(req BatchReadRequest) (*BatchReadResult, error) {
	return a.batchReadFilesWithConfig(a.effectiveConfig(ConfigState{}), req)
}

func (a *App) batchReadFilesWithConfig(cfg ConfigState, req BatchReadRequest) (*BatchReadResult, error) {
	pathCount := len(req.Paths) + len(req.Files)
	if strings.TrimSpace(req.Path) != "" {
		pathCount++
	}
	if pathCount == 0 {
		return nil, errors.New("read requires at least one path or file")
	}
	if pathCount > 20 {
		return nil, errors.New("too many files; max 20 per batch")
	}

	type batchReadKey struct {
		Path      string
		StartLine int
		EndLine   int
		Sheet     string
		MaxChars  int
	}

	// Deduplicate only truly identical effective read requests.
	seen := map[batchReadKey]bool{}
	readKey := func(path string, readReq ReadFileRequest) batchReadKey {
		return batchReadKey{
			Path:      filepath.ToSlash(filepath.Clean(path)),
			StartLine: readReq.StartLine,
			EndLine:   readReq.EndLine,
			Sheet:     readReq.Sheet,
			MaxChars:  readReq.MaxChars,
		}
	}
	addIfNotSeen := func(key batchReadKey) bool {
		if seen[key] {
			return false
		}
		seen[key] = true
		return true
	}

	// Collect (path, fileReq) pairs in request order, then execute in
	// parallel. Parallel reads are safe: read is purely read-only,
	// does not touch fileOpsMu, and each file's result is written to its
	// own slot in a pre-allocated results slice — no cross-file sharing.
	// The previous serial loop serialized N file opens + reads; with 20
	// files on a slow disk this was the dominant per-read cost.
	type pendingRead struct {
		path string
		req  ReadFileRequest
	}
	pending := make([]pendingRead, 0, pathCount)
	if strings.TrimSpace(req.Path) != "" {
		fileReq := ReadFileRequest{
			Path:      req.Path,
			StartLine: req.StartLine,
			EndLine:   req.EndLine,
			Sheet:     req.Sheet,
			MaxChars:  req.MaxChars,
		}
		if addIfNotSeen(readKey(req.Path, fileReq)) {
			pending = append(pending, pendingRead{path: req.Path, req: fileReq})
		}
	}
	for _, p := range req.Paths {
		fileReq := ReadFileRequest{
			Path:      p,
			StartLine: req.StartLine,
			EndLine:   req.EndLine,
			Sheet:     req.Sheet,
			MaxChars:  req.MaxChars,
		}
		if addIfNotSeen(readKey(p, fileReq)) {
			pending = append(pending, pendingRead{path: p, req: fileReq})
		}
	}
	for _, file := range req.Files {
		fileReq := ReadFileRequest{
			Path:      file.Path,
			StartLine: file.StartLine,
			EndLine:   file.EndLine,
			Sheet:     file.Sheet,
			MaxChars:  file.MaxChars,
		}
		if fileReq.StartLine == 0 {
			fileReq.StartLine = req.StartLine
		}
		if fileReq.EndLine == 0 {
			fileReq.EndLine = req.EndLine
		}
		if fileReq.Sheet == "" {
			fileReq.Sheet = req.Sheet
		}
		if fileReq.MaxChars == 0 {
			fileReq.MaxChars = req.MaxChars
		}
		if addIfNotSeen(readKey(file.Path, fileReq)) {
			pending = append(pending, pendingRead{path: file.Path, req: fileReq})
		}
	}

	results := make([]BatchReadResultItem, len(pending))
	if len(pending) <= 1 {
		// Fast path: 0 or 1 file — no goroutine overhead.
		for i, p := range pending {
			results[i] = a.batchReadOneWithConfig(cfg, p.path, p.req)
		}
		return &BatchReadResult{Files: results}, nil
	}
	// Parallel path: cap concurrency to 4 (matches the non-file tool batch
	// limit in runChat). 20 files / 4 concurrent ≈ 5 rounds; well below the
	// 30s tool timeout budget even on slow disks. Result slot is written by
	// exactly one goroutine per index, so no mutex is needed.
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, p := range pending {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, path string, fileReq ReadFileRequest) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = a.batchReadOneWithConfig(cfg, path, fileReq)
		}(i, p.path, p.req)
	}
	wg.Wait()
	return &BatchReadResult{Files: results}, nil
}

func batchReadErrorCode(err error) string {
	if code := toolErrorCode(err); code != "" {
		return code
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "E_PATH_NOT_FOUND"
	}
	return ""
}

func (a *App) batchReadOneWithConfig(cfg ConfigState, path string, req ReadFileRequest) BatchReadResultItem {
	result, readErr := a.readFileWithConfig(cfg, req)
	if readErr != nil {
		return BatchReadResultItem{Path: path, Error: readErr.Error(), ErrorCode: batchReadErrorCode(readErr)}
	}
	content := result.RawContent
	contentFormat := "raw"
	if result.Kind == "document" {
		content = result.Content
		contentFormat = "plain"
	}
	return BatchReadResultItem{
		Path:          result.Path,
		Content:       content,
		Text:          result.Text,
		Kind:          result.Kind,
		ContentFormat: contentFormat,
		Type:          result.Type,
		Editable:      result.Editable,
		StartLine:     result.StartLine,
		EndLine:       result.EndLine,
		NextStartLine: result.NextStartLine,
		Version:       result.Version,
		Size:          result.Size,
		TotalLines:    result.TotalLines,
		LineEnding:    result.LineEnding,
		Truncated:     result.Truncated,
		RangeStatus:   result.RangeStatus,
		EmptyRange:    result.EmptyRange,
		Sheets:        result.Sheets,
	}
}

// ── Document Read ────────────────────────────────────────

func (a *App) readDocumentWithConfig(cfg ConfigState, req DocumentReadRequest) (DocumentReadResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return DocumentReadResult{}, errors.New("path is required")
	}
	fullPath, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return DocumentReadResult{}, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return DocumentReadResult{}, err
	}
	if info.IsDir() {
		return DocumentReadResult{}, errors.New("path is a directory")
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 60000
	}
	if maxChars > 200000 {
		maxChars = 200000
	}
	ext := strings.ToLower(filepath.Ext(fullPath))
	var text string
	var sheets []string
	switch ext {
	case ".docx":
		text, err = read.ExtractDocxText(fullPath)
	case ".pptx":
		text, err = read.ExtractPptxText(fullPath)
	case ".xlsx":
		text, sheets, err = read.ExtractXlsxText(fullPath, req.Sheet)
	case ".pdf":
		text, err = read.ExtractPDFTextBestEffort(fullPath)
	case ".txt", ".md", ".json", ".csv", ".log":
		data, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			err = readErr
		} else if !utf8.Valid(data) {
			err = errors.New("file is not valid UTF-8")
		} else {
			text = string(data)
		}
	default:
		return DocumentReadResult{}, fmt.Errorf("unsupported document type: %s", ext)
	}
	if err != nil {
		return DocumentReadResult{}, err
	}
	truncated := false
	if len(text) > maxChars {
		text = text[:maxChars]
		truncated = true
	}
	return DocumentReadResult{
		Path:      displayPathForConfig(cfg, fullPath),
		Type:      strings.TrimPrefix(ext, "."),
		Text:      text,
		Sheets:    sheets,
		Truncated: truncated,
	}, nil
}

// ───────────────────────── Section 7: Scheduler ─────────────────────────
// (was scheduler.go)

// Constants re-exported from the scheduler tool package so existing call sites
// in app/ keep working without referencing the tool package directly. The
// source of truth lives in internal/tools/scheduler.
const (
	defaultScheduledTaskSteps   = scheduler.DefaultSteps
	maxScheduledTaskSteps       = scheduler.MaxSteps
	defaultScheduledTaskTimeout = scheduler.DefaultTimeout
	maxScheduledTaskTimeout     = scheduler.MaxTimeout
	minScheduledTaskInterval    = scheduler.MinInterval
	maxScheduledTasks           = scheduler.MaxTasks
	scheduledTaskSummaryLimit   = scheduler.SummaryLimit
)

type ScheduledTaskSchedule struct {
	Type     string `json:"type"`
	At       string `json:"at,omitempty"`
	Every    string `json:"every,omitempty"`
	Cron     string `json:"cron,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type ScheduledTask struct {
	ID                  string                `json:"id"`
	Name                string                `json:"name"`
	Instruction         string                `json:"instruction"`
	Workspace           string                `json:"workspace"`
	Schedule            ScheduledTaskSchedule `json:"schedule"`
	PermissionMode      string                `json:"permissionMode"`
	MaxSteps            int                   `json:"maxSteps"`
	TimeoutSeconds      int                   `json:"timeoutSeconds"`
	CreatedAt           int64                 `json:"createdAt"`
	UpdatedAt           int64                 `json:"updatedAt"`
	NextRunAt           int64                 `json:"nextRunAt,omitempty"`
	LastRunAt           int64                 `json:"lastRunAt,omitempty"`
	LastStatus          string                `json:"lastStatus"`
	LastSummary         string                `json:"lastSummary,omitempty"`
	LastError           string                `json:"lastError,omitempty"`
	RunCount            int                   `json:"runCount"`
	ConsecutiveFailures int                   `json:"consecutiveFailures"`
	Running             bool                  `json:"running"`
}

type ScheduledTaskToolRequest struct {
	Action      string `json:"action"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
}

type ScheduledTaskToolView struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Workspace      string                `json:"workspace"`
	Schedule       ScheduledTaskSchedule `json:"schedule"`
	PermissionMode string                `json:"permissionMode"`
	MaxSteps       int                   `json:"maxSteps"`
	TimeoutSeconds int                   `json:"timeoutSeconds"`
	NextRunAt      int64                 `json:"nextRunAt,omitempty"`
	LastRunAt      int64                 `json:"lastRunAt,omitempty"`
	LastStatus     string                `json:"lastStatus"`
	RunCount       int                   `json:"runCount"`
	Running        bool                  `json:"running"`
}

type ScheduledTaskToolResult struct {
	Task      *ScheduledTaskToolView  `json:"task,omitempty"`
	Tasks     []ScheduledTaskToolView `json:"tasks,omitempty"`
	Count     int                     `json:"count,omitempty"`
	Truncated bool                    `json:"truncated,omitempty"`
	Deleted   string                  `json:"deleted,omitempty"`
}

type scheduledTaskManager struct {
	app       *App
	events    eventSink
	cron      *cron.Cron
	path      string
	mu        sync.Mutex
	tasks     map[string]*ScheduledTask
	entries   map[string]cron.EntryID
	timers    map[string]*time.Timer
	schedules map[string]cron.Schedule
	cancels   map[string]context.CancelFunc
	runSem    chan struct{}
	stopped   bool
}

func (a *App) startScheduledTaskManager() error {
	a.scheduledMu.Lock()
	defer a.scheduledMu.Unlock()
	if a.scheduled != nil {
		return nil
	}
	if strings.TrimSpace(a.configPath) == "" {
		return errors.New("config path is not initialized")
	}
	manager := &scheduledTaskManager{
		app:       a,
		events:    appEventSink{app: a},
		cron:      cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger))),
		path:      filepath.Join(filepath.Dir(a.configPath), "scheduled_tasks.json"),
		tasks:     map[string]*ScheduledTask{},
		entries:   map[string]cron.EntryID{},
		timers:    map[string]*time.Timer{},
		schedules: map[string]cron.Schedule{},
		cancels:   map[string]context.CancelFunc{},
		runSem:    make(chan struct{}, 1),
	}
	loadErr := manager.load()
	manager.cron.Start()
	a.scheduled = manager
	return loadErr
}

func (a *App) stopScheduledTaskManager() {
	a.scheduledMu.Lock()
	manager := a.scheduled
	a.scheduled = nil
	a.scheduledMu.Unlock()
	if manager != nil {
		manager.stop()
	}
}

func (a *App) scheduledTaskManager() (*scheduledTaskManager, error) {
	a.scheduledMu.Lock()
	manager := a.scheduled
	a.scheduledMu.Unlock()
	if manager == nil {
		return nil, errors.New("scheduled task manager is not initialized")
	}
	return manager, nil
}

func (a *App) ListScheduledTasks() []ScheduledTask {
	manager, err := a.scheduledTaskManager()
	if err != nil {
		return []ScheduledTask{}
	}
	return manager.list()
}

func (a *App) DeleteScheduledTask(id string) error {
	manager, err := a.scheduledTaskManager()
	if err != nil {
		return err
	}
	return manager.delete(id)
}

func (a *App) executeScheduledTaskTool(cfg ConfigState, req ScheduledTaskToolRequest) (any, error) {
	manager, err := a.scheduledTaskManager()
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "create":
		task, err := manager.create(cfg, req)
		if err != nil {
			return nil, err
		}
		view := scheduledTaskToolView(task)
		return ScheduledTaskToolResult{Task: &view}, nil
	case "list":
		tasks := manager.list()
		limit := len(tasks)
		if limit > 50 {
			limit = 50
		}
		views := make([]ScheduledTaskToolView, 0, limit)
		for i := 0; i < limit; i++ {
			views = append(views, scheduledTaskToolView(&tasks[i]))
		}
		return ScheduledTaskToolResult{Tasks: views, Count: len(tasks), Truncated: len(tasks) > limit}, nil
	case "delete":
		id := strings.TrimSpace(req.ID)
		if id == "" {
			return nil, codedToolError("E_SCHEDULED_TASK_ID", errors.New("id is required for delete"))
		}
		if err := manager.delete(id); err != nil {
			return nil, err
		}
		return ScheduledTaskToolResult{Deleted: id}, nil
	default:
		return nil, codedToolError("E_SCHEDULED_TASK_ACTION", errors.New("action must be create, list, or delete"))
	}
}

func (m *scheduledTaskManager) load() error {
	// Scheduled tasks are intentionally process-local. Remove the legacy file
	// on every startup so older persistent definitions cannot restart silently.
	if err := os.Remove(m.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *scheduledTaskManager) stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	for _, timer := range m.timers {
		timer.Stop()
	}
	for _, cancel := range m.cancels {
		cancel()
	}
	m.mu.Unlock()
	ctx := m.cron.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
	_ = os.Remove(m.path)
}

func (m *scheduledTaskManager) create(cfg ConfigState, req ScheduledTaskToolRequest) (*ScheduledTask, error) {
	now := time.Now()
	schedule, err := parseScheduledTaskSchedule(req.Schedule, req.Timezone)
	if err != nil {
		return nil, err
	}
	task := &ScheduledTask{
		ID:             "task_" + newID(),
		Name:           strings.TrimSpace(req.Name),
		Instruction:    strings.TrimSpace(req.Instruction),
		Workspace:      strings.TrimSpace(cfg.Workspace),
		Schedule:       schedule,
		PermissionMode: "workspace_write",
		MaxSteps:       defaultScheduledTaskSteps,
		TimeoutSeconds: defaultScheduledTaskTimeout,
		CreatedAt:      now.UnixMilli(),
		UpdatedAt:      now.UnixMilli(),
		LastStatus:     "scheduled",
	}
	if task.Name == "" {
		return nil, codedToolError("E_SCHEDULED_TASK_NAME", errors.New("name is required"))
	}
	if task.Instruction == "" {
		return nil, codedToolError("E_SCHEDULED_TASK_INSTRUCTION", errors.New("instruction is required"))
	}
	if task.Workspace == "" {
		return nil, codedToolError("E_SCHEDULED_TASK_WORKSPACE", errors.New("workspace is required"))
	}
	root, err := workspaceRoot(cfg)
	if err != nil {
		return nil, err
	}
	task.Workspace = root
	if err := normalizeScheduledTask(task, now); err != nil {
		return nil, err
	}
	if task.Schedule.Type == "once" {
		at, _ := time.Parse(time.RFC3339, task.Schedule.At)
		if !at.After(now) {
			return nil, codedToolError("E_SCHEDULED_TASK_AT", errors.New("one-time schedule must be in the future"))
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil, errors.New("scheduled task manager is stopped")
	}
	if len(m.tasks) >= maxScheduledTasks {
		return nil, codedToolError("E_SCHEDULED_TASK_LIMIT", fmt.Errorf("scheduled task limit reached (%d)", maxScheduledTasks))
	}
	m.tasks[task.ID] = task
	if err := m.registerLocked(task, now); err != nil {
		delete(m.tasks, task.ID)
		return nil, err
	}
	if err := m.persistLocked(); err != nil {
		m.unregisterLocked(task.ID)
		delete(m.tasks, task.ID)
		return nil, err
	}
	copyTask := cloneScheduledTask(task)
	go m.emit("scheduled:update", map[string]any{"task": copyTask})
	return &copyTask, nil
}

// parseScheduledTaskSchedule delegates to the pure scheduler.ParseSchedule
// and converts the tool-local Schedule back to the app-facing type.
func parseScheduledTaskSchedule(value, timezone string) (ScheduledTaskSchedule, error) {
	sched, err := scheduler.ParseSchedule(value, timezone)
	if err != nil {
		return ScheduledTaskSchedule{}, err
	}
	return ScheduledTaskSchedule{
		Type:     sched.Type,
		At:       sched.At,
		Every:    sched.Every,
		Cron:     sched.Cron,
		Timezone: sched.Timezone,
	}, nil
}

func (m *scheduledTaskManager) delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id is required")
	}
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return codedToolError("E_SCHEDULED_TASK_NOT_FOUND", fmt.Errorf("scheduled task not found: %s", id))
	}
	m.unregisterLocked(id)
	if cancel := m.cancels[id]; cancel != nil {
		cancel()
		delete(m.cancels, id)
	}
	delete(m.tasks, id)
	err := m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.emit("scheduled:update", map[string]any{"deleted": id})
	return nil
}

func (m *scheduledTaskManager) list() []ScheduledTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks := make([]ScheduledTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, cloneScheduledTask(task))
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Running != tasks[j].Running {
			return tasks[i].Running
		}
		if tasks[i].NextRunAt == 0 {
			return false
		}
		if tasks[j].NextRunAt == 0 {
			return true
		}
		return tasks[i].NextRunAt < tasks[j].NextRunAt
	})
	return tasks
}

func (m *scheduledTaskManager) registerLocked(task *ScheduledTask, now time.Time) error {
	m.unregisterLocked(task.ID)
	switch task.Schedule.Type {
	case "once":
		at, err := time.Parse(time.RFC3339, task.Schedule.At)
		if err != nil {
			return codedToolError("E_SCHEDULED_TASK_AT", fmt.Errorf("invalid RFC3339 time: %w", err))
		}
		if !at.After(now) {
			task.NextRunAt = 0
			if task.LastRunAt == 0 {
				task.LastStatus = "missed"
				task.LastError = "one-time schedule elapsed while Ally was not running"
			}
			return nil
		}
		task.NextRunAt = at.UnixMilli()
		m.timers[task.ID] = time.AfterFunc(time.Until(at), func() { m.safeTrigger(task.ID) })
		return nil
	case "interval":
		duration, err := time.ParseDuration(task.Schedule.Every)
		if err != nil {
			return codedToolError("E_SCHEDULED_TASK_INTERVAL", fmt.Errorf("invalid interval: %w", err))
		}
		schedule := scheduler.EveryDuration(duration)
		m.schedules[task.ID] = schedule
		m.entries[task.ID] = m.cron.Schedule(schedule, cron.FuncJob(func() { m.safeTrigger(task.ID) }))
		task.NextRunAt = schedule.Next(now).UnixMilli()
		return nil
	case "cron":
		spec, err := scheduler.CronSpecWithTZ(task.Schedule.Cron, task.Schedule.Timezone)
		if err != nil {
			return err
		}
		schedule, err := scheduler.ParseCron(spec)
		if err != nil {
			return codedToolError("E_SCHEDULED_TASK_CRON", fmt.Errorf("invalid cron expression: %w", err))
		}
		m.schedules[task.ID] = schedule
		m.entries[task.ID] = m.cron.Schedule(schedule, cron.FuncJob(func() { m.safeTrigger(task.ID) }))
		task.NextRunAt = schedule.Next(now).UnixMilli()
		return nil
	default:
		return codedToolError("E_SCHEDULED_TASK_TYPE", errors.New("schedule.type must be once, interval, or cron"))
	}
}

func (m *scheduledTaskManager) unregisterLocked(id string) {
	if entryID, ok := m.entries[id]; ok {
		m.cron.Remove(entryID)
		delete(m.entries, id)
	}
	if timer := m.timers[id]; timer != nil {
		timer.Stop()
		delete(m.timers, id)
	}
	delete(m.schedules, id)
}

func (m *scheduledTaskManager) trigger(id string) {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil || m.stopped {
		m.mu.Unlock()
		return
	}
	if task.Running {
		m.advanceNextRunLocked(task, time.Now())
		task.LastStatus = "skipped"
		task.LastError = "previous execution is still running"
		task.UpdatedAt = time.Now().UnixMilli()
		_ = m.persistLocked()
		copyTask := cloneScheduledTask(task)
		m.mu.Unlock()
		m.emit("scheduled:update", map[string]any{"task": copyTask})
		return
	}
	select {
	case m.runSem <- struct{}{}:
	default:
		m.advanceNextRunLocked(task, time.Now())
		task.LastStatus = "skipped"
		task.LastError = "another scheduled task is running"
		task.UpdatedAt = time.Now().UnixMilli()
		_ = m.persistLocked()
		copyTask := cloneScheduledTask(task)
		m.mu.Unlock()
		m.emit("scheduled:update", map[string]any{"task": copyTask})
		return
	}
	now := time.Now()
	task.Running = true
	task.LastRunAt = now.UnixMilli()
	task.LastStatus = "running"
	task.LastError = ""
	task.UpdatedAt = now.UnixMilli()
	if schedule := m.schedules[id]; schedule != nil {
		task.NextRunAt = schedule.Next(now).UnixMilli()
	} else {
		task.NextRunAt = 0
	}
	_ = m.persistLocked()
	copyTask := cloneScheduledTask(task)
	m.mu.Unlock()
	m.emit("scheduled:run_start", map[string]any{"task": copyTask})
	go m.run(copyTask)
}

func (m *scheduledTaskManager) safeTrigger(id string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			m.finish(id, "failed", "", fmt.Sprintf("scheduler panic: %v", recovered))
		}
	}()
	m.trigger(id)
}

func (m *scheduledTaskManager) advanceNextRunLocked(task *ScheduledTask, now time.Time) {
	if schedule := m.schedules[task.ID]; schedule != nil {
		task.NextRunAt = schedule.Next(now).UnixMilli()
	} else {
		task.NextRunAt = 0
	}
}

func (m *scheduledTaskManager) run(task ScheduledTask) {
	defer func() { <-m.runSem }()
	finished := false
	defer func() {
		if recovered := recover(); recovered != nil && !finished {
			m.finish(task.ID, "failed", "", fmt.Sprintf("scheduled task panic: %v", recovered))
		}
	}()

	timeout := time.Duration(task.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(m.app.ctx, timeout)
	defer cancel()
	m.mu.Lock()
	if m.tasks[task.ID] == nil {
		m.mu.Unlock()
		return
	}
	m.cancels[task.ID] = cancel
	m.mu.Unlock()

	cfg := m.app.effectiveConfig(ConfigState{Workspace: task.Workspace})
	cfg.Workspace = task.Workspace
	cfg.grillMode = false

	if err := m.app.acquireSubagentSlot(ctx); err != nil {
		m.finish(task.ID, "failed", "", err.Error())
		finished = true
		return
	}
	result, runErr := m.app.executeDelegate(ctx, cfg, "scheduled:"+task.ID, AgentDelegateRequest{
		Task:         "You are executing a temporary scheduled task in isolated fresh context. It exists only for the current Ally process. Do not create, list, or delete scheduled tasks. Complete the instruction and finish with a concise report for the user.\n\n" + task.Instruction,
		Description:  "Scheduled: " + task.Name,
		CleanContext: false,
		maxSteps:     task.MaxSteps,
		tools:        m.app.scheduledTaskTools(cfg),
	}, cancel)
	m.app.releaseSubagentSlot()
	if result != nil && result.AgentID != "" {
		m.app.subRunsMu.Lock()
		delete(m.app.subRuns, result.AgentID)
		m.app.subRunsMu.Unlock()
	}
	status := "completed"
	summary := ""
	errText := ""
	if result != nil {
		summary = tailString(strings.TrimSpace(result.Summary), scheduledTaskSummaryLimit)
		if result.Status != "" && result.Status != "completed" {
			status = result.Status
		}
		if result.Error != "" {
			errText = result.Error
		}
	}
	if runErr != nil {
		status = "failed"
		errText = runErr.Error()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = "timed_out"
		errText = fmt.Sprintf("execution exceeded %ds", task.TimeoutSeconds)
	} else if errors.Is(ctx.Err(), context.Canceled) && runErr != nil {
		status = "cancelled"
	}
	m.finish(task.ID, status, summary, tailString(errText, 8*1024))
	finished = true
}

func (m *scheduledTaskManager) finish(id, status, summary, errText string) {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		delete(m.cancels, id)
		m.mu.Unlock()
		return
	}
	delete(m.cancels, id)
	task.Running = false
	task.LastStatus = status
	task.LastSummary = summary
	task.LastError = errText
	task.RunCount++
	if status == "completed" {
		task.ConsecutiveFailures = 0
	} else {
		task.ConsecutiveFailures++
	}
	task.UpdatedAt = time.Now().UnixMilli()
	_ = m.persistLocked()
	copyTask := cloneScheduledTask(task)
	m.mu.Unlock()
	event := "scheduled:run_done"
	if status != "completed" {
		event = "scheduled:run_error"
	}
	m.emit(event, map[string]any{"task": copyTask})
}

func (m *scheduledTaskManager) emit(name string, payload map[string]any) {
	if m.events != nil {
		m.events.Emit(name, payload)
	}
}

func (m *scheduledTaskManager) persistLocked() error {
	return nil
}

func normalizeScheduledTask(task *ScheduledTask, now time.Time) error {
	task.ID = strings.TrimSpace(task.ID)
	task.Name = strings.TrimSpace(task.Name)
	task.Instruction = strings.TrimSpace(task.Instruction)
	task.Workspace = strings.TrimSpace(task.Workspace)
	if task.ID == "" {
		return errors.New("task id is required")
	}
	// Scheduled tasks intentionally run with the normal workspace tool set.
	// Force the persisted value so tasks created before this behavior migrate automatically.
	task.PermissionMode = "workspace_write"

	steps, err := scheduler.ValidateSteps(task.MaxSteps)
	if err != nil {
		return err
	}
	task.MaxSteps = steps

	timeout, err := scheduler.ValidateTimeout(task.TimeoutSeconds)
	if err != nil {
		return err
	}
	task.TimeoutSeconds = timeout

	// Normalize and validate the schedule via the pure scheduler package.
	sched := scheduler.Schedule{
		Type:     task.Schedule.Type,
		At:       task.Schedule.At,
		Every:    task.Schedule.Every,
		Cron:     task.Schedule.Cron,
		Timezone: task.Schedule.Timezone,
	}
	scheduler.NormalizeSchedule(&sched)
	if err := scheduler.ValidateSchedule(sched); err != nil {
		return err
	}
	// Write the normalized values back so persistence and downstream code
	// see the trimmed/lowercased form.
	task.Schedule.Type = sched.Type
	task.Schedule.At = sched.At
	task.Schedule.Every = sched.Every
	task.Schedule.Cron = sched.Cron
	task.Schedule.Timezone = sched.Timezone

	if task.CreatedAt == 0 {
		task.CreatedAt = now.UnixMilli()
	}
	if task.UpdatedAt == 0 {
		task.UpdatedAt = task.CreatedAt
	}
	return nil
}

func scheduledTaskToolView(task *ScheduledTask) ScheduledTaskToolView {
	return ScheduledTaskToolView{
		ID: task.ID, Name: task.Name, Workspace: task.Workspace, Schedule: task.Schedule,
		PermissionMode: task.PermissionMode, MaxSteps: task.MaxSteps, TimeoutSeconds: task.TimeoutSeconds,
		NextRunAt: task.NextRunAt, LastRunAt: task.LastRunAt, LastStatus: task.LastStatus,
		RunCount: task.RunCount, Running: task.Running,
	}
}

func cloneScheduledTask(task *ScheduledTask) ScheduledTask {
	if task == nil {
		return ScheduledTask{}
	}
	return *task
}

func (a *App) scheduledTaskTools(cfg ConfigState) []openai.Tool {
	all := a.buildToolsForConfig(cfg)
	filtered := make([]openai.Tool, 0, len(all)-1)
	for _, tool := range all {
		if tool.Function != nil && (tool.Function.Name == "scheduled_task" || tool.Function.Name == "ask") {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

// ───────────────────────── Section 8: Services ─────────────────────────
// (was services.go)

const (
	serviceOutputLimit   = service.OutputLimit
	serviceOutputPreview = service.OutputPreview
	maxActiveServices    = service.MaxActive

	// Tool-facing read defaults. The model can request up to
	// maxServiceReadTailBytes of recent output per call; larger reads are
	// clamped so a single background_process.read cannot dominate the model
	// context window.
	defaultServiceReadTailBytes = service.DefaultReadTail
	maxServiceReadTailBytes     = service.MaxReadTail
)

type managedService struct {
	mu       sync.Mutex
	info     ServiceInfo
	cmd      *exec.Cmd
	output   *rollingBuffer
	cancel   context.CancelFunc
	waitDone chan struct{}
	waitErr  error
}

func (a *App) StartService(req StartServiceRequest) (ServiceInfo, error) {
	return a.startServiceWithConfig(a.effectiveConfig(ConfigState{}), req)
}

func (a *App) StopService(req StopServiceRequest) (ServiceInfo, error) {
	return a.stopService(req)
}

func (a *App) ListServices() ServiceListResult {
	return a.listServices()
}

func (a *App) GetServiceOutput(id string) (ServiceOutputResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ServiceOutputResult{}, errors.New("id is required")
	}
	a.servicesMu.Lock()
	service := a.services[id]
	a.servicesMu.Unlock()
	if service == nil {
		return ServiceOutputResult{}, fmt.Errorf("service not found: %s", id)
	}
	output, total, truncated := service.outputSnapshot()
	return ServiceOutputResult{ID: id, Output: output, Bytes: total, Truncated: truncated}, nil
}

func (a *App) startServiceWithConfig(cfg ConfigState, req StartServiceRequest) (ServiceInfo, error) {
	if strings.TrimSpace(req.Command) == "" {
		return ServiceInfo{}, codedToolError("E_BAD_COMMAND", errors.New("command is required"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return ServiceInfo{}, err
	}
	root := roots[0]
	if err := checkCommandSafety(CommandRequest{Command: req.Command, Cwd: req.Cwd}, roots); err != nil {
		return ServiceInfo{}, err
	}
	cwd := root
	if strings.TrimSpace(req.Cwd) != "" {
		cwd, err = resolveCommandCwd(roots, req.Cwd)
		if err != nil {
			return ServiceInfo{}, err
		}
	}
	a.servicesMu.Lock()
	activeCount := 0
	for _, service := range a.services {
		service.mu.Lock()
		active := service.info.Status == "starting" || service.info.Status == "running"
		service.mu.Unlock()
		if active {
			activeCount++
		}
	}
	a.servicesMu.Unlock()
	if activeCount >= maxActiveServices {
		return ServiceInfo{}, codedToolError("E_SERVICE_LIMIT", fmt.Errorf("active service limit reached (%d)", maxActiveServices))
	}

	id := "svc_" + newID()
	ctx, cancel := context.WithCancel(context.Background())
	shell := commandShell(req.Command, cfg.GitBashPath)
	cmd := exec.CommandContext(ctx, shell.path, shell.args...)
	cmd.Dir = cwd
	cmd.Env = proxyEnvironment(cfg, os.Environ())
	prepareServiceCommand(cmd)

	buf := newRollingBuffer(serviceOutputLimit)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return ServiceInfo{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return ServiceInfo{}, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return ServiceInfo{}, err
	}

	service := &managedService{
		info: ServiceInfo{
			ID:        id,
			Name:      strings.TrimSpace(req.Name),
			Command:   req.Command,
			Cwd:       filepath.ToSlash(cwd),
			PID:       cmd.Process.Pid,
			Status:    "running",
			StartedAt: time.Now().Unix(),
		},
		cmd:      cmd,
		output:   buf,
		cancel:   cancel,
		waitDone: make(chan struct{}),
	}

	a.servicesMu.Lock()
	a.services[id] = service
	a.servicesMu.Unlock()
	a.emitServiceUpdate(service.snapshot())

	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go copyServiceOutput(&copyWG, buf, stdout)
	go copyServiceOutput(&copyWG, buf, stderr)
	go func() {
		waitErr := cmd.Wait()
		copyWG.Wait()
		service.mu.Lock()
		service.waitErr = waitErr
		service.updateOutputInfoLocked()
		if service.info.Status != "stopped" {
			service.info.StoppedAt = time.Now().Unix()
			service.info.Status = "exited"
			if waitErr != nil {
				var exitErr *exec.ExitError
				if errors.As(waitErr, &exitErr) {
					service.info.ExitCode = exitErr.ExitCode()
				} else {
					service.info.Error = waitErr.Error()
				}
			}
		}
		service.mu.Unlock()
		cancel()
		close(service.waitDone)
		a.finalizeService(id, service)
	}()

	// Return immediately. The process runs in the background; the model can
	// poll status and output through background_process.list / read instead
	// of blocking the agent loop on a readiness wait.
	return service.snapshot(), nil
}

func (a *App) finalizeService(id string, service *managedService) {
	info := service.snapshot()
	a.removeService(id, service)
	a.emitServiceUpdate(info)
}

func copyServiceOutput(wg *sync.WaitGroup, dst io.Writer, src io.Reader) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
}

func (a *App) stopService(req StopServiceRequest) (ServiceInfo, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return ServiceInfo{}, errors.New("id is required")
	}
	a.servicesMu.Lock()
	service := a.services[id]
	a.servicesMu.Unlock()
	if service == nil {
		return ServiceInfo{}, fmt.Errorf("service not found: %s", id)
	}

	service.mu.Lock()
	pid := service.info.PID
	alreadyDone := service.info.Status == "stopped" || service.info.Status == "exited"
	service.mu.Unlock()
	if !alreadyDone {
		if err := stopProcessTree(pid); err != nil {
			service.cancel()
			service.mu.Lock()
			service.info.Error = err.Error()
			service.mu.Unlock()
		}
		select {
		case <-service.waitDone:
		case <-time.After(5 * time.Second):
			service.cancel()
		}
		service.mu.Lock()
		service.info.Status = "stopped"
		service.info.StoppedAt = time.Now().Unix()
		service.updateOutputInfoLocked()
		service.mu.Unlock()
	}
	info := service.snapshot()
	a.removeService(id, service)
	a.emitServiceUpdate(info)
	return info, nil
}

func (a *App) listServices() ServiceListResult {
	a.servicesMu.Lock()
	services := make([]*managedService, 0, len(a.services))
	for _, service := range a.services {
		services = append(services, service)
	}
	a.servicesMu.Unlock()
	infos := make([]ServiceInfo, 0, len(services))
	for _, service := range services {
		infos = append(infos, service.snapshot())
	}
	sort.Slice(infos, func(i, j int) bool {
		iActive := infos[i].Status == "starting" || infos[i].Status == "running"
		jActive := infos[j].Status == "starting" || infos[j].Status == "running"
		if iActive != jActive {
			return iActive
		}
		return infos[i].StartedAt > infos[j].StartedAt
	})
	return ServiceListResult{Services: infos}
}

// ServiceListToolResult is the model-facing list payload. It intentionally
// omits outputTail so listing 8 services cannot dominate the model context;
// the model must call background_process.read on a specific id to inspect
// output.
type ServiceListToolResult struct {
	ActiveCount int              `json:"activeCount"`
	MaxActive   int              `json:"maxActive"`
	Services    []ServiceSummary `json:"services"`
}

// ServiceSummary is the per-service metadata returned by the list action. It
// excludes the output tail; only byte accounting is included so the model can
// decide whether a read is worthwhile.
type ServiceSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	Command         string `json:"command"`
	Cwd             string `json:"cwd,omitempty"`
	PID             int    `json:"pid,omitempty"`
	Status          string `json:"status"`
	StartedAt       int64  `json:"startedAt"`
	StoppedAt       int64  `json:"stoppedAt,omitempty"`
	ExitCode        int    `json:"exitCode,omitempty"`
	OutputBytes     int64  `json:"outputBytes,omitempty"`
	OutputTruncated bool   `json:"outputTruncated,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (a *App) listServicesForTool() ServiceListToolResult {
	listed := a.listServices()
	summaries := make([]ServiceSummary, 0, len(listed.Services))
	activeCount := 0
	for _, info := range listed.Services {
		if info.Status == "starting" || info.Status == "running" {
			activeCount++
		}
		summaries = append(summaries, ServiceSummary{
			ID:              info.ID,
			Name:            info.Name,
			Command:         info.Command,
			Cwd:             info.Cwd,
			PID:             info.PID,
			Status:          info.Status,
			StartedAt:       info.StartedAt,
			StoppedAt:       info.StoppedAt,
			ExitCode:        info.ExitCode,
			OutputBytes:     info.OutputBytes,
			OutputTruncated: info.OutputTruncated,
			Error:           info.Error,
		})
	}
	return ServiceListToolResult{
		ActiveCount: activeCount,
		MaxActive:   maxActiveServices,
		Services:    summaries,
	}
}

// ServiceReadResult is the model-facing read payload. Output is bounded by
// maxServiceReadTailBytes so a single read cannot overload the model context.
type ServiceReadResult struct {
	ID            string `json:"id"`
	Output        string `json:"output"`
	ReturnedBytes int    `json:"returnedBytes"`
	BufferBytes   int64  `json:"bufferBytes"`
	TotalBytes    int64  `json:"totalBytes"`
	Truncated     bool   `json:"truncated"`
	Status        string `json:"status"`
	FromByte      int    `json:"fromByte"`
}

func (a *App) readServiceOutput(req ServiceReadRequest) (ServiceReadResult, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return ServiceReadResult{}, codedToolError("E_BAD_SERVICE_ID", errors.New("id is required"))
	}
	a.servicesMu.Lock()
	service := a.services[id]
	a.servicesMu.Unlock()
	if service == nil {
		return ServiceReadResult{}, codedToolError("E_SERVICE_NOT_FOUND", fmt.Errorf("service not found: %s", id))
	}

	tailBytes := req.TailBytes
	if tailBytes <= 0 {
		tailBytes = defaultServiceReadTailBytes
	}
	if tailBytes > maxServiceReadTailBytes {
		tailBytes = maxServiceReadTailBytes
	}

	service.mu.Lock()
	status := service.info.Status
	service.mu.Unlock()
	output, total, truncated := service.outputSnapshot()
	// The rolling buffer drops early bits once full. fromByte reflects where
	// the returned slice starts within the *current* buffer; the model can
	// infer how much older output was already discarded by comparing
	// totalBytes (process-lifetime output) and bufferBytes (current retained).
	bufferBytes := int64(len(output))
	fromByte := 0
	if bufferBytes > int64(tailBytes) {
		fromByte = int(bufferBytes) - tailBytes
	}
	returned := output
	if fromByte > 0 {
		returned = output[fromByte:]
	}
	return ServiceReadResult{
		ID:            id,
		Output:        returned,
		ReturnedBytes: len(returned),
		BufferBytes:   bufferBytes,
		TotalBytes:    total,
		Truncated:     truncated,
		Status:        status,
		FromByte:      fromByte,
	}, nil
}

func (a *App) stopAllServices() {
	for _, service := range a.listServices().Services {
		_, _ = a.stopService(StopServiceRequest{ID: service.ID})
	}
}

func (s *managedService) snapshot() ServiceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := s.info
	s.updateOutputInfoLocked()
	info = s.info
	return info
}

func (s *managedService) updateOutputInfoLocked() {
	if s.output == nil {
		return
	}
	output, total, truncated := s.output.Snapshot()
	s.info.OutputTail = tailString(output, serviceOutputPreview)
	s.info.OutputBytes = total
	s.info.OutputTruncated = truncated
}

func (s *managedService) outputSnapshot() (string, int64, bool) {
	if s == nil || s.output == nil {
		return "", 0, false
	}
	return s.output.Snapshot()
}

func (a *App) emitServiceUpdate(info ServiceInfo) {
	if a.ctx != nil && a.ctx.Err() == nil {
		a.emit("service:update", map[string]any{"service": info})
	}
}

func (a *App) serviceHistoryDir() string {
	a.mu.Lock()
	configPath := a.configPath
	a.mu.Unlock()
	if strings.TrimSpace(configPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "service_history")
}

func (a *App) loadServiceHistory() error {
	dir := a.serviceHistoryDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Completed services are no longer retained. Remove records written by
	// older versions so they do not reappear after upgrading.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".log") {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}

func (a *App) removeService(id string, service *managedService) {
	a.servicesMu.Lock()
	if current := a.services[id]; current == service {
		delete(a.services, id)
	}
	a.servicesMu.Unlock()

	// Keep cleanup idempotent for installations upgraded from the old
	// completed-service retention behavior.
	dir := a.serviceHistoryDir()
	if dir != "" {
		_ = os.Remove(filepath.Join(dir, id+".json"))
		_ = os.Remove(filepath.Join(dir, id+".log"))
	}
}

// ───────────────────────── Section 9: Edit ─────────────────────────
// (was file_edit.go)

type preparedFileEdit struct {
	path    string
	display string
	before  []byte
	after   []byte
	perm    os.FileMode
	result  EditResult
}

func (a *App) editFilesWithConfig(cfg ConfigState, files []FileTextEdits) (MultiEditResult, error) {
	plan, err := planLocalEditBatch(cfg, files, localEditPlanForExecution)
	if err != nil {
		return MultiEditResult{}, err
	}
	prepared := make([]preparedFileEdit, 0, len(plan.Files))
	for i, filePlan := range plan.Files {
		file := filePlan.Edit
		resolved := filePlan.ResolvedPath
		before, info, err := readTextFile(resolved)

		if err != nil {
			return MultiEditResult{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, err)
		}
		beforeVersion := hashVersion(before)
		if !strings.EqualFold(file.Version, beforeVersion) {
			return MultiEditResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("file %s expected version %s, current %s; re-read all affected files before retrying", file.Path, file.Version, beforeVersion))
		}
		text, ending := normalizeText(before)
		applied, replacements, err := edit.ApplyBatchTextChanges(text, toEditChanges(file.Changes))
		if err != nil {
			return MultiEditResult{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, err)
		}
		after := encodeLineEnding(applied.Content, ending)
		beforeLines, _ := splitLines(text)
		afterLines, _ := splitLines(applied.Content)
		diff := edit.GenerateEditDiffPreview(text, applied.Content, maxToolOutput)
		added, removed := 0, 0
		if diff != "" {
			added, removed = edit.CountEditDiffStats(diff, beforeLines, afterLines)
		} else {
			added, removed = edit.ApproximateLineDelta(beforeLines, afterLines)
		}
		classification := "edit"
		if bytes.Equal(after, before) {
			classification = "noop"
		} else if len(after) > len(before) {
			classification = "addition"
		} else if len(after) < len(before) {
			classification = "deletion"
		}
		display := filepath.ToSlash(file.Path)
		prepared = append(prepared, preparedFileEdit{
			path:    resolved,
			display: display,
			before:  before,
			after:   after,
			perm:    info.Mode().Perm(),
			result: EditResult{
				Path:              display,
				BeforeSHA256:      hashBytes(before),
				AfterSHA256:       hashBytes(after),
				BeforeVersion:     beforeVersion,
				Version:           hashVersion(after),
				BeforeBytes:       len(before),
				AfterBytes:        len(after),
				Replacements:      replacements,
				AddedLines:        added,
				RemovedLines:      removed,
				LineEnding:        ending,
				Summary:           fmt.Sprintf("%s updated: %d -> %d bytes", display, len(before), len(after)),
				Diff:              diff,
				FirstChanged:      applied.FirstChangedLine,
				LastChanged:       applied.LastChangedLine,
				Warnings:          applied.Warnings,
				Classification:    classification,
				ChangedLinesBlock: edit.BuildLineNumberContextBlock(applied.Content, applied.FirstChangedLine, applied.LastChangedLine, splitLines),
			},
		})
	}
	committed := make([]int, 0, len(prepared))
	rollback := func() error {
		var rollbackErrors []string
		for i := len(committed) - 1; i >= 0; i-- {
			item := prepared[committed[i]]
			if err := safeWriteFile(item.path, item.before, item.perm); err != nil {
				rollbackErrors = append(rollbackErrors, item.display+": "+err.Error())
			}
		}
		if len(rollbackErrors) > 0 {
			return errors.New(strings.Join(rollbackErrors, "; "))
		}
		return nil
	}
	for i, item := range prepared {
		if bytes.Equal(item.before, item.after) {
			continue
		}
		current, _, err := readTextFile(item.path)
		if err != nil || !strings.EqualFold(hashVersion(current), item.result.BeforeVersion) {
			rollbackErr := rollback()
			msg := fmt.Sprintf("file changed before commit: %s", item.display)
			if rollbackErr != nil {
				msg += "; rollback errors: " + rollbackErr.Error()
			}
			return MultiEditResult{}, codedToolError("E_VERSION_MISMATCH", errors.New(msg))
		}
		if err := safeWriteFile(item.path, item.after, item.perm); err != nil {
			rollbackErr := rollback()
			msg := fmt.Sprintf("failed to commit %s: %v", item.display, err)
			if rollbackErr != nil {
				msg += "; rollback errors: " + rollbackErr.Error()
			}
			return MultiEditResult{}, codedToolError("E_EDIT_COMMIT", errors.New(msg))
		}
		committed = append(committed, i)
	}
	result := MultiEditResult{Files: make([]EditResult, 0, len(prepared)), FileCount: len(prepared)}
	var diffs []string
	for _, item := range prepared {
		result.Files = append(result.Files, item.result)
		result.Replacements += item.result.Replacements
		result.AddedLines += item.result.AddedLines
		result.RemovedLines += item.result.RemovedLines
		for _, warning := range item.result.Warnings {
			result.Warnings = append(result.Warnings, item.display+": "+warning)
		}
		if item.result.Diff != "" {
			diffs = append(diffs, "### "+item.display+"\n"+item.result.Diff)
		}
	}
	result.Summary = fmt.Sprintf("updated %d file(s) with %d replacement(s)", result.FileCount, result.Replacements)
	if result.Replacements == 0 {
		result.Summary = fmt.Sprintf("no content changes needed in %d file(s)", result.FileCount)
	}
	result.Diff = strings.Join(diffs, "\n\n")
	return result, nil
}

func (a *App) editWithConfig(cfg ConfigState, req EditRequest) (EditResult, error) {
	plan, err := normalizeEditRequest(req)
	if err != nil {
		return EditResult{}, err
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return EditResult{}, err
	}
	path, err := safeJoin(roots, req.Path)
	if err != nil {
		return EditResult{}, err
	}
	data, _, err := readTextFile(path)
	if err != nil {
		return EditResult{}, err
	}
	beforeHash := hashBytes(data)
	beforeVersion := hashVersion(data)
	if req.Version != "" {
		if err := validateVersion(req.Version); err != nil {
			return EditResult{}, err
		}
		if !strings.EqualFold(req.Version, beforeVersion) {
			return EditResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("version %s does not match current file version %s. Re-read the file and retry", req.Version, beforeVersion))
		}
	}
	if req.ExpectedSHA256 != "" && req.ExpectedSHA256 != beforeHash {
		return EditResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("expectedSha256 %s does not match current file hash %s. Re-read the file and retry with fresh text", req.ExpectedSHA256, beforeHash))
	}
	text, ending := normalizeText(data)

	var result *edit.Result
	replacements := 0
	switch plan.mode {
	case "lines":
		result, replacements, err = edit.ApplyLineRangeReplacement(text, plan.startLine, plan.endLine, plan.newText, splitLines)
	case "batch_strings":
		result, replacements, err = edit.ApplyBatchTextChanges(text, toEditChanges(plan.changes))
	default:
		result, replacements, err = edit.ApplyStringReplacements(text, toEditOperations(plan.ops))
	}
	if err != nil {
		return EditResult{}, err
	}

	updated := result.Content
	encoded := encodeLineEnding(updated, ending)
	after := encoded
	if text != updated {
		if err := safeWriteFile(path, encoded, modeOf(path)); err != nil {
			return EditResult{}, err
		}
		after, _, err = readTextFile(path)
		if err != nil {
			return EditResult{}, err
		}
	}

	beforeLines, _ := splitLines(text)
	afterLines, _ := splitLines(updated)
	diff := edit.GenerateEditDiffPreview(text, updated, maxToolOutput)
	added := 0
	removed := 0
	if diff != "" {
		added, removed = edit.CountEditDiffStats(diff, beforeLines, afterLines)
	} else {
		added, removed = edit.ApproximateLineDelta(beforeLines, afterLines)
	}
	if text == updated {
		added, removed = 0, 0
	}

	// Classify the edit
	classification := "edit"
	if text == updated {
		classification = "noop"
	} else if len(updated) > len(text) {
		classification = "addition"
	} else if len(updated) < len(text) {
		classification = "deletion"
	}

	changedBlock := edit.BuildLineNumberContextBlock(updated, result.FirstChangedLine, result.LastChangedLine, splitLines)

	return EditResult{
		Path:              filepath.ToSlash(req.Path),
		BeforeSHA256:      beforeHash,
		AfterSHA256:       hashBytes(after),
		BeforeVersion:     beforeVersion,
		Version:           hashVersion(after),
		BeforeBytes:       len(data),
		AfterBytes:        len(after),
		Replacements:      replacements,
		AddedLines:        added,
		RemovedLines:      removed,
		LineEnding:        ending,
		Summary:           fmt.Sprintf("%s updated: %d -> %d bytes", filepath.ToSlash(req.Path), len(data), len(after)),
		Diff:              diff,
		FirstChanged:      result.FirstChangedLine,
		LastChanged:       result.LastChangedLine,
		Warnings:          result.Warnings,
		Classification:    classification,
		ChangedLinesBlock: changedBlock,
	}, nil
}

func validateModelEditToolRequest(files []FileTextEdits) error {
	if len(files) == 0 {
		return codedToolError("E_BAD_EDIT", errors.New("files must contain at least one file edit"))
	}
	if len(files) > 20 {
		return codedToolError("E_BAD_EDIT", errors.New("files supports at most 20 files per call"))
	}
	totalChanges := 0
	for i, file := range files {
		if strings.TrimSpace(file.Path) == "" {
			return codedToolError("E_BAD_EDIT", fmt.Errorf("file %d requires a non-empty path", i+1))
		}
		if err := validateVersion(file.Version); err != nil {
			return fmt.Errorf("file %d: %w", i+1, err)
		}
		if err := edit.ValidateBatchTextChanges(toEditChanges(file.Changes)); err != nil {
			return fmt.Errorf("file %d: %w", i+1, err)
		}
		totalChanges += len(file.Changes)
	}
	if totalChanges > 200 {
		return codedToolError("E_BAD_EDIT", errors.New("edit supports at most 200 total changes per call"))
	}
	return nil
}

func validateVersion(version string) error {
	return read.ValidateVersion(version)
}

func isSHA256Hex(value string) bool {
	return read.IsSHA256Hex(value)
}

func isValidVersion(value string) bool {
	return read.IsValidVersion(value)
}

func validateBatchTextChanges(changes []TextChange) error {
	return edit.ValidateBatchTextChanges(toEditChanges(changes))
}

func normalizeEditRequest(req EditRequest) (editPlan, error) {
	plan, err := edit.NormalizeEditRequest(edit.PlanRequest{
		Path:         req.Path,
		OldString:    req.OldString,
		NewString:    req.NewString,
		ReplaceAll:   req.ReplaceAll,
		StartLine:    req.StartLine,
		EndLine:      req.EndLine,
		NewText:      req.NewText,
		Edits:        toEditOperations(req.Edits),
		BatchChanges: toEditChanges(req.BatchChanges),
	})
	if err != nil {
		return editPlan{}, err
	}
	ep := editPlan{mode: planModeString(plan.Mode), newText: plan.NewText, startLine: plan.StartLine, endLine: plan.EndLine}
	ep.ops = fromEditOperations(plan.Ops)
	ep.changes = fromEditChanges(plan.Changes)
	return ep, nil
}

func planModeString(m edit.PlanMode) string {
	switch m {
	case edit.PlanModeBatchStrings:
		return "batch_strings"
	case edit.PlanModeLines:
		return "lines"
	default:
		return "strings"
	}
}

func toEditChanges(in []TextChange) []edit.TextChange {
	if len(in) == 0 {
		return nil
	}
	out := make([]edit.TextChange, len(in))
	for i, c := range in {
		out[i] = edit.TextChange{OldText: c.OldText, NewText: c.NewText}
	}
	return out
}

func fromEditChanges(in []edit.TextChange) []TextChange {
	if len(in) == 0 {
		return nil
	}
	out := make([]TextChange, len(in))
	for i, c := range in {
		out[i] = TextChange{OldText: c.OldText, NewText: c.NewText}
	}
	return out
}

func toEditOperations(in []EditOperation) []edit.EditOperation {
	if len(in) == 0 {
		return nil
	}
	out := make([]edit.EditOperation, len(in))
	for i, o := range in {
		out[i] = edit.EditOperation{OldString: o.OldString, NewString: o.NewString, ReplaceAll: o.ReplaceAll}
	}
	return out
}

func fromEditOperations(in []edit.EditOperation) []EditOperation {
	if len(in) == 0 {
		return nil
	}
	out := make([]EditOperation, len(in))
	for i, o := range in {
		out[i] = EditOperation{OldString: o.OldString, NewString: o.NewString, ReplaceAll: o.ReplaceAll}
	}
	return out
}

func normalizeEditString(s string) string {
	return edit.NormalizeEditString(s)
}

// ───────────────────── Section 10: Edit batch plan ─────────────────────
// (was file_edit_plan.go)

// localEditPlanMode selects how much validation is required while constructing
// the canonical view of one model-facing edit request. Conflict analysis only
// needs physical targets and must remain conservative even for an invalid
// request; execution additionally enforces the complete edit contract.
type localEditPlanMode uint8

const (
	localEditPlanForConflict localEditPlanMode = iota
	localEditPlanForExecution
)

type localEditFilePlan struct {
	Edit         FileTextEdits
	ResolvedPath string
	Target       fileMutationTarget
}

type localEditBatchPlan struct {
	Files   []localEditFilePlan
	Targets []fileMutationTarget
}

// planLocalEditBatch is the single normalization boundary shared by outer
// tool-batch conflict detection and the local edit executor. Repeated aliases
// of one physical path become one file plan and preserve the first display
// path. All changes remain relative to the same original version snapshot.
func planLocalEditBatch(cfg ConfigState, files []FileTextEdits, mode localEditPlanMode) (localEditBatchPlan, error) {
	if mode == localEditPlanForExecution {
		if err := validateModelEditToolRequest(files); err != nil {
			return localEditBatchPlan{}, err
		}
	}

	roots, err := workspaceRoots(cfg)
	if err != nil {
		return localEditBatchPlan{}, err
	}
	plan := localEditBatchPlan{
		Files:   make([]localEditFilePlan, 0, len(files)),
		Targets: make([]fileMutationTarget, 0, len(files)),
	}
	byTarget := make(map[string]int, len(files))
	for i, file := range files {
		resolved, resolveErr := safeJoin(roots, file.Path)
		target, targetOK := localMutationTargetFromRoots(roots, file.Path)
		if mode == localEditPlanForExecution && resolveErr != nil {
			return localEditBatchPlan{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, resolveErr)
		}
		if !targetOK {
			// An invalid target has no executable identity. Conflict analysis
			// leaves argument/path validation to executeTool, matching the
			// previous behavior while avoiding a guessed mutation key.
			continue
		}
		if existingIndex, exists := byTarget[target.key]; exists {
			existing := &plan.Files[existingIndex]
			if mode == localEditPlanForExecution && !strings.EqualFold(existing.Edit.Version, file.Version) {
				return localEditBatchPlan{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("duplicate edit entries for %s use different versions (%s and %s); re-read the file and submit one version", file.Path, existing.Edit.Version, file.Version))
			}
			existing.Edit.Changes = append(existing.Edit.Changes, file.Changes...)
			continue
		}
		byTarget[target.key] = len(plan.Files)
		plan.Targets = append(plan.Targets, target)
		plan.Files = append(plan.Files, localEditFilePlan{
			Edit: FileTextEdits{
				Path:    file.Path,
				Version: file.Version,
				Changes: append([]TextChange(nil), file.Changes...),
			},
			ResolvedPath: resolved,
			Target:       target,
		})
	}

	if mode == localEditPlanForExecution {
		merged := make([]FileTextEdits, len(plan.Files))
		for i := range plan.Files {
			merged[i] = plan.Files[i].Edit
		}
		if err := validateModelEditToolRequest(merged); err != nil {
			return localEditBatchPlan{}, err
		}
	}
	return plan, nil
}

func localMutationTarget(cfg ConfigState, filePath string) (fileMutationTarget, bool) {
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return fileMutationTarget{}, false
	}
	return localMutationTargetFromRoots(roots, filePath)
}

// localMutationTargetFromRoots creates a conservative physical identity for
// conflict detection. If safeJoin rejects the path, the executor will still
// reject it later; using the cleaned absolute candidate here prevents two
// invalid aliases from bypassing the same-batch write guard.
func localMutationTargetFromRoots(roots []string, filePath string) (fileMutationTarget, bool) {
	if len(roots) == 0 || strings.TrimSpace(filePath) == "" {
		return fileMutationTarget{}, false
	}
	root := roots[0]
	absPath, err := safeJoin(roots, filePath)
	if err != nil {
		if filepath.IsAbs(filePath) {
			absPath = filepath.Clean(filePath)
		} else {
			absPath = filepath.Join(root, filePath)
		}
	}
	absPath, _ = filepath.Abs(absPath)
	absPath = filepath.Clean(absPath)
	keyPath := filepath.ToSlash(absPath)
	if goruntime.GOOS == "windows" {
		keyPath = strings.ToLower(keyPath)
	}
	return fileMutationTarget{"local:" + keyPath, filepath.ToSlash(filePath)}, true
}

// ───────────────────── Section 11: Tool batch policy ─────────────────────
// (was batch_policy.go)

func isOrderedFileMutationTool(name string) bool {
	switch name {
	case "edit", "create_file", "delete_path", "remote_edit", "remote_create_file", "remote_delete_path":
		return true
	default:
		return false
	}
}

func detectWriteBatchConflicts(cfg ConfigState, calls []openai.ToolCall) map[int]error {
	type targetRef struct {
		index   int
		display string
	}
	groups := map[string][]targetRef{}
	for i, call := range calls {
		if !isOrderedFileMutationTool(call.Function.Name) {
			continue
		}
		for _, target := range fileMutationTargets(cfg, call.Function.Name, call.Function.Arguments) {
			groups[target.key] = append(groups[target.key], targetRef{index: i, display: target.display})
		}
	}
	conflicts := map[int]error{}
	for _, refs := range groups {
		if len(refs) < 2 {
			continue
		}
		display := refs[0].display
		err := codedToolError("E_WRITE_BATCH_CONFLICT", fmt.Errorf("multiple file mutations in the same tool batch target %s; no mutation for this path was executed. Send one write, wait for its result, then re-read before the next write", display))
		for _, ref := range refs {
			conflicts[ref.index] = err
		}
	}
	return conflicts
}

func detectToolBatchConflicts(cfg ConfigState, calls []openai.ToolCall) map[int]error {
	conflicts := detectWriteBatchConflicts(cfg, calls)
	if len(calls) <= 1 {
		return conflicts
	}
	barriers := []struct {
		name string
		code string
	}{
		{name: "ask", code: "E_ASK_BATCH_CONFLICT"},
		{name: "wait", code: "E_WAIT_BATCH_CONFLICT"},
	}
	for _, barrier := range barriers {
		found := false
		for _, call := range calls {
			if call.Function.Name == barrier.name {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		err := codedToolError(barrier.code, fmt.Errorf("%s must be the only tool call in its batch; no tool in this batch was executed", barrier.name))
		for i := range calls {
			conflicts[i] = err
		}
		return conflicts
	}
	// Deduplicate calls with semantically identical arguments within the same batch.
	// Models occasionally emit two or more tool calls that mean the same thing but
	// differ in JSON serialization: field order ({"url":"X","method":"GET"} vs
	// {"method":"GET","url":"X"}), whitespace ({"url": "X"} vs {"url":"X"}), or
	// default-value fields ({"url":"X"} vs {"url":"X","method":"GET"} when GET is
	// the default). Running them again wastes resources, races on side effects,
	// and complicates the model's own reasoning. Keep the first occurrence; reject
	// the rest with E_DUPLICATE_TOOL_CALL so the model can see the dedup happened
	// and stop retrying.
	//
	// The dedup key normalizes the arguments by parsing the JSON (when parseable)
	// and reserializing with sorted keys and no extra whitespace. This catches
	// field-order and whitespace differences for free. Default-value normalization
	// is intentionally NOT done here because it would require per-tool knowledge
	// and could mask legitimately different intents; the UI is responsible for
	// making any remaining differences visible.
	seen := map[string]int{}
	for i, call := range calls {
		if _, conflict := conflicts[i]; conflict {
			continue
		}
		key := call.Function.Name + "\x00" + normalizeToolArgsForDedup(call.Function.Arguments)
		first, ok := seen[key]
		if !ok {
			seen[key] = i
			continue
		}
		conflicts[i] = codedToolError("E_DUPLICATE_TOOL_CALL", fmt.Errorf("this tool call is a semantic duplicate of toolCallIndex %d in the same batch (same function and equivalent arguments after JSON normalization) and was skipped; reuse that result instead of re-running the identical call", first))
	}
	return conflicts
}

// normalizeToolArgsForDedup returns a canonical form of a tool-call arguments
// JSON string used only for deduplication. It parses the JSON and reserializes
// it with sorted keys and no extra whitespace, so that field-order differences
// and whitespace differences are treated as identical. If the input is not
// valid JSON, the raw string is returned unchanged so non-JSON or malformed
// arguments still dedup on exact bytes.
func normalizeToolArgsForDedup(args string) string {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return trimmed
	}
	canonical, err := json.Marshal(sortedJSON(parsed))
	if err != nil {
		return trimmed
	}
	return string(canonical)
}

// sortedJSON recursively reorders object keys in a parsed JSON value so that
// json.Marshal produces a stable canonical form. Arrays keep their order, since
// argument arrays are typically order-sensitive (e.g. edit.files, ask.questions).
func sortedJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			out[k] = sortedJSON(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = sortedJSON(item)
		}
		return out
	default:
		return v
	}
}

type fileMutationTarget struct{ key, display string }

func fileMutationTargets(cfg ConfigState, name, arguments string) []fileMutationTarget {
	if name == "edit" {
		var req ModelEditToolRequest
		if json.Unmarshal([]byte(arguments), &req) != nil {
			return nil
		}
		plan, err := planLocalEditBatch(cfg, req.Files, localEditPlanForConflict)
		if err != nil {
			return nil
		}
		return plan.Targets
	}
	if name == "remote_edit" {
		var req RemoteEditRequest
		if json.Unmarshal([]byte(arguments), &req) != nil {
			return nil
		}
		result := make([]fileMutationTarget, 0, len(req.Files))
		for _, file := range req.Files {
			cleanPath := path.Clean(strings.ReplaceAll(strings.TrimSpace(file.Path), "\\", "/"))
			result = append(result, fileMutationTarget{"remote:" + strings.TrimSpace(req.Target) + ":" + cleanPath, strings.TrimSpace(req.Target) + " · " + cleanPath})
		}
		return result
	}
	var args struct {
		Target string `json:"target"`
		Path   string `json:"path"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil || strings.TrimSpace(args.Path) == "" {
		return nil
	}
	if strings.HasPrefix(name, "remote_") {
		target := strings.TrimSpace(args.Target)
		cleanPath := path.Clean(strings.ReplaceAll(strings.TrimSpace(args.Path), "\\", "/"))
		return []fileMutationTarget{{"remote:" + target + ":" + cleanPath, target + " · " + cleanPath}}
	}
	target, ok := localMutationTarget(cfg, args.Path)
	if !ok {
		return nil
	}
	return []fileMutationTarget{target}
}
