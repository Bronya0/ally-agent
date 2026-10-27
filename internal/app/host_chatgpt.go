// SPDX-License-Identifier: GPL-3.0-only
package app

import "github.com/egoist/mygo"

func (a *App) openChatGPTAuthorization(url string) error { return mygo.Shell.OpenExternal(url) }
