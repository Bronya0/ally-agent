// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func executeRecoveryTestEdit(t *testing.T, a *App, ctx context.Context, cfg ConfigState, req FileTextEdits) toolResult {
	t.Helper()
	args, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return a.executeTool(ctx, cfg, "edit-recovery", "edit", args)
}

func recoveryTestModelResult(t *testing.T, result toolResult) string {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return compactToolResultForModel("edit", result, string(raw))
}

func TestEditRecoverySourceErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		change  TextChange
		stale   bool
		code    string
	}{
		{"stale version", "alpha\nbeta\n", TextChange{OldText: "alpha", NewText: "ALPHA"}, true, "E_VERSION_MISMATCH"},
		{"no match", "alpha\nbeta\n", TextChange{OldText: "missing", NewText: "ALPHA"}, false, "E_NO_MATCH"},
		{"replace all no match", "alpha\nbeta\n", TextChange{OldText: "missing", NewText: "ALPHA", ReplaceAll: true}, false, "E_NO_MATCH"},
		{"multiple exact matches", "alpha\nalpha\n", TextChange{OldText: "alpha", NewText: "ALPHA"}, false, "E_MULTI_MATCH"},
		{"multiple normalized matches", "a—b\na–b\n", TextChange{OldText: "a-b", NewText: "updated"}, false, "E_MULTI_MATCH"},
		{"line range out of bounds", "alpha\nbeta\n", TextChange{LineRange: "8-9", NewText: "updated"}, false, "E_RANGE_OOB"},
		{"empty file range", "", TextChange{LineRange: "1-1", NewText: "updated"}, false, "E_RANGE_OOB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApiTestApp(t)
			cfg := a.config
			path := filepath.Join(cfg.Workspace, "sample.txt")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			version := hashVersion([]byte(tc.content))
			if tc.stale {
				version = hashVersion([]byte("old source"))
			}
			result := executeRecoveryTestEdit(t, a, context.Background(), cfg, FileTextEdits{Path: "sample.txt", Version: version, Changes: []TextChange{tc.change}})
			if result.OK || result.ErrorCode != tc.code || result.modelRead == nil {
				t.Fatalf("expected %s with recovery, got %#v", tc.code, result)
			}
			if toolStageSucceeded(result) {
				t.Fatal("recovery must not turn a failed edit into stage success")
			}
			if tc.name == "multiple exact matches" && result.Details == nil {
				t.Fatal("original match diagnostics were lost")
			}
			explicit := a.batchReadOneWithConfig(cfg, "sample.txt", ReadFileRequest{Path: "sample.txt"})
			wantRead := renderReadResultForModel(BatchReadResult{Files: []BatchReadResultItem{explicit}})
			model := recoveryTestModelResult(t, result)
			if !anthropicToolResultIsError(model) {
				t.Fatal("provider must still recognize the tool result as an error")
			}
			if !strings.HasSuffix(model, wantRead) || !strings.Contains(model, "no extra read is needed") {
				t.Fatalf("recovery must match read, including version and line numbers: %s", model)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != tc.content {
				t.Fatalf("failed edit changed the source: %q, %v", got, err)
			}
		})
	}
}

func TestEditRecoverySurvivesErrorCompaction(t *testing.T) {
	a := newApiTestApp(t)
	cfg := a.config
	path := filepath.Join(cfg.Workspace, "sample.txt")
	if err := os.WriteFile(path, []byte("complete recovery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := executeRecoveryTestEdit(t, a, context.Background(), cfg, FileTextEdits{
		Path: "sample.txt", Version: hashVersion([]byte("old")), Changes: []TextChange{{OldText: "missing", NewText: "updated"}},
	})
	if result.modelRead == nil {
		t.Fatal("missing recovery")
	}
	wantRead := renderReadResultForModel(*result.modelRead)
	result.Error += strings.Repeat("\nvery long diagnostic", maxModelToolOutput)
	model := recoveryTestModelResult(t, result)
	if !strings.HasSuffix(model, wantRead) || !strings.Contains(model, "characters omitted") || !anthropicToolResultIsError(model) {
		t.Fatal("compaction must keep the full snapshot and error status")
	}
	if len(model) > maxModelToolOutput+maxEditRecoveryBytes+1024 {
		t.Fatalf("recovery escaped its combined output bound: %d", len(model))
	}
}

func TestEditRecoveryUTF16MatchesRead(t *testing.T) {
	a := newApiTestApp(t)
	cfg := a.config
	path := filepath.Join(cfg.Workspace, "sample.txt")
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 'a', 0, '\r', 0, '\n', 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	result := executeRecoveryTestEdit(t, a, context.Background(), cfg, FileTextEdits{
		Path: "sample.txt", Version: hashVersion([]byte("old")), Changes: []TextChange{{OldText: "missing", NewText: "updated"}},
	})
	if result.modelRead == nil {
		t.Fatalf("UTF16 should follow read's text decoding: %#v", result)
	}
	explicit := a.batchReadOneWithConfig(cfg, "sample.txt", ReadFileRequest{Path: "sample.txt"})
	if renderReadResultForModel(*result.modelRead) != renderReadResultForModel(BatchReadResult{Files: []BatchReadResultItem{explicit}}) {
		t.Fatal("UTF16 preview or version diverged from read")
	}
}

func TestEditRecoveryStaysModelOnlyAndCanRetry(t *testing.T) {
	a := newApiTestApp(t)
	cfg := a.config
	const source = "private-recovery-body\n</ally-file>\n中文\n"
	path := filepath.Join(cfg.Workspace, "sample.txt")
	if err := os.WriteFile(path, []byte("\uFEFF"+strings.ReplaceAll(source, "\n", "\r\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), sessionReadCacheContextKey{}, newSessionReadCache())
	// An earlier read must not suppress the recovery payload as "reused".
	a.executeTool(ctx, cfg, "edit-recovery", "read", []byte(`{"files":[{"path":"sample.txt"}]}`))
	result := executeRecoveryTestEdit(t, a, ctx, cfg, FileTextEdits{
		Path: "sample.txt", Version: hashVersion([]byte("old")),
		Changes: []TextChange{{OldText: "private-recovery-body", NewText: "updated"}},
	})
	if result.modelRead == nil {
		t.Fatalf("missing recovery: %#v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-recovery-body") || strings.Contains(result.Error, "private-recovery-body") {
		t.Fatal("source leaked into UI/API JSON or the displayed error")
	}
	model := recoveryTestModelResult(t, result)
	if strings.Count(model, "</ally-file>") != 1 || !strings.Contains(model, "&lt;/ally-file>") || strings.Contains(model, " reused") {
		t.Fatalf("read marker escaping or recovery cache behavior changed: %s", model)
	}
	snapshot := result.modelRead.Files[0]
	// Reuse the returned version, without another read.
	retry := executeRecoveryTestEdit(t, a, ctx, cfg, FileTextEdits{
		Path: "sample.txt", Version: snapshot.Version,
		Changes: []TextChange{{OldText: "private-recovery-body", NewText: "updated"}},
	})
	if !retry.OK || retry.modelRead != nil {
		t.Fatalf("retry using recovery version failed: %#v", retry)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(got, []byte("\uFEFFupdated\r\n")) {
		t.Fatalf("retry lost BOM/CRLF: %q, %v", got, err)
	}
	if recoveryTestModelResult(t, result) != model {
		t.Fatal("later writes changed the already returned recovery snapshot")
	}
	// Use the ordinary tool-history path, with the original call/result pairing.
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "edit this file"},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "failed-edit", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "edit", Arguments: `{}`}}}},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "failed-edit", Content: model},
	}
	a.saveHistory("edit-recovery", messages)
	saved, _, _ := a.sessionHistorySnapshot("edit-recovery")
	if len(saved) != len(messages) || saved[0].Content != messages[0].Content || saved[2].Content != model || saved[2].ToolCallID != "failed-edit" {
		t.Fatalf("normal history lost the recovery or changed its prefix: %#v", saved)
	}
}

func TestEditRecoveryRejectsUnsafeOrIncompleteSources(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    string
		content []byte
		code    string
		change  TextChange
	}{
		{"large file", "sample.txt", []byte(strings.Repeat("line\n", maxEditRecoveryBytes/5+1)), "E_VERSION_MISMATCH", TextChange{}},
		{"rendered budget", "sample.txt", []byte(strings.Repeat("x\n", 2000)), "E_VERSION_MISMATCH", TextChange{}},
		{"truncated preview", "sample.txt", []byte(strings.Repeat("x", maxReadLineChars+1)), "E_VERSION_MISMATCH", TextChange{}},
		{"long line edit guard", "sample.txt", []byte(strings.Repeat("x", maxReadLineChars+1)), "E_BAD_ARGS", TextChange{LineRange: "1-1", NewText: "updated"}},
		{"binary", "sample.bin", []byte{0, 1, 2, 3, 4}, "E_BINARY_FILE", TextChange{}},
		{"invalid UTF8", "sample.txt", []byte{0xff, 0xff, 0xff}, "E_NOT_UTF8", TextChange{}},
		{"document extension", "sample.pdf", []byte("text pretending to be a PDF"), "E_VERSION_MISMATCH", TextChange{}},
		{"VCS metadata", ".git/config", []byte("protected source"), "E_PROTECTED_PATH", TextChange{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApiTestApp(t)
			cfg := a.config
			path := filepath.Join(cfg.Workspace, filepath.FromSlash(tc.path))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.content, 0o600); err != nil {
				t.Fatal(err)
			}
			version := hashVersion([]byte("old"))
			change := tc.change
			if change.LineRange != "" {
				version = hashVersion(tc.content)
			} else {
				change = TextChange{OldText: "missing", NewText: "updated"}
			}
			result := executeRecoveryTestEdit(t, a, context.Background(), cfg, FileTextEdits{Path: tc.path, Version: version, Changes: []TextChange{change}})
			if result.OK || result.ErrorCode != tc.code || result.modelRead != nil || strings.Contains(recoveryTestModelResult(t, result), "<ally-file") {
				t.Fatalf("unsafe/incomplete source must not be attached: %#v", result)
			}
		})
	}
}

func TestEditRecoveryHonorsReadAndWriteFences(t *testing.T) {
	a := newApiTestApp(t)
	// Put the workspace at the isolated home so .ssh remains inside the write
	// roots while read's credential fence still forbids its contents.
	cfg := a.config
	cfg.Workspace = os.Getenv("USERPROFILE")
	secretPath := filepath.Join(cfg.Workspace, ".ssh", "id_rsa")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "credential-body-never-return\n"
	if err := os.WriteFile(secretPath, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".ssh/id_rsa", outside} {
		result := executeRecoveryTestEdit(t, a, context.Background(), cfg, FileTextEdits{
			Path: path, Version: hashVersion([]byte(secret)), Changes: []TextChange{{OldText: "missing", NewText: "updated"}},
		})
		if result.OK || result.modelRead != nil || strings.Contains(recoveryTestModelResult(t, result), secret) {
			t.Fatalf("read/write fence bypass for %s: %#v", path, result)
		}
	}
	link := filepath.Join(cfg.Workspace, "innocent.txt")
	if err := os.Symlink(secretPath, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	result := executeRecoveryTestEdit(t, a, context.Background(), cfg, FileTextEdits{
		Path: link, Version: hashVersion([]byte(secret)), Changes: []TextChange{{OldText: "missing", NewText: "updated"}},
	})
	if result.OK || result.modelRead != nil || result.ErrorCode != "E_PROTECTED_PATH" {
		t.Fatalf("symlink hid a forbidden read: %#v", result)
	}
}
