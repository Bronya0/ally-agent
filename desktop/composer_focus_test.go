// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

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
