// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ally-dev/internal/tools/read"
	openai "github.com/sashabaranov/go-openai"
)

// Observe a passed cancellation check while the test still holds the file lock.
// This schedules cancellation during the wait without sleeps or polling.
type editCheckContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *editCheckContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestEditCancellationWhileQueuedNeverWrites(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprintf("batch=%v", batch), func(t *testing.T) {
			a := newApiTestApp(t)
			count := 1
			if batch {
				count = 2
			}
			var calls []openai.ToolCall
			for i := 0; i < count; i++ {
				path := fmt.Sprintf("file-%d.txt", i)
				if err := os.WriteFile(filepath.Join(a.config.Workspace, path), []byte("old\n"), 0600); err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(FileTextEdits{Path: path, Version: hashVersion([]byte("old\n")), Changes: []TextChange{{OldText: "old", NewText: "new"}}})
				if err != nil {
					t.Fatal(err)
				}
				calls = append(calls, stagedCall("edit", string(raw)))
			}
			parent, cancel := context.WithCancel(context.Background())
			ctx := &editCheckContext{Context: parent, checked: make(chan struct{})}
			a.fileOpsMu.Lock()
			locked := true
			defer func() {
				cancel()
				if locked {
					a.fileOpsMu.Unlock()
				}
			}()
			done := make(chan []toolResult, 1)
			go func() {
				if batch {
					done <- a.executeLocalEditToolBatch(ctx, a.config, calls)
				} else {
					done <- []toolResult{a.executeTool(ctx, a.config, "cancel-test", "edit", []byte(calls[0].Function.Arguments))}
				}
			}()
			select {
			case <-ctx.checked:
			case <-time.After(3 * time.Second):
				t.Fatal("edit did not check cancellation before waiting")
			}
			cancel()
			a.fileOpsMu.Unlock()
			locked = false
			select {
			case results := <-done:
				for _, result := range results {
					if result.OK || result.ErrorCode != "E_CANCELLED" {
						t.Fatalf("cancelled edit reported success: %#v", result)
					}
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled edit did not finish after the lock was released")
			}
			for i := 0; i < count; i++ {
				data, err := os.ReadFile(filepath.Join(a.config.Workspace, fmt.Sprintf("file-%d.txt", i)))
				if err != nil || string(data) != "old\n" {
					t.Fatalf("cancelled edit changed file %d: %q err=%v", i, data, err)
				}
			}
		})
	}
}

func TestEditUnreadableEncodingLeavesAllSourcesUntouched(t *testing.T) {
	for _, tc := range []struct {
		name     string
		original []byte
		change   TextChange
	}{
		{"BOMless UTF16", []byte{'a', 0, '\n', 0, 0x49, 0x6c, 0x57, 0x5b, '\n', 0}, TextChange{LineRange: "1-2", NewText: "汉字"}},
		{"UTF8 NUL", []byte("old\n"), TextChange{OldText: "old", NewText: "a\x00b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApiTestApp(t)
			path := filepath.Join(a.config.Workspace, "source.txt")
			if err := os.WriteFile(path, tc.original, 0600); err != nil {
				t.Fatal(err)
			}
			decoded, err := read.DecodeTextBytes(tc.original)
			if err != nil {
				t.Fatal(err)
			}
			req := FileTextEdits{Path: "source.txt", Version: hashVersion(decoded), Changes: []TextChange{tc.change}}
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			single := a.executeTool(context.Background(), a.config, "encoding-test", "edit", raw)
			if single.OK || single.ErrorCode != "E_EDIT_ENCODING" {
				t.Fatalf("bad encoding was accepted: %#v", single)
			}
			peerPath := filepath.Join(a.config.Workspace, "peer.txt")
			if err := os.WriteFile(peerPath, []byte("old\n"), 0600); err != nil {
				t.Fatal(err)
			}
			peerRaw, err := json.Marshal(FileTextEdits{Path: "peer.txt", Version: hashVersion([]byte("old\n")), Changes: []TextChange{{OldText: "old", NewText: "new"}}})
			if err != nil {
				t.Fatal(err)
			}
			batch := a.executeLocalEditToolBatch(context.Background(), a.config, []openai.ToolCall{stagedCall("edit", string(peerRaw)), stagedCall("edit", string(raw))})
			for _, result := range batch {
				if result.OK || result.ErrorCode != "E_EDIT_ENCODING" {
					t.Fatalf("invalid encoding did not abort peers: %#v", result)
				}
			}
			for p, expected := range map[string][]byte{path: tc.original, peerPath: []byte("old\n")} {
				actual, err := os.ReadFile(p)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatalf("refused edit changed %s: %x err=%v", p, actual, err)
				}
			}
			// A cancelled transport ensures even a regression cannot contact a host.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = a.remoteEditOne(ctx, remoteTarget{Host: "example.invalid", WorkspaceRoot: "/srv/app"}, req, remoteRawFile{Path: req.Path, Data: decoded, RawData: tc.original, Size: int64(len(tc.original))})
			if toolErrorCode(err) != "E_EDIT_ENCODING" {
				t.Fatalf("remote edit bypassed shared encoding gate: %v", err)
			}
		})
	}
}

func TestRemoteCommandLogQuotaIncludesActiveReservations(t *testing.T) {
	for _, tc := range []struct {
		name              string
		completed, active int
		size              int64
		allowed           bool
		remaining         int
	}{
		{"evict for active byte reservations", 7, 1, maxCommandLogBytes, true, 6},
		{"evict for active file reservations", 128, 1, 1, true, 126},
		{"active reservations alone exhaust quota", 1, 8, 1, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApiTestApp(t)
			py := pickRemoteHelperPython(t)
			dir := filepath.Join(a.config.Workspace, ".tmp")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.completed; i++ {
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("ally-run-old-%03d.log", i)))
				if err != nil {
					t.Fatal(err)
				}
				truncateErr := f.Truncate(tc.size)
				closeErr := f.Close()
				if truncateErr != nil || closeErr != nil {
					t.Fatalf("create quota sample: %v %v", truncateErr, closeErr)
				}
			}
			for i := 0; i < tc.active; i++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("ally-run-active-%d.part", i)), []byte("active"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			script, err := buildRemoteScript(map[string]any{"op": "stat", "workspaceRoot": a.config.Workspace, "path": "."})
			if err != nil {
				t.Fatal(err)
			}
			probe := "def quota_probe(root, payload):\n    handle, path = open_command_log(root)\n    handle.close()\n    return {'path': as_posix_rel(root, path)}\n\n"
			script = strings.Replace(script, "OPS = {", probe+"OPS = {", 1)
			script = strings.Replace(script, "\"stat\": op_stat,", "\"stat\": quota_probe,", 1)
			resp, err := runRemoteHelperScript(t, py, script)
			if err != nil || resp.OK != tc.allowed {
				t.Fatalf("quota decision: ok=%v error=%s transport=%v", resp.OK, resp.Error, err)
			}
			if !tc.allowed && !strings.Contains(resp.Error, "quota reached") {
				t.Fatalf("unexpected refusal: %s", resp.Error)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			completed := 0
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".log") {
					completed++
				}
			}
			if completed != tc.remaining {
				t.Fatalf("wrong eviction count: got %d want %d", completed, tc.remaining)
			}
			for i := 0; i < tc.active; i++ {
				data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("ally-run-active-%d.part", i)))
				if err != nil || string(data) != "active" {
					t.Fatalf("active log was touched: %q err=%v", data, err)
				}
			}
		})
	}
}
