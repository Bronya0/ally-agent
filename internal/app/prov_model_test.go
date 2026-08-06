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
	options, ok := request["prompt_cache_options"].(map[string]any)
	if !ok || options["mode"] != "explicit" {
		t.Fatalf("prompt_cache_options = %#v, want explicit mode", request["prompt_cache_options"])
	}
	if !responsesRequestHasCacheAnchor(request) {
		t.Fatalf("request input did not contain the expected cache anchor: %#v", request["input"])
	}
}

func TestOpenAIResponsesGPT56PromptCacheIsStrictlyGated(t *testing.T) {
	cacheKey := openAIResponsesPromptCacheKey("session-1")
	tests := []struct {
		name  string
		cfg   ConfigState
		model string
	}{
		{
			name:  "older official model",
			cfg:   ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: defaultOpenAIResponsesURL, responsesPromptCacheKey: cacheKey},
			model: "gpt-5.5",
		},
		{
			name:  "custom compatible endpoint",
			cfg:   ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: "https://api.deepseek.com/v1", responsesPromptCacheKey: cacheKey},
			model: "gpt-5.6",
		},
		{
			name:  "chat completions route",
			cfg:   ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: defaultOpenAIResponsesURL, responsesPromptCacheKey: cacheKey},
			model: "gpt-5.6",
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
			if explicitPromptCache {
				t.Fatal("unexpected explicit prompt caching")
			}
			payload, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if strings.Contains(string(payload), "prompt_cache_key") || strings.Contains(string(payload), openAIResponsesPromptCacheAnchorText) {
				t.Fatalf("non-GPT-5.6 Responses cache payload leaked into incompatible request: %s", payload)
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
				breakpoint, _ := part["prompt_cache_breakpoint"].(map[string]any)
				return breakpoint["mode"] == "explicit"
			}
		}
	}
	return false
}
