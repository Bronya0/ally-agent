// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ally-dev/internal/chatgpt"
	jose "github.com/go-jose/go-jose/v4"
	openai "github.com/sashabaranov/go-openai"
)

func TestChatGPTConfigSessionAndKeyIsolation(t *testing.T) {
	entry := ModelConfig{ProviderName: "ChatGPT", Model: "subscription-model", AuthType: authChatGPTOAuth, OAuthAccountID: "account-one", APIKey: "must-not-be-used", APIKeys: []string{"paid-key"}, BaseURL: "https://evil.test", APIFormat: apiFormatAnthropicMessages, CustomHeaders: map[string]string{"Authorization": "override"}}
	cfg := ConfigState{}
	applyModelEntry(&cfg, entry)
	if cfg.ProviderName != chatGPTProviderName || cfg.ContextWindow != chatGPTContextWindow {
		t.Fatalf("unexpected OAuth defaults: %+v", cfg)
	}
	if !hasModelCredential(cfg) || cfg.APIKey != "" || len(cfg.APIKeys) > 0 || cfg.CustomHeaders != nil || cfg.BaseURL != chatgpt.Resource || cfg.APIFormat != apiFormatOpenAIResponses {
		t.Fatalf("unsafe OAuth config: %+v", cfg)
	}
	frozen := sessionModelConfigFrom(cfg).apply(ConfigState{})
	if frozen.AuthType != authChatGPTOAuth || frozen.OAuthAccountID != "account-one" {
		t.Fatal("session lost OAuth identity")
	}
	base := ConfigState{Models: []ModelConfig{entry}, LastUsedModel: &ModelIdentity{ProviderName: "ChatGPT", Model: entry.Model}}
	persisted := persistableConfig(base)
	if persisted.AuthType != "" || persisted.OAuthAccountID != "" || persisted.Models[0].APIKey != "" {
		t.Fatal("unsafe persistable config")
	}
	resolved := expandLastUsedModel(persisted)
	if resolved.OAuthAccountID != "account-one" || resolved.AuthType != authChatGPTOAuth {
		t.Fatal("restart lost model authentication")
	}
	apiKey := mergeConfig(resolved, ConfigState{AuthType: "api_key", Model: "api-model", APIKey: "old-key", APIKeys: []string{"old-key"}, APIFormat: apiFormatOpenAIChat, BaseURL: "https://api.example.test"})
	if apiKey.AuthType != "api_key" || apiKey.OAuthAccountID != "" || apiKey.APIKey != "old-key" {
		t.Fatal("API key regression")
	}
	other := cfg
	other.OAuthAccountID = "account-two"
	if reasoningReplayKey(cfg, cfg.Model) == reasoningReplayKey(other, other.Model) {
		t.Fatal("encrypted reasoning shared between accounts")
	}
}

func TestChatGPTNeverFallsBackToPaidKeys(t *testing.T) {
	a := newApiTestApp(t)
	called := false
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer s.Close()
	_, err := a.streamModelResponse(context.Background(), ConfigState{AuthType: authChatGPTOAuth, OAuthAccountID: "signed-out", APIKeys: []string{"paid-one", "paid-two"}, APIKey: "paid-one", BaseURL: s.URL, noAdapterRetry: true}, "model", nil, nil, nil)
	if err == nil || called || shouldRetryLLMError(err) {
		t.Fatalf("OAuth failed open or would retry sign-out: %v called=%v", err, called)
	}
}

func TestChatGPTFixedProviderPreservesAccountIdentity(t *testing.T) {
	models := []ModelConfig{
		{ProviderName: "ChatGPT · one", AuthType: authChatGPTOAuth, OAuthAccountID: "one", Model: "same-model"},
		{ProviderName: "ChatGPT · two", AuthType: authChatGPTOAuth, OAuthAccountID: "two", Model: "same-model", ContextWindow: 256000},
	}
	cfg := persistableConfig(ConfigState{Models: models, LastUsedModel: &ModelIdentity{ProviderName: "ChatGPT · two", Model: "same-model"}})
	if cfg.LastUsedModel == nil || cfg.LastUsedModel.OAuthAccountID != "two" || cfg.LastUsedModel.ProviderName != chatGPTProviderName {
		t.Fatalf("legacy identity changed accounts: %+v", cfg.LastUsedModel)
	}
	resolved := expandLastUsedModel(cfg)
	if resolved.OAuthAccountID != "two" || resolved.ContextWindow != 256000 {
		t.Fatalf("explicit context/account lost: %+v", resolved)
	}
	if index := modelIndexByIdentity(cfg.Models, ModelIdentity{ProviderName: chatGPTProviderName, Model: "same-model", OAuthAccountID: "missing"}); index != -1 {
		t.Fatal("missing OAuth account fell back to another account")
	}
}

func TestChatGPTPreviewRequest(t *testing.T) {
	cfg := ConfigState{AuthType: authChatGPTOAuth, BaseURL: chatgpt.Resource, APIFormat: apiFormatOpenAIResponses, MaxTokens: 1000, ReasoningEffort: "high"}
	messages := []openai.ChatCompletionMessage{{Role: "system", Content: "agent rules"}, {Role: "user", Content: "question"}, {Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "call-one", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"file"}`}}}}, {Role: "tool", ToolCallID: "call-one", Content: "file contents"}}
	tools := []openai.Tool{{Type: "function", Function: &openai.FunctionDefinition{Name: "read", Parameters: map[string]any{"type": "object"}}}}
	body := marshalResponsesRequest(t, buildOpenAIResponsesRequest(cfg, "account-model", messages, tools, nil))
	for _, key := range []string{"temperature", "top_p", "max_output_tokens", "prompt_cache_retention", "previous_response_id", "background", "conversation", "max_tool_calls", "metadata", "moderation", "multi_agent", "prompt", "safety_identifier", "top_logprobs", "truncation", "user"} {
		body[key] = "forbidden"
	}
	if err := chatGPTResponsesRequest(body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"temperature", "top_p", "max_output_tokens", "prompt_cache_retention", "previous_response_id", "background", "conversation", "max_tool_calls", "metadata", "moderation", "multi_agent", "prompt", "safety_identifier", "top_logprobs", "truncation", "user"} {
		if _, ok := body[key]; ok {
			t.Errorf("unsupported %s retained", key)
		}
	}
	if body["store"] != false || body["stream"] != true || body["instructions"] != "agent rules" {
		t.Fatal(body)
	}
	ns := body["tools"].([]any)[0].(map[string]any)
	if ns["type"] != "namespace" || ns["name"] != "ally" || len(ns["tools"].([]any)) != 1 {
		t.Fatal(ns)
	}
	items := body["input"].([]any)
	if len(items) != 3 || items[1].(map[string]any)["namespace"] != "ally" || items[2].(map[string]any)["output"] != "file contents" {
		t.Fatal(items)
	}
	if supportsOpenAIResponsesImageGeneration(cfg) {
		t.Fatal("hosted image tool enabled")
	}
	if err := chatGPTResponsesRequest(map[string]any{"input": []any{}, "tools": []any{map[string]any{"type": "tool_search"}}}); err == nil {
		t.Fatal("hosted tool allowed")
	}
}

type oauthTestProtector struct{ cipher.AEAD }

func (p oauthTestProtector) Seal(b []byte) ([]byte, error) {
	nonce := make([]byte, p.NonceSize())
	_, _ = rand.Read(nonce)
	return p.AEAD.Seal(nonce, nonce, b, nil), nil
}
func (p oauthTestProtector) Open(b []byte) ([]byte, error) {
	return p.AEAD.Open(nil, b[:p.NonceSize()], b[p.NonceSize():], nil)
}

type oauthMockTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (rt oauthMockTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "auth.openai.com" && r.URL.Host != "api.openai.com" {
		return nil, errors.New("unexpected test network destination")
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = rt.target.Scheme
	u.Host = rt.target.Host
	copy.URL = &u
	copy.Host = rt.target.Host
	resp, err := rt.base.RoundTrip(copy)
	if resp != nil {
		resp.Request = r
	}
	return resp, err
}

// Exercise the actual OAuth credential provider and existing SSE decoder over
// two complete request/response turns, including a function result replay.
func TestChatGPTAuthenticatedSSEToolLoopAndErrors(t *testing.T) {
	a := newApiTestApp(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	nonce := ""
	streamFailure := ""
	status := 200
	turn := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": chatgpt.Issuer, "jwks_uri": chatgpt.Issuer + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, Algorithm: "RS256", Use: "sig"}}})
		case "/api/accounts/oauth/token":
			signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
			claims, _ := json.Marshal(map[string]any{"iss": chatgpt.Issuer, "aud": "oaiapp_test", "sub": "user-test", "nonce": nonce, "exp": time.Now().Add(time.Hour).Unix()})
			signed, _ := signer.Sign(claims)
			token, _ := signed.CompactSerialize()
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "secret-oauth-access", "refresh_token": "secret-refresh", "id_token": token, "expires_in": 3600, "token_type": "Bearer", "scope": "openid resource.invoke chatgpt.tokens.use.direct"})
		case "/v1/responses":
			if r.Header.Get("Authorization") != "Bearer secret-oauth-access" {
				t.Error("OAuth bearer missing")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["store"] != false || body["stream"] != true {
				t.Error("invalid subscription request")
			}
			if _, ok := body["max_output_tokens"]; ok {
				t.Error("unsupported field sent")
			}
			w.Header().Set("x-request-id", "test-request")
			if status != 200 {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"test_error","message":"secret-oauth-access"}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if streamFailure != "" {
				_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
				event, _ := json.Marshal(map[string]any{"type": "response.failed", "response": map[string]any{"error": map[string]any{"code": streamFailure, "message": "secret-oauth-access", "param": "tools"}}})
				_, _ = io.WriteString(w, "data: "+string(event)+"\n\n")
				return
			}
			if turn == 0 {
				_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_one\",\"call_id\":\"call_one\",\"namespace\":\"ally\",\"name\":\"read\",\"arguments\":\"\"}}\n\n")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"item_id\":\"fc_one\",\"arguments\":\"{\\\"path\\\":\\\"file\\\"}\"}\n\n")
			} else {
				input := body["input"].([]any)
				last := input[len(input)-1].(map[string]any)
				if last["type"] != "function_call_output" || last["output"] != "file contents" {
					t.Error("tool result missing")
				}
				_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\n")
			}
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_one\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1000,\"input_tokens_details\":{\"cached_tokens\":800},\"output_tokens\":8}}}\n\n")
			turn++
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	client := &http.Client{Transport: oauthMockTransport{server.Client().Transport, target}, Timeout: 5 * time.Second}
	block, _ := aes.NewCipher(make([]byte, 32))
	gcm, _ := cipher.NewGCM(block)
	m := chatgpt.New(filepath.Join(t.TempDir(), "oauth"), oauthTestProtector{gcm})
	defer m.Close()
	account, err := m.Login(context.Background(), client, "", false, func(raw string) error {
		u, _ := url.Parse(raw)
		q := u.Query()
		nonce = q.Get("nonce")
		resp, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{"code": {"code"}, "state": {q.Get("state")}, "client_id": {"oaiapp_test"}}.Encode())
		if resp != nil {
			resp.Body.Close()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	a.chatGPT = m
	cfg := ConfigState{AuthType: authChatGPTOAuth, OAuthAccountID: account.ID, BaseURL: chatgpt.Resource, APIFormat: apiFormatOpenAIResponses, chatGPTManager: m, MaxTokens: 1000}
	tools := []openai.Tool{{Type: "function", Function: &openai.FunctionDefinition{Name: "read", Parameters: map[string]any{"type": "object"}}}}
	messages := []openai.ChatCompletionMessage{{Role: "system", Content: "rules"}, {Role: "user", Content: "read a file"}}
	for n := 0; n < 2; n++ {
		body := buildOpenAIResponsesRequest(cfg, "account-model", messages, tools, nil)
		stream, err := newOpenAIResponsesSSEStreamWithClient(context.Background(), cfg, body, client)
		if err != nil {
			t.Fatal(err)
		}
		result, _, err := a.consumeOpenAIResponsesStream(cfg, body, stream, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Usage == nil || result.Usage.PromptTokens != 1000 || result.Usage.CacheHitTokens != 800 || result.Usage.CacheMissTokens != 200 {
			t.Fatalf("OAuth SSE lost cached input: %+v", result.Usage)
		}
		calls, content := result.ToolCalls, result.Content
		if n == 0 {
			if len(calls) != 1 || calls[0].Function.Name != "read" {
				t.Fatal(calls)
			}
			messages = append(messages, openai.ChatCompletionMessage{Role: "assistant", ToolCalls: calls}, openai.ChatCompletionMessage{Role: "tool", ToolCallID: calls[0].ID, Content: "file contents"})
		} else if content != "done" {
			t.Fatal(content)
		}
	}
	for _, code := range []int{401, 403, 429, 503} {
		status = code
		_, err := newOpenAIResponsesSSEStreamWithClient(context.Background(), cfg, buildOpenAIResponsesRequest(cfg, "account-model", messages, tools, nil), client)
		if err == nil || strings.Contains(err.Error(), "secret-oauth-access") || !strings.Contains(err.Error(), "test-request") {
			t.Fatalf("unsafe error %v", err)
		}
	}
	status = 200
	for _, code := range []string{"subscription_sharing_usage_limit_exceeded", "subscription_sharing_usage_unavailable", "subscription_sharing_user_not_eligible"} {
		streamFailure = code
		body := buildOpenAIResponsesRequest(cfg, "account-model", messages, tools, nil)
		stream, err := newOpenAIResponsesSSEStreamWithClient(context.Background(), cfg, body, client)
		if err != nil {
			t.Fatal(err)
		}
		_, emitted, err := a.consumeOpenAIResponsesStream(cfg, body, stream, nil)
		if err == nil || !emitted || !strings.Contains(err.Error(), code) || !strings.Contains(err.Error(), "test-request") || strings.Contains(err.Error(), "secret-oauth-access") {
			t.Fatalf("stream failure lost/redaction failed: %v", err)
		}
		if shouldRetryLLMError(err) != (code == "subscription_sharing_usage_unavailable") {
			t.Fatalf("wrong recovery for %s", code)
		}
	}
	streamFailure = ""
	if shouldRetryLLMError(errors.New("subscription_sharing_usage_limit_exceeded")) {
		t.Fatal("usage limit would retry")
	}
	m.RecordError(account.ID, errors.New("subscription_sharing_usage_limit_exceeded"))
	status = 200
	if _, err := newOpenAIResponsesSSEStreamWithClient(context.Background(), cfg, buildOpenAIResponsesRequest(cfg, "account-model", messages, tools, nil), client); err == nil {
		t.Fatal("new request not paused after cap")
	}
}
