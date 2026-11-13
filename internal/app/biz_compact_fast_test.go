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

func TestFastCompactLeavesLessThanHalf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		before, after int
		want          bool
	}{{100, 49, true}, {100, 50, false}, {101, 50, true}, {101, 51, false}, {0, 0, false}} {
		if got := fastCompactLeavesLessThanHalf(tc.before, tc.after); got != tc.want {
			t.Errorf("fastCompactLeavesLessThanHalf(%d, %d) = %v, want %v", tc.before, tc.after, got, tc.want)
		}
	}
}

func fastCompactFixture() []openai.ChatCompletionMessage {
	var history []openai.ChatCompletionMessage
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("call-%d", i)
		history = append(history,
			openai.ChatCompletionMessage{Role: "user", Content: fmt.Sprintf("requirements-%d", i)},
			openai.ChatCompletionMessage{Role: "assistant", Content: fmt.Sprintf("answer-%d", i), ReasoningContent: strings.Repeat("old reasoning ", 600), ToolCalls: []openai.ToolCall{{ID: id, Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{"purpose":"查看入口","files":[{"path":"main.go"}]}`}}}},
			openai.ChatCompletionMessage{Role: "tool", ToolCallID: id, Content: "// read | path=\"main.go\"\n\n" + strings.Repeat("file body ", 2000)},
			openai.ChatCompletionMessage{Role: "assistant", Content: fmt.Sprintf("final-%d", i), ReasoningContent: "final reasoning"},
		)
	}
	return history
}

func TestFastCompactPreservesTaskAndBodies(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	history := fastCompactFixture()
	const sid = "fast"
	a.saveHistory(sid, history)
	cache := a.sessionReadCacheFor(sid)
	seedSessionReadCacheForTest(cache, "k", "hash")
	a.recordContextAnchor(sid, history, &modelUsage{PromptTokens: 50000})
	result, err := a.FastCompactSession(sid)
	if err != nil {
		t.Fatal(err)
	}
	after := a.loadSessionHistoryCopy(sid)
	if len(after) != 5 || !reflect.DeepEqual(after[1:], history[len(history)-4:]) {
		t.Fatal("the most recent complete task changed")
	}
	entries, ok := decodeFastCompactHistory(after[0])
	if !ok || len(entries) != 20 {
		t.Fatal("historical speakers or tools were lost")
	}
	for i := 0; i < 5; i++ {
		if entries[i*4].Content != history[i*4].Content || entries[i*4+1].Content != history[i*4+1].Content || entries[i*4+3].Content != history[i*4+3].Content {
			t.Fatal("user/assistant body was changed")
		}
		entry := entries[i*4+2]
		if entry.Role != "tool_call" || entry.Purpose == "" || strings.Contains(entry.Content, "file body") {
			t.Fatal("tool body survived or purpose was lost")
		}
	}
	encoded, _ := json.Marshal(after[:1])
	if strings.Contains(string(encoded), "old reasoning") || !utf8.Valid(encoded) {
		t.Fatal("old reasoning survived or UTF-8 broke")
	}
	if len(cache.entries) != 0 || a.contextAnchorFor(sid).tokens != 0 {
		t.Fatal("rewrite retained stale caches")
	}
	if intFromAny(result["tokensAfter"]) >= intFromAny(result["tokensBefore"]) {
		t.Fatal("fixture must shrink")
	}
	second, err := a.FastCompactSession(sid)
	if err != nil || second["changed"] != false || !reflect.DeepEqual(a.loadSessionHistoryCopy(sid), after) {
		t.Fatalf("not idempotent: %v %v", second, err)
	}
	snapshot := legacySessionSnapshot(sid, after)
	if len(snapshot.Messages) != 23 || snapshot.Messages[0]["content"] != history[0].Content || snapshot.Messages[2]["role"] != "tool_call" {
		t.Fatal("legacy recovery displayed the generated record as a user question")
	}
}

func TestFastCompactTaskProvenanceSurvivesInjectedInputsAndRestart(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	const sid = "task"
	previous := fastCompactFixture()[:20]
	a.saveHistory(sid, previous)
	current := append(cloneChatMessages(previous), openai.ChatCompletionMessage{Role: "user", Content: "current task"})
	a.beginHistoryTask(sid, current)
	// A queued user input after a text reply still belongs to the same run.
	current = append(current,
		openai.ChatCompletionMessage{Role: "assistant", Content: "intermediate reply"},
		openai.ChatCompletionMessage{Role: "user", Content: "also handle whitespace"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "continuing", ReasoningContent: "current reasoning"},
	)
	a.saveHistory(sid, current)
	disk := sanitizeHistoryMessagesForDisk(current)
	raw, err := json.Marshal(a.modelHistoryForStorage(sid, disk, current))
	if err != nil {
		t.Fatal(err)
	}
	b := newIsolatedChatTestApp(t)
	b.modelHistoryCodec = nil
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	loaded, publish := b.modelHistoryFromStorage(sid, raw, disk)
	b.mu.Lock()
	if publish != nil {
		publish()
	}
	b.mu.Unlock()
	b.saveHistory(sid, loaded)
	candidate, err := b.prepareFastCompactHistory(context.Background(), sid, loaded, 0)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.cutoff != 1 || !reflect.DeepEqual(candidate.history[candidate.tailStart:], loaded[1:]) {
		t.Fatal("injected input moved the main task boundary")
	}
	// Repairing an earlier empty assistant must rebase, rather than forget, it.
	withEmpty := append([]openai.ChatCompletionMessage{{Role: "assistant"}}, current...)
	b.mu.Lock()
	b.historyCacheStateLocked(sid).task = &historyTaskBoundary{Start: len(previous), Key: historyTaskKey(current[len(previous)])}
	b.mu.Unlock()
	// A stale index is relocated by the original input, never used to cut the chain.
	b.saveHistory(sid, withEmpty)
	if boundary := b.historyTaskFor(sid, b.loadSessionHistoryCopy(sid)); boundary == nil || boundary.Start != len(previous) {
		t.Fatal("task provenance was lost during repair")
	}
}

func TestFastCompactDoesNotCutSingleTaskOrImageInputs(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	history := []openai.ChatCompletionMessage{{Role: "user", Content: "one task"}}
	for i := 0; i < 12; i++ {
		call := openai.ToolCall{ID: fmt.Sprint(i), Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{}`}}
		history = append(history, openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{call}}, openai.ChatCompletionMessage{Role: "tool", ToolCallID: call.ID, Content: strings.Repeat("data", 1000)})
	}
	if image := readImageInjectionMessage([]readImageCandidate{{Path: "test.png", DataURL: "data:image/png;base64,AAAA"}}); image != nil {
		history = append(history, *image)
	}
	a.saveHistory("one", history)
	result, err := a.FastCompactSession("one")
	if err != nil || result["changed"] != false || !reflect.DeepEqual(a.loadSessionHistoryCopy("one"), history) {
		t.Fatal("a single long task was partially compacted")
	}
}

func TestFastCompactDoesNotGrowPlainHistory(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	history := []openai.ChatCompletionMessage{
		{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"},
		{Role: "user", Content: "next"}, {Role: "assistant", Content: "ok"},
	}
	a.saveHistory("plain", history)
	result, err := a.FastCompactSession("plain")
	if err != nil || result["changed"] != false || !reflect.DeepEqual(history, a.loadSessionHistoryCopy("plain")) {
		t.Fatal("local compaction grew a short text exchange")
	}
}

func TestFastCompactDropsOpaqueOldReasoningWithoutVisibleSummary(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	history := []openai.ChatCompletionMessage{{Role: "user", Content: "first"}, {Role: "assistant", Content: "done"}, {Role: "user", Content: "next"}, {Role: "assistant", Content: "ok"}}
	cfg := ConfigState{Model: "m", APIFormat: apiFormatOpenAIResponses, responsesPromptCacheKey: openAIResponsesPromptCacheKey("opaque")}
	key := reasoningReplayKey(cfg, "m")
	a.reasoningStash.appendResponses(key, responsesMessageKey(0, history[1]), []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"rs_old","encrypted_content":"opaque","summary":[]}`), json.RawMessage(`{"type":"message","id":"msg_old"}`)})
	a.reasoningStash.appendResponses(key, responsesMessageKey(1, history[3]), []json.RawMessage{json.RawMessage(`{"type":"message","id":"msg_recent"}`)})
	a.saveHistory("opaque", history)
	result, err := a.FastCompactSession("opaque")
	if err != nil || result["changed"] != true {
		t.Fatal("invisible old reasoning was not compacted")
	}
	after := a.loadSessionHistoryCopy("opaque")
	payload := a.reasoningStash.get(key)
	if len(payload.responsesNative) != 1 || len(payload.nativeResponses(0, after[2])) != 1 {
		t.Fatal("opaque old reasoning survived or recent replay was lost")
	}
}

func TestFastCompactPreservesLatestPlanAndRepeatedCallRecords(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	const plan = "// plan | revision=2\n\n[ ] finish verification"
	history := []openai.ChatCompletionMessage{
		{Role: "user", Content: "old task"},
		{Role: "assistant", ReasoningContent: strings.Repeat("reasoning ", 1000), ToolCalls: []openai.ToolCall{
			{ID: "same", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{"purpose":"first purpose"}`}},
			{ID: "same", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{"purpose":"second purpose"}`}},
			{ID: "plan", Type: "function", Function: openai.FunctionCall{Name: planToolName, Arguments: `{}`}},
		}},
		{Role: "tool", ToolCallID: "same", Content: "first body"},
		{Role: "tool", ToolCallID: "same", Content: "second body"},
		{Role: "tool", ToolCallID: "plan", Content: plan},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "current task"},
	}
	a.saveHistory("plan-and-ids", history)
	if _, err := a.FastCompactSession("plan-and-ids"); err != nil {
		t.Fatal(err)
	}
	entries, ok := decodeFastCompactHistory(a.loadSessionHistoryCopy("plan-and-ids")[0])
	if !ok || len(entries) != 5 || entries[1].Purpose != "first purpose" || entries[2].Purpose != "second purpose" || !strings.Contains(entries[3].Content, plan) {
		t.Fatal("duplicate call records or latest plan were lost")
	}
}

func TestFastCompactRetainsOnlyTaskReplayAcrossProtocols(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	history := fastCompactFixture()
	const sid = "replay"
	for _, format := range []string{apiFormatOpenAIChat, apiFormatOpenAIResponses, apiFormatAnthropicMessages} {
		cfg := ConfigState{Model: "m", APIFormat: format, responsesPromptCacheKey: openAIResponsesPromptCacheKey(sid)}
		key := reasoningReplayKey(cfg, "m")
		a.reasoningStash.appendTurn(key, reasoningTurn{callIDs: []string{"call-0"}, chatDetails: json.RawMessage(`[{}]`)})
		a.reasoningStash.appendTurn(key, reasoningTurn{callIDs: []string{"call-5"}, chatDetails: json.RawMessage(`[{}]`)})
		a.reasoningStash.appendResponses(key, responsesMessageKey(0, history[1]), []json.RawMessage{json.RawMessage(`{"type":"message","id":"msg_old"}`)})
		a.reasoningStash.appendResponses(key, responsesMessageKey(10, history[21]), []json.RawMessage{json.RawMessage(`{"type":"message","id":"msg_current"}`)})
	}
	other := ConfigState{Model: "m", responsesPromptCacheKey: openAIResponsesPromptCacheKey("other")}
	a.reasoningStash.appendTurn(reasoningReplayKey(other, "m"), reasoningTurn{callIDs: []string{"other"}, chatDetails: json.RawMessage(`[{}]`)})
	a.saveHistory(sid, history)
	if _, err := a.FastCompactSession(sid); err != nil {
		t.Fatal(err)
	}
	after := a.loadSessionHistoryCopy(sid)
	for _, format := range []string{apiFormatOpenAIChat, apiFormatOpenAIResponses, apiFormatAnthropicMessages} {
		cfg := ConfigState{Model: "m", APIFormat: format, responsesPromptCacheKey: openAIResponsesPromptCacheKey(sid)}
		p := a.reasoningStash.get(reasoningReplayKey(cfg, "m"))
		if len(p.turns) != 1 || p.turns[0].callIDs[0] != "call-5" || len(p.responsesNative) != 1 || len(p.nativeResponses(0, after[2])) != 1 {
			t.Fatal("replay tail was lost or old items survived")
		}
	}
	if len(a.reasoningStash.get(reasoningReplayKey(other, "m")).turns) != 1 {
		t.Fatal("other session was changed")
	}
}

func TestFastCompactMetadataAndProtectedRestart(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	a.modelHistoryCodec = &modelHistoryCodec{protector: oauthTestProtector{gcm}}
	history := fastCompactFixture()
	call := history[1].ToolCalls[0]
	call.Function.Name = "mcp__s__inspect"
	call.Function.Arguments = fmt.Sprintf(`{"purpose":"server business","_purpose":"真正的调用目的","payload":%q}`, strings.Repeat("old argument body ", 2000))
	history[1].ToolCalls[0] = call
	meta := toolHistoryMetadata{Purpose: toolHistoryPurpose(call, "_purpose"), Headers: "// mcp | status=ok"}
	a.reasoningStash.recordToolStatus(openAIResponsesPromptCacheKey("restart"), history[2], toolResult{OK: true}, meta)
	a.saveHistory("restart", history)
	disk := sanitizeHistoryMessagesForDisk(history)
	raw, err := json.Marshal(a.modelHistoryForStorage("restart", disk, history))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	loaded, publish := a.modelHistoryFromStorage("restart", raw, disk)
	if publish != nil {
		publish()
	}
	a.saveHistory("restart", loaded)
	if _, err := a.FastCompactSession("restart"); err != nil {
		t.Fatal(err)
	}
	after := a.loadSessionHistoryCopy("restart")
	entries, ok := decodeFastCompactHistory(after[0])
	if !ok || entries[2].Purpose != meta.Purpose || entries[2].Status != "success" || strings.Contains(entries[2].Content, "server business") {
		t.Fatal("MCP purpose mapping/status did not survive restart")
	}
	disk = sanitizeHistoryMessagesForDisk(after)
	raw, err = json.Marshal(a.modelHistoryForStorage("restart", disk, after))
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.modelHistoryFromStorage("restart", raw, disk)
	if !reflect.DeepEqual(loaded, sanitizeHistoryMessagesFor(after, historyProfileProtected)) {
		t.Fatal("protected restart resurrected old state or lost records")
	}
	if !reflect.DeepEqual(disk[0], after[0]) {
		t.Fatal("plain disk fallback lost the historical record")
	}
}

func TestAutoCompactRecentPreservesCurrentReportsAndFrozenPrefix(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	const sid = "auto"
	history := fastCompactFixture()
	previous := history[:20]
	a.saveHistory(sid, previous)
	a.beginHistoryTask(sid, history)
	// Multiple completed delegates are still unread by the parent. Their full
	// reports and call pairing must remain available after automatic compaction.
	history = append(history, openai.ChatCompletionMessage{Role: "user", Content: "queued followup"})
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("delegate-%d", i)
		history = append(history,
			openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: id, Type: "function", Function: openai.FunctionCall{Name: "subagent", Arguments: `{"task":"inspect"}`}}}},
			openai.ChatCompletionMessage{Role: "tool", ToolCallID: id, Content: "// subagent | status=completed\n\n" + fmt.Sprintf("essential report %d", i)},
		)
	}
	prefix := []openai.ChatCompletionMessage{{Role: "system", Content: "frozen rules"}, {Role: "system", Content: "frozen map"}}
	request := append(cloneChatMessages(prefix), history...)
	compacted, payload, err := a.compactRunHistory(context.Background(), ConfigState{AutoCompactMethod: "recent"}, sid, compactReasonThreshold, ChatRequest{Message: history[20].Content}, request, 50000)
	if err != nil || payload["tier"] != "fast" {
		t.Fatalf("local compaction failed: %v %v", payload, err)
	}
	if !reflect.DeepEqual(compacted[:2], prefix) || !reflect.DeepEqual(compacted[3:], history[20:]) {
		t.Fatal("frozen prefix/current task/reports changed")
	}
}

func TestFastCompactRequestAdaptersKeepOnlyCurrentProtocolCalls(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	history := fastCompactFixture()
	a.saveHistory("wire", history)
	if _, err := a.FastCompactSession("wire"); err != nil {
		t.Fatal(err)
	}
	after := a.loadSessionHistoryCopy("wire")
	// A Chat request has only the current task's native assistant/tool pair.
	chat := marshalResponsesRequest(t, openai.ChatCompletionRequest{Model: "test", Messages: after})
	var calls, outputs int
	for _, raw := range chat["messages"].([]any) {
		message := raw.(map[string]any)
		if message["role"] == "assistant" {
			if message["reasoning_content"] == nil {
				t.Fatal("current Chat reasoning was dropped")
			}
			if toolCalls, ok := message["tool_calls"].([]any); ok {
				calls += len(toolCalls)
			}
		}
		if message["role"] == "tool" {
			outputs++
		}
	}
	if calls != 1 || outputs != 1 {
		t.Fatal("Chat request retained old protocol calls")
	}
	// Responses must produce exactly the same current call/result identities.
	_, input := buildOpenAIResponsesInput(after, nil)
	calls, outputs = 0, 0
	for _, raw := range input {
		item := marshalResponsesRequest(t, raw)
		switch item["type"] {
		case "function_call":
			calls++
			if item["call_id"] != "call-5" {
				t.Fatal("old Responses call survived")
			}
		case "function_call_output":
			outputs++
			if item["call_id"] != "call-5" {
				t.Fatal("orphan Responses result")
			}
		}
	}
	if calls != 1 || outputs != 1 {
		t.Fatal("Responses pairing changed")
	}
	_, anthropic := buildAnthropicMessages(after, nil, true)
	raw, err := json.Marshal(anthropic)
	if err != nil {
		t.Fatal(err)
	}
	var messages []struct {
		Content []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			ToolUseID string `json:"tool_use_id"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	calls, outputs = 0, 0
	for _, m := range messages {
		for _, block := range m.Content {
			if block.Type == "tool_use" {
				calls++
				if block.ID != "call-5" {
					t.Fatal("old Anthropic call survived")
				}
			}
			if block.Type == "tool_result" {
				outputs++
				if block.ToolUseID != "call-5" {
					t.Fatal("orphan Anthropic result")
				}
			}
		}
	}
	if calls != 1 || outputs != 1 {
		t.Fatal("Anthropic pairing changed")
	}
}

func TestFastCompactDeletionAddressesOriginalQuestions(t *testing.T) {
	t.Parallel()
	for _, byContent := range []bool{false, true} {
		a := newIsolatedChatTestApp(t)
		a.modelHistoryCodec = nil
		history := fastCompactFixture()
		history[8].Content = "用户要求包含引号\"以及\n换行"
		a.saveHistory("delete", history)
		if _, err := a.FastCompactSession("delete"); err != nil {
			t.Fatal(err)
		}
		req := TruncateSessionHistoryRequest{SessionID: "delete", UserMessageIndex: 2}
		if byContent {
			req.ExpectedContent = history[8].Content
		}
		if _, err := a.TruncateSessionHistory(req); err != nil {
			t.Fatal(err)
		}
		snapshot := legacySessionSnapshot("delete", a.loadSessionHistoryCopy("delete"))
		if len(snapshot.Messages) != 8 || snapshot.Messages[0]["content"] != history[0].Content || snapshot.Messages[7]["content"] != history[7].Content {
			t.Fatal("deleting an old question removed the wrong transcript range")
		}
	}
}

func TestAutoCompactRejectsCandidateWithoutMutatingOriginal(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	a.stats = nil
	history := fastCompactFixture()[:4]
	history[0].Content = strings.Repeat("requirements ", 12000)
	history = append(history, openai.ChatCompletionMessage{Role: "user", Content: "next task"})
	a.saveHistory("fallback", history)
	candidate, err := a.prepareFastCompactHistory(context.Background(), "fallback", history, 0)
	if err != nil || !candidate.messageChange || fastCompactLeavesLessThanHalf(intFromAny(candidate.result["tokensBefore"]), intFromAny(candidate.result["tokensAfter"])) {
		t.Fatal("fixture must produce an insufficient candidate")
	}
	var received []openai.ChatCompletionMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []openai.ChatCompletionMessage `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		received = req.Messages
		http.Error(w, "summary rejected", http.StatusBadRequest)
	}))
	defer server.Close()
	cfg := ConfigState{AutoCompactMethod: "recent", Model: "test", APIKey: "key", BaseURL: server.URL, APIFormat: apiFormatOpenAIChat}
	_, _, err = a.compactRunHistory(context.Background(), cfg, "fallback", compactReasonThreshold, ChatRequest{}, history, 50000)
	if err == nil || !reflect.DeepEqual(a.loadSessionHistoryCopy("fallback"), history) {
		t.Fatal("failed summary committed the rejected local candidate")
	}
	if len(received) == 0 {
		t.Fatal("summary fallback was not attempted")
	}
	// The summary consumes the original tool body, not the local metadata record.
	for _, m := range received {
		if strings.Contains(m.Content, "file body") {
			return
		}
	}
	t.Fatal("summary did not receive the original tool evidence")
}

func TestFastCompactGuardsAndCancellation(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
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

func TestAutoCompactMethodConfigDefaultsAndPersistence(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	if a.config.AutoCompactMethod != autoCompactMethodRecent {
		t.Fatal("default must be local trimming")
	}
	for _, tc := range []struct{ value, want string }{
		{"recent", "recent"}, {"summary", "summary"}, {" SUMMARY ", "summary"}, {" RECENT ", "recent"}, {"unknown", "recent"},
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
	for _, tc := range []struct{ input, want string }{
		{`{}`, "recent"}, {`{"autoCompactMethod":""}`, "recent"},
		{`{"autoCompactMethod":"summary"}`, "summary"}, {`{"autoCompactMethod":"recent"}`, "recent"},
	} {
		if err := os.WriteFile(a.configPath, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		loaded, err := readConfigFile(a.configPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := loadedConfig(loaded).AutoCompactMethod; got != tc.want {
			t.Fatalf("load %s: got %q, want %q", tc.input, got, tc.want)
		}
	}
	if got := mergeConfig(ConfigState{AutoCompactMethod: "recent"}, ConfigState{}).AutoCompactMethod; got != "recent" {
		t.Fatal("an absent request overlay must preserve the stored method")
	}
	if got := mergeConfig(ConfigState{}, ConfigState{}).AutoCompactMethod; got != "recent" {
		t.Fatal("legacy config must default to local trimming")
	}
}

func TestAutoCompactRecentFallbackFailureCircuit(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	a.stats = nil
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "summary rejected", http.StatusBadRequest)
	}))
	defer server.Close()
	cfg := ConfigState{AutoCompactMethod: "recent", Model: "test", APIKey: "key", BaseURL: server.URL, APIFormat: apiFormatOpenAIChat}
	// Even a two-message context must reach fallback; message count is not its size.
	history := []openai.ChatCompletionMessage{
		{Role: "user", Content: strings.Repeat("requirements ", 500)},
		{Role: "assistant", Content: "last answer"},
	}
	a.saveHistory("fallback-failure", history)
	for i := 0; i < maxConsecutiveCompactFailures; i++ {
		_, _, err := a.compactRunHistory(context.Background(), cfg, "fallback-failure", compactReasonThreshold, ChatRequest{}, history, 50000)
		if err == nil || isCompactSkip(err) || a.compactFailureCount("fallback-failure") != i+1 {
			t.Fatalf("fallback failure did not accumulate: attempt=%d err=%v", i, err)
		}
	}
	before := requests.Load()
	_, _, err := a.compactRunHistory(context.Background(), cfg, "fallback-failure", compactReasonThreshold, ChatRequest{}, history, 50000)
	if !errors.Is(err, errAutoCompactCircuitOpen) || requests.Load() != before || before == 0 {
		t.Fatalf("fallback summary bypassed the failure circuit: %v", err)
	}
	if !reflect.DeepEqual(a.loadSessionHistoryCopy("fallback-failure"), history) {
		t.Fatal("failed fallback changed the retained history")
	}
}

func TestAutoCompactRecentOverflowStillUsesSummary(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
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
