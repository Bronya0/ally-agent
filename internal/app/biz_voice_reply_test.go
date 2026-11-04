// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A 200 with no transcript must come back empty. It used to return the test
// dialog's placeholder sentence, which the dictation path then pasted into the
// composer, so any false trigger wrote系统文案 into the user's draft.
func TestTestVoiceModelReturnsEmptyTranscriptAsEmpty(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{"empty object", `{}`},
		{"empty text", `{"text":""}`},
		{"whitespace text", `{"text":"   "}`},
		{"empty chat message", `{"choices":[{"message":{"content":""}}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			model := VoiceModelConfig{
				ID: "voice-empty", Deployment: "local", Protocol: "openai_audio", Endpoint: server.URL,
				FileField: "file", AuthMode: "none", TimeoutSeconds: 10,
			}
			text, err := configTransactionApp(t).TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
			if err != nil {
				t.Fatalf("a 200 with no text must not fail: %v", err)
			}
			if text != "" {
				t.Fatalf("text = %q, want an empty transcript", text)
			}
		})
	}
}

// An HTML page in a 200 is a proxy or login error. Returning it as text put markup
// into the composer, so it must be reported instead.
func TestVoiceModelRejectsHTMLReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>login required</body></html>`))
	}))
	defer server.Close()
	model := VoiceModelConfig{
		ID: "voice-html", Deployment: "local", Protocol: "openai_audio", Endpoint: server.URL,
		FileField: "file", AuthMode: "none", TimeoutSeconds: 10,
	}
	_, err := configTransactionApp(t).TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err == nil || !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("error = %v, want the HTML reply to be rejected", err)
	}
}

// Deployment must follow the endpoint when a config omits it, so an imported
// remote model is not labelled local.
func TestVoiceDeploymentForEndpoint(t *testing.T) {
	for _, test := range []struct{ endpoint, want string }{
		{"http://127.0.0.1:8786/v1/audio/transcriptions", "local"},
		{"http://localhost:9000/v1/audio/transcriptions", "local"},
		{"http://192.168.1.7:9000/v1/audio/transcriptions", "local"},
		{"http://10.0.0.5/v1/audio/transcriptions", "local"},
		{"http://172.20.3.4/v1/audio/transcriptions", "local"},
		{"https://api.siliconflow.cn/v1/audio/transcriptions", "api"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions", "api"},
		{"http://172.32.0.1/v1/audio/transcriptions", "api"},
	} {
		if got := VoiceDeploymentForEndpoint(test.endpoint); got != test.want {
			t.Errorf("VoiceDeploymentForEndpoint(%q) = %q, want %q", test.endpoint, got, test.want)
		}
	}
}

// NormalizeVoiceModels is the single place that fills defaults, so the desktop can
// stop repeating them.
func TestNormalizeVoiceModelsFillsProtocolDefaults(t *testing.T) {
	normalized := NormalizeVoiceModels([]VoiceModelConfig{
		{ID: "a", Protocol: "chat_audio", Endpoint: "https://example.com/v1/chat/completions"},
		{ID: "b", Endpoint: "http://127.0.0.1:8786/v1/audio/transcriptions"},
	})
	if len(normalized) != 2 {
		t.Fatalf("normalized = %+v", normalized)
	}
	if normalized[0].AuthMode != "bearer" || normalized[0].AuthPrefix != "Bearer " {
		t.Fatalf("chat_audio defaults = %+v", normalized[0])
	}
	if normalized[1].Protocol != "openai_audio" || normalized[1].Deployment != "local" {
		t.Fatalf("openai_audio defaults = %+v", normalized[1])
	}
	// The exported wrapper must not share maps with its input.
	original := []VoiceModelConfig{{ID: "c", Headers: map[string]string{"X": "1"}}}
	copied := NormalizeVoiceModels(original)
	copied[0].Headers["X"] = "2"
	if original[0].Headers["X"] != "1" {
		t.Fatal("NormalizeVoiceModels returned shared maps")
	}
}
