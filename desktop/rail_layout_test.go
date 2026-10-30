package desktop

import (
	backend "ally-dev/internal/app"
	"bytes"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
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

func TestRailContentMovesWithWidth(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	var inset float32
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		a.railBody(c, &a.prefs.RailWidth)
		inset = railContentInset(c)
	}, 500, 850)
	a.prefs.RailWidth = 200
	tt.Frame()
	narrow := inset
	a.prefs.RailWidth = 340
	tt.Frame()
	if inset <= narrow {
		t.Fatalf("wider sidebar did not inset content: %v -> %v", narrow, inset)
	}
}

// Revealing the reserved delete slot must keep the title stable, and the
// revealed action must still remove only the intended history entry.
func TestWorkspaceDeleteRevealsWithoutMovingTitle(t *testing.T) {
	root := t.TempDir()
	a := &desktop{sessions: []backend.SessionIndexEntry{{ID: "history", Workspace: root}}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Width(240).Children(func() { a.railWorkspaceRow(c, root, "workspace", root) })
	}, 300, 90)
	title, _ := tt.Find("workspace")
	_, ok := tt.Find("从历史记录移除")
	if !ok {
		t.Fatal("missing reserved delete slot")
	}
	tt.Move(title.X+10, title.Y+title.H/2)
	tt.Advance(200 * time.Millisecond)
	after, _ := tt.Find("workspace")
	if title != after {
		t.Fatalf("delete reveal moved title: %v -> %v", title, after)
	}
	if err := tt.Click("从历史记录移除"); err != nil {
		t.Fatal(err)
	}
	if len(a.sessions) != 0 {
		t.Fatal("history deletion no longer works")
	}
}

func TestRailSectionTitlesUseAvailableSpace(t *testing.T) {
	for _, width := range []float32{160, 200, 340} {
		a := &desktop{prefs: defaultPrefs()}
		a.prefs.RailWidth = width
		var textWidth, inset float32
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			a.railBody(c, &a.prefs.RailWidth)
			textWidth, _ = c.MeasureText(1000, ui.Span{Text: "历史工作区", Size: 12.5})
			inset = railContentInset(c)
		}, 500, 850)
		for _, title := range []string{"历史工作区", "工作区会话"} {
			r, ok := tt.Find(title)
			// The label shares the header with the icon, caret and two gaps.
			available := r.W - 2*inset - sizeRailIcon - 10 - 20
			if !ok || available < textWidth {
				t.Fatalf("width=%v header=%v leaves %.1f for %.1f text", width, r, available, textWidth)
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
			if !ok || button.Y+button.H != float32(height)-6 {
				t.Fatalf("height=%d sectionsOpen=%t: toggle is not at bottom: %v", height, sectionsOpen, button)
			}
		}
	}
}
