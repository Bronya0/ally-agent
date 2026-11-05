// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "testing"

// rowKindAt is the single judgement that decides whether a message gets a row.
// A message that another row already draws, or that draws nothing, must not
// claim a slot: an empty slot still costs the transcript column its gap.
func TestRowOwnershipDecidesWhoDraws(t *testing.T) {
	conv := &conversation{Messages: []map[string]any{
		{"role": "tool_call", "name": "subagent", "toolCallId": "call-parent", "status": "success"},
		{"role": "subagent", "parentToolCallId": "call-parent"},
		{"role": "user", "content": "question"},
		{"role": "assistant", "content": ""},
		{"role": "assistant", "content": "", "welcome": true},
		{"role": "assistant", "content": "answer"},
		{"role": "tool_call", "name": "read", "toolCallId": "call-read-1"},
		{"role": "tool_call", "name": "read", "toolCallId": "call-read-2"},
		{"role": "tool_call", "name": "read", "toolCallId": "call-read-3", "fastCompacted": true},
		{"role": "assistant", "content": "done reading"},
		{"role": "tool_call", "name": "command", "toolCallId": "call-cmd-1"},
		{"role": "tool_call", "name": "command", "toolCallId": "call-cmd-2"},
		{"role": "notice", "content": "note"},
		{"role": "unknown"},
		{"role": "unknown", "content": "text"},
	}}
	rows := newTranscriptRows(conv)
	for _, tc := range []struct {
		index int
		want  rowKind
		why   string
	}{
		{0, rowPlain, "a sub-agent card draws itself"},
		{1, rowSkipped, "drawn inside its parent's card"},
		{2, rowPlain, "a prompt draws itself"},
		{3, rowSkipped, "an empty assistant message draws nothing"},
		{4, rowPlain, "the welcome card draws itself"},
		{5, rowPlain, "an answer draws itself"},
		{6, rowReadGroup, "the first read heads the group"},
		{7, rowSkipped, "the second read folds into the group"},
		{8, rowSkipped, "a locally trimmed card keeps the run it belongs to"},
		{9, rowPlain, "an answer ends the read run"},
		{10, rowCommandGroup, "the first command heads the group"},
		{11, rowSkipped, "the second command folds into the group"},
		{12, rowPlain, "a notice draws itself"},
		{13, rowSkipped, "an unknown role without content draws nothing"},
		{14, rowPlain, "an unknown role with content still draws"},
	} {
		if got := rows.kindAt(tc.index, -1); got != tc.want {
			t.Errorf("message %d: kind %v, want %v (%s)", tc.index, got, tc.want, tc.why)
		}
	}
	// A run whose head sits before the window keeps its card on the first
	// visible member, so paging down never hides the group.
	if got := rows.kindAt(7, 7); got != rowReadGroup {
		t.Errorf("window start on a follower: kind %v, want the group", got)
	}
}

func TestSubagentChildIsFoundByAgentIDToo(t *testing.T) {
	conv := &conversation{Messages: []map[string]any{
		{"role": "tool_call", "name": "subagent", "toolCallId": "call-x", "result": map[string]any{"agentId": "agent-7"}},
		{"role": "subagent", "subId": "agent-7"},
	}}
	if got := newTranscriptRows(conv).kindAt(1, 0); got != rowSkipped {
		t.Fatalf("child matched by agent id: kind %v, want skipped", got)
	}
}

// A history page must never begin on a row that draws nothing, or the page
// opens with dead space instead of content.
func TestHistoryPagesStartOnDrawnRows(t *testing.T) {
	conv := &conversation{}
	for block := 0; block < 4; block++ {
		conv.Messages = append(conv.Messages,
			map[string]any{"role": "user", "content": "question"},
			map[string]any{"role": "assistant", "content": ""},
			map[string]any{"role": "tool_call", "name": "subagent", "toolCallId": "call-sub"},
			map[string]any{"role": "subagent", "parentToolCallId": "call-sub"},
		)
		for call := 0; call < 3; call++ {
			conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "read"})
		}
	}
	rows := newTranscriptRows(conv)
	for size := 1; size <= 6; size++ {
		for _, visible := range []int{0, 1, 2, 4, 9, 17} {
			conv.uiState().VisibleStart = visible
			start, end := historyMessageWindow(conv, size)
			if start > 0 && rows.kindAt(start, start-1) == rowSkipped {
				t.Fatalf("size %d visible %d: page starts on a skipped row %d", size, visible, start)
			}
			if end < len(conv.Messages) && rows.kindAt(end, end-1) == rowSkipped {
				t.Fatalf("size %d visible %d: page ends on a skipped row %d", size, visible, end)
			}
		}
	}
}
