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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	oa "github.com/openai/openai-go"
	legacyopenai "github.com/sashabaranov/go-openai"
)

func TestMarkAnthropicPromptCacheBreakpointsSkipsTailInjections(t *testing.T) {
	params := anthropic.MessageNewParams{
		System: []anthropic.TextBlockParam{{Text: "system"}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("question")),
			anthropic.NewAssistantMessage(anthropic.NewToolUseBlock("t1", map[string]any{"a": 1}, "grep")),
			anthropic.NewUserMessage(anthropic.NewToolResultBlock("t1", `{"ok":true}`, false)),
			anthropic.NewUserMessage(anthropic.NewTextBlock("<ally-context-budget>\nWindow: 1000 tokens\n</ally-context-budget>")),
		},
	}
	markAnthropicPromptCacheBreakpoints(&params)

	if params.System[0].CacheControl.TTL == "" {
		t.Fatalf("expected cache_control breakpoint on last system block")
	}
	toolResult := params.Messages[2].Content[len(params.Messages[2].Content)-1]
	if toolResult.OfToolResult == nil || toolResult.OfToolResult.CacheControl.TTL == "" {
		t.Fatalf("expected cache_control breakpoint on last block of last real message")
	}
	injection := params.Messages[3].Content[0]
	if injection.OfText == nil || injection.OfText.CacheControl.TTL != "" {
		t.Fatalf("transient tail injection must stay outside the cached prefix")
	}

	// The full conversion path must land the breakpoint the same way:
	// buildAnthropicMessages merges tool results and keeps the injection last.
	_, converted := buildAnthropicMessages([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "system"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "question"},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "t1", Content: `{"ok":true}`},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "<ally-context-budget>\nWindow: 1000 tokens\n</ally-context-budget>"},
	})
	if len(converted) != 3 {
		t.Fatalf("expected 3 converted messages, got %d", len(converted))
	}
	if !anthropicMessageIsTransientInjection(converted[2]) {
		t.Fatalf("expected converted tail message to be detected as transient injection")
	}
	if anthropicMessageIsTransientInjection(converted[1]) {
		t.Fatalf("tool-result message must not be detected as transient injection")
	}
}

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

func TestNormalizeToolCallsPreservesLiveTruncatedEditForSalvage(t *testing.T) {
	input := []legacyopenai.ToolCall{
		// Stream cut off mid-arguments: the accumulated string is not JSON.
		{ID: "a", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: "edit", Arguments: `{"files":[{"path":"AGENTS.md","changes":[{"oldText":"sandbox is`}},
		{ID: "b", Function: legacyopenai.FunctionCall{Name: "list_files"}},
		{ID: "c", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: "read", Arguments: `{"files":[]}`}},
	}
	out := normalizeToolCalls(input)

	if out[0].Function.Arguments != input[0].Function.Arguments {
		t.Fatalf("live truncated edit prefix must survive for salvage: %q", out[0].Function.Arguments)
	}
	if out[1].Function.Arguments != "{}" || out[1].Type != legacyopenai.ToolTypeFunction {
		t.Fatalf("empty arguments/type normalization regressed: %#v", out[1])
	}
	if out[2].Function.Arguments != `{"files":[]}` {
		t.Fatalf("valid arguments must pass through unchanged: %q", out[2].Function.Arguments)
	}
	if input[0].Function.Arguments == truncatedToolCallArguments {
		t.Fatal("normalizeToolCalls must not mutate its input")
	}
}

func TestCollapseRepeatedName(t *testing.T) {
	cases := []struct{ in, want string }{
		{strings.Repeat("http_request", 7), "http_request"}, // the observed relay artifact
		{"readread", "read"},
		{"askaskask", "ask"},
		{"read", "read"},             // single fold, unchanged
		{"list_files", "list_files"}, // never a whole-number repetition
		{"mcp__fs__read_file", "mcp__fs__read_file"},
		{"abab", "abab"},                           // period < 3, left alone
		{"edit_fileedit_fil", "edit_fileedit_fil"}, // not an exact repetition
		{"", ""},
		// 未知工具名即使恰好是整周期重复也不折叠：重复单元不在已知集合里，
		// 宁可放过不可误伤（真实的 MCP 工具名不会被改成一半）。mcp__ 前缀的
		// 名字除外：前缀检查让任意 mcp__ 单元算“已知”，这里只能靠单元本身
		// 出现在已知工具里才折叠，未知单元不折。
		{"xyzxyzxyz", "xyzxyzxyz"},
		{"readwebweb", "readwebweb"}, // "web" 重复但单元不是已知工具名
	}
	for _, c := range cases {
		if got := collapseRepeatedName(c.in); got != c.want {
			t.Fatalf("collapseRepeatedName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeToolCallsCollapsesRepeatedNames(t *testing.T) {
	input := []legacyopenai.ToolCall{
		{ID: "a", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: strings.Repeat("http_request", 7), Arguments: `{"url":"https://example.com"}`}},
	}
	out := normalizeToolCalls(input)
	if out[0].Function.Name != "http_request" {
		t.Fatalf("repeated name was not collapsed: %q", out[0].Function.Name)
	}
	if out[0].Function.Arguments != `{"url":"https://example.com"}` {
		t.Fatalf("valid arguments must pass through: %q", out[0].Function.Arguments)
	}
}

func TestIsProvider400ErrorUsesTypedStatusCodes(t *testing.T) {
	// 类型化判断优先：各 SDK 的错误类型（含被 providerRequestError 包装后的
	// 错误链）直接读状态码，不再依赖错误文本恰含 "400" 子串。
	legacy400 := &legacyopenai.RequestError{HTTPStatusCode: http.StatusBadRequest, Err: fmt.Errorf("bad request")}
	if !isProvider400Error(wrapProviderRequestError(legacy400)) {
		t.Fatal("go-openai RequestError with HTTPStatusCode 400 must be detected")
	}
	legacy500 := &legacyopenai.RequestError{HTTPStatusCode: http.StatusInternalServerError, Err: fmt.Errorf("server error")}
	if isProvider400Error(wrapProviderRequestError(legacy500)) {
		t.Fatal("500 must not be treated as a 400")
	}
	// 错误文本里恰好引用 "400 Bad Request" 但状态码不是 400，不应触发 sanitize 重试。
	// （Response/Request 是 SDK Error() 解引用的必填字段，生产路径必有值。）
	oa500 := &oa.Error{StatusCode: http.StatusInternalServerError, Message: "upstream returned 400 Bad Request for a nested call", Request: httptest.NewRequest(http.MethodGet, "https://api.example.com", nil), Response: &http.Response{StatusCode: http.StatusInternalServerError}}
	if isProvider400Error(oa500) {
		t.Fatal("500 with a 400-mentioning message must not be treated as a 400")
	}
	anthropic400 := &anthropic.Error{StatusCode: http.StatusBadRequest}
	if !isProvider400Error(anthropic400) {
		t.Fatal("anthropic Error with StatusCode 400 must be detected")
	}
	// 中继丢掉状态码、仅在文本里转述 400：字符串兜底仍生效。
	if !isProvider400Error(errors.New("relay says: status code: 400")) {
		t.Fatal("string fallback for status code: 400 must still work")
	}
	if isProvider400Error(errors.New("totally unrelated failure")) {
		t.Fatal("unrelated errors must not match")
	}
}

func TestMergeToolCallDeltasDedupesResentNames(t *testing.T) {
	// The relay pattern from the field: every tool_calls delta carries the
	// full function name (and id) again alongside each arguments chunk.
	// Appending verbatim produced "http_request" x7 and an unknown-tool error.
	var toolCalls []legacyopenai.ToolCall
	index := 0
	deltas := []legacyopenai.ToolCall{
		{Index: &index, ID: "call_1", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: "http_request", Arguments: `{"ur`}},
		{Index: &index, ID: "call_1", Function: legacyopenai.FunctionCall{Name: "http_request", Arguments: `l":"ht`}},
		{Index: &index, ID: "call_1", Function: legacyopenai.FunctionCall{Name: "http_request", Arguments: `tps:`}},
	}
	for _, d := range deltas {
		mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{d})
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected one accumulated tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "http_request" {
		t.Fatalf("re-sent names must dedupe to one, got %q", toolCalls[0].Function.Name)
	}
	if toolCalls[0].ID != "call_1" {
		t.Fatalf("re-sent ids must dedupe to one, got %q", toolCalls[0].ID)
	}
	if toolCalls[0].Function.Arguments != `{"url":"ht`+`tps:` {
		t.Fatalf("argument chunks must still concatenate, got %q", toolCalls[0].Function.Arguments)
	}
}

func TestMergeToolCallDeltasSeparatesSameIndexDifferentIDs(t *testing.T) {
	// 同一 Index、两个不同 ID 但工具名都不在已知集合（两个不同的 MCP 工具）：
	// 拼接名恰好仍带 mcp__ 前缀，旧启发式会误判为“渐进式分片”而把两个调用
	// 的参数拼在一起；不同的非空 ID 是两个调用的直接证据，必须拆分。
	var toolCalls []legacyopenai.ToolCall
	index := 0
	mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{
		{Index: &index, ID: "call_m1", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: "mcp__fs__read_file", Arguments: `{"path":"a"`}},
	})
	mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{
		{Index: &index, ID: "call_m2", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: "mcp__fs__write_file", Arguments: `{"path":"b"}`}},
	})
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 separate tool calls, got %d: %#v", len(toolCalls), toolCalls)
	}
	if toolCalls[0].Function.Name != "mcp__fs__read_file" || toolCalls[0].Function.Arguments != `{"path":"a"` {
		t.Fatalf("first call must keep its name and args: %#v", toolCalls[0])
	}
	if toolCalls[1].Function.Name != "mcp__fs__write_file" || toolCalls[1].ID != "call_m2" {
		t.Fatalf("second call must be appended with its own id: %#v", toolCalls[1])
	}
}

func TestMergeToolCallDeltasAppendsProgressiveNameChunks(t *testing.T) {
	// Standard-compliant progressive chunking (name split across deltas)
	// must keep appending.
	var toolCalls []legacyopenai.ToolCall
	index := 0
	mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{{Index: &index, ID: "call_1", Function: legacyopenai.FunctionCall{Name: "http_"}}})
	mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{{Index: &index, Function: legacyopenai.FunctionCall{Name: "request"}}})
	if toolCalls[0].Function.Name != "http_request" {
		t.Fatalf("progressive name chunks must append, got %q", toolCalls[0].Function.Name)
	}
}

func TestMergeToolCallDeltasSeparatesDifferentToolNames(t *testing.T) {
	// The exact relay pattern that produced "readlist_files": two different
	// tool calls sent with the same Index (or both without Index). Appending
	// the second name to the first produced a garbage name that failed
	// dispatch and poisoned the session.
	var toolCalls []legacyopenai.ToolCall
	index := 0
	mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{
		{Index: &index, ID: "call_a", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: "read", Arguments: `{"files":[{"path":"a.txt"}]}`}},
	})
	mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{
		{Index: &index, ID: "call_b", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: "list_files", Arguments: `{}`}},
	})
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 separate tool calls, got %d: %#v", len(toolCalls), toolCalls)
	}
	if toolCalls[0].Function.Name != "read" || toolCalls[0].ID != "call_a" {
		t.Fatalf("first call must keep its name and id: %#v", toolCalls[0])
	}
	if toolCalls[1].Function.Name != "list_files" || toolCalls[1].ID != "call_b" {
		t.Fatalf("second call must be separate: %#v", toolCalls[1])
	}
}

func TestWrapProviderRequestError(t *testing.T) {
	// The exact relay failure from the field: a 400 whose body has no
	// message/error field — go-openai formats its nil Err as "%!s(<nil>)".
	nilErrBody := []byte(`{"object":"error","model":"deepseek-v4-flash"}`)
	raw := &legacyopenai.RequestError{
		HTTPStatus:     "400 Bad Request",
		HTTPStatusCode: 400,
		Err:            nil,
		Body:           nilErrBody,
	}
	wrapped := wrapProviderRequestError(raw)
	msg := wrapped.Error()
	if strings.Contains(msg, "%!s(<nil>)") {
		t.Fatalf("nil-message artifact must be gone, got: %s", msg)
	}
	if !strings.Contains(msg, "status code: 400") || !strings.Contains(msg, `"model":"deepseek-v4-flash"`) {
		t.Fatalf("status and body must survive the wrap, got: %s", msg)
	}
	// The original error must stay reachable for classification/unwrapping.
	var reqErr *legacyopenai.RequestError
	if !errors.As(wrapped, &reqErr) || reqErr.HTTPStatusCode != 400 {
		t.Fatal("wrapped error must keep the RequestError in its chain")
	}

	// Body with a top-level message: the message is extracted for readability.
	withMessage := &legacyopenai.RequestError{
		HTTPStatus:     "429 Too Many Requests",
		HTTPStatusCode: 429,
		Body:           []byte(`{"message":"rate limited, retry later"}`),
	}
	msg = wrapProviderRequestError(withMessage).Error()
	if !strings.Contains(msg, "rate limited, retry later") {
		t.Fatalf("message must be extracted from body, got: %s", msg)
	}
	// 429 keyword must survive for retry classification.
	if !shouldRetryLLMError(wrapProviderRequestError(withMessage)) {
		t.Fatal("retry classification must still see the 429")
	}

	// Body with error.message (standard OpenAI shape): extracted too.
	standard := &legacyopenai.RequestError{
		HTTPStatus:     "401 Unauthorized",
		HTTPStatusCode: 401,
		Body:           []byte(`{"error":{"message":"invalid api key","type":"auth"}}`),
	}
	msg = wrapProviderRequestError(standard).Error()
	if !strings.Contains(msg, "invalid api key") {
		t.Fatalf("error.message must be extracted, got: %s", msg)
	}

	// Errors already carrying a message (Err != nil) pass through unchanged.
	inner := errors.New("quota exceeded")
	withErr := &legacyopenai.RequestError{
		HTTPStatus:     "402 Payment Required",
		HTTPStatusCode: 402,
		Err:            inner,
		Body:           []byte(`{"irrelevant":true}`),
	}
	if got := wrapProviderRequestError(withErr); got != withErr {
		t.Fatalf("RequestError with non-nil Err must pass through, got %#v", got)
	}

	// Non-RequestError errors pass through untouched.
	plain := errors.New("connection reset")
	if got := wrapProviderRequestError(plain); got != plain {
		t.Fatalf("plain errors must pass through, got %#v", got)
	}
	if got := wrapProviderRequestError(nil); got != nil {
		t.Fatalf("nil must stay nil, got %#v", got)
	}
}

func TestMergeToolCallDeltasSkipsDuplicatedArgumentChunks(t *testing.T) {
	// A relay that duplicates the whole first delta re-sends the opening
	// arguments chunk too; appending it verbatim corrupts the JSON.
	var toolCalls []legacyopenai.ToolCall
	index := 0
	first := legacyopenai.ToolCall{Index: &index, ID: "call_1", Function: legacyopenai.FunctionCall{Name: "read", Arguments: `{"fi`}}
	mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{first})
	mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{first})
	mergeToolCallDeltas(&toolCalls, []legacyopenai.ToolCall{{Index: &index, Function: legacyopenai.FunctionCall{Name: "read", Arguments: `les":[]}`}}})
	if toolCalls[0].Function.Arguments != `{"files":[]}` {
		t.Fatalf("duplicated argument chunks must be skipped, got %q", toolCalls[0].Function.Arguments)
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
