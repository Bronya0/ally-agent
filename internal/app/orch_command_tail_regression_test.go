// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyTailCacheSeparatesRequestedLengths(t *testing.T) {
	var source strings.Builder
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(&source, "log line %d\n", i)
	}
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote=%v", remote), func(t *testing.T) {
			cache := newSessionReadCache()
			data := source.String()
			readTail := func(lines int) BatchReadResultItem {
				t.Helper()
				var item BatchReadResultItem
				if remote {
					var err error
					item, err = buildRemoteReadResultItem(remoteRawFile{Path: "big.log", Data: []byte(data), Size: maxReadFileBytes + 1, ReadOnlyTail: true}, BatchReadFileRequest{Path: "big.log", TailLines: lines})
					if err != nil {
						t.Fatal(err)
					}
				} else {
					item = batchReadResultItem(ReadFileRequest{Path: "big.log", StartLine: -lines}, buildReadOnlyTailResult("big.log", []byte(data), maxReadFileBytes+1, lines))
				}
				result := &BatchReadResult{Files: []BatchReadResultItem{item}}
				cache.dedupeResult(result, func(item *BatchReadResultItem) string { return item.Path })
				return result.Files[0]
			}
			for i, lines := range []int{100, 500, 100, 500} {
				item := readTail(lines)
				if item.Reused != (i >= 2) || (item.Content == "") != item.Reused {
					t.Fatalf("tail %d at step %d lost its cached payload: %#v", lines, i, item)
				}
				if item.StartLine != 0 || item.EndLine != 0 || item.Editable || item.Version != "" || item.RangeStatus != "read_only_tail" {
					t.Fatalf("tail acquired edit/absolute-line metadata: %#v", item)
				}
			}
			data += "changed tail\n"
			if item := readTail(100); item.Reused || !strings.Contains(item.Content, "changed tail") {
				t.Fatalf("changed tail was omitted: %#v", item)
			}
		})
	}
}

func TestCommandPromotionFailureRetainsOutput(t *testing.T) {
	for _, tc := range []struct {
		name, reason    string
		spill, stopping bool
	}{
		{name: "full registry", reason: "E_SERVICE_LIMIT"},
		{name: "full registry with log", reason: "E_SERVICE_LIMIT", spill: true},
		{name: "shutting down", reason: "application is shutting down", stopping: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApiTestApp(t)
			if tc.stopping {
				a.stopping = true
			} else {
				for i := 0; i < maxActiveServices; i++ {
					id := fmt.Sprintf("occupied-%d", i)
					a.services[id] = &managedService{info: ServiceInfo{ID: id, Status: "running"}}
				}
			}
			output := "BEFORE_TIMEOUT"
			if tc.spill {
				output += strings.Repeat("x", maxModelToolOutput+1) + "OUTPUT_TAIL"
			}
			if err := os.WriteFile(filepath.Join(a.config.Workspace, "captured-output.txt"), []byte(output+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			command := "cat captured-output.txt; touch command-ready; sleep 30"
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			timeout := make(chan time.Time, 1)
			ready := filepath.Join(a.config.Workspace, "command-ready")
			go func() {
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(ready); err == nil {
						timeout <- time.Now()
						return
					}
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
				}
			}()
			data, err := a.runCommandWithTimer(ctx, a.config, CommandRequest{Command: command, Timeout: 1}, timeout)
			result := toolResult{OK: err == nil, Data: data}
			if !result.OK || !data.TimedOut || data.Cancelled || data.PromotedToService || data.ExitCode != -1 {
				t.Fatalf("expected a failed timeout with structured output: %#v", result)
			}
			model := compactToolResultForModel("command", result, "")
			for _, text := range []string{"BEFORE_TIMEOUT", tc.reason} {
				if !strings.Contains(data.Output, text) || !strings.Contains(model, text) {
					t.Fatalf("lost %q after promotion failure: data=%#v model=%s", text, data, model)
				}
			}
			if toolStageSucceeded(result) {
				t.Fatal("failed promotion must still block dependent stages")
			}
			if tc.spill {
				if data.OutputFilePath == "" || data.OutputFileIncomplete || !strings.Contains(model, data.OutputFilePath) {
					t.Fatalf("lost recoverable log: %#v", data)
				}
				log, err := os.ReadFile(data.OutputFilePath)
				if err != nil || strings.TrimSpace(string(log)) != output || data.OutputFileBytes != int64(len(log)) {
					t.Fatalf("log was not drained before finalization: bytes=%d expected=%d err=%v", len(log), len(output), err)
				}
			}
		})
	}
}
