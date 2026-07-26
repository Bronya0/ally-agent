package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

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
	app.emit("todo:update", payload)

	if sink.count != 1 || sink.name != "todo:update" {
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

func TestLocalEditPlanIsSharedByConflictDetectionAndExecution(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: dir}
	version := hashVersion(original)
	files := []FileTextEdits{
		{Path: "sample.txt", Version: version, Changes: []TextChange{{OldText: "alpha", NewText: "ALPHA"}}},
		{Path: "./sample.txt", Version: version, Changes: []TextChange{{OldText: "beta", NewText: "BETA"}}},
	}

	plan, err := planLocalEditBatch(cfg, files, localEditPlanForExecution)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 1 || len(plan.Files) != 1 || len(plan.Files[0].Edit.Changes) != 2 {
		t.Fatalf("expected one merged physical target and two changes, got %#v", plan)
	}

	args, err := json.Marshal(ModelEditToolRequest{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	calls := []openai.ToolCall{{Function: openai.FunctionCall{Name: "edit", Arguments: string(args)}}}
	if conflicts := detectWriteBatchConflicts(cfg, calls); len(conflicts) != 0 {
		t.Fatalf("merged local edit should not conflict with itself: %#v", conflicts)
	}
	targets := fileMutationTargets(cfg, "edit", string(args))
	if len(targets) != 1 || targets[0].key != plan.Targets[0].key {
		t.Fatalf("conflict detector and executor plan disagree: targets=%#v plan=%#v", targets, plan.Targets)
	}

	result := NewApp().executeTool(context.Background(), cfg, "session-1", "edit", args)
	if !result.OK {
		t.Fatalf("shared edit plan execution failed: %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ALPHA\nBETA\n" {
		t.Fatalf("unexpected edited content %q", got)
	}
}
