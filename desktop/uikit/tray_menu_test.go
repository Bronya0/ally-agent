package uikit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTrayMenuMaxWidthUpdates(t *testing.T) {
	open := true
	limit := float32(0)
	entries := []TrayMenuEntry{{Key: "long", Label: strings.Repeat("Wide label ", 30)}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(TrayMenuTheme(""))
		TrayMenu(c.Key("menu"), &open, "Menu", entries, TrayMenuOptions{MaxWidth: limit})
	}, 1000, 200)
	for _, tc := range []struct{ limit, want float32 }{{0, 480}, {640, 640}, {200, 200}, {50, 140}, {0, 480}} {
		limit = tc.limit
		tt.Frame()
		node, found := tt.AccessByRole(ui.RoleMenu, "Menu")
		if !found || node.Bounds.W != tc.want {
			t.Fatalf("max width %v: got %v, want %v", limit, node.Bounds.W, tc.want)
		}
	}
}

func TestTrayMenuReusableLabelsAndDisabledItems(t *testing.T) {
	open, choice := true, -1
	entries := []TrayMenuEntry{
		{Key: "disabled", Label: "Unavailable", Disabled: true},
		{Key: "action", Label: "Perform action", SeparatorBefore: true},
	}
	w, h := TrayMenuInitialSize(len(entries), 1)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(TrayMenuTheme("").WithPreferences(c.Preferences()))
		if got := TrayMenu(c.Key("menu"), &open, "Another application", entries); got >= 0 {
			choice = got
		}
	}, w, h)
	if _, found := tt.AccessByRole(ui.RoleMenu, "Another application"); !found {
		t.Fatal("menu did not expose the caller's accessible name")
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyEnter)
	if open || choice != 1 {
		t.Fatalf("keyboard should skip disabled entry: open=%v choice=%d", open, choice)
	}
}
