// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
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

type handoffTestResult struct{ text, stop string }

// Use the real main run and Chat request assembly without provider credentials.
func handoffTestRunner(t *testing.T, a *App) (ConfigState, func(*App, ConfigState, ChatRequest, handoffTestResult) []openai.ChatCompletionMessage) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	requests := make(chan []openai.ChatCompletionMessage, 4)
	results := make(chan handoffTestResult, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []openai.ChatCompletionMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var result handoffTestResult
		select {
		case result = <-results:
		default:
			t.Error("unexpected extra model request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- req.Messages
		text, _ := json.Marshal(result.text)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s,\"reasoning_content\":\"private-reasoning-data\"}}]}\n\n", text)
		if result.stop == "tool_calls" {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"suggest-1","type":"function","function":{"name":"suggest","arguments":"{\"items\":[\"private-suggest-data\"]}"}}]}}]}`+"\n\n")
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\ndata: [DONE]\n\n", result.stop)
	}))
	t.Cleanup(server.Close)
	cfg := a.config.Clone()
	cfg.Model, cfg.BaseURL, cfg.APIKeys, cfg.APIFormat = "handoff-test", server.URL, []string{"test-key"}, apiFormatOpenAIChat
	cfg.DisabledContext = []string{"user_instructions", "user_profile", "memory_index", "workspace_map"}
	return cfg, func(a *App, cfg ConfigState, req ChatRequest, result handoffTestResult) []openai.ChatCompletionMessage {
		t.Helper()
		results <- result
		a.runChat(t.Context(), req.SessionID+"-run", req, cfg)
		want := "done"
		if result.stop == "length" {
			want = "error"
		}
		if got := a.sessionRunOutcomes[req.SessionID]; got.Status != want {
			t.Fatalf("run status = %+v, want %s", got, want)
		}
		select {
		case messages := <-requests:
			return messages
		default:
			t.Fatal("no request reached the mock model")
			return nil
		}
	}
}

func assertNoHandoffText(t *testing.T, messages []openai.ChatCompletionMessage, unwanted ...string) {
	t.Helper()
	for _, m := range messages {
		for _, text := range unwanted {
			if strings.Contains(m.Content, text) {
				t.Fatalf("%q leaked into the request", text)
			}
		}
	}
}

func TestWorkspaceHandoffLatestPair(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	cfg, run := handoffTestRunner(t, a)
	const otherQ, earlierA = "earlier question data", "earlier reply data"
	const question, reply = "最后一条提问", "最终回复：已核对。"
	first := ChatRequest{SessionID: "source", Message: otherQ}
	run(a, cfg, first, handoffTestResult{earlierA, "stop"})
	started := time.Now().UTC().Truncate(time.Second)
	latest := ChatRequest{SessionID: first.SessionID, Message: "base input before queued input"}
	a.runInputs[latest.SessionID+"-run"] = make(chan runInput, 1)
	a.runInputs[latest.SessionID+"-run"] <- runInput{Text: question, Attachments: []AttachmentInput{{Name: "note.txt", Text: "private-attachment-data"}}}
	run(a, cfg, latest, handoffTestResult{reply, "tool_calls"})
	finished := time.Now().UTC()

	// A new app instance must resolve the marker from disk, without needing
	// the source session open or retaining its model/reasoning state in memory.
	b := newAppState(a.AppDataDir())
	b.initialized, b.sessionsDir, b.historiesDir = true, a.sessionsDir, a.historiesDir
	other := cfg.Clone()
	other.Workspace = t.TempDir()
	assertNoHandoffText(t, run(b, other, ChatRequest{SessionID: "other", Message: "different project"}, handoffTestResult{"ok", "stop"}), question, reply)

	current := ChatRequest{SessionID: "destination", Message: "current question"}
	wire := run(b, cfg, current, handoffTestResult{"current reply", "stop"})
	assertNoHandoffText(t, wire, otherQ, earlierA, "private-attachment-data", "private-reasoning-data", "private-suggest-data", latest.Message)
	if len(wire) < 2 || wire[len(wire)-1].Content != current.Message {
		t.Fatal("handoff displaced the current user input")
	}
	count := 0
	for _, m := range wire {
		if !strings.Contains(m.Content, question) {
			continue
		}
		count++
		var record struct {
			CompletedAt time.Time
			Question    string
			FinalReply  string
		}
		_, data, _ := strings.Cut(m.Content, "\n")
		if err := json.Unmarshal([]byte(data), &record); err != nil {
			t.Fatal(err)
		}
		if m.Role != openai.ChatMessageRoleUser || record.Question != question || record.FinalReply != reply || record.CompletedAt.Before(started) || record.CompletedAt.After(finished) {
			t.Fatalf("unexpected handoff role or data: %q %+v", m.Role, record)
		}
	}
	if count != 1 {
		t.Fatalf("handoff count = %d, want 1", count)
	}
	current.Message = "follow-up in existing session"
	assertNoHandoffText(t, run(b, cfg, current, handoffTestResult{"another reply", "stop"}), question, reply)
}

func TestWorkspaceHandoffSkipsUnusableSource(t *testing.T) {
	for _, mode := range []string{"failed", "pending", "too long", "deleted", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			a := newIsolatedChatTestApp(t)
			cfg, run := handoffTestRunner(t, a)
			const question, reply = "source question data", "source reply data"
			source := ChatRequest{SessionID: "source", Message: question}
			run(a, cfg, source, handoffTestResult{reply, "stop"})
			old := cfg.Clone()
			source.Message = "unusable latest input"
			switch mode {
			case "failed":
				run(a, cfg, source, handoffTestResult{"partial reply data", "length"})
			case "pending":
				a.beginWorkspaceHandoff(source, cfg, "unfinished-run", false)
			case "too long":
				run(a, cfg, source, handoffTestResult{strings.Repeat("长", workspaceHandoffTokenLimit+1), "stop"})
			case "deleted":
				if err := a.DeleteSession(source.SessionID); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				cfg.DisabledContext = append(cfg.DisabledContext, workspaceHandoffContext)
			}
			destination := ChatRequest{SessionID: "destination", Message: "new input"}
			wire := run(a, cfg, destination, handoffTestResult{"new reply", "stop"})
			assertNoHandoffText(t, wire, question, reply, source.Message, "partial reply data")
			if mode == "disabled" {
				next := run(a, old, ChatRequest{SessionID: "reenabled", Message: "reenabled input"}, handoffTestResult{"ok", "stop"})
				assertNoHandoffText(t, next, question, reply, destination.Message, "new reply")
			}
		})
	}
}

func TestWorkspaceHandoffOverflowKeepsTurnRetryBudget(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	cfg, run := handoffTestRunner(t, a)
	const previous = "previous question for overflow test"
	run(a, cfg, ChatRequest{SessionID: "source", Message: previous}, handoffTestResult{"prior answer", "stop"})

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if strings.Contains(string(body), previous) != (n == 1) {
			t.Errorf("request %d: handoff must be present only before overflow recovery", n)
		}
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"context length exceeded","code":"context_length_exceeded"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 3 {
			fmt.Fprint(w, sseChatChunk("recovered answer"))
		}
		fmt.Fprint(w, sseChatFinishChunk("stop"), sseDone)
	}))
	defer server.Close()
	cfg.BaseURL, cfg.LLMRetries = server.URL, 1
	req := ChatRequest{SessionID: "destination", Message: "current request"}
	a.runChat(t.Context(), "destination-run", req, cfg)
	if got := a.sessionRunOutcomes[req.SessionID]; got.Status != "done" || requests.Load() != 3 {
		t.Fatalf("dropping the handoff consumed the retry: status=%+v requests=%d", got, requests.Load())
	}
}
