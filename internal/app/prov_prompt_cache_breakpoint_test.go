// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// The subscription route rejected both explicit-cache fields with HTTP 400.
// Pin the serialized request, including system instructions and tool steps,
// so a model-generation heuristic cannot silently reintroduce those fields.
func TestResponsesImplicitCacheCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, model, base, auth string
	}{
		{"subscription-sol", "gpt-6.1-sol", openAIOfficialAPIBaseURL, authChatGPTOAuth},
		{"subscription-luna", "gpt-6-luna", openAIOfficialAPIBaseURL, authChatGPTOAuth},
		{"official-api", "gpt-6.1-sol", openAIOfficialAPIBaseURL, ""},
		{"older-gpt", "gpt-5.5", openAIOfficialAPIBaseURL, ""},
		{"unknown-model", "account-model", openAIOfficialAPIBaseURL, authChatGPTOAuth},
		{"relay", "gpt-6.1-sol", "https://relay.example.test/v1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: tc.base, AuthType: tc.auth, ReasoningEffort: "high", responsesPromptCacheKey: "ally:test-prefix"}
			messages := []openai.ChatCompletionMessage{
				{Role: "system", Content: "agent rules"},
				{Role: "system", Content: "frozen workspace"},
				{Role: "user", Content: "read a file"},
			}
			tools := []openai.Tool{{Type: "function", Function: &openai.FunctionDefinition{Name: "read", Parameters: map[string]any{"type": "object"}}}}
			wire := func(messages []openai.ChatCompletionMessage) map[string]any {
				t.Helper()
				body := marshalResponsesRequest(t, buildOpenAIResponsesRequest(cfg, tc.model, messages, tools, nil))
				if tc.auth == authChatGPTOAuth {
					if err := chatGPTResponsesRequest(body); err != nil {
						t.Fatal(err)
					}
				}
				return body
			}
			first := wire(messages)
			raw, _ := json.Marshal(first)
			if first["instructions"] != "agent rules\n\nfrozen workspace" || strings.Contains(string(raw), "prompt_cache_breakpoint") || first["prompt_cache_options"] != nil {
				t.Fatalf("implicit-cache request changed shape: %s", raw)
			}
			input := first["input"].([]any)
			if len(input) != 1 || input[0].(map[string]any)["role"] != "user" {
				t.Fatalf("instructions duplicated in conversation input: %s", raw)
			}
			second := wire(append(messages,
				openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "call_one", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{}`}}}},
				openai.ChatCompletionMessage{Role: "tool", ToolCallID: "call_one", Content: "file contents"},
			))
			nextInput := second["input"].([]any)
			if first["instructions"] != second["instructions"] || !reflect.DeepEqual(input, nextInput[:len(input)]) || !reflect.DeepEqual(first["tools"], second["tools"]) || !reflect.DeepEqual(first["reasoning"], second["reasoning"]) {
				t.Fatal("tool step changed the frozen prefix, tools or reasoning settings")
			}
			if nextInput[len(nextInput)-1].(map[string]any)["output"] != "file contents" {
				t.Fatal("tool result lost while adding the prefix anchor")
			}
		})
	}
}
