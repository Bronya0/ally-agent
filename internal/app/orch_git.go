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
	a.mu.Lock()
	workspacePath := a.config.Workspace
	a.mu.Unlock()
	return a.getGitStatus(workspacePath)
}

func (a *App) getGitStatus(workspacePath string) GitStatus {
	workspace, err := workspaceRoot(ConfigState{Workspace: workspacePath})
	if err != nil {
		return GitStatus{IsRepo: false}
	}

	for {
		a.gitStatusCacheMu.Lock()
		if entry, ok := a.gitStatusCache[workspace]; ok && time.Since(entry.generatedAt) < gitStatusCacheTTL {
			a.gitStatusCacheMu.Unlock()
			return entry.status
		}
		// Collapse concurrent cold-cache misses onto a single git spawn. The
		// init → workspace watch → run:done burst can otherwise fire several
		// GetGitStatus calls within milliseconds.
		if ch, ok := a.gitStatusInFlight[workspace]; ok {
			a.gitStatusCacheMu.Unlock()
			<-ch
			continue
		}
		ch := make(chan struct{})
		a.gitStatusInFlight[workspace] = ch
		a.gitStatusCacheMu.Unlock()

		status := computeGitStatus(workspace)
		a.cacheGitStatus(workspace, status)

		a.gitStatusCacheMu.Lock()
		delete(a.gitStatusInFlight, workspace)
		close(ch)
		a.gitStatusCacheMu.Unlock()
		return status
	}
}

// computeGitStatus runs a single `git status --porcelain=v2 --branch -z` call,
// which yields the branch name, ahead/behind counts, and per-file status in
// one process. The previous implementation spawned three git processes
// (rev-parse, status, rev-list) on every cache miss.
func computeGitStatus(workspace string) GitStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _, err := runGitLimited(ctx, workspace, 256*1024, "status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return GitStatus{IsRepo: false}
	}
	return parseGitStatusV2(out)
}

// parseGitStatusV2 parses `git status --porcelain=v2 --branch -z` output into a
// GitStatus. It is pure string processing with no App state, so it can be unit
// tested directly. With -z every record (including the `# branch.*` headers) is
// NUL-terminated; renamed/copied entries store the original path as an extra
// NUL-delimited field that must be skipped.
func parseGitStatusV2(out string) GitStatus {
	st := GitStatus{IsRepo: true}
	tokens := strings.Split(out, "\x00")
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if len(token) == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(token, "# branch.head "):
			if branch := strings.TrimSpace(strings.TrimPrefix(token, "# branch.head ")); branch != "" {
				st.Branch = branch
			}
		case strings.HasPrefix(token, "# branch.ab "):
			for _, field := range strings.Fields(strings.TrimPrefix(token, "# branch.ab ")) {
				if len(field) < 2 {
					continue
				}
				n, err := strconv.Atoi(field[1:])
				if err != nil {
					continue
				}
				switch field[0] {
				case '+':
					st.Ahead = n
				case '-':
					st.Behind = n
				}
			}
		case strings.HasPrefix(token, "# "):
			// branch.oid / branch.upstream headers: not needed for the footer.
		case strings.HasPrefix(token, "? "):
			st.Added++
		case strings.HasPrefix(token, "! "):
			// Ignored files are not surfaced in the footer counts.
		case strings.HasPrefix(token, "1 "), strings.HasPrefix(token, "u "):
			applyGitStatusV2XY(&st, token)
		case strings.HasPrefix(token, "2 "):
			applyGitStatusV2XY(&st, token)
			// Skip the original path so it isn't mistaken for a new entry.
			if i+1 < len(tokens) {
				i++
			}
		}
	}
	return st
}

// applyGitStatusV2XY maps a porcelain v2 XY status pair (bytes 2 and 3 of a
// "1 "/"2 "/"u " entry line) to footer counters, mirroring the priority of the
// old v1 parser: added > deleted > renamed/copied > modified.
func applyGitStatusV2XY(st *GitStatus, token string) {
	if len(token) < 4 {
		return
	}
	x, y := token[2], token[3]
	switch {
	case x == 'A' || y == 'A':
		st.Added++
	case x == 'D' || y == 'D':
		st.Deleted++
	case x == 'R' || y == 'R' || x == 'C' || y == 'C':
		st.Modified++
	case x == 'M' || y == 'M':
		st.Modified++
	}
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
	text, _, _ := normalizeText(data)
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
