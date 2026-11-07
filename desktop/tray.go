// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

func (a *desktop) createSystemTray(icon []byte, template bool) error {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "ally-native-main",
		Icon:           icon,
		IconIsTemplate: template,
		ToolTip:        "Ally Native · 点击打开",
	})
	if err != nil {
		return err
	}
	a.tray = tray

	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "打开主窗口", Click: func(*mygo.MenuItem, *mygo.Window) { a.showMainWindow() }},
		{Label: "检查更新", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.showMainWindow()
			if a.ready {
				a.checkUpdates(false)
			}
		}},
		mygo.Separator(),
		{Label: "退出 Ally", Click: func(*mygo.MenuItem, *mygo.Window) { a.shutdown() }},
	})

	if runtime.GOOS == "linux" {
		// AppIndicator exposes a menu on activation rather than a primary-click
		// callback. Its first item restores the same main window as other OSes.
		tray.SetMenu(menu)
	} else {
		// Keep the primary click dedicated to restoring the main window. Show a
		// native menu only for a secondary click, then remove it so it cannot
		// consume the next primary click on macOS.
		tray.OnClick(a.showMainWindow)
		tray.OnRightClick(func() {
			tray.SetMenu(menu)
			popUpSystemTrayMenu(tray)
			tray.SetMenu(nil)
		})
	}
	if runtime.GOOS == "windows" {
		off := mygo.Screen.OnDisplaysChanged(func() {
			next, template, err := systemTrayIcon()
			if err == nil && !bytes.Equal(next, icon) {
				err = tray.SetIcon(next, template)
				if err == nil {
					icon = next
				}
			}
			if err != nil {
				log.Printf("system tray icon refresh failed: %v", err)
			}
		})
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) { off() })
	}
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { tray.Destroy() })
	return nil
}

func (a *desktop) showMainWindow() {
	if a.win == nil || a.closing || a.quitReady {
		return
	}
	// Restore can reveal a hidden window without firing OnShow; show it first.
	a.win.Show()
	if a.win.IsMinimized() {
		a.win.Restore()
	}
	a.win.Focus()
	a.win.Invalidate()
}

func (a *desktop) mainWindowShown() {
	a.mainWindowHidden = false
	a.cancelMemoryTidy()
	a.syncPluginHost()
	a.restoreAttachmentImages(a.current())
}

// Stop file media and release decoded bitmaps immediately. Native WebView slots
// survive only until the existing hidden-window grace period expires.
func (a *desktop) releaseHiddenWindowResources() {
	a.suspendFilePreview()
	a.syncPluginHostVisibility(true)
	seen := make(map[*conversation]struct{}, len(a.tabs)+len(a.openSessions))
	release := func(t *conversation) {
		if t == nil {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		a.releaseAttachmentImages(t)
	}
	for _, t := range a.tabs {
		release(t)
	}
	for _, t := range a.openSessions {
		release(t)
	}
}
