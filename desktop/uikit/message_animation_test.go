package uikit

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestMessagesFadeInAndOut(t *testing.T) {
	show, close := false, false
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(ui.LightTheme())
		ui.Box(c).Fill().Background(ui.Hex("#ffffff"))
		if show {
			show = false
			c.AddToast(ui.Toast{ID: "result", Title: "保存成功", Type: "success", Timeout: -1})
		}
		if close {
			close = false
			c.CloseToast("result")
		}
		Messages(c)
	}, 400, 180)
	pixels := func() []byte { return append([]byte(nil), tt.Image().Pix...) }
	empty := pixels()
	show = true
	tt.Frame()
	start := pixels()
	tt.Advance(90 * time.Millisecond)
	middle := pixels()
	tt.Advance(200 * time.Millisecond)
	full := pixels()
	if bytes.Equal(start, full) || bytes.Equal(middle, full) || bytes.Equal(middle, empty) {
		t.Fatal("toast did not fade in over multiple frames")
	}
	close = true
	tt.Frame()
	exit := pixels()
	if bytes.Equal(exit, empty) {
		t.Fatal("last toast disappeared without exit animation")
	}
	tt.Advance(90 * time.Millisecond)
	exitMiddle := pixels()
	if bytes.Equal(exitMiddle, empty) || bytes.Equal(exitMiddle, exit) {
		t.Fatal("exit did not fade")
	}
	tt.Advance(200 * time.Millisecond)
	if !bytes.Equal(pixels(), empty) {
		t.Fatal("toast snapshot remained after fade out")
	}
}
