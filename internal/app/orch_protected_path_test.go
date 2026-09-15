// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// protectedWorkspace builds a workspace that already contains VCS metadata, so a
// rejection proves the guard fired rather than a missing-file error.
func protectedWorkspace(t *testing.T) (string, ConfigState) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[core]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, ConfigState{Workspace: dir}
}

func encodedToolArgs(t *testing.T, v any) []byte {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// TestToolWritesRefuseVCSMetadata: the system prompt promises to never delete or
// overwrite any path containing .git, but only the delete path checked it. A
// create into .git/hooks runs on the next git command, and an edit of
// .git/config can redirect hooks or the pager.
func TestToolWritesRefuseVCSMetadata(t *testing.T) {
	dir, cfg := protectedWorkspace(t)
	app := NewApp()
	ctx := t.Context()

	res := app.executeTool(ctx, cfg, "s-1", "create", encodedToolArgs(t, CreateFileRequest{
		Path:      ".git/hooks/post-commit",
		Content:   "#!/bin/sh\necho pwned\n",
		Overwrite: true,
	}))
	if res.OK || !strings.Contains(res.Error, "E_PROTECTED_PATH") {
		t.Fatalf("create into .git must be refused, got ok=%v err=%v", res.OK, res.Error)
	}

	res = app.executeTool(ctx, cfg, "s-1", "edit", encodedToolArgs(t, FileTextEdits{
		Path:    ".git/config",
		Version: hashVersion([]byte("[core]\n")),
		Changes: []TextChange{{OldText: "[core]", NewText: "[core]\n\thooksPath = /tmp/evil"}},
	}))
	if res.OK || !strings.Contains(res.Error, "E_PROTECTED_PATH") {
		t.Fatalf("edit inside .git must be refused, got ok=%v err=%v", res.OK, res.Error)
	}

	res = app.executeTool(ctx, cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: ".git", Recursive: true}))
	if res.OK || !strings.Contains(res.Error, "E_PROTECTED_PATH") {
		t.Fatalf("delete of .git must be refused, got ok=%v err=%v", res.OK, res.Error)
	}

	// A shell redirect is the same escape through another door: the target is
	// inside the workspace, so the outside-write check does not cover it.
	res = app.executeTool(ctx, cfg, "s-1", "command", encodedToolArgs(t, CommandRequest{Command: "echo pwned > .git/hooks/post-commit"}))
	if res.OK || !strings.Contains(res.Error, "E_PROTECTED_PATH") {
		t.Fatalf("a redirect into .git must be refused, got ok=%v err=%v", res.OK, res.Error)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "post-commit")); err == nil {
		t.Fatal("a hook file was written into .git")
	}
	if got, err := os.ReadFile(filepath.Join(dir, ".git", "config")); err != nil || string(got) != "[core]\n" {
		t.Fatalf(".git/config was modified: %q (err=%v)", got, err)
	}

	if runtime.GOOS == "windows" {
		// Win32 drops trailing dots while filepath.Clean keeps them, so ".git."
		// named the real directory and slipped past the literal name comparison.
		res = app.executeTool(ctx, cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: ".git.", Recursive: true}))
		if res.OK || !strings.Contains(res.Error, "E_PROTECTED_PATH") {
			t.Fatalf("the trailing-dot alias of .git must be refused, got ok=%v err=%v", res.OK, res.Error)
		}
		if _, err := os.Stat(filepath.Join(dir, ".git", "config")); err != nil {
			t.Fatalf(".git must survive the aliased delete: %v", err)
		}
	}
}

// TestWorkspaceWritesNearVCSMetadataStayAllowed: the guard must not swallow
// ordinary dot-directories that merely start with ".git".
func TestWorkspaceWritesNearVCSMetadataStayAllowed(t *testing.T) {
	dir, cfg := protectedWorkspace(t)
	app := NewApp()

	res := app.executeTool(t.Context(), cfg, "s-1", "create", encodedToolArgs(t, CreateFileRequest{
		Path:    ".github/workflows/build.yml",
		Content: "name: ci\n",
	}))
	if !res.OK {
		t.Fatalf("writing .github/workflows must stay allowed, got err=%v", res.Error)
	}
	if _, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "build.yml")); err != nil {
		t.Fatalf("the file was not written: %v", err)
	}
}

// TestEditRefusesSymlinkedDirectoryEscape: edit joined paths lexically while
// create/delete resolved the real path, so a symlinked directory inside the
// workspace carried the write to its target outside the workspace.
func TestEditRefusesSymlinkedDirectoryEscape(t *testing.T) {
	outside := t.TempDir()
	target := filepath.Join(outside, "authorized_keys")
	if err := os.WriteFile(target, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Skipf("symlinks are unavailable on this host: %v", err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: ws}

	res := app.executeTool(t.Context(), cfg, "s-1", "edit", encodedToolArgs(t, FileTextEdits{
		Path:    "link/authorized_keys",
		Version: hashVersion([]byte("original\n")),
		Changes: []TextChange{{OldText: "original", NewText: "owned"}},
	}))
	if res.OK {
		t.Fatalf("edit through a symlinked directory must be refused, got %+v", res.Data)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "original\n" {
		t.Fatalf("a file outside the workspace was modified through the link: %q (err=%v)", got, err)
	}

	// create goes through the same guard; pinned here so both entry points stay
	// on one boundary.
	res = app.executeTool(t.Context(), cfg, "s-1", "create", encodedToolArgs(t, CreateFileRequest{Path: "link/new.txt", Content: "x"}))
	if res.OK {
		t.Fatal("create through a symlinked directory must be refused")
	}
}

// TestReadOversizedImageDoesNotBufferTheFile: the size guard used to run after
// os.ReadFile, so one read call on a huge "image" (only the first bytes have to
// look like one) pulled the whole file into memory before discarding it.
func TestReadOversizedImageDoesNotBufferTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatal(err)
	}
	// Sparse padding: far larger than the inline cap without costing the disk.
	size := int64(maxReadImageBytes) * 4
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := NewApp().readFileWithConfig(ConfigState{Workspace: dir}, ReadFileRequest{Path: "huge.png"})
	if err != nil {
		t.Fatalf("readFileWithConfig() error = %v", err)
	}
	if result.DataURL != "" {
		t.Fatal("an oversized image must not be inlined")
	}
	if !strings.Contains(result.Content, "too large") {
		t.Fatalf("notice = %q, want the too-large note", result.Content)
	}
	if result.Size != size || result.SHA256 == "" || result.Version == "" {
		t.Fatalf("result = %+v, want the real size plus a streamed hash and version", result)
	}
}
