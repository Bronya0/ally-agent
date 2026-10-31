// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ally-dev/internal/tools/read"
	openai "github.com/sashabaranov/go-openai"
)

func TestLocalEditStageIndependentFileCommits(t *testing.T) {
	t.Parallel()
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale=%v", stale), func(t *testing.T) {
			a := newIsolatedToolTestApp(t)
			cfg := a.config
			cfg.ToolConcurrency = 1
			var calls []openai.ToolCall
			for _, p := range []string{"a.txt", "b.txt"} {
				if err := os.WriteFile(filepath.Join(cfg.Workspace, p), []byte("old\n"), 0600); err != nil {
					t.Fatal(err)
				}
				version := hashVersion([]byte("old\n"))
				if stale && p == "b.txt" {
					version = hashVersion([]byte("stale"))
				}
				raw, _ := json.Marshal(FileTextEdits{Path: p, Version: version, Changes: []TextChange{{OldText: "old", NewText: "new"}}})
				calls = append(calls, stagedCall("edit", string(raw)))
			}
			results := make([]toolResult, len(calls))
			a.executeStagedToolBatch(context.Background(), cfg, calls, detectToolBatchConflicts(cfg, calls),
				func(ctx context.Context, i int, c openai.ToolCall) toolResult {
					return a.executeTool(ctx, cfg, "test", c.Function.Name, []byte(c.Function.Arguments))
				},
				func(i int, _ openai.ToolCall, r toolResult, _ int64) { results[i] = r }, a.executeLocalEditToolBatch)
			for i, p := range []string{"a.txt", "b.txt"} {
				data, err := os.ReadFile(filepath.Join(cfg.Workspace, p))
				if err != nil {
					t.Fatal(err)
				}
				want := "new\n"
				if stale && p == "b.txt" {
					want = "old\n"
				}
				if string(data) != want {
					t.Fatalf("independent edit result: %s=%q want=%q result=%#v", p, data, want, results[i])
				}
				if !stale && !results[i].OK {
					t.Fatalf("valid edit %s failed: %#v", p, results[i])
				}
				if stale && p == "a.txt" && !results[i].OK {
					t.Fatalf("valid peer edit was cancelled: %#v", results[i])
				}
				if stale && p == "b.txt" && (results[i].OK || results[i].ErrorCode != "E_VERSION_MISMATCH") {
					t.Fatalf("stale edit should fail independently: %#v", results[i])
				}
			}
		})
	}
}

func TestEditPreservesUTF16AndReturnedVersion(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	p := filepath.Join(a.config.Workspace, "utf16.txt")
	before := []byte{0xff, 0xfe, 'a', 0, '\r', 0, '\n', 0}
	if err := os.WriteFile(p, before, 0600); err != nil {
		t.Fatal(err)
	}
	first, err := a.readFileWithConfig(a.config, ReadFileRequest{Path: "utf16.txt"})
	if err != nil {
		t.Fatal(err)
	}
	r := executeRecoveryTestEdit(t, a, context.Background(), a.config, FileTextEdits{Path: "utf16.txt", Version: first.Version, Changes: []TextChange{{OldText: "a", NewText: "b"}}})
	if !r.OK {
		t.Fatal(r.Error)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, []byte{0xff, 0xfe, 'b', 0, '\r', 0, '\n', 0}) {
		t.Fatalf("encoding changed: %x", after)
	}
	next, err := a.readFileWithConfig(a.config, ReadFileRequest{Path: "utf16.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Version != r.Data.(MultiEditResult).Files[0].Version {
		t.Fatal("returned version cannot be reused")
	}
}

func TestEditRollbackPreservesRawUTF16(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	root := a.config.Workspace
	before := []byte{0xff, 0xfe, 'a', 0, '\n', 0}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), before, 0600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(sub, "b.txt")
	if err := os.WriteFile(b, []byte("b\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if err := os.Chmod(b, 0444); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(b, 0600) })
	} else {
		if err := os.Chmod(sub, 0555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(sub, 0700) })
	}
	_, err := a.editFilesWithConfig(a.config, []FileTextEdits{{Path: "a.txt", Version: hashVersion([]byte("a\n")), Changes: []TextChange{{OldText: "a", NewText: "A"}}}, {Path: "sub/b.txt", Version: hashVersion([]byte("b\n")), Changes: []TextChange{{OldText: "b", NewText: "B"}}}})
	if toolErrorCode(err) != "E_EDIT_COMMIT" {
		t.Fatalf("expected commit refusal: %v", err)
	}
	var calls []openai.ToolCall
	for _, file := range []FileTextEdits{
		{Path: "a.txt", Version: hashVersion([]byte("a\n")), Changes: []TextChange{{OldText: "a", NewText: "A"}}},
		{Path: "sub/b.txt", Version: hashVersion([]byte("b\n")), Changes: []TextChange{{OldText: "b", NewText: "B"}}},
	} {
		raw, _ := json.Marshal(file)
		calls = append(calls, stagedCall("edit", string(raw)))
	}
	results := a.executeLocalEditToolBatch(t.Context(), a.config, calls)
	if !results[0].OK || results[1].ErrorCode != "E_EDIT_COMMIT" {
		t.Fatalf("commit error was not assigned to its file: %+v", results)
	}
	after, err := os.ReadFile(filepath.Join(root, "a.txt"))
	want := []byte{0xff, 0xfe, 'A', 0, '\n', 0}
	if err != nil || !bytes.Equal(after, want) {
		t.Fatalf("successful peer edit changed UTF16 encoding: %x %v", after, err)
	}
}

func TestTinyReadReceiptSavesContext(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	if err := os.WriteFile(filepath.Join(a.config.Workspace, "tiny.txt"), []byte("ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := newSessionReadCache()
	req := BatchReadRequest{Files: []BatchReadFileRequest{{Path: "tiny.txt"}}}
	first, err := cache.read(a, a.config, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.read(a, a.config, req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Files[0].Reused || len(renderReadResultForModel(*second)) >= len(renderReadResultForModel(*first)) {
		t.Fatal("deduplication grew the model payload")
	}
}

func TestReadBatchKeepsExplicitSourceRangesWhole(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	req := BatchReadRequest{}
	for i := 0; i < 20; i++ {
		p := fmt.Sprintf("file-%d.txt", i)
		if err := os.WriteFile(filepath.Join(a.config.Workspace, p), []byte(strings.Repeat(strings.Repeat("x", 99)+"\n", 100)), 0600); err != nil {
			t.Fatal(err)
		}
		req.Files = append(req.Files, BatchReadFileRequest{Path: p, StartLine: 1, EndLine: 50})
	}
	r, err := a.batchReadFilesWithConfig(a.config, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Files {
		if f.EndLine != 50 || f.Truncated {
			t.Fatalf("explicit source range was split: %#v", f)
		}
	}
}

func TestLargeLogTailLocalAndRemoteShareReadOnlyContract(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	py := pickRemoteHelperPython(t)
	p := filepath.Join(a.config.Workspace, "big.log")
	if err := os.WriteFile(p, []byte(strings.Repeat("log line\n", 4*1024*1024)+"FINAL_ERROR\n"), 0600); err != nil {
		t.Fatal(err)
	}
	local, err := a.readFileWithConfig(a.config, ReadFileRequest{Path: "big.log", StartLine: -10})
	if err != nil {
		t.Fatal(err)
	}
	script, err := buildRemoteScript(map[string]any{"op": "read", "workspaceRoot": a.config.Workspace, "path": "big.log", "maxBytes": maxReadFileBytes, "tailLines": 10})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := runRemoteHelperScript(t, py, script)
	if err != nil || !resp.OK {
		t.Fatalf("remote tail: %v %s", err, resp.Error)
	}
	var wire remoteReadBatchItem
	if err := json.Unmarshal(resp.Data, &wire.Data); err != nil {
		t.Fatal(err)
	}
	raw, err := decodeRemoteRawFile(wire.Data)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := buildRemoteReadResultItem(raw, BatchReadFileRequest{Path: "big.log", TailLines: 10})
	if err != nil {
		t.Fatal(err)
	}
	if local.Editable || remote.Editable || local.Version != "" || remote.Version != "" || local.Content != remote.Content || !strings.Contains(remote.Content, "FINAL_ERROR") {
		t.Fatalf("tail contract diverged: local=%#v remote=%#v", local, remote)
	}
}

func TestMissingReadBlocksDependentStageAndCancellationIsBounded(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	r, err := a.batchReadFilesWithConfig(a.config, BatchReadRequest{Files: []BatchReadFileRequest{{Path: "missing.txt"}}})
	if err != nil {
		t.Fatal(err)
	}
	if toolStageSucceeded(toolResult{OK: true, Data: r}) || !strings.Contains(renderReadResultForModel(*r), "E_PATH_NOT_FOUND") {
		t.Fatal("missing input reported successful")
	}
	for i := 0; i < cap(a.fileReadSlots); i++ {
		a.fileReadSlots <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled, err := a.batchReadFilesWithContext(ctx, a.config, BatchReadRequest{Files: []BatchReadFileRequest{{Path: "missing.txt"}}})
	if err != nil || len(cancelled.Files) != 1 || cancelled.Files[0].ErrorCode != "E_CANCELLED" {
		t.Fatalf("queued read ignored cancellation: %#v %v", cancelled, err)
	}
}

func TestCommandLogFailureKeepsTailAndReportsPartial(t *testing.T) {
	t.Parallel()
	buf := &limitedBuffer{limit: 8, tail: newRollingBuffer(128)}
	tw := &teeWriter{primary: buf, spillDir: t.TempDir()}
	buf.onTruncate = tw.startSpill
	_, _ = tw.Write([]byte("HEAD123456789"))
	if tw.spill == nil {
		t.Fatal("no spill")
	}
	_ = tw.spill.Close()
	_, err := tw.Write([]byte("FINAL_ERROR"))
	if err != nil {
		t.Fatal("capture failure must not break the child pipe")
	}
	p, _ := finalizeCommandSpill(tw, buf)
	r := CommandResult{Output: commandOutputPreview(buf, p), OutputFilePath: p, OutputWarning: commandLogWarning(tw, buf), OutputFileIncomplete: true}
	model := renderCommandResultForModel(r)
	if r.OutputWarning == "" || !strings.Contains(model, "FINAL_ERROR") || !strings.Contains(model, ` partial="`) || strings.Contains(model, ` full="`) {
		t.Fatalf("dishonest or stale output: %s", model)
	}
}

func TestRemoteWriteChecksRawSnapshotBeforeCommit(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	py := pickRemoteHelperPython(t)
	p := filepath.Join(a.config.Workspace, "sample.txt")
	before := []byte{0xff, 0xfe, 'a', 0, '\r', 0, '\n', 0}
	after, err := read.EncodeLike(before, "b\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []bool{true, false} {
		current := before
		if changed {
			current = []byte("external edit\n")
		}
		if err := os.WriteFile(p, current, 0600); err != nil {
			t.Fatal(err)
		}
		script, err := buildRemoteScript(map[string]any{"op": "write", "workspaceRoot": a.config.Workspace, "path": "sample.txt", "overwrite": true, "expectedSHA256": read.HashBytes(before), "dataBase64": base64.StdEncoding.EncodeToString(after)})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := runRemoteHelperScript(t, py, script)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			if resp.OK || toolErrorCode(remoteHelperError(resp)) != "E_VERSION_MISMATCH" || !bytes.Equal(got, current) {
				t.Fatalf("remote overwrote concurrent edit: %#v %x", resp, got)
			}
		} else if !resp.OK || !bytes.Equal(got, after) {
			t.Fatalf("remote UTF16 commit: %#v %x", resp, got)
		}
	}
}

func TestRemoteReadRejectsNonAdvancingBatch(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	attempts := 0
	a.remoteReadBatchFn = func(_ context.Context, _ remoteTarget, paths []string) ([]remoteReadBatchItem, []string, error) {
		attempts++
		return nil, paths, nil
	}
	r, err := a.remoteReadFile(context.Background(), RemoteReadFileRequest{Target: "example.invalid:/srv/app", Files: []BatchReadFileRequest{{Path: "a.txt"}, {Path: "b.txt"}}})
	if err != nil || attempts != 1 || len(r.Files) != 2 {
		t.Fatalf("non-advancing batch retried: calls=%d result=%#v err=%v", attempts, r, err)
	}
	for _, f := range r.Files {
		if f.ErrorCode != "E_REMOTE_READ_PROTOCOL" {
			t.Fatalf("missing protocol failure: %#v", f)
		}
	}
}

func TestRemoteCommandSavesRecoverableLog(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	py := pickRemoteHelperPython(t)
	command := "printf 'BEGIN\\n%0140000d\\nFINAL_ERROR\\n' 0"
	if runtime.GOOS == "windows" {
		command = "echo BEGIN & (for /L %i in (1,1,1400) do @echo " + strings.Repeat("x", 100) + ") & echo FINAL_ERROR"
	}
	script, err := buildRemoteScript(map[string]any{"op": "run", "workspaceRoot": a.config.Workspace, "command": command, "maxOutput": maxToolOutput, "timeoutSeconds": 10, "modelOutputBytes": maxModelToolOutput})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := runRemoteHelperScript(t, py, script)
	if err != nil || !resp.OK {
		t.Fatalf("remote command: %v %s", err, resp.Error)
	}
	var r CommandResult
	if err := json.Unmarshal(resp.Data, &r); err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 || r.OutputFilePath == "" || r.OutputFileIncomplete {
		t.Fatalf("no usable full log: %#v", r)
	}
	log, err := os.ReadFile(filepath.Join(a.config.Workspace, filepath.FromSlash(r.OutputFilePath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log, []byte("BEGIN")) || !bytes.Contains(log, []byte("FINAL_ERROR")) || !strings.Contains(renderCommandResultForModel(r), "FINAL_ERROR") {
		t.Fatal("real output missing from recovered log")
	}
}
