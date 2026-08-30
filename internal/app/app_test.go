// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	oaresp "github.com/openai/openai-go/responses"
	openai "github.com/sashabaranov/go-openai"
)

func TestDefaultConfigWorkspaceDefaultsToDocuments(t *testing.T) {
	got := defaultConfigState().Workspace
	if got == "" {
		t.Fatalf("default workspace = empty, want the user's Documents directory on first launch")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("default workspace = %q, want an absolute path", got)
	}
}

func TestDefaultConfigWindowSizeIsEmpty(t *testing.T) {
	if got := defaultConfigState().WindowWidth; got != 0 {
		t.Fatalf("default window width = %d, want 0 (first launch uses options default)", got)
	}
	if got := defaultConfigState().WindowHeight; got != 0 {
		t.Fatalf("default window height = %d, want 0 (first launch uses options default)", got)
	}
}

func TestMergeConfigWindowSizeRequiresBothDimensions(t *testing.T) {
	base := ConfigState{WindowWidth: 100, WindowHeight: 200}
	got := mergeConfig(base, ConfigState{WindowWidth: 300})
	if got.WindowWidth != 100 || got.WindowHeight != 200 {
		t.Fatalf("partial window size overlay changed stored size: got %dx%d", got.WindowWidth, got.WindowHeight)
	}
	got = mergeConfig(base, ConfigState{WindowWidth: 300, WindowHeight: 400})
	if got.WindowWidth != 300 || got.WindowHeight != 400 {
		t.Fatalf("full window size overlay not applied: got %dx%d", got.WindowWidth, got.WindowHeight)
	}
}

func TestWorkspaceRootRequiresExplicitWorkspace(t *testing.T) {
	if _, err := workspaceRoot(ConfigState{}); err == nil || !strings.Contains(err.Error(), "workspace is required") {
		t.Fatalf("workspaceRoot(empty) error = %v, want workspace required", err)
	}
}

func TestStartChatRequiresExplicitWorkspace(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.config = ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   defaultBaseURL,
		APIKey:    "test-key",
		Model:     defaultModel,
	}
	if _, err := app.StartChat(ChatRequest{SessionID: "session-1"}); err == nil || !strings.Contains(err.Error(), "workspace is required") {
		t.Fatalf("StartChat() error = %v, want workspace required", err)
	}
}

// sseChatToolCallChunk 构造一个携带 tool_calls delta 的 OpenAI 兼容流式 chunk。
func sseChatToolCallChunk(id, name, args string) string {
	return fmt.Sprintf(`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}`+"\n\n", id, name, args)
}

// sseChatFinishChunk 构造一个只携带 finish_reason 的收尾 chunk。
func sseChatFinishChunk(reason string) string {
	return fmt.Sprintf(`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":%q}]}`+"\n\n", reason)
}

func sseResponsesEvent(payload string) string {
	return "data: " + payload + "\n\n"
}

func sseAnthropicEvent(event, payload string) string {
	return "event: " + event + "\n" + "data: " + payload + "\n\n"
}

// runEventRecorder 记录事件序列并在 run 终止事件上通知，供异步 runChat 测试同步。
type runEventRecorder struct {
	mu     sync.Mutex
	names  []string
	end    string
	endSet bool
	done   chan struct{}
}

func (r *runEventRecorder) Emit(name string, _ any) {
	r.mu.Lock()
	r.names = append(r.names, name)
	if (name == "run:done" || name == "run:error") && !r.endSet {
		r.end, r.endSet = name, true
	}
	r.mu.Unlock()
	if name == "run:done" || name == "run:error" {
		select {
		case r.done <- struct{}{}:
		default:
		}
	}
}

// TestRunChatSuccessfulSuggestEndsRun 验证成功的唯一 suggest 调用直接结束
// run：chips 渲染在上一条 assistant 消息下方，之后不得再有任何模型输出。
// 伪造的 OpenAI 兼容服务对每个请求都返回 suggest 工具调用；若 chat loop
// 违约在 suggest 之后又发起模型请求，请求数断言会失败。
func TestRunChatSuccessfulSuggestEndsRun(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("Here is the answer."))
		fmt.Fprint(w, sseChatToolCallChunk("call_suggest_1", "suggest", `{"items":["Run the tests"]}`))
		fmt.Fprint(w, sseChatFinishChunk("tool_calls"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true // 跳过 ensureInitialized 的磁盘初始化
	app.stats = nil        // 跳过 token 统计落盘
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "suggest-e2e",
		Message:   "hi",
		Config: ConfigState{
			APIFormat: apiFormatOpenAIChat,
			BaseURL:   server.URL,
			APIKeys:   []string{"test-key"},
			Model:     "test-model",
			MaxTokens: 64,
			Workspace: t.TempDir(),
		},
	}); err != nil {
		t.Fatalf("StartChat() error = %v", err)
	}

	select {
	case <-recorder.done:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not finish in time")
	}
	if recorder.end != "run:done" {
		t.Fatalf("run ended with %q, want run:done", recorder.end)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("model requests = %d, want 1 (a successful sole suggest must end the run)", got)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	sawSuggestResult := false
	for _, name := range recorder.names {
		if name == "run:stream" && sawSuggestResult {
			t.Fatal("run:stream emitted after the suggest tool result; the run must stop there")
		}
		if name == "tool:result" {
			sawSuggestResult = true
		}
	}
	if !sawSuggestResult {
		t.Fatal("no tool:result event for the suggest call")
	}
}

// TestRunChatRetriesEmptyResponseAfterToolError verifies that an otherwise
// successful but empty model response cannot silently finish the run after a
// failed tool call. The empty step is transient: the loop retries it and the
// next visible assistant response reaches the user.
func TestRunChatRetriesEmptyResponseAfterToolError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		request := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		switch request {
		case 1:
			fmt.Fprint(w, sseChatToolCallChunk("call_missing_1", "definitely_missing_tool", `{}`))
			fmt.Fprint(w, sseChatFinishChunk("tool_calls"))
		case 2:
			fmt.Fprint(w, sseChatFinishChunk("stop"))
		default:
			fmt.Fprint(w, sseChatChunk("Recovered after the empty response."))
			fmt.Fprint(w, sseChatFinishChunk("stop"))
		}
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "empty-after-tool-error-e2e",
		Message:   "exercise a tool",
		Config: ConfigState{
			APIFormat: apiFormatOpenAIChat,
			BaseURL:   server.URL,
			APIKeys:   []string{"test-key"},
			Model:     "test-model",
			MaxTokens: 64,
			Workspace: t.TempDir(),
		},
	}); err != nil {
		t.Fatalf("StartChat() error = %v", err)
	}

	select {
	case <-recorder.done:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not finish in time")
	}
	if recorder.end != "run:done" {
		t.Fatalf("run ended with %q, want run:done", recorder.end)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("model requests = %d, want 3 (tool call, empty retry, visible recovery)", got)
	}
}

func TestRunChatRetriesEmptyOpenAIResponsesResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		request := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if request > 1 {
			fmt.Fprint(w, sseResponsesEvent(`{"type":"response.output_text.delta","delta":"Recovered Responses output.","item_id":"msg_1","output_index":0,"content_index":0,"sequence_number":1}`))
		}
		fmt.Fprint(w, sseResponsesEvent(`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"test-model","output":[],"parallel_tool_calls":true,"tools":[]}}`))
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "empty-openai-responses-e2e",
		Message:   "reply visibly",
		Config: ConfigState{
			APIFormat: apiFormatOpenAIResponses,
			BaseURL:   server.URL,
			APIKeys:   []string{"test-key"},
			Model:     "test-model",
			MaxTokens: 64,
			Workspace: t.TempDir(),
		},
	}); err != nil {
		t.Fatalf("StartChat() error = %v", err)
	}

	select {
	case <-recorder.done:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not finish in time")
	}
	if recorder.end != "run:done" {
		t.Fatalf("run ended with %q, want run:done", recorder.end)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("model requests = %d, want 2 (empty response then visible retry)", got)
	}
}

func TestRunChatRetriesEmptyAnthropicResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		request := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseAnthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"test-model","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`))
		if request > 1 {
			fmt.Fprint(w, sseAnthropicEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`))
			fmt.Fprint(w, sseAnthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Recovered Anthropic output."}}`))
			fmt.Fprint(w, sseAnthropicEvent("content_block_stop", `{"type":"content_block_stop","index":0}`))
		}
		fmt.Fprint(w, sseAnthropicEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`))
		fmt.Fprint(w, sseAnthropicEvent("message_stop", `{"type":"message_stop"}`))
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "empty-anthropic-e2e",
		Message:   "reply visibly",
		Config: ConfigState{
			APIFormat: apiFormatAnthropicMessages,
			BaseURL:   server.URL,
			APIKeys:   []string{"test-key"},
			Model:     "test-model",
			MaxTokens: 64,
			Workspace: t.TempDir(),
		},
	}); err != nil {
		t.Fatalf("StartChat() error = %v", err)
	}

	select {
	case <-recorder.done:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not finish in time")
	}
	if recorder.end != "run:done" {
		t.Fatalf("run ended with %q, want run:done", recorder.end)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("model requests = %d, want 2 (empty response then visible retry)", got)
	}
}

// TestRunChatRejectsOpenAIChatLengthStop verifies that a truncated compatible
// Chat Completions response is surfaced as an error instead of being persisted
// and presented as a normally completed answer.
func TestRunChatRejectsOpenAIChatLengthStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("This answer is incomplete"))
		fmt.Fprint(w, sseChatFinishChunk("length"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "openai-chat-length-e2e",
		Message:   "write a complete answer",
		Config: ConfigState{
			APIFormat: apiFormatOpenAIChat,
			BaseURL:   server.URL,
			APIKeys:   []string{"test-key"},
			Model:     "test-model",
			MaxTokens: 64,
			Workspace: t.TempDir(),
		},
	}); err != nil {
		t.Fatalf("StartChat() error = %v", err)
	}

	select {
	case <-recorder.done:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not finish in time")
	}
	if recorder.end != "run:error" {
		t.Fatalf("run ended with %q, want run:error for finish_reason=length", recorder.end)
	}
}

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

func TestRunStreamDeltaEmitterBatchesRapidDeltas(t *testing.T) {
	type event struct {
		name    string
		content string
	}
	events := []event{}
	emitter := newRunStreamDeltaEmitter("run-1", "session-1", func(name string, payload map[string]any) {
		events = append(events, event{name: name, content: payload["content"].(string)})
	})

	emitter.addContent("你")
	emitter.addContent("好")
	if len(events) != 1 {
		t.Fatalf("expected first delta to emit immediately and second to buffer, got %#v", events)
	}
	if events[0].name != runStreamEvent || events[0].content != "你" {
		t.Fatalf("unexpected first event: %#v", events[0])
	}

	emitter.flush()
	if len(events) != 2 {
		t.Fatalf("expected flush to emit buffered delta, got %#v", events)
	}
	if events[1].name != runStreamEvent || events[1].content != "好" {
		t.Fatalf("unexpected flushed event: %#v", events[1])
	}
}

// TestRunStreamDeltaEmitterMergesReasoningAndContent verifies that a single
// flush emits one run:stream event carrying both fields, halving IPC count
// versus the previous run:reasoning + run:delta pair.
func TestRunStreamDeltaEmitterMergesReasoningAndContent(t *testing.T) {
	type captured struct {
		name      string
		reasonLen int
		content   string
		hasBoth   bool
	}
	var got []captured
	emitter := newRunStreamDeltaEmitter("run-1", "session-1", func(name string, payload map[string]any) {
		c := captured{name: name}
		if v, ok := payload["reasoningLen"].(int); ok {
			c.reasonLen = v
		}
		if v, ok := payload["content"].(string); ok {
			c.content = v
		}
		c.hasBoth = c.reasonLen > 0 && c.content != ""
		got = append(got, c)
	})

	// First delta flushes immediately (lastEmit.IsZero). Subsequent deltas
	// accumulate because they're under threshold and within the throttle window.
	emitter.addContent("a")
	emitter.addReasoning("think")
	emitter.addContent("b")
	emitter.flush()

	if len(got) != 2 {
		t.Fatalf("expected 2 events (first-byte then merged flush), got %d: %#v", len(got), got)
	}
	if got[0].content != "a" || got[0].reasonLen != 0 {
		t.Fatalf("expected first event content-only, got %#v", got[0])
	}
	if !got[1].hasBoth {
		t.Fatalf("expected final flush to merge reasoning+content in one event, got %#v", got[1])
	}
	if got[1].name != runStreamEvent {
		t.Fatalf("expected merged event name %q, got %q", runStreamEvent, got[1].name)
	}
	if got[1].reasonLen != len("think") || got[1].content != "b" {
		t.Fatalf("unexpected merged payload: %#v", got[1])
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
			Name:      "create",
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

func TestModelToolCallEventGateThrottlesLargeSnapshots(t *testing.T) {
	var forwarded []modelStreamEvent
	gate := newModelToolCallEventGate(func(event modelStreamEvent) {
		forwarded = append(forwarded, event)
	})
	calls := []openai.ToolCall{{Function: openai.FunctionCall{Arguments: strings.Repeat("x", toolUpdateThreshold+1)}}}
	gate.emit(modelStreamEvent{ToolCalls: calls})
	gate.emit(modelStreamEvent{ToolCalls: calls})
	if len(forwarded) != 1 {
		t.Fatalf("expected rapid large tool snapshots to be throttled, got %d", len(forwarded))
	}
	gate.lastEmit = time.Now().Add(-toolUpdateThrottle)
	gate.emit(modelStreamEvent{ToolCalls: calls})
	if len(forwarded) != 2 {
		t.Fatalf("expected snapshot after throttle window, got %d", len(forwarded))
	}
}

// TestToolCallProgressTrackerThrottlesLargeUpdates verifies that large
// streaming argument payloads (e.g. create with thousands of lines) are
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
			Name:      "create",
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

func TestLoadAgentsMdAppendsFirstLevelSubdirsAfterRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Root rule: focused commits.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"frontend", "backend", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "AGENTS.md"), []byte("Frontend rule: keep components small.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden", "AGENTS.md"), []byte("Hidden rule: must not load.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := loadAgentsMd(root)

	rootIdx := strings.Index(got, "Root rule: focused commits.")
	subIdx := strings.Index(got, "Frontend rule: keep components small.")
	if rootIdx < 0 || subIdx < 0 {
		t.Fatalf("expected root and subdirectory AGENTS.md content, got %q", got)
	}
	if rootIdx > subIdx {
		t.Fatalf("root AGENTS.md must be placed before subdirectory files, got %q", got)
	}
	if !strings.Contains(got, "### AGENTS.md under directory `frontend/`") {
		t.Fatalf("expected directory label isolating frontend rules, got %q", got)
	}
	if strings.Contains(got, "Hidden rule") {
		t.Fatalf("dot-directory AGENTS.md must be skipped, got %q", got)
	}
}

func TestLoadAgentsMdLoadsSubdirsWhenRootMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "api", "AGENTS.md"), []byte("API rule: version all endpoints.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := loadAgentsMd(root)

	if !strings.Contains(got, "API rule: version all endpoints.") {
		t.Fatalf("expected subdirectory AGENTS.md without a root file, got %q", got)
	}
	if !strings.Contains(got, "### AGENTS.md under directory `api/`") {
		t.Fatalf("expected directory label, got %q", got)
	}
}

func TestSystemPromptDefinesWaitSequencing(t *testing.T) {
	prompt := defaultSystemPrompt(nil, "", nil, "", "", "")
	for _, expected := range []string{"Use `wait` only", "only tool in that model response", "verify the condition after it completes"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("system prompt missing wait guidance %q", expected)
		}
	}
}

func TestSystemPromptExplainsRunCommandOutsidePathRecovery(t *testing.T) {
	prompt := defaultSystemPrompt(nil, "", nil, "", "", "")
	for _, expected := range []string{"`E_PATH_OUTSIDE`", "Do not retry the unchanged command", "read the returned Chinese explanation"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("system prompt missing command recovery guidance %q", expected)
		}
	}
}

func TestSystemPromptIncludesConsolidatedSafetyRules(t *testing.T) {
	prompt := defaultSystemPrompt(nil, "", nil, "", "", "")
	for _, expected := range []string{
		"# Safety",
		"Sensitive files",
		"`~/.ssh/*`",
		"`~/.ally_agent/config.json`",
		"`.env`",
		"explicit user confirmation",
		"`E_PATH_OUTSIDE`",
		"stop and ask the user",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("system prompt missing consolidated safety rule %q", expected)
		}
	}
	sub := subagentSystemPrompt("")
	for _, expected := range []string{"# Safety", "Sensitive files", "explicit user confirmation", "`E_PATH_OUTSIDE`"} {
		if !strings.Contains(sub, expected) {
			t.Fatalf("sub-agent prompt missing consolidated safety rule %q", expected)
		}
	}
}

func TestSystemPromptDiscouragesRedundantReadsBeforeEdit(t *testing.T) {
	prompt := defaultSystemPrompt(nil, "", nil, "", "", "")
	for _, expected := range []string{
		"assume workspace files are not concurrently edited by another person",
		"do not re-read a file merely for reassurance",
		"reuse its returned `version`",
		"context compaction removed the reliable snapshot",
		"formatter/generator/command or other external process may have changed the file",
		"Use startLine=C to continue",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("system prompt missing redundant-read guidance %q", expected)
		}
	}
}

func TestSystemPromptKeepsEditBehavioralRules(t *testing.T) {
	prompt := defaultSystemPrompt(nil, "", nil, "", "", "")
	for _, expected := range []string{
		"must never be copied into edit text",
		"do not re-read a file merely for reassurance",
		"Batch edits by risk and size",
		"one failed `oldText` match or stale `version` rejects the entire call",
		"Never send multiple file-mutation tool calls for the same path",
		"Do not use patch, unified diff, or git apply",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("system prompt missing edit behavioral rule %q", expected)
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
		if tool.Function.Name == "scheduled_task" || tool.Function.Name == "ask" || tool.Function.Name == "subagent" {
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

func TestAppendFrontendHistoryDeltaMatchesBudgetedBackendTail(t *testing.T) {
	backend := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "common question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "common answer"},
	}
	frontend := []ChatMessageInput{
		{Role: openai.ChatMessageRoleUser, Content: "old question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "old answer"},
		{Role: openai.ChatMessageRoleUser, Content: "common question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "common answer"},
		{Role: openai.ChatMessageRoleUser, Content: "new question"},
	}

	got := appendFrontendHistoryDelta(cloneChatMessages(backend), backend, frontend)
	if len(got) != 3 || got[2].Content != "new question" {
		t.Fatalf("expected only frontend tail after overlap, got %#v", got)
	}
	if countMessageContent(got, "common question") != 1 || countMessageContent(got, "common answer") != 1 {
		t.Fatalf("expected overlapping backend tail not to be duplicated, got %#v", got)
	}
}

func TestAppendFrontendHistoryDeltaAfterCompactionUsesOnlyRequestTail(t *testing.T) {
	backend := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "[compacted summary]"},
	}
	frontend := []ChatMessageInput{
		{Role: openai.ChatMessageRoleUser, Content: "old question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "old answer"},
		{Role: openai.ChatMessageRoleUser, Content: "new question"},
	}

	got := appendFrontendHistoryDelta(cloneChatMessages(backend), backend, frontend)
	if len(got) != 2 || got[0].Content != "[compacted summary]" || got[1].Content != "new question" {
		t.Fatalf("expected compacted backend plus latest request tail, got %#v", got)
	}
}

func TestSaveHistoryUsesGzipAndLoadsLegacyJSON(t *testing.T) {
	app := NewApp()
	app.historiesDir = t.TempDir()
	sessionID := "compressed-history"
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: strings.Repeat("compressible history ", 200)},
		{Role: openai.ChatMessageRoleAssistant, Content: "done"},
	}
	app.saveHistory(sessionID, messages)
	// A second save must replace the compressed file on Windows as well.
	messages[1].Content = "done twice"
	app.saveHistory(sessionID, messages)

	paths := app.historyDiskPaths(sessionID)
	compressed, err := os.Open(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(compressed)
	if err != nil {
		_ = compressed.Close()
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(zr)
	_ = zr.Close()
	_ = compressed.Close()
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip []openai.ChatCompletionMessage
	if err := json.Unmarshal(decoded, &roundTrip); err != nil || len(roundTrip) != 2 || roundTrip[1].Content != "done twice" {
		t.Fatalf("invalid compressed history: err=%v messages=%#v", err, roundTrip)
	}
	if _, err := os.Stat(paths[1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy JSON should not remain after gzip save, stat err=%v", err)
	}

	legacyID := "legacy-history"
	legacyPaths := app.historyDiskPaths(legacyID)
	legacyData, _ := json.Marshal(messages)
	if err := os.WriteFile(legacyPaths[1], legacyData, 0o600); err != nil {
		t.Fatal(err)
	}
	delete(app.histories, legacyID)
	loaded := app.loadSessionHistoryCopy(legacyID)
	if len(loaded) != 2 || loaded[1].Content != "done twice" {
		t.Fatalf("expected legacy JSON compatibility, got %#v", loaded)
	}
}

func TestTrimSavedHistoryUsesTokenBudgetAndKeepsToolTurnIntact(t *testing.T) {
	messages := make([]openai.ChatCompletionMessage, 0, 100)
	for turn := 0; turn < 25; turn++ {
		callID := fmt.Sprintf("call_%d", turn)
		messages = append(messages,
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: fmt.Sprintf("question-%d %s", turn, strings.Repeat("x", 50000))},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: callID, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: `{"files":[{"path":"a"}]}`}}}},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: callID, Content: strings.Repeat("result ", 1000)},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: fmt.Sprintf("answer-%d", turn)},
		)
	}

	got := trimSavedHistory(messages)
	if len(got) >= len(messages) {
		t.Fatalf("expected token budget to trim long history, got %d messages", len(got))
	}
	if got[0].Role != openai.ChatMessageRoleUser {
		t.Fatalf("trimmed history must start at a user boundary, got %#v", got[0])
	}
	for index, message := range got {
		if message.Role == openai.ChatMessageRoleTool {
			if index == 0 || len(got[index-1].ToolCalls) == 0 {
				t.Fatalf("orphan tool result at index %d: %#v", index, got)
			}
		}
	}
	if len(got) <= 40 {
		t.Fatalf("history must no longer be fixed to 40 messages, got %d", len(got))
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

type captureEventSink struct {
	name    string
	payload any
	count   int
}

func (s *captureEventSink) Emit(name string, payload any) {
	s.name = name
	s.payload = payload
	s.count++
}

func TestAppEmitUsesHostEventSink(t *testing.T) {
	sink := &captureEventSink{}
	app := NewApp()
	app.events = sink

	payload := map[string]any{"sessionId": "session-1", "revision": int64(3)}
	app.emit("plan:update", payload)

	if sink.count != 1 || sink.name != "plan:update" {
		t.Fatalf("unexpected event forwarding: count=%d name=%q", sink.count, sink.name)
	}
	got, ok := sink.payload.(map[string]any)
	if !ok || got["sessionId"] != "session-1" {
		t.Fatalf("unexpected event payload: %#v", sink.payload)
	}
}

func TestAppEmitWithoutHostSinkIsNoop(t *testing.T) {
	app := NewApp()
	app.emit("run:delta", "ignored")
}

func TestHandleTodoListRejectsMultipleInProgress(t *testing.T) {
	app := NewApp()
	_, err := app.handleTodoList("session-1", TodoListRequest{
		Todos: []TodoEntry{
			{Title: "Inspect implementation", Status: "in_progress"},
			{Title: "Run tests", Status: "in_progress"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "at most one todo") {
		t.Fatalf("handleTodoList() error = %v, want at-most-one in_progress rejection", err)
	}
}

func TestHandleTodoListAllowsSingleInProgress(t *testing.T) {
	app := NewApp()
	res, err := app.handleTodoList("session-1", TodoListRequest{
		Todos: []TodoEntry{
			{Title: "Inspect implementation", Status: "in_progress"},
			{Title: "Run tests", Status: "pending"},
		},
	})
	if err != nil {
		t.Fatalf("handleTodoList() error = %v", err)
	}
	got, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("handleTodoList() result type = %T, want map", res)
	}
	list, ok := got["todos"].([]TodoEntry)
	if !ok || len(list) != 2 || list[0].Status != "in_progress" {
		t.Fatalf("unexpected todos in result: %#v", got)
	}
}

func TestHandleTodoListStartsFirstPendingItem(t *testing.T) {
	app := NewApp()
	res, err := app.handleTodoList("session-1", TodoListRequest{
		Todos: []TodoEntry{
			{Title: "Inspect implementation", Status: "pending"},
			{Title: "Run tests", Status: "pending"},
		},
	})
	if err != nil {
		t.Fatalf("handleTodoList() error = %v", err)
	}
	got := res.(map[string]any)["todos"].([]TodoEntry)
	if got[0].Status != "in_progress" || got[1].Status != "pending" {
		t.Fatalf("expected first pending item to start, got %#v", got)
	}
}

func TestHandleTodoListDoesNotRestartAllDoneList(t *testing.T) {
	app := NewApp()
	res, err := app.handleTodoList("session-1", TodoListRequest{
		Todos: []TodoEntry{{Title: "Finished", Status: "done"}},
	})
	if err != nil {
		t.Fatalf("handleTodoList() error = %v", err)
	}
	got := res.(map[string]any)["todos"].([]TodoEntry)
	if got[0].Status != "done" {
		t.Fatalf("completed todo was unexpectedly restarted: %#v", got)
	}
}

func TestAppendPlanForUserTurnAddsTransientPlanBeforeLatestUser(t *testing.T) {
	app := NewApp()
	if _, err := app.handleTodoList("session-1", TodoListRequest{
		Todos: []TodoEntry{{Title: "Inspect implementation", Status: "in_progress"}},
	}); err != nil {
		t.Fatalf("handleTodoList() error = %v", err)
	}
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "earlier"},
		{Role: openai.ChatMessageRoleAssistant, Content: "done"},
		{Role: openai.ChatMessageRoleUser, Content: "continue"},
	}
	got := app.appendPlanForUserTurn("session-1", messages)
	if len(got) != len(messages)+1 {
		t.Fatalf("message count = %d, want %d", len(got), len(messages)+1)
	}
	if len(messages) != 3 {
		t.Fatal("appendPlanForUserTurn mutated the original messages")
	}
	if got[2].Role != openai.ChatMessageRoleUser || !strings.Contains(got[2].Content, "- [~] Inspect implementation") {
		t.Fatalf("plan was not inserted before the latest user message: %#v", got)
	}
	if strings.Contains(got[2].Content, "revision") || strings.Contains(got[2].Content, "<ally-plan") {
		t.Fatalf("plan contains an internal marker: %q", got[2].Content)
	}
	if got[3].Content != "continue" {
		t.Fatalf("latest user message moved or changed: %#v", got[3])
	}
}

func TestAppendPlanForUserTurnDoesNothingWithoutPlan(t *testing.T) {
	app := NewApp()
	messages := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hello"}}
	got := app.appendPlanForUserTurn("session-1", messages)
	if len(got) != len(messages) || got[0].Content != messages[0].Content {
		t.Fatalf("unexpected plan injection without a plan: %#v", got)
	}
}

func TestTodoResultDoesNotContainPlanMarker(t *testing.T) {
	app := NewApp()
	res, err := app.handleTodoList("session-1", TodoListRequest{
		Todos: []TodoEntry{{Title: "Inspect implementation", Status: "pending"}},
	})
	if err != nil {
		t.Fatalf("handleTodoList() error = %v", err)
	}
	message := res.(map[string]any)["message"].(string)
	if strings.Contains(message, "revision") || strings.Contains(message, "<ally-plan") {
		t.Fatalf("todo result contains an internal plan marker: %q", message)
	}
}

func TestModelUsageFromResponsesCountsUncachedInputAsMiss(t *testing.T) {
	usage := modelUsageFromResponses(oaresp.ResponseUsage{
		InputTokens:  120,
		OutputTokens: 8,
		InputTokensDetails: oaresp.ResponseUsageInputTokensDetails{
			CachedTokens: 0,
		},
	})
	if usage == nil {
		t.Fatal("modelUsageFromResponses() returned nil")
	}
	if usage.CacheHitTokens != 0 || usage.CacheMissTokens != 120 {
		t.Fatalf("cache usage = hit %d miss %d, want hit 0 miss 120", usage.CacheHitTokens, usage.CacheMissTokens)
	}
}

func TestModelUsageFromResponsesEventReadsCachedInput(t *testing.T) {
	usage := modelUsageFromResponsesEvent([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":800},"output_tokens":8}}}`))
	if usage == nil {
		t.Fatal("modelUsageFromResponsesEvent() returned nil")
	}
	if usage.CacheHitTokens != 800 || usage.CacheMissTokens != 200 {
		t.Fatalf("cache usage = hit %d miss %d, want hit 800 miss 200", usage.CacheHitTokens, usage.CacheMissTokens)
	}
}

func TestCleanAppliedUpdateDirsKeepsOnlyCurrentTag(t *testing.T) {
	root := t.TempDir()

	// Release-tag directories: current, older, and a pre-release tag.
	for _, tag := range []string{"v1.4.0", "v1.3.0", "v1.2.0", "v1.3.0-rc1"} {
		if err := os.MkdirAll(filepath.Join(root, tag), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Non-tag entries that must never be deleted: a subdirectory, a file.
	if err := os.MkdirAll(filepath.Join(root, "mnt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "v1.5.0.zip"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanAppliedUpdateDirs(root, "v1.4.0")

	for _, tag := range []string{"v1.4.0", "mnt", "v1.5.0.zip"} {
		if _, err := os.Stat(filepath.Join(root, tag)); err != nil {
			t.Errorf("expected %q to remain, got err %v", tag, err)
		}
	}
	for _, tag := range []string{"v1.3.0", "v1.2.0", "v1.3.0-rc1"} {
		if _, err := os.Stat(filepath.Join(root, tag)); err == nil {
			t.Errorf("expected stale tag directory %q to be removed", tag)
		}
	}
}

func TestCleanAppliedUpdateDirsRejectsPathTraversalNames(t *testing.T) {
	root := t.TempDir()

	// A hostile directory name that could escape the updates root if it were
	// joined naively. The tag regex rejects it, so it must stay untouched.
	hostile := ".."
	if err := os.MkdirAll(filepath.Join(root, hostile), 0o755); err != nil {
		t.Fatal(err)
	}

	cleanAppliedUpdateDirs(root, "v1.4.0")

	if _, err := os.Stat(filepath.Join(root, hostile)); err != nil {
		t.Errorf("expected non-tag directory %q to remain, got err %v", hostile, err)
	}
}
