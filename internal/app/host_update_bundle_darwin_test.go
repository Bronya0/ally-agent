// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateExchangesCompleteBundles(t *testing.T) {
	root := t.TempDir()
	app, backup := filepath.Join(root, "Ally.app"), filepath.Join(root, "Ally.app.bak")
	updateTestFile(t, filepath.Join(app, "version"), "old")
	updateTestFile(t, filepath.Join(backup, "version"), "new")
	if err := exchangeUpdateBundles(app, backup); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{app: "new", backup: "old"} {
		data, err := os.ReadFile(filepath.Join(path, "version"))
		if err != nil || string(data) != want {
			t.Fatalf("bundle %s: %q, %v", path, data, err)
		}
	}
}

func TestUpdateCopyRejectsParentLink(t *testing.T) {
	root := t.TempDir()
	source, target, outside := filepath.Join(root, "source"), filepath.Join(root, "target"), filepath.Join(root, "outside")
	updateTestFile(t, filepath.Join(source, "tools", "rg.exe"), "new")
	updateTestFile(t, filepath.Join(outside, "rg.exe"), "untouched")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "tools")); err != nil {
		t.Fatal(err)
	}
	if err := copyUpdatePath(source, target, filepath.Join("tools", "rg.exe")); err == nil {
		t.Fatal("parent link accepted")
	}
	data, err := os.ReadFile(filepath.Join(outside, "rg.exe"))
	if err != nil || string(data) != "untouched" {
		t.Fatalf("outside file changed: %q %v", data, err)
	}
}
