package desktop

import (
	"bytes"
	"image"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The real chat follows the bottom with a very large scroll target. A popup
// can be present in the tree yet have zero on-screen bounds in that state.
func TestWelcomeTablePopoversRespondToClicks(t *testing.T) {
	for _, tc := range []struct{ trigger, action string }{
		{"mcp-trigger", "管理 MCP 服务"},
		{"0 个可用", "管理技能"},
		{"37 个工具", "管理内置工具"},
	} {
		for _, reduced := range []bool{false, true} {
			t.Run(tc.trigger+map[bool]string{true: "/reduced", false: "/animated"}[reduced], func(t *testing.T) {
				conv := &conversation{ID: "welcome"}
				a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}, mcpSummary: "mcp-trigger", toolCount: 37}
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(a.theme())
					width, _ := c.Size()
					ui.Column(c).Fill().Children(func() { a.chatColumn(c, width) })
				}, 1280, 850)
				tt.SetPreferences(ui.Preferences{ReduceMotion: reduced, TextScale: 1})
				open := func() {
					t.Helper()
					if err := tt.Click(tc.trigger); err != nil {
						t.Fatal(err)
					}
					tt.Advance(250 * time.Millisecond)
					rect, ok := tt.Find(tc.action)
					if !ok || rect.W <= 0 || rect.H <= 0 || rect.X < 0 || rect.Y < 0 || rect.X+rect.W > 1280 || rect.Y+rect.H > 850 {
						t.Fatalf("popup action %q not visible: %v, found=%t", tc.action, rect, ok)
					}
				}
				closed := func() {
					t.Helper()
					if a.welcomeMCP || a.welcomeSkills || a.welcomeTools {
						t.Fatal("popup remained open")
					}
				}
				open()
				trigger, _ := tt.Find(tc.trigger)
				tt.ClickAt(trigger.X+trigger.W/2, trigger.Y+trigger.H/2)
				closed()
				tt.Advance(250 * time.Millisecond)
				open()
				tt.ClickAt(20, 500)
				closed()
				tt.Advance(250 * time.Millisecond)
				open()
				tt.Key(0, ui.KeyEscape)
				closed()
				tt.Advance(250 * time.Millisecond)
				open()
				if err := tt.Click(tc.action); err != nil {
					t.Fatal(err)
				}
				closed()
			})
		}
	}
}

func TestWelcomePopoverTracksTranscriptScroll(t *testing.T) {
	conv := &conversation{ID: "welcome-scroll", Messages: restoredHistory(16)}
	conv.uiState().Follow = false
	a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}, mcpSummary: "mcp-trigger"}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		width, _ := c.Size()
		ui.Column(c).Fill().Children(func() { a.chatColumn(c, width) })
	}, 1280, 850)
	if err := tt.Click("mcp-trigger"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	anchorBefore, _ := tt.Find("mcp-trigger")
	popupBefore, _ := tt.Find("管理 MCP 服务")
	tt.Scroll(400, 400, 0, 40)
	anchorAfter, _ := tt.Find("mcp-trigger")
	popupAfter, _ := tt.Find("管理 MCP 服务")
	if anchorAfter.Y >= anchorBefore.Y {
		t.Fatal("transcript did not scroll")
	}
	if math.Abs(float64((popupAfter.Y-popupBefore.Y)-(anchorAfter.Y-anchorBefore.Y))) > 2 {
		t.Fatalf("popup did not follow the cell: anchor=%v -> %v, popup=%v -> %v", anchorBefore, anchorAfter, popupBefore, popupAfter)
	}
	tt.Scroll(400, 400, 0, 500)
	tt.Frame()
	if a.welcomeMCP {
		t.Fatal("popup stayed open after the table left the transcript viewport")
	}
}

func TestWelcomePopoverAppearsOverActualChat(t *testing.T) {
	conv := &conversation{ID: "welcome-animation"}
	a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}, mcpSummary: "mcp-trigger"}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		width, _ := c.Size()
		ui.Column(c).Fill().Children(func() { a.chatColumn(c, width) })
	}, 1280, 850)
	if err := tt.Click("mcp-trigger"); err != nil {
		t.Fatal(err)
	}
	capture := func() *image.RGBA {
		img := tt.Image()
		copyImage := image.NewRGBA(img.Bounds())
		copy(copyImage.Pix, img.Pix)
		return copyImage
	}
	start := capture()
	tt.Advance(90 * time.Millisecond)
	mid := capture()
	tt.Advance(200 * time.Millisecond)
	end := capture()
	r, ok := tt.Find("管理 MCP 服务")
	if !ok || r.W < 1 || r.H < 1 {
		t.Fatalf("popup has no visible action: %v", r)
	}
	area := image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H))
	regionEqual := func(a, b *image.RGBA) bool {
		for y := area.Min.Y; y < area.Max.Y; y++ {
			from := a.PixOffset(area.Min.X, y)
			to := a.PixOffset(area.Max.X, y)
			if !bytes.Equal(a.Pix[from:to], b.Pix[from:to]) {
				return false
			}
		}
		return true
	}
	if regionEqual(start, mid) || regionEqual(mid, end) {
		t.Fatal("actual welcome popup skipped its visible transition")
	}
}

func TestWelcomePopoverFitsWindowShell(t *testing.T) {
	for _, size := range [][2]int{{900, 620}, {1280, 850}} {
		conv := &conversation{ID: "welcome-shell"}
		a := &desktop{ready: true, page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}, mcpSummary: "mcp-trigger"}
		tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, size[0], size[1])
		if err := tt.Click("mcp-trigger"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(250 * time.Millisecond)
		r, ok := tt.Find("管理 MCP 服务")
		if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > float32(size[0]) || r.Y+r.H > float32(size[1]) {
			t.Fatalf("window=%v: action outside window: %v found=%t", size, r, ok)
		}
	}
}

func TestWelcomeSwitchesBetweenPopovers(t *testing.T) {
	conv := &conversation{ID: "welcome-switch"}
	a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}, mcpSummary: "mcp-trigger", toolCount: 37}
	tt := ui.NewTester(func(c *ui.Context) {
		width, _ := c.Size()
		ui.Column(c).Fill().Children(func() { a.chatColumn(c, width) })
	}, 1280, 850)
	if err := tt.Click("mcp-trigger"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	if err := tt.Click("0 个可用"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	if a.welcomeMCP || !a.welcomeSkills {
		t.Fatalf("switch left incorrect popup state: mcp=%t skills=%t", a.welcomeMCP, a.welcomeSkills)
	}
	if r, ok := tt.Find("管理技能"); !ok || r.W == 0 || r.H == 0 {
		t.Fatalf("skills popup not visible: %v", r)
	}
}
