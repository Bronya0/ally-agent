// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The switch is the only way to stop the close button from parking the app in the
// tray, so it has to write the preference itself: the settings page's dirty check
// only covers the config draft, and a prefs field left to it would be lost on the
// way out of the page.
func TestCloseToTraySwitchWritesThePreference(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), tray: &mygo.Tray{}}
	if !a.prefs.CloseToTray {
		t.Fatal("the switch must start from the default, which hides")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Padding(16).Children(func() { a.settingsCloseBehavior(c) })
	}, 620, 260)
	if _, ok := tt.Find("关闭窗口行为"); !ok {
		t.Fatalf("missing the close-behavior card: %v", tt.Texts())
	}
	if err := tt.Click("最小化到系统托盘"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.prefs.CloseToTray {
		t.Fatal("the switch did not turn the preference off")
	}
	if a.closeHidesToTray() {
		t.Fatal("turning the switch off must make the close button quit")
	}
	// Flipping it back must restore the behavior, so the switch is not one-way.
	if err := tt.Click("最小化到系统托盘"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.prefs.CloseToTray || !a.closeHidesToTray() {
		t.Fatalf("the switch did not turn hiding back on: %+v", a.prefs)
	}
}

// Without a tray there is nothing to restore the window from, so the card says the
// button always quits instead of letting the switch promise a hidden window no one
// can reopen.
func TestCloseBehaviorNotesThatNoTrayMeansQuit(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Padding(16).Children(func() { a.settingsCloseBehavior(c) })
	}, 620, 260)
	if _, ok := tt.Find("当前系统托盘不可用，关闭按钮始终退出程序。"); !ok {
		t.Fatalf("missing the no-tray note: %v", tt.Texts())
	}
}
