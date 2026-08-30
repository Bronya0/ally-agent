// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kbTestSetup builds a KB-shaped workspace: <dir>/sources/note.md plus an
// entry outside sources/. It returns the app, the KB config, and the deny
// context exactly as runChat attaches it for a KB run, so the assertions run
// through the production executeTool boundary.
func kbTestSetup(t *testing.T) (*App, ConfigState, context.Context, string) {
	t.Helper()
	app := NewApp()
	dir := t.TempDir()
	sources := filepath.Join(dir, "sources")
	if err := os.MkdirAll(sources, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sources, "note.md"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: dir, KBRoot: dir}
	ctx := withKBDenyRoots(context.Background(), kbDenyRootsForConfig(cfg))
	return app, cfg, ctx, dir
}

func TestIsKnowledgeBaseWorkspace(t *testing.T) {
	dir := t.TempDir()
	if !isKnowledgeBaseWorkspace(dir, dir) {
		t.Fatal("workspace equal to KB root must be KB mode")
	}
	if isKnowledgeBaseWorkspace(dir, filepath.Join(dir, "other")) {
		t.Fatal("different workspace must not be KB mode")
	}
	if isKnowledgeBaseWorkspace(dir, "") || isKnowledgeBaseWorkspace("", dir) {
		t.Fatal("empty sides must never match")
	}
}

func TestKBDenyRootsForConfig(t *testing.T) {
	dir := t.TempDir()
	if roots := kbDenyRootsForConfig(ConfigState{Workspace: dir}); roots != nil {
		t.Fatalf("non-KB config must have no deny roots, got %v", roots)
	}
	roots := kbDenyRootsForConfig(ConfigState{Workspace: dir, KBRoot: dir})
	if len(roots) == 0 {
		t.Fatal("KB config must produce deny roots")
	}
	for _, root := range roots {
		if filepath.Base(root) != "sources" {
			t.Fatalf("deny root must be the sources/ subtree, got %q", root)
		}
	}
}

// jsonQuote renders s as a JSON string literal for inline test payloads.
func jsonQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func TestKBDenyBlocksCreateEditDeleteViaExecuteTool(t *testing.T) {
	app, cfg, ctx, dir := kbTestSetup(t)

	// create under sources/ is denied (relative and absolute forms).
	res := app.executeTool(ctx, cfg, "s-1", "create", []byte(`{"path":"sources/new.md","content":"x"}`))
	if res.OK || !strings.Contains(res.Error, "E_KB_SOURCES_READONLY") {
		t.Fatalf("create under sources/ must be denied, got ok=%v err=%v", res.OK, res.Error)
	}
	abs := filepath.Join(dir, "sources", "abs.md")
	res = app.executeTool(ctx, cfg, "s-1", "create", []byte(`{"path":`+jsonQuote(abs)+`,"content":"x"}`))
	if res.OK || !strings.Contains(res.Error, "E_KB_SOURCES_READONLY") {
		t.Fatalf("absolute create under sources/ must be denied, got ok=%v err=%v", res.OK, res.Error)
	}

	// create outside sources/ still works.
	res = app.executeTool(ctx, cfg, "s-1", "create", []byte(`{"path":"entry.md","content":"# Entry\n"}`))
	if !res.OK {
		t.Fatalf("create outside sources/ must succeed, got err=%v", res.Error)
	}

	// edit of a sources/ file is denied before version validation: the deny
	// pre-check fires even with a deliberately bogus version.
	res = app.executeTool(ctx, cfg, "s-1", "edit", []byte(`{"path":"sources/note.md","version":"aaaaaa","changes":[{"oldText":"original","newText":"changed"}]}`))
	if res.OK || !strings.Contains(res.Error, "E_KB_SOURCES_READONLY") {
		t.Fatalf("edit under sources/ must be denied, got ok=%v err=%v", res.OK, res.Error)
	}

	// editing the same file without the KB deny ctx keeps working: a plain
	// context proves the policy is run-scoped, not config-global. A version
	// computed from different content then fails through the ordinary
	// E_VERSION_MISMATCH path instead of the KB deny.
	plainCtx := context.Background()
	bogusVersion := hashVersion([]byte("different\n"))
	res = app.executeTool(plainCtx, cfg, "s-1", "edit", []byte(`{"path":"sources/note.md","version":"`+bogusVersion+`","changes":[{"oldText":"original","newText":"changed"}]}`))
	if res.OK {
		t.Fatal("edit with stale version must still fail without deny ctx")
	}
	if strings.Contains(res.Error, "E_KB_SOURCES_READONLY") {
		t.Fatal("without deny ctx the failure must come from version validation, not the KB deny")
	}

	// delete of a sources/ path is denied; deleting the entry works.
	res = app.executeTool(ctx, cfg, "s-1", "delete", []byte(`{"path":"sources/note.md"}`))
	if res.OK || !strings.Contains(res.Error, "E_KB_SOURCES_READONLY") {
		t.Fatalf("delete under sources/ must be denied, got ok=%v err=%v", res.OK, res.Error)
	}
	res = app.executeTool(ctx, cfg, "s-1", "delete", []byte(`{"path":"entry.md"}`))
	if !res.OK {
		t.Fatalf("delete outside sources/ must succeed, got err=%v", res.Error)
	}
}

func TestKBDenyBlocksCommandWriteTargets(t *testing.T) {
	app, cfg, ctx, _ := kbTestSetup(t)

	res := app.executeTool(ctx, cfg, "s-1", "command", []byte(`{"command":"echo hi > sources/out.txt"}`))
	if res.OK || !strings.Contains(res.Error, "E_KB_SOURCES_READONLY") {
		t.Fatalf("redirect into sources/ must be denied, got ok=%v err=%v", res.OK, res.Error)
	}

	res = app.executeTool(ctx, cfg, "s-1", "command", []byte(`{"command":"echo hi > entry.md"}`))
	if !res.OK {
		t.Fatalf("redirect outside sources/ must succeed, got err=%v", res.Error)
	}

	// Non-write commands are unaffected by the deny policy.
	res = app.executeTool(ctx, cfg, "s-1", "command", []byte(`{"command":"echo hi"}`))
	if !res.OK {
		t.Fatalf("plain echo must succeed, got err=%v", res.Error)
	}
}

func TestKBConfigMergeAndClear(t *testing.T) {
	// mergeConfig: overlay non-empty wins; empty overlay keeps the base value
	// so a KB session's StartChat (or an older frontend) never clears it.
	base := ConfigState{KBRoot: "/base/kb"}
	if got := mergeConfig(base, ConfigState{KBRoot: "/overlay/kb"}).KBRoot; got != "/overlay/kb" {
		t.Fatalf("non-empty overlay must win, got %q", got)
	}
	if got := mergeConfig(base, ConfigState{}).KBRoot; got != "/base/kb" {
		t.Fatalf("empty overlay must keep base, got %q", got)
	}

	// SaveConfig adopts the request value verbatim (including clearing), so
	// the settings draft can unset the KB root.
	app := newApiTestApp(t)
	app.config.KBRoot = "/base/kb"
	if err := app.SaveConfig(ConfigState{Workspace: app.config.Workspace}); err != nil {
		t.Fatal(err)
	}
	if app.config.KBRoot != "" {
		t.Fatalf("SaveConfig with empty kbRoot must clear it, got %q", app.config.KBRoot)
	}
	if err := app.SaveConfig(ConfigState{Workspace: app.config.Workspace, KBRoot: "/next/kb"}); err != nil {
		t.Fatal(err)
	}
	if app.config.KBRoot != "/next/kb" {
		t.Fatalf("SaveConfig must adopt the request kbRoot, got %q", app.config.KBRoot)
	}
}

func TestKBPromptPartInjection(t *testing.T) {
	kbRoot := t.TempDir()
	other := t.TempDir()

	inKB := defaultSystemPrompt(nil, kbRoot, nil, "", "", kbRoot)
	if !strings.Contains(inKB, "Knowledge Base Mode") {
		t.Fatal("KB workspace must inject the KB prompt part")
	}
	if !strings.Contains(inKB, "sources/") {
		t.Fatal("KB prompt part must state the sources/ read-only boundary")
	}
	outsideKB := defaultSystemPrompt(nil, other, nil, "", "", kbRoot)
	if strings.Contains(outsideKB, "Knowledge Base Mode") {
		t.Fatal("non-KB workspace must not inject the KB prompt part")
	}
	noRoot := defaultSystemPrompt(nil, kbRoot, nil, "", "", "")
	if strings.Contains(noRoot, "Knowledge Base Mode") {
		t.Fatal("empty kbRoot must disable KB mode")
	}
}
