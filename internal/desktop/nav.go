// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"strings"
)

// changePage switches the main area and lazily loads the data that page shows.
// Loading happens in a goroutine and publishes through Window.Update so the
// view never touches the backend.
func (a *desktop) changePage(page string) {
	if !a.ready {
		return
	}
	if a.page == "settings" && page != "settings" {
		a.commitSettingsOnLeave()
	}
	if a.page == "models" && page != "models" {
		a.cancelOAuthWhenLeaving()
	}
	a.page = page
	a.pageEpoch++
	switch page {
	case "plugins":
		a.refreshPlugins()
		return
	case "models", "chat":
		if page == "models" && a.modelTab != "图片模型" && a.modelDraft.AuthType == "chatgpt_oauth" && a.oauth != nil {
			a.loadOAuthAccounts()
		}
		if page == "models" && a.imageModels != nil && !a.imageModels.busy && !a.imageModels.formOpen && a.imageModels.test == nil {
			a.imageModels = nil
		}
		return
	}
	if strings.HasPrefix(page, "plugin:") {
		return
	}
	epoch := a.pageEpoch
	go func() {
		a.win.Update(func() {
			if a.pageEpoch != epoch {
				return
			}
			a.refreshPage(page)
		})
	}()
}

// refreshPage reloads the current page's data. It is called from the update
// callback or from a goroutine that only publishes, never from the view.
func (a *desktop) refreshPage(page string) {
	switch page {
	case "settings":
		a.loadSettings()
	case "mcp":
		a.refreshMCPAll()
	case "skills":
		a.loadSkills()
	case "ssh":
		a.refreshSSH()
	case "tasks":
		a.loadTaskCenter()
	case "stats":
		a.loadStats()
	case "kb":
		a.kbRefresh()
	}
}

func (a *desktop) saveConfig(cfg backend.ConfigState, notice ...string) {
	if a.core == nil || a.win == nil {
		return
	}
	previous := a.configSaveTail
	done := make(chan struct{})
	a.configSaveTail = done
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		err := a.core.SaveConfig(cfg)
		var fresh backend.ConfigState
		if err == nil {
			fresh, err = a.core.GetConfig()
		}
		a.win.Update(func() {
			if err != nil {
				a.notify(err.Error())
				return
			}
			a.config = fresh
			if len(notice) > 0 {
				a.notify(notice[0])
			}
		})
	}()
}
