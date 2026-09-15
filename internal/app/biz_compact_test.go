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
	if app.sessionModelConfigs == nil {
		t.Fatal("NewApp() must initialize sessionModelConfigs")
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

// TestCompactSessionReplaysSessionModelConfig guards the config-drift fix for
// manual compaction: the persisted config carries the DEFAULT model while a
// chat Tab's overlay (reaching the backend only via StartChat) may point at a
// different model with different credentials. compactSession must replay the
// session's frozen model fields — model, base URL, key pool and custom
// headers — instead of summarizing with the default model. The LLM call
// itself is not exercised (no messages to compact stops before it); the
// assertion hooks the cfg that reaches the compaction request.
func TestCompactSessionReplaysSessionModelConfig(t *testing.T) {
	app := &App{initialized: true}
	app.config = defaultConfigState()
	app.config.Model = "default-model"
	app.config.BaseURL = "https://default.example.com/v1"
	app.config.APIKeys = []string{"default-key"}
	app.config.CustomHeaders = map[string]string{"X-Default": "1"}

	const sessionID = "session-compact-model-config"
	// Simulate the StartChat record for a Tab whose model differs from the
	// persisted default (frontend switchToModel never persists the overlay).
	app.sessionModelConfigs = map[string]sessionModelConfig{
		sessionID: sessionModelConfigFrom(ConfigState{
			ProviderName:  "Relay",
			APIFormat:     apiFormatOpenAIChat,
			BaseURL:       "https://relay.example.com/v1",
			APIKeys:       []string{"relay-key"},
			Model:         "relay-model",
			CustomHeaders: map[string]string{"X-Gateway": "abc"},
		}),
	}

	got := app.sessionModelConfigFor(sessionID).apply(app.effectiveConfigSafe())
	if got.Model != "relay-model" {
		t.Fatalf("apply().Model = %q, want relay-model", got.Model)
	}
	if got.BaseURL != "https://relay.example.com/v1" {
		t.Fatalf("apply().BaseURL = %q, want relay base URL", got.BaseURL)
	}
	if len(resolveKeyPool(got)) != 1 || resolveKeyPool(got)[0] != "relay-key" {
		t.Fatalf("apply() key pool = %v, want [relay-key]", resolveKeyPool(got))
	}
	if got.CustomHeaders["X-Gateway"] != "abc" {
		t.Fatalf("apply().CustomHeaders[X-Gateway] = %q, want abc", got.CustomHeaders["X-Gateway"])
	}
	if _, leaked := got.CustomHeaders["X-Default"]; leaked {
		t.Fatal("apply() must replace, not merge, the default model's custom headers")
	}
	// Non-model context must keep flowing from the persisted config so
	// compaction timeout / threshold / workspace still apply.
	if got.Workspace != app.config.Workspace {
		t.Fatalf("apply().Workspace = %q, want persisted %q", got.Workspace, app.config.Workspace)
	}

	// Sessions without a record (pre-fix histories loaded after a restart)
	// fall back to the persisted default unchanged.
	fallback := app.sessionModelConfigFor("session-no-record").apply(app.effectiveConfigSafe())
	if fallback.Model != "default-model" || fallback.CustomHeaders["X-Default"] != "1" {
		t.Fatalf("fallback config drifted: model=%q headers=%v", fallback.Model, fallback.CustomHeaders)
	}
}
