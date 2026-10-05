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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	oa "github.com/openai/openai-go/v3"
	oaresp "github.com/openai/openai-go/v3/responses"
	legacyopenai "github.com/sashabaranov/go-openai"

	"ally-dev/internal/tools/toolcall"
)

// anthropicOfficialCfg is the official Anthropic endpoint. The breakpoint helper
// takes the whole config because both the ttl and the top-level marker depend on
// which endpoint the request lands on.
var anthropicOfficialCfg = ConfigState{APIFormat: apiFormatAnthropicMessages, BaseURL: defaultAnthropicMessagesURL}

// TestMarkAnthropicPromptCacheBreakpointsLandOnLastBlocks pins where the three
// markers go: the last tool definition, the last system block, and the last
// content block of the last message that accepts one. A tail block that cannot
// carry a marker is skipped instead of costing the message breakpoint.
func TestMarkAnthropicPromptCacheBreakpointsLandOnLastBlocks(t *testing.T) {
	params := anthropic.MessageNewParams{
		System: []anthropic.TextBlockParam{{Text: "system"}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("question")),
			anthropic.NewAssistantMessage(anthropic.NewToolUseBlock("t1", map[string]any{"a": 1}, "grep")),
			anthropic.NewUserMessage(anthropic.NewToolResultBlock("t1", `{"ok":true}`, false)),
			anthropic.NewUserMessage(anthropic.NewTextBlock("follow-up question")),
		},
	}
	markAnthropicPromptCacheBreakpoints(&params, anthropicOfficialCfg)

	if got := params.System[0].CacheControl.TTL; got != "5m" {
		t.Fatalf("system breakpoint ttl = %q, want 5m", got)
	}
	tail := params.Messages[3].Content[0]
	if tail.OfText == nil || tail.OfText.CacheControl.TTL != "5m" {
		t.Fatalf("expected cache_control breakpoint on the last block of the last message")
	}
	// One message marker only: it moves with the tail instead of staying behind on
	// the previous turn's tool result.
	previous := params.Messages[2].Content[len(params.Messages[2].Content)-1]
	if previous.OfToolResult == nil || previous.OfToolResult.CacheControl.TTL != "" {
		t.Fatalf("the previous turn's tool result must not keep a marker")
	}

	// A tail message whose only block cannot carry a marker (a thinking-only
	// assistant turn) is skipped, and the marker lands on the block before it.
	thinkingOnly := anthropic.MessageNewParams{
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("question")),
			anthropic.NewAssistantMessage(anthropic.NewThinkingBlock("sig", "trace")),
		},
	}
	markAnthropicPromptCacheBreakpoints(&thinkingOnly, anthropicOfficialCfg)
	question := thinkingOnly.Messages[0].Content[0]
	if question.OfText == nil || question.OfText.CacheControl.TTL != "5m" {
		t.Fatalf("expected the marker to fall back to the last cacheable block")
	}

	// The full conversion path must land the marker the same way:
	// buildAnthropicMessages merges a trailing user message into the tool-result
	// turn, and the marker follows the newest block of that merged message.
	_, converted := buildAnthropicMessages([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "system"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "question"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{ID: "t1", Function: legacyopenai.FunctionCall{Name: "grep", Arguments: `{"a":1}`}}}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "t1", Content: `{"ok":true}`},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "follow-up question"},
	}, nil, true)
	if len(converted) != 3 {
		t.Fatalf("expected 3 converted messages (user -> assistant -> user), got %d", len(converted))
	}
	convParams := anthropic.MessageNewParams{
		Messages: converted,
	}
	markAnthropicPromptCacheBreakpoints(&convParams, anthropicOfficialCfg)
	lastUserBlocks := convParams.Messages[2].Content
	if len(lastUserBlocks) != 2 {
		t.Fatalf("expected 2 blocks in last user message, got %d", len(lastUserBlocks))
	}
	if lastUserBlocks[0].OfToolResult == nil || lastUserBlocks[0].OfToolResult.CacheControl.TTL != "" {
		t.Fatalf("tool result block must stay outside cache control")
	}
	if lastUserBlocks[1].OfText == nil || lastUserBlocks[1].OfText.CacheControl.TTL != "5m" {
		t.Fatalf("expected cache_control breakpoint on the merged message's last block")
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
	}, nil, true)
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
	}, nil, true)
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
	}, nil, true)
	if len(assistantMerged) != 2 {
		t.Fatalf("expected 2 messages (user -> assistant), got %d", len(assistantMerged))
	}
	if len(assistantMerged[1].Content) != 2 {
		t.Fatalf("expected 2 blocks in merged assistant message, got %d", len(assistantMerged[1].Content))
	}
}

// TestOpenAIResponsesPromptCacheFields pins the request-shaped half of the
// prompt-cache contract: caching stays in the provider's implicit mode (no mode
// marker, no explicit breakpoint anywhere in the input), no retention field is
// sent — the provider default stays in place — and the session routing key travels
// to the endpoints that document it (promptCacheKeyForRequest): the official API
// and the vendors that declare prompt_cache_key on their Responses API (智谱 / Kimi /
// MiniMax), so a relay of an undocumented vendor never receives a top-level key it
// may answer with 400.
func TestOpenAIResponsesPromptCacheFields(t *testing.T) {
	cacheKey := openAIResponsesPromptCacheKey("session-1")
	tests := []struct {
		name    string
		cfg     ConfigState
		model   string
		wantKey bool
	}{
		{
			name:    "caching stays implicit with the provider-default retention",
			cfg:     ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: defaultOpenAIResponsesURL, responsesPromptCacheKey: cacheKey},
			model:   "gpt-5.6-sol",
			wantKey: true,
		},
		{
			// 智谱 的 Responses 文档把这个字段写作「用于集群路由，以提高缓存命中率」，
			// 所以它那条线上必须照旧带上会话路由键。
			name:    "a vendor that documents the field keeps the routing key",
			cfg:     ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: "https://open.bigmodel.cn/api/v1", responsesPromptCacheKey: cacheKey},
			model:   "glm-5.3",
			wantKey: true,
		},
		{
			name:  "no session key for a vendor that does not document it",
			cfg:   ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: "https://api.deepseek.com/v1", responsesPromptCacheKey: cacheKey},
			model: "deepseek-v4-pro",
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
			}}, nil, nil)
			request := marshalResponsesRequest(t, body)
			gotKey, hasKey := request["prompt_cache_key"].(string)
			if hasKey != tt.wantKey || (tt.wantKey && gotKey != cacheKey) {
				t.Fatalf("prompt_cache_key = %q (present=%v), want %q (present=%v)", gotKey, hasKey, cacheKey, tt.wantKey)
			}
			if got, has := request["prompt_cache_retention"]; has {
				t.Fatalf("prompt_cache_retention must never be sent, got %#v", got)
			}
			if got, has := request["prompt_cache_options"]; has {
				t.Fatalf("prompt_cache_options must never be sent, got %#v", got)
			}
			// The input carries the conversation only: an explicit breakpoint marker
			// would disable the implicit breakpoint and push the whole history
			// outside the cached prefix.
			items, _ := request["input"].([]any)
			for _, rawItem := range items {
				item, _ := rawItem.(map[string]any)
				if item["role"] == "developer" {
					t.Fatalf("input gained a developer cache anchor: %#v", request["input"])
				}
			}
		})
	}
}

// TestOpenAIResponsesEncryptedReasoningIncludeIsOfficialOnly pins the gate on the
// third OpenAI-official request field: encrypted reasoning is asked for on the
// endpoint whose requests are stateless (store=false), and nowhere else, so a
// compatible gateway never receives the key.
func TestOpenAIResponsesEncryptedReasoningIncludeIsOfficialOnly(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		wanted bool
	}{
		{name: "official endpoint asks for the encrypted copy", base: defaultOpenAIResponsesURL, wanted: true},
		{name: "relay endpoint is not sent the field", base: "https://api.deepseek.com/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: tt.base}
			body := buildOpenAIResponsesRequest(cfg, "gpt-5.6", []legacyopenai.ChatCompletionMessage{{
				Role:    legacyopenai.ChatMessageRoleUser,
				Content: "hello",
			}}, nil, nil)
			request := marshalResponsesRequest(t, body)
			include, has := request["include"].([]any)
			if has != tt.wanted {
				t.Fatalf("include present = %v, want %v (%#v)", has, tt.wanted, request["include"])
			}
			if !tt.wanted {
				return
			}
			found := false
			for _, item := range include {
				if item == string(oaresp.ResponseIncludableReasoningEncryptedContent) {
					found = true
				}
			}
			if !found {
				t.Fatalf("include = %#v, want %s", include, oaresp.ResponseIncludableReasoningEncryptedContent)
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
	if input[0].Function.Arguments == toolcall.TruncatedArgumentsMarker {
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
	// 中继丢掉状态码、仅在文本里转述 400：字符串兜底仍生效（Responses 适配器的
	// 报错形状即 "responses request failed: 400 Bad Request: …"，两种拼写都得认）。
	for _, msg := range []string{
		"relay says: status code: 400",
		`responses request failed: 400 Bad Request: {"error":{}}`,
	} {
		if !isProvider400Error(errors.New(msg)) {
			t.Fatalf("%q must be detected as a 400", msg)
		}
	}
	if isProvider400Error(errors.New("totally unrelated failure")) {
		t.Fatal("unrelated errors must not match")
	}
}

// mergeToolCallDeltas is the test-side stateless helper: it merges one batch
// into an existing list, rebuilding the index/id tables from the slice. Deltas
// that carry an id therefore resolve across calls, but index-only deltas cannot
// — tests that span a stream use newToolCallAccumulator directly, exactly like
// the adapter does. It also rebuilds the id foundry, so a case that depends on it
// (a repeated or missing provider id) has to drive one accumulator across the
// whole stream.
func mergeToolCallDeltas(toolCalls *[]legacyopenai.ToolCall, deltas []legacyopenai.ToolCall) {
	*toolCalls = newToolCallAccumulator(*toolCalls).merge(deltas)
}

// TestToolCallAccumulatorSkipsPhantomCallsForNonZeroBasedIndexes locks the fix
// for relays that number their tool calls from 1 or skip a number: the old
// implementation treated the index as a slice position and back-filled empty
// entries, so the gap became a tool call with an empty name in the assistant
// message (server 400 / local "unknown tool"). pi keys the block by index/id and
// never creates a placeholder (openai-completions.ts:493-536).
func TestToolCallAccumulatorSkipsPhantomCallsForNonZeroBasedIndexes(t *testing.T) {
	acc := newToolCallAccumulator(nil)
	one := 1
	toolCalls := acc.merge([]legacyopenai.ToolCall{{
		Index: &one, ID: "call_b", Type: legacyopenai.ToolTypeFunction,
		Function: legacyopenai.FunctionCall{Name: "read", Arguments: `{"path":"b.txt"`},
	}})
	if len(toolCalls) != 1 {
		t.Fatalf("index 1 must produce exactly one call, got %d: %#v", len(toolCalls), toolCalls)
	}
	if toolCalls[0].Function.Name != "read" || toolCalls[0].ID != "call_b" {
		t.Fatalf("call must keep its own name and id: %#v", toolCalls[0])
	}

	// A later delta carrying only the index must continue the same call — this
	// is what requires the accumulator to outlive the whole stream.
	toolCalls = acc.merge([]legacyopenai.ToolCall{{
		Index:    &one,
		Function: legacyopenai.FunctionCall{Arguments: `,"depth":1}`},
	}})
	if len(toolCalls) != 1 {
		t.Fatalf("index-only delta must not create a second call: %#v", toolCalls)
	}
	if toolCalls[0].Function.Arguments != `{"path":"b.txt","depth":1}` {
		t.Fatalf("index-only delta must append to the same call, got %q", toolCalls[0].Function.Arguments)
	}
	if toolCalls[0].Function.Name != "read" {
		t.Fatalf("name must stay intact, got %q", toolCalls[0].Function.Name)
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
	acc := newToolCallAccumulator(nil)
	toolCalls = acc.merge([]legacyopenai.ToolCall{{Index: &index, ID: "call_1", Function: legacyopenai.FunctionCall{Name: "http_"}}})
	toolCalls = acc.merge([]legacyopenai.ToolCall{{Index: &index, Function: legacyopenai.FunctionCall{Name: "request"}}})
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

// 上游报错标记必须"只加身份、不改文本":界面据此加前缀，而分类/重试/切换 key
// 都建立在错误文案与错误链之上，任何一处被改动都会静默改变行为。
func TestMarkUpstreamErrorKeepsTextAndChain(t *testing.T) {
	raw := &legacyopenai.RequestError{
		HTTPStatus:     "429 Too Many Requests",
		HTTPStatusCode: 429,
		Err:            errors.New("rate limit reached"),
	}
	unmarked := wrapProviderRequestError(raw)
	marked := markUpstreamError(unmarked)
	if !isUpstreamError(marked) {
		t.Fatal("marked error must report as upstream")
	}
	if marked.Error() != unmarked.Error() {
		t.Fatalf("marking must not rewrite the message: %q vs %q", marked.Error(), unmarked.Error())
	}
	if classifyLLMError(marked) != llmErrorKindRateLimited || !shouldRetryLLMError(marked) {
		t.Fatal("marking broke the classification used by retry and key failover")
	}
	var reqErr *legacyopenai.RequestError
	if !errors.As(marked, &reqErr) {
		t.Fatal("marking must keep the provider error in the chain")
	}
	// 幂等:重复标记不得叠加包装。
	if got := markUpstreamError(marked); got != marked {
		t.Fatalf("marking must be idempotent, got %#v", got)
	}
	// 未标记的错误不得被误判为上游。
	if isUpstreamError(unmarked) || isUpstreamError(nil) || isUpstreamError(errors.New("plain")) {
		t.Fatal("unmarked errors are not upstream errors")
	}
	// 调用层控制流(用户取消/超时)不是上游故障。
	if markUpstreamError(nil) != nil || isUpstreamError(markUpstreamError(context.Canceled)) {
		t.Fatal("nil and context cancellation must pass through unmarked")
	}
}

func TestMergeToolCallDeltasSkipsDuplicatedArgumentChunks(t *testing.T) {
	// A relay that duplicates the whole first delta re-sends the opening
	// arguments chunk too; appending it verbatim corrupts the JSON.
	var toolCalls []legacyopenai.ToolCall
	index := 0
	acc := newToolCallAccumulator(nil)
	first := legacyopenai.ToolCall{Index: &index, ID: "call_1", Function: legacyopenai.FunctionCall{Name: "read", Arguments: `{"fi`}}
	toolCalls = acc.merge([]legacyopenai.ToolCall{first})
	toolCalls = acc.merge([]legacyopenai.ToolCall{first})
	toolCalls = acc.merge([]legacyopenai.ToolCall{{Index: &index, Function: legacyopenai.FunctionCall{Name: "read", Arguments: `les":[]}`}}})
	if toolCalls[0].Function.Arguments != `{"files":[]}` {
		t.Fatalf("duplicated argument chunks must be skipped, got %q", toolCalls[0].Function.Arguments)
	}
}

// toolCallFragment builds one streamed tool_calls fragment; index < 0 means the
// fragment carries no index at all.
func toolCallFragment(index int, id, name, args string) legacyopenai.ToolCall {
	fragment := legacyopenai.ToolCall{
		Type:     legacyopenai.ToolTypeFunction,
		ID:       id,
		Function: legacyopenai.FunctionCall{Name: name, Arguments: args},
	}
	if index >= 0 {
		i := index
		fragment.Index = &i
	}
	return fragment
}

// TestToolCallAccumulatorIdentityTable drives the production accumulator over one
// whole stream per row and locks the identity rules: the id decides, the index
// only locates a continuation, and the name test is the last resort for relays
// that send neither an id nor a usable index.
func TestToolCallAccumulatorIdentityTable(t *testing.T) {
	cases := []struct {
		name      string
		batches   [][]legacyopenai.ToolCall
		wantIDs   []string
		wantNames []string
		wantArgs  []string
	}{
		{
			name: "standard stream: the name arrives once and the arguments in chunks",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(0, "call_a", "read", `{"path":`)},
				{toolCallFragment(0, "", "", `"a.go"}`)},
			},
			wantIDs:   []string{"call_a"},
			wantNames: []string{"read"},
			wantArgs:  []string{`{"path":"a.go"}`},
		},
		{
			// The reported relay failure: two calls of the same tool share one
			// index. Their ids differ, so they are two calls — the old index-first
			// lookup welded them into one and corrupted the arguments.
			name: "two calls sharing one index are separated by their ids when the name repeats",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(0, "call_a", "read", `{"path":"a.go"}`)},
				{toolCallFragment(0, "call_b", "read", `{"path":"b.go"}`)},
			},
			wantIDs:   []string{"call_a", "call_b"},
			wantNames: []string{"read", "read"},
			wantArgs:  []string{`{"path":"a.go"}`, `{"path":"b.go"}`},
		},
		{
			name: "two calls sharing one index with different names",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(0, "call_a", "read", `{"files":[{"path":"a.txt"}]}`)},
				{toolCallFragment(0, "call_b", "list_files", `{}`)},
			},
			wantIDs:   []string{"call_a", "call_b"},
			wantNames: []string{"read", "list_files"},
			wantArgs:  []string{`{"files":[{"path":"a.txt"}]}`, `{}`},
		},
		{
			name: "the same id re-sent with every chunk stays one call",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(0, "call_1", "http_request", `{"ur`)},
				{toolCallFragment(0, "call_1", "http_request", `l":"ht`)},
				{toolCallFragment(0, "call_1", "http_request", `tps:`)},
			},
			wantIDs:   []string{"call_1"},
			wantNames: []string{"http_request"},
			wantArgs:  []string{`{"url":"https:`},
		},
		{
			name: "a name arriving in chunks is spliced onto the call it continues",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(0, "call_1", "http_", `{"url":"x"}`)},
				{toolCallFragment(0, "", "request", "")},
			},
			wantIDs:   []string{"call_1"},
			wantNames: []string{"http_request"},
			wantArgs:  []string{`{"url":"x"}`},
		},
		{
			name: "one index and one id reused for two calls: the duplicate id is rewritten",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(0, "call_a", "read", `{"path":"a"}`)},
				{toolCallFragment(0, "call_a", "list_files", `{}`)},
			},
			wantIDs:   []string{"call_a", "call_a__2"},
			wantNames: []string{"read", "list_files"},
			wantArgs:  []string{`{"path":"a"}`, `{}`},
		},
		{
			name: "index-only continuation with a non-zero index",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(1, "call_b", "read", `{"path":"b.txt"`)},
				{toolCallFragment(1, "", "", `,"depth":1}`)},
			},
			wantIDs:   []string{"call_b"},
			wantNames: []string{"read"},
			wantArgs:  []string{`{"path":"b.txt","depth":1}`},
		},
		{
			name: "fragments without an index continue the newest call",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(-1, "call_a", "read", `{"path":`)},
				{toolCallFragment(-1, "", "", `"a.go"}`)},
			},
			wantIDs:   []string{"call_a"},
			wantNames: []string{"read"},
			wantArgs:  []string{`{"path":"a.go"}`},
		},
		{
			name: "two id-less calls on one index are separated by their names and get unique ids",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(0, "", "read", `{"path":"a"}`)},
				{toolCallFragment(0, "", "list_files", `{}`)},
			},
			wantIDs:   []string{"call_1", "call_2"},
			wantNames: []string{"read", "list_files"},
			wantArgs:  []string{`{"path":"a"}`, `{}`},
		},
		{
			name: "an id-bearing fragment naming another tool opens its own call instead of adopting the id",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(0, "", "read", `{"path":"a"}`)},
				{toolCallFragment(0, "call_b", "list_files", `{}`)},
			},
			wantIDs:   []string{"call_1", "call_b"},
			wantNames: []string{"read", "list_files"},
			wantArgs:  []string{`{"path":"a"}`, `{}`},
		},
		{
			name: "the provider id arriving after the call started is adopted",
			batches: [][]legacyopenai.ToolCall{
				{toolCallFragment(0, "", "read", `{"path":"a"`)},
				{toolCallFragment(0, "call_a", "", `}`)},
			},
			wantIDs:   []string{"call_a"},
			wantNames: []string{"read"},
			wantArgs:  []string{`{"path":"a"}`},
		},
		{
			name:    "a stray continuation is dropped instead of becoming a nameless call",
			batches: [][]legacyopenai.ToolCall{{toolCallFragment(0, "", "", `{"path":"a"}`)}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acc := newToolCallAccumulator(nil)
			var got []legacyopenai.ToolCall
			for _, batch := range tc.batches {
				got = acc.merge(batch)
			}
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("call count = %d, want %d: %#v", len(got), len(tc.wantIDs), got)
			}
			for i := range got {
				if got[i].ID != tc.wantIDs[i] {
					t.Errorf("call %d id = %q, want %q", i, got[i].ID, tc.wantIDs[i])
				}
				if got[i].Function.Name != tc.wantNames[i] {
					t.Errorf("call %d name = %q, want %q", i, got[i].Function.Name, tc.wantNames[i])
				}
				if got[i].Function.Arguments != tc.wantArgs[i] {
					t.Errorf("call %d args = %q, want %q", i, got[i].Function.Arguments, tc.wantArgs[i])
				}
			}
		})
	}
}

func TestToolCallAccumulatorReportsDroppedStray(t *testing.T) {
	acc := newToolCallAccumulator(nil)
	if got := acc.merge([]legacyopenai.ToolCall{toolCallFragment(0, "", "", `{"path":"a"}`)}); len(got) != 0 {
		t.Fatalf("a stray continuation must not create a call: %#v", got)
	}
	if notes := acc.diagnostics(); len(notes) == 0 {
		t.Fatal("the dropped fragment must be reported")
	}
}

func TestToolCallAccumulatorRewritesIDsTheHistoryUses(t *testing.T) {
	// A relay that numbers its calls per response sends "call_0" again in the
	// second response of a session; the history already owns that id, so the new
	// call must get another one — otherwise the next request carries a duplicate
	// tool_call id and the provider rejects it.
	acc := newToolCallAccumulator(nil)
	acc.seedConversation([]legacyopenai.ChatCompletionMessage{
		{
			Role:      legacyopenai.ChatMessageRoleAssistant,
			ToolCalls: []legacyopenai.ToolCall{{Type: legacyopenai.ToolTypeFunction, ID: "call_0", Function: legacyopenai.FunctionCall{Name: "read"}}},
		},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "call_0", Content: "ok"},
	})
	got := acc.merge([]legacyopenai.ToolCall{toolCallFragment(0, "call_0", "read", `{"path":"a"}`)})
	if len(got) != 1 || got[0].ID != "call_0__2" {
		t.Fatalf("a re-sent id must be rewritten: %#v", got)
	}
	if notes := acc.diagnostics(); len(notes) == 0 {
		t.Fatal("the rewrite must be reported")
	}
}

func TestEnsureResponsesToolCallSeparatesItemsReusingOneOutputIndex(t *testing.T) {
	var calls []legacyopenai.ToolCall
	byOutput := map[int64]int{}
	byItemID := map[string]int{}
	first := ensureResponsesToolCall(&calls, byOutput, byItemID, 0, "fc_1")
	second := ensureResponsesToolCall(&calls, byOutput, byItemID, 0, "fc_2")
	if first == second || len(calls) != 2 {
		t.Fatalf("two items reusing output_index 0 must not share a call: %#v", calls)
	}
	if byItemID["fc_1"] != first || byItemID["fc_2"] != second {
		t.Fatalf("each item id must keep pointing at its own call: %#v", byItemID)
	}
	if got := ensureResponsesToolCall(&calls, byOutput, byItemID, 0, "fc_1"); got != first {
		t.Fatalf("an event naming the first item must resolve to it, got %d want %d", got, first)
	}
	if got := ensureResponsesToolCall(&calls, byOutput, byItemID, 0, ""); got != second {
		t.Fatalf("an event with no item id must fall back to the newest binding, got %d want %d", got, second)
	}
}

func TestEnsureResponsesToolCallAdoptsALateItemID(t *testing.T) {
	var calls []legacyopenai.ToolCall
	byOutput := map[int64]int{}
	byItemID := map[string]int{}
	opened := ensureResponsesToolCall(&calls, byOutput, byItemID, 3, "")
	named := ensureResponsesToolCall(&calls, byOutput, byItemID, 3, "fc_9")
	if opened != named || len(calls) != 1 {
		t.Fatalf("an item id arriving after the call opened must join that call: %#v", calls)
	}
	if byItemID["fc_9"] != opened {
		t.Fatalf("the adopted item id must map to the opened call: %#v", byItemID)
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

// TestPatchChatRequestFieldsAddsPromptCacheKey covers the Chat half of cache
// routing: the vendor key lands in the body, the official endpoint also pins
// store:false, and a second pass over the same bytes changes nothing — the prefix
// the provider hashes must not drift between requests.
func TestPatchChatRequestFieldsAddsPromptCacheKey(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`)
	patched, changed := patchChatRequestFields(body, "", nil, "ally:key", true, reasoningStopThinkingNone)
	if !changed {
		t.Fatal("expected the cache key to be patched in")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatalf("patched body is not JSON: %v", err)
	}
	if string(payload["prompt_cache_key"]) != `"ally:key"` || string(payload["store"]) != "false" {
		t.Fatalf("patched body = %s, want prompt_cache_key + store:false", patched)
	}
	if again, changed := patchChatRequestFields(patched, "", nil, "ally:key", true, reasoningStopThinkingNone); changed {
		t.Fatalf("second pass must be a no-op, got %s", again)
	}

	// A vendor that documents prompt_cache_key on its Chat wire gets the routing key
	// without the official store pin (see promptCachePinStore).
	vendorOnly, changed := patchChatRequestFields(body, "", nil, "ally:key", false, reasoningStopThinkingNone)
	if !changed {
		t.Fatal("expected the vendor request to gain prompt_cache_key")
	}
	var vendorPayload map[string]json.RawMessage
	if err := json.Unmarshal(vendorOnly, &vendorPayload); err != nil {
		t.Fatalf("patched body is not JSON: %v", err)
	}
	if string(vendorPayload["prompt_cache_key"]) != `"ally:key"` {
		t.Fatalf("vendor patched cache field = %s", vendorOnly)
	}
	if _, exists := vendorPayload["store"]; exists {
		t.Fatalf("a compatible endpoint must not receive store: %s", vendorOnly)
	}
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
	// MiMo's parameter table lists max_completion_tokens alone (default 1024), so a
	// request carrying the legacy field would leave the cap at the provider's own
	// default instead of the configured value.
	if !shouldUseMaxCompletionTokens("auto", "mimo-v2.5-pro") {
		t.Fatalf("expected MiMo to use max_completion_tokens under auto")
	}
	if shouldUseMaxCompletionTokens("max_tokens", "mimo-v2.5-pro") {
		t.Fatalf("expected an explicit max_tokens choice to override the MiMo default")
	}

	// 1b. The version decides, not a pinned generation: every o-digit run and every GPT
	// generation from 5 on (gpt-6-astra is already in the catalog) takes the new field,
	// while a version boundary is still required and provider prefixes / region tags are
	// stripped first.
	for _, model := range []string{
		"o2", "o5-mini", "o10", "gpt-5.1", "gpt-5-nano", "gpt-6", "gpt-6-astra", "gpt-50",
		"openai/o3-mini", "azure/gpt-5.1", "gpt-5.1@eu", "O4-MINI",
	} {
		if !shouldUseMaxCompletionTokens("auto", model) {
			t.Fatalf("expected %q to use max_completion_tokens under auto", model)
		}
	}
	// A family prefix without a version boundary, and a GPT generation before 5 (which
	// rejects both the effort field and max_completion_tokens).
	for _, model := range []string{"gpt-5x", "o1preview", "o3as", "gpt-4o", "gpt-4.1", "gpt-oss-120b"} {
		if shouldUseMaxCompletionTokens("auto", model) {
			t.Fatalf("expected %q to use max_tokens under auto", model)
		}
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

// TestAnthropicToolCallIDPairingUsesSharedSanitizer: the id rules themselves
// live in internal/tools/toolcall (see its tests); what matters here is that
// tool_use and tool_result of one turn go through the same sanitizer, or
// Anthropic rejects the turn with a mismatched pair.
func TestAnthropicToolCallIDPairingUsesSharedSanitizer(t *testing.T) {
	// buildAnthropicMessages must sanitize IDs consistently on both tool_use and tool_result
	_, messages := buildAnthropicMessages([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "run"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{
			{ID: "call:foo|bar.1", Function: legacyopenai.FunctionCall{Name: "fn", Arguments: `{"k":"v"}`}},
		}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "call:foo|bar.1", Content: `{"ok":true}`},
	}, nil, true)
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

func TestOpenAIResponsesMidTurnSystemAndDeduplication(t *testing.T) {
	// 1. Mid-turn system message becomes developer input item
	instructions, input := buildOpenAIResponsesInput([]legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "base instructions"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "hello"},
		{Role: legacyopenai.ChatMessageRoleAssistant, Content: "hi"},
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "steer reminder: stay concise"},
		{Role: legacyopenai.ChatMessageRoleUser, Content: "what is 1+1?"},
	}, nil)
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
	configureAnthropicThinking(&params37, "claude-3-7-sonnet-20250219", "high", 8192, anthropicOffPlan{})
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
	configureAnthropicThinking(&params46, "claude-sonnet-4.6", "medium", 8192, anthropicOffPlan{})
	if params46.Thinking.OfAdaptive == nil {
		t.Fatal("expected Thinking.OfAdaptive for claude-sonnet-4.6")
	}
	if string(params46.OutputConfig.Effort) != "medium" {
		t.Fatalf("expected OutputConfig.Effort = medium, got %q", params46.OutputConfig.Effort)
	}

	// 3. Effort "off": the explicit "stop thinking" form (the UI's 关闭思考).
	var paramsOff anthropic.MessageNewParams
	if enabled := configureAnthropicThinking(&paramsOff, "claude-3-7-sonnet-20250219", "off", 8192, anthropicOffPlan{disabledThinking: true}); enabled {
		t.Fatal("effort off must not report thinking as enabled")
	}
	if paramsOff.Thinking.OfDisabled == nil {
		t.Fatal("expected Thinking.OfDisabled when effort is off")
	}
}

// TestAnthropicOffPlanFollowsTheEndpoint pins the three ways "关闭思考" lands on the
// Messages wire, all resolved from the same vendor tables the Chat and Responses wires
// read (anthropicOffPlanFor): the thinking block where the endpoint documents that
// switch, DeepSeek's top-level reasoning.effort (no SDK field carries it, so the
// adapter adds the JSON key), the vendor's lowest level where its docs say the model
// cannot stop thinking, and nothing at all where the docs call the field ineffective or
// declare no such field.
func TestAnthropicOffPlanFollowsTheEndpoint(t *testing.T) {
	relay := ConfigState{APIFormat: apiFormatAnthropicMessages, BaseURL: "https://relay.example.com"}
	deepseek := ConfigState{APIFormat: apiFormatAnthropicMessages, BaseURL: "https://api.deepseek.com/anthropic"}
	tests := []struct {
		name  string
		cfg   ConfigState
		model string
		want  anthropicOffPlan
	}{
		{name: "a switch the endpoint documents", cfg: relay, model: "claude-3-7-sonnet-20250219", want: anthropicOffPlan{disabledThinking: true}},
		{name: "千问 documents the block on this wire too", cfg: relay, model: "qwen3.8-max", want: anthropicOffPlan{disabledThinking: true}},
		{name: "a relay keeps the generic block for a DeepSeek model", cfg: relay, model: "deepseek-v4-pro", want: anthropicOffPlan{disabledThinking: true}},
		{name: "DeepSeek's own endpoint spells it as reasoning.effort", cfg: deepseek, model: "deepseek-v4-pro", want: anthropicOffPlan{reasoningEffort: reasoningEffortOffWireValue}},
		{name: "Kimi K3 cannot stop thinking", cfg: relay, model: "kimi-k3", want: anthropicOffPlan{outputEffort: reasoningEffortLow}},
		{name: "GLM-5.3 cannot stop thinking", cfg: relay, model: "glm-5.3", want: anthropicOffPlan{outputEffort: reasoningEffortLow}},
		{name: "MiniMax M3.1 requires thinking", cfg: relay, model: "MiniMax-M3.1-Flash-Preview", want: anthropicOffPlan{outputEffort: reasoningEffortLow}},
		// A vendor whose current generation cannot stop thinking lands on the lowest
		// level whatever the generation: the row is keyed on the vendor, so a legacy id
		// whose docs ignore the field gets the same treatment (it is one the vendor
		// declares, just without a depth effect).
		{name: "MiniMax lands on its lowest level for off", cfg: relay, model: "MiniMax-M2.7", want: anthropicOffPlan{outputEffort: reasoningEffortLow}},
		{name: "MiMo declares no thinking field", cfg: relay, model: "mimo-v2.5-pro", want: anthropicOffPlan{}},
	}
	for _, tc := range tests {
		if got := anthropicOffPlanFor(tc.cfg, tc.model); got != tc.want {
			t.Errorf("%s: anthropicOffPlanFor(%q) = %+v, want %+v", tc.name, tc.model, got, tc.want)
		}
	}
}

// TestAnthropicReasoningEffortOptionLandsInTheBody covers the mechanism the DeepSeek
// endpoint needs: MessageNewParams carries Thinking and OutputConfig only, so the
// top-level `reasoning` object its docs spell 关闭思考 with has to be added as a raw JSON
// key (the SDK does exactly the same for `stream`). The key must reach the serialized
// body — the SDK refuses the option outright when the body is not a *bytes.Buffer, so a
// broken wiring fails loudly instead of silently dropping the field.
func TestAnthropicReasoningEffortOptionLandsInTheBody(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	client := anthropic.NewClient(
		anthropicoption.WithAPIKey("test-key"),
		anthropicoption.WithBaseURL(server.URL),
		anthropicoption.WithMaxRetries(0),
	)
	stream := client.Messages.NewStreaming(context.Background(), anthropic.MessageNewParams{
		Model:     anthropic.Model("deepseek-v4-pro"),
		MaxTokens: 64,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hi"))},
	}, anthropicoption.WithJSONSet("reasoning", map[string]string{"effort": reasoningEffortOffWireValue}))
	for stream.Next() {
	}
	_ = stream.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 request, got %d", len(bodies))
	}
	if want := `"reasoning":{"effort":"none"}`; !strings.Contains(bodies[0], want) {
		t.Fatalf("request body must contain %s: %s", want, bodies[0])
	}
}

// TestChatAdapterTurnsThinkingOffOnCompatibleEndpoint drives the "off" level end to
// end on a relay: the request carries the stop-thinking spelling the model's vendor
// documents — thinking:{"type":"disabled"} for DeepSeek (the field its own sample
// passes through extra_body, because the OpenAI Chat schema has no such field),
// enable_thinking:false for 千问 — nothing at all where writing the field would be a
// lie (MiniMax M2.x accepts it and keeps thinking on; MiMo declares no thinking switch
// anywhere), and the vendor's lowest level where its docs say the model cannot stop
// thinking at all (Kimi K3 declares no thinking field, GLM-5.3 and MiniMax M3.1 reject
// the off value). The assistant turn already in the history must not gain the reasoning
// placeholder either: with thinking off there is no reasoning to hand back.
func TestChatAdapterTurnsThinkingOffOnCompatibleEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		wantBody   []string
		absentBody []string
		// wantEffort marks the models whose docs say they cannot stop thinking: there
		// "off" lands on the lowest level, so an effort value is exactly what belongs on
		// the wire (see reasoningWireForAdapter).
		wantEffort bool
	}{
		{
			name:     "DeepSeek's spelling",
			model:    "deepseek-chat",
			wantBody: []string{`"thinking":{"type":"disabled"}`},
		},
		{
			name:       "千问 closes thinking with enable_thinking",
			model:      "qwen3.8-max",
			wantBody:   []string{`"enable_thinking":false`},
			absentBody: []string{`"thinking":`},
		},
		{
			name:       "MiniMax cannot stop thinking, so off lands on its lowest level",
			model:      "MiniMax-M2.7",
			wantBody:   []string{`"reasoning_effort":"low"`},
			absentBody: []string{`"thinking":`, `"enable_thinking"`},
			wantEffort: true,
		},
		{
			name:       "MiMo documents no thinking switch at all, so nothing is sent",
			model:      "mimo-v2.5-pro",
			absentBody: []string{`"thinking":`, `"enable_thinking"`},
		},
		{
			name:       "Kimi K3 cannot stop thinking, so off lands on its lowest level",
			model:      "kimi-k3",
			wantBody:   []string{`"reasoning_effort":"low"`},
			absentBody: []string{`"thinking":`, `"enable_thinking"`},
			wantEffort: true,
		},
		{
			name:       "GLM-5.3 rejects disabled, so off lands on its lowest level",
			model:      "glm-5.3",
			wantBody:   []string{`"reasoning_effort":"low"`},
			absentBody: []string{`"thinking":`, `"enable_thinking"`},
			wantEffort: true,
		},
		{
			name:       "MiniMax M3.1 requires thinking, so off lands on its lowest level",
			model:      "MiniMax-M3.1-Flash-Preview",
			wantBody:   []string{`"reasoning_effort":"low"`},
			absentBody: []string{`"thinking":`, `"enable_thinking"`},
			wantEffort: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, string(raw))
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
				fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()

			cfg := ConfigState{
				APIFormat:       apiFormatOpenAIChat,
				BaseURL:         server.URL,
				APIKeys:         []string{"test-key"},
				ReasoningEffort: reasoningEffortOff,
				ReasoningTag:    defaultReasoningTag,
			}
			messages := []legacyopenai.ChatCompletionMessage{
				{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"},
				{Role: legacyopenai.ChatMessageRoleAssistant, Content: "plain answer"},
				{Role: legacyopenai.ChatMessageRoleUser, Content: "hi again"},
			}
			if _, err := NewApp().streamModelResponse(context.Background(), cfg, tc.model, messages, nil, nil); err != nil {
				t.Fatalf("streamModelResponse() error = %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != 1 {
				t.Fatalf("expected 1 request, got %d", len(bodies))
			}
			for _, want := range tc.wantBody {
				if !strings.Contains(bodies[0], want) {
					t.Fatalf("request body must contain %s: %s", want, bodies[0])
				}
			}
			for _, absent := range tc.absentBody {
				if strings.Contains(bodies[0], absent) {
					t.Fatalf("request body must not contain %s: %s", absent, bodies[0])
				}
			}
			if !tc.wantEffort && strings.Contains(bodies[0], "reasoning_effort") {
				t.Fatalf("\"off\" on a model with a working switch must not also carry an effort value: %s", bodies[0])
			}
			if strings.Contains(bodies[0], "reasoning_content") {
				t.Fatalf("\"off\" leaves nothing to hand back, so no reasoning field may be added: %s", bodies[0])
			}
		})
	}
}

// TestOpenAIResponsesRequestTurnsThinkingOff: on the Responses wire "off" is
// effort "none" (DeepSeek documents "none" as 关闭思考模式) and no summary is
// requested, since there is nothing to summarize.
func TestOpenAIResponsesRequestTurnsThinkingOff(t *testing.T) {
	cfg := ConfigState{
		APIFormat:       apiFormatOpenAIResponses,
		BaseURL:         defaultOpenAIResponsesURL,
		MaxTokens:       64,
		ReasoningEffort: reasoningEffortOff,
		ReasoningTag:    defaultReasoningTag,
	}
	messages := []legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}
	body := buildOpenAIResponsesRequest(cfg, "gpt-5.5", messages, nil, nil)
	if body.Reasoning.Effort != oa.ReasoningEffort(reasoningEffortOffWireValue) {
		t.Fatalf("reasoning.effort = %q, want %q", body.Reasoning.Effort, reasoningEffortOffWireValue)
	}
	if body.Reasoning.Summary != "" {
		t.Fatalf("reasoning.summary = %q, want unset while thinking is off", body.Reasoning.Summary)
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
	markAnthropicPromptCacheBreakpoints(&params, anthropicOfficialCfg)
	lastTool := params.Tools[1].OfTool
	if lastTool == nil || lastTool.CacheControl.TTL == "" {
		t.Fatal("expected CacheControl on the last tool definition")
	}
	firstTool := params.Tools[0].OfTool
	if firstTool != nil && firstTool.CacheControl.TTL != "" {
		t.Fatal("first tool should not have cache breakpoint")
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
	item := responseInputItemParamOfFunctionCall(call, nil)
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
	_, anthropicMsgs := buildAnthropicMessages(messages, nil, true)
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

func TestParseStreamReasoningHandlesArrayDetails(t *testing.T) {
	chunk := func(delta map[string]any) []byte {
		raw, err := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": delta}}})
		if err != nil {
			t.Fatalf("marshal chunk: %v", err)
		}
		return raw
	}
	// A relay that mirrors both forms: the plain reasoning string wins for
	// display, the structured details are still captured for replay.
	text, details := parseStreamReasoning(chunk(map[string]any{
		"reasoning": "deep reasoning here",
		"reasoning_details": []map[string]any{
			{"type": "reasoning.text", "text": "deep reasoning here"},
		},
	}))
	if text != "deep reasoning here" {
		t.Fatalf("reasoning text = %q, want the reasoning field", text)
	}
	if len(details) != 1 {
		t.Fatalf("details = %+v, want the streamed detail object", details)
	}

	// OpenRouter streams only the details array: text and summary fragments are
	// concatenated (they arrive one token at a time), encrypted details stay
	// discrete.
	text, details = parseStreamReasoning(chunk(map[string]any{
		"reasoning_details": []map[string]any{
			{"type": "reasoning.text", "text": "step ", "signature": "sig-1"},
			{"type": "reasoning.text", "text": "two"},
			{"type": "reasoning.encrypted", "data": "enc-1"},
		},
	}))
	if text != "step two" {
		t.Fatalf("details text = %q, want the concatenated fragments", text)
	}
	if len(details) != 2 {
		t.Fatalf("details = %+v, want one merged text entry plus the encrypted entry", details)
	}
	if details[0]["text"] != "step two" || details[0]["signature"] != "sig-1" {
		t.Fatalf("merged text detail = %+v, want joined text with the first signature", details[0])
	}

	// A summary-only details array still feeds the thinking panel and merges the
	// same way across fragments.
	text, details = parseStreamReasoning(chunk(map[string]any{
		"reasoning_details": []map[string]any{
			{"type": "reasoning.summary", "summary": "short "},
			{"type": "reasoning.summary", "summary": "version"},
		},
	}))
	if text != "short version" || len(details) != 1 || details[0]["summary"] != "short version" {
		t.Fatalf("summary details = %q / %+v, want the concatenated summary", text, details)
	}
}

// TestBuildOpenAIResponsesInputReplaysToolItemID covers the follower-id rule: a
// replayed reasoning item is paired with the id of the item that follows it, so
// the function_call item must carry the id the model emitted for that call.
func TestBuildOpenAIResponsesInputReplaysToolItemID(t *testing.T) {
	messages := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "go"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{{
			ID:       "call_1",
			Type:     legacyopenai.ToolTypeFunction,
			Function: legacyopenai.FunctionCall{Name: "read", Arguments: "{}"},
		}}},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "call_1", Content: "ok"},
	}
	replay := &sessionReasoningPayload{turns: []reasoningTurn{{
		callIDs:        []string{"call_1"},
		responses:      []responsesReasoningItem{{ID: "rs_1", EncryptedContent: "enc"}},
		responsesItems: map[string]string{"call_1": "fc_item_1"},
	}}}
	_, input := buildOpenAIResponsesInput(messages, replay)
	// user -> reasoning item -> function_call -> function_call output
	if len(input) != 4 {
		t.Fatalf("input items = %d, want 4: %+v", len(input), input)
	}
	if input[1].OfReasoning == nil {
		t.Fatalf("input[1] = %+v, want the captured reasoning item before its function_call", input[1])
	}
	if call := input[2].OfFunctionCall; call == nil || call.ID.Value != "fc_item_1" {
		t.Fatalf("function_call = %+v, want the captured item id", input[2])
	}
	// A call from an older turn (or another model) has no captured id: the
	// field stays absent instead of being invented.
	_, bare := buildOpenAIResponsesInput(messages, nil)
	if len(bare) != 3 || bare[1].OfFunctionCall == nil || bare[1].OfFunctionCall.ID.Valid() {
		t.Fatalf("foreign turn must not carry an item id: %+v", bare)
	}
	// An id that cannot be echoed back verbatim is dropped rather than sent.
	unsafeTurn := &sessionReasoningPayload{turns: []reasoningTurn{{
		callIDs:        []string{"call_1"},
		responses:      []responsesReasoningItem{{ID: "rs_2"}},
		responsesItems: map[string]string{"call_1": "fc bad|id"},
	}}}
	_, unsafe := buildOpenAIResponsesInput(messages, unsafeTurn)
	if unsafe[2].OfFunctionCall == nil || unsafe[2].OfFunctionCall.ID.Valid() {
		t.Fatalf("unsafe item id must be dropped: %+v", unsafe[2].OfFunctionCall)
	}
}

// TestEnsureResponsesReasoningSummaries guards the required-but-omitzero summary
// key on replayed reasoning items.
func TestEnsureResponsesReasoningSummaries(t *testing.T) {
	body := map[string]any{
		"input": []any{
			map[string]any{"type": "reasoning", "id": "rs_1", "encrypted_content": "enc"},
			map[string]any{"type": "function_call", "call_id": "call_1"},
			map[string]any{"type": "reasoning", "id": "rs_2", "summary": []any{map[string]any{"type": "summary_text", "text": "kept"}}},
		},
	}
	if !ensureResponsesReasoningSummaries(body) {
		t.Fatal("a reasoning item without a summary key must be completed")
	}
	items := body["input"].([]any)
	if summary, ok := items[0].(map[string]any)["summary"].([]any); !ok || len(summary) != 0 {
		t.Fatalf("summary = %#v, want an empty array", items[0].(map[string]any)["summary"])
	}
	// The wire form matters: a nil slice would marshal as null instead of [],
	// which is not the empty array the API accepts for a replayed item.
	wire, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	if !strings.Contains(string(wire), "\"summary\":[]") {
		t.Fatalf("wire form %s must carry an empty summary array", wire)
	}
	if _, exists := items[1].(map[string]any)["summary"]; exists {
		t.Fatal("only reasoning items may gain a summary key")
	}
	if summary, ok := items[2].(map[string]any)["summary"].([]any); !ok || len(summary) != 1 {
		t.Fatalf("an existing summary must be preserved: %#v", items[2].(map[string]any)["summary"])
	}
	if ensureResponsesReasoningSummaries(body) {
		t.Fatal("the normalization must be idempotent")
	}
}

func TestOpenAIResponsesMaxOutputTokensFloor(t *testing.T) {
	cfg := ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: defaultOpenAIResponsesURL, MaxTokens: 4}
	body := buildOpenAIResponsesRequest(cfg, "gpt-5.5", nil, nil, nil)
	if !body.MaxOutputTokens.Valid() || body.MaxOutputTokens.Value != 16 {
		t.Fatalf("max_output_tokens = %+v, want the documented floor of 16", body.MaxOutputTokens)
	}
	for _, tc := range []struct{ in, want int64 }{{4, 16}, {15, 16}, {16, 16}, {17, 17}, {4096, 4096}} {
		cfg.MaxTokens = int(tc.in)
		got := buildOpenAIResponsesRequest(cfg, "gpt-5.5", nil, nil, nil).MaxOutputTokens.Value
		if got != tc.want {
			t.Fatalf("max_output_tokens for %d = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestModelUsageFromResponsesDerivesUncachedInput pins the cache arithmetic on
// the mixed case: input_tokens includes the cached and cache-write subsets, so
// the miss side is input - cached. Reading cache_write_tokens as the miss side
// would under-count it (and inflate the reported hit rate).
func TestModelUsageFromResponsesDerivesUncachedInput(t *testing.T) {
	usage := modelUsageFromResponses(oaresp.ResponseUsage{
		InputTokens:  1000,
		OutputTokens: 8,
		InputTokensDetails: oaresp.ResponseUsageInputTokensDetails{
			CachedTokens:     800,
			CacheWriteTokens: 100,
		},
	})
	if usage == nil {
		t.Fatal("modelUsageFromResponses() returned nil")
	}
	if usage.PromptTokens != 1000 || usage.CacheHitTokens != 800 || usage.CacheMissTokens != 200 {
		t.Fatalf("usage = %+v, want prompt 1000 hit 800 miss 200", usage)
	}
}

// TestConfigureAnthropicThinkingTinyMaxTokens covers the impossible budget: the
// API requires 1024 <= budget_tokens < max_tokens, so a smaller output cap must
// leave extended thinking unset instead of sending a request it rejects.
func TestConfigureAnthropicThinkingTinyMaxTokens(t *testing.T) {
	params := anthropic.MessageNewParams{}
	if enabled := configureAnthropicThinking(&params, "claude-3-7-sonnet-20250219", "high", 512, anthropicOffPlan{}); enabled {
		t.Fatal("thinking must stay disabled when max_tokens cannot hold the minimum budget")
	}
	if params.Thinking.OfEnabled != nil || params.Thinking.OfAdaptive != nil {
		t.Fatalf("thinking = %+v, want unset", params.Thinking)
	}
}

// TestIsAnthropicAdaptiveThinkingModel pins the generation rule: adaptive thinking arrived
// with Claude 4.6, and from 4.7 the manual shape (enabled + budget_tokens) answers 400, so
// a generation read wrong sends a shape the endpoint rejects. The rule reads the version
// number instead of listing ids (see isAnthropicAdaptiveThinkingModel), which is what makes
// Claude's next release inherit it.
func TestIsAnthropicAdaptiveThinkingModel(t *testing.T) {
	// Every way the ids spell their generation: family first (4-6), version first with a date
	// stamp (3-7-sonnet-20250219), no separator (opus4-6), inside a Bedrock / region wrapper,
	// and with no version at all (an alias for the current generation).
	adaptive := []string{
		"claude-sonnet-4.6", "claude-sonnet-4-6", "claude-opus-4.6", "claude-opus-4-6",
		"claude-opus-4.7", "claude-opus-4-8", "claude-sonnet-5", "claude-opus-5",
		"claude-haiku-5", "claude-5-foo", "claude-fable", "claude-mythos",
		"claude-fable-5", "claude-fable-5-1", "claude-opus-5-fast", "claude-code",
		"claude-opus4-6", "us.anthropic.claude-opus-4-6-v1", "claude-opus-4-6@eu",
		"  Claude-Opus-4-6  ",
	}
	for _, model := range adaptive {
		if !isAnthropicAdaptiveThinkingModel(model) {
			t.Fatalf("%q must be treated as an adaptive-thinking model", model)
		}
	}
	// The generations that take the manual shape only (4.5 and older), and ids of other
	// vendors carrying numbers high enough to be misread as a Claude version.
	manual := []string{
		"claude-3-7-sonnet-20250219", "claude-3-7-sonnet-latest", "claude-sonnet-4-5",
		"claude-opus-4-1", "claude-haiku-4-5", "claude-sonnet-4-20250514",
		"claude-3-5-sonnet-20241022", "claude-3-opus-latest", "deepseek-r1",
		"glm-5.3", "MiniMax-M3", "gpt-5.6", "some-relay-model",
	}
	for _, model := range manual {
		if isAnthropicAdaptiveThinkingModel(model) {
			t.Fatalf("%q must not be treated as an adaptive-thinking model", model)
		}
	}
}

// TestResponsesToolItemIDCaptureAndCompleteness guards the pairing contract: the
// captured follower id is keyed the way the replay path looks it up, ids that
// cannot be echoed verbatim are dropped, and the reasoning replay is skipped when
// a follower id is missing.
func TestResponsesToolItemIDCaptureAndCompleteness(t *testing.T) {
	call := legacyopenai.ToolCall{ID: "call:foo|bar.1", Function: legacyopenai.FunctionCall{Name: "read"}}
	ids := map[string]string{}
	captureResponsesToolItemID(ids, call, oaresp.ResponseOutputItemUnion{Type: "function_call", ID: "fc_1", CallID: call.ID})
	if got := ids[toolcall.ForResponsesCall(call.ID)]; got != "fc_1" {
		t.Fatalf("captured ids = %+v, want the sanitized call id as key", ids)
	}
	unsafe := map[string]string{}
	captureResponsesToolItemID(unsafe, call, oaresp.ResponseOutputItemUnion{Type: "function_call", ID: "fc bad|id", CallID: call.ID})
	if len(unsafe) != 0 {
		t.Fatalf("ids that cannot be echoed verbatim must be dropped: %+v", unsafe)
	}

	turn := &reasoningTurn{
		callIDs:        []string{"call_1"},
		responses:      []responsesReasoningItem{{ID: "rs_1"}},
		responsesItems: map[string]string{"call_1": "fc_1"},
	}
	calls := []legacyopenai.ToolCall{{ID: "call_1"}}
	if !responsesTurnItemsComplete(turn, calls) {
		t.Fatal("a complete pairing must allow the replay")
	}
	if responsesTurnItemsComplete(turn, []legacyopenai.ToolCall{{ID: "call_1"}, {ID: "call_2"}}) {
		t.Fatal("a missing follower id must block the replay")
	}
	if responsesTurnItemsComplete(nil, calls) {
		t.Fatal("a turn without captured items must block the replay")
	}
	if responsesTurnItemsComplete(&reasoningTurn{callIDs: []string{"call_1"}, responses: []responsesReasoningItem{{ID: "rs_1"}}}, calls) {
		t.Fatal("a turn without item ids must block the replay")
	}
}

func TestSessionAffinityHeadersOnlyForOpenRouter(t *testing.T) {
	cfg := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: "https://openrouter.ai/api/v1", responsesPromptCacheKey: "ses-key"}
	if headers := sessionAffinityHeaders(cfg); headers["x-session-id"] != "ses-key" {
		t.Fatalf("headers = %+v, want the OpenRouter session header", headers)
	}
	cfg.BaseURL = "https://api.openai.com/v1"
	if headers := sessionAffinityHeaders(cfg); len(headers) != 0 {
		t.Fatalf("headers = %+v, want none for a non-OpenRouter endpoint", headers)
	}
	cfg.BaseURL = "https://openrouter.ai/api/v1"
	cfg.responsesPromptCacheKey = ""
	if headers := sessionAffinityHeaders(cfg); len(headers) != 0 {
		t.Fatalf("headers = %+v, want none without a session key", headers)
	}
}

func TestAnthropicInterleavedThinkingBeta(t *testing.T) {
	if got := anthropicInterleavedThinkingBeta(true, true, "claude-3-7-sonnet-20250219"); got != "interleaved-thinking-2025-05-14" {
		t.Fatalf("beta = %q, want the interleaved-thinking flag", got)
	}
	cases := []struct {
		name     string
		thinking bool
		tools    bool
		model    string
	}{
		{name: "thinking off", thinking: false, tools: true, model: "claude-3-7-sonnet-20250219"},
		{name: "no tools", thinking: true, tools: false, model: "claude-3-7-sonnet-20250219"},
		{name: "adaptive model", thinking: true, tools: true, model: "claude-opus-4-6"},
	}
	for _, tt := range cases {
		if got := anthropicInterleavedThinkingBeta(tt.thinking, tt.tools, tt.model); got != "" {
			t.Fatalf("%s: beta = %q, want none", tt.name, got)
		}
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

	system, anthropicMsgs := buildAnthropicMessages(messages, nil, true)
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

// TestBuildAnthropicMessagesKeepsToolResultFirstInUserTurn pins the Messages API
// rule that a message following tool_use blocks must begin with its tool_result
// blocks. A mid-turn system reminder landing between the assistant's tool_use and
// its tool results used to be merged into the same user turn ahead of them,
// producing [text, tool_result] — rejected with "…must begin with a matching
// number of tool_result blocks".
func TestBuildAnthropicMessagesKeepsToolResultFirstInUserTurn(t *testing.T) {
	messages := []legacyopenai.ChatCompletionMessage{
		{Role: legacyopenai.ChatMessageRoleUser, Content: "question"},
		{Role: legacyopenai.ChatMessageRoleAssistant, ToolCalls: []legacyopenai.ToolCall{
			{ID: "t1", Function: legacyopenai.FunctionCall{Name: "grep", Arguments: `{"a":1}`}},
		}},
		{Role: legacyopenai.ChatMessageRoleSystem, Content: "mid-turn reminder"},
		{Role: legacyopenai.ChatMessageRoleTool, ToolCallID: "t1", Content: `{"ok":true}`},
	}
	_, msgs := buildAnthropicMessages(messages, nil, true)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 turns (user, assistant, user), got %d", len(msgs))
	}
	last := msgs[2]
	if last.Role != anthropic.MessageParamRoleUser {
		t.Fatalf("expected the last turn to be user, got %v", last.Role)
	}
	if len(last.Content) != 2 {
		t.Fatalf("expected 2 blocks (tool_result + <system>), got %d", len(last.Content))
	}
	if last.Content[0].OfToolResult == nil {
		t.Fatalf("the tool_result block must come first, got %+v", last.Content[0])
	}
	if last.Content[1].OfText == nil || !strings.Contains(last.Content[1].OfText.Text, "mid-turn reminder") {
		t.Fatalf("the reminder must follow the tool_result, got %+v", last.Content[1])
	}
}

// TestAnthropicTopLevelCacheControlOnlyForEndpointsThatDocumentIt pins the two
// spellings of the cache breakpoint. The official endpoint takes the markers nested in
// the request, Moonshot's Messages endpoint documents cache_control as top-level-only
// and writes no cache entry at all when that field is absent, and any other compatible
// gateway keeps the nested markers it already understood — nothing extra is added for
// it.
func TestAnthropicTopLevelCacheControlOnlyForEndpointsThatDocumentIt(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		wantTop bool
		// wantTTL is the ttl carried by the nested breakpoints: only the official
		// endpoint spells out the provider default.
		wantTTL string
	}{
		{name: "official", baseURL: "https://api.anthropic.com", wantTTL: "5m"},
		{name: "Moonshot documents the top-level form", baseURL: "https://api.moonshot.cn/anthropic", wantTop: true},
		{name: "another compatible gateway", baseURL: "https://api.deepseek.com/anthropic"},
	}
	for _, tc := range cases {
		params := anthropic.MessageNewParams{
			System:   []anthropic.TextBlockParam{{Text: "system"}},
			Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("question"))},
		}
		cfg := ConfigState{APIFormat: apiFormatAnthropicMessages, BaseURL: tc.baseURL}
		markAnthropicPromptCacheBreakpoints(&params, cfg)

		if got := params.CacheControl.Type == "ephemeral"; got != tc.wantTop {
			t.Fatalf("%s: top-level cache_control present = %v, want %v", tc.name, got, tc.wantTop)
		}
		if tc.wantTop && params.CacheControl.TTL != "" {
			t.Fatalf("%s: a gateway must not receive the ttl field, got %q", tc.name, params.CacheControl.TTL)
		}
		if got := string(params.System[0].CacheControl.TTL); got != tc.wantTTL {
			t.Fatalf("%s: system breakpoint ttl = %q, want %q", tc.name, got, tc.wantTTL)
		}
		if params.System[0].CacheControl.Type != "ephemeral" {
			t.Fatalf("%s: the nested breakpoints must stay in place", tc.name)
		}
	}
}

// TestAnthropicThinkingShapeFollowsTheEndpointFamily pins that the thinking shape
// is decided by what the endpoint family documents, not by the shape of the model
// id: MiniMax accepts thinking.type "disabled"/"adaptive" only, so the `enabled` +
// budget_tokens form would be rejected there, and its `display` field is
// undocumented so it stays unset.
func TestAnthropicThinkingShapeFollowsTheEndpointFamily(t *testing.T) {
	var minimax anthropic.MessageNewParams
	if enabled := configureAnthropicThinking(&minimax, "MiniMax-M2.7", "high", 8192, anthropicOffPlan{}); !enabled {
		t.Fatal("effort high on MiniMax must report thinking as enabled")
	}
	if minimax.Thinking.OfAdaptive == nil {
		t.Fatal("MiniMax must receive the adaptive shape: its endpoint does not accept `enabled`")
	}
	if minimax.Thinking.OfEnabled != nil {
		t.Fatal("MiniMax must not receive budget_tokens thinking")
	}
	if minimax.Thinking.OfAdaptive.Display != "" {
		t.Fatalf("MiniMax does not document display; want it unset, got %q", minimax.Thinking.OfAdaptive.Display)
	}
	if string(minimax.OutputConfig.Effort) != "high" {
		t.Fatalf("MiniMax output_config.effort = %q, want high", minimax.OutputConfig.Effort)
	}
	// The interleaved-thinking beta gate reads the same predicate, so an adaptive
	// family must not be handed the flag either.
	if got := anthropicInterleavedThinkingBeta(true, true, "MiniMax-M2.7"); got != "" {
		t.Fatalf("interleaved beta = %q, want none for an adaptive-thinking family", got)
	}

	// A Claude adaptive model keeps the documented display field.
	var adaptiveClaude anthropic.MessageNewParams
	configureAnthropicThinking(&adaptiveClaude, "claude-opus-4-6", "high", 8192, anthropicOffPlan{})
	if adaptiveClaude.Thinking.OfAdaptive == nil || adaptiveClaude.Thinking.OfAdaptive.Display == "" {
		t.Fatal("a Claude adaptive model must keep display: summarized")
	}

	// A budget-shape Claude model is untouched.
	var budgetClaude anthropic.MessageNewParams
	configureAnthropicThinking(&budgetClaude, "claude-3-7-sonnet-20250219", "high", 8192, anthropicOffPlan{})
	if budgetClaude.Thinking.OfEnabled == nil {
		t.Fatal("claude-3.7-sonnet must keep the budget_tokens shape")
	}

	// A family that documents no thinking parameter never carries the interleaved
	// beta: there is no thinking block for the flag to apply to.
	if got := anthropicInterleavedThinkingBeta(true, true, "kimi-k3"); got != "" {
		t.Fatalf("interleaved beta = %q, want none for a family with no thinking parameter", got)
	}
}

// TestAnthropicThinkingFamiliesCarryTheLevelWhereTheEndpointDocumentsIt pins the
// per-endpoint spelling of a thinking level: the value must reach the field the
// vendor documents. DeepSeek / 千问 / Kimi K3 all serve an Anthropic-compatible
// endpoint whose docs put the strength in output_config.effort — DeepSeek ignores
// budget_tokens, 千问 marks it deprecated, Kimi K3 rejects the thinking parameter —
// so a budget_tokens-only request left the user's selection out of the body and the
// provider ran its own default (usually the slowest and most expensive one) while
// the UI kept showing the chosen level.
//
// A model no row matches keeps the Claude budget shape: an unrecognized id on an
// Anthropic-protocol entry is most likely a relay serving a Claude model, and the
// official API validates that shape.
func TestAnthropicThinkingFamiliesCarryTheLevelWhereTheEndpointDocumentsIt(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		level          string
		wantConfigured bool
		wantShape      string // "", "enabled", "adaptive", "disabled"
		wantEffort     string
		wantDisplay    string
	}{
		{name: "Claude 3.7 keeps the budget shape and no effort field (a 400 there)", model: "claude-3-7-sonnet-20250219", level: "high", wantConfigured: true, wantShape: "enabled", wantDisplay: "summarized"},
		{name: "Claude adaptive", model: "claude-opus-4-7", level: "high", wantConfigured: true, wantShape: "adaptive", wantEffort: "high", wantDisplay: "summarized"},
		{name: "MiniMax adaptive without display", model: "MiniMax-M2.7", level: "high", wantConfigured: true, wantShape: "adaptive", wantEffort: "high"},
		{name: "DeepSeek carries the level in output_config.effort", model: "deepseek-v4-pro", level: "medium", wantConfigured: true, wantShape: "enabled", wantEffort: "high"},
		{name: "DeepSeek low", model: "deepseek-v4-pro", level: "low", wantConfigured: true, wantShape: "enabled", wantEffort: "low"},
		{name: "DeepSeek xhigh", model: "deepseek-v4-pro", level: "xhigh", wantConfigured: true, wantShape: "enabled", wantEffort: "high"},
		{name: "Kimi K3 takes no thinking parameter", model: "kimi-k3", level: "xhigh", wantConfigured: true, wantEffort: "max"},
		{name: "千问 passes every level through and writes the switch without a budget", model: "qwen3.8-max", level: "xhigh", wantConfigured: true, wantShape: "enabled", wantEffort: "xhigh"},
		// 智谱 documents the strength in output_config.effort and its Anthropic page has no
		// field-level list, so the request keeps the documented thinking block and adds the
		// one field the vendor declares. The row is keyed on the vendor, so every generation
		// gets the level its row maps to.
		{name: "智谱 carries the level beside its block", model: "glm-5.3", level: "high", wantConfigured: true, wantShape: "enabled", wantEffort: "high"},
		{name: "智谱 maps a level its narrow set does not hold", model: "glm-5.2", level: "medium", wantConfigured: true, wantShape: "enabled", wantEffort: "high"},
		{name: "an older 智谱 id takes the vendor row", model: "glm-4.7", level: "high", wantConfigured: true, wantShape: "enabled", wantEffort: "high"},
		{name: "MiMo documents no thinking configuration, so nothing is written", model: "mimo-v2.5-pro", level: "high"},
		{name: "an unknown relay model keeps the Claude shape", model: "some-relay-model", level: "high", wantConfigured: true, wantShape: "enabled", wantDisplay: "summarized"},
		// A model whose docs say it cannot stop thinking lands on its lowest level,
		// instead of a field the endpoint never declared (Kimi K3) or rejects (GLM-5.3).
		{name: "off on Kimi K3 falls back to its lowest level", model: "kimi-k3", level: "off", wantEffort: "low"},
		{name: "off on GLM-5.3 falls back to its lowest level", model: "glm-5.3", level: "off", wantEffort: "low"},
		{name: "off keeps the thinking block where the docs declare it", model: "deepseek-v4-pro", level: "off", wantShape: "disabled"},
		{name: "off on MiMo writes nothing", model: "mimo-v2.5-pro", level: "off"},
		{name: "auto sends nothing", model: "deepseek-v4-pro", level: "auto"},
	}
	// The off plan is resolved from (endpoint, model) exactly as the adapter does it;
	// this relay is not DeepSeek's own endpoint, so a DeepSeek model keeps the generic
	// spelling there (see TestAnthropicOffPlanFollowsTheEndpoint).
	relayCfg := ConfigState{APIFormat: apiFormatAnthropicMessages, BaseURL: "https://relay.example.com"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var params anthropic.MessageNewParams
			if got := configureAnthropicThinking(&params, tc.model, tc.level, 8192, anthropicOffPlanFor(relayCfg, tc.model)); got != tc.wantConfigured {
				t.Fatalf("configured = %v, want %v", got, tc.wantConfigured)
			}
			// The shape is read off the marshalled block instead of the union's pointers:
			// a switchOnly family（千问）carries the same bytes through param.Override, which
			// sets none of them (see anthropicEnabledSwitchOnlyBlock).
			shape, display := "", ""
			if raw, err := json.Marshal(params.Thinking); err == nil {
				var block struct {
					Type    string `json:"type"`
					Display string `json:"display"`
				}
				_ = json.Unmarshal(raw, &block)
				shape, display = block.Type, block.Display
			}
			if shape != tc.wantShape {
				t.Fatalf("thinking shape = %q, want %q (%+v)", shape, tc.wantShape, params.Thinking)
			}
			if got := string(params.OutputConfig.Effort); got != tc.wantEffort {
				t.Fatalf("output_config.effort = %q, want %q", got, tc.wantEffort)
			}
			if display != tc.wantDisplay {
				t.Fatalf("display = %q, want %q", display, tc.wantDisplay)
			}
		})
	}
}

// TestAnthropicThinkingFamiliesStayUsable fails on a half-filled row instead of
// letting it reach a request: a row without a shape silently falls back to the Claude
// budget shape, and a row without an effort resolver drops the level — both look like
// a working request on the wire.
func TestAnthropicThinkingFamiliesStayUsable(t *testing.T) {
	for _, family := range anthropicThinkingFamilies {
		if strings.TrimSpace(family.name) == "" || family.matches == nil {
			t.Fatalf("row %+v must carry a name and a matcher", family)
		}
		switch family.shape {
		case anthropicShapeAdaptive, anthropicShapeEnabled, anthropicShapeNone, anthropicShapeUndocumented:
		default:
			t.Fatalf("row %q declares shape %q, which is none of the documented shapes", family.name, family.shape)
		}
		// A row whose endpoint documents no thinking configuration has no field for the
		// level to travel in, so it must not carry a resolver nobody would ever call.
		if family.shape == anthropicShapeUndocumented {
			if family.effort != nil {
				t.Fatalf("row %q writes nothing, so an effort resolver would never run", family.name)
			}
			continue
		}
		if family.effort == nil {
			t.Fatalf("row %q must carry an effort resolver", family.name)
		}
	}
}

// TestAnthropicRequestCarriesTheVendorEffortField drives the selected level onto the
// wire: the body must carry output_config.effort, the field these
// Anthropic-compatible endpoints document for the thinking strength, and a family
// that documents no thinking parameter must not be handed one.
// TestAnthropicSwitchOnlyBlockCarriesNoBudget pins the shape a switchOnly family gets:
// the thinking switch travels, the deprecated budget field does not. The SDK tags
// budget_tokens as required, so this shape exists only because the adapter writes those
// bytes itself (see anthropicEnabledSwitchOnlyBlock); getting it wrong would either
// resend the deprecated field or drop the switch entirely.
func TestAnthropicSwitchOnlyBlockCarriesNoBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		display bool
		want    string
	}{
		{name: "without display", want: `"thinking":{"type":"enabled"}`},
		{name: "with display", display: true, want: `"thinking":{"display":"summarized","type":"enabled"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := anthropic.MessageNewParams{Model: "qwen3.8-max", MaxTokens: 8192}
			params.Thinking = anthropicEnabledSwitchOnlyBlock(tc.display)
			raw, err := json.Marshal(params)
			if err != nil {
				t.Fatalf("marshal anthropic params: %v", err)
			}
			if !strings.Contains(string(raw), tc.want) {
				t.Fatalf("thinking block = %s, want it to contain %s", raw, tc.want)
			}
			if strings.Contains(string(raw), "budget_tokens") {
				t.Fatalf("a switchOnly family must not receive the deprecated budget field: %s", raw)
			}
		})
	}
}

func TestAnthropicRequestCarriesTheVendorEffortField(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		level      string
		wantBody   []string
		absentBody []string
	}{
		{
			name:  "DeepSeek",
			model: "deepseek-v4-pro",
			level: "medium",
			// DeepSeek documents output_config with effort, and accepts the thinking
			// block while ignoring its budget_tokens.
			wantBody: []string{`"output_config":{"effort":"high"}`, `"type":"enabled"`},
		},
		{
			// 千问 documents the switch (thinking.type) but marks budget_tokens「即将废弃，新
			// 接入建议改用 output_config.effort」, so the block goes out without it while the
			// level still travels in output_config.effort.
			name:       "千问 sends the switch without the deprecated budget",
			model:      "qwen3.8-max",
			level:      "high",
			wantBody:   []string{`"output_config":{"effort":"high"}`, `"thinking":{"type":"enabled"}`},
			absentBody: []string{`budget_tokens`},
		},
		{
			name:       "Kimi K3",
			model:      "kimi-k3",
			level:      "low",
			wantBody:   []string{`"output_config":{"effort":"low"}`},
			absentBody: []string{`"thinking"`},
		},
		{
			// K3 declares no thinking parameter and its docs say thinking cannot be turned
			// off (“不希望思考太长就把 reasoning_effort 设为 low”), so off lands on the
			// lowest level instead of a field the endpoint never declared.
			name:       "Kimi K3 cannot stop thinking",
			model:      "kimi-k3",
			level:      "off",
			wantBody:   []string{`"output_config":{"effort":"low"}`},
			absentBody: []string{`"thinking"`},
		},
		{
			// GLM-5.3 rejects thinking.type=disabled on every wire it serves.
			name:       "GLM-5.3 cannot stop thinking",
			model:      "glm-5.3",
			level:      "off",
			wantBody:   []string{`"output_config":{"effort":"low"}`},
			absentBody: []string{`"thinking"`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, string(raw))
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				event := func(name, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data) }
				event("message_start", `{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"output_tokens":1}}}`)
				event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
				event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好的。"}}`)
				event("content_block_stop", `{"type":"content_block_stop","index":0}`)
				event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
				event("message_stop", `{"type":"message_stop"}`)
			}))
			defer server.Close()

			cfg := ConfigState{
				APIFormat:       apiFormatAnthropicMessages,
				BaseURL:         server.URL,
				APIKey:          "test-key",
				APIKeys:         []string{"test-key"},
				MaxTokens:       8192,
				ReasoningEffort: tc.level,
			}
			messages := []legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}
			if _, err := NewApp().streamModelResponse(context.Background(), cfg, tc.model, messages, nil, nil); err != nil {
				t.Fatalf("streamModelResponse() error = %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != 1 {
				t.Fatalf("expected 1 request, got %d", len(bodies))
			}
			for _, want := range tc.wantBody {
				if !strings.Contains(bodies[0], want) {
					t.Fatalf("request body must contain %s: %s", want, bodies[0])
				}
			}
			for _, absent := range tc.absentBody {
				if strings.Contains(bodies[0], absent) {
					t.Fatalf("request body must not contain %s: %s", absent, bodies[0])
				}
			}
		})
	}
}

// ── Responses: streaming final arguments & stream error events ────────────

// responsesSSEEvent renders one Responses SSE data frame.
func responsesSSEEvent(data string) string {
	return fmt.Sprintf("data: %s\n\n", data)
}

// responsesStreamTestCfg keeps adapter-internal retry backoff out of the
// assertion path so tests fail fast on deterministic streams.
func responsesStreamTestCfg(serverURL string) ConfigState {
	return ConfigState{
		APIFormat:      apiFormatOpenAIResponses,
		BaseURL:        serverURL,
		APIKeys:        []string{"test-key"},
		MaxTokens:      64,
		noAdapterRetry: true,
	}
}

// TestResponsesArgumentsDoneWithoutFieldKeepsAccumulated verifies that a
// function_call_arguments.done event omitting the arguments field (a
// non-compliant relay) cannot wipe the accumulated deltas: the tool must not
// silently run with "{}". The terminal output_item.done omits them too, so
// nothing repairs a wipe after the fact (kimi requires the field outright,
// openai-responses.ts:973-980).
func TestResponsesArgumentsDoneWithoutFieldKeepsAccumulated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_a","name":"calculate","arguments":""}}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"expression\":"}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"\"1+1\"}"}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_a","name":"calculate"}}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":3,"output_tokens":4}}}`))
	}))
	defer server.Close()

	a := NewApp()
	result, err := a.streamModelResponse(context.Background(), responsesStreamTestCfg(server.URL), "test-model",
		[]legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "1+1"}}, nil, nil)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(result.ToolCalls))
	}
	if got := result.ToolCalls[0].Function.Arguments; got != `{"expression":"1+1"}` {
		t.Fatalf("arguments = %q, want the accumulated deltas %q", got, `{"expression":"1+1"}`)
	}
}

// TestResponsesArgumentsDoneFullValueStillWins verifies the done event stays
// authoritative when it does carry the full arguments (pi overwrites,
// openai-responses-shared.ts:656-666).
func TestResponsesArgumentsDoneFullValueStillWins(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_a","name":"calculate","arguments":""}}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"expr"}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{\"expression\":\"2+2\"}"}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":3,"output_tokens":4}}}`))
	}))
	defer server.Close()

	a := NewApp()
	result, err := a.streamModelResponse(context.Background(), responsesStreamTestCfg(server.URL), "test-model",
		[]legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "2+2"}}, nil, nil)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(result.ToolCalls))
	}
	if got := result.ToolCalls[0].Function.Arguments; got != `{"expression":"2+2"}` {
		t.Fatalf("arguments = %q, want the full done value %q", got, `{"expression":"2+2"}`)
	}
}

// TestResponsesUntypedErrorEventSurfacesAsError verifies that a gateway error
// forwarded without a "type" field is surfaced — with its nested code
// unpacked so the classifier still sees the 429 marker — instead of being
// silently dropped and reported as "stream ended without terminal event"
// (kimi: openai-responses.ts:884-892 + 270-321).
func TestResponsesUntypedErrorEventSurfacesAsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, responsesSSEEvent(`{"message":"received error while streaming: {\"code\":429,\"message\":\"Too many requests\"}"}`))
	}))
	defer server.Close()

	a := NewApp()
	_, err := a.streamModelResponse(context.Background(), responsesStreamTestCfg(server.URL), "test-model",
		[]legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err == nil {
		t.Fatal("expected an error from the untyped gateway error event")
	}
	msg := err.Error()
	if !strings.Contains(msg, "429") || !strings.Contains(msg, "Too many requests") {
		t.Fatalf("error = %q, want the nested gateway code and message", msg)
	}
	if strings.Contains(msg, "stream ended without terminal event") {
		t.Fatalf("error = %q, must not degrade into the generic terminal-event failure", msg)
	}
	if classifyLLMError(err) != llmErrorKindRateLimited {
		t.Fatalf("classifyLLMError(%q) = %v, want rate limited", msg, classifyLLMError(err))
	}
}

// TestResponsesFailedWithoutErrorObjectUsesIncompleteReason verifies the
// response.failed fallback chain: with no error object the text falls back to
// incomplete_details.reason (kimi openai-responses.ts:323-350; pi
// openai-responses.ts:742-752).
func TestResponsesFailedWithoutErrorObjectUsesIncompleteReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.failed","response":{"id":"r1","incomplete_details":{"reason":"content_filter"}}}`))
	}))
	defer server.Close()

	a := NewApp()
	_, err := a.streamModelResponse(context.Background(), responsesStreamTestCfg(server.URL), "test-model",
		[]legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err == nil {
		t.Fatal("expected an error from the response.failed event")
	}
	if msg := err.Error(); !strings.Contains(msg, "content_filter") {
		t.Fatalf("error = %q, want the incomplete_details reason", msg)
	}
}

// TestResponsesUnknownTypedEventStaysIgnored guards the untyped-error default
// branch: a well-formed event type the adapter does not consume must still be
// ignored silently (kimi: "unknown future event types carry no data we
// currently consume").
func TestResponsesUnknownTypedEventStaysIgnored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"delta":"hi"}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.output_text.done","item_id":"msg_1","output_index":0,"text":"hi"}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":1,"output_tokens":1}}}`))
	}))
	defer server.Close()

	a := NewApp()
	result, err := a.streamModelResponse(context.Background(), responsesStreamTestCfg(server.URL), "test-model",
		[]legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v", err)
	}
	if !strings.Contains(result.Content, "hi") {
		t.Fatalf("content = %q, want the streamed delta", result.Content)
	}
}

// TestBuildOpenAIResponsesRequestPairsEffortWithSummary verifies that an
// explicit reasoning effort is paired with summary:"auto" (kimi
// openai-responses.ts:1116-1120; pi openai-responses.ts:319-335): without the
// summary request the Responses API emits no reasoning_summary_text deltas.
func TestBuildOpenAIResponsesRequestPairsEffortWithSummary(t *testing.T) {
	cfg := ConfigState{
		APIFormat:       apiFormatOpenAIResponses,
		BaseURL:         defaultOpenAIResponsesURL,
		MaxTokens:       64,
		ReasoningEffort: "medium",
	}
	messages := []legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}
	body := buildOpenAIResponsesRequest(cfg, "gpt-5.5", messages, nil, nil)
	if body.Reasoning.Summary != oa.ReasoningSummaryAuto {
		t.Fatalf("reasoning.summary = %q, want auto", body.Reasoning.Summary)
	}
	if body.Reasoning.Effort != oa.ReasoningEffort("medium") {
		t.Fatalf("reasoning.effort = %q, want medium", body.Reasoning.Effort)
	}

	cfg.ReasoningEffort = ""
	body = buildOpenAIResponsesRequest(cfg, "gpt-5.5", messages, nil, nil)
	if body.Reasoning.Summary != "" || body.Reasoning.Effort != "" {
		t.Fatalf("reasoning = %+v, want unset without an explicit effort", body.Reasoning)
	}
}

// TestAnthropicUsageStateMergesByPresence verifies message_delta.usage fields
// are cumulative: present fields overwrite, absent fields keep the previous
// value, and nothing is ever added (kimi anthropic.ts:872-888; pi
// anthropic-messages.ts:716-743).
func TestAnthropicUsageStateMergesByPresence(t *testing.T) {
	s := &anthropicUsageState{}
	var start anthropic.Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":10,"output_tokens":2,"cache_creation_input_tokens":4}`), &start); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	s.mergeStart(start)
	mu := s.modelUsage()
	if mu == nil || mu.PromptTokens != 14 || mu.CompletionTokens != 2 || mu.CacheHitTokens != 0 || mu.CacheMissTokens != 14 {
		t.Fatalf("start usage = %+v, want prompt=14 completion=2 hit=0 miss=14", mu)
	}

	var delta anthropic.MessageDeltaUsage
	if err := json.Unmarshal([]byte(`{"input_tokens":5,"cache_read_input_tokens":8,"output_tokens":3}`), &delta); err != nil {
		t.Fatalf("unmarshal delta: %v", err)
	}
	s.mergeDelta(delta)
	mu = s.modelUsage()
	// cache_creation stays 4 (absent in the delta), so miss = 5+4 = 9 and
	// prompt = 5+4+8 = 17.
	if mu == nil || mu.PromptTokens != 17 || mu.CompletionTokens != 3 || mu.CacheHitTokens != 8 || mu.CacheMissTokens != 9 {
		t.Fatalf("after delta = %+v, want prompt=17 completion=3 hit=8 miss=9", mu)
	}

	// A delta that omits the counters keeps them (no zero-overwrite), and the
	// cumulative values are never added on top of each other.
	var tail anthropic.MessageDeltaUsage
	if err := json.Unmarshal([]byte(`{"output_tokens":4}`), &tail); err != nil {
		t.Fatalf("unmarshal tail: %v", err)
	}
	s.mergeDelta(tail)
	mu = s.modelUsage()
	if mu == nil || mu.PromptTokens != 17 || mu.CacheHitTokens != 8 || mu.CompletionTokens != 4 {
		t.Fatalf("after tail delta = %+v, want counters kept and output updated", mu)
	}
}

// TestConfigureAnthropicThinkingSetsDisplayAndKeepsAnswerRoom covers two field
// contracts. display is sent explicitly because "summarized" is what makes the
// model stream its thinking text (pi sets it on both branches,
// anthropic-messages.ts:1127/1135/1147), and a budget_tokens value must leave the
// visible answer its reserved room under the shared max_tokens ceiling (pi:
// clampThinkingBudgetToAnswerRoom / MIN_ANSWER_TOKENS).
func TestConfigureAnthropicThinkingSetsDisplayAndKeepsAnswerRoom(t *testing.T) {
	adaptive := anthropic.MessageNewParams{}
	if !configureAnthropicThinking(&adaptive, "claude-opus-4-7", "high", 64000, anthropicOffPlan{}) {
		t.Fatal("an adaptive-thinking model must enable thinking")
	}
	if adaptive.Thinking.OfAdaptive == nil ||
		adaptive.Thinking.OfAdaptive.Display != anthropic.ThinkingConfigAdaptiveDisplaySummarized {
		t.Fatalf("adaptive thinking = %+v, want display=summarized", adaptive.Thinking.OfAdaptive)
	}

	budgeted := anthropic.MessageNewParams{}
	if !configureAnthropicThinking(&budgeted, "claude-3-7-sonnet-20250219", "high", 4096, anthropicOffPlan{}) {
		t.Fatal("a budget-thinking model must enable thinking")
	}
	if budgeted.Thinking.OfEnabled == nil {
		t.Fatalf("thinking = %+v, want enabled", budgeted.Thinking.OfEnabled)
	}
	if got := budgeted.Thinking.OfEnabled.BudgetTokens; got != 4096-minAnthropicAnswerTokens {
		t.Fatalf("budget_tokens = %d, want %d", got, 4096-minAnthropicAnswerTokens)
	}
	if budgeted.Thinking.OfEnabled.Display != anthropic.ThinkingConfigEnabledDisplaySummarized {
		t.Fatalf("display = %q, want summarized", budgeted.Thinking.OfEnabled.Display)
	}

	tiny := anthropic.MessageNewParams{}
	if configureAnthropicThinking(&tiny, "claude-3-7-sonnet-20250219", "high", minAnthropicThinkingBudget, anthropicOffPlan{}) {
		t.Fatal("a cap too small for the budget floor plus the answer room must leave thinking unset")
	}
}

// TestReasoningEffortForModel pins the per-vendor translation: a level the vendor
// declares goes out verbatim, a level it does not declare goes through the translation
// its docs state, and a model of no known vendor keeps the request free of the field.
//
// Rows are keyed on the vendor, so the cases below span generations on purpose: every id
// of a known vendor takes that vendor's row, whether or not the generation's own docs
// mention the field yet.
//
// Before this table existed the capability test was "the model name looks like
// o*/gpt-5*", so DeepSeek / 智谱 / Kimi received nothing at all: the level the user
// picked was never in the request body and the provider ran its own default
// (DeepSeek high, GLM max, Kimi max) while the UI kept showing the selection.
//
// The classification reads the model name and nothing else. Every other candidate
// is a config field that is populated for every request — the reasoning tag
// defaults to defaultReasoningTag for every model entry — so reading one as a
// capability declaration would make the gate unconditionally true.
func TestReasoningEffortForModel(t *testing.T) {
	cases := []struct{ name, model, level, want string }{
		{"deepseek passes a declared level through", "deepseek-flash", reasoningEffortLow, reasoningEffortLow},
		{"deepseek translates medium to high (documented)", "deepseek-flash", reasoningEffortMedium, reasoningEffortHigh},
		{"deepseek translates xhigh to high (documented)", "deepseek-v4-pro", reasoningEffortXHigh, reasoningEffortHigh},
		{"deepseek max", "deepseek-flash", reasoningEffortMax, reasoningEffortMax},
		{"a routing prefix does not change the vendor", "deepseek/deepseek-flash", reasoningEffortMax, reasoningEffortMax},
		{"智谱 keeps a declared level", "glm-5.3", reasoningEffortLow, reasoningEffortLow},
		{"智谱 translates medium to high", "glm-5.3", reasoningEffortMedium, reasoningEffortHigh},
		// The row holds the narrow set every generation takes (low/high/max), so a level
		// one generation would accept verbatim is translated instead.
		{"智谱 translates xhigh onto max", "glm-5.2", reasoningEffortXHigh, reasoningEffortMax},
		{"智谱 max", "glm-5.3-flashx", reasoningEffortMax, reasoningEffortMax},
		// The vendor is the key, not the version: a generation whose docs predate the
		// field takes the same row.
		{"an older 智谱 id takes the vendor row", "glm-4.6", reasoningEffortHigh, reasoningEffortHigh},
		{"moonshot keeps a declared level", "kimi-k3", reasoningEffortLow, reasoningEffortLow},
		{"moonshot translates medium to high", "kimi-k3", reasoningEffortMedium, reasoningEffortHigh},
		{"moonshot translates xhigh to max", "kimi-k3", reasoningEffortXHigh, reasoningEffortMax},
		{"an older moonshot id takes the vendor row too", "kimi-k2.6", reasoningEffortHigh, reasoningEffortHigh},
		// 千问's Chat field takes low/medium/xhigh only (no high, no max).
		{"千问 keeps a declared level", "qwen3.8-flash", reasoningEffortMedium, reasoningEffortMedium},
		{"千问 translates high onto xhigh", "qwen3.8-max", reasoningEffortHigh, reasoningEffortXHigh},
		{"千问 clamps max onto its top level", "qwen3.8-max", reasoningEffortMax, reasoningEffortXHigh},
		{"a non-3.8 qwen id takes the same row", "qwen3.7-max", reasoningEffortHigh, reasoningEffortXHigh},
		// MiniMax takes the full enum on every generation; the older ones ignore the field
		// rather than rejecting it.
		{"MiniMax accepts every level", "MiniMax-M3.1-Flash-Preview", reasoningEffortMax, reasoningEffortMax},
		{"MiniMax accepts every level on an older generation too", "MiniMax-M2.7", reasoningEffortXHigh, reasoningEffortXHigh},
		// "off" lands on the value the docs name, or on the vendor's lowest level where
		// those docs say the models cannot stop thinking at all.
		{"deepseek spells off as none (documented)", "deepseek-flash", reasoningEffortOff, reasoningEffortOffWireValue},
		{"千问 spells off as none on the effort wires", "qwen3.8-max", reasoningEffortOff, reasoningEffortOffWireValue},
		{"智谱 cannot stop thinking, so off becomes its lowest level", "glm-5.3", reasoningEffortOff, reasoningEffortLow},
		{"moonshot cannot stop thinking, so off becomes its lowest level", "kimi-k3", reasoningEffortOff, reasoningEffortLow},
		{"MiniMax cannot stop thinking, so off becomes its lowest level", "MiniMax-M3.1-Flash-Preview", reasoningEffortOff, reasoningEffortLow},
		{"openai reasoning models accept every level", "openai/o3-mini", reasoningEffortXHigh, reasoningEffortXHigh},
		{"a GPT generation after the pinned ones takes the same row", "gpt-6-astra", reasoningEffortHigh, reasoningEffortHigh},
		{"a region tag does not change the model", "gpt-5.1@eu", reasoningEffortMedium, reasoningEffortMedium},
		{"a region tag does not change the vendor", "glm-5.2@eu", reasoningEffortMedium, reasoningEffortHigh},
		{"a plain chat model of an unknown vendor declares nothing", "gpt-4o", reasoningEffortMax, ""},
		{"a gateway model of no known vendor declares nothing", "some-unknown-model", reasoningEffortMax, ""},
		{"auto leaves the decision to the provider", "deepseek-flash", reasoningEffortAuto, ""},
		{"a misspelled level normalizes to auto and sends nothing", "deepseek-flash", "highh", ""},
	}
	for _, c := range cases {
		if got := reasoningEffortForModel(c.model, c.level); got != c.want {
			t.Errorf("%s: reasoningEffortForModel(%q, %q) = %q, want %q", c.name, c.model, c.level, got, c.want)
		}
	}
}

// TestReasoningEffortFamiliesStayUsable pins the contract every row of the vendor
// table has to satisfy, so a row added later cannot ship a mapping nobody can use:
// the accepted levels are non-empty and ascending, every documented translation
// points at a declared level, and every level the UI can produce resolves to a
// level the row declares — a level inside the range without a translation is a gap
// that would otherwise silently drop the field at runtime.
func TestReasoningEffortFamiliesStayUsable(t *testing.T) {
	declared := func(family reasoningEffortFamily, level string) bool {
		for _, accepted := range family.accepts {
			if accepted == level {
				return true
			}
		}
		return false
	}
	uiLevels := []string{
		reasoningEffortLow, reasoningEffortMedium, reasoningEffortHigh,
		reasoningEffortXHigh, reasoningEffortMax,
	}
	for _, family := range reasoningEffortFamilies {
		if family.matches == nil {
			t.Errorf("family %q has no match rule", family.name)
			continue
		}
		if len(family.accepts) == 0 {
			t.Errorf("family %q declares no accepted level", family.name)
			continue
		}
		for i, level := range family.accepts {
			if reasoningEffortRank(level) == 0 {
				t.Errorf("family %q declares unknown level %q", family.name, level)
			}
			if i > 0 && reasoningEffortRank(family.accepts[i-1]) >= reasoningEffortRank(level) {
				t.Errorf("family %q declares levels out of order: %v", family.name, family.accepts)
			}
		}
		for level, target := range family.alias {
			if reasoningEffortRank(level) == 0 {
				t.Errorf("family %q translates an unknown level %q", family.name, level)
			}
			if !declared(family, target) {
				t.Errorf("family %q translates %q to %q, which it does not accept", family.name, level, target)
			}
		}
		for _, level := range uiLevels {
			if got := family.wireValue(level); !declared(family, got) {
				t.Errorf("family %q maps %q to %q, which it does not accept", family.name, level, got)
			}
		}
	}
}

// TestReasoningEffortFamilyWireValueHandlesUnlistedLevels covers the two rules a
// row needs when the selected level is not one it declares. No shipped row needs
// the clamp today (each one accepts both ends of the UI's range), so the rows are
// built here on purpose.
func TestReasoningEffortFamilyWireValueHandlesUnlistedLevels(t *testing.T) {
	t.Run("a level above the range clamps onto its highest value", func(t *testing.T) {
		row := reasoningEffortFamily{
			name:    "synthetic",
			matches: func(string) bool { return true },
			accepts: []string{reasoningEffortLow, reasoningEffortHigh},
			alias:   map[string]string{reasoningEffortMedium: reasoningEffortHigh},
		}
		cases := map[string]string{
			reasoningEffortLow:    reasoningEffortLow,
			reasoningEffortMedium: reasoningEffortHigh, // documented translation
			reasoningEffortHigh:   reasoningEffortHigh,
			reasoningEffortXHigh:  reasoningEffortHigh, // above the range
			reasoningEffortMax:    reasoningEffortHigh, // above the range
		}
		for level, want := range cases {
			if got := row.wireValue(level); got != want {
				t.Errorf("wireValue(%q) = %q, want %q", level, got, want)
			}
		}
	})

	t.Run("an undocumented level inside the range sends nothing", func(t *testing.T) {
		row := reasoningEffortFamily{
			name:    "synthetic-gap",
			matches: func(string) bool { return true },
			accepts: []string{reasoningEffortLow, reasoningEffortHigh, reasoningEffortMax},
		}
		if got := row.wireValue(reasoningEffortMedium); got != "" {
			t.Fatalf("wireValue(medium) = %q, want empty for a level the row neither accepts nor translates", got)
		}
	})
}

// TestChatAdapterSendsEffortPerVendor drives the table end to end: the shipped
// default effort is "max", and each vendor now receives exactly what it declares —
// the parameter for DeepSeek / 智谱 GLM-5.3 / Kimi K3 / OpenAI reasoning models,
// and nothing at all for a vendor that does not declare the field (the official
// endpoints reject an effort value the target model does not accept).
func TestChatAdapterSendsEffortPerVendor(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cfg := ConfigState{
		APIFormat:       apiFormatOpenAIChat,
		BaseURL:         server.URL,
		APIKeys:         []string{"test-key"},
		ReasoningEffort: "max",
		// The shipped shape: normalizeReasoningTag gives every model entry a tag, so a
		// gate that read the tag as a capability declaration would let the parameter
		// through for every model.
		ReasoningTag: defaultReasoningTag,
	}
	cases := []struct{ model, want string }{
		{"gpt-4o", ""},
		// A model of no known vendor keeps the field out: a value the vendor never
		// declared can only come back as a 400, while leaving it out keeps the
		// provider's own default.
		{"some-unknown-model", ""},
		// The table is keyed on the vendor, not on a version, so every generation of a
		// known vendor takes its row — including the ones whose docs predate the field
		// (they ignore or reject it, which is the price of a table that survives the
		// vendor's next release).
		{"qwen-plus", `"reasoning_effort":"xhigh"`},
		{"kimi-k2.6", `"reasoning_effort":"max"`},
		{"gpt-5.1", `"reasoning_effort":"max"`},
		{"deepseek-flash", `"reasoning_effort":"max"`},
		{"glm-5.3", `"reasoning_effort":"max"`},
		{"kimi-k3", `"reasoning_effort":"max"`},
	}
	messages := []legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}
	for _, c := range cases {
		if _, err := NewApp().streamModelResponse(context.Background(), cfg, c.model, messages, nil, nil); err != nil {
			t.Fatalf("streamModelResponse(%s) error = %v", c.model, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != len(cases) {
		t.Fatalf("expected %d requests, got %d", len(cases), len(bodies))
	}
	for i, c := range cases {
		has := strings.Contains(bodies[i], "reasoning_effort")
		if c.want == "" && has {
			t.Errorf("%s declares no effort field but received one: %s", c.model, bodies[i])
		}
		if c.want != "" && !strings.Contains(bodies[i], c.want) {
			t.Errorf("%s must receive %s: %s", c.model, c.want, bodies[i])
		}
	}
}

// TestOpenAIResponsesEffortFollowsVendorTable: the Responses wire carries the
// vendor-resolved level (DeepSeek's documented medium→high here) paired with the
// summary request, while a model of no known vendor keeps reasoning unset.
func TestOpenAIResponsesEffortFollowsVendorTable(t *testing.T) {
	messages := []legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}
	cfg := ConfigState{
		APIFormat:       apiFormatOpenAIResponses,
		BaseURL:         "https://api.deepseek.com",
		MaxTokens:       64,
		ReasoningEffort: reasoningEffortMedium,
	}
	body := buildOpenAIResponsesRequest(cfg, "deepseek-flash", messages, nil, nil)
	if body.Reasoning.Effort != oa.ReasoningEffort(reasoningEffortHigh) {
		t.Fatalf("reasoning.effort = %q, want %q (DeepSeek's documented translation)", body.Reasoning.Effort, reasoningEffortHigh)
	}
	if body.Reasoning.Summary != oa.ReasoningSummaryAuto {
		t.Fatalf("reasoning.summary = %q, want auto", body.Reasoning.Summary)
	}

	cfg.ReasoningEffort = reasoningEffortMax
	body = buildOpenAIResponsesRequest(cfg, "some-unknown-model", messages, nil, nil)
	if body.Reasoning.Effort != "" || body.Reasoning.Summary != "" {
		t.Fatalf("reasoning = %+v, want unset for a model of no known vendor", body.Reasoning)
	}
}

// TestOpenAIChatPromptCacheKeyStaysOnOfficialEndpoint: the official endpoint is
// the only place prompt_cache_key / store are documented, so a compatible relay
// must never see them (pi attaches the key only for api.openai.com,
// openai-completions.ts:810-819).
// TestChatAdapterRequiresStreamTerminator drives the chat adapter's terminal
// check end to end. A stream that delivers content and then just ends — no
// finish_reason, no `[DONE]` — must fail instead of being persisted as a
// complete turn, while the same stream WITH a terminator is accepted (relays
// that omit finish_reason but send `[DONE]` keep working). go-openai collapses
// both cases into a bare io.EOF (stream_reader.go:100-102 for `[DONE]`, :64-76
// for the read error), so the decision rests on the terminator recorded by
// sseDoneReadCloser.
func TestChatAdapterRequiresStreamTerminator(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantError bool
	}{
		{
			name:      "cut off without any terminator",
			body:      "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"half\"}}]}\n\n",
			wantError: true,
		},
		{
			name: "done sentinel without finish_reason",
			body: "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"half\"}}]}\n\n" +
				"data: [DONE]\n\n",
			wantError: false,
		},
		{
			// Gateways are not required to put a space after the colon:
			// go-openai normalizes the field with `^data:\s*` before matching
			// "[DONE]", so the adapter must accept the same spellings.
			name: "done sentinel without the canonical space",
			body: "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"half\"}}]}\n\n" +
				"data:[DONE]\n\n",
			wantError: false,
		},
		{
			name: "finish_reason without done sentinel",
			body: "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"half\"}}]}\n\n" +
				"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
			wantError: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, tc.body)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}))
			defer server.Close()

			cfg := ConfigState{
				APIFormat: apiFormatOpenAIChat,
				BaseURL:   server.URL,
				APIKeys:   []string{"test-key"},
				// The check under test is the judgement itself; retry policy is
				// the caller's job, so in-adapter retries stay off.
				noAdapterRetry: true,
			}
			messages := []legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}
			result, err := NewApp().streamModelResponse(context.Background(), cfg, "gpt-4o", messages, nil, nil)
			if tc.wantError {
				if err == nil {
					t.Fatalf("a stream without any terminator must fail, got %#v", result)
				}
				if !strings.Contains(err.Error(), "finish_reason") {
					t.Fatalf("the error must name the missing terminator, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("streamModelResponse() error = %v", err)
			}
			if result == nil || result.Content != "half" {
				t.Fatalf("content must survive a terminated stream, got %#v", result)
			}
		})
	}
}

// TestPromptCacheKeyFollowsTheVendorTable pins which requests carry the session-sticky
// prompt_cache_key: the official API and the vendors whose own docs declare the field —
// Kimi on Chat + Responses, 智谱 and MiniMax on Responses — and nobody else. A vendor
// that documents it on one protocol only carries exactly that protocol, and an endpoint
// whose docs never mention the field receives nothing.
func TestPromptCacheKeyFollowsTheVendorTable(t *testing.T) {
	key := "ally:abc"
	official := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: openAIOfficialAPIBaseURL, responsesPromptCacheKey: key}
	if got := promptCacheKeyForRequest(official, apiFormatOpenAIChat, "gpt-5.6"); got != key {
		t.Fatalf("official key = %q, want the session key", got)
	}
	// The rule belongs to the field, not to the adapter: the Responses path asks the
	// very same predicate.
	responsesOfficial := official
	responsesOfficial.APIFormat = apiFormatOpenAIResponses
	if got := promptCacheKeyForRequest(responsesOfficial, apiFormatOpenAIResponses, "gpt-5.6"); got != key {
		t.Fatalf("responses official key = %q, want %q", got, key)
	}

	relay := ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: "https://relay.example.com/v1", responsesPromptCacheKey: key}
	tests := []struct {
		name      string
		apiFormat string
		model     string
		want      string
	}{
		{name: "Kimi Responses", apiFormat: apiFormatOpenAIResponses, model: "kimi-k3", want: key},
		{name: "Kimi Chat", apiFormat: apiFormatOpenAIChat, model: "kimi-k2.6", want: key},
		{name: "智谱 Responses", apiFormat: apiFormatOpenAIResponses, model: "glm-5.3", want: key},
		{name: "MiniMax Responses", apiFormat: apiFormatOpenAIResponses, model: "MiniMax-M2.7", want: key},
		{name: "智谱 Chat has no such field", apiFormat: apiFormatOpenAIChat, model: "glm-5.3"},
		{name: "MiniMax Chat has no such field", apiFormat: apiFormatOpenAIChat, model: "MiniMax-M2.7"},
		{name: "DeepSeek answers 不支持", apiFormat: apiFormatOpenAIResponses, model: "deepseek-v4-pro"},
		{name: "an unknown relay model keeps it off", apiFormat: apiFormatOpenAIResponses, model: "some-relay-model"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := relay
			cfg.APIFormat = tc.apiFormat
			if got := promptCacheKeyForRequest(cfg, tc.apiFormat, tc.model); got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}

	// store:false travels with the official key alone: it is an OpenAI request field
	// with no counterpart on a compatible platform's Chat wire (patchChatRequestFields
	// takes the flag from promptCachePinStore).
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"q"}]}`)
	patched, changed := patchChatRequestFields(body, "", nil, key, true, reasoningStopThinkingNone)
	if !changed {
		t.Fatal("expected the official request to gain prompt_cache_key and store")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(patched, &payload); err != nil {
		t.Fatalf("unmarshal patched body: %v", err)
	}
	if string(payload["prompt_cache_key"]) != `"ally:abc"` || string(payload["store"]) != "false" {
		t.Fatalf("patched body = %s, want prompt_cache_key + store:false", patched)
	}
	if untouched, _ := patchChatRequestFields(body, "", nil, "", false, reasoningStopThinkingNone); string(untouched) != string(body) {
		t.Fatalf("a relay body must stay byte-identical: %s", untouched)
	}
}

// TestResponsesSummaryPartsSeparateAndTruncationIsReported covers two fields the
// adapter used to drop: summary parts are separate paragraphs (pi closes each
// with a blank line, openai-responses-shared.ts:613-622), and an
// incomplete+max_output_tokens terminal state is surfaced as the same Max Tokens
// outcome the Chat and Anthropic adapters report instead of a silent truncation.
func TestResponsesSummaryPartsSeparateAndTruncationIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"first"}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.reasoning_summary_part.done","item_id":"rs_1","output_index":0,"summary_index":0}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"second"}`))
		fmt.Fprint(w, responsesSSEEvent(`{"type":"response.incomplete","response":{"id":"r1","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":3,"output_tokens":9}}}`))
	}))
	defer server.Close()

	cfg := responsesStreamTestCfg(server.URL)
	result, err := NewApp().streamModelResponse(context.Background(), cfg, "gpt-5.5",
		[]legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("truncation is a terminal state, not a stream error: %v", err)
	}
	if result.Reasoning != "first\n\nsecond" {
		t.Fatalf("reasoning = %q, want the parts separated by a blank line", result.Reasoning)
	}
	if result.StopReason != "length" {
		t.Fatalf("stop reason = %q, want length", result.StopReason)
	}
	stopErr := modelResponseStopError(cfg, result)
	if stopErr == nil || !strings.Contains(stopErr.Error(), "Max Tokens") {
		t.Fatalf("stop error = %v, want the actionable Max Tokens message", stopErr)
	}
}

// TestNormalizeToolsForOpenAIChatAlwaysCarriesParameters: every function tool
// must ship a parameters object (the field is required), so the Chat path builds
// the same default the Anthropic / Responses converters do.
func TestNormalizeToolsForOpenAIChatAlwaysCarriesParameters(t *testing.T) {
	tools := []legacyopenai.Tool{{Type: legacyopenai.ToolTypeFunction, Function: &legacyopenai.FunctionDefinition{Name: "no_args"}}}
	out := normalizeToolsForOpenAIChat(tools)
	if len(out) != 1 || out[0].Function == nil {
		t.Fatalf("normalizeToolsForOpenAIChat() = %+v", out)
	}
	schema, ok := out[0].Function.Parameters.(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Fatalf("parameters = %#v, want an object schema", out[0].Function.Parameters)
	}
	if _, isObject := schema["properties"].(map[string]any); !isObject {
		t.Fatalf("parameters = %#v, want a properties object", schema)
	}
}

// TestSSEDoneScannerAcceptsEveryGatewaySpelling: go-openai matches the terminator
// frame as `strings.TrimSpace(line)` → prefix `data:` → TrimSpace of the rest
// equals "[DONE]" (stream_reader.go:13-16 and :96-99), so the adapter's own
// scanner must accept the same spellings. A literal-only match reports a finished
// stream as truncated (errChatStreamNoFinishReason) whenever a gateway omits the
// canonical space.
//
// Matching is anchored to a frame line: a payload that merely *contains* the
// marker (assistant text, a tool argument, an SSE sample file) must not count,
// otherwise a stream cut later is accepted as a finished turn.
func TestSSEDoneScannerAcceptsEveryGatewaySpelling(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"canonical", "data: [DONE]", true},
		{"no space after the colon", "data:[DONE]", true},
		{"extra spaces", "data:   [DONE]", true},
		{"tab separator", "data:\t[DONE]", true},
		{"trailing carriage return", "data: [DONE]\r", true},
		{"payload is not the terminator", `data: {"choices":[]}`, false},
		{"unfinished frame", "data: [DO", false},
		{"marker inside a payload", `data: {"content":"data: [DONE]"}`, false},
		{"continuation of a longer line", `{"note":"data: [DONE]"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lineIsSSEDone([]byte(tc.line)); got != tc.want {
				t.Fatalf("lineIsSSEDone(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// TestSSEDoneReadCloserIgnoresMarkerInsidePayload: a payload that literally
// contains `data: [DONE]` — assistant text reviewing this scanner, a tool
// argument writing an SSE fixture — must not mark the stream as terminated. The
// old scanner searched the raw byte window, so this body marked the stream done
// and a later cut-off was persisted as a complete turn.
func TestSSEDoneReadCloserIgnoresMarkerInsidePayload(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"data: [DONE]\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"half\"}}]}\n\n"
	watcher := &sseDoneWatcher{}
	reader := &sseDoneReadCloser{rc: io.NopCloser(strings.NewReader(body)), watcher: watcher}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read: %v", err)
	}
	if watcher.Done() {
		t.Fatal("a marker inside a payload must not terminate the stream")
	}
}

// TestSSEDoneReadCloserSurvivesSplitReadsAndResets: the terminator can be split
// across two reads (this test feeds one byte at a time), and a watcher shared by
// the retries of one stream must report nothing after reset.
func TestSSEDoneReadCloserSurvivesSplitReadsAndResets(t *testing.T) {
	watcher := &sseDoneWatcher{}
	body := "data:{\"choices\":[]}\n\ndata:\t[DONE]\n\n"
	reader := &sseDoneReadCloser{rc: io.NopCloser(strings.NewReader(body)), watcher: watcher}
	buf := make([]byte, 1)
	for {
		n, err := reader.Read(buf)
		if n == 0 && err != nil {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	if !watcher.Done() {
		t.Fatal("a terminator split across reads must still be recorded")
	}
	watcher.reset()
	if watcher.Done() {
		t.Fatal("reset must clear the recorded terminator")
	}
}

// TestChatAdapterTerminatorDoesNotLeakAcrossRetries: the first attempt fails on a
// malformed frame after its read already carried the terminator, and the retry is
// cut off mid-stream. Without a reset the retry inherits the stale `[DONE]`, its
// half answer passes the {finish_reason, [DONE]} check and is persisted as a
// complete turn.
func TestChatAdapterTerminatorDoesNotLeakAcrossRetries(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		mu.Lock()
		requests++
		attempt := requests
		mu.Unlock()
		if attempt == 1 {
			// One write, so the terminator and the malformed frame travel in the
			// same read: the SDK rejects the frame, the scanner still sees both.
			_, _ = w.Write([]byte("data: {oops}\n\ndata: [DONE]\n\n"))
			return
		}
		// Cut off mid-stream: no finish_reason, no [DONE].
		_, _ = w.Write([]byte("data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"half\"}}]}\n\n"))
	}))
	defer server.Close()

	cfg := ConfigState{
		APIFormat:  apiFormatOpenAIChat,
		BaseURL:    server.URL,
		APIKeys:    []string{"test-key"},
		LLMRetries: 1,
	}
	messages := []legacyopenai.ChatCompletionMessage{{Role: legacyopenai.ChatMessageRoleUser, Content: "hi"}}
	result, err := NewApp().streamModelResponse(context.Background(), cfg, "gpt-4o", messages, nil, nil)
	if err == nil {
		t.Fatalf("a truncated retry must fail, got %#v", result)
	}
	if !strings.Contains(err.Error(), "finish_reason") {
		t.Fatalf("error = %v, want the missing-terminator error", err)
	}
	if requests < 2 {
		t.Fatalf("the first attempt must have been retried, got %d request(s)", requests)
	}
}

func TestNormalizeReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"":            reasoningEffortAuto,
		"auto":        reasoningEffortAuto,
		"Auto":        reasoningEffortAuto,
		"default":     reasoningEffortAuto,
		"unset":       reasoningEffortAuto,
		"off":         reasoningEffortOff,
		"OFF":         reasoningEffortOff,
		"none":        reasoningEffortOff,
		"disabled":    reasoningEffortOff,
		"nothinking":  reasoningEffortOff,
		"no-think":    reasoningEffortOff,
		"NO_THINK":    reasoningEffortOff,
		"low":         reasoningEffortLow,
		"LOW":         reasoningEffortLow,
		"medium":      reasoningEffortMedium,
		"med":         reasoningEffortMedium,
		"high":        reasoningEffortHigh,
		"xhigh":       reasoningEffortXHigh,
		"X-HIGH":      reasoningEffortXHigh,
		"extra_high":  reasoningEffortXHigh,
		"extremehigh": reasoningEffortXHigh,
		"max":         reasoningEffortMax,
		"maximum":     reasoningEffortMax,
		"bogus":       reasoningEffortAuto,
		"high effort": reasoningEffortAuto,
	}
	for in, want := range cases {
		if got := normalizeReasoningEffort(in); got != want {
			t.Errorf("normalizeReasoningEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeAPIFormat(t *testing.T) {
	cases := map[string]string{
		"":                   apiFormatOpenAIChat,
		"openai":             apiFormatOpenAIChat,
		"openai_compatible":  apiFormatOpenAIChat,
		"openai_chat":        apiFormatOpenAIChat,
		"chat":               apiFormatOpenAIChat,
		"chat_completions":   apiFormatOpenAIChat,
		"chat_completion":    apiFormatOpenAIChat,
		"OpenAI-Chat":        apiFormatOpenAIChat,
		"bogus":              apiFormatOpenAIChat,
		"openai_responses":   apiFormatOpenAIResponses,
		"responses":          apiFormatOpenAIResponses,
		"response":           apiFormatOpenAIResponses,
		"anthropic":          apiFormatAnthropicMessages,
		"anthropic_messages": apiFormatAnthropicMessages,
		"claude":             apiFormatAnthropicMessages,
		"claude_messages":    apiFormatAnthropicMessages,
		"messages":           apiFormatAnthropicMessages,
	}
	for in, want := range cases {
		if got := normalizeAPIFormat(in); got != want {
			t.Errorf("normalizeAPIFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReasoningWireForAdapter(t *testing.T) {
	compatible := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: "https://api.deepseek.com"}
	official := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: openAIOfficialAPIBaseURL}
	cases := []struct {
		name   string
		cfg    ConfigState
		format string
		model  string
		effort string
		want   reasoningWirePlan
	}{
		{"empty sends nothing", compatible, apiFormatOpenAIChat, "deepseek-flash", "", reasoningWirePlan{}},
		{"auto leaves it to the provider", compatible, apiFormatOpenAIChat, "deepseek-flash", reasoningEffortAuto, reasoningWirePlan{}},
		{
			"off asks a compatible Chat endpoint to stop thinking",
			compatible, apiFormatOpenAIChat, "deepseek-flash", reasoningEffortOff, reasoningWirePlan{StopThinking: reasoningStopThinkingThinkingDisabled},
		},
		{
			"off aliases resolve to the same plan",
			compatible, apiFormatOpenAIChat, "deepseek-flash", "disabled", reasoningWirePlan{StopThinking: reasoningStopThinkingThinkingDisabled},
		},
		{
			"off is spelled as an effort on the Responses wire",
			compatible, apiFormatOpenAIResponses, "deepseek-flash", reasoningEffortOff, reasoningWirePlan{Effort: reasoningEffortOffWireValue},
		},
		{
			"off is spelled as an effort on the official Chat endpoint",
			official, apiFormatOpenAIChat, "gpt-5.5", reasoningEffortOff, reasoningWirePlan{Effort: reasoningEffortOffWireValue},
		},
		{
			// Its docs say the model cannot stop thinking at all, so off lands on the lowest
			// level the endpoint does accept instead of sending nothing — nothing would leave
			// the provider's own (most expensive) default running.
			"a model that cannot stop thinking falls back to its lowest level",
			compatible, apiFormatOpenAIResponses, "glm-5.3", reasoningEffortOff, reasoningWirePlan{Effort: reasoningEffortLow},
		},
		{
			"the same fallback applies on a compatible Chat endpoint",
			compatible, apiFormatOpenAIChat, "kimi-k3", reasoningEffortOff, reasoningWirePlan{Effort: reasoningEffortLow},
		},
		{
			"MiniMax M3.1 falls back the same way",
			compatible, apiFormatOpenAIChat, "MiniMax-M3.1-Flash-Preview", reasoningEffortOff, reasoningWirePlan{Effort: reasoningEffortLow},
		},
		{
			// The row is keyed on the vendor: a generation whose docs never declared an off
			// switch still lands on the lowest level, which is one its docs do accept.
			"MiniMax cannot stop thinking, so off lands on its lowest level",
			compatible, apiFormatOpenAIChat, "MiniMax-M3", reasoningEffortOff, reasoningWirePlan{Effort: reasoningEffortLow},
		},
		{
			"a declared level passes through",
			compatible, apiFormatOpenAIChat, "deepseek-flash", reasoningEffortLow, reasoningWirePlan{Effort: reasoningEffortLow},
		},
		{
			"a level the vendor does not declare is translated",
			compatible, apiFormatOpenAIChat, "kimi-k3", reasoningEffortXHigh, reasoningWirePlan{Effort: reasoningEffortMax},
		},
		{
			"a model of no known vendor sends nothing",
			compatible, apiFormatOpenAIChat, "some-unknown-model", reasoningEffortHigh, reasoningWirePlan{},
		},
		{
			// 千问's Chat field takes low/medium/xhigh only: high is translated and a
			// level above the range clamps onto xhigh.
			"千问 translates high onto xhigh",
			compatible, apiFormatOpenAIChat, "qwen3.8-max", reasoningEffortHigh, reasoningWirePlan{Effort: reasoningEffortXHigh},
		},
		{
			"千问 clamps a level above its range onto xhigh",
			compatible, apiFormatOpenAIChat, "qwen3.8-max", reasoningEffortMax, reasoningWirePlan{Effort: reasoningEffortXHigh},
		},
		{
			"off on 千问 is enable_thinking:false, not the generic thinking field",
			compatible, apiFormatOpenAIChat, "qwen3.8-max", reasoningEffortOff, reasoningWirePlan{StopThinking: reasoningStopThinkingQwenEnableFalse},
		},
		{
			"MiniMax M3.1 declares every level",
			compatible, apiFormatOpenAIChat, "MiniMax-M3.1-Flash-Preview", reasoningEffortXHigh, reasoningWirePlan{Effort: reasoningEffortXHigh},
		},
		{
			// 非标准值：这一行按厂商分派，所以连文档里没有该字段的那一代也走同一条规则。
			"an older MiniMax generation takes the same fallback",
			compatible, apiFormatOpenAIChat, "MiniMax-M2.7", reasoningEffortOff, reasoningWirePlan{Effort: reasoningEffortLow},
		},
		{
			"a level above the row's narrow set clamps onto its top level",
			compatible, apiFormatOpenAIResponses, "glm-5.2", reasoningEffortXHigh, reasoningWirePlan{Effort: reasoningEffortMax},
		},
		{"max passes through", official, apiFormatOpenAIChat, "gpt-5.5", reasoningEffortMax, reasoningWirePlan{Effort: reasoningEffortMax}},
	}
	for _, c := range cases {
		if got := reasoningWireForAdapter(c.cfg, c.format, c.model, c.effort); got != c.want {
			t.Errorf("%s: reasoningWireForAdapter(%q, %q, %q) = %+v, want %+v", c.name, c.format, c.model, c.effort, got, c.want)
		}
	}
}
