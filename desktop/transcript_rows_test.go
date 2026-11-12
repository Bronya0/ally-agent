// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The list builds a few rows either side of the viewport, so the handles and the
// derived text of a long conversation stay near the size of the screen, and a
// retired binding serves the next row instead of being registered again.
func TestTranscriptHandlesAndCachesStayBoundToTheView(t *testing.T) {
	conv := &conversation{ID: "pages"}
	for i := 0; i < 300; i++ {
		conv.Messages = append(conv.Messages,
			map[string]any{"role": "user", "content": fmt.Sprintf("question-%d", i)},
			map[string]any{"role": "assistant", "content": fmt.Sprintf("```go\nvar needle%d = 1\n```", i)},
		)
	}
	a := &desktop{prefs: defaultPrefs(), tabs: []*conversation{conv}, page: "chat"}
	st := conv.uiState()
	st.Search.Open = true
	st.Search.Query = "needle"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Children(func() { a.chatMessages(c, conv, 800) })
	}, 800, 600)
	seen := map[*ui.Handle]bool{}
	for prompt := 0; prompt < 30; prompt++ {
		st.PendingPromptIdx, st.PendingPrompt = prompt*8, true
		viewLo, viewHi := st.built.lo, st.built.hi
		tt.Frame()
		if st.PendingPrompt {
			t.Fatal("prompt navigation lost the new row's bounds")
		}
		// A frame prunes to what the one before it built, and may then build
		// another place entirely: a handle stays only while one of the two shows
		// its row.
		within := func(index int) bool {
			return (index >= viewLo-chatBuildMargin && index <= viewHi+chatBuildMargin) ||
				(index >= st.built.lo-chatBuildMargin && index <= st.built.hi+chatBuildMargin)
		}
		for _, handles := range []map[int]*ui.Handle{st.PromptHandles, st.Search.handles} {
			for index, handle := range handles {
				if !within(index) {
					t.Fatalf("a row outside the view kept a handle: %d, built %d..%d then %d..%d", index, viewLo, viewHi, st.built.lo, st.built.hi)
				}
				seen[handle] = true
			}
		}
		for key := range st.Search.codeCache {
			if !within(key.Message) {
				t.Fatalf("the find bar retained a derived code block outside the view: %d", key.Message)
			}
		}
		if st.Search.frameDoc != nil {
			t.Fatal("the find bar retained a frame's Markdown document")
		}
	}
	if len(seen) > 60 {
		t.Fatalf("row bindings grew with the conversation: %d", len(seen))
	}
	replaceTranscript(conv, "compacted")
	tt.Frame()
	for _, handles := range []map[int]*ui.Handle{st.PromptHandles, st.Search.handles} {
		for index := range handles {
			if index >= len(conv.Messages) {
				t.Fatalf("compaction retained row %d of %d", index, len(conv.Messages))
			}
		}
	}
	pooled := slices.Clone(st.transcriptHandlePool)
	conv.Messages = append(conv.Messages, map[string]any{"role": "user", "content": "new question"})
	tt.Frame()
	if handle := st.PromptHandles[len(conv.Messages)-1]; handle == nil || !slices.Contains(pooled, handle) {
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
			if got.kindAt(i) != full.kindAt(i) {
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

func TestAppendedSubagentParentOnlyRebuildsWhenItClaimsOldRows(t *testing.T) {
	conv := &conversation{Messages: []map[string]any{{"role": "user", "content": "hello"}}}
	before := conv.cachedTranscriptRows()
	conv.Messages = append(conv.Messages,
		map[string]any{"role": "tool_call", "name": "subagent", "toolCallId": "new"},
		map[string]any{"role": "subagent", "parentToolCallId": "new"})
	rows := conv.cachedTranscriptRows()
	if rows != before || !slices.Equal(rows.childrenOf("new"), []int{2}) {
		t.Fatal("new parent and child did not extend the existing index")
	}
	conv.Messages = append(conv.Messages, map[string]any{"role": "subagent", "subId": "late"})
	rows = conv.cachedTranscriptRows()
	conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "subagent", "toolCallId": "parent", "result": map[string]any{"agentId": "late"}})
	got := conv.cachedTranscriptRows()
	if got == rows || !slices.Equal(got.childrenOf("parent"), []int{3}) {
		t.Fatal("late parent did not claim its existing child")
	}
}

func TestRoundFootersFollowSuggestions(t *testing.T) {
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
	rows := conv.chatRows()
	if len(st.RoundAt) != 2 || st.RoundAt[4] != 1 || st.RoundAt[6] != 6 {
		t.Fatalf("a round's footer did not land on its suggestion: %v", st.RoundAt)
	}
	for _, target := range []int{4, 6} {
		if at := conv.rowOfMessage(target); at < 0 || rows[at].message != target {
			t.Fatalf("no row draws the footer at %d", target)
		}
	}
	conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "suggest"})
	conv.chatRows()
	if len(st.RoundAt) != 2 || st.RoundAt[7] != 6 {
		t.Fatal("an appended suggestion did not move its round's footer")
	}
	conv.Messages = conv.Messages[:4]
	resetTranscriptAfterDelete(conv, 4)
	conv.chatRows()
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
		if got := rows.kindAt(tc.index); got != tc.want {
			t.Errorf("message %d: kind %v, want %v (%s)", tc.index, got, tc.want, tc.why)
		}
	}
}

func TestSubagentChildIsFoundByAgentIDToo(t *testing.T) {
	conv := &conversation{Messages: []map[string]any{
		{"role": "tool_call", "name": "subagent", "toolCallId": "call-x", "result": map[string]any{"agentId": "agent-7"}},
		{"role": "subagent", "subId": "agent-7"},
	}}
	if got := newTranscriptRows(conv).kindAt(1); got != rowSkipped {
		t.Fatalf("child matched by agent id: kind %v, want skipped", got)
	}
}

// Every row the transcript asks the list to build draws something: a row that
// draws nothing would cost the list its gap for an empty band.
func TestTranscriptRowsOnlyDraw(t *testing.T) {
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
	for _, row := range conv.chatRows() {
		if row.message == welcomeRow {
			continue
		}
		if rows.kindAt(row.message) == rowSkipped {
			t.Fatalf("row %d draws nothing", row.message)
		}
	}
}
