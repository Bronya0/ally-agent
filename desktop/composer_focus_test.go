// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo/ui"
)

// TestComposerFocusLeavesOnSend pins what a send relies on: the composer input
// takes the keyboard focus from a click, and clearing the window's focus field
// takes it away again, so the box drops its focused edge once the message is
// gone.
func TestComposerFocusLeavesOnSend(t *testing.T) {
	a := &desktop{}
	value := ""
	focused := false
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Padding(20).Children(func() {
			input := uikit.Area(c.Key("draft"), &value).Label("消息输入框").Height(60).Width(240)
			focused = input.Focused()
			input.FocusBind(&a.composerFocus, composerFocusDraft)
		})
	}, 320, 140)
	tt.Frame()
	if focused || a.composerFocus != "" {
		t.Fatalf("input starts focused=%v field=%q", focused, a.composerFocus)
	}
	box, ok := tt.Find("消息输入框")
	if !ok {
		t.Fatal("input not found")
	}

	tt.ClickAt(box.X+box.W/2, box.Y+box.H/2)
	tt.Frame()
	if !focused || a.composerFocus != composerFocusDraft {
		t.Fatalf("after a click focused=%v field=%q", focused, a.composerFocus)
	}

	a.composerFocus = ""
	tt.Frame()
	tt.Frame()
	if focused {
		t.Fatal("input kept the focus after the focus field was cleared")
	}
}
