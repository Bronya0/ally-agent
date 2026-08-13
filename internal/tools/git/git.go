// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package git holds pure parsing/analysis helpers for git porcelain and
// unified-diff output. Nothing here may depend on App state, ConfigState, or
// any *App receiver — callers feed in raw git output and receive structured
// results. App-owned orchestration (running git, throttling, cancellation,
// workspace resolution) stays in internal/app.
package git

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// StatusEntry describes one row from `git status --porcelain=v1 -z`.
type StatusEntry struct {
	Path      string
	Status    string
	Untracked bool
}

// ParseStatusZ parses `git status --porcelain=v1 -z` output into entries.
func ParseStatusZ(out string) []StatusEntry {
	parts := strings.Split(out, "\x00")
	entries := make([]StatusEntry, 0, len(parts))
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
		entries = append(entries, StatusEntry{Path: rel, Status: status, Untracked: untracked})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return entries
}

// SplitUnifiedDiffByPath splits a multi-file unified diff into per-path
// sections keyed by the destination path (b/...).
func SplitUnifiedDiffByPath(diff string) map[string]string {
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
			oldPath = DecodePatchPath(strings.TrimPrefix(line, "--- "))
		}
		if strings.HasPrefix(line, "+++ ") {
			if path := DecodePatchPath(strings.TrimPrefix(line, "+++ ")); path != "" {
				return path
			}
		}
	}
	return oldPath
}

// DecodePatchPath decodes a `--- ` / `+++ ` path header, stripping the a/ b/
// prefix and unquoting C-escaped paths.
func DecodePatchPath(value string) string {
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

// LooksLikeBinaryDiff reports whether a diff section looks like a binary diff.
func LooksLikeBinaryDiff(diff string) bool {
	return strings.Contains(diff, "Binary files ") || strings.Contains(diff, "GIT binary patch")
}

// CountUnifiedDiffStats counts added/removed lines in a unified diff, skipping
// the +++/--- headers.
func CountUnifiedDiffStats(diff string) (int, int) {
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

// LimitedBuffer is a bytes.Buffer that stops accepting data after `Limit`
// bytes, setting Truncated to true. It is the bounded stdout/stderr sink used
// when capturing git output so a runaway diff cannot exhaust memory.
type LimitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

// NewLimitedBuffer returns a LimitedBuffer capped at `limit` bytes.
func NewLimitedBuffer(limit int) *LimitedBuffer {
	if limit < 1 {
		limit = 1
	}
	return &LimitedBuffer{limit: limit}
}

// Write implements io.Writer. Bytes beyond the limit are silently dropped and
// Truncated is set to true.
func (b *LimitedBuffer) Write(p []byte) (int, error) {
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

// Truncated reports whether any input was dropped.
func (b *LimitedBuffer) Truncated() bool { return b.truncated }

// String returns the captured text, appending a `[diff truncated]` marker when
// truncation occurred.
func (b *LimitedBuffer) String() string {
	out := b.buf.String()
	if b.truncated && !strings.HasSuffix(out, "\n[diff truncated]\n") {
		out = strings.TrimRight(out, "\n") + "\n[diff truncated]\n"
	}
	return out
}

// SynthesizeUntrackedDiff builds a synthetic `new file` diff for an untracked
// file from its already-read text content. `limit` bounds the emitted diff
// size. Pass an empty `fileText` and a non-nil `readErr` (with `binary` true
// for binary files) to emit a placeholder diff instead.
//
// It returns (diff, truncated, binary, errMsg).
func SynthesizeUntrackedDiff(rel, fileText string, readErr error, binary bool, limit int) (string, bool, bool, string) {
	if readErr != nil {
		header := fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", rel, rel, rel)
		if binary {
			return header + "Binary file not shown.\n", false, true, ""
		}
		return header + fmt.Sprintf("[diff omitted: %s]\n", readErr.Error()), false, false, readErr.Error()
	}
	lines := strings.Split(fileText, "\n")
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
