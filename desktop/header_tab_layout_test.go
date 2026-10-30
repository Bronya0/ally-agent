package desktop

import (
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

// A press on the header's tabs drags the strip, and the tab it lets go over is
// not chosen: the title bar's tabs scroll like any other strip's.
func TestHeaderTabsPanWithoutChoosing(t *testing.T) {
	tabs := make([]*conversation, 0, 12)
	for i := 0; i < 12; i++ {
		tabs = append(tabs, &conversation{ID: fmt.Sprintf("tab%02d", i), Workspace: fmt.Sprintf("workspace-%d", i)})
	}
	a := &desktop{ready: true, page: "chat", active: 1, tabs: tabs}
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.headerBar(c) }, 500, 60)
	st := &a.tabStripOf("header-tabs").scroll
	if st.MaxX <= 0 {
		t.Fatalf("the header's tabs did not overflow: maxX=%v", st.MaxX)
	}
	// The first tab's close button shows where that tab ends; the press goes
	// into its button, left of it.
	close, ok := tt.Find("关闭标签")
	if !ok {
		t.Fatalf("missing tab's close button: %v", tt.Texts())
	}
	x, y := close.X-40, close.Y+close.H/2
	tt.Press(x, y)
	for i := 1; i <= 4; i++ {
		tt.Move(x-float32(i*5), y)
		tt.Advance(100 * time.Millisecond)
	}
	if st.X != 20 {
		t.Fatalf("dragging 20 left moved the header's tabs to %v", st.X)
	}
	tt.Release(x-20, y)
	tt.Advance(200 * time.Millisecond)
	// The pointer never left the tab it pressed, so the tab reports a click
	// the pan drops: the session stays on 1.
	if a.active != 1 {
		t.Fatalf("the pan chose tab %d", a.active)
	}
}

func TestWorkspaceTabsFillHeaderAndShareBottomBaseline(t *testing.T) {
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{{ID: "first", Workspace: "apisec"}, {ID: "second", Workspace: "ally-agent"}}}
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.headerBar(c) }, 900, 100)
	first, ok := tt.Find("工作区标签 first")
	if !ok {
		t.Fatal("missing first tab")
	}
	second, _ := tt.Find("工作区标签 second")
	if first.Y != 0 || first.H != 44 || second.Y != 0 || second.H != 44 || second.X != first.X+first.W {
		t.Fatalf("header has gaps: %v %v", first, second)
	}
	color := tt.Image().RGBAAt(int(first.X+first.W/2), 43)
	accent := a.currentPalette().accent
	if color.R != accent.R || color.G != accent.G || color.B != accent.B {
		t.Fatalf("underline does not share header bottom: %v want %v", color, accent)
	}
	tt.Move(second.X+3, second.Y+3)
	tt.Advance(200 * time.Millisecond)
	color = tt.Image().RGBAAt(int(second.X+3), 1)
	hover := a.currentPalette().hover
	if color.R != hover.R || color.G != hover.G || color.B != hover.B {
		t.Fatalf("hover does not fill header height: %v want %v", color, hover)
	}
}

func TestHeaderDividerContinuesUnderCaptionControls(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		a := &desktop{ready: true, page: "chat", win: &mygo.Window{}, tabs: []*conversation{{ID: "tab", Workspace: "cam"}}}
		a.prefs.Appearance = appearance
		tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.headerBar(c) }, 900, 100)
		for _, hover := range []string{"最小化", "最大化", "关闭窗口"} {
			r, ok := tt.Find(hover)
			if !ok {
				t.Fatal("missing window control")
			}
			if r.Y != 0 || r.H != 44 {
				t.Fatalf("caption top/bottom mismatch: %v", r)
			}
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			tt.Advance(200 * time.Millisecond)
			for _, name := range []string{"最小化", "最大化", "关闭窗口"} {
				b, _ := tt.Find(name)
				for x := int(b.X) + 1; x < int(b.X+b.W)-1; x++ {
					v := tt.Image().RGBAAt(x, 43)
					want := a.theme().Border
					if v.R != want.R || v.G != want.G || v.B != want.B {
						t.Fatalf("%s bottom line missing on %s x=%d color=%v want=%v", appearance, name, x, v, want)
					}
				}
			}
		}
	}
}

// Header tabs share the content column's boundary as the rail expands and
// resizes. Native traffic lights must always keep their reserved area.
func TestHeaderTabsTrackSidebarBoundary(t *testing.T) {
	for _, bar := range []ui.TitleBar{{}, {Left: 80, Height: 44}} {
		a := &desktop{page: "chat", tabs: []*conversation{{ID: "aligned", Workspace: "mygo"}}}
		var railSpan float32
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			a.headerBar(c)
			railSpan = widthRailCollapsed
			if a.prefs.RailExpanded {
				railSpan = a.prefs.RailWidth + railDividerWidth
			}
		}, 1000, 100)
		tt.SetTitleBar(bar)
		for _, width := range []float32{0, 160, 240, 360, 0} {
			a.prefs.RailExpanded = width > 0
			a.prefs.RailWidth = width
			tt.Frame()
			tab, ok := tt.Find("工作区标签 aligned")
			minLeading := widthRailCollapsed
			if bar.Left > 0 {
				minLeading += headerTrafficLightGap
			}
			want := max(railSpan, bar.Left+minLeading)
			if !ok || tab.X != want {
				t.Fatalf("bar=%v rail=%v tab starts %v want %v", bar, width, tab.X, want)
			}
			logo, ok := tt.Find("应用 Logo")
			left := railPadH
			if width > 0 {
				left = 6 + railInsetForWidth(width)
			}
			if bar.Left > 0 {
				left = max(left, bar.Left+headerTrafficLightGap)
			}
			if !ok || logo.X != left {
				t.Fatalf("bar=%v rail=%v logo left=%v want %v", bar, width, logo.X, left)
			}
		}
	}
}

func TestHeaderSidebarSeparatorTurnsUpwards(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		for _, width := range []float32{44, 160, 300} {
			a := &desktop{prefs: defaultPrefs(), tabs: []*conversation{{ID: "border", Workspace: "mygo"}}}
			a.prefs.Appearance = appearance
			a.prefs.RailExpanded = width != 44
			a.prefs.RailWidth = width
			tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.headerBar(c) }, 900, 100)
			bg := a.currentPalette().chromeColor()
			line := a.currentPalette().line
			for _, point := range []struct {
				x, y int
				want ui.Color
			}{
				{10, 43, bg}, {int(width) - 1, 10, line}, {int(width) - 1, 43, line},
			} {
				got := tt.Image().RGBAAt(point.x, point.y)
				if got.R != point.want.R || got.G != point.want.G || got.B != point.want.B {
					t.Fatalf("%s width=%v separator at %d,%d got %v want %v", appearance, width, point.x, point.y, got, point.want)
				}
			}
		}
	}
}
