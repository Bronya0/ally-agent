// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"image"
	"math"
	"testing"

	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo/ui"
)

const (
	toneProbeWidth  = 640
	toneProbeHeight = 200
)

// toneProbe renders one line of text in ink over the palette background and
// returns the strongest contrast it puts on screen: how loudly that tone reads.
func toneProbe(t *testing.T, a *desktop, inkOf func(c *ui.Context) ui.Color) float64 {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Background(a.currentPalette().bg).Padding(12).Children(func() {
			ui.Text(c, composerHint).FontSize(14).TextColor(inkOf(c))
		})
	}, toneProbeWidth, toneProbeHeight)
	tester.Frame()
	return inkTone(tester.Image(), a.currentPalette().bg)
}

// TestPlaceholderIsFainterThanSecondaryText pins the split this app makes: the
// theme's TextMuted carries the placeholder tone that MyGo paints empty inputs
// in, while secondary text keeps the palette's muted. The placeholder has to
// read plainer than the timestamps and counts around it, in both appearances,
// or an empty box competes with the content it sits under.
func TestPlaceholderIsFainterThanSecondaryText(t *testing.T) {
	for _, appearance := range []string{"dark", "light"} {
		for index := range palettes {
			a := &desktop{}
			a.prefs.Appearance = appearance
			a.prefs.Palette = index
			t.Run(palettes[index].name+"/"+appearance, func(t *testing.T) {
				placeholder := toneProbe(t, a, func(c *ui.Context) ui.Color { return c.Theme().TextMuted })
				secondary := toneProbe(t, a, func(c *ui.Context) ui.Color { return a.secondaryInk() })
				if placeholder < 20 {
					t.Fatalf("the placeholder is invisible (%v): the comparison means nothing", placeholder)
				}
				if placeholder >= secondary {
					t.Errorf("the placeholder reads %v against %v for secondary text: it must sit further back", placeholder, secondary)
				}
			})
		}
	}
}

// inkTone reports how far the strongest pixel strays from the colour behind it:
// the tone of the text as the eye sees it, on a light or a dark background.
func inkTone(img *image.RGBA, background ui.Color) float64 {
	bounds := img.Bounds()
	behind := luma(background.R, background.G, background.B)
	far := 0.0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := img.RGBAAt(x, y)
			far = max(far, math.Abs(luma(c.R, c.G, c.B)-behind))
		}
	}
	return far
}

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

// TestSecondaryInkKeepsPaletteTone checks the tone reaches helpers that only see
// the context: an explicit ink wins, so a leaf cannot fall back to the generic
// per-appearance value and lose the preset's own muted colour.
func TestSecondaryInkKeepsPaletteTone(t *testing.T) {
	a := &desktop{}
	a.prefs.Palette = 4 // 森林: its muted differs from the generic dark value
	theme := a.theme()
	ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		if got, want := uikit.SecondaryInk(c, []ui.Color{a.secondaryInk()}), a.secondaryInk(); got != want {
			t.Errorf("explicit ink resolved to %v, want the palette's %v", got, want)
		}
		// Without the ink, a helper can only fall back to the generic tone; the
		// callers that render real UI must pass it, which is what this pins.
		if generic := uikit.SecondaryInkOf(c.Theme()); generic != a.secondaryInk() {
			t.Logf("the generic fallback %v differs from the preset's %v, so callers must pass it", generic, a.secondaryInk())
		}
	}, 200, 60)
	if a.secondaryInk() != a.currentPalette().muted {
		t.Fatal("secondary ink drifted from the palette's muted")
	}
}
