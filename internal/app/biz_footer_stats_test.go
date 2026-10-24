// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// footerStatsTestApp: HOME/TMP and the git config already point at the throwaway
// root set up by TestMain, so the app only needs its own private data directory.
func footerStatsTestApp(t *testing.T) *App {
	t.Helper()
	return newIsolatedChatTestApp(t)
}

func TestFooterPrefixPreviewFreezeAndRefresh(t *testing.T) {
	app := footerStatsTestApp(t)
	const sid = "footer-prefix-cache"
	root := app.config.Workspace
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("old project rule"), 0600); err != nil {
		t.Fatal(err)
	}
	app.getContextBreakdown(sid, root)
	if _, ok := app.sessionSystemPrompts[sid]; ok {
		t.Fatal("footer preview froze the prompt")
	}
	if _, ok := app.sessionToolsets[sid]; ok {
		t.Fatal("footer preview froze the tools")
	}
	app.sessionSystemPromptParts(sid, app.config, nil)
	app.sessionWorkspaceMap(sid, app.config)
	app.buildToolsForSession(sid, app.config)
	frozen := app.getContextBreakdown(sid, root)
	if _, ok := app.frozenPrefixStats[sid]; !ok {
		t.Fatal("frozen prefix estimate was not cached")
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("a changed project rule with considerably more text than before"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := app.getContextBreakdown(sid, root); got.SystemPrompt != frozen.SystemPrompt {
		t.Fatal("frozen prompt changed after a disk edit")
	}
	_, parts := app.sessionPrefixBreakdown(sid, app.config, nil)
	parts[0].Tokens = -1
	if _, again := app.sessionPrefixBreakdown(sid, app.config, nil); again[0].Tokens < 0 {
		t.Fatal("caller mutated the prefix cache")
	}
	app.refreshSessionPromptPrefix(sid)
	if _, ok := app.frozenPrefixStats[sid]; ok {
		t.Fatal("compaction retained old token estimates")
	}
	if got := app.getContextBreakdown(sid, root); got.SystemPrompt == frozen.SystemPrompt {
		t.Fatal("refreshed preview did not include the new project rule")
	}
	if err := app.ReleaseSession(sid); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.historyCacheStates[sid]; ok {
		t.Fatal("release retained history cache state")
	}
}

func TestFooterSavedHistoryCacheTracksRewriteAndAnchor(t *testing.T) {
	app := footerStatsTestApp(t)
	const sid = "footer-history-cache"
	old := []openai.ChatCompletionMessage{{Role: "user", Content: "old history"}}
	if err := writeCompressedHistoryPayload(app.historyDiskPaths(sid)[0], old); err != nil {
		t.Fatal(err)
	}
	first := app.getContextBreakdown(sid, app.config.Workspace)
	state := app.historyCacheStates[sid]
	if state == nil || state.breakdown == nil {
		t.Fatal("cold history estimate was not cached")
	}
	cached := state.breakdown
	app.getContextBreakdown(sid, app.config.Workspace)
	if state.breakdown != cached {
		t.Fatal("unchanged history was counted again")
	}
	app.recordContextAnchor(sid, old, &modelUsage{PromptTokens: 100, CompletionTokens: 20})
	if got := app.getContextBreakdown(sid, app.config.Workspace); got.Total != 120 {
		t.Fatalf("anchor was not reflected: %#v", got)
	}
	app.clearContextAnchor(sid)
	if got := app.getContextBreakdown(sid, app.config.Workspace); got.Total != first.Total {
		t.Fatal("cleared anchor remained cached")
	}
	next := []openai.ChatCompletionMessage{{Role: "user", Content: "a much longer rewritten conversation with a different token count"}}
	app.saveHistory(sid, next)
	app.mu.Lock()
	delete(app.liveBreakdown, sid)
	app.mu.Unlock()
	if got := app.getContextBreakdown(sid, app.config.Workspace); got.UserMessages == first.UserMessages {
		t.Fatal("rewritten history reused old estimates")
	}
	if err := app.ReleaseSession(sid); err != nil {
		t.Fatal(err)
	}
	if got := app.loadSessionHistoryCopy(sid); len(got) != 1 || got[0].Content != next[0].Content {
		t.Fatalf("release/reload lost saved history: %#v", got)
	}
}

func TestFooterGitInvalidationAndRepoDiscovery(t *testing.T) {
	t.Parallel()
	app := footerStatsTestApp(t)
	root := app.config.Workspace
	repo := filepath.Join(root, "child")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "--initial-branch=main", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	first := app.GetGitStatus(root)
	if !first.IsMultiRepo || len(first.Repos) != 1 {
		t.Fatalf("subrepository not found: %#v", first)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := app.GetGitStatus(root); got.Added != 0 {
		t.Fatal("expected warm cache before invalidation")
	}
	app.invalidateGitStatus(repo)
	if got := app.GetGitStatus(root); got.Added != 1 {
		t.Fatalf("parent view was not invalidated: %#v", got)
	}
	if len(app.gitRepoDiscovery) != 1 {
		t.Fatal("repository discovery was not cached separately")
	}
}

func TestFooterGitCancelledBatchSkipsRemainingRepos(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repos := make([]gitRepoCandidate, maxSubRepos)
	for i := range repos {
		repos[i] = gitRepoCandidate{name: "repo", path: root}
	}
	started := time.Now()
	if got := computeMultiRepoGitStatusWithContext(ctx, repos); got.IsRepo {
		t.Fatal("cancelled scan returned repository results")
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancelled batch did not stop promptly")
	}
}

func TestFooterGitWaitHasDeadline(t *testing.T) {
	prev := gitStatusWaitBudget
	gitStatusWaitBudget = 200 * time.Millisecond
	t.Cleanup(func() { gitStatusWaitBudget = prev })
	app := footerStatsTestApp(t)
	root := workspaceMapCacheKey(app.config.Workspace)
	// An existing computation never completes; a later caller still has its own deadline.
	app.gitStatusInFlight[root] = make(chan struct{})
	started := time.Now()
	if got := app.GetGitStatus(root); got.IsRepo {
		t.Fatal("timed-out waiter returned a repository")
	}
	if elapsed := time.Since(started); elapsed < 150*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("waiter did not respect its budget: %s", elapsed)
	}
}
