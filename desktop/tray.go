// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"bytes"
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

func (a *desktop) createSystemTray(icon []byte, template bool) error {
	tray, err := uikit.NewTray(uikit.TrayOptions{
		ID:             "ally-native-main",
		Icon:           icon,
		IconIsTemplate: template,
		Tooltip:        locales.Text("Ally Native · 点击打开"),
		MenuLabel:      "Ally",
		Font:           nativeFontFamily(),
		Items:          a.trayMenuItems(),
		ItemsForOpen:   a.trayMenuItems,
		MaxWidth:       300,
		OnClick:        a.showMainWindow,
		CanOpen:        func() bool { return !a.closing && !a.quitReady },
	})
	if err != nil {
		return err
	}
	a.tray = tray

	offLocale := mygo.App.OnLocaleChanged(func(string) {
		tray.SetTooltip(locales.Text("Ally Native · 点击打开"))
		if err := tray.SetItems(a.trayMenuItems()); err != nil {
			log.Printf("tray menu refresh failed: %v", err)
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
		tray.Close()
		if a.tray == tray {
			a.tray = nil
		}
	})
	return nil
}

// Linux's native indicator does not report menu opening. Refresh it only on
// tab, run and proxy changes. The custom flyout takes its snapshot on opening.
func (a *desktop) refreshNativeTray() {
	if runtime.GOOS == "linux" && a.tray != nil && !a.closing && !a.quitReady {
		if err := a.tray.SetItems(a.trayMenuItems()); err != nil {
			log.Printf("tray menu refresh failed: %v", err)
		}
	}
}

func (a *desktop) showMainWindow() {
	if a.tray != nil {
		a.tray.CloseMenu()
	}
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
