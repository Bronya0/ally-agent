package desktop

import (
	"fmt"
	"testing"
)

// A run of read calls is one row, and the row that draws it is the run's head:
// the list is asked for far fewer rows than the transcript has messages.
func TestChatRowsFoldToolRuns(t *testing.T) {
	conv := &conversation{}
	for block := 0; block < 5; block++ {
		conv.Messages = append(conv.Messages,
			map[string]any{"role": "user", "content": fmt.Sprintf("question %d", block)},
			map[string]any{"role": "assistant", "content": "answer"},
		)
		for call := 0; call < 10; call++ {
			conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "read"})
		}
	}
	rows := conv.chatRows()
	want := 1 + 5*3 // the welcome card, then a question, an answer and the run per block
	if len(rows) != want {
		t.Fatalf("rows = %d, want %d", len(rows), want)
	}
	if rows[0].message != welcomeRow {
		t.Fatalf("the welcome card does not open the transcript: %+v", rows[0])
	}
	for _, row := range rows[1:] {
		if got := conv.cachedTranscriptRows().kindAt(row.message); got == rowSkipped {
			t.Fatalf("row %d draws nothing", row.message)
		}
	}
	// A folded call answers with the row that draws its run.
	if got, want := conv.rowOfMessage(5), 1+2; got != want {
		t.Fatalf("a folded call maps to row %d, want %d", got, want)
	}
}

// The welcome card takes the row of a transcript that has no welcome message,
// and gives way to the message once one arrives.
func TestChatRowsCarryTheWelcomeCard(t *testing.T) {
	conv := &conversation{Messages: []map[string]any{{"role": "assistant", "content": "hello"}}}
	if rows := conv.chatRows(); rows[0].message != welcomeRow {
		t.Fatalf("a transcript without a welcome message does not draw one: %+v", rows[0])
	}
	conv.Messages = append(conv.Messages, map[string]any{"role": "assistant", "content": "hi", "welcome": true})
	if rows := conv.chatRows(); rows[0].message == welcomeRow {
		t.Fatal("the transcript still draws a second welcome card")
	}
}

// Deleting the messages a row stood for leaves the list something to draw: a
// cached row past the shortened transcript would be an empty list.
func TestChatRowsSurviveDeletingTheTranscript(t *testing.T) {
	conv := &conversation{}
	for i := 0; i < 200; i++ {
		conv.Messages = append(conv.Messages, map[string]any{"role": "user", "content": "question"})
	}
	if len(conv.chatRows()) < 100 {
		t.Fatal("the transcript draws too few rows")
	}
	conv.Messages = conv.Messages[:120]
	resetTranscriptAfterDelete(conv, 120)
	for _, row := range conv.chatRows() {
		if row.message >= len(conv.Messages) {
			t.Fatalf("row %d outlived the transcript of %d messages", row.message, len(conv.Messages))
		}
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
