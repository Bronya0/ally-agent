// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The market menu button shows its label once. caretButton used to draw the
// label in a row of its own inside the button as well, which showed it twice on
// the skills and MCP pages: 市场 市场 ⌄.
func TestMarketMenuButtonDrawsItsLabelOnce(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Row(c).Padding(10).Children(func() {
			a.linkMenu(c, "links", [][2]string{{"魔搭 ModelScope", "https://modelscope.cn"}})
		})
	}, 400, 140)
	count := 0
	for _, text := range tt.Texts() {
		if text == "市场" {
			count++
		}
	}
	// The button names itself with its label and draws it as text: a second
	// drawn copy would count three.
	if count != 2 {
		t.Fatalf("市场 drawn %d times, want a name and one text: %v", count, tt.Texts())
	}
}
