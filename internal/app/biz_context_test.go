package app

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// TestResolveContextAnchorCountsConversationOnly locks the anchor's coverage
// unit: the system prompt and the workspace map are re-prepared for every
// request and never persisted, so they must not shift which messages a recorded
// measurement is considered to cover.
func TestResolveContextAnchorCountsConversationOnly(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system prompt"},
		{Role: openai.ChatMessageRoleUser, Content: "hello"},
		{Role: openai.ChatMessageRoleAssistant, Content: "hi"},
	}
	if got := conversationMessageCount(messages); got != 2 {
		t.Fatalf("conversationMessageCount() = %d, want 2", got)
	}
	_, trailing, ok := resolveContextAnchor(messages, contextAnchor{covered: 2, tokens: 1000})
	if !ok {
		t.Fatal("an anchor covering both conversation messages must apply")
	}
	if trailing != 0 {
		t.Fatalf("trailing = %d, want 0: the assistant reply is inside the measurement", trailing)
	}
}

// TestResolveContextAnchorEstimatesOnlyAddedMessages: everything the provider
// already counted is reported as measured; only the messages added afterwards
// are estimated on top (pi: estimateContextTokens).
func TestResolveContextAnchorEstimatesOnlyAddedMessages(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hello"},
		{Role: openai.ChatMessageRoleAssistant, Content: "hi"},
		{Role: openai.ChatMessageRoleTool, Content: `{"ok":true,"data":"tool output"}`},
	}
	measured, trailing, ok := resolveContextAnchor(messages, contextAnchor{covered: 2, tokens: 4000})
	if !ok || measured.tokens != 4000 || measured.covered != 2 {
		t.Fatalf("resolveContextAnchor() measured = %#v, ok = %v", measured, ok)
	}
	if want := messageTokens(messages[2]) + reasoningTokens(messages[2]); trailing != want {
		t.Fatalf("trailing = %d, want %d", trailing, want)
	}
}

// TestResolveContextAnchorDiscardsRewrittenConversation: compaction and
// truncation replace the conversation, and a list shorter than the recorded
// coverage cannot be described by the anchor any more. The caller must fall back
// to the text estimate instead of under-counting the request.
func TestResolveContextAnchorDiscardsRewrittenConversation(t *testing.T) {
	messages := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "[summary]"}}
	if _, _, ok := resolveContextAnchor(messages, contextAnchor{covered: 6, tokens: 90000}); ok {
		t.Fatal("an anchor covering more messages than exist must be discarded")
	}
	if _, _, ok := resolveContextAnchor(messages, contextAnchor{}); ok {
		t.Fatal("a zero anchor must not apply")
	}
}

// TestFinalizeContextBreakdownPrefersMeasurement locks the total rule: the
// category sum is the fallback, and a provider measurement replaces the estimate
// for everything it covered.
func TestFinalizeContextBreakdownPrefersMeasurement(t *testing.T) {
	breakdown := ContextBreakdown{SystemPrompt: 100, ToolSchemas: 50, UserMessages: 10}
	finalizeContextBreakdownTotal(&breakdown)
	if breakdown.Estimated != 160 || breakdown.Total != 160 {
		t.Fatalf("estimated = %d, total = %d, want 160/160", breakdown.Estimated, breakdown.Total)
	}

	breakdown.MeasuredTokens = 4000
	breakdown.MeasuredMessages = 3
	breakdown.TrailingTokens = 25
	finalizeContextBreakdownTotal(&breakdown)
	if breakdown.Total != 4025 {
		t.Fatalf("total = %d, want 4025 (measurement + trailing messages)", breakdown.Total)
	}
	if breakdown.Estimated != 160 {
		t.Fatalf("estimated = %d, want the unanchored category sum 160", breakdown.Estimated)
	}
}

// TestContextBreakdownUsesProviderMeasurement drives the writer/reader pair: the
// session measurement recorded from a response must survive into the footer
// report, both through the live breakdown and, between runs, through the stored
// conversation.
func TestContextBreakdownUsesProviderMeasurement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	app := NewApp()
	app.initialized = true
	app.config = ConfigState{Workspace: t.TempDir()}
	const sessionID = "anchor-session"
	history := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hello"},
		{Role: openai.ChatMessageRoleAssistant, Content: "hi"},
	}
	app.recordContextAnchor(sessionID, history, &modelUsage{PromptTokens: 900, CompletionTokens: 100})
	app.saveHistory(sessionID, history)

	live := app.getContextBreakdown(sessionID, "")
	if live.MeasuredTokens != 1000 || live.MeasuredMessages != 2 {
		t.Fatalf("measured = %d tokens / %d messages, want 1000/2", live.MeasuredTokens, live.MeasuredMessages)
	}
	if live.TrailingTokens != 0 || live.Total != 1000 {
		t.Fatalf("trailing = %d, total = %d, want 0/1000", live.TrailingTokens, live.Total)
	}

	// Without a live run the footer resolves the anchor against the stored
	// conversation itself; the reported total must not change.
	app.mu.Lock()
	delete(app.liveBreakdown, sessionID)
	app.mu.Unlock()
	stored := app.getContextBreakdown(sessionID, "")
	if stored.MeasuredTokens != 1000 || stored.Total != 1000 {
		t.Fatalf("stored conversation lost the measurement: %#v", stored)
	}

	// A cleared anchor must fall back to the pure text estimate.
	app.clearContextAnchor(sessionID)
	estimated := app.getContextBreakdown(sessionID, "")
	if estimated.MeasuredTokens != 0 || estimated.Total != estimated.Estimated {
		t.Fatalf("cleared anchor must fall back to the estimate, got %#v", estimated)
	}
}

// TestSessionPrefixBreakdownCoversRequestPrefix: the prefix the run loop adds to
// the auto-compaction trigger is the same figure the footer displays — the
// system prompt parts, the workspace map and the plan snapshot.
func TestSessionPrefixBreakdownCoversRequestPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	root := t.TempDir()
	writeToolTestFile(t, root, "main.go", "package main\n")
	app := NewApp()
	app.initialized = true
	app.config = ConfigState{Workspace: root}
	const sessionID = "prefix-session"

	prefix, _ := app.sessionPrefixBreakdown(sessionID, app.config, app.listCachedSkills())
	if prefix <= 0 {
		t.Fatalf("expected a non-zero request prefix, got %d", prefix)
	}

	app.mu.Lock()
	app.todos[sessionID] = []TodoEntry{{Title: "finish the fix", Status: "in_progress"}}
	app.mu.Unlock()

	withPlan, parts := app.sessionPrefixBreakdown(sessionID, app.config, app.listCachedSkills())
	if withPlan <= prefix {
		t.Fatalf("plan snapshot must add to the prefix: %d -> %d", prefix, withPlan)
	}
	found := false
	for _, part := range parts {
		if part.Label == planSnapshotPartLabel {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the plan snapshot part, got %#v", parts)
	}
}

// TestRunBreakdownIncludesRequestPrefix locks the auto-compaction trigger
// accounting: the trigger reads the breakdown total, and the request prefix is
// not part of the message buckets, so the run loop must add it before the total
// is finalized. Without that step the trigger under-counts every request by the
// size of the system prompt and workspace map.
func TestRunBreakdownIncludesRequestPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	app := NewApp()
	app.initialized = true
	app.config = ConfigState{Workspace: t.TempDir()}
	const sessionID = "trigger-session"
	messages := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hello"}}

	// The run loop's per-step sequence, minus the network round trip.
	breakdown := computeLiveBreakdown(messages)
	prefix, _ := app.sessionPrefixBreakdown(sessionID, app.config, app.listCachedSkills())
	if prefix <= 0 {
		t.Fatal("expected a non-zero request prefix")
	}
	breakdown.SystemPrompt = prefix
	app.finalizeSessionBreakdown(sessionID, &breakdown, messages)

	if want := prefix + breakdown.UserMessages; breakdown.Total != want {
		t.Fatalf("trigger total = %d, want %d (prefix %d + messages)", breakdown.Total, want, prefix)
	}
	if breakdown.Total <= computeLiveBreakdown(messages).Total {
		t.Fatal("the trigger total must exceed the message-only estimate")
	}
}
