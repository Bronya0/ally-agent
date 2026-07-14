package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestNormalizeAPIFormatAliases(t *testing.T) {
	cases := map[string]string{
		"":                   apiFormatOpenAIChat,
		"chat-completions":   apiFormatOpenAIChat,
		"responses":          apiFormatOpenAIResponses,
		"OpenAI Responses":   apiFormatOpenAIResponses,
		"anthropic":          apiFormatAnthropicMessages,
		"claude messages":    apiFormatAnthropicMessages,
		"unknown-new-format": apiFormatOpenAIChat,
	}
	for input, want := range cases {
		if got := normalizeAPIFormat(input); got != want {
			t.Fatalf("normalizeAPIFormat(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAnthropicBaseURLRemovesVersionSuffix(t *testing.T) {
	got := baseURLForAPIFormat(ConfigState{APIFormat: apiFormatAnthropicMessages, BaseURL: "https://api.anthropic.com/v1/"})
	if got != "https://api.anthropic.com" {
		t.Fatalf("unexpected Anthropic base URL: %q", got)
	}
}

func TestAnthropicDefaultMaxTokensIsConservative(t *testing.T) {
	if got := defaultMaxTokensForAPIFormat(apiFormatAnthropicMessages); got != 8192 {
		t.Fatalf("unexpected Anthropic default max tokens: %d", got)
	}
}

func TestOpenAIResponsesInputPreservesFunctionCallRoundTrip(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system prompt"},
		{Role: openai.ChatMessageRoleUser, Content: "read app.go"},
		{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "I will read it.",
			ToolCalls: []openai.ToolCall{{
				ID:   "call_read",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"app.go"}`,
				},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_read", Content: `{"ok":true}`},
	}

	instructions, input := buildOpenAIResponsesInput(messages)
	if instructions != "system prompt" {
		t.Fatalf("unexpected instructions: %q", instructions)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		`"role":"assistant"`,
		`"type":"function_call"`,
		`"id":"fc_call_read"`,
		`"call_id":"call_read"`,
		`"name":"read_file"`,
		`"arguments":"{\"path\":\"app.go\"}"`,
		`"type":"function_call_output"`,
		`"output":"{\"ok\":true}"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected Responses input JSON to contain %s\n%s", want, text)
		}
	}
}

func TestChatMaxCompletionTokensFallbackDetection(t *testing.T) {
	cases := []string{
		"unknown parameter: max_completion_tokens",
		"max_completion_tokens is not supported by this model",
		"invalid extra field max_completion_tokens",
	}
	for _, msg := range cases {
		if !shouldRetryChatWithMaxTokens(errors.New(msg)) {
			t.Fatalf("expected fallback for %q", msg)
		}
	}
	if shouldRetryChatWithMaxTokens(errors.New("rate limit exceeded")) {
		t.Fatal("did not expect fallback for unrelated error")
	}
}

func TestOpenAIChatStreamSkipsEmptyDataEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: \n\n"))
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":""}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.streamOpenAIChat(context.Background(), ConfigState{
		APIKey:      "test-key",
		BaseURL:     server.URL + "/v1",
		Model:       "test-model",
		MaxTokens:   16,
		Temperature: 0,
	}, "test-model", []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello" {
		t.Fatalf("expected content %q, got %q", "hello", got.Content)
	}
}

func TestOpenAIChatStreamKeepsContentWhenTailJSONIsTruncated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":""}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"id":"2"` + "\n\n"))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.streamOpenAIChat(context.Background(), ConfigState{
		APIKey:      "test-key",
		BaseURL:     server.URL + "/v1",
		Model:       "test-model",
		MaxTokens:   16,
		Temperature: 0,
	}, "test-model", []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello" {
		t.Fatalf("expected content %q, got %q", "hello", got.Content)
	}
}

func TestOpenAIResponsesStreamSkipsEmptyDataEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: \n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"hello","output_index":0,"content_index":0}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.streamOpenAIResponses(context.Background(), ConfigState{
		APIKey:      "test-key",
		BaseURL:     server.URL,
		Model:       "test-model",
		MaxTokens:   16,
		Temperature: 0,
	}, "test-model", []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello" {
		t.Fatalf("expected content %q, got %q", "hello", got.Content)
	}
}

func TestOpenAIResponsesStreamKeepsContentWhenTailJSONIsTruncated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"hello","output_index":0,"content_index":0}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed"` + "\n\n"))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.streamOpenAIResponses(context.Background(), ConfigState{
		APIKey:      "test-key",
		BaseURL:     server.URL,
		Model:       "test-model",
		MaxTokens:   16,
		Temperature: 0,
	}, "test-model", []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello" {
		t.Fatalf("expected content %q, got %q", "hello", got.Content)
	}
}

func TestAnthropicMessagesPreserveToolUseAndResult(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system prompt"},
		{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "I will read it.",
			ToolCalls: []openai.ToolCall{{
				ID:   "toolu_read",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"app.go"}`,
				},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "toolu_read", Content: `{"ok":true}`},
	}

	system, converted := buildAnthropicMessages(messages)
	if system != "system prompt" {
		t.Fatalf("unexpected system prompt: %q", system)
	}
	raw, err := json.Marshal(converted)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		`"role":"assistant"`,
		`"type":"tool_use"`,
		`"id":"toolu_read"`,
		`"name":"read_file"`,
		`"input":{"path":"app.go"}`,
		`"role":"user"`,
		`"type":"tool_result"`,
		`"tool_use_id":"toolu_read"`,
		`"content":[{"text":"{\"ok\":true}","type":"text"}]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected Anthropic message JSON to contain %s\n%s", want, text)
		}
	}
}

func TestAnthropicMessagesMarkFailedToolResultAsError(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{
			Role: openai.ChatMessageRoleAssistant,
			ToolCalls: []openai.ToolCall{{
				ID:   "toolu_failed",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"missing.go"}`,
				},
			}},
		},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "toolu_failed", Content: `{"ok":false,"error":"not found"}`},
	}

	_, converted := buildAnthropicMessages(messages)
	raw, err := json.Marshal(converted)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"is_error":true`) {
		t.Fatalf("expected failed tool result to set is_error=true: %s", raw)
	}
}

func TestAnthropicStopReasonErrors(t *testing.T) {
	for _, reason := range []string{"max_tokens", "refusal", "pause_turn", "model_context_window_exceeded", "future_reason"} {
		if err := anthropicStopReasonError(reason); err == nil {
			t.Fatalf("expected stop reason %q to fail", reason)
		}
	}
	for _, reason := range []string{"", "end_turn", "tool_use", "stop_sequence"} {
		if err := anthropicStopReasonError(reason); err != nil {
			t.Fatalf("expected stop reason %q to be accepted, got %v", reason, err)
		}
	}
}

func TestStreamAnthropicMessagesCapturesStopReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing Anthropic API key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_start\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"partial"}}`+"\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_stop\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
		_, _ = fmt.Fprint(w, "event: message_delta\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"output_tokens":4}}`+"\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer server.Close()

	app := NewApp()
	result, err := app.streamAnthropicMessages(context.Background(), ConfigState{
		APIFormat:   apiFormatAnthropicMessages,
		APIKey:      "test-key",
		BaseURL:     server.URL,
		Model:       "claude-sonnet-5",
		MaxTokens:   16,
		Temperature: 0.2,
	}, "claude-sonnet-5", []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hello"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "partial" || result.StopReason != "max_tokens" {
		t.Fatalf("unexpected Anthropic stream result: %#v", result)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 3 || result.Usage.CompletionTokens != 4 {
		t.Fatalf("unexpected Anthropic usage: %#v", result.Usage)
	}
}

func TestAnthropicInputSchemaPreservesJSONSchemaFields(t *testing.T) {
	schema := anthropicInputSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required":             []string{"path"},
		"additionalProperties": false,
		"anyOf": []any{
			map[string]any{"required": []string{"path"}},
		},
	})
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		`"type":"object"`,
		`"properties":{"path":{"type":"string"}}`,
		`"required":["path"]`,
		`"additionalProperties":false`,
		`"anyOf":[{"required":["path"]}]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected Anthropic schema JSON to contain %s\n%s", want, text)
		}
	}
}

func TestSplitImageDataURLNormalizesJPG(t *testing.T) {
	mediaType, data, ok := splitImageDataURL("data:image/jpg;base64,abcd")
	if !ok {
		t.Fatal("expected valid image data URL")
	}
	if mediaType != "image/jpeg" || data != "abcd" {
		t.Fatalf("unexpected split result: %q %q", mediaType, data)
	}
}

func TestPartialTagMatch(t *testing.T) {
	cases := []struct {
		s, tag string
		want   int
	}{
		{"hello", "<sink>", 0},
		{"hello<sin", "<sink>", 4},
		{"hello<sink", "<sink>", 5},
		{"hello<sink>", "<sink>", 6},
		{"", "<sink>", 0},
		{"<s", "<sink>", 2},
		{"<sink>rest", "<sink>", 0},
	}
	for _, c := range cases {
		got := partialTagMatch(c.s, c.tag)
		if got != c.want {
			t.Errorf("partialTagMatch(%q, %q) = %d, want %d", c.s, c.tag, got, c.want)
		}
	}
}

func TestOpenAIChatStreamParsesReasoningTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"Hello <sink>Let me think"},"finish_reason":""}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"id":"2","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":" about this</sink> World"},"finish_reason":""}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	app := NewApp()
	var contentDeltas, reasoningDeltas []string
	onEvent := func(e modelStreamEvent) {
		if e.ContentDelta != "" {
			contentDeltas = append(contentDeltas, e.ContentDelta)
		}
		if e.ReasoningDelta != "" {
			reasoningDeltas = append(reasoningDeltas, e.ReasoningDelta)
		}
	}
	got, err := app.streamOpenAIChat(context.Background(), ConfigState{
		APIKey:       "test-key",
		BaseURL:      server.URL + "/v1",
		Model:        "test-model",
		MaxTokens:    16,
		Temperature:  0,
		ReasoningTag: "sink",
	}, "test-model", []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, onEvent)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Hello  World" {
		t.Fatalf("expected content %q, got %q", "Hello  World", got.Content)
	}
	if got.Reasoning != "Let me think about this" {
		t.Fatalf("expected reasoning %q, got %q", "Let me think about this", got.Reasoning)
	}
	expectedContentDeltas := []string{"Hello ", " World"}
	if len(contentDeltas) != len(expectedContentDeltas) {
		t.Fatalf("expected %d content deltas, got %d: %v", len(expectedContentDeltas), len(contentDeltas), contentDeltas)
	}
	for i, expected := range expectedContentDeltas {
		if contentDeltas[i] != expected {
			t.Fatalf("content delta %d: expected %q, got %q", i, expected, contentDeltas[i])
		}
	}
	expectedReasoningDeltas := []string{"Let me think", " about this"}
	if len(reasoningDeltas) != len(expectedReasoningDeltas) {
		t.Fatalf("expected %d reasoning deltas, got %d: %v", len(expectedReasoningDeltas), len(reasoningDeltas), reasoningDeltas)
	}
	for i, expected := range expectedReasoningDeltas {
		if reasoningDeltas[i] != expected {
			t.Fatalf("reasoning delta %d: expected %q, got %q", i, expected, reasoningDeltas[i])
		}
	}
}

func TestOpenAIChatStreamParsesSplitReasoningTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"Hello <si"},"finish_reason":""}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"id":"2","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"nk>thinking"},"finish_reason":""}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"id":"3","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"done</si"},"finish_reason":""}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"id":"4","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"nk> result"},"finish_reason":""}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.streamOpenAIChat(context.Background(), ConfigState{
		APIKey:       "test-key",
		BaseURL:      server.URL + "/v1",
		Model:        "test-model",
		MaxTokens:    16,
		Temperature:  0,
		ReasoningTag: "sink",
	}, "test-model", []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Hello  result" {
		t.Fatalf("expected content %q, got %q", "Hello  result", got.Content)
	}
	if got.Reasoning != "thinkingdone" {
		t.Fatalf("expected reasoning %q, got %q", "thinkingdone", got.Reasoning)
	}
}

func TestOpenAIChatStreamNoReasoningTagUsesReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"answer","reasoning_content":"because"},"finish_reason":""}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.streamOpenAIChat(context.Background(), ConfigState{
		APIKey:      "test-key",
		BaseURL:     server.URL + "/v1",
		Model:       "test-model",
		MaxTokens:   16,
		Temperature: 0,
	}, "test-model", []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "answer" {
		t.Fatalf("expected content %q, got %q", "answer", got.Content)
	}
	if got.Reasoning != "because" {
		t.Fatalf("expected reasoning %q, got %q", "because", got.Reasoning)
	}
}
