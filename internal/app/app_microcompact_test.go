// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General Public
// License v3. See the LICENSE file for details.
package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// microcompactServer answers every request with a plain completion and counts the
// ones that carry the compaction prompt, so a test can tell whether the expensive
// macro summary ran. Compaction requests are answered normally: the assertion is
// about whether they happen at all.
type microcompactServer struct {
	requests    atomic.Int32
	compactions atomic.Int32
}

func (s *microcompactServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.requests.Add(1)
		if strings.Contains(string(body), overflowCompactPromptMarker) {
			s.compactions.Add(1)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("ok"))
		fmt.Fprint(w, sseChatFinishChunk("stop"))
		fmt.Fprint(w, sseDone)
	}
}

// TestMicrocompactionDropsStaleProviderMeasurement pins the invariant behind the
// auto-compaction trigger: a recorded provider measurement describes one specific
// request, and micro-compaction rewrites tool results in place without changing
// the message count that measurement is validated against. If the measurement
// survives the rewrite, the reported total stays pinned to the pre-clearing
// request, so the macro LLM summary still fires even though the context came back
// under the threshold — which is exactly what micro-compaction exists to avoid.
func TestMicrocompactionDropsStaleProviderMeasurement(t *testing.T) {
	server := &microcompactServer{}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()

	sessionID := "microcompact-stale-measurement"
	// Older huge tool results (these get cleared) followed by four small ones
	// (kept), matching defaultMicrocompactKeepRecentToolResults.
	history := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "start"}}
	appendTurn := func(id string, output string) {
		history = append(history,
			openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleAssistant,
				ToolCalls: []openai.ToolCall{{
					ID:       id,
					Type:     openai.ToolTypeFunction,
					Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"x.txt"}`},
				}},
			},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: id, Content: output},
		)
	}
	for i := 0; i < 3; i++ {
		appendTurn(fmt.Sprintf("old-%d", i), strings.Repeat("huge tool output line\n", 1000))
	}
	for i := 0; i < defaultMicrocompactKeepRecentToolResults; i++ {
		appendTurn(fmt.Sprintf("recent-%d", i), fmt.Sprintf("recent output %d", i))
	}

	app := NewApp()
	app.initialized = true // skip the disk bootstrap
	app.stats = nil        // skip token-stat persistence
	recorder := &compactRecorder{done: make(chan struct{})}
	app.events = recorder
	app.mu.Lock()
	app.histories[sessionID] = history
	app.mu.Unlock()

	cfg := ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   httpServer.URL,
		APIKeys:   []string{"test-key"},
		Model:     "test-model",
		MaxTokens: 64,
		Workspace: t.TempDir(),
	}

	// The threshold has to sit between the post-clearing estimate and the recorded
	// measurement, otherwise the two behaviours are indistinguishable. Both parts
	// are measured with the same helpers the run loop uses.
	cleared, clearedCount := microcompactMessages(history, defaultMicrocompactKeepRecentToolResults)
	if clearedCount == 0 {
		t.Fatalf("fixture must have tool results old enough to clear")
	}
	tools := app.buildToolsForSession(sessionID, cfg)
	prefix, _ := app.sessionPrefixBreakdown(sessionID, cfg, app.listCachedSkills())
	clearedEstimate := prefix + estimateToolSchemaTokens(tools) + computeLiveBreakdown(cleared).Total
	threshold := clearedEstimate + 2000
	cfg.ContextWindow = threshold * 2
	cfg.CompactThreshold = 0.5
	if got := compactThresholdLimit(cfg); got != threshold {
		t.Fatalf("compact threshold fixture = %d, want %d", got, threshold)
	}
	// A provider measurement from an earlier request that covered the full-sized
	// tool results: measured tokens sit above the threshold, the re-estimated
	// context after clearing sits below it.
	measured := threshold + 20000
	app.recordContextAnchor(sessionID, history, &modelUsage{PromptTokens: measured})

	if _, err := app.StartChat(ChatRequest{SessionID: sessionID, Message: "continue", Config: cfg}); err != nil {
		t.Fatalf("StartChat() error = %v", err)
	}
	select {
	case <-recorder.done:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not finish in time")
	}

	if got := recorder.endStatus(); got != "run:done" {
		t.Fatalf("run ended with %q, want run:done (errors: %v)", got, recorder.errorEvents())
	}
	events := recorder.compactedEvents()
	if len(events) == 0 || !strings.Contains(events[0], `"reason":"microcompact"`) {
		t.Fatalf("run:compacted events = %#v, want a microcompact event first", events)
	}
	// The footer token counter drops just as visibly here as it does after the macro
	// summary, and the frontend notice stays silent unless the event reports both
	// sides of the delta — so the pair is part of the event contract.
	if !strings.Contains(events[0], `"tokensBefore":`) || !strings.Contains(events[0], `"tokensAfter":`) {
		t.Fatalf("run:compacted event must carry tokensBefore/tokensAfter, got %s", events[0])
	}
	if got := server.compactions.Load(); got != 0 {
		t.Fatalf("macro summary calls = %d, want 0: micro-compaction brought the context back under the threshold, but the stale provider measurement (measured %d, re-estimated %d, threshold %d) still triggered the summary",
			got, measured, clearedEstimate, threshold)
	}
}
