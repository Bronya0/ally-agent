// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"image"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// toolCardMark renders one tool card in state and finds the mark it shows, the
// dot at the left of its header.
func toolCardMark(t *testing.T, state string) (*ui.Tester, image.Rectangle) {
	t.Helper()
	a := &desktop{}
	a.prefs.Appearance = "dark"
	conv := &conversation{ID: "dot"}
	msg := map[string]any{"name": "command", "toolCallId": "dot", "args": `{"command":"echo hi"}`,
		"result": map[string]any{"stdout": "ok", "exitCode": 0}}
	if state != "" {
		msg["status"] = state
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Padding(24, 20).Children(func() { a.toolCard(c, conv, 0, msg) })
	}, 520, 140)
	r, ok := tt.Find(toolStatusLabel(state))
	if !ok {
		t.Fatalf("state %q shows no mark: %v", state, tt.Texts())
	}
	header, ok := tt.Find("工具详情 dot")
	if !ok || math.Abs(float64(r.Y+r.H/2-header.Y-header.H/2)) > 0.5 {
		t.Fatalf("state %q mark is not centered in header: mark=%+v header=%+v", state, r, header)
	}
	return tt, image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H))
}

// markTone reads the mark: how large the dot is, and which colour it draws, by
// telling its pixels from the card behind it. A grey dot differs from that
// background in brightness alone, so the colour of its hue says nothing.
func markTone(t *testing.T, tt *ui.Tester, area image.Rectangle) (w, h int, tone string) {
	t.Helper()
	img := tt.Image()
	bounds := img.Bounds()
	area = area.Intersect(bounds)
	if area.Empty() {
		t.Fatalf("the mark lies outside the window: %v", area)
	}
	bg := img.RGBAAt(area.Min.X, area.Min.Y)
	lum := func(x, y int) int {
		c := img.RGBAAt(x, y)
		return (int(c.R) + int(c.G) + int(c.B)) / 3
	}
	bgLum := (int(bg.R) + int(bg.G) + int(bg.B)) / 3
	minX, minY, maxX, maxY := 1<<30, 1<<30, -1, -1
	var sumR, sumG, sumB, n int
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if d := lum(x, y) - bgLum; d < 12 && d > -12 {
				continue
			}
			c := img.RGBAAt(x, y)
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x), max(maxY, y)
			sumR, sumG, sumB, n = sumR+int(c.R), sumG+int(c.G), sumB+int(c.B), n+1
		}
	}
	if n == 0 {
		return 0, 0, "none"
	}
	r, g, b := sumR/n, sumG/n, sumB/n
	switch spread := max(r, g, b) - min(r, g, b); {
	case spread < 14:
		tone = "grey"
	case g >= r && g >= b:
		tone = "green"
	case r >= g && r >= b:
		tone = "red"
	default:
		tone = "accent"
	}
	return maxX - minX + 1, maxY - minY + 1, tone
}

// A finished call marks itself with a green dot and a failed one with a red dot:
// the tick and cross they replace read as two shapes, while a dot the same size
// as the waiting one differs only by its colour.
func TestToolStatusMarksColouredDots(t *testing.T) {
	for _, tc := range []struct{ state, want string }{
		{"success", "green"},
		{"error", "red"},
		{"", "grey"},
		{"running", "accent"},
	} {
		tt, area := toolCardMark(t, tc.state)
		w, h, tone := markTone(t, tt, area)
		if tone != tc.want {
			t.Fatalf("state %q marks %s, want %s", tc.state, tone, tc.want)
		}
		// One size in every state: a card's line never moves as the call goes.
		if w != toolStatusDot || h != toolStatusDot {
			t.Fatalf("state %q draws a %dx%d mark, want %d", tc.state, w, h, toolStatusDot)
		}
	}
}
