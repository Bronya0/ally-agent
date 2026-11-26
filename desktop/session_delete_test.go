package desktop

import (
	"errors"
	"testing"

	backend "ally-dev/internal/app"
)

// A refused or failed backend sync must not shrink the transcript: the old flow
// cut the visible turns first and only then reported the mismatch, so the UI
// and the model context kept different histories.
func TestFailedDeletionKeepsTheTranscript(t *testing.T) {
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	conv := &conversation{ID: "delete-failure", Workspace: t.TempDir(), Messages: []map[string]any{
		{"role": "user", "content": "first"},
		{"role": "assistant", "content": "answer"},
		{"role": "user", "content": "second"},
	}}
	a := &desktop{core: backend.NewApp()}

	a.applyUserMessageDeletion(conv, 2, errors.New("session is compacting; retry the deletion after the compaction finishes"))
	if len(conv.Messages) != 3 {
		t.Fatalf("the transcript was cut before the backend accepted: %v", conv.Messages)
	}
	if len(a.notifications) == 0 {
		t.Fatal("the failure was not surfaced to the user")
	}

	a.applyUserMessageDeletion(conv, 2, nil)
	if a.persistTail != nil {
		// The snapshot write runs in a worker; wait for it so the test's temp
		// directory is not cleaned while the write is still in flight.
		<-a.persistTail
	}
	if len(conv.Messages) != 2 || str(conv.Messages[1], "content") != "answer" {
		t.Fatalf("an accepted deletion did not cut the transcript: %v", conv.Messages)
	}
}
