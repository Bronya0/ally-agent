// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoiceEndpointProbeAcceptsMethodNotAllowedAndReportsAuthFailure(t *testing.T) {
	app := configTransactionApp(t)
	model := VoiceModelConfig{ID: "v", Deployment: "local", Protocol: "openai_audio", FileField: "file", TimeoutSeconds: 10, AuthMode: "none"}
	methodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer methodServer.Close()
	model.Endpoint = methodServer.URL
	result, err := app.TestNativeVoiceEndpoint(model)
	if err != nil || !strings.Contains(result, "可达") {
		t.Fatalf("405 should prove the transcription route is reachable: %q %v", result, err)
	}
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer authServer.Close()
	model.Endpoint = authServer.URL
	if result, err = app.TestNativeVoiceEndpoint(model); err == nil || !strings.Contains(result, "鉴权失败") {
		t.Fatalf("expected auth failure, got %q %v", result, err)
	}
}

// Gateways that only route POST answer 404 to HEAD; the empty POST then proves
// the path exists, which is what an API-key-valid-but-HEAD-refusing service needs.
func TestVoiceEndpointProbeFallsBackToPost(t *testing.T) {
	app := configTransactionApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	model := VoiceModelConfig{ID: "v", Deployment: "api", Protocol: "openai_audio", Endpoint: server.URL, FileField: "file", TimeoutSeconds: 10, AuthMode: "none"}
	result, err := app.TestNativeVoiceEndpoint(model)
	if err != nil || !strings.Contains(result, "路由存在") {
		t.Fatalf("HEAD 404 + POST 422 should count as reachable: %q %v", result, err)
	}
}

func TestVoiceEndpointProbeReportsMissingRoute(t *testing.T) {
	app := configTransactionApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	model := VoiceModelConfig{ID: "v", Deployment: "api", Protocol: "openai_audio", Endpoint: server.URL + "/nope", FileField: "file", TimeoutSeconds: 10, AuthMode: "none"}
	result, err := app.TestNativeVoiceEndpoint(model)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("HEAD and POST both 404 must report a missing route: %q %v", result, err)
	}
}

func TestVoiceTranscriptionAllowsBlankDisplayNameInDraft(t *testing.T) {
	app := configTransactionApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method=%s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"识别成功"}`))
	}))
	defer server.Close()

	model := VoiceModelConfig{ID: "draft", Endpoint: server.URL, AuthMode: "none", TimeoutSeconds: 10}
	text, err := app.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err != nil || text != "识别成功" {
		t.Fatalf("transcription with blank display name: text=%q err=%v", text, err)
	}
}

func TestVoiceModelValidationAndResponseRedaction(t *testing.T) {
	model := VoiceModelConfig{ID: "voice", Deployment: "local", Protocol: "multipart", Endpoint: "http://127.0.0.1", FileField: "file", AuthMode: "bearer", APIKey: "top-secret", AuthPrefix: "Bearer ", TimeoutSeconds: 30}
	if err := validateVoiceModel(model); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	bad := model
	bad.AuthPrefix = "Bearer\r\nX-Injected: yes"
	if err := validateVoiceModel(bad); err == nil {
		t.Fatal("expected invalid auth value error")
	}

	a := configTransactionApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid token top-secret"))
	}))
	defer server.Close()
	model.Endpoint = server.URL
	_, err := a.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err == nil || strings.Contains(err.Error(), "top-secret") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error did not safely redact key: %v", err)
	}
}
