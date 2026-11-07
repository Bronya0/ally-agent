// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"testing"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func TestComposerPickersRevealSelectionWithoutMovingChat(t *testing.T) {
	for _, picker := range []string{"sessions", "commands", "mentions"} {
		t.Run(picker, func(t *testing.T) {
			root := t.TempDir()
			conv := &conversation{ID: "session-25", Workspace: root, Messages: restoredHistory(60)}
			a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}}
			st := conv.uiState()
			for i := 0; i < 30; i++ {
				id := fmt.Sprintf("session-%d", i)
				a.sessions = append(a.sessions, backend.SessionIndexEntry{ID: id, Workspace: root, Title: id})
				st.Mention.Entries = append(st.Mention.Entries, backend.WorkspacePathEntry{Path: fmt.Sprintf("file-%d.go", i)})
			}
			tester := ui.NewTester(func(c *ui.Context) {
				c.DisableAnimations()
				c.SetTheme(a.theme())
				width, _ := c.Size()
				a.chatColumn(c, width)
			}, 900, 360)
			st.Follow = false
			conv.Scroll.Y = conv.Scroll.MaxY / 2
			tester.Frame()
			before, _ := tester.Find("消息输入框")
			y, maxY := conv.Scroll.Y, conv.Scroll.MaxY
			var listLabel, selectedLabel string
			open := func() {
				a.composerFocus = conv.ID
				switch picker {
				case "sessions":
					a.openSessionsMenu(conv)
					listLabel, selectedLabel = "历史会话列表", "session-25"
				case "commands":
					conv.Draft = "/"
					st.CmdOpen = true
					listLabel, selectedLabel = "指令列表", "/push"
				case "mentions":
					conv.Draft = "@"
					st.Mention.Open = true
					st.Mention.Index = 25
					listLabel, selectedLabel = "文件引用列表", "file-25.go"
				}
				tester.Frame()
			}
			unchanged := func() {
				t.Helper()
				if after, _ := tester.Find("消息输入框"); after != before || conv.Scroll.Y != y || conv.Scroll.MaxY != maxY || st.Follow {
					t.Fatal("picker displaced the input or transcript")
				}
			}
			visible := func() {
				t.Helper()
				list, _ := tester.Find(listLabel)
				row, ok := tester.Find(selectedLabel)
				if !ok || row.H <= 0 || row.Y < list.Y || row.Y+row.H > list.Y+list.H {
					t.Fatalf("selection not visible: row=%+v list=%+v", row, list)
				}
			}
			open()
			if picker == "commands" {
				for range 9 {
					tester.Key(0, ui.KeyDown)
				}
			}
			visible()
			unchanged()
			tester.Key(0, ui.KeyDown)
			if picker == "sessions" {
				selectedLabel = "session-26"
			} else if picker == "mentions" {
				selectedLabel = "file-26.go"
			}
			visible()
			list, _ := tester.Find(listLabel)
			tester.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, -2000)
			manual, _ := tester.Find(selectedLabel)
			for range 3 {
				tester.Frame()
			}
			if after, _ := tester.Find(selectedLabel); after != manual {
				t.Fatal("ordinary redraw stole manual list scrolling")
			}
			tester.Key(0, ui.KeyUp)
			if picker == "sessions" {
				selectedLabel = "session-25"
			} else if picker == "mentions" {
				selectedLabel = "file-25.go"
			} else {
				selectedLabel = "/compact-fast"
			}
			visible()
			tester.Key(0, ui.KeyEscape)
			if st.SessionsOpen || st.CmdOpen || st.Mention.Open {
				t.Fatal("Escape did not close the picker")
			}
			unchanged()
		})
	}
}

func TestSessionsPickerOwnsComposerMenus(t *testing.T) {
	conv := &conversation{ID: "current", Workspace: t.TempDir(), Draft: "/"}
	a := &desktop{tabs: []*conversation{conv}}
	st := conv.uiState()
	st.CmdOpen, st.Mention.Open = true, true
	a.openSessionsMenu(conv)
	for _, draft := range []string{"/", "/sessions", "@file"} {
		conv.Draft = draft
		a.updateComposerMenus(conv)
		if !st.SessionsOpen || st.CmdOpen || st.Mention.Open {
			t.Fatal("another picker took keyboard ownership from history")
		}
	}
}

func TestSessionsTriggerClosesOnSecondClick(t *testing.T) {
	root := t.TempDir()
	conv := &conversation{ID: "current", Workspace: root}
	a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}, sessions: []backend.SessionIndexEntry{
		{ID: "newest", Workspace: root, Title: "newest"}, {ID: conv.ID, Workspace: root, Title: "current"},
	}}
	st := conv.uiState()
	tester := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		ui.Column(c).Fill().Children(func() {
			ui.Button(c, "outside")
			ui.Spacer(c)
			a.composer(c, conv)
		})
	}, 1000, 600)
	for range 3 {
		if err := tester.Click("查看和切换历史会话"); err != nil {
			t.Fatal(err)
		}
		if !st.SessionsOpen || st.SessionsIndex != 1 {
			t.Fatal("picker did not open with the current session selected")
		}
		bounds, _ := tester.Find("查看和切换历史会话")
		x, y := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
		tester.Press(x, y)
		if !st.SessionsOpen {
			t.Fatal("trigger was treated as an outside press")
		}
		tester.Release(x, y)
		if st.SessionsOpen {
			t.Fatal("second click reopened the picker")
		}
	}
	if err := tester.Click("查看和切换历史会话"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("outside"); err != nil {
		t.Fatal(err)
	}
	if st.SessionsOpen {
		t.Fatal("outside click did not close the picker")
	}
	if err := tester.Click("查看和切换历史会话"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEscape)
	if st.SessionsOpen {
		t.Fatal("Escape did not close the picker")
	}
	if err := tester.Click("查看和切换历史会话"); err != nil {
		t.Fatal(err)
	}
	if !tester.Focused("消息输入框") {
		t.Fatal("history picker lost composer focus")
	}
	tester.Key(0, ui.KeyUp)
	if st.SessionsIndex != 0 {
		t.Fatal("up arrow did not select the previous session")
	}
	tester.Key(0, ui.KeyDown)
	if st.SessionsIndex != 1 {
		t.Fatal("down arrow did not select the next session")
	}
	tester.Key(0, ui.KeyEnter)
	if st.SessionsOpen || a.current() != conv || !tester.Focused("消息输入框") {
		t.Fatal("choosing the current session did not close history and preserve composer focus")
	}
}

func TestSessionsOverlayPreservesChatLayoutAndScroll(t *testing.T) {
	for _, size := range [][2]int{{900, 600}, {1500, 900}, {900, 360}} {
		for _, withPlan := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/plan=%v", size[0], size[1], withPlan), func(t *testing.T) {
				root := t.TempDir()
				conv := &conversation{ID: "current", Workspace: root, Messages: restoredHistory(60)}
				a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}}
				for i := 0; i < 40; i++ {
					a.sessions = append(a.sessions, backend.SessionIndexEntry{ID: fmt.Sprintf("session-%d", i), Workspace: root, Title: fmt.Sprintf("session-%d", i)})
				}
				if withPlan {
					a.plans = map[string][]backend.PlanStep{conv.ID: {{Title: "current step", Status: "doing"}}}
				}
				tester := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(a.theme())
					width, _ := c.Size()
					a.chatColumn(c, width)
				}, size[0], size[1])
				st := conv.uiState()
				st.Follow = false
				conv.Scroll.Y = conv.Scroll.MaxY / 2
				tester.Frame()
				position, maxY := conv.Scroll.Y, conv.Scroll.MaxY
				if position <= 0 || position >= maxY {
					t.Fatal("fixture did not place the reader in the middle of the transcript")
				}
				before := map[string]ui.Rect{}
				for _, label := range []string{"聊天滚动区域", "消息输入框", "查看和切换历史会话"} {
					bounds, ok := tester.Find(label)
					if !ok {
						t.Fatalf("missing control %q", label)
					}
					before[label] = bounds
				}
				unchanged := func() {
					t.Helper()
					for label, bounds := range before {
						if got, ok := tester.Find(label); !ok || got != bounds {
							t.Fatalf("opening history displaced %q: %+v -> %+v", label, bounds, got)
						}
					}
					if conv.Scroll.Y != position || conv.Scroll.MaxY != maxY || st.Follow {
						t.Fatalf("history changed transcript scroll: y=%v/%v max=%v/%v", conv.Scroll.Y, position, conv.Scroll.MaxY, maxY)
					}
				}
				if err := tester.Click("查看和切换历史会话"); err != nil {
					t.Fatal(err)
				}
				unchanged()
				tester.Advance(90 * time.Millisecond)
				unchanged()
				tester.Advance(200 * time.Millisecond)
				unchanged()
				panel, ok := tester.Find("历史会话面板")
				if !ok || panel.W <= 0 || panel.H <= 0 || panel.X < 0 || panel.Y < 0 || panel.X+panel.W > float32(size[0]) || panel.Y+panel.H > float32(size[1]) {
					t.Fatalf("history did not fit in the window: %+v", panel)
				}
				if size[1] >= 600 && panel.Y+panel.H > before["消息输入框"].Y {
					t.Fatalf("history did not open above the composer: %+v", panel)
				}
				list, ok := tester.Find("历史会话列表")
				if !ok || list.H < 28 {
					t.Fatalf("history list has no usable scroll viewport: %+v", list)
				}
				first, _ := tester.Find("session-0")
				tester.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 120)
				if after, ok := tester.Find("session-0"); !ok || after.Y >= first.Y {
					t.Fatalf("scrolling the history list did not move its rows: panel=%+v list=%+v first=%+v after=%+v found=%v open=%v", panel, list, first, after, ok, st.SessionsOpen)
				}
				unchanged()
				if err := tester.Click("关闭"); err != nil {
					t.Fatal(err)
				}
				if st.SessionsOpen {
					t.Fatal("history close action did not receive the overlay click")
				}
				tester.Advance(90 * time.Millisecond)
				unchanged()
				tester.Advance(200 * time.Millisecond)
				unchanged()
			})
		}
	}
}

func TestNewConversationFocusesComposer(t *testing.T) {
	for _, create := range []string{"session", "workspace"} {
		t.Run(create, func(t *testing.T) {
			workspace := t.TempDir()
			a := &desktop{page: "chat", tabs: []*conversation{{ID: "old", Workspace: workspace}}}
			var other string
			visible := true
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() {
					ui.Button(c, "创建会话").OnClick(func() {
						if create == "session" {
							a.newSession()
						} else {
							a.newConversation(workspace)
						}
					})
					ui.TextInput(c.Key("other"), &other).Label("其他输入框")
					if visible {
						a.composer(c, a.current())
					}
				})
			}, 900, 400)
			// The request survives while the composer is absent from the view.
			visible = false
			tt.Frame()
			if err := tt.Click("创建会话"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			visible = true
			tt.Frame()
			if !tt.Focused("消息输入框") {
				t.Fatal("new conversation did not focus its composer")
			}
			tt.Type("直接输入")
			if a.current().Draft != "直接输入" {
				t.Fatalf("draft = %q", a.current().Draft)
			}
			if err := tt.Click("其他输入框"); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				tt.Frame()
			}
			tt.Type("保持焦点")
			if other != "保持焦点" || a.current().Draft != "直接输入" {
				t.Fatal("composer stole focus after the user moved it")
			}
		})
	}
}

// Clearing a submitted draft and showing the running state must leave the
// real composer ready for typing, including another submission during a run.
func TestComposerKeepsFocusAfterDraftIsSent(t *testing.T) {
	conv := &conversation{ID: "keep-focus", Draft: "待发送消息"}
	tt := pasteInto(t, conv)
	for _, busy := range []bool{true, true, false} {
		conv.Draft = ""
		conv.Busy = busy
		tt.Frame()
		tt.Type("继续输入")
		if conv.Draft != "继续输入" {
			t.Fatalf("composer lost focus after send: busy=%v draft=%q", busy, conv.Draft)
		}
	}
}

func TestNewSessionWhileRunningPreservesBothSessions(t *testing.T) {
	for _, compacting := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "compacting"}[compacting], func(t *testing.T) {
			old := &conversation{ID: "running-session", Workspace: t.TempDir(), Model: "test-model", Reasoning: "high", Busy: !compacting, RunID: "original-run"}
			old.uiState().CompactBusy = compacting
			a := &desktop{page: "chat", tabs: []*conversation{old}}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() {
					ui.Button(c, "新会话").OnClick(a.newSession)
					a.composer(c, a.current())
				})
			}, 900, 500)
			if err := tt.Click("新会话"); err != nil {
				t.Fatal(err)
			}
			next := a.current()
			if len(a.tabs) != 2 || a.tabs[0] != old || next == old || next.ID == old.ID || next.Workspace != old.Workspace {
				t.Fatal("new session replaced the active run instead of adding a tab")
			}
			if next.Model != old.Model || next.Reasoning != old.Reasoning || old.RunID != "original-run" {
				t.Fatal("new session lost model preferences or changed the old run")
			}
			tt.Type("新任务")
			if next.Draft != "新任务" || old.Draft != "" {
				t.Fatal("new composer did not receive focus")
			}
			// Both sessions in the same workspace keep receiving their own events.
			a.applyEvent("run:stream", map[string]any{"sessionId": old.ID, "content": "旧任务继续"})
			a.applyEvent("run:start", map[string]any{"sessionId": next.ID, "runId": "second-run"})
			a.applyEvent("run:stream", map[string]any{"sessionId": next.ID, "content": "新任务输出"})
			if len(old.Messages) != 1 || str(old.Messages[0], "content") != "旧任务继续" || len(next.Messages) != 1 || str(next.Messages[0], "content") != "新任务输出" || !next.Busy {
				t.Fatal("concurrent session output was lost or routed to the wrong session")
			}
			a.openSession(old.ID)
			if a.current() != old {
				t.Fatal("could not switch back to the original session")
			}
		})
	}
}
