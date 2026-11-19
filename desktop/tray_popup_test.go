// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestDrawnTrayMenuSelectsProxyAndActions(t *testing.T) {
	for _, tc := range []struct {
		label string
		index int
	}{{"跟随系统代理", 7}, {"project", 5}, {"打开主窗口", 2}} {
		t.Run(tc.label, func(t *testing.T) {
			open, choice := true, -1
			a := &desktop{ready: true, tabs: []*conversation{{ID: "tab", Workspace: "project"}}}
			tt := ui.NewTester(func(c *ui.Context) {
				c.DisableAnimations()
				c.SetTheme(uikit.TrayMenuTheme(nativeFontFamily()).WithPreferences(c.Preferences()))
				items := a.trayMenuItems()
				entries := make([]uikit.TrayMenuEntry, len(items))
				for i, item := range items {
					entries[i] = uikit.TrayMenuEntry{Key: item.Key, Label: item.Label, AccessibilityLabel: item.AccessibilityLabel, Icon: item.Icon, Checkable: item.Checkable, Checked: item.Checked, Disabled: item.Disabled, SeparatorBefore: item.SeparatorBefore}
					for _, child := range item.Children {
						entries[i].Children = append(entries[i].Children, uikit.TrayMenuEntry{Key: child.Key, Label: child.Label, AccessibilityLabel: child.AccessibilityLabel, Icon: child.Icon, StatusColor: child.StatusColor, Checkable: child.Checkable, Checked: child.Checked, Disabled: child.Disabled})
					}
				}
				if got := uikit.TrayMenu(c.Key("tray-menu"), &open, "Ally", entries); got >= 0 {
					choice = got
				}
			}, 500, 300)
			if tc.label == "跟随系统代理" {
				if err := tt.Click("网络代理"); err != nil {
					t.Fatal(err)
				}
			} else if tc.label == "project" {
				if err := tt.Click("工作区"); err != nil {
					t.Fatal(err)
				}
			}
			if err := tt.Click(tc.label); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if open || choice != tc.index {
				t.Fatalf("open=%v choice=%d, want false/%d", open, choice, tc.index)
			}
		})
	}
}
