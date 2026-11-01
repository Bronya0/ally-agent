// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
)

// The info bar edits the model entry, not just the Tab: a level kept only in
// the Tab was gone after a restart, because a fresh conversation re-reads the
// model config.
func TestWithModelReasoningWritesModelEntry(t *testing.T) {
	base := backend.ConfigState{Models: []backend.ModelConfig{
		{ProviderName: "alpha", Model: "one", ReasoningEffort: "low"},
		{ProviderName: "beta", Model: "two", ReasoningEffort: "high"},
	}}
	cfg, changed := withModelReasoning(base, modelOption(base.Models[1]), "max")
	if !changed {
		t.Fatal("existing model rejected the new level")
	}
	if cfg.Models[1].ReasoningEffort != "max" {
		t.Fatalf("target entry = %q, want max", cfg.Models[1].ReasoningEffort)
	}
	if cfg.Models[0].ReasoningEffort != "low" {
		t.Fatalf("unrelated entry changed to %q", cfg.Models[0].ReasoningEffort)
	}
	if base.Models[1].ReasoningEffort != "high" {
		t.Fatalf("caller's config was mutated: %q", base.Models[1].ReasoningEffort)
	}
}

func TestWithModelReasoningSkipsNoOpAndMissingModel(t *testing.T) {
	base := backend.ConfigState{Models: []backend.ModelConfig{{ProviderName: "alpha", Model: "one", ReasoningEffort: "low"}}}
	if _, changed := withModelReasoning(base, modelOption(base.Models[0]), "low"); changed {
		t.Fatal("unchanged level still requested a save")
	}
	if _, changed := withModelReasoning(base, "missing / model", "max"); changed {
		t.Fatal("missing model still requested a save")
	}
	// An entry that never carried a level already means auto, so picking auto
	// must not rewrite the config or announce a change.
	empty := backend.ConfigState{Models: []backend.ModelConfig{{ProviderName: "gamma", Model: "three"}}}
	if _, changed := withModelReasoning(empty, modelOption(empty.Models[0]), "auto"); changed {
		t.Fatal("picking auto on an entry with no level still requested a save")
	}
}
