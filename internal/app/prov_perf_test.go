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

	"github.com/openai/openai-go/v3/packages/ssestream"
	openai "github.com/sashabaranov/go-openai"
)

type auditTailErrorReader struct{}

func (auditTailErrorReader) Read([]byte) (int, error) { return 0, errors.New("unexpected EOF") }

func auditStream(s string, failAfterCompletion bool) *openAIResponsesSSEStream {
	var reader io.Reader = strings.NewReader(s)
	if failAfterCompletion {
		reader = io.MultiReader(reader, auditTailErrorReader{})
	}
	return &openAIResponsesSSEStream{decoder: ssestream.NewDecoder(&http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(reader)})}
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
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"private-answer\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":20,\"input_tokens_details\":{\"cached_tokens\":80},\"output_tokens_details\":{\"reasoning_tokens\":15},\"total_tokens\":120}}}\n\n")
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
}
