package desktop

import (
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
)

const updateCheckInterval = 2 * time.Hour

func updateApplyLabel(goos string) string {
	if goos == "windows" {
		return "安装更新并退出"
	}
	return "立即重启更新"
}

func updateReadyNote(goos string) string {
	if goos == "windows" {
		return "安装前保存会话、草稿与设置。安装后退出，请重新打开 Ally。"
	}
	return "安装前保存会话、草稿与设置，完成后自动重新打开 Ally。"
}

func (u *updateUI) installing() bool {
	return u.State == "preparing" || u.State == "applying" || u.State == "restarting"
}

func (a *desktop) stopUpdateChecks() {
	if a.upd.Timer != nil {
		a.upd.Timer.Stop()
		a.upd.Timer = nil
	}
}

func (a *desktop) installedRelease() bool {
	return packagedReleaseVersion(mygo.App.Version(), a.upd.ReleaseVer)
}

func packagedReleaseVersion(current, bundled string) bool {
	return stableReleasePattern.MatchString(bundled) &&
		strings.TrimPrefix(bundled, "v") == strings.TrimPrefix(current, "v")
}

func (a *desktop) scheduleUpdateCheck(delay time.Duration) {
	a.stopUpdateChecks()
	// Local builds deliberately have no bundled release tag: never offer the
	// old main release as an "upgrade" from a development MyGo binary.
	if !a.ready || a.closing || !a.installedRelease() {
		return
	}
	a.upd.Timer = time.AfterFunc(delay, func() {
		a.win.Update(func() {
			if a.closing {
				return
			}
			a.checkUpdates(true)
			a.scheduleUpdateCheck(updateCheckInterval)
		})
	})
}

func (a *desktop) startUpdate() { a.downloadUpdate(false) }

func (a *desktop) downloadUpdate(silent bool) {
	if a.closing || a.upd.Busy || a.upd.CheckBusy || a.upd.Applied || a.core == nil {
		return
	}
	tag := a.upd.Tag
	if tag == "" {
		a.checkUpdates(false)
		return
	}
	if !a.upd.CanAuto {
		a.upd.State = "available"
		a.upd.Open = true
		return
	}
	a.upd.Open = !silent
	a.upd.State, a.upd.Error = "downloading", ""
	a.upd.Progress, a.upd.BytesNow, a.upd.BytesTotal = 0, 0, 0
	a.upd.Busy, a.upd.Silent, a.upd.Cancelling = true, silent, false
	a.upd.RetryApply = false
	go func() {
		result := a.core.DownloadUpdate(tag)
		a.win.Update(func() {
			a.upd.Busy = false
			if a.upd.Cancelling || result.Error == "cancelled" {
				a.upd.State, a.upd.Cancelling = "idle", false
				return
			}
			if !result.OK {
				a.updateFailed(result.Error)
				return
			}
			a.upd.State, a.upd.Progress, a.upd.Open = "ready", 100, true
		})
	}()
}

func (a *desktop) updateFailed(message string) {
	a.upd.Busy = false
	a.upd.State, a.upd.Error = "error", message
	if !a.upd.Silent || a.upd.Applied {
		a.upd.Open = true
	}
	if a.upd.Silent {
		a.settings.UpdateNote = "更新失败：" + message
	}
}

func (a *desktop) applyUpdate() {
	if a.core == nil || a.win == nil || a.closing || a.upd.Busy || a.upd.CheckBusy || !a.upd.CanAuto || (a.upd.State != "ready" && !a.upd.Applied && !a.upd.RetryApply) {
		return
	}
	if a.modelSaving || a.mcpF.Busy || a.sshF.Busy {
		a.notify("配置正在保存，请稍后更新")
		return
	}
	for _, t := range a.tabs {
		if t.AttachmentLoading || len(t.PendingAttachments) > 0 {
			a.notify("请先发送或移除待发送附件，再安装更新")
			return
		}
	}
	a.upd.Busy, a.upd.Silent, a.upd.Open = true, false, true
	a.upd.State = "preparing"
	a.upd.RetryApply = true
	proceed := func() {
		a.flushWindowWrites(func(err error) {
			if err != nil {
				a.updateFailed(err.Error())
				return
			}
			if a.upd.Applied {
				a.quitAfterUpdate()
				return
			}
			tag := a.upd.Tag // captured on the UI thread, never read by a worker
			a.upd.State = "applying"
			go func() {
				result := a.core.ApplyUpdate(tag)
				a.win.Update(func() {
					if !result.OK {
						a.upd.RetryApply = false
						a.updateFailed(result.Error)
						return
					}
					a.upd.Applied = true
					// Stopping runs can enqueue final messages and session writes.
					a.flushWindowWrites(func(err error) {
						if err != nil {
							a.updateFailed(err.Error())
							return
						}
						a.quitAfterUpdate()
					})
				})
			}()
		})
	}
	if a.upd.Applied {
		proceed()
		return
	}
	a.prepareWindowClose(proceed, func() { a.upd.Busy = false; a.upd.State = "ready" })
}

func (a *desktop) quitAfterUpdate() {
	a.upd.State = "restarting"
	if err := a.core.QuitForUpdate(); err != nil {
		a.updateFailed(err.Error())
	}
}

func (a *desktop) skipUpdate() {
	if a.upd.Busy || a.upd.CheckBusy || a.upd.Applied {
		return
	}
	tag := a.upd.Tag
	a.upd.Busy, a.upd.Silent = true, false
	go func() {
		result := a.core.SkipUpdate(tag)
		a.win.Update(func() {
			a.upd.Busy = false
			if !result.OK {
				a.updateFailed(result.Error)
				return
			}
			a.prefs.DismissedUpdateTag = tag
			a.savePrefs()
			a.upd.State, a.upd.Open = "available", false
		})
	}()
}

func (a *desktop) checkUpdates(silent bool) {
	if a.core == nil || a.closing || a.upd.CheckBusy || a.upd.Busy || a.upd.Applied {
		return
	}
	if !a.installedRelease() {
		a.settings.UpdateNote = "开发构建请手动安装正式发行版"
		return
	}
	a.upd.CheckBusy, a.settings.UpdateBusy = true, true
	if !silent {
		a.upd.State, a.upd.Open = "checking", true
	}
	go func() {
		result := a.core.CheckForUpdates()
		a.win.Update(func() {
			a.upd.CheckBusy, a.settings.UpdateBusy = false, false
			if a.closing {
				return
			}
			if a.acceptUpdateCheck(result, mygo.App.Version(), silent) {
				a.downloadUpdate(silent)
			}
		})
	}()
}

// A single decision path for startup, periodic checks and the manual button.
func (a *desktop) acceptUpdateCheck(result backend.CheckForUpdatesResult, current string, silent bool) bool {
	if !result.OK {
		a.settings.UpdateNote = "检查更新失败：" + result.Error
		if !silent {
			a.upd.Silent = false
			a.updateFailed(result.Error)
		}
		return false
	}
	if !newerStableRelease(result.Tag, current) {
		a.upd.Tag, a.upd.State = "", "latest"
		a.settings.UpdateNote = "已是最新版本"
		return false
	}
	if a.upd.Tag != result.Tag {
		a.upd.RetryApply = false
	}
	a.upd.Tag, a.upd.CanAuto, a.upd.State = result.Tag, result.CanAutoUpdate, "available"
	a.upd.Error = ""
	a.settings.UpdateNote = "发现新版本 " + result.Tag
	skipped := result.Skipped || a.prefs.DismissedUpdateTag == result.Tag
	if !silent {
		a.upd.Open = true
	}
	if skipped {
		return false
	}
	if result.CanAutoUpdate && result.StagedVersion == result.Tag {
		a.upd.State, a.upd.Open = "ready", true
		return false
	}
	return result.CanAutoUpdate && result.AutoUpdateEnabled
}
