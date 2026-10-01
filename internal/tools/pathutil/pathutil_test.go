// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeRuntime is the minimal pathutil.Runtime: the absolute path to the data dir.
type fakeRuntime struct{ dir string }

func (f fakeRuntime) AppDataDir() string { return f.dir }

// TestJoinPathNormalizesWithoutContainment pins the split the kernel-owned host
// depends on: JoinPath normalizes and judges nothing, SafeJoin is JoinPath plus
// that judgement, and the shared half can not drift between the two.
func TestJoinPathNormalizesWithoutContainment(t *testing.T) {
	rt := fakeRuntime{dir: t.TempDir()}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escaped.txt")

	joined, err := JoinPath([]string{root}, "a/b.txt")
	if err != nil {
		t.Fatalf("JoinPath must join a relative path: %v", err)
	}
	if want := filepath.Join(root, "a", "b.txt"); joined != want {
		t.Fatalf("JoinPath(%q) = %q, want %q", "a/b.txt", joined, want)
	}
	joined, err = JoinPath([]string{root}, outside)
	if err != nil || joined != filepath.Clean(outside) {
		t.Fatalf("JoinPath must accept an outside path unchanged, got %q %v", joined, err)
	}
	if _, err := SafeJoin(rt, []string{root}, outside); err == nil {
		t.Fatal("SafeJoin must still refuse an outside path")
	}
}

// TestVCSMetadataReasonBlocksDirectoryAndContent pins the single judgement the
// write, delete, and command guards share: .git itself and anything below it.
func TestVCSMetadataReasonBlocksDirectoryAndContent(t *testing.T) {
	blocked := []string{
		".git",
		"repo/.git",
		"repo/.git/config",
		"repo/.git/hooks/pre-commit",
		"/home/me/repo/.git/HEAD",
		"repo/.svn/entries",
		"repo/.hg/store",
	}
	for _, path := range blocked {
		got, reason := VCSMetadataReason(path)
		if !got || reason == "" {
			t.Fatalf("VCSMetadataReason(%q) = %v, %q; want blocked with a reason", path, got, reason)
		}
	}
}

// TestVCSMetadataReasonAllowsSimilarNames guards against over-blocking: ordinary
// dot-directories and files that merely start with ".git".
func TestVCSMetadataReasonAllowsSimilarNames(t *testing.T) {
	allowed := []string{
		".github/workflows/build.yml",
		"repo/.gitattributes",
		"notes/.gitkeep",
		"gitignore",
		"git.ts",
	}
	for _, path := range allowed {
		if got, reason := VCSMetadataReason(path); got {
			t.Fatalf("VCSMetadataReason(%q) = true, %q; want allowed", path, reason)
		}
	}
}

// TestCanonicalPathKeepsNamesOnNonWindowsHosts: elsewhere a trailing dot is part
// of the name, so the canonical form must leave it alone.
func TestCanonicalPathKeepsNamesOnNonWindowsHosts(t *testing.T) {
	if IsWindows {
		t.Skip("windows canonicalization is covered by the alias test")
	}
	if got := CanonicalPath("/ws/.git."); got != "/ws/.git." {
		t.Fatalf("CanonicalPath(/ws/.git.) = %q, want the name untouched", got)
	}
}

// TestCanonicalPathStripsWindowsTrailingDotsAndSpaces: Win32 drops trailing dots
// and spaces, so ".git." opens the real .git while filepath.Clean keeps the dot —
// the literal comparison the delete guard used to make was therefore bypassable.
func TestCanonicalPathStripsWindowsTrailingDotsAndSpaces(t *testing.T) {
	if !IsWindows {
		t.Skip("windows path aliasing")
	}
	cases := []struct{ in, want string }{
		{`C:\ws\.git.`, `C:\ws\.git`},
		{`C:\ws\.git `, `C:\ws\.git`},
		{`C:\ws\dir. \file.txt`, `C:\ws\dir\file.txt`},
		{`C:\ws\.git..\config`, `C:\ws\.git\config`},
		{`C:\ws\..\other`, `C:\other`},
	}
	for _, tc := range cases {
		if got := CanonicalPath(tc.in); got != tc.want {
			t.Fatalf("CanonicalPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if blocked, _ := VCSMetadataReason(`C:\ws\.git.`); !blocked {
		t.Fatal("the trailing-dot alias of .git must be recognized")
	}
}

// TestInsideWriteRootResolvesSymlinkedAppDataDir: target arrives already
// symlink-resolved, so the ~/.ally_agent whitelist has to resolve its own side
// too. A symlinked $HOME (or /var → /private/var on macOS) otherwise made every
// write inside the data directory fail with E_PATH_OUTSIDE.
func TestInsideWriteRootResolvesSymlinkedAppDataDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "data")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are unavailable on this host: %v", err)
	}
	// Created through the link, so the real prefix carries the symlink.
	memories := filepath.Join(link, ".ally_agent", "memories")
	if err := os.MkdirAll(memories, 0o755); err != nil {
		t.Fatal(err)
	}
	rt := fakeRuntime{dir: filepath.Join(link, ".ally_agent")}
	// resolveWritableFilePath hands over the evalExistingPrefix result.
	resolved, err := filepath.EvalSymlinks(memories)
	if err != nil {
		t.Fatal(err)
	}
	if !InsideWriteRoot(rt, nil, filepath.Join(resolved, "note.md")) {
		t.Fatalf("the resolved data dir must be honored: %q", resolved)
	}
	if InsideWriteRoot(rt, nil, filepath.Join(filepath.Dir(real), "elsewhere.txt")) {
		t.Fatal("a path outside the data dir must stay outside")
	}
}

// TestInsideWriteRootRejectsDataDirPointingAtFilesystemRoot: the fallback resolves
// its own side, so it must refuse the degenerate case where the data directory is a
// symlink to the filesystem root — otherwise the whitelist would cover the disk.
func TestInsideWriteRootRejectsDataDirPointingAtFilesystemRoot(t *testing.T) {
	rootLink := filepath.Join(t.TempDir(), "rootlink")
	if err := os.Symlink(string(filepath.Separator), rootLink); err != nil {
		t.Skipf("symlinks are unavailable on this host: %v", err)
	}
	rt := fakeRuntime{dir: rootLink}
	if InsideWriteRoot(rt, nil, filepath.Join(string(filepath.Separator), "etc", "passwd")) {
		t.Fatal("a data dir resolving to the filesystem root must not whitelist the whole disk")
	}
}

// 根工作区会让一切边界检查形同虚设（整盘都算"工作区内"），选择器与运行入口
// 都靠这个判定拒绝。Unix 侧任何平台上都能测；Windows 形态走手写解析，同样
// 全平台可测（这就是不用 filepath.VolumeName 的原因）。
func TestIsSystemRootPathUnixForms(t *testing.T) {
	if IsWindows {
		t.Skip("unix forms are not meaningful on windows")
	}
	for _, path := range []string{"/", "//", "/.", "/../..", "  /  "} {
		if !IsSystemRootPath(path) {
			t.Fatalf("IsSystemRootPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"", "  ", "/Users", "/home/x", "relative", "/tmp/", ".", "C:\\"} {
		if IsSystemRootPath(path) {
			t.Fatalf("IsSystemRootPath(%q) = true, want false", path)
		}
	}
}

func TestIsWindowsVolumeRootForms(t *testing.T) {
	roots := []string{
		`C:\`, `C:/`, `C:`, `c:\`, `D:\`,
		`\\server\share`, `//server/share`, `\\?\C:\`, `\\?\UNC\server\share`,
		`\`, `/`,
	}
	for _, path := range roots {
		if !isWindowsVolumeRoot(path) {
			t.Fatalf("isWindowsVolumeRoot(%q) = false, want true", path)
		}
	}
	notRoots := []string{
		`C:\Users`, `C:\Users\x\proj`, `C:/Users`, `D:\proj`,
		`\\server\share\docs`, `//server/share/docs/x`, `\\?\C:\Users`,
		`\\?\UNC\server\share\docs`, `\Users`, `/Users`, `Users`, `file.txt`, `C:proj`,
	}
	for _, path := range notRoots {
		if isWindowsVolumeRoot(path) {
			t.Fatalf("isWindowsVolumeRoot(%q) = true, want false", path)
		}
	}
}

// Windows 构建上 IsSystemRootPath 必须路由到手写解析；这里顺带验证空路径永远
// 不是根（它连有效路径都不是，调用方各有自己的“必填”报错）。
func TestIsSystemRootPathEmptyIsNeverRoot(t *testing.T) {
	if IsSystemRootPath("") || IsSystemRootPath("   ") {
		t.Fatal("an empty path is never a filesystem root")
	}
}
