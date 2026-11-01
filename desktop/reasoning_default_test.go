// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
)

// An entry with no level reads as "auto", so an unrelated model edit compares
// equal and never resets a Tab's own pick.
func TestSavedAndPersistedReasoningTreatEmptyAsAuto(t *testing.T) {
	cfg := backend.ConfigState{Models: []backend.ModelConfig{
		{ProviderName: "alpha", Model: "one"},
		{ProviderName: "beta", Model: "two", ReasoningEffort: "high"},
	}}
	if got := savedReasoning(cfg, modelIdentity(cfg.Models[0])); got != "auto" {
		t.Fatalf("empty entry = %q, want auto", got)
	}
	if got := savedReasoning(cfg, modelIdentity(cfg.Models[1])); got != "high" {
		t.Fatalf("explicit entry = %q, want high", got)
	}
	if got := persistedReasoning(cfg, modelOption(cfg.Models[0])); got != "auto" {
		t.Fatalf("persisted empty = %q, want auto", got)
	}
	if got := persistedReasoning(cfg, "gone / model"); got != "auto" {
		t.Fatalf("missing model = %q, want auto", got)
	}
	// Duplicate entries must not make the comparison read a different entry than
	// the write does, so the first match wins everywhere.
	dup := backend.ConfigState{Models: []backend.ModelConfig{
		{ProviderName: "alpha", Model: "one", ReasoningEffort: "high"},
		{ProviderName: "alpha", Model: "one", ReasoningEffort: "low"},
	}}
	if got := savedReasoning(dup, modelIdentity(dup.Models[0])); got != "high" {
		t.Fatalf("duplicate entries = %q, want the first entry high", got)
	}
}
