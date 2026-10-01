// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toolshared "ally-dev/internal/tools/shared"
)

// deleteTestWorkspace builds a small workspace (two root files plus a nested
// directory) so a refusal can be told apart from a missing-path error.
func deleteTestWorkspace(t *testing.T) (string, ConfigState) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt", "sub/c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, ConfigState{Workspace: dir}
}

func deleteResultOf(t *testing.T, res toolResult) DeletePathsResult {
	t.Helper()
	typed, ok := res.Data.(DeletePathsResult)
	if !ok {
		t.Fatalf("expected a DeletePathsResult payload, got %#v", res.Data)
	}
	return typed
}

// TestResolveDeletePathListFoldsBothSpellings pins the one place that turns the
// two request spellings into one candidate list. Both delete tools and the Wails
// API (App.DeletePath) go through it, so a divergence between "one path or
// several" and "at most N" can only be introduced here.
func TestResolveDeletePathListFoldsBothSpellings(t *testing.T) {
	tooMany := make([]string, toolshared.DeletePathListLimit+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("f%d.txt", i)
	}

	for _, tc := range []struct {
		name        string
		single      string
		list        []string
		want        int
		wantCode    string
		wantMention string
	}{
		{name: "single path", single: "a.txt", want: 1},
		{name: "path list", list: []string{"a.txt", "b.txt"}, want: 2},
		{name: "at the limit", list: tooMany[:toolshared.DeletePathListLimit], want: toolshared.DeletePathListLimit},
		{name: "both spellings", single: "a.txt", list: []string{"b.txt"}, wantCode: "E_BAD_ARGS", wantMention: "not both"},
		{name: "neither spelling", wantCode: "E_BAD_ARGS", wantMention: "non-empty paths"},
		{name: "blank entry", list: []string{"a.txt", "  "}, wantCode: "E_BAD_ARGS", wantMention: "blank"},
		{name: "above the limit", list: tooMany, wantCode: "E_BAD_ARGS", wantMention: fmt.Sprintf("%d", toolshared.DeletePathListLimit)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveDeletePathList(tc.single, tc.list)
			if tc.wantCode != "" {
				if err == nil || toolErrorCode(err) != tc.wantCode || !strings.Contains(err.Error(), tc.wantMention) {
					t.Fatalf("expected %s mentioning %q, got %v", tc.wantCode, tc.wantMention, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("expected %d candidates, got %v", tc.want, got)
			}
		})
	}
}

// TestCheckDeletePathListRejectsDuplicateAndNesting locks the two cross-path
// rules on both key spaces: a path listed twice, and a path listed inside
// another. Both make the outcome depend on the order the paths are handled in,
// so both are refused; siblings that merely share a prefix are not nested.
func TestCheckDeletePathListRejectsDuplicateAndNesting(t *testing.T) {
	remote := func(rel string) fileMutationTarget {
		return fileMutationTarget{remoteMutationKey("dev:/srv/app", rel), rel}
	}
	local := func(abs string) fileMutationTarget {
		return fileMutationTarget{localMutationKey(abs), abs}
	}

	if err := checkDeletePathList([]fileMutationTarget{remote("a"), remote("a")}); toolErrorCode(err) != "E_DUPLICATE_PATH" {
		t.Fatalf("a repeated remote path must be refused, got %v", err)
	}
	if err := checkDeletePathList([]fileMutationTarget{local(filepath.Join("ws", "a")), local(filepath.Join("ws", "a"))}); toolErrorCode(err) != "E_DUPLICATE_PATH" {
		t.Fatalf("a repeated local path must be refused, got %v", err)
	}

	for _, targets := range [][]fileMutationTarget{
		{remote("sub/c.txt"), remote("sub")},
		{remote("sub"), remote("sub/c.txt")},
		{local(filepath.Join("ws", "sub", "c.txt")), local(filepath.Join("ws", "sub"))},
		{local(filepath.Join("ws", "sub")), local(filepath.Join("ws", "sub", "c.txt"))},
	} {
		if err := checkDeletePathList(targets); toolErrorCode(err) != "E_PATH_OVERLAP" {
			t.Fatalf("nesting must be refused in both orders, got %v", err)
		}
	}

	if err := checkDeletePathList([]fileMutationTarget{remote("app"), remote("app-backup")}); err != nil {
		t.Fatalf("a shared prefix is not nesting: %v", err)
	}
	if err := checkDeletePathList([]fileMutationTarget{local(filepath.Join("ws", "app")), local(filepath.Join("ws", "app-backup"))}); err != nil {
		t.Fatalf("a shared prefix is not nesting for local paths either: %v", err)
	}
}

// TestLocalDeleteToolTakesBothSpellings covers the local tool end to end: one
// call deletes every listed path, a path it cannot delete refuses the whole call
// before anything is removed, a path that does not exist comes back as an absent
// slot without failing the rest, and the gate holds the model to the declared
// shape (exactly one of path/paths, at most N entries).
func TestLocalDeleteToolTakesBothSpellings(t *testing.T) {
	dir, cfg := deleteTestWorkspace(t)
	app := NewApp()
	ctx := t.Context()

	res := app.executeTool(ctx, cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Paths: []string{"a.txt", "b.txt"}}))
	if !res.OK {
		t.Fatalf("batch delete must succeed, got %v", res.Error)
	}
	batch := deleteResultOf(t, res)
	if batch.DeletedCount != 2 || batch.FailedCount != 0 || len(batch.Paths) != 2 {
		t.Fatalf("expected two successful slots, got %#v", batch)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be deleted, lstat err: %v", name, err)
		}
	}

	// A missing entry is no longer a refusal: it comes back as an absent slot, the
	// rest of the call still runs, and nothing was deleted for that entry.
	fresh := filepath.Join(dir, "fresh.txt")
	if err := os.WriteFile(fresh, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res = app.executeTool(ctx, cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Paths: []string{"fresh.txt", "missing.txt"}}))
	if !res.OK {
		t.Fatalf("a missing path must not fail the call, got %v", res.Error)
	}
	withMissing := deleteResultOf(t, res)
	if withMissing.DeletedCount != 1 || withMissing.FailedCount != 0 || withMissing.AbsentCount != 1 || len(withMissing.Paths) != 2 {
		t.Fatalf("expected one deleted slot and one absent slot, got %#v", withMissing)
	}
	if absent := withMissing.Paths[1]; !absent.OK || !absent.Absent || absent.Path != "missing.txt" {
		t.Fatalf("the missing path must report an ok+absent slot, got %#v", absent)
	}
	if _, err := os.Lstat(fresh); !os.IsNotExist(err) {
		t.Fatalf("the deletable path in the same call must still be deleted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "sub", "c.txt")); err != nil {
		t.Fatalf("an untouched path must survive the call: %v", err)
	}

	// Single path, nothing there: ok with one absent slot and no error at all.
	res = app.executeTool(ctx, cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: "gone.txt"}))
	if !res.OK {
		t.Fatalf("deleting a missing path must not error, got %v", res.Error)
	}
	onlyAbsent := deleteResultOf(t, res)
	if onlyAbsent.DeletedCount != 0 || onlyAbsent.FailedCount != 0 || onlyAbsent.AbsentCount != 1 || len(onlyAbsent.Paths) != 1 || !onlyAbsent.Paths[0].Absent {
		t.Fatalf("expected a single absent slot, got %#v", onlyAbsent)
	}

	res = app.executeTool(ctx, cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Paths: []string{"sub", "sub/c.txt"}, Recursive: true}))
	if res.OK || res.ErrorCode != "E_PATH_OVERLAP" {
		t.Fatalf("a nested pair must be refused, got ok=%v code=%q err=%v", res.OK, res.ErrorCode, res.Error)
	}
	if _, err := os.Lstat(filepath.Join(dir, "sub", "c.txt")); err != nil {
		t.Fatalf("an overlapping list must delete nothing: %v", err)
	}

	res = app.executeTool(ctx, cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Paths: []string{"sub/c.txt", "sub/c.txt"}}))
	if res.OK || res.ErrorCode != "E_DUPLICATE_PATH" {
		t.Fatalf("a repeated path must be refused, got ok=%v code=%q err=%v", res.OK, res.ErrorCode, res.Error)
	}

	tooMany := make([]string, toolshared.DeletePathListLimit+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("f%d.txt", i)
	}
	for _, tc := range []struct {
		name string
		args []byte
	}{
		{name: "both spellings", args: encodedToolArgs(t, DeletePathRequest{Path: "a.txt", Paths: []string{"b.txt"}})},
		{name: "neither spelling", args: encodedToolArgs(t, map[string]any{"recursive": false})},
		{name: "above the limit", args: encodedToolArgs(t, DeletePathRequest{Paths: tooMany})},
	} {
		res := app.executeTool(ctx, cfg, "s-1", "delete", tc.args)
		if res.OK || res.ErrorCode != "E_BAD_ARGS" {
			t.Fatalf("%s must be refused by the gate, got ok=%v code=%q err=%v", tc.name, res.OK, res.ErrorCode, res.Error)
		}
	}

	// The single spelling keeps working, and its result is a one-slot batch.
	res = app.executeTool(ctx, cfg, "s-1", "delete", encodedToolArgs(t, DeletePathRequest{Path: "sub/c.txt"}))
	if !res.OK {
		t.Fatalf("single-path delete must still work, got %v", res.Error)
	}
	single := deleteResultOf(t, res)
	if single.DeletedCount != 1 || len(single.Paths) != 1 || single.Paths[0].Path != "sub/c.txt" || !single.Paths[0].OK {
		t.Fatalf("single-path delete must report one ok slot, got %#v", single)
	}
}

// TestDeletePathsFailureSummaryNamesEveryFailedPath 覆盖「槽位 → error」的转写：
// Wails API（App.DeletePath）的契约是 error，失败槽不发出去就等于 UI 把删失败当
// 成功吞掉。
func TestDeletePathsFailureSummaryNamesEveryFailedPath(t *testing.T) {
	summary := deletePathsFailureSummary(DeletePathsResult{
		Paths: []DeleteResult{
			{Path: "a.txt", OK: true},
			{Path: "b.txt", OK: false, Error: "permission denied"},
		},
		DeletedCount: 1,
		FailedCount:  1,
	})
	for _, want := range []string{"1 of 2", "b.txt", "permission denied"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary must mention %q, got %q", want, summary)
		}
	}
	if got := deletePathsFailureSummary(DeletePathsResult{Paths: []DeleteResult{{Path: "a.txt", OK: true}}}); got != "" {
		t.Fatalf("a fully successful batch has no failure summary, got %q", got)
	}
}

// TestRemoteDeleteToolGatesPathAndPaths holds the remote tool to the same
// declared shape. Only gate-level rejections are exercised: they are decided
// before the SSH target is resolved, so no server is needed.
func TestRemoteDeleteToolGatesPathAndPaths(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	ctx := t.Context()

	tooMany := make([]string, toolshared.DeletePathListLimit+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("f%d.txt", i)
	}
	for _, tc := range []struct {
		name string
		args []byte
	}{
		{name: "neither spelling", args: encodedToolArgs(t, map[string]any{"target": "dev:/srv/app"})},
		{name: "both spellings", args: encodedToolArgs(t, RemoteDeletePathRequest{Target: "dev:/srv/app", Path: "a.txt", Paths: []string{"b.txt"}})},
		{name: "above the limit", args: encodedToolArgs(t, RemoteDeletePathRequest{Target: "dev:/srv/app", Paths: tooMany})},
	} {
		res := app.executeTool(ctx, cfg, "s-1", "remote_delete_path", tc.args)
		if res.OK || res.ErrorCode != "E_BAD_ARGS" {
			t.Fatalf("%s must be refused by the gate, got ok=%v code=%q err=%v", tc.name, res.OK, res.ErrorCode, res.Error)
		}
	}
}

// TestDeleteAbsentSlotsRenderForModel locks the two model-facing shapes of「本来
// 就不存在」：块里的 absent 属性与卡片标题用的短描述。顺手锁住块里是真换行 ——
// 这些属性原本是用反引号里的 `\n` 拼的，模型看到的是字面反斜杠 n。
func TestDeleteAbsentSlotsRenderForModel(t *testing.T) {
	mixed := DeletePathsResult{
		Paths: []DeleteResult{
			{Path: "gone.txt", OK: true, Absent: true},
			{Path: "kept.txt", OK: false, Error: "permission denied", ErrorCode: "E_IO"},
		},
		FailedCount: 1,
		AbsentCount: 1,
	}
	block := renderDeleteResultForModel(mixed)
	for _, want := range []string{
		`<ally-deleted deleted="0" failed="1" absent="1">`,
		`value="gone.txt" ok=true absent="true"`,
		`value="kept.txt" ok=false`,
		`error="permission denied"`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("the model block must contain %q, got %q", want, block)
		}
	}
	if strings.Contains(block, `\n`) || !strings.Contains(block, "\n<path") {
		t.Fatalf("every slot must start on a real new line, got %q", block)
	}

	// 全是空删时不给模型看「deleted」：这次调用什么都没删。
	allAbsent := DeletePathsResult{Paths: []DeleteResult{{Path: "gone.txt", OK: true, Absent: true}}, AbsentCount: 1}
	if got := toolResultSummary("delete", &toolResult{OK: true, Data: allAbsent}); got != "already absent" {
		t.Fatalf("an all-absent delete must read as already absent, got %q", got)
	}
}
