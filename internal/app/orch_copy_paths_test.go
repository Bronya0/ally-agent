// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyDroppedDirectoryResolvesParentAliasesBeforeSelfCheck(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	base := t.TempDir()
	root := filepath.Join(base, "real")
	for _, rel := range []string{"source/target", "destination"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "file.txt"), []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(alias, "source")
	cfg := ConfigState{Workspace: root}
	result, err := a.copyFilesIntoWorkspaceWithConfig(cfg, CopyFilesIntoWorkspaceRequest{Sources: []string{source}, TargetDir: "source/target"})
	if err != nil || len(result.Copied) != 0 || len(result.Failed) != 1 || !strings.Contains(result.Failed[0].Error, "cannot copy a directory into itself") {
		t.Fatalf("self-copy through a parent alias was not rejected up front: %+v %v", result, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "source", "target"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected copy left a recursive partial tree: %v %v", entries, err)
	}
	// Alias spelling alone must not prevent copying into an unrelated directory.
	cfg.Workspace = alias
	result, err = a.copyFilesIntoWorkspaceWithConfig(cfg, CopyFilesIntoWorkspaceRequest{Sources: []string{source}, TargetDir: "destination"})
	if err != nil || len(result.Copied) != 1 || len(result.Failed) != 0 {
		t.Fatalf("valid copy through a parent alias failed: %+v %v", result, err)
	}
	if result.Copied[0] != "destination/source" {
		t.Fatalf("workspace alias leaked into the returned relative path: %q", result.Copied[0])
	}
	data, err := os.ReadFile(filepath.Join(root, "destination", "source", "file.txt"))
	if err != nil || string(data) != "contents" {
		t.Fatalf("copied data changed: %q %v", data, err)
	}
}
