// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestAPISendMessageRejectsFinishingRun(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	const sessionID, runID = "finishing-session", "finishing-run"
	if err := a.SaveSessionIndex(SessionIndexEntry{ID: sessionID, Workspace: a.config.Workspace}); err != nil {
		t.Fatal(err)
	}
	registerApiRun(t, a, runID, sessionID)
	announced, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a.events = nativeEventSink{send: func(name string, _ any) {
		if name == "run:done" {
			close(announced)
			<-resume
		}
	}}
	go func() {
		defer close(finished)
		a.finishRun(runFinish{ctx: context.Background(), runID: runID, sessionID: sessionID, workspace: a.config.Workspace, started: time.Now(), syncIndex: true}, []openai.ChatCompletionMessage{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: "first task completed"},
		}, runOutcome{kind: "done"})
	}()
	<-announced
	rec, _ := apiRequest(t, a.apiMux(), http.MethodPost, "/api/v1/sessions/"+sessionID+"/messages", "", `{"message":"second task"}`)
	close(resume)
	<-finished
	if rec.Code != http.StatusConflict {
		t.Fatalf("finishing run accepted an input it cannot process: %s", rec.Body.String())
	}
	rec, payload := apiRequest(t, a.apiMux(), http.MethodGet, "/api/v1/sessions/"+sessionID+"/result", "", "")
	data := apiRequireOK(t, rec, payload)
	if data["status"] != "done" || data["result"] != "first task completed" {
		t.Fatalf("completed result changed: %v", data)
	}
}

func TestFinishRunPreservesAcceptedInputsOnCancellation(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	const sessionID, runID = "cancelled-session", "cancelled-run"
	if err := a.saveHistory(sessionID, []openai.ChatCompletionMessage{{Role: "user", Content: "original task"}}); err != nil {
		t.Fatal(err)
	}
	registerApiRun(t, a, runID, sessionID)
	if err := a.InjectRunMessage(runID, "accepted followup"); err != nil {
		t.Fatal(err)
	}
	a.finishRun(runFinish{ctx: context.Background(), runID: runID, sessionID: sessionID, started: time.Now()}, nil, runOutcome{kind: "cancelled", message: "cancelled"})
	history, _ := a.readHistoryFromDisk(sessionID)
	if len(history) != 2 || history[0].Content != "original task" || history[1].Role != "user" || history[1].Content != "accepted followup" {
		t.Fatalf("accepted followup missing from checkpoint: %+v", history)
	}
	if err := a.InjectRunMessage(runID, "too late"); err == nil {
		t.Fatal("finished run still accepts messages")
	}
}

func TestRunInputAdmissionClosesOnlyWithEmptyQueue(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	registerApiRun(t, a, "run", "session")
	if err := a.InjectRunMessage("run", "followup"); err != nil {
		t.Fatal(err)
	}
	if a.closeRunInputIfEmpty("run") {
		t.Fatal("closed admission with an unprocessed message")
	}
	inputs := a.drainPendingInputs("run")
	if len(inputs) != 1 || inputs[0].Text != "followup" {
		t.Fatalf("followup lost: %+v", inputs)
	}
	if !a.closeRunInputIfEmpty("run") {
		t.Fatal("empty queue prevented completion")
	}
	if err := a.InjectRunMessage("run", "too late"); err == nil {
		t.Fatal("closed queue accepted another message")
	}
}

func TestAPIHistoryWriteFailureIsReported(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	const sessionID, runID = "storage-session", "storage-run"
	if err := a.SaveSessionIndex(SessionIndexEntry{ID: sessionID, Workspace: a.config.Workspace}); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	a.historiesDir = blocked
	registerApiRun(t, a, runID, sessionID)
	a.finishRun(runFinish{ctx: context.Background(), runID: runID, sessionID: sessionID, workspace: a.config.Workspace, started: time.Now(), syncIndex: true}, []openai.ChatCompletionMessage{
		{Role: "user", Content: "durable task"},
		{Role: "assistant", Content: "finished answer"},
	}, runOutcome{kind: "done"})
	rec, payload := apiRequest(t, a.apiMux(), http.MethodGet, "/api/v1/sessions/"+sessionID+"/result", "", "")
	data := apiRequireOK(t, rec, payload)
	if data["status"] != "error" || !strings.Contains(data["error"].(string), "save session checkpoint") || data["result"] != "finished answer" {
		t.Fatalf("checkpoint failure was not surfaced with the available answer: %v", data)
	}
}
