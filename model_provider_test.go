package main

import (
	"context"
	"encoding/json"
	"errors"
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
