// SPDX-License-Identifier: GPL-3.0-only
package app

import "strings"

// VoicePresetMode is one call mode a service offers, e.g. Bailian's
// non-realtime (whole recording) and realtime (websocket) ASR.
type VoicePresetMode struct {
	ID    string
	Label string
	// Model overrides the preset template for this mode.
	Model VoiceModelConfig
}

// VoiceProviderPreset fills the model editor with the values one known service
// documents, so a user does not have to know which protocol, file field or
// optional form fields a vendor expects. A preset never carries credentials and
// never chooses a model.
type VoiceProviderPreset struct {
	ID    string
	Label string
	// Modes are the call modes this service offers. When there is more than one,
	// the editor asks which one and picks its protocol, endpoint and list filter.
	Modes []VoicePresetMode
	// Model is the template applied to the draft: only the fields a preset sets
	// are touched.
	Model VoiceModelConfig
}

var voiceProviderPresets = []VoiceProviderPreset{
	{
		ID:    "local_openai_asr",
		Label: "本地 ASR（OpenAI 格式）",
		Model: VoiceModelConfig{
			Deployment: "local", Protocol: "openai_audio", Provider: "本地服务",
			Endpoint:   "http://127.0.0.1:8786/v1/audio/transcriptions",
			AuthMode:   "none",
			AuthHeader: "Authorization",
			FileField:  "file", TimeoutSeconds: 30,
		},
	},
	{
		ID:    "siliconflow",
		Label: "硅基流动（OpenAI 格式）",
		Model: VoiceModelConfig{
			Deployment: "api", Protocol: "openai_audio", Provider: "SiliconFlow",
			Endpoint:   "https://api.siliconflow.cn/v1/audio/transcriptions",
			AuthMode:   "bearer",
			AuthHeader: "Authorization", AuthPrefix: "Bearer ",
			FileField: "file", TimeoutSeconds: 60,
		},
	},
	{
		ID:    "dashscope",
		Label: "阿里云百炼",
		// Bailian serves non-realtime ASR through two routes, but the model name
		// decides which one: qwen3-asr on chat/completions, qwen-audio and fun-asr
		// on multimodal-generation. One preset, one mode, no pairing to get wrong.
		// Realtime (websocket) is not implemented and therefore not offered.
		Model: VoiceModelConfig{
			Deployment: "api", Protocol: "chat_audio", Provider: "DashScope",
			AuthMode:   "bearer",
			Endpoint:   "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions",
			AuthHeader: "Authorization", AuthPrefix: "Bearer ",
			FileField: "file", TimeoutSeconds: 60,
		},
		// One mode only: realtime streaming is not implemented yet, so the choice
		// stays visible but offers nothing that cannot work.
		Modes: []VoicePresetMode{
			{ID: "batch", Label: "非实时"},
		},
	},
}

// VoiceProviderPresets returns the preset list for the model editor.
func VoiceProviderPresets() []VoiceProviderPreset {
	out := make([]VoiceProviderPreset, len(voiceProviderPresets))
	copy(out, voiceProviderPresets)
	return out
}

// VoiceProviderPresetSpec looks up one preset by its ID.
func VoiceProviderPresetSpec(id string) (VoiceProviderPreset, bool) {
	for _, p := range voiceProviderPresets {
		if p.ID == strings.TrimSpace(id) {
			return p, true
		}
	}
	return VoiceProviderPreset{}, false
}

// Apply keeps the user's identity, credentials and model name, and replaces the
// endpoint configuration with the preset's template plus the chosen mode. Presets
// deliberately never choose a model: it has to be fetched from the service or
// typed by hand.
func (p VoiceProviderPreset) Apply(current VoiceModelConfig, modeID string) VoiceModelConfig {
	template := p.Model
	if mode, ok := p.Mode(modeID); ok {
		template = mergeVoicePresetTemplate(template, mode.Model)
	}
	out := current
	if template.Deployment != "" {
		out.Deployment = template.Deployment
	}
	if template.Protocol != "" {
		out.Protocol = template.Protocol
	}
	if template.Provider != "" {
		out.Provider = template.Provider
	}
	if template.Endpoint != "" {
		out.Endpoint = template.Endpoint
	}
	if template.AuthMode != "" {
		out.AuthMode = template.AuthMode
	}
	if template.FileField != "" {
		out.FileField = template.FileField
	}
	if template.AuthHeader != "" {
		out.AuthHeader = template.AuthHeader
	}
	out.AuthPrefix = template.AuthPrefix
	if template.TimeoutSeconds > 0 {
		out.TimeoutSeconds = template.TimeoutSeconds
	}
	// Empty template values clear the field on purpose: an endpoint that does not
	// document `language` or `response_format` must not receive them.
	out.Language = template.Language
	out.ResponseFormat = template.ResponseFormat
	return out
}

// Mode looks up one call mode of the preset.
func (p VoiceProviderPreset) Mode(id string) (VoicePresetMode, bool) {
	id = strings.TrimSpace(id)
	for _, mode := range p.Modes {
		if mode.ID == id {
			return mode, true
		}
	}
	return VoicePresetMode{}, false
}

// DefaultMode is the first declared mode, used when a preset is first applied.
func (p VoiceProviderPreset) DefaultMode() string {
	if len(p.Modes) == 0 {
		return ""
	}
	return p.Modes[0].ID
}

// mergeVoicePresetTemplate overlays a mode on the preset defaults, mode wins.
func mergeVoicePresetTemplate(base, mode VoiceModelConfig) VoiceModelConfig {
	out := base
	if mode.Deployment != "" {
		out.Deployment = mode.Deployment
	}
	if mode.Protocol != "" {
		out.Protocol = mode.Protocol
	}
	if mode.Provider != "" {
		out.Provider = mode.Provider
	}
	if mode.Endpoint != "" {
		out.Endpoint = mode.Endpoint
	}
	if mode.AuthMode != "" {
		out.AuthMode = mode.AuthMode
	}
	out.Language, out.ResponseFormat = mode.Language, mode.ResponseFormat
	return out
}
