package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func TestModelMenuSearchRefreshesChangedMetadata(t *testing.T) {
	a := &desktop{config: backend.ConfigState{Models: []backend.ModelConfig{
		{ProviderName: "alpha", Model: "a1", DisplayName: "中文模型"},
		{ProviderName: "beta", Model: "b1", DisplayName: "English model"},
	}}}
	conv := &conversation{Model: "outside / model"}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.modelSelector(c, conv)
	}, 640, 480)
	if err := tt.Click("outside · model"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("搜索模型或供应商"); err != nil {
		t.Fatal(err)
	}
	tt.Type("中文")
	if _, ok := tt.Find("alpha"); !ok {
		t.Fatal("search missed the model's display name")
	}
	if _, ok := tt.Find("beta"); ok {
		t.Fatal("search left an unrelated provider visible")
	}
	// Same length and same slice: editing a label must invalidate cached metadata.
	a.config.Models[0].DisplayName = "renamed"
	tt.Frame()
	if _, ok := tt.Find("alpha"); ok {
		t.Fatal("search used the old display name after a configuration edit")
	}
	tt.Key(0, ui.KeyEscape)
	if conv.uiState().ModelOpen {
		t.Fatal("Escape failed with the search field focused")
	}
	if err := tt.Click("outside · model"); err != nil {
		t.Fatal(err)
	}
	if conv.uiState().ModelSearch != "" {
		t.Fatal("reopening kept a previous search")
	}
	if _, ok := tt.Find("beta"); !ok {
		t.Fatal("reopening did not restore all providers")
	}
}

func TestModelMenuReopensWithoutPreviousChild(t *testing.T) {
	a := &desktop{config: backend.ConfigState{Models: []backend.ModelConfig{{ProviderName: "alpha", Model: "a1"}}}}
	conv := &conversation{Model: "outside / model"}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.modelSelector(c, conv)
	}, 640, 480)
	if err := tt.Click("outside · model"); err != nil {
		t.Fatal(err)
	}
	row, ok := tt.Find("alpha")
	if !ok {
		t.Fatal("provider missing")
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	if !tt.HasText("a1") {
		t.Fatal("hover did not open the provider")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyEscape)
	if conv.uiState().ModelOpen {
		t.Fatal("second Escape did not close the parent")
	}
	if err := tt.Click("outside · model"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("a1") {
		t.Fatal("reopening revived the previous hover submenu")
	}
}

func TestModelMenuKeyboardSelectsProviderModel(t *testing.T) {
	a := &desktop{config: backend.ConfigState{Models: []backend.ModelConfig{
		{ProviderName: "alpha", Model: "a1"}, {ProviderName: "alpha", Model: "a2", ReasoningEffort: "high"},
	}}}
	conv := &conversation{Model: "outside / model"}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.modelSelector(c, conv)
	}, 640, 480)
	if err := tt.Click("outside · model"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyEnter)
	if !tt.HasText("a2") {
		t.Fatal("keyboard did not enter the provider")
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if conv.Model != "alpha / a2" || conv.Reasoning != "high" || conv.uiState().ModelOpen {
		t.Fatalf("keyboard chose the wrong model or left the parent open: model=%q reasoning=%q open=%v", conv.Model, conv.Reasoning, conv.uiState().ModelOpen)
	}
}

func TestModelMenuCatalogKeepsOAuthIdentitiesDistinct(t *testing.T) {
	models := []backend.ModelConfig{
		{ProviderName: "provider", Model: "same", DisplayName: "label", OAuthAccountID: "one"},
		{ProviderName: "provider", Model: "same", DisplayName: "label", OAuthAccountID: "two"},
	}
	var catalog modelMenuCatalog
	catalog.refresh(models)
	if catalog.entries[0].option == catalog.entries[1].option {
		t.Fatal("two OAuth accounts became one menu identity")
	}
	models[0].OAuthAccountID = "three"
	models[0].DisplayName = "changed"
	catalog.refresh(models)
	if catalog.entries[0].identity.OAuthAccountID != "three" || catalog.entries[0].name != "changed" || catalog.entries[0].option != modelOption(models[0]) {
		t.Fatal("menu metadata did not follow a same-size configuration edit")
	}
}
