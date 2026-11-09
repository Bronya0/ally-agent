package desktop

import (
	backend "ally-dev/internal/app"
	"slices"
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

// The editor's options must cover every format the app stores and shows: a value
// outside that list leaves the field reading as a raw identifier and hides the
// settings that depend on the format.
func TestModelEditorListsEveryStoredAPIFormat(t *testing.T) {
	labels := make(map[string]string, len(apiFormats))
	for _, format := range apiFormats {
		label := apiFormatLabel(format)
		if label == format {
			t.Fatalf("%q has no display name", format)
		}
		if other, dup := labels[label]; dup {
			t.Fatalf("%q and %q share the label %q", other, format, label)
		}
		labels[label] = format
	}
	for _, provider := range availableModelCatalog().Providers {
		if !slices.Contains(apiFormats, provider.APIFormat) {
			t.Fatalf("catalog provider %q uses %q, which the editor cannot show", provider.Name, provider.APIFormat)
		}
	}
}
