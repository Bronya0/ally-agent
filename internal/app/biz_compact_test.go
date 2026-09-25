package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// compactEventRecorder captures the compaction event stream, so a test can
// assert what the UI would have seen while the summary was produced.
type compactEventRecorder struct {
	mu     sync.Mutex
	events map[string][]string
}

func (r *compactEventRecorder) Emit(name string, payload any) {
	encoded, _ := json.Marshal(payload)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.events == nil {
		r.events = map[string][]string{}
	}
	r.events[name] = append(r.events[name], string(encoded))
}

func (r *compactEventRecorder) payloads(name string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events[name]...)
}

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

// TestCompactSessionManualAlwaysRunsTheSummaryTier pins the single compaction
// strategy: manual, threshold and overflow all go through one LLM summary that
// rewrites the history. Below the auto threshold the manual button used to stop
// at the (now deleted) micro-compaction tier, which rewrote the middle of the
// history in place and broke the provider prompt cache from that point on.
func TestCompactSessionManualAlwaysRunsTheSummaryTier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseChatChunk("## 已完成工作\n- 压缩前的历史"))
		fmt.Fprint(w, sseChatFinishChunk("stop"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil // skip token-stat persistence
	app.config = defaultConfigState()
	app.config.Model = "test-model"
	app.config.APIKey = "test-key"
	app.config.APIFormat = apiFormatOpenAIChat
	app.config.BaseURL = server.URL
	recorder := &compactEventRecorder{}
	app.events = recorder
	const sessionID = "session-manual-single-tier"

	// The todo list of the plan tool only lives in its tool result: the rewrite
	// must carry it over verbatim instead of trusting the prose summary.
	history := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "go"},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "p1", Function: openai.FunctionCall{Name: "plan", Arguments: `{"todos":[]}`}}}},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "p1", Content: "Todos:\n- [ ] 收尾前端"},
	}
	app.saveHistory(sessionID, history)

	result, err := app.CompactSession(sessionID, "")
	if err != nil {
		t.Fatalf("CompactSession() error = %v", err)
	}
	if result["tier"] != "summary" {
		t.Fatalf("tier = %v, want summary (one LLM tier only)", result["tier"])
	}
	summary, _ := result["summary"].(string)
	if !strings.Contains(summary, "已完成工作") {
		t.Fatalf("summary = %q, want the model summary", summary)
	}

	// The streamed deltas and the one completion event are what the UI renders.
	if len(recorder.payloads("compact:delta")) == 0 {
		t.Fatal("the summary call must stream compact:delta events")
	}
	done := recorder.payloads("compact:done")
	if len(done) != 1 || !strings.Contains(done[0], `"ok":true`) {
		t.Fatalf("compact:done = %#v, want exactly one with ok=true", done)
	}

	after := app.loadSessionHistoryCopy(sessionID)
	if len(after) != 1 || after[0].Role != openai.ChatMessageRoleUser {
		t.Fatalf("history after compaction = %#v, want the single summary message", after)
	}
	if !strings.Contains(after[0].Content, "<ally-plan-snapshot>") || !strings.Contains(after[0].Content, "收尾前端") {
		t.Fatalf("the plan snapshot must survive the rewrite: %q", after[0].Content)
	}
	if after[0].Content != summary {
		t.Fatalf("the persisted summary and the reported one must not drift apart:\n%q\n%q", after[0].Content, summary)
	}
	if app.compactSessionRunning(sessionID) {
		t.Fatal("in-flight compaction state must be cleaned up")
	}
}

// TestLatestPlanSnapshot pins the lookup: the newest plan result wins, but a
// result already replaced by the placeholder carries no todo state and must be
// skipped in favour of the newest one that still has some.
func TestLatestPlanSnapshot(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "p1", Function: openai.FunctionCall{Name: "plan"}}}},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "p1", Content: "Todos:\n- [ ] old"},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "r1", Function: openai.FunctionCall{Name: "read"}}}},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "r1", Content: "file body"},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "p2", Function: openai.FunctionCall{Name: "plan"}}}},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "p2", Content: toolResultPlaceholder},
	}
	if got := latestPlanSnapshot(messages); got != "Todos:\n- [ ] old" {
		t.Fatalf("latestPlanSnapshot() = %q, want the newest result that still carries state", got)
	}
	if got := latestPlanSnapshot(messages[3:]); got != "" {
		t.Fatalf("latestPlanSnapshot() without a plan call = %q, want empty", got)
	}
}
