// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/packages/ssestream"
	openai "github.com/sashabaranov/go-openai"
)

func newAuditDecoder(body io.ReadCloser) ssestream.Decoder {
	return ssestream.NewDecoder(&http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body})
}

func protocolConfig(url, format string) ConfigState {
	return ConfigState{BaseURL: url, APIFormat: format, APIKey: "mock-key", APIKeys: []string{"mock-key"}, Model: "gpt-5", ReasoningEffort: "max", LLMRetries: 1, ProxyMode: "off", responsesPromptCacheKey: openAIResponsesPromptCacheKey("protocol-session")}
}

func nativeReply(output []json.RawMessage) string {
	raw, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"output": output, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "output_tokens_details": map[string]any{"reasoning_tokens": 15}, "total_tokens": 120}}})
	return "data: " + string(raw) + "\n\n"
}

func TestResponsesNativeReplayAndProtectedRestart(t *testing.T) {
	a := newApiTestApp(t)
	block, _ := aes.NewCipher(make([]byte, 32))
	gcm, _ := cipher.NewGCM(block)
	protector := oauthTestProtector{gcm}
	a.modelHistoryCodec = &modelHistoryCodec{protector: protector}
	first := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-state-1","summary":[]}`),
		json.RawMessage(`{"type":"message","id":"msg_1","role":"assistant","phase":"commentary","status":"completed","content":[{"type":"output_text","text":"working","annotations":[]}]}`),
		json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"mock.txt\"}","status":"completed"}`),
	}
	second := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_2","encrypted_content":"opaque-state-2","summary":[]}`),
		json.RawMessage(`{"type":"message","id":"msg_2","role":"assistant","phase":"final_answer","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}`),
	}
	var count atomic.Int32
	var sent [][]map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input     []map[string]any `json:"input"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Reasoning.Effort != "max" {
			t.Errorf("effort=%q", body.Reasoning.Effort)
		}
		sent = append(sent, body.Input)
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 {
			io.WriteString(w, nativeReply(first))
		} else {
			io.WriteString(w, nativeReply(second))
		}
	}))
	defer server.Close()
	cfg := protocolConfig(server.URL, apiFormatOpenAIResponses)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	history := []openai.ChatCompletionMessage{{Role: "user", Content: "mock task"}}
	r1, err := a.streamModelResponse(ctx, cfg, cfg.Model, history, nil, nil)
	if err != nil || r1.Content != "working" || len(r1.ToolCalls) != 1 {
		t.Fatalf("terminal-only output lost: %+v %v", r1, err)
	}
	history = append(history, openai.ChatCompletionMessage{Role: "assistant", Content: r1.Content, ToolCalls: r1.ToolCalls}, openai.ChatCompletionMessage{Role: "tool", ToolCallID: r1.ToolCalls[0].ID, Content: "private-tool-result-sentinel"})
	r2, err := a.streamModelResponse(ctx, cfg, cfg.Model, history, nil, nil)
	if err != nil || r2.Content != "done" {
		t.Fatalf("second turn: %+v %v", r2, err)
	}
	history = append(history, openai.ChatCompletionMessage{Role: "assistant", Content: r2.Content})
	if len(sent[1]) != 5 || sent[1][1]["type"] != "reasoning" || sent[1][2]["phase"] != "commentary" || sent[1][3]["type"] != "function_call" || sent[1][4]["call_id"] != "call_1" {
		t.Fatalf("native ordering/pairing: %#v", sent[1])
	}
	for i, raw := range first {
		var original map[string]any
		_ = json.Unmarshal(raw, &original)
		if !reflect.DeepEqual(original, sent[1][i+1]) {
			t.Fatalf("item %d changed: %#v != %#v", i, original, sent[1][i+1])
		}
	}
	sid := "protocol-session"
	a.saveHistory(sid, history)
	f, err := os.Open(a.historyDiskPaths(sid)[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-tool-result-sentinel") || strings.Contains(string(raw), "opaque-state-1") {
		t.Fatal("protected model state leaked in plaintext")
	}
	var legacy []openai.ChatCompletionMessage
	if json.Unmarshal(raw, &legacy) != nil || legacy[2].Content != toolResultPlaceholder {
		t.Fatal("legacy history compatibility lost")
	}
	b := newApiTestApp(t)
	b.historiesDir = a.historiesDir
	b.modelHistoryCodec = &modelHistoryCodec{protector: protector}
	restored, _, _ := b.sessionHistorySnapshot(sid)
	if restored[2].Content != "private-tool-result-sentinel" {
		t.Fatal("model tool evidence lost on restart")
	}
	_, input := buildOpenAIResponsesInput(restored, b.reasoningStash.get(reasoningReplayKey(cfg, cfg.Model)))
	encoded, _ := json.Marshal(input)
	if !strings.Contains(string(encoded), `"phase":"final_answer"`) || !strings.Contains(string(encoded), "opaque-state-2") {
		t.Fatalf("restart lost ordinary reasoning: %s", encoded)
	}
	// OAuth's public route must preserve native replay while applying its
	// existing namespace normalization, without needing a real credential.
	var wire map[string]any
	request, _ := json.Marshal(buildOpenAIResponsesRequest(cfg, cfg.Model, restored, nil, b.reasoningStash.get(reasoningReplayKey(cfg, cfg.Model))))
	_ = json.Unmarshal(request, &wire)
	if err := chatGPTResponsesRequest(wire); err != nil {
		t.Fatal(err)
	}
	items := wire["input"].([]any)
	if items[3].(map[string]any)["namespace"] != "ally" || items[6].(map[string]any)["phase"] != "final_answer" {
		t.Fatalf("OAuth replay changed: %v", items)
	}
	// Changing the text history invalidates the protected snapshot as a unit.
	var rows []map[string]json.RawMessage
	_ = json.Unmarshal(raw, &rows)
	rows[0]["content"] = json.RawMessage(`"changed history"`)
	tampered, _ := json.Marshal(rows)
	_ = json.Unmarshal(tampered, &legacy)
	_, publish := b.modelHistoryFromStorage(sid, tampered, legacy)
	if publish != nil {
		t.Fatal("stale replay survived changed history")
	}
	other := cfg
	other.BaseURL = "https://other-provider.invalid/v1"
	if b.reasoningStash.get(reasoningReplayKey(other, other.Model)) != nil {
		t.Fatal("opaque state crossed endpoints")
	}
	b.reasoningStash.clearSession(sid)
	if b.reasoningStash.get(reasoningReplayKey(cfg, cfg.Model)) != nil {
		t.Fatal("session cleanup retained replay")
	}
}

func TestResponsesTerminalDoesNotWaitForConnection(t *testing.T) {
	for _, auth := range []string{"", authChatGPTOAuth} {
		t.Run(auth, func(t *testing.T) {
			reader, writer := io.Pipe()
			go func() { _, _ = io.WriteString(writer, nativeReply(nil)) }()
			s := auditStream("", false)
			// Use the same real SSE decoder, with a body which remains open.
			s.decoder = newAuditDecoder(reader)
			a := &App{reasoningStash: newReasoningStash()}
			done := make(chan error, 1)
			go func() {
				_, _, err := a.consumeOpenAIResponsesStream(ConfigState{AuthType: auth}, buildOpenAIResponsesRequest(ConfigState{}, "gpt-5", nil, nil, nil), s, nil)
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				_ = writer.Close()
				t.Fatal("completed response still waits for EOF")
			}
			_ = writer.Close()
		})
	}
}

func TestSubagentDoesNotMultiplyAdapterRetries(t *testing.T) {
	for _, format := range []string{apiFormatOpenAIChat, apiFormatOpenAIResponses} {
		t.Run(format, func(t *testing.T) {
			a := newApiTestApp(t)
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.WriteHeader(503)
				io.WriteString(w, `{"error":{"message":"temporary unavailable","type":"server_error"}}`)
			}))
			defer server.Close()
			cfg := protocolConfig(server.URL, format)
			cfg.Workspace = t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := a.executeDelegate(ctx, cfg, "retry-session", AgentDelegateRequest{Task: "mock task", MaxSteps: 1})
			if err == nil || count.Load() != 2 {
				t.Fatalf("pre-output failure repeated entire retry group: calls=%d err=%v", count.Load(), err)
			}
		})
	}
}

type protocolTailTransport struct {
	body string
	tail error
}
type protocolErrorReader struct{ err error }

func (r protocolErrorReader) Read([]byte) (int, error) { return 0, r.err }
func (tr protocolTailTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader(tr.body), protocolErrorReader{tr.tail})), Request: req}, nil
}

func TestChatKeepsTrailingUsageAndCompletedTransportErrors(t *testing.T) {
	for _, tc := range []struct {
		name, tail string
		err        error
		wantError  bool
		wantUsage  bool
	}{
		{"usage after finish", `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"completion_tokens_details":{"reasoning_tokens":15},"total_tokens":120}}` + "\n\n", io.ErrUnexpectedEOF, false, true},
		{"transport error after finish", "", io.ErrUnexpectedEOF, false, false},
		{"idle timeout after finish", "", errStreamIdleTimeout, false, false},
		{"explicit error after finish", `data: {"error":{"message":"explicit server rejection","type":"server_error"}}` + "\n\n", io.EOF, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientCfg := openai.DefaultConfig("mock-key")
			clientCfg.HTTPClient = &http.Client{Transport: protocolTailTransport{body: sseChatChunk("done") + sseChatFinishChunk("stop") + tc.tail, tail: tc.err}}
			a := &App{reasoningStash: newReasoningStash()}
			r, _, err := a.openAIChatStreamAttempt(context.Background(), ConfigState{}, openai.NewClientWithConfig(clientCfg), openai.ChatCompletionRequest{Model: "gpt-5"}, &sseDoneWatcher{}, nil)
			if (err != nil) != tc.wantError {
				t.Fatalf("result=%v err=%v", r, err)
			}
			if tc.wantError {
				return
			}
			if r.Content != "done" {
				t.Fatalf("answer lost: %v", r)
			}
			if tc.wantUsage && (r.Usage == nil || r.Usage.ReasoningTokens != 15 || r.Usage.TotalTokens != 120 || r.Usage.CompletionTokens != 20) {
				t.Fatalf("usage lost/double counted: %v", r.Usage)
			}
		})
	}
}

func TestRetryModelTurnOnlyOwnsInterruptedOrEmptyResponses(t *testing.T) {
	for _, tc := range []struct {
		err           error
		emitted, want bool
	}{
		{errors.New("503 temporarily unavailable"), false, false},
		{io.ErrUnexpectedEOF, true, true},
		{errEmptyModelResponse, false, true},
		{context.Canceled, true, false},
		{errors.New("subscription_sharing_usage_limit_exceeded"), true, false},
	} {
		if got := retryModelTurn(tc.err, tc.emitted); got != tc.want {
			t.Errorf("%v emitted=%v: got %v", tc.err, tc.emitted, got)
		}
	}
}

func TestChatRemembersExplicitStreamOptionsRejection(t *testing.T) {
	var requests, rejected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["reasoning_effort"] != "max" {
			t.Error("reasoning effort changed")
		}
		if body["stream_options"] != nil {
			rejected.Add(1)
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"stream_options is unsupported","type":"invalid_request_error","param":"stream_options"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseChatChunk("done")+sseChatFinishChunk("stop")+sseDone)
	}))
	defer server.Close()
	cfg := protocolConfig(server.URL, apiFormatOpenAIChat)
	a := &App{reasoningStash: newReasoningStash()}
	for i := 0; i < 2; i++ {
		if _, err := a.streamModelResponse(context.Background(), cfg, cfg.Model, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if rejected.Load() != 1 || requests.Load() != 3 {
		t.Fatalf("known-invalid capability retried: requests=%d rejections=%d", requests.Load(), rejected.Load())
	}
}

func TestResponsesRepeatedAnswersKeepTheirOwnPhase(t *testing.T) {
	cfg := protocolConfig("https://mock.invalid/v1", apiFormatOpenAIResponses)
	a := &App{reasoningStash: newReasoningStash()}
	var history []openai.ChatCompletionMessage
	for _, phase := range []string{"commentary", "final_answer"} {
		history = append(history, openai.ChatCompletionMessage{Role: "user", Content: "next"})
		raw, _ := json.Marshal(map[string]any{"type": "message", "id": "msg_" + phase, "role": "assistant", "phase": phase, "content": []map[string]string{{"type": "output_text", "text": "same answer"}}})
		a.captureResponsesOutput(cfg, cfg.Model, history, &modelStreamResult{Content: "same answer", StopReason: "stop", responsesOutput: []json.RawMessage{raw}})
		history = append(history, openai.ChatCompletionMessage{Role: "assistant", Content: "same answer"})
	}
	_, input := buildOpenAIResponsesInput(history, a.reasoningStash.get(reasoningReplayKey(cfg, cfg.Model)))
	raw, _ := json.Marshal(input)
	var items []map[string]any
	_ = json.Unmarshal(raw, &items)
	if items[1]["phase"] != "commentary" || items[3]["phase"] != "final_answer" {
		t.Fatalf("identical text borrowed another turn: %s", raw)
	}
}

func TestRunUsageIncludesNestedAndFailedTerminalRequests(t *testing.T) {
	ctx := withModelRunUsage(context.Background())
	child := withModelRunUsage(ctx)
	if ctx.Value(modelRunUsageKey{}) != child.Value(modelRunUsageKey{}) {
		t.Fatal("delegate usage detached from parent")
	}
	failed := `data: {"type":"response.failed","response":{"error":{"code":"server_error","message":"temporary unavailable"},"usage":{"input_tokens":100,"output_tokens":20,"output_tokens_details":{"reasoning_tokens":15},"total_tokens":120}}}` + "\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, failed)
	}))
	defer server.Close()
	a := &App{reasoningStash: newReasoningStash()}
	cfg := protocolConfig(server.URL, apiFormatOpenAIResponses)
	cfg.noAdapterRetry = true
	if _, err := a.streamModelResponse(child, cfg, cfg.Model, nil, nil, nil); err == nil {
		t.Fatal("failed terminal accepted")
	}
	noteRunModelUsage(child, &modelUsage{PromptTokens: 50, CompletionTokens: 10, ReasoningTokens: 7}, nil)
	payload := map[string]any{}
	publishRunModelUsage(ctx, payload)
	if payload["inputTokens"] != 150 || payload["outputTokens"] != 30 || payload["reasoningTokens"] != 22 || payload["modelRequests"] != 2 {
		t.Fatalf("run usage lost or double counted: %v", payload)
	}
}

func TestChatCacheMissIncludesCompletelyUncachedRequests(t *testing.T) {
	var records []statsRecord
	for _, hit := range []int{80, 0} {
		usage := modelUsageFromLegacy(&openai.Usage{
			PromptTokens: 100, PromptTokensDetails: &openai.PromptTokensDetails{CachedTokens: hit},
		}, nil)
		records = append(records, statsRecord{InputTokens: usage.PromptTokens, CacheHitTokens: usage.CacheHitTokens, CacheMissTokens: usage.CacheMissTokens})
	}
	summary := summarizeStatsRecords(records)
	if summary.CacheHitTokens != 80 || summary.CacheMissTokens != 120 || summary.CacheHitRate != 0.4 {
		t.Fatalf("mixed cached/uncached requests inflated hit rate: %+v", summary)
	}
}

func TestAnthropicFailedAttemptsKeepUsage(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprint("retry=", retry), func(t *testing.T) {
			a := newApiTestApp(t)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n")
				if attempt == 1 {
					// EOF without a terminal event must retain the observed usage.
					return
				}
				io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n")
				io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":20}}\n\n")
				io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()
			cfg := protocolConfig(server.URL, apiFormatAnthropicMessages)
			cfg.Workspace = a.config.Workspace
			cfg.noAdapterRetry = !retry
			ctx := withModelRunUsage(t.Context())
			result, err := a.streamModelResponse(ctx, cfg, cfg.Model, []openai.ChatCompletionMessage{{Role: "user", Content: "test"}}, nil, nil)
			if retry {
				if err != nil {
					t.Fatal(err)
				}
				a.recordLLMUsage(cfg.Model, cfg.Workspace, result.Usage, 0)
			} else if err == nil {
				t.Fatal("truncated response was accepted")
			}
			wantCalls, wantInput, wantOutput := 1, 100, 1
			if retry {
				wantCalls, wantInput, wantOutput = 2, 200, 21
			}
			payload := map[string]any{}
			publishRunModelUsage(ctx, payload)
			if payload["modelRequests"] != wantCalls || payload["inputTokens"] != wantInput || payload["outputTokens"] != wantOutput {
				t.Fatalf("run usage = %v", payload)
			}
			stats := a.GetTokenStats().SummaryToday
			if stats.InputTokens != wantInput || stats.OutputTokens != wantOutput || stats.Requests != wantCalls {
				t.Fatalf("dashboard usage = %+v", stats)
			}
		})
	}
}

func TestResponsesIncompleteIsNotACompleteNativeTurn(t *testing.T) {
	cfg := protocolConfig("https://mock.invalid/v1", apiFormatOpenAIResponses)
	a := &App{reasoningStash: newReasoningStash()}
	r, _, err := a.consumeOpenAIResponsesStream(cfg, buildOpenAIResponsesRequest(cfg, cfg.Model, nil, nil, nil), auditStream(`data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":10,"output_tokens":20}}}`+"\n\n", true), nil)
	if err != nil || r.StopReason != "length" || modelResponseStopError(cfg, r) == nil {
		t.Fatalf("incomplete state lost: %v %v", r, err)
	}
	a.captureResponsesOutput(cfg, cfg.Model, nil, r)
	if a.reasoningStash.get(reasoningReplayKey(cfg, cfg.Model)) != nil {
		t.Fatal("incomplete response was persisted as complete")
	}
}

type unavailableHistoryProtector struct{}

func (unavailableHistoryProtector) Seal([]byte) ([]byte, error) {
	return nil, errors.New("unavailable")
}
func (unavailableHistoryProtector) Open([]byte) ([]byte, error) {
	return nil, errors.New("unavailable")
}

func TestProtectedHistoryFailureFallsBackWithoutPlaintextState(t *testing.T) {
	a := newApiTestApp(t)
	a.modelHistoryCodec = &modelHistoryCodec{protector: unavailableHistoryProtector{}}
	cfg := protocolConfig("https://mock.invalid/v1", apiFormatOpenAIResponses)
	history := []openai.ChatCompletionMessage{{Role: "user", Content: "task"}, {Role: "assistant", Content: "done"}}
	a.reasoningStash.appendResponses(reasoningReplayKey(cfg, cfg.Model), responsesMessageKey(0, history[1]), []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]}`), json.RawMessage(`{"type":"message","id":"msg_1","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}`)})
	disk := sanitizeHistoryMessagesForDisk(history)
	raw, err := json.Marshal(a.modelHistoryForStorage("protocol-session", disk, history))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "opaque") || strings.Contains(string(raw), protectedModelStateField) {
		t.Fatal("secure-storage failure exposed model state")
	}
	restored, publish := a.modelHistoryFromStorage("protocol-session", raw, disk)
	if publish != nil || len(restored) != len(history) {
		t.Fatal("legacy fallback broken")
	}
}

func TestResponsesOutputCaptureOrdersCompletionEventsAndHandlesReusedIndex(t *testing.T) {
	var capture responsesOutputCapture
	for _, raw := range []string{
		`{"output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"one"}}`,
		`{"output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]}}`,
		`{"output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","phase":"commentary"}}`,
		`{"output_index":2,"item":{"type":"function_call","id":"fc_2","call_id":"two"}}`,
	} {
		capture.observe("response.output_item.done", []byte(raw))
	}
	output, _ := capture.indexedOutput()
	want := []string{"rs_1", "msg_1", "fc_1", "fc_2"}
	if len(output) != len(want) {
		t.Fatal("reused index merged separate items")
	}
	for i, raw := range output {
		var item struct{ ID string }
		_ = json.Unmarshal(raw, &item)
		if item.ID != want[i] {
			t.Fatalf("order[%d]=%s", i, item.ID)
		}
	}
}

func TestResponsesInterruptedBeforeTerminalCannotBecomeReplayState(t *testing.T) {
	cfg := protocolConfig("https://mock.invalid/v1", apiFormatOpenAIResponses)
	a := &App{reasoningStash: newReasoningStash()}
	r, _, err := a.consumeOpenAIResponsesStream(cfg, buildOpenAIResponsesRequest(cfg, cfg.Model, nil, nil, nil), auditStream(`data: {"type":"response.output_text.delta","delta":"unfinished"}`+"\n\n", true), nil)
	if err == nil || r != nil || a.reasoningStash.get(reasoningReplayKey(cfg, cfg.Model)) != nil {
		t.Fatalf("interrupted stream accepted: %v %v", r, err)
	}
}

func TestResponsesCallIDsIndependentOfNativeReplay(t *testing.T) {
	for _, tc := range []struct {
		name, terminal string
		scope, native  bool
	}{
		{"no-native-output", "response.completed", true, false},
		{"no-replay-scope", "response.completed", false, true},
		{"incomplete", "response.incomplete", true, true},
		{"legacy-ledger", "response.completed", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApiTestApp(t)
			items := []json.RawMessage{
				json.RawMessage(`{"type":"reasoning","id":"rs_duplicate","encrypted_content":"opaque","summary":[]}`),
				json.RawMessage(`{"type":"function_call","id":"fc_a","call_id":"shared","name":"read","arguments":"{}"}`),
				json.RawMessage(`{"type":"function_call","id":"fc_b","call_id":"shared","name":"read","arguments":"{}"}`),
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for i, item := range items {
					kind := "response.output_item.added"
					if tc.native {
						kind = "response.output_item.done"
					}
					raw, _ := json.Marshal(map[string]any{"type": kind, "output_index": i, "item": item})
					io.WriteString(w, "data: "+string(raw)+"\n\n")
				}
				io.WriteString(w, `data: {"type":"`+tc.terminal+`","response":{}}`+"\n\n")
			}))
			defer server.Close()
			cfg := protocolConfig(server.URL, apiFormatOpenAIResponses)
			if !tc.scope {
				cfg.responsesPromptCacheKey = ""
			}
			history := []openai.ChatCompletionMessage{
				{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "shared", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: "{}"}}}},
				{Role: "tool", ToolCallID: "shared", Content: "previous result"},
			}
			r, err := a.streamOpenAIResponses(context.Background(), cfg, cfg.Model, history, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.ToolCalls) != 2 || r.ToolCalls[0].ID != "shared__2" || r.ToolCalls[1].ID != "shared__3" {
				t.Fatalf("call identities: %+v", r.ToolCalls)
			}
			if tc.native {
				p := a.reasoningStash.get(reasoningReplayKey(cfg, cfg.Model))
				if p == nil || len(p.turns) != 1 || !reflect.DeepEqual(p.turns[0].callIDs, []string{"shared__2", "shared__3"}) {
					t.Fatalf("legacy ledger uses stale ids: %+v", p)
				}
				if p.turns[0].responsesItems["shared__2"] != "fc_a" || p.turns[0].responsesItems["shared__3"] != "fc_b" {
					t.Fatalf("item pairing lost: %+v", p.turns[0].responsesItems)
				}
				if tc.scope && tc.terminal == "response.completed" {
					m := openai.ChatCompletionMessage{Role: "assistant", ToolCalls: r.ToolCalls}
					output := p.nativeResponses(1, m)
					if len(output) != 3 {
						t.Fatal("native replay missing after id normalization")
					}
					for i, id := range []string{"shared__2", "shared__3"} {
						var call struct {
							CallID string `json:"call_id"`
						}
						if json.Unmarshal(output[i+1], &call) != nil || call.CallID != id {
							t.Fatalf("native replay has stale call id: %s", output[i+1])
						}
					}
				}
			}
		})
	}
}

func TestProtectedHistoryOmitsImagesButKeepsToolEvidence(t *testing.T) {
	a := newApiTestApp(t)
	block, _ := aes.NewCipher(make([]byte, 32))
	gcm, _ := cipher.NewGCM(block)
	a.modelHistoryCodec = &modelHistoryCodec{protector: oauthTestProtector{gcm}}
	cfg := protocolConfig("https://provider.invalid", apiFormatOpenAIResponses)
	call := openai.ToolCall{ID: "call_image", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: "{}"}}
	history := []openai.ChatCompletionMessage{
		{Role: "user", MultiContent: []openai.ChatMessagePart{
			{Type: openai.ChatMessagePartTypeText, Text: "look at image"},
			{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64," + strings.Repeat("A", maxProtectedModelStateBytes)}},
		}},
		{Role: "assistant", ToolCalls: []openai.ToolCall{call}},
		{Role: "tool", ToolCallID: call.ID, Content: "real private tool evidence"},
	}
	a.captureResponsesOutput(cfg, cfg.Model, history[:1], &modelStreamResult{StopReason: "stop", ToolCalls: []openai.ToolCall{call}, responsesOutput: []json.RawMessage{json.RawMessage(`{"type":"function_call","id":"fc_image","call_id":"call_image","name":"read","arguments":"{}"}`)}})
	disk := sanitizeHistoryMessagesForDisk(history)
	raw, err := json.Marshal(a.modelHistoryForStorage("protocol-session", disk, history))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), protectedModelStateField) {
		t.Fatal("image caused protected history fallback")
	}
	restored, publish := a.modelHistoryFromStorage("protocol-session", raw, disk)
	if publish == nil || len(restored) != 3 || restored[2].Content != history[2].Content {
		t.Fatal("tool evidence or replay lost")
	}
	if len(restored[0].MultiContent) != 0 || !strings.Contains(restored[0].Content, "image attachment(s) omitted") {
		t.Fatal("image payload persisted")
	}
	if history[0].MultiContent[1].ImageURL.URL == "" {
		t.Fatal("in-memory image was modified")
	}
}

func TestResponsesSparseOutputIndexesDoNotDuplicateCalls(t *testing.T) {
	a := newApiTestApp(t)
	cfg := protocolConfig("https://mock.invalid", apiFormatOpenAIResponses)
	wire := ""
	for _, event := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_missing_done","role":"assistant"}}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","call_id":"one_call","name":"command","arguments":"{}"}}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","call_id":"one_call","name":"command","arguments":"{}"}}`,
		`{"type":"response.completed","response":{}}`,
	} {
		wire += "data: " + event + "\n\n"
	}
	r, _, err := a.consumeOpenAIResponsesStream(cfg, buildOpenAIResponsesRequest(cfg, cfg.Model, nil, nil, nil), auditStream(wire, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].ID != "one_call" {
		t.Fatalf("sparse output duplicated a side-effecting call: %+v", r.ToolCalls)
	}
}

func TestProtectedReplayRebasesOnlyUnchangedTurns(t *testing.T) {
	call := func(name, args string) openai.ToolCall {
		return openai.ToolCall{ID: "bad_call", Type: "function", Function: openai.FunctionCall{Name: name, Arguments: args}}
	}
	for _, tc := range []struct {
		name       string
		message    openai.ChatCompletionMessage
		withResult bool
	}{
		{"reasoning-only", openai.ChatCompletionMessage{Role: "assistant", ReasoningContent: "unfinished reasoning"}, false},
		{"dangling", openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{call("read", "{}")}}, false},
		{"concatenated-name", openai.ChatCompletionMessage{Role: "assistant", Content: "same", ToolCalls: []openai.ToolCall{call("readedit", "{}")}}, true},
		{"truncated-arguments", openai.ChatCompletionMessage{Role: "assistant", Content: "same", ToolCalls: []openai.ToolCall{call("read", `{"path":`)}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApiTestApp(t)
			block, _ := aes.NewCipher(make([]byte, 32))
			gcm, _ := cipher.NewGCM(block)
			protector := oauthTestProtector{gcm}
			a.modelHistoryCodec = &modelHistoryCodec{protector: protector}
			cfg := protocolConfig("https://mock.invalid", apiFormatOpenAIResponses)
			history := []openai.ChatCompletionMessage{{Role: "user", Content: "start"}}
			appendAnswer := func(id string) {
				raw := json.RawMessage(`{"type":"message","id":"` + id + `","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"same"}]}`)
				a.captureResponsesOutput(cfg, cfg.Model, history, &modelStreamResult{Content: "same", StopReason: "stop", responsesOutput: []json.RawMessage{raw}})
				history = append(history, openai.ChatCompletionMessage{Role: "assistant", Content: "same"})
			}
			appendAnswer("msg_first")
			// Give the malformed turn its own native state; it must never be
			// restored after its call projection changes during hygiene.
			if len(tc.message.ToolCalls) > 0 {
				c := tc.message.ToolCalls[0]
				item, _ := json.Marshal(map[string]any{"type": "function_call", "id": "fc_bad", "call_id": c.ID, "name": c.Function.Name, "arguments": c.Function.Arguments})
				a.captureResponsesOutput(cfg, cfg.Model, history, &modelStreamResult{Content: tc.message.Content, ToolCalls: tc.message.ToolCalls, StopReason: "stop", responsesOutput: []json.RawMessage{item}})
			}
			history = append(history, tc.message)
			if tc.withResult {
				history = append(history, openai.ChatCompletionMessage{Role: "tool", ToolCallID: "bad_call", Content: "old result"})
			}
			history = append(history, openai.ChatCompletionMessage{Role: "user", Content: "continue"})
			appendAnswer("msg_last")
			// Another identical answer catches ordinal collisions: retaining an
			// old key could otherwise attach msg_last to this later answer.
			appendAnswer("msg_tail")
			disk := sanitizeHistoryMessagesForDisk(history)
			raw, err := json.Marshal(a.modelHistoryForStorage("protocol-session", disk, history))
			if err != nil {
				t.Fatal(err)
			}
			b := newApiTestApp(t)
			b.modelHistoryCodec = &modelHistoryCodec{protector: protector}
			restored, publish := b.modelHistoryFromStorage("protocol-session", raw, disk)
			if publish == nil {
				t.Fatal("cleaning a preceding turn discarded all replay")
			}
			publish()
			p := b.reasoningStash.get(reasoningReplayKey(cfg, cfg.Model))
			ordinal := 0
			var found []string
			for _, m := range restored {
				if m.Role != "assistant" {
					continue
				}
				output := p.nativeResponses(ordinal, m)
				ordinal++
				if len(output) == 0 {
					continue
				}
				var item struct{ ID string }
				if json.Unmarshal(output[0], &item) != nil {
					t.Fatal("invalid native item")
				}
				found = append(found, item.ID)
			}
			if !reflect.DeepEqual(found, []string{"msg_first", "msg_last", "msg_tail"}) {
				t.Fatalf("replay lost, mispaired, or resurrected a removed call: %v", found)
			}
		})
	}
}
