// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	legacyopenai "github.com/sashabaranov/go-openai"
)

// ── OpenAI Chat: reasoning_content replay in the tool loop ──────────────────

// TestRunChatReplaysReasoningContentOnToolTurn verifies the DeepSeek/Kimi/GLM
// thinking-mode contract end-to-end: after a tool call whose response carried
// reasoning_content, the NEXT request must replay the assistant tool-call
// message WITH its reasoning_content. Without it, thinking-mode providers
// reject the request with 400 ("The reasoning_content in the thinking mode
// must be passed back to the API").
func TestRunChatReplaysReasoningContentOnToolTurn(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		n := requests.Add(1)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			fmt.Fprint(w, "data: "+`{"id":"1","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"需要调用 calculate 工具来算 1+1。"}}]}`+"\n\n")
			fmt.Fprint(w, "data: "+`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_calc_1","type":"function","function":{"name":"calculate","arguments":"{\"expression\":\"1+1\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, "data: "+`{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			fmt.Fprint(w, "data: "+`{"id":"2","choices":[{"index":0,"delta":{"role":"assistant","content":"The answer is 2."}}]}`+"\n\n")
			fmt.Fprint(w, "data: "+`{"id":"2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "reasoning-replay-chat",
		Message:   "1+1 等于几",
		Config: ConfigState{
			APIFormat: apiFormatOpenAIChat,
			BaseURL:   server.URL,
			APIKeys:   []string{"test-key"},
			Model:     "test-model",
			MaxTokens: 256,
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

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected >= 2 requests, got %d", len(bodies))
	}
	if !assistantToolCallHasReasoning(t, bodies[1], "call_calc_1", "需要调用 calculate 工具来算 1+1。") {
		t.Fatalf("second request must replay the assistant tool-call message with its reasoning_content")
	}
}

// TestRunChatKeepsEmptyReasoningFieldWithExplicitEffort verifies the empty
// reasoning contract: with an explicit effort level (thinking mode on), a
// tool-call response with EMPTY reasoning still replayed with the FIELD
// present (reasoning_content:"") — go-openai's omitempty would otherwise
// drop it and DeepSeek V4 rejects the request (karminski's 59% repro).
func TestRunChatKeepsEmptyReasoningFieldWithExplicitEffort(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		n := requests.Add(1)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			// Obvious tool call: provider emits an explicitly empty reasoning.
			fmt.Fprint(w, "data: "+`{"id":"1","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":""}}]}`+"\n\n")
			fmt.Fprint(w, "data: "+`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_calc_1","type":"function","function":{"name":"calculate","arguments":"{\"expression\":\"2+2\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, "data: "+`{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			fmt.Fprint(w, "data: "+`{"id":"2","choices":[{"index":0,"delta":{"role":"assistant","content":"4"}}]}`+"\n\n")
			fmt.Fprint(w, "data: "+`{"id":"2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "reasoning-empty-chat",
		Message:   "2+2",
		Config: ConfigState{
			APIFormat:      apiFormatOpenAIChat,
			BaseURL:        server.URL,
			APIKeys:        []string{"test-key"},
			Model:          "test-model",
			MaxTokens:      256,
			Workspace:      t.TempDir(),
			ReasoningEffort: reasoningEffortMedium,
		},
	}); err != nil {
		t.Fatalf("StartChat() error = %v", err)
	}

	select {
	case <-recorder.done:
	case <-time.After(15 * time.Second):
		mu.Lock()
		t.Fatalf("run did not finish in time; requests=%d bodies=%d end=%q", requests.Load(), len(bodies), recorder.end)
		mu.Unlock()
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected >= 2 requests, got %d", len(bodies))
	}
	if !assistantToolCallHasReasoningField(t, bodies[1], "call_calc_1") {
		t.Fatalf("with explicit effort, the replayed tool-call message must keep an explicit (empty) reasoning_content field")
	}
}

// TestRunChatNoReasoningFieldByDefault verifies the zero-impact guarantee:
// under the default auto effort with no captured reasoning, the wire body
// stays untouched — assistant messages carry NO reasoning_content field, so
// non-thinking providers never see the extension.
func TestRunChatNoReasoningFieldByDefault(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		n := requests.Add(1)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			fmt.Fprint(w, "data: "+`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_calc_1","type":"function","function":{"name":"calculate","arguments":"{\"expression\":\"5+5\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, "data: "+`{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			fmt.Fprint(w, "data: "+`{"id":"2","choices":[{"index":0,"delta":{"role":"assistant","content":"10"}}]}`+"\n\n")
			fmt.Fprint(w, "data: "+`{"id":"2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "reasoning-default-chat",
		Message:   "5+5",
		Config: ConfigState{
			APIFormat: apiFormatOpenAIChat,
			BaseURL:   server.URL,
			APIKeys:   []string{"test-key"},
			Model:     "test-model",
			MaxTokens: 256,
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

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected >= 2 requests, got %d", len(bodies))
	}
	if assistantToolCallHasReasoningField(t, bodies[1], "call_calc_1") {
		t.Fatalf("default auto effort with no reasoning must NOT add reasoning_content to the wire body")
	}
}

// ── Anthropic: end-to-end thinking replay through streamAnthropicMessages ──

func TestAnthropicStreamReplaysThinkingBlocks(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		n := requests.Add(1)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		event := func(name, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
		}
		event("message_start", `{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"output_tokens":1}}}`)
		if n == 1 {
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"先思考一下。"}}`)
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_abc"}}`)
			event("content_block_stop", `{"type":"content_block_stop","index":0}`)
			event("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"calculate","input":{}}}`)
			event("content_block_stop", `{"type":"content_block_stop","index":1}`)
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`)
		} else {
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"答案。"}}`)
			event("content_block_stop", `{"type":"content_block_stop","index":0}`)
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
		}
		event("message_stop", `{"type":"message_stop"}`)
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "anthropic-thinking-e2e",
		Message:   "算一下",
		Config: ConfigState{
			APIFormat: apiFormatAnthropicMessages,
			BaseURL:   server.URL,
			APIKeys:   []string{"test-key"},
			Model:     "claude-test",
			MaxTokens: 1024,
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

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected >= 2 requests, got %d", len(bodies))
	}
	second := bodies[1]
	if !strings.Contains(second, `"type":"thinking"`) {
		t.Fatal("second Anthropic request must replay the thinking block")
	}
	if !strings.Contains(second, "sig_abc") {
		t.Fatal("second Anthropic request must replay the signature verbatim")
	}
	if !strings.Contains(second, `"thinking":"先思考一下。"`) {
		t.Fatal("second Anthropic request must replay the thinking text")
	}
}

// ── Responses: end-to-end reasoning item replay ─────────────────────────────

func TestResponsesStreamReplaysReasoningItems(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		n := requests.Add(1)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		event := func(name, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
		}
		event("response.created", `{"type":"response.created","response":{"id":"resp_1"}}`)
		if n == 1 {
			event("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"推理摘要"}],"encrypted_content":"enc-payload-1"}}`)
			event("response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_calc_1","name":"calculate","arguments":""}}`)
			event("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"{\"expression\":\"3+3\"}"}`)
			event("response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":1,"arguments":"{\"expression\":\"3+3\"}"}`)
			event("response.output_item.done", `{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_calc_1","name":"calculate","arguments":"{\"expression\":\"3+3\"}"}}`)
		} else {
			event("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant"}}`)
			event("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"delta":"6"}`)
		}
		event("response.completed", `{"type":"response.completed","response":{"id":"resp_x","usage":{"input_tokens":10,"output_tokens":5}}}`)
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	app.stats = nil
	recorder := &runEventRecorder{done: make(chan struct{}, 1)}
	app.events = recorder

	if _, err := app.StartChat(ChatRequest{
		SessionID: "responses-reasoning-e2e",
		Message:   "3+3",
		Config: ConfigState{
			APIFormat: apiFormatOpenAIResponses,
			BaseURL:   server.URL,
			APIKeys:   []string{"test-key"},
			Model:     "gpt-test",
			MaxTokens: 256,
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

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected >= 2 requests, got %d", len(bodies))
	}
	second := bodies[1]
	if !strings.Contains(second, `"type":"reasoning"`) {
		t.Fatal("second Responses request must replay the reasoning item")
	}
	if !strings.Contains(second, "enc-payload-1") {
		t.Fatal("second Responses request must carry the encrypted_content")
	}
	if !strings.Contains(second, "rs_1") {
		t.Fatal("second Responses request must reference the reasoning item id")
	}
	if !strings.Contains(second, "reasoning.encrypted_content") {
		t.Fatal("requests must ask for reasoning.encrypted_content via include")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func assistantToolCallHasReasoning(t *testing.T, body, callID, wantReasoning string) bool {
	t.Helper()
	var req struct {
		Messages []struct {
			Role             string          `json:"role"`
			ReasoningContent json.RawMessage `json:"reasoning_content"`
			ToolCalls        []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	for _, m := range req.Messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != callID {
				continue
			}
			var got string
			if len(m.ReasoningContent) > 0 {
				_ = json.Unmarshal(m.ReasoningContent, &got)
			}
			return got == wantReasoning
		}
	}
	return false
}

func assistantToolCallHasReasoningField(t *testing.T, body, callID string) bool {
	t.Helper()
	var req struct {
		Messages []struct {
			Role             string          `json:"role"`
			ReasoningContent json.RawMessage `json:"reasoning_content"`
			ToolCalls        []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	for _, m := range req.Messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == callID {
				return len(m.ReasoningContent) > 0 && string(m.ReasoningContent) != "null"
			}
		}
	}
	return false
}

// ── Unit: replay activation & empty-field patch ─────────────────────────────

func TestReasoningContentReplayActive(t *testing.T) {
	plain := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "hello"},
	}
	if reasoningContentReplayActive(plain, ConfigState{}) {
		t.Fatal("auto effort with plain history must keep replay off")
	}
	withEffort := ConfigState{ReasoningEffort: reasoningEffortHigh}
	if !reasoningContentReplayActive(plain, withEffort) {
		t.Fatal("explicit effort must turn replay on")
	}
	withHistory := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "hello", ReasoningContent: "thought"},
	}
	if !reasoningContentReplayActive(withHistory, ConfigState{}) {
		t.Fatal("history carrying reasoning must turn replay on")
	}

	// Model switch: earlier turn had reasoning, but the current turn's model
	// produced an assistant response with NO reasoning (e.g. user switched to GPT-4o).
	// Replay must stay OFF so non-thinking models are not contaminated.
	switchedModel := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "old question"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "r1 answer", ReasoningContent: "thought"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "new question"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "plain answer", ToolCalls: []legacyopenai.ToolCall{{ID: "c1"}}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "c1", Content: "result"},
	}
	if reasoningContentReplayActive(switchedModel, ConfigState{}) {
		t.Fatal("switched non-thinking model in current turn must keep replay off despite older history")
	}
}

func TestPatchReasoningContentFields(t *testing.T) {
	// Message 0: user message
	// Message 1: frozen historical assistant (without tool calls; must stay untouched)
	// Message 2: active tool-call assistant (lacks reasoning; must gain reasoning_content: "")
	// Message 3: tool result message for the active call
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"plain history"},{"role":"assistant","content":"","tool_calls":[{"id":"c1"}]},{"role":"tool","tool_call_id":"c1","content":"ok"}]}`)
	patched, changed := patchReasoningContentFields(body, "")
	if !changed {
		t.Fatal("expected the active tool-call assistant message to gain reasoning_content")
	}
	var payload struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatalf("unmarshal patched body: %v", err)
	}
	// Historical assistant (Message 1) must remain frozen without reasoning_content.
	if _, exists := payload.Messages[1]["reasoning_content"]; exists {
		t.Fatal("historical assistant message must remain frozen and not gain reasoning_content")
	}
	// Active tool-call assistant (Message 2) must gain reasoning_content: "".
	if got := string(payload.Messages[2]["reasoning_content"]); got != `""` {
		t.Fatalf("tool-call assistant reasoning_content = %s, want \"\"", got)
	}
	if _, exists := payload.Messages[0]["reasoning_content"]; exists {
		t.Fatal("user message must not gain reasoning_content")
	}

	// Dialect test: when a message in the body uses "reasoning" (vLLM style),
	// the empty field must be echoed back under the same dialect.
	vllmBody := []byte(`{"model":"m","messages":[{"role":"user","content":"q"},{"role":"assistant","content":"","tool_calls":[{"id":"c2"}]},{"role":"tool","tool_call_id":"c2","content":"ok"}]}`)
	vllmPatched, changed := patchReasoningContentFields(vllmBody, "reasoning")
	if !changed {
		t.Fatal("expected tool-call assistant to be patched under detected dialect")
	}
	var vllmPayload struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(vllmPatched, &vllmPayload); err != nil {
		t.Fatalf("unmarshal vllm patched body: %v", err)
	}
	if got := string(vllmPayload.Messages[1]["reasoning"]); got != `""` {
		t.Fatalf("tool-call assistant reasoning = %s, want \"\"", got)
	}
	if _, exists := vllmPayload.Messages[1]["reasoning_content"]; exists {
		t.Fatal("vLLM dialect must not inject reasoning_content")
	}
}

// ── Anthropic: thinking block + signature replay ────────────────────────────

func TestWithAnthropicThinkingBlocks(t *testing.T) {
	blocks := []anthropicThinkingBlock{
		{Thinking: "step one", Signature: "sig_1"},
		{Data: "redacted-bytes"},
	}
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock("question")),
		anthropic.NewAssistantMessage(
			anthropic.NewToolUseBlock("t1", map[string]any{"a": 1}, "grep"),
		),
		anthropic.NewUserMessage(anthropic.NewToolResultBlock("t1", "ok", false)),
	}
	out := withAnthropicThinkingBlocks(messages, blocks, "claude-3-7-sonnet")
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	assistant := out[1]
	if assistant.Role != anthropic.MessageParamRoleAssistant {
		t.Fatalf("message 1 role = %v, want assistant", assistant.Role)
	}
	if len(assistant.Content) != 3 {
		t.Fatalf("expected thinking + redacted + tool_use blocks, got %d", len(assistant.Content))
	}
	if assistant.Content[0].OfThinking == nil || assistant.Content[0].OfThinking.Thinking != "step one" || assistant.Content[0].OfThinking.Signature != "sig_1" {
		t.Fatalf("block 0 must be the thinking block with signature, got %+v", assistant.Content[0])
	}
	if assistant.Content[1].OfRedactedThinking == nil || assistant.Content[1].OfRedactedThinking.Data != "redacted-bytes" {
		t.Fatalf("block 1 must be the redacted thinking block, got %+v", assistant.Content[1])
	}
	if assistant.Content[2].OfToolUse == nil || assistant.Content[2].OfToolUse.ID != "t1" {
		t.Fatalf("block 2 must be the tool_use block, got %+v", assistant.Content[2])
	}
	// Idempotence: replaying again must not stack blocks.
	again := withAnthropicThinkingBlocks(out, blocks, "claude-3-7-sonnet")
	if len(again[1].Content) != 3 {
		t.Fatalf("second application must be a no-op, got %d blocks", len(again[1].Content))
	}
	// No assistant message: payload dropped, messages unchanged.
	onlyUser := []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("q"))}
	if got := withAnthropicThinkingBlocks(onlyUser, blocks, "claude-3-7-sonnet"); len(got) != 1 || len(got[0].Content) != 1 {
		t.Fatal("without an assistant message the blocks must be dropped")
	}

	// Claude unsigned thinking guard: an unsigned thinking block must NOT be replayed on Claude models (would trigger 400),
	// but is permitted on non-Claude Anthropic-compatible proxies.
	unsignedBlocks := []anthropicThinkingBlock{
		{Thinking: "unsigned thoughts", Signature: ""},
	}
	freshClaudeMessages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock("question")),
		anthropic.NewAssistantMessage(
			anthropic.NewToolUseBlock("t1", map[string]any{"a": 1}, "grep"),
		),
	}
	claudeGuarded := withAnthropicThinkingBlocks(freshClaudeMessages, unsignedBlocks, "claude-3-7-sonnet")
	if len(claudeGuarded[1].Content) != 1 {
		t.Fatalf("Claude model must not replay unsigned thinking block, got %d blocks", len(claudeGuarded[1].Content))
	}
	freshProxyMessages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock("question")),
		anthropic.NewAssistantMessage(
			anthropic.NewToolUseBlock("t1", map[string]any{"a": 1}, "grep"),
		),
	}
	proxyAllowed := withAnthropicThinkingBlocks(freshProxyMessages, unsignedBlocks, "deepseek-r1")
	if len(proxyAllowed[1].Content) != 2 || proxyAllowed[1].Content[0].OfThinking == nil {
		t.Fatalf("non-Claude model must allow replaying unsigned thinking block, got %d blocks", len(proxyAllowed[1].Content))
	}
}

// ── Responses: reasoning item + encrypted_content replay ────────────────────

func TestWithResponsesReasoningItems(t *testing.T) {
	items := []responsesReasoningItem{
		{ID: "rs_1", EncryptedContent: "enc-1", SummaryText: "thoughts"},
	}
	messages := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "question"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{ID: "fc_1", Function: legacyopenai.FunctionCall{Name: "calculate", Arguments: "{}"}}}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "fc_1", Content: "2"},
	}
	out := withResponsesReasoningItems(messages, items)
	if len(out) != 4 {
		t.Fatalf("expected 4 messages (carrier injected), got %d", len(out))
	}
	if out[1].Role != roleResponsesReasoningCarrier {
		t.Fatalf("message 1 role = %q, want carrier", out[1].Role)
	}
	carriers := responsesReasoningCarriers(out)
	if len(carriers) != 1 || carriers[0].ID != "rs_1" || carriers[0].EncryptedContent != "enc-1" || carriers[0].SummaryText != "thoughts" {
		t.Fatalf("carrier round-trip mismatch: %+v", carriers)
	}
	// The carrier must expand into a real reasoning input item.
	_, input := buildOpenAIResponsesInput(out)
	if len(input) < 2 || input[1].OfReasoning == nil {
		t.Fatalf("expected a reasoning input item at position 1, got %+v", input)
	}
	if input[1].OfReasoning.ID != "rs_1" {
		t.Fatalf("reasoning item id = %q, want rs_1", input[1].OfReasoning.ID)
	}
	if input[1].OfReasoning.EncryptedContent.Value != "enc-1" {
		t.Fatalf("reasoning item encrypted_content mismatch: %+v", input[1].OfReasoning.EncryptedContent)
	}
	// Sanitizer must drop the carrier so it never persists.
	sanitized := sanitizeHistoryMessages(out)
	for _, m := range sanitized {
		if m.Role == roleResponsesReasoningCarrier {
			t.Fatal("sanitizeHistoryMessages must drop reasoning-item carriers")
		}
	}
}
