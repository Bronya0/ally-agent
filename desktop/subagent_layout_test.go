// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSubagentRowsLeaveNoBlankBand: a sub-agent row draws inside its parent's
// tool card, so it must not claim a row of its own either. An empty row still
// cost the transcript column its gap, and a batch of sub-agents left a blank
// band as tall as the batch between the last card and the next row.
func TestSubagentRowsLeaveNoBlankBand(t *testing.T) {
	parent := map[string]any{
		"role": "tool_call", "name": "subagent", "toolCallId": "call_sub",
		"status": "success", "args": `{"description":"审计测试"}`, "result": map[string]any{},
	}
	child := func(index int) map[string]any {
		return map[string]any{
			"role": "subagent", "subId": fmt.Sprintf("s%d", index), "parentToolCallId": "call_sub",
			"subagentRole": "测试审计员", "status": "completed",
			"steps": index, "maxSteps": 10, "durationMs": 1000, "summary": "报告",
		}
	}
	read := map[string]any{
		"role": "tool_call", "name": "read", "toolCallId": "call_read",
		"status": "success", "args": `{"path":"a.go"}`, "result": map[string]any{"content": "x"},
	}

	messages := []map[string]any{parent}
	for i := 1; i <= 8; i++ {
		messages = append(messages, child(i))
	}
	messages = append(messages, read)

	a := &desktop{prefs: defaultPrefs()}
	conv := &conversation{ID: "subagent-band", Messages: messages}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() {
			rows := newTranscriptRows(conv)
			for i := range conv.Messages {
				a.messageRow(c, conv, i, rows)
			}
		})
	}, 700, 2400)
	tt.Frame()
	tt.Frame()

	last, ok := tt.Find("8/10 步")
	if !ok {
		t.Fatalf("last sub-agent card missing: %v", tt.Texts())
	}
	next, ok := tt.Find("读取 1 次")
	if !ok {
		t.Fatalf("row after the sub-agents missing: %v", tt.Texts())
	}
	// Card padding (10) plus the transcript gap (10), with room for rounding.
	if gap := next.Y - (last.Y + last.H); gap > 30 {
		t.Fatalf("sub-agent rows left a %.1fpx blank band before the next row", gap)
	}
}
