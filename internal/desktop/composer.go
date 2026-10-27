// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// composer is the input area: menus, attachments, run status, the editor and
// the info bar with model, reasoning, workspace and context controls.
func (a *desktop) composer(c *ui.Context, t *conversation) {
	st := t.uiState()
	p := a.currentPalette()
	ui.Column(c).Shrink(0).Padding(6, 20, 8).Gap(gapInline).Children(func() {
		if a.page == "kb" {
			a.kbActionBar(c, t)
		}
		if st.Retry != nil {
			a.retryBanner(c, st.Retry)
		}
		if st.SessionsOpen {
			a.sessionsMenu(c, t)
		}
		if st.CmdOpen {
			a.commandMenu(c, t)
		}
		if st.Mention.Open {
			a.mentionMenu(c, t)
		}
		if t.Busy || (a.status != "" && a.status != "就绪" && a.status != "操作完成") || st.CompactNote != "" {
			a.composerStatusRow(c, t)
		}
		if len(t.PendingAttachments) > 0 {
			a.pendingAttachmentRow(c, t)
		}
		ui.Row(c.Key("composer-input-row")).Gap(gapField).AlignItems(ui.End).Children(func() {
			input := ui.TextArea(c.Key(t.ID+"-draft"), &t.Draft).Label("消息输入框").
				Placeholder("输入消息，Enter 发送，Ctrl+Enter 换行，Esc 中断，/ 选择指令，@ 引用文件").
				Height(58).MinHeight(58).Lines(1, 8).Border(1, c.Theme().Border).Radius(radiusControl).
				Background(p.bg).Disabled(t.Busy || a.closing || (a.page == "kb" && a.kbIndexMissing())).Grow(1)
			if paths := input.DroppedFiles(); len(paths) > 0 {
				a.dropAttachments(t, paths)
			}
			if input.FileDragOver() {
				input.Border(2, p.accent)
			}
			if st.RestoreCaret {
				input.SetTextSelection(st.DraftCaret, st.DraftCaret)
				st.RestoreCaret = false
			}
			a.composerKeys(c, t, input)
		})
		if st.CompactBusy || st.CompactNote != "" || t.AttachmentLoading {
			a.composerSendRow(c, t)
		}
		a.composerInfoBar(c, t)
	})
}

func (a *desktop) cancelConversation(t *conversation) {
	if t == nil {
		return
	}
	st := t.uiState()

	if st.CmdOpen || st.Mention.Open || st.SessionsOpen {
		st.CmdOpen = false
		st.Mention.Open = false
		st.SessionsOpen = false
		return
	}
	if t.Busy {
		t.Status = "正在取消…"
		if t.RunID == "" {
			t.CancelRequested = true
		} else {
			id := t.RunID
			a.job(func() error { return a.core.CancelRun(id) })
		}
	} else if st.CompactBusy {
		st.CompactNote = "正在取消压缩…"
		sid := t.ID
		a.job(func() error { return a.core.CancelCompaction(sid) })
	}
}

func (a *desktop) composerKeys(c *ui.Context, t *conversation, input ui.Element) {
	st := t.uiState()
	services := c.Services()

	selectionStart, selectionEnd := input.TextSelection()
	composing := input.Composing()
	if input.Changed() {
		a.updateComposerMenus(t)
	}
	input.OnTextKey(func(k ui.TextKey) bool {
		if k.Key == ui.KeyEnter && k.Mods&ui.Ctrl != 0 && !composing {
			runes := []rune(k.Text())
			start := max(0, min(len(runes), selectionStart))
			end := max(start, min(len(runes), selectionEnd))
			k.SetText(string(runes[:start]) + "\n" + string(runes[end:]))
			st.DraftCaret = start + 1
			st.RestoreCaret = true
			return true
		}
		if k.Key == ui.KeyV && k.Mods == ui.Cmd && !t.Busy && !t.AttachmentLoading && !composing {
			clip := services.ReadClipboardData(transfer.PNG)
			for _, item := range clip.Items() {
				if data, err := item.Read(transfer.PNG); err == nil && len(data) > 0 {
					a.pasteAttachment(t, data)
					return true
				}
			}
		}
		// Menu navigation takes the arrow keys before the editor moves the caret.
		if st.CmdOpen {
			switch k.Key {
			case ui.KeyUp:
				if st.CmdIndex > 0 {
					st.CmdIndex--
				}
				return true
			case ui.KeyDown:
				if st.CmdIndex < len(a.filteredCommands(t))-1 {
					st.CmdIndex++
				}
				return true
			case ui.KeyTab:
				a.completeCommand(t)
				return true
			case ui.KeyEnter:
				if cmd := a.selectedCommand(t); cmd != nil {
					a.runCommand(t, cmd)
					return true
				}
			case ui.KeyEscape:
				st.CmdOpen = false
				return true
			case ui.KeyBackspace:
				if strings.TrimSpace(t.Draft) == "/" {
					st.CmdOpen = false
				}
			}
		}
		if st.Mention.Open {
			switch k.Key {
			case ui.KeyUp:
				if st.Mention.Index > 0 {
					st.Mention.Index--
				}
				return true
			case ui.KeyDown:
				if st.Mention.Index < len(st.Mention.Entries)-1 {
					st.Mention.Index++
				}
				return true
			case ui.KeyTab, ui.KeyEnter:
				if st.Mention.Loading {
					return true
				}
				a.applyMention(t)
				return true
			case ui.KeyEscape:
				st.Mention.Open = false
				return true
			}
		}
		if st.SessionsOpen && k.Key == ui.KeyEscape {
			st.SessionsOpen = false
			return true
		}
		if k.Key == ui.KeyEnter && k.Mods&ui.Shift == 0 && !t.Busy && !composing {
			if st.CmdOpen && a.selectedCommand(t) != nil {
				return false
			}
			if a.send(t, k.Text()) {
				k.SetText("")
				st.CmdOpen = false
				st.Mention.Open = false
			}
			return true
		}
		return false
	})
}

// updateComposerMenus derives the slash-command and @-mention menus from the
// draft. Both are closed while another menu owns the input.
func (a *desktop) updateComposerMenus(t *conversation) {
	st := t.uiState()
	draft := t.Draft
	trimmed := strings.TrimLeft(draft, " ")
	if strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, " ") {
		st.CmdOpen = true
		st.CmdIndex = 0
	} else {
		st.CmdOpen = false
	}
	if st.CmdOpen || st.SessionsOpen {
		st.Mention.Open = false
		return
	}
	if query, ok := mentionQuery(draft); ok {
		st.Mention.Open = true
		a.searchMentions(t, query)
	} else {
		st.Mention.Open = false
	}
}

func mentionQuery(draft string) (string, bool) {
	at := strings.LastIndex(draft, "@")
	if at < 0 {
		return "", false
	}
	if at > 0 {
		prev := rune(draft[at-1])
		if !strings.ContainsRune(" \t\n([{,;:", prev) {
			return "", false
		}
	}
	tail := draft[at+1:]
	if strings.Contains(tail, "\n") || len([]rune(tail)) > 180 {
		return "", false
	}
	return strings.TrimPrefix(tail, "\""), true
}

func (a *desktop) searchMentions(t *conversation, query string) {
	st := t.uiState()
	st.Mention.Seq++
	seq := st.Mention.Seq
	workspace := t.Workspace
	st.Mention.Loading = true
	time.AfterFunc(90*time.Millisecond, func() {
		a.win.Update(func() {
			if st.Mention.Seq != seq || a.closing {
				return
			}
			go func() {
				result, err := a.core.SearchWorkspacePaths(backend.WorkspacePathSearchRequest{Workspace: workspace, Query: query, Limit: 30})
				a.win.Update(func() {
					if st.Mention.Seq != seq {
						return
					}
					st.Mention.Loading = false
					if err != nil {
						st.Mention.Meta = err.Error()
						st.Mention.Entries = nil
						return
					}
					st.Mention.Entries = result.Entries
					st.Mention.Index = 0
					st.Mention.Meta = fmt.Sprintf("%d/%d · %s · %dms", result.Count, result.Total, result.Source, result.BuildDuration)
				})
			}()
		})
	})
}

func (a *desktop) applyMention(t *conversation) {
	st := t.uiState()
	if st.Mention.Index < 0 || st.Mention.Index >= len(st.Mention.Entries) {
		return
	}
	entry := st.Mention.Entries[st.Mention.Index]
	path := entry.Path
	if entry.Dir {
		path += "/"
	}
	replacement := "@" + path
	if strings.ContainsAny(path, " \"\t@") {
		replacement = "@\"" + path + "\""
	}
	at := strings.LastIndex(t.Draft, "@")
	if at >= 0 {
		t.Draft = t.Draft[:at] + replacement + " "
	}
	st.Mention.Open = false
}

// ---- Menus ---------------------------------------------------------------

type slashCommand struct {
	Label  string
	Desc   string
	Skill  string
	Action func()
}

func (a *desktop) builtinCommands(t *conversation) []slashCommand {
	return []slashCommand{
		{"new", "新建会话，清空上下文", "", func() { a.newSession() }},
		{"skills", "查看可用技能", "", func() { a.changePage("skills") }},
		{"sessions", "查看/切换历史会话", "", func() { t.uiState().SessionsOpen = true }},
		{"init", "分析项目并生成 AGENTS.md", "", func() { a.send(t, "请分析这个项目，生成或更新 AGENTS.md。") }},
		{"note", "保存长期记忆", "", func() { a.send(t, projectMemoryPrompt) }},
		{"lesson", "总结易踩坑并更新 LESSONS.md", "", func() { a.send(t, "请总结这次对话里容易踩坑的地方，并更新 LESSONS.md。") }},
		{"review", "审查当前改动", "", func() { a.send(t, "请审查当前工作区最近的改动，指出真实缺陷与回归风险。") }},
		{"compact", "压缩上下文（调用模型总结）", "", func() { a.compact(t, false) }},
		{"compact-fast", "本地裁剪上下文，不调模型", "", func() { a.compact(t, true) }},
		{"push", "提交并推送", "", func() { a.send(t, "请检查改动并提交，然后推送到远端。") }},
	}
}

func (a *desktop) allCommands(t *conversation) []slashCommand {
	cmds := a.builtinCommands(t)
	for _, name := range a.enabledSkillNames() {
		desc := ""
		for _, s := range a.skills {
			if s.Name == name {
				desc = s.Description
				if desc == "" {
					desc = s.WhenToUse
				}
				break
			}
		}
		cmds = append(cmds, slashCommand{Label: name, Desc: desc, Skill: name})
	}
	return cmds
}

func (a *desktop) filteredCommands(t *conversation) []slashCommand {
	token := strings.TrimPrefix(strings.TrimSpace(t.Draft), "/")
	token = strings.SplitN(token, " ", 2)[0]
	var out []slashCommand
	for _, cmd := range a.allCommands(t) {
		if token == "" || strings.HasPrefix(strings.ToLower(cmd.Label), strings.ToLower(token)) {
			out = append(out, cmd)
		}
	}
	return out
}

func (a *desktop) selectedCommand(t *conversation) *slashCommand {
	st := t.uiState()
	items := a.filteredCommands(t)
	if st.CmdIndex < 0 || st.CmdIndex >= len(items) {
		return nil
	}
	return &items[st.CmdIndex]
}

func (a *desktop) completeCommand(t *conversation) {
	if cmd := a.selectedCommand(t); cmd != nil {
		t.Draft = "/" + cmd.Label + " "
		t.uiState().CmdOpen = false
	}
}

func (a *desktop) runCommand(t *conversation, cmd *slashCommand) {
	st := t.uiState()
	st.CmdOpen = false
	t.Draft = ""
	if cmd.Action != nil {
		cmd.Action()
		return
	}
	if cmd.Skill != "" {
		a.send(t, "/"+cmd.Skill+" ")
	}
}

func (a *desktop) commandMenu(c *ui.Context, t *conversation) {
	items := a.filteredCommands(t)
	st := t.uiState()
	builtin, skills := 0, 0
	for _, item := range items {
		if item.Skill != "" {
			skills++
		} else {
			builtin++
		}
	}
	p := a.currentPalette()
	ui.Column(c).FillWidth().MaxHeight(260).Border(1, c.Theme().Border).Background(p.panel).Radius(radiusCard).Children(func() {
		ui.Row(c).Padding(6, 10).Gap(gapField).Children(func() {
			ui.Text(c, fmt.Sprintf("指令 %d · 技能 %d", builtin, skills)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
			ui.Spacer(c)
			fieldNote(c, "↑↓ 选择 · Enter 执行 · Tab 补全")
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(0, 4, 4).Gap(1).Children(func() {
				if len(items) == 0 {
					fieldNote(c, "无匹配指令")
				}
				for i, item := range items {
					i, item := i, item
					row := ui.Row(c.Key(fmt.Sprintf("cmd-%s-%d", item.Label, i))).Height(26).Padding(0, 10).Gap(gapField).Radius(radiusSmall).AlignItems(ui.Center)
					if i == st.CmdIndex {
						row.Background(c.Theme().SurfaceHover)
					}
					row.Children(func() {
						ui.Text(c, "/"+item.Label).Font(monoFamily).FontSize(fsSmall).TextColor(p.accent)
						ui.Text(c, short(item.Desc, 70)).FontSize(fsTiny).TextColor(c.Theme().TextMuted).SingleLine()
						ui.Spacer(c)
					})
					if row.Clicked() {
						a.runCommand(t, &item)
					}
				}
			})
		})
	})
}

func (a *desktop) mentionMenu(c *ui.Context, t *conversation) {
	st := t.uiState()
	p := a.currentPalette()
	ui.Column(c).FillWidth().MaxHeight(240).Border(1, c.Theme().Border).Background(p.panel).Radius(radiusCard).Children(func() {
		ui.Row(c).Padding(6, 10).Children(func() {
			ui.Text(c, "@ 文件和目录").FontSize(fsTiny).TextColor(c.Theme().TextMuted)
			ui.Spacer(c)
			ui.Text(c, st.Mention.Meta).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(0, 4, 4).Gap(1).Children(func() {
				if st.Mention.Loading && len(st.Mention.Entries) == 0 {
					busyRow(c, "索引中…")
				} else if len(st.Mention.Entries) == 0 {
					fieldNote(c, "没有匹配路径")
				}
				for i, entry := range st.Mention.Entries {
					i, entry := i, entry
					mark := "."
					if entry.Dir {
						mark = "/"
					}
					row := ui.Row(c.Key(fmt.Sprintf("mention-%d-%s", i, entry.Path))).Height(24).Padding(0, 10).Gap(gapField).Radius(radiusSmall).AlignItems(ui.Center)
					if i == st.Mention.Index {
						row.Background(c.Theme().SurfaceHover)
					}
					row.Children(func() {
						ui.Text(c, mark).Font(monoFamily).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
						ui.Text(c, entry.Path).Font(monoFamily).FontSize(fsSmall).SingleLine()
					})
					if row.Clicked() {
						st.Mention.Index = i
						a.applyMention(t)
					}
				}
			})
		})
	})
}

func (a *desktop) sessionsMenu(c *ui.Context, t *conversation) {
	st := t.uiState()
	sessions := a.workspaceSessions(t.Workspace)
	p := a.currentPalette()
	ui.Column(c).FillWidth().MaxHeight(260).Border(1, c.Theme().Border).Background(p.panel).Radius(radiusCard).Children(func() {
		ui.Row(c).Padding(6, 10).Children(func() {
			ui.Text(c, fmt.Sprintf("会话 (%d)", len(sessions))).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
			ui.Spacer(c)
			iconAction(c, "sess-close", "关闭", "close", 20, func() { st.SessionsOpen = false })
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(0, 4, 4).Gap(1).Children(func() {
				if len(sessions) == 0 {
					fieldNote(c, "当前工作区暂无历史会话")
					other := 0
					for _, s := range a.sessions {
						if !sameWorkspace(s.Workspace, t.Workspace) {
							other++
						}
					}
					if other > 0 {
						fieldNote(c, fmt.Sprintf("其他工作区还有 %d 个（切换工作区后可见）", other))
					}
				}
				for i, s := range sessions {
					i, s := i, s
					busy := sessionBusyID(a, s.ID) != ""
					row := ui.Row(c.Key("sess-"+s.ID)).Height(28).Padding(0, 10).Gap(gapField).Radius(radiusSmall).AlignItems(ui.Center)
					if i == st.SessionsIndex {
						row.Background(c.Theme().SurfaceHover)
					}
					row.Children(func() {
						ui.Text(c, fmt.Sprint(i+1)).Font(monoFamily).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
						ui.Text(c, fmtTime(s.UpdatedAt)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
						ui.Text(c, fmt.Sprintf("%d条", s.MessageCount)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
						title := s.Title
						if title == "" {
							title = "会话"
						}
						ui.Text(c, short(title, 50)).FontSize(fsSmall).SingleLine()
						if s.ID == t.ID {
							accentChip(c, "当前")
						}
						if busy {
							ui.Box(c).Size(5, 5).Radius(3).Background(p.accent)
						}
						ui.Spacer(c)
						iconAction(c, "sess-del-"+s.ID, "删除会话", "close", 18, func() {
							a.deleteSession(s.ID, title)
						}).Disabled(busy)
					})
					if row.Clicked() {
						st.SessionsOpen = false
						a.openSession(s.ID)
					}
				}
			})
		})
	})
}

// ---- Status, attachments, info bar --------------------------------------

func (a *desktop) composerStatusRow(c *ui.Context, t *conversation) {
	p := a.currentPalette()
	ui.Row(c).Height(18).Gap(gapField).AlignItems(ui.Center).Children(func() {
		if t.Busy {
			a.runSpinner(c)
			label := ""
			switch {
			case strings.Contains(t.Status, "MCP"):
				label = t.Status
			case strings.Contains(t.Status, "思考"):
				label = "Thinking"
			case strings.HasPrefix(t.Status, "正在使用"):
				label = t.Status
			}
			if label != "" {
				ui.Text(c, label).FontSize(fsTiny).TextColor(c.Theme().TextMuted).SingleLine()
			}
		} else if a.status != "" && a.status != "就绪" && a.status != "操作完成" {
			ui.Text(c, short(a.status, 90)).FontSize(fsTiny).TextColor(c.Theme().TextMuted).Tooltip(a.status)
		}
		ui.Spacer(c)
		if note := t.uiState().CompactNote; note != "" {
			ui.Text(c, note).FontSize(fsTiny).TextColor(p.accent)
		} else if prompt := lastUserPrompt(t); prompt != "" {
			ui.Text(c, short(prompt, 60)).FontSize(fsTiny).TextColor(c.Theme().TextMuted).Tooltip(prompt)
		}
	})
}

func lastUserPrompt(t *conversation) string {
	for i := len(t.Messages) - 1; i >= 0; i-- {
		if str(t.Messages[i], "role") == "user" {
			return firstLine(str(t.Messages[i], "content"))
		}
	}
	return ""
}

// runSpinner is the three-bar activity mark used by the original composer.
func (a *desktop) runSpinner(c *ui.Context) {
	now := c.Now()
	box := ui.Box(c).Width(26).Height(16).Center()
	box.Children(func() {
		ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
			for i := 0; i < 3; i++ {
				phase := float64(now.UnixMilli())/1300 + float64(i)*0.16
				frac := phase - math.Floor(phase)
				h := float32(6 + 9*math.Abs(math.Sin(frac*6.283)))
				ui.Box(c).Width(3).Height(h).Radius(1).Background(c.Theme().TextMuted)
			}
		})
	})
	c.AnimationFrame()
}

func (a *desktop) retryBanner(c *ui.Context, info *retryInfo) {
	p := a.currentPalette()
	ui.Row(c).FillWidth().Padding(5, 10).Gap(gapField).Radius(radiusSmall).
		Background(ui.RGBA(217, 164, 65, 0.10)).BorderWidth(0, 0, 0, 3).BorderColor(colWarning()).Children(func() {
		iconGlyph(c, "refresh", 13).TextColor(colWarning())
		msg := fmt.Sprintf("重试中 %d/%d：%s", info.Attempt, info.MaxAttempts, info.Error)
		ui.Text(c, short(msg, 120)).FontSize(fsTiny).TextColor(colWarning()).SingleLine().Tooltip(msg)
		ui.Spacer(c)
		if info.TotalKeys > 1 {
			ui.Text(c, fmt.Sprintf("第 %d/%d 个 Key 失败", info.KeyIndex+1, info.TotalKeys)).FontSize(fsTiny).TextColor(p.muted)
		}
	})
}

func (a *desktop) kbActionBar(c *ui.Context, t *conversation) {
	running := t.Busy
	if a.kbIndexMissing() {
		flat(c, "初始化知识库").Disabled(running).OnClick(func() { a.send(t, "请创建知识库根索引 index.md 与分类结构。") })
		fieldNote(c, "请先初始化知识库")
		return
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		flat(c, "资料入库").Disabled(running).OnClick(func() { a.send(t, "请把 sources/ 里新增的资料整理入库，并更新索引。") })
		flat(c, "整理索引").Disabled(running).OnClick(func() { a.send(t, "请检查并整理知识库索引，修正过期或重复的条目。") })
		if running {
			fieldNote(c, "知识库任务运行中…")
		}
	})
}

// kbIndexMissing reports whether the configured KB root has no index.md.
// The explorer listing is the cheap signal; an empty listing falls back to
// false so the input is never disabled for an unknown state.
func (a *desktop) kbIndexMissing() bool {
	if strings.TrimSpace(a.config.KBRoot) == "" {
		return true
	}
	if len(a.files) == 0 {
		return false
	}
	for _, entry := range a.files {
		if strings.EqualFold(entry.Name, "index.md") {
			return false
		}
	}
	return true
}

func (a *desktop) composerInfoBar(c *ui.Context, t *conversation) {
	st := t.uiState()
	p := a.currentPalette()
	ui.Row(c).Gap(gapTight).AlignItems(ui.Center).Wrap().Children(func() {
		iconAction(c, "composer-new", "新建会话", "plus", 26, func() { a.newSession() }).TextColor(p.accent)
		iconAction(c, "composer-sessions", "查看和切换历史会话", "menu", 26, func() {
			st.SessionsOpen = !st.SessionsOpen
			if st.SessionsOpen {
				st.SessionsIndex = 0
			}
		}).TextColor(p.muted)
		a.modelSelector(c, t)
		a.reasoningSelector(c, t)
		flat(c, short(workspaceLabel(t.Workspace), 22)).Tooltip("在文件管理器中打开工作区：" + t.Workspace).OnClick(func() {
			if t.Workspace != "" {
				a.job(func() error { return a.core.OpenPathInFileManager(t.Workspace) })
			}
		})
		a.extraRootsButton(c, t)
		iconAction(c, "composer-terminal", "在终端中打开工作区", "terminal", 26, func() {
			req := backend.WorkspacePathRequest{Workspace: t.Workspace}
			a.job(func() error { return a.core.OpenWorkspaceTerminalAt(req) })
		}).TextColor(p.muted)
		a.gitButton(c, t)
		ui.Spacer(c)
		running := 0
		for _, s := range a.services {
			if s.Status == "running" {
				running++
			}
		}
		footerBadge(c, "footer-services", "S", fmt.Sprint(running), ui.Hex("#6486d8"), fmt.Sprintf("后台服务 · 运行中 %d", running), func() {
			a.tasksF.Tab = 0
			a.taskPopup = true
			a.loadTaskCenter()
		})
		tasksRunning := 0
		for _, task := range a.tasks {
			if task.Running {
				tasksRunning++
			}
		}
		footerBadge(c, "footer-scheduled", "C", fmt.Sprint(tasksRunning), ui.Hex("#b49143"), fmt.Sprintf("定时任务 · 运行中 %d", tasksRunning), func() {
			a.tasksF.Tab = 1
			a.taskPopup = true
			a.loadTaskCenter()
		})
		a.sshChip(c, t)
		a.contextUsage(c, t)
	})
}

func (a *desktop) modelSelector(c *ui.Context, t *conversation) {
	label := t.Model
	if label == "" {
		label = "选择模型"
	}
	btn := flat(c, short(strings.ReplaceAll(label, " / ", " · "), 34)).Tooltip("切换模型")
	if t.Busy {
		btn.Tooltip("运行中锁定：本轮结束后切换才生效").Disabled(true)
	}
	st := t.uiState()
	btn.OnClick(func() {
		st.ModelOpen = !st.ModelOpen
		if st.ModelOpen {
			st.ModelSearch = ""
		}
	})
	popoverPanel(c, btn, &st.ModelOpen, 340, func() {
		ui.TextInput(c.Key("model-menu-search"), &st.ModelSearch).Placeholder("搜索模型或供应商").Label("搜索模型或供应商").FillWidth()
		query := strings.ToLower(strings.TrimSpace(st.ModelSearch))
		matches := func(model backend.ModelConfig) bool {
			return query == "" || strings.Contains(strings.ToLower(model.ProviderName+" "+model.DisplayName+" "+model.Model), query)
		}
		ui.Scroll(c).MaxHeight(300).Children(func() {
			ui.Column(c).Children(func() {
				recent := append([]string(nil), a.prefs.RecentModels...)
				if len(recent) == 0 && a.config.LastUsedModel != nil {
					for _, model := range a.config.Models {
						if sameModelIdentity(modelIdentity(model), *a.config.LastUsedModel) {
							recent = append(recent, modelOption(model))
							break
						}
					}
				}
				if len(recent) == 0 && t.Model != "" {
					recent = append(recent, t.Model)
				}
				shownRecent := false
				for _, identity := range recent {
					for i, model := range a.config.Models {
						if modelOption(model) != identity || !matches(model) {
							continue
						}
						if !shownRecent {
							ui.Text(c, "最近使用").FontSize(fsTiny).TextColor(c.Theme().TextMuted).Padding(6, 10)
							shownRecent = true
						}
						model := model
						name := model.DisplayName
						if name == "" {
							name = model.Model
						}
						popoverItem(c, fmt.Sprintf("model-recent-%d", i), name, model.ProviderName, modelOption(model) == t.Model, func() { st.ModelOpen = false; a.selectChatModel(t, model) })
					}
				}
				groups := map[string][]int{}
				for i, model := range a.config.Models {
					if matches(model) {
						provider := strings.TrimSpace(model.ProviderName)
						if provider == "" {
							provider = "OpenAI Compatible"
						}
						groups[provider] = append(groups[provider], i)
					}
				}
				names := make([]string, 0, len(groups))
				for name := range groups {
					names = append(names, name)
				}
				sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
				for _, provider := range names {
					ui.Text(c, provider).FontSize(fsTiny).TextColor(c.Theme().TextMuted).Padding(8, 10, 2)
					indices := groups[provider]
					sort.SliceStable(indices, func(i, j int) bool {
						return strings.ToLower(a.config.Models[indices[i]].Model) < strings.ToLower(a.config.Models[indices[j]].Model)
					})
					for _, i := range indices {
						model := a.config.Models[i]
						name := model.DisplayName
						if name == "" {
							name = model.Model
						}
						popoverItem(c, fmt.Sprintf("model-choice-%d", i), name, "", modelOption(model) == t.Model, func() { st.ModelOpen = false; a.selectChatModel(t, model) })
					}
				}
				if len(groups) == 0 {
					fieldNote(c, "没有匹配的模型")
				}
			})
		})
		divider(c)
		popoverItem(c, "model-manage", "管理模型", "", false, func() { st.ModelOpen = false; a.changePage("models") })
	})
}

func (a *desktop) selectChatModel(t *conversation, model backend.ModelConfig) {
	t.Model = modelOption(model)
	t.Reasoning = model.ReasoningEffort
	if t.Reasoning == "" {
		t.Reasoning = "auto"
	}
	id := modelIdentity(model)
	a.config.LastUsedModel = &id
	recent := []string{t.Model}
	for _, old := range a.prefs.RecentModels {
		if old != t.Model && len(recent) < 8 {
			recent = append(recent, old)
		}
	}
	a.prefs.RecentModels = recent
	a.savePrefs()
	go func() {
		err := a.core.SelectNativeModel(id)
		a.win.Update(func() {
			if err != nil {
				a.notify(err.Error())
			}
		})
	}()
}

func (a *desktop) reasoningSelector(c *ui.Context, t *conversation) {
	levels := []string{"auto", "off", "low", "medium", "high", "xhigh", "max"}
	label := t.Reasoning
	if label == "" {
		label = "auto"
	}
	btn := flat(c, label+" ▾").Tooltip("思考强度（需模型支持，不支持的模型请保持 auto）")
	if t.Busy {
		btn.Disabled(true).Tooltip("运行中锁定：本轮结束后切换才生效")
	}
	st := t.uiState()
	btn.OnClick(func() { st.EffortOpen = !st.EffortOpen })
	popoverPanel(c, btn, &st.EffortOpen, 200, func() {
		ui.Text(c, "思考强度").FontSize(fsSection).Bold().Padding(6, 10)
		for _, level := range levels {
			level := level
			popoverItem(c, "effort-"+level, level, "", level == t.Reasoning, func() { t.Reasoning = level; st.EffortOpen = false })
		}
	})
}

func (a *desktop) extraRootsButton(c *ui.Context, t *conversation) {
	if t.Workspace == "" {
		return
	}
	roots := a.extraRoots[t.ID]
	label := "附加目录"
	if len(roots) > 0 {
		label = fmt.Sprintf("附加目录 %d", len(roots))
	}
	btn := flat(c, label).Tooltip("会话级附加工作区。添加后允许写入其他工作区。")
	st := t.uiState()
	btn.OnClick(func() { st.ExtrasOpen = !st.ExtrasOpen })
	popoverPanel(c, btn, &st.ExtrasOpen, 320, func() {
		ui.Text(c, "附加目录").FontSize(fsSection).Bold().Padding(6, 10)
		if len(roots) == 0 {
			ui.Text(c, "尚未添加附加工作区").FontSize(fsSmall).TextColor(c.Theme().TextMuted).Padding(8, 10)
		}
		ui.Scroll(c).MaxHeight(200).Children(func() {
			ui.Column(c).Children(func() {
				for i, root := range roots {
					root := root
					popoverItem(c, fmt.Sprintf("extra-root-%d", i), filepath.Base(root), root, false, func() { st.ExtrasOpen = false; a.removeExtraRoot(t, root) })
				}
			})
		})
		divider(c)
		popoverItem(c, "extra-add", "＋ 添加目录", "", false, func() { st.ExtrasOpen = false; a.addExtraRoot(t) })
	})
}

func (a *desktop) addExtraRoot(t *conversation) {
	go func() {
		p, err := a.core.SelectDirectory("")
		if err != nil {
			a.fail(err)
			return
		}
		if p == "" {
			return
		}
		a.win.Update(func() {
			if sameWorkspace(p, t.Workspace) {
				a.status = "该目录就是当前主工作区"
				return
			}
			for _, existing := range a.extraRoots[t.ID] {
				if sameWorkspace(existing, p) {
					a.status = "该目录已在附加工作区中"
					return
				}
			}
			if a.extraRoots == nil {
				a.extraRoots = map[string][]string{}
			}
			a.extraRoots[t.ID] = append(a.extraRoots[t.ID], p)
			roots := append([]string(nil), a.extraRoots[t.ID]...)
			a.job(func() error { return a.core.SetSessionExtraRoots(t.ID, roots) })
		})
	}()
}

func (a *desktop) removeExtraRoot(t *conversation, root string) {
	var kept []string
	for _, item := range a.extraRoots[t.ID] {
		if !sameWorkspace(item, root) {
			kept = append(kept, item)
		}
	}
	a.extraRoots[t.ID] = kept
	a.job(func() error { return a.core.SetSessionExtraRoots(t.ID, kept) })
}

func (a *desktop) sshChip(c *ui.Context, t *conversation) {
	if t.Workspace == "" {
		return
	}
	count := len(a.sshAllowed[t.Workspace])
	footerBadge(c, "footer-ssh", "SSH", fmt.Sprint(count), ui.Hex("#3b9f70"), "SSH 集群授权", func() {
		a.sshF.AuthOpen = true
		a.loadSSHAuth(t.Workspace)
	})
}

func (a *desktop) gitButton(c *ui.Context, t *conversation) {
	st := t.uiState()
	if !st.GitLoaded {
		muted(c, "…", fsTiny)
		return
	}
	git := st.Git
	if !git.IsRepo && !git.IsMultiRepo {
		return
	}
	label := git.Branch
	if git.IsMultiRepo {
		label = fmt.Sprintf("%d repos", len(git.Repos))
	}
	if label == "" {
		label = "git"
	}
	btn := flat(c, short(label+gitStatsText(git), 46)).Tooltip("查看 Git 改动")
	open := a.popupFlag("git-repositories-" + t.ID)
	btn.OnClick(func() {
		if git.IsMultiRepo {
			*open = !*open
			return
		}
		a.filesF.GitOpen = true
		a.filesF.GitRepo = ""
		a.filesF.GitFile = -1
		a.loadGitDiff(t, "")
	})
	if git.IsMultiRepo {
		popoverPanel(c, btn, open, 320, func() {
			ui.Scroll(c).MaxHeight(240).Children(func() {
				ui.Column(c).Children(func() {
					for i, repo := range git.Repos {
						repo := repo
						stats := fmt.Sprintf("↑%d ↓%d +%d ~%d -%d", repo.Ahead, repo.Behind, repo.Added, repo.Modified, repo.Deleted)
						if repo.Ahead+repo.Behind+repo.Added+repo.Modified+repo.Deleted == 0 {
							stats = "clean"
						}
						popoverItem(c, fmt.Sprintf("git-repo-%d", i), repo.Name+"/  "+repo.Branch, stats, false, func() { *open = false; a.filesF.GitOpen = true; a.loadGitDiff(t, repo.Path) })
					}
				})
			})
		})
	}
}

func gitStatsText(git backend.GitStatus) string {
	var parts []string
	if git.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", git.Ahead))
	}
	if git.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", git.Behind))
	}
	if git.Added > 0 {
		parts = append(parts, fmt.Sprintf("+%d", git.Added))
	}
	if git.Modified > 0 {
		parts = append(parts, fmt.Sprintf("~%d", git.Modified))
	}
	if git.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("-%d", git.Deleted))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func (a *desktop) contextUsage(c *ui.Context, t *conversation) {
	st := t.uiState()
	window := 0
	for _, model := range a.config.Models {
		if modelOption(model) == t.Model {
			window = model.ContextWindow
			break
		}
	}
	used := st.Breakdown.Total
	if used == 0 {
		used = t.ContextTokens
	}
	label := "…"
	if st.FooterLoaded || used > 0 {
		label = fmt.Sprintf("%s (%s/%s)", pctOf(used, window), fmtK(used), fmtK(window))
	}
	color := ui.Hex("#69a982")
	if !c.Theme().Dark {
		color = ui.Hex("#287b4d")
	}
	ratio := 0.0
	if window > 0 {
		ratio = float64(used) / float64(window)
	}
	switch {
	case ratio > 0.4:
		color = colDanger()
	case ratio > 0.2:
		color = colWarning()
	}
	btn := flat(c, label).TextColor(color).Tooltip("上下文占用（点击查看明细）")
	if !st.FooterLoaded {
		btn.OnClick(func() { a.refreshSessionFooter(t) })
		return
	}
	btn.OnClick(func() { st.ContextOpen = !st.ContextOpen })
	popoverPanel(c, btn, &st.ContextOpen, 300, func() {
		b := st.Breakdown
		ui.Text(c, "上下文构成").FontSize(fsSection).Bold().Padding(6, 10)
		ui.Column(c).Padding(4, 10).Gap(6).Children(func() {
			contextPart(c, "系统提示词", b.SystemPrompt)
			for _, part := range b.SystemPromptParts {
				if part.Tokens > 0 {
					contextPart(c, "  · "+part.Label, part.Tokens)
				}
			}
			contextPart(c, "工具 Schema", b.ToolSchemas)
			contextPart(c, "用户提问", b.UserMessages)
			contextPart(c, "AI 回复", b.AssistantMsgs)
			contextPart(c, "工具结果", b.ToolResults)
			if b.Reasoning > 0 {
				contextPart(c, "思考内容", b.Reasoning)
			}
			ui.Text(c, fmt.Sprintf("累计输入 %s / 输出 %s", fmtK(st.Usage.InputTokens), fmtK(st.Usage.OutputTokens))).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
		})
		divider(c)
		popoverItem(c, "context-compact", "压缩上下文", "", false, func() {
			if !t.Busy {
				st.ContextOpen = false
				a.compact(t, false)
			}
		})
		popoverItem(c, "context-fast-compact", "快速压缩上下文", "", false, func() {
			if !t.Busy {
				st.ContextOpen = false
				a.compact(t, true)
			}
		})
		popoverItem(c, "context-lessons", "压缩教训", "", false, func() {
			if !t.Busy {
				st.ContextOpen = false
				a.send(t, "请压缩并总结这个项目的经验教训（LESSONS.md）。")
			}
		})
		popoverItem(c, "context-graph", "更新图谱", "", false, func() {
			if !t.Busy {
				st.ContextOpen = false
				a.send(t, "请更新 CODEGRAPH.md，让导航与当前代码一致。")
			}
		})
	})
}

func contextPart(c *ui.Context, name string, tokens int) {
	ui.Row(c).FillWidth().Children(func() {
		ui.Text(c, name).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
		ui.Spacer(c)
		ui.Text(c, fmtK(tokens)).FontSize(fsSmall)
	})
}

func (a *desktop) compact(t *conversation, fast bool) {
	if t.Busy {
		return
	}
	t.uiState().CompactBusy = true
	t.uiState().CompactNote = "正在压缩上下文…"
	a.job(func() error {
		var err error
		if fast {
			_, err = a.core.FastCompactSession(t.ID)
		} else {
			_, err = a.core.CompactSession(t.ID, "", a.config)
		}
		a.win.Update(func() {
			t.uiState().CompactBusy = false
			if err != nil {
				t.uiState().CompactNote = "压缩失败：" + err.Error()
				a.notify(t.uiState().CompactNote)
				return
			}
			t.uiState().CompactNote = "上下文压缩完成"
			a.notify("上下文压缩完成")
			a.refreshSessionFooter(t)
		})
		return err
	})
}

// composerSendRow carries the transient run hints under the input. The send
// button itself lives next to the input so it stays anchored to the cursor.
func (a *desktop) composerSendRow(c *ui.Context, t *conversation) {
	st := t.uiState()
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		if st.CompactBusy {
			ui.Spinner(c).Size(13, 13)
			muted(c, st.CompactNote, fsTiny)
		} else if st.CompactNote != "" {
			muted(c, st.CompactNote, fsTiny)
		}
		ui.Spacer(c)
		if t.AttachmentLoading {
			ui.Spinner(c).Size(13, 13)
			muted(c, "正在处理附件…", fsTiny)
		}
	})
}
