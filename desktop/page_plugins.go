// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *desktop) refreshPlugins() {
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
				a.notify(err.Error())
				return
			}
			a.plugins = plugins
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
				a.notify(err.Error())
				return
			}
			a.plugins = list
			a.config = cfg
			if message != "" {
				a.status = message
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
			return "已导入 " + pluginName(p) + "，但它不可用：" + p.LoadError, nil
		}
		return "已导入插件 " + pluginName(p), nil
	})
}

func (a *desktop) setPluginEnabled(p backend.PluginInfo, enabled bool) {
	a.pluginAction(func() (string, error) {
		err := a.core.SetPluginEnabled(p.ID, enabled)
		label := "已禁用 "
		if enabled {
			label = "已启用 "
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
		return "已导出到 " + path, err
	})
}

func (a *desktop) deletePlugin(p backend.PluginInfo, purge bool) {
	a.pluginsF.DeleteOpen = false
	a.pluginAction(func() (string, error) {
		err := a.core.DeletePlugin(p.ID, purge)
		return "已删除插件 " + pluginName(p), err
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
		fieldNote(c, fmt.Sprintf("%d 个已安装", len(a.plugins)))
		ui.Spacer(c)
		flat(c, "开发教程").Tooltip("在浏览器打开插件开发文档").OnClick(func() {
			services.OpenURL("https://github.com/Bronya0/ally-agent/blob/main/docs/plugin-system.md")
		})
		flat(c, "刷新").OnClick(func() { a.refreshPlugins() })
		flat(c, "从目录安装").Tooltip("从本机任意位置的插件源码目录安装").OnClick(func() { a.importPlugin(true) })
		primary(c, "添加").Tooltip("选择插件 zip 包").OnClick(func() { a.importPlugin(false) })
	})
	if a.pluginsF.Busy {
		busyRow(c, "正在处理插件…")
	}
	if len(a.plugins) == 0 {
		a.pluginEmptyState(c)
		return
	}
	for _, p := range a.plugins {
		p := p
		a.pluginRow(c, p)
	}
	a.pluginDeleteDialog(c)
}

func (a *desktop) pluginEmptyState(c *ui.Context) {
	card(c, "plugin-empty", func() {
		ui.Row(c).Gap(gapSection).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).Gap(gapField).Children(func() {
				accentChip(c, "插件系统 v1")
				ui.Text(c, "把 Ally 变成你自己的控制台").FontSize(19).Bold()
				ui.Text(c, "三分钟定制自己想要的页面，比如公司 OA、BUG 管理页面、工时计划表等等。").FontSize(fsBody).TextColor(c.Theme().TextMuted)
				flat(c, "开发教程").AlignSelf(ui.Start).OnClick(func() {
					c.Services().OpenURL("https://github.com/Bronya0/ally-agent/blob/main/docs/plugin-system.md")
				})
			})
			ui.Column(c).Width(240).Gap(gapTight).Children(func() {
				fieldNote(c, "可用的宿主能力")
				for _, cap := range []string{"host.http", "host.store", "host.files", "host.ui.notify", "host.events", "host.ui.theme"} {
					ui.Text(c, cap).Font(monoFamily).FontSize(fsTiny).TextColor(c.Theme().Accent)
				}
			})
		})
	})
}

func (a *desktop) pluginRow(c *ui.Context, p backend.PluginInfo) {
	name := pluginName(p)
	rowCard(c, "plugin-row-"+p.ID, func() {
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, name).FontSize(fsBody).Bold()
			version := p.ID + "@" + p.Version
			if p.Version == "" {
				version = p.ID + "@-"
			}
			fieldNote(c, version)
			switch {
			case !p.Valid:
				statusBadge(c, "不可用", "error")
			case !p.Enabled:
				statusBadge(c, "已禁用", "idle")
			}
			ui.Spacer(c)
			enabled := p.Enabled
			if sw := ui.Switch(c.Key("plugin-switch-"+p.ID), &enabled).Disabled(a.pluginsF.Busy || !p.Valid).Label(name); sw.Changed() {
				a.setPluginEnabled(p, enabled)
			}
			p := p
			flat(c.Key("plugin-export-button-"+p.ID), "导出 ▾").Disabled(a.pluginsF.Busy).OnClick(func() {
				a.popupMenu([]*mygo.MenuItem{
					menuAction("导出（只含包本体）", func() { a.exportPlugin(p, false) }),
					menuAction("导出（连插件数据一起）", func() { a.exportPlugin(p, true) }),
				})
			})
			dangerButton(c, "删除").Disabled(a.pluginsF.Busy).OnClick(func() {
				a.pluginsF.DeleteID = p.ID
				a.pluginsF.Purge = false
				a.pluginsF.DeleteOpen = true
			})
		})
		if p.Description != "" {
			ui.Text(c, p.Description).FontSize(fsSmall).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(p.Description)
		} else {
			fieldNote(c, "无描述")
		}
		if meta := pluginMeta(p); meta != "" {
			fieldNote(c, meta)
		}
		hosts := "无"
		if len(p.Permissions.HTTP) > 0 {
			hosts = strings.Join(p.Permissions.HTTP, "、")
		}
		workspace := "无"
		if p.Permissions.Workspace == "read" {
			workspace = "只读"
		}
		fieldNote(c, "权限：网络 "+hosts+" ｜ 工作区 "+workspace)
		ui.Text(c, p.Dir).Font(monoFamily).FontSize(fsTiny).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(p.Dir)
		if p.LoadError != "" {
			inlineError(c, "plugin-error-"+p.ID, p.LoadError)
		}
	})
}

func pluginMeta(p backend.PluginInfo) string {
	var parts []string
	if p.Author != "" {
		parts = append(parts, "作者 "+p.Author)
	}
	if p.HasPackage {
		parts = append(parts, "包 "+fmtBytes(p.PackageBytes))
	}
	if p.UpdatedAt != "" {
		parts = append(parts, "安装于 "+p.UpdatedAt)
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
		modalTitle(c, "删除插件 "+pluginName(target), "删除后侧栏入口会一并消失；重新导入同一个包即可恢复。")
		ui.Checkbox(c.Key("plugin-purge"), &a.pluginsF.Purge, "同时删除插件自己保存的数据（账号、令牌等）")
		fieldNote(c, "不勾选时，重装插件可继续使用原有数据。")
		modalActions(c, func() {
			flat(c, "取消").OnClick(func() { a.pluginsF.DeleteOpen = false })
			dangerButton(c, "删除").Disabled(a.pluginsF.Busy).OnClick(func() { a.deletePlugin(target, a.pluginsF.Purge) })
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

// pluginPageBody is the native presentation of a web plugin page. Plugin code
// is JavaScript running in the web host, which the native build cannot embed;
// the page states that plainly and keeps the plugin's data reachable.
func (a *desktop) pluginPageBody(c *ui.Context) {
	p := a.currentPlugin()
	if p == nil {
		emptyState(c, "插件不存在或已停用", "返回插件管理重新启用。")
		flat(c, "插件管理").AlignSelf(ui.Start).OnClick(func() { a.changePage("plugins") })
		return
	}
	card(c, "plugin-page", func() {
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
			ui.Text(c, pluginName(*p)).FontSize(18).Bold()
			neutralChip(c, p.ID+"@"+p.Version)
			ui.Spacer(c)
			flat(c, "打开插件目录").OnClick(func() {
				if p.Dir != "" {
					a.job(func() error { return a.core.OpenPathInFileManager(p.Dir) })
				}
			})
			flat(c, "插件管理").OnClick(func() { a.changePage("plugins") })
		})
		notice(c, "info", "这个插件是网页型页面：它在 Web 宿主里执行 JavaScript 与 DOM 渲染，原生窗口无法内嵌执行。原有数据、权限与启停状态都保留在本地。")
		if p.Description != "" {
			ui.Text(c, p.Description).FontSize(fsBody).TextColor(c.Theme().TextMuted)
		}
		kv(c, "入口", p.Entry)
		kv(c, "安装目录", p.Dir)
	})
}
