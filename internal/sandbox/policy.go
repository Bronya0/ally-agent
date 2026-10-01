// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// policyPath is one path the policy treats by kind: bubblewrap can only mount a
// tmpfs over a directory and a read-only bind over a file, and Seatbelt needs a
// literal rule for a single file.
type policyPath struct {
	path string
	dir  bool
}

// policyPlan is the whole decision for one confined launch: what may be
// written, what is re-denied inside that, what is hidden from view, and the
// protections the caller asked for that will not apply. It is computed once, so
// the profile that runs and the diagnostics that explain it cannot drift.
type policyPlan struct {
	writable  []string
	denyWrite []policyPath
	masks     []policyPath
	problems  []string
}

// buildPlan resolves a spec into this platform's policy.
func buildPlan(spec Spec) policyPlan {
	writable, problems := writableDirs(spec, platformWriteExtras...)
	denyWrite, denyProblems := denyWritePaths(spec.DenyWriteRoots, spec.WriteRoots)
	masks, maskProblems := readMasks(spec.ForbidReadRoots, spec.WriteRoots)
	return policyPlan{
		writable:  writable,
		denyWrite: denyWrite,
		masks:     masks,
		problems:  append(append(problems, denyProblems...), maskProblems...),
	}
}

// writableDirs returns every directory a confined command may write to along
// with the writable locations that will not work: the caller's roots, the
// process temp directory, and the per-user toolchain caches builds and package
// managers need. Leaving the caches out is not a hardening win: `go build`,
// `npm ci` and friends then fail with permission errors that look like sandbox
// breakage. platformExtras are appended before resolution (macOS additionally
// allows /dev).
//
// A caller's root that does not exist is reported: a silently dropped write root
// turns into a permission error inside the sandbox with nothing to explain it. A
// cache that does not exist yet is reported too, because EnsureWritableDirs is
// what creates it before a launch — and if that failed, the next build inside
// the sandbox is the thing that breaks.
//
// spec.OnlyWriteRoots stops after the caller's roots, leaving out the temp
// directory, the toolchain caches and the platform extras: a caller that asks
// for it confines one coreutil against one path, and every location besides its
// own roots is a place it has no business writing.
func writableDirs(spec Spec, platformExtras ...string) ([]string, []string) {
	roots := spec.WriteRoots
	var problems []string
	capacity := len(roots) + len(platformExtras) + 8
	seen := make(map[string]bool, capacity)
	out := make([]string, 0, capacity)
	add := func(path string, required bool) {
		resolved, isDir, ok := classifyPath(path)
		if !ok || !isDir {
			if required {
				problems = append(problems, fmt.Sprintf("写根 %s 不存在或不是目录，已跳过：该位置在沙箱内不可写", filepath.ToSlash(strings.TrimSpace(path))))
			}
			return
		}
		if seen[resolved] {
			return
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	for _, root := range roots {
		add(root, true)
	}
	if spec.OnlyWriteRoots {
		return out, problems
	}
	for _, extra := range platformExtras {
		add(extra, false)
	}
	add(os.TempDir(), false)

	home, homeErr := os.UserHomeDir()
	if homeErr != nil || strings.TrimSpace(home) == "" {
		return out, problems
	}
	resolvedHome, homeOK := "", false
	if resolved, isDir, ok := classifyPath(home); ok && isDir {
		resolvedHome, homeOK = resolved, true
	}
	for _, sub := range cacheSubdirs() {
		candidate := filepath.Join(home, sub)
		resolved, isDir, ok := classifyPath(candidate)
		if !ok || !isDir {
			problems = append(problems, fmt.Sprintf("工具链缓存 %s 不存在；沙箱启动前会尝试创建，创建失败会让依赖它的构建命令在沙箱内报权限错误", filepath.ToSlash(candidate)))
			continue
		}
		if homeOK && !isWithin(resolved, resolvedHome, true) {
			// 缓存目录被换成了指向别处的链接：跟着它走等于把那个“别处”加成可写。
			problems = append(problems, fmt.Sprintf("工具链缓存 %s 指向家目录之外（%s），已丢弃：不做为可写根", filepath.ToSlash(candidate), filepath.ToSlash(resolved)))
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out, problems
}

// EnsureWritableDirs creates the per-user toolchain caches the writable surface
// expects to bind. Without them a fresh machine has no ~/.cache for `go build`,
// the missing directory is dropped from the policy, and the build fails with a
// permission error the user reads as a broken sandbox.
//
// It runs in Ally's own process, which is not confined, and only ever creates a
// fixed list of directories under $HOME — it cannot become a way to write where
// the policy would forbid it. Failure is the caller's to ignore: a directory
// that cannot be created simply stays out of the writable surface, and
// writableDirs reports it.
func EnsureWritableDirs() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if strings.TrimSpace(home) == "" {
		return nil
	}
	var failures []error
	for _, sub := range cacheSubdirs() {
		if mkErr := os.MkdirAll(filepath.Join(home, sub), 0o755); mkErr != nil {
			failures = append(failures, fmt.Errorf("create %s: %w", sub, mkErr))
		}
	}
	return errors.Join(failures...)
}

// ensureWritableDirsOnce bounds EnsureWritableDirs to one attempt per process.
var ensureWritableDirsOnce sync.Once

// EnsureWritableDirsOnce creates the toolchain caches once per process. It is
// what Wrap calls before rendering a profile: the caches must exist before the
// profile names them (inside the sandbox the command cannot create them, and
// their parents are not writable), but creating them on every command would
// touch the user's home directory for no reason. The settings path calls it too,
// so flipping confinement on materializes them immediately.
func EnsureWritableDirsOnce() {
	ensureWritableDirsOnce.Do(func() { _ = EnsureWritableDirs() })
}

// cacheSubdirs are the per-user caches a development command is expected to
// write. macOS keeps most of them under Library/Caches, where Go's build cache
// also lives. The list is deliberately short and additive: a missing entry
// shows up as a confusing permission error, never as a silent hole.
func cacheSubdirs() []string {
	subs := []string{".cache", ".cargo", ".npm", "go"}
	if runtime.GOOS == "darwin" {
		subs = append(subs, "Library/Caches")
	}
	return subs
}

// canonicalDirs resolves a path list to absolute, symlink-free directories,
// dropping duplicates, missing paths and non-directories. Callers that need to
// know what was dropped use writableDirs, which reports instead.
func canonicalDirs(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		resolved, isDir, ok := classifyPath(p)
		if !ok || !isDir || seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out
}

// classifyPath resolves one path to the absolute, symlink-free location the
// kernel actually matches. Symlink resolution is not cosmetic: on macOS /tmp is
// /private/tmp and $TMPDIR lives under /var/folders, so an unresolved rule would
// silently match nothing (or, worse, a different file). Missing paths are
// dropped: a mount or rule for a path that does not exist is at best a no-op and
// at worst makes bubblewrap refuse to start.
func classifyPath(path string) (resolved string, isDir bool, ok bool) {
	if strings.TrimSpace(path) == "" {
		return "", false, false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false, false
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", false, false
	}
	return abs, info.IsDir(), true
}

// denyWritePaths returns the re-deny targets that survive, with the requested
// ones that will not apply. Only paths inside a write root are kept (a deny
// outside the writable surface is already denied by the profile), and a child
// of a denied parent is dropped because bubblewrap cannot mount over a
// directory that a parent mask emptied. A target that does not exist is
// reported: it protects nothing, and saying so is the only way a typo surfaces.
func denyWritePaths(deny, writes []string) ([]policyPath, []string) {
	var problems []string
	writeRoots := canonicalDirs(writes)
	candidates := make([]policyPath, 0, len(deny))
	seen := make(map[string]bool, len(deny))
	for _, p := range deny {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			problems = append(problems, "禁止写清单里的空条目已忽略")
			continue
		}
		resolved, isDir, ok := classifyPath(trimmed)
		if !ok {
			problems = append(problems, fmt.Sprintf("禁止写目标 %s 不存在或无法解析，该保护未生效", filepath.ToSlash(trimmed)))
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		if !insideAny(resolved, writeRoots) {
			continue
		}
		candidates = append(candidates, policyPath{path: resolved, dir: isDir})
	}
	return outermostPaths(candidates), problems
}

// readMasks returns the forbid-read targets to mask, with the requested ones
// that will not apply. Write roots always win: a mask that sits inside a write
// root, or that contains one, is dropped — masking the workspace a command may
// write in would turn a permission rule into a broken sandbox — but a dropped
// mask is reported, because the caller believes that path is hidden. Children
// of a masked parent are dropped like in denyWritePaths, so the result does not
// depend on the caller's input order.
func readMasks(forbid, writes []string) ([]policyPath, []string) {
	var problems []string
	writeRoots := canonicalDirs(writes)
	candidates := make([]policyPath, 0, len(forbid))
	seen := make(map[string]bool, len(forbid))
	for _, p := range forbid {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			problems = append(problems, "禁读清单里的空条目已忽略")
			continue
		}
		resolved, isDir, ok := classifyPath(trimmed)
		if !ok {
			problems = append(problems, fmt.Sprintf("禁读目标 %s 不存在或无法解析，该保护未生效", filepath.ToSlash(trimmed)))
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		if insideAny(resolved, writeRoots) || containsAny(resolved, writeRoots) {
			problems = append(problems, fmt.Sprintf("禁读目标 %s 与可写根重叠，已按「可写优先」丢弃：该保护未生效", filepath.ToSlash(resolved)))
			continue
		}
		candidates = append(candidates, policyPath{path: resolved, dir: isDir})
	}
	return outermostPaths(candidates), problems
}

// insideAny reports whether path is one of roots or lives below one.
func insideAny(path string, roots []string) bool {
	for _, root := range roots {
		if isWithin(path, root, true) {
			return true
		}
	}
	return false
}

// containsAny reports whether path is a strict ancestor of one of roots.
func containsAny(path string, roots []string) bool {
	for _, root := range roots {
		if isWithin(root, path, false) {
			return true
		}
	}
	return false
}

// outermostPaths keeps the entries no other entry contains, carrying each one's
// kind along. Inputs are de-duplicated by the callers, so "contains" always
// means strictly above: a child of a kept parent is dropped because the parent
// already covers it, and a mask can only be mounted once. Filtering before
// emitting — instead of while emitting — is what makes the result independent of
// the caller's input order.
func outermostPaths(paths []policyPath) []policyPath {
	out := make([]policyPath, 0, len(paths))
	for i, entry := range paths {
		covered := false
		for j, other := range paths {
			if i != j && isWithin(entry.path, other.path, true) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, entry)
		}
	}
	return out
}

// isWithin reports whether child resolves inside parent. inclusive also accepts
// the path itself. Both inputs are canonical absolute paths, and filepath.Rel
// only walks lexical components, which is all a policy comparison needs.
func isWithin(child, parent string, inclusive bool) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return inclusive
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
