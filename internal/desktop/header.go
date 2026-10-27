// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// headerBar is the 44px window chrome: wordmark, workspace tabs, workspace
// management, explorer toggle, update entry and the window buttons.
func (a *desktop) headerBar(c *ui.Context) {
	p := a.currentPalette()
	ui.Row(c).Height(44).Shrink(0).Background(p.panel).BorderWidth(0, 0, 1, 0).BorderColor(p.line).DragWindow().Children(func() {
		wordmark := ui.Column(c).Width(64).Center().Children(func() {
			ui.Text(c, "Ally").FontSize(20).FontWeight(700).TextColor(p.accent).LetterSpacing(-0.4)
		})
		wordmark.Cursor(ui.CursorPointer).Tooltip("点击 logo：切换樱花特效")
		if wordmark.Clicked() {
			a.togglePetals()
		}
		a.workspaceTabs(c)
		a.headerActions(c)
	})
}

func (a *desktop) workspaceTabs(c *ui.Context) {
	p := a.currentPalette()
	ui.Box(c).Grow(1).MinWidth(0).Clip().Children(func() {
		tabScroller(c).FillWidth().Children(func() {
			ui.Row(c).Gap(1).Children(func() {
				for i, t := range a.tabs {
					idx := i
					tab := t
					label := workspaceLabel(tab.Workspace)
					row := ui.Row(c.Key("wtab-"+tab.ID)).Height(44).MinWidth(112).MaxWidth(180).Padding(0, 8).
						BorderWidth(0, 0, 2, 0).BorderColor(ui.Transparent).Gap(6).AlignItems(ui.Center)
					if idx == a.active {
						row.BorderColor(p.accent)
					}
					row.Children(func() {
						if tab.Busy {
							ui.Box(c).Size(6, 6).Radius(3).Background(p.accent).Tooltip("运行中")
						}
						style := ui.ButtonBase(c.Key("wtab-btn-"+tab.ID)).Grow(1).Height(40).Padding(0, 2).Label(label).OnClick(func() {
							if a.page == "settings" {
								a.commitSettingsOnLeave()
							}
							if a.page == "models" {
								a.cancelOAuthWhenLeaving()
							}
							a.active = idx
							a.page = "chat"
							a.refreshFiles()
							a.refreshSessionFooter(tab)
							a.refreshGit(tab)
						})
						if idx == a.active {
							style.TextColor(p.text)
						} else {
							style.TextColor(p.muted)
						}
						style.Children(func() { ui.Text(c, label).FontSize(12.5).SingleLine() })
						if len(a.tabs) > 1 {
							iconAction(c, "wtab-close-"+tab.ID, "关闭标签", "close", 20, func() {
								a.closeWorkspaceTab(idx)
							}).Disabled(tab.Busy).TextColor(p.muted)
						}
					})
				}
			})
		})
	})
}

func (a *desktop) headerActions(c *ui.Context) {
	p := a.currentPalette()
	add := iconButton(c, "workspace-add", "添加工作区", "addfolder", func() { a.workspaceMenu = !a.workspaceMenu })
	add.TextColor(p.accent)
	popoverPanel(c, add, &a.workspaceMenu, 380, func() {
		popoverItem(c, "workspace-choose", "＋ 添加工作区", "", false, func() { a.workspaceMenu = false; a.chooseWorkspace() })
		popoverItem(c, "workspace-temporary", "临时工作区", "", false, func() { a.workspaceMenu = false; a.newConversation("") })
		divider(c)
		ui.Scroll(c).MaxHeight(320).Children(func() {
			ui.Column(c).Children(func() {
				seen := map[string]bool{}
				for _, entry := range a.sessions {
					root := entry.Workspace
					if root == "" || seen[strings.ToLower(root)] || strings.HasPrefix(filepath.Base(root), "ally-temp-") {
						continue
					}
					seen[strings.ToLower(root)] = true
					popoverItem(c, "workspace-menu-"+root, filepath.Base(root), root, false, func() { a.workspaceMenu = false; a.openWorkspace(root) })
				}
				if len(seen) == 0 {
					fieldNote(c, "无历史记录")
				}
			})
		})
	})
	add.Disabled(!a.ready)

	pageOK := a.page == "chat" || a.page == "kb" || strings.HasPrefix(a.page, "plugin:")
	explorer := iconButton(c, "files-toggle", "打开工作区文件浏览器", "folderopen", func() { a.explorer = !a.explorer })
	if a.explorer {
		explorer.TextColor(p.accent)
	}
	explorer.Disabled(!pageOK)

	services := c.Services()
	if a.upd.Tag != "" && a.upd.Tag != a.prefs.DismissedUpdateTag {
		download := iconButton(c, "update-badge", "发现新版本 "+a.upd.Tag+"，点击自动更新", "download", func() { a.startUpdate() })
		download.TextColor(okColor).Background(ui.Hex("#1f2c24"))
	} else {
		iconButton(c, "github", "打开 GitHub 项目主页", "github", func() {
			services.OpenURL("https://github.com/Bronya0/ally-agent")
		}).TextColor(p.muted)
	}

	if a.win != nil {
		windowAction(c, "window-min", "最小化", "min", func() { a.win.Minimize() }).Height(44).Radius(0).TextColor(p.muted)
		windowAction(c, "window-max", "最大化", "max", func() {
			if a.win.IsMaximized() {
				a.win.Unmaximize()
			} else {
				a.win.Maximize()
			}
		}).Height(44).Radius(0).TextColor(p.muted)
		closeBtn := windowAction(c, "window-close", "关闭窗口", "close", func() { a.win.Close() }).Height(44).Radius(0).TextColor(p.muted)
		if closeBtn.Hovered() {
			closeBtn.Background(ui.Hex("#e81123")).TextColor(ui.Hex("#ffffff"))
		}
	}
}

func workspaceLabel(root string) string {
	if root == "" {
		return "未选择工作区"
	}
	base := filepath.Base(root)
	if strings.HasPrefix(base, "ally-temp-") {
		return "临时"
	}
	return base
}

func (a *desktop) closeWorkspaceTab(index int) {
	if index < 0 || index >= len(a.tabs) || len(a.tabs) <= 1 {
		return
	}
	t := a.tabs[index]
	if t.Busy {
		return
	}
	a.rememberSession(t)
	if t.Workspace != "" && strings.HasPrefix(filepath.Base(t.Workspace), "ally-temp-") {
		// Temporary workspaces are removed with the tab, after the backend
		// finishes anything still using them.
		go func(path string) {
			time.Sleep(400 * time.Millisecond)
			if err := a.core.DeleteTempWorkspace(path); err != nil {
				a.fail(err)
			}
		}(t.Workspace)
	}
	a.tabs = append(a.tabs[:index], a.tabs[index+1:]...)
	stillOpen := false
	for _, tab := range a.tabs {
		if sameWorkspace(tab.Workspace, t.Workspace) {
			stillOpen = true
			break
		}
	}
	if !stillOpen {
		key := workspaceKey(t.Workspace)
		if file := a.workspaceFiles[key]; file != nil && !file.Saving && file.Editor != nil && !file.Editor.Buffer.Modified() {
			delete(a.workspaceFiles, key)
		}
	}
	if index < a.active {
		a.active--
	}
	if a.active >= len(a.tabs) {
		a.active = len(a.tabs) - 1
	}
	if a.page == "chat" {
		a.refreshFiles()
		if t := a.current(); t != nil {
			a.refreshGit(t)
		}
	}
}

// updateDialog mirrors the original card modal: progress, state text, and the
// action row that changes with the update lifecycle.
func (a *desktop) updateDialog(c *ui.Context) {
	p := a.currentPalette()
	modalCard(c, &a.upd.Open, 460, func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, "软件更新").FontSize(17).Bold()
				ui.Spacer(c)
				iconAction(c, "update-close", "关闭", "close", 24, func() { a.upd.Open = false })
			})
			switch a.upd.State {
			case "downloading", "extracting", "applying", "restarting", "ready", "error", "latest", "checking":
			default:
				a.upd.State = "idle"
			}
			switch a.upd.State {
			case "checking":
				busyRow(c, "正在检查更新…")
			case "downloading", "extracting":
				ui.Text(c, map[string]string{"downloading": "正在下载更新…", "extracting": "正在解压更新…"}[a.upd.State])
				ui.Text(c, "版本 "+a.upd.Tag).FontSize(11).TextColor(p.muted)
				value := a.upd.Progress / 100
				if value < 0 || value > 1 {
					value = -1
				}
				ui.Progress(c, value).Label("更新进度")
				ui.Row(c).Children(func() {
					ui.Text(c, fmt.Sprintf("%.0f%%", a.upd.Progress)).FontSize(11).TextColor(p.muted)
					ui.Spacer(c)
					if a.upd.BytesTotal > 0 {
						ui.Text(c, fmtBytes(a.upd.BytesNow)+" / "+fmtBytes(a.upd.BytesTotal)).FontSize(11).TextColor(p.muted)
					}
				})
			case "ready":
				ui.Text(c, "新版本 "+a.upd.Tag+" 已下载完成")
				fieldNote(c, "重启后生效。当前会话与未保存的草稿会自动保存。")
			case "applying":
				busyRow(c, "正在应用更新…")
			case "restarting":
				busyRow(c, "正在重启 Ally…")
			case "error":
				ui.Text(c, "更新失败").TextColor(c.Theme().Danger)
				ui.Text(c, a.upd.Error).FontSize(12).Selectable().TextColor(c.Theme().Danger)
			case "latest":
				ui.Text(c, "已是最新版本").TextColor(okColor)
			}
			if a.upd.Note != "" && a.upd.State != "error" {
				fieldNote(c, a.upd.Note)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				services := c.Services()
				flat(c, "发行说明").OnClick(func() {
					services.OpenURL("https://github.com/Bronya0/ally-agent/releases")
				})
				flat(c, "项目主页").OnClick(func() { services.OpenURL("https://github.com/Bronya0/ally-agent") })
				ui.Spacer(c)
				switch {
				case a.upd.Busy:
					flat(c, "取消").OnClick(func() {
						if a.core.CancelUpdate() {
							a.upd.State = "idle"
							a.upd.Busy = false
						}
					})
				case a.upd.State == "ready":
					flat(c, "跳过此版本").OnClick(func() {
						a.prefs.DismissedUpdateTag = a.upd.Tag
						a.savePrefs()
						a.upd.Open = false
					})
					flat(c, "稍后").OnClick(func() { a.upd.Open = false })
					ui.PrimaryButton(c, "立即重启更新").OnClick(func() { a.applyUpdate() })
				case a.upd.State == "error":
					flat(c, fmt.Sprint("关闭")).OnClick(func() { a.upd.Open = false })
					ui.PrimaryButton(c, "重试").OnClick(func() { a.startUpdate() })
				default:
					if a.upd.State == "latest" {
						flat(c, "关闭").OnClick(func() { a.upd.Open = false })
					}
				}
			})
		})
	})
	_ = backend.UpdateDownloadResult{}
}

func (a *desktop) startUpdate() {
	if a.upd.Busy || a.core == nil {
		return
	}
	tag := a.upd.Tag
	if tag == "" {
		a.upd.Open = true
		a.upd.State = "checking"
		go func() {
			result := a.core.CheckForUpdates()
			a.win.Update(func() {
				if !result.OK {
					a.upd.State = "error"
					a.upd.Error = result.Error
					return
				}
				if result.Tag == "" {
					a.upd.State = "latest"
					return
				}
				a.upd.Tag = result.Tag
				a.upd.State = "ready"
				a.startUpdate()
			})
		}()
		return
	}
	a.upd.Open = true
	a.upd.State = "downloading"
	a.upd.Progress = 0
	a.upd.Busy = true
	go func() {
		result := a.core.DownloadUpdate(tag)
		a.win.Update(func() {
			a.upd.Busy = false
			if !result.OK {
				a.upd.State = "error"
				a.upd.Error = result.Error
				return
			}
			a.upd.State = "ready"
			a.upd.Progress = 100
		})
	}()
}

func (a *desktop) applyUpdate() {
	a.upd.Busy = true
	a.upd.State = "applying"
	go func() {
		res := a.core.ApplyUpdate(a.upd.Tag)
		a.win.Update(func() {
			a.upd.Busy = false
			if !res.OK {
				a.upd.State = "error"
				a.upd.Error = res.Error
				return
			}
			a.upd.State = "restarting"
			if err := a.core.QuitForUpdate(); err != nil {
				a.upd.State = "error"
				a.upd.Error = err.Error()
			}
		})
	}()
}

func (a *desktop) checkUpdates(silent bool) {
	if a.upd.State == "checking" {
		return
	}
	a.upd.State = "checking"
	go func() {
		result := a.core.CheckForUpdates()
		a.win.Update(func() {
			if !result.OK {
				a.upd.State = "error"
				a.upd.Error = result.Error
				if silent {
					a.upd.State = "idle"
					a.settings.UpdateNote = "检查更新失败：" + result.Error
				}
				return
			}
			if result.Tag == "" {
				a.upd.State = "latest"
				a.settings.UpdateNote = "已是最新版本"
				return
			}
			a.upd.Tag = result.Tag
			a.upd.State = "ready"
			a.settings.UpdateNote = "发现新版本 " + result.Tag
			if !silent {
				a.upd.Open = true
			}
		})
	}()
}

// togglePetals is a small decorative switch kept from the Vue welcome page:
// clicking the avatar or wordmark turns the falling-petal overlay on or off.
func (a *desktop) togglePetals() {
	a.petals = !a.petals
}
