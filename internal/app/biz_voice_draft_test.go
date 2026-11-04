// SPDX-License-Identifier: GPL-3.0-only
package app

import "testing"

// A model without an ID must not crash the transcription entry point: normalize
// drops entries without an ID, so indexing [0] panicked before this guard.
func TestTranscribeWithoutIDDoesNotPanic(t *testing.T) {
	app := configTransactionApp(t)
	model := VoiceModelConfig{
		Protocol: "openai_audio", Endpoint: "http://127.0.0.1:1/v1/audio/transcriptions",
		FileField: "file", AuthMode: "none", TimeoutSeconds: 1,
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("transcribing an unnamed draft panicked: %v", r)
		}
	}()
	if _, err := app.TestNativeVoiceModelAudio(model, makeSilenceWAV(4000, 16000)); err == nil {
		t.Fatal("an unreachable endpoint must still fail")
	}
}
