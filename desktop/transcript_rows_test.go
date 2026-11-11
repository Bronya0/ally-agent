// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTranscriptHandleReuseKeepsPageNavigationAndCompactionAligned(t *testing.T) {
	conv := &conversation{ID: "pages"}
	for i := 0; i < 300; i++ {
		conv.Messages = append(conv.Messages,
			map[string]any{"role": "user", "content": fmt.Sprintf("question-%d", i)},
			map[string]any{"role": "assistant", "content": fmt.Sprintf("```go\nvar needle%d = 1\n```", i)},
		)
	}
	a := &desktop{prefs: defaultPrefs(), tabs: []*conversation{conv}, page: "chat"}
	a.prefs.HistoryPageSize = 20
	st := conv.uiState()
	st.Search.Open = true
	st.Search.Query = "needle"
	observeFirst, scrolledBeforeCommit := false, false
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Children(func() { a.chatMessages(c, conv, 800) })
		if observeFirst {
			observeFirst = false
			scrolledBeforeCommit = !st.PendingPrompt
		}
	}, 800, 600)
	seen := map[*ui.Handle]bool{}
	for page := 0; page < 30; page++ {
		start := page * 20
		st.VisibleStart, st.PendingPromptIdx, st.PendingPrompt = start+1, start, true
		st.Follow = false
		conv.Scroll.Y = 0
		observeFirst = true
		tt.Frame()
		if scrolledBeforeCommit {
			t.Fatal("recycled handle scrolled before its new row was committed")
		}
		tt.Frame()
		if st.PendingPrompt {
			t.Fatal("prompt navigation lost the new row's bounds")
		}
		for _, handles := range []map[int]*ui.Handle{st.PromptHandles, st.Search.handles} {
			for index, handle := range handles {
				if index < start || index >= start+20 {
					t.Fatal("an older page still owns a row handle")
				}
				seen[handle] = true
			}
		}
		if len(seen) > 20 {
			t.Fatal("row bindings grew with the conversation instead of the page")
		}
		if st.Search.frameDoc != nil {
			t.Fatal("the find bar retained a frame's Markdown document")
		}
		for key := range st.Search.codeCache {
			if key.Message < start || key.Message >= start+20 {
				t.Fatal("the find bar retained an older page's derived code")
			}
		}
	}
	replaceTranscript(conv, "compacted")
	if len(st.PromptHandles) != 0 || len(st.Search.handles) != 0 || len(st.transcriptHandlePool) != 20 {
		t.Fatal("compaction lost reusable handles or retained old row identities")
	}
	conv.Messages = append(conv.Messages, map[string]any{"role": "user", "content": "new question"})
	tt.Frame()
	if handle := st.PromptHandles[1]; handle == nil || !seen[handle] {
		t.Fatal("the compacted conversation did not reuse a retired binding")
	}
	conv.Messages = conv.Messages[:1]
	resetTranscriptAfterDelete(conv, 1)
	if st.PromptHandles[1] != nil || st.Search.handles[1] != nil {
		t.Fatal("deleting a prompt retained its navigation identity")
	}
}

func TestToolEventsRefreshCachedRowOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, toolName string
		child          map[string]any
		result         any
	}{
		{"agent result", "subagent", map[string]any{"role": "subagent", "subId": "child"}, map[string]any{"agentId": "child"}},
		{"JSON agent result", "subagent", map[string]any{"role": "subagent", "subId": "child"}, `{"agentId":"child"}`},
		{"late tool name", "", map[string]any{"role": "subagent", "parentToolCallId": "parent"}, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv := &conversation{Messages: []map[string]any{
				{"role": "tool_call", "name": tc.toolName, "toolCallId": "parent"}, tc.child,
			}}
			before := conv.cachedTranscriptRows()
			_ = conv.blockStarts()
			a := &desktop{}
			payload := map[string]any{"name": "subagent", "toolCallId": "parent", "result": tc.result}
			a.applyToolEvent(conv, "tool:result", payload)
			rows := conv.cachedTranscriptRows()
			if rows == before || !slices.Equal(rows.childrenOf("parent"), []int{1}) {
				t.Fatalf("result did not refresh child ownership: %v", rows.childrenOf("parent"))
			}
			if got := conv.blockStarts(); !slices.Equal(got, []int{0, 2}) {
				t.Fatalf("history still counts the nested child as a block: %v", got)
			}
			// Status and repeated results do not change ownership or require a scan.
			a.applyToolEvent(conv, "tool:result", payload)
			if conv.cachedTranscriptRows() != rows {
				t.Fatal("unchanged ownership discarded the cache")
			}
			if tc.toolName == "subagent" {
				a.applyToolEvent(conv, "tool:result", map[string]any{
					"name": "subagent", "toolCallId": "parent", "result": map[string]any{"agentId": "other"},
				})
				if got := conv.cachedTranscriptRows().childrenOf("parent"); len(got) != 0 {
					t.Fatalf("changed agent ID retained the old child: %v", got)
				}
				if got := conv.blockStarts(); !slices.Equal(got, []int{0, 1, 2}) {
					t.Fatalf("detached child did not regain its history block: %v", got)
				}
			}
		})
	}
}

func TestHistoryAppendMatchesFullOwnershipRebuild(t *testing.T) {
	conv := &conversation{ID: "incremental"}
	check := func() {
		t.Helper()
		full := newTranscriptRows(conv)
		want := []int{}
		for i := range conv.Messages {
			if full.startsBlock(i) {
				want = append(want, i)
			}
		}
		want = append(want, len(conv.Messages))
		if got := conv.blockStarts(); !slices.Equal(got, want) {
			t.Fatalf("history %d: page boundaries %v, full rebuild %v", len(conv.Messages), got, want)
		}
		got := conv.cachedTranscriptRows()
		for i := range conv.Messages {
			if got.kindAt(i, 0) != full.kindAt(i, 0) {
				t.Fatalf("history %d: row %d changed owner", len(conv.Messages), i)
			}
		}
		for id, children := range full.children {
			if !slices.Equal(got.childrenOf(id), children) {
				t.Fatalf("parent %s lost child order", id)
			}
		}
	}
	check() // empty sentinel must not become a duplicate first row
	for _, m := range []map[string]any{
		{"role": "user", "content": "question"},
		{"role": "assistant", "content": ""},
		{"role": "tool_call", "name": "read", "toolCallId": "read-1"},
		{"role": "tool_call", "name": "command", "toolCallId": "command-1"},
		{"role": "subagent", "parentToolCallId": "parent", "subId": "agent"},
		{"role": "tool_call", "name": "subagent", "toolCallId": "parent", "result": map[string]any{"agentId": "agent"}},
		{"role": "subagent", "subId": "agent"},
		{"role": "tool_call", "name": "list", "toolCallId": "list-1"},
		{"role": "tool_call", "name": "read", "toolCallId": "read-2"},
		{"role": "assistant", "content": "done"},
	} {
		conv.Messages = append(conv.Messages, m)
		check()
	}
	// Multiple appends may reach the ownership cache before the page index.
	for i := 0; i < 3; i++ {
		conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "read"})
		_ = conv.cachedTranscriptRows()
	}
	check()
	a := &desktop{}
	a.applyToolEvent(conv, "tool:result", map[string]any{"name": "subagent", "toolCallId": "parent", "result": map[string]any{"agentId": "other"}})
	check()
	conv.Messages[1]["content"] = "first token"
	conv.markHistoryAt(1)
	check()
	conv.Messages = conv.Messages[:4]
	conv.resetTranscriptLookup()
	conv.markHistory()
	check()
	conv.Messages = append(conv.Messages, map[string]any{"role": "assistant", "content": "after deletion"})
	check()
	replaceTranscript(conv, "summary")
	check()
}

func TestRoundFootersFollowSuggestionsAcrossPartialPages(t *testing.T) {
	conv := &conversation{Messages: []map[string]any{
		{"role": "user", "content": "question"},
		{"role": "assistant", "content": "answer"},
		{"role": "tool_call", "name": "read"},
		{"role": "tool_call", "name": "command"},
		{"role": "tool_call", "name": "suggest"},
		{"role": "user", "content": "next question"},
		{"role": "assistant", "content": "next answer"},
	}}
	st := conv.uiState()
	st.Round[1], st.Round[6] = &roundStat{Done: true}, &roundStat{Done: true}
	refreshRoundPlacements(conv, 2, 5)
	if len(st.RoundAt) != 1 || st.RoundAt[4] != 1 {
		t.Fatal("a partial page lost the preceding round's suggestion footer")
	}
	refreshRoundPlacements(conv, 5, 7)
	if len(st.RoundAt) != 1 || st.RoundAt[6] != 6 {
		t.Fatal("the next prompt inherited the previous round's footer")
	}
	conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "suggest"})
	refreshRoundPlacements(conv, 5, 8)
	if len(st.RoundAt) != 1 || st.RoundAt[7] != 6 {
		t.Fatal("an appended suggestion did not move its round's footer")
	}
	conv.Messages = conv.Messages[:4]
	resetTranscriptAfterDelete(conv, 4)
	refreshRoundPlacements(conv, 0, 4)
	if len(st.RoundAt) != 1 || st.RoundAt[1] != 1 {
		t.Fatal("deleting suggestions retained a retired footer placement")
	}
}

func TestHistoryRewritesInvalidateBeforeSameLengthRegrowth(t *testing.T) {
	for _, operation := range []string{"delete", "drop preview"} {
		t.Run(operation, func(t *testing.T) {
			conv := &conversation{Messages: []map[string]any{
				{"role": "user", "content": "question"},
				{"role": "tool_call", "name": "read"},
				{"role": "assistant", "content": "old", compactPreviewFlag: true},
			}}
			_ = conv.blockStarts()
			if operation == "delete" {
				conv.Messages = conv.Messages[:2]
				resetTranscriptAfterDelete(conv, 2)
			} else {
				dropCompactPreview(conv)
			}
			// No UI frame between removal and another append: the length key
			// returns to its old value, but this tool now folds into the read.
			conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "command"})
			if got := conv.blockStarts(); !slices.Equal(got, []int{0, 1, 3}) {
				t.Fatalf("retired answer still opens a block: %v", got)
			}
		})
	}
}

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
