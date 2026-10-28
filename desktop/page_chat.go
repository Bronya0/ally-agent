// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/pkg/uikit"
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
	start, end := chatMessageWindow(t)
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
		ui.Scroll(c.Key(t.ID + "-messages")).TrackScroll(&t.Scroll).Grow(1).MinHeight(0).HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputScroll && ev.DY < 0 {
				st.Follow = false
				st.FollowPending = false
			}
			return false
		}).Children(func() {
			ui.Column(c).Padding(18, inlineInset, 14).Gap(8).Children(func() {
				if len(t.Messages) == 0 || !hasWelcomeMessage(t) {
					a.welcomeView(c, t)
				}
				for idx := range t.MD {
					if idx < start || idx >= end {
						delete(t.MD, idx)
					}
				}
				if start > 0 {
					flat(c, "查看更早消息").OnClick(func() { st.VisibleStart = max(0, start-140) + 1; st.Follow = false; t.Scroll.Y = 0 })
				}
				if end < len(t.Messages) {
					flat(c, "查看更新消息").OnClick(func() { st.VisibleStart = min(len(t.Messages)-1, start+140) + 1; t.Scroll.Y = 0 })
					flat(c, "回到最新消息").OnClick(func() { st.VisibleStart = 0; st.Follow = true; st.FollowPending = true })
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
	start, _ := chatMessageWindow(t)
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
	st.VisibleStart = max(0, targetIndex-80) + 1
	st.PendingPromptIdx = targetIndex
	st.PendingPrompt = true
	t.Scroll.Y = 0
}

func (a *desktop) messageRow(c *ui.Context, t *conversation, i int) {
	m := t.Messages[i]
	start, _ := chatMessageWindow(t)
	if isFoldableReadTool(m) && i > start && isFoldableReadTool(t.Messages[i-1]) &&
		(str(t.Messages[i-1], "role") == "tool_call" || str(t.Messages[i-1], "toolCallId") != "") {
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
			notice(c, "warn", str(m, "content"))
		case role == "tool_call" || str(m, "toolCallId") != "":
			// Adjacent read/grep/list cards fold into one group row, mirroring
			// main's read-grep-group: only the group's first row renders, the
			// rest are consumed by it.
			start, _ := chatMessageWindow(t)
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
		t.MD[i] = &markdown.Options{WrapCode: true}
	}
	if t.MD[i].Theme == nil {
		t.MD[i].Theme = &markdown.Theme{}
	}
	t.MD[i].Theme.Font = nativeFontFamily()
	if i >= 0 && i < len(t.Messages) && str(t.Messages[i], "role") == "user" {
		t.MD[i].Theme.Text = a.currentPalette().accent
	} else {
		t.MD[i].Theme.Text = a.currentPalette().text.Mix(ui.Hex("#ffffff"), 0.20)
	}
	t.MD[i].Theme.StrongWeight = 600
	t.MD[i].Theme.LineHeight = 1.75
	t.MD[i].Theme.BlockSpacing = 0.55
	t.MD[i].Theme.TableCellPadding = [2]float32{12 / 15.5, 8 / 15.5}
	t.MD[i].Theme.Strong = ui.Hex("#c4b894")
	t.MD[i].Theme.Code = ui.Hex("#d4a574")
	t.MD[i].Theme.CodeBackground = ui.RGBA(212, 165, 116, 0.035)
	if !a.currentPalette().dark {
		t.MD[i].Theme.Strong = a.currentPalette().text
		t.MD[i].Theme.Code = ui.Hex("#805326")
	}
	t.MD[i].Theme.CodeFont = monoFamily
	t.MD[i].Theme.FontSize = float32(clampView(a.config.MessageFontSize, 15.5))
	t.MD[i].Theme.CodeSize = float32(clampView(a.config.CodeFontSize, 14)) / t.MD[i].Theme.FontSize
	return t.MD[i]
}

// userMessage mirrors the full-width user tint and hanging caret. Hover-only deletion matches the
// Vue row; the delete button must not consume layout space.
func (a *desktop) userMessage(c *ui.Context, t *conversation, i int, m map[string]any) {
	content := str(m, "content")
	p := a.currentPalette()
	row := ui.Row(c).Margin(0, -28).Padding(10).Gap(6).AlignItems(ui.Start).Background(p.hover)
	row.Children(func() {
		ui.Text(c, "›").Width(12).Shrink(0).FontSize(15).Bold().TextColor(p.accent).LineHeight(1.7)
		ui.Column(c).Grow(1).Gap(gapInline).Children(func() {
			if str(m, "skill") != "" || str(m, "skillArgs") != "" {
				ui.Row(c).AlignItems(ui.Center).Gap(gapInline).Children(func() {
					if skill := str(m, "skill"); skill != "" {
						accentChip(c, "/"+skill)
					}
					if args := str(m, "skillArgs"); args != "" && args != content {
						ui.Text(c, args).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
					}
				})
			}
			a.collapsiblePrompt(c, t, i, content)
			a.messageAttachments(c, t, m)
		})
		// Hover-only delete, overlaid at the row corner: it must not take part
		// in the row flex layout (main pins it absolute), and it is available
		// on every question row, not just the last one.
		if !t.Busy && row.Hovered() {
			ui.Box(c).Absolute().Top(0).Bottom(0).Right(8).Width(22).Center().Children(func() {
				iconAction(c, fmt.Sprintf("del-user-%s-%d", t.ID, i), "删除该提问及之后", "close", 20, func() {
					a.requestDeleteUserMessage(t, i)
				}).TextColor(c.Theme().TextMuted)
			})
		}
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
		markdown.View(c, shown, a.markdownOptions(t, i)).FillWidth()
		if long {
			labelText := fmt.Sprintf("展开完整提问（%d 行，%d 字）", lines, len([]rune(content)))
			if st.PromptOpen[i] {
				labelText = "收起提问"
			}
			flat(c, labelText).AlignSelf(ui.Start).OnClick(func() { st.PromptOpen[i] = !st.PromptOpen[i] })
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
		notice(c, "error", content)
		return
	}
	markdown.View(c.Key(fmt.Sprintf("md-%s-%d", t.ID, i)), content, a.markdownOptions(t, i)).FillWidth()
	if suggestions, ok := m["suggestions"].([]any); ok && len(suggestions) > 0 && !t.Busy {
		ui.Row(c).Gap(gapInline).Wrap().Children(func() {
			for n, s := range suggestions {
				text := fmt.Sprint(s)
				suggestionButton(c.Key(fmt.Sprintf("suggest-%d-%d", i, n)), short(text, 40)).OnClick(func() {
					a.send(t, text)
				})
			}
		})
	}
}

func (a *desktop) roundStatsRow(c *ui.Context, t *conversation, i int, stat *roundStat, show bool) {
	seconds := float64(stat.DurationMS) / 1000
	ui.Row(c.Key(fmt.Sprintf("round-stats-%s-%d", t.ID, i))).FillWidth().Height(heightRow + 2).Gap(gapInline + 1).AlignItems(ui.Center).Children(func() {
		if !show {
			return
		}
		ui.Text(c, fmtDur(stat.DurationMS)).FontSize(fsSmall).TextColor(c.Theme().TextMuted).Tooltip("本轮耗时")
		if hit := stat.CacheHit + stat.CacheMiss; hit > 0 {
			ui.Text(c, fmt.Sprintf("cache %d%%", int(float64(stat.CacheHit)*100/float64(hit)))).FontSize(fsSmall).
				TextColor(c.Theme().TextMuted).Tooltip(fmt.Sprintf("cache hit %d / miss %d", stat.CacheHit, stat.CacheMiss))
		}
		if stat.InputTokens > 0 || stat.OutputTokens > 0 {
			ui.Text(c, fmt.Sprintf("↑%s ↓%s", fmtK(stat.InputTokens), fmtK(stat.OutputTokens))).FontSize(fsSmall).
				TextColor(c.Theme().TextMuted).Tooltip(fmt.Sprintf("input %d / output %d tokens", stat.InputTokens, stat.OutputTokens))
		}
		if seconds > 0 && stat.OutputTokens > 0 {
			ui.Text(c, fmt.Sprintf("%d token/s", int(float64(stat.OutputTokens)/seconds))).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
		}
		services := c.Services()
		exportOpen := a.popupFlag(fmt.Sprintf("message-export-%s-%d", t.ID, i))
		export := mutedIconAction(c, fmt.Sprintf("export-%d", i), "导出", "download", 22, func() { *exportOpen = !*exportOpen })
		popoverPanel(c, export, exportOpen, 180, func() {
			popoverItem(c, "export-answer", "导出回答", "", false, func() { *exportOpen = false; a.exportText("ally-response.md", str(t.Messages[i], "content")) })
			popoverItem(c, "export-session", "导出会话", "", false, func() { *exportOpen = false; a.exportText("ally-session.md", sessionMarkdown(t)) })
		})
		mutedIconAction(c, fmt.Sprintf("copy-summary-%d", i), "复制总结", "copy", 22, func() {
			services.WriteClipboard(str(t.Messages[i], "content"))
			c.Toast("已复制最终总结")
		})
		quickOpen := a.popupFlag(fmt.Sprintf("message-quick-%s-%d", t.ID, i))
		quick := mutedIconAction(c, fmt.Sprintf("quick-%d", i), "快捷消息", "wand", 22, func() { *quickOpen = !*quickOpen })
		popoverPanel(c, quick, quickOpen, 180, func() {
			for idx, item := range quickMessages {
				item := item
				popoverItem(c, fmt.Sprintf("quick-item-%d", idx), item.label, "", false, func() { *quickOpen = false; a.send(t, item.prompt) })
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
		_, err := a.core.ExportTextFile(name, content, "Markdown", "*.md")
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
			FontSize(fsSmall).TextColor(c.Theme().TextMuted).LineHeight(1.5).Selectable()
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
		a.status = "对话已变化，请重新选择要删除的提问。"
		return
	}
	request := backend.TruncateSessionHistoryRequest{SessionID: t.ID, UserMessageIndex: d.UserIndex, ExpectedContent: d.Content}
	go func() {
		if _, syncErr := a.core.TruncateSessionHistory(request); syncErr != nil {
			a.win.Update(func() { a.status = "界面已删除，但模型上下文同步失败：" + syncErr.Error() })
		}
		a.win.Update(func() {
			if index <= len(t.Messages) {
				t.Messages = append([]map[string]any(nil), t.Messages[:index]...)
			}
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
				img.Children(func() {
					if bitmap := a.avatarImage(c); bitmap != nil {
						ui.Image(c, bitmap).Size(140, 140).Fit(ui.Contain)
					} else {
						ui.Image(c, eyeArtwork).Size(140, 140).Fit(ui.Contain)
					}
				})
				img.Cursor(ui.CursorPointer).Tooltip("点击头像：切换樱花特效")
				if img.Clicked() {
					a.togglePetals()
				}
				ui.Text(c, "Ally Native · 0.1.0").FontSize(fsTiny).TextColor(c.Theme().TextMuted)
			})
			ui.Column(c).Grow(1).Border(1, c.Theme().Border).Radius(radiusCard).Children(func() {
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
								e := ui.Text(c, value).FontSize(fsSmall).SingleLine().Tooltip(value)
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
						fieldNote(c, "无 MCP 服务")
					}
					for _, server := range a.mcpServers {
						ui.Text(c, fmt.Sprintf("%s · %s · %d 个工具", str(server, "name"), mcpStatusText(str(server, "status")), int(num(server, "toolCount")))).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
					}
				case col == 0:
					names := a.enabledSkillNames()
					if len(names) == 0 {
						fieldNote(c, "当前没有可用技能")
					}
					for _, name := range names {
						ui.Text(c, name).FontSize(fsSmall)
					}
				default:
					if len(a.toolInventory) == 0 {
						fieldNote(c, "暂无工具")
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
		})
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
	ui.Column(c).Key("plan-"+t.ID).FillWidth().Border(1, c.Theme().Border).Radius(radiusCard).Background(c.Theme().Surface).Children(func() {
		header := ui.ButtonBase(c.Key("plan-head-"+t.ID)).FillWidth().Height(30).Padding(0, 10).Cursor(ui.CursorPointer)
		header.Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(gapField).Children(func() {
				ui.Text(c, "计划").FontSize(fsSmall).Bold()
				ui.Text(c, fmt.Sprintf("%d/%d", done, len(steps))).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
				ui.Spacer(c)
				iconGlyph(c, "chevrondown", 13)
			})
		})
		if header.Clicked() {
			st.PlanCollapsed = !st.PlanCollapsed
		}
		if st.PlanCollapsed {
			return
		}
		ui.Scroll(c).Height(150).Padding(0, 6, 4).Gap(6).Children(func() {
			for n, step := range steps {
				mark := "○"
				color := c.Theme().TextMuted
				switch step.Status {
				case "done":
					mark = "✓"
					color = colSuccess()
				case "in_progress":
					mark = "●"
					color = c.Theme().Accent
				}
				ui.Row(c).Key(fmt.Sprintf("plan-step-%d", n)).Padding(2, 8).Gap(gapField).Children(func() {
					ui.Text(c, mark).FontSize(fsTiny).TextColor(color)
					ui.Text(c, fmt.Sprintf("%d.", n+1)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
					// Finished steps read as struck through, the way main's
					// .plan-item.done .plan-title does.
					done := step.Status == "done"
					titleColor := c.Theme().Text
					if done {
						titleColor = c.Theme().TextMuted
					}
					ui.RichText(c, ui.Span{Text: step.Title, Size: fsSmall, Color: titleColor, Strikethrough: done, DecorationColor: titleColor})
				})
			}
		})
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
			fieldNote(c, fmt.Sprintf("%d/%d 已回答", answered, len(t.Questions)))
		})
		ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
			iconAction(c, "ask-prev", "上一题", "chevron", 22, func() {
				if st.AskIndex > 0 {
					st.AskIndex--
				}
			}).Disabled(index == 0)
			for n := range t.Questions {
				n := n
				btn := flat(c.Key(fmt.Sprintf("ask-tab-%d", n)), fmt.Sprint(n+1))
				if n == index {
					btn.TextColor(c.Theme().Accent).Background(c.Theme().SurfaceHover)
				}
				btn.OnClick(func() { st.AskIndex = n })
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
				ui.Column(c.Key("ask-option-card-"+q.ID+"-"+opt.ID)).FillWidth().Padding(8, 10).Gap(4).Radius(radiusSmall).Border(1, c.Theme().Border).Children(func() {
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
						ui.Text(c, opt.Description).PaddingStart(22).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
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
func chatMessageWindow(t *conversation) (int, int) {
	end := len(t.Messages)
	start := max(0, end-140)
	if t.uiState().VisibleStart > 0 {
		start = min(end, t.uiState().VisibleStart-1)
		end = min(end, start+140)
	}
	return start, end
}
