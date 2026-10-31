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
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseGitStatusV2(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want GitStatus
	}{
		{
			name: "clean repo with upstream ahead/behind",
			out:  "# branch.oid abc123\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +2 -1\x00",
			want: GitStatus{IsRepo: true, Branch: "main", Ahead: 2, Behind: 1},
		},
		{
			name: "mixed worktree states plus untracked",
			out: "# branch.head main\x00" +
				"1 .M N... 1 2 3 4 5 file-mod.txt\x00" +
				"1 A. N... 1 2 3 4 5 file-add.txt\x00" +
				"1 D. N... 1 2 3 4 5 file-del.txt\x00" +
				"? file-new.txt\x00",
			want: GitStatus{IsRepo: true, Branch: "main", Modified: 1, Added: 2, Deleted: 1},
		},
		{
			name: "rename skips original path token",
			out: "# branch.head main\x00" +
				"2 R. N... 1 2 3 4 5 new-name\x00old-name\x00" +
				"1 .M N... 1 2 3 4 5 other.txt\x00",
			want: GitStatus{IsRepo: true, Branch: "main", Modified: 2},
		},
		{
			name: "detached head without upstream",
			out:  "# branch.oid abc\x00# branch.head (detached)\x00",
			want: GitStatus{IsRepo: true, Branch: "(detached)"},
		},
		{
			name: "empty output keeps repo flag with zero counts",
			out:  "",
			want: GitStatus{IsRepo: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitStatusV2(tt.out)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseGitStatusV2 = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestComputeMultiRepoGitStatusEmptyOrNonRepo(t *testing.T) {
	tempDir := t.TempDir()
	// Test on an empty folder with no repos
	ctx := context.Background()
	st := computeMultiRepoGitStatusWithContext(ctx, discoverSubRepos(ctx, tempDir))
	if st.IsRepo {
		t.Fatalf("expected IsRepo=false for empty dir, got %#v", st)
	}
}

// A worktree container can also be a detached worktree itself. Its ignored
// branch directories must remain browsable and independently selectable in Git.
func TestGitParentRepoIncludesIgnoredLinkedWorktrees(t *testing.T) {
	redirectAppStateDir(t)
	root := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runGitTestCommand(t, root, "init", "--initial-branch=container")
	runGitTestCommand(t, root, "config", "user.name", "Ally Test")
	runGitTestCommand(t, root, "config", "user.email", "ally-test@example.com")
	runGitTestCommand(t, root, "config", "commit.gpgsign", "false")
	runGitTestCommand(t, root, "config", "core.hooksPath", t.TempDir())
	write(filepath.Join(root, ".gitignore"), "/main/\n/mygo/\n")
	write(filepath.Join(root, "tracked.txt"), "before\n")
	runGitTestCommand(t, root, "add", ".")
	runGitTestCommand(t, root, "commit", "-m", "initial")
	runGitTestCommand(t, root, "checkout", "--detach")
	for _, branch := range []string{"main", "mygo"} {
		runGitTestCommand(t, root, "worktree", "add", "-b", branch, filepath.Join(root, branch))
		write(filepath.Join(root, branch, "tracked.txt"), "after "+branch+"\n")
	}
	write(filepath.Join(root, "parent-local.txt"), "parent change\n")
	app := NewApp()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status := computeGitStatusWithContext(ctx, root, app.discoverGitRepos)
	if !status.IsMultiRepo || len(status.Repos) != 3 || status.Modified != 2 || status.Added != 1 {
		t.Fatalf("missing parent/worktree changes: %#v", status)
	}
	for _, repo := range status.Repos {
		diff := app.GetGitDiff(root, repo.Path)
		if !diff.IsRepo || diff.Error != "" || len(diff.Files) != 1 {
			t.Fatalf("%s diff: %#v", repo.Path, diff)
		}
		if repo.Path != "." && (repo.Branch != repo.Path || diff.Branch != repo.Path || diff.Files[0].Path != "tracked.txt") {
			t.Fatalf("wrong worktree: %#v %#v", repo, diff)
		}
	}
	for _, ignored := range []bool{false, true} {
		listing, err := app.listFilesWithConfig(ConfigState{Workspace: root}, ListFilesRequest{Path: ".", MaxDepth: 1, IncludeHidden: true, IncludeIgnored: ignored})
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, entry := range listing.Entries {
			found[entry.Name] = true
		}
		if found["main"] != ignored || found["mygo"] != ignored {
			t.Fatalf("includeIgnored=%v: %#v", ignored, listing.Entries)
		}
	}
}
