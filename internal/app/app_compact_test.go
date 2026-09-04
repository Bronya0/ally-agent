package app

import (
	"strings"
	"testing"
)

// TestNewAppInitializesCompactionMaps guards the compactSession in-flight
// state: a nil map write while a.mu is held panics and leaves the mutex
// locked forever, deadlocking the whole app (ESC cancel, window shutdown and
// every later binding call hang). The maps must be initialized eagerly.
func TestNewAppInitializesCompactionMaps(t *testing.T) {
	app := NewApp()
	if app.compactingSessions == nil || app.compactingCancels == nil {
		t.Fatal("NewApp() must initialize compactingSessions and compactingCancels")
	}
}

// TestCompactSessionCleansUpCompactionState drives the manual compaction
// entry point on a zero-value App (nil compaction maps) so the lazy guard in
// front of the map writes is exercised directly: before the guard this call
// panicked with "assignment to entry in nil map" while holding a.mu. The call
// must fail with the expected validation error and clean up its in-flight
// compaction state.
func TestCompactSessionCleansUpCompactionState(t *testing.T) {
	app := &App{initialized: true}
	app.config.Model = "test-model"
	app.config.APIKey = "test-key"
	const sessionID = "session-compact-guard"

	if _, err := app.CompactSession(sessionID, ""); err == nil || !strings.Contains(err.Error(), "no messages to compact") {
		t.Fatalf("CompactSession() error = %v, want no messages to compact", err)
	}
	if app.compactSessionRunning(sessionID) {
		t.Fatal("compaction in-flight state must be cleaned up after the call ends")
	}
	if err := app.CancelCompaction(sessionID); err != nil {
		t.Fatalf("CancelCompaction() error = %v", err)
	}
}
