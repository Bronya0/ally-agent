package desktop

import (
	"fmt"
	"strings"
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
func TestHeaderTabsDragInsertsAndKeepsActiveConversation(t *testing.T) {
	tabs := []*conversation{
		{ID: "first", Workspace: "workspace-0", Title: "session-0"},
		{ID: "middle", Workspace: "workspace-1", Title: "session-1"},
		{ID: "last", Workspace: "workspace-2", Title: "session-2"},
	}
	a := &desktop{ready: true, page: "chat", active: 1, tabs: tabs}
	reduceMotion := false
	tt := ui.NewTester(func(c *ui.Context) {
		reduceMotion = c.Preferences().ReduceMotion
		c.SetTheme(a.theme())
		a.headerBar(c)
	}, 1000, 60)
	tt.SetLocale("zh-Hans")
	from, ok := tt.Find("session-0")
	if !ok {
		t.Fatalf("missing drag source: %v", tt.Texts())
	}
	to, ok := tt.Find("session-2")
	if !ok {
		t.Fatalf("missing drop target: %v", tt.Texts())
	}
	sourceStartX := float32(0)
	for _, position := range a.tabStripOf("header-tabs").positions {
		if position.id == "first" {
			sourceStartX = position.x
			break
		}
	}
	sx, sy, tx, ty := from.X+from.W/2, from.Y+from.H/2, to.X+to.W/2+20, to.Y+to.H/2
	tt.Press(sx, sy)
	for i := 1; i <= 8; i++ {
		x := sx + (tx-sx)*float32(i)/8
		y := sy + (ty-sy)*float32(i)/8
		tt.Move(x, y)
		tt.Advance(16 * time.Millisecond)
		if i == 4 {
			strip := a.tabStripOf("header-tabs")
			if strip.reorderTarget == "" || !strip.reorderIndexValid {
				t.Fatalf("dragging did not expose a drop target: target=%q index=%d valid=%v", strip.reorderTarget, strip.reorderIndex, strip.reorderIndexValid)
			}
			var currentSourceX float32
			for _, position := range strip.positions {
				if position.id == "first" {
					currentSourceX = position.x
					break
				}
			}
			if currentSourceX != sourceStartX {
				t.Fatalf("source tab moved during drag: x=%v, want fixed at %v", currentSourceX, sourceStartX)
			}
		}
		if i == 8 && (a.tabs[0].ID != "first" || a.tabs[1].ID != "middle" || a.tabs[2].ID != "last") {
			t.Fatalf("tab order changed before drop: %q/%q/%q", a.tabs[0].ID, a.tabs[1].ID, a.tabs[2].ID)
		}
	}
	tt.Release(tx, ty)
	tt.Advance(16 * time.Millisecond)
	motions := a.tabStripOf("header-tabs").reorderMotions
	if len(motions) == 0 {
		t.Fatalf("tab swap did not schedule its position transition (reduce motion=%v)", reduceMotion)
	}
	for id, motion := range motions {
		if !motion.armed || motion.offset == 0 {
			t.Fatalf("tab %q has no armed movement transition: %+v", id, motion)
		}
	}
	tt.Advance(80 * time.Millisecond)
	for id, motion := range a.tabStripOf("header-tabs").reorderMotions {
		if motion.startedAt.IsZero() {
			t.Fatalf("tab %q did not enter the movement phase: %+v", id, motion)
		}
	}
	if a.tabs[0].ID != "middle" || a.tabs[1].ID != "last" || a.tabs[2].ID != "first" {
		t.Fatalf("dragged tab was not inserted after release: %q %q %q", a.tabs[0].ID, a.tabs[1].ID, a.tabs[2].ID)
	}
	if a.active != 0 || a.tabs[a.active].ID != "middle" {
		t.Fatalf("active conversation changed after insertion: active=%d id=%q", a.active, a.tabs[a.active].ID)
	}
}

func TestHeaderTabReorderClearsAfterReleaseOutsideTabs(t *testing.T) {
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{
		{ID: "first", Title: "first", Workspace: "workspace-1"},
		{ID: "middle", Title: "middle", Workspace: "workspace-2"},
		{ID: "last", Title: "last", Workspace: "workspace-3"},
	}}
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.headerBar(c) }, 900, 60)
	source, ok := tt.Find("first")
	if !ok {
		t.Fatal("missing source tab")
	}
	y := source.Y + source.H/2
	tt.Press(source.X+source.W/2, y)
	tt.Move(700, y)
	tt.Advance(16 * time.Millisecond)
	if a.tabStripOf("header-tabs").reorderFrom != "first" {
		t.Fatal("drag did not enter reorder state")
	}
	tt.Release(850, y)
	tt.Advance(16 * time.Millisecond)
	strip := a.tabStripOf("header-tabs")
	if strip.reorderFrom != "" || strip.gesture.Panning {
		t.Fatalf("release left stale drag state: source=%q panning=%v", strip.reorderFrom, strip.gesture.Panning)
	}
	if a.tabs[2].ID != "first" {
		t.Fatalf("release outside tabs did not commit the indicated end slot: %q/%q/%q", a.tabs[0].ID, a.tabs[1].ID, a.tabs[2].ID)
	}
}

func TestConversationTabInsertionIndexUsesScrolledVariableWidths(t *testing.T) {
	tabs := []*conversation{{ID: "dragged"}, {ID: "middle"}, {ID: "long-label"}, {ID: "last"}}
	positions := []tabPosition{
		{id: "dragged", x: -120, width: 80},
		{id: "middle", x: -40, width: 180},
		{id: "long-label", x: 140, width: 120},
		{id: "last", x: 260, width: 150},
	}
	for _, tc := range []struct {
		pointer float32
		want    int
	}{
		{pointer: -80, want: 0}, // stays before the first remaining tab
		{pointer: 70, want: 1},  // after the wide middle tab
		{pointer: 150, want: 2}, // after long-label, before last
		{pointer: 300, want: 3}, // after the last tab
	} {
		if got := conversationTabInsertionIndex(tabs, positions, "dragged", tc.pointer, tabReorderHysteresis); got != tc.want {
			t.Fatalf("pointer=%v insertion=%d, want %d", tc.pointer, got, tc.want)
		}
	}
	if got := conversationTabDropTarget(tabs, "dragged", 0); got != "dragged" {
		t.Fatalf("same-slot marker target=%q, want dragged source", got)
	}
	if got := conversationTabDropTarget(tabs, "dragged", 1); got != "long-label" {
		t.Fatalf("after-middle marker target=%q, want long-label", got)
	}
}

func TestConversationTabInsertionIndexSupportsMultiTabMoves(t *testing.T) {
	const count = 8
	tabs := make([]*conversation, count)
	positions := make([]tabPosition, count)
	x := float32(-260) // simulate a horizontally scrolled tab strip
	for i := range tabs {
		width := float32(60 + (i*37)%120)
		tabs[i] = &conversation{ID: fmt.Sprintf("tab-%d", i)}
		positions[i] = tabPosition{id: tabs[i].ID, x: x, width: width}
		x += width
	}

	for from := range tabs {
		centers := make([]float32, 0, count-1)
		virtualX := positions[0].x
		for i := range tabs {
			if i == from {
				continue
			}
			centers = append(centers, virtualX+positions[i].width/2)
			virtualX += positions[i].width
		}
		for destination := range tabs {
			var pointer float32
			switch {
			case destination == 0:
				pointer = centers[0] - 24
			case destination == count-1:
				pointer = centers[len(centers)-1] + 24
			default:
				pointer = (centers[destination-1] + centers[destination]) / 2
			}
			got := conversationTabInsertionIndex(tabs, positions, tabs[from].ID, pointer, tabReorderHysteresis)
			if got != destination {
				t.Fatalf("from=%d pointer=%v insertion=%d, want %d", from, pointer, got, destination)
			}
			movedTabs := append([]*conversation(nil), tabs...)
			activeID := movedTabs[4].ID
			active, _, moved := moveOpenConversationTabToIndex(movedTabs, positions, 4, tabs[from].ID, got)
			if moved != (from != destination) {
				t.Fatalf("from=%d destination=%d moved=%v", from, destination, moved)
			}
			if movedTabs[active].ID != activeID {
				t.Fatalf("from=%d destination=%d active session=%q, want %q", from, destination, movedTabs[active].ID, activeID)
			}
			if movedTabs[destination].ID != tabs[from].ID {
				t.Fatalf("from=%d destination=%d landed at %q", from, destination, movedTabs[destination].ID)
			}
		}
	}
}

func TestMoveOpenConversationTabShiftsTargetAndFollowingTabs(t *testing.T) {
	tabs := []*conversation{{ID: "first"}, {ID: "target"}, {ID: "following"}, {ID: "dragged"}}
	positions := []tabPosition{{id: "first", x: 0, width: 100}, {id: "target", x: 100, width: 100}, {id: "following", x: 200, width: 100}, {id: "dragged", x: 300, width: 100}}
	active, motions, moved := moveOpenConversationTabToIndex(tabs, positions, 2, "dragged", 1)
	if !moved || tabs[0].ID != "first" || tabs[1].ID != "dragged" || tabs[2].ID != "target" || tabs[3].ID != "following" {
		t.Fatalf("tab was not inserted before target: moved=%v order=%q/%q/%q/%q", moved, tabs[0].ID, tabs[1].ID, tabs[2].ID, tabs[3].ID)
	}
	if active != 3 || tabs[active].ID != "following" {
		t.Fatalf("active conversation did not follow insertion: active=%d id=%q", active, tabs[active].ID)
	}
	if motions["target"].offset != -100 || motions["following"].offset != -100 {
		t.Fatalf("target and following tabs lack shift animations: %+v", motions)
	}
}

func TestHeaderTabClickDoesNotReorderTabs(t *testing.T) {
	a := &desktop{ready: true, page: "chat", active: 0, tabs: []*conversation{{ID: "first", Workspace: "one", Title: "first session"}, {ID: "second", Workspace: "two", Title: "second session"}}}
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.headerBar(c) }, 900, 60)
	tab, ok := tt.Find("second session")
	if !ok {
		t.Fatal("missing second tab")
	}
	x, y := tab.X+tab.W/2, tab.Y+tab.H/2
	tt.Press(x, y)
	tt.Move(x+3, y+1)
	tt.Advance(16 * time.Millisecond)
	tt.Release(x+3, y+1)
	tt.Advance(16 * time.Millisecond)
	if len(a.tabs) != 2 || a.tabs[0].ID != "first" || a.tabs[1].ID != "second" {
		t.Fatalf("click changed tab order: %+v", a.tabs)
	}
}

func TestSessionTabLabelAndWorkspaceTip(t *testing.T) {
	title := strings.Repeat("默认名称", 12)
	if got, want := sessionTabLabel(&conversation{Title: title}), short(title, 36); got != want {
		t.Fatalf("session tab label=%q, want truncated session title %q", got, want)
	}
	if got, want := sessionTabTip(&conversation{Title: title, Workspace: "C:/work/alpha"}), short(title, 72)+"\nC:/work/alpha"; got != want {
		t.Fatalf("long-title tooltip=%q, want full title and workspace %q", got, want)
	}
	if got := sessionTabLabel(&conversation{Title: "Custom title"}); got != "Custom title" {
		t.Fatalf("session tab label=%q, want custom title", got)
	}
	if got := sessionTabTip(&conversation{Title: "Custom title", Workspace: "C:/work/alpha"}); got != "C:/work/alpha" {
		t.Fatalf("short-title tooltip=%q, want workspace path", got)
	}
	if got := sessionTabLabel(&conversation{}); got == "" || strings.Contains(got, "workspace") {
		t.Fatalf("empty session title fallback=%q", got)
	}
}

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
