// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadPreviewBudgetKeepsBatchBoundedAndContinuationEditable(t *testing.T) {
	a := newApiTestApp(t)
	root := a.config.Workspace
	text := strings.Repeat(strings.Repeat("x", 99)+"\n", 1000)
	req := BatchReadRequest{}
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("file-%02d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		req.Files = append(req.Files, BatchReadFileRequest{Path: name})
	}
	result, err := a.batchReadFilesWithConfig(a.config, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 20 {
		t.Fatalf("expected all 20 files, got %d", len(result.Files))
	}
	for _, f := range result.Files {
		if f.Error != "" || !f.Truncated || len(f.Content) > maxReadPreviewBytes+256 || !f.Editable || f.Version == "" {
			t.Fatalf("expected a bounded editable preview for %s: error=%q truncated=%v bytes=%d", f.Path, f.Error, f.Truncated, len(f.Content))
		}
		if f.NextStartLine != f.EndLine+1 || f.NextStartLine <= 1 {
			t.Fatalf("continuation skipped lines: end=%d next=%d", f.EndLine, f.NextStartLine)
		}
	}
	first := result.Files[0]
	remoteBatch := BatchReadResult{}
	for _, f := range req.Files {
		item, itemErr := buildRemoteReadResultItem(remoteRawFile{Path: f.Path, Data: []byte(text)}, f)
		if itemErr != nil {
			t.Fatal(itemErr)
		}
		remoteBatch.Files = append(remoteBatch.Files, item)
	}
	boundBatchReadPreview(&remoteBatch)
	remote := remoteBatch.Files[0]
	if err != nil || remote.Content != first.Content || remote.Version != first.Version {
		t.Fatalf("local/remote previews must agree: err=%v local=%d remote=%d", err, len(first.Content), len(remote.Content))
	}
	next, err := a.batchReadFilesWithConfig(a.config, BatchReadRequest{Files: []BatchReadFileRequest{{Path: req.Files[0].Path, StartLine: first.NextStartLine}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Files) != 1 || next.Files[0].StartLine != first.EndLine+1 || next.Files[0].Version != first.Version || next.Files[0].Error != "" {
		t.Fatalf("continuation must retain the version and next unread line: %#v", next)
	}
	model := renderReadResultForModel(*result)
	if len(model) > maxReadBatchPreviewBytes+4096 || !strings.Contains(model, fmt.Sprintf("Use startLine=%d to continue", first.NextStartLine)) {
		t.Fatalf("model batch must stay bounded and expose continuation: bytes=%d", len(model))
	}
	t.Logf("20 x 100KB file previews: %d bytes in model context", len(model))
}

func TestGrepModelKeepsEveryFileInPage(t *testing.T) {
	for _, count := range []int{30, 200} {
		for _, next := range []int{0, 400} {
			t.Run(fmt.Sprintf("files=%d/next=%d", count, next), func(t *testing.T) {
				r := GrepResult{Mode: "lines", Files: count, MatchedLines: count, Hits: count, NextOffset: next}
				for i := 0; i < count; i++ {
					r.LineHits = append(r.LineHits, GrepFileMatch{Path: fmt.Sprintf("file-%03d.go", i), Lines: []int{1}, Texts: []string{"match"}})
				}
				got := renderGrepResultForModel(r)
				for _, f := range r.LineHits {
					if !strings.Contains(got, f.Path+"\n  1: match") {
						t.Fatalf("file silently omitted: %s", f.Path)
					}
				}
				if next > 0 && !strings.Contains(got, fmt.Sprintf(`next-offset="%d"`, next)) {
					t.Fatal("rendering a complete page must preserve its continuation")
				}
			})
		}
	}
}

func TestCommandPreviewRetainsFullOutputAndActualTail(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"medium", strings.Repeat("H", 20*1024) + "MIDDLE_SENTINEL" + strings.Repeat("T", 40*1024)},
		{"overflow", strings.Repeat("H", 200*1024)},
		{"unicode-overflow", strings.Repeat("中文日志", 20*1024)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := "COMMAND_START\n" + tc.text + "\nFINAL_ERROR: build failed"
			buf := &limitedBuffer{limit: maxToolOutput}
			tw := &teeWriter{primary: buf, spillDir: t.TempDir()}
			buf.onTruncate = tw.startSpill
			// Multiple writes cover the transition from prefix capture to spill.
			for start := 0; start < len(output); start += 1024 {
				end := min(start+1024, len(output))
				if _, err := tw.Write([]byte(output[start:end])); err != nil {
					t.Fatal(err)
				}
			}
			path, size := finalizeCommandSpill(tw, buf)
			full, err := os.ReadFile(path)
			if err != nil || string(full) != output || size != int64(len(output)) {
				t.Fatalf("full output must be retrievable: path=%q size=%d err=%v", path, size, err)
			}
			preview := commandOutputPreview(buf, path)
			if !utf8.ValidString(preview) || len(preview) > maxToolOutput {
				t.Fatalf("preview must stay bounded UTF-8: bytes=%d", len(preview))
			}
			result := CommandResult{Output: preview, OutputFilePath: path, OutputFileBytes: size, Truncated: buf.truncated}
			model := renderCommandResultForModel(result)
			if !strings.Contains(model, "COMMAND_START") || !strings.Contains(model, "FINAL_ERROR: build failed") || !strings.Contains(model, `full="`) {
				t.Fatal("model must see the real head/tail and full-output path")
			}
			if tc.name == "medium" && strings.Contains(model, "MIDDLE_SENTINEL") {
				t.Fatal("the omitted middle should be accessible through the log, not repeated in context")
			}
			promoted := (&App{}).promotedCommandResult(CommandRequest{}, shellInvocation{}, "", buf, 1, ServiceInfo{ID: "svc-test"}, path, size)
			if strings.Contains(promoted.Output, "完整输出已保存") || strings.Contains(promoted.Output, "read 分段") {
				t.Fatal("log retrieval guidance belongs in the model renderer, not command card output")
			}
			model = renderCommandResultForModel(promoted)
			if !strings.Contains(model, "FINAL_ERROR: build failed") || !strings.Contains(model, "svc-test") {
				t.Fatal("promotion must preserve the actual tail and service instructions")
			}
		})
	}
}

func TestCommandSpillSkipsOutputThatFitsModel(t *testing.T) {
	for _, output := range []string{"small output", strings.Repeat("中", maxModelToolOutput/3)} {
		buf := &limitedBuffer{limit: maxToolOutput}
		dir := t.TempDir()
		tw := &teeWriter{primary: buf, spillDir: dir}
		buf.onTruncate = tw.startSpill
		_, _ = tw.Write([]byte(output))
		path, size := finalizeCommandSpill(tw, buf)
		entries, err := os.ReadDir(dir)
		if err != nil || path != "" || size != 0 || len(entries) != 0 || commandOutputPreview(buf, path) != output {
			t.Fatalf("output fitting the model should remain unchanged without a log: path=%q size=%d err=%v", path, size, err)
		}
	}
}

func TestCommandFullLogCanBeReadAndSearched(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, name := findWindowsBash(""); name == "" {
			t.Skip("requires Git Bash")
		}
	}
	a := newApiTestApp(t)
	r, err := a.runCommandWithConfig(context.Background(), a.config, CommandRequest{
		Command: "printf 'COMMAND_START\\n%0140000d\\nFINAL_ERROR: build failed\\n' 0",
		Timeout: 10,
	})
	if err != nil || r.ExitCode != 0 || r.OutputFilePath == "" {
		t.Fatalf("command must retain its full output: exit=%d path=%q err=%v", r.ExitCode, r.OutputFilePath, err)
	}
	if strings.Contains(r.Output, "完整输出已保存") || strings.Contains(r.Output, "read 分段") {
		t.Fatal("command card output must not contain model log retrieval instructions")
	}
	if !strings.Contains(renderCommandResultForModel(r), "FINAL_ERROR: build failed") {
		t.Fatal("the command result must include its real final output")
	}
	read, err := a.readFileWithConfig(a.config, ReadFileRequest{Path: r.OutputFilePath, StartLine: -1})
	if err != nil || !strings.Contains(read.Content, "FINAL_ERROR: build failed") {
		t.Fatalf("read must retrieve the full log tail: err=%v content=%q", err, read.Content)
	}
	hits, err := a.grepFilesWithConfig(context.Background(), a.config, GrepRequest{Path: r.OutputFilePath, Pattern: "FINAL_ERROR"})
	if err != nil || hits.MatchedLines != 1 {
		t.Fatalf("grep must search the retained log directly: err=%v hits=%#v", err, hits)
	}
}
