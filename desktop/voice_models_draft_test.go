// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"testing"

	backend "ally-dev/internal/app"
)

// The editor no longer asks for an auth mode or a config id, so both are derived.
// A blank key must not stay on bearer: the validator would demand a key the user
// has no reason to have for a local ASR service.
func TestVoiceAuthForDerivesFromApiKey(t *testing.T) {
	bearer := backend.VoiceModelConfig{Protocol: "chat_audio", AuthMode: "bearer", AuthPrefix: "Bearer "}
	for _, test := range []struct {
		name  string
		model backend.VoiceModelConfig
		want  string
	}{
		{"no key means open service", bearer, "none"},
		{"key keeps bearer", backend.VoiceModelConfig{Protocol: "chat_audio", AuthMode: "bearer", APIKey: "sk-1"}, "bearer"},
		{"key keeps custom header", backend.VoiceModelConfig{Protocol: "chat_audio", AuthMode: "header", AuthHeader: "X-Token", APIKey: "sk-1"}, "header"},
		{"empty mode with key falls back to bearer", backend.VoiceModelConfig{Protocol: "openai_audio", APIKey: "sk-1"}, "bearer"},
		{"signed protocol keeps its mode", backend.VoiceModelConfig{Protocol: "tencent_asr", AuthMode: "tencent"}, "tencent"},
	} {
		got, header, _ := voiceAuthFor(test.model)
		if got != test.want {
			t.Errorf("%s: auth mode = %q, want %q", test.name, got, test.want)
		}
		if header == "" {
			t.Errorf("%s: the auth header must always be named", test.name)
		}
	}
	if _, _, prefix := voiceAuthFor(backend.VoiceModelConfig{Protocol: "openai_audio", APIKey: "sk-1"}); prefix != "Bearer " {
		t.Errorf("a bearer key must default to the Bearer prefix, got %q", prefix)
	}
}

// Reopening a saved model must show the service it was built from instead of an
// empty picker, because there is no "custom" entry to fall back to.
func TestVoicePresetForInfersTheService(t *testing.T) {
	for _, test := range []struct{ endpoint, want string }{
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions", "dashscope"},
		{"https://api.siliconflow.cn/v1/audio/transcriptions", "siliconflow"},
		{"http://127.0.0.1:8786/v1/audio/transcriptions", "local_openai_asr"},
		{"http://192.168.1.5:9000/v1/audio/transcriptions", "local_openai_asr"},
		{"https://gateway.example.com/v1/audio/transcriptions", ""},
	} {
		if got := voicePresetFor(test.endpoint); got != test.want {
			t.Errorf("voicePresetFor(%q) = %q, want %q", test.endpoint, got, test.want)
		}
	}
}

// Switching the service must not carry the previous service's answers over: a
// stale key or model id would silently test the wrong account.
func TestApplyVoicePresetClearsServiceSpecificValues(t *testing.T) {
	dashscope, ok := backend.VoiceProviderPresetSpec("dashscope")
	if !ok {
		t.Fatal("dashscope preset is missing")
	}
	s := &voiceModelsState{
		preset:           "siliconflow",
		draft:            backend.VoiceModelConfig{ID: "voice-1", Model: "FunAudioLLM/SenseVoiceSmall", Name: "我的 ASR", APIKey: "sk-old", Headers: map[string]string{"X-A": "b"}, Endpoint: "https://api.siliconflow.cn/v1/audio/transcriptions"},
		fetched:          []string{"FunAudioLLM/SenseVoiceSmall"},
		fetchNote:        "已获取 1 个语音模型",
		includeAllModels: true,
		endpointEditing:  true,
		endpointPassKey:  "stale",
	}
	s.applyVoicePreset(dashscope, dashscope.DefaultMode())
	if s.draft.APIKey != "" || s.draft.Model != "" || s.draft.Name != "" {
		t.Fatalf("draft kept previous service values: %+v", s.draft)
	}
	if len(s.draft.Headers) != 0 || len(s.fetched) != 0 || s.fetchNote != "" {
		t.Fatalf("fetched list/headers survived the switch: %+v %v %q", s.draft.Headers, s.fetched, s.fetchNote)
	}
	if s.includeAllModels || s.endpointEditing || s.endpointPassKey != "" {
		t.Fatalf("endpoint state survived the switch: %v %v %q", s.includeAllModels, s.endpointEditing, s.endpointPassKey)
	}
	if s.draft.ID != "voice-1" || !strings.Contains(s.draft.Endpoint, "dashscope.aliyuncs.com") {
		t.Fatalf("the preset must keep the id and replace the endpoint: %+v", s.draft)
	}
}

func TestNextVoiceModelIDSkipsTakenIDs(t *testing.T) {
	models := []backend.VoiceModelConfig{{ID: "voice-1"}, {ID: "voice-2"}}
	if got := nextVoiceModelID(models); got != "voice-3" {
		t.Fatalf("nextVoiceModelID = %q, want voice-3", got)
	}
	if got := nextVoiceModelID(nil); got != "voice-1" {
		t.Fatalf("nextVoiceModelID(empty) = %q, want voice-1", got)
	}
}
