// SPDX-License-Identifier: GPL-3.0-only
package app

import "errors"

// Keep the URL (including retained identity hints) exclusively in the backend.
func (a *App) openChatGPTAuthorization(url string) error {
	if a.wails == nil || a.wails.app == nil {
		return errors.New("Desktop browser unavailable")
	}
	return a.wails.app.Browser.OpenURL(url)
}
