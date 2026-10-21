// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	openai "github.com/sashabaranov/go-openai"
)

func fastCompactFixture() []openai.ChatCompletionMessage {
	h := []openai.ChatCompletionMessage{{Role: "user", Content: "keep my requirements"}}
	for i := 0; i < 6; i++ {
		calls := []openai.ToolCall{{ID: fmt.Sprintf("call-%d", i), Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{}`}}}
		if i == 2 { // The first retained round has parallel results: keep both.
			calls = append(calls, openai.ToolCall{ID: "parallel", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{}`}})
		}
		h = append(h, openai.ChatCompletionMessage{Role: "assistant", Content: fmt.Sprintf("answer-%d", i), ReasoningContent: strings.Repeat("思考依据", 600), ToolCalls: calls})
		for _, call := range calls {
			h = append(h, openai.ChatCompletionMessage{Role: "tool", ToolCallID: call.ID, Content: "BEGIN " + strings.Repeat("文件内容", 900) + " END"})
		}
	}
	return append(h, openai.ChatCompletionMessage{Role: "assistant", Content: "final answer", ReasoningContent: "recent thinking"})
}

func TestFastCompactPreservesTailAndIsIdempotent(t *testing.T) {
	a := newApiTestApp(t)
	a.modelHistoryCodec = nil // no OS credentials/storage in unit tests
	history := fastCompactFixture()
	const sid = "fast"
	a.saveHistory(sid, history)
	cache := a.sessionReadCacheFor(sid)
	seedSessionReadCacheForTest(cache, "k", "hash")
	a.recordContextAnchor(sid, history, &modelUsage{PromptTokens: 50000})
	result, err := a.FastCompactSession(sid) // no model or credentials configured
	if err != nil {
		t.Fatal(err)
	}
	after := a.loadSessionHistoryCopy(sid)
	cutoff := fastCompactCutoff(history, 4)
	if !reflect.DeepEqual(after[cutoff:], history[cutoff:]) {
		t.Fatal("recent complete tool rounds and following messages changed")
	}
	if len(after) != len(history) || !reflect.DeepEqual(after[0], history[0]) {
		t.Fatal("message order/count or user requirements changed")
	}
	if result["reasoningCount"] != 2 || len(result["toolSummaries"].(map[string]string)) != 2 {
		t.Fatalf("unexpected compact counts: %v", result)
	}
	for _, m := range after[:cutoff] {
		if !utf8.ValidString(m.Content) || m.ReasoningContent != "" {
			t.Fatal("invalid UTF-8 or old reasoning remained")
		}
	}
	if len(cache.entries) != 0 || a.contextAnchorFor(sid).tokens != 0 {
		t.Fatal("read cache or measured context survived rewrite")
	}
	if result["tokensAfter"].(int) >= result["tokensBefore"].(int) {
		t.Fatal("context did not shrink")
	}
	second, err := a.FastCompactSession(sid)
	if err != nil || second["changed"] != false || !reflect.DeepEqual(a.loadSessionHistoryCopy(sid), after) {
		t.Fatalf("repeated compaction must be a no-op: %v %v", second, err)
	}
}

func TestFastCompactKeepsLatestPlanAndSmallResults(t *testing.T) {
	a := newApiTestApp(t)
	a.modelHistoryCodec = nil
	history := fastCompactFixture()
	history[1].ToolCalls[0].Function.Name = planToolName
	history[4].Content = "OK"
	a.saveHistory("plan", history)
	if _, err := a.FastCompactSession("plan"); err != nil {
		t.Fatal(err)
	}
	after := a.loadSessionHistoryCopy("plan")
	if after[2].Content != history[2].Content || after[4].Content != "OK" {
		t.Fatal("latest plan or small result was replaced")
	}
}

func TestFastCompactDoesNotGrowSmallHistory(t *testing.T) {
	a := newApiTestApp(t)
	h := fastCompactFixture()
	for i := range h {
		if h[i].Role == "assistant" {
			h[i].ReasoningContent = "x"
		}
		if h[i].Role == "tool" {
			h[i].Content = "OK"
		}
	}
	a.saveHistory("small", h)
	result, err := a.FastCompactSession("small")
	if err != nil || result["changed"] != false || !reflect.DeepEqual(a.loadSessionHistoryCopy("small"), h) {
		t.Fatalf("small messages must not grow due to excerpt markers: %v %v", result, err)
	}
}

func TestFastCompactGuardsAndCancellation(t *testing.T) {
	a := newApiTestApp(t)
	a.runSessions["run"] = "running"
	if _, err := a.FastCompactSession("running"); !errors.Is(err, errSessionRunning) {
		t.Fatalf("active run error: %v", err)
	}
	a.compactingSessions["busy"] = struct{}{}
	if _, err := a.FastCompactSession("busy"); !errors.Is(err, errSessionCompacting) {
		t.Fatalf("concurrent compact error: %v", err)
	}
	a.stopping = true
	if _, err := a.FastCompactSession("stopping"); !errors.Is(err, errAppStopping) {
		t.Fatalf("stopping error: %v", err)
	}
	a.stopping = false
	h := fastCompactFixture()
	a.saveHistory("cancel", h)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.compactSessionWithMode(ctx, "cancel", "", ConfigState{}, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error: %v", err)
	}
	if !reflect.DeepEqual(a.loadSessionHistoryCopy("cancel"), h) || a.compactSessionRunning("cancel") {
		t.Fatal("cancelled compaction changed history or left session busy")
	}
}

func TestFastCompactSurvivesProtectedRestartAndDiskFallback(t *testing.T) {
	a := newApiTestApp(t)
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	a.modelHistoryCodec = &modelHistoryCodec{protector: oauthTestProtector{gcm}}
	a.saveHistory("restart", fastCompactFixture())
	if _, err := a.FastCompactSession("restart"); err != nil {
		t.Fatal(err)
	}
	after := a.loadSessionHistoryCopy("restart")
	disk := sanitizeHistoryMessagesForDisk(after)
	if disk[2].Content != after[2].Content || !strings.HasPrefix(disk[2].Content, fastCompactToolMarker) {
		t.Fatal("disk fallback dropped the bounded tool excerpt")
	}
	raw, err := json.Marshal(a.modelHistoryForStorage("restart", disk, after))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), protectedModelStateField) {
		t.Fatal("retained tail must be protected even without Responses replay")
	}
	loaded, _ := a.modelHistoryFromStorage("restart", raw, disk)
	if !reflect.DeepEqual(loaded, after) {
		t.Fatal("restart lost compacted excerpts or retained recent results/reasoning")
	}
}

func TestFastCompactRetainsOnlyTailReplayAcrossProtocols(t *testing.T) {
	h := fastCompactFixture()
	s := newReasoningStash()
	for _, format := range []string{apiFormatOpenAIChat, apiFormatOpenAIResponses, apiFormatAnthropicMessages} {
		cfg := ConfigState{Model: "m", APIFormat: format, responsesPromptCacheKey: openAIResponsesPromptCacheKey("s")}
		key := reasoningReplayKey(cfg, "m")
		s.appendTurn(key, reasoningTurn{callIDs: []string{"call-0"}, chatDetails: json.RawMessage(`[{}]`)})
		s.appendTurn(key, reasoningTurn{callIDs: []string{"call-2", "parallel"}, chatDetails: json.RawMessage(`[{}]`)})
		s.appendResponses(key, responsesMessageKey(0, h[1]), []json.RawMessage{json.RawMessage(`{"type":"reasoning"}`)})
		cutoff := fastCompactCutoff(h, 4)
		s.appendResponses(key, responsesMessageKey(2, h[cutoff]), []json.RawMessage{json.RawMessage(`{"type":"message","id":"msg_recent"}`)})
	}
	other := ConfigState{Model: "m", responsesPromptCacheKey: openAIResponsesPromptCacheKey("other")}
	s.appendTurn(reasoningReplayKey(other, "m"), reasoningTurn{callIDs: []string{"other"}, chatDetails: json.RawMessage(`[{}]`)})
	if !s.retainFastCompactTail("s", h, h, fastCompactCutoff(h, 4)) {
		t.Fatal("expected old replay to be removed")
	}
	for _, format := range []string{apiFormatOpenAIChat, apiFormatOpenAIResponses, apiFormatAnthropicMessages} {
		cfg := ConfigState{Model: "m", APIFormat: format, responsesPromptCacheKey: openAIResponsesPromptCacheKey("s")}
		p := s.get(reasoningReplayKey(cfg, "m"))
		if len(p.turns) != 1 || p.turns[0].callIDs[0] != "call-2" || len(p.responsesNative) != 1 {
			t.Fatal("recent replay lost or old replay survived")
		}
	}
	if len(s.get(reasoningReplayKey(other, "m")).turns) != 1 {
		t.Fatal("another session's replay was modified")
	}
}

func TestFastCompactNativeResponsesPreservesPhaseAndCalls(t *testing.T) {
	output := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_old","encrypted_content":"old-secret"}`),
		json.RawMessage(`{"type":"message","id":"msg_old","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"original answer","annotations":[]}]}`),
		json.RawMessage(`{"type":"function_call","id":"fc_old","call_id":"call-old","name":"read","arguments":"{}"}`),
	}
	clean, changed := compactNativeResponses(output, "bounded reasoning excerpt")
	if !changed || len(clean) != 3 || string(clean[1]) != string(output[1]) || string(clean[2]) != string(output[2]) {
		t.Fatal("native compaction changed tool identity or retained reasoning")
	}
	var message struct {
		Content string
	}
	if err := json.Unmarshal(clean[0], &message); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(clean[0]), "old-secret") || message.Content != "bounded reasoning excerpt" {
		t.Fatal("old opaque reasoning survived or excerpt was lost")
	}
	if !responsesOutputReplayable(clean) {
		t.Fatal("compacted native input is not replayable")
	}
	if string(output[0]) != `{"type":"reasoning","id":"rs_old","encrypted_content":"old-secret"}` {
		t.Fatal("copy-on-write snapshot was mutated")
	}
	_, repeated := compactNativeResponses(clean, "")
	if repeated {
		t.Fatal("already compacted native history must be idempotent")
	}
}

func TestAutoCompactMethodConfigDefaultsAndPersistence(t *testing.T) {
	a := newApiTestApp(t)
	if a.config.AutoCompactMethod != autoCompactMethodSummary {
		t.Fatal("default must remain the full LLM summary")
	}
	for _, tc := range []struct{ value, want string }{
		{"recent", "recent"}, {"summary", "summary"}, {" RECENT ", "recent"}, {"unknown", "summary"},
	} {
		if err := a.SaveConfig(ConfigState{AutoCompactMethod: tc.value}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(a.configPath)
		if err != nil {
			t.Fatal(err)
		}
		var stored ConfigState
		if err := json.Unmarshal(raw, &stored); err != nil {
			t.Fatal(err)
		}
		if stored.AutoCompactMethod != tc.want || a.effectiveConfig(ConfigState{}).AutoCompactMethod != tc.want {
			t.Fatalf("method %q did not persist as %q", tc.value, tc.want)
		}
	}
	if got := mergeConfig(ConfigState{AutoCompactMethod: "recent"}, ConfigState{}).AutoCompactMethod; got != "recent" {
		t.Fatal("an absent request overlay must preserve the stored method")
	}
	if got := mergeConfig(ConfigState{}, ConfigState{}).AutoCompactMethod; got != "summary" {
		t.Fatal("legacy config must default to the full LLM summary")
	}
}

func TestAutoCompactRecentPreservesRequestWithoutLLM(t *testing.T) {
	a := newApiTestApp(t)
	a.modelHistoryCodec = nil
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	cfg := ConfigState{AutoCompactMethod: "recent", Model: "test", APIKey: "key", BaseURL: server.URL}
	prefix := []openai.ChatCompletionMessage{{Role: "system", Content: "frozen rules"}, {Role: "system", Content: "frozen workspace map"}}
	history := append(cloneChatMessages(prefix), fastCompactFixture()...)
	history = append(history, openai.ChatCompletionMessage{Role: "user", Content: "current question"}, openai.ChatCompletionMessage{Role: "user", Content: "injected followup"})
	a.compactFailures["recent-run"] = maxConsecutiveCompactFailures
	compacted, payload, err := a.compactRunHistory(context.Background(), cfg, "recent-run", compactReasonThreshold, ChatRequest{Message: "current question"}, history, 50000)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 || payload["tier"] != "fast" || payload["sessionId"] != "recent-run" {
		t.Fatal("recent threshold compaction called the model or lost its event metadata")
	}
	if len(compacted) != len(history) || !reflect.DeepEqual(compacted[:len(prefix)], prefix) {
		t.Fatal("local compaction changed the frozen prefix or duplicated the user turn")
	}
	if !reflect.DeepEqual(compacted[len(compacted)-2:], history[len(history)-2:]) {
		t.Fatal("current and injected user messages must be preserved once, in order")
	}
	if a.compactFailureCount("recent-run") != 0 {
		t.Fatal("a successful local compaction must clear previous summary failures")
	}
	_, _, err = a.compactRunHistory(context.Background(), cfg, "recent-run", compactReasonThreshold, ChatRequest{}, compacted, 40000)
	if !errors.Is(err, errHistoryUnchanged) || !isCompactSkip(err) || requests.Load() != 0 || a.compactFailureCount("recent-run") != 0 {
		t.Fatalf("unchanged history must quietly skip without an LLM fallback: %v", err)
	}
}

func TestAutoCompactRecentOverflowStillUsesSummary(t *testing.T) {
	a := newApiTestApp(t)
	a.stats = nil
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseChatChunk("overflow summary"), sseChatFinishChunk("stop"), sseDone)
	}))
	defer server.Close()
	cfg := ConfigState{AutoCompactMethod: "recent", Model: "test", APIKey: "key", BaseURL: server.URL, APIFormat: apiFormatOpenAIChat}
	_, payload, err := a.compactRunHistory(context.Background(), cfg, "overflow-recent", compactReasonOverflow, ChatRequest{}, fastCompactFixture(), 50000)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || payload["summary"] != "overflow summary" || len(a.loadSessionHistoryCopy("overflow-recent")) != 1 {
		t.Fatal("overflow must use the full summary recovery even when recent mode is selected")
	}
}
