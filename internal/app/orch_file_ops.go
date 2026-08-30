// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section: File ops orchestration (was in app.go)
// App-owned create/delete/run-command orchestration plus the shell detection
// helpers and destructive-path safety guards that those operations share.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	goruntime "runtime"

	"ally-dev/internal/tools/pathutil"
)

func (a *App) createFileWithConfig(cfg ConfigState, req CreateFileRequest) (EditResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return EditResult{}, codedToolError("E_BAD_PATH", errors.New("create requires a non-empty path"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return EditResult{}, err
	}
	path, err := resolveWritableFilePath(roots, req.Path)
	if err != nil {
		return EditResult{}, err
	}
	// Record which parent directories MkdirAll actually needs to create so the
	// result can report them without a follow-up list_files. The walk must run
	// before MkdirAll, while the missing ancestors still exist as such.
	createdDirs := newlyCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return EditResult{}, err
	}
	path, err = resolveWritableFilePath(roots, req.Path)
	if err != nil {
		return EditResult{}, err
	}

	before := []byte{}
	beforeHash := ""
	beforeVersion := ""
	perm := os.FileMode(0o644)
	exists := false
	if info, err := os.Lstat(path); err == nil {
		exists = true
		if info.Mode()&os.ModeSymlink != 0 {
			return EditResult{}, codedToolError("E_SYMLINK_PATH", fmt.Errorf("refusing to overwrite symlink target: %s", req.Path))
		}
		if info.IsDir() {
			return EditResult{}, codedToolError("E_TARGET_IS_DIRECTORY", fmt.Errorf("path is a directory: %s", req.Path))
		}
		if !req.Overwrite {
			return EditResult{}, codedToolError("E_EXISTS", fmt.Errorf("file already exists: %s", req.Path))
		}
		before, _, err = readTextFile(path)
		if err != nil {
			return EditResult{}, codedToolError("E_TEXT_OVERWRITE", fmt.Errorf("refusing to overwrite non-text or unreadable file %s: %w", req.Path, err))
		}
		beforeHash, beforeVersion = hashBytesAndVersion(before)
		perm = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return EditResult{}, err
	}

	content, ending, hadBOM := normalizeText([]byte(req.Content))
	encoded := encodeText(content, ending, hadBOM)
	if req.Overwrite {
		if err := safeWriteFileWithDir(path, encoded, perm, false); err != nil {
			return EditResult{}, err
		}
	} else {
		if err := safeWriteNewFile(path, encoded, perm); err != nil {
			return EditResult{}, err
		}
	}
	// The after content is exactly what we just wrote (encoded). Re-reading
	// the file would only repeat the IO and normalization work we already
	// did; for overwrite paths the bytes on disk are byte-identical to
	// encoded because safeWriteFileWithDir writes encoded verbatim, and for
	// new files safeWriteNewFile does the same. Using encoded directly also
	// avoids a second hash pass on the same content.
	result := makeEditResult(req.Path, beforeHash, beforeVersion, before, encoded, ending, 1, string(before), content)
	created := !exists
	result.Created = &created
	if len(createdDirs) > 0 {
		result.CreatedDirs = createdDirsToDisplay(roots, createdDirs)
	}
	if created {
		result.Summary = fmt.Sprintf("%s created: %d bytes", filepath.ToSlash(req.Path), len(encoded))
	}
	return result, nil
}

// createDirectoryWithConfig creates a directory (with any missing parents)
// inside the writable roots. The path boundary checks mirror createFileWithConfig:
// safeJoin confines the target to a root, symlink components are rejected, and
// an existing path at the target fails with E_EXISTS instead of being reused.
func (a *App) createDirectoryWithConfig(cfg ConfigState, req CreateDirectoryRequest) error {
	if strings.TrimSpace(req.Path) == "" {
		return codedToolError("E_BAD_PATH", errors.New("create directory requires a non-empty path"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return err
	}
	path, err := resolveWritableFilePath(roots, req.Path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		// resolveWritableFilePath already rejected symlink targets; anything
		// else that exists collides with the new directory.
		return codedToolError("E_EXISTS", fmt.Errorf("path already exists: %s", req.Path))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

// newlyCreatedDirs walks up from dir until it finds an existing ancestor and
// returns the chain of directories that MkdirAll would create, ordered from the
// outermost to the innermost. Returns nil when dir already exists.
func newlyCreatedDirs(dir string) []string {
	var missing []string
	cur := filepath.Clean(dir)
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
}

// createdDirsToDisplay converts an absolute created-dir chain (outermost
// first) to workspace-relative paths when it lies inside the primary workspace
// (roots[0]), falling back to absolute slash paths for extra-root writes.
func createdDirsToDisplay(roots []string, dirs []string) []string {
	out := make([]string, len(dirs))
	abs := func() {
		for i, d := range dirs {
			out[i] = filepath.ToSlash(d)
		}
	}
	if len(roots) == 0 {
		abs()
		return out
	}
	if rel, err := filepath.Rel(roots[0], dirs[0]); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		abs()
		return out
	}
	for i, d := range dirs {
		if r, err := filepath.Rel(roots[0], d); err == nil {
			out[i] = filepath.ToSlash(r)
		} else {
			out[i] = filepath.ToSlash(d)
		}
	}
	return out
}

func (a *App) deletePathWithConfig(cfg ConfigState, req DeletePathRequest) (DeleteResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return DeleteResult{}, codedToolError("E_BAD_PATH", errors.New("delete requires a non-empty path"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return DeleteResult{}, err
	}
	path, err := resolveDeletablePath(roots, req.Path)
	if err != nil {
		return DeleteResult{}, err
	}
	for _, root := range roots {
		if samePath(path, root) {
			return DeleteResult{}, codedToolError("E_DELETE_BLOCKED", errors.New("refusing to delete workspace root"))
		}
	}

	// Safety: block dangerous delete targets
	if blocked, reason := isDangerousDeletePath(path); blocked {
		return DeleteResult{}, codedToolError("E_DELETE_BLOCKED", fmt.Errorf("%s\n\nThis operation has been blocked for safety. If you really need to delete this path, do it manually outside the agent.", reason))
	}

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DeleteResult{}, codedToolError("E_PATH_NOT_FOUND", err)
		}
		return DeleteResult{}, err
	}
	if info.IsDir() && !req.Recursive {
		return DeleteResult{}, codedToolError("E_DIR_REQUIRES_RECURSIVE", errors.New("path is a directory; set recursive=true"))
	}
	result, err := inspectDeleteTarget(req.Path, path, req.Recursive, info)
	if err != nil {
		return DeleteResult{}, err
	}
	if req.Recursive && info.IsDir() {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		return DeleteResult{}, err
	}
	return result, nil
}

func (a *App) runCommandWithConfig(parent context.Context, cfg ConfigState, req CommandRequest) (CommandResult, error) {
	if strings.TrimSpace(req.Command) == "" {
		return CommandResult{}, codedToolError("E_BAD_COMMAND", errors.New("command is required"))
	}
	if looksLikeLongRunningService(req.Command) {
		return CommandResult{}, longRunningCommandError(req.Command)
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return CommandResult{}, err
	}
	root := roots[0]
	cwd := root
	if strings.TrimSpace(req.Cwd) != "" {
		cwd, err = resolveCommandCwd(roots, req.Cwd)
		if err != nil {
			return CommandResult{}, err
		}
	}
	if err := checkCommandSafetyAtCwd(req, roots, cwd); err != nil {
		return CommandResult{}, err
	}
	// Knowledge-base runs additionally block literal write targets under the
	// read-only sources/ subtree (deny roots ride in on parent ctx).
	if err := checkKBDenyTargets(req.Command, cwd, kbDenyRoots(parent)); err != nil {
		return CommandResult{}, err
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultShellLimit
	}
	if timeout > 600 {
		timeout = 600
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()

	shell := commandShell(req.Command, cfg.GitBashPath)
	cmd := exec.CommandContext(ctx, shell.path, shell.args...)
	cmd.Dir = cwd
	cmd.Env = commandEnvironment(cfg)
	buf := &limitedBuffer{limit: maxToolOutput}
	// 完整输出延迟落盘：仅在内存缓冲首次溢出时创建工作区 .tmp 下的 spill 文件，
	// 把已缓冲前缀写入后继续边跑边写；未截断的小输出全程不碰磁盘。
	tw := &teeWriter{primary: buf, spillDir: filepath.Join(root, ".tmp")}
	buf.onTruncate = tw.startSpill
	cmd.Stdout = tw
	cmd.Stderr = tw
	job := prepareServiceCommand(cmd)
	// ESC 取消运行时，杀掉整棵进程树而不是只杀外壳 bash/powershell，
	// 否则 npm/vite/devserver 等子进程会变成孤儿继续占用端口。
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return stopProcessTree(cmd.Process.Pid)
	}
	started := time.Now()
	outputDone := make(chan struct{})
	var outputWG sync.WaitGroup
	if meta, ok := parent.Value(toolExecutionMetaContextKey{}).(toolExecutionMeta); ok && meta.runID != "" && meta.sessionID != "" {
		outputWG.Add(1)
		go func() {
			defer outputWG.Done()
			ticker := time.NewTicker(120 * time.Millisecond)
			defer ticker.Stop()
			// Track the last emitted length so we can skip the full String()
			// copy when the buffer hasn't grown since the last tick. The
			// previous code called buf.String() (which copies the entire
			// buffered output under the lock) on every 120ms tick even when
			// the command hadn't produced any new output — for a long-running
			// build emitting one line per second, ~99% of ticks were no-ops
			// that still held the write lock during a multi-MB copy.
			lastLen := -1
			emit := func() {
				curLen := buf.Len()
				if curLen == 0 || curLen == lastLen {
					return
				}
				lastLen = curLen
				// During streaming, emit only the tail of the buffer instead
				// of the full content. A long build can fill the 128KB buffer;
				// emitting the full content on every 120ms tick means ~1MB/s
				// of JSON marshal + IPC transmission + frontend re-split, most
				// of which the user cannot read at 8 FPS anyway. The complete
				// output is delivered in the final CommandResult once the
				// command exits. 16KB is enough to show the last ~100 lines
				// of typical build output.
				const streamingTailBytes = 16 * 1024
				tail := decodeConsoleOutput(buf.TailString(streamingTailBytes))
				payload := map[string]any{
					"runId":         meta.runID,
					"sessionId":     meta.sessionID,
					"toolBatchId":   meta.toolBatchID,
					"toolCallIndex": meta.toolCallIndex,
					"toolCallId":    meta.toolCallID,
					"name":          meta.toolName,
					"args":          meta.toolArgs,
					"output":        tail,
					"streaming":     true,
				}
				if curLen > streamingTailBytes {
					payload["outputTruncated"] = true
					payload["outputTotalBytes"] = curLen
				}
				a.emit("tool:update", payload)
			}
			for {
				select {
				case <-ticker.C:
					emit()
				case <-outputDone:
					emit()
					return
				case <-parent.Done():
					return
				}
			}
		}()
	}
	err = cmd.Start()
	if err == nil {
		// Job Object 注册失败（例如进程已被其他 job 接管）时忽略，
		// 取消时回退到 taskkill /T。
		_ = registerProcessJob(cmd.Process.Pid, job)
		err = cmd.Wait()
		// 进程退出后关闭 job handle；若取消时 TerminateJobObject 因
		// 竞态失败导致仍有残留孙进程，KILL_ON_JOB_CLOSE 会兜底杀掉。
		unregisterProcessJob(cmd.Process.Pid)
	} else {
		discardProcessJob(job)
	}
	close(outputDone)
	outputWG.Wait()
	outputFilePath := ""
	var outputFileSize int64
	if f := tw.spill; f != nil {
		name := f.Name()
		_ = f.Close()
		if buf.truncated {
			// On codepage-936 systems the full output is commonly GBK while
			// the model-facing cap keeps UTF-8 in memory; rewrite the spill
			// as UTF-8 so `read` can open it, then stat the final size.
			outputFilePath = name
			transcodeSpillFileForRead(name)
			if info, statErr := os.Stat(name); statErr == nil {
				outputFileSize = info.Size()
			}
		} else {
			_ = os.Remove(name)
		}
	}
	duration := time.Since(started).Milliseconds()
	result := CommandResult{
		Command:         req.Command,
		Cwd:             filepath.ToSlash(cwd),
		Shell:           shell.name,
		ShellPath:       shell.path,
		Output:          decodeConsoleOutput(buf.String()),
		ExitCode:        0,
		TimedOut:        errors.Is(ctx.Err(), context.DeadlineExceeded),
		Cancelled:       errors.Is(ctx.Err(), context.Canceled),
		DurationMS:      duration,
		Truncated:       buf.truncated,
		OutputFilePath:  outputFilePath,
		OutputFileBytes: outputFileSize,
	}
	if outputFilePath != "" {
		result.Output += fmt.Sprintf("\n\n[输出已截断：仅保留前 %d KB。完整输出已保存到 %s（共 %s），可用 read 工具读取该文件查看全部内容；大文件建议分段或按需检索，避免整读]", maxToolOutput/1024, outputFilePath, formatMapFileSize(outputFileSize))
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		if result.TimedOut {
			result.ExitCode = -1
			return result, nil
		}
		return result, err
	}
	return result, nil
}

// teeWriter mirrors command output to primary and, once the buffer first
// overflows, lazily spills the complete output to a file under spillDir so the
// full content survives truncation. Write ordering matters: primary.Write may
// trigger startSpill (under the buffer lock); reading w.spill afterwards is
// ordered by that lock's acquire/release, so the same chunk is never missed or
// duplicated in the file.
type teeWriter struct {
	primary  *limitedBuffer
	spillDir string
	spill    *os.File // nil until the first truncation
}

func (w *teeWriter) Write(p []byte) (int, error) {
	n, err := w.primary.Write(p)
	if w.spill != nil {
		_, _ = w.spill.Write(p)
	}
	return n, err
}

// startSpill is invoked by limitedBuffer exactly once, on first overflow, with
// the buffered prefix. It creates the spill file under spillDir and copies the
// prefix into it; failures degrade gracefully to buffer-only capture.
func (w *teeWriter) startSpill(prefix []byte) {
	if w.spill != nil || w.spillDir == "" {
		return
	}
	if err := os.MkdirAll(w.spillDir, 0o755); err != nil {
		return
	}
	f, err := os.CreateTemp(w.spillDir, "ally-run-*.log")
	if err != nil {
		return
	}
	w.spill = f
	_, _ = f.Write(prefix)
}

// cleanupCommandSpillFiles removes stale command full-output temp files
// older than 24 hours from the workspace .tmp directory (and the legacy
// .ally/tmp location), once at startup, to avoid unbounded accumulation.
func cleanupCommandSpillFiles(workspaceRoot string) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return
	}
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return
	}
	dirs := []string{
		filepath.Join(abs, ".tmp"),
		// Legacy location used before the switch to workspace .tmp; sweep
		// leftovers so they do not linger forever.
		filepath.Join(abs, ".ally", "tmp"),
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "ally-run-") || !strings.HasSuffix(name, ".log") {
				continue
			}
			if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
}

type shellInvocation struct {
	name string
	path string
	args []string
}

// commandShell determines which shell to use for executing a command.
//
// On Windows it prefers Git Bash (bash.exe) so command syntax is unified with
// Linux/macOS. Detection order:
//  1. The gitBashPath setting (manual user override, passed as configuredPath)
//  2. Git for Windows common installation paths
//  3. A Git for Windows installation found through git.exe on PATH
//  4. A Git Bash executable found on PATH
//  5. Fallback to PowerShell (pwsh.exe → powershell.exe), which is always
//     available on Windows (5.1 is built-in, no installation required).
//
// On Linux/macOS it uses bash -c directly and ignores configuredPath.
func commandShell(command, configuredPath string) shellInvocation {
	if goruntime.GOOS == "windows" {
		if bashPath, bashName := findWindowsBash(configuredPath); bashPath != "" {
			return shellInvocation{name: bashName, path: bashPath, args: []string{"-c", command}}
		}
		shell := windowsPowerShell()
		return shellInvocation{
			name: shell.name,
			path: shell.path,
			args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", wrapPowerShellCommand(command)},
		}
	}
	return shellInvocation{name: "bash", path: "bash", args: []string{"-c", command}}
}

// shellBinary holds a resolved shell name and executable path.
type shellBinary struct {
	name string
	path string
}

// findWindowsBash searches for a usable bash.exe on Windows.
// configuredPath is an explicit user override from the gitBashPath setting;
// when set and valid it takes priority. Returns the path and a display name
// ("bash"), or empty strings if no bash was found.
func findWindowsBash(configuredPath string) (string, string) {
	// 1. User-configured path (manual override).
	if p := existingGitBashPath(configuredPath); p != "" {
		return p, "bash"
	}

	// 2. Git for Windows common installation paths.
	gitBashPaths := []string{}
	if progFiles := os.Getenv("ProgramFiles"); progFiles != "" {
		gitBashPaths = append(gitBashPaths,
			filepath.Join(progFiles, "Git", "bin", "bash.exe"),
			filepath.Join(progFiles, "Git", "usr", "bin", "bash.exe"),
		)
	}
	if progFilesX86 := os.Getenv("ProgramFiles(x86)"); progFilesX86 != "" {
		gitBashPaths = append(gitBashPaths,
			filepath.Join(progFilesX86, "Git", "bin", "bash.exe"),
			filepath.Join(progFilesX86, "Git", "usr", "bin", "bash.exe"),
		)
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		gitBashPaths = append(gitBashPaths,
			filepath.Join(localAppData, "Programs", "Git", "bin", "bash.exe"),
			filepath.Join(localAppData, "Programs", "Git", "usr", "bin", "bash.exe"),
		)
	}
	for _, p := range gitBashPaths {
		if p = existingGitBashPath(p); p != "" {
			return p, "bash"
		}
	}

	// 3. Derive Git Bash from git.exe on PATH. This supports portable and
	// non-default Git for Windows installations without accidentally selecting
	// C:\Windows\System32\bash.exe, which is the legacy WSL launcher.
	for _, gitName := range []string{"git.exe", "git"} {
		gitPath, err := exec.LookPath(gitName)
		if err != nil {
			continue
		}
		for _, candidate := range gitBashCandidatesFromGitExecutable(gitPath) {
			if p := existingGitBashPath(candidate); p != "" {
				return p, "bash"
			}
		}
	}

	// 4. bash.exe on PATH, but only when it belongs to a Git for Windows
	// installation. Accepting an arbitrary bash.exe here can select WSL, whose
	// command-line forwarding and Linux PATH semantics break Windows tools and
	// can cause shell input such as $BASH_VERSION or $(...) to be parsed twice.
	if p, err := exec.LookPath("bash.exe"); err == nil {
		if p = existingGitBashPath(p); p != "" {
			return p, "bash"
		}
	}

	return "", ""
}

func gitBashCandidatesFromGitExecutable(gitPath string) []string {
	dir := filepath.Dir(filepath.Clean(strings.TrimSpace(gitPath)))
	base := strings.ToLower(filepath.Base(dir))
	var root string
	switch base {
	case "cmd", "bin":
		root = filepath.Dir(dir)
	default:
		return nil
	}
	return []string{
		filepath.Join(root, "bin", "bash.exe"),
		filepath.Join(root, "usr", "bin", "bash.exe"),
	}
}

func existingGitBashPath(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return ""
	}

	dir := filepath.Dir(filepath.Clean(candidate))
	if !strings.EqualFold(filepath.Base(dir), "bin") {
		return ""
	}
	root := filepath.Dir(dir)
	if strings.EqualFold(filepath.Base(root), "usr") {
		root = filepath.Dir(root)
	}
	for _, gitPath := range []string{
		filepath.Join(root, "cmd", "git.exe"),
		filepath.Join(root, "bin", "git.exe"),
	} {
		if gitInfo, statErr := os.Stat(gitPath); statErr == nil && !gitInfo.IsDir() {
			return candidate
		}
	}
	return ""
}

// windowsPowerShell resolves the best available PowerShell on Windows.
func windowsPowerShell() shellBinary {
	for _, candidate := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
		if p, err := exec.LookPath(candidate); err == nil {
			name := strings.TrimSuffix(strings.ToLower(filepath.Base(p)), ".exe")
			return shellBinary{name: name, path: p}
		}
	}
	return shellBinary{name: "powershell", path: "powershell.exe"}
}

// windowsShellInfo returns the display name and path of the shell that
// commandShell will use on Windows. On non-Windows it returns ("bash", "bash").
func windowsShellInfo(configuredPath string) shellBinary {
	if goruntime.GOOS != "windows" {
		return shellBinary{name: "bash", path: "bash"}
	}
	if bashPath, _ := findWindowsBash(configuredPath); bashPath != "" {
		return shellBinary{name: "bash", path: bashPath}
	}
	return windowsPowerShell()
}

func wrapPowerShellCommand(command string) string {
	return "$ErrorActionPreference = 'Stop'; try { " + command + "; if ($global:LASTEXITCODE -is [int]) { exit $global:LASTEXITCODE } } catch { Write-Error $_; exit 1 }"
}

func inspectDeleteTarget(requestPath, absPath string, recursive bool, info os.FileInfo) (DeleteResult, error) {
	result := DeleteResult{
		Deleted:      filepath.ToSlash(requestPath),
		Path:         filepath.ToSlash(requestPath),
		ResolvedPath: filepath.ToSlash(absPath),
		Kind:         deleteTargetKind(info),
		Recursive:    recursive,
		WasSymlink:   info.Mode()&os.ModeSymlink != 0,
	}
	if info.IsDir() {
		if !recursive {
			result.RemovedDirs = 1
			return result, nil
		}
		err := filepath.WalkDir(absPath, func(_ string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			entryInfo, err := d.Info()
			if err != nil {
				return err
			}
			if d.IsDir() {
				result.RemovedDirs++
			} else {
				result.RemovedFiles++
				result.RemovedBytes += entryInfo.Size()
			}
			return nil
		})
		return result, err
	}
	result.RemovedFiles = 1
	result.RemovedBytes = info.Size()
	return result, nil
}

func deleteTargetKind(info os.FileInfo) string {
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return "symlink"
	}
	if info.IsDir() {
		return "directory"
	}
	if mode.IsRegular() {
		return "file"
	}
	return "other"
}

func makeEditResult(rel string, beforeHash, beforeVersion string, before, after []byte, ending string, replacements int, beforeText, afterText string) EditResult {
	beforeLines, _ := splitLines(beforeText)
	afterLines, _ := splitLines(afterText)
	added := len(afterLines) - len(beforeLines)
	removed := 0
	if added < 0 {
		removed = -added
		added = 0
	}
	afterHash, afterVersion := hashBytesAndVersion(after)
	return EditResult{
		Path:          filepath.ToSlash(rel),
		BeforeSHA256:  beforeHash,
		AfterSHA256:   afterHash,
		BeforeVersion: beforeVersion,
		Version:       afterVersion,
		BeforeBytes:   len(before),
		AfterBytes:    len(after),
		Replacements:  replacements,
		AddedLines:    added,
		RemovedLines:  removed,
		LineEnding:    ending,
		Summary:       fmt.Sprintf("%s updated: %d -> %d bytes", filepath.ToSlash(rel), len(before), len(after)),
	}
}

// ── Safety guards for destructive / expensive operations ──

// 删除保护清单（单一来源）：本地 isOSProtectedDeletePath 的 Linux/macOS
// 分支与远程 Python helper 的 is_protected_delete_path 共用同一份数据，
// 避免 Go/Python 双份清单漂移（远程注入 remoteDeleteProtected* 并集）。
var (
	protectedDeleteLinuxExactOnly  = []string{"/home", "/mnt", "/media"}
	protectedDeleteLinuxTrees      = []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32", "/lost+found", "/opt", "/proc", "/root", "/run", "/sbin", "/snap", "/srv", "/sys", "/usr", "/var"}
	protectedDeleteDarwinExactOnly = []string{"/Users", "/Volumes", "/Network"}
	protectedDeleteDarwinTrees     = []string{"/Applications", "/bin", "/cores", "/dev", "/etc", "/Library", "/opt", "/private", "/sbin", "/System", "/usr", "/var"}
)

// remoteDeleteProtectedExactOnly / remoteDeleteProtectedTrees 是本地
// Linux 与 macOS 保护清单的并集，注入远程 Python helper 使用。
func remoteDeleteProtectedExactOnly() []string {
	return append(append([]string(nil), protectedDeleteLinuxExactOnly...), protectedDeleteDarwinExactOnly...)
}

func remoteDeleteProtectedTrees() []string {
	return append(append([]string(nil), protectedDeleteLinuxTrees...), protectedDeleteDarwinTrees...)
}

// isDangerousDeletePath returns (blocked, reason). Blocks paths that are
// OS-protected locations, home roots, VCS metadata, or workspace root.
func isDangerousDeletePath(absPath string) (bool, string) {
	abs := filepath.Clean(absPath)
	lower := strings.ToLower(abs)

	// 1. VCS metadata — never delete .git or similar
	base := strings.ToLower(filepath.Base(abs))
	if base == ".git" || base == ".svn" || base == ".hg" {
		return true, fmt.Sprintf("refusing to delete VCS directory %q; if you need to remove version control data, do it manually", abs)
	}
	// Also block any path containing /.git/ (not just the .git dir itself)
	if strings.Contains(lower, string(filepath.Separator)+".git"+string(filepath.Separator)) ||
		strings.HasSuffix(lower, string(filepath.Separator)+".git") {
		return true, fmt.Sprintf("path %q contains .git; refusing to protect version control data", abs)
	}

	// Test and build workspaces commonly live below the OS temp directory.
	// Workspace confinement still applies before this guard is reached.
	if tmp := os.TempDir(); tmp != "" && isPathOrDescendant(abs, tmp) {
		return false, ""
	}

	if blocked, reason := isOSProtectedDeletePath(abs); blocked {
		return true, reason
	}

	// Home directory — block outright deletion of any user's home root, but
	// allow ordinary project files under a user's home directory.
	if homeDir, err := os.UserHomeDir(); err == nil {
		if abs == filepath.Clean(homeDir) {
			return true, fmt.Sprintf("refusing to delete home directory %q", abs)
		}
	}
	if allyDir, err := filepath.Abs(appDataDir()); err == nil && samePath(abs, allyDir) {
		return true, fmt.Sprintf("refusing to delete Ally data directory %q", abs)
	}
	// Also check for other users' homes (Unix: /home/*, macOS: /Users/*, Windows: C:\Users\*)
	parent := filepath.Dir(abs)
	parentLower := strings.ToLower(parent)
	if parentLower == "/home" || parentLower == "/users" || parentLower == `c:\users` {
		return true, fmt.Sprintf("refusing to delete user home directory %q", abs)
	}

	return false, ""
}

func isOSProtectedDeletePath(abs string) (bool, string) {
	switch goruntime.GOOS {
	case "windows":
		if isWindowsVolumeRoot(abs) {
			return true, fmt.Sprintf("refusing to delete drive root %q", abs)
		}
		if windowsPathIsTopLevelDir(abs, "users") {
			return true, fmt.Sprintf("refusing to delete Windows protected path %q", abs)
		}
		for _, protected := range []string{
			`windows`,
			`windows.old`,
			`program files`,
			`program files (x86)`,
			`programdata`,
			`recovery`,
			`system volume information`,
			`$recycle.bin`,
			`perflogs`,
			`documents and settings`,
			`config.msi`,
			`$windows.~bt`,
			`$windows.~ws`,
			`$winreagent`,
			`$sysreset`,
			`msocache`,
			`inetpub`,
			`intel`,
			`amd`,
		} {
			if windowsPathHasTopLevelDir(abs, protected) {
				return true, fmt.Sprintf("refusing to delete Windows protected path %q", abs)
			}
		}
	case "darwin":
		if abs == "/" {
			return true, `refusing to delete filesystem root "/"`
		}
		// /Users, /Volumes, /Network are parent roots whose descendants may
		// legitimately contain user projects (e.g. /Users/tangs/projects,
		// /Volumes/ExternalDrive/repos, /Network/servershare/code). Block
		// only the directory itself; individual home roots are handled by
		// the parent-dir check in isDangerousDeletePath, and mounted volume
		// roots are safe to delete into as long as the workspace is confined.
		for _, root := range protectedDeleteDarwinExactOnly {
			if abs == root {
				return true, fmt.Sprintf("refusing to delete macOS top-level root %q", abs)
			}
		}
		for _, protected := range protectedDeleteDarwinTrees {
			if isPathOrDescendant(abs, protected) {
				return true, fmt.Sprintf("refusing to delete macOS protected path %q", abs)
			}
		}
	case "linux":
		if abs == "/" {
			return true, `refusing to delete filesystem root "/"`
		}
		// /home, /mnt, /media are parent roots whose descendants may
		// legitimately contain user projects (e.g. /home/tangs/projects,
		// /mnt/external/repos, /media/user/USB/code). Block only the
		// directory itself; individual home roots are handled by the
		// parent-dir check in isDangerousDeletePath.
		for _, root := range protectedDeleteLinuxExactOnly {
			if abs == root {
				return true, fmt.Sprintf("refusing to delete Linux top-level root %q", abs)
			}
		}
		for _, protected := range protectedDeleteLinuxTrees {
			if isPathOrDescendant(abs, protected) {
				return true, fmt.Sprintf("refusing to delete Linux protected path %q", abs)
			}
		}
	default:
		if abs == string(os.PathSeparator) {
			return true, fmt.Sprintf("refusing to delete filesystem root %q", abs)
		}
	}
	return false, ""
}

func isWindowsVolumeRoot(abs string) bool {
	volume := filepath.VolumeName(abs)
	if volume == "" {
		return false
	}
	rest := strings.TrimPrefix(abs, volume)
	rest = strings.Trim(rest, `\/`)
	return rest == ""
}

func windowsPathHasTopLevelDir(abs string, dir string) bool {
	parts := windowsPathParts(abs)
	return len(parts) > 0 && strings.EqualFold(parts[0], dir)
}

func windowsPathIsTopLevelDir(abs string, dir string) bool {
	parts := windowsPathParts(abs)
	return len(parts) == 1 && strings.EqualFold(parts[0], dir)
}

func windowsPathParts(abs string) []string {
	volume := filepath.VolumeName(abs)
	rest := abs
	if volume != "" {
		rest = strings.TrimPrefix(abs, volume)
	}
	rest = strings.Trim(rest, `\/`)
	if rest == "" {
		return nil
	}
	return strings.FieldsFunc(rest, func(r rune) bool {
		return r == '\\' || r == '/'
	})
}

func isPathOrDescendant(abs, protected string) bool {
	abs = filepath.Clean(abs)
	protected = filepath.Clean(protected)
	if samePath(abs, protected) {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(abs, strings.TrimRight(protected, sep)+sep)
}

// isDangerousSearchRoot returns (blocked, reason). Blocks grep/list operations
// that would traverse system directories, home directories, or other high-risk paths.
func isDangerousSearchRoot(absPath string) (bool, string) {
	abs := filepath.Clean(absPath)
	lower := strings.ToLower(abs)

	if insideAllyAgentDir(abs) {
		return false, ""
	}

	// 1. Root paths — too broad
	if abs == "/" || lower == `c:\` || lower == `c:` {
		return true, fmt.Sprintf("refusing to search from root %q; this would scan the entire filesystem. Specify a project subdirectory instead", abs)
	}

	// Test and temporary workspaces commonly live below /var on macOS.
	if tmp := os.TempDir(); tmp != "" && isPathOrDescendant(abs, tmp) {
		return false, ""
	}

	// 2. Unix/macOS system directories
	unixDangerous := []string{
		"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64",
		"/boot", "/dev", "/proc", "/sys", "/var", "/opt", "/root",
		"/System", "/Library", "/Applications",
	}
	for _, d := range unixDangerous {
		if abs == d || strings.HasPrefix(abs, d+"/") {
			return true, fmt.Sprintf("refusing to search system directory %q; this path is outside the project scope", abs)
		}
	}

	// 3. Windows system directories
	winPrefixes := []string{
		`c:\windows`, `c:\program files`, `c:\program files (x86)`,
	}
	for _, d := range winPrefixes {
		if lower == d || strings.HasPrefix(lower, d+`\`) {
			return true, fmt.Sprintf("refusing to search system directory %q; this path is outside the project scope", abs)
		}
	}

	// 4. Home directories — too broad
	if homeDir, err := os.UserHomeDir(); err == nil {
		cleanHome := filepath.Clean(homeDir)
		if abs == cleanHome {
			return true, fmt.Sprintf("refusing to search from home directory %q; this would scan personal files. Specify a project subdirectory", abs)
		}
	}

	return false, ""
}

// ── Workspace path resolution (thin pathutil wrappers) ───────

func workspaceRoot(cfg ConfigState) (string, error) {
	return pathutil.RootFromConfig(cfg.Workspace)
}

// workspaceRoots 返回主工作区 + 会话级 ExtraRoots 的去重列表。
// 主工作区始终是 roots[0]，且必须存在；ExtraRoots 中不存在或非目录的条目被静默跳过。
// 重复路径（按 OS 风格归一化后）只保留首次出现。
func workspaceRoots(cfg ConfigState) ([]string, error) {
	return pathutil.RootsFromConfig(cfg.Workspace, cfg.ExtraRoots)
}

// insideAnyRoot 判断 target 是否落在任一 root 内（不含 symlink 解析）。
func insideAnyRoot(roots []string, target string) bool {
	return pathutil.InsideAnyRoot(roots, target)
}

func safeJoin(roots []string, p string) (string, error) {
	return pathutil.SafeJoin(pathRuntime, roots, p)
}

func resolveWritableFilePath(roots []string, p string) (string, error) {
	abs, err := safeJoin(roots, p)
	if err != nil {
		return "", codedToolError("E_PATH_OUTSIDE", err)
	}
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", codedToolError("E_SYMLINK_PATH", fmt.Errorf("refusing to write through symlink path: %s", p))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	resolved, err := evalExistingPrefix(abs)
	if err != nil {
		return "", err
	}
	if !insideWriteRoot(roots, resolved) {
		return "", codedToolError("E_PATH_OUTSIDE", fmt.Errorf("path resolves outside workspace or ~/.ally_agent: %s\n允许写入的根目录：%s", p, formatAllowedRoots(roots)))
	}
	return abs, nil
}

func resolveDeletablePath(roots []string, p string) (string, error) {
	abs, err := safeJoin(roots, p)
	if err != nil {
		return "", codedToolError("E_PATH_OUTSIDE", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", codedToolError("E_PATH_NOT_FOUND", err)
		}
		return "", err
	}
	checkPath := abs
	if info.Mode()&os.ModeSymlink != 0 {
		checkPath = filepath.Dir(abs)
	}
	resolved, err := evalExistingPrefix(checkPath)
	if err != nil {
		return "", err
	}
	if !insideWriteRoot(roots, resolved) {
		return "", codedToolError("E_PATH_OUTSIDE", fmt.Errorf("path resolves outside workspace or ~/.ally_agent: %s\n允许写入的根目录：%s", p, formatAllowedRoots(roots)))
	}
	return abs, nil
}

func resolveCommandCwd(roots []string, p string) (string, error) {
	abs, err := safeJoin(roots, p)
	if err != nil {
		return "", codedToolError("E_PATH_OUTSIDE", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", codedToolError("E_CWD_INVALID", err)
		}
		return "", err
	}
	if !insideWriteRoot(roots, resolved) {
		return "", codedToolError("E_PATH_OUTSIDE", fmt.Errorf("cwd resolves outside workspace or ~/.ally_agent: %s\n允许写入的根目录：%s", p, formatAllowedRoots(roots)))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", codedToolError("E_CWD_INVALID", fmt.Errorf("cwd is not a directory: %s", p))
	}
	return filepath.Clean(resolved), nil
}

// formatAllowedRoots 把 roots 列表格式化为换行分隔的字符串，用于错误信息提示。
func formatAllowedRoots(roots []string) string {
	return pathutil.FormatAllowedRoots(roots)
}
