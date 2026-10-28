// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
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
		if st.CmdOpen {
			a.commandMenu(c, t)
		}
		if st.Mention.Open {
			a.mentionMenu(c, t)
		}
		if t.Busy || st.CompactBusy {
			a.composerStatusRow(c, t)
		}
		if len(t.PendingAttachments) > 0 {
			a.pendingAttachmentRow(c, t)
		}
		ui.Row(c.Key("composer-input-row")).Gap(gapField).AlignItems(ui.End).Children(func() {
			input := uikit.Area(c.Key(t.ID+"-draft"), &t.Draft).Label("消息输入框").
				Placeholder("输入消息，Enter 发送，Ctrl+Enter 换行，Esc 中断，/ 选择指令，@ 引用文件").
				MinHeight(58).MaxHeight(576).Lines(1, 24).Border(1, c.Theme().Border).Radius(radiusControl).
				Background(p.bg).Disabled(a.closing || (a.page == "kb" && a.kbIndexMissing())).Grow(1)
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
		if t.AttachmentLoading {
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

	// macOS delivers Cmd+V as the Edit menu's paste command rather than a key
	// the input sees, so an image paste is taken here as well as below.
	input.OnPaste(func(string) bool { return a.pasteImageFromClipboard(c, t) })

	selectionStart, selectionEnd := input.TextSelection()
	composing := input.Composing()
	if input.Changed() {
		if st.HistoryIndex >= 0 {
			history := inputHistory(t)
			if st.HistoryIndex >= len(history) || t.Draft != history[st.HistoryIndex] {
				st.HistoryIndex = -1
			}
		}
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
		if k.Key == ui.KeyV && k.Mods == ui.Cmd && !composing {
			// Windows and Linux report the paste as a key; macOS as the command
			// taken by OnPaste above.
			if a.pasteImageFromClipboard(c, t) {
				return true
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
		if !composing && k.Mods == 0 && (k.Key == ui.KeyUp || k.Key == ui.KeyDown) {
			if text, ok := navigateInputHistory(t, k.Text(), k.Key == ui.KeyUp); ok {
				k.SetText(text)
				return true
			}
		}
		if k.Key == ui.KeyEnter && k.Mods&ui.Shift == 0 && !composing {
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
	overlayPanel(c, "cmd-menu", 0, 360, func() {
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
					hoverRow(c, row, i == st.CmdIndex)
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
	overlayPanel(c, "mention-menu", 0, 240, func() {
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
					hoverRow(c, row, i == st.Mention.Index)
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
	panel := overlayPanel(c, "sessions-menu", 0, 360, func() {
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
					row := ui.Row(c.Key("sess-"+s.ID)).Height(28).Padding(0, padRow).Gap(gapField).Radius(radiusSmall).AlignItems(ui.Center)
					if row.Hovered() {
						st.SessionsIndex = i
					}
					row.Cursor(ui.CursorPointer)
					hoverRow(c, row, i == st.SessionsIndex)
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
	if panel.PressedOutside() {
		st.SessionsOpen = false
	}
}

// ---- Status, attachments, info bar --------------------------------------

func (a *desktop) composerStatusRow(c *ui.Context, t *conversation) {
	st := t.uiState()
	ui.Row(c).Height(18).Gap(gapField).AlignItems(ui.Center).Children(func() {
		switch {
		case st.CompactBusy:
			// A compaction owns this row exactly like a run does, with the same
			// spinner and the same thinking label once the summarising model thinks.
			ui.Spinner(c).Size(13, 13)
			note := st.CompactNote
			if note == "" {
				note = "正在压缩对话上下文…"
			}
			ui.Text(c, note).FontSize(fsTiny).TextColor(c.Theme().TextMuted).SingleLine().MinWidth(0)
			if count := compactThinkingTokens(st); count > 0 {
				ui.Text(c, fmt.Sprintf("Thinking %s tokens", fmtK(count))).FontSize(fsTiny).TextColor(c.Theme().TextMuted).SingleLine()
			}
		case t.Busy:
			a.runSpinner(c)
			count := (st.ThinkingBytes + 3) / 4
			if count > 0 {
				ui.Text(c, fmt.Sprintf("Thinking %s tokens", fmtK(count))).FontSize(13).TextColor(c.Theme().TextMuted).SingleLine()
			}
		}
		ui.Spacer(c)
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

// runSpinner mirrors main's RunSpinner: three 3px bars in a fixed 16px box,
// rest 9px, peak 16px, one wave per 1.3s travelling left->right. The keyframes
// are main's (rest at 0/80/100%, peak at 40%), neighbours staggered 208ms so
// the three phases never all freeze inside the still window together. The box
// height is frozen at the peak: only the bar scale moves, so the status row
// never grows or shifts.
//
// The bars are drawn rather than animated with Loop: Loop asks for a frame that
// rebuilds the whole view, so while the model ran this rebuilt every message at
// the display's rate and made open tooltips shimmer.
func (a *desktop) runSpinner(c *ui.Context) {
	const (
		cycle = 1300
		step  = 208
		rest  = float32(9.0 / 16)
	)
	color := c.Theme().TextMuted
	ui.Box(c).Width(15).Height(16).Draw(func(p *ui.Painter, r ui.Rect) {
		p.AnimationFrameRate(30)
		ms := float64(p.Now().UnixMilli() % cycle)
		for i := 0; i < 3; i++ {
			// Bar i starts partway through its cycle, leftmost furthest ahead,
			// so the wave enters mid-flight instead of jumping from rest.
			frac := math.Mod(ms/float64(cycle)+float64(2-i)*float64(step)/float64(cycle), 1)
			scale := rest
			if frac < 0.8 {
				// 0->40% rest climbs to peak, 40%->80% falls back; the cosine
				// eases both ends like the CSS keyframes.
				scale = rest + (1-rest)*float32(0.5-0.5*math.Cos(frac/0.8*2*math.Pi))
			}
			h := 16 * scale
			p.Fill(ui.Rect{X: r.X + float32(i)*6, Y: r.Y + (16-h)/2, W: 3, H: h}, color, 1)
		}
	})
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
		composerFlat(c, "初始化知识库").Disabled(running).OnClick(func() { a.send(t, "请创建知识库根索引 index.md 与分类结构。") })
		fieldNote(c, "请先初始化知识库")
		return
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		composerFlat(c, "资料入库").Disabled(running).OnClick(func() { a.send(t, "请把 sources/ 里新增的资料整理入库，并更新索引。") })
		composerFlat(c, "整理索引").Disabled(running).OnClick(func() { a.send(t, "请检查并整理知识库索引，修正过期或重复的条目。") })
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
	ui.Row(c).FillWidth().MinWidth(0).Gap(2).AlignItems(ui.Center).Children(func() {
		addButton(c, "composer-new", "新建会话", "plus", 26, func() { a.newSession() })
		iconAction(c, "composer-sessions", "查看和切换历史会话", "menu", 26, func() {
			st.SessionsOpen = !st.SessionsOpen
			if st.SessionsOpen {
				st.SessionsIndex = 0
			}
		}).TextColor(p.muted)
		a.modelSelector(c, t)
		a.reasoningSelector(c, t)
		a.workspaceButton(c, t)
		a.extraRootsButton(c, t)
		iconAction(c, "composer-terminal", "在终端中打开工作区", "terminal", 26, func() {
			req := backend.WorkspacePathRequest{Workspace: t.Workspace}
			a.job(func() error { return a.core.OpenWorkspaceTerminalAt(req) })
		}).TextColor(p.muted)
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
		a.gitButton(c, t)
		a.contextUsage(c, t)
	})
}

func (a *desktop) modelSelector(c *ui.Context, t *conversation) {
	label := t.Model
	if label == "" {
		label = "选择模型"
	}
	btn := composerFlat(c, short(strings.ReplaceAll(label, " / ", " · "), 34)).SingleLine().MinWidth(0).Shrink(1).Tooltip("切换模型")
	if t.Busy {
		btn.Tooltip("运行中锁定：本轮结束后切换才生效").Disabled(true)
	}
	st := t.uiState()
	btn.OnClick(func() {
		st.ModelOpen = !st.ModelOpen
		if st.ModelOpen {
			st.ModelSearch = ""
			st.ModelGroup = ""
		}
	})
	ui.PopoverBase(c, btn, &st.ModelOpen, func(panel ui.Element) {
		popoverChrome(c, panel, 340)
		st.FlyoutHot = false
		ui.Column(c).FillWidth().Children(func() {
			uikit.Input(c.Key("model-menu-search"), &st.ModelSearch).Placeholder("搜索模型或供应商").Label("搜索模型或供应商").FillWidth()
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
					if shownRecent {
						divider(c)
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
						indices := groups[provider]
						sort.SliceStable(indices, func(i, j int) bool {
							return strings.ToLower(a.config.Models[indices[i]].Model) < strings.ToLower(a.config.Models[indices[j]].Model)
						})
						a.modelGroupRow(c, t, st, provider, indices)
					}
					if len(groups) == 0 {
						fieldNote(c, "没有匹配的模型")
					}
				})
			})
			divider(c)
			popoverItem(c, "model-manage", "管理模型", "", false, func() { st.ModelOpen = false; a.changePage("models") })
		})
		// 分组浮层可能盖住本面板右缘，指针在面板或浮层任一处都保持展开；
		// 两处都离开才收起，避免指针穿过缝隙时浮层闪退。
		if !panel.Hovered() && !st.FlyoutHot {
			st.ModelGroup = ""
		}
	})
}

// modelGroupRow is one provider in the model menu. Hovering it opens a flyout
// on its right with that provider's models, so the menu lists groups only.
func (a *desktop) modelGroupRow(c *ui.Context, t *conversation, st *chatUI, provider string, indices []int) {
	th := c.Theme()
	row := ui.ButtonBase(c.Key("model-group-"+provider)).FillWidth().MinHeight(32).Padding(6, 10).Justify(ui.Start).Radius(radiusSmall).Cursor(ui.CursorPointer).Label(provider)
	open := st.ModelGroup == provider
	if row.Hovered() {
		st.ModelGroup = provider
		open = true
	}
	hasActive := false
	for _, i := range indices {
		if modelOption(a.config.Models[i]) == t.Model {
			hasActive = true
		}
	}
	if open {
		row.Background(th.SurfaceHover)
	}
	row.Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			name := ui.Text(c, provider).FontSize(fsSmall).SingleLine().Grow(1)
			if hasActive {
				name.TextColor(th.Accent)
			}
			ui.Text(c, fmt.Sprint(len(indices))).FontSize(fsTiny).TextColor(th.TextMuted)
			iconGlyph(c, "chevron", 10).TextColor(th.TextMuted)
		})
	})
	ui.PopoverBase(c.Key("model-flyout-"+provider), row, &open, func(panel ui.Element) {
		panel.AttachTo(row, ui.AnchorTopEnd, ui.AnchorTopStart).MarginStart(-6)
		popoverChrome(c, panel, 260)
		if panel.Hovered() {
			st.FlyoutHot = true
		}
		ui.Scroll(c).MaxHeight(360).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, i := range indices {
					model := a.config.Models[i]
					name := model.DisplayName
					if name == "" {
						name = model.Model
					}
					popoverItem(c, fmt.Sprintf("model-choice-%d", i), name, "", modelOption(model) == t.Model, func() { st.ModelOpen = false; a.selectChatModel(t, model) })
				}
			})
		})
	})
	if !open && st.ModelGroup == provider {
		st.ModelGroup = ""
	}
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

// reasoningSelector reports and sets the thinking level. The levels are a plain
// list of values, so the system's own menu carries them: it shows the current
// one with its radio mark and keys itself, without a popover of our own.
func (a *desktop) reasoningSelector(c *ui.Context, t *conversation) {
	levels := []string{"auto", "off", "low", "medium", "high", "xhigh", "max"}
	label := t.Reasoning
	if label == "" {
		label = "auto"
	}
	btn := composerFlat(c, label+" ▾").Tooltip("思考强度（需模型支持，不支持的模型请保持 auto）")
	if t.Busy {
		btn.Disabled(true).Tooltip("运行中锁定：本轮结束后切换才生效")
	}
	btn.OnClick(func() {
		items := make([]*mygo.MenuItem, 0, len(levels))
		for _, level := range levels {
			level := level
			items = append(items, menuItem(level, level == label, func() {
				t.Reasoning = level
				a.win.Update(func() {})
			}))
		}
		a.popupMenu(items)
	})
}

// workspaceButton is the workspace entry of the info bar: the folder glyph on
// its own, the same 26px box and 16px glyph as the icons beside it, with the
// workspace and its path in the tip. The name already shows in the header tab.
func (a *desktop) workspaceButton(c *ui.Context, t *conversation) {
	tip := "在文件管理器中打开工作区：" + t.Workspace
	if t.Workspace == "" {
		tip = "尚未选择工作区"
	}
	iconAction(c, "composer-workspace", tip, "folderopen", 26, func() {
		if t.Workspace != "" {
			a.job(func() error { return a.core.OpenPathInFileManager(t.Workspace) })
		}
	}).TextColor(a.currentPalette().muted)
}

// extraRootsButton mirrors main's extra-roots entry: a glyph button with the
// number of attached roots beside it, in the accent colour once any is set.
func (a *desktop) extraRootsButton(c *ui.Context, t *conversation) {
	if t.Workspace == "" {
		return
	}
	p := a.currentPalette()
	roots := a.extraRoots[t.ID]
	count := len(roots)
	tip := "附加目录：会话级附加工作区，添加后允许写入其他工作区。"
	if count > 0 {
		tip = fmt.Sprintf("附加目录（%d 个）：添加后允许写入其他工作区。", count)
	}
	color := p.muted
	if count > 0 {
		color = p.accent
	}
	btn := ui.ButtonBase(c.Key("composer-extra-roots")).Height(26).MinWidth(26).Padding(0, 3).Gap(2).
		Radius(radiusSmall).AlignItems(ui.Center).Center().Label("附加目录").Tooltip(tip).
		TextColor(color).Cursor(ui.CursorPointer)
	if btn.Hovered() {
		btn.Background(fadeColor(btn, "fill", p.hover))
	}
	btn.OnClick(func() {
		// The attached roots are a plain list with an add action, so the system's
		// own menu carries them; the path of each one is its tooltip, which a
		// menu shows on hover and a nested column of text could not.
		items := make([]*mygo.MenuItem, 0, len(roots)+2)
		if len(roots) == 0 {
			items = append(items, &mygo.MenuItem{Label: "尚未添加附加工作区", Disabled: true})
		}
		for _, root := range roots {
			root := root
			items = append(items, &mygo.MenuItem{
				Label:   "📁 " + filepath.Base(root),
				ToolTip: root,
				Click:   func(*mygo.MenuItem, *mygo.Window) { a.removeExtraRoot(t, root) },
			})
		}
		items = append(items, menuSeparator(), menuAction("＋ 添加目录", func() { a.addExtraRoot(t) }))
		a.popupMenu(items)
	})
	btn.Children(func() {
		iconGlyph(c, "grid", 16).TextColor(color)
		if count > 0 {
			ui.Text(c, fmt.Sprint(count)).FontSize(fsTiny).TextColor(color)
		}
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
	btn := ui.ButtonBase(c.Key("footer-git")).Height(22).Padding(1, 7).Radius(11).Border(1, c.Theme().Border).Background(c.Theme().Surface).Cursor(ui.CursorPointer).Tooltip("查看 Git 改动")
	btn.Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, short(label, 22)).FontSize(fsTiny).TextColor(c.Theme().Accent).SingleLine()
			for _, part := range []struct {
				value  int
				symbol string
				color  ui.Color
			}{{git.Ahead, "↑", c.Theme().TextMuted}, {git.Behind, "↓", c.Theme().TextMuted}, {git.Added, "+", colSuccess()}, {git.Modified, "~", colWarning()}, {git.Deleted, "-", colDanger()}} {
				if part.value > 0 {
					ui.Text(c, fmt.Sprintf("%s%d", part.symbol, part.value)).FontSize(fsTiny).TextColor(part.color).NoWrap()
				}
			}
		})
	})
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
	btn, labelEl := composerFlatParts(c, label)
	// The colour goes on the label itself: a TextColor on the button never reaches
	// it, which is why the meter used to sit at one muted grey whatever the ratio.
	labelEl.TextColor(fadeColor(labelEl, "context-tone", color))
	btn = btn.SingleLine().Shrink(0).NoWrap().Tooltip("上下文占用（点击查看明细）")
	if !st.FooterLoaded {
		btn.OnClick(func() { a.refreshSessionFooter(t) })
		return
	}
	btn.OnClick(func() { st.ContextOpen = !st.ContextOpen })
	ui.PopoverBase(c, btn, &st.ContextOpen, func(panel ui.Element) {
		popoverChrome(c, panel, 300)
		panel.Column().Label("上下文占用明细").AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight).Left(-12)
		b := st.Breakdown
		ui.Text(c, "上下文构成").FontSize(fsSection).Bold().Padding(6, 10)
		// The breakdown scrolls so the panel stays inside its height limit; the
		// buttons below keep their room instead of spilling out of the panel.
		ui.Scroll(c).MaxHeight(280).Children(func() {
			ui.Column(c).FillWidth().Padding(4, 10).Gap(6).Children(func() {
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
		})
		divider(c)
		ui.Column(c).FillWidth().Shrink(0).Padding(4, 10, 6).Gap(8).Children(func() {
			// Two rows of two buttons.
			ui.Row(c).FillWidth().Shrink(0).Gap(8).Children(func() {
				contextCompactButton(c, "context-compact", "压缩上下文", true, t.Busy || st.CompactBusy, "压缩对话上下文，用摘要替换历史消息", func() {
					st.ContextOpen = false
					a.compact(t, false)
				})
				contextCompactButton(c, "context-fast-compact", "压缩上下文(快速)", true, t.Busy || st.CompactBusy, "本地裁剪旧回复、工具结果与思考，保留最近内容，不调用模型", func() {
					st.ContextOpen = false
					a.compact(t, true)
				})
			})
			ui.Row(c).FillWidth().Shrink(0).Gap(8).Children(func() {
				contextCompactButton(c, "context-lessons", "压缩教训", false, t.Busy, "压缩并总结项目经验教训", func() {
					st.ContextOpen = false
					a.send(t, "请压缩并总结这个项目的经验教训（LESSONS.md）。")
				})
				contextCompactButton(c, "context-graph", "更新图谱", false, t.Busy, "更新 CODEGRAPH.md 导航", func() {
					st.ContextOpen = false
					a.send(t, "请更新 CODEGRAPH.md，让导航与当前代码一致。")
				})
			})
		})
	})
}

func contextCompactButton(c *ui.Context, key, label string, primary, disabled bool, tip string, action func()) {
	// 对齐 main 的 .context-compact-btn：透明底 + 1px 边框，字号 12，前两个主操作走
	// 主题高亮色；圆角与边框透明度跟其它按钮同一套 token。
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Grow(1).Basis(0).MinWidth(0).Height(28).Padding(2, 4).Radius(radiusControl).
		Border(1, surfaceBorder(c)).Center().Label(label).Tooltip(tip).Cursor(ui.CursorPointer).Disabled(disabled)
	color := t.TextMuted
	if primary {
		color = t.Accent
	}
	fill := ui.Transparent
	if disabled {
		b.Opacity(0.45)
	} else if b.Hovered() {
		fill, color = t.SurfaceHover, t.Text
	}
	b.Background(fadeColor(b, "fill", fill))
	b.TextColor(fadeColor(b, "label", color))
	b.OnClick(func() {
		if disabled {
			return
		}
		action()
	})
	b.Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(color).SingleLine().MinWidth(0).TextAlign(ui.Center)
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
	st := t.uiState()
	if t.Busy || st.CompactBusy {
		return
	}
	st.CompactBusy = true
	st.CompactThinking = 0
	// The summarising model is the Tab's, exactly as a chat request would use it:
	// the backend cannot read Tab state, and a session restored from disk has no
	// frozen model of its own yet.
	cfg := a.config
	if fast {
		st.CompactNote = "正在快速压缩旧回复、工具参数、工具结果与思考内容…"
	} else {
		st.CompactNote = "正在压缩对话上下文…"
		if resolved, ok := a.chatConfig(t); ok {
			cfg = resolved
		}
	}
	sid := t.ID
	go func() {
		var result map[string]any
		var err error
		if fast {
			result, err = a.core.FastCompactSession(sid)
		} else {
			result, err = a.core.CompactSession(sid, "", cfg)
		}
		a.win.Update(func() {
			if err != nil {
				st.CompactBusy = false
				st.CompactThinking = 0
				st.CompactNote = ""
				a.notify("压缩失败：" + err.Error())
				a.refreshSessionFooter(t)
				return
			}
			a.finishCompact(t, result, fast)
		})
	}()
}

// finishCompact applies the backend's rewrite to the transcript and reports it.
// Both tiers end here, so the toast and the context footer never disagree with
// what the history now contains.
func (a *desktop) finishCompact(t *conversation, result map[string]any, fast bool) {
	st := t.uiState()
	st.CompactBusy = false
	st.CompactThinking = 0
	st.CompactNote = ""
	if fast && !boolOf(result, "changed") {
		dropCompactPreview(t)
		a.notify(fastCompactNoop(result))
	} else {
		a.applyCompactionResult(t, result)
		a.notify(compactToastText(int(num(result, "tokensBefore")), int(num(result, "tokensAfter"))))
	}
	st.Follow = true
	st.FollowPending = true
	a.persist(t)
	a.refreshSessionFooter(t)
}

// composerSendRow carries the transient run hints under the input. The send
// button itself lives next to the input so it stays anchored to the cursor.
func (a *desktop) composerSendRow(c *ui.Context, t *conversation) {
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		ui.Spacer(c)
		if t.AttachmentLoading {
			ui.Spinner(c).Size(13, 13)
			muted(c, "正在处理附件…", fsTiny)
		}
	})
}

func navigateInputHistory(t *conversation, draft string, up bool) (string, bool) {
	st := t.uiState()
	history := inputHistory(t)
	if len(history) == 0 {
		return draft, false
	}
	if up {
		if st.HistoryIndex < 0 {
			if strings.TrimSpace(draft) != "" {
				return draft, false
			}
			st.HistoryDraft = draft
			st.HistoryIndex = len(history)
		}
		if st.HistoryIndex > 0 {
			st.HistoryIndex--
		}
		return history[st.HistoryIndex], true
	}
	if st.HistoryIndex < 0 {
		return draft, false
	}
	st.HistoryIndex++
	if st.HistoryIndex >= len(history) {
		st.HistoryIndex = -1
		return st.HistoryDraft, true
	}
	return history[st.HistoryIndex], true
}

func inputHistory(t *conversation) []string {
	history := append([]string(nil), t.uiState().WorkspaceHistory...)
	for _, m := range t.Messages {
		if str(m, "role") == "user" {
			if text := str(m, "content"); strings.TrimSpace(text) != "" {
				history = append(history, text)
			}
		}
	}
	var unique []string
	seen := map[string]bool{}
	for n := len(history) - 1; n >= 0; n-- {
		if !seen[history[n]] {
			unique = append(unique, history[n])
			seen[history[n]] = true
		}
	}
	for i, j := 0, len(unique)-1; i < j; i, j = i+1, j-1 {
		unique[i], unique[j] = unique[j], unique[i]
	}
	return unique
}

func composerFlat(c *ui.Context, text string) ui.Element {
	return button(c, nil, text, btnGhost, true, "").Padding(1, 6).MinWidth(0).Shrink(1)
}

// composerFlatParts is composerFlat for a label whose colour carries meaning
// rather than being the ghost button's own (see the context meter).
func composerFlatParts(c *ui.Context, text string) (ui.Element, ui.Element) {
	e, label := buttonParts(c, nil, text, btnGhost, true, "")
	return e.Padding(1, 6).MinWidth(0).Shrink(1), label
}
