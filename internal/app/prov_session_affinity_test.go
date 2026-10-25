package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestSessionAffinityWireCustomHeadersOverrideDefaults(t *testing.T) {
	a := newApiTestApp(t)
	for _, format := range []string{apiFormatOpenAIChat, apiFormatOpenAIResponses, apiFormatAnthropicMessages} {
		t.Run(format, func(t *testing.T) {
			for _, custom := range []map[string]string{nil, {"SESSION-ID": "custom-session"}, {"X-SESSION-ID": "custom-relay"}, {"session-id": "custom-session", "x-session-id": "custom-relay"}} {
				for _, sid := range []string{"session-a", "session-a", "session-b"} {
					key := openAIResponsesPromptCacheKey(sid)
					calls := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						for _, name := range []string{"session-id", "x-session-id"} {
							want := key
							for k, v := range custom {
								if http.CanonicalHeaderKey(k) == http.CanonicalHeaderKey(name) {
									want = v
								}
							}
							if got := r.Header.Values(name); len(got) != 1 || got[0] != want {
								t.Errorf("%s: got %v, want %q", name, got, want)
							}
						}
						// A terminal client error verifies the outgoing request without retries.
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":{"message":"test complete","type":"invalid_request_error"}}`))
					}))
					cfg := protocolConfig(server.URL, format)
					cfg.responsesPromptCacheKey = key
					cfg.CustomHeaders = custom
					_, _ = a.streamModelResponse(context.Background(), cfg, cfg.Model, []openai.ChatCompletionMessage{{Role: "user", Content: "test"}}, nil, nil)
					server.Close()
					if calls != 1 {
						t.Fatalf("got %d requests, want 1", calls)
					}
				}
			}
		})
	}
}
