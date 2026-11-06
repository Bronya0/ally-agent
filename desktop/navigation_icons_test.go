// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A failed asset load must not silently fall back to the old navigation artwork.
func TestNavigationIconArtworkUsesSharedSource(t *testing.T) {
	for _, name := range []string{"skills", "models", "tasks", "plugins", "folderopen"} {
		icon := originalRailIcons[name]
		if icon == nil {
			t.Fatalf("navigation artwork %q was not loaded", name)
		}
		if iconImage(name) != icon {
			t.Fatalf("navigation artwork %q is not the shared action source", name)
		}
		if w, h := icon.Size(); w != 24 || h != 24 {
			t.Fatalf("navigation artwork %q has size %vx%v, want 24x24", name, w, h)
		}
	}
}

func TestNavigationIconArtworkRendersWithThemeInk(t *testing.T) {
	for _, appearance := range []string{"dark", "light"} {
		a := &desktop{prefs: defaultPrefs()}
		a.prefs.Appearance = appearance
		if appearance == "light" {
			a.settings.AppearanceMode = 1
		}
		theme := a.theme()
		for _, size := range []float32{sizeRailIcon, sizeRailIconCollapsed} {
			for _, name := range []string{"skills", "models", "tasks", "plugins", "folderopen"} {
				t.Run(fmt.Sprintf("%s/%s/%d", name, appearance, int(size)), func(t *testing.T) {
					ink := theme.TextMuted
					tt := ui.NewTester(func(c *ui.Context) {
						c.SetTheme(theme)
						c.DisableAnimations()
						ui.Box(c).Fill().Background(theme.Background).Children(func() {
							railIcon(c, name, size).TextColor(ink)
						})
					}, int(size), int(size))
					img := tt.Image()
					background := img.RGBAAt(0, 0)
					painted := false
					for y := 0; y < int(size); y++ {
						for x := 0; x < int(size); x++ {
							if img.RGBAAt(x, y) != background {
								painted = true
							}
						}
					}
					if !painted {
						t.Fatal("navigation icon rendered no visible strokes")
					}
					before := append([]byte(nil), img.Pix...)
					ink = theme.Accent
					tt.Frame()
					if bytes.Equal(before, tt.Image().Pix) {
						t.Fatal("navigation icon did not follow the selected theme ink")
					}
				})
			}
		}
	}
}
