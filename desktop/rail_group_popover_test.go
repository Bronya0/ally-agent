// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// The rail group used to open the operating system's menu. Its pages now list in
// the app's own panel, and the current page is the tinted one there.
func TestRailGroupOpensAppPanel(t *testing.T) {
	a := &desktop{ready: true, page: "stats"}
	group := []backend.PluginInfo{
		{ID: "a", Name: "插件甲", Mode: "stats"},
		{ID: "b", Name: "插件乙", Mode: "tasks"},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Width(220).Padding(8).Children(func() {
			a.railGroupButton(c, true, group[0], "插件组", group, true)
		})
	}, 420, 320)
	tester.Frame()
	if tester.HasText("插件乙") {
		t.Fatal("the group panel showed before the entry was pressed")
	}
	if err := tester.Click("插件组"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	for _, name := range []string{"插件甲", "插件乙"} {
		if !tester.HasText(name) {
			t.Fatalf("missing %q in the group panel: %v", name, tester.Texts())
		}
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if tester.HasText("插件乙") {
		t.Fatal("Escape did not close the group panel")
	}
}
