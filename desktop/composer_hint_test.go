// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"image"
	"math"
	"testing"

	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo/ui"
)


func luma(r, g, b uint8) float64 {
	return 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
}

// TestComposerHintShowsWhileEmpty checks the placeholder end to end: MyGo draws
// it, in the faint tone the theme gives it, and only while the buffer is empty.
// The framework paints a placeholder itself, so it has no text node to look up;
// the check reads the pixels inside the input instead.
func TestComposerHintShowsWhileEmpty(t *testing.T) {
	a := &desktop{}
	conv := &conversation{}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Background(a.currentPalette().bg).Padding(20).Children(func() { a.composer(c, conv) })
	}, 720, 260)
	tester.Frame()
	box, ok := tester.Find("消息输入框")
	if !ok {
		t.Fatalf("the composer input is missing: %v", tester.Texts())
	}
	placeholder := a.currentPalette().hint
	empty := countInkNear(tester.Image(), box, a.currentPalette().bg, placeholder)
	if empty < 100 {
		t.Fatalf("an empty composer shows almost no hint in the placeholder tone: %d pixels", empty)
	}

	conv.Draft = "你好"
	tester.Frame()
	// Typed text is body ink; the few pixels left in the placeholder tone are
	// the glyph antialiasing on its way from ink to background.
	if typed := countInkNear(tester.Image(), box, a.currentPalette().bg, placeholder); typed*10 > empty {
		t.Fatalf("the hint stayed over typed text: %d pixels in the placeholder tone, against %d when empty", typed, empty)
	}
}

// countInkNear counts the pixels of box painted in tone, give or take the
// antialiasing that softens glyph edges.
func countInkNear(img *image.RGBA, box ui.Rect, background, tone ui.Color) int {
	bounds := img.Bounds()
	zoom := float64(bounds.Dx()) / 720
	target, behind := luma(tone.R, tone.G, tone.B), luma(background.R, background.G, background.B)
	n := 0
	for y := max(int(float64(box.Y+4)*zoom), bounds.Min.Y); y < min(int(float64(box.Y+box.H-4)*zoom), bounds.Max.Y); y++ {
		for x := max(int(float64(box.X+4)*zoom), bounds.Min.X); x < min(int(float64(box.X+box.W-4)*zoom), bounds.Max.X); x++ {
			c := img.RGBAAt(x, y)
			l := luma(c.R, c.G, c.B)
			if math.Abs(l-target) < 6 && math.Abs(l-behind) > 12 {
				n++
			}
		}
	}
	return n
}

// TestSecondaryInkKeepsPaletteTone pins the rule helpers depend on: an explicit
// ink wins over the per-appearance fallback, and that ink is the palette's own
// muted tone. Without it a leaf silently loses the preset's colour.
func TestSecondaryInkKeepsPaletteTone(t *testing.T) {
	a := &desktop{}
	a.prefs.Palette = paletteForest
	ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		if got, want := uikit.SecondaryInk(c, []ui.Color{a.secondaryInk()}), a.secondaryInk(); got != want {
			t.Errorf("explicit ink resolved to %v, want the palette's %v", got, want)
		}
	}, 200, 60)
	if a.secondaryInk() != a.currentPalette().muted {
		t.Fatal("secondary ink drifted from the palette's muted")
	}
}
