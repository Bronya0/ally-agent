package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestMergeConfigKeepsModelsWhenOmitted(t *testing.T) {
	base := ConfigState{
		Models: []ModelConfig{{Model: "saved-model"}},
	}

	got := mergeConfig(base, ConfigState{})

	if len(got.Models) != 1 {
		t.Fatalf("expected omitted model list to keep existing models, got %d", len(got.Models))
	}
	if got.Models[0].Model != "saved-model" {
		t.Fatalf("expected saved model to remain, got %q", got.Models[0].Model)
	}
}

func TestCancelRunKeepsSessionRegisteredUntilRunExits(t *testing.T) {
	app := NewApp()
	runID := "run-cancel-race"
	sessionID := "session-cancel-race"
	ctx, cancel := context.WithCancel(context.Background())

	app.mu.Lock()
	app.runs[runID] = cancel
	app.runSessions[runID] = sessionID
	app.histories[sessionID] = []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "keep"}}
	app.mu.Unlock()

	runCanExit := make(chan struct{})
	runExited := make(chan struct{})
	go func() {
		defer close(runExited)
		<-ctx.Done()
		<-runCanExit
		app.finishRun(runID)
	}()

	if err := app.CancelRun(runID); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("CancelRun did not cancel the run context promptly")
	}

	app.mu.Lock()
	_, runStillRegistered := app.runs[runID]
	registeredSession := app.runSessions[runID]
	app.mu.Unlock()
	if !runStillRegistered || registeredSession != sessionID {
		t.Fatalf("cancelled run was unregistered before exit: run=%v session=%q", runStillRegistered, registeredSession)
	}
	if err := app.ReleaseSession(sessionID); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("ReleaseSession() while cancelled run is exiting = %v, want still running error", err)
	}
	if err := app.DeleteSession(sessionID); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("DeleteSession() while cancelled run is exiting = %v, want still running error", err)
	}

	close(runCanExit)
	select {
	case <-runExited:
	case <-time.After(time.Second):
		t.Fatal("run cleanup did not finish")
	}

	if err := app.ReleaseSession(sessionID); err != nil {
		t.Fatalf("ReleaseSession() after run exit error = %v", err)
	}
	app.mu.Lock()
	_, runStillRegistered = app.runs[runID]
	_, sessionStillRegistered := app.runSessions[runID]
	_, historyStillPresent := app.histories[sessionID]
	app.mu.Unlock()
	if runStillRegistered || sessionStillRegistered || historyStillPresent {
		t.Fatalf("run/session cleanup incomplete: run=%v session=%v history=%v", runStillRegistered, sessionStillRegistered, historyStillPresent)
	}
}

func TestMergeConfigDefaultsReasoningTags(t *testing.T) {
	got := mergeConfig(ConfigState{}, ConfigState{
		Models: []ModelConfig{{Model: "test-model"}},
	})
	if got.ReasoningTag != defaultReasoningTag {
		t.Fatalf("expected default reasoning tag %q, got %q", defaultReasoningTag, got.ReasoningTag)
	}
	if len(got.Models) != 1 || got.Models[0].ReasoningTag != defaultReasoningTag {
		t.Fatalf("expected saved model reasoning tag to default to %q, got %#v", defaultReasoningTag, got.Models)
	}
}

func TestMergeConfigClearsModelsWhenEmptyListProvided(t *testing.T) {
	base := ConfigState{
		Models: []ModelConfig{{Model: "saved-model"}},
	}

	got := mergeConfig(base, ConfigState{Models: []ModelConfig{}})

	if len(got.Models) != 0 {
		t.Fatalf("expected explicit empty model list to clear models, got %d", len(got.Models))
	}
}

func TestMergeConfigAcceptsExplicitZeroTemperature(t *testing.T) {
	base := ConfigState{Temperature: 0.7}
	overlay := ConfigState{Temperature: 0, temperatureSet: true}

	got := mergeConfig(base, overlay)

	if got.Temperature != 0 {
		t.Fatalf("expected explicit zero temperature, got %v", got.Temperature)
	}
}

func TestParsePythonVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "stdout", in: "Python 3.12.4\n", want: "3.12.4"},
		{name: "stderr style", in: "Python 3.11.9\r\n", want: "3.11.9"},
		{name: "unexpected", in: "not found", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePythonVersion(tt.in); got != tt.want {
				t.Fatalf("parsePythonVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPythonExecutableSkipsWindowsAppsAlias(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "python alias", path: `C:\Users\me\AppData\Local\Microsoft\WindowsApps\python.exe`, want: true},
		{name: "python3 alias", path: `C:/Users/me/AppData/Local/Microsoft/WindowsApps/python3.exe`, want: true},
		{name: "real python", path: `C:\Python312\python.exe`, want: false},
		{name: "venv python", path: `F:\project\.venv\Scripts\python.exe`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWindowsAppsPythonAliasPath(tt.path); got != tt.want {
				t.Fatalf("isWindowsAppsPythonAliasPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestPythonExecutableForRuntimeUsesLookPathAndSkipsAliases(t *testing.T) {
	aliasLookPath := func(string) (string, error) {
		return `C:\Users\me\AppData\Local\Microsoft\WindowsApps\python.exe`, nil
	}
	if got, ok := pythonExecutableForRuntime(aliasLookPath); runtime.GOOS == "windows" && (ok || got != "") {
		t.Fatalf("expected WindowsApps alias to be skipped on Windows, got %q ok=%v", got, ok)
	}

	realLookPath := func(string) (string, error) {
		return `C:\Python312\python.exe`, nil
	}
	if got, ok := pythonExecutableForRuntime(realLookPath); !ok || got == "" {
		t.Fatalf("expected real python path to be accepted, got %q ok=%v", got, ok)
	}
}

func TestSaveConfigClearsCustomPrompt(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	app := NewApp()
	app.initialized = true
	app.configPath = configPath
	app.config = defaultConfigState()
	app.config.CustomPrompt = "saved prompt"

	req := app.config
	req.CustomPrompt = ""
	if err := app.SaveConfig(req); err != nil {
		t.Fatal(err)
	}

	if app.config.CustomPrompt != "" {
		t.Fatalf("expected in-memory custom prompt to be cleared, got %q", app.config.CustomPrompt)
	}

	got, err := app.ReloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.CustomPrompt != "" {
		t.Fatalf("expected reloaded custom prompt to be cleared, got %q", got.CustomPrompt)
	}
}

func TestReloadConfigLoadsModelsFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := []byte(`{
  "providerName": "Reloaded Provider",
  "baseUrl": "https://example.test/v1",
  "model": "reloaded-model",
  "models": [
    {
      "name": "Reloaded",
      "providerName": "Reloaded Provider",
      "baseUrl": "https://example.test/v1",
      "model": "reloaded-model"
    }
  ]
}`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.initialized = true
	app.configPath = configPath
	app.config = ConfigState{
		ProviderName: "Old Provider",
		BaseURL:      "https://old.test/v1",
		Model:        "old-model",
		Models:       []ModelConfig{{Model: "old-model"}},
	}

	got, err := app.ReloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "reloaded-model" {
		t.Fatalf("expected reloaded model, got %q", got.Model)
	}
	if len(got.Models) != 1 || got.Models[0].Model != "reloaded-model" {
		t.Fatalf("expected reloaded model list, got %#v", got.Models)
	}

	mem, err := app.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if mem.Model != got.Model || len(mem.Models) != 1 || mem.Models[0].Model != "reloaded-model" {
		t.Fatalf("expected in-memory config to be reloaded, got %#v", mem)
	}
}

func TestAgentDelegateSemaphoreRespectsCancelledContext(t *testing.T) {
	app := NewApp()
	for i := 0; i < cap(app.subSem); i++ {
		app.subSem <- struct{}{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan toolResult, 1)
	go func() {
		done <- app.executeTool(ctx, ConfigState{}, "session-1", "subagent", []byte(`{"task":"noop"}`))
	}()

	select {
	case result := <-done:
		if result.OK {
			t.Fatalf("expected cancelled delegate to fail, got %#v", result)
		}
		if !strings.Contains(result.Error, "context canceled") {
			t.Fatalf("expected context cancellation error, got %q", result.Error)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("subagent blocked while waiting for a sub-agent slot after context cancellation")
	}
}

func TestToolCallProgressTrackerEmitsStreamingUpdates(t *testing.T) {
	tracker := newToolCallProgressTracker()
	toolCalls := []openai.ToolCall{}
	idx := 0

	mergeToolCallDeltas(&toolCalls, []openai.ToolCall{{
		Index: &idx,
		ID:    "call_create",
		Type:  openai.ToolTypeFunction,
		Function: openai.FunctionCall{
			Name:      "create_file",
			Arguments: `{"path":"demo.txt","content":"alpha`,
		},
	}})

	events := tracker.events("run-1", "session-1", "0:0", toolCalls, nil)
	if len(events) != 1 {
		t.Fatalf("expected initial streaming event, got %#v", events)
	}
	if events[0].Name != "tool:start" {
		t.Fatalf("expected tool:start, got %q", events[0].Name)
	}
	if events[0].Payload["toolCallIndex"] != 0 {
		t.Fatalf("expected toolCallIndex 0, got %#v", events[0].Payload["toolCallIndex"])
	}
	if events[0].Payload["toolBatchId"] != "0:0" {
		t.Fatalf("expected toolBatchId 0:0, got %#v", events[0].Payload["toolBatchId"])
	}
	if events[0].Payload["args"] != `{"path":"demo.txt","content":"alpha` {
		t.Fatalf("expected partial args in start event, got %#v", events[0].Payload["args"])
	}

	mergeToolCallDeltas(&toolCalls, []openai.ToolCall{{
		Index: &idx,
		Function: openai.FunctionCall{
			Arguments: `\nbeta"}`,
		},
	}})

	events = tracker.events("run-1", "session-1", "0:0", toolCalls, nil)
	if len(events) != 1 {
		t.Fatalf("expected streaming update event, got %#v", events)
	}
	if events[0].Name != "tool:update" {
		t.Fatalf("expected tool:update, got %q", events[0].Name)
	}
	if events[0].Payload["args"] != `{"path":"demo.txt","content":"alpha\nbeta"}` {
		t.Fatalf("expected accumulated args in update event, got %#v", events[0].Payload["args"])
	}

	if events = tracker.events("run-1", "session-1", "0:0", toolCalls, nil); len(events) != 0 {
		t.Fatalf("expected no duplicate event without changes, got %#v", events)
	}
}

// TestToolCallProgressTrackerThrottlesLargeUpdates verifies that large
// streaming argument payloads (e.g. create_file with thousands of lines) are
// throttled to avoid flooding the frontend with O(N^2) data, while small
// payloads pass through unchanged and forceEvents always emits the final state.
func TestToolCallProgressTrackerThrottlesLargeUpdates(t *testing.T) {
	tracker := newToolCallProgressTracker()
	toolCalls := []openai.ToolCall{}
	idx := 0

	// Large initial payload (above toolUpdateThreshold) emits tool:start.
	largePrefix := `{"path":"big.txt","content":"`
	largeSuffix := strings.Repeat("a", toolUpdateThreshold+100) + `"`
	mergeToolCallDeltas(&toolCalls, []openai.ToolCall{{
		Index: &idx,
		ID:    "call_big",
		Type:  openai.ToolTypeFunction,
		Function: openai.FunctionCall{
			Name:      "create_file",
			Arguments: largePrefix + largeSuffix,
		},
	}})
	events := tracker.events("run-1", "session-1", "0:0", toolCalls, nil)
	if len(events) != 1 || events[0].Name != "tool:start" {
		t.Fatalf("expected one tool:start for large payload, got %#v", events)
	}

	// A rapid second update with still-large args should be throttled to zero.
	mergeToolCallDeltas(&toolCalls, []openai.ToolCall{{
		Index: &idx,
		Function: openai.FunctionCall{
			Arguments: `more"`,
		},
	}})
	if events = tracker.events("run-1", "session-1", "0:0", toolCalls, nil); len(events) != 0 {
		t.Fatalf("expected throttled update to emit nothing, got %#v", events)
	}

	// forceEvents ignores the throttle and always emits the final state.
	if events = tracker.forceEvents("run-1", "session-1", "0:0", toolCalls, nil); len(events) != 1 {
		t.Fatalf("expected forceEvents to emit the final state, got %#v", events)
	}
	if events[0].Name != "tool:update" {
		t.Fatalf("expected tool:update from forceEvents, got %q", events[0].Name)
	}
	if events[0].Payload["args"] != largePrefix+largeSuffix+`more"` {
		t.Fatalf("expected full accumulated args from forceEvents, got %#v", events[0].Payload["args"])
	}

	// Small payloads (below threshold) are never throttled.
	smallTracker := newToolCallProgressTracker()
	smallCalls := []openai.ToolCall{}
	sidx := 0
	mergeToolCallDeltas(&smallCalls, []openai.ToolCall{{
		Index: &sidx,
		ID:    "call_small",
		Type:  openai.ToolTypeFunction,
		Function: openai.FunctionCall{
			Name:      "list_files",
			Arguments: `{"path":"."}`,
		},
	}})
	if events := smallTracker.events("run-1", "session-1", "0:0", smallCalls, nil); len(events) != 1 || events[0].Name != "tool:start" {
		t.Fatalf("expected tool:start for small payload, got %#v", events)
	}
	mergeToolCallDeltas(&smallCalls, []openai.ToolCall{{
		Index: &sidx,
		Function: openai.FunctionCall{
			Arguments: `,"depth":2}`,
		},
	}})
	if events = smallTracker.events("run-1", "session-1", "0:0", smallCalls, nil); len(events) != 1 || events[0].Name != "tool:update" {
		t.Fatalf("expected unthrottled tool:update for small payload, got %#v", events)
	}
}

func TestBuildMessagesWithFrontendMessagesKeepsSavedToolActivity(t *testing.T) {
	app := NewApp()
	sessionID := "session-with-tools"
	toolSummary := "Tool activity from previous turn:\n- edit({\"path\":\"app.go\"}): success - changed app.go"
	app.histories[sessionID] = []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "change the file"},
		{Role: openai.ChatMessageRoleAssistant, Content: toolSummary},
		{Role: openai.ChatMessageRoleAssistant, Content: "done"},
	}

	got := app.buildMessages(ChatRequest{
		SessionID: sessionID,
		Messages: []ChatMessageInput{
			{Role: openai.ChatMessageRoleUser, Content: "change the file"},
			{Role: openai.ChatMessageRoleAssistant, Content: "done"},
			{Role: openai.ChatMessageRoleUser, Content: "what changed?"},
		},
	}, ConfigState{}, nil)

	if !messageContentExists(got, toolSummary) {
		t.Fatalf("expected saved tool activity summary to be preserved in model context; got %#v", got)
	}
	if !messageContentExists(got, "what changed?") {
		t.Fatalf("expected frontend request messages to remain in model context; got %#v", got)
	}
}

func TestBuildMessagesIgnoresFrontendSystemMessages(t *testing.T) {
	app := NewApp()
	got := app.buildMessages(ChatRequest{
		Messages: []ChatMessageInput{
			{Role: openai.ChatMessageRoleSystem, Content: "malicious system override"},
			{Role: openai.ChatMessageRoleUser, Content: "hello"},
		},
	}, ConfigState{}, nil)

	if messageContentExists(got, "malicious system override") {
		t.Fatalf("frontend system message must not enter model context; got %#v", got)
	}
	if !messageContentExists(got, "hello") {
		t.Fatalf("expected user message to remain; got %#v", got)
	}
}

func TestBuildMessagesIgnoresBackendSystemHistory(t *testing.T) {
	app := NewApp()
	sessionID := "session-with-system-history"
	app.histories[sessionID] = []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "malicious saved system override"},
		{Role: openai.ChatMessageRoleUser, Content: "hello"},
	}

	got := app.buildMessages(ChatRequest{SessionID: sessionID}, ConfigState{}, nil)

	if messageContentExists(got, "malicious saved system override") {
		t.Fatalf("backend system history must not enter model context; got %#v", got)
	}
	if !messageContentExists(got, "hello") {
		t.Fatalf("expected user history to remain; got %#v", got)
	}
}

func TestBuildMessagesInsertsGrillModeInstructionAsSystemPolicy(t *testing.T) {
	app := NewApp()
	got := app.buildMessages(ChatRequest{
		GrillMode: true,
		Messages: []ChatMessageInput{
			{Role: openai.ChatMessageRoleUser, Content: "old question"},
			{Role: openai.ChatMessageRoleAssistant, Content: "old answer"},
			{Role: openai.ChatMessageRoleUser, Content: "new plan"},
		},
	}, ConfigState{}, nil)

	grillAt := -1
	firstNonSystemAt := len(got)
	for i, msg := range got {
		if isGrillModeInstructionMessage(msg) {
			grillAt = i
		}
		if firstNonSystemAt == len(got) && msg.Role != openai.ChatMessageRoleSystem {
			firstNonSystemAt = i
		}
	}
	if grillAt < 0 {
		t.Fatalf("expected grill mode instruction in model context; got %#v", got)
	}
	if got[grillAt].Role != openai.ChatMessageRoleSystem {
		t.Fatalf("expected grill instruction to use system role, got %#v", got[grillAt])
	}
	if grillAt != firstNonSystemAt-1 {
		t.Fatalf("expected grill instruction after system context and before conversation history, grillAt=%d firstNonSystemAt=%d messages=%#v", grillAt, firstNonSystemAt, got)
	}
	if !messageContentExists(got, "new plan") {
		t.Fatalf("expected latest user message in model context; got %#v", got)
	}
	joined := joinMessageContents(got)
	if !strings.Contains(joined, "Call 'ask' as the only tool call") || !strings.Contains(joined, "<ally-grill-complete/>") {
		t.Fatalf("expected grill mode to enforce ask-or-complete protocol; got %#v", got)
	}
}

func TestStripGrillCompletionMarker(t *testing.T) {
	content, complete := stripGrillCompletionMarker("  <ally-grill-complete/>\nAll decisions are resolved.  ")
	if !complete || content != "All decisions are resolved." {
		t.Fatalf("unexpected grill completion parsing: complete=%v content=%q", complete, content)
	}
	if _, complete := stripGrillCompletionMarker("What should we do next?"); complete {
		t.Fatal("plain assistant text must not complete grill mode")
	}
	if _, complete := stripGrillCompletionMarker("<ally-grill-complete/>"); complete {
		t.Fatal("grill completion requires a final decision summary")
	}
}

func TestGrillModeDoesNotAutoContinueGoal(t *testing.T) {
	if shouldAutoContinueGoal(true) {
		t.Fatal("grill mode must wait for the user's answer instead of auto-continuing a goal")
	}
	if !shouldAutoContinueGoal(false) {
		t.Fatal("normal goal mode should retain automatic continuation")
	}
}

func TestSaveHistoryDropsGrillModeInstruction(t *testing.T) {
	app := NewApp()
	sessionID := "session-grill-history"
	app.saveHistory(sessionID, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "before"},
		grillModeInstructionMessage(),
		{Role: openai.ChatMessageRoleUser, Content: "plan"},
		{Role: openai.ChatMessageRoleAssistant, Content: "question"},
	})

	got := app.histories[sessionID]
	if len(got) != 3 {
		t.Fatalf("expected saved history to drop grill instruction, got %#v", got)
	}
	for _, msg := range got {
		if isGrillModeInstructionMessage(msg) {
			t.Fatalf("grill mode instruction must not be persisted: %#v", got)
		}
	}
}

func TestLoadHistoryLockedSanitizesSystemMessages(t *testing.T) {
	app := NewApp()
	sessionID := "session-disk-system"
	app.historiesDir = t.TempDir()
	diskPath := filepath.Join(app.historiesDir, url.PathEscape(sessionID)+".json")
	data, err := json.Marshal([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "malicious disk system override"},
		{Role: openai.ChatMessageRoleUser, Content: "hello from disk"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(diskPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got := app.loadSessionHistoryCopy(sessionID)

	if messageContentExists(got, "malicious disk system override") {
		t.Fatalf("disk system history must not enter loaded history; got %#v", got)
	}
	if !messageContentExists(got, "hello from disk") {
		t.Fatalf("expected disk user history to remain; got %#v", got)
	}
}

func TestSubagentInstructionContextIncludesProjectAndCustomInstructions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Project rule: use focused tests.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	got := app.buildSubagentInstructionContext(ConfigState{Workspace: root, CustomPrompt: "Custom rule: concise summaries."})

	if !strings.Contains(got, "Project rule: use focused tests.") {
		t.Fatalf("expected project instructions in subagent context, got %q", got)
	}
	if !strings.Contains(got, "Custom rule: concise summaries.") {
		t.Fatalf("expected custom instructions in subagent context, got %q", got)
	}
}

func TestSystemPromptDefinesWaitSequencing(t *testing.T) {
	prompt := defaultSystemPrompt(nil, "", "", "")
	for _, expected := range []string{"Use `wait` only", "only tool in that model response", "verify the condition after it completes"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("system prompt missing wait guidance %q", expected)
		}
	}
}

func TestBatchReadSchemaIncludesCanonicalExamples(t *testing.T) {
	var description string
	for _, tool := range chatTools() {
		if tool.Function != nil && tool.Function.Name == "read" {
			description = tool.Function.Description
			break
		}
	}
	for _, expected := range []string{`{"files":[{"path":"app.go"}]}`, "Do not pass top-level path", "string array"} {
		if !strings.Contains(description, expected) {
			t.Fatalf("read description missing canonical guidance %q: %s", expected, description)
		}
	}
}

func TestEveryBuiltinToolDescriptionIncludesCanonicalExample(t *testing.T) {
	for _, tool := range chatTools() {
		if tool.Function == nil {
			continue
		}
		if !strings.Contains(tool.Function.Description, "Canonical JSON example(s):") {
			t.Fatalf("tool %s is missing a canonical JSON example", tool.Function.Name)
		}
	}
}

func TestEditDescriptionIncludesSingleAndCrossFileMultiChangeExamples(t *testing.T) {
	var description string
	for _, tool := range chatTools() {
		if tool.Function != nil && tool.Function.Name == "edit" {
			description = tool.Function.Description
			break
		}
	}
	for _, expected := range []string{
		"single file with multiple changes",
		`"oldText":"const oldName = \"ally\""`,
		"multiple files with multiple changes per file",
		`"path":"frontend/src/App.vue"`,
		`"oldText":"oldSubtitle"`,
	} {
		if !strings.Contains(description, expected) {
			t.Fatalf("edit description missing example guidance %q: %s", expected, description)
		}
	}
}

func TestSystemPromptExplainsRunCommandOutsidePathRecovery(t *testing.T) {
	prompt := defaultSystemPrompt(nil, "", "", "")
	for _, expected := range []string{"`E_PATH_OUTSIDE`", "Do not retry the unchanged command", "literal verifiable target", "read the returned Chinese explanation"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("system prompt missing run_command recovery guidance %q", expected)
		}
	}
}

func TestGoalProgressIsAppendedAfterStableHistory(t *testing.T) {
	app := NewApp()
	sessionID := "goal-cache-session"
	app.goalStates[goalSessionKey(sessionID)] = &GoalState{
		GoalID:     "goal-1",
		Objective:  "improve cache reuse",
		Status:     "active",
		TurnsUsed:  3,
		TurnBudget: 12,
	}

	messages := app.buildMessages(ChatRequest{
		SessionID: sessionID,
		Messages:  []ChatMessageInput{{Role: openai.ChatMessageRoleUser, Content: "continue"}},
	}, ConfigState{}, nil)
	if len(messages) < 2 {
		t.Fatalf("expected system, history, and goal progress messages: %#v", messages)
	}
	stable := joinMessageContents(app.buildSystemContextMessages(sessionID, ConfigState{}, nil))
	if strings.Contains(stable, "Continuation turns used") || strings.Contains(stable, "Status: active") {
		t.Fatalf("dynamic goal progress must not be in the stable system prefix: %s", stable)
	}
	last := messages[len(messages)-1]
	if last.Role != openai.ChatMessageRoleUser || !strings.Contains(last.Content, "<ally-goal-progress>") || !strings.Contains(last.Content, "Continuation turns used: 3") {
		t.Fatalf("goal progress should be appended at the request tail, got %#v", last)
	}
	sanitized := sanitizeHistoryMessages(messages)
	for _, message := range sanitized {
		if isGoalProgressMessage(message) {
			t.Fatalf("goal progress must not persist in session history: %#v", sanitized)
		}
	}
}

func TestSubagentToolsExcludeInteractiveToolsAndIncludeMCP(t *testing.T) {
	app := NewApp()
	app.mcpManager = NewMcpManager(t.TempDir(), nil)
	app.mcpManager.clients["demo"] = &McpClientHandle{
		ServerName: "demo",
		Status:     "connected",
		ToolDefs: []McpDiscoveredTool{{
			ServerName: "demo", Name: "lookup", FunctionName: "mcp__demo__lookup",
			Schema: map[string]any{"type": "object", "properties": map[string]any{}},
		}},
	}
	tools := app.subagentTools(ConfigState{})
	foundMCP := false
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		if tool.Function.Name == "memory_write" || tool.Function.Name == "scheduled_task" || tool.Function.Name == "ask" || tool.Function.Name == "subagent" {
			t.Fatalf("sub-agent must not receive %s tool schema", tool.Function.Name)
		}
		if tool.Function.Name == "mcp__demo__lookup" {
			foundMCP = true
		}
	}
	if !foundMCP {
		t.Fatal("sub-agent should receive connected MCP tool schemas")
	}
}

func TestSaveHistoryPreservesToolCallsAndResults(t *testing.T) {
	app := NewApp()
	sessionID := "session-raw-tools"
	toolResult := `{"ok":true,"data":{"files":[{"path":"a.txt","content":"1: alpha\n2: beta\n"}]}}`

	app.saveHistory(sessionID, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "read a.txt"},
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{
				ID:   "call_1",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "batch_read",
					Arguments: `{"path":"a.txt"}`,
				},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_1", Content: toolResult},
		{Role: openai.ChatMessageRoleAssistant, Content: "a.txt contains alpha and beta."},
	})

	got := app.histories[sessionID]
	if len(got) != 4 {
		t.Fatalf("expected 4 persisted model messages, got %d: %#v", len(got), got)
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].Function.Name != "batch_read" {
		t.Fatalf("expected assistant tool call to be preserved, got %#v", got[1])
	}
	if got[2].Role != openai.ChatMessageRoleTool || got[2].ToolCallID != "call_1" || got[2].Content != toolResult {
		t.Fatalf("expected raw tool result to be preserved, got %#v", got[2])
	}
}

func TestBuildMessagesWithRestoredFrontendUsesBackendToolResults(t *testing.T) {
	app := NewApp()
	sessionID := "session-restore-tools"
	toolResult := `{"ok":true,"data":{"files":[{"path":"a.txt","content":"1: alpha\n2: beta\n"}]}}`
	app.histories[sessionID] = []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "read a.txt"},
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{
				ID:   "call_1",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "batch_read",
					Arguments: `{"path":"a.txt"}`,
				},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_1", Content: toolResult},
		{Role: openai.ChatMessageRoleAssistant, Content: "a.txt contains alpha and beta."},
	}

	got := app.buildMessages(ChatRequest{
		SessionID: sessionID,
		Messages: []ChatMessageInput{
			{Role: openai.ChatMessageRoleUser, Content: "read a.txt"},
			{Role: openai.ChatMessageRoleAssistant, Content: "a.txt contains alpha and beta."},
			{Role: openai.ChatMessageRoleUser, Content: "what did the file contain?"},
		},
	}, ConfigState{}, nil)

	if !messageContentExists(got, toolResult) {
		t.Fatalf("expected restored backend tool result in context; got %#v", got)
	}
	if countMessageContent(got, "read a.txt") != 1 {
		t.Fatalf("expected restored visible history not to be duplicated; got %#v", got)
	}
	if !messageContentExists(got, "what did the file contain?") {
		t.Fatalf("expected new frontend message appended; got %#v", got)
	}
}

func messageContentExists(messages []openai.ChatCompletionMessage, content string) bool {
	for _, m := range messages {
		if m.Content == content {
			return true
		}
	}
	return false
}

func countMessageContent(messages []openai.ChatCompletionMessage, content string) int {
	count := 0
	for _, m := range messages {
		if m.Content == content {
			count++
		}
	}
	return count
}

func TestGetSubagentsReturnsSnapshot(t *testing.T) {
	app := NewApp()
	app.subRuns["sub-1"] = &SubagentRun{
		ID:          "sub-1",
		Description: "test",
		Status:      "running",
		FilesRead:   []string{"a.go"},
		FilesEdited: []string{"b.go"},
		ToolCalls: []SubToolEvent{{
			ToolCallID: "tool-1",
			Name:       "read_file",
			Status:     "success",
		}},
	}

	got := app.GetSubagents()
	if len(got) != 1 {
		t.Fatalf("expected one sub-agent, got %d", len(got))
	}
	got[0].Status = "mutated"
	got[0].FilesRead[0] = "mutated-read"
	got[0].FilesEdited[0] = "mutated-edit"
	got[0].ToolCalls[0].Status = "mutated-tool"

	stored := app.subRuns["sub-1"]
	if stored.Status != "running" {
		t.Fatalf("expected stored status to remain running, got %q", stored.Status)
	}
	if stored.FilesRead[0] != "a.go" {
		t.Fatalf("expected stored filesRead to remain unchanged, got %q", stored.FilesRead[0])
	}
	if stored.FilesEdited[0] != "b.go" {
		t.Fatalf("expected stored filesEdited to remain unchanged, got %q", stored.FilesEdited[0])
	}
	if stored.ToolCalls[0].Status != "success" {
		t.Fatalf("expected stored tool call status to remain success, got %q", stored.ToolCalls[0].Status)
	}
}

func TestBatchReadKeepsSamePathWithDifferentEffectiveRanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	result, err := app.batchReadFilesWithConfig(cfg, BatchReadRequest{
		Files: []BatchReadFileRequest{
			{Path: "sample.txt", EndLine: 1},
			{Path: "sample.txt", StartLine: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("expected both distinct read ranges, got %d result(s): %#v", len(result.Files), result.Files)
	}
	if result.Files[0].Content != "one\n" {
		t.Fatalf("expected first read to contain only line 1, got:\n%s", result.Files[0].Content)
	}
	if result.Files[1].Content != "two\nthree\n" {
		t.Fatalf("expected second read to contain line 2 through EOF, got:\n%s", result.Files[1].Content)
	}
	if result.Files[0].ContentFormat != "raw" || result.Files[0].Version != hashVersion([]byte("one\ntwo\nthree\n")) {
		t.Fatalf("expected raw content with version metadata, got %#v", result.Files[0])
	}
}

func TestFileVersionIsStableCrockfordBase32(t *testing.T) {
	version := hashVersion([]byte("ally"))
	if version != "fx0t3f9mp005" {
		t.Fatalf("unexpected version: %q", version)
	}
	if !isValidVersion(version) || !isValidVersion(strings.ToUpper(version)) {
		t.Fatalf("expected version validation to be case-insensitive: %q", version)
	}
	for _, invalid := range []string{"", "2q4rsqh3dhn", "2q4rsqh3dhnqq", "2q4rsqh3dhno", "2q4rsqh3dhni"} {
		if isValidVersion(invalid) {
			t.Fatalf("expected invalid version %q to be rejected", invalid)
		}
	}
}

func TestEditUsesExactStringReplacementAndExpectedSHA(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	read, err := app.readFileWithConfig(cfg, ReadFileRequest{Path: "sample.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.Content, "2: beta") {
		t.Fatalf("expected line-numbered read content, got %q", read.Content)
	}
	if read.ContentFormat != "line_numbers" {
		t.Fatalf("expected line_numbers content format, got %q", read.ContentFormat)
	}

	result, err := app.editWithConfig(cfg, EditRequest{
		Path:           "sample.txt",
		ExpectedSHA256: read.SHA256,
		OldString:      "beta\n",
		NewString:      "beta\ninserted one\ninserted two\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AddedLines != 2 || result.RemovedLines != 0 {
		t.Fatalf("expected +2 -0 stats, got +%d -%d", result.AddedLines, result.RemovedLines)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha\nbeta\ninserted one\ninserted two\ngamma\n"
	if string(got) != want {
		t.Fatalf("unexpected file content:\nwant %q\ngot  %q", want, string(got))
	}
}

func TestEditAppliesMultipleReplacementsInOneCall(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	result, err := app.editWithConfig(cfg, EditRequest{
		Path: "sample.txt",
		Edits: []EditOperation{
			{OldString: "alpha", NewString: "ALPHA"},
			{OldString: "gamma", NewString: "GAMMA"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replacements != 2 {
		t.Fatalf("expected two replacements, got %d", result.Replacements)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "ALPHA\nbeta\nGAMMA\n"
	if string(got) != want {
		t.Fatalf("unexpected file content:\nwant %q\ngot  %q", want, string(got))
	}
}

func TestExecuteToolRejectsLegacyStringEditFields(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	result := app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(`{
		"files": [{"path": "sample.txt", "version": "000000000000",
		"edits": [
			{"oldString": "alpha", "newString": "ALPHA"},
			{"oldString": "gamma", "newString": "GAMMA"}
		]}]
	}`))
	if result.OK || !strings.Contains(result.Error, "unknown field \"edits\"") {
		t.Fatalf("expected model-facing edit tool to reject legacy edits array, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("expected rejected edit to leave file unchanged, got %q", string(got))
	}
}

func TestExecuteToolAppliesMultipleAtomicTextChanges(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	result := app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(fmt.Sprintf(`{
		"files": [{"path": "sample.txt",
		"version": %q,
		"changes": [
			{"oldText": "alpha", "newText": "ALPHA"},
			{"oldText": "gamma", "newText": "GAMMA"}
		]}]
	}`, strings.ToUpper(hashVersion(original)))))
	if !result.OK {
		t.Fatalf("expected atomic edit to succeed, got error %q", result.Error)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ALPHA\nbeta\nGAMMA\n" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestExecuteToolEditsMultipleFilesInOneCall(t *testing.T) {
	dir := t.TempDir()
	a, b := []byte("alpha\n"), []byte("beta\n")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), a, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	args := fmt.Sprintf(`{"files":[{"path":"a.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"}]},{"path":"b.txt","version":%q,"changes":[{"oldText":"beta","newText":"BETA"}]}]}`, hashVersion(a), hashVersion(b))
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if !result.OK {
		t.Fatalf("cross-file edit failed: %#v", result)
	}
	for path, want := range map[string]string{"a.txt": "ALPHA\n", "b.txt": "BETA\n"} {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestExecuteToolRequiresVersionForEdit(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(`{
		"files": [{"path": "sample.txt", "changes": [{"oldText": "beta", "newText": "BETA"}]}]
	}`))
	if result.OK || result.ErrorCode != "E_VERSION_REQUIRED" {
		t.Fatalf("expected E_VERSION_REQUIRED, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("expected missing-version edit to leave file unchanged, got %q", string(got))
	}
}

func TestExecuteToolEditHandlesUniqueSingleLineSubstring(t *testing.T) {
	dir := t.TempDir()
	original := []byte(`{"enabled":false,"name":"ally"}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{
		"files": [{"path": "config.json",
		"version": %q,
		"changes": [{"oldText": "false", "newText": "true"}]}]
	}`, hashVersion(original))))
	if !result.OK {
		t.Fatalf("expected edit to succeed, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"enabled":true,"name":"ally"}` {
		t.Fatalf("unexpected edit content %q", string(got))
	}
}

func TestEditTreatsCRLFAndLFAsEquivalent(t *testing.T) {
	result, replacements, err := applyAtomicTextChanges("alpha\nbeta\n", []TextChange{{
		OldText: "alpha\r\nbeta",
		NewText: "ALPHA\r\nBETA",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if replacements != 1 || result.content != "ALPHA\nBETA\n" {
		t.Fatalf("unexpected normalized edit: replacements=%d content=%q", replacements, result.content)
	}
}

func TestExecuteToolEditRejectsMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	original := []byte("foo foo")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{
		"files": [{"path": "sample.txt",
		"version": %q,
		"changes": [{"oldText": "foo", "newText": "bar"}]}]
	}`, hashVersion(original))))
	if result.OK || !strings.Contains(result.Error, "[E_MULTI_MATCH]") {
		t.Fatalf("expected edit multi-match failure, got %#v", result)
	}
}

func TestExecuteToolEditRejectsOverlappingChangesAtomically(t *testing.T) {
	dir := t.TempDir()
	original := []byte("abcdef")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{
		"files": [{"path": "sample.txt",
		"version": %q,
		"changes": [
			{"oldText": "abc", "newText": "ABC"},
			{"oldText": "bc", "newText": "BC"}
		]}]
	}`, hashVersion(original))))
	if result.OK || result.ErrorCode != "E_OVERLAPPING_CHANGES" {
		t.Fatalf("expected overlap failure, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("overlapping edit must be atomic, got %q", string(got))
	}
}

func TestEditLineRangeDeletesWholeLines(t *testing.T) {
	dir := t.TempDir()
	original := "alpha\nbeta\ngamma\ndelta\n"
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}
	newText := ""

	result, err := app.editWithConfig(cfg, EditRequest{
		Path:      "sample.txt",
		StartLine: 2,
		EndLine:   3,
		NewText:   &newText,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedLines == 0 {
		t.Fatalf("expected deletion stats, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha\ndelta\n" {
		t.Fatalf("expected whole lines to be deleted without blank lines, got %q", string(got))
	}
}

func TestEditRejectsMixedLineAndExactForms(t *testing.T) {
	app := NewApp()
	newText := "ALPHA"
	_, err := app.editWithConfig(ConfigState{Workspace: t.TempDir()}, EditRequest{
		Path:      "sample.txt",
		OldString: "alpha",
		NewString: "ALPHA",
		StartLine: 1,
		NewText:   &newText,
	})
	if err == nil || !strings.Contains(err.Error(), "[E_BAD_EDIT]") {
		t.Fatalf("expected mixed edit form validation error, got %v", err)
	}
}

func TestEditLineRangeRequiresExplicitNewText(t *testing.T) {
	app := NewApp()
	_, err := app.editWithConfig(ConfigState{Workspace: t.TempDir()}, EditRequest{
		Path:      "sample.txt",
		StartLine: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "requires \"newText\"") {
		t.Fatalf("expected missing newText validation error, got %v", err)
	}
}

func TestEditMultipleReplacementsAreAtomicOnFailure(t *testing.T) {
	dir := t.TempDir()
	original := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	_, err := app.editWithConfig(cfg, EditRequest{
		Path: "sample.txt",
		Edits: []EditOperation{
			{OldString: "alpha", NewString: "ALPHA"},
			{OldString: "missing", NewString: "MISSING"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "edit 2/2 failed") || !strings.Contains(err.Error(), "[E_NO_MATCH]") {
		t.Fatalf("expected second edit no-match error, got %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("expected failed multi-edit to leave file unchanged:\nwant %q\ngot  %q", original, string(got))
	}
}

func TestEditLargeFileReturnsLocalizedDiff(t *testing.T) {
	dir := t.TempDir()
	var original strings.Builder
	for i := 1; i <= 600; i++ {
		original.WriteString(fmt.Sprintf("line-%04d\n", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(original.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	cfg := ConfigState{Workspace: dir}
	read, err := app.readFileWithConfig(cfg, ReadFileRequest{Path: "large.txt", StartLine: 300, LineCount: 1})
	if err != nil {
		t.Fatal(err)
	}

	result, err := app.editWithConfig(cfg, EditRequest{
		Path:           "large.txt",
		ExpectedSHA256: read.SHA256,
		OldString:      "line-0300",
		NewString:      "line-0300 updated",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Diff, "[diff omitted:") {
		t.Fatalf("expected localized diff, got omitted marker:\n%s", result.Diff)
	}
	if !strings.Contains(result.Diff, "@@ -297,7 +297,7 @@") ||
		!strings.Contains(result.Diff, "-line-0300") ||
		!strings.Contains(result.Diff, "+line-0300 updated") {
		t.Fatalf("expected localized hunk around changed line, got:\n%s", result.Diff)
	}
	if strings.Contains(result.Diff, "line-0001") || strings.Contains(result.Diff, "line-0600") {
		t.Fatalf("expected diff to omit distant context, got:\n%s", result.Diff)
	}
}

func TestHugeEditDiffPreviewIsTruncated(t *testing.T) {
	var beforeLines []string
	var afterLines []string
	for i := 1; i <= 700; i++ {
		beforeLines = append(beforeLines, fmt.Sprintf("before-%04d", i))
		afterLines = append(afterLines, fmt.Sprintf("after-%04d", i))
	}
	diff := generateEditDiffPreview(strings.Join(beforeLines, "\n")+"\n", strings.Join(afterLines, "\n")+"\n", maxToolOutput)
	if !strings.Contains(diff, "@@ -1,700 +1,700 @@") {
		t.Fatalf("expected unified hunk header, got:\n%s", diff)
	}
	if !strings.Contains(diff, "[diff truncated:") {
		t.Fatalf("expected truncation marker, got:\n%s", diff)
	}
	if strings.Contains(diff, "[diff omitted:") {
		t.Fatalf("did not expect omitted marker, got:\n%s", diff)
	}
}

func TestEditRejectsExpectedSHAMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	_, err := app.editWithConfig(ConfigState{Workspace: dir}, EditRequest{
		Path:           "sample.txt",
		ExpectedSHA256: "not-current",
		OldString:      "alpha",
		NewString:      "beta",
	})
	if err == nil || !strings.Contains(err.Error(), "[E_VERSION_MISMATCH]") {
		t.Fatalf("expected version mismatch error, got %v", err)
	}
}

func TestEditRejectsAmbiguousStringUnlessReplaceAll(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("foo\nfoo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	_, err := app.editWithConfig(cfg, EditRequest{
		Path:      "sample.txt",
		OldString: "foo",
		NewString: "bar",
	})
	if err == nil || !strings.Contains(err.Error(), "[E_MULTI_MATCH]") {
		t.Fatalf("expected multi-match error, got %v", err)
	}

	result, err := app.editWithConfig(cfg, EditRequest{
		Path:       "sample.txt",
		OldString:  "foo",
		NewString:  "bar",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replacements != 2 {
		t.Fatalf("expected two replacements, got %d", result.Replacements)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "bar\nbar\n" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestWindowsDeleteSafetyAllowsOrdinaryCDriveWorkspacePaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows path safety")
	}

	allowed, reason := isDangerousDeletePath(`C:\Users\alice\project\temp.txt`)
	if allowed {
		t.Fatalf("expected ordinary C: workspace path to be allowed, got %q", reason)
	}

	for _, path := range []string{
		`C:\`,
		`C:\Windows\System32\kernel32.dll`,
		`C:\Program Files\App`,
		`C:\Users\alice`,
		`C:\Users\alice\project\.git\config`,
	} {
		blocked, reason := isDangerousDeletePath(path)
		if !blocked {
			t.Fatalf("expected %s to be blocked", path)
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("expected block reason for %s", path)
		}
	}
}

func TestReplaceLinesUsesCurrentFileContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.initialized = true
	app.config = ConfigState{Workspace: dir}

	result, err := app.ReplaceLines(ReplaceLinesRequest{
		Path:      "sample.txt",
		StartLine: 2,
		EndLine:   2,
		NewText:   "BETA",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AddedLines != 1 || result.RemovedLines != 1 {
		t.Fatalf("expected replacement stats +1 -1, got +%d -%d", result.AddedLines, result.RemovedLines)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestParseGitStatusZBuildsStableEntries(t *testing.T) {
	status := strings.Join([]string{
		" M frontend/src/App.vue",
		"?? frontend/src/components/GitDiffModal.vue",
		"A  app_test.go",
		"D  old.txt",
		"R  new-name.txt",
		"old-name.txt",
		"",
	}, "\x00")

	got := parseGitStatusZ(status)
	if len(got) != 5 {
		t.Fatalf("expected 5 entries, got %#v", got)
	}

	want := map[string]struct {
		status    string
		untracked bool
	}{
		"app_test.go":                              {status: "added"},
		"frontend/src/App.vue":                     {status: "modified"},
		"frontend/src/components/GitDiffModal.vue": {status: "untracked", untracked: true},
		"new-name.txt":                             {status: "renamed"},
		"old.txt":                                  {status: "deleted"},
	}
	for _, entry := range got {
		expected, ok := want[entry.Path]
		if !ok {
			t.Fatalf("unexpected entry %#v", entry)
		}
		if entry.Status != expected.status || entry.Untracked != expected.untracked {
			t.Fatalf("unexpected entry for %s: got status=%s untracked=%v", entry.Path, entry.Status, entry.Untracked)
		}
	}
}

func TestSplitUnifiedDiffByPath(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/app.go b/app.go",
		"--- a/app.go",
		"+++ b/app.go",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/docs/old name.md b/docs/old name.md",
		"deleted file mode 100644",
		"--- a/docs/old name.md",
		"+++ /dev/null",
		"@@ -1 +0,0 @@",
		"-gone",
	}, "\n")

	got := splitUnifiedDiffByPath(diff)
	if len(got) != 2 {
		t.Fatalf("expected two per-file sections, got %#v", got)
	}
	if !strings.Contains(got["app.go"], "+new") {
		t.Fatalf("app.go section missing change: %q", got["app.go"])
	}
	if !strings.Contains(got["docs/old name.md"], "deleted file mode") {
		t.Fatalf("deleted path section missing: %q", got["docs/old name.md"])
	}
}

func TestDecodeGitPatchPathSupportsQuotedNames(t *testing.T) {
	got := decodeGitPatchPath(`"b/docs/file name.md"`)
	if got != "docs/file name.md" {
		t.Fatalf("unexpected decoded path %q", got)
	}
}

func TestGetGitDiffUsesRepositoryRootForSubdirectoryWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runGitTestCommand(t, dir, "init")
	runGitTestCommand(t, dir, "config", "user.email", "ally-test@example.com")
	runGitTestCommand(t, dir, "config", "user.name", "Ally Test")
	runGitTestCommand(t, dir, "add", ".")
	runGitTestCommand(t, dir, "commit", "-m", "init")

	if err := os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.initialized = true
	app.config = ConfigState{Workspace: subdir}

	got := app.GetGitDiff()
	if !got.IsRepo {
		t.Fatalf("expected subdirectory workspace to be recognized as repo, error=%q", got.Error)
	}

	var file *GitDiffFile
	for i := range got.Files {
		if got.Files[i].Path == "sub/file.txt" {
			file = &got.Files[i]
			break
		}
	}
	if file == nil {
		t.Fatalf("expected sub/file.txt in diff result, got %#v", got.Files)
	}
	if !strings.Contains(file.Diff, "-alpha") || !strings.Contains(file.Diff, "+beta") {
		t.Fatalf("expected file diff content for subdirectory workspace, got:\n%s", file.Diff)
	}
}

func runGitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestCountUnifiedDiffStatsIgnoresHeaders(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a.txt b/a.txt",
		"--- a/a.txt",
		"+++ b/a.txt",
		"@@ -1,2 +1,3 @@",
		" context",
		"-old",
		"+new",
		"+added",
	}, "\n")

	added, deleted := countUnifiedDiffStats(diff)
	if added != 2 || deleted != 1 {
		t.Fatalf("expected +2 -1, got +%d -%d", added, deleted)
	}
}

// TestNormalizeToolArgsForDedup verifies that the dedup canonicalization is
// invariant under field reordering and whitespace differences, while still
// distinguishing genuinely different argument values and preserving array
// order (e.g. edit.files, ask.questions).
func TestNormalizeToolArgsForDedup(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool // true if a and b should dedup to the same key
	}{
		{
			name: "field order",
			a:    `{"url":"https://x","method":"GET"}`,
			b:    `{"method":"GET","url":"https://x"}`,
			want: true,
		},
		{
			name: "whitespace only",
			a:    `{"url": "https://x", "method": "GET"}`,
			b:    `{"url":"https://x","method":"GET"}`,
			want: true,
		},
		{
			name: "different url",
			a:    `{"url":"https://x"}`,
			b:    `{"url":"https://y"}`,
			want: false,
		},
		{
			name: "different method",
			a:    `{"url":"https://x","method":"GET"}`,
			b:    `{"url":"https://x","method":"POST"}`,
			want: false,
		},
		{
			name: "default value present vs absent stays distinct",
			a:    `{"url":"https://x"}`,
			b:    `{"url":"https://x","method":"GET"}`,
			want: false,
		},
		{
			name: "array order preserved",
			a:    `{"items":["a","b"]}`,
			b:    `{"items":["b","a"]}`,
			want: false,
		},
		{
			name: "nested object order",
			a:    `{"query":{"a":"1","b":"2"}}`,
			b:    `{"query":{"b":"2","a":"1"}}`,
			want: true,
		},
		{
			name: "empty strings",
			a:    ``,
			b:    ``,
			want: true,
		},
		{
			name: "invalid json falls back to raw",
			a:    `not-json`,
			b:    `not-json`,
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ka := normalizeToolArgsForDedup(tc.a)
			kb := normalizeToolArgsForDedup(tc.b)
			got := ka == kb
			if got != tc.want {
				t.Fatalf("normalize(%q)=%q normalize(%q)=%q; want equal=%v got equal=%v",
					tc.a, ka, tc.b, kb, tc.want, got)
			}
		})
	}
}

// TestDetectToolBatchConflictsDedupNormalized verifies that batch-level dedup
// now treats field-reordered and whitespace-different JSON arguments as
// duplicates, while still distinguishing genuinely different calls.
func TestDetectToolBatchConflictsDedupNormalized(t *testing.T) {
	cfg := ConfigState{Workspace: "/ws"}
	mk := func(id, args string) openai.ToolCall {
		return openai.ToolCall{
			ID:   id,
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "http_request",
				Arguments: args,
			},
		}
	}

	t.Run("field reorder dedups", func(t *testing.T) {
		calls := []openai.ToolCall{
			mk("call_a", `{"url":"https://api.example.com","method":"GET"}`),
			mk("call_b", `{"method":"GET","url":"https://api.example.com"}`),
		}
		conflicts := detectToolBatchConflicts(cfg, calls)
		if len(conflicts) != 1 {
			t.Fatalf("expected 1 conflict, got %d: %v", len(conflicts), conflicts)
		}
		err, ok := conflicts[1]
		if !ok {
			t.Fatalf("expected conflict at index 1")
		}
		if !strings.Contains(err.Error(), "E_DUPLICATE_TOOL_CALL") {
			t.Fatalf("expected E_DUPLICATE_TOOL_CALL, got %v", err)
		}
	})

	t.Run("whitespace difference dedups", func(t *testing.T) {
		calls := []openai.ToolCall{
			mk("call_a", `{"url": "https://api.example.com"}`),
			mk("call_b", `{"url":"https://api.example.com"}`),
		}
		conflicts := detectToolBatchConflicts(cfg, calls)
		if len(conflicts) != 1 {
			t.Fatalf("expected 1 conflict, got %d: %v", len(conflicts), conflicts)
		}
	})

	t.Run("different method stays distinct", func(t *testing.T) {
		calls := []openai.ToolCall{
			mk("call_a", `{"url":"https://api.example.com","method":"GET"}`),
			mk("call_b", `{"url":"https://api.example.com","method":"POST"}`),
		}
		conflicts := detectToolBatchConflicts(cfg, calls)
		if len(conflicts) != 0 {
			t.Fatalf("expected 0 conflicts, got %d: %v", len(conflicts), conflicts)
		}
	})
}
