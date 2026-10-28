// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type modeItem struct {
	id, label, icon string
}

var baseModes = []modeItem{
	{"chat", "Agent", "chat"},
	{"kb", "知识库", "kb"},
	{"skills", "技能", "skills"},
	{"mcp", "MCP 服务", "mcp"},
	{"models", "模型", "models"},
	{"ssh", "SSH 集群", "ssh"},
	{"tasks", "任务中心", "tasks"},
	{"stats", "Token 统计", "stats"},
	{"games", "人机对战", "sword"},
	{"plugins", "插件", "plugins"},
}

func (a *desktop) modeHidden(id string) bool {
	if id == "chat" || id == "settings" {
		return false
	}
	for _, name := range a.config.HiddenModes {
		if strings.EqualFold(strings.TrimSpace(name), id) {
			return true
		}
	}
	return false
}

// pluginModes returns the rail entries contributed by enabled plugins, with
// grouped plugins folded into a single entry.
func (a *desktop) pluginModes() (top []backend.PluginInfo, groups [][]backend.PluginInfo) {
	groupIndex := map[string]int{}
	for _, p := range a.plugins {
		if !p.Enabled || !p.Valid || p.Mode == "" {
			continue
		}
		if p.Group == nil || p.Group.Key == "" {
			top = append(top, p)
			continue
		}
		key := p.Group.Key
		idx, ok := groupIndex[key]
		if !ok {
			idx = len(groups)
			groupIndex[key] = idx
			groups = append(groups, nil)
		}
		groups[idx] = append(groups[idx], p)
	}
	return top, groups
}

func (a *desktop) rail(c *ui.Context) {
	p := a.currentPalette()
	if !a.prefs.RailExpanded {
		ui.Column(c).Width(widthRailCollapsed).Shrink(0).Background(p.panel).BorderWidth(0, 1, 0, 0).BorderColor(p.line).Padding(4, railPadH).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Scroll(c).Key("rail-modes").FillWidth().Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).FillWidth().Gap(4).Children(func() { a.railModes(c, false) })
			})
			railToggleButton(c, "rail-open", "展开侧边栏（显示菜单名）", "expand", func() {
				a.prefs.RailExpanded = true
				a.savePrefs()
			}).TextColor(p.muted)
		})
		return
	}
	a.railBody(c, &a.prefs.RailWidth)
}

func (a *desktop) railBody(c *ui.Context, width *float32) {
	p := a.currentPalette()
	ui.Column(c).Width(*width).Shrink(0).Background(p.panel).BorderWidth(0, 1, 0, 0).BorderColor(p.line).Padding(6).Gap(6).Children(func() {
		a.railModes(c, true)
		divider(c)
		ui.Column(c).Grow(1).MinHeight(0).Gap(6).Children(func() {
			a.railWorkspaces(c)
			a.railSessions(c)
		})
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			label := "收起侧边栏"
			iconName := "collapse"
			railToggleButton(c, "rail-close", label, iconName, func() {
				a.prefs.RailExpanded = false
				a.savePrefs()
			}).TextColor(p.muted)
			ui.Spacer(c)
			tone := toneColor(c, statusTone(a.status))
			if a.status == "" || a.status == "就绪" || a.status == "操作完成" {
				tone = p.muted
			}
			ui.Text(c, short(a.status, 20)).FontSize(10.5).TextColor(tone).Tooltip(a.status).SingleLine()
		})
	})
}

func (a *desktop) railModes(c *ui.Context, expanded bool) {
	pluginTop, pluginGroups := a.pluginModes()
	for _, mode := range baseModes {
		if mode.id == "plugins" {
			// Plugin entries sit right before the plugin manager, matching the
			// original rail order.
			for _, item := range pluginTop {
				a.railModeButton(c, expanded, item.Mode, pluginTitle(item), "plugins", false)
			}
			for _, group := range pluginGroups {
				if len(group) == 0 {
					continue
				}
				head := group[0]
				title := pluginTitle(head)
				if head.Group != nil && head.Group.Title != "" {
					title = head.Group.Title
				}
				active := false
				for _, item := range group {
					if item.Mode == a.page {
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
		if mode.id == "kb" && strings.HasPrefix(a.page, "kb") {
			active = true
		}
		if mode.id == "chat" && strings.HasPrefix(a.page, "plugin:") {
			active = false
		}
		a.railModeButton(c, expanded, mode.id, mode.label, mode.icon, active)
	}
	// The settings entry is always last and never hidden.
	active := a.page == "settings"
	a.railModeButton(c, expanded, "settings", "设置", "settings", active)
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
	railNavigationButton(c, "nav-"+id, label, iconName, expanded, active, nil).OnClick(func() {
		a.changePage(id)
	})
}

// railGroupButton opens the group's pages in the system's own menu: it is a
// plain list of pages with the current one marked, which the menu does itself.
func (a *desktop) railGroupButton(c *ui.Context, expanded bool, head backend.PluginInfo, title string, group []backend.PluginInfo, active bool) {
	railNavigationButton(c, "nav-group-"+head.ID, title, "plugins", expanded, active, func() {
		ui.Spacer(c)
		iconGlyph(c, "chevrondown", 12)
	}).OnClick(func() {
		items := make([]*mygo.MenuItem, 0, len(group))
		for _, item := range group {
			item := item
			items = append(items, menuItem(pluginTitle(item), item.Mode == a.page, func() {
				a.changePage(item.Mode)
				a.win.Update(func() {})
			}))
		}
		a.popupMenu(items)
	})
}

func (a *desktop) railWorkspaces(c *ui.Context) {
	p := a.currentPalette()
	ui.Column(c).Key("rail-workspaces").FillWidth().Shrink(1).MinHeight(24).Gap(3).Children(func() {
		ui.Row(c).Height(24).Padding(0, 6, 0, 4).Children(func() {
			toggle := ui.ButtonBase(c.Key("rail-ws-toggle")).Grow(1).Height(24).Justify(ui.Start).Label("历史工作区").Cursor(ui.CursorPointer).OnClick(func() { a.prefs.RailWorkspaceOpen = !a.prefs.RailWorkspaceOpen; a.savePrefs() })
			toggle.Children(func() {
				ui.Row(c).FillWidth().Gap(5).Children(func() {
					railIcon(c, "folderopen", 13).TextColor(p.accent)
					ui.Text(c, "历史工作区").FontSize(11).TextColor(p.muted)
					ui.Spacer(c)
					name := "chevron"
					if a.prefs.RailWorkspaceOpen {
						name = "chevrondown"
					}
					iconGlyph(c, name, 10).TextColor(p.muted)
				})
			})
			addButton(c, "rail-ws-add", "添加工作区", "plus", 20, func() { a.chooseWorkspace() })
		})
		if !a.prefs.RailWorkspaceOpen {
			return
		}
		ui.Scroll(c.Key("workspace-items")).FillWidth().MaxHeight(150).MinHeight(0).Children(func() {
			a.railWorkspaceRow(c, "", "临时工作区", "在系统临时目录创建沙箱，随标签页关闭自动删除")
			seen := map[string]bool{}
			var roots []string
			for _, entry := range a.sessions {
				root := entry.Workspace
				if root == "" || seen[strings.ToLower(root)] || strings.HasPrefix(filepath.Base(root), "ally-temp-") {
					continue
				}
				seen[strings.ToLower(root)] = true
				roots = append(roots, root)
			}
			for _, root := range roots {
				root := root
				a.railWorkspaceRow(c, root, workspaceLabel(root), root)
			}
			if len(roots) == 0 {
				ui.Text(c, "无历史记录").FontSize(11).TextColor(faint).Padding(0, 10)
			}
		})
	})
}

func (a *desktop) railWorkspaceRow(c *ui.Context, root, label, tip string) {
	p := a.currentPalette()
	active := false
	if t := a.current(); t != nil && strings.EqualFold(t.Workspace, root) {
		active = true
	}
	row := ui.Row(c.Key("rail-ws-"+root)).Height(26).Radius(6).Padding(0, 8).Gap(6).AlignItems(ui.Center)
	fill := ui.Transparent
	if active || row.Hovered() {
		fill = p.hover
	}
	row.Background(fadeColor(row, "fill", fill))
	row.Tooltip(tip)
	row.Children(func() {
		btn := ui.ButtonBase(c.Key("rail-ws-btn-" + root)).Grow(1).Height(24).Justify(ui.Start).Label(label).Cursor(ui.CursorPointer)
		btn.OnClick(func() {
			if root == "" {
				a.newConversation("")
				return
			}
			a.openWorkspace(root)
		})
		btn.Children(func() { ui.Text(c, label).FontSize(11.5).SingleLine() })
		if root != "" {
			dangerIconAction(c, "rail-ws-del-"+root, "从历史记录移除", "close", 18, func() {
				a.removeWorkspaceHistory(root)
			})
		}
	})
}

func (a *desktop) removeWorkspaceHistory(root string) {
	filtered := a.sessions[:0]
	for _, entry := range a.sessions {
		if !strings.EqualFold(entry.Workspace, root) {
			filtered = append(filtered, entry)
		}
	}
	a.sessions = filtered
	a.status = "已从历史记录移除"
}

func (a *desktop) railSessions(c *ui.Context) {
	p := a.currentPalette()
	ui.Column(c).Key("rail-sessions").FillWidth().Grow(1).MinHeight(24).Gap(3).Children(func() {
		ui.Row(c).Height(24).Padding(0, 6, 0, 4).Children(func() {
			toggle := ui.ButtonBase(c.Key("rail-sess-toggle")).Grow(1).Height(24).Justify(ui.Start).Label("工作区会话").Cursor(ui.CursorPointer).OnClick(func() { a.prefs.RailSessionsOpen = !a.prefs.RailSessionsOpen; a.savePrefs() })
			toggle.Children(func() {
				ui.Row(c).FillWidth().Gap(5).Children(func() {
					railIcon(c, "history", 13).TextColor(p.accent)
					ui.Text(c, "工作区会话").FontSize(11).TextColor(p.muted)
					ui.Spacer(c)
					name := "chevron"
					if a.prefs.RailSessionsOpen {
						name = "chevrondown"
					}
					iconGlyph(c, name, 10).TextColor(p.muted)
				})
			})
			addButton(c, "rail-sess-new", "新建会话", "plus", 20, func() { a.newSession() })
		})
		if !a.prefs.RailSessionsOpen {
			return
		}
		t := a.current()
		workspace := ""
		if t != nil {
			workspace = t.Workspace
		}
		entries := a.workspaceSessions(workspace)
		if len(entries) == 0 {
			ui.Text(c, "当前工作区暂无历史会话").FontSize(11).TextColor(faint).Padding(0, 10)
			return
		}
		ui.Scroll(c.Key("session-items")).FillWidth().Grow(1).MinHeight(0).Children(func() {
			for _, entry := range entries {
				entry := entry
				busy := entry.ID == sessionBusyID(a, entry.ID)
				active := t != nil && t.ID == entry.ID
				row := ui.Row(c.Key("rail-sess-"+entry.ID)).Height(30).Radius(6).Padding(0, 8).AlignItems(ui.Center).Gap(6)
				fill := ui.Transparent
				if active || row.Hovered() {
					fill = p.hover
				}
				row.Background(fadeColor(row, "fill", fill))
				title := entry.Title
				if title == "" {
					title = "会话"
				}
				row.Tooltip(fmt.Sprintf("%s\n%s · %d 条", title, fmtTime(entry.UpdatedAt), entry.MessageCount))
				row.Children(func() {
					btn := ui.ButtonBase(c.Key("rail-sess-btn-" + entry.ID)).Grow(1).Height(28).Justify(ui.Start).Label(title).Cursor(ui.CursorPointer)
					btn.OnClick(func() { a.openSession(entry.ID) })
					btn.Children(func() {
						ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
							ui.Text(c, short(title, 18)).FontSize(11.5).SingleLine()
							if busy {
								ui.Box(c).Size(5, 5).Radius(3).Background(p.accent)
							}
						})
					})
					iconAction(c, "rail-sess-del-"+entry.ID, "删除会话", "close", 18, func() {
						a.deleteSession(entry.ID, title)
					}).Disabled(busy).TextColor(p.muted)
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

func (a *desktop) workspaceSessions(workspace string) []backend.SessionIndexEntry {
	var entries []backend.SessionIndexEntry
	for _, entry := range a.sessions {
		if workspace == "" || strings.EqualFold(entry.Workspace, workspace) {
			entries = append(entries, entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].UpdatedAt > entries[j].UpdatedAt
	})
	if len(entries) > 80 {
		entries = entries[:80]
	}
	return entries
}

// petalsOverlay draws a light decorative layer after the original welcome
// effect: a handful of drifting sakura petals. It runs only while enabled.
func (a *desktop) petalsOverlay(c *ui.Context) {
	if !a.petals {
		return
	}
	ui.Overlay(c, func() {
		box := ui.Box(c).Fill().PassThrough()
		box.Draw(func(p *ui.Painter, r ui.Rect) {
			// Paint the petals alone: AnimationFrame on the context asked for a
			// frame that rebuilt the whole view at the display's rate, which a
			// chat full of messages paid for on every petal of every frame.
			p.AnimationFrameRate(30)
			now := p.Now()
			for i := 0; i < 14; i++ {
				seed := float64(i)*0.37 + float64(now.UnixMilli())/4200
				x := float32((float64(i)*0.077 + 0.05) * float64(r.W))
				drift := float32(24 * math.Sin(seed*2.1))
				y := float64(now.UnixMilli())/9000 + float64(i)*0.113
				y = float64(r.H) * (y - float64(int(y)))
				p.Fill(ui.Rect{X: r.X + x + drift, Y: r.Y + float32(y), W: 7, H: 4}, ui.RGBA(240, 183, 205, 0.55), 2)
			}
		})
	})
}
