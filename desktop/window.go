// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// view composes the window: header, mode rail and the active page. Every
// sub-view is a method so a page can be read (and ported) on its own.
func (a *desktop) view(c *ui.Context) {
	c.SetTheme(a.theme())
	a.globalShortcuts(c)
	c.OnShortcut(0, ui.KeyEscape, func() { a.cancelConversation(a.current()) })
	if len(a.notifications) > 0 {
		pending := a.notifications
		a.notifications = nil
		for _, message := range pending {
			c.Toast(message)
		}
	}
	width, _ := c.Size()
	p := a.currentPalette()
	ui.Column(c).Fill().Background(p.bg).Selectable().Children(func() {
		a.headerBar(c)
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.rail(c)
			if a.prefs.RailExpanded {
				a.resizeDivider(c, "rail-resize", &a.prefs.RailWidth, 1, 160, 360, &a.railResizing)
			}
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				if !a.ready {
					a.splash(c)
					return
				}
				a.pageContent(c, width)
			})
		})
	})
	a.gitDiffDialog(c)
	a.taskPopupDialog(c)
	a.sshAuthDialog(c)
	a.updateDialog(c)
	a.releaseNotesDialog(c)
	a.deleteUserMessageDialog(c)
	a.petalsOverlay(c)
	toastWidth := min(float32(380), max(float32(220), width-32))
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(16).AlignItems(ui.End).Gap(8)
		for _, item := range toasts {
			toast := ui.ToastBase(c, item)
			toast.Root.Row().Width(toastWidth).MaxWidth(width-32).AlignItems(ui.Center).Gap(10).
				Padding(10, 14).Radius(radiusControl).Background(p.panel).Border(1, p.line).
				Shadow(0, 5, 16, 0, ui.RGBA(0, 0, 0, 0.22))
			toast.Root.Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Shrink(0).Background(p.accent)
				ui.Text(c, item.Title).Grow(1).MinWidth(0).FontSize(fsSmall).TextColor(p.text).LineHeight(1.4).MaxLines(3)
				close := toast.CloseButton().Size(20, 20).Center().Radius(4).TextColor(p.muted).Cursor(ui.CursorPointer)
				close.Label("关闭通知")
				close.Children(func() { ui.Text(c, "×").FontSize(16).TextColor(p.muted) })
			})
		}
	})
}

func (a *desktop) splash(c *ui.Context) {
	p := a.currentPalette()
	ui.Column(c).Fill().Center().Gap(gapSection).Children(func() {
		ui.Text(c, "Ally").FontSize(34).Bold().TextColor(p.accent)
		ui.Text(c, a.status).FontSize(fsBody).Selectable()
		ui.Spinner(c).Size(18, 18)
	})
}

func (a *desktop) pageContent(c *ui.Context, width float32) {
	switch {
	case strings.HasPrefix(a.page, "plugin:"):
		a.panelPage(c, "插件页面", "", nil, a.pluginPageBody)
	case a.page == "chat":
		a.chatPage(c, width)
	case a.page == "kb":
		a.kbPage(c, width)
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
	case a.page == "games":
		a.panelPage(c, "人机对战", "", nil, a.gamesPageBody)
	default:
		emptyState(c, "页面不存在", a.page)
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
	if a.explorer && a.current() != nil {
		if a.activeFile() != nil {
			// A file being edited takes the main area, the tree keeps its
			// column, exactly like the Vue explorer overlay.
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				a.fileEditor(c, a.activeFile())
				a.resizeDivider(c, "tree-resize", &a.prefs.TreeWidth, -1, 150, 600, &a.treeResizing)
				a.fileTree(c)
			})
			return
		}
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() { chat() })
			a.resizeDivider(c, "tree-resize", &a.prefs.TreeWidth, -1, 150, 600, &a.treeResizing)
			a.fileTree(c)
		})
		return
	}
	chat()
}

func (a *desktop) chatColumn(c *ui.Context, width float32) {
	t := a.current()
	if t == nil {
		emptyState(c, "还没有会话", "选择或创建一个工作区开始对话")
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		a.chatMessages(c, t, width)
		a.composer(c, t)
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
					fieldNote(c, "选择一个知识库目录，Ally 会在其中维护 index.md 与分类结构；sources/ 子目录对模型只读。")
					ui.Row(c).Gap(gapField).Children(func() {
						primary(c, "选择知识库目录").OnClick(func() { a.pickKBRoot() })
						flat(c, "打开设置").OnClick(func() { a.changePage("settings") })
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
			ui.Text(c, "整理资料、检索与维护知识索引。").FontSize(fsSmall).TextColor(c.Theme().TextMuted)
			ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
				iconGlyph(c, "folderopen", 13)
				ui.Text(c, a.config.KBRoot).FontSize(fsTiny).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(a.config.KBRoot)
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
			a.openWorkspace(p)
			a.page = "kb"
		})
	}()
}

// releaseNotesDialog shows the bundled changelog once per version.
func (a *desktop) releaseNotesDialog(c *ui.Context) {
	if !a.upd.ReleaseOpen {
		return
	}
	modalCard(c, &a.upd.ReleaseOpen, widthModalWide, func() {
		modalTitle(c, "本次更新", "版本 "+a.upd.ReleaseVer)
		if strings.TrimSpace(a.upd.ReleaseBody) == "" {
			fieldNote(c, "此构建未内置更新日志。")
		} else {
			ui.Text(c, a.upd.ReleaseBody).Selectable().FontSize(fsBody).LineHeight(1.6)
		}
		modalActions(c, func() {
			primary(c, "关闭").OnClick(func() { a.upd.ReleaseOpen = false })
		})
	})
}
