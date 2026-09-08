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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestNormalizeCustomHeaders(t *testing.T) {
	got := normalizeCustomHeaders(map[string]string{
		"  x-api-version ": " 2023-06-01 ",
		"Empty-Value":      "   ",
		"":                 "value",
		"Host":             "evil.example",
		"content-length":   "999",
		"X-Title":          "Ally",
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 surviving headers, got %#v", got)
	}
	if got["X-Api-Version"] != "2023-06-01" {
		t.Fatalf("key/value trimming + canonicalization failed: %#v", got)
	}
	if got["X-Title"] != "Ally" {
		t.Fatalf("plain header lost: %#v", got)
	}
	if _, ok := got["Host"]; ok {
		t.Fatal("transport-managed Host header must be dropped")
	}
	if _, ok := got["Content-Length"]; ok {
		t.Fatal("transport-managed Content-Length header must be dropped")
	}
}

func TestNormalizeCustomHeadersDedupesCaseVariantsDeterministically(t *testing.T) {
	got := normalizeCustomHeaders(map[string]string{
		"X-Custom": "from-lowercase-key",
		"x-custom": "from-canonical-key",
	})
	if len(got) != 1 {
		t.Fatalf("case-variant keys must collapse to one entry, got %#v", got)
	}
	// 字典序首个（X-Custom）胜出，行为确定。
	if got["X-Custom"] != "from-lowercase-key" {
		t.Fatalf("unexpected winner for case-variant keys: %#v", got)
	}
}

func TestNormalizeCustomHeadersEmptyReturnsNil(t *testing.T) {
	if got := normalizeCustomHeaders(map[string]string{"A": " ", "B": ""}); got != nil {
		t.Fatalf("all-empty input must normalize to nil, got %#v", got)
	}
	if got := normalizeCustomHeaders(nil); got != nil {
		t.Fatalf("nil input must normalize to nil, got %#v", got)
	}
}

func TestMergeConfigCustomHeaders(t *testing.T) {
	base := ConfigState{CustomHeaders: map[string]string{"X-Keep": "yes"}}
	// nil overlay = 字段缺席，保留原值（旧前端整份回传不丢配置）。
	got := mergeConfig(base, ConfigState{})
	if got.CustomHeaders["X-Keep"] != "yes" {
		t.Fatalf("nil overlay must keep existing headers, got %#v", got.CustomHeaders)
	}
	// 非 nil overlay 整体替换。
	got = mergeConfig(base, ConfigState{CustomHeaders: map[string]string{"X-New": "v", "": "drop"}})
	if len(got.CustomHeaders) != 1 || got.CustomHeaders["X-New"] != "v" {
		t.Fatalf("non-nil overlay must replace headers, got %#v", got.CustomHeaders)
	}
	// 显式空 map 清空。
	got = mergeConfig(base, ConfigState{CustomHeaders: map[string]string{}})
	if got.CustomHeaders != nil {
		t.Fatalf("explicit empty overlay must clear headers, got %#v", got.CustomHeaders)
	}
	// 模型条目同步归一化。
	got = mergeConfig(ConfigState{}, ConfigState{Models: []ModelConfig{{
		Model:         "m",
		CustomHeaders: map[string]string{"Host": "drop", "X-Relay": "token"},
	}}})
	if len(got.Models) != 1 || len(got.Models[0].CustomHeaders) != 1 || got.Models[0].CustomHeaders["X-Relay"] != "token" {
		t.Fatalf("model entry headers must be normalized on merge, got %#v", got.Models)
	}
}

// TestModelRequestCarriesCustomHeaders 验证自定义头经完整适配器链路注入：
// httptest 服务器收到 OpenAI Chat 流式请求时必须看到配置头，且能覆盖
// Authorization（网关自定义鉴权场景）。go-openai 客户端走 modelHTTPClient
// 的 customHeadersTransport，与生产路径一致。
func TestModelRequestCarriesCustomHeaders(t *testing.T) {
	var mu sync.Mutex
	var sawAuth, sawRelay, sawVersion bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawAuth = r.Header.Get("Authorization") == "Gateway-Token"
		sawRelay = r.Header.Get("X-Relay-Route") == "premium"
		sawVersion = r.Header.Get("x-api-version") == "2023-06-01"
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseChatChunk("ok"))
		fmt.Fprint(w, sseChatFinishChunk("stop"))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	result, err := app.streamModelResponse(
		context.Background(),
		ConfigState{
			APIFormat:      apiFormatOpenAIChat,
			BaseURL:        server.URL,
			APIKeys:        []string{"test-key"},
			Model:          "test-model",
			MaxTokens:      64,
			CustomHeaders:  map[string]string{"Authorization": "Gateway-Token", "X-Relay-Route": "premium", "x-api-version": "2023-06-01"},
		},
		"test-model", nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v", err)
	}
	if strings.TrimSpace(result.Content) != "ok" {
		t.Fatalf("unexpected stream content: %q", result.Content)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawAuth || !sawRelay || !sawVersion {
		t.Fatalf("custom headers missing on model request: auth=%v relay=%v version=%v", sawAuth, sawRelay, sawVersion)
	}
}

// TestResponsesSSEStreamCarriesCustomHeaders 覆盖不走 SDK 客户端的
// Responses SSE 路径（newOpenAIResponsesSSEStream + applyCustomHeaders）。
func TestResponsesSSEStreamCarriesCustomHeaders(t *testing.T) {
	var mu sync.Mutex
	var sawHeader bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawHeader = r.Header.Get("X-Gateway-Auth") == "secret-token"
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseResponsesEvent(`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":1,"output_tokens":1}}}`))
		fmt.Fprint(w, sseDone)
	}))
	defer server.Close()

	cfg := ConfigState{
		APIFormat:     apiFormatOpenAIResponses,
		BaseURL:       server.URL,
		APIKey:        "test-key",
		CustomHeaders: map[string]string{"X-Gateway-Auth": "secret-token"},
	}
	endpoint := strings.TrimRight(baseURLForAPIFormat(cfg), "/") + "/responses"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, strings.NewReader(`{"model":"m","stream":true}`))
	if err != nil {
		t.Fatalf("probe request build failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	applyCustomHeaders(req, cfg)
	resp, err := proxyHTTPClient(cfg, true, 0).Do(req)
	if err != nil {
		t.Fatalf("probe request failed: %v", err)
	}
	defer resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if !sawHeader {
		t.Fatal("Responses SSE request must carry the configured custom header")
	}
}

// TestSwitchModelMirrorsCustomHeaders 验证激活模型条目时顶层镜像同步。
func TestSwitchModelMirrorsCustomHeaders(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.configPath = t.TempDir() + "/config.json"
	app.config = ConfigState{
		Models: []ModelConfig{{
			Model:         "relay-model",
			CustomHeaders: map[string]string{"X-Relay": "token", "Host": "drop"},
		}},
	}
	if err := app.SwitchModel(0); err != nil {
		t.Fatalf("SwitchModel() error = %v", err)
	}
	if app.config.CustomHeaders["X-Relay"] != "token" {
		t.Fatalf("SwitchModel must mirror entry headers to top level, got %#v", app.config.CustomHeaders)
	}
	if len(app.config.CustomHeaders) != 1 {
		t.Fatalf("mirrored headers must be normalized, got %#v", app.config.CustomHeaders)
	}
}

func TestFetchModelListCarriesCustomHeaders(t *testing.T) {
	var mu sync.Mutex
	var sawHeader bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawHeader = r.Header.Get("X-Gate") == "open"
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"m-1"},{"id":"m-2"}]}`)
	}))
	defer server.Close()

	app := NewApp()
	app.initialized = true
	models, err := app.FetchModelList(server.URL, "k", map[string]string{"X-Gate": "open"})
	if err != nil {
		t.Fatalf("FetchModelList() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("unexpected model list: %#v", models)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawHeader {
		t.Fatal("FetchModelList must send configured custom headers")
	}
}

func TestCustomHeaderNamesRedacted(t *testing.T) {
	names := customHeaderNames(map[string]string{"Authorization": "secret", "X-Ok": "v"})
	if len(names) != 2 || names[0] != "Authorization" || names[1] != "X-Ok" {
		t.Fatalf("unexpected names: %#v", names)
	}
	if got := customHeaderNames(nil); len(got) != 0 {
		t.Fatalf("nil headers must yield empty names, got %#v", got)
	}
}
