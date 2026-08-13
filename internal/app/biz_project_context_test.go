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

	openai "github.com/sashabaranov/go-openai"
)

func TestBuildWorkspaceMapContextRespectsIgnoresAndDetectsStack(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module example\n")
	writeTestFile(t, root, "wails.json", "{}\n")
	writeTestFile(t, root, "frontend/package.json", "{}\n")
	writeTestFile(t, root, "frontend/src/App.vue", "<template />\n")
	writeTestFile(t, root, "frontend/dist/bundle.js", "ignored\n")
	writeTestFile(t, root, "node_modules/pkg/index.js", "ignored\n")
	writeTestFile(t, root, ".env", "SECRET=1\n")
	writeTestFile(t, root, ".env.example", "SECRET=\n")
	writeTestFile(t, root, "tmp.log", "ignored\n")
	writeTestFile(t, root, "important.log", "kept\n")
	writeTestFile(t, root, "secret.txt", "ignored\n")
	writeTestFile(t, root, ".gitignore", "frontend/dist/\n*.log\n!important.log\nsecret.txt\n")

	ctx := buildWorkspaceMapContext(root)
	mustContain(t, ctx, "Detected stack: Go, Wails, Node, Vue")
	mustContain(t, ctx, "Key files:")
	mustContain(t, ctx, "go.mod")
	mustContain(t, ctx, "wails.json")
	mustContain(t, ctx, "frontend/package.json")
	mustContain(t, ctx, ".env.example")
	mustContain(t, ctx, "important.log")
	mustNotContain(t, ctx, "bundle.js")
	mustNotContain(t, ctx, "node_modules")
	mustNotContain(t, ctx, "- .env\n")
	mustNotContain(t, ctx, "tmp.log")
	mustNotContain(t, ctx, "secret.txt")
}

func TestWorkspaceMapCacheInvalidation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module example\n")

	app := NewApp()
	cfg := ConfigState{Workspace: root}
	first := app.workspaceMapContext(cfg)
	mustContain(t, first, "go.mod")

	writeTestFile(t, root, "new-file.txt", "hello\n")
	cached := app.workspaceMapContext(cfg)
	mustNotContain(t, cached, "new-file.txt")

	app.invalidateWorkspaceMapCache(cfg)
	refreshed := app.workspaceMapContext(cfg)
	mustContain(t, refreshed, "new-file.txt")
}

func TestBuildMessagesInjectsWorkspaceMapAsHiddenSystemContext(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module example\n")

	app := NewApp()
	cfg := ConfigState{Workspace: root}
	messages := app.buildMessages(ChatRequest{SessionID: "s1", Message: "hello"}, cfg, nil)

	found := false
	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "# Workspace Map") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected hidden workspace map system message in %#v", messages)
	}

	app.saveHistory("s1", messages)
	for _, msg := range app.histories["s1"] {
		if strings.Contains(msg.Content, "# Workspace Map") {
			t.Fatalf("workspace map leaked into saved history: %#v", msg)
		}
	}
}

func TestSaveHistoryPreservesCompactToolActivityForLaterTurns(t *testing.T) {
	app := NewApp()
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system prompt"},
		{Role: openai.ChatMessageRoleUser, Content: "项目里ally出现了几次"},
		{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "Let me search the codebase.",
			ToolCalls: []openai.ToolCall{{
				ID:   "call_1",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "grep_files",
					Arguments: `{"pattern":"ally","includeIgnored":true}`,
				},
			}},
		},
		{
			Role:       openai.ChatMessageRoleTool,
			ToolCallID: "call_1",
			Content:    `{"ok":false,"error":"json: unknown field \"includeIgnored\""}`,
		},
		{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "Let me retry with supported arguments.",
			ToolCalls: []openai.ToolCall{{
				ID:   "call_2",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "grep_files",
					Arguments: `{"pattern":"ally"}`,
				},
			}},
		},
		{
			Role:       openai.ChatMessageRoleTool,
			ToolCallID: "call_2",
			Content:    `{"ok":true,"data":{"matches":[],"count":46,"files":13,"truncated":false}}`,
		},
		{Role: openai.ChatMessageRoleAssistant, Content: "找到 46 处匹配。"},
	}

	app.saveHistory("s1", messages)

	foundToolCall := false
	foundToolResult := false
	for _, msg := range app.histories["s1"] {
		if len(msg.ToolCalls) > 0 && msg.ToolCalls[0].Function.Name == "grep_files" {
			foundToolCall = true
		}
		if msg.Role == openai.ChatMessageRoleTool && strings.Contains(msg.Content, `"count":46`) {
			foundToolResult = true
		}
	}
	if !foundToolCall {
		t.Fatalf("saved history should preserve raw assistant tool calls: %#v", app.histories["s1"])
	}
	if !foundToolResult {
		t.Fatalf("saved history should preserve raw tool results: %#v", app.histories["s1"])
	}

	next := app.buildMessages(ChatRequest{SessionID: "s1", Message: "为啥你grep了好几次"}, ConfigState{}, nil)
	combined := joinMessageContents(next)
	mustContain(t, combined, "grep_files")
	mustContain(t, combined, "unknown field")
	mustContain(t, combined, `"count":46`)
}

func joinMessageContents(messages []openai.ChatCompletionMessage) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		parts = append(parts, msg.Content)
		for _, call := range msg.ToolCalls {
			parts = append(parts, call.Function.Name)
			parts = append(parts, call.Function.Arguments)
		}
	}
	return strings.Join(parts, "\n")
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustContain(t *testing.T, text, needle string) {
	t.Helper()
	if !strings.Contains(text, needle) {
		t.Fatalf("expected text to contain %q\n%s", needle, text)
	}
}

func mustNotContain(t *testing.T, text, needle string) {
	t.Helper()
	if strings.Contains(text, needle) {
		t.Fatalf("expected text not to contain %q\n%s", needle, text)
	}
}
