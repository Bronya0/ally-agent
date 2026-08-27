// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestSessionFilesUseIndexAndOnDemandSnapshot(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.historiesDir = t.TempDir()

	const sessionID = "session/one"
	snapshot := SessionSnapshot{
		ID:            sessionID,
		Title:         "Build session",
		Workspace:     "/tmp/workspace",
		CreatedAt:     100,
		UpdatedAt:     200,
		ContextTokens: 42,
		Messages: []map[string]any{
			{"role": "user", "content": "hello from disk"},
			{"role": "assistant", "content": "answer"},
		},
	}
	if err := app.SaveSession(snapshot); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}

	entries, err := app.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(entries) != 1 || entries[0].ID != sessionID {
		t.Fatalf("unexpected session index: %#v", entries)
	}
	indexData, err := os.ReadFile(app.sessionIndexPath())
	if err != nil {
		t.Fatalf("read session index: %v", err)
	}
	// The index may store a short FirstPrompt preview (used by the session
	// list UI), but must not contain the full conversation message bodies
	// or assistant replies — those live only in the compressed snapshot.
	if bytes.Contains(indexData, []byte(`"messages"`)) {
		t.Fatal("session index must not contain conversation message bodies")
	}
	if bytes.Contains(indexData, []byte("answer")) {
		t.Fatal("session index must not contain assistant reply bodies")
	}

	loaded, err := app.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}
	if loaded.Title != snapshot.Title || len(loaded.Messages) != 2 || loaded.Messages[0]["content"] != "hello from disk" {
		t.Fatalf("unexpected loaded snapshot: %#v", loaded)
	}

	if err := app.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	if _, err := os.Stat(app.sessionSnapshotPath(sessionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session snapshot still exists after delete, stat error = %v", err)
	}
	entries, err = app.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() after delete error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("deleted session remains in index: %#v", entries)
	}
}

func TestListSessionsRecoversSnapshotWhenIndexIsMissing(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.historiesDir = t.TempDir()

	snapshot := SessionSnapshot{
		ID:        "orphaned-snapshot",
		Title:     "Recovered",
		CreatedAt: 100,
		UpdatedAt: 100,
		Messages:  []map[string]any{{"role": "user", "content": "recover me"}},
	}
	if err := writeCompressedJSONAtomic(app.sessionSnapshotPath(snapshot.ID), mustJSON(snapshot)); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	entries, err := app.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(entries) != 1 || entries[0].ID != snapshot.ID || !entries[0].HasSnapshot {
		t.Fatalf("snapshot was not recovered into index: %#v", entries)
	}
}

func TestHistoryLoadRepairsTruncatedToolCallArguments(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.historiesDir = t.TempDir()
	const sessionID = "poisoned"

	// Simulate a history persisted by a build without the truncation repair:
	// the provider stream was cut off mid-arguments, so the saved assistant
	// tool call carries an invalid JSON string that makes providers with
	// server-side arguments parsing reject every subsequent request.
	poisoned := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "fix AGENTS.md"},
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{
				ID:       "call_1",
				Type:     openai.ToolTypeFunction,
				Function: openai.FunctionCall{Name: "edit", Arguments: `{"files":[{"path":"AGENTS.md","changes":[{"oldText":"sandbox is`},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_1", Content: `{"ok":false,"error":"tool arguments JSON was truncated"}`},
	}
	paths := app.historyDiskPaths(sessionID)
	if err := writeCompressedHistory(paths[0], poisoned); err != nil {
		t.Fatalf("writeCompressedHistory() error = %v", err)
	}

	loaded := app.loadSessionHistoryCopy(sessionID)
	// Truncated-arguments tool calls are now dropped entirely (along with
	// their orphan tool result) so the session can continue without the
	// provider rejecting every request.
	if len(loaded) != 1 {
		t.Fatalf("expected only the user message to survive, got %d: %#v", len(loaded), loaded)
	}
	if loaded[0].Role != openai.ChatMessageRoleUser || loaded[0].Content != "fix AGENTS.md" {
		t.Fatalf("the user message must survive, got %#v", loaded[0])
	}
}

func TestHistoryLoadCollapsesRepeatedToolCallNames(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.historiesDir = t.TempDir()
	const sessionID = "relay-duplicate-name"

	// Exactly what the relay-duplicate-name bug persisted: the merged name is
	// "http_request" repeated 7 times, the tool result explains the unknown
	// tool, and every later request for the session died with provider 400.
	repeated := strings.Repeat("http_request", 7)
	poisoned := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "check the latest release"},
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{
				ID:   "call_dup",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      repeated,
					Arguments: `{"url":"https://api.github.com/repos/example/repo/releases/latest"}`,
				},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_dup", Content: "unknown tool: " + repeated},
	}
	paths := app.historyDiskPaths(sessionID)
	if err := writeCompressedHistory(paths[0], poisoned); err != nil {
		t.Fatalf("writeCompressedHistory() error = %v", err)
	}

	loaded := app.loadSessionHistoryCopy(sessionID)
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages, got %d: %#v", len(loaded), loaded)
	}
	calls := loaded[1].ToolCalls
	if len(calls) != 1 || calls[0].Function.Name != "http_request" {
		t.Fatalf("repeated tool name must collapse on load: %#v", calls)
	}
	if calls[0].ID != "call_dup" || loaded[2].ToolCallID != "call_dup" {
		t.Fatalf("pairing must survive the collapse: %#v", loaded)
	}
}

func TestHistoryLoadDropsUnknownToolCallNames(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.historiesDir = t.TempDir()
	const sessionID = "concatenated-name"

	// What the same-Index merge bug persisted: "read" + "list_files" was
	// concatenated into "readlist_files", which is not a known tool.
	poisoned := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "go"},
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{
				ID:   "call_concat",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "readlist_files",
					Arguments: `{"files":[{"path":"a.txt"}]}`,
				},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_concat", Content: "unknown tool: readlist_files"},
	}
	paths := app.historyDiskPaths(sessionID)
	if err := writeCompressedHistory(paths[0], poisoned); err != nil {
		t.Fatalf("writeCompressedHistory() error = %v", err)
	}

	loaded := app.loadSessionHistoryCopy(sessionID)
	// The unknown tool_call and its orphan tool result must both be gone.
	for _, m := range loaded {
		if m.Role == openai.ChatMessageRoleAssistant {
			for _, call := range m.ToolCalls {
				if call.Function.Name == "readlist_files" {
					t.Fatalf("unknown tool name must be dropped on load: %#v", call)
				}
			}
		}
		if m.Role == openai.ChatMessageRoleTool && m.ToolCallID == "call_concat" {
			t.Fatalf("orphan tool result for dropped call must be removed: %#v", m)
		}
	}
}

func TestHistoryLoadDropsTruncatedArgsMarker(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.historiesDir = t.TempDir()
	const sessionID = "truncated-mcp"

	// What a truncated MCP tool call persisted: normalizeToolCalls replaced
	// the cut-off arguments with {"allyTruncatedArguments":true}, the MCP
	// server returned an error, and the pair poisoned the session.
	poisoned := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "search the web"},
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{
				ID:   "call_trunc",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "mcp__web__global_search",
					Arguments: `{"allyTruncatedArguments":true}`,
				},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_trunc", Content: "MCP call failed: query参数必须提供"},
	}
	paths := app.historyDiskPaths(sessionID)
	if err := writeCompressedHistory(paths[0], poisoned); err != nil {
		t.Fatalf("writeCompressedHistory() error = %v", err)
	}

	loaded := app.loadSessionHistoryCopy(sessionID)
	for _, m := range loaded {
		if m.Role == openai.ChatMessageRoleAssistant {
			for _, call := range m.ToolCalls {
				if isTruncatedArgsMarker([]byte(call.Function.Arguments)) {
					t.Fatalf("truncated-args marker must be dropped on load: %#v", call)
				}
			}
		}
		if m.Role == openai.ChatMessageRoleTool && m.ToolCallID == "call_trunc" {
			t.Fatalf("orphan tool result for truncated call must be removed: %#v", m)
		}
	}
}

func TestRepairDanglingToolCalls(t *testing.T) {
	assistantWithCalls := func(ids ...string) openai.ChatCompletionMessage {
		calls := make([]openai.ToolCall, 0, len(ids))
		for _, id := range ids {
			calls = append(calls, openai.ToolCall{ID: id, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: "{}"}})
		}
		return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, ToolCalls: calls}
	}
	toolMsg := func(id string) openai.ChatCompletionMessage {
		return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: id, Content: `{"ok":true}`}
	}
	userMsg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "next"}

	t.Run("valid pairing passes through unchanged", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a", "b"),
			toolMsg("a"), toolMsg("b"),
			{Role: openai.ChatMessageRoleAssistant, Content: "done"},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 5 || len(out[1].ToolCalls) != 2 || out[2].ToolCallID != "a" || out[3].ToolCallID != "b" {
			t.Fatalf("valid pairing was modified: %#v", out)
		}
	})

	t.Run("trailing dangling call stripped, answered call kept", func(t *testing.T) {
		// The exact state a mid-batch panic would persist: assistant declared
		// two calls, only one result arrived before the crash.
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a", "b"),
			toolMsg("a"),
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
		}
		if len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].ID != "a" {
			t.Fatalf("dangling call b was not stripped: %#v", out[1].ToolCalls)
		}
		if out[2].ToolCallID != "a" {
			t.Fatalf("answered result must survive: %#v", out[2])
		}
	})

	t.Run("mid-history dangling closed by user message", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a"),
			userMsg,
			{Role: openai.ChatMessageRoleAssistant, Content: "after"},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
		}
		if len(out[1].ToolCalls) != 0 {
			t.Fatalf("dangling call a must be stripped when the turn closes: %#v", out[1].ToolCalls)
		}
	})

	t.Run("assistant with all calls dangling and no text dropped", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a"),
			toolMsg("a"),
			assistantWithCalls("b"),
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("empty dangling assistant must be dropped, got %d: %#v", len(out), out)
		}
	})

	t.Run("assistant text preserved when calls dangle", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			{Role: openai.ChatMessageRoleAssistant, Content: "partial stream", ToolCalls: assistantWithCalls("a").ToolCalls},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 2 || out[1].Content != "partial stream" || len(out[1].ToolCalls) != 0 {
			t.Fatalf("assistant text must survive call stripping: %#v", out)
		}
	})

	t.Run("orphan and duplicate tool messages dropped", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a"),
			toolMsg("ghost"),
			toolMsg("a"),
			toolMsg("a"),
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("orphan and duplicate results must be dropped, got %d: %#v", len(out), out)
		}
		if out[2].ToolCallID != "a" {
			t.Fatalf("first result must survive: %#v", out[2])
		}
	})
}

func TestHistoryLoadRepairsDanglingToolCalls(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.historiesDir = t.TempDir()
	const sessionID = "panic-window"

	// What a run that panicked between appending the assistant tool_calls
	// message and appending tool results would have persisted.
	poisoned := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "go"},
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{
				ID: "call_1", Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{Name: "read", Arguments: `{"files":[{"path":"a.txt"}]}`},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_1", Content: `{"ok":true,"data":{}}`},
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{
				ID: "call_2", Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{Name: "edit", Arguments: `{"files":[{"path":"a.txt","version":"000000000000","changes":[]}]}`},
			}},
		},
		// call_2 never received a result: the process died mid-batch.
	}
	paths := app.historyDiskPaths(sessionID)
	if err := writeCompressedHistory(paths[0], poisoned); err != nil {
		t.Fatalf("writeCompressedHistory() error = %v", err)
	}

	loaded := app.loadSessionHistoryCopy(sessionID)
	if len(loaded) != 3 {
		t.Fatalf("expected the dangling assistant to be dropped, got %d messages: %#v", len(loaded), loaded)
	}
	last := loaded[len(loaded)-1]
	if last.Role != openai.ChatMessageRoleTool || last.ToolCallID != "call_1" {
		t.Fatalf("history must end with the answered tool result: %#v", last)
	}
	for _, m := range loaded {
		for _, call := range m.ToolCalls {
			answered := false
			for _, r := range loaded {
				if r.Role == openai.ChatMessageRoleTool && r.ToolCallID == call.ID {
					answered = true
				}
			}
			if !answered {
				t.Fatalf("loaded history still carries dangling tool call %s", call.ID)
			}
		}
	}
}

func TestSessionIndexUnreadableDegradesToEmpty(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.historiesDir = t.TempDir()

	snapshot := SessionSnapshot{
		ID:        "snapshot-survivor",
		Title:     "Recovered from disk",
		Workspace: "/tmp/workspace",
		CreatedAt: 100,
		UpdatedAt: 200,
		Messages: []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}
	if err := app.SaveSession(snapshot); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}

	// Simulate an unreadable index (legacy oversized file, truncated write,
	// manual corruption). ListSessions must degrade to the empty-index path
	// instead of failing every session operation.
	if err := os.WriteFile(app.sessionIndexPath(), []byte("not json at all"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	entries, err := app.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() must not fail on an unreadable index: %v", err)
	}
	found := false
	for _, entry := range entries {
		if entry.ID == "snapshot-survivor" && entry.HasSnapshot {
			found = true
		}
	}
	if !found {
		t.Fatalf("session snapshot on disk must be rediscovered after index loss: %#v", entries)
	}
	// The next index write replaces the corrupted file with a healthy one.
	if _, err := app.ListSessions(); err != nil {
		t.Fatalf("second ListSessions() error = %v", err)
	}
	reloaded, err := app.readSessionIndexLocked()
	if err != nil {
		t.Fatalf("read rebuilt index: %v", err)
	}
	if len(reloaded) == 0 {
		t.Fatal("rebuilt index must contain rediscovered entries")
	}
}

func TestSessionIndexWritePrunesOldestEntries(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.historiesDir = t.TempDir()

	base := time.Now().Add(-time.Hour).UnixMilli()
	for i := 0; i <= maxSessionIndexEntries; i++ {
		entry := SessionIndexEntry{
			ID:        fmt.Sprintf("s-%04d", i),
			Title:     fmt.Sprintf("Session %d", i),
			Workspace: "/tmp/workspace",
			CreatedAt: base + int64(i)*1000,
			UpdatedAt: base + int64(i)*1000,
		}
		if err := app.SaveSessionIndex(entry); err != nil {
			t.Fatalf("SaveSessionIndex(%d) error = %v", i, err)
		}
	}

	entries, err := app.readSessionIndexLocked()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if len(entries) != maxSessionIndexEntries {
		t.Fatalf("index size after pruning = %d, want %d", len(entries), maxSessionIndexEntries)
	}
	// entries are sorted newest-first: everything except the oldest (s-0000)
	// must survive.
	ids := make(map[string]bool, len(entries))
	for _, entry := range entries {
		ids[entry.ID] = true
	}
	if ids["s-0000"] {
		t.Fatal("oldest entry should have been evicted first")
	}
	if !ids[fmt.Sprintf("s-%04d", maxSessionIndexEntries)] {
		t.Fatal("newest entry must survive eviction")
	}
}

func TestNormalizeSessionIndexEntryClampsFirstPrompt(t *testing.T) {
	longPrompt := strings.Repeat("很长的提问字符", 200) // > maxSessionIndexFirstPromptChars runes
	entry := normalizeSessionIndexEntry(SessionIndexEntry{
		ID:          "prompt-clamp",
		Title:       "Prompt clamp",
		FirstPrompt: longPrompt,
	})
	if got := []rune(entry.FirstPrompt); len(got) > maxSessionIndexFirstPromptChars {
		t.Fatalf("FirstPrompt length = %d, want <= %d", len(got), maxSessionIndexFirstPromptChars)
	}
	if !strings.HasSuffix(entry.FirstPrompt, "\u2026") {
		t.Fatalf("clamped FirstPrompt must end with an ellipsis: %q", entry.FirstPrompt[len(entry.FirstPrompt)-3:])
	}
	// Long multi-line input collapses to single-spaced single line.
	messy := normalizeSessionIndexEntry(SessionIndexEntry{
		ID:          "prompt-messy",
		FirstPrompt: "hello \n\n world \t tab",
	}).FirstPrompt
	if messy != "hello world tab" {
		t.Fatalf("whitespace-collapsed FirstPrompt = %q", messy)
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
