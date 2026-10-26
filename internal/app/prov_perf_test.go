// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

type auditTailErrorReader struct{}

func (auditTailErrorReader) Read([]byte) (int, error) { return 0, errors.New("unexpected EOF") }

func auditStream(s string, failAfterCompletion bool) *openAIResponsesSSEStream {
	var reader io.Reader = strings.NewReader(s)
	if failAfterCompletion {
		reader = io.MultiReader(reader, auditTailErrorReader{})
	}
	return &openAIResponsesSSEStream{decoder: newResponsesSSEDecoder(&http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(reader)})}
}

// A valid terminal event is sufficient; connection-tail errors cannot discard
// already completed output or its usage.
func TestAuditResponsesCompletedThenTransportFailure(t *testing.T) {
	t.Setenv("ALLY_PERF_DIAGNOSTICS", filepath.Join(t.TempDir(), "perf.jsonl"))
	ctx, finish := beginPerfRun(context.Background(), "mock-run")
	d := beginPerfAttempt(perfModelContext(ctx), ConfigState{ReasoningEffort: "max"}, "gpt-5")
	s := auditStream("data: {\"type\":\"response.output_text.delta\",\"delta\":\"private-prompt-sentinel\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"input_tokens_details\":{\"cached_tokens\":80},\"output_tokens\":20,\"output_tokens_details\":{\"reasoning_tokens\":15},\"total_tokens\":120}}}\n\n", true)
	s.diagnostic = d
	a := &App{reasoningStash: newReasoningStash()}
	result, emitted, err := a.consumeOpenAIResponsesStream(ConfigState{}, buildOpenAIResponsesRequest(ConfigState{}, "gpt-5", nil, nil, nil), s, nil)
	if err != nil || result == nil || !emitted || result.Usage == nil || result.Usage.CompletionTokens != 20 {
		t.Fatalf("completed response lost: result=%v emitted=%v err=%v", result, emitted, err)
	}
	d.finish(err)
	finish()
	raw, err := os.ReadFile(os.Getenv("ALLY_PERF_DIAGNOSTICS"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-prompt-sentinel") {
		t.Fatal("diagnostics leaked content")
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("records=%d", len(lines))
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatal(err)
	}
	if row["reasoning_tokens"] != float64(15) || row["output_tokens"] != float64(20) || row["total_tokens"] != float64(120) || row["terminal_event"] != "response.completed" {
		t.Fatalf("lost terminal usage: %v", row)
	}
}

func TestPerfDisabledAndToolMetadata(t *testing.T) {
	t.Setenv("ALLY_PERF_DIAGNOSTICS", "")
	ctx, finish := beginPerfRun(context.Background(), "disabled")
	finish()
	if ctx.Value(perfContextKey{}) != nil {
		t.Fatal("diagnostics enabled by default")
	}
	path := filepath.Join(t.TempDir(), "perf.jsonl")
	t.Setenv("ALLY_PERF_DIAGNOSTICS", path)
	ctx, finish = beginPerfRun(context.Background(), "tools")
	end1 := startPerfTool(ctx, "read", []byte("private-args-sentinel"))
	end2 := startPerfTool(ctx, "read", []byte("private-args-sentinel"))
	end2()
	end1()
	finish()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-args-sentinel") || !strings.Contains(string(raw), `"repeated_arguments":true`) || !strings.Contains(string(raw), `"event":"tool_overlap"`) {
		t.Fatalf("unsafe or missing tool metadata: %s", raw)
	}
}

func TestResponsesPlainAnswerKeepsNativeReasoning(t *testing.T) {
	cfg := ConfigState{APIFormat: apiFormatOpenAIResponses, Model: "gpt-5", responsesPromptCacheKey: "mock-session"}
	a := &App{reasoningStash: newReasoningStash()}
	s := auditStream("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_mock\",\"encrypted_content\":\"opaque\",\"summary\":[]}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"message\",\"id\":\"msg_mock\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\",\"annotations\":[]}]}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n", false)
	result, _, err := a.consumeOpenAIResponsesStream(cfg, buildOpenAIResponsesRequest(cfg, cfg.Model, nil, nil, nil), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.captureResponsesOutput(cfg, cfg.Model, nil, result)
	if !result.ReasoningPresent {
		t.Fatal("reasoning presence lost")
	}
	_, input := buildOpenAIResponsesInput([]openai.ChatCompletionMessage{{Role: "assistant", Content: result.Content}}, a.reasoningStash.get(reasoningReplayKey(cfg, cfg.Model)))
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "phase") || !strings.Contains(string(raw), "encrypted_content") {
		t.Fatalf("native replay lost: %s", raw)
	}
}

func TestPerfResponsesMockRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf.jsonl")
	t.Setenv("ALLY_PERF_DIAGNOSTICS", path)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Reasoning.Effort != "max" {
			t.Errorf("effort changed: %q", body.Reasoning.Effort)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "mock-request-id")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"private-answer\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"reasoning\":{\"context\":\"all_turns\"},\"usage\":{\"input_tokens\":100,\"output_tokens\":20,\"input_tokens_details\":{\"cached_tokens\":80,\"cache_write_tokens\":20},\"output_tokens_details\":{\"reasoning_tokens\":15},\"total_tokens\":120}}}\n\n")
	}))
	defer server.Close()
	cfg := ConfigState{APIFormat: apiFormatOpenAIResponses, BaseURL: server.URL, Model: "gpt-5", APIKey: "private-key", ReasoningEffort: "max", ProxyMode: "off"}
	ctx, finish := beginPerfRun(context.Background(), "mock-http")
	a := &App{reasoningStash: newReasoningStash()}
	result, err := a.streamModelResponse(perfReason(ctx, "agent_step"), cfg, cfg.Model, []openai.ChatCompletionMessage{{Role: "user", Content: "private-prompt"}}, nil, nil)
	finish()
	if err != nil || result == nil {
		t.Fatalf("mock request failed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-key", "private-prompt", "private-answer"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("content leaked")
		}
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.Split(string(raw), "\n")[0]), &row); err != nil {
		t.Fatal(err)
	}
	if row["request_id"] != "mock-request-id" || row["effort_wire"] != "max" || row["first_sse_at"] == nil || row["first_response_byte_at"] == nil || row["connection_reused"] == nil {
		t.Fatalf("missing request telemetry: %v", row)
	}
	if row["wire_prefix"] != "first_request" || row["cache_write_tokens"] != float64(20) || row["cache_usage_reported"] != true || row["reasoning_context_effective"] != "all_turns" {
		t.Fatalf("missing cache telemetry: %v", row)
	}
}

func TestPerfFinalResponsesPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf.jsonl")
	t.Setenv("ALLY_PERF_DIAGNOSTICS", path)
	ctx, finish := beginPerfRun(context.Background(), "mock-prefix")
	cfg := ConfigState{AuthType: authChatGPTOAuth, OAuthAccountID: "private-account", APIFormat: apiFormatOpenAIResponses, responsesPromptCacheKey: "private-route"}
	// Observe the post-OAuth wire. Replaying the old reasoning differently is
	// invisible to the orchestration fingerprint, which sees only chat messages.
	body := map[string]any{
		"model": "gpt-6.1-sol", "instructions": "private-instructions",
		"input":            []any{map[string]any{"role": "user", "content": "private-question"}},
		"prompt_cache_key": "private-route",
		"tools":            []any{map[string]any{"type": "function", "name": "read"}},
	}
	if err := chatGPTResponsesRequest(body); err != nil {
		t.Fatal(err)
	}
	observe := func(config ConfigState, want string) {
		t.Helper()
		d := beginPerfAttempt(perfModelContext(ctx), config, "gpt-6.1-sol")
		d.request(config, body)
		if d.row["wire_prefix"] != want {
			t.Fatalf("prefix = %v, want %s", d.row["wire_prefix"], want)
		}
		d.finish(nil)
	}
	observe(cfg, "first_request")
	reasoning := map[string]any{"type": "reasoning", "encrypted_content": "private-encrypted"}
	body["input"] = append(body["input"].([]any), reasoning, map[string]any{"type": "function_call_output", "output": "private-result"})
	observe(cfg, "unchanged_prefix")
	// Only hashes live in the baseline, so an in-place mutation is detected.
	reasoning["encrypted_content"] = "private-rewritten"
	observe(cfg, "tail_changed")
	body["input"] = body["input"].([]any)[:1]
	observe(cfg, "removed_changed")
	body["tools"].([]any)[0].(map[string]any)["description"] = "private-schema-change"
	observe(cfg, "head_changed")
	other := cfg
	other.reasoningScope = "private-subagent"
	observe(other, "first_request")
	// Unknown server strings must not be copied into the content-free log.
	d := beginPerfAttempt(ctx, cfg, "gpt-6.1-sol")
	d.event("response.completed", []byte(`{"response":{"reasoning":{"context":"private-server-string"},"usage":{"input_tokens":100,"output_tokens":1}}}`))
	d.finish(nil)
	finish()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-") {
		t.Fatal("cache diagnostics leaked content or identity")
	}
	if !strings.Contains(string(raw), `"wire_encrypted_reasoning_items":1`) || !strings.Contains(string(raw), `"cache_usage_reported":false`) {
		t.Fatal("missing reasoning or unknown-cache telemetry")
	}
}
