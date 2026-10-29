// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"time"

	backend "ally-dev/internal/app"
)

// autostartValue mirrors the OS state so the switch can detect a toggle.
// Set by loadAutostart / setAutostart only.

func (a *desktop) loadAutostart() {
	go func() {
		enabled, _ := a.core.GetAutostartEnabled()
		a.win.Update(func() {
			a.autostartValue = enabled
			a.settings.Autostart = enabled
		})
	}()
}

func (a *desktop) setAutostart(enabled bool) {
	a.autostartValue = enabled
	go func() {
		err := a.core.SetAutostartEnabled(enabled)
		a.win.Update(func() {
			if err != nil {
				// notify, not status: a failed toggle springs back on its own, so the
				// reason has to show up somewhere the user is already looking.
				a.notifyError("设置开机自启失败：" + err.Error())
				a.settings.Autostart = !enabled
				a.autostartValue = !enabled
			}
		})
	}()
}

func (a *desktop) selectBackgroundImage() {
	go func() {
		path, err := a.core.SelectBackgroundImage()
		if err != nil {
			a.fail(err)
			return
		}
		if path == "" {
			return
		}
		a.win.Update(func() {
			a.configDraft.BackgroundImage = path
			a.saveSettings()
		})
	}()
}

func (a *desktop) clearBackgroundImage() {
	go func() {
		if err := a.core.ClearBackgroundImage(); err != nil {
			a.fail(err)
			return
		}
		a.win.Update(func() {
			a.configDraft.BackgroundImage = ""
			a.settings.BgOpacity = 0.15
			a.saveSettings()
		})
	}()
}

func nowUnixNano() int64 { return time.Now().UnixNano() }

var _ = backend.ConfigState{}
