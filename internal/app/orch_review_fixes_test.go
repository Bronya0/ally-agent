// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestLocalEditStageHasIndependentCallBudgets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ files, changes int }{{21, 1}, {5, 45}} {
		t.Run(fmt.Sprintf("%dx%d", tc.files, tc.changes), func(t *testing.T) {
			a := newIsolatedToolTestApp(t)
			var before, after strings.Builder
			var changes []TextChange
			for i := 0; i < tc.changes; i++ {
				old, next := fmt.Sprintf("old-%03d", i), fmt.Sprintf("new-%03d", i)
				fmt.Fprintln(&before, old)
				fmt.Fprintln(&after, next)
				changes = append(changes, TextChange{OldText: old, NewText: next})
			}
			var calls []openai.ToolCall
			for i := 0; i < tc.files; i++ {
				p := fmt.Sprintf("file-%d.txt", i)
				if err := os.WriteFile(filepath.Join(a.config.Workspace, p), []byte(before.String()), 0600); err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(FileTextEdits{Path: p, Version: hashVersion([]byte(before.String())), Changes: changes})
				if err != nil {
					t.Fatal(err)
				}
				calls = append(calls, stagedCall("edit", string(raw)))
			}
			for i, result := range a.executeLocalEditToolBatch(t.Context(), a.config, calls) {
				if !result.OK {
					t.Fatalf("call %d failed: %+v", i, result)
				}
				data, err := os.ReadFile(filepath.Join(a.config.Workspace, fmt.Sprintf("file-%d.txt", i)))
				if err != nil || string(data) != after.String() {
					t.Fatalf("file %d: %q, %v", i, data, err)
				}
			}
		})
	}
}

func TestLocalEditStageRecoveryBelongsToFailedCall(t *testing.T) {
	t.Parallel()
	for _, failedIndex := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(failedIndex), func(t *testing.T) {
			a := newIsolatedToolTestApp(t)
			var calls []openai.ToolCall
			for i := 0; i < 3; i++ {
				p := fmt.Sprintf("file-%d.txt", i)
				if err := os.WriteFile(filepath.Join(a.config.Workspace, p), []byte("old\n"), 0600); err != nil {
					t.Fatal(err)
				}
				version := hashVersion([]byte("old\n"))
				if i == failedIndex {
					version = hashVersion([]byte("stale\n"))
				}
				// Exercise canonical matching of an absolute alias as well.
				if i == 1 {
					p = filepath.Join(a.config.Workspace, p)
				}
				raw, _ := json.Marshal(FileTextEdits{Path: p, Version: version, Changes: []TextChange{{OldText: "old", NewText: "new"}}})
				calls = append(calls, stagedCall("edit", string(raw)))
			}
			for i, result := range a.executeLocalEditToolBatch(t.Context(), a.config, calls) {
				wantCode := "E_EDIT_BATCH_ABORTED"
				if i == failedIndex {
					wantCode = "E_VERSION_MISMATCH"
				}
				if result.ErrorCode != wantCode {
					t.Fatalf("call %d received another file's error: want %s, got %+v", i, wantCode, result)
				}
				if result.OK || (result.modelRead != nil) != (i == failedIndex) {
					t.Fatalf("snapshot attached to wrong call %d (failed %d): %+v", i, failedIndex, result)
				}
				if result.modelRead != nil && result.modelRead.Files[0].Path != fmt.Sprintf("file-%d.txt", i) {
					t.Fatalf("wrong source snapshot: %+v", result.modelRead)
				}
				data, err := os.ReadFile(filepath.Join(a.config.Workspace, fmt.Sprintf("file-%d.txt", i)))
				if err != nil || string(data) != "old\n" {
					t.Fatalf("failed batch wrote file %d: %q, %v", i, data, err)
				}
			}
		})
	}
}

func TestLocalEditBatchDistinctFailuresDoNotShareDiagnostics(t *testing.T) {
	t.Parallel()
	for first := 0; first < 3; first++ {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			a := newIsolatedToolTestApp(t)
			files := []FileTextEdits{
				{Path: "multi.txt", Version: hashVersion([]byte("old\nold\n")), Changes: []TextChange{{OldText: "old", NewText: "new"}}},
				{Path: "range.txt", Version: hashVersion([]byte("old\n")), Changes: []TextChange{{LineRange: "9", NewText: "new"}}},
				{Path: "stale.txt", Version: hashVersion([]byte("stale")), Changes: []TextChange{{OldText: "old", NewText: "new"}}},
			}
			originals := []string{"old\nold\n", "old\n", "old\n"}
			codes := []string{"E_MULTI_MATCH", "E_RANGE_OOB", "E_VERSION_MISMATCH"}
			for i, file := range files {
				if err := os.WriteFile(filepath.Join(a.config.Workspace, file.Path), []byte(originals[i]), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var calls []openai.ToolCall
			for i := 0; i < len(files); i++ {
				raw, _ := json.Marshal(files[(first+i)%len(files)])
				calls = append(calls, stagedCall("edit", string(raw)))
			}
			results := a.executeLocalEditToolBatch(t.Context(), a.config, calls)
			if results[0].OK || results[0].ErrorCode != codes[first] || results[0].modelRead == nil {
				t.Fatalf("first failed call lost its diagnosis: %+v", results[0])
			}
			for i, result := range results[1:] {
				if result.OK || result.ErrorCode != "E_EDIT_BATCH_ABORTED" || result.modelRead != nil || result.Details != nil || strings.Contains(result.Error, files[first].Path) {
					t.Fatalf("unprocessed call %d received another file's diagnosis: %+v", i+1, result)
				}
			}
			for i, file := range files {
				data, err := os.ReadFile(filepath.Join(a.config.Workspace, file.Path))
				if err != nil || string(data) != originals[i] {
					t.Fatalf("failed batch wrote %s: %q, %v", file.Path, data, err)
				}
			}
		})
	}
}

func TestLocalEditBatchErrorOwnershipWithoutRecovery(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"large-stale", "missing", "protected"} {
		t.Run(kind, func(t *testing.T) {
			a := newIsolatedToolTestApp(t)
			peer := FileTextEdits{Path: "peer.txt", Version: hashVersion([]byte("old\n")), Changes: []TextChange{{OldText: "old", NewText: "new"}}}
			if err := os.WriteFile(filepath.Join(a.config.Workspace, peer.Path), []byte("old\n"), 0600); err != nil {
				t.Fatal(err)
			}
			bad := FileTextEdits{Path: "bad.txt", Version: hashVersion([]byte("stale")), Changes: peer.Changes}
			wantCode := ""
			if kind == "large-stale" {
				wantCode = "E_VERSION_MISMATCH"
				if err := os.WriteFile(filepath.Join(a.config.Workspace, bad.Path), []byte(strings.Repeat("old\n", maxEditRecoveryBytes)), 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "protected" {
				bad.Path = ".git/config"
				wantCode = "E_PROTECTED_PATH"
			}
			var calls []openai.ToolCall
			for _, file := range []FileTextEdits{peer, bad} {
				raw, _ := json.Marshal(file)
				calls = append(calls, stagedCall("edit", string(raw)))
			}
			results := a.executeLocalEditToolBatch(t.Context(), a.config, calls)
			if results[0].OK || results[0].ErrorCode != "E_EDIT_BATCH_ABORTED" || results[0].modelRead != nil || results[0].Details != nil {
				t.Fatalf("healthy peer received file error: %+v", results[0])
			}
			if results[1].OK || results[1].ErrorCode != wantCode || results[1].modelRead != nil || results[1].Error == "" {
				t.Fatalf("failed call lost snapshot-free error: %+v", results[1])
			}
			data, err := os.ReadFile(filepath.Join(a.config.Workspace, peer.Path))
			if err != nil || string(data) != "old\n" {
				t.Fatalf("aborted peer was modified: %q, %v", data, err)
			}
		})
	}
}

func TestMcpSessionWaitContinuesWhenManagerStops(t *testing.T) {
	t.Parallel()
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			a := newIsolatedToolTestApp(t)
			m := NewMcpManager(a.config.Workspace, nil)
			defer m.Shutdown()
			a.mcpManager = m
			sink := &mcpWaitEventSink{waiting: make(chan struct{}, 1)}
			a.events = sink
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- a.waitForSessionMcp(ctx, "run", "session") }()
			select {
			case <-sink.waiting:
			case <-time.After(3 * time.Second):
				t.Fatal("no discovery wait")
			}
			if cancelled {
				cancel()
			}
			m.Shutdown()
			select {
			case err := <-done:
				if (!cancelled && err != nil) || (cancelled && err != context.Canceled) {
					t.Fatalf("shutdown/cancellation result: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("manager shutdown stranded session")
			}
		})
	}
}

func TestLocalCommandLogReservesActualByteCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Eight active 64 MiB sinks fit the 512 MiB quota, even before they write.
	for i := 0; i < 8; i++ {
		f, err := createCommandLog(dir)
		if err != nil {
			t.Fatalf("log %d refused within quota: %v", i+1, err)
		}
		t.Cleanup(func() { _ = f.Close(); releaseCommandLog(f.Name()) })
	}
	if f, err := createCommandLog(dir); err == nil {
		_ = f.Close()
		releaseCommandLog(f.Name())
		t.Fatal("ninth active log exceeded quota")
	}
}
