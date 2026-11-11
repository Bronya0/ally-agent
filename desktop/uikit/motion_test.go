package uikit

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestActionHoverFadesWithoutMoving(t *testing.T) {
	tt := chineseTester(func(c *ui.Context) {
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

// The hover fade now runs as a pose, painted in frames that only repaint: it
// has to interpolate all the same, settle at its target, and settle at once
// when the desktop asks for less motion.
func TestHoverFadeSettles(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		tt := chineseTester(func(c *ui.Context) {
			c.SetTheme(ui.LightTheme())
			ui.Column(c).Fill().Padding(20).Background(ui.Hex("#ffffff")).Children(func() {
				row := ui.Row(c.Key("row")).Size(80, 40).Label("悬停行")
				show := row.Hovered()
				opacity := float32(0)
				if show {
					opacity = 1
				}
				row.Children(func() {
					HoverFade(c, ui.Box(c.Key("fade")).Fill().Background(ui.Hex("#000000")), opacity)
				})
			})
		}, 160, 120)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduced, TextScale: 1})
		tt.Frame()
		before := append([]byte(nil), tt.Image().Pix...)
		r, _ := tt.Find("悬停行")
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Advance(70 * time.Millisecond)
		mid := append([]byte(nil), tt.Image().Pix...)
		tt.Advance(300 * time.Millisecond)
		after := append([]byte(nil), tt.Image().Pix...)
		switch {
		case bytes.Equal(before, after):
			t.Fatalf("reduced=%v: the box never showed", reduced)
		case reduced && !bytes.Equal(mid, after):
			t.Fatalf("reduced=%v: reduced motion faded instead of settling at once", reduced)
		case !reduced && (bytes.Equal(before, mid) || bytes.Equal(mid, after)):
			t.Fatalf("reduced=%v: hover did not interpolate", reduced)
		}
	}
}

func TestRevealAndReducedMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		open := true
		tt := chineseTester(func(c *ui.Context) {
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

func TestRevealClosingContentIsInertAndReleased(t *testing.T) {
	open := true
	clicks, builds := 0, 0
	tt := chineseTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			Reveal(c.Key("details"), open, func() {
				builds++
				ui.Button(c.Key("inner"), "inner").OnClick(func() { clicks++ })
			})
		})
	}, 240, 160)
	if err := tt.Click("inner"); err != nil {
		t.Fatal(err)
	}
	r, _ := tt.Find("inner")
	open = false
	tt.Frame()
	if _, ok := tt.Access("inner"); ok {
		t.Fatal("closing content remains accessible")
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	if clicks != 1 {
		t.Fatal("closing content accepted a click")
	}
	tt.Advance(300 * time.Millisecond)
	settledBuilds := builds
	tt.Frame()
	if builds != settledBuilds {
		t.Fatal("collapsed content was rebuilt")
	}
	open = true
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if err := tt.Click("inner"); err != nil || clicks != 2 {
		t.Fatalf("reopened content did not respond: clicks=%d err=%v", clicks, err)
	}
}

func TestFadeRevealRepaintsAndReleasesContent(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		open, builds, contentBuilds := false, 0, 0
		tt := chineseTester(func(c *ui.Context) {
			builds++
			ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
				FadeReveal(c.Key("fade"), open, func() {
					contentBuilds++
					ui.Box(c).Size(100, 80).Background(ui.Hex("#000000"))
					ui.Button(c.Key("action"), "action")
				})
			})
		}, 240, 180)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduced, TextScale: 1})
		tt.Frame()
		pixels := func() []byte { return append([]byte(nil), tt.Image().Pix...) }
		empty := pixels()
		open = true
		tt.Frame()
		before := builds
		tt.Advance(100 * time.Millisecond)
		mid := pixels()
		tt.Advance(200 * time.Millisecond)
		full := pixels()
		if bytes.Equal(empty, full) || (reduced && !bytes.Equal(mid, full)) || (!reduced && (bytes.Equal(mid, full) || bytes.Equal(mid, empty))) {
			t.Fatalf("reduced=%t: fade in did not settle correctly", reduced)
		}
		if builds > before+2 {
			t.Fatalf("reduced=%t: fade rebuilt the view %d times", reduced, builds-before)
		}
		open = false
		tt.Frame()
		if _, ok := tt.Access("action"); ok {
			t.Fatal("closing content is accessible")
		}
		tt.Advance(100 * time.Millisecond)
		closing := pixels()
		if !reduced && (bytes.Equal(closing, full) || bytes.Equal(closing, empty)) {
			t.Fatal("fade out skipped its intermediate frame")
		}
		tt.Advance(200 * time.Millisecond)
		if !bytes.Equal(pixels(), empty) {
			t.Fatal("closed content remained visible")
		}
		settled := contentBuilds
		tt.Frame()
		if settled != contentBuilds {
			t.Fatal("closed content was rebuilt")
		}
	}
}

func TestFadeRevealRapidToggleStillReleasesContent(t *testing.T) {
	open, builds, frames := true, 0, 0
	tt := chineseTester(func(c *ui.Context) {
		frames++
		FadeReveal(c.Key("fade"), open, func() { builds++; ui.Box(c).Size(40, 40) })
	}, 80, 80)
	open = false
	tt.Frame()
	tt.Advance(50 * time.Millisecond)
	open = true
	tt.Frame()
	tt.Advance(50 * time.Millisecond)
	open = false
	tt.Frame()
	before := frames
	tt.Advance(300 * time.Millisecond)
	if frames <= before {
		t.Fatal("no frame released the content after rapid toggles")
	}
	settled := builds
	tt.Frame()
	if builds != settled {
		t.Fatal("rapid toggles left closed content mounted")
	}
}
