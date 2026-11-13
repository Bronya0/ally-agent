// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The switch changes the close behavior in both directions.
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
