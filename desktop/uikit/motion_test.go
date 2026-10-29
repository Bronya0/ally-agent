package uikit

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestActionHoverFadesWithoutMoving(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(ui.LightTheme())
		ui.Column(c).Fill().Padding(20).Background(ui.Hex("#ffffff")).Children(func() { Action(c.Key("action"), "测试", ActionOptions{Style: Quaternary}) })
	}, 240, 120)
	r, _ := tt.Find("测试")
	before := append([]byte(nil), tt.Image().Pix...)
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(70 * time.Millisecond)
	mid := append([]byte(nil), tt.Image().Pix...)
	tt.Advance(200 * time.Millisecond)
	after := tt.Image().Pix
	if bytes.Equal(before, mid) || bytes.Equal(mid, after) {
		t.Fatal("hover did not interpolate")
	}
	bounds, _ := tt.Find("测试")
	if bounds != r {
		t.Fatal("hover moved button")
	}
}

func TestRevealAndReducedMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		open := true
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() {
				Reveal(c.Key("details"), open, func() { ui.Box(c).Height(80).FillWidth() })
				ui.Text(c, "后续内容").Label("following")
			})
		}, 240, 200)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduced, TextScale: 1})
		tt.Frame()
		start, _ := tt.Find("following")
		open = false
		tt.Frame()
		tt.Advance(100 * time.Millisecond)
		mid, _ := tt.Find("following")
		tt.Advance(200 * time.Millisecond)
		end, _ := tt.Find("following")
		if end.Y >= start.Y {
			t.Fatal("panel did not collapse")
		}
		if reduced {
			if mid.Y != end.Y {
				t.Fatal("reduced motion animated")
			}
		} else if mid.Y <= end.Y || mid.Y >= start.Y {
			t.Fatal("panel skipped intermediate height")
		}
	}
}
