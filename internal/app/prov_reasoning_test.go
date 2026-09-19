// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

import (
	"bytes"
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

	oaresp "github.com/openai/openai-go/v3/responses"
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
			APIFormat:       apiFormatOpenAIChat,
			BaseURL:         server.URL,
			APIKeys:         []string{"test-key"},
			Model:           "test-model",
			MaxTokens:       256,
			Workspace:       t.TempDir(),
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

// TestRunChatBackfillsEmptyReasoningPlaceholder: with "auto" effort and a model
// that produced no reasoning at all, the tool-turn assistant message still
// carries an explicitly empty reasoning field — presence is what DeepSeek/Kimi
// validate, and a history restored from disk has nothing else to send. Nothing
// is invented beyond the placeholder.
func TestRunChatBackfillsEmptyReasoningPlaceholder(t *testing.T) {
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
	if !assistantToolCallHasReasoningField(t, bodies[1], "call_calc_1") {
		t.Fatal("expected the empty reasoning_content placeholder on the tool-turn assistant message")
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

func TestPatchReasoningContentFields(t *testing.T) {
	// Message 0: user message
	// Message 1: historical assistant (gains the empty placeholder too: the
	//            provider validates the field per assistant message)
	// Message 2: active tool-call assistant (lacks reasoning; gains reasoning_content: "")
	// Message 3: tool result message for the active call
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"plain history"},{"role":"assistant","content":"","tool_calls":[{"id":"c1"}]},{"role":"tool","tool_call_id":"c1","content":"ok"}]}`)
	// Thinking-mode replay is on (the adapter passes the resolved wire key);
	// an empty key would mean replay is off and nothing may be backfilled.
	patched, changed := patchChatRequestFields(body, defaultReasoningTag, nil, "", false)
	if !changed {
		t.Fatal("expected the assistant messages to gain reasoning_content")
	}
	var payload struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatalf("unmarshal patched body: %v", err)
	}
	// Every assistant message carries the placeholder: a history restored from
	// disk keeps no reasoning text, and DeepSeek rejects a thinking-mode request
	// whose assistant messages omit the field.
	for _, idx := range []int{1, 2} {
		if got := string(payload.Messages[idx]["reasoning_content"]); got != `""` {
			t.Fatalf("assistant message %d reasoning_content = %s, want \"\"", idx, got)
		}
	}
	if _, exists := payload.Messages[0]["reasoning_content"]; exists {
		t.Fatal("user message must not gain reasoning_content")
	}
	if _, exists := payload.Messages[3]["reasoning_content"]; exists {
		t.Fatal("tool message must not gain reasoning_content")
	}

	// Dialect test: when the configured dialect is "reasoning" (vLLM style),
	// the empty field must be written under that dialect.
	vllmBody := []byte(`{"model":"m","messages":[{"role":"user","content":"q"},{"role":"assistant","content":"","tool_calls":[{"id":"c2"}]},{"role":"tool","tool_call_id":"c2","content":"ok"}]}`)
	vllmPatched, changed := patchChatRequestFields(vllmBody, "reasoning", nil, "", false)
	if !changed {
		t.Fatal("expected the assistant message to be patched under the configured dialect")
	}
	var vllmPayload struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(vllmPatched, &vllmPayload); err != nil {
		t.Fatalf("unmarshal vllm patched body: %v", err)
	}
	if got := string(vllmPayload.Messages[1]["reasoning"]); got != `""` {
		t.Fatalf("assistant reasoning = %s, want \"\"", got)
	}
	if _, exists := vllmPayload.Messages[1]["reasoning_content"]; exists {
		t.Fatal("the vLLM dialect must not inject reasoning_content")
	}
}

// TestPatchReasoningContentFieldsCoversRestoredHistory pins the restart case:
// the session was loaded from disk (reasoning was never persisted), the tool turn
// is old and complete, and the assistant messages still need the field —
// otherwise DeepSeek answers 400 with "the reasoning_content in the thinking mode
// must be passed back to the API".
func TestPatchReasoningContentFieldsCoversRestoredHistory(t *testing.T) {
	body := []byte(`{"model":"m","messages":[` +
		`{"role":"user","content":"q1"},` +
		`{"role":"assistant","content":"","tool_calls":[{"id":"c1"}]},` +
		`{"role":"tool","tool_call_id":"c1","content":"ok"},` +
		`{"role":"assistant","content":"answer"},` +
		`{"role":"assistant","content":"spoken","reasoning_content":"real trace"},` +
		`{"role":"user","content":"q2"}]}`)
	patched, changed := patchChatRequestFields(body, defaultReasoningTag, nil, "", false)
	if !changed {
		t.Fatal("a restored history must gain the missing reasoning fields")
	}
	var payload struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatalf("unmarshal patched body: %v", err)
	}
	for _, idx := range []int{1, 3} {
		if got := string(payload.Messages[idx]["reasoning_content"]); got != `""` {
			t.Fatalf("assistant message %d reasoning_content = %s, want \"\"", idx, got)
		}
	}
	if got := string(payload.Messages[4]["reasoning_content"]); got != `"real trace"` {
		t.Fatalf("an existing reasoning trace must survive untouched, got %s", got)
	}
	if _, exists := payload.Messages[5]["reasoning_content"]; exists {
		t.Fatal("user message must not gain reasoning_content")
	}
}

// TestPatchChatRequestFieldsAddsStopThinkingField: the "off" level reaches a
// compatible Chat endpoint as thinking:{"type":"disabled"} — the field DeepSeek
// documents and passes through extra_body in their own sample, because the typed
// OpenAI Chat schema has no such field. It is a top-level parameter, so the
// message prefix stays untouched, it is written once, and it never overwrites a
// field the caller already set.
func TestPatchChatRequestFieldsAddsStopThinkingField(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"q"}]}`)
	patched, changed := patchChatRequestFields(body, defaultReasoningTag, nil, "", true)
	if !changed {
		t.Fatal("expected the body to gain the stop-thinking field")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatalf("unmarshal patched body: %v", err)
	}
	if got := string(payload["thinking"]); got != `{"type":"disabled"}` {
		t.Fatalf("thinking = %s, want {\"type\":\"disabled\"}", got)
	}
	if strings.Contains(string(patched), "reasoning_content") {
		t.Fatalf("the stop-thinking field must not drag a reasoning placeholder along: %s", patched)
	}

	// Idempotent: presence is what the provider validates, so the second pass
	// must leave the body byte-identical.
	if again, changed := patchChatRequestFields(patched, defaultReasoningTag, nil, "", true); changed {
		t.Fatalf("second patch must be a no-op, got %s", again)
	}

	// A field the caller already set wins.
	explicit := []byte(`{"model":"m","messages":[],"thinking":{"type":"enabled"}}`)
	kept, _ := patchChatRequestFields(explicit, defaultReasoningTag, nil, "", true)
	if !strings.Contains(string(kept), `"thinking":{"type":"enabled"}`) {
		t.Fatalf("an existing thinking field must survive: %s", kept)
	}
}

// TestChatReasoningBackfillKey: the placeholder is written for every endpoint
// except the official OpenAI API, so a session restored from disk (which carries
// no reasoning text at all) still satisfies the providers that validate the
// field on every assistant message. The dialect follows the configured one, and
// asking the provider to stop thinking opts out of the field entirely.
func TestChatReasoningBackfillKey(t *testing.T) {
	if got := chatReasoningBackfillKey(ConfigState{BaseURL: openAIOfficialAPIBaseURL}); got != "" {
		t.Fatalf("official OpenAI endpoint: key = %q, want empty", got)
	}
	compatible := ConfigState{BaseURL: "https://api.deepseek.com"}
	if got := chatReasoningBackfillKey(compatible); got != defaultReasoningTag {
		t.Fatalf("compatible endpoint: key = %q, want %q", got, defaultReasoningTag)
	}
	compatible.ReasoningTag = "reasoning"
	if got := chatReasoningBackfillKey(compatible); got != "reasoning" {
		t.Fatalf("the configured dialect must win over the default, got %q", got)
	}
	compatible.ReasoningTag = "sink"
	if got := chatReasoningBackfillKey(compatible); got != defaultReasoningTag {
		t.Fatalf("a banner tag must fall back to the default dialect, got %q", got)
	}
	// 关闭思考: the request asks the provider to stop thinking, so there is no
	// reasoning to hand back — no assistant message may gain the field. An alias
	// of the level resolves the same way.
	compatible.ReasoningTag = defaultReasoningTag
	compatible.ReasoningEffort = reasoningEffortOff
	if got := chatReasoningBackfillKey(compatible); got != "" {
		t.Fatalf("the off level must not backfill a reasoning field, got %q", got)
	}
	compatible.ReasoningEffort = "disabled"
	if got := chatReasoningBackfillKey(compatible); got != "" {
		t.Fatalf("an off alias must not backfill a reasoning field, got %q", got)
	}
}

// ── Anthropic: thinking block + signature replay ────────────────────────────

func TestAnthropicThinkingBlockParams(t *testing.T) {
	blocks := []anthropicThinkingBlock{
		{Thinking: "step one", Signature: "sig_1"},
		{Data: "redacted-bytes"},
	}
	params := anthropicThinkingBlockParams(blocks, "claude-3-7-sonnet")
	if len(params) != 2 {
		t.Fatalf("expected thinking + redacted blocks, got %d", len(params))
	}
	if params[0].OfThinking == nil || params[0].OfThinking.Thinking != "step one" || params[0].OfThinking.Signature != "sig_1" {
		t.Fatalf("block 0 must be the thinking block with signature, got %+v", params[0])
	}
	if params[1].OfRedactedThinking == nil || params[1].OfRedactedThinking.Data != "redacted-bytes" {
		t.Fatalf("block 1 must be the redacted thinking block, got %+v", params[1])
	}
	// Claude unsigned thinking guard: an unsigned thinking block must NOT be
	// replayed on Claude models (the official API rejects it with a 400), but
	// is permitted on non-Claude Anthropic-compatible proxies.
	unsigned := []anthropicThinkingBlock{{Thinking: "unsigned thoughts"}}
	if got := anthropicThinkingBlockParams(unsigned, "claude-3-7-sonnet"); len(got) != 0 {
		t.Fatalf("Claude model must not replay unsigned thinking blocks, got %d", len(got))
	}
	if got := anthropicThinkingBlockParams(unsigned, "deepseek-r1"); len(got) != 1 || got[0].OfThinking == nil {
		t.Fatalf("non-Claude model must allow replaying unsigned thinking blocks, got %d", len(got))
	}
}

// TestBuildAnthropicMessagesReplaysThinkingPerTurn pins the prefix-stability
// contract: EVERY tool-loop turn keeps its captured thinking blocks in every
// later request, not just the trailing one. A payload that decorates only the
// trailing turn is dropped from that (now older) message in the next request —
// the prefix bytes change and the provider prompt cache is lost from that
// message onward, re-billing the recent tool output on every agent step.
func TestBuildAnthropicMessagesReplaysThinkingPerTurn(t *testing.T) {
	replay := &sessionReasoningPayload{turns: []reasoningTurn{
		{callIDs: []string{"t1"}, anthropic: []anthropicThinkingBlock{{Thinking: "step one", Signature: "sig_1"}}},
		{callIDs: []string{"t2"}, anthropic: []anthropicThinkingBlock{{Thinking: "step two", Signature: "sig_2"}}},
	}}
	turn1 := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "question"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{ID: "t1", Function: legacyopenai.FunctionCall{Name: "grep", Arguments: "{}"}}}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "t1", Content: "ok"},
	}
	turn2 := append(append([]legacyopenai.ChatCompletionMessage{}, turn1...),
		legacyopenai.ChatCompletionMessage{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{ID: "t2", Function: legacyopenai.FunctionCall{Name: "read", Arguments: "{}"}}}},
		legacyopenai.ChatCompletionMessage{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "t2", Content: "data"},
	)

	_, step1 := buildAnthropicMessages(turn1, replay, "claude-3-7-sonnet")
	if len(step1) != 3 {
		t.Fatalf("step 1 messages = %d, want 3", len(step1))
	}
	if blocks := step1[1].Content; len(blocks) != 2 || blocks[0].OfThinking == nil || blocks[0].OfThinking.Thinking != "step one" || blocks[1].OfToolUse == nil {
		t.Fatalf("step 1 assistant blocks = %+v, want thinking + tool_use", blocks)
	}

	_, step2 := buildAnthropicMessages(turn2, replay, "claude-3-7-sonnet")
	if len(step2) != 5 {
		t.Fatalf("step 2 messages = %d, want 5", len(step2))
	}
	// The step-1 request must be a byte-identical prefix of the step-2
	// request: that is exactly what the provider prompt cache hashes.
	firstStep1, err := json.Marshal(step1)
	if err != nil {
		t.Fatalf("marshal step 1: %v", err)
	}
	firstStep2, err := json.Marshal(step2[:3])
	if err != nil {
		t.Fatalf("marshal step 2 prefix: %v", err)
	}
	if string(firstStep1) != string(firstStep2) {
		t.Fatalf("the step-1 prefix must be byte-identical in the step-2 request:\n%s\n%s", firstStep1, firstStep2)
	}
	if blocks := step2[3].Content; len(blocks) != 2 || blocks[0].OfThinking == nil || blocks[0].OfThinking.Thinking != "step two" {
		t.Fatalf("step 2 assistant blocks = %+v, want the turn's own thinking blocks", blocks)
	}

	// A turn without a ledger entry (no thinking captured, another model's
	// payload, or an unmatched id) simply emits none — on every request.
	_, bare := buildAnthropicMessages(turn2, nil, "claude-3-7-sonnet")
	if blocks := bare[1].Content; len(blocks) != 1 || blocks[0].OfToolUse == nil {
		t.Fatalf("a turn without a ledger entry must emit its tool_use only, got %+v", blocks)
	}
}

// TestBuildOpenAIResponsesInputReplaysReasoningPerTurn pins the same
// prefix-stability contract for the Responses wire: every captured reasoning
// item stays right before its own turn's function_call outputs in every later
// request, so the input prefix never sheds replay decorations.
func TestBuildOpenAIResponsesInputReplaysReasoningPerTurn(t *testing.T) {
	replay := &sessionReasoningPayload{turns: []reasoningTurn{
		{
			callIDs:        []string{"call_1"},
			responses:      []responsesReasoningItem{{ID: "rs_1", EncryptedContent: "enc-1", SummaryTexts: []string{"thoughts"}}},
			responsesItems: map[string]string{"call_1": "fc_item_1"},
		},
		{
			callIDs:        []string{"call_2"},
			responses:      []responsesReasoningItem{{ID: "rs_2", EncryptedContent: "enc-2"}},
			responsesItems: map[string]string{"call_2": "fc_item_2"},
		},
	}}
	turn1 := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "question"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{ID: "call_1", Function: legacyopenai.FunctionCall{Name: "calculate", Arguments: "{}"}}}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "call_1", Content: "2"},
	}
	turn2 := append(append([]legacyopenai.ChatCompletionMessage{}, turn1...),
		legacyopenai.ChatCompletionMessage{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{ID: "call_2", Function: legacyopenai.FunctionCall{Name: "read", Arguments: "{}"}}}},
		legacyopenai.ChatCompletionMessage{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "call_2", Content: "data"},
	)

	_, step1 := buildOpenAIResponsesInput(turn1, replay)
	// user -> reasoning -> function_call -> function_call output
	if len(step1) != 4 || step1[1].OfReasoning == nil || step1[1].OfReasoning.ID != "rs_1" {
		t.Fatalf("step 1 input = %+v, want the reasoning item before its function_call", step1)
	}
	_, step2 := buildOpenAIResponsesInput(turn2, replay)
	if len(step2) != 7 {
		t.Fatalf("step 2 input = %d items, want 7", len(step2))
	}
	firstStep1, err := json.Marshal(step1)
	if err != nil {
		t.Fatalf("marshal step 1: %v", err)
	}
	firstStep2, err := json.Marshal(step2[:4])
	if err != nil {
		t.Fatalf("marshal step 2 prefix: %v", err)
	}
	if string(firstStep1) != string(firstStep2) {
		t.Fatalf("the step-1 input prefix must be byte-identical in the step-2 request:\n%s\n%s", firstStep1, firstStep2)
	}
}

// TestResponsesReasoningItemKeepsEveryField covers the faithful-replay contract:
// the item sent back must be a copy of the one the API produced, so every
// documented field survives capture -> carrier -> request. pi stores the whole
// item as JSON for exactly that reason (openai-responses-shared.ts:222/689-690).
func TestResponsesReasoningItemKeepsEveryField(t *testing.T) {
	var item oaresp.ResponseReasoningItem
	if err := json.Unmarshal([]byte(`{"type":"reasoning","id":"rs_9","status":"completed","encrypted_content":"enc-9","summary":[{"type":"summary_text","text":"part one"},{"type":"summary_text","text":"part two"}],"content":[{"type":"reasoning_text","text":"raw chain"}]}`), &item); err != nil {
		t.Fatalf("unmarshal reasoning item: %v", err)
	}
	captured, ok := captureResponsesReasoningItem(item)
	if !ok {
		t.Fatal("a reasoning item with a usable id must be captured")
	}
	if captured.Status != "completed" || captured.EncryptedContent != "enc-9" ||
		len(captured.SummaryTexts) != 2 || captured.SummaryTexts[1] != "part two" || len(captured.ContentTexts) != 1 {
		t.Fatalf("captured = %+v, want every documented field", captured)
	}

	ledger := &sessionReasoningPayload{turns: []reasoningTurn{{
		callIDs:   []string{"call_9"},
		responses: []responsesReasoningItem{captured},
	}}}
	if turn := ledger.turnForAny([]string{"call_9"}); turn == nil || len(turn.responses) != 1 || len(turn.responses[0].SummaryTexts) != 2 || turn.responses[0].Status != "completed" {
		t.Fatalf("ledger turn = %+v, want the captured item", turn)
	}

	input := responsesReasoningInputItem(ledger.turns[0].responses[0])
	if input.OfReasoning == nil {
		t.Fatal("expected a reasoning input item")
	}
	if input.OfReasoning.ID != "rs_9" || input.OfReasoning.EncryptedContent.Value != "enc-9" ||
		len(input.OfReasoning.Summary) != 2 || len(input.OfReasoning.Content) != 1 ||
		string(input.OfReasoning.Status) != "completed" {
		t.Fatalf("replayed item = %+v, want every field preserved", input.OfReasoning)
	}
}

// TestResponsesReasoningItemWithoutUsableIDDropped: the API resolves a replayed
// reasoning item by id, so an item a relay returned without one must never be
// sent back. Dropping it keeps the request valid — the follower function_call
// carries its own id, and only a reasoning item requires its follower.
func TestResponsesReasoningItemWithoutUsableIDDropped(t *testing.T) {
	var item oaresp.ResponseReasoningItem
	if err := json.Unmarshal([]byte(`{"type":"reasoning","id":"","summary":[]}`), &item); err != nil {
		t.Fatalf("unmarshal reasoning item: %v", err)
	}
	if _, ok := captureResponsesReasoningItem(item); ok {
		t.Fatal("a reasoning item without a usable id must not be captured")
	}
}

// TestReasoningReplayKeyScopesModelAndProtocol guards cross-model replay:
// signatures and encrypted reasoning are only valid for the model that emitted
// them, so a model switch must start a fresh payload.
func TestReasoningReplayKeyScopesModelAndProtocol(t *testing.T) {
	base := ConfigState{APIFormat: apiFormatOpenAIResponses, Model: "gpt-5.6", responsesPromptCacheKey: "sess"}
	same := ConfigState{APIFormat: apiFormatOpenAIResponses, Model: "GPT-5.6", responsesPromptCacheKey: "sess"}
	if reasoningReplayKey(base, base.Model) != reasoningReplayKey(same, same.Model) {
		t.Fatal("model comparison must be case-insensitive")
	}
	others := map[string]ConfigState{
		"model":    {APIFormat: apiFormatOpenAIResponses, Model: "gpt-5.1", responsesPromptCacheKey: "sess"},
		"protocol": {APIFormat: apiFormatAnthropicMessages, Model: "gpt-5.6", responsesPromptCacheKey: "sess"},
		"session":  {APIFormat: apiFormatOpenAIResponses, Model: "gpt-5.6", responsesPromptCacheKey: "other"},
	}
	for name, cfg := range others {
		if reasoningReplayKey(base, base.Model) == reasoningReplayKey(cfg, cfg.Model) {
			t.Fatalf("replay key must differ when the %s changes", name)
		}
	}
}

// TestPatchChatRequestFieldsAddsContent covers the missing-key bug: go-openai
// tags content omitempty, so a pure tool-call assistant message and an empty
// tool result used to drop the key entirely (kimi-code emits content: null for
// the same reason). The captured reasoning_details ride on the same message.
func TestPatchChatRequestFieldsAddsContent(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"model": "m",
		"messages": []map[string]any{
			{"role": "user", "content": "q"},
			{"role": "assistant", "tool_calls": []map[string]any{{"id": "c1"}}},
			{"role": "tool", "tool_call_id": "c1"},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	details, err := json.Marshal([]map[string]any{{"type": "reasoning.text", "text": "why"}})
	if err != nil {
		t.Fatalf("marshal details: %v", err)
	}
	patched, changed := patchChatRequestFields(body, "", map[string]json.RawMessage{"c1": details}, "", false)
	if !changed {
		t.Fatal("expected the tool-call and tool messages to be patched")
	}
	var payload struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatalf("unmarshal patched body: %v", err)
	}
	for _, idx := range []int{1, 2} {
		got, exists := payload.Messages[idx]["content"]
		if !exists {
			t.Fatalf("message %d must carry an explicit content key: %+v", idx, payload.Messages[idx])
		}
		if len(got) != 2 || got[0] != '"' || got[1] != '"' {
			t.Fatalf("message %d content = %s, want an explicit empty JSON string", idx, got)
		}
	}
	if got, exists := payload.Messages[1]["reasoning_details"]; !exists || string(got) != string(details) {
		t.Fatalf("reasoning_details = %s (present=%v), want %s", got, exists, details)
	}
	if _, exists := payload.Messages[1]["reasoning_content"]; exists {
		t.Fatal("a message replayed with reasoning_details must not also carry reasoning_content")
	}

	// A history without a matching tool-call assistant message gets no
	// reasoning fields at all: the ledger only ever attaches to the turn it
	// was captured with.
	noTurn, err := json.Marshal(map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "q"}, {"role": "assistant", "content": "a"}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, changed := patchChatRequestFields(noTurn, "", map[string]json.RawMessage{"c1": details}, "", false); changed {
		t.Fatal("a history without a matching tool turn must not receive reasoning fields")
	}
}

// TestPatchChatRequestFieldsDetailsPerTurn pins the Chat wire's share of the
// prefix-stability contract: each turn's reasoning_details attach to that
// turn's own assistant message in every request, so a patched two-turn body
// keeps the first turn's messages byte-identical to the one-turn body.
func TestPatchChatRequestFieldsDetailsPerTurn(t *testing.T) {
	marshal := func(v any) []byte {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		return raw
	}
	details := marshal([]map[string]any{{"type": "reasoning.text", "text": "why"}})
	turn1 := map[string]any{
		"model": "m",
		"messages": []map[string]any{
			{"role": "user", "content": "q"},
			{"role": "assistant", "tool_calls": []map[string]any{{"id": "c1"}}},
			{"role": "tool", "tool_call_id": "c1"},
		},
	}
	turn2Messages := append(append([]map[string]any{}, turn1["messages"].([]map[string]any)...),
		map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"id": "c2"}}},
		map[string]any{"role": "tool", "tool_call_id": "c2"},
	)
	turn2 := map[string]any{"model": "m", "messages": turn2Messages}

	patched1, _ := patchChatRequestFields(marshal(turn1), "", map[string]json.RawMessage{"c1": details}, "", false)
	patched2, _ := patchChatRequestFields(marshal(turn2), "", map[string]json.RawMessage{"c1": details, "c2": details}, "", false)
	var payload1, payload2 struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(patched1, &payload1); err != nil {
		t.Fatalf("unmarshal patched body 1: %v", err)
	}
	if err := json.Unmarshal(patched2, &payload2); err != nil {
		t.Fatalf("unmarshal patched body 2: %v", err)
	}
	if len(payload1.Messages) != 3 || len(payload2.Messages) != 5 {
		t.Fatalf("messages = %d / %d, want 3 / 5", len(payload1.Messages), len(payload2.Messages))
	}
	for i := range payload1.Messages {
		if string(payload1.Messages[i]) != string(payload2.Messages[i]) {
			t.Fatalf("message %d must be byte-identical across steps:\n%s\n%s", i, payload1.Messages[i], payload2.Messages[i])
		}
	}
	if !bytes.Contains(payload2.Messages[3], []byte("reasoning_details")) {
		t.Fatalf("the second turn's message must carry its own details: %s", payload2.Messages[3])
	}
}

// TestBuildAnthropicMessagesThinkingRequiresMatchingTurn: the per-turn gate
// replaces the old trailing-turn gate — a payload only ever lands on the
// assistant turn whose call ids it was captured with, so a stale or foreign
// payload cannot be glued to an unrelated message.
func TestBuildAnthropicMessagesThinkingRequiresMatchingTurn(t *testing.T) {
	replay := &sessionReasoningPayload{turns: []reasoningTurn{
		{callIDs: []string{"other"}, anthropic: []anthropicThinkingBlock{{Thinking: "step one", Signature: "sig_1"}}},
	}}
	messages := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "question"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{ID: "t1", Function: legacyopenai.FunctionCall{Name: "grep", Arguments: "{}"}}}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "t1", Content: "ok"},
	}
	_, converted := buildAnthropicMessages(messages, replay, "claude-3-7-sonnet")
	if blocks := converted[1].Content; len(blocks) != 1 || blocks[0].OfToolUse == nil {
		t.Fatalf("a payload captured for another turn must not be replayed, got %+v", blocks)
	}
	// A plain assistant answer (no tool calls) never matches a ledger turn.
	answer := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "first"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "answer", ReasoningContent: "thoughts"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "second"},
	}
	_, convertedAnswer := buildAnthropicMessages(answer, replay, "claude-3-7-sonnet")
	if blocks := convertedAnswer[1].Content; len(blocks) != 1 || blocks[0].OfText == nil {
		t.Fatalf("a non-tool assistant message must not gain thinking blocks, got %+v", blocks)
	}
}

// TestReasoningStashEvictionKeepsOtherEntries guards the eviction policy: with
// keys scoped per session x protocol x model (plus one per subagent run) a full
// map is a normal state, so a new entry may only drop the coldest one — never the
// live payload of another session or of a concurrent tool loop.
func TestReasoningStashEvictionKeepsOtherEntries(t *testing.T) {
	stash := newReasoningStash()
	turn := func() reasoningTurn {
		return reasoningTurn{callIDs: []string{"c"}, chatDetails: json.RawMessage("[]")}
	}
	for i := 0; i < maxReasoningStashEntries; i++ {
		stash.appendTurn(fmt.Sprintf("session-%d", i), turn())
	}
	for i := 0; i < maxReasoningStashEntries; i++ {
		if stash.get(fmt.Sprintf("session-%d", i)) == nil {
			t.Fatalf("session-%d was evicted while filling the map", i)
		}
	}
	stash.appendTurn("overflow", turn())
	stash.mu.Lock()
	remaining := len(stash.entries)
	stash.mu.Unlock()
	if remaining != maxReasoningStashEntries {
		t.Fatalf("stash holds %d entries, want the cap %d (exactly one eviction)", remaining, maxReasoningStashEntries)
	}
	if stash.get("session-0") != nil {
		t.Fatal("the coldest entry must be the one evicted")
	}
	if stash.get("session-1") == nil {
		t.Fatal("warm entries must survive an eviction")
	}
}

// TestReasoningStashClearSession drops every protocol/model payload of one
// session without touching another session's live payload. Keys are built
// through the production derivation on purpose: clearSession only has the raw
// session id while the write path keys on the hashed prompt-cache scope, and the
// two must agree. They used to be derived separately (raw id vs hash), which made
// this cleanup a silent no-op that kept dead turns' signatures replayed onto
// later turns and left their entries occupying the LRU.
func TestReasoningStashClearSession(t *testing.T) {
	scope := func(sessionID string) string { return openAIResponsesPromptCacheKey(sessionID) }
	key := func(sessionID, format, model string) string {
		return reasoningReplayKey(ConfigState{
			APIFormat:               format,
			Model:                   model,
			responsesPromptCacheKey: scope(sessionID),
		}, model)
	}
	stash := newReasoningStash()
	chatKey := key("sess", apiFormatOpenAIChat, "model-a")
	responsesKey := key("sess", apiFormatOpenAIResponses, "model-b")
	otherKey := key("sess-other", apiFormatOpenAIChat, "model-a")
	if chatKey == otherKey {
		t.Fatalf("the two sessions must not share a key: %q", chatKey)
	}
	stash.appendTurn(chatKey, reasoningTurn{callIDs: []string{"c1"}, chatDetails: json.RawMessage("[]")})
	stash.appendTurn(responsesKey, reasoningTurn{callIDs: []string{"c2"}, responses: []responsesReasoningItem{{ID: "rs_1"}}})
	stash.appendTurn(otherKey, reasoningTurn{callIDs: []string{"c3"}, chatDetails: json.RawMessage("[]")})
	stash.clearSession("sess")
	if got := stash.get(chatKey); got != nil {
		t.Fatalf("the chat payload must be dropped, got %+v", got)
	}
	if got := stash.get(responsesKey); got != nil {
		t.Fatalf("the responses payload must be dropped, got %+v", got)
	}
	if stash.get(otherKey) == nil {
		t.Fatal("another session must keep its payload")
	}
}

// TestReasoningReplayKeyIsolatesStoredPayloads makes the scoping behavioural: a
// payload stored for one model must not be readable for another, and the key must
// follow the model that actually issues the request.
func TestReasoningReplayKeyIsolatesStoredPayloads(t *testing.T) {
	stash := newReasoningStash()
	cfgA := ConfigState{APIFormat: apiFormatOpenAIResponses, Model: "gpt-5.6", responsesPromptCacheKey: "sess"}
	keyA := reasoningReplayKey(cfgA, cfgA.Model)
	stash.appendTurn(keyA, reasoningTurn{callIDs: []string{"c1"}, responses: []responsesReasoningItem{{ID: "rs_1"}}})
	cfgB := ConfigState{APIFormat: apiFormatOpenAIResponses, Model: "gpt-5.1", responsesPromptCacheKey: "sess"}
	if got := stash.get(reasoningReplayKey(cfgB, cfgB.Model)); got != nil {
		t.Fatalf("a model switch must not expose the previous payload: %+v", got)
	}
	if got := stash.get(keyA); got == nil || got.turnForAny([]string{"c1"}) == nil || len(got.turns) != 1 {
		t.Fatalf("the original payload must stay reachable: %+v", got)
	}
	if reasoningReplayKey(cfgA, "other-model") == keyA {
		t.Fatal("the effective model must be part of the key")
	}
	cfgOtherFormat := ConfigState{APIFormat: apiFormatAnthropicMessages, Model: "gpt-5.6", responsesPromptCacheKey: "sess"}
	if reasoningReplayKey(cfgA, cfgA.Model) == reasoningReplayKey(cfgOtherFormat, cfgOtherFormat.Model) {
		t.Fatal("the wire protocol must be part of the key")
	}
}

// TestReasoningStashAppendTurnAccumulates: the stash is a per-turn ledger —
// turns accumulate within one key, a re-captured turn replaces its entry
// instead of stacking, and an empty or unidentified turn is ignored.
func TestReasoningStashAppendTurnAccumulates(t *testing.T) {
	stash := newReasoningStash()
	stash.appendTurn("k", reasoningTurn{callIDs: []string{"c1"}, anthropic: []anthropicThinkingBlock{{Thinking: "one", Signature: "s1"}}})
	stash.appendTurn("k", reasoningTurn{callIDs: []string{"c2"}, anthropic: []anthropicThinkingBlock{{Thinking: "two", Signature: "s2"}}})
	payload := stash.get("k")
	if payload == nil || len(payload.turns) != 2 {
		t.Fatalf("turns = %+v, want two accumulated turns", payload)
	}
	// Re-capturing the same turn replaces it.
	stash.appendTurn("k", reasoningTurn{callIDs: []string{"c1"}, anthropic: []anthropicThinkingBlock{{Thinking: "one again", Signature: "s1"}}})
	if payload = stash.get("k"); len(payload.turns) != 2 || payload.turnForAny([]string{"c1"}).anthropic[0].Thinking != "one again" {
		t.Fatalf("re-captured turn must replace its entry: %+v", payload.turns)
	}
	// Empty turns and turns without identity are ignored.
	stash.appendTurn("k", reasoningTurn{callIDs: []string{"c3"}})
	stash.appendTurn("k", reasoningTurn{anthropic: []anthropicThinkingBlock{{Thinking: "x", Signature: "s"}}})
	if payload = stash.get("k"); len(payload.turns) != 2 {
		t.Fatalf("empty or unidentified turns must be ignored: %+v", payload.turns)
	}
}

// TestParseStreamReasoningSupportsReasoningText locks the dialect that was
// missing: newer vLLM / llama.cpp-class servers stream `reasoning_text`, which
// pi supports too (openai-completions.ts:280 lists reasoning,
// reasoning_content, reasoning_text).
func TestParseStreamReasoningSupportsReasoningText(t *testing.T) {
	text, details := parseStreamReasoning([]byte(`{"choices":[{"delta":{"reasoning_text":"thinking..."}}]}`))
	if text != "thinking..." {
		t.Fatalf("reasoning_text must be parsed, got %q", text)
	}
	if len(details) != 0 {
		t.Fatalf("no reasoning details expected, got %#v", details)
	}

	// Field precedence follows pi: the first non-empty field wins, so
	// `reasoning` beats `reasoning_text`.
	if text, _ := parseStreamReasoning([]byte(`{"choices":[{"delta":{"reasoning":"first","reasoning_text":"second"}}]}`)); text != "first" {
		t.Fatalf("reasoning must win over reasoning_text, got %q", text)
	}

	// A chunk that only mentions an unrelated parameter must not enter the parse
	// path: the quoted-key check exists to keep "reasoning_effort" out.
	if text, _ := parseStreamReasoning([]byte(`{"reasoning_effort":"high"}`)); text != "" {
		t.Fatalf("a non-reasoning chunk must not be parsed, got %q", text)
	}

	// The same key list drives replay-dialect selection, so a model configured
	// with tag=reasoning_text replays under that field name.
	if !isKnownWireReasoningKey("reasoning_text") {
		t.Fatal("reasoning_text must be a known replay dialect")
	}
}
