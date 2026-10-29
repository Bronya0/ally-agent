// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// headerBarHeight is the height of the window chrome: the header bar, and the
// title bar the window declares.
const headerBarHeight = 44

// headerBar is the 44px window chrome: wordmark, workspace tabs, workspace
// management, explorer toggle, update entry and the window buttons.
func (a *desktop) headerBar(c *ui.Context) {
	p := a.currentPalette()
	// macOS draws the traffic lights over the content of a hidden title bar.
	// Keep their room clear so no control sits under them, and start the drag
	// strip beside them; the bar is zero elsewhere and in full screen.
	bar := c.TitleBar()
	row := ui.Row(c).Height(max(headerBarHeight, bar.Height)).Shrink(0).Background(p.panel).
		Border(0, ui.Transparent).Draw(func(painter *ui.Painter, r ui.Rect) {
		painter.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, p.line, 0)
	})
	if bar.Left > 0 || bar.Right > 0 {
		row.Padding(0, bar.Right, 0, bar.Left)
	}
	row.DragWindow().Children(func() {
		wordmark := ui.Column(c).Width(64).Center().Children(func() {
			colors := [4]ui.Color{
				wordmarkGradientColor(p, 0.14),
				wordmarkGradientColor(p, 0.43),
				wordmarkGradientColor(p, 0.61),
				wordmarkGradientColor(p, 0.84),
			}
			ui.RichText(c,
				ui.Span{Text: "A", Color: colors[0]},
				ui.Span{Text: "l", Color: colors[1]},
				ui.Span{Text: "l", Color: colors[2]},
				ui.Span{Text: "y", Color: colors[3]},
			).FontSize(20).FontWeight(700).LetterSpacing(-0.4)
		})
		uikit.Tooltip(c, wordmark.Cursor(ui.CursorPointer), "点击 logo：切换樱花特效")
		if wordmark.Clicked() {
			a.togglePetals()
		}
		a.workspaceTabs(c)
		a.headerActions(c)
	})
}

func (a *desktop) workspaceTabs(c *ui.Context) {
	ui.Box(c).Grow(1).MinWidth(0).FillHeight().Clip().Children(func() {
		tabScroller(c).FillWidth().FillHeight().Children(func() {
			ui.Row(c).FillHeight().AlignItems(ui.Stretch).Gap(0).Padding(0).Children(func() {
				for i, t := range a.tabs {
					idx, tab := i, t
					uikit.WorkspaceTab(c.Key("wtab-"+tab.ID), workspaceLabel(tab.Workspace), idx == a.active, tab.Busy, len(a.tabs) > 1, iconImage("close"), func() {
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
					}, func() { a.closeWorkspaceTab(idx) }).FillHeight().Label("工作区标签 " + tab.ID)
				}
			})
		})
	})
}

// Header history always creates a fresh tab, matching main's onHistorySelect.
// The sidebar keeps its separate switch-to-workspace behavior.
func (a *desktop) openWorkspaceEntry(workspace string) { a.newConversation(workspace) }

func (a *desktop) workspaceMenu(c *ui.Context, anchor ui.Element) {
	ui.PopoverBase(c.Key("workspace-menu"), anchor, &a.workspaceMenuOpen, func(panel ui.Element) {
		popoverChrome(c, panel, 300)
		panel.Label("工作区历史菜单")
		panel.AttachTo(anchor, ui.AnchorBottomRight, ui.AnchorTopRight).Top(6).MaxHeightPercent(85).Transition(uikit.PopupTransition())
		popoverItem(c, "add", "＋ 添加工作区", "", false, func() { a.workspaceMenuOpen = false; a.chooseWorkspace() })
		popoverItem(c, "temporary", "临时工作区", "", false, func() { a.workspaceMenuOpen = false; a.newConversation("") })
		divider(c)
		_, height := c.Size()
		ui.Scroll(c.Key("history")).FillWidth().MinHeight(0).MaxHeight(max(float32(80), min(float32(460), height-150))).Children(func() {
			seen := map[string]bool{}
			count := 0
			for _, entry := range a.sessions {
				root := entry.Workspace
				if root == "" || seen[strings.ToLower(root)] || strings.HasPrefix(filepath.Base(root), "ally-temp-") {
					continue
				}
				seen[strings.ToLower(root)] = true
				count++
				b := ui.ButtonBase(c.Key(root)).FillWidth().Height(32).Padding(0, 9).Gap(8).Justify(ui.Start).AlignItems(ui.Center).Radius(5).Cursor(ui.CursorPointer).Label("打开工作区 " + root)
				fill := ui.Transparent
				if b.Hovered() {
					fill = c.Theme().SurfaceHover
				}
				b.Background(fadeColor(b, "history-fill", fill)).OnClick(func() { a.workspaceMenuOpen = false; a.openWorkspaceEntry(root) })
				b.Children(func() {
					iconGlyph(c, "folderopen", 15).Shrink(0).TextColor(c.Theme().TextMuted)
					ui.Text(c, filepath.Base(root)).FontSize(13).TextColor(c.Theme().Text).SingleLine().MinWidth(0)
				})
				uikit.Tooltip(c, b, root)
			}
			if count == 0 {
				muted(c, "无历史记录", fsSmall).Padding(8)
			}
		})
	})
}

// headerActions is the right side of the header: the workspace menu, the file
// explorer toggle and the window actions.
func (a *desktop) headerActions(c *ui.Context) {
	p := a.currentPalette()
	add := addButton(c, "workspace-add", "添加工作区", "addfolder", 30, func() { a.workspaceMenuOpen = !a.workspaceMenuOpen })
	add.Disabled(!a.ready)
	a.workspaceMenu(c, add)

	pageOK := a.page == "chat" || a.page == "kb" || strings.HasPrefix(a.page, "plugin:")
	// The explorer toggle carries the theme colour even while it is off: a muted
	// glyph made the header's folder read as decoration. Off is half accent, on is
	// the full accent, so the state reads from the stroke alone — no fill behind it.
	tone := iconAccent
	if a.explorer {
		tone = iconAccentOn
	}
	explorer := iconButtonSized(c, "files-toggle", "打开工作区文件浏览器", "folderopen", 30, tone, func() { a.explorer = !a.explorer })
	explorer.Disabled(!pageOK)

	services := c.Services()
	if a.upd.Tag != "" && a.upd.Tag != a.prefs.DismissedUpdateTag {
		tip := "发现新版本 " + a.upd.Tag + "，点击自动更新"
		uikit.Tooltip(c, uikit.Action(c.Key("update-badge"), tip, uikit.ActionOptions{Tone: uikit.Success, Style: uikit.Secondary, Circle: true, Icon: iconImage("download")}).Size(30, 30).OnClick(func() { a.startUpdate() }), tip)
	} else {
		iconButton(c, "github", "打开 GitHub 项目主页", "github", func() {
			services.OpenURL("https://github.com/Bronya0/ally-agent")
		}).TextColor(p.muted)
	}

	// The header draws these itself unless the system already shows them:
	// macOS keeps its traffic lights, and its title bar room is never zero.
	if bar := c.TitleBar(); a.win != nil && bar.Height == 0 && bar.Left == 0 && bar.Right == 0 {
		windowAction(c, "window-min", "最小化", "min", func() { a.win.Minimize() })
		windowAction(c, "window-max", "最大化", "max", func() {
			if a.win.IsMaximized() {
				a.win.Unmaximize()
			} else {
				a.win.Maximize()
			}
		})
		closeBtn := windowButton(c, "window-close", "关闭窗口", "close", true, func() { a.win.Close() })
		_ = closeBtn
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
			a.releaseWorkspaceFile(t.Workspace)
		}
	}
	sessionStillOpen := false
	for _, tab := range a.tabs {
		if tab.ID == t.ID {
			sessionStillOpen = true
			break
		}
	}
	if !sessionStillOpen {
		a.releaseSessionHTMLPreviews(t.ID)
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
			case "downloading", "extracting", "applying", "restarting", "ready", "error", "latest", "checking", "available":
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
			case "available":
				ui.Text(c, "发现新版本 "+a.upd.Tag)
				if !a.upd.CanAuto {
					fieldNote(c, "当前平台请下载压缩包后手动更新。")
				}
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
				ui.Text(c, "已是最新版本").TextColor(c.Theme().Success)
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
				case a.upd.State == "available":
					flat(c, "稍后").OnClick(func() { a.upd.Open = false })
					if a.upd.CanAuto {
						primary(c, "下载更新").OnClick(func() { a.startUpdate() })
					} else {
						primary(c, "前往下载").OnClick(func() { services.OpenURL("https://github.com/Bronya0/ally-agent/releases/tag/" + a.upd.Tag) })
					}
				case a.upd.State == "ready":
					flat(c, "跳过此版本").OnClick(func() {
						a.prefs.DismissedUpdateTag = a.upd.Tag
						a.savePrefs()
						a.upd.Open = false
					})
					flat(c, "稍后").OnClick(func() { a.upd.Open = false })
					primary(c, "立即重启更新").OnClick(func() { a.applyUpdate() })
				case a.upd.State == "error":
					flat(c, fmt.Sprint("关闭")).OnClick(func() { a.upd.Open = false })
					primary(c, "重试").OnClick(func() { a.startUpdate() })
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
		a.checkUpdates(false)
		return
	}
	if !a.upd.CanAuto {
		a.upd.State = "available"
		a.upd.Open = true
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
	if a.upd.State != "ready" || !a.upd.CanAuto || a.upd.Busy {
		return
	}
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
			if result.Tag == "" || !newerStableRelease(result.Tag, mygo.App.Version()) {
				a.upd.State = "latest"
				a.settings.UpdateNote = "已是最新版本"
				return
			}
			a.upd.Tag = result.Tag
			a.upd.CanAuto = result.CanAutoUpdate
			a.upd.State = "available"
			if result.StagedVersion == result.Tag && result.CanAutoUpdate {
				a.upd.State = "ready"
			}
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
