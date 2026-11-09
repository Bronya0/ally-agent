// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// headerBarHeight is the height of the window chrome: the header bar, and the
// title bar the window declares.
const headerBarHeight = 44

const headerTrafficLightGap float32 = 12

// headerBar is the 44px window chrome: wordmark, workspace tabs, workspace
// management, explorer toggle, update entry and the window buttons.
func (a *desktop) headerBar(c *ui.Context) {
	p := a.currentPalette()
	// macOS draws the traffic lights over the content of a hidden title bar.
	// Keep their room clear so no control sits under them, and start the drag
	// strip beside them; the bar is zero elsewhere and in full screen.
	bar := c.TitleBar()
	railWidth := widthRailCollapsed
	railSpan := railWidth
	if a.prefs.RailExpanded {
		railWidth = a.prefs.RailWidth
		railSpan = railWidth + railDividerWidth
	}
	minLeading := widthRailCollapsed
	if bar.Left > 0 {
		minLeading += headerTrafficLightGap
	}
	leading := max(minLeading, railSpan-bar.Left)
	// The wordmark starts on the icon column when the rail is open, and is centred
	// in the 45 wide collapsed rail, where a left-aligned mark would read as
	// crowded to one side. The few DIPs it moves there are worth the balance.
	logoInset := float32(6) + railGutter
	logoAlign := ui.Start
	if !a.prefs.RailExpanded {
		logoInset, logoAlign = 0, ui.Center
	}
	logoInset = max(float32(0), logoInset-bar.Left)
	if bar.Left > 0 {
		logoInset = max(headerTrafficLightGap, logoInset)
	}
	tabsStart := bar.Left + leading
	row := ui.Row(c).Height(max(headerBarHeight, bar.Height)).Shrink(0).Background(p.headerColor()).
		Border(0, ui.Transparent).Draw(func(painter *ui.Painter, r ui.Rect) {
		painter.Fill(ui.Rect{X: r.X + tabsStart, Y: r.Y + r.H - 1, W: max(float32(0), r.W-tabsStart), H: 1}, p.line, 0)
	})
	if bar.Left > 0 || bar.Right > 0 {
		row.Padding(0, bar.Right, 0, bar.Left)
	}
	row.DragWindow().Children(func() {
		// The leading header column shares the rail's live width, including
		// its resize divider. Native traffic lights take precedence on macOS.
		ui.Column(c.Key("header-rail")).Label(locales.Text("标题栏侧栏区")).Width(leading).Shrink(0).FillHeight().Padding(0, 0, 0, logoInset).Justify(ui.Center).AlignItems(ui.Start).Children(func() {
			wordmark := ui.Column(c).Label(locales.Text("应用 Logo")).Width(min(float32(64), leading-logoInset)).FillHeight().Justify(ui.Center).AlignItems(logoAlign).Children(func() {
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
				).FontSize(logoFontSize).FontWeight(700).LetterSpacing(-0.4)
			})
			uikit.Tooltip(c, wordmark.Cursor(ui.CursorPointer), locales.Text("点击 logo：切换樱花特效"))
			if wordmark.Clicked() {
				a.togglePetals()
			}
		})
		a.workspaceTabs(c)
		a.headerActions(c)
	})
}

// workspaceTabs draws open workspaces and their history-menu entry.
func (a *desktop) workspaceTabs(c *ui.Context) {
	s := a.tabStripOf("header-tabs")
	ui.Box(c).Grow(1).MinWidth(0).FillHeight().Clip().Children(func() {
		tabScroller(c, s).FillWidth().FillHeight().Children(func() {
			ui.Row(c).FillHeight().AlignItems(ui.Stretch).Gap(0).Padding(0).Children(func() {
				for i, t := range a.tabs {
					idx, tab := i, t
					uikit.WorkspaceTab(c.Key("wtab-"+tab.ID), workspaceLabel(tab.Workspace), workspaceTip(tab.Workspace), idx == a.active, tab.Busy, len(a.tabs) > 1, iconImage("close"), func() {
						if s.gesture.Panning {
							// The press panned the strip: it chose nothing.
							return
						}
						if a.page == "settings" {
							a.commitSettingsOnLeave()
						}
						if a.page == "models" {
							a.cancelOAuthWhenLeaving()
						}
						if a.page != "chat" || idx != a.active {
							a.hideFilePreview()
						}
						a.page = "chat"
						a.activateTab(idx)
						a.refreshFiles()
						a.refreshSessionFooter(tab)
						a.refreshGit(tab)
					}, func() { a.closeWorkspaceTab(idx) }, &s.gesture, a.secondaryInk()).FillHeight().Label(locales.Text("工作区标签 ") + tab.ID)
				}
				// The plus closes the strip, right where the tabs end: it asks for
				// another workspace and opens the same history the header's add
				// button does — one menu, built by whichever entry opened it.
				a.workspaceHistoryMenu(c.Key("tabs-workspace-add"), func(b ui.Element) {
					uikit.IconButtonOn(c, b.Disabled(!a.ready), locales.Text("打开工作区历史"), iconImage("plus"), 26, 26*uikit.IconGlyphScale, uikit.IconQuiet, a.secondaryInk())
				}).AlignSelf(ui.Center).MarginStart(4)
			})
		})
	})
	s.pan()
}

// Header history always creates a fresh tab, matching main's onHistorySelect.
// The sidebar keeps its separate switch-to-workspace behavior.
func (a *desktop) openWorkspaceEntry(workspace string) { a.newConversation(workspace) }

// Both workspace entries share the same menu builder. Their open flags belong
// to the controls, not global desktop state; MenuBase handles outside presses.
func (a *desktop) workspaceHistoryMenu(c *ui.Context, trigger func(ui.Element)) ui.Element {
	return uikit.MenuButton(c, nil, uikit.MenuOptions{
		Width: 300, MaxHeightPercent: 85, Label: locales.Text("工作区历史菜单"),
		At: ui.AnchorBottomRight, Self: ui.AnchorTopRight, Gap: 6,
	}, trigger, func(m ui.MenuParts) {
		uikit.MenuItem(c, m, "add", locales.Text("＋ 添加工作区"), uikit.MenuItemOptions{}, a.secondaryInk()).OnClick(a.chooseWorkspace)
		uikit.MenuItem(c, m, "temporary", locales.Text("临时工作区"), uikit.MenuItemOptions{}, a.secondaryInk()).OnClick(func() { a.newConversation("") })
		divider(c)
		roots := a.workspaceHistoryRoots()
		for _, root := range roots {
			root := root
			item := uikit.MenuItem(c, m, root, filepath.Base(root), uikit.MenuItemOptions{Icon: iconImage("folderopen"), FontSize: fsSmall}, a.secondaryInk()).
				Label(locales.Text("打开工作区 ")+root).Height(32).Padding(0, 9)
			uikit.Tooltip(c, item, root).OnClick(func() { a.openWorkspaceEntry(root) })
		}
		if len(roots) == 0 {
			muted(c, locales.Text("无历史记录"), fsSmall, a.secondaryInk()).Padding(padRow)
		}
	})
}

// headerActions draws the workspace history, explorer and window actions.
func (a *desktop) headerActions(c *ui.Context) {
	p := a.currentPalette()
	a.workspaceHistoryMenu(c.Key("workspace-add"), func(b ui.Element) {
		addButtonOn(c, b.Disabled(!a.ready), locales.Text("添加工作区"), "addfolder", 30)
	})

	pageOK := a.page == "chat" || strings.HasPrefix(a.page, "plugin:")
	// The explorer toggle carries the theme colour even while it is off: a muted
	// glyph made the header's folder read as decoration. Off is half accent, on is
	// the full accent, so the state reads from the stroke alone — no fill behind it.
	tone := iconAccent
	if a.explorer {
		tone = iconAccentOn
	}
	explorer := iconButtonSized(c, "files-toggle", locales.Text("打开工作区文件浏览器"), "folderopen", 30, tone, func() { a.setExplorerOpen(!a.explorer) })
	explorer.Disabled(!pageOK)

	services := c.Services()
	if a.upd.Tag != "" && a.upd.Tag != a.prefs.DismissedUpdateTag {
		tip := locales.Text("发现新版本 ") + a.upd.Tag + locales.Text("，点击自动更新")
		uikit.Tooltip(c, uikit.Action(c.Key("update-badge"), tip, uikit.ActionOptions{Tone: uikit.Success, Style: uikit.Secondary, Circle: true, Icon: iconImage("download")}).Size(30, 30).OnClick(func() { a.startUpdate() }), tip)
	} else {
		iconButton(c, "github", locales.Text("打开 GitHub 项目主页"), "github", func() {
			services.OpenURL("https://github.com/Bronya0/ally-agent")
		}).TextColor(p.muted)
	}

	// The header draws these itself unless the system already shows them:
	// macOS keeps its traffic lights, and its title bar room is never zero.
	if bar := c.TitleBar(); a.win != nil && bar.Height == 0 && bar.Left == 0 && bar.Right == 0 {
		windowAction(c, "window-min", locales.Text("最小化"), "min", p.headerColor(), func() { a.win.Minimize() }, a.secondaryInk())
		windowAction(c, "window-max", locales.Text("最大化"), "max", p.headerColor(), func() {
			if a.win.IsMaximized() {
				a.win.Unmaximize()
			} else {
				a.win.Maximize()
			}
		}, a.secondaryInk())
		closeLabel := locales.Text("退出应用")
		if a.closeHidesToTray() {
			closeLabel = locales.Text("隐藏到系统托盘")
		}
		closeBtn := windowButton(c, "window-close", closeLabel, "close", p.headerColor(), true, func() { a.win.Close() }, a.secondaryInk())
		_ = closeBtn
	}
}

func workspaceLabel(root string) string {
	if root == "" {
		return locales.Text("未选择工作区")
	}
	base := filepath.Base(root)
	if strings.HasPrefix(base, "ally-temp-") {
		return locales.Text("临时")
	}
	return base
}

// workspaceTip is what a tab's hover reveals: the label keeps only the last
// segment, so the tooltip carries the workspace's full path.
func workspaceTip(root string) string {
	if root == "" {
		return locales.Text("未选择工作区")
	}
	return root
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
	a.tabs = slices.Delete(a.tabs, index, index+1)
	stillOpen := false
	for _, tab := range a.tabs {
		if sameWorkspace(tab.Workspace, t.Workspace) {
			stillOpen = true
			break
		}
	}
	if !stillOpen {
		key := workspaceKey(t.Workspace)
		a.releaseWorkspaceFileTree(t.Workspace)
		a.workspacePreview.releaseWorkspace(key)
		delete(a.mediaPlayback, key)
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
		releaseCommandDetails(t)
		a.releaseSessionHTMLPreviews(t.ID)
	}
	if index < a.active {
		a.active--
	}
	if a.active >= len(a.tabs) {
		a.active = len(a.tabs) - 1
	}
	a.activateTab(a.active)
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
	if a.upd.installing() {
		a.upd.Open = true
	}
	p := a.currentPalette()
	modalCard(c, &a.upd.Open, 460, func() {
		ui.Column(c).Gap(gapSection).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.Text("软件更新")).FontSize(fsDialogTitle).Bold()
				ui.Spacer(c)
				iconAction(c, "update-close", locales.Text("关闭"), "close", 24, func() {
					if !a.upd.installing() {
						a.upd.Open = false
					}
				}).Disabled(a.upd.installing())
			})
			switch a.upd.State {
			case "downloading", "extracting", "preparing", "applying", "restarting", "ready", "error", "latest", "checking", "available":
			default:
				a.upd.State = "idle"
			}
			switch a.upd.State {
			case "checking":
				busyRow(c, locales.Text("正在检查更新…"), a.secondaryInk())
			case "downloading", "extracting":
				ui.Text(c, map[string]string{"downloading": locales.Text("正在下载更新…"), "extracting": locales.Text("正在解压更新…")}[a.upd.State])
				ui.Text(c, locales.Text("版本 ")+a.upd.Tag).FontSize(fsCaption).TextColor(p.muted)
				value := a.upd.Progress / 100
				if value < 0 || value > 1 {
					value = -1
				}
				ui.Progress(c, value).Label(locales.Text("更新进度"))
				ui.Row(c).Children(func() {
					label := fmt.Sprintf("%.0f%%", a.upd.Progress)
					if a.upd.Progress < 0 {
						label = locales.Text("已下载 ") + fmtBytes(a.upd.BytesNow)
					}
					ui.Text(c, label).FontSize(fsCaption).TextColor(p.muted)
					ui.Spacer(c)
					if a.upd.BytesTotal > 0 {
						ui.Text(c, fmtBytes(a.upd.BytesNow)+" / "+fmtBytes(a.upd.BytesTotal)).FontSize(fsCaption).TextColor(p.muted)
					}
				})
			case "available":
				ui.Text(c, locales.Text("发现新版本 ")+a.upd.Tag)
				if !a.upd.CanAuto {
					fieldNote(c, locales.Text("当前平台请下载压缩包后手动更新。"), a.secondaryInk())
				}
			case "ready":
				ui.Text(c, locales.Text("新版本 ")+a.upd.Tag+locales.Text(" 已下载完成"))
				fieldNote(c, updateReadyNote(runtime.GOOS), a.secondaryInk())
			case "preparing":
				busyRow(c, locales.Text("正在保存会话与设置…"), a.secondaryInk())
			case "applying":
				busyRow(c, locales.Text("正在应用更新…"), a.secondaryInk())
			case "restarting":
				label := locales.Text("正在重启 Ally…")
				if runtime.GOOS == "windows" {
					label = locales.Text("更新已安装，正在退出；请重新打开 Ally。")
				}
				busyRow(c, label, a.secondaryInk())
			case "error":
				ui.Text(c, locales.Text("更新失败")).TextColor(c.Theme().Danger)
				ui.Text(c, a.upd.Error).FontSize(fsTiny).Selectable().TextColor(c.Theme().Danger)
			case "latest":
				ui.Text(c, locales.Text("已是最新版本")).TextColor(c.Theme().Success)
			}
			if a.upd.Note != "" && a.upd.State != "error" {
				fieldNote(c, a.upd.Note, a.secondaryInk())
			}
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				services := c.Services()
				flat(c, locales.Text("发行说明")).OnClick(func() {
					services.OpenURL("https://github.com/Bronya0/ally-agent/releases")
				})
				flat(c, locales.Text("项目主页")).OnClick(func() { services.OpenURL("https://github.com/Bronya0/ally-agent") })
				ui.Spacer(c)
				switch {
				case a.upd.Busy:
					if a.upd.State == "downloading" || a.upd.State == "extracting" {
						flat(c, locales.Text("取消下载")).Disabled(a.upd.Cancelling).OnClick(func() {
							if a.core.CancelUpdate() {
								a.upd.Cancelling = true
							}
						})
					}
				case a.upd.State == "available":
					flat(c, locales.Text("稍后")).OnClick(func() { a.upd.Open = false })
					if a.upd.CanAuto {
						primary(c, locales.Text("下载更新")).OnClick(func() { a.startUpdate() })
					} else {
						primary(c, locales.Text("前往下载")).OnClick(func() { services.OpenURL("https://github.com/Bronya0/ally-agent/releases/tag/" + a.upd.Tag) })
					}
				case a.upd.State == "ready":
					flat(c, locales.Text("跳过此版本")).OnClick(a.skipUpdate)
					flat(c, locales.Text("稍后")).OnClick(func() { a.upd.Open = false })
					primary(c, updateApplyLabel(runtime.GOOS)).OnClick(a.applyUpdate)
				case a.upd.State == "error":
					flat(c, fmt.Sprint(locales.Text("关闭"))).OnClick(func() { a.upd.Open = false })
					primary(c, locales.Text("重试")).OnClick(func() {
						if a.upd.Applied || a.upd.RetryApply {
							a.applyUpdate()
						} else {
							a.startUpdate()
						}
					})
				default:
					if a.upd.State == "latest" {
						flat(c, locales.Text("关闭")).OnClick(func() { a.upd.Open = false })
					}
				}
			})
		})
	})
	_ = backend.UpdateDownloadResult{}
}

// togglePetals is a small decorative switch kept from the Vue welcome page:
// clicking the avatar or wordmark turns the falling-petal overlay on or off.
func (a *desktop) togglePetals() {
	a.petals = !a.petals
}
