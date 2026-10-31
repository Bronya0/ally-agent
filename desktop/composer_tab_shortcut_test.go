// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func tabShortcutComposer(t *testing.T, draft string) (*desktop, *ui.Tester) {
	t.Helper()
	workspace := t.TempDir()
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{
		{ID: "first", Workspace: workspace, Draft: draft},
		{ID: "second", Workspace: workspace},
		{ID: "third", Workspace: workspace},
	}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.globalShortcuts(c)
		ui.Column(c).Fill().Children(func() { a.composer(c, a.current()) })
	}, 900, 400)
	if err := tt.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	if !tt.Focused("消息输入框") {
		t.Fatal("composer did not receive focus")
	}
	return a, tt
}

func TestEmptyComposerSwitchesWorkspaceTabsWithArrows(t *testing.T) {
	a, tt := tabShortcutComposer(t, "")
	for _, step := range []struct {
		key  ui.Key
		want int
	}{
		{ui.KeyRight, 1}, {ui.KeyRight, 2}, {ui.KeyRight, 0},
		{ui.KeyLeft, 2}, {ui.KeyLeft, 1}, {ui.KeyLeft, 0},
	} {
		tt.Key(ui.Cmd, step.key)
		if a.active != step.want {
			t.Fatalf("empty focused composer chose tab %d, want %d", a.active, step.want)
		}
		if !tt.Focused("消息输入框") {
			t.Fatal("switching tabs lost composer focus")
		}
	}
	tt.Type("继续输入")
	if a.current().Draft != "继续输入" || a.tabs[1].Draft != "" || a.tabs[2].Draft != "" {
		t.Fatal("typing after tab switching went to the wrong draft")
	}
}

func TestComposerArrowTabsDoNotTakeTextNavigation(t *testing.T) {
	for _, draft := range []string{"alpha beta", " ", "\n"} {
		t.Run(draft, func(t *testing.T) {
			a, tt := tabShortcutComposer(t, draft)
			for _, key := range []ui.Key{ui.KeyLeft, ui.KeyRight} {
				tt.Key(ui.Cmd, key)
				if a.active != 0 || a.current().Draft != draft || !tt.Focused("消息输入框") {
					t.Fatalf("nonempty draft lost text navigation: tab=%d draft=%q", a.active, a.current().Draft)
				}
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Key(0, ui.KeyBackspace)
			if a.current().Draft != "" {
				t.Fatal("draft did not clear")
			}
			tt.Key(ui.Cmd, ui.KeyRight)
			if a.active != 1 {
				t.Fatal("tab switching did not become available after clearing the draft")
			}
		})
	}
}

func TestEmptyComposerArrowTabsRequireExactModifiers(t *testing.T) {
	a, tt := tabShortcutComposer(t, "")
	for _, mods := range []ui.Modifiers{0, ui.Shift, ui.Cmd | ui.Shift, ui.Cmd | ui.Alt} {
		for _, key := range []ui.Key{ui.KeyLeft, ui.KeyRight} {
			tt.Key(mods, key)
			if a.active != 0 {
				t.Fatalf("unexpected tab switch for modifiers %v", mods)
			}
		}
	}
}
