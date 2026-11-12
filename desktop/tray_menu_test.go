// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"image/color"
	"testing"

	"github.com/egoist/mygo"
)

func TestSystemTrayMenuKeepsNativeActionsAndGrouping(t *testing.T) {
	var calls [3]int
	menu, err := newSystemTrayMenu(
		func() { calls[0]++ },
		func() { calls[1]++ },
		func() { calls[2]++ },
	)
	if err != nil {
		t.Fatal(err)
	}
	items := menu.Items()
	if len(items) != 4 {
		t.Fatalf("got %d menu items, want three actions and one separator", len(items))
	}
	separator := items[2]
	if separator.Type != mygo.MenuItemSeparator || separator.Icon != nil || separator.Click != nil {
		t.Fatal("the native separator before quit must stay non-interactive and icon-free")
	}
	for action, position := range []int{0, 1, 3} {
		item := items[position]
		if item.Type != mygo.MenuItemNormal || item.Disabled || item.Hidden || item.Label == "" {
			t.Fatalf("action %d is not a visible, enabled native menu item", action)
		}
		if item.Icon == nil || item.Click == nil {
			t.Fatalf("action %d is missing its icon or callback", action)
		}
		calls = [3]int{}
		item.Click(item, nil)
		for i, count := range calls {
			want := 0
			if i == action {
				want = 1
			}
			if count != want {
				t.Fatalf("choosing action %d called action %d %d times, want %d", action, i, count, want)
			}
		}
	}
}

func TestSystemTrayMenuIconsUseSharedTemplateArtwork(t *testing.T) {
	noop := func() {}
	menu, err := newSystemTrayMenu(noop, noop, noop)
	if err != nil {
		t.Fatal(err)
	}
	items := menu.Items()
	for _, tc := range []struct {
		position int
		name     string
	}{{0, "home"}, {1, "refresh"}, {3, "power"}} {
		t.Run(tc.name, func(t *testing.T) {
			source := icons[tc.name]
			if source == nil {
				t.Fatal("shared action artwork was not loaded")
			}
			want, err := mygo.NewMenuIconSVG(source, true)
			if err != nil {
				t.Fatal(err)
			}
			got := items[tc.position].Icon.PlatformMenuIcon()
			if got == nil || !got.Template {
				t.Fatal("tray artwork must follow the native menu's text color")
			}
			if !bytes.Equal(got.PNG, want.PlatformMenuIcon().PNG) {
				t.Fatal("tray action does not use its shared SVG artwork")
			}
			// Native menu icons are 16 DIP; cover their usual display scalings
			// and the 64-pixel source MyGo keeps for high-DPI displays.
			for _, size := range []int{16, 24, 32, 64} {
				img := source.Image(size, size, color.Black)
				painted, transparent := false, false
				for y := 0; y < size; y++ {
					for x := 0; x < size; x++ {
						_, _, _, alpha := img.At(x, y).RGBA()
						painted = painted || alpha != 0
						transparent = transparent || alpha == 0
					}
				}
				if !painted || !transparent {
					t.Fatalf("%d-pixel artwork must have visible strokes on a transparent background", size)
				}
			}
		})
	}
}
