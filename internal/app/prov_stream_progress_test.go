// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	legacyopenai "github.com/sashabaranov/go-openai"
)

// TestOpenAIChatStreamEmitsToolStartDuringStreaming reproduces the reported
// "tool cards jump straight to completed" regression at the adapter level:
// while tool-call arguments stream in gradually, the frontend must receive
// tool:start (and subsequent tool:update events) BEFORE the stream ends, so
// the running card ("Creating"/"Editing") is visible during argument
// streaming — not only after tool:result lands.
func TestOpenAIChatStreamEmitsToolStartDuringStreaming(t *testing.T) {
	const argsPayload = `{"path":"/tmp/demo.txt","content":"line1\nline2\nline3\n"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		chunk := func(payload string) {
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
		chunk(`{"id":"1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"create","arguments":""}}]}}]}`)
		// Stream the arguments in small deltas with real delays, the way a
		// provider streams tokens. The card must appear mid-stream.
		for i := 0; i < len(argsPayload); i += 8 {
			end := i + 8
			if end > len(argsPayload) {
				end = len(argsPayload)
			}
			time.Sleep(5 * time.Millisecond)
			chunk(fmt.Sprintf(`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}`, argsPayload[i:end]))
		}
		chunk(`{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		chunk(`{"id":"1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   server.URL,
		APIKey:    "test",
		Model:     "test-model",
		MaxTokens:  1024,
	}

	var events []string
	var argsAtStart string
	toolProgress := newToolCallProgressTracker()
	_, err := a.streamModelResponse(context.Background(), cfg, "test-model", nil, nil, func(event modelStreamEvent) {
		if event.ToolCalls != nil {
			for _, ev := range toolProgress.events("run-1", "sess-1", "0", event.ToolCalls, nil) {
				if ev.Name == "tool:start" {
					if s, ok := ev.Payload["args"].(string); ok {
						argsAtStart = s
					}
				}
				events = append(events, ev.Name)
			}
		}
	})
	if err != nil {
		t.Fatalf("streamModelResponse failed: %v", err)
	}
	// After the provider returns, runChat force-flushes the final state.
	for _, ev := range toolProgress.forceEvents("run-1", "sess-1", "0", []legacyopenai.ToolCall{{
		ID:   "call_1",
		Type: legacyopenai.ToolTypeFunction,
		Function: legacyopenai.FunctionCall{
			Name:      "create",
			Arguments: argsPayload,
		},
	}}, nil) {
		events = append(events, ev.Name)
	}

	if len(events) == 0 || events[0] != "tool:start" {
		t.Fatalf("expected first progress event to be tool:start during streaming, got %v", events)
	}
	// The tool:start must be emitted with only partial args (mid-stream), not
	// the full payload — proving it fires while arguments are still streaming.
	if argsAtStart == argsPayload {
		t.Fatalf("tool:start carried the complete arguments (%q); it should arrive mid-stream with partial args", argsAtStart)
	}
	updateCount := 0
	for _, e := range events {
		if e == "tool:update" {
			updateCount++
		}
	}
	if updateCount < 1 {
		t.Fatalf("expected at least one tool:update during streaming, got events %v", events)
	}
}