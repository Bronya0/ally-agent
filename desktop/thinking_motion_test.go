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
