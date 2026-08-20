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
	"os"
	"testing"

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
	if len(loaded) != 3 {
		t.Fatalf("expected 3 loaded messages, got %d: %#v", len(loaded), loaded)
	}
	repaired := loaded[1].ToolCalls[0].Function.Arguments
	if repaired != truncatedToolCallArguments || !json.Valid([]byte(repaired)) {
		t.Fatalf("truncated arguments were not repaired on load: %q", repaired)
	}
	if loaded[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool call ID must survive the repair: %#v", loaded[1].ToolCalls[0])
	}
	if loaded[2].Role != openai.ChatMessageRoleTool || loaded[2].ToolCallID != "call_1" {
		t.Fatalf("paired tool result must survive the repair: %#v", loaded[2])
	}

	// A save through the current build must persist the repaired arguments,
	// so the on-disk copy stops carrying the poison.
	app.saveHistory(sessionID, poisoned)
	app.mu.Lock()
	delete(app.histories, sessionID)
	app.mu.Unlock()
	reloaded := app.loadSessionHistoryCopy(sessionID)
	if len(reloaded) != 3 || reloaded[1].ToolCalls[0].Function.Arguments != truncatedToolCallArguments {
		t.Fatalf("saveHistory persisted truncated arguments: %#v", reloaded)
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
