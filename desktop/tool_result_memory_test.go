// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHistoryRewritesReleaseRetiredStreamingAnswers(t *testing.T) {
	for _, operation := range []string{"replace", "fast", "delete"} {
		t.Run(operation, func(t *testing.T) {
			conv := &conversation{ID: "rewrite"}
			body := appendStreamChunk(conv.uiState(), 0, "", strings.Repeat("old answer\n", 8000))
			conv.Messages = []map[string]any{{"role": "assistant", "content": body}}
			switch operation {
			case "replace":
				replaceTranscript(conv, "summary")
			case "fast":
				applyFastCompaction(conv, map[string]any{"assistantEdits": []any{map[string]any{"before": body, "fromEnd": 1, "after": "excerpt"}}})
			case "delete":
				conv.Messages = nil
				resetTranscriptAfterDelete(conv, 0)
			}
			if conv.uiState().Stream != nil {
				t.Fatal("rewritten history retained the retired answer buffer")
			}
			before := ""
			if len(conv.Messages) > 0 {
				before = str(conv.Messages[0], "content")
			}
			if got := appendStreamChunk(conv.uiState(), 0, before, " next"); got != before+" next" {
				t.Fatal("streaming after a history rewrite reused the old answer")
			}
		})
	}
}

func TestSnapshotOmitsTransientOutputsAndPreservesCardMetadata(t *testing.T) {
	body := strings.Repeat("STALE_RESULT_BODY", 8000)
	for _, tc := range []struct {
		name   string
		result map[string]any
	}{
		{"read", map[string]any{"ok": true, "files": []any{map[string]any{"path": "main.go", "startLine": 1, "endLine": 40, "content": body, "rawContent": body}}}},
		{"remote_read", map[string]any{"startLine": 1, "endLine": 40, "content": body}},
		{"batch_read", map[string]any{"files": []any{map[string]any{"path": "main.go", "totalLines": 40, "content": body}}}},
		{"grep", map[string]any{"matches": []any{map[string]any{"path": "main.go", "lines": []any{map[string]any{"text": body}, map[string]any{"text": body}}}}}},
		{"list_files", map[string]any{"entries": []any{map[string]any{"path": body}}, "truncated": true}},
		{"Glob", map[string]any{"matches": []any{body}, "truncated": true}},
		{"remote_run_command", map[string]any{"output": body, "stdout": body, "exitCode": 2, "durationMs": 12}},
		{"command", map[string]any{"output": body, "stderr": body, "exitCode": 2, "durationMs": 12}},
		{"Bash", map[string]any{"stdout": body, "exitCode": 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]any{"role": "tool_call", "toolCallId": "call", "name": tc.name, "args": `{"path":"main.go"}`, "status": "success", "result": tc.result}
			before, _ := json.Marshal(m)
			conv := &conversation{ID: "snapshot", Messages: []map[string]any{m}}
			saved := snapshot(conv)
			after, _ := json.Marshal(m)
			if string(before) != string(after) {
				t.Fatal("snapshot mutated live evidence")
			}
			encoded, _ := json.Marshal(saved)
			if strings.Contains(string(encoded), body) || len(encoded) > 2048 {
				t.Fatal("raw observation persisted")
			}
			restored := saved.Messages[0]
			if str(restored, "toolCallId") != "call" || str(restored, "args") != str(m, "args") || toolState(restored) != toolState(m) {
				t.Fatal("identity, arguments or status lost")
			}
			name := toolKind(tc.name)
			if name == "command" || name == "remote_run_command" {
				if num(toolResult(restored), "exitCode") != 2 {
					t.Fatal("exit metadata lost")
				}
			} else if toolGroupResultSummary(name, toolResult(restored)) != toolGroupResultSummary(name, toolResult(m)) {
				t.Fatal("summary changed after restart")
			}
			if files := list(toolResult(restored), "files"); len(files) > 0 && str(files[0].(map[string]any), "path") != "main.go" {
				t.Fatal("batch file identity lost")
			}
		})
	}
}

func TestSnapshotObservationEnvelopeErrorsAndExplicitExcerpts(t *testing.T) {
	m := map[string]any{"name": "read", "toolCallId": "call", "result": map[string]any{"ok": false, "errorCode": "DENIED", "data": map[string]any{"content": strings.Repeat("stale", 10000), "error": "cannot read"}}}
	before, _ := json.Marshal(m)
	saved := snapshot(&conversation{Messages: []map[string]any{m}}).Messages[0]
	after, _ := json.Marshal(m)
	if string(before) != string(after) {
		t.Fatal("envelope merge mutated live result")
	}
	result := toolResult(saved)
	if result["ok"] != false || str(result, "errorCode") != "DENIED" || str(result, "error") != "cannot read" || str(result, "content") != "" {
		t.Fatal("error metadata lost or stale body retained")
	}
	for _, name := range []string{"read", "edit", "create", "plan"} {
		m := map[string]any{"name": name, "result": map[string]any{"_text": "explicit excerpt"}, "body": "explicit excerpt", "fastCompacted": true}
		saved := snapshot(&conversation{Messages: []map[string]any{m}}).Messages[0]
		if str(toolResult(saved), "_text") != "explicit excerpt" {
			t.Fatal("bounded compaction or non-transient result lost")
		}
	}
}

func TestLegacyObservationSnapshotsKeepFailuresWithoutBodies(t *testing.T) {
	for _, name := range []string{"read", "batch_read", "remote_run_command", "command"} {
		for _, wire := range []string{
			`{"ok":false,"error":"cannot access","errorCode":"DENIED","data":{"output":"STALE_BODY","cancelled":true,"files":[{"path":"main.go","error":"cannot read","content":"STALE_BODY"}],"missing":[{"path":"gone.go","errorCode":"NOT_FOUND"}]}}`,
			"STALE_BODY",
		} {
			m := map[string]any{"role": "tool_call", "toolName": name, "toolCallId": "old", "toolState": "tool:error", "error": "cannot access", "body": wire}
			normalizeToolMessage(m)
			state := toolState(m)
			retainSnapshotToolSummary(m)
			encoded, _ := json.Marshal(m)
			if strings.Contains(string(encoded), "STALE_BODY") || toolState(m) != state || str(m, "error") == "" {
				t.Fatal("legacy output retained or failure/cancellation lost")
			}
			result := toolResult(m)
			if strings.HasPrefix(wire, "{") {
				if !boolOf(result, "cancelled") || str(result, "errorCode") != "DENIED" || str(result, "error") != "cannot access" {
					t.Fatal("envelope error metadata lost")
				}
				if name == "read" || name == "batch_read" {
					missing := list(result, "missing")
					if len(missing) != 1 || str(missing[0].(map[string]any), "path") != "gone.go" {
						t.Fatal("missing file identity lost")
					}
				}
			}
			retainSnapshotToolSummary(m)
			again, _ := json.Marshal(m)
			if string(encoded) != string(again) {
				t.Fatal("summary changed on repeated save/load")
			}
		}
	}
}

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
