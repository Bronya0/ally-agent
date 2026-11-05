// SPDX-License-Identifier: GPL-3.0-only
package app

import "testing"

func TestVoiceProviderPresetsAreUsable(t *testing.T) {
	seen := map[string]bool{}
	for _, preset := range VoiceProviderPresets() {
		if preset.ID == "" || preset.Label == "" {
			t.Fatalf("preset needs an id and a label: %+v", preset)
		}
		if seen[preset.ID] {
			t.Fatalf("duplicate preset %q", preset.ID)
		}
		seen[preset.ID] = true
		// Every mode must name a known protocol.
		for _, mode := range preset.Modes {
			if mode.ID == "" || mode.Label == "" {
				t.Fatalf("preset %q has a mode without id/label: %+v", preset.ID, mode)
			}
			if _, ok := VoiceProtocolSpec(mode.Model.Protocol); !ok {
				t.Fatalf("preset %q mode %q uses an unknown protocol", preset.ID, mode.ID)
			}
			applied := normalizeVoiceModels([]VoiceModelConfig{preset.Apply(VoiceModelConfig{ID: "voice-1", APIKey: "secret"}, mode.ID)})[0]
			if err := validateVoiceModel(applied); err != nil {
				t.Fatalf("preset %q mode %q does not validate: %v", preset.ID, mode.ID, err)
			}
			if applied.Model != "" {
				t.Fatalf("preset %q mode %q chose a model %q", preset.ID, mode.ID, applied.Model)
			}
		}
		if len(preset.Modes) > 0 {
			continue
		}
		spec, ok := VoiceProtocolSpec(preset.Model.Protocol)
		if !ok {
			t.Fatalf("preset %q uses an unknown protocol %q", preset.ID, preset.Model.Protocol)
		}
		// Applying a preset must leave a model the user only has to give a key to.
		applied := normalizeVoiceModels([]VoiceModelConfig{preset.Apply(VoiceModelConfig{ID: "voice-1", APIKey: "secret"}, "")})[0]
		// A preset must leave nothing to fill in but the key, the endpoint and
		// the model id: the file field comes from the protocol.
		if spec.Uses(VoiceFieldFileField) && applied.FileField == "" {
			t.Fatalf("preset %q needs a file field", preset.ID)
		}
		if err := validateVoiceModel(applied); err != nil {
			t.Fatalf("preset %q does not validate: %v", preset.ID, err)
		}
		if applied.APIKey != "secret" {
			t.Fatalf("preset %q dropped the user's API key: %q", preset.ID, applied.APIKey)
		}
	}
	if _, ok := VoiceProviderPresetSpec("nope"); ok {
		t.Fatal("unknown preset must not resolve")
	}
}

// Only the three paths this project actually verified are offered: an untested
// provider or a "custom" escape hatch made the picker harder, not more flexible.
func TestVoicePresetListShape(t *testing.T) {
	want := []string{"local_openai_asr", "siliconflow", "dashscope"}
	presets := VoiceProviderPresets()
	if len(presets) != len(want) {
		t.Fatalf("presets = %+v, want %v", presets, want)
	}
	for i, id := range want {
		if presets[i].ID != id {
			t.Fatalf("preset %d = %q, want %q", i, presets[i].ID, id)
		}
	}
	for _, gone := range []string{"openai_whisper", "custom"} {
		if _, ok := VoiceProviderPresetSpec(gone); ok {
			t.Errorf("preset %q must not be offered", gone)
		}
	}
}

// SiliconFlow documents only `file` and `model` for /audio/transcriptions, so the
// preset must not send language or response_format.
func TestSiliconFlowPresetOmitsUndocumentedFields(t *testing.T) {
	preset, ok := VoiceProviderPresetSpec("siliconflow")
	if !ok {
		t.Fatal("siliconflow preset is missing")
	}
	applied := normalizeVoiceModels([]VoiceModelConfig{preset.Apply(VoiceModelConfig{ID: "voice-1", APIKey: "k"}, "")})[0]
	if applied.Endpoint != "https://api.siliconflow.cn/v1/audio/transcriptions" {
		t.Fatalf("endpoint = %q", applied.Endpoint)
	}
	if applied.Model != "" {
		t.Fatalf("presets must not choose a model, got %q", applied.Model)
	}
	if applied.FileField != "file" {
		t.Fatalf("file field = %q", applied.FileField)
	}
	if applied.Protocol != "openai_audio" || applied.AuthMode != "bearer" || applied.Deployment != "api" {
		t.Fatalf("protocol/auth/deployment = %q/%q/%q", applied.Protocol, applied.AuthMode, applied.Deployment)
	}
	if applied.Language != "" || applied.ResponseFormat != "" {
		t.Fatalf("undocumented fields must stay empty: language=%q responseFormat=%q", applied.Language, applied.ResponseFormat)
	}
}

// A preset must leave nothing to fill in but the credential and the model id:
// the model is fetched from the service or typed by hand, never preset.
func TestVoicePresetFillsEverythingButTheKey(t *testing.T) {
	preset, ok := VoiceProviderPresetSpec("siliconflow")
	if !ok {
		t.Fatal("siliconflow preset is missing")
	}
	applied := preset.Apply(VoiceModelConfig{ID: "voice-1"}, "")
	if applied.AuthHeader != "Authorization" || applied.AuthPrefix != "Bearer " {
		t.Fatalf("auth chrome = %q/%q", applied.AuthHeader, applied.AuthPrefix)
	}
	if applied.TimeoutSeconds != 60 || applied.FileField != "file" {
		t.Fatalf("timeout/file field = %d/%q", applied.TimeoutSeconds, applied.FileField)
	}
	if applied.Model != "" || applied.Name != "" {
		t.Fatalf("preset must leave model and display name empty, got %q/%q", applied.Model, applied.Name)
	}
	// A name or model the user already chose is never overwritten or cleared.
	kept := preset.Apply(VoiceModelConfig{ID: "voice-1", Name: "我的 ASR", Model: "my-model"}, "")
	if kept.Name != "我的 ASR" || kept.Model != "my-model" {
		t.Fatalf("user values were changed: %q/%q", kept.Name, kept.Model)
	}
	localPreset, ok := VoiceProviderPresetSpec("local_openai_asr")
	if !ok {
		t.Fatal("local preset is missing")
	}
	if got := localPreset.Apply(VoiceModelConfig{}, "").AuthPrefix; got != "" {
		t.Fatalf("a keyless preset must clear the auth prefix, got %q", got)
	}
}

func TestVoiceResponseFormatIsOptional(t *testing.T) {
	normalized := normalizeVoiceModels([]VoiceModelConfig{{
		ID: "voice-1", Deployment: "api", Protocol: "openai_audio", Endpoint: "https://example.com/v1/audio/transcriptions",
	}})[0]
	if normalized.ResponseFormat != "" {
		t.Fatalf("response format should stay empty so it is not sent, got %q", normalized.ResponseFormat)
	}
	if normalized.FileField != "file" {
		t.Fatalf("file field default = %q, want file", normalized.FileField)
	}
}
