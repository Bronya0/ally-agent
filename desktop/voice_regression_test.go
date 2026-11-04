// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"testing"

	backend "ally-dev/internal/app"
)

// A new draft must already carry its category, otherwise the 类别 row in the add
// dialog renders an empty chip.
func TestNewVoiceModelDraftIsLabelledASR(t *testing.T) {
	s := &voiceModelsState{editing: -1, presetOptions: voicePresetOptions()}
	s.open(-1)
	if s.draft.Kind != backend.VoiceKindASR {
		t.Fatalf("new draft kind = %q, want asr", s.draft.Kind)
	}
	if label := backend.VoiceKindLabel(s.draft.Kind); label == "" {
		t.Fatal("the category chip would be empty")
	}
	// A second new draft (after saving the first) must be labelled too.
	s.models = append(s.models, s.draft)
	s.open(-1)
	if s.draft.Kind != backend.VoiceKindASR {
		t.Fatalf("second new draft kind = %q, want asr", s.draft.Kind)
	}
}

// The list row's derived text is rebuilt only when the fields change, so a frame
// does not allocate a provider·endpoint join per model.
func TestVoiceRowLinesAreCached(t *testing.T) {
	s := &voiceModelsState{}
	model := backend.VoiceModelConfig{ID: "v1", Provider: "SiliconFlow", Endpoint: "https://api.siliconflow.cn/v1/audio/transcriptions", Model: "SenseVoiceSmall"}
	first := s.rowLines(model)
	if first.providerLine != "SiliconFlow · https://api.siliconflow.cn/v1/audio/transcriptions" || first.modelLine != "模型：SenseVoiceSmall" {
		t.Fatalf("lines = %+v", first)
	}
	if len(s.lines) != 1 {
		t.Fatalf("cache size = %d", len(s.lines))
	}
	// Same fields: the cached struct comes back and nothing is rewritten.
	second := s.rowLines(model)
	if second.providerLine != first.providerLine || len(s.lines) != 1 {
		t.Fatalf("cache miss on unchanged fields: %+v", second)
	}
	// A changed endpoint must rebuild the line.
	model.Endpoint = "http://127.0.0.1:8786/v1/audio/transcriptions"
	if got := s.rowLines(model).providerLine; !strings.Contains(got, "127.0.0.1") {
		t.Fatalf("stale cache: %q", got)
	}
	// Clearing the model name clears the second line.
	model.Model = ""
	if got := s.rowLines(model).modelLine; got != "" {
		t.Fatalf("model line = %q, want empty", got)
	}
}

// A model file written before the ASR/TTS split has no kind; it must import as ASR
// instead of being rejected.
func TestImportedVoiceModelIsASRWithoutKind(t *testing.T) {
	const file = `{"kind":"ally-voice-models","formatVersion":1,"models":[
		{"id":"old","endpoint":"http://127.0.0.1:8786/v1/audio/transcriptions"}]}`
	models, err := parseVoiceModelFile([]byte(file))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if len(models) != 1 || models[0].Kind != backend.VoiceKindASR {
		t.Fatalf("imported kind = %+v, want asr", models)
	}
}

// Dictation only offers ASR models: a TTS model has no transcription request, so it
// must not appear in the voice input picker.
func TestVoiceModelsOfKindFiltersCategory(t *testing.T) {
	models := []backend.VoiceModelConfig{
		{ID: "asr-1", Kind: backend.VoiceKindASR},
		{ID: "tts-1", Kind: backend.VoiceKindTTS},
		{ID: "legacy"},
	}
	asr := voiceModelsOfKind(models, backend.VoiceKindASR)
	if len(asr) != 2 || asr[0].ID != "asr-1" || asr[1].ID != "legacy" {
		t.Fatalf("asr models = %+v", asr)
	}
	tts := voiceModelsOfKind(models, backend.VoiceKindTTS)
	if len(tts) != 1 || tts[0].ID != "tts-1" {
		t.Fatalf("tts models = %+v", tts)
	}
}

// 每页展示块数 lives in prefs, not ConfigState. Leaving the settings page used to
// compare only the config, so a changed page size was silently dropped.
func TestSettingsPendingEditsSeesPrefsFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	core := backend.NewApp()
	saved, err := core.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	a := &desktop{core: core, config: saved, configDraft: saved, prefs: defaultPrefs()}
	a.syncSettingsForm()
	if a.settingsPendingEdits() {
		t.Fatal("an untouched page must not report edits")
	}
	a.settings.HistoryPageSize = float64(a.messageWindowSize() + 10)
	if !a.settingsPendingEdits() {
		t.Fatal("a changed page size must count as a pending edit")
	}
}

// A model file that omits authMode must import: the backend derives the protocol
// default (bearer for chat_audio). A local copy of the defaults set it to none and
// then validation rejected the model as "bearer / header".
func TestImportedVoiceModelUsesBackendDefaults(t *testing.T) {
	const file = `{"kind":"ally-voice-models","formatVersion":1,"models":[
		{"id":"imported","protocol":"chat_audio","endpoint":"https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions","model":"qwen3-asr-flash"}]}`
	models, err := parseVoiceModelFile([]byte(file))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].AuthMode != "bearer" {
		t.Fatalf("authMode = %q, want the chat_audio default bearer", models[0].AuthMode)
	}
	// A remote endpoint must not be labelled local just because the field was
	// omitted.
	if models[0].Deployment != "api" {
		t.Fatalf("deployment = %q, want api for a remote host", models[0].Deployment)
	}
}

// The endpoint check must not survive a model change: the model chooses the route,
// so a stale pass leads straight to a bare "format is empty".
func TestVoiceEndpointShapeTracksModelAndProtocol(t *testing.T) {
	base := backend.VoiceModelConfig{
		Endpoint: "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions",
		Protocol: "chat_audio", Model: "qwen3-asr-flash", AuthMode: "bearer", APIKey: "sk-1",
	}
	if voiceEndpointShape(base) != voiceEndpointShape(base) {
		t.Fatal("identical models must share a shape")
	}
	for _, test := range []struct {
		name  string
		apply func(*backend.VoiceModelConfig)
	}{
		{"model", func(m *backend.VoiceModelConfig) { m.Model = "qwen-audio-3.0-asr-flash" }},
		{"protocol", func(m *backend.VoiceModelConfig) { m.Protocol = "dashscope_sync_audio" }},
		{"endpoint", func(m *backend.VoiceModelConfig) { m.Endpoint += "/v1/audio/transcriptions" }},
		{"key", func(m *backend.VoiceModelConfig) { m.APIKey = "sk-2" }},
	} {
		changed := base
		test.apply(&changed)
		if voiceEndpointShape(base) == voiceEndpointShape(changed) {
			t.Errorf("changing %s kept the endpoint check valid", test.name)
		}
	}
}

// A clip shorter than the useful minimum must not be sent: a mistaken tap on a
// modifier-only shortcut produces a few milliseconds of silence.
func TestVoiceClipTooShort(t *testing.T) {
	short := makeTestWAV(16000, 100) // 100 ms
	if !voiceClipTooShort(short) {
		t.Fatal("a 100 ms clip must be dropped")
	}
	ok := makeTestWAV(16000, 800) // 800 ms
	if voiceClipTooShort(ok) {
		t.Fatal("an 800 ms clip must be transcribed")
	}
}

// An empty transcript must stay empty, so no display copy can reach the draft.
func TestEmptyTranscriptNeverReachesDraft(t *testing.T) {
	draft := "已有内容"
	if got := appendVoiceTranscript(draft, "   "); got != draft {
		t.Fatalf("appendVoiceTranscript kept whitespace: %q", got)
	}
	if strings.Contains(appendVoiceTranscript("", ""), "服务已接受") {
		t.Fatal("the test-dialog placeholder leaked into the draft")
	}
}

// makeTestWAV builds a silent PCM16 mono clip of the given duration.
func makeTestWAV(rate, millis int) []byte {
	frames := rate * millis / 1000
	format := capturePCMFormat{SampleRate: uint32(rate), Channels: 1, BytesPerSample: 2, BlockAlign: 2}
	wav, err := encodeVoiceWAV(make([]byte, frames*format.BlockAlign), format)
	if err != nil {
		panic(err)
	}
	return wav
}
