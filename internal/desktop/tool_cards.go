// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"fmt"
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
// completion: "已编辑" on a failed edit misleads the reader.
var toolTable = map[string]toolDescriptor{
	"read":               {"读取中", "已读取", "读取失败", false, false},
	"remote_read":        {"读取中", "已读取", "远程读取失败", false, false},
	"edit":               {"编辑中", "已编辑", "编辑失败", true, false},
	"create":             {"创建中", "已创建", "创建失败", true, false},
	"delete":             {"删除中", "已删除", "删除失败", true, false},
	"remote_delete_path": {"删除中", "已删除", "远程删除失败", true, false},
	"remote_edit":        {"编辑中", "已编辑", "远程编辑失败", true, false},
	"remote_create_file": {"创建中", "已创建", "远程创建失败", true, false},
	"command":            {"运行中", "已运行", "运行失败", true, true},
	"remote_run_command": {"运行中", "已运行", "远程命令失败", true, true},
	"grep":               {"搜索中", "搜索", "搜索失败", false, false},
	"list":               {"列目录中", "列目录", "列目录失败", true, false},
	"glob":               {"匹配中", "匹配文件", "匹配失败", true, false},
	"http_request":       {"请求中", "已请求", "请求失败", true, false},
	"web_fetch":          {"抓取中", "已抓取", "抓取失败", true, false},
	"calculate":          {"计算中", "计算", "计算失败", false, false},
	"plan":               {"更新中", "计划", "计划失败", false, false},
	"skill":              {"加载中", "技能", "技能失败", false, false},
	"ask":                {"提问中", "提问", "提问失败", true, false},
	"subagent":           {"委派中", "子代理", "委派失败", true, true},
	"ssh_cluster":        {"检查中", "SSH 集群", "SSH 检查失败", true, true},
	"service":            {"运行中", "后台服务", "服务失败", true, true},
	"scheduled_task":     {"处理中", "定时任务", "任务失败", true, true},
	"remote_transfer":    {"传输中", "已传输", "传输失败", true, true},
	"remote_transfer_":   {"传输中", "已传输", "传输失败", true, true},
	"screenshot":         {"截图中", "已截图", "截图失败", true, false},
	"render_html":        {"渲染中", "已渲染", "渲染失败", true, true},
	"wait":               {"等待中", "已等待", "等待失败", false, true},
	"image_generate":     {"生成中", "已生成", "生成失败", true, true},
	"image_edit":         {"编辑中", "已编辑", "生图编辑失败", true, true},
}

func describeTool(name string) toolDescriptor {
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
	status := strings.ToLower(str(m, "status"))
	state := str(m, "toolState")
	switch {
	case status == "error" || state == "tool:error":
		return "error"
	case status == "success" || state == "tool:result":
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
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	switch name {
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
}

// readGrepGroupCard mirrors main's ReadGrepGroupCard: consecutive
// read/grep/list calls render as one collapsed row ("已读取 N 次，搜索 M 次，
// 罗列 K 次 ›"); expanding lists each call in order. The folded row carries
// no status icon and never turns red for a failed child — failures show on
// the expanded entries, same as main.
func (a *desktop) readGrepGroupCard(c *ui.Context, t *conversation, index int) {
	var entries []readGrepGroupEntry
	reads, greps, lists := 0, 0, 0
	end := index
	for i := index; i < len(t.Messages); i++ {
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
		args := parseToolArgs(str(m, "args"))
		title := toolArgSummary(name, args)
		switch name {
		case "grep":
			greps++
			entries = append(entries, readGrepGroupEntry{"搜索", title, ""})
		case "list":
			lists++
			entries = append(entries, readGrepGroupEntry{"罗列", title, ""})
		default:
			reads++
			entries = append(entries, readGrepGroupEntry{"读取", title, ""})
		}
		end = i
	}
	if len(entries) == 0 {
		return
	}
	key := fmt.Sprintf("%s-readgroup-%d", t.ID, index)
	open := t.uiState().ToolOpen[key]
	var parts []string
	if reads > 0 {
		parts = append(parts, fmt.Sprintf("已读取 %d 次", reads))
	}
	if greps > 0 {
		parts = append(parts, fmt.Sprintf("搜索 %d 次", greps))
	}
	if lists > 0 {
		parts = append(parts, fmt.Sprintf("罗列 %d 次", lists))
	}
	ui.Column(c).Key("tool-" + key).Gap(gapInline).Children(func() {
		row := ui.Row(c).Padding(2, 8, 2, 0).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer)
		row.Children(func() {
			ui.Text(c, strings.Join(parts, "，")).FontSize(fsTool).TextColor(c.Theme().TextMuted).LineHeight(1.4).SingleLine()
			caret := "›"
			if open {
				caret = "⌄"
			}
			ui.Text(c, caret).FontSize(fsTool).TextColor(c.Theme().TextMuted)
		})
		if row.Clicked() {
			t.uiState().ToolOpen[key] = !open
		}
		if open {
			for n, e := range entries {
				prefix := "├─"
				if n == len(entries)-1 {
					prefix = "└─"
				}
				ui.Row(c).Padding(1, 8, 1, 0).Gap(gapInline).AlignItems(ui.Center).Children(func() {
					ui.Text(c, prefix).Font(monoFamily).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
					ui.Text(c, e.verb).FontSize(fsTool).TextColor(c.Theme().TextMuted)
					ui.Text(c, short(e.title, 80)).FontSize(fsTool).TextColor(c.Theme().Text).SingleLine().Tooltip(e.title)
				})
			}
		}
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
	state := toolState(m)
	desc := describeTool(name)
	verb := desc.doneVerb
	switch state {
	case "running", "pending":
		verb = desc.runningVerb
	case "error":
		verb = desc.failedVerb
	}
	args := parseToolArgs(str(m, "args"))
	summary := toolArgSummary(name, args)
	result := toolResult(m)
	expandable := desc.expandable
	key := str(m, "toolCallId")
	if key == "" {
		key = fmt.Sprintf("%s-%d", t.ID, index)
	}
	open := t.uiState().ToolOpen[key]
	p := a.currentPalette()
	// Tool rows mirror main's .rich-tool-card: transparent, borderless, with a
	// hanging status icon; the row itself carries the 6px rhythm below.
	ui.Column(c).Key("tool-" + key).Gap(gapInline).Children(func() {
		row := ui.Row(c).Padding(2, 8, 2, 0).Gap(4).AlignItems(ui.Center)
		if expandable {
			row.Cursor(ui.CursorPointer)
		}
		row.Children(func() {
			toolStatusIcon(c, state)
			verbColor := p.accent
			if state == "error" {
				verbColor = colDanger()
			}
			ui.Text(c, verb).FontSize(fsTool).TextColor(verbColor).LineHeight(1.4).NoWrap()
			if summary != "" {
				ui.Text(c, short(summary, 120)).FontSize(fsTool).TextColor(c.Theme().Text).LineHeight(1.4).SingleLine().Tooltip(summary)
			}
			for _, extra := range toolChips(name, state, args, result) {
				neutralChip(c, extra)
			}
			ui.Spacer(c)
			if desc.showTiming {
				if ms := int64(num(m, "durationMs")); ms > 0 {
					ui.Text(c, fmtDur(ms)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
				}
			}
		})
		if expandable && row.Clicked() {
			t.uiState().ToolOpen[key] = !open
		}
		switch {
		case state == "error":
			errText := str(m, "body")
			if errText == "" {
				errText = str(m, "error")
			}
			// Main's .tool-error-block: full box, red border, 8% red fill.
			ui.Row(c).FillWidth().Padding(4, 9).Gap(gapField).Radius(6).Background(ui.RGBA(239, 68, 68, 0.08)).
				Border(1, ui.RGBA(239, 68, 68, 0.2)).Children(func() {
				ui.Text(c, shortBlock(errText, 1800)).FontSize(fsAux).TextColor(colDanger()).LineHeight(1.45).Selectable()
			})
			a.messageAttachments(c, t, m)
		case expandable && !open:
			if preview := toolPreview(name, args, result, 4); preview != "" {
				codeBlock(c, key+"-preview", preview, 96)
			}
		case expandable && open:
			a.toolBody(c, t, index, name, args, result, m)
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

// toolStatusIcon mirrors main's ToolStatusIcon: every state owns a 16x14 box
// so the swap running->check never shifts the line. Running is a 6px dot
// fading 1<->0.3 on a 1.2s ease-in-out loop (main's tool-svg-blink), not a
// cosine sweep — the cosine never sat at either end, so it read as too fast.
// Opacity only: no scale, no rotation (a growing/spinning dot reads as
// urgency instead of "still working").
func toolStatusIcon(c *ui.Context, state string) {
	switch state {
	case "success":
		ui.Box(c).Width(16).Height(14).Center().Children(func() { iconGlyph(c, "check", 13).TextColor(colSuccess()) })
	case "error":
		ui.Box(c).Width(16).Height(14).Center().Children(func() { iconGlyph(c, "close", 13).TextColor(colDanger()) })
	case "running":
		dot := ui.Box(c).Width(16).Height(14).Center()
		// Loop's ease bends the phase, so sample it linear and ease the
		// triangle myself: the value must be a true 1<->0.3 clock, or the
		// dot reads faster than main's.
		pulse := dot.Loop("tool-pulse", 1200*time.Millisecond, ui.Linear)
		tri := pulse * 2
		if tri > 1 {
			tri = 2 - tri
		}
		opacity := float32(0.3 + 0.7*easeInOut(tri))
		dot.Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(c.Theme().TextMuted).Opacity(opacity)
		})
	default:
		ui.Box(c).Width(16).Height(14).Center().Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Border(1, c.Theme().TextMuted)
		})
	}
}

func parseToolArgs(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		return map[string]any{"_raw": raw}
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
			return fmt.Sprintf("%d 个路径", len(paths))
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
	if raw, ok := args["_raw"].(string); ok {
		return raw
	}
	return pick("path", "name", "title", "action")
}

// toolResult normalizes the result payload: the backend sends a JSON string.
func toolResult(m map[string]any) map[string]any {
	switch raw := m["result"].(type) {
	case string:
		var out map[string]any
		if json.Unmarshal([]byte(raw), &out) == nil {
			return out
		}
		return map[string]any{"_text": raw}
	case map[string]any:
		return raw
	}
	if body, ok := m["body"].(string); ok && body != "" {
		var out map[string]any
		if json.Unmarshal([]byte(body), &out) == nil {
			return out
		}
		return map[string]any{"_text": body}
	}
	return map[string]any{}
}

func resultText(result map[string]any) string {
	for _, k := range []string{"output", "text", "content", "body", "_text", "summary", "message", "error"} {
		if v, ok := result[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	if raw, err := json.MarshalIndent(result, "", "  "); err == nil && len(result) > 0 {
		return string(raw)
	}
	return ""
}

func toolChips(name, state string, args, result map[string]any) []string {
	var chips []string
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
			chips = append(chips, fmt.Sprintf("%d 行", total))
		} else if next := int(num(result, "nextStartLine")); next > 0 {
			chips = append(chips, "已截断")
		}
	case "calculate":
		if v, ok := result["result"]; ok {
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
		chips = append(chips, "已截断")
	}
	return chips
}

// toolPreview returns the collapsed preview body for a card.
func toolPreview(name string, args, result map[string]any, lines int) string {
	switch name {
	case "command", "remote_run_command":
		return tailLines(resultText(result), lines)
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
		if html := str(result, "html"); html != "" {
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
			codeBlock(c, fmt.Sprintf("tool-changed-%d", index), block, 150)
			return
		}
		codeBlock(c, fmt.Sprintf("tool-edit-%d", index), resultText(result), 150)
	case "command", "remote_run_command":
		output := resultText(result)
		if output == "" {
			output = "(没有输出)"
		}
		codeBlock(c, fmt.Sprintf("tool-out-%d", index), output, 150)
		if len(list(result, "warnings")) > 0 {
			for _, w := range list(result, "warnings") {
				fieldNote(c, fmt.Sprint(w))
			}
		}
	case "create", "remote_create_file":
		codeBlock(c, fmt.Sprintf("tool-create-%d", index), resultText(result), 150)
	case "list":
		a.toolListBody(c, index, result)
	case "render_html":
		html := str(result, "html")
		if html == "" {
			html = str(result, "source")
		}
		codeBlock(c, fmt.Sprintf("tool-html-%d", index), html, 150)
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
				ui.Text(c, "├─").Font(monoFamily).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
				ui.Text(c, path).Font(monoFamily).FontSize(fsSmall)
				if absent {
					neutralChip(c, "本就不存在")
				} else if okFlag {
					chip(c, "已删除", colSuccess(), ui.RGBA(78, 200, 126, 0.12))
				} else {
					chip(c, str(row, "error"), colDanger(), ui.RGBA(232, 84, 84, 0.12))
				}
			})
		}
	case "remote_transfer":
		for _, pair := range [][2]string{{"方向", str(result, "direction")}, {"来源", str(result, "source")}, {"目标", str(result, "target")}, {"文件数", fmt.Sprint(int(num(result, "files")))}, {"目录数", fmt.Sprint(int(num(result, "dirs")))}} {
			if pair[1] != "" && pair[1] != "0" {
				kv(c, pair[0], pair[1])
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
			fieldNote(c, "没有可显示的内容。")
			return
		}
		codeBlock(c, fmt.Sprintf("tool-body-%d", index), text, 150)
	}
	a.messageAttachments(c, t, m)
}

func (a *desktop) toolListBody(c *ui.Context, index int, result map[string]any) {
	entries := list(result, "entries")
	if len(entries) == 0 {
		fieldNote(c, resultText(result))
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
				iconGlyph(c, glyph, 12).TextColor(c.Theme().TextMuted)
				ui.Text(c, path).Font(monoFamily).FontSize(fsSmall)
				if size := int64(num(m, "size")); size > 0 && !boolOf(m, "dir") {
					ui.Text(c, fmtBytes(size)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
				}
			})
		}
	})
}

// diffHeadLimit mirrors main's DiffView: the collapsed card shows at most 20
// diff rows with 3 context lines around changes; the full diff opens on demand
// instead of a hard truncation.
const diffHeadLimit = 20

// diffBlock renders a unified diff with the two-tone row backgrounds the Vue
// diff view used. split=true keeps a compact single column (edits), the same
// reading order as the original card.
func (a *desktop) diffBlock(c *ui.Context, key, diff string, compact bool) {
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	added, removed := 0, 0
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added++
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			removed++
		}
	}
	full := a.popupFlag(key + "-full")
	shown := diffVisibleLines(lines, diffHeadLimit, *full)
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		if added > 0 {
			chip(c, fmt.Sprintf("+%d", added), colSuccess(), ui.RGBA(78, 200, 126, 0.12))
		}
		if removed > 0 {
			chip(c, fmt.Sprintf("-%d", removed), colDanger(), ui.RGBA(232, 84, 84, 0.12))
		}
	})
	box := ui.Column(c.Key(key)).FillWidth().Clip().Radius(radiusSmall).Background(c.Theme().Background)
	box.Children(func() {
		for _, dl := range shown {
			if dl.gap > 0 {
				ui.Box(c).FillWidth().Padding(2, 8).Children(func() {
					ui.Text(c, fmt.Sprintf("… %d 行未变更 …", dl.gap)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
				})
				continue
			}
			line := dl.text
			fill := ui.Transparent
			color := c.Theme().Text
			switch {
			case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"):
				color = c.Theme().TextMuted
			case strings.HasPrefix(line, "+"):
				fill = ui.RGBA(78, 200, 126, 0.10)
				color = colSuccess()
			case strings.HasPrefix(line, "-"):
				fill = ui.RGBA(232, 84, 84, 0.10)
				color = ui.Hex("#f0b5b5")
			}
			ui.Box(c).FillWidth().Padding(0, 8).Background(fill).Children(func() {
				ui.Text(c, line).Font(monoFamily).FontSize(fsTool - 2).TextColor(color).LineHeight(1.58).NoWrap()
			})
		}
	})
	if len(lines) > diffHeadLimit {
		label := fmt.Sprintf("… 显示完整 diff（%d 行） …", len(lines))
		if *full {
			label = "收起 diff"
		}
		flat(c.Key(key+"-toggle"), label).Cursor(ui.CursorPointer).AlignSelf(ui.Start).OnClick(func() { *full = !*full })
	}
}

// diffVisibleLine is one rendered diff row, or a collapsed gap of unchanged
// rows summarized by its count.
type diffVisibleLine struct {
	text string
	gap  int
}

// diffVisibleLines mirrors main's DiffView collapsing: when folded, changed
// rows show with 3 context lines around them and the gaps between windows
// collapse into "… N 行未变更 …" markers. File headers (---/+++) and hunk
// headers (@@) always show. Full view returns every row.
func diffVisibleLines(lines []string, limit int, full bool) []diffVisibleLine {
	if full || len(lines) <= limit {
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
		out := make([]diffVisibleLine, 0, limit)
		for i := 0; i < limit && i < len(lines); i++ {
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
	role := str(m, "role")
	if role == "" {
		role = "子代理"
	}
	tone := toneColor(c, status)
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
				anchor := ui.Box(c).Size(6, 6)
				pulse := anchor.Loop(fmt.Sprintf("sub-dot-%s-%d", str(m, "subId"), index), 1200*time.Millisecond, ui.Linear)
				tri := pulse * 2
				if tri > 1 {
					tri = 2 - tri
				}
				anchor.Radius(3).Background(c.Theme().Accent).Opacity(float32(0.3 + 0.7*easeInOut(tri)))
			} else {
				ui.Box(c).Size(6, 6).Radius(3).Background(tone)
			}
			ui.Text(c, role).FontSize(fsBody).Bold()
			ui.Spacer(c)
			ui.Text(c, label).FontSize(fsTiny).TextColor(tone)
			if ms := int64(num(m, "durationMs")); ms > 0 {
				ui.Text(c, fmtDur(ms)).Font(monoFamily).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
			}
		})
		if desc := str(m, "description"); desc != "" {
			ui.Text(c, desc).FontSize(fsSmall).TextColor(c.Theme().TextMuted).MaxLines(2).Tooltip(desc)
		}
		ui.Row(c).Gap(gapInline).Wrap().AlignItems(ui.Center).Children(func() {
			if steps := int(num(m, "steps")); steps > 0 {
				neutralChip(c, fmt.Sprintf("%d 步", steps))
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
			if summary != "" || errText != "" {
				open := t.uiState().ToolOpen["sub-"+str(m, "subId")]
				btn := flat(c, "报告 ›")
				if open {
					btn = flat(c, "报告 ⌄")
				}
				btn.OnClick(func() {
					t.uiState().ToolOpen["sub-"+str(m, "subId")] = !open
				})
			}
		})
		if t.uiState().ToolOpen["sub-"+str(m, "subId")] {
			if errText := str(m, "error"); errText != "" {
				notice(c, "error", errText)
			}
			if summary := str(m, "summary"); summary != "" {
				ui.Text(c, summary).FontSize(fsSmall).Selectable().LineHeight(1.6)
			}
		}
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
