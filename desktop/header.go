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
	if a.prefs.RailExpanded && !a.prefs.TopNavigation {
		railWidth = a.prefs.RailWidth
		railSpan = railWidth + railDividerWidth
	}
	if a.prefs.TopNavigation {
		railSpan = 80
	}
	minLeading := widthRailCollapsed
	if bar.Left > 0 {
		minLeading += headerTrafficLightGap
	}
	leading := max(minLeading, railSpan-bar.Left)
	if a.prefs.TopNavigation {
		leading = 80
		if bar.Left > 0 {
			leading += headerTrafficLightGap
		}
	}
	// The wordmark starts on the icon column when the rail is open, and is centred
	// in the 45 wide collapsed rail, where a left-aligned mark would read as
	// crowded to one side. The few DIPs it moves there are worth the balance.
	logoInset := float32(6) + railGutter
	logoAlign := ui.Start
	if !a.prefs.RailExpanded || a.prefs.TopNavigation {
		logoInset, logoAlign = 0, ui.Center
	}
	logoInset = max(float32(0), logoInset-bar.Left)
	if bar.Left > 0 && !a.prefs.TopNavigation {
		logoInset, logoAlign = 0, ui.Center
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
		ui.Row(c.Key("header-rail")).Label(locales.Text("标题栏侧栏区")).Width(leading).Shrink(0).FillHeight().Padding(0, 0, 0, logoInset).AlignItems(ui.Center).Children(func() {
			logoWidth := float32(64)
			if bar.Left > 0 && !a.prefs.TopNavigation {
				logoWidth = leading
			}
			if a.prefs.TopNavigation {
				logoWidth = 45
			}
			wordmark := ui.Column(c).Label(locales.Text("应用 Logo")).Width(min(logoWidth, leading-logoInset)).FillHeight().Justify(ui.Center).AlignItems(logoAlign).Children(func() {
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
			// The wordmark is the app's home entry, matching the first rail item:
			// it returns to the Agent page from wherever the user is.
			uikit.Tooltip(c, wordmark.Cursor(ui.CursorPointer), locales.Text("点击 logo：回到 Agent 页面"))
			if wordmark.Clicked() {
				a.changePage("chat")
			}
			if a.prefs.TopNavigation {
				a.topNavigationMenu(c)
			}
		})
		a.workspaceTabs(c)
		a.headerActions(c)
	})
}

// workspaceTabs draws open workspaces and their history-menu entry.
func (a *desktop) workspaceTabs(c *ui.Context) {
	s := a.tabStripOf("header-tabs")
	positions := s.positions[:0]
	released := false
	dragPointerX := float32(0)
	dragPointerValid := false
	motionDeferred := false
	if s.reorderFrom == "" && s.gesture.Released {
		s.gesture.Panning, s.gesture.Released = false, false
	}
	ui.Box(c).Grow(1).MinWidth(0).FillHeight().Clip().Children(func() {
		tabScroller(c, s).FillWidth().FillHeight().Children(func() {
			ui.Row(c).FillHeight().AlignItems(ui.Stretch).Gap(0).Padding(0).Children(func() {
				for i, t := range a.tabs {
					idx, tab := i, t
					e := uikit.WorkspaceTab(c.Key("wtab-"+tab.ID), sessionTabLabel(tab), sessionTabTip(tab), idx == a.active, tab.Busy, len(a.tabs) > 1 && s.reorderFrom == "", iconImage("close"), func() {
						if s.gesture.Panning {
							// A pan or a reorder drag never activates its release tab.
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
					if s.reorderFrom == "" && s.pressID == "" && e.Pressed() {
						if _, _, over := e.PointerPosition(); over {
							s.pressID = tab.ID
							s.pressDX, s.pressDY = 0, 0
						}
					}
					bounds := e.Bounds()
					layoutX := bounds.X
					positions = append(positions, tabPosition{id: tab.ID, x: layoutX, width: bounds.W})
					if s.reorderFrom != "" && (s.reorderTarget == tab.ID || (s.reorderTarget == tabDropAtEnd && idx == len(a.tabs)-1)) {
						accent := c.Theme().Accent
						e.Draw(func(p *ui.Painter, r ui.Rect) {
							x := r.X - 2
							if s.reorderTarget == tabDropAtEnd {
								x = r.X + r.W - 2
							}
							p.Fill(ui.Rect{X: x, Y: r.Y + 5, W: 4, H: r.H - 10}, accent, 2)
						})
					}
					if motion, ok := s.reorderMotions[tab.ID]; ok {
						if c.Preferences().ReduceMotion {
							delete(s.reorderMotions, tab.ID)
						} else if c.Instant() {
							// Pointer-release updates may be built as instant frames. Wait
							// one refresh so that they cannot collapse this requested motion.
							if !motionDeferred {
								c.After(16 * time.Millisecond)
								motionDeferred = true
							}
						} else if !motion.armed {
							motion.armed = true
							s.reorderMotions[tab.ID] = motion
							c.Invalidate()
							e = e.AnimatePose(ui.Pose{X: motion.offset}, ui.Animation{Duration: uikit.TabReorderDuration, Ease: ui.EaseOut})
						} else {
							e = e.AnimatePose(ui.Pose{}, ui.Animation{Duration: uikit.TabReorderDuration, Ease: ui.EaseOut})
							if motion.startedAt.IsZero() {
								motion.startedAt = c.Now()
								s.reorderMotions[tab.ID] = motion
							} else if c.Now().Sub(motion.startedAt) >= uikit.TabReorderDuration {
								delete(s.reorderMotions, tab.ID)
							}
						}
					}
					dx, dy, dragging := e.Dragged()
					if dragging && s.pressID == tab.ID {
						s.pressDX += dx
						s.pressDY += dy
					}
					if dragging && s.reorderFrom == "" && s.pressID == tab.ID {
						absX, absY := s.pressDX, s.pressDY
						if absX < 0 {
							absX = -absX
						}
						if absY < 0 {
							absY = -absY
						}
						if absX >= tabReorderDragSlop && absX > absY {
							s.reorderFrom = tab.ID
							s.gesture.Panning = true
						}
					}
					if tab.ID == s.reorderFrom {
						pointerX, _, _ := e.PointerPosition()
						dragPointerX = layoutX + pointerX
						dragPointerValid = true
						_, _, released = e.DragEnded()
					}
					if s.reorderFrom == "" && s.pressID == tab.ID && !e.Pressed() {
						s.pressID = ""
					}
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
	if s.reorderFrom != "" && s.gesture.Released {
		released = true
	}
	s.positions = positions
	draggedID := s.reorderFrom
	insertionIndex := -1
	if dragPointerValid && draggedID != "" {
		insertionIndex = conversationTabInsertionIndex(a.tabs, s.positions, draggedID, dragPointerX, tabReorderHysteresis)
		if insertionIndex >= 0 {
			s.reorderTarget = conversationTabDropTarget(a.tabs, draggedID, insertionIndex)
			s.reorderIndex, s.reorderIndexValid = insertionIndex, true
		}
	}
	if released {
		if s.reorderIndexValid {
			a.active, s.reorderMotions, _ = moveOpenConversationTabToIndex(
				a.tabs, s.positions, a.active, draggedID, s.reorderIndex,
			)
		}
		s.pressID, s.pressDX, s.pressDY = "", 0, 0
		s.reorderTarget, s.reorderIndex, s.reorderIndexValid = "", 0, false
		if c.Preferences().ReduceMotion {
			s.reorderMotions = nil
		}
		s.reorderFrom = ""
	}
	s.pan()
}

const tabReorderHysteresis float32 = 8
const tabReorderDragSlop float32 = 10
const tabDropAtEnd = "\x00end"

func conversationTabDropTarget(tabs []*conversation, draggedID string, destination int) string {
	from := -1
	for i, tab := range tabs {
		if tab != nil && tab.ID == draggedID {
			from = i
			break
		}
	}
	if from < 0 {
		return ""
	}
	if destination == from {
		return draggedID
	}
	if destination < from {
		return tabs[destination].ID
	}
	if destination+1 < len(tabs) {
		return tabs[destination+1].ID
	}
	return tabDropAtEnd
}

func conversationTabInsertionIndex(tabs []*conversation, positions []tabPosition, draggedID string, pointerX, hysteresis float32) int {
	if len(tabs) < 2 || len(positions) != len(tabs) {
		return -1
	}
	from := -1
	for i, tab := range tabs {
		if tab != nil && tab.ID == draggedID {
			from = i
			break
		}
	}
	if from < 0 {
		return -1
	}
	current := from
	newIndex := 0
	x := positions[0].x
	virtualIndex := 0
	leftBarrier, rightBarrier := float32(-1<<30), float32(1<<30)
	for i, tab := range tabs {
		if tab.ID == draggedID {
			continue
		}
		mid := x + positions[i].width/2
		if virtualIndex == current-1 {
			leftBarrier = mid
		}
		if virtualIndex == current {
			rightBarrier = mid
		}
		if pointerX >= mid {
			newIndex++
		}
		virtualIndex++
		x += positions[i].width
	}
	if newIndex > current && pointerX < rightBarrier+hysteresis {
		newIndex = current
	} else if newIndex < current && pointerX > leftBarrier-hysteresis {
		newIndex = current
	}
	return newIndex
}

func moveOpenConversationTabToIndex(tabs []*conversation, positions []tabPosition, active int, fromID string, destination int) (int, map[string]tabReorderMotion, bool) {
	if len(tabs) < 2 || len(positions) != len(tabs) {
		return active, nil, false
	}
	from := -1
	for i, tab := range tabs {
		if tab != nil && tab.ID == fromID {
			from = i
			break
		}
	}
	if from < 0 || destination < 0 || destination >= len(tabs) || from == destination {
		return active, nil, false
	}
	byID := make(map[string]tabPosition, len(positions))
	for i, tab := range tabs {
		if tab != nil {
			byID[tab.ID] = positions[i]
		}
	}
	dragged := tabs[from]
	if from < destination {
		copy(tabs[from:destination], tabs[from+1:destination+1])
	} else {
		copy(tabs[destination+1:from+1], tabs[destination:from])
	}
	tabs[destination] = dragged
	if active == from {
		active = destination
	} else {
		if from < active {
			active--
		}
		if destination <= active {
			active++
		}
	}
	newX := positions[0].x
	motions := make(map[string]tabReorderMotion, len(tabs)-1)
	for _, tab := range tabs {
		old := byID[tab.ID]
		if tab.ID != fromID {
			if delta := old.x - newX; delta > 0.5 || delta < -0.5 {
				motions[tab.ID] = tabReorderMotion{offset: delta}
			}
		}
		newX += old.width
	}
	if len(motions) == 0 {
		motions = nil
	}
	return active, motions, true
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
		uikit.Tooltip(c, uikit.Action(c.Key("update-badge"), tip, uikit.ActionOptions{Tone: uikit.Success, Style: uikit.Secondary, Circle: true, Icon: iconImage("download")}).Size(30, 30).AlignSelf(ui.Center).OnClick(func() { a.startUpdate() }), tip)
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

// sessionTabLabel is the primary tab identity; workspaceTip supplies its location on hover.
func sessionTabLabel(tab *conversation) string {
	if tab == nil || strings.TrimSpace(tab.Title) == "" {
		return locales.Text("会话")
	}
	return short(strings.TrimSpace(tab.Title), 36)
}

func sessionTabTip(tab *conversation) string {
	if tab == nil {
		return workspaceTip("")
	}
	workspace := workspaceTip(tab.Workspace)
	title := strings.TrimSpace(tab.Title)
	if title == "" || title == sessionTabLabel(tab) {
		return workspace
	}
	return short(title, 72) + "\n" + workspace
}

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
	a.releaseMemory()
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
