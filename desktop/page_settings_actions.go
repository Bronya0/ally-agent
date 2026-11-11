// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "path/filepath"

type autostartHost interface {
	GetAutostartEnabled() (bool, error)
	SetAutostartEnabled(bool) error
}

// initializeAutostart applies the default once. Later launches only read the
// system state, so disabling it in Ally or in the OS remains effective.
func initializeAutostart(host autostartHost, initialized bool) (bool, bool, error) {
	enabled, err := host.GetAutostartEnabled()
	if err != nil {
		return false, initialized, err
	}
	if initialized || enabled {
		return enabled, true, nil
	}
	if err := host.SetAutostartEnabled(true); err != nil {
		return false, false, err
	}
	return true, true, nil
}

func (a *desktop) selectBackgroundImage() {
	go func() {
		path, err := a.core.SelectBackgroundImagePath()
		if err != nil {
			a.fail(err)
			return
		}
		if path == "" {
			return
		}
		a.win.Update(func() {
			a.configDraft.BackgroundImage = filepath.Base(path)
			a.settings.BackgroundPath = &path
		})
	}()
}

func (a *desktop) clearBackgroundImage() {
	a.configDraft.BackgroundImage = ""
	a.settings.BgOpacity = 0.15
	path := ""
	a.settings.BackgroundPath = &path
}
