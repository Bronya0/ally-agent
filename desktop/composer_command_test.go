// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTabCompletionMovesCaretToEnd(t *testing.T) {
	conv := &conversation{ID: "cmd-tab-caret"}
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.composer(c, conv)
	}, 800, 400)
	if err := tester.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	tester.Type("/session")
	tester.Key(0, ui.KeyTab)
	tester.Type(" x")
	if got, want := conv.Draft, "/sessions x"; got != want {
		t.Fatalf("typing after Tab completion produced %q, want %q", got, want)
	}
}

func TestTabCompletedCommandRunsOnEnterWithoutSpace(t *testing.T) {
	workspace := t.TempDir()
	conv := &conversation{ID: "cmd-tab-enter", Workspace: workspace}
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.composer(c, conv)
	}, 800, 400)
	if err := tester.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	tester.Type("/session")
	tester.Key(0, ui.KeyTab)
	tester.Key(0, ui.KeyEnter)
	if !conv.uiState().SessionsOpen {
		t.Fatal("Enter after Tab completion did not run /sessions")
	}
}

func TestTabCompletedCommandRemainsClickable(t *testing.T) {
	workspace := t.TempDir()
	conv := &conversation{ID: "cmd-tab", Workspace: workspace}
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.composer(c, conv)
	}, 800, 400)
	if err := tester.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	tester.Type("/session")
	if !conv.uiState().CmdOpen {
		t.Fatal("typing a slash command did not open the command menu")
	}
	tester.Key(0, ui.KeyTab)
	if conv.Draft != "/sessions" {
		t.Fatalf("Tab completion = %q, want /sessions", conv.Draft)
	}
	if !conv.uiState().CmdOpen {
		t.Fatal("Tab completion closed the command menu before the user could activate the command")
	}
	if got, want := conv.uiState().DraftCaret, len([]rune(conv.Draft)); got != want {
		t.Fatalf("caret index = %d, want end of completed command (%d)", got, want)
	}
	if err := tester.Click("/sessions"); err != nil {
		t.Fatal(err)
	}
	if !conv.uiState().SessionsOpen {
		t.Fatal("clicking the completed /sessions command did not open the session picker")
	}
}
