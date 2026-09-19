// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3.0. See the LICENSE file for details.
package app

import (
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// TestMarkAnthropicPromptCacheBreakpointsUsesRetentionTTL covers the breakpoint
// marker itself: all three breakpoints carry the provider-default ttl.
func TestMarkAnthropicPromptCacheBreakpointsUsesRetentionTTL(t *testing.T) {
	build := func() anthropic.MessageNewParams {
		params := anthropic.MessageNewParams{
			System: []anthropic.TextBlockParam{{Text: "system"}},
			Tools:  []anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{Name: "read"}}},
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("question")),
			},
		}
		markAnthropicPromptCacheBreakpoints(&params)
		return params
	}

	built := build()
	if got := built.System[0].CacheControl.TTL; got != "5m" {
		t.Fatalf("system breakpoint ttl = %q, want 5m", got)
	}
	if tool := built.Tools[0].OfTool; tool == nil || tool.CacheControl.TTL != "5m" {
		t.Fatalf("tool breakpoint ttl = %#v, want 5m", tool)
	}
	if block := built.Messages[0].Content[0]; block.OfText == nil || block.OfText.CacheControl.TTL != "5m" {
		t.Fatalf("message breakpoint ttl = %#v, want 5m", block.OfText)
	}
}

// TestSubagentCacheConfigSplitsRouteFromReplayScope pins the two identities a
// sub-agent run needs: a lane-stable cache route — repeated delegations by one
// session reuse the cached header, the shape pi gives its own lanes
// (`<session id>:<lane name>`) — and a run-local replay scope, because a ledger
// turn is matched by its first tool-call id and two concurrent runs can mint the
// same id.
func TestSubagentCacheConfigSplitsRouteFromReplayScope(t *testing.T) {
	base := ConfigState{APIFormat: apiFormatOpenAIResponses, Model: "gpt-5.6"}
	first := subagentCacheConfig(base, "sess-1", "run-1")
	second := subagentCacheConfig(base, "sess-1", "run-2")

	wantRoute := openAIResponsesPromptCacheKey("sess-1" + subagentCacheLane)
	if first.responsesPromptCacheKey != wantRoute || second.responsesPromptCacheKey != wantRoute {
		t.Fatalf("runs of one session must share the lane route: %q / %q, want %q",
			first.responsesPromptCacheKey, second.responsesPromptCacheKey, wantRoute)
	}
	if first.reasoningScope == "" || first.reasoningScope == second.reasoningScope {
		t.Fatalf("concurrent runs must not share a replay scope: %q / %q", first.reasoningScope, second.reasoningScope)
	}
	if want := subagentReasoningScope("run-1"); first.reasoningScope != want {
		t.Fatalf("replay scope = %q, want %q", first.reasoningScope, want)
	}
	if key := reasoningReplayKey(first, base.Model); !strings.Contains(key, reasoningStashScope(subagentReasoningScope("run-1"))) {
		t.Fatalf("replay key %q must use the run-local scope", key)
	}
	if reasoningReplayKey(first, base.Model) == reasoningReplayKey(second, base.Model) {
		t.Fatal("two runs derived the same replay key")
	}
	// Without a parent session there is no lane to share.
	if orphan := subagentCacheConfig(base, "  ", "run-3"); orphan.responsesPromptCacheKey != subagentReasoningScope("run-3") {
		t.Fatalf("orphan route = %q, want the run-local identity", orphan.responsesPromptCacheKey)
	}
}

// TestReasoningStashClearScopeDropsOnlyItsScope covers the release path of a
// finished sub-agent run: its run-local ledger must not keep occupying the bounded
// stash, and an empty scope must never clear the stateless callers' bucket.
func TestReasoningStashClearScopeDropsOnlyItsScope(t *testing.T) {
	stash := newReasoningStash()
	runCfg := subagentCacheConfig(ConfigState{APIFormat: apiFormatOpenAIResponses, Model: "gpt-5.6"}, "sess", "run-1")
	runKey := reasoningReplayKey(runCfg, runCfg.Model)
	mainCfg := ConfigState{
		APIFormat:               apiFormatOpenAIResponses,
		Model:                   "gpt-5.6",
		responsesPromptCacheKey: openAIResponsesPromptCacheKey("sess"),
	}
	mainKey := reasoningReplayKey(mainCfg, mainCfg.Model)
	if runKey == mainKey {
		t.Fatalf("a sub-agent run must not write into the session's ledger: %q", runKey)
	}
	stash.appendTurn(runKey, reasoningTurn{callIDs: []string{"c1"}, responses: []responsesReasoningItem{{ID: "rs_1"}}})
	stash.appendTurn(mainKey, reasoningTurn{callIDs: []string{"c2"}, responses: []responsesReasoningItem{{ID: "rs_2"}}})

	stash.clearScope(subagentReasoningScope("run-1"))
	if got := stash.get(runKey); got != nil {
		t.Fatalf("the finished run's ledger must be released, got %+v", got)
	}
	if got := stash.get(mainKey); got == nil {
		t.Fatal("the session's own ledger must survive")
	}
	stash.clearScope("")
	if got := stash.get(mainKey); got == nil {
		t.Fatal("an empty scope must clear nothing")
	}
}
