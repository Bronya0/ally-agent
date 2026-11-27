package desktop

import (
	"errors"
	"testing"
)

// Unsaved-file confirmation is open: closing is already set, but run events
// must keep flowing until the user accepts the quit — a run that finishes
// behind the dialog would otherwise leave its tab busy forever after cancel.
func TestQuitConfirmationKeepsConsumingRunEvents(t *testing.T) {
	a := &desktop{page: "chat", closing: true, tabs: []*conversation{{ID: "busy", Workspace: t.TempDir(), Busy: true, RunID: "run-1"}}}
	a.applyEvent("run:stream", map[string]any{"sessionId": "busy", "content": "still streaming"})
	if len(a.tabs[0].Messages) != 1 || str(a.tabs[0].Messages[0], "content") != "still streaming" {
		t.Fatalf("run output dropped during quit confirmation: %v", a.tabs[0].Messages)
	}

	a.acceptQuit()
	a.applyEvent("run:stream", map[string]any{"sessionId": "busy", "content": "past the barrier"})
	if len(a.tabs[0].Messages) != 1 {
		t.Fatalf("events flowed past the accepted quit: %v", a.tabs[0].Messages)
	}
	if !a.quitAccepted {
		t.Fatal("acceptQuit did not close the event stream")
	}
}

// A failed backend shutdown keeps the window alive for retry: the backend
// retained its data lock and skipped cleanup on purpose, so the old flow —
// quitting regardless — stranded the user with no window and no backend.
func TestShutdownFailureKeepsTheWindowAlive(t *testing.T) {
	busy := &conversation{ID: "busy", Workspace: t.TempDir(), Busy: true, RunID: "run-1"}
	a := &desktop{closing: true, quitAccepted: true, tabs: []*conversation{busy}, openSessions: map[string]*conversation{}}

	a.applyShutdownOutcome(errors.New("checkpoint timeout"))

	if a.quitReady {
		t.Fatal("a failed shutdown readied the quit")
	}
	if a.closing || a.quitAccepted {
		t.Fatal("a failed shutdown must reopen the UI state")
	}
	if busy.Busy || busy.RunID != "" {
		t.Fatal("runs the shutdown cancelled were left busy")
	}
	if len(a.notifications) == 0 {
		t.Fatal("the failure was not surfaced to the user")
	}
}
