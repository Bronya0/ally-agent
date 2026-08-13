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

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
