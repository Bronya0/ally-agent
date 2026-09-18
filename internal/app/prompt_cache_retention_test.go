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

func TestNormalizeCacheRetentionFallsBackToShort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", cacheRetentionShort},
		{"short", cacheRetentionShort},
		{"unknown", cacheRetentionShort},
		{"long", cacheRetentionLong},
		{" LONG ", cacheRetentionLong},
	}
	for _, tt := range tests {
		if got := normalizeCacheRetention(tt.input); got != tt.want {
			t.Fatalf("normalizeCacheRetention(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestMergeConfigKeepsCacheRetention pins the config plumbing: the default is the
// no-op "short", an explicit level survives a save, and an overlay written before
// the field existed (empty) never resets the stored value.
func TestMergeConfigKeepsCacheRetention(t *testing.T) {
	if got := mergeConfig(defaultConfigState(), ConfigState{}).CacheRetention; got != cacheRetentionShort {
		t.Fatalf("default retention = %q, want %q", got, cacheRetentionShort)
	}
	if got := mergeConfig(defaultConfigState(), ConfigState{CacheRetention: cacheRetentionLong}).CacheRetention; got != cacheRetentionLong {
		t.Fatalf("explicit long retention = %q, want %q", got, cacheRetentionLong)
	}
	base := defaultConfigState()
	base.CacheRetention = cacheRetentionLong
	if got := mergeConfig(base, ConfigState{Model: "gpt-5.6"}).CacheRetention; got != cacheRetentionLong {
		t.Fatalf("an overlay without the field reset retention to %q", got)
	}
	if got := mergeConfig(defaultConfigState(), ConfigState{CacheRetention: "garbage"}).CacheRetention; got != cacheRetentionShort {
		t.Fatalf("unknown retention = %q, want %q", got, cacheRetentionShort)
	}
}

// TestCacheRetentionWireSpellings pins the three spellings of one user-facing
// level — the whole point of keeping them in prov_wire_config.go. A drift here
// silently downgrades the setting (or turns an opt-in into a 400).
func TestCacheRetentionWireSpellings(t *testing.T) {
	responses := func(format string, long bool) ConfigState {
		cfg := ConfigState{APIFormat: format, BaseURL: defaultOpenAIResponsesURL}
		if long {
			cfg.CacheRetention = cacheRetentionLong
		}
		return cfg
	}

	if got := anthropicCacheControlTTL(responses(apiFormatAnthropicMessages, false)); got != "5m" {
		t.Fatalf("short retention on Anthropic = %q, want the provider default 5m", got)
	}
	if got := anthropicCacheControlTTL(responses(apiFormatAnthropicMessages, true)); got != "1h" {
		t.Fatalf("long retention on Anthropic = %q, want 1h", got)
	}

	tests := []struct {
		name          string
		cfg           ConfigState
		model         string
		wantRetention string
		wantTTL       string
	}{
		{"short sends nothing", responses(apiFormatOpenAIResponses, false), "gpt-5.5", "", ""},
		{"long before GPT-5.6 uses prompt_cache_retention", responses(apiFormatOpenAIResponses, true), "gpt-5.5", "24h", ""},
		{"long on GPT-5.6 uses prompt_cache_options.ttl", responses(apiFormatOpenAIResponses, true), "gpt-5.6", "", "30m"},
		{"long on GPT-5.6 in Chat format sends nothing", responses(apiFormatOpenAIChat, true), "gpt-5.6", "", ""},
		{"a relay endpoint sends nothing", ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: "https://api.deepseek.com/v1", CacheRetention: cacheRetentionLong}, "gpt-5.5", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openAIExtendedCacheRetention(tt.cfg, tt.model); got != tt.wantRetention {
				t.Fatalf("prompt_cache_retention = %q, want %q", got, tt.wantRetention)
			}
			if got := openAIPromptCacheOptionsTTL(tt.cfg, tt.model); got != tt.wantTTL {
				t.Fatalf("prompt_cache_options.ttl = %q, want %q", got, tt.wantTTL)
			}
		})
	}
}

// TestMarkAnthropicPromptCacheBreakpointsUsesRetentionTTL covers the breakpoint
// marker itself: all three breakpoints carry the ttl of the selected level.
func TestMarkAnthropicPromptCacheBreakpointsUsesRetentionTTL(t *testing.T) {
	build := func(cfg ConfigState) anthropic.MessageNewParams {
		params := anthropic.MessageNewParams{
			System: []anthropic.TextBlockParam{{Text: "system"}},
			Tools:  []anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{Name: "read"}}},
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("question")),
			},
		}
		markAnthropicPromptCacheBreakpoints(&params, cfg)
		return params
	}

	long := build(ConfigState{APIFormat: apiFormatAnthropicMessages, CacheRetention: cacheRetentionLong})
	if got := long.System[0].CacheControl.TTL; got != "1h" {
		t.Fatalf("system breakpoint ttl = %q, want 1h", got)
	}
	if tool := long.Tools[0].OfTool; tool == nil || tool.CacheControl.TTL != "1h" {
		t.Fatalf("tool breakpoint ttl = %#v, want 1h", tool)
	}
	if block := long.Messages[0].Content[0]; block.OfText == nil || block.OfText.CacheControl.TTL != "1h" {
		t.Fatalf("message breakpoint ttl = %#v, want 1h", block.OfText)
	}

	short := build(ConfigState{APIFormat: apiFormatAnthropicMessages})
	if got := short.System[0].CacheControl.TTL; got != "5m" {
		t.Fatalf("system breakpoint ttl = %q, want 5m", got)
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
