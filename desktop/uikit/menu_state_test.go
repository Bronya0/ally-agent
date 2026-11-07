package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMenuButtonLockClosesAnOpenMenu(t *testing.T) {
	open, locked := true, false
	calls := 0
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		MenuButton(c.Key("locked"), &open, MenuOptions{Width: 200}, func(b ui.Element) {
			b.Label("trigger").Size(60, 28).Disabled(locked).DimDisabled(false)
		}, func(m ui.MenuParts) {
			MenuItem(c, m, "action", "action", MenuItemOptions{}).OnClick(func() { calls++ })
		})
	}, 400, 300)
	if !tt.HasText("action") {
		t.Fatal("idle menu missing")
	}
	locked = true
	tt.Frame()
	if open || tt.HasText("action") {
		t.Fatal("locking the trigger left an actionable menu visible")
	}
	tt.ClickAt(30, 14)
	tt.Key(0, ui.KeyDown)
	if open || calls != 0 {
		t.Fatal("locked menu activated")
	}
	locked = false
	tt.Frame()
	if err := tt.Click("trigger"); err != nil {
		t.Fatal(err)
	}
	if !open || !tt.HasText("action") {
		t.Fatal("menu did not recover after unlocking")
	}
}
