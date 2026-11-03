// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Qwen3-ASR on Bailian is an OpenAI chat completion with an audio content part,
// so the request must carry a base64 data URI and the reply is assistant text.
func TestChatAudioTranscriptionRequestAndReply(t *testing.T) {
	app := configTransactionApp(t)
	var gotPath, gotAuth, gotContentType string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotContentType = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"今天天气不错"}}],"usage":{"total_tokens":12}}`)
	}))
	defer server.Close()

	model := VoiceModelConfig{
		ID: "voice-qwen", Deployment: "api", Protocol: "chat_audio",
		Endpoint: server.URL + "/compatible-mode/v1/chat/completions",
		Model:    "qwen3-asr-flash", AuthMode: "bearer", AuthPrefix: "Bearer ",
		APIKey: "sk-test", Language: "zh", TimeoutSeconds: 20,
	}
	text, err := app.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err != nil {
		t.Fatal(err)
	}
	if text != "今天天气不错" {
		t.Fatalf("text = %q", text)
	}
	if gotPath != "/compatible-mode/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" || gotContentType != "application/json" {
		t.Fatalf("auth/content-type = %q / %q", gotAuth, gotContentType)
	}
	if gotBody["model"] != "qwen3-asr-flash" || gotBody["stream"] != false {
		t.Fatalf("body = %v", gotBody)
	}
	options, _ := gotBody["asr_options"].(map[string]any)
	if options["language"] != "zh" || options["enable_itn"] != true {
		t.Fatalf("asr_options = %v", options)
	}
	messages, _ := gotBody["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %v", messages)
	}
	message, _ := messages[0].(map[string]any)
	if message["role"] != "user" {
		t.Fatalf("message = %v", message)
	}
	content, _ := message["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %v", content)
	}
	part, _ := content[0].(map[string]any)
	if part["type"] != "input_audio" {
		t.Fatalf("content part = %v", part)
	}
	audioPart, _ := part["input_audio"].(map[string]any)
	data, _ := audioPart["data"].(string)
	if !strings.HasPrefix(data, "data:audio/wav;base64,") {
		t.Fatalf("the chat shape must send a data URI, got %.40q", data)
	}
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(data, "data:audio/wav;base64,")); err != nil || len(decoded) < 44 {
		t.Fatalf("audio payload is not the recorded WAV: %v", err)
	}
	if audioPart["format"] != "wav" {
		t.Fatalf("input_audio.format = %v, want wav", audioPart["format"])
	}
	if _, ok := gotBody["parameters"]; ok {
		t.Fatal("the chat payload must not add top-level parameters")
	}
}

// The model family decides the route, so a model picked in the other mode still
// works: qwen-audio on the chat protocol must be sent as the multimodal body.
func TestChatAudioFallsBackToDashScopeShape(t *testing.T) {
	app := configTransactionApp(t)
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = io.WriteString(w, `{"output":{"output":{"sentence":{"text":"收到"}}}}`)
	}))
	defer server.Close()

	model := VoiceModelConfig{
		ID: "voice-qwen", Deployment: "api", Protocol: "chat_audio", Endpoint: server.URL,
		Model: "qwen-audio-3.0-asr-flash", AuthMode: "bearer", AuthPrefix: "Bearer ", APIKey: "sk-test", TimeoutSeconds: 20,
	}
	text, err := app.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err != nil {
		t.Fatal(err)
	}
	if text != "收到" {
		t.Fatalf("text = %q", text)
	}
	if _, chat := gotBody["messages"]; chat {
		t.Fatal("a qwen-audio model must not be sent as a chat body")
	}
	params, _ := gotBody["parameters"].(map[string]any)
	if params["format"] != "wav" {
		t.Fatalf("the multimodal body needs parameters.format, got %v", gotBody)
	}
}

// The reverse pairing: qwen3-asr on the multimodal protocol must be sent as the
// chat body instead of returning "Input should be a valid string".
func TestDashScopeSyncProtocolAcceptsChatFamilyModel(t *testing.T) {
	app := configTransactionApp(t)
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"收到"}}]}`)
	}))
	defer server.Close()

	model := VoiceModelConfig{
		ID: "voice-qwen", Deployment: "api", Protocol: "dashscope_sync_audio", Endpoint: server.URL,
		Model: "qwen3-asr-flash", AuthMode: "bearer", AuthPrefix: "Bearer ", APIKey: "sk-test", TimeoutSeconds: 20,
	}
	text, err := app.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err != nil {
		t.Fatal(err)
	}
	if text != "收到" {
		t.Fatalf("text = %q", text)
	}
	if _, chat := gotBody["messages"]; !chat {
		t.Fatalf("a qwen3-asr model must be sent as a chat body, got %v", gotBody)
	}
}

// An unknown model keeps the selected protocol's body, and the rejection must be
// a single request with a hint instead of a silent blind retry.
func TestChatAudioReportsRouteMismatchOnce(t *testing.T) {
	app := configTransactionApp(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"format is empty","type":"UNSUPPORTED_FORMAT"}}`)
	}))
	defer server.Close()

	model := VoiceModelConfig{
		ID: "voice-custom", Deployment: "api", Protocol: "chat_audio", Endpoint: server.URL,
		Model: "my-asr-1", AuthMode: "bearer", AuthPrefix: "Bearer ", APIKey: "sk-test", TimeoutSeconds: 20,
	}
	_, err := app.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err == nil || !strings.Contains(err.Error(), "qwen3-asr 系列走 chat/completions") {
		t.Fatalf("error = %v, want the route pairing hint", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want a single request", calls)
	}
}

// Bailian's two non-realtime routes are not interchangeable. Switching mode must
// move the URL with it instead of posting a qwen3-asr model to the multimodal
// route (or the reverse); unrelated gateways keep the URL the user typed.
func TestVoiceAudioEndpointFollowsProtocolMode(t *testing.T) {
	const (
		chatURL = "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
		syncURL = "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
	)
	for _, test := range []struct{ name, shape, endpoint, want string }{
		{"chat keeps its route", "chat_audio", chatURL, chatURL},
		{"sync keeps its route", "dashscope_sync_audio", syncURL, syncURL},
		{"chat corrects the sync route", "chat_audio", syncURL, chatURL},
		{"sync corrects the chat route", "dashscope_sync_audio", chatURL, syncURL},
		{"other hosts untouched", "dashscope_sync_audio", "https://api.siliconflow.cn/v1/chat/completions", "https://api.siliconflow.cn/v1/chat/completions"},
	} {
		got := voiceAudioEndpoint(VoiceModelConfig{Endpoint: test.endpoint}, test.shape)
		if got != test.want {
			t.Errorf("%s: endpoint = %q, want %q", test.name, got, test.want)
		}
	}
}

// The model family, not the mode, decides the request shape: both families are
// selectable in one non-realtime mode, so a wrong pair must still transcribe.
func TestVoiceAudioShapeFollowsModelFamily(t *testing.T) {
	for _, test := range []struct{ name, protocol, model, want string }{
		{"qwen3-asr on chat", "chat_audio", "qwen3-asr-flash", "chat_audio"},
		{"qwen-audio on chat", "chat_audio", "qwen-audio-3.0-asr-flash", "dashscope_sync_audio"},
		{"fun-asr on chat", "chat_audio", "fun-asr-flash-2026-06-15", "dashscope_sync_audio"},
		{"qwen3-asr on sync", "dashscope_sync_audio", "qwen3-asr-flash", "chat_audio"},
		{"qwen-audio on sync", "dashscope_sync_audio", "qwen-audio-3.0-asr-flash", "dashscope_sync_audio"},
		{"realtime id keeps the protocol", "dashscope_sync_audio", "qwen3-asr-flash-realtime", "dashscope_sync_audio"},
		{"unknown id keeps the protocol", "chat_audio", "my-asr-1", "chat_audio"},
	} {
		got := voiceAudioShapeFor(VoiceModelConfig{Protocol: test.protocol, Model: test.model})
		if got != test.want {
			t.Errorf("%s: shape = %q, want %q", test.name, got, test.want)
		}
	}
}

// A 200 with no text is what a silent test clip produces; saying so is the only
// way a user learns the endpoint works but the audio has no speech.
func TestChatAudioBlankTranscriptExplainsSilence(t *testing.T) {
	app := configTransactionApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":""}}]}`)
	}))
	defer server.Close()

	model := VoiceModelConfig{
		ID: "voice-qwen", Deployment: "api", Protocol: "chat_audio", Endpoint: server.URL,
		Model: "qwen3-asr-flash", AuthMode: "bearer", AuthPrefix: "Bearer ", APIKey: "sk-test", TimeoutSeconds: 20,
	}
	_, err := app.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err == nil || !strings.Contains(err.Error(), "空白测试音") {
		t.Fatalf("error = %v, want the silent-clip explanation", err)
	}
}

func TestChatAudioTranscriptionReportsServerError(t *testing.T) {
	app := configTransactionApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"InvalidParameter: model not found","code":"InvalidParameter"}}`)
	}))
	defer server.Close()

	model := VoiceModelConfig{
		ID: "voice-qwen", Deployment: "api", Protocol: "chat_audio", Endpoint: server.URL,
		Model: "qwen3-asr-flash", AuthMode: "bearer", AuthPrefix: "Bearer ", APIKey: "sk-secret", TimeoutSeconds: 20,
	}
	_, err := app.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000))
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("error = %v, want the server's message", err)
	}
	if strings.Contains(err.Error(), "sk-secret") {
		t.Fatal("the API key leaked into the error")
	}
}

func TestParseVoiceChatTextVariants(t *testing.T) {
	for _, test := range []struct{ name, body, want string }{
		{"string content", `{"choices":[{"message":{"content":"  你好  "}}]}`, "你好"},
		{"content parts", `{"choices":[{"message":{"content":[{"type":"text","text":"你"},{"type":"text","text":"好"}]}}]}`, "你好"},
		{"no choices", `{"choices":[]}`, ""},
		{"garbage", `not json`, ""},
	} {
		if got := parseVoiceChatText([]byte(test.body)); got != test.want {
			t.Errorf("%s: parseVoiceChatText = %q, want %q", test.name, got, test.want)
		}
	}
}

// One 阿里云百炼 preset with a single 非实时 mode: the choice stays visible but
// offers nothing that cannot work, and the model name decides the route.
func TestDashscopePresetModes(t *testing.T) {
	preset, ok := VoiceProviderPresetSpec("dashscope")
	if !ok {
		t.Fatal("the Bailian preset is missing")
	}
	if len(preset.Modes) != 1 || preset.Modes[0].ID != "batch" || preset.Modes[0].Label != "非实时" {
		t.Fatalf("Bailian should offer exactly one 非实时 mode, got %+v", preset.Modes)
	}
	if preset.DefaultMode() != "batch" {
		t.Fatalf("default mode = %q", preset.DefaultMode())
	}
	if preset.Model.Protocol != "chat_audio" {
		t.Fatalf("preset protocol = %q", preset.Model.Protocol)
	}
	applied := normalizeVoiceModels([]VoiceModelConfig{preset.Apply(VoiceModelConfig{ID: "voice-1", APIKey: "sk-x"}, preset.DefaultMode())})[0]
	if err := validateVoiceModel(applied); err != nil {
		t.Fatalf("preset does not validate: %v", err)
	}
	if !strings.HasSuffix(applied.Endpoint, "/compatible-mode/v1/chat/completions") || applied.AuthMode != "bearer" {
		t.Fatalf("endpoint/auth = %q/%q", applied.Endpoint, applied.AuthMode)
	}
	if applied.Model != "" {
		t.Fatalf("preset must not choose a model, got %q", applied.Model)
	}
	// Realtime is gone from the protocol table until the client speaks it.
	if _, ok := VoiceProtocolSpec("dashscope_realtime"); ok {
		t.Fatal("the realtime protocol must not be offered yet")
	}
}
