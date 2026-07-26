package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
	for _, entry := range parseGitStatusZ(out) {
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

	entries := parseGitStatusZ(statusOut)
	const maxFiles = 80
	const maxTotalDiffBytes = 512 * 1024
	const maxDiffBytesPerFile = 96 * 1024
	const maxAggregateDiffBytes = maxFiles * maxDiffBytesPerFile
	// Fetch tracked changes in two repository-wide calls. The previous
	// implementation spawned two git processes per file, which is especially
	// expensive on Windows and could approach the request's 10-second timeout.
	unstagedOut, unstagedTruncated, unstagedErr := runGitLimited(ctx, root, maxAggregateDiffBytes, "diff", "--no-ext-diff", "--find-renames", "--find-copies")
	stagedOut, stagedTruncated, stagedErr := runGitLimited(ctx, root, maxAggregateDiffBytes, "diff", "--cached", "--no-ext-diff", "--find-renames", "--find-copies")
	unstagedByPath := splitUnifiedDiffByPath(unstagedOut)
	stagedByPath := splitUnifiedDiffByPath(stagedOut)
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
			file.Diff, file.Truncated, file.Binary, file.Error = synthesizeUntrackedDiff(root, entry.Path, fileLimit)
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
			file.Binary = looksLikeBinaryDiff(file.Diff)
		}
		file.Added, file.Deleted = countUnifiedDiffStats(file.Diff)
		if file.Truncated {
			result.Truncated = true
		}
		totalBytes += len(file.Diff)
		result.Files = append(result.Files, file)
	}

	return result
}

func splitUnifiedDiffByPath(diff string) map[string]string {
	result := map[string]string{}
	if strings.TrimSpace(diff) == "" {
		return result
	}
	starts := []int{}
	for offset := 0; offset < len(diff); {
		idx := strings.Index(diff[offset:], "diff --git ")
		if idx < 0 {
			break
		}
		idx += offset
		if idx == 0 || diff[idx-1] == '\n' {
			starts = append(starts, idx)
		}
		offset = idx + len("diff --git ")
	}
	for i, start := range starts {
		end := len(diff)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		section := strings.TrimRight(diff[start:end], "\n")
		path := unifiedDiffSectionPath(section)
		if path == "" {
			continue
		}
		if existing := result[path]; existing != "" {
			result[path] = existing + "\n" + section
		} else {
			result[path] = section
		}
	}
	return result
}

func unifiedDiffSectionPath(section string) string {
	var oldPath string
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "--- ") {
			oldPath = decodeGitPatchPath(strings.TrimPrefix(line, "--- "))
		}
		if strings.HasPrefix(line, "+++ ") {
			if path := decodeGitPatchPath(strings.TrimPrefix(line, "+++ ")); path != "" {
				return path
			}
		}
	}
	return oldPath
}

func decodeGitPatchPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(value, `"`) {
		if decoded, err := strconv.Unquote(value); err == nil {
			value = decoded
		}
	}
	value = strings.TrimPrefix(value, "a/")
	value = strings.TrimPrefix(value, "b/")
	return filepath.ToSlash(value)
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

type gitStatusEntry struct {
	Path      string
	Status    string
	Untracked bool
}

func parseGitStatusZ(out string) []gitStatusEntry {
	parts := strings.Split(out, "\x00")
	entries := make([]gitStatusEntry, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if len(part) < 4 {
			continue
		}
		x, y := part[0], part[1]
		rel := strings.TrimSpace(part[3:])
		if rel == "" {
			continue
		}
		if x == 'R' || x == 'C' {
			i++ // porcelain -z includes the original path as the next field.
		}
		status := "modified"
		untracked := x == '?' && y == '?'
		switch {
		case untracked:
			status = "untracked"
		case x == 'A' || y == 'A':
			status = "added"
		case x == 'D' || y == 'D':
			status = "deleted"
		case x == 'R' || y == 'R':
			status = "renamed"
		case x == 'C' || y == 'C':
			status = "copied"
		case x == 'M' || y == 'M':
			status = "modified"
		}
		entries = append(entries, gitStatusEntry{Path: rel, Status: status, Untracked: untracked})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return entries
}

func synthesizeUntrackedDiff(root, rel string, limit int) (string, bool, bool, string) {
	fullPath, err := safeJoin([]string{root}, rel)
	if err != nil {
		return "", false, false, err.Error()
	}
	data, _, err := readTextFile(fullPath)
	if err != nil {
		binary := strings.Contains(strings.ToLower(err.Error()), "binary")
		header := fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", rel, rel, rel)
		if binary {
			return header + "Binary file not shown.\n", false, true, ""
		}
		return header + fmt.Sprintf("[diff omitted: %s]\n", err.Error()), false, false, err.Error()
	}
	text, _ := normalizeText(data)
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", rel, rel)
	b.WriteString("new file mode 100644\n")
	b.WriteString("--- /dev/null\n")
	fmt.Fprintf(&b, "+++ b/%s\n", rel)
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	truncated := false
	for _, line := range lines {
		if b.Len()+len(line)+2 > limit {
			truncated = true
			b.WriteString("[diff truncated]\n")
			break
		}
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String(), truncated, false, ""
}

func runGitLimited(ctx context.Context, root string, limit int, args ...string) (string, bool, error) {
	if limit < 1 {
		limit = 1
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	hideCommandWindow(cmd)
	buf := &limitedOutputBuffer{limit: limit}
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return buf.String(), buf.truncated, err
}

type limitedOutputBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedOutputBuffer) Write(p []byte) (int, error) {
	if b.buf.Len() < b.limit {
		remaining := b.limit - b.buf.Len()
		if len(p) <= remaining {
			b.buf.Write(p)
		} else {
			b.buf.Write(p[:remaining])
			b.truncated = true
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedOutputBuffer) String() string {
	out := b.buf.String()
	if b.truncated && !strings.HasSuffix(out, "\n[diff truncated]\n") {
		out = strings.TrimRight(out, "\n") + "\n[diff truncated]\n"
	}
	return out
}

func looksLikeBinaryDiff(diff string) bool {
	return strings.Contains(diff, "Binary files ") || strings.Contains(diff, "GIT binary patch")
}

func countUnifiedDiffStats(diff string) (int, int) {
	added, deleted := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			added++
		} else if strings.HasPrefix(line, "-") {
			deleted++
		}
	}
	return added, deleted
}
