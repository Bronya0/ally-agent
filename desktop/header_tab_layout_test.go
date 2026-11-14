package desktop

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
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
	tt.SetLocale("zh-Hans")
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

func TestWorkspaceTabsFillHeaderWithoutGaps(t *testing.T) {
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

		}
	}
}
