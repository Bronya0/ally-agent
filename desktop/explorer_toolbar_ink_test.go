// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"image"
	"math"
	"testing"

	"ally-dev/desktop/uikit"

	"github.com/egoist/mygo/ui"
)

func TestExplorerOpenIconUsesQuietToolbarInk(t *testing.T) {
	for _, appearance := range []string{"dark", "light"} {
		t.Run(appearance, func(t *testing.T) {
			a := &desktop{prefs: defaultPrefs()}
			a.prefs.Appearance = appearance
			if appearance == "light" {
				a.settings.AppearanceMode = 1
			}
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				a.fileTree(c)
			}, 300, 500)
			button, ok := tt.Find("在文件管理器中打开工作区")
			if !ok || button.W != 22 || button.H != 22 {
				t.Fatalf("missing compact workspace button: %v", button)
			}
			p := a.currentPalette()
			background := colChrome(a)
			ink := p.muted.Mix(p.text, 0.25)
			quietDelta := math.Abs(colorLuminance(ink) - colorLuminance(background))
			restDelta := explorerIconLuminanceDelta(tt.Image(), button, background)
			if restDelta == 0 || restDelta > quietDelta+0.005 {
				t.Fatalf("workspace icon is not using quiet toolbar ink: contrast %.3f, limit %.3f", restDelta, quietDelta)
			}

			tt.Move(button.X+button.W/2, button.Y+button.H/2)
			tt.Advance(2 * uikit.HoverDuration)
			if after, _ := tt.Find("在文件管理器中打开工作区"); after != button {
				t.Fatalf("hover moved or resized the workspace button: %v -> %v", button, after)
			}
			if hoverDelta := explorerIconLuminanceDelta(tt.Image(), button, background); hoverDelta <= restDelta+0.01 {
				t.Fatalf("hover did not emphasize the workspace icon: %.3f -> %.3f", restDelta, hoverDelta)
			}
			tt.Move(0, 0)
			tt.Advance(2 * uikit.HoverDuration)
			if after := explorerIconLuminanceDelta(tt.Image(), button, background); math.Abs(after-restDelta) > 0.005 {
				t.Fatalf("workspace icon did not return to quiet ink: %.3f -> %.3f", restDelta, after)
			}
		})
	}
}

// Sample the rendered strokes rather than SVG metadata; antialiasing may reduce
// their contrast, but idle pixels must never exceed the toolbar's quiet ink.
func explorerIconLuminanceDelta(img *image.RGBA, button ui.Rect, background ui.Color) float64 {
	base := colorLuminance(background)
	maximum := 0.0
	for y := int(math.Ceil(float64(button.Y))) + 2; y < int(math.Floor(float64(button.Y+button.H)))-2; y++ {
		for x := int(math.Ceil(float64(button.X))) + 2; x < int(math.Floor(float64(button.X+button.W)))-2; x++ {
			pixel := img.RGBAAt(x, y)
			maximum = max(maximum, math.Abs(colorLuminance(ui.RGB(pixel.R, pixel.G, pixel.B))-base))
		}
	}
	return maximum
}
