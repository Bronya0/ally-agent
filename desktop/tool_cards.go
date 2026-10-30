// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// toolDescriptor is the single table that classifies tool cards: the verb
// shown for each state, whether the body expands, and where the preview
// comes from. Cards never re-derive these per site.
type toolDescriptor struct {
	runningVerb string
	doneVerb    string
	failedVerb  string
	expandable  bool
	showTiming  bool
}

// failedVerb exists because a card in error state must never claim
// completion: "编辑" on a failed edit misleads the reader. The done verbs are
// bare action words, the same shape as 搜索 and 计算, so a card reads as the
// action it took rather than as a badge that already happened.
var toolTable = map[string]toolDescriptor{
	"read":               {"读取中", "读取", "读取失败", false, false},
	"remote_read":        {"读取中", "读取", "远程读取失败", false, false},
	"edit":               {"编辑中", "编辑", "编辑失败", true, false},
	"create":             {"创建中", "创建", "创建失败", true, false},
	"delete":             {"删除中", "删除", "删除失败", true, false},
	"remote_delete_path": {"删除中", "删除", "远程删除失败", true, false},
	"remote_edit":        {"编辑中", "编辑", "远程编辑失败", true, false},
	"remote_create_file": {"创建中", "创建", "远程创建失败", true, false},
	"command":            {"运行中", "运行", "运行失败", true, true},
	"remote_run_command": {"运行中", "运行", "远程命令失败", true, true},
	"grep":               {"搜索中", "搜索", "搜索失败", false, false},
	"list":               {"列目录中", "列目录", "列目录失败", true, false},
	"glob":               {"匹配中", "匹配文件", "匹配失败", true, false},
	"http_request":       {"请求中", "请求", "请求失败", true, true},
	"web_fetch":          {"抓取中", "抓取", "抓取失败", true, true},
	"calculate":          {"计算中", "计算", "计算失败", false, false},
	"plan":               {"更新中", "计划", "计划失败", false, false},
	"skill":              {"加载中", "技能", "技能失败", false, false},
	"ask":                {"提问中", "提问", "提问失败", true, false},
	"subagent":           {"委派中", "子代理", "委派失败", true, true},
	"ssh_cluster":        {"检查中", "SSH 集群", "SSH 检查失败", true, false},
	"service":            {"运行中", "后台服务", "服务失败", true, false},
	"scheduled_task":     {"处理中", "定时任务", "任务失败", true, true},
	"remote_transfer":    {"传输中", "传输", "传输失败", true, true},
	"remote_transfer_":   {"传输中", "传输", "传输失败", true, true},
	"screenshot":         {"截图中", "截图", "截图失败", true, false},
	"render_html":        {"渲染中", "渲染", "渲染失败", true, true},
	"suggest":            {"建议中", "建议", "建议失败", true, false},
	"wait":               {"等待中", "等待", "等待失败", false, true},
	"image_generate":     {"生成中", "生成", "生成失败", true, true},
	"image_edit":         {"编辑中", "编辑", "生图编辑失败", true, true},
}

func toolKind(name string) string {
	switch name {
	case "list_files":
		return "list"
	case "Glob":
		return "glob"
	case "Bash":
		return "command"
	case "Skill":
		return "skill"
	case "replace_exact", "replace_lines":
		return "edit"
	case "agent_delegate":
		return "subagent"
	case "start_service", "stop_service", "list_services":
		return "service"
	}
	return name
}

func describeTool(name string) toolDescriptor {
	name = toolKind(name)
	if d, ok := toolTable[name]; ok {
		return d
	}
	for prefix, d := range toolTable {
		if strings.HasSuffix(prefix, "_") && strings.HasPrefix(name, prefix) {
			return d
		}
	}
	switch {
	case strings.HasPrefix(name, "replace_"):
		return toolTable["edit"]
	case strings.HasPrefix(name, "mcp__"):
		return toolDescriptor{"调用中", "调用", "MCP 调用失败", true, true}
	}
	return toolDescriptor{"调用中", "调用", name + "失败", true, false}
}

func toolState(m map[string]any) string {
	if userCancelledMessage(m) || userCancelledMessage(toolResult(m)) {
		return "cancelled"
	}
	status := strings.ToLower(str(m, "status"))
	state := str(m, "toolState")
	switch {
	case (status == "error" || status == "failed") || state == "tool:error":
		return "error"
	case (status == "success" || status == "completed" || status == "done") || state == "tool:result":
		result := toolResult(m)
		if boolOf(result, "cancelled") {
			return "cancelled"
		}
		if value, exists := result["ok"]; exists && value == false {
			return "error"
		}
		return "success"
	case state == "tool:start" || state == "tool:update" || status == "running":
		return "running"
	}
	return "pending"
}

// isFoldableReadTool reports whether a message joins main's read-grep-list
// folding: consecutive read/grep/list tool calls collapse into one group row.
// The backend sends `list` for directory listing; main also folds
// `batch_read`, which arrives here as plain `read`.
func isFoldableReadTool(m map[string]any) bool {
	// A locally trimmed card no longer carries its result, only an excerpt: it has
	// nothing left to group with, so it stays its own row (as main's
	// !next.fastCompacted check does).
	if boolOf(m, "fastCompacted") {
		return false
	}
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	switch toolKind(name) {
	case "read", "remote_read", "batch_read", "grep", "list", "glob":
		return true
	}
	return false
}

// readGrepGroupEntry is one folded call inside a read-grep-list group.
type readGrepGroupEntry struct {
	verb   string
	title  string
	detail string
	state  string
}

// readGrepGroupCard mirrors main's ReadGrepGroupCard: consecutive
// read/grep/list calls render as one collapsed row ("读取 N 次，搜索 M 次，
// 罗列 K 次 ›"); expanding lists each call in order. The folded row carries
// no status icon and never turns red for a failed child — failures show on
// the expanded entries, same as main.
func (a *desktop) readGrepGroupCard(c *ui.Context, t *conversation, index int) {
	key := fmt.Sprintf("%s-readgroup-%d", t.ID, index)
	open := t.uiState().ToolOpen[key]
	container := ui.Column(c.Key("tool-" + key)).Gap(2)
	extra := ui.Local(container, "entries-extra", func() int { return 0 })
	if !open {
		*extra = 0
	}
	var entries []readGrepGroupEntry
	reads, greps, lists := 0, 0, 0
	end := index
	_, windowEnd := a.chatMessageWindow(t)
	for i := index; i < windowEnd; i++ {
		m := t.Messages[i]
		if str(m, "role") != "tool_call" && str(m, "toolCallId") == "" {
			break
		}
		if !isFoldableReadTool(m) {
			break
		}
		name := str(m, "name")
		if name == "" {
			name = str(m, "toolName")
		}
		name = toolKind(name)
		if !open || i-index >= 100+*extra {
			switch name {
			case "grep":
				greps++
			case "list", "glob":
				lists++
			default:
				reads++
			}
			end = i
			continue
		}
		args := parseToolArgs(str(m, "args"))
		title := toolArgSummary(name, args)
		switch name {
		case "grep":
			greps++
			entries = append(entries, readGrepGroupEntry{"搜索", title, "", toolState(m)})
		case "list", "glob":
			lists++
			entries = append(entries, readGrepGroupEntry{"罗列", title, "", toolState(m)})
		default:
			reads++
			entries = append(entries, readGrepGroupEntry{"读取", title, "", toolState(m)})
		}
		entry := &entries[len(entries)-1]
		result := toolResult(m)
		// Entries show paths/queries only; preserve error diagnostics below.
		if entry.state == "error" {
			entry.detail = str(result, "error")
			if entry.detail == "" {
				entry.detail = str(m, "error")
			}
		}
		if files := list(result, "files"); len(files) > 0 && (name == "read" || name == "remote_read") {
			entries = entries[:len(entries)-1]
			for _, raw := range files {
				if file, ok := raw.(map[string]any); ok {
					entries = append(entries, readGrepGroupEntry{"读取", str(file, "path"), "", toolState(m)})
				}
			}
		}
		end = i
	}
	if reads+greps+lists == 0 {
		return
	}
	var parts []string
	if reads > 0 {
		parts = append(parts, fmt.Sprintf("读取 %d 次", reads))
	}
	if greps > 0 {
		parts = append(parts, fmt.Sprintf("搜索 %d 次", greps))
	}
	if lists > 0 {
		parts = append(parts, fmt.Sprintf("罗列 %d 次", lists))
	}
	container.Children(func() {
		row := ui.ButtonBase(c.Key(key+"-header")).FillWidth().Height(24).Padding(0, 8, 0, 0).Gap(4).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).Label("读取分组 " + key)
		row.Children(func() {
			disclosureIcon(c, "search", a.secondaryInk())
			ui.Text(c, strings.Join(parts, "，")).FontSize(fsTool).TextColor(a.secondaryInk()).LineHeight(1.4).SingleLine()
			caret := "chevron"
			if open {
				caret = "chevrondown"
			}
			iconGlyph(c, caret, 12).Size(16, 16).TextColor(a.secondaryInk())
		})
		if row.Clicked() {
			t.uiState().ToolOpen[key] = !open
		}
		uikit.Reveal(c.Key(key+"-reveal"), open, func() {
			ui.Scroll(c.Key(key + "-entries")).FillWidth().Height(min(float32(240), float32(len(entries))*28)).Shrink(0).Clip().Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for n, e := range entries {
						prefix := "├─"
						if n == len(entries)-1 {
							prefix = "└─"
						}
						ui.Row(c).Padding(1, 8, 1, 16).Gap(gapInline).AlignItems(ui.Center).Children(func() {
							ui.Text(c, prefix).Font(monoFamily).FontSize(fsSmall).TextColor(a.secondaryInk())
							ui.Text(c, e.verb).FontSize(fsTool).TextColor(a.secondaryInk())
							color := a.secondaryInk()
							if e.state == "error" {
								color = c.Theme().Danger
							}
							uikit.Tooltip(c, ui.Text(c, short(e.title, 80)).FontSize(fsTool).TextColor(color).SingleLine(), e.title)
							if e.detail != "" {
								uikit.ErrorText(c, e.detail).FontSize(fsAux).Grow(1)
							}
						})
					}
				})
			})
			if remaining := end - index + 1 - len(entries); remaining > 0 {
				transcriptAction(c, key+"-more", fmt.Sprintf("Show more (%d)", remaining)).OnClick(func() { *extra += 100 })
			}
			if *extra > 0 {
				transcriptAction(c, key+"-less", "Show less").OnClick(func() { *extra = 0 })
			}
		})
	})
	_ = end
}

func (a *desktop) toolCard(c *ui.Context, t *conversation, index int, m map[string]any) {
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	if name == "" {
		name = "tool"
	}
	name = toolKind(name)
	state := toolState(m)
	desc := describeTool(name)
	verb := desc.doneVerb
	switch state {
	case "running", "pending":
		verb = desc.runningVerb
	case "error":
		verb = desc.failedVerb
	}
	container := ui.Column(c.Key("tool-" + toolCardKey(t, index, m))).Gap(2)
	parsed := ui.Local(container, "parsed-args", func() toolArgsMemo { return toolArgsMemo{} })
	args := parsed.get(str(m, "args"))
	verb = toolActionVerb(name, state, args, verb)
	summary := toolArgSummary(name, args)
	isMCP := str(m, "mcpServer") != "" || str(m, "mcpTool") != "" || strings.HasPrefix(name, "mcp")
	if isMCP {
		verb = "Called MCP"
		if state == "running" || state == "pending" {
			verb = "Calling MCP"
		}
		summary = strings.Trim(str(m, "mcpServer")+"/"+str(m, "mcpTool"), "/")
		if summary == "" {
			summary = name
		}
	}
	result := toolResult(m)
	if name == "scheduled_task" {
		summary = a.scheduledTaskSummary(args, result)
	}
	expandable := desc.expandable && !isMCP && name != "screenshot" && name != "image_generate" && name != "image_edit"
	if isMCP {
		desc.showTiming = true
	}
	key := str(m, "toolCallId")
	if key == "" {
		key = fmt.Sprintf("%s-%d", t.ID, index)
	}
	open := t.uiState().ToolOpen[key]
	p := a.currentPalette()
	// Tool rows mirror main's .rich-tool-card: transparent, borderless, with a
	// hanging status icon; the row itself carries the 6px rhythm below.
	container.Children(func() {
		row := ui.ButtonBase(c.Key(key+"-header")).FillWidth().Height(24).Padding(0, 8, 0, 0).Gap(4).AlignItems(ui.Center).Justify(ui.Start).Label("工具详情 " + key)
		if expandable {
			row.Cursor(ui.CursorPointer)
		}
		row.Hidden(name == "suggest").Children(func() {
			ui.Box(c).Absolute().Left(-20).Top(0).Width(16).HeightPercent(100).Center().Children(func() { toolStatusIcon(c, state) })
			verbColor := p.accent
			if state == "error" || name == "delete" || name == "remote_delete_path" {
				verbColor = c.Theme().Danger
			}
			ui.Text(c, verb).FontSize(fsTool).TextColor(verbColor).Shrink(0).LineHeight(1.4).NoWrap()
			if summary != "" {
				text := "(" + short(summary, 120) + ")"
				if name == "command" || name == "remote_run_command" {
					uikit.Tooltip(c, ui.RichText(c.Key(key+"-summary"), codeSpansWithBase(c, text, "command.sh", commandLineInk(c.Theme(), a.secondaryInk()))...).FontSize(fsTool).SingleLine().MinWidth(0).Shrink(1), summary)
				} else {
					color := a.secondaryInk()
					if name == "edit" || name == "create" || name == "delete" || name == "remote_edit" || name == "remote_create_file" || name == "remote_delete_path" {
						color = color.Mix(c.Theme().Text, 0.4)
					}
					uikit.Tooltip(c, ui.Text(c.Key(key+"-summary"), text).FontSize(fsTool).TextColor(color).LineHeight(1.4).SingleLine().MinWidth(0).Shrink(1), summary)
				}
			}
			if isMCP {
				label := map[string]string{"success": "成功", "error": "失败", "running": "运行中", "pending": "等待中", "cancelled": "已取消"}[state]
				ui.Text(c, label).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
			}
			if name == "screenshot" || name == "image_generate" || name == "image_edit" {
				if w, h := int(num(result, "width")), int(num(result, "height")); w > 0 && h > 0 {
					ui.Text(c, fmt.Sprintf("%d × %d", w, h)).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
				if size := int64(num(result, "bytes")); size > 0 {
					ui.Text(c, fmtBytes(size)).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			}
			if name == "command" || name == "remote_run_command" {
				// Exit status and duration describe the command, so they sit right
				// after it instead of being pushed to the row's far edge.
				if code, exists := result["exitCode"]; exists {
					ui.Text(c, fmt.Sprintf("exit %v", code)).FontSize(fsTool - 2).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
				if boolOf(result, "timedOut") {
					neutralChip(c, "超时")
				}
				if boolOf(result, "promotedToService") {
					neutralChip(c, "已转后台服务")
				}
				if ms := toolDuration(m, result); ms > 0 || state == "success" || state == "error" || state == "cancelled" {
					ui.Text(c, fmtDur(ms)).FontSize(fsTool - 2).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			}
			if name == "wait" && state == "running" {
				if started, ok := m["startedAt"].(time.Time); ok {
					remaining := int(num(args, "seconds")) - int(time.Since(started).Seconds())
					if remaining < 0 {
						remaining = 0
					}
					neutralChip(c, fmt.Sprintf("剩余 %ds", remaining))
					c.After(time.Second)
				}
			}
			if name == "http_request" || name == "web_fetch" {
				if status := int(num(result, "status")); status > 0 {
					ui.Text(c, fmt.Sprintf("%d", status)).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
				if size := int64(num(result, "bytesRead")); size > 0 {
					ui.Text(c, fmtBytes(size)).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			}
			chips := toolChips(name, state, args, result)
			if isMCP {
				chips = nil
			}
			for _, extra := range chips {
				if (name == "edit" || name == "remote_edit") && strings.HasPrefix(extra, "+") {
					parts := strings.Fields(extra)
					if len(parts) > 0 {
						ui.Text(c, parts[0]).FontSize(fsAux).TextColor(c.Theme().Success).NoWrap()
					}
					if len(parts) > 1 {
						ui.Text(c, parts[1]).FontSize(fsAux).TextColor(c.Theme().Danger).NoWrap()
					}
				} else if name == "calculate" {
					ui.Text(c, extra).FontSize(fsTool).TextColor(a.secondaryInk()).NoWrap()
				} else {
					neutralChip(c, extra)
				}
			}
			if boolOf(m, "fastCompacted") {
				neutralChip(c, fastCompactChip)
			}
			// A command's duration already sits next to its exit status.
			if desc.showTiming && name != "command" && name != "remote_run_command" {
				if ms := toolDuration(m, result); ms > 0 || state == "success" || state == "error" || state == "cancelled" {
					ui.Text(c, fmtDur(ms)).FontSize(fsTool - 2).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			}
		})
		if isMCP {
			return
		} // Result payloads stay in model context, outside the UI.
		if expandable && row.Clicked() {
			t.uiState().ToolOpen[key] = !open
			if name == "command" {
				if open {
					delete(t.uiState().CommandDetails, key)
				} else if state != "pending" && state != "running" {
					a.loadCommandDetail(t, key)
				}
			}
		}
		// Local command output (including failures) is entirely demand-loaded.
		if name == "command" {
			if open {
				a.commandDetailView(c, t, index, key, args, m)
			}
			return
		}
		body := ui.Column(c).FillWidth().MinWidth(0).Gap(4)
		inset := float32(0)
		if name == "create" || name == "remote_create_file" {
			inset += 4
		}
		body.Padding(0, 0, 0, inset)
		body.Children(func() {
			switch {
			case state == "cancelled":
				cancelledResult(c, a.secondaryInk())
			case state == "error" && name != "delete" && name != "remote_delete_path":
				errText := str(m, "body")
				if errText == "" {
					errText = str(m, "error")
				}
				if boolOf(result, "cancelled") {
					errText = "已取消"
				}
				if reason := str(result, "error"); reason != "" {
					errText = reason
				}
				// Keep diagnostics in context; the visible error is one ellipsized line.
				ui.Row(c).FillWidth().Padding(4, 9).Gap(gapField).Radius(6).Background(ui.RGBA(239, 68, 68, 0.08)).
					Border(1, ui.RGBA(239, 68, 68, 0.2)).Children(func() {
					uikit.ErrorText(c, errText).FontSize(fsAux)
				})
			case (name == "edit" || name == "remote_edit") && len(list(result, "files")) > 0:
				for n, item := range list(result, "files") {
					if file, ok := item.(map[string]any); ok {
						if len(list(result, "files")) > 1 {
							ui.Text(c, str(file, "path")).FontSize(fsSmall).TextColor(a.secondaryInk())
						}
						a.diffBlock(c, fmt.Sprintf("%s-file-%d", key, n), str(file, "diff"), !open)
					}
				}
			case name == "delete" || name == "remote_delete_path":
				toolDeleteTree(c, args, result, a.secondaryInk())
				if state == "error" {
					reason := str(m, "error")
					if reason == "" {
						reason = str(result, "error")
					}
					notice(c, "error", reason, a.secondaryInk())
				}
			case (name == "edit" || name == "remote_edit") && str(result, "diff") != "":
				a.diffBlock(c, key+"-diff", str(result, "diff"), !open)
			case name == "create" || name == "remote_create_file":
				content := str(args, "content")
				startLine := 1
				if content == "" {
					content = str(result, "content")
				}
				if !open {
					startLine = max(1, strings.Count(strings.TrimSuffix(content, "\n"), "\n")+1-5)
					content = tailLines(content, 6)
				}
				if content != "" {
					if state == "running" || state == "pending" {
						ui.Box(c).FillWidth().Height(150).Clip().Children(func() {
							toolSourceWindow(c, key+"-source", content, str(args, "path"), false, startLine, a.secondaryInk())
						})
					} else {
						toolSourceWindow(c, key+"-source", content, str(args, "path"), open, startLine, a.secondaryInk())
					}
				}
			case name == "suggest":
				ui.Row(c).Wrap().Gap(6).Children(func() {
					for n, raw := range list(result, "items") {
						text := fmt.Sprint(raw)
						suggestionButton(c.Key(fmt.Sprintf("%s-suggest-%d", key, n)), text, a.secondaryInk()).OnClick(func() {
							t.Draft = text
							if a.core != nil {
								a.send(t, text)
							}
						})
					}
				})
			case name == "service" || name == "scheduled_task" || name == "ssh_cluster":
				a.toolResourceCards(c, key, name, result, open)
			case name == "remote_transfer":
				toolFields(c, result, []string{"action", "target", "localPath", "remotePath", "fileCount", "dirCount", "totalBytes"}, a.secondaryInk())
			case name == "http_request" || name == "web_fetch":
				// Network tool summaries stay on the header line.
			case name == "render_html":
				html := str(args, "html")
				if state == "running" || state == "pending" {
					// 工具还没回，没有结果就没有可载入的页面；把已经到达的 HTML 尾部显示
					// 出来，让人看见页面在长，而不是只看到一个等着点的空按钮。
					if strings.TrimSpace(html) == "" {
						ui.Text(c, "正在生成页面…").FontSize(fsSmall).TextColor(a.secondaryInk())
					} else {
						toolCodeWindow(c, key+"-html-stream", tailLines(html, 18), true, "preview.html", a.secondaryInk())
					}
					break
				}
				a.toolHTMLView(c, t, m)
				ui.Row(c).Gap(8).Children(func() {
					flat(c, "导出 HTML").OnClick(func() { a.exportHTML("preview.html", html) })
					flat(c, "在浏览器打开").OnClick(func() { a.openHTMLPreview(html) })
				})
				if open {
					toolCodeWindow(c, key+"-html-source", html, true, "preview.html", a.secondaryInk())
				}

			case name == "image_generate" || name == "image_edit" || name == "screenshot":
				// Metadata belongs to the title; captured/generated images remain below.
			case name == "ask":
				if state == "running" && len(t.Questions) > 0 {
					a.askComposer(c, t)
				} else {
					toolAskSummary(c, key, args, result)
				}
			case name == "subagent":
				found := false
				for n, child := range t.Messages {
					if str(child, "role") == "subagent" && subagentParent(t, child) == str(m, "toolCallId") {
						a.subagentRow(c, t, n, child)
						found = true
					}
				}
				if !found && open {
					if summary := str(result, "summary"); summary != "" {
						fieldNote(c, summary, a.secondaryInk())
					}
				}
			case name == "skill":
				if path := str(result, "path"); path != "" {
					fieldNote(c, path, a.secondaryInk())
				}
			case name == "plan":
				// The shared plan panel below the timeline owns the steps.
			case expandable && !open:
				lines := 4
				if name == "command" || name == "remote_run_command" {
					// A command's detail is the tail of its output; one line says what
					// it printed, and expanding the card still shows all of it.
					lines = 1
				}
				if preview := toolPreview(name, args, result, lines); preview != "" {
					toolCodeWindow(c, key+"-preview", preview, false, "", a.secondaryInk())
				}
			case expandable && open:
				a.toolBody(c, t, index, name, args, result, m)
			}
		})
		a.messageAttachments(c, t, m)
		for _, warning := range list(result, "warnings") {
			toolWarning(c, fmt.Sprint(warning), a.secondaryInk())
		}
		if boolOf(result, "sandboxDenied") {
			toolWarning(c, str(result, "deniedWriteHint"), a.secondaryInk())
		}
		if validation := str(result, "validation"); strings.HasPrefix(validation, "自动校验失败") {
			toolWarning(c, strings.TrimSpace(strings.TrimPrefix(validation, "自动校验失败（文件已写入）：")), a.secondaryInk())
		}
	})
}

func smallMono() string { return monoFamily }

// easeInOut is the cubic ease-in-out main's tool-svg-blink runs under:
// slow at both ends, so the dot dwells at full and dim instead of sweeping
// past them.
func easeInOut(t float32) float32 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	u := 2 - 2*t
	return 1 - u*u*u/2
}

// toolStatusDot is the mark in the transcript gutter: the same filled dot the
// rest of the app marks status with (pillDot), kept small beside command text and
// the same size in every state. A tick and a cross read as two different shapes
// that also shift the line as a call finishes; a dot says the state by colour.
const toolStatusDot = 6

// toolStatusIcon draws the state of a tool call: green once it finished, red when
// it failed, the accent blinking while it runs, and the palette's grey while it
// waits — pillDotColor keeps the grey readable on a light page. Every state owns
// a 16x16 box, so the line never moves.
//
// Running fades 1<->0.3 on a 1.2s ease-in-out loop (main's tool-svg-blink), not a
// cosine sweep — the cosine never sat at either end, so it read as too fast.
// Opacity only: no scale, no rotation (a growing/spinning dot reads as urgency
// instead of "still working").
// toolStatusLabel names a state for assistive technology: the mark is a dot, so
// the colour is the only thing telling a reader which state it is.
func toolStatusLabel(state string) string {
	switch state {
	case "success":
		return "已完成"
	case "error":
		return "失败"
	case "running":
		return "运行中"
	}
	return "等待中"
}

func toolStatusIcon(c *ui.Context, state string) {
	ui.Box(c).Size(16, 16).Center().Label(toolStatusLabel(state)).Children(func() {
		switch state {
		case "running":
			color := c.Theme().Accent
			// Drawn instead of a Loop on a box: Loop asks for a frame that
			// rebuilds the whole view, which a thinking model would do at the
			// display's rate.
			ui.Box(c).Size(toolStatusDot, toolStatusDot).Draw(func(p *ui.Painter, r ui.Rect) {
				p.AnimationFrameRate(30)
				ms := float64(p.Now().UnixMilli()%1200) / 1200
				tri := ms * 2
				if tri > 1 {
					tri = 2 - tri
				}
				p.Fill(r, color.Alpha(0.3+0.7*easeInOut(float32(tri))), toolStatusDot/2)
			})
		default:
			tone := pillNeutral
			switch state {
			case "success":
				tone = pillOK
			case "error":
				tone = pillDanger
			}
			ui.Box(c).Size(toolStatusDot, toolStatusDot).Radius(toolStatusDot / 2).Background(pillDotColor(c, tone))
		}
	})
}

func parseToolArgs(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		// Streaming arguments may end inside a JSON string. Decode complete
		// fields and the available content prefix without inventing a suffix.
		dec := json.NewDecoder(strings.NewReader(raw))
		if _, err := dec.Token(); err == nil {
			for dec.More() {
				token, err := dec.Token()
				if err != nil {
					break
				}
				key, ok := token.(string)
				if !ok {
					break
				}
				var value any
				if err := dec.Decode(&value); err != nil {
					// 流式参数会在某个字符串中间断掉，断在哪个字段都可能：把已经到达的
					// 那一段交出去，而不是整个字段丢掉。以前这里只认 content，于是
					// render_html 的 html 字段永远取不到前缀，卡片只能显示一个空按钮，
					// 直到工具跑完才突然出现成品。
					if err == io.ErrUnexpectedEOF || err == io.EOF {
						rest := strings.TrimSpace(raw[dec.InputOffset():])
						rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
						if strings.HasPrefix(rest, "\"") {
							for cut := len(rest); cut > 1 && cut >= len(rest)-6; cut-- {
								var text string
								if json.Unmarshal([]byte(rest[:cut]+"\""), &text) == nil {
									out[key] = text
									break
								}
							}
						}
					}
					break
				}
				out[key] = value
			}
		}
		out["_raw"] = raw
		return out
	}
	return out
}

func toolArgSummary(name string, args map[string]any) string {
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k]; ok {
				switch x := v.(type) {
				case string:
					if strings.TrimSpace(x) != "" {
						return x
					}
				case float64:
					return trimZero(x)
				}
			}
		}
		return ""
	}
	switch name {
	case "command", "remote_run_command":
		return pick("command", "cmd")
	case "edit", "create", "read", "delete", "remote_read", "remote_edit", "remote_create_file", "remote_delete_path":
		if p := pick("path", "file_path", "file"); p != "" {
			return p
		}
		if paths, ok := args["paths"].([]any); ok && len(paths) > 0 {
			return fmt.Sprintf("%d paths", len(paths))
		}
	case "grep":
		return pick("pattern")
	case "glob":
		return pick("pattern")
	case "list":
		return pick("path")
	case "http_request", "web_fetch":
		return pick("url")
	case "calculate":
		return pick("expression")
	case "plan":
		for _, step := range list(args, "steps") {
			if title, ok := step.(string); ok && title != "" {
				return title
			}
		}
		return pick("finish")
	case "skill":
		return pick("name", "skill")
	case "ask":
		return pick("question")
	case "subagent":
		return pick("description", "task", "prompt")
	case "ssh_cluster":
		return pick("action", "alias")
	case "service":
		return pick("action", "name", "id")
	case "scheduled_task":
		return pick("action", "name")
	case "render_html":
		return pick("title")
	case "image_generate", "image_edit":
		return pick("prompt")
	case "wait":
		if v := pick("seconds"); v != "" {
			return v + "s"
		}
	}

	return pick("path", "name", "title", "action")
}

// normalizeToolMessage runs at ingestion, replacing the wire JSON with its
// decoded value. Keep one representation rather than a raw string plus a cache.
func normalizeToolMessage(m map[string]any) {
	raw, ok := m["result"].(string)
	if !ok {
		if _, exists := m["result"].(map[string]any); exists {
			return
		}
		raw, ok = m["body"].(string)
	}
	if !ok || raw == "" {
		return
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(raw), &decoded) != nil || decoded == nil {
		decoded = map[string]any{"_text": raw}
	}
	m["result"] = decoded
	// Result events used to retain the same wire string in body as well.
	if body, ok := m["body"].(string); ok && body == raw && str(m, "toolState") != "tool:error" {
		delete(m, "body")
	}
}

// toolResult also accepts legacy wire values; live/history ingestion normalizes
// them once, so drawing reads maps without decoding JSON.

func toolResult(m map[string]any) map[string]any {
	switch raw := m["result"].(type) {
	case string:
		var out map[string]any
		if json.Unmarshal([]byte(raw), &out) == nil {
			return unwrapToolResult(out)
		}
		return map[string]any{"_text": raw}
	case map[string]any:
		return unwrapToolResult(raw)
	}
	if body, ok := m["body"].(string); ok && body != "" {
		var out map[string]any
		if json.Unmarshal([]byte(body), &out) == nil {
			return unwrapToolResult(out)
		}
		return map[string]any{"_text": body}
	}
	return map[string]any{}
}

// The runtime wraps every tool payload in {ok, data}; keep the envelope
// out of presentation while retaining errors for failed operations.
func unwrapToolResult(raw map[string]any) map[string]any {
	if data, ok := raw["data"].(map[string]any); ok {
		// These are UI-owned maps. Merge envelope metadata in place instead of
		// allocating and copying the entire payload on every render.
		out := data
		for _, k := range []string{"error", "errorCode", "ok", "warnings"} {
			if _, exists := out[k]; !exists && raw[k] != nil {
				if k != "ok" || raw[k] == false {
					out[k] = raw[k]
				}
			}
		}
		return out
	}
	return raw
}

func resultText(result map[string]any) string {
	for _, k := range []string{"output", "stdout", "text", "content", "body", "_text", "summary", "message", "error"} {
		if v, ok := result[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	if raw, err := json.MarshalIndent(result, "", "  "); err == nil && len(result) > 0 {
		return string(raw)
	}
	return ""
}

// commandOutput keeps protocol metadata out of the terminal transcript.
func commandOutput(result map[string]any) string {
	var parts []string
	for _, key := range []string{"output", "stdout", "stderr", "text", "_text"} {
		if value := str(result, key); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

// stripBlankLines drops empty and whitespace-only lines. Shell output arrives
// padded with blank lines that make the transcript look ragged; main's terminal
// view trims them the same way.
func stripBlankLines(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// Match main's collapsed terminal white-space: pre-line. Only the display
// preview collapses horizontal whitespace; expanded output keeps indentation.
func commandPreviewText(output string, lines int) string {
	preview := tailLines(stripBlankLines(output), lines)
	rows := strings.Split(preview, "\n")
	for i, row := range rows {
		rows[i] = strings.Join(strings.FieldsFunc(row, func(r rune) bool { return r == ' ' || r == 9 || r == 12 || r == 11 }), " ")
	}
	return strings.Join(rows, "\n")
}

func toolChips(name, state string, args, result map[string]any) []string {
	var chips []string
	if name == "read" || name == "batch_read" || name == "grep" || name == "list" || name == "glob" {
		return nil
	}
	switch name {
	case "edit", "remote_edit":
		if state == "success" {
			added := int(num(result, "addedLines"))
			removed := int(num(result, "removedLines"))
			if added > 0 || removed > 0 {
				chips = append(chips, fmt.Sprintf("+%d -%d", added, removed))
			}
		}
	case "read", "remote_read":
		if total := int(num(result, "totalLines")); total > 0 {
			chips = append(chips, fmt.Sprintf("%d lines", total))
		} else if next := int(num(result, "nextStartLine")); next > 0 {
			chips = append(chips, "已截断")
		}
	case "calculate":
		if v, ok := result["text"]; ok {
			chips = append(chips, "= "+fmt.Sprint(v))
		} else if v, ok := result["value"]; ok {
			chips = append(chips, "= "+fmt.Sprint(v))
		} else if v, ok := result["result"]; ok {
			chips = append(chips, fmt.Sprint(v))
		}
	case "grep":
		if n := len(list(result, "matches")); n > 0 {
			chips = append(chips, fmt.Sprintf("%d 命中", n))
		} else if c := int(num(result, "count")); c > 0 {
			chips = append(chips, fmt.Sprintf("%d 命中", c))
		}
	case "list":
		if c := int(num(result, "count")); c > 0 {
			chips = append(chips, fmt.Sprintf("%d 项", c))
		}
	case "ask":
		if qs := list(result, "questions"); len(qs) > 0 {
			chips = append(chips, fmt.Sprintf("%d 个问题", len(qs)))
		}
	case "wait":
		if s := int(num(args, "seconds")); s > 0 {
			chips = append(chips, fmt.Sprintf("%ds", s))
		}
	case "subagent":
		if steps := int(num(result, "steps")); steps > 0 {
			chips = append(chips, fmt.Sprintf("%d 步", steps))
		}
	}
	if trunc, _ := result["truncated"].(bool); trunc {
		if name == "screenshot" {
			// A window enumeration limit is not a truncated screenshot or text.
			if total := int(num(result, "total")); total > len(list(result, "windows")) {
				chips = append(chips, fmt.Sprintf("%d / %d 个窗口", len(list(result, "windows")), total))
			}
		} else {
			chips = append(chips, "已截断")
		}
	}
	return chips
}

// toolPreview returns the collapsed preview body for a card.
func toolPreview(name string, args, result map[string]any, lines int) string {
	switch name {
	case "command", "remote_run_command":
		return commandPreviewText(commandOutput(result), lines)
	case "edit", "remote_edit":
		if diff := str(result, "diff"); diff != "" {
			return tailLines(unifiedSnippet(diff, 8), lines)
		}
		return tailLines(str(result, "changedLinesBlock"), lines)
	case "create", "remote_create_file":
		return tailLines(resultText(result), lines)
	case "list":
		var entries []string
		for _, item := range list(result, "entries") {
			if m, ok := item.(map[string]any); ok {
				entries = append(entries, str(m, "path"))
			} else {
				entries = append(entries, fmt.Sprint(item))
			}
		}
		if len(entries) == 0 {
			return tailLines(resultText(result), lines)
		}
		return strings.Join(head(entries, 8), "\n")
	case "render_html":
		if html := str(args, "html"); html != "" {
			return tailLines(html, lines)
		}
		return tailLines(str(result, "source"), lines)
	case "delete", "remote_delete_path":
		var paths []string
		for _, item := range list(result, "results") {
			if m, ok := item.(map[string]any); ok {
				paths = append(paths, str(m, "path"))
			}
		}
		if len(paths) == 0 {
			return ""
		}
		return strings.Join(paths, "\n")
	}
	return tailLines(resultText(result), lines)
}

func (a *desktop) toolBody(c *ui.Context, t *conversation, index int, name string, args, result map[string]any, m map[string]any) {
	switch name {
	case "edit", "remote_edit":
		diff := str(result, "diff")
		if diff != "" {
			a.diffBlock(c, fmt.Sprintf("tool-diff-%d", index), diff, true)
			return
		}
		if block := str(result, "changedLinesBlock"); block != "" {
			codeBlock(c, fmt.Sprintf("tool-changed-%d", index), block, 150, a.secondaryInk())
			return
		}
		codeBlock(c, fmt.Sprintf("tool-edit-%d", index), resultText(result), 150, a.secondaryInk())
	case "command", "remote_run_command":
		output := stripBlankLines(commandOutput(result))
		if output == "" {
			output = "(没有输出)"
		}
		toolCodeWindow(c, fmt.Sprintf("tool-out-%d", index), output, true, "", a.secondaryInk())
	case "create", "remote_create_file":
		codeBlock(c, fmt.Sprintf("tool-create-%d", index), resultText(result), 150, a.secondaryInk())
	case "list":
		a.toolListBody(c, index, result)
	case "render_html":
		html := str(args, "html")
		if html == "" {
			html = str(result, "source")
		}
		codeBlock(c, fmt.Sprintf("tool-html-%d", index), html, 150, a.secondaryInk())
		ui.Row(c).Gap(gapField).Children(func() {
			flat(c, "导出 HTML").OnClick(func() {
				a.exportHTML(fmt.Sprintf("render-%d.html", index), html)
			})
			flat(c, "在浏览器打开").OnClick(func() { a.openHTMLPreview(html) })
		})
	case "delete", "remote_delete_path":
		for _, item := range list(result, "results") {
			row, ok := item.(map[string]any)
			if !ok {
				continue
			}
			path := str(row, "path")
			okFlag := boolOf(row, "ok")
			absent := boolOf(row, "absent")
			ui.Row(c).Gap(gapField).Children(func() {
				ui.Text(c, "├─").Font(monoFamily).FontSize(fsSmall).TextColor(a.secondaryInk())
				ui.Text(c, path).Font(monoFamily).FontSize(fsSmall)
				if absent {
					neutralChip(c, "本就不存在")
				} else if okFlag {
					chip(c, "已删除", c.Theme().Success, ui.RGBA(78, 200, 126, 0.12))
				} else {
					uikit.ErrorText(c, str(row, "error")).Grow(1)
				}
			})
		}
	case "remote_transfer":
		for _, pair := range [][2]string{{"方向", str(result, "direction")}, {"来源", str(result, "source")}, {"目标", str(result, "target")}, {"文件数", fmt.Sprint(int(num(result, "files")))}, {"目录数", fmt.Sprint(int(num(result, "dirs")))}} {
			if pair[1] != "" && pair[1] != "0" {
				kv(c, pair[0], pair[1], a.secondaryInk())
			}
		}
	case "ask":
		if qs := list(result, "questions"); len(qs) > 0 {
			for _, q := range qs {
				if qm, ok := q.(map[string]any); ok {
					ui.Text(c, str(qm, "question")).FontSize(fsSmall)
				}
			}
		}
	default:
		text := resultText(result)
		if text == "" {
			fieldNote(c, "没有可显示的内容。", a.secondaryInk())
			return
		}
		codeBlock(c, fmt.Sprintf("tool-body-%d", index), text, 150, a.secondaryInk())
	}
}

func (a *desktop) toolListBody(c *ui.Context, index int, result map[string]any) {
	entries := list(result, "entries")
	if len(entries) == 0 {
		fieldNote(c, resultText(result), a.secondaryInk())
		return
	}
	ui.Column(c).Key(fmt.Sprintf("tool-list-%d", index)).Gap(1).Children(func() {
		for _, item := range entries {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			path := str(m, "path")
			row := ui.Row(c).Gap(gapField).AlignItems(ui.Center)
			row.Children(func() {
				glyph := "file"
				if boolOf(m, "dir") {
					glyph = "folder"
				}
				iconGlyph(c, glyph, 12).TextColor(a.secondaryInk())
				ui.Text(c, path).Font(monoFamily).FontSize(fsSmall)
				if size := int64(num(m, "size")); size > 0 && !boolOf(m, "dir") {
					ui.Text(c, fmtBytes(size)).FontSize(fsTiny).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			})
		}
	})
}

// diffBlock keeps vertical rows aligned and gives each side its own native
// horizontal scroller. Long lines never wrap or truncate.
func (a *desktop) diffBlock(c *ui.Context, key, diff string, compact bool) {
	row := ui.Row(c.Key(key)).FillWidth().MinWidth(0).Shrink(0).AlignItems(ui.Stretch)
	memo := ui.Local(row, "diff", func() diffMemo { return diffMemo{} })
	pairs := memo.get(diff)
	limit := memo.limit(compact)
	visible := pairs[:min(len(pairs), limit)]
	row.Children(func() {
		for side := 0; side < 2; side++ {
			ui.ScrollHorizontal(c.Key(fmt.Sprintf("%s-side-%d", key, side))).Grow(1).Basis(0).MinWidth(0).Children(func() {
				ui.Column(c).Children(func() {
					for _, pair := range visible {
						if strings.HasPrefix(pair.left, "@@") || strings.HasPrefix(pair.left, "---") || strings.HasPrefix(pair.right, "+++") {
							continue
						}
						if pair.gap > 0 {
							ui.Text(c, fmt.Sprintf("… %d 行未变更 …", pair.gap)).Height(22).FontSize(fsTiny).TextColor(a.secondaryInk()).NoWrap().Padding(1, 8)
							continue
						}
						text, changed, number := pair.left, pair.removed, pair.leftNumber
						if side == 1 {
							text, changed, number = pair.right, pair.added, pair.rightNumber
						}
						diffCellNoWrap(c, text, changed, number, a.secondaryInk())
					}
				})
			})
		}
	})
	if len(visible) < len(pairs) {
		transcriptAction(c, key+"-more", fmt.Sprintf("Show more (%d)", len(pairs)-len(visible))).OnClick(func() { memo.extra += 120 })
	}
	if memo.extra > 0 {
		transcriptAction(c, key+"-less", "Show less").OnClick(func() { memo.extra = 0 })
	}
}

// diffVisibleLine is one rendered diff row, or a collapsed gap of unchanged
// rows summarized by its count.
type diffVisibleLine struct {
	text string
	gap  int
}

// diffVisibleLines mirrors main's DiffView clustering: changed rows show with 3
// context lines around them and the gaps between windows collapse into
// "… N 行未变更 …" markers. File headers (---/+++) and hunk headers (@@) always
// show. limit trims a diff that has no changes at all; limit <= 0 keeps every row.
func diffVisibleLines(lines []string, limit int, full bool) []diffVisibleLine {
	if full || (limit > 0 && len(lines) <= limit) {
		out := make([]diffVisibleLine, 0, len(lines))
		for _, l := range lines {
			out = append(out, diffVisibleLine{text: l})
		}
		return out
	}
	const context = 3
	keep := make([]bool, len(lines))
	changed := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") || strings.HasPrefix(l, "@@") {
			keep[i] = true
			continue
		}
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			changed++
			for j := i - context; j <= i+context; j++ {
				if j >= 0 && j < len(lines) {
					keep[j] = true
				}
			}
		}
	}
	if changed == 0 {
		n := len(lines)
		if limit > 0 && limit < n {
			n = limit
		}
		out := make([]diffVisibleLine, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, diffVisibleLine{text: lines[i]})
		}
		return out
	}
	var out []diffVisibleLine
	gap := 0
	flush := func() {
		if gap > 0 {
			out = append(out, diffVisibleLine{gap: gap})
			gap = 0
		}
	}
	for i, l := range lines {
		if !keep[i] {
			gap++
			continue
		}
		flush()
		out = append(out, diffVisibleLine{text: l})
	}
	flush()
	return out
}

func (a *desktop) exportHTML(name, html string) {
	a.job(func() error {
		path, err := a.core.ExportTextFile(name, html, "HTML", "*.html")
		if err == nil && path != "" {
			a.win.Update(func() { a.notify("已导出：" + path) })
		}
		return err
	})
}

func (a *desktop) openHTMLPreview(html string) {
	path, err := a.core.ExportTextFile("ally-preview.html", html, "HTML", "*.html")
	if err != nil {
		a.fail(err)
		return
	}
	a.job(func() error { return a.core.OpenPathInFileManager(path) })
}

// ---- Sub-agent rows ------------------------------------------------------

func (a *desktop) subagentRow(c *ui.Context, t *conversation, index int, m map[string]any) {
	status := strings.ToLower(str(m, "status"))
	if status == "completed" {
		status = "success"
	}
	if status == "failed" {
		status = "error"
	}
	if str(m, "error") != "" {
		status = "error"
	} else if status == "success" && str(m, "stopReason") == "step_limit" {
		status = "partial"
	}
	role := str(m, "subagentRole")
	if role == "" {
		role = "子代理"
	}
	tone := toneColor(c, status, a.secondaryInk())
	label := map[string]string{"running": "运行中", "success": "已完成", "partial": "步数已耗尽", "error": "失败", "pending": "等待中"}[status]
	if label == "" {
		label = status
	}
	ui.Column(c).Key(fmt.Sprintf("sub-%s-%d", str(m, "subId"), index)).Padding(10, 12).Gap(gapInline).
		Border(1, c.Theme().Border).Background(c.Theme().Surface).Radius(radiusCard).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			// Main's SubagentInlineCard status dot: 6px, running blinks
			// 1<->0.3 on a 1.2s ease-in-out loop in the accent tone;
			// finished states are static dots in their own tones.
			dot := status
			if dot != "running" && dot != "success" && dot != "partial" && dot != "error" {
				dot = "pending"
			}
			if dot == "running" {
				color := c.Theme().Accent
				ui.Box(c).Size(6, 6).Draw(func(p *ui.Painter, r ui.Rect) {
					p.AnimationFrameRate(30)
					ms := float64(p.Now().UnixMilli()%1200) / 1200
					tri := ms * 2
					if tri > 1 {
						tri = 2 - tri
					}
					p.Fill(r, color.Alpha(0.3+0.7*easeInOut(float32(tri))), 3)
				})
			} else {
				ui.Box(c).Size(6, 6).Radius(3).Background(pillDotColor(c, pillToneOf(status)))
			}
			ui.Text(c, role).FontSize(fsBody).Bold()
			ui.Spacer(c)
			ui.Text(c, label).FontSize(fsTiny).TextColor(tone)
			ms := int64(num(m, "durationMs"))
			if status == "running" && num(m, "startTime") > 0 {
				ms = time.Now().UnixMilli() - int64(num(m, "startTime"))
				c.After(time.Second)
			}
			if ms > 0 {
				ui.Text(c, fmtDur(ms)).Font(monoFamily).FontSize(fsTiny).TextColor(a.secondaryInk())
			}
		})
		if desc := str(m, "description"); desc != "" {
			uikit.Tooltip(c, ui.Text(c, desc).FontSize(fsSmall).TextColor(a.secondaryInk()).MaxLines(2), desc)
		}
		ui.Row(c).Gap(gapInline).Wrap().AlignItems(ui.Center).Children(func() {
			if steps := int(num(m, "steps")); steps > 0 {
				label := fmt.Sprintf("%d 步", steps)
				if limit := int(num(m, "maxSteps")); limit > 0 {
					label = fmt.Sprintf("%d/%d 步", steps, limit)
				}
				neutralChip(c, label)
			}
			if ctxTokens := int(num(m, "contextTokens")); ctxTokens > 0 {
				neutralChip(c, "上下文 "+fmtK(ctxTokens))
			}
			hit, miss := int(num(m, "cacheHitTokens")), int(num(m, "cacheMissTokens"))
			if hit+miss > 0 {
				neutralChip(c, fmt.Sprintf("缓存 %d%%", hit*100/(hit+miss)))
			}
			in, out := int(num(m, "inputTokens")), int(num(m, "outputTokens"))
			if in+out > 0 {
				neutralChip(c, fmt.Sprintf("↑%s ↓%s", fmtK(in), fmtK(out)))
			}
			ui.Spacer(c)
			summary, errText := str(m, "summary"), str(m, "error")
			hasReport := (status != "running" && summary != "") || errText != ""
			open := t.uiState().ToolOpen["sub-"+str(m, "subId")]
			reportButton, _ := buttonParts(c, "sub-report-"+str(m, "subId"), "", btnSecondary, true, "")
			reportButton.Disabled(!hasReport).OnClick(func() { t.uiState().ToolOpen["sub-"+str(m, "subId")] = !open })
			reportButton.Children(func() {
				ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
					disclosureIcon(c, "file", a.secondaryInk())
					ui.Text(c, "报告").FontSize(uikit.ButtonFontSmall)
					caret := "chevron"
					if open {
						caret = "chevrondown"
					}
					iconGlyph(c, caret, 10).Shrink(0)
				})
			})
		})
		uikit.Reveal(c.Key("sub-report-reveal"), t.uiState().ToolOpen["sub-"+str(m, "subId")], func() {
			if errText := str(m, "error"); errText != "" {
				notice(c, "error", errText, a.secondaryInk())
			}
			if summary := str(m, "summary"); summary != "" {
				ui.Scroll(c.Key("sub-report-body-" + str(m, "subId"))).FillWidth().Height(240).Shrink(0).Clip().Children(func() { ui.Text(c, summary).FontSize(fsSmall).Selectable().LineHeight(1.6) })
			}
		})
	})
}

// ---- Small string helpers used by cards ---------------------------------

func head(items []string, n int) []string {
	if len(items) > n {
		return items[:n]
	}
	return items
}

func tailLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func shortBlock(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "\n…（内容已截断）"
	}
	return s
}

func unifiedSnippet(diff string, context int) string {
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") || strings.HasPrefix(l, "@@") {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return diff
	}
	return strings.Join(out, "\n")
}

var _ = markdown.View

func isFoldableCommand(m map[string]any) bool {
	if str(m, "role") != "tool_call" && str(m, "toolCallId") == "" {
		return false
	}
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	// Membership depends on tool kind, never execution state: pending and
	// streaming calls enter the same group they keep after completion.
	return toolKind(name) == "command"
}

func (a *desktop) commandGroupCard(c *ui.Context, t *conversation, index int) {
	_, limit := a.chatMessageWindow(t)
	end := index
	for end < limit && isFoldableCommand(t.Messages[end]) {
		end++
	}
	key := fmt.Sprintf("%s-commandgroup-%d", t.ID, index)
	open := t.uiState().ToolOpen[key]
	container := ui.Column(c.Key(key)).FillWidth().MinWidth(0).Gap(2)
	extra := ui.Local(container, "entries-extra", func() int { return 0 })
	if !open {
		*extra = 0
	}
	visibleEnd := min(end, index+100+*extra)
	container.Children(func() {
		row := ui.ButtonBase(c.Key(key + "-header")).FillWidth().Height(24).Justify(ui.Start).Gap(4).Cursor(ui.CursorPointer).Label("命令分组 " + key)
		row.Children(func() {
			disclosureIcon(c, "terminal", a.secondaryInk())
			ui.Text(c, fmt.Sprintf("执行命令 %d 次", end-index)).FontSize(fsTool).TextColor(a.secondaryInk()).SingleLine()
			caret := "chevron"
			if open {
				caret = "chevrondown"
			}
			iconGlyph(c, caret, 12).Shrink(0).TextColor(a.secondaryInk())
		})
		if row.Clicked() {
			t.uiState().ToolOpen[key] = !open
			if open {
				for i := index; i < end; i++ {
					child := str(t.Messages[i], "toolCallId")
					if child == "" {
						child = fmt.Sprintf("%s-%d", t.ID, i)
					}
					delete(t.uiState().ToolOpen, child)
					delete(t.uiState().CommandDetails, child)
				}
			}
		}
		uikit.Reveal(c.Key(key+"-reveal"), open, func() {
			ui.Column(c).FillWidth().MinWidth(0).Gap(10).Children(func() {
				for i := index; i < visibleEnd; i++ {
					a.toolCard(c, t, i, t.Messages[i])
				}
			})
			if visibleEnd < end {
				transcriptAction(c, key+"-more", fmt.Sprintf("Show more (%d)", end-visibleEnd)).OnClick(func() { *extra += 100 })
			}
			if *extra > 0 {
				transcriptAction(c, key+"-less", "Show less").OnClick(func() { *extra = 0 })
			}
		})
	})
}
