package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestSessionAffinityWireCustomHeadersOverrideDefaults(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	states := []struct {
		name                          string
		sessionID, xSessionID         *bool
		wantSessionID, wantXSessionID bool
	}{
		{"default", nil, nil, true, true},
		{"both-enabled", boolPtr(true), boolPtr(true), true, true},
		{"session-only", boolPtr(true), boolPtr(false), true, false},
		{"x-session-only", boolPtr(false), boolPtr(true), false, true},
		{"both-disabled", boolPtr(false), boolPtr(false), false, false},
	}
	for _, format := range []string{apiFormatOpenAIChat, apiFormatOpenAIResponses, apiFormatAnthropicMessages} {
		for _, state := range states {
			t.Run(format+"/"+state.name, func(t *testing.T) {
				for _, custom := range []map[string]string{nil, {"SESSION-ID": "custom-session"}, {"X-SESSION-ID": "custom-relay"}, {"session-id": "custom-session", "x-session-id": "custom-relay"}} {
					for _, sid := range []string{"session-a", "session-a", "session-b"} {
						key := openAIResponsesPromptCacheKey(sid)
						calls := 0
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls++
							for _, name := range []string{"session-id", "x-session-id"} {
								want := key
								if name == "session-id" && !state.wantSessionID || name == "x-session-id" && !state.wantXSessionID {
									want = ""
								}
								for k, v := range custom {
									if http.CanonicalHeaderKey(k) == http.CanonicalHeaderKey(name) {
										want = v
									}
								}
								got := r.Header.Values(name)
								if want == "" {
									if len(got) != 0 {
										t.Errorf("%s: got %v, want absent", name, got)
									}
								} else if len(got) != 1 || got[0] != want {
									t.Errorf("%s: got %v, want %q", name, got, want)
								}
							}
							// A terminal client error verifies the outgoing request without retries.
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"error":{"message":"test complete","type":"invalid_request_error"}}`))
						}))
						cfg := protocolConfig(server.URL, format)
						cfg.responsesPromptCacheKey = key
						cfg.SendSessionIDHeader = state.sessionID
						cfg.SendXSessionIDHeader = state.xSessionID
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
}
