// SPDX-License-Identifier: GPL-3.0-only
package app

import "strings"

// Config fields a voice protocol may read. The model editor shows exactly the
// fields its protocol lists, and the validator requires the same ones, so a
// protocol can never be edited into a shape it cannot send.
const (
	VoiceFieldEndpoint       = "endpoint"
	VoiceFieldModel          = "model"
	VoiceFieldLanguage       = "language"
	VoiceFieldResponseFormat = "responseFormat"
	VoiceFieldFileField      = "fileField"
	VoiceFieldExtraFields    = "extraFields"
	VoiceFieldHeaders        = "headers"
	VoiceFieldAPIKey         = "apiKey"
	VoiceFieldTimeout        = "timeout"
)

// Model-list filter. A provider's catalogue mixes capabilities (chat, ASR, TTS,
// realtime), so the voice editor narrows it by the protocol's mode: ids carrying
// "realtime" are dropped, because this client only speaks whole-recording HTTP
// transcription for now.
const (
	VoiceListAny         = ""
	VoiceListNonRealtime = "speech_non_realtime"
)

// VoiceProtocol describes how one transcription endpoint is called: which config
// fields its request builder reads, and which auth modes it accepts. Adding a
// vendor adapter means adding a spec here and one request builder next to the
// multipart one.
//
// Fields is the request's field list, not a form schema: the editor and validation
// currently enforce only Model, APIKey and FileField through Uses. Anything else
// listed here is sent by the builder and may be edited by a preset, but a form
// that stops asking for it does not make the protocol stop reading it.
type VoiceProtocol struct {
	ID    string
	Label string
	// Kind is the model category this protocol serves: asr or tts. Only ASR has a
	// request builder today; a TTS protocol is added here when synthesis lands.
	Kind string
	// AuthModes are the accepted AuthMode values for this protocol.
	AuthModes []string
	// Fields are the config fields this protocol's request builder reads.
	Fields []string
	// ListFilter narrows the 拉取模型 result to models this protocol can drive.
	ListFilter string
}

// Uses reports whether the protocol reads a config field. Only Model, APIKey and
// FileField are enforced by the editor and by validateVoiceModel.
func (p VoiceProtocol) Uses(field string) bool {
	for _, f := range p.Fields {
		if f == field {
			return true
		}
	}
	return false
}

// AllowsAuth reports whether the protocol accepts an auth mode.
func (p VoiceProtocol) AllowsAuth(mode string) bool {
	for _, m := range p.AuthModes {
		if m == mode {
			return true
		}
	}
	return false
}

var voiceProtocols = []VoiceProtocol{
	{
		ID:        "openai_audio",
		Label:     "OpenAI 格式（multipart 上传）",
		Kind:      VoiceKindASR,
		AuthModes: []string{"none", "bearer", "header"},
		Fields: []string{
			VoiceFieldEndpoint, VoiceFieldModel, VoiceFieldLanguage, VoiceFieldResponseFormat,
			VoiceFieldFileField, VoiceFieldExtraFields, VoiceFieldHeaders, VoiceFieldAPIKey, VoiceFieldTimeout,
		},
	},
	{
		// The stored value older configs use for the same multipart adapter.
		ID:        "multipart",
		Label:     "自定义 multipart 上传",
		Kind:      VoiceKindASR,
		AuthModes: []string{"none", "bearer", "header"},
		Fields: []string{
			VoiceFieldEndpoint, VoiceFieldModel, VoiceFieldLanguage, VoiceFieldResponseFormat,
			VoiceFieldFileField, VoiceFieldExtraFields, VoiceFieldHeaders, VoiceFieldAPIKey, VoiceFieldTimeout,
		},
	},
	{
		// Qwen3-ASR on Bailian is reached through the OpenAI-compatible
		// chat/completions endpoint with an audio content part, not through
		// /audio/transcriptions; the reply is ordinary assistant text.
		ID:         "chat_audio",
		Label:      "OpenAI 格式 chat/completions（非实时）",
		Kind:       VoiceKindASR,
		AuthModes:  []string{"bearer", "header"},
		ListFilter: VoiceListNonRealtime,
		Fields: []string{
			VoiceFieldEndpoint, VoiceFieldModel, VoiceFieldLanguage, VoiceFieldHeaders, VoiceFieldAPIKey, VoiceFieldTimeout,
		},
	},
	{
		// DashScope's native synchronous ASR shape: one POST to
		// /api/v1/services/aigc/multimodal-generation/generation with the audio
		// inline, and the text in output.output.sentence.text. Chosen
		// automatically for qwen-audio and fun-asr models.
		ID:         "dashscope_sync_audio",
		Label:      "百炼 multimodal-generation（非实时）",
		Kind:       VoiceKindASR,
		AuthModes:  []string{"bearer", "header"},
		ListFilter: VoiceListNonRealtime,
		Fields: []string{
			VoiceFieldEndpoint, VoiceFieldModel, VoiceFieldHeaders, VoiceFieldAPIKey, VoiceFieldTimeout,
		},
	},
}

// DefaultVoiceProtocol is the protocol an empty config value means.
const DefaultVoiceProtocol = "openai_audio"

// VoiceProtocolSpec looks up one protocol by its stored value.
func VoiceProtocolSpec(id string) (VoiceProtocol, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		id = DefaultVoiceProtocol
	}
	for _, p := range voiceProtocols {
		if p.ID == id {
			return p, true
		}
	}
	return VoiceProtocol{}, false
}

// VoiceProtocolLabel is the human name of a stored protocol value, falling back
// to the raw value so an unknown or hand-edited config still renders.
func VoiceProtocolLabel(id string) string {
	if p, ok := VoiceProtocolSpec(id); ok {
		return p.Label
	}
	return id
}
