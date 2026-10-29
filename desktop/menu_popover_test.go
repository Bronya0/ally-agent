// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The rows that used to open the operating system's menu now open the app's own
// panel: the same chrome and rows as every other menu, dismissed by choosing,
// Escape or a press outside, and toggled shut by its own button.
func TestMenuPopoverButtonOpensAppPanel(t *testing.T) {
	chosen := ""
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(ui.DarkTheme())
		c.DisableAnimations()
		ui.Column(c).Fill().Padding(12).Children(func() {
			button := mutedIconAction(c, "export", "导出", "download", 22, nil)
			menuPopoverButton(c, button, 180, func(dismiss func()) {
				popoverItem(c, "answer", "导出回答", "", false, func() {
					dismiss()
					chosen = "answer"
				})
				popoverItem(c, "session", "导出会话", "", false, func() {
					dismiss()
					chosen = "session"
				})
			})
		})
	}, 320, 240)
	tester.Frame()
	if tester.HasText("导出回答") {
		t.Fatal("the panel showed before its button was pressed")
	}
	if err := tester.Click("导出"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("导出回答") || !tester.HasText("导出会话") {
		t.Fatalf("the button did not open the app panel: %v", tester.Texts())
	}
	if err := tester.Click("导出回答"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if chosen != "answer" || tester.HasText("导出会话") {
		t.Fatalf("choosing an item did not run it and close the panel: chosen=%q texts=%v", chosen, tester.Texts())
	}
	if err := tester.Click("导出"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("导出"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if tester.HasText("导出会话") {
		t.Fatal("pressing the button again did not close the panel")
	}
	for _, dismiss := range []string{"escape", "outside"} {
		if err := tester.Click("导出"); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
		if dismiss == "escape" {
			tester.Key(0, ui.KeyEscape)
		} else {
			tester.ClickAt(4, 200)
		}
		tester.Frame()
		if tester.HasText("导出会话") {
			t.Fatalf("the panel stayed open after %s", dismiss)
		}
	}
	if chosen != "answer" {
		t.Fatal("dismissing the panel chose an item")
	}
}
