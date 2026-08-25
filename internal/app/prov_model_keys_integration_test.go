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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// sseChatChunk 构造一个 OpenAI 兼容的流式 chunk(data: {...}\n\n)。
func sseChatChunk(content string) string {
	return fmt.Sprintf(`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":%q},"finish_reason":null}]}`+"\n\n", content)
}

const sseDone = "data: [DONE]\n\n"

// TestStreamModelResponseMultiKeyFailover 验证核心故障转移路径:第一个 key
// 返回 401,请求切换到第二个 key 成功;失败 key 进入冷却;retry 事件携带
// key 序号与池大小;请求顺序为 bad-key → good-key。
func TestStreamModelResponseMultiKeyFailover(t *testing.T) {
	var mu sync.Mutex
	order := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		order = append(order, auth)
		mu.Unlock()
		if strings.Contains(auth, "bad-key") {
			http.Error(w, `{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("hi from k2"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   server.URL,
		APIKeys:   []string{"bad-key", "good-key"},
		MaxTokens: 32,
	}
	var retries []*modelRetryInfo
	onEvent := func(e modelStreamEvent) {
		if e.Retry != nil {
			retries = append(retries, e.Retry)
		}
	}
	result, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, onEvent)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v", err)
	}
	if !strings.Contains(result.Content, "hi from k2") {
		t.Fatalf("result content = %q, want to contain %q", result.Content, "hi from k2")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 {
		t.Fatalf("request order = %v, want [bad-key good-key]", order)
	}
	if !strings.Contains(order[0], "bad-key") || !strings.Contains(order[1], "good-key") {
		t.Fatalf("request order = %v, want bad-key then good-key", order)
	}
	if !a.isKeyCoolingDown(cfg, "bad-key") {
		t.Fatal("bad-key should be cooling down after 401")
	}
	if a.isKeyCoolingDown(cfg, "good-key") {
		t.Fatal("good-key should not be cooling down")
	}
	if len(retries) != 1 || retries[0].KeyIndex != 0 || retries[0].TotalKeys != 2 {
		t.Fatalf("retry events = %+v, want 1 event keyIndex=0 totalKeys=2", retries)
	}
}

// TestStreamModelResponseMultiKeyAllFail 验证全部 key 失败时返回最后一个
// 错误、两个 key 都进入冷却、每个失败都发出 retry 事件。
func TestStreamModelResponseMultiKeyAllFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   server.URL,
		APIKeys:   []string{"k1", "k2"},
		MaxTokens: 32,
	}
	var retries []*modelRetryInfo
	onEvent := func(e modelStreamEvent) {
		if e.Retry != nil {
			retries = append(retries, e.Retry)
		}
	}
	_, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, onEvent)
	if err == nil {
		t.Fatal("expected error when all keys fail")
	}
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("error = %v, want to contain %q", err, "invalid api key")
	}
	if !a.isKeyCoolingDown(cfg, "k1") || !a.isKeyCoolingDown(cfg, "k2") {
		t.Fatal("both keys should be cooling down after failures")
	}
	if len(retries) != 2 {
		t.Fatalf("retry events = %d, want 2", len(retries))
	}
}

// TestStreamModelResponseMultiKeySkipsCoolingKey 验证已冷却的主 key 被跳过,
// 请求直接使用下一个可用 key(不会重复命中失败 key)。
func TestStreamModelResponseMultiKeySkipsCoolingKey(t *testing.T) {
	var mu sync.Mutex
	order := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		order = append(order, auth)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("ok"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   server.URL,
		APIKeys:   []string{"k1", "k2"},
		MaxTokens: 32,
	}
	a.recordKeyFailure(cfg, "k1", keyAuthCooldownDuration)
	_, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 || !strings.Contains(order[0], "k2") {
		t.Fatalf("request order = %v, want only k2", order)
	}
}

// TestStreamModelResponseAllCoolingProbesAndSucceeds 验证全部 key 冷却时
// 不做无期限硬拒绝:仍用最早到期的 key 探测一次,服务端已恢复则请求成功
// (回归:错误分类曾把限流 429 误判为配额失效,冷却 30 分钟内所有请求
// 立刻失败,只能重启恢复)。
func TestStreamModelResponseAllCoolingProbesAndSucceeds(t *testing.T) {
	var mu sync.Mutex
	order := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		order = append(order, auth)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("recovered"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   server.URL,
		APIKeys:   []string{"k1", "k2"},
		MaxTokens: 32,
	}
	a.recordKeyFailure(cfg, "k1", keyAuthCooldownDuration)
	a.recordKeyFailure(cfg, "k2", keyAuthCooldownDuration)
	result, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v, want probe to succeed", err)
	}
	if !strings.Contains(result.Content, "recovered") {
		t.Fatalf("result content = %q, want to contain %q", result.Content, "recovered")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 {
		t.Fatalf("request order = %v, want exactly one probe request", order)
	}
}

// TestStreamModelResponseAllCoolingProbeReturnsRealError 验证全部 key 冷却且
// 探测仍失败时,返回真实的服务端错误(而非泛化的 "all API keys are cooling
// down"),且探测有界:每次调用只发一次请求。
func TestStreamModelResponseAllCoolingProbeReturnsRealError(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		http.Error(w, `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","code":"429"}}`, http.StatusTooManyRequests)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   server.URL,
		APIKeys:   []string{"k1", "k2"},
		MaxTokens: 32,
	}
	a.recordKeyFailure(cfg, "k1", keyAuthCooldownDuration)
	a.recordKeyFailure(cfg, "k2", keyAuthCooldownDuration)
	_, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err == nil {
		t.Fatal("expected error when probe still fails")
	}
	if strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("error = %v, want real server error instead of synthetic cooling-down message", err)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %v, want to contain real 429", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("requests = %d, want exactly one bounded probe", requests)
	}
}

// TestStreamModelResponseRateLimitUsesTransientCooldown 验证阿里云式 429
// 限流文案(含 quota)只触发短冷却:第一轮全部 key 失败后,冷却窗口内
// 的下一次调用仍能通过探测拿到真实结果,而不是被 30 分钟冷却锁死。
func TestStreamModelResponseRateLimitUsesTransientCooldown(t *testing.T) {
	var mu sync.Mutex
	order := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		order = append(order, auth)
		mu.Unlock()
		if len(order) == 1 {
			http.Error(w, `{"error":{"message":"You exceeded your current quota, please check your plan and billing details. For details, see https://help.aliyun.com/zh/model-studio/error-code#token-limit"}}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("ok"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   server.URL,
		APIKeys:   []string{"k1", "k2"},
		MaxTokens: 32,
	}
	// 第一轮:k1 429(限流)→ 短冷却并切换 k2 成功。
	_, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("first streamModelResponse() error = %v", err)
	}
	if a.isKeyCoolingDown(cfg, "k1") {
		a.keyStateMu.Lock()
		remaining := time.Until(a.keyCooldowns[keyCooldownID(cfg, "k1")])
		a.keyStateMu.Unlock()
		if remaining > keyTransientCooldownDuration {
			t.Fatalf("k1 cooldown remaining = %v, want <= transient %v (429 must not trigger 30min auth cooldown)", remaining, keyTransientCooldownDuration)
		}
	}
	// 冷却窗口内再次调用:k1 仍被跳过,继续用 k2(不依赖探测)。
	_, err = a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "again"}}, nil, nil)
	if err != nil {
		t.Fatalf("second streamModelResponse() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || !strings.Contains(order[2], "k2") {
		t.Fatalf("request order = %v, want third request on k2", order)
	}
}

// TestStreamModelResponseNoSwitchAfterEmit 验证已发出流内容后失败不再切换
// key(避免重复输出):第一个 key 发出一个 delta 后连接出错,请求直接失败,
// 不会用第二个 key 重试。
func TestStreamModelResponseNoSwitchAfterEmit(t *testing.T) {
	var mu sync.Mutex
	order := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		order = append(order, auth)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// 先发一个合法 delta,再发字段类型错误的 chunk 触发解码失败。
		fmt.Fprint(w, sseChatChunk("partial"))
		fmt.Fprint(w, `data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":123},"finish_reason":null}]}`+"\n\n")
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   server.URL,
		APIKeys:   []string{"k1", "k2"},
		MaxTokens: 32,
	}
	var emitted []string
	onEvent := func(e modelStreamEvent) {
		if e.ContentDelta != "" {
			emitted = append(emitted, e.ContentDelta)
		}
	}
	_, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, onEvent)
	if err == nil {
		t.Fatal("expected decode error from malformed chunk")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 {
		t.Fatalf("request order = %v, want only one request (no switch after emit)", order)
	}
	if len(emitted) != 1 || emitted[0] != "partial" {
		t.Fatalf("emitted deltas = %v, want [partial]", emitted)
	}
}

// TestStreamModelResponseSingleKeyNoFailover 验证单 key 路径完全不触发切换:
// 401 直接失败,不发 retry 事件,请求只发生一次(与旧行为一致)。
func TestStreamModelResponseSingleKeyNoFailover(t *testing.T) {
	var mu sync.Mutex
	order := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		order = append(order, auth)
		mu.Unlock()
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat:  apiFormatOpenAIChat,
		BaseURL:    server.URL,
		APIKeys:    []string{"only"},
		MaxTokens:  32,
		LLMRetries: 0,
	}
	retries := 0
	onEvent := func(e modelStreamEvent) {
		if e.Retry != nil {
			retries++
		}
	}
	_, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, onEvent)
	if err == nil {
		t.Fatal("expected error for single bad key")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 {
		t.Fatalf("request order = %v, want single request", order)
	}
	if retries != 0 {
		t.Fatalf("retry events = %d, want 0 for single key", retries)
	}
}

// TestStreamOpenAIChatPreservesMaxReasoningEffort verifies that the selected
// max level reaches the OpenAI-compatible request body unchanged.
func TestStreamOpenAIChatPreservesMaxReasoningEffort(t *testing.T) {
	var request map[string]any
	var decodeErr error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeErr = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("ok"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat:       apiFormatOpenAIChat,
		BaseURL:         server.URL,
		APIKeys:         []string{"test-key"},
		MaxTokens:       32,
		ReasoningEffort: reasoningEffortMax,
	}
	_, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v", err)
	}
	if decodeErr != nil {
		t.Fatalf("decode request body: %v", decodeErr)
	}
	if got, _ := request["reasoning_effort"].(string); got != reasoningEffortMax {
			t.Fatalf("request reasoning_effort = %v, want %q", request["reasoning_effort"], reasoningEffortMax)
		}
}

// TestAnthropicToolsSchemaTypeObject verifies every tool sent on the
// anthropic_messages path serializes input_schema with a concrete
// "type":"object" — a missing type makes gateways/proxies reject the whole
// request with 400 "tools.N.custom.input_schema.type: Field required".
func TestAnthropicToolsSchemaTypeObject(t *testing.T) {
	tools := convertToolsToAnthropic(chatTools())
	if len(tools) == 0 {
		t.Fatal("chatTools() returned no tools")
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for i, tool := range decoded {
		toolObj, ok := tool["tool"].(map[string]any)
		if !ok {
			toolObj = tool
		}
		schema, ok := toolObj["input_schema"].(map[string]any)
		if !ok {
			t.Fatalf("tool[%d] missing input_schema", i)
		}
		if typ, _ := schema["type"].(string); typ != "object" {
			t.Fatalf("tool[%d] input_schema.type = %q, want object", i, typ)
		}
	}
}

// TestOpenAIChatToolsParametersTypeObject verifies the openai chat path keeps a
// top-level "type":"object" on every tool's parameters, so proxies that
// convert chat->anthropic do not reject the request the same way.
func TestOpenAIChatToolsParametersTypeObject(t *testing.T) {
	raw, err := json.Marshal(chatTools())
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for i, tool := range decoded {
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			t.Fatalf("tool[%d] missing function", i)
		}
		params, ok := fn["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("tool[%d] missing parameters", i)
		}
		if typ, _ := params["type"].(string); typ != "object" {
			t.Fatalf("tool[%d] parameters.type = %q, want object", i, typ)
		}
	}
}

// TestStreamModelResponseMultiKeyRetryBudgetFromSetting 验证多 key 路径的
// 重试预算统一取自 LLMRetries 设置(单一来源):重试事件的 MaxAttempts 是
// 设置值而非 key 池大小(回归:曾按 min(len(keys), 8) 截断,配 6 重试的
// 用户只看到 key 数量决定的重试次数)。
func TestStreamModelResponseMultiKeyRetryBudgetFromSetting(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n <= 2 {
			http.Error(w, `{"error":{"message":"service overloaded"}}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseChatChunk("recovered"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat:  apiFormatOpenAIChat,
		BaseURL:    server.URL,
		APIKeys:    []string{"k1", "k2"},
		MaxTokens:  32,
		LLMRetries: 3,
	}
	var retries []*modelRetryInfo
	onEvent := func(e modelStreamEvent) {
		if e.Retry != nil {
			retries = append(retries, e.Retry)
		}
	}
	result, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, onEvent)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v", err)
	}
	if !strings.Contains(result.Content, "recovered") {
		t.Fatalf("result content = %q, want to contain %q", result.Content, "recovered")
	}
	if len(retries) != 2 {
		t.Fatalf("retry events = %d, want 2", len(retries))
	}
	for i, r := range retries {
		if r.MaxAttempts != 3 {
			t.Fatalf("retry[%d].MaxAttempts = %d, want 3 (from LLMRetries setting, not key count)", i, r.MaxAttempts)
		}
		if r.Attempt != i+1 {
			t.Fatalf("retry[%d].Attempt = %d, want %d", i, r.Attempt, i+1)
		}
	}
}

// TestStreamModelResponseMultiKeyRetryBudgetExceedsKeyCount 验证重试预算
// 可以超过 key 池大小:全部 key 失败进入短冷却后,等待冷却到期继续用满
// 预算,而不是探测一次就放弃(此前总尝试次数被 key 池大小截断)。
// 全程瞬时 503 + LLMRetries=2 + 2 个 key:首次 + 2 次重试 + 1 次探测 = 4 次请求。
func TestStreamModelResponseMultiKeyRetryBudgetExceedsKeyCount(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for key cooldown expiry (~10s)")
	}
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		http.Error(w, `{"error":{"message":"service overloaded"}}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()

	a := NewApp()
	cfg := ConfigState{
		APIFormat:  apiFormatOpenAIChat,
		BaseURL:    server.URL,
		APIKeys:    []string{"k1", "k2"},
		MaxTokens:  32,
		LLMRetries: 2,
	}
	var retries []*modelRetryInfo
	onEvent := func(e modelStreamEvent) {
		if e.Retry != nil {
			retries = append(retries, e.Retry)
		}
	}
	_, err := a.streamModelResponse(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}, nil, onEvent)
	if err == nil {
		t.Fatal("expected error when endpoint is persistently down")
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 4 {
		t.Fatalf("requests = %d, want 4 (first + 2 retries + 1 probe; budget must exceed key count)", requests)
	}
	if len(retries) != 2 {
		t.Fatalf("retry events = %d, want 2 (probe failures do not emit retry events)", len(retries))
	}
}
