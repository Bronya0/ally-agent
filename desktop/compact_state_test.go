package desktop

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFastCompactionPreservesToolOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		row   map[string]any
		state string
		meta  map[string]any
	}{
		{"error", map[string]any{"status": "error", "error": "process failed", "errorCode": "E_EXIT", "result": map[string]any{"exitCode": 2}}, "error", map[string]any{"exitCode": 2}},
		{"legacy failure", map[string]any{"toolState": "tool:error", "error": "cannot read", "result": map[string]any{"errorCode": "E_READ"}}, "error", map[string]any{"errorCode": "E_READ"}},
		{"envelope failure", map[string]any{"status": "success", "result": map[string]any{"ok": false, "error": "denied", "errorCode": "E_DENIED", "data": map[string]any{"exitCode": 3}}}, "error", map[string]any{"ok": false, "error": "denied", "errorCode": "E_DENIED", "exitCode": 3}},
		{"cancelled status", map[string]any{"status": "cancelled", "result": map[string]any{"exitCode": 130}}, "cancelled", map[string]any{"exitCode": 130}},
		{"cancelled result", map[string]any{"status": "error", "result": map[string]any{"cancelled": true, "timedOut": true}}, "cancelled", map[string]any{"cancelled": true, "timedOut": true}},
		{"legacy cancellation", map[string]any{"status": "error", "stopReason": "user_cancelled", "body": `{"exitCode":130}`}, "cancelled", map[string]any{"exitCode": float64(130)}},
		{"success", map[string]any{"status": "success", "result": map[string]any{"ok": true, "exitCode": 0, "elapsedMs": 12}}, "success", map[string]any{"ok": true, "exitCode": 0, "elapsedMs": 12}},
		{"pending", map[string]any{}, "pending", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.row
			original["role"], original["name"], original["toolCallId"] = "tool_call", "command", "call"
			original["args"] = `{"command":"test"}`
			original["durationMs"] = 25
			if result, ok := original["result"].(map[string]any); ok {
				if data, ok := result["data"].(map[string]any); ok {
					data["output"] = strings.Repeat("discarded output ", 5000)
				} else {
					result["output"] = strings.Repeat("discarded output ", 5000)
				}
			}
			before, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			conv := &conversation{ID: "compact-outcome", Messages: []map[string]any{original}}
			const excerpt = "bounded excerpt"
			applyFastCompaction(conv, map[string]any{"toolSummaries": map[string]any{"call": excerpt}})
			got := conv.Messages[0]
			if toolState(got) != tc.state || str(got, "status") != tc.state {
				t.Fatalf("outcome changed: got %q, want %q", toolState(got), tc.state)
			}
			for _, key := range []string{"error", "errorCode", "durationMs"} {
				if got[key] != original[key] {
					t.Fatalf("row metadata %q changed", key)
				}
			}
			for key, want := range tc.meta {
				if value := toolResult(got)[key]; value != want {
					t.Fatalf("result metadata %q: got %v, want %v", key, value, want)
				}
			}
			if str(toolResult(got), "_text") != excerpt || got["args"] != nil || !boolOf(got, "fastCompacted") {
				t.Fatal("compaction did not replace the original input/output with an excerpt")
			}
			after, err := json.Marshal(original)
			if err != nil || string(before) != string(after) {
				t.Fatal("compaction mutated original result evidence")
			}
			encoded, err := json.Marshal(got)
			if err != nil || len(encoded) > 2048 {
				t.Fatal("compacted row retained the original output body")
			}
			saved := snapshot(conv).Messages[0]
			if toolState(saved) != tc.state {
				t.Fatal("snapshot lost the compacted tool outcome")
			}
			for key, want := range tc.meta {
				if toolResult(saved)[key] != want {
					t.Fatalf("snapshot lost result metadata %q", key)
				}
			}
		})
	}
}
