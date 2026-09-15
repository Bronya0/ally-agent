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

	openai "github.com/sashabaranov/go-openai"
)

// overflowLengthError is the 400 body a provider returns when the request does
// not fit its window. It has to be a typed HTTP error whose text still spells out
// the overflow: classification refines 400s by text, and only
// llmErrorKindContextTooLong triggers the compaction recovery.
const overflowLengthError = `{"error":{"message":"This model's maximum context length is 128000 tokens, however you requested 300000 tokens","type":"invalid_request_error"}}`

const overflowSummaryText = "## Summary\ncompacted history"

// overflowSummaryMarker is the part of the summary that survives JSON encoding in
// a request body (the newline is escaped there), so the fake provider can tell the
// post-compaction retry from the raw over-window history.
const overflowSummaryMarker = "compacted history"

// compactRecorder captures the compaction events and the terminal event of one
// run, so a test can tell "compacted and recovered" from "failed loudly".
type compactRecorder struct {
	mu        sync.Mutex
	compacted []string
	errors    []string
	end       string
	done      chan struct{}
}

func (r *compactRecorder) Emit(name string, payload any) {
	r.mu.Lock()
	switch name {
	case "run:compacted":
		encoded, _ := json.Marshal(payload)
		r.compacted = append(r.compacted, string(encoded))
	case "run:error":
		encoded, _ := json.Marshal(payload)
		r.errors = append(r.errors, string(encoded))
	}
	if (name == "run:done" || name == "run:error") && r.end == "" {
		r.end = name
		close(r.done)
	}
	r.mu.Unlock()
}

func (r *compactRecorder) endStatus() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.end
}

func (r *compactRecorder) compactedEvents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.compacted...)
}

func (r *compactRecorder) errorEvents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errors...)
}

// overflowServer answers the way a provider does when the history no longer fits
// the model window. It decides from the request body instead of a request
// counter: the compaction call carries the compaction prompt, the recovered
// retry carries the summary the compaction produced, and anything else is the
// raw over-window history that gets rejected. That keeps the assertions
// independent of how many times the 400-sanitize path consumes the first
// rejection.
type overflowServer struct {
	compactions atomic.Int32
	rejected    atomic.Int32
	accepted    atomic.Int32
	failSummary bool
}

const overflowCompactPromptMarker = "is being compacted"

func (s *overflowServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		request := string(body)
		switch {
		case strings.Contains(request, overflowCompactPromptMarker):
			s.compactions.Add(1)
			if s.failSummary {
				// The summary call sends the same over-window history to the same
				// model, so in practice it fails for exactly the same reason.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, overflowLengthError)
				return
			}
		case strings.Contains(request, overflowSummaryMarker):
			s.accepted.Add(1)
		default:
			s.rejected.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, overflowLengthError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk(overflowSummaryText))
		fmt.Fprint(w, sseChatFinishChunk("stop"))
		fmt.Fprint(w, sseDone)
	}
}

// runOverflowSession starts one run against the fake provider with a history long
// enough for compaction, and waits for the terminal event.
func runOverflowSession(t *testing.T, baseURL, sessionID string) (*App, *compactRecorder) {
	t.Helper()
	app := NewApp()
	app.initialized = true // skip the disk bootstrap
	app.stats = nil        // skip token-stat persistence
	recorder := &compactRecorder{done: make(chan struct{})}
	app.events = recorder
	app.saveHistory(sessionID, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "first question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "first answer"},
		{Role: openai.ChatMessageRoleUser, Content: "second question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "second answer"},
	})

	if _, err := app.StartChat(ChatRequest{
		SessionID: sessionID,
		Message:   "continue",
		Config: ConfigState{
			APIFormat: apiFormatOpenAIChat,
			BaseURL:   baseURL,
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
	return app, recorder
}

// TestRunChatRecoversFromContextTooLong: a context overflow is a deterministic
// failure — resending the same history is rejected again, so without a recovery
// path the session can never send another message. The loop must compact once
// (ignoring the auto-compaction threshold) and retry the request, like pi's
// overflow recovery.
func TestRunChatRecoversFromContextTooLong(t *testing.T) {
	server := &overflowServer{}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()

	_, recorder := runOverflowSession(t, httpServer.URL, "overflow-recovery")
	if got := recorder.endStatus(); got != "run:done" {
		t.Fatalf("run ended with %q, want run:done (errors: %v)", got, recorder.errorEvents())
	}
	if got := server.compactions.Load(); got != 1 {
		t.Fatalf("compaction calls = %d, want 1", got)
	}
	if got := server.accepted.Load(); got != 1 {
		t.Fatalf("accepted requests = %d, want 1 (the retry that carried the summary)", got)
	}
	if got := server.rejected.Load(); got == 0 {
		t.Fatal("the raw over-window history must be rejected at least once")
	}
	events := recorder.compactedEvents()
	if len(events) != 1 || !strings.Contains(events[0], `"reason":"overflow"`) {
		t.Fatalf("run:compacted events = %#v, want exactly one with reason=overflow", events)
	}
}

// TestRunChatReportsFailedOverflowCompaction: when the summary call fails too (it
// sends the same over-window history to the same model), the user has to be told
// that deleting history is the way out instead of getting the bare provider error.
func TestRunChatReportsFailedOverflowCompaction(t *testing.T) {
	server := &overflowServer{failSummary: true}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()

	_, recorder := runOverflowSession(t, httpServer.URL, "overflow-failed")
	if got := recorder.endStatus(); got != "run:error" {
		t.Fatalf("run ended with %q, want run:error", got)
	}
	if got := server.compactions.Load(); got != 1 {
		t.Fatalf("compaction calls = %d, want 1", got)
	}
	if got := server.accepted.Load(); got != 0 {
		t.Fatalf("accepted requests = %d, want 0", got)
	}
	errors := recorder.errorEvents()
	if len(errors) == 0 || !strings.Contains(errors[0], "压缩失败") {
		t.Fatalf("run:error events = %#v, want the actionable compaction-failure hint", errors)
	}
}
