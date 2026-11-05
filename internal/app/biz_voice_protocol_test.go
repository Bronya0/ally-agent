// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"strings"
	"testing"
)

func TestVoiceProtocolTable(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range voiceProtocols {
		if p.ID == "" || p.Label == "" {
			t.Fatalf("protocol needs an id and a label: %+v", p)
		}
		if seen[p.ID] {
			t.Fatalf("duplicate protocol %q", p.ID)
		}
		seen[p.ID] = true
		if len(p.AuthModes) == 0 {
			t.Fatalf("protocol %q accepts no auth mode", p.ID)
		}
		if !p.Uses(VoiceFieldEndpoint) {
			t.Fatalf("protocol %q must read the endpoint", p.ID)
		}
	}
	// The multipart adapters carry a file plus optional form fields.
	for _, id := range []string{"openai_audio", "multipart"} {
		p, ok := VoiceProtocolSpec(id)
		if !ok {
			t.Fatalf("%q is missing", id)
		}
		if !p.Uses(VoiceFieldFileField) || !p.Uses(VoiceFieldExtraFields) {
			t.Fatalf("%q should accept multipart fields", id)
		}
	}
	// The inline-audio adapters post JSON, so they read a model id and headers.
	for _, id := range []string{"chat_audio", "dashscope_sync_audio"} {
		p, ok := VoiceProtocolSpec(id)
		if !ok {
			t.Fatalf("%q is missing", id)
		}
		if p.Uses(VoiceFieldFileField) || !p.Uses(VoiceFieldModel) || !p.Uses(VoiceFieldHeaders) {
			t.Fatalf("%q should be a JSON inline-audio protocol: %+v", id, p)
		}
	}
	// Vendors and modes this client cannot drive are not listed at all: an entry
	// that can only fail is worse than no entry.
	for _, gone := range []string{"tencent_asr", "xfyun_iat", "dashscope_async", "dashscope_realtime"} {
		if _, ok := VoiceProtocolSpec(gone); ok {
			t.Errorf("protocol %q must not be offered", gone)
		}
	}
	if p, ok := VoiceProtocolSpec(""); !ok || p.ID != DefaultVoiceProtocol {
		t.Fatalf("empty protocol should resolve to %q, got %+v", DefaultVoiceProtocol, p)
	}
	if _, ok := VoiceProtocolSpec("nope"); ok {
		t.Fatal("unknown protocol must not resolve")
	}
	if got := VoiceProtocolLabel("chat_audio"); !strings.Contains(got, "chat/completions") {
		t.Fatalf("label = %q, want the route name", got)
	}
	if got := VoiceProtocolLabel("hand-written"); got != "hand-written" {
		t.Fatalf("unknown label = %q, want the raw value", got)
	}
}

func TestValidateVoiceModelFollowsProtocol(t *testing.T) {
	base := VoiceModelConfig{
		ID: "voice-1", Deployment: "api", Endpoint: "https://example.com/v1/audio/transcriptions",
		Protocol: "openai_audio", AuthMode: "bearer", APIKey: "k", FileField: "file", TimeoutSeconds: 30,
	}
	if err := validateVoiceModel(normalizeVoiceModels([]VoiceModelConfig{base})[0]); err != nil {
		t.Fatalf("valid multipart model rejected: %v", err)
	}
	noKey := base
	noKey.APIKey = ""
	if err := validateVoiceModel(normalizeVoiceModels([]VoiceModelConfig{noKey})[0]); err == nil {
		t.Fatal("bearer without a key must be rejected")
	}
	badAuth := base
	badAuth.AuthMode = "tencent"
	if err := validateVoiceModel(normalizeVoiceModels([]VoiceModelConfig{badAuth})[0]); err == nil {
		t.Fatal("an auth mode outside the protocol must be rejected")
	}
	unknown := base
	unknown.Protocol = "no_such_protocol"
	if err := validateVoiceModel(normalizeVoiceModels([]VoiceModelConfig{unknown})[0]); err == nil {
		t.Fatal("unknown protocol must be rejected")
	}
}

func TestVoiceModelAuthModeDefaultsToProtocol(t *testing.T) {
	normalized := normalizeVoiceModels([]VoiceModelConfig{{
		ID: "voice-1", Protocol: "chat_audio", Endpoint: "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions",
	}})[0]
	if normalized.AuthMode != "bearer" {
		t.Fatalf("auth mode = %q, want the protocol's first mode", normalized.AuthMode)
	}
}

func TestRedactVoiceSecrets(t *testing.T) {
	model := VoiceModelConfig{APIKey: "key-123"}
	got := redactVoiceSecrets("failed with key-123", model)
	if strings.Contains(got, "key-123") {
		t.Fatalf("the key leaked: %q", got)
	}
	if got != "failed with [redacted]" {
		t.Fatalf("redacted message = %q", got)
	}
	// A model without a key must pass through untouched.
	if got := redactVoiceSecrets("plain", VoiceModelConfig{}); got != "plain" {
		t.Fatalf("keyless message = %q", got)
	}
}
