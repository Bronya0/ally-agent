// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package pathutil holds the workspace path-safety helpers shared by every
// tool that resolves, reads, or writes files under the configured workspace.
//
// All functions here are host-neutral pure helpers: they depend only on the
// roots slice (already resolved by the caller), the path string, and the OS.
// The only piece of App state they need is the ~/.ally_agent directory path
// (used as a write whitelist for global config/memories); that is injected
// through the Runtime interface so the package does not import app.
//
// Convention: roots[0] is the primary workspace; the rest are session-level
// ExtraRoots. All comparisons are lexical (no symlink resolution here);
// symlink-aware checks live in the write-path helpers that call EvalSymlinks.
package pathutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	goruntime "runtime"
)

// Runtime is the minimal host capability surface pathutil needs. *App
// satisfies this structurally by returning the absolute path to
// ~/.ally_agent.
type Runtime interface {
	// AppDataDir returns the absolute path to ~/.ally_agent/, used as a
	// write whitelist for global config and memories.
	AppDataDir() string
}

// IsWindows is exposed so tool packages can branch on OS without importing
// the runtime package themselves. It is a const, not a var, so calls can be
// statically resolved.
const IsWindows = goruntime.GOOS == "windows"

// RootFromConfig resolves the primary workspace root from a workspace string
// (the cfg.Workspace field). Returns an error if the workspace is empty,
// missing, or not a directory.
func RootFromConfig(workspace string) (string, error) {
	root := strings.TrimSpace(workspace)
	if root == "" {
		return "", errors.New("workspace is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory: %s", abs)
	}
	return filepath.Clean(abs), nil
}

// RootsFromConfig returns the primary workspace (roots[0], must exist) plus
// the deduplicated, existing-directory ExtraRoots. Non-existent or non-dir
// extra roots are silently skipped. Duplicate paths (OS-normalized) are kept
// only on first appearance.
func RootsFromConfig(workspace string, extraRoots []string) ([]string, error) {
	primary, err := RootFromConfig(workspace)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	roots := make([]string, 0, 1+len(extraRoots))
	markKey := func(clean string) string {
		if IsWindows {
			return strings.ToLower(clean)
		}
		return clean
	}
	addRoot := func(path string) {
		abs, err := filepath.Abs(strings.TrimSpace(path))
		if err != nil {
			return
		}
		clean := filepath.Clean(abs)
		info, err := os.Stat(clean)
		if err != nil {
			return // 不存在的附加目录被跳过
		}
		if !info.IsDir() {
			return
		}
		key := markKey(clean)
		if seen[key] {
			return
		}
		seen[key] = true
		roots = append(roots, clean)
	}
	addRoot(primary)
	for _, extra := range extraRoots {
		if strings.TrimSpace(extra) == "" {
			continue
		}
		addRoot(extra)
	}
	return roots, nil
}

// InsideRoot reports whether target is lexically inside root, after
// filepath.Clean. Windows paths are compared case-insensitively. No symlink
// resolution is performed.
func InsideRoot(root, target string) bool {
	if SamePath(root, target) {
		return true
	}
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if IsWindows {
		root = strings.ToLower(root)
		target = strings.ToLower(target)
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(target, strings.TrimRight(root, sep)+sep)
}

// InsideAnyRoot reports whether target falls under any of the given roots.
func InsideAnyRoot(roots []string, target string) bool {
	for _, root := range roots {
		if InsideRoot(root, target) {
			return true
		}
	}
	return false
}

// InsideAllyAgentDir reports whether target falls under the ~/.ally_agent
// directory. The directory path is obtained from the Runtime interface so
// this package does not import app.
func InsideAllyAgentDir(rt Runtime, target string) bool {
	if rt == nil {
		return false
	}
	dir, err := filepath.Abs(rt.AppDataDir())
	if err != nil {
		return false
	}
	return InsideRoot(filepath.Clean(dir), filepath.Clean(target))
}

// SamePath reports whether two paths refer to the same location, after
// filepath.Clean and OS-appropriate case normalization.
func SamePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if IsWindows {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// SafeJoin joins p onto the primary workspace root (roots[0]) and validates
// that the result is inside one of the roots or ~/.ally_agent. Absolute paths
// are accepted as-is; relative paths are resolved against roots[0] only.
// Returns an error if roots is empty or the resolved path escapes all roots.
func SafeJoin(rt Runtime, roots []string, p string) (string, error) {
	if len(roots) == 0 {
		return "", errors.New("workspace is required")
	}
	primaryAbs, err := filepath.Abs(roots[0])
	if err != nil {
		return "", err
	}
	var target string
	if strings.TrimSpace(p) == "" || p == "." {
		target = primaryAbs
	} else if filepath.IsAbs(p) {
		target = p
	} else {
		target = filepath.Join(primaryAbs, filepath.Clean(p))
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	absClean := filepath.Clean(abs)
	if !InsideAnyRoot(roots, absClean) && !InsideAllyAgentDir(rt, absClean) {
		return "", fmt.Errorf("path is outside workspace or ~/.ally_agent: %s", p)
	}
	return absClean, nil
}

// ResolveReadable resolves a readable path: absolute paths are accepted as-is
// (after Abs/Clean), relative paths are joined under the primary workspace
// root only. Extra roots are intentionally not searched for reads; callers
// that need them should pass an absolute path.
func ResolveReadable(rt Runtime, roots []string, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		return filepath.Clean(abs), nil
	}
	return SafeJoin(rt, roots, p)
}

// FormatAllowedRoots renders the roots list as a newline-separated string for
// user-facing error messages. The first entry is labeled "主工作区",
// subsequent entries "附加工作区".
func FormatAllowedRoots(roots []string) string {
	if len(roots) == 0 {
		return "(无)"
	}
	parts := make([]string, 0, len(roots))
	for i, root := range roots {
		prefix := "  附加工作区"
		if i == 0 {
			prefix = "  主工作区"
		}
		parts = append(parts, prefix+" "+filepath.ToSlash(root))
	}
	return strings.Join(parts, "\n")
}

// InsideWriteRoot reports whether target (already symlink-resolved) falls
// under any writable root. Each root is checked both lexically and after
// EvalSymlinks so symlinked workspace roots are honored. ~/.ally_agent is
// always whitelisted as a fallback.
func InsideWriteRoot(rt Runtime, roots []string, target string) bool {
	clean := filepath.Clean(target)
	for _, root := range roots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rootClean := filepath.Clean(rootAbs)
		if InsideRoot(rootClean, clean) {
			return true
		}
		if resolvedRoot, err := filepath.EvalSymlinks(rootClean); err == nil && InsideRoot(filepath.Clean(resolvedRoot), clean) {
			return true
		}
	}
	return InsideAllyAgentDir(rt, clean)
}
