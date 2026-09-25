// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3.0. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestRepairDanglingToolCalls(t *testing.T) {
	assistantWithCalls := func(ids ...string) openai.ChatCompletionMessage {
		calls := make([]openai.ToolCall, 0, len(ids))
		for _, id := range ids {
			calls = append(calls, openai.ToolCall{ID: id, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: "{}"}})
		}
		return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, ToolCalls: calls}
	}
	toolMsg := func(id string) openai.ChatCompletionMessage {
		return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: id, Content: `{"ok":true}`}
	}
	userMsg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "next"}

	t.Run("valid pairing passes through unchanged", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a", "b"),
			toolMsg("a"), toolMsg("b"),
			{Role: openai.ChatMessageRoleAssistant, Content: "done"},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 5 || len(out[1].ToolCalls) != 2 || out[2].ToolCallID != "a" || out[3].ToolCallID != "b" {
			t.Fatalf("valid pairing was modified: %#v", out)
		}
	})

	t.Run("trailing dangling call stripped, answered call kept", func(t *testing.T) {
		// The exact state a mid-batch panic would persist: assistant declared
		// two calls, only one result arrived before the crash.
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a", "b"),
			toolMsg("a"),
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
		}
		if len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].ID != "a" {
			t.Fatalf("dangling call b was not stripped: %#v", out[1].ToolCalls)
		}
		if out[2].ToolCallID != "a" {
			t.Fatalf("answered result must survive: %#v", out[2])
		}
	})

	t.Run("mid-history dangling closed by user message", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a"),
			userMsg,
			{Role: openai.ChatMessageRoleAssistant, Content: "after"},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
		}
		if len(out[1].ToolCalls) != 0 {
			t.Fatalf("dangling call a must be stripped when the turn closes: %#v", out[1].ToolCalls)
		}
	})

	t.Run("assistant with all calls dangling and no text dropped", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a"),
			toolMsg("a"),
			assistantWithCalls("b"),
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("empty dangling assistant must be dropped, got %d: %#v", len(out), out)
		}
	})

	t.Run("assistant text preserved when calls dangle", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			{Role: openai.ChatMessageRoleAssistant, Content: "partial stream", ToolCalls: assistantWithCalls("a").ToolCalls},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 2 || out[1].Content != "partial stream" || len(out[1].ToolCalls) != 0 {
			t.Fatalf("assistant text must survive call stripping: %#v", out)
		}
	})

	t.Run("orphan and duplicate tool messages dropped", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a"),
			toolMsg("ghost"),
			toolMsg("a"),
			toolMsg("a"),
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("orphan and duplicate results must be dropped, got %d: %#v", len(out), out)
		}
		if out[2].ToolCallID != "a" {
			t.Fatalf("first result must survive: %#v", out[2])
		}
	})

	t.Run("duplicate call IDs are repaired instead of left dangling", func(t *testing.T) {
		// A relay can return two calls sharing one ID. The old ID-keyed table
		// consumed its entry on the first result, dropped the second result as a
		// duplicate and then had nothing left to strip — the unanswered call stayed
		// in the history and every later request was rejected with 400.
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("dup", "dup"),
			toolMsg("dup"),
			toolMsg("dup"),
			{Role: openai.ChatMessageRoleAssistant, Content: "done"},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 5 || len(out[1].ToolCalls) != 2 {
			t.Fatalf("two answered calls must both survive: %#v", out)
		}

		// Only one result for two same-ID calls: the second call has to be
		// stripped so nothing is left unanswered.
		out = repairDanglingToolCalls(in[:3])
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
		}
		if len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].ID != "dup" {
			t.Fatalf("the unanswered duplicate call must be stripped: %#v", out[1].ToolCalls)
		}
		if out[2].ToolCallID != "dup" {
			t.Fatalf("the answered result must survive: %#v", out[2])
		}
	})
}

func TestIsAnthropicSignatureRejectionError(t *testing.T) {
	cases := []struct {
		err  string
		want bool
	}{
		{"messages.3: signature in thinking block cannot be modified", true},
		{"invalid signature for thinking block", true},
		{"thinking block signature required", true},
		{"rate limit exceeded", false},
		{"context length exceeded", false},
	}
	for _, tc := range cases {
		if got := isAnthropicSignatureRejectionError(errors.New(tc.err)); got != tc.want {
			t.Errorf("isAnthropicSignatureRejectionError(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestStripReasoningContent(t *testing.T) {
	in := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
		{Role: openai.ChatMessageRoleAssistant, Content: "hello", ReasoningContent: "let me think..."},
		{Role: openai.ChatMessageRoleTool, Content: "tool result", ToolCallID: "c1"},
		{Role: openai.ChatMessageRoleAssistant, Content: "final answer", ReasoningContent: "final thoughts"},
	}
	out := stripReasoningContent(in)
	if len(out) != len(in) {
		t.Fatalf("expected len %d, got %d", len(in), len(out))
	}
	if out[1].ReasoningContent != "" {
		t.Fatalf("expected assistant 1 ReasoningContent to be stripped, got %q", out[1].ReasoningContent)
	}
	if out[1].Content != "hello" {
		t.Fatalf("expected assistant 1 Content to be preserved, got %q", out[1].Content)
	}
	if out[3].ReasoningContent != "" {
		t.Fatalf("expected assistant 2 ReasoningContent to be stripped, got %q", out[3].ReasoningContent)
	}
	// Original must not be modified
	if in[1].ReasoningContent != "let me think..." {
		t.Fatalf("original message was mutated")
	}
}

func TestIsCrossModelSwitch(t *testing.T) {
	cases := []struct {
		name string
		prev sessionModelConfig
		curr ConfigState
		want bool
	}{
		{
			name: "empty prev model",
			prev: sessionModelConfig{},
			curr: ConfigState{Model: "gpt-4o"},
			want: false,
		},
		{
			name: "same model same format",
			prev: sessionModelConfig{model: "claude-3-7-sonnet", apiFormat: "anthropic"},
			curr: ConfigState{Model: "claude-3-7-sonnet", APIFormat: "anthropic"},
			want: false,
		},
		{
			name: "same model case insensitive",
			prev: sessionModelConfig{model: "Claude-3-7-Sonnet", apiFormat: "anthropic"},
			curr: ConfigState{Model: "claude-3-7-sonnet", APIFormat: "anthropic"},
			want: false,
		},
		{
			name: "different model",
			prev: sessionModelConfig{model: "claude-3-7-sonnet", apiFormat: "anthropic"},
			curr: ConfigState{Model: "gpt-4o", APIFormat: "anthropic"},
			want: true,
		},
		{
			name: "same model different api format",
			prev: sessionModelConfig{model: "claude-3-7-sonnet", apiFormat: "anthropic"},
			curr: ConfigState{Model: "claude-3-7-sonnet", APIFormat: "openai_chat"},
			want: true,
		},
		{
			name: "same model different provider",
			prev: sessionModelConfig{model: "deepseek-r1", apiFormat: "openai_chat", providerName: "deepseek"},
			curr: ConfigState{Model: "deepseek-r1", APIFormat: "openai_chat", ProviderName: "openrouter"},
			want: true,
		},
		{
			name: "same model different base URL",
			prev: sessionModelConfig{model: "deepseek-r1", apiFormat: "openai_chat", baseURL: "https://api.deepseek.com"},
			curr: ConfigState{Model: "deepseek-r1", APIFormat: "openai_chat", BaseURL: "https://custom-gateway.internal"},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isCrossModelSwitch(tc.prev, tc.curr)
			if got != tc.want {
				t.Fatalf("isCrossModelSwitch(%+v, %+v) = %v, want %v", tc.prev, tc.curr, got, tc.want)
			}
		})
	}
}

func TestStripSessionReasoning(t *testing.T) {
	app := NewApp()
	app.config.Workspace = t.TempDir()
	sessionID := "test-session-strip"

	app.mu.Lock()
	app.histories[sessionID] = []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "calc 1+1"},
		{Role: openai.ChatMessageRoleAssistant, Content: "2", ReasoningContent: "1+1=2, obvious"},
	}
	app.liveBreakdown[sessionID] = ContextBreakdown{Reasoning: 50, Total: 100}
	app.contextAnchors[sessionID] = contextAnchor{covered: 2, tokens: 100}
	app.mu.Unlock()

	// Seed reasoning stash with a turn
	cfg := ConfigState{responsesPromptCacheKey: openAIResponsesPromptCacheKey(sessionID), APIFormat: "anthropic"}
	app.reasoningStash.appendTurn(reasoningReplayKey(cfg, "claude-3-7-sonnet"), reasoningTurn{
		callIDs:   []string{"c1"},
		anthropic: []anthropicThinkingBlock{{Thinking: "some thought", Signature: "sig"}},
	})

	app.stripSessionReasoning(sessionID)

	app.mu.Lock()
	hist := app.histories[sessionID]
	_, hasAnchor := app.contextAnchors[sessionID]
	bd := app.liveBreakdown[sessionID]
	app.mu.Unlock()

	if len(hist) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(hist))
	}
	if hist[1].ReasoningContent != "" {
		t.Fatalf("expected ReasoningContent to be stripped, got %q", hist[1].ReasoningContent)
	}
	if hasAnchor {
		t.Fatalf("expected context anchor to be deleted")
	}
	if bd.Reasoning != 0 {
		t.Fatalf("expected liveBreakdown Reasoning to be 0, got %d", bd.Reasoning)
	}
	if payload := app.reasoningStash.get(reasoningReplayKey(cfg, "claude-3-7-sonnet")); payload != nil && len(payload.turns) > 0 {
		t.Fatalf("expected reasoning stash to be cleared for session")
	}
}

// TestStartChatCrossModelSwitchStripsReasoning covers the StartChat wiring: a
// model change for a session drops the previous model's reasoning before the
// next request goes out, and switching back must not resurrect it. The provider
// is a local fake — the test asserts on what the requests actually carry, which
// keeps it off the network (a real endpoint would make it slow and dependent on
// this machine's connectivity).
func TestStartChatCrossModelSwitchStripsReasoning(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("ok"))
		fmt.Fprint(w, sseChatFinishChunk("stop"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	app := NewApp()
	ws := t.TempDir()
	app.config.Workspace = ws
	app.initialized = true
	app.stats = nil
	sessionID := "test-session-cross-model"

	cfgA := ConfigState{
		Model:     "model-a",
		APIFormat: apiFormatOpenAIChat,
		APIKeys:   []string{"key-1"},
		BaseURL:   server.URL,
		MaxTokens: 64,
		Workspace: ws,
	}
	cfgB := ConfigState{
		Model:     "model-b",
		APIFormat: apiFormatOpenAIChat,
		APIKeys:   []string{"key-2"},
		BaseURL:   server.URL,
		MaxTokens: 64,
		Workspace: ws,
	}

	// The session is parked in the state a finished model-A turn leaves behind: its
	// reasoning text on the history and its signed trace in the replay ledger.
	app.mu.Lock()
	app.sessionModelConfigs = map[string]sessionModelConfig{sessionID: sessionModelConfigFrom(cfgA)}
	app.histories[sessionID] = []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
		{Role: openai.ChatMessageRoleAssistant, Content: "hello", ReasoningContent: "thinking from model A"},
	}
	app.mu.Unlock()

	replayKeyA := reasoningReplayKey(ConfigState{
		responsesPromptCacheKey: openAIResponsesPromptCacheKey(sessionID),
		APIFormat:               apiFormatOpenAIChat,
	}, "model-a")
	app.reasoningStash.appendTurn(replayKeyA, reasoningTurn{
		callIDs:     []string{"call-1"},
		chatDetails: json.RawMessage(`[{"type":"reasoning.text","text":"thinking from model A"}]`),
	})

	runOn := func(cfg ConfigState, message string) {
		t.Helper()
		recorder := &runEventRecorder{done: make(chan struct{}, 1)}
		app.events = recorder
		if _, err := app.StartChat(ChatRequest{SessionID: sessionID, Message: message, Config: cfg}); err != nil {
			t.Fatalf("StartChat(%s) error = %v", cfg.Model, err)
		}
		select {
		case <-recorder.done:
		case <-time.After(15 * time.Second):
			t.Fatalf("run on %s did not finish in time", cfg.Model)
		}
		if recorder.end != "run:done" {
			t.Fatalf("run on %s ended with %q, want run:done", cfg.Model, recorder.end)
		}
	}

	// 1. Switch model A -> model B.
	runOn(cfgB, "next message")
	app.mu.Lock()
	histAfterSwitch := app.histories[sessionID]
	app.mu.Unlock()

	if len(histAfterSwitch) >= 2 && histAfterSwitch[1].ReasoningContent != "" {
		t.Fatalf("expected model A ReasoningContent to be stripped after switching to model B, got %q", histAfterSwitch[1].ReasoningContent)
	}
	if payload := app.reasoningStash.get(replayKeyA); payload != nil && len(payload.turns) > 0 {
		t.Fatalf("expected model A reasoning stash to be cleared after switching to model B")
	}

	// 2. Switch back model B -> model A: the old trace must stay gone, replaying a
	// trace another model produced is what the provider rejects with 400.
	runOn(cfgA, "third message")
	if payload := app.reasoningStash.get(replayKeyA); payload != nil && len(payload.turns) > 0 {
		t.Fatalf("switching back to model A must NOT resurrect old thinking blocks")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected >= 2 provider requests, got %d", len(bodies))
	}
	for i, body := range bodies {
		if strings.Contains(body, "thinking from model A") {
			t.Fatalf("request %d still carries the previous model's reasoning: %s", i+1, body)
		}
	}
}
