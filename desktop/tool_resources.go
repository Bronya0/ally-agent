// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"strconv"
	"strings"
	"time"
)

const terminalOutputSize float32 = fsTool - 1

func toolCodeWindow(c *ui.Context, key, text string, expanded bool, filename ...string) {
	path := ""
	if len(filename) > 0 {
		path = filename[0]
	}
	body := func() {
		base := c.Theme().TextMuted
		size := readingCodeSize
		if path == "" {
			base = terminalOutputInk(c.Theme())
			size = terminalOutputSize
		}
		spans := codeSpansWithBase(c, text, path, base)
		ui.RichText(c, spans...).AlignSelf(ui.Start).TextAlign(ui.Start).Font(monoFamily).FontSize(size).Shrink(0).Selectable().Padding(2, 8, 4, 0).NoWrap()
	}
	if !expanded {
		ui.Box(c.Key(key)).FillWidth().MaxHeight(150).Clip().Background(c.Theme().Background).Radius(radiusSmall).Children(body)
		return
	}
	ui.ScrollBoth(c.Key(key)).FillWidth().MinWidth(0).Height(240).Shrink(0).Clip().Label("工具输出 " + key).Background(c.Theme().Background).Radius(radiusSmall).Children(body)
}

type highlightCacheEntry struct {
	text, path string
	dark       bool
	accent     ui.Color
	base       ui.Color
	spans      []ui.Span
}

var highlightCache []highlightCacheEntry

// UI-thread-only bounded cache: repeated streaming frames do not retokenize
// unchanged previews. Oversized sources are not retained in the cache.
func codeSpans(c *ui.Context, text, path string) []ui.Span {
	return codeSpansWithBase(c, text, path, c.Theme().TextMuted)
}
func codeSpansWithBase(c *ui.Context, text, path string, base ui.Color) []ui.Span {
	for _, entry := range highlightCache {
		if entry.text == text && entry.path == path && entry.dark == c.Theme().Dark && entry.accent == c.Theme().Accent && entry.base == base {
			return entry.spans
		}
	}
	spans := buildCodeSpans(c, text, path, base)
	if len(text) <= 32768 {
		if len(highlightCache) >= 32 {
			copy(highlightCache, highlightCache[1:])
			highlightCache = highlightCache[:31]
		}
		highlightCache = append(highlightCache, highlightCacheEntry{text: text, path: path, dark: c.Theme().Dark, accent: c.Theme().Accent, base: base, spans: spans})
	}
	return spans
}
func buildCodeSpans(c *ui.Context, text, path string, base ui.Color) []ui.Span {
	language := editor.LanguageFor(path)
	if language == nil || language.Tokenizer == nil {
		return []ui.Span{{Text: text, Color: base}}
	}
	var spans []ui.Span
	var state uint64
	for n, line := range strings.Split(text, "\n") {
		if n > 0 {
			spans = append(spans, ui.Span{Text: "\n"})
		}
		tokens, next := language.Tokenizer.Tokenize(line, state, nil)
		state = next
		start := 0
		for _, token := range tokens {
			end := token.End
			if end < start || end > len(line) {
				continue
			}
			color := base
			switch token.Kind {
			case editor.TokenKeyword, editor.TokenType, editor.TokenFunction:
				color = c.Theme().Accent
			case editor.TokenString, editor.TokenInserted:
				color = c.Theme().Success
			case editor.TokenNumber, editor.TokenConstant:
				color = c.Theme().Warning
			case editor.TokenComment:
				color = c.Theme().TextMuted
			case editor.TokenInvalid, editor.TokenDeleted:
				color = c.Theme().Danger
			}
			spans = append(spans, ui.Span{Text: line[start:end], Color: color})
			start = end
		}
		if start < len(line) {
			spans = append(spans, ui.Span{Text: line[start:], Color: base})
		}
	}
	return spans
}

func toolFields(c *ui.Context, data map[string]any, keys []string) {
	labels := map[string]string{"action": "操作", "localPath": "本地路径", "remotePath": "远端路径", "files": "文件", "dirs": "目录", "bytes": "大小", "command": "命令", "cwd": "工作目录", "pid": "PID", "schedule": "调度", "nextRunAt": "下次执行", "prompt": "任务", "host": "主机", "port": "端口", "user": "用户", "description": "说明", "id": "ID", "instruction": "任务内容", "username": "用户", "fileCount": "文件", "dirCount": "目录", "totalBytes": "大小", "target": "目标", "riskLevel": "风险级别", "timeoutSeconds": "超时秒数", "outputBytes": "输出大小", "exitCode": "退出码", "model": "模型", "width": "宽度", "height": "高度"}
	for _, k := range keys {
		v, ok := data[k]
		if !ok || v == nil || fmt.Sprint(v) == "" {
			continue
		}
		label := labels[k]
		if label == "" {
			label = k
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, label+":").Width(76).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
			text := fmt.Sprint(v)
			if k == "schedule" {
				raw, _ := json.Marshal(v)
				var schedule backend.ScheduledTaskSchedule
				if json.Unmarshal(raw, &schedule) == nil {
					text = scheduleText(schedule)
				}
			}
			if k == "nextRunAt" {
				if ms := int64(num(data, k)); ms > 0 {
					text = time.UnixMilli(ms).Format("01-02 15:04:05")
				} else {
					text = "—"
				}
			}
			if k == "totalBytes" || k == "bytes" || k == "outputBytes" {
				text = fmtBytes(int64(num(data, k)))
			}
			ui.Text(c, text).FontSize(fsSmall).Selectable()
		})
	}
}

func (a *desktop) toolResourceCards(c *ui.Context, key, kind string, result map[string]any, open bool) {
	collection := map[string]string{"service": "services", "scheduled_task": "tasks", "ssh_cluster": "servers"}[kind]
	items, isList := result[collection].([]any)
	if kind == "service" {
		if max := int(num(result, "maxActive")); max > 0 {
			fieldNote(c, fmt.Sprintf("运行中 %d / %d", int(num(result, "activeCount")), max))
		}
		if output, ok := result["output"].(string); ok {
			if !open {
				output = tailLines(output, 6)
			}
			toolCodeWindow(c, key+"-output", output, open)
			return
		}
	}
	if !isList {
		item := result
		if task, ok := result["task"].(map[string]any); ok {
			item = task
		}
		if str(item, "id") == "" && str(item, "name") == "" && str(item, "host") == "" && str(item, "alias") == "" {
			if text := commandOutput(result); text != "" {
				toolCodeWindow(c, key+"-output", tailLines(text, 6), false)
			}
			return
		}
		items = []any{item}
	}
	if len(items) == 0 {
		fieldNote(c, "暂无记录")
		return
	}
	ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
		for n, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			ui.Column(c.Key(fmt.Sprintf("%s-resource-%d", key, n))).MinWidth(240).Grow(1).Padding(10, 12).Gap(6).Border(1, c.Theme().Border).Radius(radiusCard).Background(c.Theme().Surface).Children(func() {
				title := str(item, "name")
				if title == "" {
					title = str(item, "alias")
				}
				if title == "" {
					title = str(item, "id")
				}
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, title).FontSize(fsSmall).Bold()
					ui.Spacer(c)
					status := str(item, "status")
					if kind == "scheduled_task" {
						status = str(item, "lastStatus")
						if boolOf(item, "running") {
							status = "running"
						}
						if status == "" {
							status = "idle"
						}
					}
					if status != "" {
						chip(c, resourceStatus(status), toneColor(c, status), c.Theme().Background)
					}
				})
				toolFields(c, item, strings.Fields("description command cwd host port username riskLevel schedule nextRunAt instruction pid timeoutSeconds outputBytes error id"))
			})
		}
	})
}

type splitDiffRow struct {
	left, right             string
	removed, added          bool
	leftNumber, rightNumber int
	gap                     int
}

func splitDiffRows(lines []diffVisibleLine) []splitDiffRow {
	var rows []splitDiffRow
	for i := 0; i < len(lines); {
		l := lines[i]
		if l.gap > 0 {
			text := fmt.Sprintf("… %d 行未变更 …", l.gap)
			rows = append(rows, splitDiffRow{left: text, right: text, gap: l.gap})
			i++
			continue
		}
		if strings.HasPrefix(l.text, "-") && !strings.HasPrefix(l.text, "---") {
			var removed, added []string
			for i < len(lines) && lines[i].gap == 0 && strings.HasPrefix(lines[i].text, "-") && !strings.HasPrefix(lines[i].text, "---") {
				removed = append(removed, lines[i].text)
				i++
			}
			for i < len(lines) && lines[i].gap == 0 && strings.HasPrefix(lines[i].text, "+") && !strings.HasPrefix(lines[i].text, "+++") {
				added = append(added, lines[i].text)
				i++
			}
			count := len(removed)
			if len(added) > count {
				count = len(added)
			}
			for n := 0; n < count; n++ {
				row := splitDiffRow{}
				if n < len(removed) {
					row.left = removed[n]
					row.removed = true
				}
				if n < len(added) {
					row.right = added[n]
					row.added = true
				}
				rows = append(rows, row)
			}
			continue
		}
		if strings.HasPrefix(l.text, "+") && !strings.HasPrefix(l.text, "+++") {
			rows = append(rows, splitDiffRow{right: l.text, added: true})
		} else {
			rows = append(rows, splitDiffRow{left: l.text, right: l.text})
		}
		i++
	}
	oldLine, newLine := 0, 0
	for n := range rows {
		row := &rows[n]
		if row.gap > 0 {
			oldLine += row.gap
			newLine += row.gap
			continue
		}
		if strings.HasPrefix(row.left, "@@") {
			fields := strings.Fields(row.left)
			if len(fields) >= 3 {
				oldLine, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(fields[1], "-"), ",")[0])
				newLine, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(fields[2], "+"), ",")[0])
			}
			continue
		}
		if strings.HasPrefix(row.left, "---") || strings.HasPrefix(row.right, "+++") {
			continue
		}
		if row.left != "" && oldLine > 0 {
			row.leftNumber = oldLine
			oldLine++
		}
		if row.right != "" && newLine > 0 {
			row.rightNumber = newLine
			newLine++
		}
	}
	return rows
}

func diffCell(c *ui.Context, text string, changed bool, number int) {
	fill := ui.Transparent
	color := c.Theme().TextMuted
	if changed {
		if strings.HasPrefix(text, "-") {
			fill = ui.RGBA(232, 84, 84, 0.10)
			color = c.Theme().Danger
		} else {
			fill = ui.RGBA(78, 200, 126, 0.10)
			color = c.Theme().Success
		}
	}
	// Each cell takes half the row and lets its code wrap: the row then grows to
	// the taller side, so wrapped lines keep the two columns lined up.
	ui.Row(c).Grow(1).Basis(0).MinWidth(0).Shrink(1).MinHeight(22).Padding(1, 8).AlignItems(ui.Start).Background(fill).Children(func() {
		if text == "" {
			text = " "
		}
		if number > 0 {
			ui.Text(c, strconv.Itoa(number)).Width(28).Shrink(0).Font(monoFamily).FontSize(fsTiny).TextColor(c.Theme().TextMuted).Unselectable()
		}
		ui.Text(c, text).Font(monoFamily).FontSize(readingCodeSize).TextColor(color).Grow(1).MinWidth(0).Selectable()
	})
}

func toolSourceWindow(c *ui.Context, key, text, path string, expanded bool, start int) {
	height := float32(150)
	if expanded {
		height = 300
	}
	body := func() {
		var numbers []string
		for n := 0; n < len(strings.Split(strings.TrimSuffix(text, "\n"), "\n")); n++ {
			numbers = append(numbers, strconv.Itoa(start+n))
		}
		ui.Row(c).AlignItems(ui.Start).Padding(2, 8, 4, 0).Gap(12).Children(func() {
			ui.Text(c, strings.Join(numbers, "\n")).Font(monoFamily).FontSize(readingCodeSize).TextColor(c.Theme().TextMuted).FixedLineHeight(20).Unselectable()
			ui.RichText(c, codeSpans(c, text, path)...).Font(monoFamily).FontSize(readingCodeSize).FixedLineHeight(20).Selectable().NoWrap()
		})
	}
	if expanded {
		ui.ScrollBoth(c.Key(key)).FillWidth().Height(height).Shrink(0).Clip().Children(body)
	} else {
		ui.Box(c.Key(key)).FillWidth().MaxHeight(height).Clip().Children(body)
	}
}

func diffCellNoWrap(c *ui.Context, text string, changed bool, number int) {
	fill, color := ui.Transparent, c.Theme().TextMuted
	if changed {
		if strings.HasPrefix(text, "-") {
			fill = ui.RGBA(232, 84, 84, 0.10)
			color = c.Theme().Danger
		} else {
			fill = ui.RGBA(78, 200, 126, 0.10)
			color = c.Theme().Success
		}
	}
	ui.Row(c).FillWidth().Height(22).Shrink(0).Padding(1, 8).AlignItems(ui.Center).Background(fill).Children(func() {
		label := ""
		if number > 0 {
			label = strconv.Itoa(number)
		}
		ui.Text(c, label).Width(34).Shrink(0).Font(monoFamily).FontSize(fsTiny).TextColor(c.Theme().TextMuted).Unselectable()
		if text == "" {
			text = " "
		}
		ui.Text(c, text).Font(monoFamily).FontSize(readingCodeSize).TextColor(color).NoWrap().Shrink(0).Selectable()
	})
}

// Terminal heading and transcript have different roles, especially on light
// surfaces. Keep the dark palette intact; avoid fading light output too far.
func commandLineInk(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.TextMuted
	}
	return t.Text.Mix(t.TextMuted, 0.30)
}
func terminalOutputInk(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.TextMuted
	}
	return t.TextMuted.Mix(t.Background, 0.12)
}
