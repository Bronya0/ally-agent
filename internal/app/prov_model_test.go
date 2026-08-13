// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"strings"
	"testing"

	legacyopenai "github.com/sashabaranov/go-openai"
)

func TestOpenAIResponsesGPT56PromptCacheRequest(t *testing.T) {
	cacheKey := openAIResponsesPromptCacheKey("session-1")
	cfg := ConfigState{
		APIFormat:               apiFormatOpenAIResponses,
		BaseURL:                 defaultOpenAIResponsesURL,
		MaxTokens:               1024,
		responsesPromptCacheKey: cacheKey,
	}
	body, explicitPromptCache := buildOpenAIResponsesRequest(cfg, "gpt-5.6-sol", []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "stable system context"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "inspect the cache"},
	}, nil)
	if !explicitPromptCache {
		t.Fatal("GPT-5.6 official Responses request did not enable explicit prompt caching")
	}

	request := marshalResponsesRequest(t, body)
	if got, _ := request["prompt_cache_key"].(string); got != cacheKey {
		t.Fatalf("prompt_cache_key = %q, want %q", got, cacheKey)
	}
	items, ok := request["input"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("input = %#v, want cache anchor followed by request content", request["input"])
	}
	first, ok := items[0].(map[string]any)
	if !ok || first["role"] != "developer" {
		t.Fatalf("first input item = %#v, want developer cache anchor", items[0])
	}
	if err := applyOpenAIResponsesPromptCacheOptions(request); err != nil {
		t.Fatalf("applyOpenAIResponsesPromptCacheOptions() error = %v", err)
	}
	options, ok := request["prompt_cache_options"].(map[string]string)
	if !ok || options["mode"] != "explicit" {
		t.Fatalf("prompt_cache_options = %#v, want explicit mode", request["prompt_cache_options"])
	}
	if !responsesRequestHasCacheAnchor(request) {
		t.Fatalf("request input did not contain the expected cache anchor: %#v", request["input"])
	}
}

func TestOpenAIResponsesPromptCacheKeyFollowsCodexForCompatibleEndpoints(t *testing.T) {
	cacheKey := openAIResponsesPromptCacheKey("session-1")
	tests := []struct {
		name            string
		cfg             ConfigState
		model           string
		wantKey         bool
		wantExplicit    bool
		wantCacheAnchor bool
	}{
		{
			name:            "older official model",
			cfg:             ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: defaultOpenAIResponsesURL, responsesPromptCacheKey: cacheKey},
			model:           "gpt-5.5",
			wantKey:         true,
			wantCacheAnchor: false,
		},
		{
			name:            "custom compatible endpoint",
			cfg:             ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: "https://api.deepseek.com/v1", responsesPromptCacheKey: cacheKey},
			model:           "gpt-5.6",
			wantKey:         true,
			wantCacheAnchor: false,
		},
		{
			name:            "official GPT-5.6",
			cfg:             ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: defaultOpenAIResponsesURL, responsesPromptCacheKey: cacheKey},
			model:           "gpt-5.6",
			wantKey:         true,
			wantExplicit:    true,
			wantCacheAnchor: true,
		},
		{
			name:  "missing session key",
			cfg:   ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: defaultOpenAIResponsesURL},
			model: "gpt-5.6",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, explicitPromptCache := buildOpenAIResponsesRequest(tt.cfg, tt.model, []legacyopenai.ChatCompletionMessage{{
				Role:    legacyopenai.ChatMessageRoleUser,
				Content: "hello",
			}}, nil)
			if explicitPromptCache != tt.wantExplicit {
				t.Fatalf("explicit prompt caching = %v, want %v", explicitPromptCache, tt.wantExplicit)
			}
			request := marshalResponsesRequest(t, body)
			gotKey, hasKey := request["prompt_cache_key"].(string)
			if hasKey != tt.wantKey || (tt.wantKey && gotKey != cacheKey) {
				t.Fatalf("prompt_cache_key = %q (present=%v), want %q (present=%v)", gotKey, hasKey, cacheKey, tt.wantKey)
			}
			if responsesRequestHasCacheAnchorText(request) != tt.wantCacheAnchor {
				t.Fatalf("cache anchor present = %v, want %v: %#v", responsesRequestHasCacheAnchorText(request), tt.wantCacheAnchor, request["input"])
			}
			if _, ok := request["prompt_cache_options"]; ok {
				t.Fatalf("prompt_cache_options must be injected only by the explicit GPT-5.6 stream path: %#v", request)
			}
		})
	}
}

func TestModelUsageFromResponsesEventSupportsCompatibleCacheFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want modelUsage
	}{
		{
			name: "standard responses usage",
			raw:  `{"response":{"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":800},"output_tokens":8}}}`,
			want: modelUsage{PromptTokens: 1000, CompletionTokens: 8, CacheHitTokens: 800, CacheMissTokens: 200},
		},
		{
			name: "chat shaped usage from compatible gateway",
			raw:  `{"response":{"usage":{"prompt_tokens":900,"prompt_tokens_details":{"cached_tokens":600},"completion_tokens":7}}}`,
			want: modelUsage{PromptTokens: 900, CompletionTokens: 7, CacheHitTokens: 600, CacheMissTokens: 300},
		},
		{
			name: "top level cache counters",
			raw:  `{"response":{"usage":{"input_tokens":700,"prompt_cache_hit_tokens":500,"prompt_cache_miss_tokens":200,"output_tokens":6}}}`,
			want: modelUsage{PromptTokens: 700, CompletionTokens: 6, CacheHitTokens: 500, CacheMissTokens: 200},
		},
		{
			name: "usage at event root",
			raw:  `{"usage":{"input_tokens":300,"cached_input_tokens":120,"output_tokens":4}}`,
			want: modelUsage{PromptTokens: 300, CompletionTokens: 4, CacheHitTokens: 120, CacheMissTokens: 180},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := modelUsageFromResponsesEvent([]byte(tt.raw))
			if got == nil {
				t.Fatal("modelUsageFromResponsesEvent() returned nil")
			}
			if *got != tt.want {
				t.Fatalf("usage = %#v, want %#v", *got, tt.want)
			}
		})
	}
}

func TestOpenAIResponsesPromptCacheKeyIsOpaqueAndStable(t *testing.T) {
	first := openAIResponsesPromptCacheKey("session-1")
	if first == "" || first != openAIResponsesPromptCacheKey("session-1") {
		t.Fatalf("cache key is not stable: %q", first)
	}
	if first == openAIResponsesPromptCacheKey("session-2") {
		t.Fatal("different sessions shared a cache key")
	}
	if strings.Contains(first, "session-1") {
		t.Fatalf("cache key leaked the session ID: %q", first)
	}
	if got := openAIResponsesPromptCacheKey(" "); got != "" {
		t.Fatalf("empty session cache key = %q, want empty", got)
	}
}

func TestApplyOpenAIResponsesPromptCacheOptionsRequiresAnchor(t *testing.T) {
	err := applyOpenAIResponsesPromptCacheOptions(map[string]any{
		"input": []any{map[string]any{
			"role":    "developer",
			"content": []any{map[string]any{"type": "input_text", "text": "ordinary content"}},
		}},
	})
	if err == nil {
		t.Fatal("applyOpenAIResponsesPromptCacheOptions() succeeded without an anchor")
	}
}

func marshalResponsesRequest(t *testing.T, body any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	request := map[string]any{}
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return request
}

func responsesRequestHasCacheAnchor(request map[string]any) bool {
	items, _ := request["input"].([]any)
	for _, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		if item["role"] != "developer" {
			continue
		}
		parts, _ := item["content"].([]any)
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if part["type"] == "input_text" && part["text"] == openAIResponsesPromptCacheAnchorText {
				switch breakpoint := part["prompt_cache_breakpoint"].(type) {
				case map[string]string:
					return breakpoint["mode"] == "explicit"
				case map[string]any:
					return breakpoint["mode"] == "explicit"
				}
			}
		}
	}
	return false
}

func responsesRequestHasCacheAnchorText(request map[string]any) bool {
	items, _ := request["input"].([]any)
	for _, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		parts, _ := item["content"].([]any)
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if part["type"] == "input_text" && part["text"] == openAIResponsesPromptCacheAnchorText {
				return true
			}
		}
	}
	return false
}
