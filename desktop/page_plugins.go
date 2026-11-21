// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"fmt"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func (a *desktop) refreshPlugins(manual ...bool) {
	if a.pluginsF.Busy || a.core == nil {
		return
	}
	a.pluginSeq++
	seq := a.pluginSeq
	go func() {
		plugins, err := a.core.ListPlugins()
		a.win.Update(func() {
			if a.pluginSeq != seq || a.closing {
				return
			}
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			a.plugins = plugins
			a.pluginsRevision++
			if len(manual) > 0 && manual[0] {
				a.notifySuccess(locales.Text("插件列表已刷新"))
			}
		})
	}()
}

// All plugin mutations go through the existing backend. Serializing actions in
// the UI also prevents a slow refresh from restoring a stale list afterwards.
func (a *desktop) pluginAction(work func() (string, error)) {
	if a.pluginsF.Busy || a.core == nil {
		return
	}
	a.pluginsF.Busy = true
	a.pluginSeq++
	go func() {
		message, err := work()
		var list []backend.PluginInfo
		var cfg backend.ConfigState
		if err == nil {
			list, err = a.core.ListPlugins()
		}
		if err == nil {
			cfg, err = a.core.GetConfig()
		}
		a.win.Update(func() {
			a.pluginsF.Busy = false
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			a.plugins = list
			a.pluginsRevision++
			a.config = cfg
			if message != "" {
				a.notifySuccess(message)
			}
			if strings.HasPrefix(a.page, "plugin:") {
				found := false
				for _, p := range list {
					if p.Mode == a.page && p.Enabled && p.Valid {
						found = true
						break
					}
				}
				if !found {
					a.hideFilePreview()
					a.page = "plugins"
				}
			}
		})
	}()
}

func (a *desktop) importPlugin(directory bool) {
	a.pluginAction(func() (string, error) {
		var path string
		var err error
		if directory {
			path, err = a.core.SelectDirectory("")
		} else {
			path, err = a.core.SelectPluginPackage()
		}
		if err != nil || path == "" {
			return "", err
		}
		var p backend.PluginInfo
		if directory {
			p, err = a.core.ImportPluginFromDir(path)
		} else {
			p, err = a.core.ImportPlugin(path)
		}
		if err != nil {
			return "", err
		}
		if !p.Valid {
			return locales.Text("已导入 ") + pluginName(p) + locales.Text("，但它不可用：") + p.LoadError, nil
		}
		return locales.Text("已导入插件 ") + pluginName(p), nil
	})
}

func (a *desktop) setPluginEnabled(p backend.PluginInfo, enabled bool) {
	a.pluginAction(func() (string, error) {
		err := a.core.SetPluginEnabled(p.ID, enabled)
		label := locales.Text("已禁用 ")
		if enabled {
			label = locales.Text("已启用 ")
		}
		return label + pluginName(p), err
	})
}

func (a *desktop) exportPlugin(p backend.PluginInfo, includeData bool) {
	a.pluginAction(func() (string, error) {
		path, err := a.core.ExportPlugin(p.ID, includeData)
		if path == "" {
			return "", err
		}
		return locales.Text("已导出到 ") + path, err
	})
}

func (a *desktop) deletePlugin(p backend.PluginInfo, purge bool) {
	a.pluginsF.DeleteOpen = false
	a.pluginAction(func() (string, error) {
		err := a.core.DeletePlugin(p.ID, purge)
		return locales.Text("已删除插件 ") + pluginName(p), err
	})
}

func pluginName(p backend.PluginInfo) string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

func (a *desktop) pluginsPageBody(c *ui.Context) {
	services := c.Services()
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
		fieldNote(c, fmt.Sprintf(locales.Text("%d 个已安装"), len(a.plugins)), a.secondaryInk())
		ui.Spacer(c)
		uikit.Tooltip(c, flat(c, locales.Text("开发教程")), locales.Text("在浏览器打开插件开发文档")).OnClick(func() {
			services.OpenURL("https://github.com/Bronya0/ally-agent/blob/main/docs/plugin-system.md")
		})
		flat(c, locales.Text("刷新")).OnClick(func() { a.refreshPlugins(true) })
		uikit.Tooltip(c, flat(c, locales.Text("从目录安装")), locales.Text("从本机任意位置的插件源码目录安装")).OnClick(func() { a.importPlugin(true) })
		uikit.Tooltip(c, primary(c, locales.Text("添加")), locales.Text("选择插件 zip 包")).OnClick(func() { a.importPlugin(false) })
	})
	if a.pluginsF.Busy {
		busyRow(c, locales.Text("正在处理插件…"), a.secondaryInk())
	}
	if len(a.plugins) == 0 {
		a.pluginEmptyState(c)
		return
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(gapField).Children(func() {
			for _, p := range a.plugins {
				p := p
				a.pluginRow(c, p)
			}
		})
	})
	a.pluginDeleteDialog(c)
}

// pluginEmptyState is the page body until the first plugin: one centred block
// under the toolbar, no panel around it.
func (a *desktop) pluginEmptyState(c *ui.Context) {
	th := c.Theme()
	ui.Column(c).Key("plugin-empty").Fill().Center().Gap(gapField).
		Draw(func(p *ui.Painter, r ui.Rect) {
			// One radial wash behind the copy, centred on the block, so the empty
			// page has depth without an edge to it.
			side := min(max(r.W, r.H), float32(720))
			glow := ui.Rect{X: r.X + (r.W-side)/2, Y: r.Y + (r.H-side)/2 - 24, W: side, H: side}
			p.FillRadialGradient(glow, ui.RadialGradient{
				Center: ui.Point{X: 0.5, Y: 0.5},
				Radius: side / 2,
				Stops: []ui.GradientStop{
					{Color: th.Accent.Alpha(0.13), At: 0},
					{Color: th.Accent.Alpha(0), At: 1},
				},
				Oklab: true,
			}, 0)
		}).Children(func() {
		ui.Text(c, "PLUGIN · SDK v1").Font(monoFamily).FontSize(fsMicro).
			LetterSpacing(1.6).TextColor(th.Accent)
		ui.Text(c, locales.Text("把 Ally 变成你自己的控制台")).FontSize(fsDisplay).Bold().
			TextAlign(ui.Center).LineHeight(1.3)
		ui.Text(c, locales.Text("三分钟定制自己想要的页面，比如公司 OA、BUG 管理页面、工时计划表等等。")).
			FontSize(fsSmall).TextColor(a.secondaryInk()).TextAlign(ui.Center).
			LineHeight(1.7).MaxWidth(560)
		ui.Box(c).Margin(gapField, 0).Children(func() {
			uikit.Link(c, locales.Text("开发教程")+"  →",
				"https://github.com/Bronya0/ally-agent/blob/main/docs/plugin-system.md").FontSize(fsBody)
		})
	})
}

func (a *desktop) pluginRow(c *ui.Context, p backend.PluginInfo) {
	name := pluginName(p)
	rowCard(c, "plugin-row-"+p.ID, func() {
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, name).FontSize(fsBody).Bold()
			version := p.ID
			if p.Version != "" {
				version += "@" + p.Version
			}
			fieldNote(c, version, a.secondaryInk())
			switch {
			case !p.Valid:
				statusBadge(c, locales.Text("不可用"), "error")
			case !p.Enabled:
				statusBadge(c, locales.Text("已禁用"), "idle")
			}
			ui.Spacer(c)
			enabled := p.Enabled
			uikit.SwitchNative(c.Key("plugin-switch-"+p.ID), &enabled).Disabled(a.pluginsF.Busy || !p.Valid).Label(name).OnClick(func() { a.setPluginEnabled(p, enabled) })
			p := p
			disclosureMenu(c, "plugin-export-button-"+p.ID, locales.Text("导出"), disclosureCard, 210, a.pluginsF.Busy, func(m ui.MenuParts) {
				uikit.MenuItem(c, m, "package", locales.Text("导出（只含包本体）"), uikit.MenuItemOptions{}, a.secondaryInk()).OnClick(func() {
					a.exportPlugin(p, false)
				})
				uikit.MenuItem(c, m, "data", locales.Text("导出（连插件数据一起）"), uikit.MenuItemOptions{}, a.secondaryInk()).OnClick(func() {
					a.exportPlugin(p, true)
				})
			})
			cardDanger(c, locales.Text("删除")).Disabled(a.pluginsF.Busy).OnClick(func() {
				a.pluginsF.DeleteID = p.ID
				a.pluginsF.Purge = false
				a.pluginsF.DeleteOpen = true
			})
		})
		if p.Description != "" {
			uikit.Tooltip(c, ui.Text(c, p.Description).FontSize(fsSmall).TextColor(a.secondaryInk()).SingleLine(), p.Description)
		} else {
			fieldNote(c, locales.Text("无描述"), a.secondaryInk())
		}
		if meta := pluginMeta(p); meta != "" {
			fieldNote(c, meta, a.secondaryInk())
		}
		hosts := locales.Text("无")
		if len(p.Permissions.HTTP) > 0 {
			hosts = strings.Join(p.Permissions.HTTP, "、")
		}
		workspace := locales.Text("无")
		if p.Permissions.Workspace == "read" {
			workspace = locales.Text("只读")
		}
		fieldNote(c, locales.Text("权限：网络 ")+hosts+locales.Text(" ｜ 工作区 ")+workspace, a.secondaryInk())
		uikit.Tooltip(c, ui.Text(c, p.Dir).Font(monoFamily).FontSize(fsTiny).TextColor(a.secondaryInk()).SingleLine(), p.Dir)
		if p.LoadError != "" {
			inlineError(c, "plugin-error-"+p.ID, p.LoadError)
		}
	})
}

func pluginMeta(p backend.PluginInfo) string {
	var parts []string
	if p.Author != "" {
		parts = append(parts, locales.Text("作者 ")+p.Author)
	}
	if p.HasPackage {
		parts = append(parts, locales.Text("包 ")+fmtBytes(p.PackageBytes))
	}
	if p.UpdatedAt != "" {
		parts = append(parts, locales.Text("安装于 ")+p.UpdatedAt)
	}
	return strings.Join(parts, "  ·  ")
}

func (a *desktop) pluginDeleteDialog(c *ui.Context) {
	if !a.pluginsF.DeleteOpen {
		return
	}
	target := backend.PluginInfo{}
	for _, p := range a.plugins {
		if p.ID == a.pluginsF.DeleteID {
			target = p
			break
		}
	}
	modalCard(c, &a.pluginsF.DeleteOpen, widthModalSmall, func() {
		modalTitle(c, locales.Text("删除插件 ")+pluginName(target), locales.Text("删除后侧栏入口会一并消失；重新导入同一个包即可恢复。"))
		uikit.Checkbox(c.Key("plugin-purge"), &a.pluginsF.Purge, locales.Text("同时删除插件自己保存的数据（账号、令牌等）"))
		fieldNote(c, locales.Text("不勾选时，重装插件可继续使用原有数据。"), a.secondaryInk())
		modalActions(c, func() {
			flat(c, locales.Text("取消")).OnClick(func() { a.pluginsF.DeleteOpen = false })
			dangerButton(c, locales.Text("删除")).Disabled(a.pluginsF.Busy).OnClick(func() { a.deletePlugin(target, a.pluginsF.Purge) })
		})
	})
}

func (a *desktop) currentPlugin() *backend.PluginInfo {
	for i := range a.plugins {
		if a.plugins[i].Mode == a.page && a.plugins[i].Enabled && a.plugins[i].Valid {
			return &a.plugins[i]
		}
	}
	return nil
}

// pluginPageBody hosts the unchanged v1 plugin mount contract in a MyGo WebView.
func (a *desktop) pluginPageBody(c *ui.Context) {
	p := a.currentPlugin()
	if p == nil {
		emptyState(c, locales.Text("插件不存在或已停用"), locales.Text("返回插件管理重新启用。"), a.secondaryInk())
		return
	}
	ui.Column(c.Key("plugin-page-" + p.ID)).Fill().MinHeight(0).Children(func() {
		ui.Row(c).Padding(8, 16).Gap(gapField).Children(func() {
			ui.Text(c, pluginName(*p)).Bold()
			ui.Spacer(c)
			flat(c, locales.Text("重新加载")).OnClick(func() {
				if a.pluginHost != nil {
					a.pluginHost.Reload(p.ID)
				}
			})
			flat(c, locales.Text("插件管理")).OnClick(func() { a.changePage("plugins") })
		})
		if a.pluginHost == nil {
			return
		}
		view, err := a.pluginHost.Page(p.ID)
		if err != nil {
			notice(c, "error", err.Error(), a.secondaryInk())
		} else if view != nil {
			ui.WebView(c.Key("plugin-surface-"+p.ID), view).Grow(1).FillWidth()
		} else {
			busyRow(c, locales.Text("正在加载插件…"), a.secondaryInk())
		}
	})
}
