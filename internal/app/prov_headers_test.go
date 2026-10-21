// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	legacyopenai "github.com/sashabaranov/go-openai"
)

func TestModelUserAgentAcrossProtocols(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("ALLY_DATA_DIR", dir)
	for _, format := range []string{apiFormatOpenAIChat, apiFormatOpenAIResponses, apiFormatAnthropicMessages} {
		for _, tc := range []struct {
			name    string
			global  string
			headers map[string]string
			want    string
		}{
			{name: "default", want: defaultHTTPUA},
			{name: "selected", global: "  selected-agent/1.0  ", want: "selected-agent/1.0"},
			{name: "model-override", global: "selected-agent/1.0", headers: map[string]string{"user-agent": "model-agent/1.0"}, want: "model-agent/1.0"},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				received := make(chan string, 8)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					received <- r.Header.Get("User-Agent")
					// A terminal error stops after the request, without relying on a
					// mock implementation of each provider's streaming response.
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"header probe complete"}}`)
				}))
				defer server.Close()
				cfg := ConfigState{
					APIFormat: format, BaseURL: server.URL, APIKey: "test-key", Model: "test-model", MaxTokens: 64,
					ProxyMode: proxyModeOff, UserAgent: tc.global, CustomHeaders: tc.headers, noAdapterRetry: true,
				}
				app := NewApp()
				_, err := app.streamModelResponseWithKey(context.Background(), cfg, cfg.Model, []legacyopenai.ChatCompletionMessage{{Role: "user", Content: "probe"}}, nil, nil)
				if err == nil {
					t.Fatal("expected the probe endpoint's terminal error")
				}
				select {
				case got := <-received:
					if got != tc.want {
						t.Fatalf("received User-Agent = %q, want %q", got, tc.want)
					}
				default:
					t.Fatalf("no request reached the probe: %v", err)
				}
			})
		}
	}
}

func TestModelListUserAgent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("ALLY_DATA_DIR", dir)
	for _, tc := range []struct {
		name    string
		global  string
		headers map[string]string
		want    string
	}{
		{name: "default", want: defaultHTTPUA},
		{name: "selected", global: "selected-agent/1.0", want: "selected-agent/1.0"},
		{name: "model-override", global: "selected-agent/1.0", headers: map[string]string{"User-Agent": "model-agent/1.0"}, want: "model-agent/1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Header.Get("User-Agent")
				fmt.Fprint(w, `{"data":[{"id":"test-model"}]}`)
			}))
			defer server.Close()
			app := NewApp()
			app.initialized = true
			app.config = ConfigState{UserAgent: tc.global, ProxyMode: proxyModeOff}
			if _, err := app.FetchModelList(server.URL, "test-key", tc.headers); err != nil {
				t.Fatal(err)
			}
			if got := <-received; got != tc.want {
				t.Fatalf("received User-Agent = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionModelSnapshotKeepsLiveUserAgent(t *testing.T) {
	frozen := sessionModelConfigFrom(ConfigState{Model: "test-model", UserAgent: "old-agent/1.0"})
	for _, ua := range []string{"new-agent/1.0", ""} {
		if got := frozen.apply(ConfigState{UserAgent: ua}); got.UserAgent != ua {
			t.Fatalf("session restored old UA: got %q, want %q", got.UserAgent, ua)
		}
	}
}
