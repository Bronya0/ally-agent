// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The round footer only shows its controls while the message row is hovered. Its
// menu draws in the window's overlay, outside that row, so the row stops being
// hovered as soon as the pointer moves onto the panel: the controls must stay in
// the view (faded and inert) or the open panel disappears with them.
func TestRoundStatsMenuStaysOpenUnderThePointer(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "conv"}
	stat := &roundStat{DurationMS: 1500, OutputTokens: 120, Done: true}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Padding(10).Children(func() {
			row := ui.Row(c.Key("message-row")).FillWidth().Height(28)
			show := row.Hovered()
			row.Children(func() {
				a.roundStatsRow(c, conv, 0, stat, show)
			})
		})
	}, 420, 300)
	tester.Frame()
	if tester.HasText("导出回答") {
		t.Fatal("the export panel showed before the footer was used")
	}
	// The footer only appears while the message row itself is hovered.
	tester.Move(40, 20)
	tester.Frame()
	button, ok := tester.Find("导出")
	if !ok {
		t.Fatalf("missing footer controls: %v", tester.Texts())
	}
	tester.Move(button.X+button.W/2, button.Y+button.H/2)
	tester.Frame()
	if err := tester.Click("导出"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("导出回答") {
		t.Fatalf("the export panel did not open: %v", tester.Texts())
	}
	item, ok := tester.Find("导出回答")
	if !ok {
		t.Fatalf("missing the first entry: %v", tester.Texts())
	}
	tester.Move(item.X+item.W/2, item.Y+item.H/2)
	tester.Frame()
	if !tester.HasText("导出回答") || !tester.HasText("导出会话") {
		t.Fatalf("the panel closed as the pointer left the row: %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if tester.HasText("导出回答") {
		t.Fatal("Escape did not close the panel")
	}
}
