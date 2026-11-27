// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"testing"
)

// A tool error inside a sub-agent is the sub-agent's own business: it sees the
// failure, works around it and keeps going. Copying that message onto the run
// row marked a healthy run failed mid-flight and lit up its report button.
func TestSubagentToolFailureDoesNotFailTheRun(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "sess"}
	a.tabs = []*conversation{conv}
	a.applySubagentEvent("sub:spawn", map[string]any{"id": "sub-1", "sessionId": "sess"})
	a.applySubagentEvent("sub:tool:error", map[string]any{
		"id": "sub-1", "sessionId": "sess", "toolCallId": "call-1", "name": "read",
		"error": "boom", "errorCode": "E_READ",
	})
	row := conv.Messages[0]
	if got := str(row, "status"); got != "running" {
		t.Fatalf("tool failure changed the run status to %q", got)
	}
	if got := str(row, "error"); got != "" {
		t.Fatalf("tool error leaked onto the run row: %q", got)
	}
	tools, _ := row["toolCalls"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tool entry missing: %#v", row["toolCalls"])
	}
	entry, _ := tools[0].(map[string]any)
	if got := str(entry, "error"); got != "boom" {
		t.Fatalf("the tool entry lost its own error: %q", got)
	}
	a.applySubagentEvent("sub:done", map[string]any{"id": "sub-1", "sessionId": "sess", "status": "completed", "summary": "done"})
	if got := str(conv.Messages[0], "status"); got != "success" {
		t.Fatalf("clean finish reads %q", got)
	}
}

func TestSubagentLatestActivityTracksStartsNotOutcomes(t *testing.T) {
	conv := &conversation{ID: "sess"}
	a := &desktop{tabs: []*conversation{conv}}
	emit := func(name string, fields map[string]any) {
		fields["id"], fields["sessionId"] = "worker", conv.ID
		a.applySubagentEvent(name, fields)
	}
	emit("sub:spawn", map[string]any{})
	row := conv.Messages[0]
	emit("sub:tool:start", map[string]any{"toolCallId": "old", "name": "read", "purpose": "核对配置"})
	first := str(row, "latestTool")
	if !strings.Contains(first, "核对配置") {
		t.Fatalf("the activity line lost the tool purpose: %q", first)
	}
	emit("sub:tool:start", map[string]any{"toolCallId": "new", "name": "grep"})
	latest := str(row, "latestTool")
	if latest == "" || latest == first {
		t.Fatalf("the latest call without a purpose must still replace the previous activity: %q", latest)
	}
	emit("sub:tool:result", map[string]any{"toolCallId": "old", "name": "read"})
	emit("sub:tool:error", map[string]any{"toolCallId": "skipped", "name": "edit", "errorCode": "E_STAGE_BLOCKED"})
	emit("sub:compact", map[string]any{})
	if !boolOf(row, "compacting") || str(row, "status") != "running" || str(row, "latestTool") != latest {
		t.Fatalf("compaction or an older outcome corrupted the active row: %#v", row)
	}
	emit("sub:compacted", map[string]any{"contextTokens": 120})
	if boolOf(row, "compacting") || num(row, "contextTokens") != 120 {
		t.Fatalf("compaction did not clear the live state and update context: %#v", row)
	}
}
