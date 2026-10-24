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
	for _, tc := range []struct{ files, changes int }{{21, 1}, {5, 45}} {
		t.Run(fmt.Sprintf("%dx%d", tc.files, tc.changes), func(t *testing.T) {
			a := newApiTestApp(t)
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
	for _, failedIndex := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(failedIndex), func(t *testing.T) {
			a := newApiTestApp(t)
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

func TestMcpSessionWaitContinuesWhenManagerStops(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			a := newApiTestApp(t)
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
