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
	oa "github.com/openai/openai-go/v3"
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
	// buildAnthropicMessages merges tool results with tail injection into one valid user turn
	// while markAnthropicPromptCacheBreakpoints skips transient blocks and marks the real content.
	_, converted := buildAnthropicMessages([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "system"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "question"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{ID: "t1", Function: legacyopenai.FunctionCall{Name: "grep", Arguments: `{"a":1}`}}}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "t1", Content: `{"ok":true}`},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "<ally-context-budget>\nWindow: 1000 tokens\n</ally-context-budget>"},
	})
	if len(converted) != 3 {
		t.Fatalf("expected 3 converted messages (user -> assistant -> user), got %d", len(converted))
	}
	convParams := anthropic.MessageNewParams{
		Messages: converted,
	}
	markAnthropicPromptCacheBreakpoints(&convParams)
	// Cache breakpoint must land on the tool result block, skipping the trailing transient budget block
	lastUserBlocks := convParams.Messages[2].Content
	if len(lastUserBlocks) != 2 {
		t.Fatalf("expected 2 blocks in last user message, got %d", len(lastUserBlocks))
	}
	if lastUserBlocks[0].OfToolResult == nil || lastUserBlocks[0].OfToolResult.CacheControl.TTL == "" {
		t.Fatalf("expected cache_control breakpoint on tool result block")
	}
	if lastUserBlocks[1].OfText == nil || lastUserBlocks[1].OfText.CacheControl.TTL != "" {
		t.Fatalf("transient tail injection block must stay outside cache control")
	}
}

func TestBuildAnthropicMessagesMergesConsecutiveSameRoleMessages(t *testing.T) {
	// Case 1: ESC interrupt pattern (user question -> user cancelled marker -> user new question)
	// Must merge into a single user message with 3 content blocks to satisfy Anthropic's role alternation.
	system, messages := buildAnthropicMessages([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "system instruction"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "first question"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "<ally-cancelled>\n上一条提问已被用户取消\n</ally-cancelled>"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "new question"},
	})
	if system != "system instruction" {
		t.Fatalf("system = %q, want %q", system, "system instruction")
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 merged user message, got %d", len(messages))
	}
	if messages[0].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("expected user role, got %v", messages[0].Role)
	}
	if len(messages[0].Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(messages[0].Content))
	}
	if messages[0].Content[0].OfText.Text != "first question" {
		t.Fatalf("block 0 text = %q, want %q", messages[0].Content[0].OfText.Text, "first question")
	}
	if !strings.Contains(messages[0].Content[1].OfText.Text, "<ally-cancelled>") {
		t.Fatalf("block 1 text = %q, want containing <ally-cancelled>", messages[0].Content[1].OfText.Text)
	}
	if messages[0].Content[2].OfText.Text != "new question" {
		t.Fatalf("block 2 text = %q, want %q", messages[0].Content[2].OfText.Text, "new question")
	}

	// Case 2: Tool turn interrupted by ESC (user -> assistant tool use -> tool result -> user cancelled -> user new question)
	// Must produce exactly 3 messages: user -> assistant -> user (tool_result + cancelled marker + new question merged)
	_, toolTurnMessages := buildAnthropicMessages([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "read file"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{ID: "call_1", Function: legacyopenai.FunctionCall{Name: "read", Arguments: `{"path":"app.go"}`}}}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "call_1", Content: `{"ok":true,"data":"content"}`},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "<ally-cancelled>\n上一条提问已被用户取消\n</ally-cancelled>"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "cancel and do something else"},
	})
	if len(toolTurnMessages) != 3 {
		t.Fatalf("expected 3 alternating messages (user -> assistant -> user), got %d", len(toolTurnMessages))
	}
	if toolTurnMessages[0].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("msg 0 role = %v, want user", toolTurnMessages[0].Role)
	}
	if toolTurnMessages[1].Role != anthropic.MessageParamRoleAssistant {
		t.Fatalf("msg 1 role = %v, want assistant", toolTurnMessages[1].Role)
	}
	if toolTurnMessages[2].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("msg 2 role = %v, want user", toolTurnMessages[2].Role)
	}
	// Last user message must merge tool_result, cancelled marker, and new question
	lastBlocks := toolTurnMessages[2].Content
	if len(lastBlocks) != 3 {
		t.Fatalf("expected 3 blocks in final user message, got %d", len(lastBlocks))
	}
	if lastBlocks[0].OfToolResult == nil || lastBlocks[0].OfToolResult.ToolUseID != "call_1" {
		t.Fatalf("block 0 must be tool_result with id call_1")
	}
	if lastBlocks[1].OfText == nil || !strings.Contains(lastBlocks[1].OfText.Text, "<ally-cancelled>") {
		t.Fatalf("block 1 must be cancelled marker")
	}
	if lastBlocks[2].OfText == nil || lastBlocks[2].OfText.Text != "cancel and do something else" {
		t.Fatalf("block 2 must be new question")
	}

	// Case 3: Consecutive assistant messages merged
	_, assistantMerged := buildAnthropicMessages([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "hello"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "how can I help?"},
	})
	if len(assistantMerged) != 2 {
		t.Fatalf("expected 2 messages (user -> assistant), got %d", len(assistantMerged))
	}
	if len(assistantMerged[1].Content) != 2 {
		t.Fatalf("expected 2 blocks in merged assistant message, got %d", len(assistantMerged[1].Content))
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
	body := buildOpenAIResponsesRequest(cfg, "gpt-5.6-sol", []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "stable system context"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "inspect the cache"},
	}, nil)

	request := marshalResponsesRequest(t, body)
	if got, _ := request["prompt_cache_key"].(string); got != cacheKey {
		t.Fatalf("prompt_cache_key = %q, want %q", got, cacheKey)
	}
	options, ok := request["prompt_cache_options"].(map[string]any)
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
		wantCacheAnchor bool
		wantExplicitOpt bool
	}{
		{
			name:            "older official model",
			cfg:             ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: defaultOpenAIResponsesURL, responsesPromptCacheKey: cacheKey},
			model:           "gpt-5.5",
			wantKey:         true,
			wantCacheAnchor: false,
			wantExplicitOpt: false,
		},
		{
			name:            "custom compatible endpoint",
			cfg:             ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: "https://api.deepseek.com/v1", responsesPromptCacheKey: cacheKey},
			model:           "gpt-5.6",
			wantKey:         true,
			wantCacheAnchor: false,
			wantExplicitOpt: false,
		},
		{
			name:            "official GPT-5.6",
			cfg:             ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: defaultOpenAIResponsesURL, responsesPromptCacheKey: cacheKey},
			model:           "gpt-5.6",
			wantKey:         true,
			wantCacheAnchor: true,
			wantExplicitOpt: true,
		},
		{
			name:  "missing session key",
			cfg:   ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: defaultOpenAIResponsesURL},
			model: "gpt-5.6",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := buildOpenAIResponsesRequest(tt.cfg, tt.model, []legacyopenai.ChatCompletionMessage{{
				Role:    legacyopenai.ChatMessageRoleUser,
				Content: "hello",
			}}, nil)
			request := marshalResponsesRequest(t, body)
			gotKey, hasKey := request["prompt_cache_key"].(string)
			if hasKey != tt.wantKey || (tt.wantKey && gotKey != cacheKey) {
				t.Fatalf("prompt_cache_key = %q (present=%v), want %q (present=%v)", gotKey, hasKey, cacheKey, tt.wantKey)
			}
			if responsesRequestHasCacheAnchorText(request) != tt.wantCacheAnchor {
				t.Fatalf("cache anchor present = %v, want %v: %#v", responsesRequestHasCacheAnchorText(request), tt.wantCacheAnchor, request["input"])
			}
			_, hasOpt := request["prompt_cache_options"]
			if hasOpt != tt.wantExplicitOpt {
				t.Fatalf("prompt_cache_options present = %v, want %v: %#v", hasOpt, tt.wantExplicitOpt, request["prompt_cache_options"])
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


func TestNormalizeToolCallsPreservesRawArgumentsForExecutionRewrite(t *testing.T) {
	input := []legacyopenai.ToolCall{
		// Stream cut off mid-arguments: the accumulated string is not JSON.
		{ID: "a", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: "edit", Arguments: `{"files":[{"path":"AGENTS.md","changes":[{"oldText":"sandbox is`}},
		{ID: "b", Function: legacyopenai.FunctionCall{Name: "list_files"}},
		{ID: "c", Type: legacyopenai.ToolTypeFunction, Function: legacyopenai.FunctionCall{Name: "read", Arguments: `{"files":[]}`}},
	}
	out := normalizeToolCalls(input)

	if out[0].Function.Arguments != input[0].Function.Arguments {
		t.Fatalf("raw truncated arguments must pass through; rewriting is prepareToolCallsForExecution's job: %q", out[0].Function.Arguments)
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

func TestOpenAIChatTokenParamAndToolChoice(t *testing.T) {
	// 1. Auto-detection of o-series models
	if !shouldUseMaxCompletionTokens("auto", "o1") {
		t.Fatalf("expected o1 to use max_completion_tokens under auto")
	}
	if !shouldUseMaxCompletionTokens("auto", "o3-mini") {
		t.Fatalf("expected o3-mini to use max_completion_tokens under auto")
	}
	if !shouldUseMaxCompletionTokens("auto", "gpt-5-codex") {
		t.Fatalf("expected gpt-5 to use max_completion_tokens under auto")
	}
	if shouldUseMaxCompletionTokens("auto", "gpt-4o") {
		t.Fatalf("expected gpt-4o to use max_tokens under auto")
	}
	if shouldUseMaxCompletionTokens("auto", "deepseek-chat") {
		t.Fatalf("expected deepseek-chat to use max_tokens under auto")
	}

	// 2. User overrides take precedence
	if shouldUseMaxCompletionTokens("max_tokens", "o1") {
		t.Fatalf("expected explicit max_tokens to override o1 auto-detection")
	}
	if !shouldUseMaxCompletionTokens("max_completion_tokens", "gpt-4o") {
		t.Fatalf("expected explicit max_completion_tokens to override gpt-4o auto-detection")
	}
}

func TestMoonshotCachedTokensExtraction(t *testing.T) {
	raw := []byte(`{
		"usage": {
			"prompt_tokens": 150,
			"completion_tokens": 50,
			"total_tokens": 200,
			"cached_tokens": 80
		}
	}`)
	usage := &legacyopenai.Usage{
		PromptTokens:     150,
		CompletionTokens: 50,
		TotalTokens:      200,
	}
	mu := modelUsageFromLegacy(usage, raw)
	if mu == nil {
		t.Fatalf("expected non-nil modelUsage")
	}
	if mu.CacheHitTokens != 80 {
		t.Fatalf("expected CacheHitTokens = 80 for Moonshot, got %d", mu.CacheHitTokens)
	}
	if mu.CacheMissTokens != 70 {
		t.Fatalf("expected CacheMissTokens = 70 for Moonshot, got %d", mu.CacheMissTokens)
	}
}

func TestAnthropicToolCallIDSanitization(t *testing.T) {
	dirtyID := "call:tool.run|123_456-abc"
	clean := sanitizeAnthropicToolCallID(dirtyID)
	if clean != "call_tool_run_123_456-abc" {
		t.Fatalf("sanitized ID = %q, want %q", clean, "call_tool_run_123_456-abc")
	}

	longID := strings.Repeat("a", 100)
	cleanLong := sanitizeAnthropicToolCallID(longID)
	if len(cleanLong) != 64 {
		t.Fatalf("expected 64 chars, got %d", len(cleanLong))
	}

	// Two distinct IDs sharing the same 70-char prefix must NOT collide after sanitization
	prefix := strings.Repeat("x", 70)
	idA := prefix + "_alpha"
	idB := prefix + "_bravo"
	cleanA := sanitizeAnthropicToolCallID(idA)
	cleanB := sanitizeAnthropicToolCallID(idB)
	if cleanA == cleanB {
		t.Fatalf("cleanA and cleanB must not collide: %q == %q", cleanA, cleanB)
	}
	if len(cleanA) != 64 || len(cleanB) != 64 {
		t.Fatalf("expected 64 chars for both, got %d and %d", len(cleanA), len(cleanB))
	}

	// buildAnthropicMessages must sanitize IDs consistently on both tool_use and tool_result
	_, messages := buildAnthropicMessages([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "run"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{
			{ID: "call:foo|bar.1", Function: legacyopenai.FunctionCall{Name: "fn", Arguments: `{"k":"v"}`}},
		}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "call:foo|bar.1", Content: `{"ok":true}`},
	})
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	assistantToolUse := messages[1].Content[0].OfToolUse
	if assistantToolUse == nil || assistantToolUse.ID != "call_foo_bar_1" {
		t.Fatalf("assistant tool_use id = %q, want call_foo_bar_1", assistantToolUse.ID)
	}
	userToolResult := messages[2].Content[0].OfToolResult
	if userToolResult == nil || userToolResult.ToolUseID != "call_foo_bar_1" {
		t.Fatalf("user tool_result id = %q, want call_foo_bar_1", userToolResult.ToolUseID)
	}
}

func TestAnthropicToolResultIsError(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{`{"ok":false}`, true},
		{`{"ok":true}`, false},
		{`{"isError":true}`, true},
		{`{"is_error":true}`, true},
		{`{"isError":false}`, false},
		{`{"error":"failed to connect"}`, true},
		{`{"error":"failed to connect","ok":true}`, false},
		{`error: command exited status 1`, true},
		{`MCP call failed: connection refused`, true},
		{`unknown tool: foobar`, true},
		{`错误: 找不到指定文件`, true},
		{`错误：参数格式不正确`, true},
		{`执行失败: 退出码 1`, true},
		{`调用失败：连接超时`, true},
		{`未知工具: my_tool`, true},
		{`File written successfully`, false},
	}
	for _, tc := range cases {
		got := anthropicToolResultIsError(tc.input)
		if got != tc.want {
			t.Errorf("anthropicToolResultIsError(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestDecodeToolArgumentsEnsuresObject(t *testing.T) {
	// Arrays must be wrapped in map
	resArr := decodeToolArguments(`[1, 2, 3]`)
	if _, ok := resArr["_raw"]; !ok {
		t.Fatalf("array arguments must be wrapped in map with _raw, got %v", resArr)
	}

	// Primitives must be wrapped in map
	resNum := decodeToolArguments(`42`)
	if _, ok := resNum["_raw"]; !ok {
		t.Fatalf("number arguments must be wrapped in map with _raw, got %v", resNum)
	}

	// Objects must be preserved
	resObj := decodeToolArguments(`{"foo":"bar"}`)
	if resObj["foo"] != "bar" {
		t.Fatalf("object arguments must be preserved, got %v", resObj)
	}

	// Empty string must return empty map
	resEmpty := decodeToolArguments(``)
	if len(resEmpty) != 0 {
		t.Fatalf("empty arguments must return empty map, got %v", resEmpty)
	}
}

func TestOpenAIResponsesMidTurnSystemAndDeduplication(t *testing.T) {
	// 1. Mid-turn system message becomes developer input item
	instructions, input := buildOpenAIResponsesInput([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "base instructions"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "hello"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "hi"},
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "steer reminder: stay concise"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "what is 1+1?"},
	})
	if instructions != "base instructions" {
		t.Fatalf("instructions = %q, want base instructions", instructions)
	}
	// input should have 4 items: user(hello) -> assistant(hi) -> developer(steer reminder) -> user(what is 1+1?)
	if len(input) != 4 {
		t.Fatalf("expected 4 input items, got %d", len(input))
	}
	midTurn := input[2]
	if midTurn.OfMessage == nil || midTurn.OfMessage.Role != "developer" {
		t.Fatalf("expected developer role for mid-turn system message, got %v", midTurn.OfMessage)
	}

	// 2. Content deduplication when MultiContent has text
	multiMsg := legacyopenai.ChatCompletionMessage{
		Role:    legacyopenai.ChatMessageRoleUser,
		Content: "duplicate prompt text",
		MultiContent: []legacyopenai.ChatMessagePart{
			{Type: legacyopenai.ChatMessagePartTypeText, Text: "duplicate prompt text"},
		},
	}
	multiContent := openAIResponsesContentFromMulti(multiMsg)
	if len(multiContent) != 1 {
		t.Fatalf("expected 1 content part without duplication, got %d", len(multiContent))
	}

	anthropicBlocks := anthropicBlocksFromMessage(multiMsg)
	if len(anthropicBlocks) != 1 {
		t.Fatalf("expected 1 anthropic block without duplication, got %d", len(anthropicBlocks))
	}
}

func TestConfigureAnthropicThinking(t *testing.T) {
	// 1. Claude 3.7 Sonnet (budget model): sets Thinking.OfEnabled, does NOT set OutputConfig.Effort
	var params37 anthropic.MessageNewParams
	configureAnthropicThinking(&params37, "claude-3-7-sonnet-20250219", "high", 8192)
	if params37.Thinking.OfEnabled == nil {
		t.Fatal("expected Thinking.OfEnabled for claude-3.7-sonnet")
	}
	if params37.Thinking.OfEnabled.BudgetTokens < 1024 {
		t.Fatalf("expected BudgetTokens >= 1024, got %d", params37.Thinking.OfEnabled.BudgetTokens)
	}
	if params37.OutputConfig.Effort != "" {
		t.Fatalf("claude-3.7-sonnet must not set OutputConfig.Effort (would cause 400), got %q", params37.OutputConfig.Effort)
	}

	// 2. Claude 4.6 Sonnet (adaptive model): sets Thinking.OfAdaptive and OutputConfig.Effort
	var params46 anthropic.MessageNewParams
	configureAnthropicThinking(&params46, "claude-sonnet-4.6", "medium", 8192)
	if params46.Thinking.OfAdaptive == nil {
		t.Fatal("expected Thinking.OfAdaptive for claude-sonnet-4.6")
	}
	if string(params46.OutputConfig.Effort) != "medium" {
		t.Fatalf("expected OutputConfig.Effort = medium, got %q", params46.OutputConfig.Effort)
	}

	// 3. Effort "off"
	var paramsOff anthropic.MessageNewParams
	configureAnthropicThinking(&paramsOff, "claude-3-7-sonnet-20250219", "off", 8192)
	if paramsOff.Thinking.OfDisabled == nil {
		t.Fatal("expected Thinking.OfDisabled when effort is off")
	}
}

func TestStopReasonHandling(t *testing.T) {
	cfg := ConfigState{APIFormat: apiFormatOpenAIChat}

	// Non-standard OpenAI finish reasons (DashScope, vLLM, OpenRouter) must not error
	for _, reason := range []string{"stop", "stop_sequence", "eos", "end_turn"} {
		res := &modelStreamResult{StopReason: reason, Content: "some output"}
		if err := modelResponseStopError(cfg, res); err != nil {
			t.Errorf("finish_reason %q with output should succeed, got error: %v", reason, err)
		}
	}

	// Unknown finish reason with output should not fail
	resUnknown := &modelStreamResult{StopReason: "custom_done", Content: "generated text"}
	if err := modelResponseStopError(cfg, resUnknown); err != nil {
		t.Fatalf("unknown finish_reason with output should not fail, got: %v", err)
	}

	// Anthropic pause_turn must not error
	anthropicCfg := ConfigState{APIFormat: apiFormatAnthropicMessages}
	resPause := &modelStreamResult{StopReason: "pause_turn", Content: "paused here"}
	if err := modelResponseStopError(anthropicCfg, resPause); err != nil {
		t.Fatalf("pause_turn with output should not fail, got: %v", err)
	}
}

func TestClassifyLLMError429BillingVsRateLimit(t *testing.T) {
	// Moonshot 429 quota exhaustion
	moonshotErr := errors.New("error code: 429, body: {\"error\":{\"type\":\"exceeded_current_quota_error\",\"message\":\"You exceeded your current token quota: ... please check your account balance\"}}")
	if kind := classifyLLMError(moonshotErr); kind != llmErrorKindBilling {
		t.Fatalf("Moonshot 429 quota exhaustion should classify as Billing, got %v", kind)
	}
	if !isAuthKeyError(moonshotErr) {
		t.Fatal("Moonshot 429 quota exhaustion should be considered an auth/key error")
	}

	// OpenAI 429 insufficient_quota
	openAIErr := errors.New("error code: 429, message: insufficient_quota")
	if kind := classifyLLMError(openAIErr); kind != llmErrorKindBilling {
		t.Fatalf("OpenAI 429 insufficient_quota should classify as Billing, got %v", kind)
	}
	if !isAuthKeyError(openAIErr) {
		t.Fatal("OpenAI 429 insufficient_quota should be considered an auth/key error")
	}

	// Plain 429 rate limit
	rateLimitErr := errors.New("error code: 429, status: 429 Too Many Requests, rate limit reached")
	if kind := classifyLLMError(rateLimitErr); kind != llmErrorKindRateLimited {
		t.Fatalf("Plain 429 should classify as RateLimited, got %v", kind)
	}
	if isAuthKeyError(rateLimitErr) {
		t.Fatal("Plain 429 should not be considered an auth/key error")
	}
}

func TestMoonshotChoicesUsageExtraction(t *testing.T) {
	raw := []byte(`{
		"id": "chatcmpl-123",
		"choices": [
			{
				"index": 0,
				"delta": {},
				"usage": {
					"prompt_tokens": 1000,
					"completion_tokens": 200,
					"total_tokens": 1200,
					"cached_tokens": 600
				}
			}
		]
	}`)
	choiceUsage := extractChoiceUsageFromRaw(raw)
	if choiceUsage == nil {
		t.Fatal("expected choice usage to be extracted")
	}
	usage := modelUsageFromLegacy(choiceUsage, raw)
	if usage == nil {
		t.Fatal("expected modelUsage to be parsed")
	}
	if usage.PromptTokens != 1000 || usage.CompletionTokens != 200 {
		t.Fatalf("unexpected tokens: prompt=%d completion=%d", usage.PromptTokens, usage.CompletionTokens)
	}
	if usage.CacheHitTokens != 600 {
		t.Fatalf("expected CacheHitTokens=600, got %d", usage.CacheHitTokens)
	}
	if usage.CacheMissTokens != 400 {
		t.Fatalf("expected CacheMissTokens=400, got %d", usage.CacheMissTokens)
	}
}

func TestAnthropicToolPromptCacheBreakpoint(t *testing.T) {
	params := anthropic.MessageNewParams{
		Model: "claude-3-7-sonnet",
		Tools: []anthropic.ToolUnionParam{
			{OfTool: &anthropic.ToolParam{Name: "read"}},
			{OfTool: &anthropic.ToolParam{Name: "write"}},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("hello")),
		},
	}
	markAnthropicPromptCacheBreakpoints(&params)
	lastTool := params.Tools[1].OfTool
	if lastTool == nil || lastTool.CacheControl.TTL == "" {
		t.Fatal("expected CacheControl on the last tool definition")
	}
	firstTool := params.Tools[0].OfTool
	if firstTool != nil && firstTool.CacheControl.TTL != "" {
		t.Fatal("first tool should not have cache breakpoint")
	}
}

func TestDerefJSONSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filter": map[string]any{
				"$ref":        "#/$defs/FilterType",
				"description": "override description",
			},
		},
		"$defs": map[string]any{
			"FilterType": map[string]any{
				"type": "string",
				"enum": []any{"all", "active"},
			},
		},
	}
	derefed := derefJSONSchema(schema)
	if _, hasDefs := derefed["$defs"]; hasDefs {
		t.Fatal("expected $defs to be cleaned up after full inlining")
	}
	props, ok := derefed["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing")
	}
	filter, ok := props["filter"].(map[string]any)
	if !ok {
		t.Fatal("filter missing")
	}
	if filter["type"] != "string" {
		t.Fatalf("expected inlined type=string, got %v", filter["type"])
	}
	if filter["description"] != "override description" {
		t.Fatalf("sibling property override failed, got %v", filter["description"])
	}
}

func TestResponseInputItemParamOfFunctionCallEmptyArgs(t *testing.T) {
	call := legacyopenai.ToolCall{
		ID: "call_123",
		Function: legacyopenai.FunctionCall{
			Name:      "test_tool",
			Arguments: "",
		},
	}
	item := responseInputItemParamOfFunctionCall(call)
	if item.OfFunctionCall == nil {
		t.Fatal("expected OfFunctionCall")
	}
	if item.OfFunctionCall.Arguments != "{}" {
		t.Fatalf("expected arguments to default to {}, got %q", item.OfFunctionCall.Arguments)
	}
}

func TestBuildAnthropicMessagesEmptyToolCallID(t *testing.T) {
	messages := []legacyopenai.ChatCompletionMessage{
		{
			Role: legacyopenai.ChatMessageRoleAssistant,
			ToolCalls: []legacyopenai.ToolCall{
				{ID: "call_1", Function: legacyopenai.FunctionCall{Name: "fn"}},
			},
		},
		{
			Role:       legacyopenai.ChatMessageRoleTool,
			ToolCallID: "",
			Content:    "result text",
		},
	}
	_, anthropicMsgs := buildAnthropicMessages(messages)
	if len(anthropicMsgs) < 2 {
		t.Fatalf("expected assistant and user turn, got %d messages", len(anthropicMsgs))
	}
	userTurn := anthropicMsgs[1]
	if len(userTurn.Content) == 0 {
		t.Fatal("tool_result block must not be dropped on empty ToolCallID")
	}
	if userTurn.Content[0].OfToolResult == nil {
		t.Fatal("expected OfToolResult block")
	}
	if userTurn.Content[0].OfToolResult.ToolUseID != "tool_call" {
		t.Fatalf("expected fallback tool_call ID, got %q", userTurn.Content[0].OfToolResult.ToolUseID)
	}
}

func TestExtractRawStreamReasoningArrayTolerant(t *testing.T) {
	raw := []byte(`{
		"choices": [
			{
				"delta": {
					"reasoning": "deep reasoning here",
					"reasoning_details": [{"type": "text", "text": "deep reasoning here"}]
				}
			}
		]
	}`)
	got := extractRawStreamReasoning(raw)
	if got != "deep reasoning here" {
		t.Fatalf("expected reasoning to be extracted despite array reasoning_details, got %q", got)
	}
}

func TestNormalizeToolSchemaTypes(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"missing_type_enum_string": map[string]any{
				"enum": []any{"asc", "desc"},
			},
			"missing_type_enum_number": map[string]any{
				"enum": []any{1, 2, 3},
			},
			"contradictory_type": map[string]any{
				"type": "object",
				"enum": []any{"opt_a", "opt_b"},
				"properties": map[string]any{
					"bad": map[string]any{"type": "string"},
				},
			},
			"missing_type_object": map[string]any{
				"properties": map[string]any{
					"sub": map[string]any{"type": "string"},
				},
			},
			"missing_type_array": map[string]any{
				"items": map[string]any{"type": "string"},
			},
			"fallback_typeless": map[string]any{
				"description": "no info",
			},
		},
	}

	norm := normalizeToolSchemaTypes(schema)
	props := norm["properties"].(map[string]any)

	if props["missing_type_enum_string"].(map[string]any)["type"] != "string" {
		t.Fatalf("expected string type for enum strings, got %v", props["missing_type_enum_string"])
	}
	if props["missing_type_enum_number"].(map[string]any)["type"] != "number" {
		t.Fatalf("expected number type for enum numbers, got %v", props["missing_type_enum_number"])
	}
	contra := props["contradictory_type"].(map[string]any)
	if contra["type"] != "string" {
		t.Fatalf("expected contradictory object type to be repaired to string, got %v", contra["type"])
	}
	if _, hasProps := contra["properties"]; hasProps {
		t.Fatalf("expected properties to be deleted when repaired from object to string")
	}
	if props["missing_type_object"].(map[string]any)["type"] != "object" {
		t.Fatalf("expected object type inferred from properties")
	}
	if props["missing_type_array"].(map[string]any)["type"] != "array" {
		t.Fatalf("expected array type inferred from items")
	}
	if props["fallback_typeless"].(map[string]any)["type"] != "string" {
		t.Fatalf("expected fallback typeless property to be string")
	}
}

func TestSanitizeOpenAIResponsesCallID(t *testing.T) {
	// Compound call|item ID should take the first token
	got := sanitizeOpenAIResponsesCallID("call_12345|item_67890")
	if got != "call_12345" {
		t.Fatalf("expected call_12345, got %q", got)
	}

	// Safe chars replacement
	got = sanitizeOpenAIResponsesCallID("call:special!chars")
	if got != "call_special_chars" {
		t.Fatalf("expected call_special_chars, got %q", got)
	}

	// Length > 64 truncation with deterministic hash
	longID := strings.Repeat("a", 80)
	got = sanitizeOpenAIResponsesCallID(longID)
	if len(got) != 64 {
		t.Fatalf("expected length 64, got %d (%q)", len(got), got)
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 56)+"_") {
		t.Fatalf("expected prefix of 56 a's and underscore, got %q", got)
	}

	// Empty fallback
	if sanitizeOpenAIResponsesCallID("") != "call_tool" {
		t.Fatalf("expected call_tool on empty")
	}
}

func TestBuildAnthropicMessagesMidTurnSystem(t *testing.T) {
	messages := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "base system prompt"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "user prompt 1"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "assistant reply 1"},
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "compaction summary / reminder"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "user prompt 2"},
	}

	system, anthropicMsgs := buildAnthropicMessages(messages)
	// Base system prompt must stay strictly intact to preserve prompt cache breakpoint
	if system != "base system prompt" {
		t.Fatalf("expected system to be 'base system prompt', got %q", system)
	}

	// The mid-conversation system message must be wrapped inside a user turn
	// and merged with the following user prompt to preserve alternating roles
	if len(anthropicMsgs) != 3 {
		t.Fatalf("expected 3 turns (user 1, assistant 1, user 2 merged), got %d turns", len(anthropicMsgs))
	}
	lastUserTurn := anthropicMsgs[2]
	if lastUserTurn.Role != anthropic.MessageParamRoleUser {
		t.Fatalf("expected last turn to be user, got %v", lastUserTurn.Role)
	}
	if len(lastUserTurn.Content) != 2 {
		t.Fatalf("expected 2 blocks in last user turn (<system> and user prompt 2), got %d", len(lastUserTurn.Content))
	}
	if lastUserTurn.Content[0].OfText == nil || !strings.Contains(lastUserTurn.Content[0].OfText.Text, "<system>\ncompaction summary / reminder\n</system>") {
		t.Fatalf("expected first block to be wrapped system, got %+v", lastUserTurn.Content[0])
	}
	if lastUserTurn.Content[1].OfText == nil || lastUserTurn.Content[1].OfText.Text != "user prompt 2" {
		t.Fatalf("expected second block to be user prompt 2, got %+v", lastUserTurn.Content[1])
	}
}

