// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"
	"bytes"
	"fmt"
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

func (a *desktop) createSystemTray(icon []byte, template bool) error {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "ally-native-main",
		Icon:           icon,
		IconIsTemplate: template,
		ToolTip:        locales.Text("Ally Native · 点击打开"),
	})
	if err != nil {
		return err
	}
	a.tray = tray

	var items []*mygo.MenuItem
	var offClick, offRightClick func()
	if runtime.GOOS == "linux" {
		// AppIndicator does not reliably deliver icon clicks or an anchor. Keep
		// its system-menu fallback; Windows/macOS never attach a native menu.
		menu, err := newSystemTrayMenu(a.showMainWindow, a.checkTrayUpdates, a.shutdown)
		if err != nil {
			a.tray = nil
			tray.Destroy()
			return err
		}
		items = menu.Items()
		tray.SetMenu(menu)
	} else {
		offClick = tray.OnClick(a.showMainWindow)
		offRightClick = tray.OnRightClick(a.toggleTrayMenu)
	}
	offLocale := mygo.App.OnLocaleChanged(func(string) {
		tray.SetToolTip(locales.Text("Ally Native · 点击打开"))
		if len(items) != 0 {
			items[0].SetLabel(locales.Text("打开主窗口"))
			items[1].SetLabel(locales.Text("检查更新"))
			items[3].SetLabel(locales.Text("退出 Ally"))
		}
		if menu := a.trayMenu; menu != nil && menu.win != nil {
			menu.win.Invalidate()
		}
	})
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
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		offLocale()
		if offClick != nil {
			offClick()
			offRightClick()
		}
		a.closeTrayMenu()
		tray.SetMenu(nil)
		tray.Destroy()
		if a.tray == tray {
			a.tray = nil
		}
	})
	return nil
}

// newSystemTrayMenu is the Linux AppIndicator fallback. Template icons use the
// same line artwork as the self-drawn menu and follow the panel's text color.
func newSystemTrayMenu(openMainWindow, checkUpdates, quit func()) (*mygo.Menu, error) {
	var menuIcons [3]*mygo.MenuIcon
	for i, name := range []string{"home", "refresh", "power"} {
		icon, err := mygo.NewMenuIconSVG(iconImage(name), true)
		if err != nil {
			return nil, fmt.Errorf("system tray menu icon %q: %w", name, err)
		}
		menuIcons[i] = icon
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: locales.Text("打开主窗口"), Icon: menuIcons[0], Click: func(*mygo.MenuItem, *mygo.Window) { openMainWindow() }},
		{Label: locales.Text("检查更新"), Icon: menuIcons[1], Click: func(*mygo.MenuItem, *mygo.Window) { checkUpdates() }},
		mygo.Separator(),
		{Label: locales.Text("退出 Ally"), Icon: menuIcons[2], Click: func(*mygo.MenuItem, *mygo.Window) { quit() }},
	}), nil
}

func (a *desktop) showMainWindow() {
	a.closeTrayMenu()
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

// Release attachment bitmaps, but retain preview pages for the next show.
func (a *desktop) releaseHiddenWindowResources() {
	a.hideFilePreview()
	a.closeImagePreview()
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
