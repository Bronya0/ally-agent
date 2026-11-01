// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// Hovering a provider opens its models beside it. Two rules keep that honest:
// exactly one flyout shows per frame (a quick move to the next group must not
// leave the old one stacked underneath), and the flyout survives the pointer
// travelling onto it, or no model could ever be picked.
func TestModelGroupFlyoutFollowsPointer(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), config: backend.ConfigState{Models: []backend.ModelConfig{
		{ProviderName: "alpha", Model: "a1"},
		{ProviderName: "alpha", Model: "a2"},
		{ProviderName: "beta", Model: "b1"},
	}}}
	// The Tab runs a model outside this list, so the menu shows no "最近使用"
	// block whose provider subtitles would answer Find("alpha") first.
	conv := &conversation{Model: "gamma / g1"}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Row(c).Padding(12).Children(func() { a.modelSelector(c, conv) })
	}, 640, 480)

	if err := tester.Click("gamma · g1"); err != nil {
		t.Fatalf("open the model menu: %v", err)
	}
	tester.Frame()

	alpha, ok := tester.Find("alpha")
	if !ok {
		t.Fatalf("alpha group row missing: %v", tester.Texts())
	}
	tester.Move(alpha.X+alpha.W/2, alpha.Y+alpha.H/2)
	tester.Frame()
	if !tester.HasText("a2") {
		t.Fatalf("hovering alpha did not open its models: %v", tester.Texts())
	}

	// Reaching the flyout is how a model gets picked: it must stay open once the
	// pointer leaves the row for it.
	item, ok := tester.Find("a2")
	if !ok {
		t.Fatalf("alpha flyout has no models: %v", tester.Texts())
	}
	tester.Move(item.X+item.W/2, item.Y+item.H/2)
	tester.Frame()
	if !tester.HasText("a2") {
		t.Fatal("the flyout closed when the pointer moved onto it")
	}

	// Straight to the next group: alpha must be gone in the same frame.
	beta, ok := tester.Find("beta")
	if !ok {
		t.Fatalf("beta group row missing: %v", tester.Texts())
	}
	tester.Move(beta.X+beta.W/2, beta.Y+beta.H/2)
	tester.Frame()
	if !tester.HasText("b1") {
		t.Fatalf("beta flyout did not open: %v", tester.Texts())
	}
	if tester.HasText("a2") {
		t.Fatal("alpha flyout stayed open under beta's after the pointer moved")
	}

	// And a model picked from the flyout changes the conversation's model.
	tester.Move(alpha.X+alpha.W/2, alpha.Y+alpha.H/2)
	tester.Frame()
	if err := tester.Click("a2"); err != nil {
		t.Fatalf("pick a model: %v", err)
	}
	tester.Frame()
	if conv.Model != "alpha / a2" {
		t.Fatalf("chose %q, want alpha / a2", conv.Model)
	}
	if conv.uiState().ModelOpen {
		t.Fatal("the menu stayed open after a model was chosen")
	}
}

// Escape closes the flyout even when the pointer still rests on the group row.
// Dropping the group alone was not enough: the hover reopened it on the next
// frame, so the key looked dead unless the pointer happened to be in the list.
func TestModelGroupFlyoutStaysClosedWhilePointerRestsOnRow(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), config: backend.ConfigState{Models: []backend.ModelConfig{
		{ProviderName: "alpha", Model: "a1"},
		{ProviderName: "beta", Model: "b1"},
	}}}
	conv := &conversation{Model: "gamma / g1"}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Row(c).Padding(12).Children(func() { a.modelSelector(c, conv) })
	}, 640, 480)

	if err := tester.Click("gamma · g1"); err != nil {
		t.Fatalf("open the model menu: %v", err)
	}
	tester.Frame()
	alpha, ok := tester.Find("alpha")
	if !ok {
		t.Fatalf("alpha group row missing: %v", tester.Texts())
	}
	tester.Move(alpha.X+alpha.W/2, alpha.Y+alpha.H/2)
	tester.Frame()
	if !tester.HasText("a1") {
		t.Fatalf("hovering alpha did not open its models: %v", tester.Texts())
	}

	// The pointer never leaves the row: Escape has to win over the hover.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if tester.HasText("a1") {
		t.Fatalf("Escape did not close the flyout under the pointer: %v", tester.Texts())
	}
	if st := conv.uiState(); st.ModelGroup != "" {
		t.Fatalf("the group stayed set to %q", st.ModelGroup)
	}

	// Leaving the row and coming back opens it again.
	tester.Move(5, 5)
	tester.Frame()
	alpha, ok = tester.Find("alpha")
	if !ok {
		t.Fatalf("the menu closed when the pointer left the row: %v", tester.Texts())
	}
	tester.Move(alpha.X+alpha.W/2, alpha.Y+alpha.H/2)
	tester.Frame()
	if !tester.HasText("a1") {
		t.Fatalf("hovering alpha again did not reopen it: %v", tester.Texts())
	}
}

// Escape closes the flyout when the pointer is inside it. Ignoring the panel's
// own close left the group set, so the next frame rebuilt a flyout the key had
// just dismissed.
func TestModelGroupFlyoutClosesOnEscape(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), config: backend.ConfigState{Models: []backend.ModelConfig{
		{ProviderName: "alpha", Model: "a1"},
		{ProviderName: "beta", Model: "b1"},
	}}}
	conv := &conversation{Model: "gamma / g1"}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Row(c).Padding(12).Children(func() { a.modelSelector(c, conv) })
	}, 640, 480)

	if err := tester.Click("gamma · g1"); err != nil {
		t.Fatalf("open the model menu: %v", err)
	}
	tester.Frame()
	alpha, ok := tester.Find("alpha")
	if !ok {
		t.Fatalf("alpha group row missing: %v", tester.Texts())
	}
	tester.Move(alpha.X+alpha.W/2, alpha.Y+alpha.H/2)
	tester.Frame()
	item, ok := tester.Find("a1")
	if !ok {
		t.Fatalf("hovering alpha did not open its models: %v", tester.Texts())
	}
	tester.Move(item.X+item.W/2, item.Y+item.H/2)
	tester.Frame()

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if tester.HasText("a1") {
		t.Fatalf("Escape left the flyout open: %v", tester.Texts())
	}
}

// The recents block belongs to no provider, so a flyout left open over it covers
// the very model the pointer is reaching for. Hovering the block closes the
// flyout instead of holding it open the way a group row does.
func TestModelGroupFlyoutClosesOverRecentModels(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), config: backend.ConfigState{Models: []backend.ModelConfig{
		{ProviderName: "alpha", Model: "a1"},
		{ProviderName: "alpha", Model: "a2"},
		{ProviderName: "beta", Model: "b1"},
		{ProviderName: "beta", Model: "b2"},
	}}}
	// The Tab runs a model from the list, so the menu opens with a recents block.
	conv := &conversation{Model: "alpha / a1"}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Row(c).Padding(12).Children(func() { a.modelSelector(c, conv) })
	}, 640, 480)

	if err := tester.Click("alpha · a1"); err != nil {
		t.Fatalf("open the model menu: %v", err)
	}
	tester.Frame()
	recent, ok := tester.Find("a1 alpha")
	if !ok {
		t.Fatalf("the recents block is missing: %v", tester.Texts())
	}
	beta, ok := tester.Find("beta")
	if !ok {
		t.Fatalf("beta group row missing: %v", tester.Texts())
	}
	tester.Move(beta.X+beta.W/2, beta.Y+beta.H/2)
	tester.Frame()
	if !tester.HasText("b2") {
		t.Fatalf("hovering beta did not open its models: %v", tester.Texts())
	}

	tester.Move(recent.X+recent.W/2, recent.Y+recent.H/2)
	tester.Frame()
	if tester.HasText("b2") {
		t.Fatalf("the beta flyout stayed open over the recents block: %v", tester.Texts())
	}
	if st := conv.uiState(); st.ModelGroup != "" {
		t.Fatalf("the group stayed set to %q", st.ModelGroup)
	}
}
