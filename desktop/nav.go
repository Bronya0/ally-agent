// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"ally-dev/locales"
	"strings"
)

// changePage switches the main area and lazily loads the data that page shows.
// Loading happens in a goroutine and publishes through Window.Update so the
// view never touches the backend.
func (a *desktop) changePage(page string) {
	if !a.ready {
		return
	}
	// These pages are intentionally not connected to navigation yet.
	if page == "kb" || page == "games" {
		return
	}
	if page != "chat" {
		if a.page == "chat" {
			a.resetFileExplorer()
		}
		a.hideFilePreview()
	}
	if a.page == "settings" && page != "settings" {
		a.commitSettingsOnLeave()
	}
	if a.page == "models" && page != "models" {
		a.cancelVoiceRecording()
		a.cancelOAuthWhenLeaving()
	}
	a.page = page
	a.pageEpoch++
	if page == "settings" {
		a.beginSettings()
	}
	if page == "settings" && voiceInputSupported() && !a.voiceInputDevicesScanned {
		// The scan is a COM enumeration, so it waits until someone opens the page that
		// shows the device list instead of running on every start.
		a.refreshVoiceInputDevices()
	}
	if page == "chat" {
		a.activateTab(a.active)
	}
	switch page {
	case "plugins":
		a.refreshPlugins()
		return
	case "models", "chat":
		if page == "models" && a.modelTab == "对话模型" && a.modelDraft.AuthType == "chatgpt_oauth" && a.oauth != nil {
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
	a.win.Update(func() {
		if a.pageEpoch != epoch {
			return
		}
		a.refreshPage(page)
	})
}

// refreshPage reloads the current page's data. It is called from the update
// callback, never from the view.
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
	}
}

// saveConfig writes the config in the background, queued behind every write
// that came before it, and publishes the reloaded config once it lands.
func (a *desktop) saveConfig(cfg backend.ConfigState, notice ...string) {
	if a.core == nil || a.win == nil {
		return
	}
	previous, done := a.configWriteSlot()
	go func() {
		defer close(done)
		waitConfigWrite(previous)
		err := a.core.SaveConfig(cfg)
		a.writeFailures.record("config", err)
		var fresh backend.ConfigState
		if err == nil {
			fresh, err = a.core.GetConfig()
		}
		a.win.Update(func() {
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			a.config = fresh
			if len(notice) > 0 {
				a.notifySuccess(notice[0])
			} else {
				a.notifySuccess(locales.Text("设置已保存"))
			}
		})
	}()
}

// configWriteSlot reserves a place in the queue every config write joins: the
// settings pages, the thinking level, the tool toggles and a model save. It
// returns the write this one must wait for and the signal the next writer waits
// on, so two of them never interleave and a later one cannot overwrite what an
// earlier one just saved. Close done once the write and its publish are through.
func (a *desktop) configWriteSlot() (chan struct{}, chan struct{}) {
	previous := a.configSaveTail
	done := make(chan struct{})
	a.configSaveTail = done
	return previous, done
}

// waitConfigWrite holds a background writer until the write before it landed.
func waitConfigWrite(previous chan struct{}) {
	if previous != nil {
		<-previous
	}
}
