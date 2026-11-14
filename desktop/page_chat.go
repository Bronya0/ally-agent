// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// chatMessages is the conversation viewport: history, welcome card, plan
// panel and the floating jump-to-bottom controls.
func (a *desktop) chatMessages(c *ui.Context, t *conversation, width float32) {
	st := t.uiState()
	if st.ChatLayoutSeen && st.ChatWithExplorer != a.explorer {
		// The explorer changes the list's parent. Its old pixel offset is not
		// valid at the new width; let the list restore its own row anchor.
		t.Scroll.Y = 0
		st.LastScrollY = 0
		if st.Follow {
			st.List.ScrollToEnd()
		}
	}
	st.ChatLayoutSeen, st.ChatWithExplorer = true, a.explorer
	a.beginChatSearchFrame(t)
	pruneTranscriptOutsideView(t, st)
	// The welcome card's popovers live in the card: once the list scrolls it out
	// of view they have no anchor, so they close with it instead of reopening
	// when it comes back.
	if st.built.ready && !st.built.welcome {
		a.welcomeMCP, a.welcomeSkills, a.welcomeTools = false, false, false
	}
	rows := t.chatRows()
	st.transcriptRows = t.cachedTranscriptRows()
	defer func() {
		st.transcriptRows = nil
		// The search hooks share this document only within the current build.
		st.Search.frameDoc = nil
	}()
	if st.PendingPrompt {
		if row := t.rowOfMessage(st.PendingPromptIdx); row >= 0 {
			st.Follow, st.FollowPending, st.FollowBottom = false, false, 0
			st.List.ScrollTo(row, ui.Start)
			st.PendingPrompt = false
		}
	}
	a.chatSearchReveal(c, t)
	// The reader scrolled up: the transcript stops following its end and keeps
	// where it is. The input handler reports a wheel at once, as its step may
	// arrive frames later; this catches the scroll bar and the keys too. A
	// layout that shrank above the reader lowers the offset just as well, so a
	// place the content no longer reaches is not a scroll either.
	shrank := st.LastScrollY > t.Scroll.MaxY+2
	// AtEnd includes the list's follow animation. Layout corrections while it
	// follows can lower Y even as new tool rows increase MaxY; that is not input.
	if !shrank && !st.List.AtEnd() && t.Scroll.Y < 1e8 && t.Scroll.Y < st.LastScrollY-2 && t.Scroll.Y < t.Scroll.MaxY-2 {
		st.Follow, st.FollowPending, st.FollowBottom = false, false, 0
	}
	// Following resumes once the reader comes back down to the newest message.
	// The wheel reports its intent at once, before its offset moves; the scroll
	// bar and the keys only show up as a position that moved down a frame
	// later, so both count. The step is more than a pixel: a wheel's snap to the
	// end leaves the offset read by the next build a fraction below where the
	// last layout put it, and that must not count as coming back down.
	scrolledDown := t.Scroll.Y < 1e8 && t.Scroll.Y > st.LastScrollY+1
	if t.Scroll.Y < 1e8 {
		st.LastScrollY = t.Scroll.Y
	}
	// The wheel asked for the end it saw; the scroll bar and the keys only
	// show up as a position that moved down to the end there is now.
	requested := st.FollowPending && st.FollowBottom > 0 && t.Scroll.Y >= min(st.FollowBottom, t.Scroll.MaxY)-2
	if requested || (scrolledDown && t.Scroll.Y >= t.Scroll.MaxY-2) {
		st.Follow, st.FollowPending, st.FollowBottom = true, false, 0
		if requested && t.Scroll.Y < t.Scroll.MaxY-2 {
			// The answer grew while the wheel glided: catch up with the end it
			// has now, not only with the end the wheel was heading for.
			st.List.ScrollToEnd()
		}
	}
	st.List.FollowEnd = st.Follow
	// A reply follows its end by gliding, as chat apps scroll their streams.
	st.List.FollowAnimation = &ui.Smooth
	if a.holdPlace {
		// A fold in a row changed its height: keep the place clicked for this
		// layout. Reading a fold is looking at that row, so the transcript stops
		// following its end as a manual scroll would, and the jump back shows.
		st.List.HoldPlace()
		st.Follow, st.FollowPending = false, false
		a.holdPlace = false
	}
	// From here the frame builds the rows in view: the messages they cover are
	// what the next frame prunes the caches to, so the range starts empty.
	st.built = builtRange{}
	inlineInset := float32(28)
	if a.prefs.RailExpanded && !a.prefs.TopNavigation {
		inlineInset = 44
		if width <= 1000 {
			inlineInset = 36
		}
	}

	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		if st.Search.Open {
			// The bar sits in the column above the viewport, capped and centred like
			// the transcript, so it lines up with the messages it searches.
			ui.Column(c).FillWidth().MaxWidth(chatMaxWidth).AlignSelf(ui.Center).Shrink(0).Padding(0, 20, 8).Children(func() {
				a.chatSearchBar(c, t)
			})
		}
		ui.List(c.Key(t.ID+"-messages"), &st.List, len(rows), func(i int) {
			a.chatRowView(c, t, rows[i])
		}).Label(locales.Text("聊天滚动区域")).TrackScroll(&t.Scroll).HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputScroll && ev.DY != 0 {
				// A wheel's offset arrives frames after the notch, so the intent is
				// kept here rather than read from where the list happens to be.
				if ev.DY < 0 {
					st.Follow, st.FollowPending, st.FollowBottom = false, false, 0
				} else if !st.Follow {
					// The end as it is now, not wherever it grows to before the
					// wheel's glide arrives.
					st.FollowPending, st.FollowBottom = true, t.Scroll.MaxY
				}
			}
			return false
		}).Grow(1).MinHeight(0).
			Padding(18, inlineInset, 14).Gap(10)
		// The way back shows as soon as the transcript stops following it: a
		// trackpad's nudge leaves the end by a few DIPs, and waiting for 40 left
		// the reader with no button at all. The list's own end covers the jump
		// that reached it without a wheel to report it, as a click in the scroll
		// bar's track is one frame there; following a stream keeps the list at
		// its end while the glide lags behind the content, so the end decides
		// only when the app still believes it follows.
		if (!st.Follow || !st.List.AtEnd()) && t.Scroll.Y < t.Scroll.MaxY-2 {
			ui.Box(c).Absolute().Right(18).Bottom(12).Children(func() {
				ui.Column(c).Gap(gapTight).Children(func() {
					iconAction(c, "chat-jump-up", locales.Text("跳到上一条提问"), "arrowup", 28, func() {
						a.jumpToPreviousPrompt(c, t)
					})
					iconAction(c, "chat-jump-down", locales.Text("回到最新"), "arrowdown", 28, func() {
						followLatest(t)
					})
				})
			})
		}
		if len(a.plans[t.ID]) > 0 {
			// The transcript and the composer are capped and centred, so a panel pinned
			// to the window edge never lines up with either. Same cap, same centre,
			// same side inset as the composer box it sits directly above.
			ui.Column(c).FillWidth().MaxWidth(chatMaxWidth).AlignSelf(ui.Center).Shrink(0).Padding(0, 20).Children(func() {
				a.planPanel(c, t)
			})
		}
	})
}

// jumpToPreviousPrompt scrolls to the prompt above the one in view, or to the
// last one before the rows the list built.
func (a *desktop) jumpToPreviousPrompt(c *ui.Context, t *conversation) {
	st := t.uiState()
	viewport := t.Scroll.Bounds(c)
	if viewport.H <= 0 {
		return
	}
	target := -1
	targetY := float32(-math.MaxFloat32)
	for index, handle := range st.PromptHandles {
		if handle == nil {
			continue
		}
		bounds := handle.Bounds(c)
		if bounds.H <= 0 || bounds.Y >= viewport.Y-12 {
			continue
		}
		if bounds.Y > targetY {
			target, targetY = index, bounds.Y
		}
	}
	if target < 0 {
		// Nothing above is drawn: the rows the list has yet to build hold the
		// previous prompt, and scrolling to its row builds it.
		for i := st.built.lo - 1; i >= 0; i-- {
			if str(t.Messages[i], "role") == "user" {
				target = i
				break
			}
		}
	}
	if row := t.rowOfMessage(target); row >= 0 {
		st.Follow = false
		st.List.ScrollTo(row, ui.Start)
	}
}

// chatRowView draws one row of the transcript list: the welcome card, or a
// message with the statistics of the round it ends. The list spaces, scrolls
// and recycles the rows, so a row is only capped and centred like the composer.
func (a *desktop) chatRowView(c *ui.Context, t *conversation, row chatRow) {
	st := t.uiState()
	st.built.cover(row)
	ui.Column(c).FillWidth().MaxWidth(chatMaxWidth).AlignSelf(ui.Center).Children(func() {
		if row.message == welcomeRow {
			a.welcomeView(c, t)
			return
		}
		a.messageRow(c, t, row.message, t.cachedTranscriptRows())
	})
}

func (a *desktop) messageRow(c *ui.Context, t *conversation, i int, rows *transcriptRows) {
	m := t.Messages[i]
	kind := rows.kindAt(i)
	if kind == rowSkipped {
		// Nothing draws here: rowKindAt decided it, and the container stays
		// unbuilt because an empty one still costs the column its gap.
		a.roundStatsIfAny(c, t, i, false)
		return
	}
	role := str(m, "role")
	st := t.uiState()
	row := ui.Column(c.Key(t.transcriptKey("message", i))).FillWidth().MinWidth(0).Gap(10)
	if role == "user" {
		if st.PromptHandles == nil {
			st.PromptHandles = map[int]*ui.Handle{}
		}
		handle := st.PromptHandles[i]
		if handle == nil {
			handle = takeTranscriptHandle(st)
			st.PromptHandles[i] = handle
		}
		row.Bind(handle)
	}
	if st.Search.Open {
		// The bar needs the row's bounds to scroll a hit into view. A prompt row
		// already carries the jump-to-prompt handle, and any other row takes a
		// search handle on itself: extra wrappers on the row would change the
		// subtree's shape, which resets the state of the widgets under it (a code
		// block's sideways scroll, a selection).
		if st.Search.handles == nil {
			st.Search.handles = map[int]*ui.Handle{}
		}
		handle := st.Search.handles[i]
		if role == "user" {
			st.Search.handles[i] = st.PromptHandles[i]
		} else {
			if handle == nil {
				handle = takeTranscriptHandle(st)
				st.Search.handles[i] = handle
			}
			row.Bind(handle)
		}
	}
	showStats := row.Hovered()
	row.Children(func() {
		switch kind {
		case rowReadGroup, rowCommandGroup:
			a.toolActivityGroupCard(c, t, i)
		default:
			a.messageBody(c, t, i, m, role)
		}
		if idx, ok := st.RoundAt[i]; ok {
			a.roundStatsRow(c, t, idx, st.Round[idx], showStats)
		}
	})
}

// messageBody draws the message itself; its row, grouping and statistics belong
// to messageRow. Precedence is by role first, exactly as the transcript always
// resolved it: a tool record only draws as a card when no role claims it.
func (a *desktop) messageBody(c *ui.Context, t *conversation, i int, m map[string]any, role string) {
	switch role {
	case "user":
		a.userMessage(c, t, i, m)
	case "assistant":
		if _, ok := m["welcome"]; ok {
			a.welcomeView(c, t)
			return
		}
		a.assistantMessage(c, t, i, m)
	case "notice":
		tone := str(m, "tone")
		if tone == "" {
			tone = "warn"
		}
		notice(c, tone, str(m, "content"), a.secondaryInk())
	case "subagent":
		a.subagentRow(c, t, i, m)
	default:
		if isToolRecord(m) {
			a.toolCard(c, t, i, m)
			return
		}
		if content := str(m, "content"); content != "" {
			markdown.View(c, content, a.markdownOptions(t, i)).FillWidth()
		}
	}
}

func (a *desktop) markdownOptions(t *conversation, i int) *markdown.Options {
	// Both maps are filled in here: a conversation can arrive with its options
	// already started (a new session creates them), and writing to a nil map
	// panics on the very frame it is first drawn.
	if t.MD == nil {
		t.MD = map[int]*markdown.Options{}
	}
	if t.MDCodeBlock == nil {
		t.MDCodeBlock = map[int]func(*ui.Context, *markdown.CodeBlockView){}
	}
	if t.MD[i] == nil {
		t.MD[i] = a.readingMarkdownOptionsForSource(func() string {
			if i < len(t.Messages) {
				return str(t.Messages[i], "content")
			}
			return ""
		}, fmt.Sprintf("%s:markdown:%d", t.ID, i))
		t.MDCodeBlock[i] = t.MD[i].CodeBlock
	}
	if t.MD[i].Theme == nil {
		t.MD[i].Theme = &markdown.Theme{}
	}
	a.configureReadingMarkdown(t.MD[i])
	// While the find bar is open, the blocks that hold a hit draw their text with
	// the matched runs marked; every other block and message is untouched. The
	// code blocks are wrapped too, so a hit inside them is marked as well, and a
	// block without one keeps the renderer's colouring. The wrapper always wraps
	// the theme's own hook, never the wrapper a previous frame installed: this
	// runs every frame, and a chain of them would grow for as long as the bar is
	// open and stay after it closes.
	if t.uiState().Search.Open && a.chatSearchMessageMatch(t, i) {
		t.MD[i].Block = a.chatSearchBlock(t, i)
		t.MD[i].CodeBlock = a.chatSearchCodeHook(t, i, t.MDCodeBlock[i])
	} else {
		t.MD[i].Block = nil
		if base, ok := t.MDCodeBlock[i]; ok {
			t.MD[i].CodeBlock = base
		}
	}
	return t.MD[i]
}

// userMessage keeps prompts in a right-aligned bubble with hover actions below.
func (a *desktop) userMessage(c *ui.Context, t *conversation, i int, m map[string]any) {
	content := str(m, "content")
	p := a.currentPalette()
	services := c.Services()
	message := ui.Column(c.Key(t.transcriptKey("user-bubble", i))).Label(locales.Text("用户消息区域")).AlignSelf(ui.End).AlignItems(ui.End).MaxWidthPercent(85).MinWidth(0).Gap(gapTight)
	message.Children(func() {
		background := p.accent.Alpha(0.10)
		if p.dark {
			background = userBubbleDark
		}
		ui.Column(c.Key("bubble")).Label(locales.Text("用户消息")).Selectable().MinWidth(0).Padding(10, 16).Radius(16).Background(background).Children(func() {
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
		actions := ui.Row(c.Key("actions")).Label(locales.Text("用户消息操作")).Height(26).Gap(gapTight).Shrink(0)
		// IgnoreInput keeps an unseen button out of the pointer's way while a
		// screen reader can still reach it, which Disabled takes away.
		uikit.HoverFade(c, actions, opacity).Children(func() {
			mutedIconAction(c, "copy", locales.Text("复制提问"), "copy", 26, func() {
				services.WriteClipboard(content)
			}).IgnoreInput(!show).Shrink(0)
			dangerIconAction(c, "delete", locales.Text("删除该提问及之后"), "trash", 26, func() {
				a.requestDeleteUserMessage(t, i)
			}).IgnoreInput(!show).Disabled(t.Busy).Shrink(0)
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
	v := ui.Column(c).Key(t.transcriptKey("prompt", i)).Gap(gapInline)
	// The counts and the collapsed preview change only with the content, so a
	// pasted log is cut, split and joined once per message instead of on every
	// frame the bubble is visible.
	counts := memoAt[promptCounts](v, "counts").get(content)
	lines, runes, long := counts.lines, counts.runes, counts.long
	st := t.uiState()
	shown := content
	if long && !st.PromptOpen[i] {
		shown = counts.head
	}
	v.Children(func() {
		// Share selection with the bubble instead of installing a separate
		// Markdown selection scope.
		options := a.markdownOptions(t, i)
		options.ShareSelection = true
		markdown.View(c, shown, options).FillWidth()
		if long {
			labelText := fmt.Sprintf(locales.Text("展开完整提问（%d 行，%d 字）"), lines, runes)
			if st.PromptOpen[i] {
				labelText = locales.Text("收起提问")
			}
			button(c, nil, labelText, btnSecondary, false, "chat").AlignSelf(ui.Start).OnClick(func() {
				st.PromptOpen[i] = !st.PromptOpen[i]
				a.holdTranscriptPlace()
			})
		}
	})
}

// promptCounts is how long a question reads: its lines, its runes and the
// preview it collapses to. All three follow the content, so they are derived
// once and kept beside the prompt until that content changes.
type promptCounts struct {
	content string
	lines   int
	runes   int
	head    string
	long    bool
	ready   bool
}

func (p *promptCounts) get(content string) promptCounts {
	if !p.ready || p.content != content {
		lines := strings.Count(content, "\n") + 1
		runes := utf8.RuneCountInString(content)
		// Cutting walks rune boundaries instead of converting the whole prompt.
		head, truncated := cutRunes(content, 400)
		long := truncated || lines > 6
		if long {
			if truncated {
				head += "…"
			}
			if ls := strings.Split(head, "\n"); len(ls) > 6 {
				head = strings.Join(ls[:6], "\n") + "\n…"
			}
		}
		p.content, p.lines, p.runes, p.head, p.long, p.ready = content, lines, runes, head, long, true
	}
	return *p
}

func (a *desktop) assistantMessage(c *ui.Context, t *conversation, i int, m map[string]any) {
	content := str(m, "content")
	attachmentCount := messageAttachmentCount(m)
	if content == "" && attachmentCount == 0 {
		return
	}
	if attachmentCount > 0 {
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
	markdown.View(c.Key(t.transcriptKey("md", i)), content, a.markdownOptions(t, i)).FillWidth()
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
	modelSeconds := float64(stat.ModelDurationMS) / 1000
	row := ui.Row(c.Key(t.transcriptKey("round-stats", i))).FillWidth().Height(heightRow + 2).Gap(gapInline + 1).AlignItems(ui.Center)
	// The controls fade in with the message row but never go inert. Removing
	// them instead took away the button a panel is anchored to, and the panel
	// lives in the window's overlay: it vanished the moment the pointer moved
	// onto it, so its entries could never be chosen. An inert row also left the
	// footer of a round with nothing drawn above it, such as one that has not
	// written its first token yet, unreachable; the row answers the pointer on
	// its own so no click ever falls on dead controls.
	if row.Hovered() {
		show = true
	}
	visible := float32(0)
	if show {
		visible = 1
	}
	row.Opacity(visible)
	row.Children(func() {
		uikit.Tooltip(c, ui.Text(c, fmtDur(stat.DurationMS)).FontSize(fsSmall).TextColor(a.secondaryInk()), locales.Text("本轮耗时"))
		if hit := stat.CacheHit + stat.CacheMiss; hit > 0 {
			uikit.Tooltip(c, ui.Text(c, fmt.Sprintf("cache %d%%", int(float64(stat.CacheHit)*100/float64(hit)))).FontSize(fsSmall).
				TextColor(a.secondaryInk()), fmt.Sprintf("cache hit %d / miss %d", stat.CacheHit, stat.CacheMiss))
		}
		if stat.InputTokens > 0 || stat.OutputTokens > 0 {
			uikit.Tooltip(c, ui.Text(c, fmt.Sprintf("↑%s ↓%s", fmtK(stat.InputTokens), fmtK(stat.OutputTokens))).FontSize(fsSmall).
				TextColor(a.secondaryInk()), fmt.Sprintf("input %d / output %d tokens", stat.InputTokens, stat.OutputTokens))
		}
		if modelSeconds > 0 && stat.OutputTokens > 0 {
			uikit.Tooltip(c, ui.Text(c, fmt.Sprintf("%d token/s", int(float64(stat.OutputTokens)/modelSeconds))).FontSize(fsSmall).TextColor(a.secondaryInk()), locales.Text("累计模型请求耗时内的平均输出速度，包含首 token 等待，不含工具执行时间"))
		}
		services := c.Services()
		iconMenu(c, t.roundKey("export", i), locales.Text("导出"), "download", 22, 180, func(m ui.MenuParts) {
			uikit.MenuItem(c, m, "answer", locales.Text("导出回答"), uikit.MenuItemOptions{}, a.secondaryInk()).OnClick(func() {
				a.exportText("ally-response.md", str(t.Messages[i], "content"))
			})
			uikit.MenuItem(c, m, "session", locales.Text("导出会话"), uikit.MenuItemOptions{}, a.secondaryInk()).OnClick(func() {
				a.exportText("ally-session.md", sessionMarkdown(t))
			})
		}, a.secondaryInk())
		mutedIconAction(c, t.roundKey("copy-summary", i), locales.Text("复制总结"), "copy", 22, func() {
			services.WriteClipboard(str(t.Messages[i], "content"))
			c.Toast(locales.Text("已复制最终总结"))
		})
		iconMenu(c, t.roundKey("quick", i), locales.Text("快捷消息"), "list", 22, 180, func(m ui.MenuParts) {
			for _, item := range quickMessages {
				item := item
				uikit.MenuItem(c, m, item.label, locales.Text(item.label), uikit.MenuItemOptions{}, a.secondaryInk()).OnClick(func() {
					a.send(t, locales.Text(item.prompt))
				})
			}
		}, a.secondaryInk())
		mutedIconAction(c, t.roundKey("remember", i), locales.Text("记住这次对话的长期记忆"), "book", 22, func() {
			a.send(t, locales.Text(projectMemoryPrompt)+"\n\nLocal date for this memory update: "+time.Now().Format("2006-01-02")+".")
		})
	})
}

var quickMessages = []struct{ label, prompt string }{
	{"继续", "继续"},
	{"说人话", "请说人话：去掉套话和术语，直接讲结论和原因。"},
	{"提交并推送", "请检查改动并提交，然后推送到远端。"},
	{"代码审查", "请审查当前工作区最近的改动，指出真实缺陷与回归风险。"},
	{"保持代码整洁", "请把这轮改动收拾整洁：删掉调试残留、死代码和多余注释，统一命名与结构，不要顺手改无关代码。"},
	{"记录教训", "请总结这一轮里容易踩坑的地方，并更新 LESSONS.md。"},
}

const projectMemoryPrompt = "请把这次对话里值得长期记住的项目事实、约定与踩坑写进项目记忆（~/.ally_agent/memories/project-knowledge/），只写可复用的事实。"

func (a *desktop) exportText(name, content string) {
	a.job(func() error {
		path, err := a.core.ExportTextFile(name, content, "Markdown", "*.md")
		if err == nil && path != "" {
			a.win.Update(func() { a.notify(locales.Text("已导出：") + path) })
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
			b.WriteString(locales.Text("## 你\n\n") + content + "\n\n")
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
	// All dismissal paths release the conversation the confirmation targeted.
	defer func() {
		if !d.Open && a.deletePrompt == d {
			a.deletePrompt = nil
		}
	}()
	if !d.Open {
		return
	}
	modalCard(c, &d.Open, widthModalSmall, func() {
		modalTitle(c, locales.Text("删除该提问及之后？"), "")
		ui.Text(c, locales.Text("将删除此提问及之后的所有对话，并同步清理模型上下文。此操作不可恢复。")).
			FontSize(fsSmall).TextColor(a.secondaryInk()).LineHeight(1.5).Selectable()
		modalActions(c, func() {
			flat(c, locales.Text("取消")).OnClick(func() { d.Open = false })
			dangerButton(c, locales.Text("删除")).OnClick(func() { a.confirmDeleteUserMessage(d) })
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
		a.notify(locales.Text("对话已变化，请重新选择要删除的提问。"))
		return
	}
	request := backend.TruncateSessionHistoryRequest{SessionID: t.ID, UserMessageIndex: d.UserIndex, ExpectedContent: d.Content}
	go func() {
		if _, syncErr := a.core.TruncateSessionHistory(request); syncErr != nil {
			a.win.Update(func() {
				a.notifyError(locales.Text("界面已删除，但模型上下文同步失败：") + syncErr.Error())
			})
		}
		a.win.Update(func() {
			if index <= len(t.Messages) {
				invalidateChatSearch(t)
				t.Messages = append([]map[string]any(nil), t.Messages[:index]...)
			}
			resetTranscriptAfterDelete(t, index)
			a.persist(t)
		})
	}()
}

// resetTranscriptAfterDelete clears view state bound to the deleted tail.
// Round entries below the cut still describe the remaining messages, so only
// the removed indices are dropped; followLatest is required because deleting
// the first row of the visible page would otherwise leave VisibleStart past
// the shortened transcript and collapse the history window.
func resetTranscriptAfterDelete(t *conversation, index int) {
	t.resetTranscriptLookup()
	t.markHistory()
	t.groupChanges = nil
	releaseStaleStream(t)
	pruneTranscriptHandles(t, 0, index)
	releaseCommandDetails(t)
	rounds := t.uiState().Round
	for k := range rounds {
		if k >= index {
			delete(rounds, k)
		}
	}
	followLatest(t)
}

// ---- Welcome -------------------------------------------------------------

func (a *desktop) welcomeView(c *ui.Context, t *conversation) {
	availableSkills := a.enabledSkillNames()
	now := time.Now()
	greeting := locales.Text("上午好")
	switch {
	case now.Hour() < 6:
		greeting = locales.Text("夜深了")
	case now.Hour() < 12:
		greeting = locales.Text("上午好")
	case now.Hour() < 14:
		greeting = locales.Text("中午好")
	case now.Hour() < 18:
		greeting = locales.Text("下午好")
	default:
		greeting = locales.Text("晚上好")
	}
	ui.Column(c).Key("welcome").Padding(36, 0, 12).Gap(gapPage).Children(func() {
		ui.Row(c).Gap(22).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Width(170).AlignItems(ui.Center).Gap(gapSection).Children(func() {
				img := ui.Column(c.Key("welcome-avatar")).Size(150, 150).Center().Label(locales.Text("欢迎页头像"))
				img.Children(func() { a.avatarDrawing(c) })
				hint := locales.Text("点击头像：切换樱花特效")
				if !a.prefs.Pet.Enabled {
					hint = locales.Text("点击头像：摸摸头并切换樱花特效")
				}
				uikit.Tooltip(c, img.Cursor(ui.CursorPointer), hint)
				if img.Clicked() {
					a.togglePetals()
					if !a.prefs.Pet.Enabled {
						a.patAvatar(time.Now())
					}
				}
				ui.Text(c, a.versionLabel).FontSize(fsTiny).TextColor(a.secondaryInk())
			})
			ui.Column(c).Grow(1).Border(1, surfaceBorder(c)).Radius(radiusCard).Children(func() {
				rows := [][4]string{
					{locales.Text("工作区"), workspaceLabel(t.Workspace), "Bash", a.config.GitBashPath},
					{locales.Text("模型"), strings.ReplaceAll(t.Model, " / ", " · "), "MCP", a.mcpSummary},
					{locales.Text("技能"), fmt.Sprintf(locales.Text("%d 个可用"), len(availableSkills)), locales.Text("工具"), fmt.Sprintf(locales.Text("%d 个工具"), a.toolCount)},
				}
				if rows[0][3] == "" {
					rows[0][3] = locales.Text("自动检测")
				}
				if rows[1][3] == "" {
					rows[1][3] = locales.Text("无 MCP 服务")
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
								a.welcomePopover(c, t, cell, open, i, j)
							}
						}
					})
				}
			})
		})
		ui.Text(c, fmt.Sprintf(locales.Text("今天是 %d月%d日 %s，现在是 %02d:%02d。%s，我们把注意力放回眼前，先解决最重要的一件事。"),
			now.Month(), now.Day(), []string{locales.Text("周日"), locales.Text("周一"), locales.Text("周二"), locales.Text("周三"), locales.Text("周四"), locales.Text("周五"), locales.Text("周六")}[now.Weekday()], now.Hour(), now.Minute(), greeting)).
			FontSize(15).Bold().LineHeight(1.8)
	})
}

func (a *desktop) welcomePopover(c *ui.Context, t *conversation, anchor ui.Element, open *bool, row, col int) {
	if !*open {
		return
	}
	bounds := anchor.Bounds()
	// The overlay trigger must not intercept clicks outside the transcript
	// viewport when the user scrolls the table partially or fully out of view.
	viewport := t.Scroll.Bounds(c)
	if viewport.W > 0 && viewport.H > 0 {
		left, top := max(bounds.X, viewport.X), max(bounds.Y, viewport.Y)
		right, bottom := min(bounds.X+bounds.W, viewport.X+viewport.W), min(bounds.Y+bounds.H, viewport.Y+viewport.H)
		if right <= left || bottom <= top {
			*open = false
			return
		}
		bounds = ui.Rect{X: left, Y: top, W: right - left, H: bottom - top}
	}
	// TrackScroll requests the end with MaxFloat32 while following. A popover
	// attached inside that scroll can be placed at the sentinel instead of the
	// visible cell. Anchor the native popover to the cell's committed window
	// bounds so it keeps native dismissal without changing transcript following.
	ui.Overlay(c, func() {
		trigger := ui.ButtonBase(c.Key(fmt.Sprintf("welcome-popup-anchor-%d-%d", row, col))).Absolute().
			Left(bounds.X).Top(bounds.Y).Size(bounds.W, bounds.H).Cursor(ui.CursorPointer).Label(locales.Text("关闭信息面板"))
		trigger.OnClick(func() { *open = false })
		popoverPanel(c, trigger, open, 320, func() {
			title := locales.Text("MCP 服务")
			if row == 2 && col == 0 {
				title = locales.Text("可用技能")
			} else if row == 2 {
				title = locales.Text("工具")
			}
			ui.Text(c, title).FontSize(fsSection).Bold().Padding(gapInline, padCard)
			ui.Scroll(c).MaxHeight(260).Children(func() {
				ui.Column(c).Gap(3).Padding(4, 10).Children(func() {
					switch {
					case row == 1:
						if len(a.mcpServers) == 0 {
							fieldNote(c, locales.Text("无 MCP 服务"), a.secondaryInk())
						}
						for _, server := range a.mcpServers {
							ui.Text(c, fmt.Sprintf(locales.Text("%s · %s · %d 个工具"), str(server, "name"), mcpStatusText(str(server, "status")), int(num(server, "toolCount")))).FontSize(fsSmall).TextColor(a.secondaryInk())
						}
					case col == 0:
						names := a.enabledSkillNames()
						if len(names) == 0 {
							fieldNote(c, locales.Text("当前没有可用技能"), a.secondaryInk())
						}
						for _, name := range names {
							ui.Text(c, name).FontSize(fsSmall)
						}
					default:
						if len(a.toolInventory) == 0 {
							fieldNote(c, locales.Text("暂无工具"), a.secondaryInk())
						}
						for _, tool := range a.toolInventory {
							suffix := ""
							if !tool.Enabled {
								suffix = locales.Text("（已停用）")
							}
							ui.Text(c, tool.Name+suffix).FontSize(fsSmall)
						}
					}
				})
			})
			divider(c)
			label, page := locales.Text("管理 MCP 服务"), "mcp"
			if row == 2 && col == 0 {
				label, page = locales.Text("管理技能"), "skills"
			} else if row == 2 {
				label, page = locales.Text("管理内置工具"), "settings"
			}
			popoverItem(c, fmt.Sprintf("welcome-manage-%d-%d", row, col), label, "", false, func() {
				*open = false
				a.changePage(page)
				if row == 2 && col == 1 {
					a.focusBuiltinToolsSettings()
				}
			}, a.secondaryInk())
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
		return locales.Text("已连接")
	case "connecting":
		return locales.Text("连接中")
	case "failed", "error":
		return locales.Text("连接失败")
	case "disabled":
		return locales.Text("已停用")
	}
	return locales.Text("未连接")
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
				iconGlyph(c, "chevrondown", sizeCaretBlock).Rotate(-90 * (1 - parts.Progress()))
			})
		})
		parts.Panel(func() {
			scroll := ui.Scroll(c.Key("plan-scroll-"+t.ID)).MaxHeight(150).Padding(0, 6, 4).Gap(gapInline)
			lastScrolledRevision := ui.Local(scroll, "auto-scroll-revision", func() int64 { return -1 })
			revision := a.planRev[t.ID]
			autoScroll := open && *lastScrolledRevision != revision
			if autoScroll {
				*lastScrolledRevision = revision
			}
			target := len(steps) - 1
			for n, step := range steps {
				if step.Status != "done" && step.Status != "completed" {
					target = n
					break
				}
			}
			// Follow the current step only when the plan itself changes. Revealing
			// it every frame, or again each time the panel reopens, would fight the
			// user's own scrolling.
			scroll.Children(func() {
				for n, step := range steps {
					row := ui.Row(c).Key(fmt.Sprintf("plan-step-%d", n)).AlignItems(ui.Center).Padding(2, 8).Gap(gapField)
					if autoScroll && n == target {
						row.ScrollIntoView()
					}
					row.Children(func() {
						a.planMark(c, step.Status)
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

// planMark is the step bullet. Every state occupies the same 9px square: the
// glyphs ○ and ● have different ink sizes at one font size, so a pending step
// read as a much bigger circle than a running one.
func (a *desktop) planMark(c *ui.Context, status string) ui.Element {
	switch status {
	case "done":
		return iconGlyph(c, "check", 9).Shrink(0).TextColor(c.Theme().Success)
	case "in_progress":
		return ui.Box(c).Size(9, 9).Shrink(0).Radius(5).Background(c.Theme().Accent)
	}
	return ui.Box(c).Size(9, 9).Shrink(0).Radius(5).Border(1, a.secondaryInk())
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
	// 控件状态按 key 存，而问题 id 是后端补的（每次都叫 q1、q2），所以 key 里必须
	// 带上本次提问：新提问换一套新控件，等价于重新初始化。
	wkey := "ask-" + t.AskID + ":" + q.ID
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
			ui.Text(c, locales.Text("Ally 需要你的输入")).Bold().FontSize(fsBody)
			neutralChip(c, fmt.Sprintf(locales.Text("%d 个问题"), len(t.Questions)))
			ui.Spacer(c)
			fieldNote(c, fmt.Sprintf(locales.Text("%d/%d 已回答"), answered, len(t.Questions)), a.secondaryInk())
		})
		ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
			iconAction(c, "ask-prev", locales.Text("上一题"), "chevron", 22, func() {
				if st.AskIndex > 0 {
					st.AskIndex--
				}
			}).Disabled(index == 0)
			for n := range t.Questions {
				n := n
				choiceButton(c, fmt.Sprintf("ask-tab-%s-%d", t.AskID, n), fmt.Sprint(n+1), n == index, c.Theme().Accent).
					OnClick(func() { st.AskIndex = n })
			}
			iconAction(c, "ask-next", locales.Text("下一题"), "chevron", 22, func() {
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
				card := ui.Column(c.Key("ask-option-card-"+wkey+"-"+opt.ID)).FillWidth().Padding(8, 10).Gap(gapTight).
					Radius(radiusSmall).Border(1, surfaceBorder(c)).Cursor(ui.CursorPointer)
				if card.Hovered() {
					card.Background(c.Theme().SurfaceHover)
				}
				if card.Clicked() {
					checked = !checked
					st.AskSelected[q.ID] = toggleString(st.AskSelected[q.ID], opt.ID, checked)
				}
				card.Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(gapInline).Children(func() {
						box := ui.Checkbox(c.Key("ask-opt-"+wkey+"-"+opt.ID), &checked, opt.Label)
						if box.Changed() {
							st.AskSelected[q.ID] = toggleString(st.AskSelected[q.ID], opt.ID, checked)
						}
						if opt.Recommended {
							accentChip(c, locales.Text("推荐"))
						}
					})
					if opt.Description != "" {
						ui.Text(c, opt.Description).PaddingStart(22).FontSize(fsTiny).TextColor(a.secondaryInk())
					}
				})
			}
			custom := st.AskCustom[q.ID]
			customRow := ui.Row(c.Key("ask-custom-row-"+wkey)).FillWidth().MinHeight(32).Padding(4, 8).
				AlignItems(ui.Center).Radius(radiusSmall).Cursor(ui.CursorPointer)
			if customRow.Hovered() {
				customRow.Background(c.Theme().SurfaceHover)
			}
			if customRow.Clicked() {
				custom = !custom
			}
			customRow.Children(func() {
				ui.Checkbox(c.Key("ask-custom-"+wkey), &custom, locales.Text("自定义回答"))
			})
			st.AskCustom[q.ID] = custom
			if custom {
				txt := st.AskCustomTxt[q.ID]
				uikit.Area(c.Key("ask-custom-text-"+wkey), &txt).Placeholder(locales.Text("输入你的回答")).Height(70)
				st.AskCustomTxt[q.ID] = txt
			}
		})
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			disabled := answered < len(t.Questions) || st.AskSubmitted
			primary(c, locales.Text("提交回答")).Disabled(disabled).OnClick(func() { a.submitAsk(t) })
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

// Welcome detection shares the append-aware transcript index.
func hasWelcomeMessage(t *conversation) bool {
	return t.cachedTranscriptRows().welcome
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

type roundIndexKey struct {
	rev   uint64
	count int
}

// Rendering reads explicit cancellation markers.
func userCancelledMessage(m map[string]any) bool {
	for _, key := range []string{"status", "stopReason", "reason"} {
		switch strings.ToLower(strings.TrimSpace(str(m, key))) {
		case "cancelled", "canceled", "user_cancelled", "user_canceled":
			return true
		}
	}
	if boolOf(m, "cancelled") || boolOf(m, "canceled") || str(m, "errorCode") == "E_CANCELLED" {
		return true
	}
	return false
}
func cancelledResult(c *ui.Context, ink ...ui.Color) {
	ui.Text(c, locales.Text("用户已取消")).FontSize(fsSmall).TextColor(uikit.SecondaryInk(c, ink)).Selectable()
}
