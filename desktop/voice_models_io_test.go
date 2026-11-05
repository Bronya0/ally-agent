// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"testing"
)

func TestParseVoiceModelFile(t *testing.T) {
	good := `{"kind":"ally-voice-models","formatVersion":1,"models":[{"id":"v1","name":"Local"}]}`
	models, err := parseVoiceModelFile([]byte(good))
	if err != nil || len(models) != 1 || models[0].Protocol != "openai_audio" || models[0].TimeoutSeconds != 30 {
		t.Fatalf("parse: %+v, %v", models, err)
	}
	unnamed := `{"kind":"ally-voice-models","formatVersion":1,"models":[{"id":"v2"}]}`
	if models, err := parseVoiceModelFile([]byte(unnamed)); err != nil || len(models) != 1 || models[0].Name != "" {
		t.Fatalf("display name should be optional in imported config: %+v, %v", models, err)
	}
	duplicate := `{"kind":"ally-voice-models","formatVersion":1,"models":[{"id":"v1","name":"A"},{"id":"v1","name":"B"}]}`
	if _, err := parseVoiceModelFile([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "不能重复") {
		t.Fatalf("expected duplicate rejection: %v", err)
	}
	if _, err := parseVoiceModelFile([]byte(`{"kind":"ally-chat-models","formatVersion":1,"models":[]}`)); err == nil {
		t.Fatal("accepted unrelated model file")
	}
}
