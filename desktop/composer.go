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
	"github.com/egoist/mygo/ui"
)

// composerHint is what an empty composer shows. MyGo paints it in the theme's
// TextMuted, which this app now sets to the faint placeholder tone: an empty
// box says where to type without competing with the conversation above it.
const composerHint = "输入消息，Enter 发送，Ctrl+Enter 换行，Esc 中断，/ 选择指令，@ 引用文件"

// composer is the input area: menus, attachments, run status, the editor and
// the info bar with model, reasoning, workspace and context controls.
func (a *desktop) composer(c *ui.Context, t *conversation) {
	st := t.uiState()
	p := a.currentPalette()
	// A closed find bar hands the caret back here, once the engine no longer holds
	// its field: both fields change in the closing frame, and the engine walks its
	// focus bindings in map order, so granting the composer then can be undone by
	// the bar's own release.
	if a.focusBack != "" && ui.FocusedValue(c, &a.chatSearchFocus) == "" {
		a.focusBack, a.composerFocus = "", a.focusBack
	}
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
		if a.prefs.VoiceInputEnabled {
			a.voiceStatusRow(c)
		}
		ui.Row(c.Key("composer-input-row")).Gap(gapField).AlignItems(ui.End).Children(func() {
			input := ui.TextAreaBase(c.Key(t.ID+"-draft"), &t.Draft).Label("消息输入框").
				FocusBind(&a.composerFocus, t.ID).
				Placeholder(composerHint).
				MinHeight(58).MaxHeight(576).Lines(1, 24).Padding(c.Theme().Space(1.5), c.Theme().Space(2.5)).FontSize(fsBody).TextColor(c.Theme().Text).MinWidth(0).Radius(radiusControl).
				Background(p.bg).Disabled(a.closing || (a.page == "kb" && a.kbIndexMissing())).Grow(1)
			border := c.Theme().Border
			if input.Focused() || input.FocusWithin() {
				border = border.Mix(p.accent, 0.35)
			}
			input.Border(1, input.AnimateColor("composer-border", border, ui.Animation{Duration: motionFade, Ease: ui.EaseInOut}))
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
		// Even an empty native text editor consumes word-navigation arrows.
		// Use the live editor text, not the possibly stale bound draft, and
		// handle the tab shortcut before the editor takes it.
		if !composing && k.Mods == ui.Cmd && (k.Key == ui.KeyLeft || k.Key == ui.KeyRight) && a.workspaceTabArrowsAvailable(t, k.Text()) {
			delta := 1
			if k.Key == ui.KeyLeft {
				delta = -1
			}
			a.activateWorkspaceTab((a.active + len(a.tabs) + delta) % len(a.tabs))
			a.composerFocus = a.current().ID
			return true
		}
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
				if st.Mention.Loading && len(st.Mention.Entries) == 0 && k.Key == ui.KeyTab {
					return true
				}
				if !st.Mention.Loading && a.applyMention(t) {
					return true
				}
			case ui.KeyEscape:
				st.Mention.Open = false
				return true
			}
		}
		// The history picker owns the arrows while it is open: without this the
		// editor moved the caret and the draft history behind the panel, and a row
		// could only ever be opened with the mouse.
		if st.SessionsOpen {
			sessions, _ := a.workspaceSessions(t.Workspace)
			switch k.Key {
			case ui.KeyUp:
				if st.SessionsIndex > 0 {
					st.SessionsIndex--
				}
				return true
			case ui.KeyDown:
				if st.SessionsIndex < len(sessions)-1 {
					st.SessionsIndex++
				}
				return true
			case ui.KeyEnter:
				if st.SessionsIndex >= 0 && st.SessionsIndex < len(sessions) {
					st.SessionsOpen = false
					a.chooseSession(sessions[st.SessionsIndex].ID)
					return true
				}
			case ui.KeyEscape:
				st.SessionsOpen = false
				return true
			}
		}
		if !composing && k.Mods == 0 && (k.Key == ui.KeyUp || k.Key == ui.KeyDown) {
			if text, ok := navigateInputHistory(t, k.Text(), k.Key == ui.KeyUp); ok {
				k.SetText(text)
				// Recalling history puts the caret after the recalled text, so
				// typing continues it instead of prepending to it.
				st.DraftCaret = len([]rune(text))
				st.RestoreCaret = true
				return true
			}
		}
		if k.Key == ui.KeyEnter && k.Mods&ui.Shift == 0 && !composing {
			if st.CmdOpen && a.selectedCommand(t) != nil {
				return false
			}
			// A built-in command typed by hand runs here rather than going to the
			// model: only the menu path ever executed one, so "/sessions" and Enter
			// used to arrive as plain text. It is taken at this entry point only, so
			// the same words coming from a suggestion button stay a message, and a
			// skill is left alone since the backend reads its arguments.
			if cmd := a.builtinCommandFor(t, k.Text()); cmd != nil {
				a.runCommand(t, cmd)
				k.SetText("")
				st.CmdOpen, st.Mention.Open = false, false
				return true
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
					meta := fmt.Sprintf("%d/%d · %s", result.Count, result.Total, result.Source)
					if dur := fmtDur(result.BuildDuration); dur != "" {
						meta += " · " + dur
					}
					st.Mention.Meta = meta
				})
			}()
		})
	})
}

func (a *desktop) applyMention(t *conversation) bool {
	st := t.uiState()
	if st.Mention.Index < 0 || st.Mention.Index >= len(st.Mention.Entries) {
		return false
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
	if at < 0 {
		return false
	}
	t.Draft = t.Draft[:at] + replacement + " "
	st.Mention.Open = false
	return true
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
		{"sessions", "查看/切换历史会话", "", func() { a.openSessionsMenu(t) }},
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
	// Resolve each skill's description once: the slash menu is rebuilt on every
	// keystroke, and a per-name scan of a.skills made this O(skills²).
	descByName := make(map[string]string, len(a.skills))
	for _, s := range a.skills {
		if _, ok := descByName[s.Name]; ok {
			continue
		}
		desc := s.Description
		if desc == "" {
			desc = s.WhenToUse
		}
		descByName[s.Name] = desc
	}
	for _, name := range a.enabledSkillNames() {
		cmds = append(cmds, slashCommand{Label: name, Desc: descByName[name], Skill: name})
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
	cmd := a.selectedCommand(t)
	if cmd == nil {
		return
	}
	st := t.uiState()
	t.Draft = "/" + cmd.Label
	if cmd.Skill != "" {
		// A skill takes arguments, so completion leaves the space that starts them
		// and hands the input back: typing a letter must not become /skillargs.
		t.Draft += " "
		st.CmdOpen = false
	} else {
		// A built-in command runs as it stands: keep it whole and the menu open, so
		// Enter or a click executes exactly what was completed.
		st.CmdOpen = true
	}
	st.DraftCaret = len([]rune(t.Draft))
	st.RestoreCaret = true
	st.CmdIndex = 0
}

// builtinCommandFor reports the built-in command a draft names exactly, so Enter
// runs it rather than sending it as a message. A skill is not one: the backend
// reads "/skill args" from the message, so it has to be sent.
func (a *desktop) builtinCommandFor(t *conversation, text string) *slashCommand {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return nil
	}
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) != 1 {
		return nil
	}
	cmds := a.builtinCommands(t)
	for i := range cmds {
		if cmds[i].Skill == "" && strings.EqualFold(cmds[i].Label, fields[0]) {
			return &cmds[i]
		}
	}
	return nil
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
		ui.Row(c).Padding(gapInline, padCard).Gap(gapField).Children(func() {
			ui.Text(c, fmt.Sprintf("指令 %d · 技能 %d", builtin, skills)).FontSize(fsTiny).TextColor(a.secondaryInk())
			ui.Spacer(c)
			fieldNote(c, "↑↓ 选择 · Enter 执行 · Tab 补全", a.secondaryInk())
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(0, 4, 4).Gap(1).Children(func() {
				if len(items) == 0 {
					fieldNote(c, "无匹配指令", a.secondaryInk())
				}
				for i, item := range items {
					i, item := i, item
					row := ui.Row(c.Key(fmt.Sprintf("cmd-%s-%d", item.Label, i))).Height(26).Padding(0, 10).Gap(gapField).Radius(radiusSmall).AlignItems(ui.Center)
					hoverRow(c, row, i == st.CmdIndex)
					row.Children(func() {
						ui.Text(c, "/"+item.Label).Font(monoFamily).FontSize(fsSmall).TextColor(p.accent)
						ui.Text(c, short(item.Desc, 70)).FontSize(fsTiny).TextColor(a.secondaryInk()).SingleLine()
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
		ui.Row(c).Padding(gapInline, padCard).Children(func() {
			ui.Text(c, "@ 文件和目录").FontSize(fsTiny).TextColor(a.secondaryInk())
			ui.Spacer(c)
			ui.Text(c, st.Mention.Meta).FontSize(fsTiny).TextColor(a.secondaryInk())
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(0, 4, 4).Gap(1).Children(func() {
				if st.Mention.Loading && len(st.Mention.Entries) == 0 {
					busyRow(c, "索引中…", a.secondaryInk())
				} else if len(st.Mention.Entries) == 0 {
					fieldNote(c, "没有匹配路径", a.secondaryInk())
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
						ui.Text(c, mark).Font(monoFamily).FontSize(fsSmall).TextColor(a.secondaryInk())
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

// openSessionsMenu opens the history picker with a row already selected. Both
// entries (the /sessions command and the info-bar button) come through here, so
// the list never opens with nothing highlighted: the current session when the
// list holds it, otherwise the newest one.
func (a *desktop) openSessionsMenu(t *conversation) {
	st := t.uiState()
	st.SessionsOpen = true
	st.SessionsIndex = 0
	sessions, _ := a.workspaceSessions(t.Workspace)
	for i, s := range sessions {
		if s.ID == t.ID {
			st.SessionsIndex = i
			break
		}
	}
	// The arrows and Enter for this panel arrive through the composer input, so
	// opening the panel from its button hands the keyboard back to it.
	a.composerFocus = t.ID
}

func (a *desktop) toggleSessionsMenu(t *conversation) {
	if t.uiState().SessionsOpen {
		t.uiState().SessionsOpen = false
		return
	}
	a.openSessionsMenu(t)
}

// chooseSession switches to the picked session and hands the keyboard back to
// its composer, so the next keystroke is a message instead of dead input.
func (a *desktop) chooseSession(id string) {
	a.openSession(id)
	a.composerFocus = id
}

func (a *desktop) sessionsMenu(c *ui.Context, t *conversation) {
	st := t.uiState()
	sessions, total := a.workspaceSessions(t.Workspace)
	// A background index refresh can shrink the list under an open panel; keeping
	// the index in range keeps exactly one row selected.
	st.SessionsIndex = max(0, min(st.SessionsIndex, len(sessions)-1))
	p := a.currentPalette()
	panel := overlayPanel(c, "sessions-menu", 0, 360, func() {
		ui.Row(c).Padding(gapInline, padCard).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("会话 (%d)", total)).FontSize(fsTiny).TextColor(a.secondaryInk())
			ui.Spacer(c)
			fieldNote(c, "↑↓ 选择 · Enter 打开", a.secondaryInk())
			iconAction(c, "sess-close", "关闭", "close", 20, func() { st.SessionsOpen = false })
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(0, 4, 4).Gap(1).Children(func() {
				if len(sessions) == 0 {
					fieldNote(c, "当前工作区暂无历史会话", a.secondaryInk())
					other := 0
					for _, s := range a.sessions {
						if !sameWorkspace(s.Workspace, t.Workspace) {
							other++
						}
					}
					if other > 0 {
						fieldNote(c, fmt.Sprintf("其他工作区还有 %d 个（切换工作区后可见）", other), a.secondaryInk())
					}
				}
				for i, s := range sessions {
					i, s := i, s
					busy := sessionBusyID(a, s.ID) != ""
					selected := i == st.SessionsIndex
					row := ui.Row(c.Key("sess-"+s.ID)).Height(28).Padding(0, padRow).Gap(gapField).Radius(radiusSmall).AlignItems(ui.Center)
					row.Cursor(ui.CursorPointer)
					hoverRow(c, row, selected)
					row.Children(func() {
						// The rail reserves its width on every row, so selecting a row cannot
						// shift the columns beside it.
						rail := ui.Box(c).Width(3).Height(13).Shrink(0).Radius(2)
						if selected {
							rail.Background(p.accent)
						}
						ui.Text(c, fmt.Sprint(i+1)).Font(monoFamily).FontSize(fsTiny).TextColor(a.secondaryInk())
						ui.Text(c, fmtTime(s.UpdatedAt)).FontSize(fsTiny).TextColor(a.secondaryInk())
						ui.Text(c, fmt.Sprintf("%d条", s.MessageCount)).FontSize(fsTiny).TextColor(a.secondaryInk())
						title := s.Title
						if title == "" {
							title = "会话"
						}
						label := ui.Text(c, short(title, 50)).FontSize(fsSmall).SingleLine()
						if selected {
							label.TextColor(p.accent).FontWeight(600)
						}
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
						a.chooseSession(s.ID)
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
			ui.Text(c, note).FontSize(fsTiny).TextColor(a.secondaryInk()).SingleLine().MinWidth(0)
			if count := compactThinkingTokens(st); count > 0 {
				a.thinkingTokensLine(c, count)
			}
		case t.Busy:
			a.runSpinner(c)
			a.thinkingTokensLine(c, (st.ThinkingBytes+3)/4)
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

// thinkingTokensLine draws the one "Thinking N tokens" line shared by the
// compacting and busy states, so both keep a single font size.
func (a *desktop) thinkingTokensLine(c *ui.Context, count int) {
	if count <= 0 {
		return
	}
	ui.Text(c, fmt.Sprintf("Thinking %s tokens", fmtK(count))).FontSize(fsTiny).TextColor(a.secondaryInk()).SingleLine()
}

// runSpinner draws a continuous, staggered wave in a fixed 15x16 box.
// Only painting is scheduled; messages and layout are not rebuilt per frame.
func (a *desktop) runSpinner(c *ui.Context) {
	color := a.secondaryInk()
	reduced := uikit.StillMotion(c)
	ui.Box(c).Width(15).Height(16).Draw(func(p *ui.Painter, r ui.Rect) {
		if !reduced {
			p.AnimationFrameRate(30)
		}
		elapsed := time.Duration(p.Now().UnixNano() % int64(uikit.ThinkingCycle))
		for i := 0; i < 3; i++ {
			height := float32(9)
			if !reduced {
				height = thinkingBarHeight(elapsed, i)
			}
			drawThinkingBar(p, ui.Rect{X: r.X + float32(i)*6, Y: r.Y + (16-height)/2, W: 3, H: height}, color)
		}
	})
}

// Paths retain subpixel coverage; Fill snaps edges to whole device pixels,
// making these small, slowly moving bars appear to stop and jump.
func drawThinkingBar(p *ui.Painter, r ui.Rect, color ui.Color) {
	x, y, right, bottom := r.X, r.Y, r.X+r.W, r.Y+r.H
	const radius = float32(1)
	const k = float32(0.55228475) // Cubic approximation of a quarter circle.
	var path ui.Path
	path.MoveTo(x+radius, y).LineTo(right-radius, y).
		CubeTo(right-radius+k*radius, y, right, y+radius-k*radius, right, y+radius).
		LineTo(right, bottom-radius).
		CubeTo(right, bottom-radius+k*radius, right-radius+k*radius, bottom, right-radius, bottom).
		LineTo(x+radius, bottom).
		CubeTo(x+radius-k*radius, bottom, x, bottom-radius+k*radius, x, bottom-radius).
		LineTo(x, y+radius).
		CubeTo(x, y+radius-k*radius, x+radius-k*radius, y, x+radius, y).Close()
	p.FillPath(&path, color)
}

func thinkingBarHeight(elapsed time.Duration, index int) float32 {
	const cycle = uikit.ThinkingCycle
	phase := float64((elapsed+time.Duration(2-index)*uikit.ThinkingStagger)%cycle) / float64(cycle)
	return 9 + 7*float32(0.5-0.5*math.Cos(phase*2*math.Pi))
}

func (a *desktop) retryBanner(c *ui.Context, info *retryInfo) {
	p := a.currentPalette()
	ui.Row(c).FillWidth().Padding(5, 10).Gap(gapField).Radius(radiusSmall).AlignItems(ui.Center).
		Background(ui.RGBA(217, 164, 65, 0.10)).Children(func() {
		iconGlyph(c, "refresh", 13).TextColor(c.Theme().Warning)
		msg := fmt.Sprintf("重试中 %d/%d：%s", info.Attempt, info.MaxAttempts, info.Error)
		uikit.Tooltip(c, ui.Text(c, short(msg, 120)).FontSize(fsTiny).TextColor(c.Theme().Warning).SingleLine(), msg)
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
		fieldNote(c, "请先初始化知识库", a.secondaryInk())
		return
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		composerFlat(c, "资料入库").Disabled(running).OnClick(func() { a.send(t, "请把 sources/ 里新增的资料整理入库，并更新索引。") })
		composerFlat(c, "整理索引").Disabled(running).OnClick(func() { a.send(t, "请检查并整理知识库索引，修正过期或重复的条目。") })
		if running {
			fieldNote(c, "知识库任务运行中…", a.secondaryInk())
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
	p := a.currentPalette()
	ui.Row(c).FillWidth().MinWidth(0).Gap(2).AlignItems(ui.Center).Children(func() {
		addButton(c, "composer-new", "新建会话", "plus", 26, func() { a.newSession() })
		iconAction(c, "composer-sessions", "查看和切换历史会话", "menu", 26, func() {
			a.toggleSessionsMenu(t)
		}).KeepFocus().TextColor(p.muted)
		a.modelSelector(c, t)
		a.reasoningSelector(c, t)
		a.workspaceButton(c, t)
		a.extraRootsButton(c, t)
		mutedIconAction(c, "composer-terminal", "在终端中打开工作区", "terminal", 26, func() {
			req := backend.WorkspacePathRequest{Workspace: t.Workspace}
			a.job(func() error { return a.core.OpenWorkspaceTerminalAt(req) })
		})
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
		}, a.secondaryInk())
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
		}, a.secondaryInk())
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
	st := t.uiState()
	btn := uikit.Tooltip(c, composerSettingButton(c.Key("composer-model"), short(strings.ReplaceAll(label, " / ", " · "), 34), t.Busy, func() {
		st.ModelOpen = !st.ModelOpen
		if st.ModelOpen {
			st.ModelSearch = ""
			// The menu opens as a plain list of providers; a group opens on hover.
			st.ModelGroup = ""
			st.FlyoutHot = false
			st.FlyoutDismiss = ""
		}
	}, a.secondaryInk()).SingleLine().MinWidth(0).Shrink(1), "切换模型")
	if t.Busy {
		uikit.Tooltip(c, btn, "运行中锁定：本轮结束后切换才生效")
	}
	ui.PopoverBase(c, btn, &st.ModelOpen, func(panel ui.Element) {
		popoverChrome(c, panel, 340)
		panel.MaxHeight(520)
		ui.Column(c).FillWidth().Children(func() {
			uikit.Input(c.Key("model-menu-search"), &st.ModelSearch).Placeholder("搜索模型或供应商").Label("搜索模型或供应商").FillWidth()
			query := strings.ToLower(strings.TrimSpace(st.ModelSearch))
			// The menu is rebuilt on every frame the pointer moves, so the option
			// identity, the provider group, the lowered model name and the search
			// haystack are worked out once per model here, not once per comparison or
			// once per recently used entry.
			models := a.config.Models
			options := make([]string, len(models))
			providerOf := make([]string, len(models))
			lowerNames := make([]string, len(models))
			haystacks := make([]string, len(models))
			for i, model := range models {
				options[i] = modelOption(model)
				providerOf[i] = providerGroupName(model)
				lowerNames[i] = strings.ToLower(model.Model)
				if query != "" {
					haystacks[i] = strings.ToLower(model.ProviderName + " " + model.DisplayName + " " + model.Model)
				}
			}
			matches := func(i int) bool {
				return query == "" || strings.Contains(haystacks[i], query)
			}
			ui.Scroll(c).MaxHeight(440).Children(func() {
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
					if len(recent) > 3 {
						recent = recent[:3]
					}
					shownRecent := false
					// recentHot tracks the pointer on the recents block, which is the one
					// part of the menu that belongs to no provider.
					recentHot := false
					for _, identity := range recent {
						for i := range models {
							if options[i] != identity || !matches(i) {
								continue
							}
							if !shownRecent {
								head := ui.Text(c, "最近使用").FontSize(fsTiny).TextColor(a.secondaryInk()).Padding(gapInline, padCard)
								if head.Hovered() {
									recentHot = true
								}
								shownRecent = true
							}
							model := models[i]
							name := model.DisplayName
							if name == "" {
								name = model.Model
							}
							if popoverModelRecent(c, fmt.Sprintf("model-recent-%d", i), name, model.ProviderName, options[i] == t.Model, func() { st.ModelOpen = false; a.selectChatModel(t, model) }, a.secondaryInk()) {
								recentHot = true
							}
						}
					}
					if shownRecent {
						divider(c)
					}
					groups := map[string][]int{}
					for i := range models {
						if matches(i) {
							provider := providerOf[i]
							groups[provider] = append(groups[provider], i)
						}
					}
					names := make([]string, 0, len(groups))
					for name := range groups {
						names = append(names, name)
					}
					// Groups reached recently come first, in the order they were used. A
					// group with no entry in rank was never used: the alphabet only decides
					// the order among those, never between a used and an unused one. The
					// Tab's own model counts as the most recent.
					rank := map[string]int{}
					for pos, identity := range recent {
						for i := range models {
							if options[i] != identity {
								continue
							}
							provider := providerOf[i]
							if seen, ok := rank[provider]; !ok || pos < seen {
								rank[provider] = pos
							}
						}
					}
					for i := range models {
						if options[i] == t.Model {
							rank[providerOf[i]] = -1
							break
						}
					}
					sort.Slice(names, func(i, j int) bool {
						ri, iUsed := rank[names[i]]
						rj, jUsed := rank[names[j]]
						switch {
						case iUsed && jUsed:
							return ri < rj
						case iUsed != jUsed:
							return iUsed
						default:
							return strings.ToLower(names[i]) < strings.ToLower(names[j])
						}
					})
					// The pointer opens a group's models beside it. One group at a time:
					// every row only reports whether it is hovered, this loop picks the
					// single group to attach a flyout to, and the flyout then holds it open
					// while the pointer is on its way into the list.
					providers := make([]string, 0, len(names))
					rows := make([]ui.Element, 0, len(names))
					hovered := ""
					for _, provider := range names {
						indices := groups[provider]
						sort.SliceStable(indices, func(i, j int) bool {
							return lowerNames[indices[i]] < lowerNames[indices[j]]
						})
						row, hot := a.modelGroupRow(c, t, provider, indices)
						providers = append(providers, provider)
						rows = append(rows, row)
						if hot && hovered == "" {
							hovered = provider
						}
					}
					// Not on a row: the pointer may be heading into the flyout, resting on
					// the menu, or gone. Carry the current group and let the check below
					// decide whether it stays.
					// Escape closed a flyout under the pointer: that row must not rebuild
					// it on the next frame, or the key reads as doing nothing. The mark
					// clears as soon as the pointer leaves the row, so hovering the group
					// again opens it as always.
					if st.FlyoutDismiss != "" {
						if hovered == st.FlyoutDismiss {
							hovered = ""
						} else {
							st.FlyoutDismiss = ""
						}
					}
					open := hovered
					sticky := false
					switch {
					case open != "":
						// A row is hovered: its group opens.
					case recentHot:
						// The pointer is on the recents block, which belongs to no provider:
						// drop the flyout instead of leaving it over the very models the
						// pointer is reaching for.
					case st.ModelGroup != "":
						open = st.ModelGroup
						sticky = true
					}
					st.FlyoutHot = false
					opened := false
					closed := false
					for i, provider := range providers {
						if provider != open {
							continue
						}
						st.FlyoutHot, closed = a.modelGroupFlyout(c, t, providers[i], groups[provider], rows[i])
						opened = true
						break
					}
					switch {
					case !opened:
						// The group is gone from the list, as after a search that no longer
						// matches any of its models.
						open = ""
					case closed:
						// Escape or a press outside the flyout closed it: drop the group, and
						// keep its row from opening it again while the pointer rests there.
						st.FlyoutHot = false
						st.FlyoutDismiss = open
						open = ""
					case sticky && !st.FlyoutHot && !panel.Hovered():
						// The pointer left both the menu and the flyout: drop the group.
						open = ""
					}
					st.ModelGroup = open
					// One row reads as current: the one the pointer is on, or the one whose
					// flyout it went into. The rows were drawn before this frame knew which,
					// so the highlight lands here, once, instead of leaving the group the
					// pointer just left lit beside the new one.
					for i, provider := range providers {
						if provider == open {
							rows[i].Background(c.Theme().SurfaceHover)
							break
						}
					}
					if len(groups) == 0 {
						fieldNote(c, "没有匹配的模型", a.secondaryInk())
					}
				})
			})
			divider(c)
			popoverItem(c, "model-manage", "管理模型", "", false, func() { st.ModelOpen = false; a.changePage("models") }, a.secondaryInk())
		})
	})
}

// providerGroupName is the menu's grouping key for one model: its provider, with
// the unnamed entries folded into the custom group.
func providerGroupName(model backend.ModelConfig) string {
	provider := strings.TrimSpace(model.ProviderName)
	if provider == "" {
		provider = customProviderName
	}
	return provider
}

// modelGroupRow draws one provider in the model menu: its name, how many models
// it holds, and a chevron. It reports whether the pointer is on the row, which
// is what opens the flyout; the row itself builds no flyout, so a frame cannot
// end up showing two. Its highlight is left to the caller, which alone knows
// which group the frame settled on.
func (a *desktop) modelGroupRow(c *ui.Context, t *conversation, provider string, indices []int) (ui.Element, bool) {
	th := c.Theme()
	row := ui.ButtonBase(c.Key("model-group-"+provider)).FillWidth().MinHeight(32).Padding(gapInline, padCard).Justify(ui.Start).Radius(radiusSmall).Cursor(ui.CursorPointer).Label(provider)
	hovered := row.Hovered()
	hasActive := false
	for _, i := range indices {
		if modelOption(a.config.Models[i]) == t.Model {
			hasActive = true
			break
		}
	}
	row.Children(func() {
		ui.Row(c).FillWidth().Gap(gapField).AlignItems(ui.Center).Children(func() {
			name := ui.Text(c, provider).FontSize(fsSmall).SingleLine().Grow(1)
			if hasActive {
				name.TextColor(th.Accent)
			}
			ui.Text(c, fmt.Sprint(len(indices))).FontSize(fsTiny).TextColor(a.secondaryInk())
			iconGlyph(c, "chevron", sizeCaretInline).TextColor(a.secondaryInk())
		})
	})
	return row, hovered
}

// modelGroupFlyout lists one provider's models beside its row and reports whether
// the pointer is inside it, which keeps it open on the next frame, and whether
// this frame closed it (Escape, or a press outside it and its row), so the caller
// can drop the group instead of rebuilding a flyout nobody asked for.
func (a *desktop) modelGroupFlyout(c *ui.Context, t *conversation, provider string, indices []int, row ui.Element) (bool, bool) {
	open := true
	hot := false
	ui.PopoverBase(c.Key("model-flyout-"+provider), row, &open, func(panel ui.Element) {
		panel.AttachTo(row, ui.AnchorTopEnd, ui.AnchorTopStart).MarginStart(-6)
		popoverChrome(c, panel, 260)
		if panel.Hovered() {
			hot = true
		}
		ui.Scroll(c).MaxHeight(360).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, i := range indices {
					model := a.config.Models[i]
					name := model.DisplayName
					if name == "" {
						name = model.Model
					}
					popoverItem(c, fmt.Sprintf("model-choice-%d", i), name, "", modelOption(model) == t.Model, func() {
						t.uiState().ModelOpen = false
						a.selectChatModel(t, model)
					}, a.secondaryInk())
				}
			})
		})
	})
	return hot, !open
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
		if old != t.Model && len(recent) < 3 {
			recent = append(recent, old)
		}
	}
	a.prefs.RecentModels = recent
	a.savePrefs()
	if a.core == nil || a.win == nil {
		return
	}
	go func() {
		err := a.core.SelectNativeModel(id)
		a.win.Update(func() {
			if err != nil {
				a.notify(err.Error())
			}
		})
	}()
}

// reasoningSelector is the info bar's thinking-level trigger: compact while a
// run is off, locked while one is on, and its choices are MyGo's drawn menu.
// Picking a level edits the model entry itself, so the choice survives a
// restart: a session snapshot carries no model fields, and a fresh
// conversation re-reads the model config.
func (a *desktop) reasoningSelector(c *ui.Context, t *conversation) {
	if t.Reasoning == "" {
		t.Reasoning = "auto"
	}
	btn := uikit.CompactMenu(c.Key("composer-reasoning"), &t.Reasoning, reasoningLevels, t.Busy, func(level string) {
		a.rememberReasoning(t, level)
	}).Label("思考强度")
	tip := "思考强度（需模型支持，不支持的模型请保持 auto）"
	if t.Busy {
		tip = "运行中锁定：本轮结束后切换才生效"
	}
	uikit.Tooltip(c, btn, tip)
}

// rememberReasoning stores the picked level on the model entry the Tab uses.
// The info bar edits that model's default instead of a per-session override:
// keeping it in the Tab alone lost the choice on every restart and reset it
// again whenever the Tab switched models.
//
// The pick is replayed on the config as it is on disk, not on the copy this
// window holds: a tool toggle or a model save may be halfway through, and
// writing this window's copy would throw that one away.
func (a *desktop) rememberReasoning(t *conversation, level string) {
	if a.core == nil || a.win == nil || t.Model == "" {
		t.Reasoning = level
		return
	}
	t.Reasoning = level
	model := t.Model
	previous, done := a.configWriteSlot()
	go func() {
		defer close(done)
		waitConfigWrite(previous)
		fresh, status, err := a.writeReasoning(model, level)
		a.win.Update(func() {
			if err != nil {
				// The entry keeps the level it had, so the Tab may not show one the
				// next restart would not read back.
				if t.Model == model && t.Reasoning == level {
					t.Reasoning = persistedReasoning(a.config, model)
				}
				a.notifyError(err.Error())
				return
			}
			a.config = fresh
			switch status {
			case reasoningMissing:
				if t.Model == model {
					t.Reasoning = persistedReasoning(fresh, model)
				}
				a.notify("这个模型已不在模型配置里，思考强度没改")
			case reasoningSaved:
				a.notifySuccess("思考强度已改为 " + level)
			}
		})
	}()
}

// reasoningWrite is what replaying a pick on disk did.
type reasoningWrite int

const (
	reasoningSaved     reasoningWrite = iota // the entry now carries the level
	reasoningUnchanged                       // it already did: nothing to write
	reasoningMissing                         // the Tab points at a model the config lost
)

// writeReasoning replays one thinking-level pick against the config on disk and
// returns the config afterwards, so every save that landed before it survives.
func (a *desktop) writeReasoning(model, level string) (backend.ConfigState, reasoningWrite, error) {
	cfg, err := a.core.GetConfig()
	if err != nil {
		return backend.ConfigState{}, reasoningSaved, err
	}
	next, changed := withModelReasoning(cfg, model, level)
	if !changed {
		// withModelReasoning also reports nothing to change for a model that is
		// gone; telling the two apart keeps a stale Tab from looking saved.
		if _, found := modelReasoning(cfg, model); !found {
			return cfg, reasoningMissing, nil
		}
		return cfg, reasoningUnchanged, nil
	}
	if err := a.core.SaveConfig(next); err != nil {
		return backend.ConfigState{}, reasoningSaved, err
	}
	fresh, err := a.core.GetConfig()
	return fresh, reasoningSaved, err
}

// modelReasoning is the level the model entry behind option holds, "auto" when it
// has none, and whether that entry is there at all.
func modelReasoning(cfg backend.ConfigState, option string) (string, bool) {
	for _, m := range cfg.Models {
		if modelOption(m) != option {
			continue
		}
		if m.ReasoningEffort == "" {
			return "auto", true
		}
		return m.ReasoningEffort, true
	}
	return "auto", false
}

// persistedReasoning is the level the model entry behind option holds, "auto"
// when it has none or is gone.
func persistedReasoning(cfg backend.ConfigState, option string) string {
	level, _ := modelReasoning(cfg, option)
	return level
}

// withModelReasoning writes level onto the model entry behind option. changed is
// false when that model is gone (deleted, or the Tab points at a stale option)
// or already carries the level: there is nothing to persist then.
func withModelReasoning(cfg backend.ConfigState, option, level string) (backend.ConfigState, bool) {
	for i, m := range cfg.Models {
		if modelOption(m) != option {
			continue
		}
		// An entry that never carried a level means "auto", so picking auto on
		// it is no change and must not rewrite the config or announce one.
		stored := cfg.Models[i].ReasoningEffort
		if stored == "" {
			stored = "auto"
		}
		if stored == level {
			return cfg, false
		}
		models := append([]backend.ModelConfig(nil), cfg.Models...)
		models[i].ReasoningEffort = level
		cfg.Models = models
		return cfg, true
	}
	return cfg, false
}

// workspaceButton is the workspace entry of the info bar: the folder glyph on
// its own, the same 26px box and 16px glyph as the icons beside it, with the
// workspace and its path in the tip. The name already shows in the header tab.
func (a *desktop) workspaceButton(c *ui.Context, t *conversation) {
	tip := "在文件管理器中打开工作区：" + t.Workspace
	if t.Workspace == "" {
		tip = "尚未选择工作区"
	}
	mutedIconAction(c, "composer-workspace", tip, "folderopen", 26, func() {
		if t.Workspace != "" {
			a.job(func() error { return a.core.OpenPathInFileManager(t.Workspace) })
		}
	})
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
	btn := uikit.Tooltip(c, ui.ButtonBase(c.Key("composer-extra-roots")).Height(26).MinWidth(26).Padding(0, 3).Gap(2).
		Radius(radiusSmall).AlignItems(ui.Center).Center().Label("附加目录"), tip).
		TextColor(color).Cursor(ui.CursorPointer)
	fill := ui.Transparent
	if btn.Hovered() {
		fill = p.hover
		if count == 0 {
			color = p.text
		}
	}
	st := t.uiState()
	if st.ExtraRootsOpen {
		color = p.accent
	}
	color = fadeColor(btn, "icon-ink", color)
	btn.Background(fadeColor(btn, "fill", fill)).TextColor(color)
	btn.OnClick(func() { st.ExtraRootsOpen = !st.ExtraRootsOpen })
	ui.PopoverBase(c.Key("extra-roots-menu"), btn, &st.ExtraRootsOpen, func(panel ui.Element) {
		popoverChrome(c, panel, 300)
		panel.Label("附加工作区菜单").AttachTo(btn, ui.AnchorTopLeft, ui.AnchorBottomLeft).Margin(0, 0, 6, 0).MaxHeightPercent(75).Transition(uikit.PopupTransition())
		ui.Scroll(c.Key("roots")).FillWidth().MaxHeight(260).Children(func() {
			if len(roots) == 0 {
				muted(c, "尚未添加附加工作区", fsSmall, a.secondaryInk()).Padding(8, 10)
			}
			for _, root := range roots {
				root := root
				popoverItem(c, root, "📁 "+filepath.Base(root), root+" · 点击移除", false, func() { st.ExtraRootsOpen = false; a.removeExtraRoot(t, root) }, a.secondaryInk())
			}
		})
		divider(c)
		popoverItem(c, "add-root", "＋ 添加目录", "", false, func() { st.ExtraRootsOpen = false; a.addExtraRoot(t) }, a.secondaryInk())
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
				a.notify("该目录就是当前主工作区")
				return
			}
			for _, existing := range a.extraRoots[t.ID] {
				if sameWorkspace(existing, p) {
					a.notify("该目录已在附加工作区中")
					return
				}
			}
			roots := append(append([]string(nil), a.extraRoots[t.ID]...), p)
			if err := a.setExtraRoots(t, roots); err != nil {
				a.notifyError(err.Error())
			}
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
	if err := a.setExtraRoots(t, kept); err != nil {
		a.notifyError(err.Error())
	}
}

// This setter only publishes in-memory state under a short backend lock.
// Commit the UI after the fence has accepted the list; asynchronous jobs can
// otherwise publish add/remove operations in the wrong order.
func (a *desktop) setExtraRoots(t *conversation, roots []string) error {
	if err := a.core.SetSessionExtraRoots(t.ID, roots); err != nil {
		return err
	}
	if a.extraRoots == nil {
		a.extraRoots = map[string][]string{}
	}
	a.extraRoots[t.ID] = append([]string{}, roots...)
	return nil
}

func (a *desktop) sshChip(c *ui.Context, t *conversation) {
	if t.Workspace == "" {
		return
	}
	count := len(a.sshAllowed[t.Workspace])
	footerBadge(c, "footer-ssh", "SSH", fmt.Sprint(count), ui.Hex("#3b9f70"), "SSH 集群授权", func() {
		a.sshF.AuthOpen = true
		a.loadSSHAuth(t.Workspace)
	}, a.secondaryInk())
}

func (a *desktop) gitButton(c *ui.Context, t *conversation) {
	st := t.uiState()
	if !st.GitLoaded {
		muted(c, "…", fsTiny, a.secondaryInk())
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
	btn := uikit.Tooltip(c, ui.ButtonBase(c.Key("footer-git")).Height(22).Padding(1, 7).Radius(11).Background(c.Theme().Surface).Cursor(ui.CursorPointer), "查看 Git 改动")
	btn.Children(func() {
		ui.Row(c).Gap(gapTight).AlignItems(ui.Center).Children(func() {
			ui.Text(c, short(label, 22)).FontSize(fsTiny).TextColor(c.Theme().Accent).SingleLine()
			for _, part := range []struct {
				value  int
				symbol string
				color  ui.Color
			}{{git.Ahead, "↑", a.secondaryInk()}, {git.Behind, "↓", a.secondaryInk()}, {git.Added, "+", c.Theme().Success}, {git.Modified, "~", c.Theme().Warning}, {git.Deleted, "-", c.Theme().Danger}} {
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
						gitRepoPopoverItem(c, fmt.Sprintf("git-repo-%d", i), repo, func() {
							*open = false
							a.filesF.GitOpen = true
							a.filesF.GitRepo = repo.Path
							a.filesF.GitFile = -1
							a.loadGitDiff(t, repo.Path)
						}, a.secondaryInk())
					}
				})
			})
		})
	}
}

func gitRepoPopoverItem(c *ui.Context, key string, repo backend.GitRepoItem, action func(), ink ...ui.Color) {
	t := c.Theme()
	label := repo.Name + "/" + repo.Branch
	row := ui.ButtonBase(c.Key(key)).FillWidth().MinHeight(44).Padding(5, 10).
		Justify(ui.Start).Radius(radiusSmall).Label(label).Cursor(ui.CursorPointer).OnClick(action)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Column(c).FillWidth().Gap(gapTight).Children(func() {
			ui.Row(c).FillWidth().Gap(3).AlignItems(ui.Center).Children(func() {
				ui.Text(c, short(repo.Name, 22)).FontSize(fsBody).TextColor(t.Text).SingleLine().Shrink(1).MinWidth(0)
				ui.Text(c, "/").FontSize(fsTiny).TextColor(uikit.SecondaryInk(c, ink)).Shrink(0)
				ui.Text(c, short(repo.Branch, 18)).FontSize(fsSmall).TextColor(t.Accent).SingleLine().Shrink(0)
				ui.Spacer(c)
			})
			if repo.Ahead > 0 || repo.Behind > 0 || repo.Added > 0 || repo.Modified > 0 || repo.Deleted > 0 {
				ui.Row(c).Gap(gapTight).AlignItems(ui.Center).Children(func() {
					gitRepoStatusTag(c, "ahead", "↑", repo.Ahead, uikit.Info)
					gitRepoStatusTag(c, "behind", "↓", repo.Behind, uikit.Warning)
					gitRepoStatusTag(c, "added", "+", repo.Added, uikit.Success)
					gitRepoStatusTag(c, "modified", "~", repo.Modified, uikit.Warning)
					gitRepoStatusTag(c, "deleted", "−", repo.Deleted, uikit.Error)
				})
			}
		})
	})
	uikit.Tooltip(c, row, label)
}

func gitRepoStatusTag(c *ui.Context, key, symbol string, count int, tone uikit.Tone) {
	if count <= 0 {
		return
	}
	gitRepoStatusChip(c.Key(key), fmt.Sprintf("%s%d", symbol, count), tone)
}

func gitRepoStatusChip(c *ui.Context, label string, tone uikit.Tone) {
	color := uikit.ToneColor(c, tone)
	ui.Row(c).Height(18).Padding(0, 5).AlignItems(ui.Center).Radius(radiusSmall).
		Background(color.Alpha(0.08)).Children(func() {
		ui.Text(c, label).FontSize(fsTiny).TextColor(color).SingleLine()
	})
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

// contextBreakdownHeight is how tall the context breakdown may get. Its rows are
// a fixed set and the panel opens upwards, so the room is there: thirteen rows
// (every instruction file a project can have) measure about 300, and this leaves
// a handful more before anything scrolls.
const contextBreakdownHeight float32 = 420

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
		color = c.Theme().Danger
	case ratio > 0.2:
		color = c.Theme().Warning
	}
	btn, labelEl := composerFlatParts(c, label)
	// The colour goes on the label itself: a TextColor on the button never reaches
	// it, which is why the meter used to sit at one muted grey whatever the ratio.
	labelEl.TextColor(fadeColor(labelEl, "context-tone", color))
	btn = uikit.Tooltip(c, btn.SingleLine().Shrink(0).NoWrap(), "上下文占用（点击查看明细）")
	if !st.FooterLoaded {
		btn.OnClick(func() { a.refreshSessionFooter(t) })
		return
	}
	btn.OnClick(func() { st.ContextOpen = !st.ContextOpen })
	ui.PopoverBase(c, btn, &st.ContextOpen, func(panel ui.Element) {
		popoverChrome(c, panel, 300)
		// The breakdown is a short list — the prompt's total, the four kinds of
		// message, the running tally — and the sections behind the fold still fit
		// when a reader opens them, so the panel shows the whole thing at once. It
		// opens upwards over the conversation, and the scrollbar it used to carry
		// only hid the numbers it exists for. A short window still bounds it, and
		// then the buttons below the list keep their room instead of spilling out.
		panel.MaxHeightPercent(85)
		panel.Column().Label("上下文占用明细").AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight).Left(-12)
		b := st.Breakdown
		ui.Text(c, "上下文构成").FontSize(fsSection).Bold().Padding(gapInline, padCard)
		// Only a session with more sections than usual — a long memory index, one
		// more instruction file — scrolls here.
		ui.Scroll(c).MaxHeight(contextBreakdownHeight).Children(func() {
			ui.Column(c).FillWidth().Padding(4, 10).Gap(gapInline).Children(func() {
				// The prompt's own sections are detail, and the message kinds below are
				// the rows that actually grow, so the sections start folded behind the
				// total they add up to.
				contextPartFold(c, "system-prompt", "系统提示词", b.SystemPrompt, st.PromptPartsOpen, func() {
					st.PromptPartsOpen = !st.PromptPartsOpen
				}, a.secondaryInk())
				if st.PromptPartsOpen {
					for _, part := range b.SystemPromptParts {
						if part.Tokens > 0 {
							contextPart(c, "  · "+part.Label, part.Tokens, a.secondaryInk())
						}
					}
				}
				contextPart(c, "工具 Schema", b.ToolSchemas, a.secondaryInk())
				contextPart(c, "用户提问", b.UserMessages, a.secondaryInk())
				contextPart(c, "AI 回复", b.AssistantMsgs, a.secondaryInk())
				contextPart(c, "工具结果", b.ToolResults, a.secondaryInk())
				if b.Reasoning > 0 {
					contextPart(c, "思考内容", b.Reasoning, a.secondaryInk())
				}
				ui.Text(c, fmt.Sprintf("累计输入 %s / 输出 %s", fmtK(st.Usage.InputTokens), fmtK(st.Usage.OutputTokens))).FontSize(fsTiny).TextColor(a.secondaryInk())
			})
		})
		divider(c)
		ui.Column(c).FillWidth().Shrink(0).Padding(4, 10, 6).Gap(gapField).Children(func() {
			// Two rows of two buttons.
			ui.Row(c).FillWidth().Shrink(0).Gap(gapField).Children(func() {
				contextCompactButton(c, "context-compact", "压缩上下文", true, t.Busy || st.CompactBusy, "压缩对话上下文，用摘要替换历史消息", func() {
					st.ContextOpen = false
					a.compact(t, false)
				}, a.secondaryInk())
				contextCompactButton(c, "context-fast-compact", "压缩上下文(快速)", true, t.Busy || st.CompactBusy, "本地裁剪旧回复、工具结果与思考，保留最近内容，不调用模型", func() {
					st.ContextOpen = false
					a.compact(t, true)
				}, a.secondaryInk())
			})
			ui.Row(c).FillWidth().Shrink(0).Gap(gapField).Children(func() {
				contextCompactButton(c, "context-lessons", "压缩教训", false, t.Busy, "压缩并总结项目经验教训", func() {
					st.ContextOpen = false
					a.send(t, "请压缩并总结这个项目的经验教训（LESSONS.md）。")
				}, a.secondaryInk())
				contextCompactButton(c, "context-graph", "更新图谱", false, t.Busy, "更新 CODEGRAPH.md 导航", func() {
					st.ContextOpen = false
					a.send(t, "请更新 CODEGRAPH.md，让导航与当前代码一致。")
				}, a.secondaryInk())
			})
		})
	})
}

func contextCompactButton(c *ui.Context, key, label string, primary, disabled bool, tip string, action func(), ink ...ui.Color) {
	// 对齐 main 的 .context-compact-btn：透明底 + 1px 边框，字号 12，前两个主操作走
	// 主题高亮色；圆角与边框透明度跟其它按钮同一套 token。
	t := c.Theme()
	b := uikit.Tooltip(c, ui.ButtonBase(c.Key(key)).Grow(1).Basis(0).MinWidth(0).Height(28).Padding(2, 4).Radius(radiusControl).
		Border(1, surfaceBorder(c)).Center().Label(label), tip).Cursor(ui.CursorPointer).Disabled(disabled)
	color := uikit.SecondaryInk(c, ink)
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
		ui.Text(c, label).FontSize(fsTiny).TextColor(color).SingleLine().MinWidth(0).TextAlign(ui.Center)
	})
}

func contextPart(c *ui.Context, name string, tokens int, ink ...ui.Color) {
	ui.Row(c).FillWidth().Children(func() {
		ui.Text(c, name).FontSize(fsSmall).TextColor(uikit.SecondaryInk(c, ink))
		ui.Spacer(c)
		ui.Text(c, fmtK(tokens)).FontSize(fsSmall)
	})
}

// contextPartFold is a breakdown row whose sections fold away: the total stays on
// the parent line, so a closed group hides detail and no number. It follows the
// model menu's group row — a keyed ButtonBase whose chevron sits at the trailing
// edge, after the value — and the chevron is chosen per state instead of rotated,
// so both keep the same optical weight. The chevron's gutter is what the row's own
// number gives up, which is why only this line sits short of the column below it.
func contextPartFold(c *ui.Context, key, name string, tokens int, open bool, toggle func(), ink ...ui.Color) {
	// The 1px of vertical padding is the hover band: without it the fill hugs the
	// glyphs. Horizontal padding stays 0 so the name keeps the column's left edge.
	row := ui.ButtonBase(c.Key(key)).FillWidth().Padding(1, 0).Radius(radiusSmall).
		Justify(ui.Start).Cursor(ui.CursorPointer).Label(name)
	fill := ui.Transparent
	if row.Hovered() {
		fill = c.Theme().SurfaceHover
	}
	// The fill snaps instead of fading: a popover row is rebuilt every frame, and a
	// colour animation here would rebuild the whole view for each frame of the fade.
	row.Background(fill).OnClick(toggle)
	icon := "chevron"
	if open {
		icon = "chevrondown"
	}
	mut := uikit.SecondaryInk(c, ink)
	row.Children(func() {
		ui.Row(c).FillWidth().Gap(gapInline).AlignItems(ui.Center).Children(func() {
			ui.Text(c, name).FontSize(fsSmall).TextColor(mut).SingleLine()
			ui.Spacer(c)
			ui.Text(c, fmtK(tokens)).FontSize(fsSmall)
			iconGlyph(c, icon, sizeCaretInline).TextColor(mut).Shrink(0)
		})
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
		a.notify(compactionToast(result))
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
			muted(c, "正在处理附件…", fsTiny, a.secondaryInk())
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
