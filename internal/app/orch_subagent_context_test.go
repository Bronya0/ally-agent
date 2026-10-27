// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSubagentCleanContextKeepsInstructions(t *testing.T) {
	redirectAppStateDir(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("project rule"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "CODEGRAPH.md"), []byte("# Code Graph\n\n- entry: internal/main.go\n"), 0600); err != nil {
		t.Fatal(err)
	}
	harness := newSubagentReportHarness(t, subagentTextReply("Done."))
	a := newIsolatedTestApp(t)
	a.stats = nil
	cfg := subagentReportBaseConfig(harness.server.URL, workspace)
	cfg.CustomPrompt = "custom rule"
	_, err := a.executeDelegate(context.Background(), cfg, "test", AgentDelegateRequest{Task: "Check", Role: "reviewer", CleanContext: true, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	var contents string
	for _, message := range harness.recorded()[0].Messages {
		contents += message.Content
	}
	for _, want := range []string{"project rule", "custom rule"} {
		if !strings.Contains(contents, want) {
			t.Fatalf("clean context lost %q: %s", want, contents)
		}
	}
	for _, banned := range []string{"## Project files", "Project Code Graph", "internal/main.go"} {
		if strings.Contains(contents, banned) {
			t.Fatalf("clean context must skip %q: %s", banned, contents)
		}
	}
}

func TestSubagentFileTrackingSeparatesReadsAndSuccessfulWrites(t *testing.T) {
	var reads, edits []string
	seenReads, seenEdits := map[string]bool{}, map[string]bool{}
	read := toolResult{OK: true, Data: BatchReadResult{Files: []BatchReadResultItem{{Path: "a.go"}, {Path: "missing.go", Error: "missing"}}}}
	trackFileFromToolResult("read", `{}`, &read, &reads, &edits, seenReads, seenEdits)
	ok := toolResult{OK: true}
	trackFileFromToolResult("edit", `{"path":"a.go"}`, &ok, &reads, &edits, seenReads, seenEdits)
	failed := toolResult{OK: false}
	trackFileFromToolResult("create", `{"path":"failed.go"}`, &failed, &reads, &edits, seenReads, seenEdits)
	if !reflect.DeepEqual(reads, []string{"a.go"}) || !reflect.DeepEqual(edits, []string{"a.go"}) {
		t.Fatalf("reads=%v edits=%v", reads, edits)
	}
}

func TestSubagentTracksRemoteFilesByTarget(t *testing.T) {
	var reads, edits []string
	seenReads, seenEdits := map[string]bool{}, map[string]bool{}
	result := toolResult{OK: true, Data: BatchReadResult{Files: []BatchReadResultItem{{Path: "a.go"}, {Path: "missing.go", Error: "missing"}}}}
	for _, host := range []string{"one:/work", "two:/work"} {
		args := `{"target":"` + host + `","path":"a.go"}`
		trackFileFromToolResult("remote_read", args, &result, &reads, &edits, seenReads, seenEdits)
		trackFileFromToolResult("remote_edit", args, &result, &reads, &edits, seenReads, seenEdits)
		trackFileFromToolResult("remote_create_file", args, &result, &reads, &edits, seenReads, seenEdits)
	}
	want := []string{"one:/work :: a.go", "two:/work :: a.go"}
	if !reflect.DeepEqual(reads, want) || !reflect.DeepEqual(edits, want) {
		t.Fatalf("reads=%v edits=%v", reads, edits)
	}
}
