// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"testing"
)

func TestToolResultNormalizesOnceAndRefreshesOnEvent(t *testing.T) {
	a := &desktop{}
	conv := &conversation{}
	raw := `{"ok":false,"errorCode":"DENIED","data":{"output":"` + strings.Repeat("x", 64000) + `"}}`
	a.applyToolEvent(conv, "tool:result", map[string]any{"toolCallId": "call", "name": "read", "result": raw})
	m := conv.Messages[0]
	if _, ok := m["result"].(map[string]any); !ok {
		t.Fatal("wire JSON retained")
	}
	if _, exists := m["body"]; exists {
		t.Fatal("duplicate wire body retained")
	}
	if toolState(m) != "error" || str(toolResult(m), "errorCode") != "DENIED" {
		t.Fatal("envelope failure lost")
	}
	a.applyToolEvent(conv, "tool:result", map[string]any{"toolCallId": "call", "name": "read", "result": `{"ok":true,"data":{"output":"new"}}`})
	if toolState(m) != "success" || str(toolResult(m), "output") != "new" || str(toolResult(m), "errorCode") != "" {
		t.Fatal("old result reused after update")
	}
}

func TestToolResultLegacyAndRoundTrip(t *testing.T) {
	for _, m := range []map[string]any{
		{"body": "plain legacy output"},
		{"result": "null"},
		{"result": `{"ok":false,"data":{"ok":true,"error":"detail"}}`},
	} {
		normalizeToolMessage(m)
		first := toolResult(m)
		normalizeToolMessage(m)
		if len(toolResult(m)) != len(first) {
			t.Fatal("normalizing twice changed result")
		}
	}
	m := map[string]any{"result": `{"ok":false,"data":{"output":"saved"}}`}
	normalizeToolMessage(m)
	saved := snapshot(&conversation{Messages: []map[string]any{m}})
	restored := saved.Messages[0]
	normalizeToolMessage(restored)
	if str(toolResult(restored), "output") != "saved" || toolResult(restored)["ok"] != false {
		t.Fatal("snapshot lost normalized result")
	}
}

func TestRememberSessionKeepsOnlyLatestDisposableTranscript(t *testing.T) {
	draft := &conversation{ID: "draft", Draft: "unsent"}
	pending := &conversation{ID: "loading", AttachmentLoading: true}
	busy := &conversation{ID: "running", Busy: true}
	old := &conversation{ID: "old", Messages: []map[string]any{{"content": "saved"}}}
	a := &desktop{openSessions: map[string]*conversation{"draft": draft, "loading": pending, "running": busy, "old": old}}
	latest := &conversation{ID: "latest"}
	a.rememberSession(latest)
	if a.openSessions["old"] != nil || a.openSessions["latest"] != latest {
		t.Fatal("older clean transcript retained")
	}
	for _, t1 := range []*conversation{draft, pending, busy} {
		if a.openSessions[t1.ID] != t1 {
			t.Fatalf("lost live state: %s", t1.ID)
		}
	}
}
