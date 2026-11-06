package desktop

import "testing"

func TestHistoryWindowConfigDefaultsAndBounds(t *testing.T) {
	// Real prompts: a page counts blocks that draw, and an empty message draws
	// nothing, so a fixture of blanks would measure no paging at all.
	conv := &conversation{Messages: make([]map[string]any, 500)}
	for i := range conv.Messages {
		conv.Messages[i] = map[string]any{"role": "user", "content": "question"}
	}
	for _, size := range []int{0, -1, 19, 20, 60, 140, 200, 201} {
		app := &desktop{prefs: uiPrefs{HistoryPageSize: size}}
		start, end := app.chatMessageWindow(conv)
		want := size
		if size < 20 || size > 200 {
			want = 80
		}
		if end-start != want {
			t.Fatalf("size %d: %d:%d", size, start, end)
		}
	}
}

// Deleting the first row of the visible page used to leave VisibleStart past
// the shortened transcript, which made historyMessageWindow return an empty
// range.
func TestDeletingPageStartKeepsTranscriptVisible(t *testing.T) {
	conv := &conversation{}
	for i := 0; i < 200; i++ {
		conv.Messages = append(conv.Messages, map[string]any{"role": "user", "content": "question"})
	}
	conv.uiState().VisibleStart = 121
	conv.Messages = conv.Messages[:120]
	resetTranscriptAfterDelete(conv, 120)
	start, end := historyMessageWindow(conv, 80)
	if start == end {
		t.Fatalf("transcript renders nothing after deleting the page's first row")
	}
}

// Rounds before the cut still describe surviving messages; only the deleted
// tail's entries may be dropped.
func TestDeletingTailKeepsRoundStatsBeforeCut(t *testing.T) {
	conv := &conversation{}
	for i := 0; i < 10; i++ {
		conv.Messages = append(conv.Messages, map[string]any{"role": "user", "content": "question"})
	}
	rounds := conv.uiState().Round
	rounds[1] = &roundStat{}
	rounds[7] = &roundStat{}
	conv.Messages = conv.Messages[:6]
	resetTranscriptAfterDelete(conv, 6)
	rounds = conv.uiState().Round
	if rounds[1] == nil {
		t.Fatal("a round before the cut must survive")
	}
	if rounds[7] != nil {
		t.Fatal("a round in the deleted tail must be dropped")
	}
}

// Paging counts grouped blocks and must never split a run of tool calls.
func TestHistoryPagesCountGroupsAndNeverSplitThem(t *testing.T) {
	conv := &conversation{}
	for block := 0; block < 65; block++ {
		conv.Messages = append(conv.Messages, map[string]any{"role": "user", "content": "question"})
		for call := 0; call < 10; call++ {
			conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "read"})
		}
	}
	// 130 visible blocks; 650 tool calls are only 65 grouped blocks.
	start, end := historyMessageWindow(conv, 50)
	if start != 40*11 || end != len(conv.Messages) {
		t.Fatalf("latest %d:%d", start, end)
	}
	for start > 0 {
		previousEnd := start
		conv.uiState().VisibleStart = previousHistoryPage(conv, start, 50) + 1
		start, end = historyMessageWindow(conv, 50)
		if end < previousEnd {
			t.Fatal("pagination skipped a block")
		}
		if start > 0 && newTranscriptRows(conv).foldsInto(start) {
			t.Fatal("split group")
		}
	}
	// Restored legacy offsets inside a group align to its first call.
	conv.uiState().VisibleStart = 5
	start, _ = historyMessageWindow(conv, 50)
	if start != 1 {
		t.Fatalf("group alignment: %d", start)
	}
}
