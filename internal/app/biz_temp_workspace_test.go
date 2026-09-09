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
	"runtime"
	"strings"
	"testing"
	"time"
)

// redirectTempRoot 把 Go 的临时目录根重定向到测试沙箱，绝不让测试触碰
// 真实的系统临时目录。
func redirectTempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TEMP", root)
	t.Setenv("TMP", root)
	return root
}

func TestCreateAndDeleteTempWorkspace(t *testing.T) {
	root := redirectTempRoot(t)
	a := NewApp()
	defer func() {
		// 清空进程集合，避免影响其他测试对全局状态的假设。
		tempWorkspaces.Lock()
		tempWorkspaces.paths = map[string]struct{}{}
		tempWorkspaces.Unlock()
	}()

	dir, err := a.CreateTempWorkspace()
	if err != nil {
		t.Fatalf("CreateTempWorkspace: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(dir), tempWorkspacePrefix) {
		t.Fatalf("directory base %q lacks prefix %q", filepath.Base(dir), tempWorkspacePrefix)
	}
	if parent := filepath.Dir(dir); !samePath(parent, root) {
		t.Fatalf("created dir %q is not under redirected temp root %q", dir, root)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("created dir missing: %v", err)
	}

	if err := a.DeleteTempWorkspace(dir); err != nil {
		t.Fatalf("DeleteTempWorkspace: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir still exists after delete: %v", err)
	}

	// 幂等：再删一次已不存在的目录不应报错（RemoveAll 语义）。
	if err := a.DeleteTempWorkspace(dir); err != nil {
		t.Fatalf("second DeleteTempWorkspace: %v", err)
	}
}

func TestDeleteTempWorkspaceRejectsInvalidPaths(t *testing.T) {
	root := redirectTempRoot(t)
	a := NewApp()

	// 在重定向根下造几个诱饵目录，验证护栏拒绝时它们原样保留。
	decoyHome := filepath.Join(t.TempDir(), "user-home")
	if err := os.MkdirAll(filepath.Join(decoyHome, "ally-temp-evil"), 0o755); err != nil {
		t.Fatal(err)
	}

	invalid := []string{
		"",
		"   ",
		filepath.Join(root, "not-ally-temp"),
		filepath.Join(root, "ally-temp-x", "nested"),
		filepath.Join(root, "..", "ally-temp-x"),
		filepath.Join(decoyHome, "ally-temp-evil"),
		"ally-temp-relative",
	}
	if runtime.GOOS == "windows" {
		invalid = append(invalid,
			`C:\Windows\ally-temp-x`,
			`C:\Windows\System32\ally-temp-x`,
		)
	} else {
		invalid = append(invalid,
			"/etc/ally-temp-x",
			"/usr/ally-temp-x",
			"/tmp/../etc/ally-temp-x",
		)
	}
	for _, path := range invalid {
		if err := a.DeleteTempWorkspace(path); err == nil {
			t.Fatalf("DeleteTempWorkspace(%q) unexpectedly succeeded", path)
		} else if code := toolErrorCode(err); code != tempWorkspaceErrorCode {
			t.Fatalf("DeleteTempWorkspace(%q) error code = %q, want %q", path, code, tempWorkspaceErrorCode)
		}
	}
	if _, err := os.Stat(filepath.Join(decoyHome, "ally-temp-evil")); err != nil {
		t.Fatalf("decoy directory was deleted: %v", err)
	}
}

func TestCleanupTempWorkspacesOnExit(t *testing.T) {
	redirectTempRoot(t)
	a := NewApp()

	dirs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		dir, err := a.CreateTempWorkspace()
		if err != nil {
			t.Fatalf("CreateTempWorkspace: %v", err)
		}
		dirs = append(dirs, dir)
	}
	a.cleanupTempWorkspacesOnExit()
	for _, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("temp dir %q survived exit cleanup: %v", dir, err)
		}
	}
}

func TestCleanupStaleTempWorkspaces(t *testing.T) {
	root := redirectTempRoot(t)
	a := NewApp()

	stale := filepath.Join(root, tempWorkspacePrefix+"stale")
	fresh := filepath.Join(root, tempWorkspacePrefix+"fresh")
	foreign := filepath.Join(root, "unrelated-dir")
	for _, dir := range []string{stale, fresh, foreign} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	staleTime := time.Now().Add(-staleTempWorkspaceAge - time.Hour)
	if err := os.Chtimes(stale, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	a.cleanupStaleTempWorkspaces()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale dir survived cleanup: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh dir was wrongly deleted: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign dir was wrongly deleted: %v", err)
	}
}
