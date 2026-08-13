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
	"testing"
)

func TestRollbackReplacedResourcesRestoresFilesAndExe(t *testing.T) {
	exeDir := t.TempDir()
	backupDir := t.TempDir()

	// Old resource that existed before the update and was backed up.
	oldRes := filepath.Join(exeDir, "tools", "rg.exe")
	if err := os.MkdirAll(filepath.Dir(oldRes), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldRes, []byte("old-rg"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupRes := filepath.Join(backupDir, "tools", "rg.exe")
	if err := os.MkdirAll(filepath.Dir(backupRes), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupRes, []byte("old-rg-backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Resource that did not exist before the update (no backup) — rollback
	// must remove it.
	newOnly := filepath.Join(exeDir, "new.txt")
	if err := os.WriteFile(newOnly, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	backupExe := filepath.Join(exeDir, "Ally.exe.bak")
	currentExe := filepath.Join(exeDir, "Ally.exe")
	if err := os.WriteFile(backupExe, []byte("old-exe"), 0o644); err != nil {
		t.Fatal(err)
	}

	replaced := []string{filepath.ToSlash("tools/rg.exe"), "new.txt"}
	if msg := rollbackReplacedResources(backupDir, exeDir, replaced, backupExe, currentExe); msg != "" {
		t.Fatalf("rollback reported failures: %s", msg)
	}

	data, err := os.ReadFile(oldRes)
	if err != nil || string(data) != "old-rg-backup" {
		t.Fatalf("resource not restored: %q err=%v", data, err)
	}
	if _, err := os.Stat(newOnly); !os.IsNotExist(err) {
		t.Fatalf("new-only resource should be removed, stat err=%v", err)
	}
	exeData, err := os.ReadFile(currentExe)
	if err != nil || string(exeData) != "old-exe" {
		t.Fatalf("exe not restored: %q err=%v", exeData, err)
	}
	if _, err := os.Stat(backupExe); !os.IsNotExist(err) {
		t.Fatalf("exe backup should be consumed, stat err=%v", err)
	}
}
