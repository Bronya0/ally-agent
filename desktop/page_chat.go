// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"math"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// chatMessages is the conversation viewport: history, welcome card, plan
// panel and the floating jump-to-bottom controls.
func (a *desktop) chatMessages(c *ui.Context, t *conversation, width float32) {
	st := t.uiState()
	start, end := historyMessageWindow(t, a.messageWindowSize())
	st.windowStart, st.windowEnd, st.windowBuilding = start, end, true
	defer func() { st.windowBuilding = false }()
	st.RoundAt = map[int]int{}
	for idx, stat := range st.Round {
		if stat != nil && stat.Done {
			st.RoundAt[roundStatsIndex(t, idx)] = idx
		}
	}
	for idx := range st.PromptHandles {
		if idx < start || idx >= end {
			delete(st.PromptHandles, idx)
		}
	}
	if st.PendingPrompt {
		if handle := st.PromptHandles[st.PendingPromptIdx]; handle != nil {
			question := handle.Bounds(c)
			viewport := t.Scroll.Bounds(c)
			if question.H > 0 && viewport.H > 0 {
				t.Scroll.Y = max(float32(0), t.Scroll.Y+question.Y-viewport.Y-12)
				st.PendingPrompt = false
			}
		}
	}
	if t.Scroll.Y < 1e8 && t.Scroll.Y < st.LastScrollY-2 {
		st.Follow = false
		st.FollowPending = false
	}
	if !st.Follow && t.Scroll.MaxY > 0 && t.Scroll.Y >= t.Scroll.MaxY-2 {
		st.Follow = true
	}
	if t.Scroll.Y < 1e8 {
		st.LastScrollY = t.Scroll.Y
	}
	if st.Follow && st.FollowPending {
		t.Scroll.Y = math.MaxFloat32
		st.FollowPending = false
	}
	inlineInset := float32(28)
	if a.prefs.RailExpanded {
		inlineInset = 44
		if width <= 1000 {
			inlineInset = 36
		}
	}

	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Scroll(c.Key(t.ID + "-messages")).Label("聊天滚动区域").TrackScroll(&t.Scroll).Grow(1).MinHeight(0).HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputScroll && ev.DY < 0 {
				st.Follow = false
				st.FollowPending = false
			}
			return false
		}).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(chatMaxWidth).AlignSelf(ui.Center).Padding(18, inlineInset, 14).Gap(10).Children(func() {
				if len(t.Messages) == 0 || !hasWelcomeMessage(t) {
					a.welcomeView(c, t)
				}
				for idx := range t.MD {
					if idx < start || idx >= end {
						delete(t.MD, idx)
					}
				}
				if start > 0 {
					olderCount := historyBlockIndex(historyBlockStarts(t), start)
					transcriptAction(c, "history-older", fmt.Sprintf("更早消息（%d 条）", olderCount), "history").OnClick(func() {
						st.VisibleStart = previousHistoryPage(t, start, a.messageWindowSize()) + 1
						st.Follow = false
						t.Scroll.Y = 0
					})
				}
				if end < len(t.Messages) {
					transcriptAction(c, "history-newer", "更新消息").OnClick(func() { st.VisibleStart = end + 1; t.Scroll.Y = 0 })
					transcriptAction(c, "history-latest", "回到最新").OnClick(func() { st.VisibleStart = 0; st.Follow = true; st.FollowPending = true })
				}
				for i := start; i < end; i++ {
					a.messageRow(c, t, i)
				}
				if st.PendingPrompt {
					c.After(16 * time.Millisecond)
				}
				ui.Box(c).Height(4)
			})
		})
		if !st.Follow && t.Scroll.Y < t.Scroll.MaxY-40 {
			ui.Box(c).Absolute().Right(18).Bottom(12).Children(func() {
				ui.Column(c).Gap(gapTight).Children(func() {
					iconAction(c, "chat-jump-up", "跳到上一条提问", "arrowup", 28, func() {
						a.jumpToPreviousPrompt(c, t)
					})
					iconAction(c, "chat-jump-down", "回到最新", "arrowdown", 28, func() {
						st.Follow = true
						st.FollowPending = true
					})
				})
			})
		}
		if st.SessionsOpen || len(a.plans[t.ID]) > 0 {
			ui.Column(c).FillWidth().Shrink(0).Padding(0, 20).Children(func() {
				if st.SessionsOpen {
					a.sessionsMenu(c, t)
				} else {
					a.planPanel(c, t)
				}
			})
		}
	})
}

func (a *desktop) jumpToPreviousPrompt(c *ui.Context, t *conversation) {
	st := t.uiState()
	viewport := t.Scroll.Bounds(c)
	if viewport.H <= 0 {
		return
	}
	targetIndex := -1
	targetY := float32(-math.MaxFloat32)
	for idx, handle := range st.PromptHandles {
		if handle == nil {
			continue
		}
		bounds := handle.Bounds(c)
		if bounds.H <= 0 || bounds.Y >= viewport.Y-12 {
			continue
		}
		if bounds.Y > targetY {
			targetIndex, targetY = idx, bounds.Y
		}
	}
	if targetIndex >= 0 {
		st.Follow = false
		st.FollowPending = false
		t.Scroll.Y = max(float32(0), t.Scroll.Y+targetY-viewport.Y-12)
		return
	}
	start, _ := a.chatMessageWindow(t)
	for i := start - 1; i >= 0; i-- {
		if str(t.Messages[i], "role") == "user" {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		return
	}
	st.Follow = false
	st.FollowPending = false
	st.LastScrollY = 0
	st.VisibleStart = max(0, targetIndex-min(80, a.messageWindowSize()/2)) + 1
	st.PendingPromptIdx = targetIndex
	st.PendingPrompt = true
	t.Scroll.Y = 0
}

func (a *desktop) messageRow(c *ui.Context, t *conversation, i int) {
	m := t.Messages[i]
	start, _ := a.chatMessageWindow(t)
	if i > start && sameHistoryBlock(t.Messages[i-1], m) {
		st := t.uiState()
		if idx, ok := st.RoundAt[i]; ok {
			a.roundStatsRow(c, t, idx, st.Round[idx], false)
		}
		return
	}
	role := str(m, "role")
	st := t.uiState()
	row := ui.Column(c.Key(fmt.Sprintf("message-%s-%d", t.ID, i))).FillWidth().MinWidth(0).Gap(10)
	if role == "user" {
		if st.PromptHandles == nil {
			st.PromptHandles = map[int]*ui.Handle{}
		}
		handle := st.PromptHandles[i]
		if handle == nil {
			handle = &ui.Handle{}
			st.PromptHandles[i] = handle
		}
		row.Bind(handle)
	}
	showStats := row.Hovered()
	row.Children(func() {
		switch {
		case role == "user":
			a.userMessage(c, t, i, m)
		case role == "assistant":
			if _, ok := m["welcome"]; ok {
				a.welcomeView(c, t)
				return
			}
			a.assistantMessage(c, t, i, m)
		case role == "notice":
			tone := str(m, "tone")
			if tone == "" {
				tone = "warn"
			}
			notice(c, tone, str(m, "content"), a.secondaryInk())
		case role == "tool_call" || str(m, "toolCallId") != "":
			// Adjacent read/grep/list cards fold into one group row, mirroring
			// main's read-grep-group: only the group's first row renders, the
			// rest are consumed by it.
			start, _ := a.chatMessageWindow(t)
			if isFoldableReadTool(m) && i > start {
				if prev := t.Messages[i-1]; isFoldableReadTool(prev) &&
					(str(prev, "role") == "tool_call" || str(prev, "toolCallId") != "") {
					break
				}
			}
			if isFoldableReadTool(m) {
				a.readGrepGroupCard(c, t, i)
				break
			}
			if isFoldableCommand(m) {
				a.commandGroupCard(c, t, i)
				break
			}
			a.toolCard(c, t, i, m)
		case role == "subagent":
			if subagentParent(t, m) != "" {
				return
			}
			a.subagentRow(c, t, i, m)
		default:
			if content := str(m, "content"); content != "" {
				markdown.View(c, content, a.markdownOptions(t, i)).FillWidth()
			}
		}
		if idx, ok := st.RoundAt[i]; ok {
			a.roundStatsRow(c, t, idx, st.Round[idx], showStats)
		}
	})
}

func (a *desktop) markdownOptions(t *conversation, i int) *markdown.Options {
	if t.MD == nil {
		t.MD = map[int]*markdown.Options{}
	}
	if t.MD[i] == nil {
		t.MD[i] = a.readingMarkdownOptions(fmt.Sprintf("%s:markdown:%d", t.ID, i))
	}
	if t.MD[i].Theme == nil {
		t.MD[i].Theme = &markdown.Theme{}
	}
	a.configureReadingMarkdown(t.MD[i])
	return t.MD[i]
}

// userMessage keeps prompts in a right-aligned bubble with hover actions below.
func (a *desktop) userMessage(c *ui.Context, t *conversation, i int, m map[string]any) {
	content := str(m, "content")
	p := a.currentPalette()
	services := c.Services()
	message := ui.Column(c.Key(fmt.Sprintf("user-bubble-%s-%d", t.ID, i))).Label("用户消息区域").AlignSelf(ui.End).AlignItems(ui.End).MaxWidthPercent(85).MinWidth(0).Gap(4)
	message.Children(func() {
		background := p.accent.Alpha(0.10)
		if p.dark {
			background = userBubbleDark
		}
		ui.Column(c.Key("bubble")).Label("用户消息").Selectable().MinWidth(0).Padding(10, 16).Radius(16).Background(background).Children(func() {
			ui.Column(c).MinWidth(0).Gap(gapInline).Children(func() {
				if str(m, "skill") != "" || str(m, "skillArgs") != "" {
					ui.Row(c).AlignItems(ui.Center).Gap(gapInline).Children(func() {
						if skill := str(m, "skill"); skill != "" {
							accentChip(c, "/"+skill)
						}
						if args := str(m, "skillArgs"); args != "" && args != content {
							ui.Text(c, args).FontSize(fsSmall).TextColor(a.secondaryInk())
						}
					})
				}
				a.collapsiblePrompt(c, t, i, content)
				a.messageAttachments(c, t, m)
			})
		})
		// Keep the action row in the hover area and reserve its space so moving
		// from the bubble to a button neither hides it nor shifts the transcript.
		show := message.Hovered() || message.FocusWithin()
		opacity := float32(0)
		if show {
			opacity = 1
		}
		actions := ui.Row(c.Key("actions")).Label("用户消息操作").Height(26).Gap(4).Shrink(0)
		actions.Opacity(actions.Animate("visibility", opacity, uikit.HoverDuration)).Children(func() {
			mutedIconAction(c, "copy", "复制提问", "copy", 26, func() {
				services.WriteClipboard(content)
			}).Disabled(!show).Shrink(0)
			dangerIconAction(c, "delete", "删除该提问及之后", "trash", 26, func() {
				a.requestDeleteUserMessage(t, i)
			}).Disabled(!show || t.Busy).Shrink(0)
		})
	})
}

func lastUserIndex(messages []map[string]any) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if str(messages[i], "role") == "user" {
			return i
		}
	}
	return -1
}

// collapsiblePrompt keeps very long questions readable: the first lines show,
// the rest expands on demand.
func (a *desktop) collapsiblePrompt(c *ui.Context, t *conversation, i int, content string) {
	lines := strings.Count(content, "\n") + 1
	long := len([]rune(content)) > 400 || lines > 6
	st := t.uiState()
	shown := content
	if long && !st.PromptOpen[i] {
		cut := content
		if r := []rune(cut); len(r) > 400 {
			cut = string(r[:400]) + "…"
		}
		ls := strings.Split(cut, "\n")
		if len(ls) > 6 {
			cut = strings.Join(ls[:6], "\n") + "\n…"
		}
		shown = cut
	}
	v := ui.Column(c).Key(fmt.Sprintf("prompt-%s-%d", t.ID, i)).Gap(gapInline)
	v.Children(func() {
		// Share selection with the bubble instead of installing a separate
		// Markdown selection scope.
		options := a.markdownOptions(t, i)
		options.ShareSelection = true
		markdown.View(c, shown, options).FillWidth()
		if long {
			labelText := fmt.Sprintf("展开完整提问（%d 行，%d 字）", lines, len([]rune(content)))
			if st.PromptOpen[i] {
				labelText = "收起提问"
			}
			button(c, nil, labelText, btnSecondary, false, "chat").AlignSelf(ui.Start).OnClick(func() { st.PromptOpen[i] = !st.PromptOpen[i] })
		}
	})
}

func (a *desktop) assistantMessage(c *ui.Context, t *conversation, i int, m map[string]any) {
	content := str(m, "content")
	attachments := messageAttachmentList(m)
	if content == "" && len(attachments) == 0 {
		return
	}
	if len(attachments) > 0 {
		a.messageAttachments(c, t, m)
	}
	if content == "" {
		return
	}
	isError := m["error"] != nil
	if isError {
		if userCancelledMessage(m) {
			cancelledResult(c, a.secondaryInk())
			return
		}
		notice(c, "error", content, a.secondaryInk())
		return
	}
	markdown.View(c.Key(fmt.Sprintf("md-%s-%d", t.ID, i)), content, a.markdownOptions(t, i)).FillWidth()
	if suggestions, ok := m["suggestions"].([]any); ok && len(suggestions) > 0 {
		ui.Row(c).Gap(gapInline).Wrap().Children(func() {
			for n, s := range suggestions {
				text := fmt.Sprint(s)
				suggestionButton(c.Key(fmt.Sprintf("suggest-%d-%d", i, n)), short(text, 40), a.secondaryInk()).OnClick(func() {
					a.send(t, text)
				})
			}
		})
	}
}

func (a *desktop) roundStatsRow(c *ui.Context, t *conversation, i int, stat *roundStat, show bool) {
	seconds := float64(stat.DurationMS) / 1000
	row := ui.Row(c.Key(fmt.Sprintf("round-stats-%s-%d", t.ID, i))).FillWidth().Height(heightRow + 2).Gap(gapInline + 1).AlignItems(ui.Center)
	// The controls stay in the view, faded and inert, while the row is not
	// hovered. Removing them instead took away the button a panel is anchored
	// to, and the panel lives in the window's overlay: it vanished the moment
	// the pointer moved onto it, so its entries could never be chosen.
	visible := float32(0)
	if show {
		visible = 1
	}
	row.Opacity(visible).Disabled(!show)
	row.Children(func() {
		uikit.Tooltip(c, ui.Text(c, fmtDur(stat.DurationMS)).FontSize(fsSmall).TextColor(a.secondaryInk()), "本轮耗时")
		if hit := stat.CacheHit + stat.CacheMiss; hit > 0 {
			uikit.Tooltip(c, ui.Text(c, fmt.Sprintf("cache %d%%", int(float64(stat.CacheHit)*100/float64(hit)))).FontSize(fsSmall).
				TextColor(a.secondaryInk()), fmt.Sprintf("cache hit %d / miss %d", stat.CacheHit, stat.CacheMiss))
		}
		if stat.InputTokens > 0 || stat.OutputTokens > 0 {
			uikit.Tooltip(c, ui.Text(c, fmt.Sprintf("↑%s ↓%s", fmtK(stat.InputTokens), fmtK(stat.OutputTokens))).FontSize(fsSmall).
				TextColor(a.secondaryInk()), fmt.Sprintf("input %d / output %d tokens", stat.InputTokens, stat.OutputTokens))
		}
		if seconds > 0 && stat.OutputTokens > 0 {
			ui.Text(c, fmt.Sprintf("%d token/s", int(float64(stat.OutputTokens)/seconds))).FontSize(fsSmall).TextColor(a.secondaryInk())
		}
		services := c.Services()
		exportButton := mutedIconAction(c, fmt.Sprintf("export-%d", i), "导出", "download", 22, nil)
		menuPopoverButton(c, exportButton, 180, func(dismiss func()) {
			popoverItem(c, fmt.Sprintf("export-answer-%d", i), "导出回答", "", false, func() {
				dismiss()
				a.exportText("ally-response.md", str(t.Messages[i], "content"))
			}, a.secondaryInk())
			popoverItem(c, fmt.Sprintf("export-session-%d", i), "导出会话", "", false, func() {
				dismiss()
				a.exportText("ally-session.md", sessionMarkdown(t))
			}, a.secondaryInk())
		})
		mutedIconAction(c, fmt.Sprintf("copy-summary-%d", i), "复制总结", "copy", 22, func() {
			services.WriteClipboard(str(t.Messages[i], "content"))
			c.Toast("已复制最终总结")
		})
		quickButton := mutedIconAction(c, fmt.Sprintf("quick-%d", i), "快捷消息", "wand", 22, nil)
		menuPopoverButton(c, quickButton, 180, func(dismiss func()) {
			for _, item := range quickMessages {
				item := item
				popoverItem(c, fmt.Sprintf("quick-%d-%s", i, item.label), item.label, "", false, func() {
					dismiss()
					a.send(t, item.prompt)
				}, a.secondaryInk())
			}
		})
		mutedIconAction(c, fmt.Sprintf("remember-%d", i), "记住这次对话的长期记忆", "book", 22, func() {
			a.send(t, projectMemoryPrompt+"\n\nLocal date for this memory update: "+time.Now().Format("2006-01-02")+".")
		})
	})
}

var quickMessages = []struct{ label, prompt string }{
	{"继续", "继续"},
	{"说人话", "请说人话：去掉套话和术语，直接讲结论和原因。"},
	{"提交并推送", "请检查改动并提交，然后推送到远端。"},
	{"代码审查", "请审查当前工作区最近的改动，指出真实缺陷与回归风险。"},
	{"记录教训", "请总结这一轮里容易踩坑的地方，并更新 LESSONS.md。"},
}

const projectMemoryPrompt = "请把这次对话里值得长期记住的项目事实、约定与踩坑写进项目记忆（~/.ally_agent/memories/project-knowledge/），只写可复用的事实。"

func (a *desktop) exportText(name, content string) {
	a.job(func() error {
		path, err := a.core.ExportTextFile(name, content, "Markdown", "*.md")
		if err == nil && path != "" {
			a.win.Update(func() { a.notify("已导出：" + path) })
		}
		return err
	})
}

func sessionMarkdown(t *conversation) string {
	var b strings.Builder
	b.WriteString("# " + t.Title + "\n\n")
	for _, m := range t.Messages {
		role := str(m, "role")
		content := str(m, "content")
		if content == "" {
			continue
		}
		switch role {
		case "user":
			b.WriteString("## 你\n\n" + content + "\n\n")
		case "assistant":
			b.WriteString("## Ally\n\n" + content + "\n\n")
		}
	}
	return b.String()
}

func (a *desktop) requestDeleteUserMessage(t *conversation, index int) {
	if t == nil || t.Busy || index < 0 || index >= len(t.Messages) || str(t.Messages[index], "role") != "user" {
		return
	}
	content := str(t.Messages[index], "content")
	ordinal := 0
	for i := 0; i < index && i < len(t.Messages); i++ {
		if str(t.Messages[i], "role") == "user" {
			ordinal++
		}
	}
	a.deletePrompt = &deletePromptState{
		Open:         true,
		Conversation: t,
		MessageIndex: index,
		UserIndex:    ordinal,
		Content:      content,
	}
}

func (a *desktop) deleteUserMessageDialog(c *ui.Context) {
	d := a.deletePrompt
	if d == nil {
		return
	}
	modalCard(c, &d.Open, widthModalSmall, func() {
		modalTitle(c, "删除该提问及之后？", "")
		ui.Text(c, "将删除此提问及之后的所有对话，并同步清理模型上下文。此操作不可恢复。").
			FontSize(fsSmall).TextColor(a.secondaryInk()).LineHeight(1.5).Selectable()
		modalActions(c, func() {
			flat(c, "取消").OnClick(func() { d.Open = false })
			dangerButton(c, "删除").OnClick(func() { a.confirmDeleteUserMessage(d) })
		})
	})
}

func (a *desktop) confirmDeleteUserMessage(d *deletePromptState) {
	if d == nil || !d.Open {
		return
	}
	d.Open = false
	t := d.Conversation
	index := d.MessageIndex
	if t == nil || t.Busy || index < 0 || index >= len(t.Messages) || str(t.Messages[index], "content") != d.Content {
		a.notify("对话已变化，请重新选择要删除的提问。")
		return
	}
	request := backend.TruncateSessionHistoryRequest{SessionID: t.ID, UserMessageIndex: d.UserIndex, ExpectedContent: d.Content}
	go func() {
		if _, syncErr := a.core.TruncateSessionHistory(request); syncErr != nil {
			a.win.Update(func() { a.notifyError("界面已删除，但模型上下文同步失败：" + syncErr.Error()) })
		}
		a.win.Update(func() {
			if index <= len(t.Messages) {
				t.Messages = append([]map[string]any(nil), t.Messages[:index]...)
			}
			releaseCommandDetails(t)
			t.uiState().Round = map[int]*roundStat{}
			a.persist(t)
		})
	}()
}

// ---- Welcome -------------------------------------------------------------

func (a *desktop) welcomeView(c *ui.Context, t *conversation) {
	availableSkills := a.enabledSkillNames()
	now := time.Now()
	greeting := "上午好"
	switch {
	case now.Hour() < 6:
		greeting = "夜深了"
	case now.Hour() < 12:
		greeting = "上午好"
	case now.Hour() < 14:
		greeting = "中午好"
	case now.Hour() < 18:
		greeting = "下午好"
	default:
		greeting = "晚上好"
	}
	ui.Column(c).Key("welcome").Padding(36, 0, 12).Gap(gapPage).Children(func() {
		ui.Row(c).Gap(22).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Width(170).AlignItems(ui.Center).Gap(gapSection).Children(func() {
				img := ui.Column(c).Size(150, 150).Center()
				img.Children(func() { a.avatarDrawing(c) })
				uikit.Tooltip(c, img.Cursor(ui.CursorPointer), "点击头像：切换樱花特效")
				if img.Clicked() {
					a.togglePetals()
				}
				ui.Text(c, "Ally Native · 0.1.0").FontSize(fsTiny).TextColor(a.secondaryInk())
			})
			ui.Column(c).Grow(1).Border(1, surfaceBorder(c)).Radius(radiusCard).Children(func() {
				rows := [][4]string{
					{"工作区", workspaceLabel(t.Workspace), "Bash", a.config.GitBashPath},
					{"模型", strings.ReplaceAll(t.Model, " / ", " · "), "MCP", a.mcpSummary},
					{"技能", fmt.Sprintf("%d 个可用", len(availableSkills)), "工具", fmt.Sprintf("%d 个工具", a.toolCount)},
				}
				if rows[0][3] == "" {
					rows[0][3] = "自动检测"
				}
				if rows[1][3] == "" {
					rows[1][3] = "无 MCP 服务"
				}
				for i, row := range rows {
					ui.Row(c).Key(fmt.Sprintf("welcome-row-%d", i)).MinHeight(40).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).AlignItems(ui.Center).Children(func() {
						for j := 0; j < 2; j++ {
							ui.Box(c).Width(64).MinHeight(40).Padding(10, 8).BorderWidth(0, 1, 0, 0).BorderColor(c.Theme().Border).Children(func() {
								ui.Text(c, row[j*2]).FontSize(fsSmall).Bold()
							})
							value := row[j*2+1]
							cell := ui.Box(c).MinHeight(40).Padding(10, 8).BorderWidth(0, 1, 0, 0).BorderColor(c.Theme().Border).Clip()
							if j == 0 {
								cell.Grow(1.4).Basis(0)
							} else {
								cell.Grow(1).Basis(0)
							}
							var open *bool
							switch {
							case i == 1 && j == 1:
								open = &a.welcomeMCP
							case i == 2 && j == 0:
								open = &a.welcomeSkills
							case i == 2 && j == 1:
								open = &a.welcomeTools
							}
							if open != nil {
								cell.Cursor(ui.CursorPointer)
								if cell.Clicked() {
									*open = !*open
								}
							}

							cell.Children(func() {
								e := uikit.Tooltip(c, ui.Text(c, value).FontSize(fsSmall).SingleLine(), value)
								if i == 2 || (i == 1 && j == 1) {
									e.TextColor(c.Theme().Accent)
								}
							})
							if open != nil {
								a.welcomePopover(c, cell, open, i, j)
							}
						}
					})
				}
			})
		})
		ui.Text(c, fmt.Sprintf("今天是 %d月%d日 %s，现在是 %02d:%02d。%s，我们把注意力放回眼前，先解决最重要的一件事。",
			now.Month(), now.Day(), []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[now.Weekday()], now.Hour(), now.Minute(), greeting)).
			FontSize(15).Bold().LineHeight(1.8)
	})
}

func (a *desktop) welcomePopover(c *ui.Context, anchor ui.Element, open *bool, row, col int) {
	popoverPanel(c, anchor, open, 320, func() {
		title := "MCP 服务"
		if row == 2 && col == 0 {
			title = "可用技能"
		} else if row == 2 {
			title = "工具"
		}
		ui.Text(c, title).FontSize(fsSection).Bold().Padding(6, 10)
		ui.Scroll(c).MaxHeight(260).Children(func() {
			ui.Column(c).Gap(3).Padding(4, 10).Children(func() {
				switch {
				case row == 1:
					if len(a.mcpServers) == 0 {
						fieldNote(c, "无 MCP 服务", a.secondaryInk())
					}
					for _, server := range a.mcpServers {
						ui.Text(c, fmt.Sprintf("%s · %s · %d 个工具", str(server, "name"), mcpStatusText(str(server, "status")), int(num(server, "toolCount")))).FontSize(fsSmall).TextColor(a.secondaryInk())
					}
				case col == 0:
					names := a.enabledSkillNames()
					if len(names) == 0 {
						fieldNote(c, "当前没有可用技能", a.secondaryInk())
					}
					for _, name := range names {
						ui.Text(c, name).FontSize(fsSmall)
					}
				default:
					if len(a.toolInventory) == 0 {
						fieldNote(c, "暂无工具", a.secondaryInk())
					}
					for _, tool := range a.toolInventory {
						suffix := ""
						if !tool.Enabled {
							suffix = "（已停用）"
						}
						ui.Text(c, tool.Name+suffix).FontSize(fsSmall)
					}
				}
			})
		})
		divider(c)
		label, page := "管理 MCP 服务", "mcp"
		if row == 2 && col == 0 {
			label, page = "管理技能", "skills"
		} else if row == 2 {
			label = "管理内置工具"
		}
		popoverItem(c, fmt.Sprintf("welcome-manage-%d-%d", row, col), label, "", false, func() {
			*open = false
			a.changePage(page)
			if row == 2 && col == 1 {
				a.mcpF.Tab = 1
			}
		}, a.secondaryInk())
	})
}

func (a *desktop) enabledSkillNames() []string {
	var names []string
	for _, s := range a.skills {
		disabled := false
		for _, name := range a.config.DisabledSkills {
			if strings.EqualFold(name, s.Name) {
				disabled = true
				break
			}
		}
		if !disabled {
			names = append(names, s.Name)
		}
	}
	return names
}

func mcpStatusText(status string) string {
	switch status {
	case "connected":
		return "已连接"
	case "connecting":
		return "连接中"
	case "failed", "error":
		return "连接失败"
	case "disabled":
		return "已停用"
	}
	return "未连接"
}

// ---- Plan panel ----------------------------------------------------------

func (a *desktop) planPanel(c *ui.Context, t *conversation) {
	steps := a.plans[t.ID]
	if len(steps) == 0 {
		return
	}
	st := t.uiState()
	done := 0
	for _, s := range steps {
		if s.Status == "done" {
			done++
		}
	}
	// The disclosure is MyGo's CollapsibleBase: it owns opening and closing, the
	// arrow's progress and the expanded state assistive technology reads, so this
	// only supplies the look of the header and the step list.
	open := !st.PlanCollapsed
	ui.Column(c).Key("plan-"+t.ID).FillWidth().Border(1, c.Theme().Border).Radius(radiusCard).Background(c.Theme().Surface).Children(func() {
		parts := ui.CollapsibleBase(c, &open)
		parts.Trigger.FillWidth().Height(30).Padding(0, 10).Cursor(ui.CursorPointer).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(gapField).Children(func() {
				ui.Text(c, "plan").FontSize(fsSmall).Bold().TextColor(a.secondaryInk())
				ui.Text(c, fmt.Sprintf("%d/%d", done, len(steps))).FontSize(fsTiny).TextColor(a.secondaryInk())
				ui.Spacer(c)
				iconGlyph(c, "chevrondown", 13).Rotate(-90 * (1 - parts.Progress()))
			})
		})
		parts.Panel(func() {
			ui.Scroll(c).Height(150).Padding(0, 6, 4).Gap(6).Children(func() {
				for n, step := range steps {
					mark := "○"
					color := a.secondaryInk()
					switch step.Status {
					case "done":
						mark = "✓"
						color = c.Theme().Success
					case "in_progress":
						mark = "●"
						color = c.Theme().Accent
					}
					ui.Row(c).Key(fmt.Sprintf("plan-step-%d", n)).Padding(2, 8).Gap(gapField).Children(func() {
						ui.Text(c, mark).FontSize(fsTiny).TextColor(color)
						ui.Text(c, fmt.Sprintf("%d.", n+1)).FontSize(fsTiny).TextColor(a.secondaryInk())
						// Finished steps read as struck through, the way main's
						// .plan-item.done .plan-title does.
						finished := step.Status == "done"
						titleColor := c.Theme().Text
						if finished {
							titleColor = a.secondaryInk()
						}
						ui.RichText(c, ui.Span{Text: step.Title, Size: fsSmall, Color: titleColor, Strikethrough: finished, DecorationColor: titleColor})
					})
				}
			})
		})
		if open != !st.PlanCollapsed {
			st.PlanCollapsed = !open
		}
	})
}

// ---- Ask form ------------------------------------------------------------

func (a *desktop) askComposer(c *ui.Context, t *conversation) {
	st := t.uiState()
	if len(t.Questions) == 0 {
		return
	}
	index := st.AskIndex
	if index < 0 || index >= len(t.Questions) {
		index = 0
	}
	q := t.Questions[index]
	answered := 0
	for _, question := range t.Questions {
		if len(st.AskSelected[question.ID]) > 0 || (st.AskCustom[question.ID] && strings.TrimSpace(st.AskCustomTxt[question.ID]) != "") {
			answered++
		}
	}
	p := a.currentPalette()
	ui.Column(c).Shrink(0).Margin(0, 0, 8).Padding(14).Gap(gapSection).Border(1, c.Theme().Border).
		Background(p.panel).Radius(radiusCard).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(gapField).Children(func() {
			statusDot(c, "warn")
			ui.Text(c, "Ally 需要你的输入").Bold().FontSize(fsBody)
			neutralChip(c, fmt.Sprintf("%d 个问题", len(t.Questions)))
			ui.Spacer(c)
			fieldNote(c, fmt.Sprintf("%d/%d 已回答", answered, len(t.Questions)), a.secondaryInk())
		})
		ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
			iconAction(c, "ask-prev", "上一题", "chevron", 22, func() {
				if st.AskIndex > 0 {
					st.AskIndex--
				}
			}).Disabled(index == 0)
			for n := range t.Questions {
				n := n
				choiceButton(c, fmt.Sprintf("ask-tab-%d", n), fmt.Sprint(n+1), n == index, c.Theme().Accent).
					OnClick(func() { st.AskIndex = n })
			}
			iconAction(c, "ask-next", "下一题", "chevron", 22, func() {
				if st.AskIndex < len(t.Questions)-1 {
					st.AskIndex++
				}
			}).Disabled(index >= len(t.Questions)-1)
		})
		ui.Text(c, q.Question).FontSize(fsBody).Bold()
		ui.Column(c).Gap(gapInline).Children(func() {
			for _, opt := range q.Options {
				opt := opt
				checked := containsString(st.AskSelected[q.ID], opt.ID)
				ui.Column(c.Key("ask-option-card-"+q.ID+"-"+opt.ID)).FillWidth().Padding(8, 10).Gap(4).Radius(radiusSmall).Border(1, surfaceBorder(c)).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
						box := ui.Checkbox(c.Key("ask-opt-"+q.ID+"-"+opt.ID), &checked, opt.Label)
						if box.Changed() {
							st.AskSelected[q.ID] = toggleString(st.AskSelected[q.ID], opt.ID, checked)
						}
						if opt.Recommended {
							accentChip(c, "推荐")
						}
					})
					if opt.Description != "" {
						ui.Text(c, opt.Description).PaddingStart(22).FontSize(fsTiny).TextColor(a.secondaryInk())
					}
				})
			}
			custom := st.AskCustom[q.ID]
			ui.Checkbox(c.Key("ask-custom-"+q.ID), &custom, "自定义回答")
			st.AskCustom[q.ID] = custom
			if custom {
				txt := st.AskCustomTxt[q.ID]
				uikit.Area(c.Key("ask-custom-text-"+q.ID), &txt).Placeholder("输入你的回答").Height(70)
				st.AskCustomTxt[q.ID] = txt
			}
		})
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			disabled := answered < len(t.Questions) || st.AskSubmitted
			primary(c, "提交回答").Disabled(disabled).OnClick(func() { a.submitAsk(t) })
		})
	})
}

func containsString(items []string, v string) bool {
	for _, item := range items {
		if item == v {
			return true
		}
	}
	return false
}

func toggleString(items []string, v string, on bool) []string {
	if on {
		if !containsString(items, v) {
			return append(items, v)
		}
		return items
	}
	out := items[:0]
	for _, item := range items {
		if item != v {
			out = append(out, item)
		}
	}
	return out
}

func (a *desktop) submitAsk(t *conversation) {
	st := t.uiState()
	req := backend.AskSubmitRequest{AskID: t.AskID, SessionID: t.ID}
	for _, q := range t.Questions {
		answer := backend.AskSubmittedAnswer{QuestionID: q.ID}
		if st.AskCustom[q.ID] {
			answer.CustomText = strings.TrimSpace(st.AskCustomTxt[q.ID])
		}
		answer.SelectedOptionIDs = append(answer.SelectedOptionIDs, st.AskSelected[q.ID]...)
		req.Answers = append(req.Answers, answer)
	}
	st.AskSubmitted = true
	a.job(func() error {
		err := a.core.SubmitAskResponse(req)
		if err != nil {
			a.win.Update(func() { st.AskSubmitted = false })
		}
		return err
	})
}

func hasWelcomeMessage(t *conversation) bool {
	for _, m := range t.Messages {
		if _, ok := m["welcome"]; ok {
			return true
		}
	}
	return false
}

func hasLaterSuggestion(t *conversation, index int) bool {
	for n := index + 1; n < len(t.Messages); n++ {
		m := t.Messages[n]
		if str(m, "role") == "user" {
			return false
		}
		if toolKind(str(m, "name")) == "suggest" {
			return true
		}
	}
	return false
}

func roundStatsIndex(t *conversation, index int) int {
	target := index
	for n := index + 1; n < len(t.Messages); n++ {
		m := t.Messages[n]
		if str(m, "role") == "user" {
			break
		}
		if toolKind(str(m, "name")) == "suggest" {
			target = n
		}
	}
	return target
}
func normalizeHistoryPageSize(size int) int {
	if size < 20 || size > 200 {
		return 80
	}
	return size
}
func (a *desktop) messageWindowSize() int { return normalizeHistoryPageSize(a.prefs.HistoryPageSize) }
func (a *desktop) chatMessageWindow(t *conversation) (int, int) {
	st := t.uiState()
	if st.windowBuilding {
		return st.windowStart, st.windowEnd
	}
	return historyMessageWindow(t, a.messageWindowSize())
}

// Pagination and row rendering share this grouping rule. Only adjacent tool
// records combine; an ordinary message always starts its own visible block.
func sameHistoryBlock(left, right map[string]any) bool {
	tool := func(m map[string]any) bool { return str(m, "role") == "tool_call" || str(m, "toolCallId") != "" }
	return tool(left) && tool(right) && ((isFoldableReadTool(left) && isFoldableReadTool(right)) || (isFoldableCommand(left) && isFoldableCommand(right)))
}
func historyBlockStarts(t *conversation) []int {
	starts := make([]int, 0, len(t.Messages)+1)
	for i := range t.Messages {
		if i == 0 || !sameHistoryBlock(t.Messages[i-1], t.Messages[i]) {
			starts = append(starts, i)
		}
	}
	return append(starts, len(t.Messages))
}
func historyMessageWindow(t *conversation, size int) (int, int) {
	size = normalizeHistoryPageSize(size)
	starts := historyBlockStarts(t)
	blocks := len(starts) - 1
	first := max(0, blocks-size)
	if t.uiState().VisibleStart > 0 {
		first = historyBlockIndex(starts, min(len(t.Messages), t.uiState().VisibleStart-1))
	}
	return starts[first], starts[min(blocks, first+size)]
}
func historyBlockIndex(starts []int, index int) int {
	// Upper bound keeps a restored index inside a tool group on its header.
	lo, hi := 0, len(starts)
	for lo < hi {
		mid := (lo + hi) / 2
		if starts[mid] <= index {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return max(0, lo-1)
}
func previousHistoryPage(t *conversation, start, size int) int {
	starts := historyBlockStarts(t)
	return starts[max(0, historyBlockIndex(starts, start)-normalizeHistoryPageSize(size))]
}

// followLatest parks a transcript at its newest message. A scroll container
// shows its start at offset 0, so a history restored from disk — or replaced by
// a summary — would otherwise open on its oldest message.
func followLatest(t *conversation) {
	st := t.uiState()
	st.VisibleStart = 0
	st.Follow = true
	st.FollowPending = true
}

// Recognize explicit cancellation markers and legacy persisted cancellation text,
// without treating errors which merely mention cancellation as a user action.
func userCancelledMessage(m map[string]any) bool {
	for _, key := range []string{"status", "stopReason", "reason"} {
		switch strings.ToLower(strings.TrimSpace(str(m, key))) {
		case "cancelled", "canceled", "user_cancelled", "user_canceled":
			return true
		}
	}
	if boolOf(m, "cancelled") || boolOf(m, "canceled") {
		return true
	}
	for _, key := range []string{"content", "error"} {
		text := strings.TrimSpace(str(m, key))
		if text == "已取消" || text == "用户已取消" || text == "用户取消" ||
			strings.EqualFold(text, "cancelled by user") || strings.EqualFold(text, "canceled by user") {
			return true
		}
	}
	return false
}
func cancelledResult(c *ui.Context, ink ...ui.Color) {
	ui.Text(c, "用户已取消").FontSize(fsSmall).TextColor(uikit.SecondaryInk(c, ink)).Selectable()
}
