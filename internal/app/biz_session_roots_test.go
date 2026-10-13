// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"path/filepath"
	"testing"
)

// 会话附加工作区的活记录必须当轮生效：run 起步时冻结的 cfg 只是快照，运行中
// （含模型 ask 暂停期间）加/删只能靠这条记录追上围栏。
func TestLiveExtraRootsReachTheFence(t *testing.T) {
	isolateSandboxHome(t)
	// 围栏在位时才由 Ally 判越界；内核接管边界的宿主上这条断言无从验证。
	pinBoundaryOwnership(t, false)
	workspace := t.TempDir()
	extra := t.TempDir()
	app := NewApp()
	if app.sessionExtraRoots == nil {
		t.Fatal("NewApp must initialize sessionExtraRoots")
	}
	cfg := ConfigState{Workspace: workspace}
	target := filepath.Join(extra, "notes.txt")

	// 从未发布过列表的会话保持请求带的那份，不被活记录改动。
	roots, err := workspaceRoots(app.cfgWithLiveExtraRoots("s1", cfg))
	if err != nil || len(roots) != 1 || !samePath(roots[0], workspace) {
		t.Fatalf("an unpublished session must keep the request's roots, got %v err=%v", roots, err)
	}
	if _, err := resolveWritableFilePath(roots, target); toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("an outside write must be refused before the root is published, got %v", err)
	}

	// 发布后：同一种写法（带空格/带分隔符）只算一条，且立刻进入本轮的写根。
	if err := app.SetSessionExtraRoots("s1", []string{extra, extra + string(filepath.Separator) + "  "}); err != nil {
		t.Fatal(err)
	}
	roots, err = workspaceRoots(app.cfgWithLiveExtraRoots("s1", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || !samePath(roots[1], extra) {
		t.Fatalf("the published root must reach the run's roots, got %v", roots)
	}
	if _, err := resolveWritableFilePath(roots, target); err != nil {
		t.Fatalf("the fence must accept a write into the published root, got %v", err)
	}

	// 记录按会话隔离：另一个会话拿不到别人的附加工作区。
	if other, _ := workspaceRoots(app.cfgWithLiveExtraRoots("s2", cfg)); len(other) != 1 {
		t.Fatalf("another session must not inherit the published root, got %v", other)
	}

	// 运行中删除同样当轮生效：列表清空后写回区外立刻被拒。
	if err := app.SetSessionExtraRoots("s1", nil); err != nil {
		t.Fatal(err)
	}
	roots, err = workspaceRoots(app.cfgWithLiveExtraRoots("s1", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 {
		t.Fatalf("a removed root must be gone from the run's roots, got %v", roots)
	}
	if _, err := resolveWritableFilePath(roots, target); toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("an outside write must be refused again after the root is removed, got %v", err)
	}
}

// 同一份根列表只有一种写法：否则通知会因重排/重拼而误报「变了」。
func TestNormalizeExtraRootsIsOneForm(t *testing.T) {
	got := normalizeExtraRoots([]string{" /a/ ", "/a", "", "/b"})
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("normalized roots = %v, want [/a /b]", got)
	}
	// 空列表必须是 []（非 nil）：通知里要序列化成 []，nil 会退化成 null。
	if empty := normalizeExtraRoots(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("an empty list must stay a non-nil empty slice, got %#v", empty)
	}
}

// 空会话 id 不是合法上报目标：它会让记录落进一个谁也读不到的键。
func TestSetSessionExtraRootsRequiresSessionID(t *testing.T) {
	app := NewApp()
	if err := app.SetSessionExtraRoots("  ", []string{t.TempDir()}); err == nil {
		t.Fatal("an empty session id must be refused")
	}
	if len(app.sessionExtraRoots) != 0 {
		t.Fatalf("a refused publish must not write anything, got %v", app.sessionExtraRoots)
	}
}
