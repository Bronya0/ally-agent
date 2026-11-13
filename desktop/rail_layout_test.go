package desktop

import (
	backend "ally-dev/internal/app"
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestAgentHoverNewSessionKeepsLayout(t *testing.T) {
	a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{{ID: "busy", Busy: true}}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Width(220).Children(func() { a.railModeButton(c, true, "chat", "Agent", "chat", true) })
	}, 300, 100)
	r, ok := tt.Find("Agent")
	if !ok {
		t.Fatal("missing Agent")
	}
	before := append([]byte(nil), tt.Image().Pix...)
	tt.Move(r.X+10, r.Y+r.H/2)
	tt.Advance(200 * time.Millisecond)
	after, _ := tt.Find("Agent")
	if r != after {
		t.Fatalf("hover moved Agent: %v -> %v", r, after)
	}
	if bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("hover did not reveal action")
	}
	if err := tt.Click("新建会话"); err != nil {
		t.Fatal(err)
	}
	if len(a.notifications) != 1 {
		t.Fatal("new session action did not reach session command")
	}
}

func TestRailContentKeepsOneColumn(t *testing.T) {
	xs := map[float32]float32{}
	for _, width := range []float32{160, 340} {
		a := &desktop{prefs: defaultPrefs()}
		a.prefs.RailWidth = width
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			a.railBody(c, &a.prefs.RailWidth)
		}, 500, 850)
		title, ok := tt.Find("工作区")
		if !ok {
			t.Fatalf("width=%v: section title not found", width)
		}
		xs[width] = title.X
	}
	// A wider rail keeps its content in one column, so nothing shifts when the
	// window is resized or the rail collapses.
	if xs[160] != xs[340] {
		t.Fatalf("section title moved with the rail width: %v at 160, %v at 340", xs[160], xs[340])
	}
}

// Collapsing the rail must not move the navigation entries at all: the badge and
// the expanded row keep one column and one row pitch.
func TestRailIconsShareOneColumn(t *testing.T) {
	for _, page := range []string{"chat", "models"} {
		a := &desktop{prefs: defaultPrefs(), page: page}
		boxes := map[bool]ui.Rect{}
		for _, expanded := range []bool{false, true} {
			a.prefs.RailExpanded = expanded
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				a.rail(c)
			}, 500, 850)
			button, ok := tt.Find(navLabelFor(page))
			if !ok {
				t.Fatalf("page=%s expanded=%t: nav entry not found", page, expanded)
			}
			boxes[expanded] = button
		}
		// The entry keeps its left edge, its height and its place down the rail;
		// only its width changes, since one state has no label to show.
		if boxes[true].X != boxes[false].X || boxes[true].Y != boxes[false].Y || boxes[true].H != boxes[false].H {
			t.Fatalf("page=%s: entry moved between states: collapsed %v, expanded %v", page, boxes[false], boxes[true])
		}
	}
}

func navLabelFor(page string) string {
	for _, item := range baseModes {
		if item.id == page {
			return item.label
		}
	}
	return "Agent"
}

// The navigation keeps one column and one row pitch as the rail opens; the
// wordmark's own placement is covered by the header and centring tests.
func TestRailKeepsItsColumnAcrossStates(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		a := &desktop{prefs: defaultPrefs(), page: "chat"}
		a.prefs.RailExpanded = expanded
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			ui.Column(c).Fill().Children(func() {
				a.headerBar(c)
				content := func() { ui.Column(c).Key("page-content").Grow(1).MinHeight(0).Fill() }
				if a.prefs.RailExpanded {
					ui.Split(c, &a.prefs.RailWidth, func() { a.rail(c) }, content).Grow(1).MinHeight(0).FillWidth()
				} else {
					ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
						a.rail(c)
						content()
					})
				}
			})
		}, 1200, 800)
		tt.Frame()
		entry, okEntry := tt.Find("Agent")
		if !okEntry {
			t.Fatalf("expanded=%t: nav entry not found", expanded)
		}
		// The tinted block and the icons keep their column in both states.
		if entry.X != 6 {
			t.Fatalf("expanded=%t: selected block starts at %v, want 6", expanded, entry.X)
		}
		iconEdge := entry.X + railContentInset()
		if !expanded {
			iconEdge = entry.X + (heightRailItemCollapsed-sizeRailIconCollapsed)/2
		}
		if iconEdge != 12 {
			t.Fatalf("expanded=%t: icons start at %v, want 12", expanded, iconEdge)
		}
	}
}

// Revealing the reserved delete slot must keep the title stable, and the
// revealed action must still target only the intended workspace.
func TestWorkspaceDeleteRevealsWithoutMovingTitle(t *testing.T) {
	root := t.TempDir()
	a := &desktop{sessions: []backend.SessionIndexEntry{{ID: "history", Workspace: root}}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Width(240).Children(func() { a.railWorkspaceRow(c, root, "workspace", root) })
	}, 300, 90)
	title, _ := tt.Find("workspace")
	_, ok := tt.Find(workspaceDeleteHint)
	if !ok {
		t.Fatal("missing reserved delete slot")
	}
	tt.Move(title.X+10, title.Y+title.H/2)
	tt.Advance(200 * time.Millisecond)
	after, _ := tt.Find("workspace")
	if title != after {
		t.Fatalf("delete reveal moved title: %v -> %v", title, after)
	}
	if err := tt.Click(workspaceDeleteHint); err != nil {
		t.Fatal(err)
	}
	if ids := a.workspaceSessionIDs(root); len(ids) != 1 || ids[0] != "history" {
		t.Fatalf("workspace history removal targeted %v", ids)
	}
}

func TestRailSectionTitlesUseAvailableSpace(t *testing.T) {
	titles := []string{"工作区", "历史会话"}
	for _, width := range []float32{160, 200, 340} {
		a := &desktop{prefs: defaultPrefs()}
		a.prefs.RailWidth = width
		textWidths := make([]float32, len(titles))
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			a.railBody(c, &a.prefs.RailWidth)
			for i, title := range titles {
				textWidths[i], _ = c.MeasureText(1000, ui.Span{Text: title, Size: fontRailLabel})
			}
		}, 500, 850)
		for i, title := range titles {
			r, ok := tt.Find(title)
			// The found rect already stops where the add button starts and begins at
			// the row edge; only the caret and its gap sit inside it. Subtracting the
			// row inset and the button again counted that chrome twice, so the label
			// read as 1 DIP too narrow while the drawn text still had plenty of room.
			available := r.W - sizeRailIcon - 10
			if !ok || available < textWidths[i] {
				t.Fatalf("width=%v title=%q header=%v leaves %.1f for %.1f text", width, title, r, available, textWidths[i])
			}
		}
	}
}

func TestExpandedRailToggleStaysAtBottom(t *testing.T) {
	for _, height := range []int{620, 850} {
		for _, sectionsOpen := range []bool{false, true} {
			a := &desktop{prefs: defaultPrefs()}
			a.prefs.RailExpanded = true
			a.prefs.RailWorkspaceOpen, a.prefs.RailSessionsOpen = sectionsOpen, sectionsOpen
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				ui.Column(c).Fill().Children(func() {
					ui.Box(c).Height(heightHeader).Shrink(0)
					ui.Split(c, &a.prefs.RailWidth, func() { a.rail(c) }, func() { ui.Column(c).Fill() }).Grow(1).MinHeight(0).FillWidth()
				})
			}, 1000, height)
			button, ok := tt.Find("收起侧边栏")
			// The rail's vertical padding is 4, matching the collapsed rail.
			if !ok || button.Y+button.H != float32(height)-4 {
				t.Fatalf("height=%d sectionsOpen=%t: toggle is not at bottom: %v", height, sectionsOpen, button)
			}
		}
	}
}

// The session rail draws the rows in view only: a long history must not rebuild
// every row on every frame, nor push the collapse button off the rail.
func TestRailSessionsBuildRowsInView(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), page: "chat", tabs: []*conversation{{ID: "s0", Workspace: "/work/alpha"}}}
	a.prefs.RailExpanded, a.prefs.RailSessionsOpen = true, true
	for i := range 80 {
		a.sessions = append(a.sessions, backend.SessionIndexEntry{ID: fmt.Sprint("s", i), Title: fmt.Sprint("session ", i), Workspace: "/work/alpha", UpdatedAt: 1700000000, MessageCount: 3})
	}
	a.sessRev = 1
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Children(func() {
			ui.Box(c).Height(heightHeader).Shrink(0)
			ui.Split(c, &a.prefs.RailWidth, func() { a.rail(c) }, func() { ui.Column(c).Fill() }).Grow(1).MinHeight(0).FillWidth()
		})
	}, 1000, 850)
	button, ok := tt.Find("收起侧边栏")
	if !ok || button.Y+button.H != 850-4 {
		t.Fatalf("sessions pushed the toggle off the rail: %v", button)
	}
	if !tt.HasText("session 0") {
		t.Fatal("the newest session was not built")
	}
	if tt.HasText("session 79") {
		t.Fatal("a session below the fold was built: the rail draws every row again")
	}
}
