// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"strings"
	"testing"
)

// Every model written before the split has no kind at all, and it must come back
// as ASR: otherwise an existing dictation setup would silently disappear from the
// picker.
func TestNormalizeVoiceKindDefaultsToASR(t *testing.T) {
	for _, test := range []struct{ stored, want string }{
		{"", VoiceKindASR},
		{"asr", VoiceKindASR},
		{"ASR", VoiceKindASR},
		{" asr ", VoiceKindASR},
		{"tts", VoiceKindTTS},
		{"TTS", VoiceKindTTS},
		{"whisper", VoiceKindASR}, // a typo must not hide the model from dictation
	} {
		if got := VoiceKindOf(test.stored); got != test.want {
			t.Errorf("VoiceKindOf(%q) = %q, want %q", test.stored, got, test.want)
		}
	}
	normalized := NormalizeVoiceModels([]VoiceModelConfig{{ID: "old", Endpoint: "http://127.0.0.1:8786/v1/audio/transcriptions"}})
	if len(normalized) != 1 || normalized[0].Kind != VoiceKindASR {
		t.Fatalf("a model without a kind must normalize to asr: %+v", normalized)
	}
}

// TTS is a stored category, not a working one: a TTS model saves and lists, but no
// transcription request is ever built from it.
func TestVoiceTTSPresetAndValidation(t *testing.T) {
	if VoiceKindImplemented(VoiceKindTTS) {
		t.Fatal("TTS must not be reported as implemented")
	}
	if !VoiceKindImplemented(VoiceKindASR) {
		t.Fatal("ASR must be implemented")
	}
	if got := VoiceKindLabel(VoiceKindTTS); !strings.Contains(got, "合成") {
		t.Fatalf("tts label = %q", got)
	}
	if got := VoiceKindLabel(""); !strings.Contains(got, "识别") {
		t.Fatalf("asr label = %q", got)
	}
	model := NormalizeVoiceModels([]VoiceModelConfig{{
		ID: "tts-1", Kind: VoiceKindTTS, Protocol: "openai_audio", Model: "tts-1-hd",
		Endpoint: "https://example.com/v1/audio/speech", AuthMode: "bearer", APIKey: "sk-1",
	}})[0]
	if err := validateVoiceModel(model); err != nil {
		t.Fatalf("a TTS model must be storable, got %v", err)
	}
	// But it must never reach a transcription request.
	if _, err := configTransactionApp(t).TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000)); err == nil || !strings.Contains(err.Error(), "TTS") {
		t.Fatalf("transcribing with a TTS model must fail clearly, got %v", err)
	}
}

// The protocols and presets this build ships are all ASR; a TTS entry would need a
// request builder that does not exist yet.
func TestVoiceProtocolsAndPresetsAreASR(t *testing.T) {
	for _, protocol := range voiceProtocols {
		if protocol.Kind != VoiceKindASR {
			t.Errorf("protocol %q kind = %q, want asr", protocol.ID, protocol.Kind)
		}
	}
	for _, preset := range voiceProviderPresets {
		if preset.Kind != VoiceKindASR {
			t.Errorf("preset %q kind = %q, want asr", preset.ID, preset.Kind)
		}
	}
}

// A draft built from a preset must carry the ASR category, so the saved model is
// labelled without the user choosing anything.
func TestPresetApplySetsKind(t *testing.T) {
	preset, ok := VoiceProviderPresetSpec("dashscope")
	if !ok {
		t.Fatal("the Bailian preset is missing")
	}
	applied := preset.Apply(VoiceModelConfig{ID: "voice-1", APIKey: "sk-1"}, preset.DefaultMode())
	if applied.Kind != VoiceKindASR {
		t.Fatalf("applied kind = %q, want asr", applied.Kind)
	}
}

// A file that predates the split imports as ASR rather than failing validation.
func TestNormalizeVoiceModelsKeepsKindOnClone(t *testing.T) {
	models := NormalizeVoiceModels([]VoiceModelConfig{
		{ID: "asr", Endpoint: "http://127.0.0.1:8786/v1/audio/transcriptions"},
		{ID: "tts", Kind: "TTS", Endpoint: "https://example.com/v1/audio/speech"},
	})
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].Kind != VoiceKindASR || models[1].Kind != VoiceKindTTS {
		t.Fatalf("kinds = %q/%q, want asr/tts", models[0].Kind, models[1].Kind)
	}
}
