package desktop

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestThinkingReducedMotionStaysStill(t *testing.T) {
	a := &desktop{}
	tt := ui.NewTester(func(c *ui.Context) { a.runSpinner(c) }, 80, 40)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()
	before := append([]byte(nil), tt.Image().Pix...)
	tt.Advance(time.Second)
	if !bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("reduced motion continued animating")
	}
	if tt.FramePending() {
		t.Fatal("reduced motion keeps requesting frames")
	}
}

func TestThinkingBarPreservesSubpixelMotion(t *testing.T) {
	height := float32(12.2)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Width(15).Height(16).Draw(func(p *ui.Painter, r ui.Rect) {
			drawThinkingBar(p, ui.Rect{X: r.X, Y: r.Y + (16-height)/2, W: 3, H: height}, ui.RGB(80, 80, 80))
		})
	}, 80, 40)
	tt.Frame()
	before := append([]byte(nil), tt.Image().Pix...)
	// Both heights round to the same integer top and bottom edges with Fill.
	height = 12.7
	tt.Frame()
	if bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("subpixel height change did not change bar coverage")
	}
}
