// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// A built-in command used to reach the model as plain text whenever the menu was
// closed, as it is after Tab completion or a trailing space. Enter must run it.
func TestTypedBuiltinCommandRunsInsteadOfSending(t *testing.T) {
	conv := &conversation{ID: "cmd-typed", Workspace: t.TempDir()}
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.composer(c, conv)
	}, 800, 400)
	if err := tester.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	tester.Type("/sessions ")
	tester.Key(0, ui.KeyEnter)
	if !conv.uiState().SessionsOpen {
		t.Fatal("a typed /sessions did not open the session picker")
	}
	if len(conv.Messages) != 0 {
		t.Fatalf("a command was sent as a message: %v", conv.Messages)
	}
}

// A skill still goes to the model: the backend reads "/skill args" from the text.
func TestTypedSkillIsStillSent(t *testing.T) {
	conv := &conversation{ID: "skill-typed", Workspace: t.TempDir(), Model: "m1"}
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv},
		skills: []backend.SkillDefinition{{Name: "review-nav"}},
		config: backend.ConfigState{Models: []backend.ModelConfig{{ProviderName: "p", Model: "m1"}}}}
	if cmd := a.builtinCommandFor(conv, "/review-nav "); cmd != nil {
		t.Fatalf("a skill must not be taken for a built-in command: %+v", cmd)
	}
	if cmd := a.builtinCommandFor(conv, "/sessions"); cmd == nil || cmd.Label != "sessions" {
		t.Fatalf("builtin command lookup = %+v, want sessions", cmd)
	}
	if cmd := a.builtinCommandFor(conv, "/sessions extra"); cmd != nil {
		t.Fatalf("a command with arguments must not run as one: %+v", cmd)
	}
	if cmd := a.builtinCommandFor(conv, "  /sessions  "); cmd == nil || cmd.Label != "sessions" {
		t.Fatalf("a padded command lookup = %+v, want sessions", cmd)
	}
	if cmd := a.builtinCommandFor(conv, "sessions"); cmd != nil {
		t.Fatal("text without the slash is not a command")
	}
}
