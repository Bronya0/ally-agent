// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
)

func (a *desktop) trayMenuItems() []uikit.TrayItem {
	noSeparator := false
	return []uikit.TrayItem{
		{Key: "open", Label: locales.Text("打开主窗口"), Icon: iconImage("home"), OnClick: a.showMainWindow},
		{Key: "update", Label: locales.Text("检查更新"), Icon: iconImage("refresh"), SeparatorBefore: true, NativeSeparatorBefore: &noSeparator, OnClick: a.checkTrayUpdates},
		{Key: "quit", Label: locales.Text("退出 Ally"), Icon: iconImage("power"), SeparatorBefore: true, OnClick: a.shutdown},
	}
}

func (a *desktop) checkTrayUpdates() {
	a.showMainWindow()
	if a.ready && !a.closing && !a.quitReady {
		a.checkUpdates(false)
	}
}
