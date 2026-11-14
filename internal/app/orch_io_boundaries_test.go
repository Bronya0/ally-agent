// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	t.Parallel()
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprintf("batch=%v", batch), func(t *testing.T) {
			a := newIsolatedToolTestApp(t)
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
	t.Parallel()
	for _, tc := range []struct {
		name     string
		original []byte
		change   TextChange
	}{
		{"BOMless UTF16", []byte{'a', 0, '\n', 0, 0x49, 0x6c, 0x57, 0x5b, '\n', 0}, TextChange{LineRange: "1-2", NewText: "汉字"}},
		{"UTF8 NUL", []byte("old\n"), TextChange{OldText: "old", NewText: "a\x00b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newIsolatedToolTestApp(t)
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
			if !batch[0].OK || batch[1].OK || batch[1].ErrorCode != "E_EDIT_ENCODING" {
				t.Fatalf("invalid encoding should fail only its own edit: %#v", batch)
			}
			for p, expected := range map[string][]byte{path: tc.original, peerPath: []byte("new\n")} {
				actual, err := os.ReadFile(p)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatalf("unexpected edit result for %s: %x err=%v", p, actual, err)
				}
			}
			// A cancelled transport ensures even a regression cannot contact a host.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = a.remoteEditOne(ctx, remoteTarget{Host: "example.invalid"}, req, remoteRawFile{Path: req.Path, Data: decoded, RawData: tc.original, Size: int64(len(tc.original))})
			if toolErrorCode(err) != "E_EDIT_ENCODING" {
				t.Fatalf("remote edit bypassed shared encoding gate: %v", err)
			}
		})
	}
}
