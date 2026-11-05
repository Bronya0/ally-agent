// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Qwen-Audio-ASR (qwen-audio-3.x-asr-flash) is NOT the chat/completions shape:
// it posts input.messages with parameters.format/sample_rate to DashScope's
// multimodal-generation route and answers with output.output.sentence.text, no
// choices. parameters.format is required: without it the route answers
// "format is empty".
func TestDashScopeSyncAudioRequestAndReply(t *testing.T) {
	app := configTransactionApp(t)
	var gotPath, gotAuth, gotSSE string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotSSE = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-DashScope-SSE")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = io.WriteString(w, `{"output":{"sentence":{"text":"你好世界"},"text":"你好世界"},"request_id":"x"}`)
	}))
	defer server.Close()

	model := VoiceModelConfig{
		ID: "voice-audio-asr", Deployment: "api", Protocol: "dashscope_sync_audio",
		Endpoint: server.URL + "/api/v1/services/aigc/multimodal-generation/generation",
		Model:    "qwen-audio-3.0-asr-flash", AuthMode: "bearer", AuthPrefix: "Bearer ",
		APIKey: "sk-test", TimeoutSeconds: 30,
	}
	text, err := app.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err != nil {
		t.Fatal(err)
	}
	if text != "你好世界" {
		t.Fatalf("text = %q", text)
	}
	if gotPath != "/api/v1/services/aigc/multimodal-generation/generation" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" || gotSSE != "disable" {
		t.Fatalf("auth/sse = %q / %q", gotAuth, gotSSE)
	}
	if gotBody["model"] != "qwen-audio-3.0-asr-flash" {
		t.Fatalf("model = %v", gotBody["model"])
	}
	// The chat-style keys must not appear: this endpoint rejects them.
	if _, ok := gotBody["messages"]; ok {
		t.Fatal("messages must be nested under input for DashScope sync")
	}
	input, _ := gotBody["input"].(map[string]any)
	messages, _ := input["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("input.messages = %v", messages)
	}
	message, _ := messages[0].(map[string]any)
	content, _ := message["content"].([]any)
	part, _ := content[0].(map[string]any)
	audio, _ := part["input_audio"].(map[string]any)
	data, _ := audio["data"].(string)
	if !strings.HasPrefix(data, "data:audio/wav;base64,") {
		t.Fatalf("audio data = %.40q", data)
	}
	params, _ := gotBody["parameters"].(map[string]any)
	if params["format"] != "wav" || params["sample_rate"] != "16000" {
		t.Fatalf("parameters = %v", params)
	}
}

func TestParseVoiceDashScopeTextVariants(t *testing.T) {
	for _, test := range []struct{ name, body, want string }{
		{"output.text", `{"output":{"text":" 你好 "}}`, "你好"},
		{"sentence.text", `{"output":{"sentence":{"text":"语句"}}}`, "语句"},
		{"choices fallback", `{"output":{"choices":[{"message":{"content":"对话"}}]}}`, "对话"},
		// The synchronous ASR route really answers with the result wrapped
		// twice; reading only output.* dropped every transcript.
		{"nested output", `{"output":{"output":{"sentence":{"text":"嵌套"}}}}`, "嵌套"},
		{"empty", `{}`, ""},
	} {
		if got := parseVoiceDashScopeText([]byte(test.body)); got != test.want {
			t.Errorf("%s: parseVoiceDashScopeText = %q, want %q", test.name, got, test.want)
		}
	}
	// The combined reader also accepts a plain chat completion body.
	if got := parseVoiceAudioText([]byte(`{"choices":[{"message":{"content":"兼容"}}]}`)); got != "兼容" {
		t.Errorf("parseVoiceAudioText chat fallback = %q, want 兼容", got)
	}
}

func TestDashScopeSyncPreset(t *testing.T) {
	// The preset has no mode picker any more; a qwen-audio model chosen in it must
	// still be sent to the multimodal route.
	preset, ok := VoiceProviderPresetSpec("dashscope")
	if !ok {
		t.Fatal("the Bailian preset is missing")
	}
	applied := preset.Apply(VoiceModelConfig{ID: "voice-1"}, preset.DefaultMode())
	if applied.Model != "" {
		t.Fatalf("preset must not choose a model, got %q", applied.Model)
	}
	shape := voiceAudioShapeFor(VoiceModelConfig{Protocol: applied.Protocol, Model: "qwen-audio-3.0-asr-flash"})
	if shape != "dashscope_sync_audio" {
		t.Fatalf("a qwen-audio model in the preset must use the multimodal route, got %q", shape)
	}
	got := voiceAudioEndpoint(applied, shape)
	if !strings.HasSuffix(got, "/api/v1/services/aigc/multimodal-generation/generation") {
		t.Fatalf("endpoint = %q", got)
	}
}
