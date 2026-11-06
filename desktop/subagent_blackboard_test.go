// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func blackboardEvent(seq int, direction string) map[string]any {
	return map[string]any{"direction": direction, "seq": seq, "from": "reviewer", "to": "*", "type": "finding", "body": "entry body", "at": int64(1700000000000 + seq)}
}

func TestSubagentBlackboardEventsAreIsolatedAndDeduplicated(t *testing.T) {
	first, second := &conversation{ID: "first"}, &conversation{ID: "second"}
	a := &desktop{tabs: []*conversation{first, second}}
	for _, session := range []string{"first", "second"} {
		a.applyEvent("sub:spawn", map[string]any{"sessionId": session, "id": "same-id", "toolCallId": "parent"})
	}
	payload := map[string]any{"sessionId": "first", "id": "same-id", "name": "reviewer", "blackboardSent": 1, "blackboardReceived": 0, "blackboardEvent": blackboardEvent(1, "sent")}
	a.applyEvent("sub:blackboard", payload)
	a.applyEvent("sub:blackboard", payload)
	row := first.Messages[0]
	if len(list(row, "blackboardEvents")) != 1 || num(row, "blackboardSent") != 1 || str(row, "status") != "running" {
		t.Fatalf("duplicate changed count, log or run status: %#v", row)
	}
	if str(row, "parentToolCallId") != "parent" || str(row, "subagentName") != "reviewer" {
		t.Fatal("communication changed row ownership or lost identity")
	}
	if str(payload["blackboardEvent"].(map[string]any), "_blackboardKey") != "" {
		t.Fatal("UI normalization mutated the event payload")
	}
	if str(second.Messages[0], "_blackboardLabel") != "" {
		t.Fatal("communication leaked to another session")
	}
	for _, p := range []map[string]any{
		{"sessionId": "missing", "id": "same-id", "blackboardSent": 90},
		{"sessionId": "first", "id": "missing", "blackboardSent": 90},
		{"sessionId": "first", "blackboardSent": 90},
	} {
		a.applyEvent("sub:blackboard", p)
	}
	if len(first.Messages) != 1 || num(row, "blackboardSent") != 1 {
		t.Fatal("unknown communication created an orphan card")
	}
	payload["blackboardReceived"] = 1
	payload["blackboardEvent"] = blackboardEvent(1, "received")
	a.applyEvent("sub:blackboard", payload)
	if len(list(row, "blackboardEvents")) != 2 {
		t.Fatal("sending and receiving the same sequence must remain distinct")
	}
	for i := 2; i <= backend.MaxSubagentBlackboardEvents+4; i++ {
		payload["blackboardSent"] = i
		payload["blackboardEvent"] = blackboardEvent(i, "sent")
		a.applyEvent("sub:blackboard", payload)
	}
	entries := list(row, "blackboardEvents")
	if len(entries) != backend.MaxSubagentBlackboardEvents || num(row, "blackboardSent") != backend.MaxSubagentBlackboardEvents+4 {
		t.Fatal("bounded log truncated cumulative counts")
	}
	if num(entries[0].(map[string]any), "seq") != 5 {
		t.Fatal("old entries survived the rolling window")
	}
	payload["blackboardSent"] = 1
	payload["blackboardReceived"] = 0
	payload["blackboardEvent"] = blackboardEvent(1, "sent")
	a.applyEvent("sub:blackboard", payload)
	if num(row, "blackboardReceived") != 1 || num(row, "blackboardSent") != backend.MaxSubagentBlackboardEvents+4 || num(list(row, "blackboardEvents")[0].(map[string]any), "seq") != 5 {
		t.Fatal("stale replay regressed counts or evicted a newer entry")
	}
}

func TestSubagentBlackboardSnapshotAndHistoryRestore(t *testing.T) {
	row := map[string]any{"role": "subagent", "subId": "sub", "blackboardSent": 3, "blackboardEvents": []any{blackboardEvent(3, "sent")}}
	run := &backend.SubagentRun{ID: "sub", SessionID: "session", BlackboardSent: 2, BlackboardReceived: 1, BlackboardEvents: []backend.SubagentBlackboardEvent{
		{Direction: "sent", Seq: 2, From: "reviewer", To: "peer", Type: "question", Body: "snapshot", At: 1700000000002},
	}}
	mergeSubagentBlackboardRun(row, run)
	mergeSubagentBlackboardRun(row, run)
	if num(row, "blackboardSent") != 3 || num(row, "blackboardReceived") != 1 || len(list(row, "blackboardEvents")) != 2 {
		t.Fatal("late snapshot regressed live communication or duplicated entries")
	}
	data, err := json.Marshal(snapshot(&conversation{ID: "session", Messages: []map[string]any{row}}))
	if err != nil {
		t.Fatal(err)
	}
	var restored backend.SessionSnapshot
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	normalizeSubagentBlackboard(restored.Messages[0])
	entries := list(restored.Messages[0], "blackboardEvents")
	if len(entries) != 2 || str(entries[0].(map[string]any), "body") != "snapshot" || str(entries[1].(map[string]any), "_blackboardHeading") == "" {
		t.Fatal("JSON-shaped restored history lost normalized communication")
	}
	conv := &conversation{ID: "session"}
	a := &desktop{tabs: []*conversation{conv}}
	if !a.hasSubagentConversation(conv, "session") || a.hasSubagentConversation(conv, "other") {
		t.Fatal("snapshot accepted the wrong session")
	}
	a.tabs[0] = &conversation{ID: "session"}
	if a.hasSubagentConversation(conv, "session") {
		t.Fatal("late snapshot accepted a replaced conversation")
	}
	a.tabs[0] = conv
	a.closing = true
	if a.hasSubagentConversation(conv, "session") {
		t.Fatal("late snapshot accepted during shutdown")
	}
}

func TestSubagentBlackboardNativeDisclosure(t *testing.T) {
	for _, dark := range []bool{false, true} {
		a := &desktop{prefs: defaultPrefs()}
		if dark {
			a.prefs.Appearance = "dark"
		} else {
			a.prefs.Appearance = "light"
		}
		row := map[string]any{"role": "subagent", "subId": "sub", "status": "running"}
		conv := &conversation{ID: "session", Messages: []map[string]any{row}}
		tt := ui.NewTester(func(c *ui.Context) {
			c.DisableAnimations()
			c.SetTheme(a.theme())
			ui.Column(c).FillWidth().Padding(20).Children(func() { a.subagentRow(c, conv, 0, row) })
		}, 850, 700)
		if _, ok := tt.Find("子代理黑板 sub"); ok {
			t.Fatal("silent agent exposed a blackboard entrance")
		}
		event := blackboardEvent(1, "received")
		event["replyTo"], event["body"], event["truncated"] = 9, "BLACKBOARD_BODY", true
		applySubagentBlackboard(row, map[string]any{"blackboardReceived": 1, "blackboardEvent": event})
		tt.Frame()
		if tt.HasText("BLACKBOARD_BODY") {
			t.Fatal("collapsed card constructed log entries")
		}
		if err := tt.Click("子代理黑板 sub"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !tt.HasText("BLACKBOARD_BODY") || !conv.uiState().ToolOpen["sub-blackboard-sub"] {
			t.Fatalf("running agent's blackboard did not open: %v", tt.Texts())
		}
		bounds, ok := tt.Find("子代理黑板记录 sub")
		if !ok || bounds.H >= 280 {
			t.Fatalf("short log has a fixed blank area: %+v", bounds)
		}
		entry := list(row, "blackboardEvents")[0].(map[string]any)
		if !tt.HasText(str(entry, "_blackboardHeading")) || !tt.HasText(str(entry, "_blackboardRoute")) {
			t.Fatal("communication metadata did not render")
		}
		row["status"], row["summary"] = "success", "REPORT_BODY"
		tt.Frame()
		if err := tt.Click("子代理报告 sub"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !tt.HasText("REPORT_BODY") || tt.HasText("BLACKBOARD_BODY") || conv.uiState().ToolOpen["sub-blackboard-sub"] {
			t.Fatal("report did not exclusively replace the blackboard")
		}
		if err := tt.Click("子代理黑板 sub"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if tt.HasText("REPORT_BODY") || !tt.HasText("BLACKBOARD_BODY") || conv.uiState().ToolOpen["sub-sub"] {
			t.Fatal("blackboard did not exclusively replace the report")
		}
		for i := 2; i <= backend.MaxSubagentBlackboardEvents; i++ {
			e := blackboardEvent(i, "received")
			e["body"] = strings.Repeat("long body ", 40)
			applySubagentBlackboard(row, map[string]any{"blackboardReceived": i, "blackboardEvent": e})
		}
		tt.Frame()
		tt.Frame()
		bounds, _ = tt.Find("子代理黑板记录 sub")
		if bounds.H > 280.5 || bounds.H < 279 {
			t.Fatalf("long log did not cap its native scroll area: %+v", bounds)
		}
		before, ok := tt.Find("BLACKBOARD_BODY")
		if !ok {
			t.Fatal("first log entry missing before scrolling")
		}
		tt.Scroll(bounds.X+bounds.W/2, bounds.Y+bounds.H/2, 0, 200)
		tt.Frame()
		after, ok := tt.Find("BLACKBOARD_BODY")
		if !ok || after.Y >= before.Y {
			t.Fatal("native scroll did not move the log contents")
		}
		if err := tt.Click("子代理黑板 sub"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if conv.uiState().ToolOpen["sub-blackboard-sub"] || tt.HasText("BLACKBOARD_BODY") {
			t.Fatal("second click did not collapse the log")
		}
	}
}
