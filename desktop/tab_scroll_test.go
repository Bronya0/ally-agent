package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

var tabStripLabels = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet"}

func TestTabStripConsumesPanReleaseOnce(t *testing.T) {
	s := &tabStrip{}
	s.scroll.MaxX = 300
	s.gesture.Panning, s.gesture.Released = true, true
	selected := 1
	if s.afterTabs(&selected, 0, true) || selected != 0 {
		t.Fatal("pan release selected a tab")
	}
	// A later keyboard selection has no new pointer press to reset the gesture.
	selected = 1
	if !s.afterTabs(&selected, 0, true) || selected != 1 {
		t.Fatal("stale pan release swallowed a later selection")
	}
}

func newTabStripTester(a *desktop, selected *int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Padding(10).FillWidth().Children(func() {
			a.scrollSubTabs(c, "test-strip", selected, tabStripLabels...)
		})
	}, 300, 120)
}

// The tab strip scrolls only sideways, and MyGo's wheel turns the axis its
// notches point at: a plain wheel over the strip used to do nothing at all
// unless Shift was held.
func TestTabStripWheelScrollsWithoutShift(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	selected := 0
	tt := newTabStripTester(a, &selected)
	st := &a.tabStripOf("test-strip").scroll
	if st.MaxX <= 0 {
		t.Fatalf("the strip did not overflow: maxX=%v", st.MaxX)
	}
	tt.Wheel(150, 24, 0, 40)
	tt.Advance(2 * time.Second)
	// A notch goes tabWheelStep times as far, or the strip crawls.
	if want := float32(40 * tabWheelStep); st.X != want {
		t.Fatalf("a notch of 40 moved the strip to %v, want %v", st.X, want)
	}
	before := st.X
	tt.Wheel(150, 24, 0, -40)
	tt.Advance(2 * time.Second)
	if st.X >= before {
		t.Fatalf("the wheel up did not scroll back: %v then %v", before, st.X)
	}
	if st.X < 0 {
		t.Fatalf("the strip scrolled past its start: %v", st.X)
	}
}

// At either edge the strip leaves the wheel to the page around it, so a strip
// that fits, or one already at its end, never swallows the page's scrolling.
func TestTabStripLeavesTheWheelToThePageAtItsEdges(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	selected := 0
	page := &ui.ScrollState{}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Scroll(c.Key("page")).TrackScroll(page).Fill().Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				ui.Box(c).Height(40)
				a.scrollSubTabs(c, "test-strip", &selected, tabStripLabels...)
				ui.Box(c).Height(600)
			})
		})
	}, 300, 200)
	st := &a.tabStripOf("test-strip").scroll
	if st.MaxX <= 0 {
		t.Fatalf("the strip did not overflow: maxX=%v", st.MaxX)
	}
	tt.Wheel(150, 55, 0, 30) // over the strip, which starts at its beginning
	tt.Advance(2 * time.Second)
	if st.X <= 0 {
		t.Fatalf("the strip did not scroll: %v", st.X)
	}
	if page.Y != 0 {
		t.Fatalf("the strip took the whole wheel: page at %v", page.Y)
	}
	for i := 0; i < 20; i++ {
		tt.Wheel(150, 55, 0, 120)
		tt.Advance(time.Second)
	}
	if st.X < st.MaxX {
		t.Fatalf("the strip never reached its end: %v of %v", st.X, st.MaxX)
	}
	tt.Wheel(150, 55, 0, 40) // now the page takes it
	tt.Advance(2 * time.Second)
	if page.Y <= 0 {
		t.Fatalf("the page did not scroll once the strip ended: %v", page.Y)
	}
}

// A press on the tabs drags the strip along with the pointer, as a touchpad's
// fingers do: the wheel alone is too slow to reach the tabs past the edge.
func TestTabStripDragPansIt(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	selected := 0
	tt := newTabStripTester(a, &selected)
	st := &a.tabStripOf("test-strip").scroll
	if st.MaxX <= 0 {
		t.Fatalf("the strip did not overflow: maxX=%v", st.MaxX)
	}
	tt.Press(150, 24)
	for i := 1; i <= 6; i++ {
		tt.Move(150-float32(i*20), 24)
		tt.Advance(100 * time.Millisecond)
	}
	// The tabs follow the pointer, which moved 120 left: the strip shows what
	// was past its right edge.
	if want := float32(120); st.X != want {
		t.Fatalf("dragging 120 left moved the strip to %v, want %v", st.X, want)
	}
	if selected != 0 {
		t.Fatalf("the pan chose tab %d", selected)
	}
	tt.Release(30, 24)
	tt.Advance(200 * time.Millisecond)
	if selected != 0 {
		t.Fatalf("letting a pan go chose tab %d", selected)
	}
	if st.X < 120 {
		t.Fatalf("letting go moved the strip back to %v", st.X)
	}
	tt.Press(150, 24)
	for i := 1; i <= 6; i++ {
		tt.Move(150+float32(i*30), 24)
		tt.Advance(100 * time.Millisecond)
	}
	if st.X != 0 {
		t.Fatalf("dragging right stopped at %v, want the start", st.X)
	}
	tt.Release(330, 24)
}

// A press that moves the strip pans it, and the tab it lets go over is not
// chosen, though a click there would choose it.
func TestTabStripDragDoesNotChooseTheTabItEndsOn(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	selected, chosen := 0, false
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Padding(10).FillWidth().Children(func() {
			chosen = chosen || a.scrollSubTabs(c, "test-strip", &selected, tabStripLabels...)
		})
	}, 300, 120)
	st := &a.tabStripOf("test-strip").scroll
	delta, ok := tt.Find("delta")
	if !ok {
		t.Fatalf("missing tab: %v", tt.Texts())
	}
	x, y := delta.X+delta.W/2, delta.Y+delta.H/2
	tt.Press(x, y)
	for i := 1; i <= 4; i++ {
		tt.Move(x-float32(i*15), y)
		tt.Advance(100 * time.Millisecond)
	}
	if st.X != 60 {
		t.Fatalf("dragging 60 left moved the strip to %v", st.X)
	}
	over := tabUnder(tt, x, y)
	if over == 0 {
		t.Fatalf("the strip did not move a tab under the pointer: %v", tt.Texts())
	}
	tt.Release(x, y)
	tt.Advance(200 * time.Millisecond)
	if selected != 0 {
		t.Fatalf("letting the pan go chose tab %d, which a click there would", selected)
	}
	if chosen {
		t.Fatalf("the strip reported a choice the pan ended with")
	}
}

// The press after a pan is a click again: the pan's state does not carry over
// to it.
func TestTabStripClickAfterPanChooses(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	selected := 0
	tt := newTabStripTester(a, &selected)
	golf, ok := tt.Find("golf")
	if !ok {
		t.Fatalf("missing tab: %v", tt.Texts())
	}
	tt.Press(golf.X+golf.W/2, golf.Y+golf.H/2)
	for i := 1; i <= 4; i++ {
		tt.Move(golf.X+golf.W/2-float32(i*15), golf.Y+golf.H/2)
		tt.Advance(100 * time.Millisecond)
	}
	tt.Release(golf.X+golf.W/2-60, golf.Y+golf.H/2)
	tt.Advance(200 * time.Millisecond)
	if selected != 0 {
		t.Fatalf("the pan chose tab %d", selected)
	}
	bravo, ok := tt.Find("bravo")
	if !ok {
		t.Fatalf("missing tab after the pan: %v", tt.Texts())
	}
	tt.Press(bravo.X+bravo.W/2, bravo.Y+bravo.H/2)
	tt.Release(bravo.X+bravo.W/2, bravo.Y+bravo.H/2)
	tt.Advance(200 * time.Millisecond)
	if selected != 1 {
		t.Fatalf("the click after a pan chose tab %d, want 1", selected)
	}
}

// A pan shorter than a tab keeps the pointer on the tab it pressed, so the tab
// reports a click as it lets go: the strip drops it, as the press moved.
func TestTabStripShortPanDropsItsClick(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	selected, chosen := 0, false
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Padding(10).FillWidth().Children(func() {
			chosen = chosen || a.scrollSubTabs(c, "test-strip", &selected, tabStripLabels...)
		})
	}, 300, 120)
	st := &a.tabStripOf("test-strip").scroll
	charlie, ok := tt.Find("charlie")
	if !ok {
		t.Fatalf("missing tab: %v", tt.Texts())
	}
	x, y := charlie.X+charlie.W/2, charlie.Y+charlie.H/2
	tt.Press(x, y)
	tt.Move(x-10, y)
	tt.Advance(100 * time.Millisecond)
	if st.X != 10 {
		t.Fatalf("a short drag panned the strip to %v", st.X)
	}
	tt.Release(x-10, y)
	tt.Advance(200 * time.Millisecond)
	if selected != 0 || chosen {
		t.Fatalf("a short pan chose tab %d", selected)
	}
}

// tabUnder returns the index of the tab label under (x, y), or -1 for none.
func tabUnder(tt *ui.Tester, x, y float32) int {
	for i, label := range tabStripLabels {
		r, ok := tt.Find(label)
		if ok && x >= r.X && x <= r.X+r.W && y >= r.Y && y <= r.Y+r.H {
			return i
		}
	}
	return -1
}

// A press that does not move still chooses its tab, and a strip that fits does
// not pan at all: neither reaches beyond the strip's overflow.
func TestTabStripDragNeedsOverflow(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	selected := 0
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Padding(10).FillWidth().Children(func() {
			a.scrollSubTabs(c, "fits", &selected, "alpha", "bravo")
		})
	}, 900, 120)
	st := &a.tabStripOf("fits").scroll
	if st.MaxX != 0 {
		t.Fatalf("a strip that fits overflows by %v", st.MaxX)
	}
	tt.Press(60, 24)
	for i := 1; i <= 4; i++ {
		tt.Move(60-float32(i*20), 24)
		tt.Advance(100 * time.Millisecond)
	}
	tt.Release(20, 24)
	tt.Advance(100 * time.Millisecond)
	if st.X != 0 {
		t.Fatalf("a strip that fits panned to %v", st.X)
	}
}

// A press that shakes a little is still a click, and a click with no overflow
// chooses its tab as before.
func TestTabStripClickStillChooses(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	selected := 0
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Padding(10).FillWidth().Children(func() {
			a.scrollSubTabs(c, "fits", &selected, "alpha", "bravo", "charlie")
		})
	}, 900, 120)
	label, ok := tt.Find("charlie")
	if !ok {
		t.Fatalf("missing tab: %v", tt.Texts())
	}
	x, y := label.X+label.W/2, label.Y+label.H/2
	tt.Press(x, y)
	tt.Move(x+1, y)
	tt.Release(x, y)
	tt.Advance(100 * time.Millisecond)
	if selected != 2 {
		t.Fatalf("a click chose tab %d, want 2", selected)
	}
}
