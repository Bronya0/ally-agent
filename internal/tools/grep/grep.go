// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package grep retains ripgrep discovery, shared traversal policies, and
// result types needed to display historical searches. Content search runs
// through the command tool.
package grep

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	toolerrors "ally-dev/internal/tools/shared"
)

// LineFileMatch groups matching lines for one file. The path is emitted
// once; Lines lists the 1-based line numbers of matching lines in ascending
// order, and Texts carries the sanitized text of each matching line in
// parallel (trimmed of surrounding whitespace and capped at
// the historical preview limit).
type LineFileMatch struct {
	Path  string   `json:"path"`
	Lines []int    `json:"lines"`
	Texts []string `json:"texts,omitempty"`
}

// FileCount is one entry in the per-file count list returned as
// Result.FileCounts in count_matches mode.
type FileCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// Result is the historical grep tool result. Mode lines returns matching lines (number
// plus capped text preview) grouped by file. Mode count_matches returns
// per-file counts. MatchedLines/Hits/Files stay exact across all modes.
// NextOffset pages the active mode's result entries.
type Result struct {
	Mode         string          `json:"mode"`
	LineHits     []LineFileMatch `json:"matches,omitempty"`
	FileCounts   []FileCount     `json:"fileCounts,omitempty"`
	MatchedLines int             `json:"matchedLines"`
	Hits         int             `json:"hits"`
	Files        int             `json:"files"`
	Truncated    bool            `json:"truncated"`
	NextOffset   int             `json:"nextOffset,omitempty"`
	// Offset is the page's starting position, needed if the model view trims a
	// final page (whose NextOffset is zero) and must offer a continuation.
	Offset int `json:"offset,omitempty"`
	// OffsetExhausted reports that the requested offset skipped past the end of the
	// match stream: matches is empty even though Hits > 0. Reset offset to 0
	// to page from the beginning.
	OffsetExhausted bool `json:"offsetExhausted,omitempty"`
	// Skipped describes workspace-wide search policies that may hide content.
	// Explicit path searches do not apply these broad exclusions.
	Skipped []string `json:"skipped,omitempty"`
	// Warnings carries non-fatal issues encountered during search, such as
	// individual files that rg could not open (Windows reserved names, etc.).
	// Results are still returned; only the bad files were skipped.
	Warnings []string `json:"warnings,omitempty"`
}

// Find locates the ripgrep binary. It checks the ALLY_RG_PATH env variable,
// the directory next to the running executable, common macOS homebrew paths,
// and finally PATH.
func Find() (string, error) {
	return FindForOS(runtime.GOOS)
}

// FindForOS is Find with an explicit GOOS for testability.
func FindForOS(goos string) (string, error) {
	for _, candidate := range CandidatesForOS(goos) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return exec.LookPath("rg")
}

// CandidatesForOS returns the candidate ripgrep paths for the given OS.
func CandidatesForOS(goos string) []string {
	candidates := []string{}
	if p := strings.TrimSpace(os.Getenv("ALLY_RG_PATH")); p != "" {
		candidates = append(candidates, p)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		names := []string{"rg"}
		if goos == "windows" {
			names = []string{"rg.exe", "rg"}
		}
		for _, name := range names {
			candidates = append(candidates, filepath.Join(dir, "tools", name), filepath.Join(dir, name))
		}
	}
	if goos == "darwin" {
		candidates = append(candidates,
			"/opt/homebrew/bin/rg",
			"/usr/local/bin/rg",
		)
	}
	return candidates
}

// MissingError returns the coded error reported when ripgrep is not found.
func MissingError() error {
	return toolerrors.New("E_RIPGREP_NOT_FOUND", fmt.Errorf("ripgrep (`rg`) was not found in PATH or the Ally tools directory.\n\nInstall ripgrep and restart Ally:\n%s", strings.Join(InstallInstructions(), "\n")))
}

// InstallInstructions returns the per-OS install instructions shown when
// ripgrep is missing.
func InstallInstructions() []string {
	return []string{
		"Windows: winget install BurntSushi.ripgrep  (or: scoop install ripgrep / choco install ripgrep)",
		"macOS: brew install ripgrep",
		"Debian/Ubuntu: sudo apt install ripgrep",
		"Fedora: sudo dnf install ripgrep",
		"Arch: sudo pacman -S ripgrep",
		"openSUSE: sudo zypper install ripgrep",
		"Alpine: sudo apk add ripgrep",
		"Rust/Cargo: cargo install ripgrep",
	}
}

func DisplayPathForRoot(root, p string) string {
	if p == "" {
		return ""
	}
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		clean = filepath.Join(root, clean)
	}
	rel, err := filepath.Rel(root, clean)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return filepath.ToSlash(clean)
	}
	return filepath.ToSlash(rel)
}

func ExcludedDirs() []string {
	return []string{
		"node_modules",
		".pnpm-store",
		"jars",
		"vendor",
		"dist",
		"target",
		"bower_components",
		"site-packages",
		"Pods",
		"Carthage",
		"_build",
		"lib64",
		".gradle",
		".next",
		".nuxt",
		".svelte-kit",
		"__pycache__",
		".venv",
		"venv",
		".cache",
		"coverage",
		".pytest_cache",
		".mypy_cache",
		".ruff_cache",
		".turbo",
		".parcel-cache",
		".vite",
	}
}

// excludedDirNameSet 是 ExcludedDirs 的小写集合，供逐组件比对用。清单本身仍然只有
// ExcludedDirs 一处，这里只是它的派生索引。
var excludedDirNameSet = func() map[string]struct{} {
	set := make(map[string]struct{})
	for _, dir := range ExcludedDirs() {
		set[strings.ToLower(dir)] = struct{}{}
	}
	return set
}()

// PointIntoExcludedDir 判断调用方是不是把搜索根指进了依赖/产物目录里面：搜索根
// 自己就是清单里的目录，或者位于其中某个之下。只有这种明确指向才让宽泛排除让位；
// 指到父目录（"." 或 "frontend"）不算，那下面的 node_modules 照旧跳过。
//
// 工作区内按「相对工作区根的路径组件」比对，这样工作区自己叫 dist/build/vendor
// 时也不会把排除全部关掉。工作区外没有可比的根，只认最后一级目录名。
func PointIntoExcludedDir(workspaceRoot, searchRoot string) bool {
	rel, err := filepath.Rel(workspaceRoot, searchRoot)
	if err != nil {
		return IsExcludedDir(filepath.Base(searchRoot))
	}
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return IsExcludedDir(filepath.Base(searchRoot))
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if IsExcludedDir(part) {
			return true
		}
	}
	return false
}

// IsExcludedDir is the shared default dependency/build-directory policy.
func IsExcludedDir(name string) bool {
	_, ok := excludedDirNameSet[strings.ToLower(name)]
	return ok
}
