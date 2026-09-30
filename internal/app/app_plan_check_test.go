// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
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

// planCheckServer plays a scripted conversation and records every request body,
// so a test can tell which round the host message entered the conversation.
type planCheckServer struct {
	mu       sync.Mutex
	requests []string
	// script answers one round: it receives the round number (1-based) and the
	// raw request body, and returns the SSE stream to write back.
	script func(round int, request string) string
}

func (s *planCheckServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		request := string(body)
		s.mu.Lock()
		s.requests = append(s.requests, request)
		round := len(s.requests)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, s.script(round, request))
	}
}

func (s *planCheckServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *planCheckServer) requestBody(round int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if round < 1 || round > len(s.requests) {
		return ""
	}
	return s.requests[round-1]
}

// runPlanCheckSession starts one run against the fake provider with a running
// two-step plan already in place, and waits for the terminal event.
func runPlanCheckSession(t *testing.T, baseURL, sessionID, workspace string) (*App, *compactRecorder) {
	t.Helper()
	app := NewApp()
	app.initialized = true // skip the disk bootstrap
	app.stats = nil        // skip token-stat persistence
	recorder := &compactRecorder{done: make(chan struct{})}
	app.events = recorder
	app.plans[sessionID] = []PlanStep{
		{Title: "改造入口", Status: "in_progress"},
		{Title: "补测试", Status: "pending"},
	}

	if _, err := app.StartChat(ChatRequest{
		SessionID: sessionID,
		Message:   "干活",
		Config: ConfigState{
			APIFormat: apiFormatOpenAIChat,
			BaseURL:   baseURL,
			APIKeys:   []string{"test-key"},
			Model:     "test-model",
			MaxTokens: 64,
			Workspace: workspace,
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

// TestToolDidPlanWorkClassifiesByName pins the single predicate the end-of-run
// plan check rests on: work is a file mutation or a command, and everything that
// only reads leaves the plan where it is.
func TestToolDidPlanWorkClassifiesByName(t *testing.T) {
	for _, name := range []string{
		"edit", "Create", "delete",
		"remote_edit", "remote_create_file", "remote_delete_path",
		"command", "Remote_Run_Command",
	} {
		if !toolDidPlanWork(name) {
			t.Fatalf("%q must count as work a plan could have moved past", name)
		}
	}
	for _, name := range []string{"read", "grep", "list_files", "web_fetch", "http_request", "plan", "wait", "ask", "suggest"} {
		if toolDidPlanWork(name) {
			t.Fatalf("%q must not count as work", name)
		}
	}
}

// TestPlanCheckMarkerSpeaksOnlyWhenThePlanLags: the marker exists for a plan that
// still claims unfinished work, and stays silent for an empty plan and one whose
// every step is done — both have nothing to reconcile.
func TestPlanCheckMarkerSpeaksOnlyWhenThePlanLags(t *testing.T) {
	app := NewApp()
	if _, ok := app.planCheckMarker("s-1"); ok {
		t.Fatal("an empty plan has nothing to reconcile")
	}

	app.plans["s-1"] = []PlanStep{{Title: "A", Status: "done"}, {Title: "B", Status: "done"}}
	if _, ok := app.planCheckMarker("s-1"); ok {
		t.Fatal("a plan whose every step is done has nothing to reconcile")
	}

	app.plans["s-1"] = []PlanStep{{Title: "A", Status: "done"}, {Title: "B", Status: "in_progress"}, {Title: "C", Status: "pending"}}
	marker, ok := app.planCheckMarker("s-1")
	if !ok {
		t.Fatal("a running plan must be checkable")
	}
	if marker.Role != openai.ChatMessageRoleUser {
		t.Fatalf("the check must ride a user message, got role %q", marker.Role)
	}
	for _, want := range []string{"<ally-plan-check>", "B", "1 of 3", `{"finish":"<`} {
		if !strings.Contains(marker.Content, want) {
			t.Fatalf("the check must carry %q, got %q", want, marker.Content)
		}
	}

}

// TestRunChatAsksForAPlanReportWhenWorkOutranThePlan is the end-to-end shape of
// the bug this check exists for: the model does the work, stops calling tools,
// and never reports where the plan got to. One host message, once per run, asks
// for the report — the model is the only party that knows which steps it passed.
func TestRunChatAsksForAPlanReportWhenWorkOutranThePlan(t *testing.T) {
	server := &planCheckServer{}
	server.script = func(round int, request string) string {
		switch round {
		case 1:
			// Work, without touching the plan.
			return sseChatToolCallChunk("call_1", "create", `{"path":"note.txt","content":"work"}`) +
				sseChatFinishChunk("tool_calls") + sseDone
		case 2:
			// The model stops calling tools: this is where the check fires, so
			// the message cannot be in this request yet. The body is JSON, so the
			// marker's angle brackets arrive escaped: match on the tag's name.
			if strings.Contains(request, "ally-plan-check") {
				t.Errorf("the plan check must not appear before the run stops calling tools")
			}
			return sseChatChunk("工作做完了") + sseChatFinishChunk("stop") + sseDone
		case 3:
			if !strings.Contains(request, "ally-plan-check") {
				t.Errorf("the round after the run stops must carry the plan check")
			}
			if !strings.Contains(request, "改造入口") {
				t.Errorf("the check must name the step the plan is on")
			}
			return sseChatToolCallChunk("call_2", "plan", `{"finish":"改造入口"}`) +
				sseChatFinishChunk("tool_calls") + sseDone
		default:
			return sseChatChunk("ok") + sseChatFinishChunk("stop") + sseDone
		}
	}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()

	app, recorder := runPlanCheckSession(t, httpServer.URL, "plan-check-report", t.TempDir())
	if got := recorder.endStatus(); got != "run:done" {
		t.Fatalf("run ended with %q, want run:done (errors: %v)", got, recorder.errorEvents())
	}
	if got := server.count(); got != 4 {
		t.Fatalf("provider rounds = %d, want 4 (work, final, report, closing line)", got)
	}
	plan := app.GetPlan("plan-check-report")
	if len(plan) != 2 || plan[0].Status != "done" || plan[1].Status != "in_progress" {
		t.Fatalf("the report must land in the plan, got %#v", plan)
	}
	// The check reached the provider (round 3 saw it) but must not outlive the
	// request: appended to the history it would be saved and resent for the rest
	// of the session's life, one fixed-size user message per run that did work.
	app.mu.Lock()
	saved := app.histories["plan-check-report"]
	app.mu.Unlock()
	for _, m := range saved {
		if strings.Contains(m.Content, "ally-plan-check") {
			t.Fatalf("the plan check must not persist in the saved history: %#v", m.Content)
		}
	}
}

// TestRunChatSkipsThePlanCheckWhenTheRunDidNoWork: a run that only answered a
// question has nothing the plan could be behind, so it must not pay a round for
// the check.
func TestRunChatSkipsThePlanCheckWhenTheRunDidNoWork(t *testing.T) {
	server := &planCheckServer{}
	server.script = func(round int, request string) string {
		if strings.Contains(request, "ally-plan-check") {
			t.Errorf("a run without work must not be asked about the plan")
		}
		return sseChatChunk("只是回答了一个问题") + sseChatFinishChunk("stop") + sseDone
	}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()

	app, recorder := runPlanCheckSession(t, httpServer.URL, "plan-check-quiet", t.TempDir())
	if got := recorder.endStatus(); got != "run:done" {
		t.Fatalf("run ended with %q, want run:done (errors: %v)", got, recorder.errorEvents())
	}
	if got := server.count(); got != 1 {
		t.Fatalf("provider rounds = %d, want 1", got)
	}
	if plan := app.GetPlan("plan-check-quiet"); len(plan) != 2 || plan[0].Status != "in_progress" {
		t.Fatalf("the plan must be left alone, got %#v", plan)
	}
}

// TestRunChatSkipsThePlanCheckWhenTheBatchMovedThePlan: a batch that reports
// progress and does work in the same response has already answered the question,
// so the run must end without the extra round.
func TestRunChatSkipsThePlanCheckWhenTheBatchMovedThePlan(t *testing.T) {
	server := &planCheckServer{}
	server.script = func(round int, request string) string {
		if strings.Contains(request, "ally-plan-check") {
			t.Errorf("a run whose batch moved the plan must not be asked about it")
		}
		if round == 1 {
			return sseChatToolCallChunk("call_1", "create", `{"path":"note.txt","content":"work"}`) +
				sseChatToolCallChunk("call_2", "plan", `{"finish":"改造入口"}`) +
				sseChatFinishChunk("tool_calls") + sseDone
		}
		return sseChatChunk("报告过了") + sseChatFinishChunk("stop") + sseDone
	}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()

	app, recorder := runPlanCheckSession(t, httpServer.URL, "plan-check-synced", t.TempDir())
	if got := recorder.endStatus(); got != "run:done" {
		t.Fatalf("run ended with %q, want run:done (errors: %v)", got, recorder.errorEvents())
	}
	if got := server.count(); got != 2 {
		t.Fatalf("provider rounds = %d, want 2 (work + final)", got)
	}
	if plan := app.GetPlan("plan-check-synced"); len(plan) != 2 || plan[0].Status != "done" {
		t.Fatalf("the batch's report must stand, got %#v", plan)
	}
}
