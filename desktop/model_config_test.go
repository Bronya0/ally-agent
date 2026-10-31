package desktop

import (
	backend "ally-dev/internal/app"
	"testing"
)

func TestModelEditorPreservesExplicitTokenParameter(t *testing.T) {
	for _, param := range []string{"auto", "max_tokens", "max_completion_tokens"} {
		model := backend.ModelConfig{ProviderName: "custom", Model: "gpt-5", BaseURL: "https://example.invalid/v1", TokenParam: param, MaxTokens: 8192, ContextWindow: 128000}
		a := &desktop{config: backend.ConfigState{Models: []backend.ModelConfig{model}}}
		a.editModel(0)
		got, err := a.preparedModelDraft()
		if err != nil {
			t.Fatal(err)
		}
		if got.TokenParam != param || got.MaxTokens != model.MaxTokens || got.ContextWindow != model.ContextWindow {
			t.Fatalf("editing changed request parameters: %+v", got)
		}
	}
}
