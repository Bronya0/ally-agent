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

// vcsMetadataDirNames are the version-control metadata directories that no tool
// may create, overwrite, or delete: writing .git/hooks executes code on the
// next git command, and overwriting .git/index or a ref corrupts the
// repository.
var vcsMetadataDirNames = []string{".git", ".svn", ".hg"}

// IsSystemRootPath reports whether path denotes a filesystem root: "/" on
// macOS and Linux, a drive root (C:\) or a UNC share root (\\server\share) on
// Windows. A root used as the workspace makes every boundary check trivially
// pass — the whole disk counts as "inside the workspace" — so the workspace
// picker and the chat run entry reject it up front, on every platform.
//
// The Windows forms are parsed by hand rather than via filepath.VolumeName:
// VolumeName returns "" when compiled off Windows, which would leave the
// Windows branch permanently untestable anywhere else.
func IsSystemRootPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	if IsWindows {
		return isWindowsVolumeRoot(trimmed)
	}
	return filepath.Clean(trimmed) == "/"
}

// isWindowsVolumeRoot matches drive roots (C:\, C:/), UNC share roots
// (\\server\share, //server/share), their long-path spellings (\\?\C:\,
// \\?\UNC\server\share), and a bare separator (the current drive's root).
// Anything one component deeper is not a root.
func isWindowsVolumeRoot(path string) bool {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		// \\?\UNC\server\share 就是 \\server\share 的长路径拼法。
		path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	} else {
		path = strings.TrimPrefix(path, `\\?\`)
	}
	normalized := strings.ReplaceAll(path, `/`, `\`)
	if strings.HasPrefix(normalized, `\\`) {
		// UNC 共享根恰好两段（server\share）；再深一层就不是根。
		parts := strings.Split(strings.Trim(normalized, `\`), `\`)
		return len(parts) == 2
	}
	if len(normalized) >= 2 && normalized[1] == ':' {
		// 盘符根：卷名之后只剩分隔符。“C:”（无分隔符）是盘上当前目录，
		// 选择器给不出这种形态，按盘根保守处理。
		return strings.Trim(normalized[2:], `\`) == ""
	}
	return strings.Trim(normalized, `\`) == ""
}

// CanonicalPath returns the form every path comparison must use. Beyond
// filepath.Clean it strips trailing dots and spaces from each component on
// Windows, where the Win32 path parser ignores them: ".git." and ".git" name
// the same directory, so a guard comparing literal names can be side-stepped by
// appending a dot (filepath.Clean keeps it).
func CanonicalPath(p string) string {
	cleaned := filepath.Clean(p)
	if !IsWindows {
		return cleaned
	}
	volume := filepath.VolumeName(cleaned)
	rest := strings.TrimPrefix(cleaned, volume)
	if rest == "" {
		return cleaned
	}
	leadingSep := rest[0] == '\\' || rest[0] == '/'
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' })
	for i, part := range parts {
		parts[i] = trimWindowsAlias(part)
	}
	joined := strings.Join(parts, `\`)
	if leadingSep {
		joined = `\` + joined
	}
	return volume + joined
}

// trimWindowsAlias drops the trailing dots and spaces Win32 ignores. "." and
// ".." are path syntax rather than names and must survive untouched.
func trimWindowsAlias(name string) string {
	if name == "." || name == ".." {
		return name
	}
	trimmed := strings.TrimRight(name, ". ")
	if trimmed == "" {
		return name
	}
	return trimmed
}

// VCSMetadataReason reports whether p is, or lies inside, version-control
// metadata. It is the single judgement shared by the write-path guards
// (create/edit/editor save/saveTo/move), the delete guard, and the command
// target guard, so a new entry point cannot silently omit it and the delete and
// write sides can not drift apart.
func VCSMetadataReason(p string) (bool, string) {
	canonical := CanonicalPath(p)
	for _, part := range strings.Split(filepath.ToSlash(canonical), "/") {
		for _, name := range vcsMetadataDirNames {
			if strings.EqualFold(part, name) {
				return true, fmt.Sprintf("path %q is inside version control metadata (%s); refusing to create, modify, or delete it", filepath.ToSlash(p), name)
			}
		}
	}
	return false, ""
}

// sensitivePath is one credential store: a file or a directory where private
// keys, tokens or passwords live in the clear.
type sensitivePath struct {
	path   string
	dir    bool
	reason string
}

// sensitiveHomeEntries are the credential stores under the user's home
// directory. The list is deliberately about credentials, not about
// “private-looking” files: every entry holds a key, a token or a password that
// would hand over the user's accounts if it reached a model context (or a web
// page the model was told to fetch).
var sensitiveHomeEntries = []sensitivePath{
	{path: ".ssh", dir: true, reason: "SSH 私钥与主机记录"},
	{path: ".aws", dir: true, reason: "AWS 凭据"},
	{path: ".gnupg", dir: true, reason: "GPG 私钥与信任库"},
	{path: ".kube", dir: true, reason: "Kubernetes 集群凭据"},
	{path: filepath.Join(".docker", "config.json"), reason: "镜像仓库登录令牌"},
	{path: ".netrc", reason: "登录凭据"},
	{path: ".git-credentials", reason: "Git 明文凭据"},
	{path: ".npmrc", reason: "npm 发布令牌"},
	{path: ".pypirc", reason: "PyPI 发布令牌"},
	{path: filepath.Join(".config", "gcloud"), dir: true, reason: "GCP 凭据"},
	{path: filepath.Join(".config", "gh"), dir: true, reason: "GitHub CLI 令牌"},
	{path: filepath.Join(".config", "glab-cli"), dir: true, reason: "GitLab CLI 令牌"},
}

// sensitivePlatformEntries are the credential stores whose location is specific
// to the host: the three platforms name the keychain differently, and missing
// one would leave that platform's keys readable.
func sensitivePlatformEntries() []sensitivePath {
	switch goruntime.GOOS {
	case "darwin":
		return []sensitivePath{{path: filepath.Join("Library", "Keychains"), dir: true, reason: "macOS 钥匙串"}}
	case "linux":
		return []sensitivePath{{path: filepath.Join(".local", "share", "keyrings"), dir: true, reason: "GNOME 钥匙串"}}
	case "windows":
		return []sensitivePath{
			{path: filepath.Join("AppData", "Roaming", "Microsoft", "Credentials"), dir: true, reason: "Windows 凭据管理器"},
			{path: filepath.Join("AppData", "Roaming", "Microsoft", "Crypto"), dir: true, reason: "Windows 密钥容器"},
		}
	}
	return nil
}

// allySecretFileNames are the files in Ally's own data directory that hold
// secrets. The directory is judged file by file on purpose: memories/ and the
// user profile sit in the same directory and are exactly what the model is
// supposed to read.
var allySecretFileNames = []string{"config.json", "api.json", "mcp.json", "ssh_clusters.json"}

// SensitiveReadReason reports whether p is a credential store the model must not
// read, with the reason to show. Callers pass the canonical path and, where they
// can, the symlink-resolved form as well: a link with a clean name must not hide
// a key.
func SensitiveReadReason(rt Runtime, p string) (bool, string) {
	clean := CanonicalPath(p)
	if strings.TrimSpace(clean) == "" {
		return false, ""
	}
	for _, entry := range sensitiveReadPaths(rt) {
		if entry.dir {
			if InsideRoot(entry.path, clean) {
				return true, entry.reason
			}
			continue
		}
		if SamePath(entry.path, clean) {
			return true, entry.reason
		}
	}
	return false, ""
}

// sensitiveReadPaths builds the list for this host. It is computed per call
// rather than cached: the home directory is re-read every time, which is what
// keeps a test (or a user with a different HOME) from inheriting a stale list.
func sensitiveReadPaths(rt Runtime) []sensitivePath {
	paths := make([]sensitivePath, 0, len(sensitiveHomeEntries)+len(allySecretFileNames)+4)
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		entries := append(append([]sensitivePath{}, sensitiveHomeEntries...), sensitivePlatformEntries()...)
		for _, entry := range entries {
			paths = append(paths, sensitivePath{path: filepath.Join(home, entry.path), dir: entry.dir, reason: entry.reason})
		}
	}
	if rt != nil {
		if dir, err := filepath.Abs(rt.AppDataDir()); err == nil && strings.TrimSpace(dir) != "" {
			for _, name := range allySecretFileNames {
				paths = append(paths, sensitivePath{path: filepath.Join(dir, name), reason: "Ally 自己的配置与凭据（模型 API key、SSH 凭据）"})
			}
		}
	}
	return paths
}

// JoinPath normalizes p against the primary workspace root (roots[0]) without
// judging whether the result stays inside any root: absolute paths are accepted
// as-is, relative paths are joined onto roots[0] only. It is the half of
// SafeJoin a caller needs where the OS sandbox owns the boundary — the kernel
// refuses the outside write itself, so a lexical containment error here would
// only pre-empt the authority that does not have to guess.
func JoinPath(roots []string, p string) (string, error) {
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
	return filepath.Clean(abs), nil
}

// SafeJoin joins p onto the primary workspace root (roots[0]) and validates
// that the result is inside one of the roots or ~/.ally_agent. Absolute paths
// are accepted as-is; relative paths are resolved against roots[0] only.
// Returns an error if roots is empty or the resolved path escapes all roots.
// It is JoinPath plus the containment judgement: the two live here so the
// normalization can not drift between the fenced and the kernel-owned host.
func SafeJoin(rt Runtime, roots []string, p string) (string, error) {
	absClean, err := JoinPath(roots, p)
	if err != nil {
		return "", err
	}
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
// always whitelisted as a fallback, and resolved the same way: target arrives
// symlink-resolved, so comparing it against an unresolved ~/.ally_agent refuses
// legitimate writes whenever the path to the data directory contains a symlink
// (a symlinked $HOME, or /var → /private/var on macOS).
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
	if InsideAllyAgentDir(rt, clean) {
		return true
	}
	if rt == nil {
		return false
	}
	dirAbs, err := filepath.Abs(rt.AppDataDir())
	if err != nil {
		return false
	}
	resolvedDir, err := filepath.EvalSymlinks(filepath.Clean(dirAbs))
	if err != nil {
		return false
	}
	resolvedDir = filepath.Clean(resolvedDir)
	// 数据目录被换成指向文件系统根的软链时，兜底白名单不能跟着放大成整个盘。
	if filepath.Dir(resolvedDir) == resolvedDir {
		return false
	}
	return InsideRoot(resolvedDir, clean)
}
