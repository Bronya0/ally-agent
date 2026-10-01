// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateHomeAndTemp 把 home 与 temp 指向测试自己的临时目录：writableDirs 会探测
// 这两个位置，测试绝不能读到（更不会写到）真实用户目录。
func isolateHomeAndTemp(t *testing.T) (home, temp string) {
	t.Helper()
	home = t.TempDir()
	temp = t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(key, home)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, temp)
	}
	return home, temp
}

// containsDir 按策略口径（绝对 + 解析 symlink）比对目录是否在集合里。
func containsDir(dirs []string, want string) bool {
	resolved, isDir, ok := classifyPath(want)
	if !ok || !isDir {
		return false
	}
	for _, dir := range dirs {
		if dir == resolved {
			return true
		}
	}
	return false
}

func mustMkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

// problemMentions 报告某条路径是否出现在诊断里。诊断面向人，只保证带上了归一化
// 后的路径，不保证整句措辞。
func problemMentions(problems []string, path string) bool {
	want := filepath.ToSlash(path)
	for _, problem := range problems {
		if strings.Contains(problem, want) {
			return true
		}
	}
	return false
}

func TestWritableDirsKeepsRootsTempAndCaches(t *testing.T) {
	home, temp := isolateHomeAndTemp(t)
	for _, sub := range cacheSubdirs() {
		mustMkdir(t, filepath.Join(home, sub))
	}
	root := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope")

	dirs, problems := writableDirs(Spec{WriteRoots: []string{root, missing}})

	for _, want := range []string{root, temp, filepath.Join(home, ".cache"), filepath.Join(home, "go")} {
		if !containsDir(dirs, want) {
			t.Fatalf("writableDirs dropped %s: %v", want, dirs)
		}
	}
	// 不存在的路径不能进策略：bubblewrap 会因此拒绝启动。但丢的是调用方的写根，
	// 必须出声——否则它只会变成沙箱内一次无法解释的权限错误。
	if containsDir(dirs, missing) {
		t.Fatalf("writableDirs kept a missing path: %v", dirs)
	}
	if !problemMentions(problems, missing) {
		t.Fatalf("a dropped write root must be reported, got %v", problems)
	}
}

func TestWritableDirsDeduplicatesAndDropsFiles(t *testing.T) {
	_, _ = isolateHomeAndTemp(t)
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}

	dirs, problems := writableDirs(Spec{WriteRoots: []string{root, root, file, ""}})

	count := 0
	for _, dir := range dirs {
		if contains, _, _ := classifyPath(root); dir == contains {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("root must appear once, got %d in %v", count, dirs)
	}
	// 文件不是目录，不能成为绑定点。
	if resolvedFile, _, _ := classifyPath(file); containsDir(dirs, resolvedFile) {
		t.Fatalf("a file must never be a write root: %v", dirs)
	}
	if !problemMentions(problems, file) {
		t.Fatalf("a write root that is not a directory must be reported, got %v", problems)
	}
}

func TestDenyWritePathsOnlySurviveInsideWriteRoots(t *testing.T) {
	root := t.TempDir()
	sources := mustMkdir(t, filepath.Join(root, "sources"))
	child := mustMkdir(t, filepath.Join(sources, "sub"))
	outside := t.TempDir()
	missing := filepath.Join(root, "missing")

	got, problems := denyWritePaths([]string{sources, outside, child, missing}, []string{root})

	if len(got) != 1 {
		t.Fatalf("want exactly the sources root, got %+v", got)
	}
	if resolved, _, _ := classifyPath(sources); got[0].path != resolved || !got[0].dir {
		t.Fatalf("got %+v, want %s as a directory", got, resolved)
	}
	// 写根之外的目标本来就不可写，不算保护丢失；不存在的目标则要出声——它什么
	// 都没保护到，不说就只能靠用户自己发现拼错了。
	if len(problems) != 1 || !problemMentions(problems, missing) {
		t.Fatalf("want exactly the missing target reported, got %v", problems)
	}
}

// 写根永远赢：遮蔽一个命令可写的目录会把权限规则变成坏掉的沙箱。
func TestReadMasksWriteRootsWin(t *testing.T) {
	root := t.TempDir()
	inside := mustMkdir(t, filepath.Join(root, "secret"))
	outside := t.TempDir()
	nested := mustMkdir(t, filepath.Join(outside, "nested"))

	masks, problems := readMasks([]string{
		inside,                       // 在工作区内 → 丢弃
		root,                         // 就是工作区 → 丢弃
		filepath.Dir(root),           // 包住工作区 → 丢弃
		nested,                       // 被父级遮蔽覆盖 → 丢弃
		filepath.Join(outside, "no"), // 不存在 → 丢弃
		outside,                      // 唯一应该留下的
	}, []string{root})

	resolved, _, _ := classifyPath(outside)
	if len(masks) != 1 || masks[0].path != resolved || !masks[0].dir {
		t.Fatalf("want exactly %s as a directory mask, got %+v", resolved, masks)
	}
	// 被写根挤掉的三条要出声（那是"你以为有保护其实没有"）；被父级遮蔽覆盖的
	// 不算丢：父级照样遮着，保护仍在。
	if len(problems) != 4 {
		t.Fatalf("want the 3 write-root conflicts and 1 missing target reported, got %v", problems)
	}
}

// 文件与目录要区分：bubblewrap 不能把 tmpfs 挂在文件上。两个目标必须彼此独立，
// 否则父目录的遮蔽会（正确地）吞掉里面的文件。
func TestReadMasksClassifiesFilesAndDirs(t *testing.T) {
	dir := t.TempDir()
	plainDir := mustMkdir(t, filepath.Join(dir, "plain"))
	file := filepath.Join(dir, "api.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}

	masks, _ := readMasks([]string{plainDir, file}, nil)

	if len(masks) != 2 {
		t.Fatalf("want both targets, got %+v", masks)
	}
	kinds := map[string]bool{}
	for _, mask := range masks {
		kinds[mask.path] = mask.dir
	}
	for _, target := range []string{plainDir, file} {
		resolved, isDir, _ := classifyPath(target)
		gotDir, ok := kinds[resolved]
		if !ok {
			t.Fatalf("%s missing from %+v", resolved, masks)
		}
		if gotDir != isDir {
			t.Fatalf("%s classified as dir=%v, want %v", resolved, gotDir, isDir)
		}
	}
}

// 遮蔽结果与输入顺序无关：先给子路径、后给父路径，父级同样要吞掉子级（否则
// bubblewrap 会先在被遮蔽的目录里建挂载点，再被父级 tmpfs 整片覆盖）。
func TestReadMasksIsOrderIndependent(t *testing.T) {
	outside := t.TempDir()
	nested := mustMkdir(t, filepath.Join(outside, "nested"))

	childFirst, _ := readMasks([]string{nested, outside}, nil)
	parentFirst, _ := readMasks([]string{outside, nested}, nil)

	resolved, _, _ := classifyPath(outside)
	if len(childFirst) != 1 || childFirst[0].path != resolved {
		t.Fatalf("child-first input must keep only %s, got %+v", resolved, childFirst)
	}
	if len(parentFirst) != 1 || parentFirst[0].path != resolved {
		t.Fatalf("parent-first input must keep only %s, got %+v", resolved, parentFirst)
	}
}

// 禁止写要能点名一个文件：只支持目录会把"保护这个文件"静默丢掉。
func TestDenyWritePathsAcceptsSingleFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "sources.txt")
	if err := os.WriteFile(file, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}

	got, problems := denyWritePaths([]string{file}, []string{root})

	if len(got) != 1 || got[0].dir {
		t.Fatalf("want one file entry, got %+v", got)
	}
	if len(problems) != 0 {
		t.Fatalf("a file target inside the workspace is applied, not reported: %v", problems)
	}
}

// 可写面里的缓存目录必须能在沙箱启动前补建：命令在沙箱内没有权限创建它们，
// 而它们的父目录也不可写。
func TestEnsureWritableDirsCreatesCaches(t *testing.T) {
	home, _ := isolateHomeAndTemp(t)

	if err := EnsureWritableDirs(); err != nil {
		t.Fatalf("EnsureWritableDirs: %v", err)
	}
	for _, sub := range cacheSubdirs() {
		if info, err := os.Stat(filepath.Join(home, sub)); err != nil || !info.IsDir() {
			t.Fatalf("cache %s was not created: %v", sub, err)
		}
	}
}

// 缓存目录被换成指向家目录之外的链接时不能跟着走：那等于把链接的目标加成可写。
func TestWritableDirsDropsRedirectedCache(t *testing.T) {
	home, _ := isolateHomeAndTemp(t)
	elsewhere := t.TempDir()
	link := filepath.Join(home, ".cache")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	dirs, problems := writableDirs(Spec{})

	if containsDir(dirs, elsewhere) {
		t.Fatalf("a redirected cache must not become writable: %v", dirs)
	}
	if !problemMentions(problems, link) {
		t.Fatalf("a redirected cache must be reported, got %v", problems)
	}
}

// 只留调用方自己的写根：临时目录、工具链缓存、平台附加位置一个都不进策略。文件
// 工具的落盘动作用这一档——工作区内的符号链接指向那些位置时，内核不该放行。
func TestWritableDirsOnlyWriteRootsDropsEverydayWrites(t *testing.T) {
	home, temp := isolateHomeAndTemp(t)
	for _, sub := range cacheSubdirs() {
		mustMkdir(t, filepath.Join(home, sub))
	}
	root := t.TempDir()

	dirs, problems := writableDirs(Spec{WriteRoots: []string{root}, OnlyWriteRoots: true})

	if len(dirs) != 1 || !containsDir(dirs, root) {
		t.Fatalf("OnlyWriteRoots must keep exactly the caller's roots, got %v", dirs)
	}
	if len(problems) != 0 {
		t.Fatalf("a present root has nothing to report, got %v", problems)
	}
	for _, forbidden := range []string{temp, filepath.Join(home, ".cache"), filepath.Join(home, "go")} {
		if containsDir(dirs, forbidden) {
			t.Fatalf("OnlyWriteRoots must not keep %s writable: %v", forbidden, dirs)
		}
	}
}
