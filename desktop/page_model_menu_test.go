package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// The model menu lists provider groups only; hovering a group opens a flyout
// on its right with that group's models, and other groups' models stay hidden.
func TestModelMenuGroupsOpenFlyoutOnHover(t *testing.T) {
	a := demoDesktop()
	a.prefs.RecentModels = nil
	a.config.LastUsedModel = nil
	a.config.Models = []backend.ModelConfig{
		{ProviderName: "Alpha", Model: "alpha-1"},
		{ProviderName: "Beta", Model: "beta-1"},
		{ProviderName: "Beta", Model: "beta-2"},
	}
	st := a.current().uiState()
	st.ModelOpen = true
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	if !tt.HasText("Beta") || !tt.HasText("Alpha") {
		t.Fatal("provider groups should be listed")
	}
	if tt.HasText("beta-1") || tt.HasText("alpha-1") {
		t.Fatal("models should stay hidden until their group is hovered")
	}
	rect, ok := tt.Find("Beta")
	if !ok {
		t.Fatal("Beta group row missing")
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()
	tt.Frame()
	if st.ModelGroup != "Beta" {
		t.Fatalf("hovering Beta should open its flyout, got %q", st.ModelGroup)
	}
	if !tt.HasText("beta-1") || !tt.HasText("beta-2") {
		t.Fatal("hovered group's models should appear in the flyout")
	}
	if tt.HasText("alpha-1") {
		t.Fatal("other groups' models must not be shown")
	}
}
