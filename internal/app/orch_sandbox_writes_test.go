// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ally-dev/internal/sandbox"
)

// spec 必须跟随宿主解析出的档位：强制平台 enforce，其余平台根本没有可打开它的开关。
func TestFileMutationSpecFollowsTheResolvedMode(t *testing.T) {
	spec := fileMutationSpec(ConfigState{}, []string{t.TempDir()})
	if spec.Enforce() != (sandbox.ResolvedMode() == sandbox.ModeEnforce) {
		t.Fatalf("the spec must follow the resolved mode, got %v", spec.Mode)
	}
}

// 写工具的写根必须比命令多出 ~/.ally_agent（memory / 全局配置落盘处），且不携带
// 任何禁读遮罩：mutation 只回传错误文本，mv/rm 不构成凭据外泄面，遮罩只会把
// memory 写坏。
func TestFileMutationSpecAddsDataDirAndSkipsMasks(t *testing.T) {
	isolateSandboxHome(t)
	workspace := t.TempDir()
	spec := fileMutationSpec(ConfigState{}, []string{workspace})
	if !spec.Enforce() {
		t.Skip("no backend on this platform")
	}
	want := appDataDir()
	found := false
	for _, root := range spec.WriteRoots {
		if samePath(root, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the Ally data directory %s must be a write root for mutations, got %v", want, spec.WriteRoots)
	}
	if len(spec.ForbidReadRoots) != 0 {
		t.Fatalf("mutations must not carry forbid-read masks, got %v", spec.ForbidReadRoots)
	}
}

// 全链路（策略层 + 沙箱）跑一遍写工具：enforce 档位下 create / overwrite /
// create_directory / rename / delete 必须照常工作，E_EXISTS 语义不变。
func TestWriteToolsRunConfinedInsideWorkspace(t *testing.T) {
	requireConfinement(t)
	isolateSandboxHome(t)
	workspace := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	a := NewApp()
	if err := a.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: workspace}

	if _, err := a.createFileWithConfig(cfg, CreateFileRequest{Path: "docs/guide.md", Content: "hello\n"}); err != nil {
		t.Fatalf("a confined create must succeed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "docs", "guide.md")); err != nil || string(data) != "hello\n" {
		t.Fatalf("confined create wrote wrong content: %q %v", data, err)
	}
	// create 默认不覆盖：E_EXISTS 语义在沙箱下必须保留。
	if _, err := a.createFileWithConfig(cfg, CreateFileRequest{Path: "docs/guide.md", Content: "x"}); toolErrorCode(err) != "E_EXISTS" {
		t.Fatalf("creating over an existing file must stay E_EXISTS, got %v (%s)", err, toolErrorCode(err))
	}
	if _, err := a.createFileWithConfig(cfg, CreateFileRequest{Path: "docs/guide.md", Content: "replaced\n", Overwrite: true}); err != nil {
		t.Fatalf("a confined overwrite must succeed: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(workspace, "docs", "guide.md")); string(data) != "replaced\n" {
		t.Fatalf("confined overwrite wrote wrong content: %q", data)
	}
	if err := a.createDirectoryWithConfig(cfg, CreateDirectoryRequest{Path: "docs/manual"}); err != nil {
		t.Fatalf("a confined create_directory must succeed: %v", err)
	}
	if renamed, err := a.renamePathWithConfig(cfg, RenamePathRequest{Path: "docs/guide.md", NewName: "readme.md"}); err != nil || renamed != "docs/readme.md" {
		t.Fatalf("a confined rename must succeed: %q %v", renamed, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "docs", "readme.md")); err != nil {
		t.Fatalf("the renamed file must exist: %v", err)
	}
	if _, err := a.deletePathsWithConfig(cfg, []string{"docs/readme.md"}, false); err != nil {
		t.Fatalf("a confined delete must succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "docs", "readme.md")); !os.IsNotExist(err) {
		t.Fatal("the confined delete must remove the file")
	}
	if _, err := a.deletePathsWithConfig(cfg, []string{"docs"}, true); err != nil {
		t.Fatalf("a confined recursive delete must succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "docs")); !os.IsNotExist(err) {
		t.Fatal("the confined recursive delete must remove the tree")
	}
}

// 内核兜底：绕过策略层、直接对写根之外的目标调用 mutation，必须被沙箱拒绝且不落
// 盘、不留 staging 残骸，模型还能读到能照做的拦截提示。
func TestConfinedMutationRefusedOutsideRoots(t *testing.T) {
	requireConfinement(t)
	isolateSandboxHome(t)
	// 工作区与区外目标必须在改写 TMPDIR 之前建好：os.TempDir() 本身在可写面里。
	workspace := t.TempDir()
	outside := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	cfg := ConfigState{Workspace: workspace}
	roots := []string{workspace}
	spec := fileMutationSpec(cfg, roots)
	outsideFile := filepath.Join(outside, "escaped.txt")

	err := sandboxedWriteFile(spec, roots, outsideFile, []byte("pwn"), 0o644)
	if toolErrorCode(err) != "E_SANDBOX_WRITE_DENIED" {
		t.Fatalf("a write outside the roots must be refused by the kernel, got %v (%s)", err, toolErrorCode(err))
	}
	if !strings.Contains(err.Error(), "沙箱已拦截") {
		t.Fatalf("the refusal must carry the model-facing hint, got %v", err)
	}
	assertNoMutationSideEffects(t, outside, outsideFile)

	// rm -f 对不存在的路径静默成功，删除/改名语义验证需要一个真实存在的区外
	// 目标（由测试本体、也就是“策略层之外的角色”直接落盘）。
	if err := os.WriteFile(outsideFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sandboxedRemove(spec, roots, outsideFile, false); toolErrorCode(err) != "E_SANDBOX_WRITE_DENIED" {
		t.Fatalf("a delete outside the roots must be refused, got %v (%s)", err, toolErrorCode(err))
	}
	if _, statErr := os.Stat(outsideFile); statErr != nil {
		t.Fatalf("the refused delete must leave the file in place: %v", statErr)
	}
	if err := sandboxedRename(spec, roots, outsideFile, filepath.Join(outside, "moved.txt")); toolErrorCode(err) != "E_SANDBOX_WRITE_DENIED" {
		t.Fatalf("a rename outside the roots must be refused, got %v (%s)", err, toolErrorCode(err))
	}
	if err := sandboxedMkdirAll(spec, roots, filepath.Join(outside, "sub", "dir")); toolErrorCode(err) != "E_SANDBOX_WRITE_DENIED" {
		t.Fatalf("a mkdir outside the roots must be refused, got %v (%s)", err, toolErrorCode(err))
	}
	if _, statErr := os.Stat(filepath.Join(outside, "sub")); statErr == nil {
		t.Fatal("the refused mkdir must leave nothing behind")
	}
}

// memory / 全局配置的落盘位置（~/.ally_agent）在写工具的沙箱里必须保持可写：
// 命令沙箱对它永远禁读（护 API key），写工具对它永远可写，两边各有各的道理。
func TestConfinedMutationAllowsAllyDataDir(t *testing.T) {
	requireConfinement(t)
	home := isolateSandboxHome(t)
	memories := filepath.Join(home, ".ally_agent", "memories")
	if err := os.MkdirAll(memories, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())

	workspace := t.TempDir()
	cfg := ConfigState{Workspace: workspace}
	roots := []string{workspace}
	spec := fileMutationSpec(cfg, roots)

	note := filepath.Join(memories, "note.md")
	if err := sandboxedWriteNewFile(spec, roots, note, []byte("memory"), 0o644); err != nil {
		t.Fatalf("writing into ~/.ally_agent must stay possible under confinement: %v", err)
	}
	if data, err := os.ReadFile(note); err != nil || string(data) != "memory" {
		t.Fatalf("the memory note must be written, got %q %v", data, err)
	}
	if err := sandboxedWriteFile(spec, roots, note, []byte("updated"), 0o644); err != nil {
		t.Fatalf("overwriting inside ~/.ally_agent must stay possible: %v", err)
	}
}

// 边界交给内核之后，越界写与越界删不再由策略层报错，而是内核在执行时拒：整条链路
// 必须给出沙箱的 E_SANDBOX_WRITE_DENIED 与可照做的提示，并且什么都不留下。
func TestWriteToolsLeaveTheBoundaryToTheKernel(t *testing.T) {
	requireConfinement(t)
	isolateSandboxHome(t)
	pinBoundaryOwnership(t, true)
	workspace := t.TempDir()
	// 区外目标与工作区都在改 TMPDIR 之前建好：os.TempDir() 本身在沙箱可写面里。
	outside := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	a := NewApp()
	if err := a.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: workspace}
	outsideFile := filepath.Join(outside, "escaped.txt")

	_, err := a.createFileWithConfig(cfg, CreateFileRequest{Path: outsideFile, Content: "pwn\n"})
	if toolErrorCode(err) != "E_SANDBOX_WRITE_DENIED" {
		t.Fatalf("an outside create must be refused by the kernel, got %v (%s)", err, toolErrorCode(err))
	}
	if !strings.Contains(err.Error(), "沙箱已拦截") {
		t.Fatalf("the refusal must carry the model-facing hint, got %v", err)
	}
	assertNoMutationSideEffects(t, outside, outsideFile)

	// rm -f 对不存在的路径静默成功：验证越界删除需要一个真实存在的区外目标，
	// 由测试本体（也就是策略层之外的角色）直接落盘。
	if err := os.WriteFile(outsideFile, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deleted, err := a.deletePathsWithConfig(cfg, []string{outsideFile}, false)
	if err != nil {
		t.Fatalf("a refused delete is a per-path result, not a tool error: %v", err)
	}
	if deleted.DeletedCount != 0 || deleted.FailedCount != 1 {
		t.Fatalf("the refused delete must be reported as failed, got %#v", deleted)
	}
	if got := deleted.Paths[0].ErrorCode; got != "E_SANDBOX_WRITE_DENIED" {
		t.Fatalf("an outside delete must be refused by the kernel, got %q (%s)", got, deleted.Paths[0].Error)
	}
	if _, statErr := os.Stat(outsideFile); statErr != nil {
		t.Fatalf("the refused delete must leave the file in place: %v", statErr)
	}
}

// assertNoMutationSideEffects verifies a refused mutation left neither the
// target nor any staging temp file behind.
func assertNoMutationSideEffects(t *testing.T, dir, target string) {
	t.Helper()
	if _, err := os.Stat(target); err == nil {
		t.Fatalf("the refused mutation must not create %s", target)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".ally-stage-") {
			t.Fatalf("the refused mutation left staging file %s behind", entry.Name())
		}
	}
}

// staging 是写工具里唯一一次不受沙箱约束的落盘（Ally 自己建临时兄弟文件），所以
// 目标目录本身被系统拒绍时（/etc 这类 root 拥有的目录）失败发生在内核之前，以前只
// 会给模型一句裸 "permission denied"、连错误码都没有——同一件事，一句解释都不剩。
// 这里用 0o555 目录复现：区外要给出与内核路径同一句提示，区内则是普通权限问题，
// 不能安到沙箱头上。
func TestStagingRefusalCarriesTheSameExplanation(t *testing.T) {
	requireConfinement(t)
	isolateSandboxHome(t)
	workspace := t.TempDir()
	outside := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	roots := []string{workspace}
	spec := fileMutationSpec(ConfigState{Workspace: workspace}, roots)

	outsideBlocked := filepath.Join(outside, "readonly")
	createReadOnlyDir(t, outsideBlocked)
	outsideTarget := filepath.Join(outsideBlocked, "new.txt")
	err := sandboxedWriteFile(spec, roots, outsideTarget, []byte("x"), 0o644)
	if toolErrorCode(err) != "E_SANDBOX_WRITE_DENIED" {
		t.Fatalf("a staging refusal outside the roots must use the sandbox error code, got %v (%s)", err, toolErrorCode(err))
	}
	if !strings.Contains(err.Error(), "沙箱已拦截") {
		t.Fatalf("the staging refusal must carry the kernel path's hint, got %v", err)
	}
	assertNoMutationSideEffects(t, outsideBlocked, outsideTarget)

	insideBlocked := filepath.Join(workspace, "readonly")
	createReadOnlyDir(t, insideBlocked)
	insideTarget := filepath.Join(insideBlocked, "new.txt")
	err = sandboxedWriteFile(spec, roots, insideTarget, []byte("x"), 0o644)
	if err == nil {
		t.Fatal("a write into a read-only directory must fail")
	}
	if code := toolErrorCode(err); code == "E_SANDBOX_WRITE_DENIED" {
		t.Fatalf("an ordinary permission problem inside the workspace must not be blamed on the sandbox, got %v", err)
	}
	assertNoMutationSideEffects(t, insideBlocked, insideTarget)
}

// createReadOnlyDir 建一个存在但不可写的目录：对非 root 的测试进程来说，往里面建
// 文件就是 EACCES，与 /etc 那类目录给 staging 的拒绍同源。
func createReadOnlyDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	// 权限得还回去，否则 t.TempDir 的清理收不掉这层目录。
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}
