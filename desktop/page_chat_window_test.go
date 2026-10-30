package desktop

import "testing"

func TestHistoryWindowConfigDefaultsAndBounds(t *testing.T) {
	conv := &conversation{Messages: make([]map[string]any, 500)}
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
		if start > 0 && sameHistoryBlock(conv.Messages[start-1], conv.Messages[start]) {
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
