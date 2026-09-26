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
		Changes: []TextChange{{OldText: "[core]", NewText: textPtr("[core]\n\thooksPath = /tmp/evil")}},
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

// TestSymlinkedVCSMetadataAliasIsRefused: the VCS judgement was lexical, so a
// workspace symlink pointing at .git carried the write straight into it —
// `ln -s .git gh` then `create gh/hooks/post-commit` runs on the next git
// command. The write, delete, and command paths must all judge the resolved
// path as well as the literal one.
func TestSymlinkedVCSMetadataAliasIsRefused(t *testing.T) {
	dir, cfg := protectedWorkspace(t)
	if err := os.Symlink(".git", filepath.Join(dir, "gh")); err != nil {
		t.Skipf("symlinks are unavailable on this host: %v", err)
	}
	app := NewApp()
	ctx := t.Context()

	res := app.executeTool(ctx, cfg, "s-1", "create", encodedToolArgs(t, CreateFileRequest{
		Path:      "gh/hooks/post-commit",
		Content:   "#!/bin/sh\necho pwned\n",
		Overwrite: true,
	}))
	if res.OK || !strings.Contains(res.Error, "E_PROTECTED_PATH") {
		t.Fatalf("create through a symlink alias of .git must be refused, got ok=%v err=%v", res.OK, res.Error)
	}

	res = app.executeTool(ctx, cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: "gh/hooks", Recursive: true}))
	if res.OK || !strings.Contains(res.Error, "E_PROTECTED_PATH") {
		t.Fatalf("delete through a symlink alias of .git must be refused, got ok=%v err=%v", res.OK, res.Error)
	}

	res = app.executeTool(ctx, cfg, "s-1", "command", encodedToolArgs(t, CommandRequest{Command: "echo pwned > gh/hooks/post-commit"}))
	if res.OK || !strings.Contains(res.Error, "E_PROTECTED_PATH") {
		t.Fatalf("a redirect through a symlink alias of .git must be refused, got ok=%v err=%v", res.OK, res.Error)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "post-commit")); err == nil {
		t.Fatal("a hook file was written into .git through the symlink")
	}
	if info, err := os.Stat(filepath.Join(dir, ".git", "hooks")); err != nil || !info.IsDir() {
		t.Fatalf(".git/hooks must survive the aliased delete: %v", err)
	}
}

// TestTildeWriteTargetsStayInsideTheFence: `~` is expanded by the real shell, so
// the fence must resolve it before deciding; treating it as an unresolvable
// dynamic target let `>> ~/.zshrc` through with no inspection at all.
func TestTildeWriteTargetsStayInsideTheFence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("export PATH=$PATH:/usr/bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	app := NewApp()
	cfg := ConfigState{Workspace: ws}

	res := app.executeTool(t.Context(), cfg, "s-1", "command", encodedToolArgs(t, CommandRequest{Command: "echo evil >> ~/.zshrc"}))
	if res.OK || !strings.Contains(res.Error, "E_PATH_OUTSIDE") {
		t.Fatalf("appending to ~/.zshrc must be refused, got ok=%v err=%v", res.OK, res.Error)
	}
	if got, err := os.ReadFile(rc); err != nil || !strings.Contains(string(got), "export PATH") || strings.Contains(string(got), "evil") {
		t.Fatalf("the shell rc file must be untouched: %q (err=%v)", got, err)
	}

	// The same target inside the workspace stays allowed: the guard is about the
	// location, not about the command shape.
	res = app.executeTool(t.Context(), cfg, "s-1", "command", encodedToolArgs(t, CommandRequest{Command: "echo ok >> ./notes.txt"}))
	if !res.OK {
		t.Fatalf("an in-workspace append must stay allowed, got err=%v", res.Error)
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
		Changes: []TextChange{{OldText: "original", NewText: textPtr("owned")}},
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

// TestRemoveAllWithinBaseRefusesEscape covers the helper every recursive deletion
// of an externally derived path goes through. filepath.Join cleans `..` away, so
// one unvalidated component used to be able to walk the target up to the
// filesystem root — or straight to the base directory itself — before RemoveAll
// ran.
func TestRemoveAllWithinBaseRefusesEscape(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "updates")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(root, "keep")
	siblingFile := filepath.Join(sibling, "inner.txt")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(siblingFile, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	staged := filepath.Join(base, "v1.2.3")
	if err := os.MkdirAll(filepath.Join(staged, "staged"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeAllWithinBase(base, staged); err != nil {
		t.Fatalf("a strict descendant must stay removable: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("the staged version dir must be gone, stat err = %v", err)
	}

	for _, target := range []string{
		base,
		filepath.Join(base, ".."),
		filepath.Join(base, "..", ".."),
		sibling,
		filepath.Join(base, "v1.2.3", "..", "..", ".."),
	} {
		if err := removeAllWithinBase(base, target); err == nil {
			t.Fatalf("removeAllWithinBase(%q, %q) must be refused", base, target)
		}
	}
	if got, err := os.ReadFile(siblingFile); err != nil || string(got) != "keep\n" {
		t.Fatalf("an escape target must survive: %q (err=%v)", got, err)
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("the base directory itself must survive: %v", err)
	}
}

// TestDeleteRefusesAllyDataSubtree: ~/.ally_agent stays a write whitelist, but a
// recursive delete inside it wiped every saved session, memory, or staged update
// in one tool call. Single files stay deletable (the memory tool drops individual
// notes); directory trees do not.
func TestDeleteRefusesAllyDataSubtree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	allyDir := filepath.Join(home, ".ally_agent")
	histories := filepath.Join(allyDir, "histories")
	sessionDir := filepath.Join(histories, "s-1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	historyFile := filepath.Join(sessionDir, "history.json")
	if err := os.WriteFile(historyFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(allyDir, "memories", "note.md")
	if err := os.MkdirAll(filepath.Dir(note), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("# note\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}

	res := app.executeTool(t.Context(), cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: histories, Recursive: true}))
	if res.OK || !strings.Contains(res.Error, "E_DELETE_BLOCKED") {
		t.Fatalf("recursive delete inside ~/.ally_agent must be refused, got ok=%v err=%v", res.OK, res.Error)
	}
	if _, err := os.Stat(historyFile); err != nil {
		t.Fatalf("saved history must survive: %v", err)
	}

	res = app.executeTool(t.Context(), cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: sessionDir, Recursive: true}))
	if res.OK || !strings.Contains(res.Error, "E_DELETE_BLOCKED") {
		t.Fatalf("recursive delete of a history subdir must be refused, got ok=%v err=%v", res.OK, res.Error)
	}

	res = app.executeTool(t.Context(), cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: note}))
	if !res.OK {
		t.Fatalf("deleting a single memory note must stay allowed, got err=%v", res.Error)
	}
	if _, err := os.Stat(note); !os.IsNotExist(err) {
		t.Fatalf("the memory note must be gone, stat err = %v", err)
	}
}

// TestDeleteRefusesAllyDataSubtreeThroughSymlink: the guard is handed the
// *lexical* path, so a workspace symlink aimed at the data directory carried the
// delete straight into it (`ln -s ~/.ally_agent link` then
// `delete link/histories recursive`), wiping every saved session in one call —
// the fence cannot catch it either, because the data directory sits inside the
// write whitelist. Both path forms have to be judged.
func TestDeleteRefusesAllyDataSubtreeThroughSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	allyDir := filepath.Join(home, ".ally_agent")
	sessionDir := filepath.Join(allyDir, "histories", "s-1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	historyFile := filepath.Join(sessionDir, "history.json")
	if err := os.WriteFile(historyFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	link := filepath.Join(ws, "link")
	if err := os.Symlink(allyDir, link); err != nil {
		t.Skipf("symlinks are unavailable on this host: %v", err)
	}

	app := NewApp()
	cfg := ConfigState{Workspace: ws}
	aliased := filepath.Join(link, "histories")
	if _, err := os.Stat(aliased); err != nil {
		t.Fatalf("the alias must resolve before the guard is exercised: %v", err)
	}

	res := app.executeTool(t.Context(), cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: aliased, Recursive: true}))
	if res.OK || !strings.Contains(res.Error, "E_DELETE_BLOCKED") {
		t.Fatalf("a symlinked path into the data directory must be refused, got ok=%v err=%v", res.OK, res.Error)
	}
	if _, err := os.Stat(historyFile); err != nil {
		t.Fatalf("saved history must survive the aliased delete: %v", err)
	}

	// The alias must not be usable to delete the data directory itself either.
	res = app.executeTool(t.Context(), cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: link, Recursive: true}))
	if res.OK {
		t.Fatalf("a symlink resolving to the data directory must be refused, got %+v", res.Data)
	}
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("the data directory must survive: %v", err)
	}
}
