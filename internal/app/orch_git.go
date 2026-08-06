package app

// Section 3: Git tools (was git_tools.go)
// App-owned git orchestration that binds internal/tools/git porcelain/diff
// algorithms to workspace resolution, the gitDiffMu serialization guard, and
// App-level timeout/cancel state.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ally-dev/internal/tools/git"
)

// gitStatusCacheTTL is how long a cached GetGitStatus result is served
// without re-spawning git. Short enough that file edits made via the agent
// are reflected promptly, long enough to collapse the init → watch → run:done
// burst that would otherwise spawn 3×2 git processes in <1s.
const gitStatusCacheTTL = 2 * time.Second

func (a *App) GetGitStatus() GitStatus {
	workspace, err := workspaceRoot(a.config)
	if err != nil {
		return GitStatus{IsRepo: false}
	}

	// Serve from cache if fresh. Keyed by the resolved workspace path so
	// switching tabs to a different repo doesn't return stale counts.
	a.gitStatusCacheMu.Lock()
	if entry, ok := a.gitStatusCache[workspace]; ok && time.Since(entry.generatedAt) < gitStatusCacheTTL {
		a.gitStatusCacheMu.Unlock()
		return entry.status
	}
	a.gitStatusCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Merge the two rev-parse calls (toplevel + branch) into a single git
	// process. Both are porcelain-free and return one line per arg, so we
	// split on the first newline to separate root from branch.
	revOut, _, err := runGitLimited(ctx, workspace, 64*1024, "rev-parse", "--show-toplevel", "--abbrev-ref", "HEAD")
	if err != nil {
		a.cacheGitStatus(workspace, GitStatus{IsRepo: false})
		return GitStatus{IsRepo: false}
	}
	revLines := strings.SplitN(strings.TrimRight(revOut, "\n"), "\n", 2)
	if len(revLines) < 1 {
		a.cacheGitStatus(workspace, GitStatus{IsRepo: false})
		return GitStatus{IsRepo: false}
	}
	root := strings.TrimSpace(revLines[0])
	if root == "" {
		a.cacheGitStatus(workspace, GitStatus{IsRepo: false})
		return GitStatus{IsRepo: false}
	}
	abs, err := filepath.Abs(filepath.FromSlash(root))
	if err != nil {
		a.cacheGitStatus(workspace, GitStatus{IsRepo: false})
		return GitStatus{IsRepo: false}
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		a.cacheGitStatus(workspace, GitStatus{IsRepo: false})
		return GitStatus{IsRepo: false}
	}
	root = filepath.Clean(abs)
	branch := ""
	if len(revLines) >= 2 {
		branch = strings.TrimSpace(revLines[1])
	}

	out, _, err := runGitLimited(ctx, root, 256*1024, "status", "--porcelain=v1", "-z")
	if err != nil {
		a.cacheGitStatus(workspace, GitStatus{IsRepo: false})
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
	st.Ahead, st.Behind = gitAheadBehind(ctx, root)
	a.cacheGitStatus(workspace, st)
	return st
}

// gitAheadBehind returns the number of local commits not on the upstream
// branch (ahead) and upstream commits not pulled locally (behind). Missing
// upstream, detached HEAD, or an unborn branch yield zero counts, so the UI
// simply shows nothing for them.
func gitAheadBehind(ctx context.Context, root string) (int, int) {
	out, _, err := runGitLimited(ctx, root, 4096, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return 0, 0
	}
	ahead, err1 := strconv.Atoi(fields[0])
	behind, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return 0, 0
	}
	return ahead, behind
}

func (a *App) cacheGitStatus(workspace string, status GitStatus) {
	a.gitStatusCacheMu.Lock()
	a.gitStatusCache[workspace] = gitStatusCacheEntry{status: status, generatedAt: time.Now()}
	a.gitStatusCacheMu.Unlock()
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
