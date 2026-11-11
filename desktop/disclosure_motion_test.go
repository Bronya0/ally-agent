package desktop

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func TestWelcomeInformationPopoverAnimates(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		open := false
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Padding(20).Children(func() {
				anchor := ui.Button(c.Key("anchor"), "anchor")
				popoverPanel(c, anchor, &open, 200, func() { ui.Text(c, "information") })
			})
		}, 400, 300)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduced, TextScale: 1})
		tt.Frame()
		pixels := func() []byte { return append([]byte(nil), tt.Image().Pix...) }
		empty := pixels()
		open = true
		tt.Frame()
		tt.Advance(90 * time.Millisecond)
		mid := pixels()
		tt.Advance(200 * time.Millisecond)
		full := pixels()
		// MyGo removes translation under ReduceMotion but permits a fade in place.
		if bytes.Equal(empty, full) || bytes.Equal(mid, empty) || (!reduced && bytes.Equal(mid, full)) {
			t.Fatalf("reduced=%t: empty=full %t, mid=full %t, mid=empty %t", reduced, bytes.Equal(empty, full), bytes.Equal(mid, full), bytes.Equal(mid, empty))
		}
		open = false
		tt.Frame()
		tt.Advance(250 * time.Millisecond)
		if !bytes.Equal(pixels(), empty) {
			t.Fatal("popover did not close")
		}
	}
}

func TestExplorerHelpAnimatesWithoutMovingToolbar(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		a := &desktop{prefs: defaultPrefs()}
		tt := ui.NewTester(func(c *ui.Context) { a.fileTree(c) }, 300, 600)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduced, TextScale: 1})
		tt.Frame()
		start, _ := tt.Find("工作区文件列表")
		toolbar, _ := tt.AccessByRole(ui.RoleButton, "文件浏览器帮助")
		a.filesF.HelpOpen = true
		tt.Frame()
		tt.Advance(100 * time.Millisecond)
		mid, _ := tt.Find("工作区文件列表")
		tt.Advance(200 * time.Millisecond)
		end, _ := tt.Find("工作区文件列表")
		if end.H >= start.H {
			t.Fatalf("help did not open: %v -> %v", start, end)
		}
		if reduced && mid.H != end.H {
			t.Fatal("reduced motion continued")
		}
		if !reduced && (mid.H >= start.H || mid.H <= end.H) {
			t.Fatalf("no intermediate height: %v -> %v -> %v", start, mid, end)
		}
		after, _ := tt.AccessByRole(ui.RoleButton, "文件浏览器帮助")
		if toolbar.Bounds != after.Bounds {
			t.Fatalf("toolbar moved: %v -> %v", toolbar, after)
		}
	}
}

func TestContextPromptPartsAnimate(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		conv := &conversation{ID: "motion-context"}
		st := conv.uiState()
		st.FooterLoaded, st.ContextOpen = true, true
		st.Breakdown = backend.ContextBreakdown{SystemPromptParts: []backend.ContextBreakdownPart{{Label: "part-a", Tokens: 12}, {Label: "part-b", Tokens: 18}}}
		a := &desktop{}
		tt := ui.NewTester(func(c *ui.Context) { a.contextUsage(c, conv) }, 900, 800)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduced, TextScale: 1})
		tt.Advance(300 * time.Millisecond)
		distance := func() float32 {
			first, ok1 := tt.Find("系统提示词")
			next, ok2 := tt.Find("工具 Schema")
			if !ok1 || !ok2 {
				t.Fatal("context rows missing")
			}
			return next.Y - first.Y
		}
		start := distance()
		st.PromptPartsOpen = true
		tt.Frame()
		tt.Advance(100 * time.Millisecond)
		mid := distance()
		tt.Advance(200 * time.Millisecond)
		end := distance()
		if end <= start {
			t.Fatalf("context parts did not expand: %v -> %v", start, end)
		}
		if reduced && mid != end {
			t.Fatal("context parts continued to animate with reduced motion")
		}
		if !reduced && (mid <= start || mid >= end) {
			t.Fatalf("no intermediate context height: %v -> %v -> %v", start, mid, end)
		}
	}
}

func TestModelAdvancedFieldsSurviveFolding(t *testing.T) {
	for _, kind := range []string{"chat", "image"} {
		t.Run(kind, func(t *testing.T) {
			a := &desktop{prefs: defaultPrefs()}
			a.modelForm.Open, a.modelForm.AdvancedOpen = true, true
			a.modelHeaders = `{"X-Test":"chat"}`
			a.imageModels = &imageModelsState{formOpen: true, advancedOpen: true, headers: `{"X-Test":"image"}`}
			tt := ui.NewTester(func(c *ui.Context) {
				if kind == "chat" {
					a.chatModelDialog(c)
				} else {
					a.imageModelDialog(c)
				}
			}, 1000, 1200)
			if _, ok := tt.Access("自定义请求头"); !ok {
				t.Fatal("advanced field did not open")
			}
			if kind == "chat" {
				a.modelForm.AdvancedOpen = false
			} else {
				a.imageModels.advancedOpen = false
			}
			tt.Frame()
			if _, ok := tt.Access("自定义请求头"); ok {
				t.Fatal("folded field remained accessible")
			}
			tt.Advance(300 * time.Millisecond)
			if kind == "chat" {
				a.modelForm.AdvancedOpen = true
			} else {
				a.imageModels.advancedOpen = true
			}
			tt.Frame()
			tt.Advance(300 * time.Millisecond)
			if _, ok := tt.Access("自定义请求头"); !ok {
				t.Fatal("advanced field did not reopen")
			}
			if a.modelHeaders != `{"X-Test":"chat"}` || a.imageModels.headers != `{"X-Test":"image"}` {
				t.Fatal("folding changed header values")
			}
		})
	}
}

// Check the actual cards, not just the shared Reveal helper: the following
// content must move through intermediate positions and settle for reduced motion.
func TestDetailsAnimateAndRespectReducedMotion(t *testing.T) {
	for _, section := range []string{"mcp", "service"} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reduced=%t", section, reduced), func(t *testing.T) {
				a := &desktop{mcpText: `{"mcpServers":{"server":{"command":"test"}}}`}
				a.mcpF.ToolOpen = map[string]bool{}
				a.mcpServers = []map[string]any{{"name": "server", "tools": []map[string]any{{"name": "read"}, {"name": "write"}}}}
				a.services = []backend.ServiceInfo{{ID: "service", Name: "service", Status: "exited", Cwd: "work"}}
				tt := ui.NewTester(func(c *ui.Context) {
					ui.Column(c).FillWidth().Children(func() {
						if section == "mcp" {
							a.mcpServerList(c)
						} else {
							a.servicesSection(c, "")
						}
						ui.Text(c, "after").Label("after")
					})
				}, 800, 900)
				tt.SetPreferences(ui.Preferences{ReduceMotion: reduced, TextScale: 1})
				tt.Frame()
				start, _ := tt.Find("after")
				key := "server"
				if section == "service" {
					key = "service-service"
				}
				a.mcpF.ToolOpen[key] = true
				tt.Frame()
				tt.Advance(100 * time.Millisecond)
				mid, _ := tt.Find("after")
				tt.Advance(200 * time.Millisecond)
				end, _ := tt.Find("after")
				if end.Y <= start.Y {
					t.Fatalf("details did not open: %v -> %v", start, end)
				}
				if reduced && mid.Y != end.Y {
					t.Fatalf("reduced motion did not settle: %v -> %v", mid, end)
				}
				if !reduced && (mid.Y <= start.Y || mid.Y >= end.Y) {
					t.Fatalf("missing intermediate position: %v -> %v -> %v", start, mid, end)
				}
				a.mcpF.ToolOpen[key] = false
				tt.Frame()
				tt.Advance(300 * time.Millisecond)
				closed, _ := tt.Find("after")
				if closed.Y != start.Y {
					t.Fatalf("collapsed height changed: %v -> %v", start, closed)
				}
			})
		}
	}
}

func TestRailSectionsAnimateWithoutDisplacingBottom(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		a := &desktop{prefs: defaultPrefs()}
		a.prefs.RailExpanded, a.prefs.RailWorkspaceOpen, a.prefs.RailSessionsOpen = true, true, true
		a.prefs.RecentWorkspaces = []string{`C:\work\one`, `C:\work\two`, `C:\work\three`}
		for i := 0; i < 40; i++ {
			a.sessions = append(a.sessions, backend.SessionIndexEntry{ID: fmt.Sprint(i), Title: fmt.Sprintf("history-%02d", i)})
		}
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() { a.railBody(c, &a.prefs.RailWidth) })
		}, 400, 850)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduced, TextScale: 1})
		tt.Frame()
		start, _ := tt.Find("历史会话")
		bottom, _ := tt.Find("收起侧边栏")
		a.prefs.RailWorkspaceOpen = false
		tt.Frame()
		tt.Advance(100 * time.Millisecond)
		mid, _ := tt.Find("历史会话")
		tt.Advance(200 * time.Millisecond)
		end, _ := tt.Find("历史会话")
		if end.Y >= start.Y {
			t.Fatalf("reduced=%t: workspace list did not collapse: %v -> %v", reduced, start, end)
		}
		if reduced && mid.Y != end.Y {
			t.Fatalf("reduced motion continued: %v -> %v", mid, end)
		}
		if !reduced && (mid.Y >= start.Y || mid.Y <= end.Y) {
			t.Fatalf("no intermediate rail position: %v -> %v -> %v", start, mid, end)
		}
		a.prefs.RailSessionsOpen = false
		tt.Frame()
		tt.Advance(300 * time.Millisecond)
		a.prefs.RailSessionsOpen, a.prefs.RailWorkspaceOpen = true, true
		tt.Frame()
		tt.Advance(300 * time.Millisecond)
		first, ok := tt.Find("history-00")
		if !ok {
			t.Fatal("session list did not reopen")
		}
		tt.Scroll(first.X+10, first.Y+first.H/2, 0, 120)
		tt.Advance(50 * time.Millisecond)
		scrolled, _ := tt.Find("history-00")
		if scrolled.Y >= first.Y {
			t.Fatalf("reopened session list did not scroll: %v -> %v", first, scrolled)
		}
		after, _ := tt.Find("收起侧边栏")
		if bottom != after {
			t.Fatalf("rail toggle moved: %v -> %v", bottom, after)
		}
	}
}

func TestLargeMCPChecklistRemainsUsable(t *testing.T) {
	tools := make([]map[string]any, mcpAnimatedChecklistLimit+1)
	for i := range tools {
		tools[i] = map[string]any{"name": fmt.Sprintf("tool-%02d", i)}
	}
	a := &desktop{mcpText: `{"mcpServers":{"server":{"command":"test"}}}`, mcpServers: []map[string]any{{"name": "server", "tools": tools}}}
	a.mcpF.ToolOpen = map[string]bool{}
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) { builds++; ui.Column(c).FillWidth().Children(func() { a.mcpServerList(c) }) }, 800, 1500)
	tt.Move(790, 1490)
	tt.Advance(300 * time.Millisecond)
	a.mcpF.ToolOpen["server"] = true
	tt.Frame()
	beforeAnimation := builds
	tt.Advance(250 * time.Millisecond)
	if builds > beforeAnimation+2 {
		t.Fatalf("large checklist rebuilt on fade frames: %d", builds-beforeAnimation)
	}
	if _, ok := tt.Access("tool-00"); !ok {
		t.Fatal("large checklist did not open")
	}
	if err := tt.Click(fmt.Sprintf("工具 %d/%d", len(tools), len(tools))); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Access("tool-00"); ok {
		t.Fatal("closed checklist remained interactive")
	}
	tt.Advance(250 * time.Millisecond)
	if err := tt.Click(fmt.Sprintf("工具 %d/%d", len(tools), len(tools))); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Access("tool-00"); !ok {
		t.Fatal("large checklist did not reopen")
	}
}
