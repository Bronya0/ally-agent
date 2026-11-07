package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMenuButtonKeyboardSkipsDisabledAndRestoresFocus(t *testing.T) {
	var open bool
	var trigger ui.Handle
	chosen := 0
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		ui.Column(c).Fill().Padding(12).Children(func() {
			MenuButton(c.Key("actions"), &open, MenuOptions{Width: 200}, func(b ui.Element) {
				b.Bind(&trigger).Label("actions").Size(60, 28).Children(func() { ui.Text(c, "actions") })
			}, func(m ui.MenuParts) {
				MenuItem(c, m, "first", "first", MenuItemOptions{})
				MenuItem(c, m, "disabled", "disabled", MenuItemOptions{}).Disabled(true).OnClick(func() { t.Fatal("disabled item activated") })
				MenuItem(c, m, "last", "last", MenuItemOptions{}).OnClick(func() { chosen++ })
			})
		})
	}, 400, 300)
	if err := tt.Click("actions"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if open || chosen != 1 || tt.HasText("last") {
		t.Fatalf("keyboard choice did not close exactly once: open=%v chosen=%d", open, chosen)
	}
	// Focus returned to the trigger, so Down can open it again.
	tt.Key(0, ui.KeyDown)
	if !open || !tt.HasText("first") {
		t.Fatal("closing the menu lost its trigger's focus")
	}
	tt.Key(0, ui.KeyEscape)
	if open || chosen != 1 {
		t.Fatal("Escape changed the choice or left the menu open")
	}
}

func TestMenuButtonExternalOpenAndStableItemIdentity(t *testing.T) {
	open := true
	order := []string{"alpha", "beta"}
	chosen := ""
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		MenuButton(c.Key("items"), &open, MenuOptions{Width: 200}, func(b ui.Element) { b.Label("items").Size(60, 28) }, func(m ui.MenuParts) {
			for _, id := range order {
				id := id
				MenuItem(c, m, id, id, MenuItemOptions{}).OnClick(func() { chosen = id })
			}
		})
	}, 400, 300)
	if !tt.HasText("beta") {
		t.Fatal("externally opened menu did not show")
	}
	order = []string{"beta", "alpha"}
	tt.Frame()
	if err := tt.Click("beta"); err != nil {
		t.Fatal(err)
	}
	if chosen != "beta" || open {
		t.Fatalf("reordered menu chose %q, open=%v", chosen, open)
	}
	open = true
	tt.Frame()
	open = false
	tt.Frame()
	if tt.HasText("beta") {
		t.Fatal("external close did not remove the menu")
	}
}

func TestMenuButtonPlacementKeepsGapAndFitsWindow(t *testing.T) {
	for _, width := range []float32{280, 700} {
		open := true
		tt := ui.NewTester(func(c *ui.Context) {
			c.DisableAnimations()
			ui.Box(c).Absolute().Left(width - 40).Bottom(20).Children(func() {
				MenuButton(c.Key("above"), &open, MenuOptions{Width: 300, At: ui.AnchorTopRight, Self: ui.AnchorBottomRight, Gap: 6, Label: "popup"}, func(b ui.Element) {
					b.Size(26, 26).Label("trigger")
				}, func(m ui.MenuParts) {
					MenuItem(c, m, "long", "较长的中文菜单名称必须完整布局或截断", MenuItemOptions{Subtitle: "中文路径与说明"})
				})
			})
		}, int(width), 400)
		panel, found := tt.Find("popup")
		trigger, triggerFound := tt.Find("trigger")
		if !found || !triggerFound || panel.X < 0 || panel.X+panel.W > width || panel.Y < 0 || panel.Y+panel.H > trigger.Y-5.9 {
			t.Fatalf("menu exceeded the window or lost its upward gap: panel=%+v trigger=%+v", panel, trigger)
		}
	}
}

func TestHoverMenusKeyboardChoosesChildAndClosesOnlyChild(t *testing.T) {
	open := true
	chosen := 0
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		MenuButton(c.Key("groups"), &open, MenuOptions{Width: 200}, func(b ui.Element) { b.Size(50, 28).Label("groups") }, func(m ui.MenuParts) {
			body := ui.Column(c).FillWidth()
			h := NewHoverMenus(body)
			body.Children(func() {
				row := MenuItem(c, m, "provider", "provider", MenuItemOptions{})
				h.Item(c, "provider", row)
				h.Popup(c, body, func(string) ui.Element { return row }, MenuOptions{Width: 160}, func(_ string, child ui.MenuParts) {
					MenuItem(c, child, "one", "one", MenuItemOptions{})
					MenuItem(c, child, "two", "two", MenuItemOptions{}).OnClick(func() { chosen++ })
				})
			})
		})
	}, 500, 400)
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyEnter)
	if !tt.HasText("one") || !open {
		t.Fatal("keyboard activation did not open the child and keep its parent")
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if chosen != 1 || !open || tt.HasText("two") {
		t.Fatalf("child activation or dismissal failed: chosen=%d parent=%v", chosen, open)
	}
}
