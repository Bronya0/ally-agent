// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// A popup owns one generation of view state. Neither its delayed selection nor
// its OnClosed callback may operate on a later popup.
type trayMenuState struct {
	win     *mygo.Window
	open    bool
	pending bool
}

func (a *desktop) toggleTrayMenu() {
	if a.tray == nil || a.closing || a.quitReady {
		return
	}
	if a.trayMenu != nil {
		a.closeTrayMenu()
		return
	}
	position := captureTrayMenuPosition()
	menu := &trayMenuState{open: true}
	a.trayMenu = menu
	width, height := uikit.TrayMenuInitialSize(3, 2)
	menu.win = mygo.NewFlyout(mygo.FlyoutOptions{
		// Tray retains platform dismissal and works with a hidden/minimized main
		// window. Windows overrides the icon anchor with the captured click.
		Tray: a.tray, Placement: mygo.PlacementBottom,
		Width: width, Height: height, FitContent: true,
		Focusable: true, Hidden: true,
		// The view draws the border, rounded corners and shadow once, including
		// on Windows 10. A system shadow would add a second, square outline.
		Content: ui.View(func(c *ui.Context) {
			choice := renderTrayMenu(c, &menu.open)
			if choice >= 0 || !menu.open {
				a.finishTrayMenu(menu, choice)
			}
		}),
	})
	menu.win.OnClosed(func() {
		menu.open = false
		if a.trayMenu == menu {
			a.trayMenu = nil
		}
	})
	if position != nil {
		position(menu.win)
	}
	menu.win.Show()
}

// finishTrayMenu schedules selection/dismissal after the current build. Closing
// the flyout while it is still building would invalidate the renderer's nodes.
func (a *desktop) finishTrayMenu(menu *trayMenuState, choice int) {
	if menu == nil || a.trayMenu != menu || menu.pending || menu.win == nil {
		return
	}
	menu.pending = true
	menu.win.Update(func() {
		if a.trayMenu != menu || menu.win.IsDestroyed() {
			return
		}
		a.closeTrayMenu()
		if a.closing || a.quitReady {
			return
		}
		switch choice {
		case 0:
			a.showMainWindow()
		case 1:
			a.checkTrayUpdates()
		case 2:
			a.shutdown()
		}
	})
}

func (a *desktop) closeTrayMenu() {
	menu := a.trayMenu
	a.trayMenu = nil
	if menu != nil {
		menu.open = false
		if menu.win != nil {
			menu.win.Destroy()
		}
	}
}

func (a *desktop) checkTrayUpdates() {
	a.showMainWindow()
	if a.ready && !a.closing && !a.quitReady {
		a.checkUpdates(false)
	}
}

func renderTrayMenu(c *ui.Context, open *bool) int {
	// Match the supplied light menu independently of the main app's palette.
	// System accessibility preferences still apply to text and motion.
	c.SetTheme(uikit.TrayMenuTheme(nativeFontFamily()).WithPreferences(c.Preferences()))
	c.Root().Background(ui.Transparent)
	return uikit.TrayMenu(c.Key("tray-menu"), open, "Ally", []uikit.TrayMenuEntry{
		{Key: "open", Label: locales.Text("打开主窗口"), Icon: iconImage("home")},
		{Key: "update", Label: locales.Text("检查更新"), Icon: iconImage("refresh"), SeparatorBefore: true},
		{Key: "quit", Label: locales.Text("退出 Ally"), Icon: iconImage("power"), SeparatorBefore: true},
	})
}
