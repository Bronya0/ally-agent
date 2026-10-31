// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// view composes the window: header, mode rail and the active page. Every
// sub-view is a method so a page can be read (and ported) on its own.
func (a *desktop) view(c *ui.Context) {
	a.previewRequests = a.previewRequests[:0]
	defer a.schedulePreviewSync()
	defer a.syncPluginHost()
	c.SetTheme(a.theme())
	a.globalShortcuts(c)
	c.OnShortcut(0, ui.KeyEscape, func() { a.cancelConversation(a.current()) })
	a.flushNotices(c, time.Now())
	width, _ := c.Size()
	p := a.currentPalette()
	ui.Column(c).Fill().Background(p.bg).Selectable().Children(func() {
		a.headerBar(c)
		content := func() {
			ui.Column(c.Key("page-content")).Grow(1).MinWidth(0).MinHeight(0).FillHeight().Children(func() {
				if !a.ready {
					a.startupBody(c)
					return
				}
				a.pageContent(c, width)
			})
		}
		if a.prefs.RailExpanded {
			ui.Split(c.Key("rail-split"), &a.prefs.RailWidth, func() { a.rail(c) }, content).
				Grow(1).MinHeight(0).FillWidth().OnChange(func() {
				a.prefs.RailWidth = max(160, min(360, a.prefs.RailWidth))
				a.savePrefs()
			})
		} else {
			ui.Row(c.Key("collapsed-layout")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				a.rail(c)
				content()
			})
		}
	})
	a.gitDiffDialog(c)
	a.taskPopupDialog(c)
	a.sshAuthDialog(c)
	a.updateDialog(c)
	a.releaseNotesDialog(c)
	a.deleteUserMessageDialog(c)
	a.petalsOverlay(c)
	uikit.Messages(c)
}

// Startup uses the final window shell, without a second logo or animated scene.
func (a *desktop) startupBody(c *ui.Context) {
	ui.Column(c.Key("startup-body")).Fill().Padding(padPage).Children(func() {
		if a.startupError != "" {
			notice(c, "error", a.startupError, a.secondaryInk())
		}
	})
}

func (a *desktop) pageContent(c *ui.Context, width float32) {
	switch {
	case strings.HasPrefix(a.page, "plugin:"):
		a.pluginPageBody(c)
	case a.page == "chat":
		a.chatPage(c, width)
	case a.page == "settings":
		a.panelPage(c, "设置", "", nil, a.settingsPageBody)
	case a.page == "models":
		a.panelPage(c, "模型", "按 Provider 管理对话模型与生图模型", nil, a.modelsPageBody)
	case a.page == "mcp":
		a.panelPage(c, "MCP 服务", "", nil, a.mcpPageBody)
	case a.page == "skills":
		a.panelPage(c, "技能", "", nil, a.skillsPageBody)
	case a.page == "ssh":
		a.panelPage(c, "SSH 集群授权",
			"添加服务器后，在对话底部勾选授权，模型即可使用 Ally 的 SSH 工具；密钥等凭据由 Ally 管理，不会传给模型。",
			nil, a.sshPageBody)
	case a.page == "plugins":
		a.panelPage(c, "插件", "", nil, a.pluginsPageBody)
	case a.page == "tasks":
		a.panelPage(c, "任务中心", "", nil, a.tasksPageBody)
	case a.page == "stats":
		a.panelPage(c, "用量统计", "", nil, a.statsPageBody)
	default:
		emptyState(c, "页面不存在", a.page, a.secondaryInk())
	}
}

// panelPage is the standard full-height page shell: optional title header plus
// a padded, scrollable body. Pages own their own toolbars so search fields and
// action buttons stay next to the content they act on.
func (a *desktop) panelPage(c *ui.Context, title, subtitle string, headerActions func(), body func(*ui.Context)) {
	fixed := a.page == "models" || a.page == "mcp" || a.page == "skills" || a.page == "ssh" || a.page == "tasks" || a.page == "settings"
	if fixed {
		ui.Column(c).Grow(1).MinHeight(0).Padding(padPage, padPage, padPage+8).Gap(gapSection).Children(func() {
			if title != "" {
				pageHeader(c, title, subtitle, headerActions)
			}
			body(c)
		})
		return
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(padPage, padPage, padPage+8).Gap(gapSection).Children(func() {
			if title != "" {
				pageHeader(c, title, subtitle, headerActions)
			}
			body(c)
		})
	})
}

// chatPage hosts the conversation column plus the optional explorer.
func (a *desktop) chatPage(c *ui.Context, width float32) {
	chat := func() { a.chatColumn(c, width) }
	if !a.explorer || a.current() == nil {
		chat()
		return
	}
	if a.prefs.TreeWidth < 150 || a.prefs.TreeWidth > 600 {
		a.prefs.TreeWidth = 270
	}
	// Split sizes its first pane. RTL places that pane on the right; each
	// pane restores the application's text direction before building content.
	ui.Column(c.Key("explorer-layout")).Direction(ui.RightToLeft).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Split(c.Key("explorer-split"), &a.prefs.TreeWidth, func() {
			ui.Column(c).Direction(ui.LeftToRight).Fill().Children(func() { a.fileTree(c) })
		}, func() {
			ui.Column(c).Direction(ui.LeftToRight).Fill().MinWidth(0).Children(func() {
				if file := a.activeFile(); file != nil {
					a.fileEditor(c, file)
				} else {
					chat()
				}
			})
		}).Fill().OnChange(func() {
			a.prefs.TreeWidth = max(150, min(600, a.prefs.TreeWidth))
			a.savePrefs()
		})
	})
}

func (a *desktop) chatColumn(c *ui.Context, width float32) {
	t := a.current()
	if t == nil {
		emptyState(c, "还没有会话", "选择或创建一个工作区开始对话", a.secondaryInk())
		return
	}
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		a.chatMessages(c, t, width)
		ui.Column(c).FillWidth().MaxWidth(chatMaxWidth).AlignSelf(ui.Center).Shrink(0).Children(func() {
			a.composer(c, t)
		})
	})
}

// kbPage shows either the knowledge-base guidance card (no root configured) or
// the normal workbench with the KB identity header.
func (a *desktop) kbPage(c *ui.Context, width float32) {
	if strings.TrimSpace(a.config.KBRoot) == "" {
		ui.Column(c).Fill().Center().Children(func() {
			card(c, "kb-empty", func() {
				ui.Column(c).Width(460).Gap(gapSection).Children(func() {
					ui.Text(c, "知识库").FontSize(20).Bold()
					fieldNote(c, "选择一个知识库目录，Ally 会在其中维护 index.md 与分类结构；sources/ 子目录对模型只读。", a.secondaryInk())
					ui.Row(c).Gap(gapField).Children(func() {
						cardPrimary(c, "选择知识库目录").OnClick(func() { a.pickKBRoot() })
						cardFlat(c, "打开设置").OnClick(func() { a.changePage("settings") })
					})
				})
			})
		})
		return
	}
	t := a.current()
	if t == nil || !sameWorkspace(t.Workspace, a.config.KBRoot) {
		a.openWorkspace(a.config.KBRoot)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(16, padPage, 4).Gap(2).Children(func() {
			ui.Text(c, "知识库").FontSize(19).Bold()
			ui.Text(c, "整理资料、检索与维护知识索引。").FontSize(fsSmall).TextColor(a.secondaryInk())
			ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
				iconGlyph(c, "folderopen", 13)
				uikit.Tooltip(c, ui.Text(c, a.config.KBRoot).FontSize(fsTiny).TextColor(a.secondaryInk()).SingleLine(), a.config.KBRoot)
			})
		})
		a.chatColumn(c, width)
	})
}

func (a *desktop) pickKBRoot() {
	go func() {
		p, err := a.core.SelectKnowledgeBaseRoot()
		if err != nil {
			a.fail(err)
			return
		}
		if p == "" {
			return
		}
		a.win.Update(func() {
			a.config.KBRoot = p
			cfg := a.config
			a.saveConfig(cfg)
		})
	}()
}
