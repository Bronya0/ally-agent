// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"fmt"
	"math"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

type modeItem struct {
	id, label, icon string
}

// railModeGap is the distance between two rail entries, shared by both states:
// with one row pitch the entries keep their heights when the rail collapses.
const railModeGap float32 = 3

var baseModes = []modeItem{
	{"chat", "Agent", "chat"},
	{"skills", "技能", "skills"},
	{"mcp", "MCP", "mcp"},
	{"models", "模型", "models"},
	{"ssh", "SSH", "ssh"},
	{"tasks", "任务", "tasks"},
	{"stats", "Token", "stats"},
	{"plugins", "插件", "plugins"},
}

func (a *desktop) modeHidden(id string) bool {
	return modeHiddenIn(a.config.HiddenModes, id)
}

func modeHiddenIn(hiddenModes []string, id string) bool {
	if id == "chat" || id == "settings" {
		return false
	}
	for _, name := range hiddenModes {
		if strings.EqualFold(strings.TrimSpace(name), id) {
			return true
		}
	}
	return false
}

// pluginModeIndex lists the rail entries enabled plugins contribute, as places
// in a.plugins: grouped plugins fold into one entry led by their first member.
type pluginModeIndex struct {
	top    []int
	groups [][]int
}

// pluginModes answers the rail's plugin entries once per change of the plugin
// list: the rail asks on every frame, and building the groups allocates a map.
func (a *desktop) pluginModes() pluginModeIndex {
	return a.pluginIndex.get(a.pluginsRevision, func(uint64) pluginModeIndex {
		var index pluginModeIndex
		groupIndex := map[string]int{}
		for i, p := range a.plugins {
			if !p.Enabled || !p.Valid || p.Mode == "" {
				continue
			}
			if p.Group == nil || p.Group.Key == "" {
				index.top = append(index.top, i)
				continue
			}
			key := p.Group.Key
			at, ok := groupIndex[key]
			if !ok {
				at = len(index.groups)
				groupIndex[key] = at
				index.groups = append(index.groups, nil)
			}
			index.groups[at] = append(index.groups[at], i)
		}
		return index
	})
}

func (a *desktop) rail(c *ui.Context) {
	p := a.currentPalette()
	if !a.prefs.RailExpanded {
		rail := ui.Column(c).Width(widthRailCollapsed).Shrink(0).Background(p.chromeColor()).BorderWidth(0, 1, 0, 0).BorderColor(p.line).Padding(4, railPadH).Gap(railModeGap).AlignItems(ui.Center)
		rail.Children(func() {
			ui.Scroll(c).Key("rail-modes").FillWidth().Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(railModeGap).Children(func() { a.railModes(c, false) })
			})
			railToggleButton(c, "rail-open", locales.Text("展开侧边栏（显示菜单名）"), "expand", func() {
				a.prefs.RailExpanded = true
				a.savePrefs()
			}, a.secondaryInk()).TextColor(p.muted)
		})
		return
	}
	a.railBody(c, &a.prefs.RailWidth)
}

func (a *desktop) railBody(c *ui.Context, width *float32) {
	p := a.currentPalette()
	// The vertical padding matches the collapsed rail's 4, and the gap their shared
	// railModeGap: one row pitch in both states keeps the entries at the same
	// heights, so collapsing does not scroll them.
	rail := ui.Column(c).Width(*width).FillHeight().Shrink(0).Background(p.chromeColor()).BorderWidth(0, 1, 0, 0).BorderColor(p.line).Padding(4, 6).Gap(railModeGap)
	rail.Children(func() {
		a.railModes(c, true)
		ui.Column(c).Grow(1).MinHeight(0).Gap(gapInline).Children(func() {
			a.railWorkspaces(c)
			a.railSessions(c)
		})
		ui.Row(c).Shrink(0).Gap(gapTight).AlignItems(ui.Center).Children(func() {
			railToggleButton(c, "rail-close", locales.Text("收起侧边栏"), "collapse", func() {
				a.prefs.RailExpanded = false
				a.savePrefs()
			}, a.secondaryInk()).TextColor(p.muted)
		})
	})
}

func (a *desktop) railModes(c *ui.Context, expanded bool) {
	plugins := a.pluginModes()
	for _, mode := range baseModes {
		if mode.id == "plugins" {
			// Plugin entries sit right before the plugin manager, matching the
			// original rail order.
			for _, i := range plugins.top {
				item := a.plugins[i]
				a.railModeButton(c, expanded, item.Mode, pluginTitle(item), "plugins", false)
			}
			for _, group := range plugins.groups {
				if len(group) == 0 {
					continue
				}
				head := a.plugins[group[0]]
				title := pluginTitle(head)
				if head.Group != nil && head.Group.Title != "" {
					title = head.Group.Title
				}
				active := false
				for _, i := range group {
					if a.plugins[i].Mode == a.page {
						active = true
					}
				}
				a.railGroupButton(c, expanded, head, title, group, active)
			}
		}
		if a.modeHidden(mode.id) {
			continue
		}
		active := a.page == mode.id
		if mode.id == "chat" && strings.HasPrefix(a.page, "plugin:") {
			active = false
		}
		a.railModeButton(c, expanded, mode.id, locales.Text(mode.label), mode.icon, active)
	}
	// The settings entry is always last and never hidden.
	active := a.page == "settings"
	a.railModeButton(c, expanded, "settings", locales.Text("设置"), "settings", active)
}

func pluginTitle(p backend.PluginInfo) string {
	if p.Menu.Title != "" {
		return p.Menu.Title
	}
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

func (a *desktop) railModeButton(c *ui.Context, expanded bool, id, label, iconName string, active bool) {
	if id == "chat" && active && expanded {
		row := ui.Row(c.Key("agent-nav-row")).FillWidth().Height(heightRailItem)
		show := row.Hovered() || row.FocusWithin()
		row.Children(func() {
			railNavigationButton(c, "nav-"+id, label, iconName, expanded, active, nil, a.secondaryInk()).Grow(1).MinWidth(0).Padding(0, 28, 0, 0).OnClick(func() { a.changePage(id) })
			ui.Box(c.Key("agent-new-slot")).Absolute().Right(4).Top(3).Size(26, 26).Children(func() {
				b := iconAction(c, "agent-new-session", locales.Text("新建会话"), "plus", 26, func() { a.newSession() })
				opacity := float32(0)
				if show {
					opacity = 1
				}
				uikit.HoverFade(c, b, opacity)
			})
		})
		return
	}
	railNavigationButton(c, "nav-"+id, label, iconName, expanded, active, nil, a.secondaryInk()).OnClick(func() {
		a.changePage(id)
	})
}

// railGroupButton opens the group's pages in the app's own menu panel, the same
// one the composer selectors use: the current page carries the accent tint.
func (a *desktop) railGroupButton(c *ui.Context, expanded bool, head backend.PluginInfo, title string, group []int, active bool) {
	root := uikit.MenuButton(c.Key("nav-group-"+head.ID), nil, uikit.MenuOptions{Width: 210}, func(b ui.Element) {
		navItemOn(c, b, title, "plugins", expanded, active, func() {
			iconGlyph(c, "chevrondown", sizeCaretInline).Shrink(0)
		}, a.secondaryInk())
	}, func(m ui.MenuParts) {
		for _, i := range group {
			item := a.plugins[i]
			uikit.MenuItem(c, m, item.ID, pluginTitle(item), uikit.MenuItemOptions{Choice: true, Selected: item.Mode == a.page}, a.secondaryInk()).OnClick(func() {
				a.changePage(item.Mode)
			})
		}
	})
	if expanded {
		root.FillWidth()
	}
}

func (a *desktop) railWorkspaces(c *ui.Context) {
	ui.Column(c).Key("rail-workspaces").FillWidth().Shrink(1).MinHeight(24).Gap(3).Children(func() {
		a.railSectionHeader(c, "rail-ws", locales.Text("工作区"), "folderopen", a.prefs.RailWorkspaceOpen, locales.Text("添加工作区"), func() {
			a.prefs.RailWorkspaceOpen = !a.prefs.RailWorkspaceOpen
			a.savePrefs()
		}, func() { a.chooseWorkspace() })
		if !a.prefs.RailWorkspaceOpen {
			return
		}
		ui.Scroll(c.Key("workspace-items")).FillWidth().MaxHeight(180).MinHeight(0).Children(func() {
			a.railWorkspaceRow(c, "", locales.Text("临时工作区"), locales.Text("在系统临时目录创建沙箱，随标签页关闭自动删除"))
			roots := a.workspaceHistoryRoots()
			for _, root := range roots {
				root := root
				a.railWorkspaceRow(c, root, workspaceLabel(root), root)
			}
			if len(roots) == 0 {
				ui.Text(c, locales.Text("无历史记录")).FontSize(fsSmall).TextColor(a.secondaryInk()).Padding(0, railContentInset())
			}
		})
	})
}

// workspaceDeleteHint labels the rail's per-workspace delete action, which
// removes the workspace's persisted sessions.
const workspaceDeleteHint = "删除该工作区的全部会话"

func (a *desktop) railWorkspaceRow(c *ui.Context, root, label, tip string) {
	p := a.currentPalette()
	active := false
	if t := a.current(); t != nil && strings.EqualFold(t.Workspace, root) {
		active = true
	}
	row := ui.Row(c.Key("rail-ws-"+root)).Height(26).Radius(6).Padding(0, railContentInset()).Gap(gapInline).AlignItems(ui.Center)
	fill := ui.Transparent
	ink := a.secondaryInk()
	if active {
		fill = selectionFill(c.Theme().Accent)
		ink = c.Theme().Accent
	} else if row.Hovered() {
		fill = p.hover
	}
	row.Background(fadeColor(row, "fill", fill))
	uikit.RightTooltip(c, row, tip)
	row.Children(func() {
		btn := ui.ButtonBase(c.Key("rail-ws-btn-" + root)).Grow(1).Height(24).Justify(ui.Start).Label(label).Cursor(ui.CursorPointer)
		btn.OnClick(func() {
			if root == "" {
				a.newConversation("")
				return
			}
			a.openWorkspace(root)
		})
		btn.Children(func() { ui.Text(c, label).FontSize(fsSmall).TextColor(ink).SingleLine() })
		if root != "" {
			b := dangerIconAction(c, "rail-ws-del-"+root, locales.Text(workspaceDeleteHint), "close", 18, func() {
				a.removeWorkspaceHistory(root)
			})
			railHoverAction(c, row, b, false)
		}
	})
}

// removeWorkspaceHistory deletes the persisted sessions recorded for one
// workspace and forgets its visit after successful deletion. A workspace opened
// without a saved session only needs its visit removed.
func (a *desktop) removeWorkspaceHistory(root string) {
	if root == "" || a.closing || a.core == nil {
		return
	}
	ids := a.workspaceSessionIDs(root)
	if len(ids) == 0 {
		a.forgetWorkspaceVisit(root)
		return
	}
	var failures []string
	a.confirmAction(locales.Text("删除历史记录"), fmt.Sprintf(locales.Text("%s 的 %d 个会话将被永久删除，无法恢复。正在运行或压缩中的会话会跳过。"), workspaceLabel(root), len(ids)), func() error {
		failures = failures[:0]
		for _, id := range ids {
			if err := a.core.DeleteSession(id); err != nil {
				failures = append(failures, err.Error())
			}
		}
		return nil
	}, func() {
		a.forgetSessions(ids)
		a.reloadSessions()
		if len(failures) > 0 {
			a.notifyError(fmt.Sprintf(locales.Text("已删除 %d 个会话，%d 个未能删除：%s"), len(ids)-len(failures), len(failures), strings.Join(failures, "；")))
			return
		}
		a.forgetWorkspaceVisit(root)
		a.notifySuccess(fmt.Sprintf(locales.Text("已删除 %d 个会话"), len(ids)))
	})
}

// workspaceSessionIDs lists every session recorded under root. It walks the
// whole index: workspaceSessions caps the list for display, and a deletion must
// not silently leave the rest on disk.
func (a *desktop) workspaceSessionIDs(root string) []string {
	ids := make([]string, 0, len(a.sessions))
	for _, entry := range a.sessions {
		if entry.ID != "" && sameWorkspace(entry.Workspace, root) {
			ids = append(ids, entry.ID)
		}
	}
	return ids
}

func (a *desktop) railSessions(c *ui.Context) {
	p := a.currentPalette()
	ui.Column(c).Key("rail-sessions").FillWidth().Grow(1).MinHeight(24).Gap(3).Children(func() {
		a.railSectionHeader(c, "rail-sess", locales.Text("历史会话"), "history", a.prefs.RailSessionsOpen, locales.Text("新建会话"), func() {
			a.prefs.RailSessionsOpen = !a.prefs.RailSessionsOpen
			a.savePrefs()
		}, func() { a.newSession() })
		if !a.prefs.RailSessionsOpen {
			return
		}
		t := a.current()
		workspace := ""
		if t != nil {
			workspace = t.Workspace
		}
		entries, _ := a.workspaceSessions(workspace)
		if len(entries) == 0 {
			ui.Text(c, locales.Text("当前工作区暂无历史会话")).FontSize(fsSmall).TextColor(a.secondaryInk()).Padding(0, railContentInset())
			return
		}
		ui.Scroll(c.Key("session-items")).FillWidth().Grow(1).MinHeight(0).Children(func() {
			for _, entry := range entries {
				entry := entry
				busy := entry.ID == sessionBusyID(a, entry.ID)
				active := t != nil && t.ID == entry.ID
				row := ui.Row(c.Key("rail-sess-"+entry.ID)).Height(30).Radius(6).Padding(0, railContentInset()).AlignItems(ui.Center).Gap(gapInline)
				fill := ui.Transparent
				ink := a.secondaryInk()
				if active {
					fill = selectionFill(c.Theme().Accent)
					ink = c.Theme().Accent
				} else if row.Hovered() {
					fill = p.hover
				}
				row.Background(fadeColor(row, "fill", fill))
				title := entry.Title
				if title == "" {
					title = locales.Text("会话")
				}
				uikit.RightTooltip(c, row, fmt.Sprintf(locales.Text("%s\n%s · %d 条"), title, fmtTime(entry.UpdatedAt), entry.MessageCount))
				row.Children(func() {
					btn := ui.ButtonBase(c.Key("rail-sess-btn-" + entry.ID)).Grow(1).Height(28).Justify(ui.Start).Label(title).Cursor(ui.CursorPointer)
					btn.OnClick(func() { a.openSession(entry.ID) })
					btn.Children(func() {
						ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
							ui.Text(c, title).FontSize(fsSmall).TextColor(ink).SingleLine()
							if busy {
								ui.Box(c).Size(5, 5).Radius(3).Background(p.accent)
							}
						})
					})
					b := dangerIconAction(c, "rail-sess-del-"+entry.ID, locales.Text("删除会话"), "close", 18, func() {
						a.deleteSession(entry.ID, title)
					})
					railHoverAction(c, row, b, busy)
				})
			}
		})
	})
}

func sessionBusyID(a *desktop, id string) string {
	for _, t := range a.tabs {
		if t.ID == id && t.Busy {
			return id
		}
	}
	if t := a.openSessions[id]; t != nil && t.Busy {
		return id
	}
	return ""
}

type workspaceSessionKey struct {
	revision  uint64
	workspace string
}

// workspaceSessionList is the bounded list the rail and the pickers draw plus
// the number of sessions that exist: the header must say how many there are, not
// how many of them fit on screen.
type workspaceSessionList struct {
	entries []backend.SessionIndexEntry
	total   int
}

// workspaceSessions is a per-frame read for the rail, the composer's session
// menu and the workspace menus: the scan, the copy and the sort run once per
// replacement of the session index instead of once per frame.
func (a *desktop) workspaceSessions(workspace string) ([]backend.SessionIndexEntry, int) {
	list := a.wsSessionMemo.get(workspaceSessionKey{a.sessRev, workspace}, func(key workspaceSessionKey) workspaceSessionList {
		var entries []backend.SessionIndexEntry
		for _, entry := range a.sessions {
			if key.workspace == "" || strings.EqualFold(entry.Workspace, key.workspace) {
				entries = append(entries, entry)
			}
		}
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].UpdatedAt > entries[j].UpdatedAt
		})
		total := len(entries)
		if len(entries) > 80 {
			// Retain only the displayed prefix, not the full sorted backing
			// array. The authoritative index still owns every session.
			entries = append([]backend.SessionIndexEntry(nil), entries[:80]...)
		}
		return workspaceSessionList{entries: entries, total: total}
	})
	return list.entries, list.total
}

// petalsOverlay draws a light decorative layer after the original welcome
// effect: a handful of drifting sakura petals. It runs only while enabled.
func (a *desktop) petalsOverlay(c *ui.Context) {
	if !a.petals {
		return
	}
	still := uikit.StillMotion(c)
	ui.Overlay(c, func() {
		box := ui.Box(c).Fill().PassThrough()
		box.Draw(func(p *ui.Painter, r ui.Rect) {
			// Paint the petals alone: AnimationFrame on the context asked for a
			// frame that rebuilt the whole view at the display's rate, which a
			// chat full of messages paid for on every petal of every frame.
			// With reduced motion they rest in one still arrangement.
			ms := 0.0
			if !still {
				p.AnimationFrameRate(30)
				ms = float64(p.Now().UnixMilli())
			}
			for i := 0; i < 14; i++ {
				seed := float64(i)*0.37 + ms/4200
				x := float32((float64(i)*0.077 + 0.05) * float64(r.W))
				drift := float32(24 * math.Sin(seed*2.1))
				y := ms/9000 + float64(i)*0.113
				y = float64(r.H) * (y - float64(int(y)))
				p.Fill(ui.Rect{X: r.X + x + drift, Y: r.Y + float32(y), W: 7, H: 4}, ui.RGBA(240, 183, 205, 0.55), 2)
			}
		})
	})
}

// Section headers reuse the navigation layout and ink. The add action is a
// sibling, so it never toggles the section when pressed.
func (a *desktop) railSectionHeader(c *ui.Context, id, label, icon string, open bool, tip string, toggle, add func()) {
	ui.Row(c.Key(id + "-header")).FillWidth().Height(heightRailItem).Children(func() {
		navItem(c, id+"-toggle", label, icon, true, false, func() {
			arrow := "chevron"
			if open {
				arrow = "chevrondown"
			}
			iconGlyph(c, arrow, sizeCaretInline).Shrink(0).TextColor(a.secondaryInk())
		}, a.secondaryInk()).Grow(1).MinWidth(0).OnClick(toggle)
		uikit.IconButtonSized(c, id+"-add", tip, iconImage("plus"), 22, 20*uikit.IconGlyphScale, uikit.IconQuiet, add, a.secondaryInk()).Shrink(0)
	})
}

// Reserve the close slot while hidden; revealing it cannot move the title.
func railHoverAction(c *ui.Context, row, button ui.Element, disabled bool) {
	show := row.Hovered() || row.FocusWithin()
	opacity := float32(0)
	if show {
		opacity = 1
	}
	button.Shrink(0).Disabled(disabled || !show)
	uikit.HoverFade(c, button, opacity)
}
