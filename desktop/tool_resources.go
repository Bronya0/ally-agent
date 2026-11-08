// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"sort"
	"strconv"
	"strings"
	"time"
)

const terminalOutputSize float32 = fsTool - 1

// toolCodeWindow shows a tool's code or terminal output. path names the file the
// text came from (it picks syntax highlighting); it is empty for terminal output.
// ink is the caller's secondary-text tone.
func toolCodeWindow(c *ui.Context, key, text string, expanded bool, path string, ink ...ui.Color) {
	toolCodeWindowHeight(c, key, text, expanded, path, 240, false, ink...)
}

func toolCodeWindowHeight(c *ui.Context, key, text string, expanded bool, path string, height float32, maximum bool, ink ...ui.Color) {
	body := func() { toolCodeLines(c, text, path, ink...) }
	if !expanded {
		ui.Box(c.Key(key)).FillWidth().MaxHeight(150).Clip().Background(c.Theme().Background).Radius(radiusSmall).Children(body)
		return
	}
	scroll := ui.ScrollBoth(c.Key(key)).FillWidth().MinWidth(0).Shrink(0).Clip().Label("工具输出 " + key).Background(c.Theme().Background).Radius(radiusSmall)
	if maximum {
		scroll.MaxHeight(height)
	} else {
		scroll.Height(height)
	}
	scroll.Children(body)
}

// toolCodeLines draws the text itself; callers wrap it in the scroll area they want.
func toolCodeLines(c *ui.Context, text, path string, ink ...ui.Color) {
	base := uikit.SecondaryInk(c, ink)
	size := readingCodeSize
	if path == "" {
		base = terminalOutputInk(c.Theme(), ink...)
		size = terminalOutputSize
	}
	spans := codeSpansWithBase(c, text, path, base)
	ui.RichText(c, spans...).AlignSelf(ui.Start).TextAlign(ui.Start).Font(monoFamily).FontSize(size).Shrink(0).Selectable().Padding(2, 8, 4, 0).NoWrap()
}

type highlightCacheEntry struct {
	text, path string
	dark       bool
	accent     ui.Color
	base       ui.Color
	spans      []ui.Span
	bytes      int
}

// Admission bounds source bytes and a weighted span count, rather than just
// one source's size. The weight is an estimate, not actual heap accounting.
const (
	highlightCacheBudget = 256 << 10
	highlightSpanCost    = 48 // admission weight; ui.Span's actual size is larger
	highlightCacheSize   = 32
)

var (
	highlightCache      []highlightCacheEntry
	highlightCacheBytes int
)

// UI-thread-only bounded cache: repeated streaming frames do not retokenize
// unchanged previews. The oldest entries go first once the cache is full.
func codeSpans(c *ui.Context, text, path string, ink ...ui.Color) []ui.Span {
	return codeSpansWithBase(c, text, path, uikit.SecondaryInk(c, ink))
}
func codeSpansWithBase(c *ui.Context, text, path string, base ui.Color) []ui.Span {
	for _, entry := range highlightCache {
		if entry.text == text && entry.path == path && entry.dark == c.Theme().Dark && entry.accent == c.Theme().Accent && entry.base == base {
			return entry.spans
		}
	}
	// A tail or snippet can share the backing string of a whole file/log. Own
	// the bounded source before tokenizing: both the key and every span must
	// release that original source when its conversation is released.
	sourceBytes := len(text) + len(path)
	if sourceBytes <= highlightCacheBudget {
		text = strings.Clone(text)
		path = strings.Clone(path)
	}
	spans := buildCodeSpans(c, text, path, base)
	// A source that would take the whole budget is drawn but not kept, so one
	// huge file cannot push every other preview out on each frame.
	size := sourceBytes + len(spans)*highlightSpanCost
	if size > highlightCacheBudget {
		return spans
	}
	for len(highlightCache) > 0 && (len(highlightCache) >= highlightCacheSize || highlightCacheBytes+size > highlightCacheBudget) {
		highlightCacheBytes -= highlightCache[0].bytes
		copy(highlightCache, highlightCache[1:])
		highlightCache[len(highlightCache)-1] = highlightCacheEntry{}
		highlightCache = highlightCache[:len(highlightCache)-1]
	}
	highlightCache = append(highlightCache, highlightCacheEntry{text: text, path: path, dark: c.Theme().Dark, accent: c.Theme().Accent, base: base, spans: spans, bytes: size})
	highlightCacheBytes += size
	return spans
}
func buildCodeSpans(c *ui.Context, text, path string, base ui.Color) []ui.Span {
	language := editor.LanguageFor(path)
	if language == nil || language.Tokenizer == nil {
		return []ui.Span{{Text: text, Color: base}}
	}
	var spans []ui.Span
	var state uint64
	var tokens []editor.Token
	first := true
	for line := range strings.SplitSeq(text, "\n") {
		if !first {
			spans = append(spans, ui.Span{Text: "\n"})
		}
		first = false
		tokens, state = language.Tokenizer.Tokenize(line, state, tokens[:0])
		lineFirst, runStart := len(spans), 0
		start := 0
		for _, token := range tokens {
			end := token.End
			if end <= start || end > len(line) {
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
				color = base
			case editor.TokenInvalid, editor.TokenDeleted:
				color = c.Theme().Danger
			}
			// Adjacent tokens with the same displayed style can share a span.
			// Extend their source slice without joining or copying their text.
			if len(spans) > lineFirst && spans[len(spans)-1].Color == color {
				spans[len(spans)-1].Text = line[runStart:end]
			} else {
				runStart = start
				spans = append(spans, ui.Span{Text: line[start:end], Color: color})
			}
			start = end
		}
		if start < len(line) {
			if len(spans) > lineFirst && spans[len(spans)-1].Color == base {
				spans[len(spans)-1].Text = line[runStart:]
			} else {
				spans = append(spans, ui.Span{Text: line[start:], Color: base})
			}
		}
	}
	return spans
}

var toolFieldLabels = map[string]string{"action": "操作", "localPath": "本地路径", "remotePath": "远端路径", "files": "文件", "dirs": "目录", "bytes": "大小", "command": "命令", "cwd": "工作目录", "pid": "PID", "schedule": "调度", "nextRunAt": "下次执行", "prompt": "任务", "host": "主机", "port": "端口", "user": "用户", "description": "说明", "id": "ID", "instruction": "任务内容", "username": "用户", "fileCount": "文件", "dirCount": "目录", "totalBytes": "大小", "target": "目标", "riskLevel": "风险级别", "timeoutSeconds": "超时秒数", "outputBytes": "输出大小", "exitCode": "退出码", "model": "模型", "width": "宽度", "height": "高度", "error": "失败原因"}

func toolFields(c *ui.Context, data map[string]any, keys []string, ink ...ui.Color) {
	for _, k := range keys {
		valueText := resourceFieldValue(k, data)
		if valueText == "" {
			continue
		}
		label := toolFieldLabels[k]
		if label == "" {
			label = k
		}
		ui.Row(c).FillWidth().MinWidth(0).Gap(gapField).AlignItems(ui.Start).Children(func() {
			ui.Text(c, label+":").Width(76).Shrink(0).FontSize(fsTiny).TextColor(uikit.SecondaryInk(c, ink))
			ui.Text(c, valueText).FontSize(fsSmall).Grow(1).MinWidth(0).Selectable()
		})
	}
}

func (a *desktop) toolResourceCards(c *ui.Context, key, kind string, result map[string]any, open bool) {
	collection := map[string]string{"service": "services", "scheduled_task": "tasks", "ssh_cluster": "servers"}[kind]
	items, isList := result[collection].([]any)
	if kind == "service" {
		if max := int(num(result, "maxActive")); max > 0 {
			fieldNote(c, fmt.Sprintf("运行中 %d / %d", int(num(result, "activeCount")), max), a.secondaryInk())
		}
		if output, ok := result["output"].(string); ok {
			if !open {
				output = tailLines(output, 6)
			}
			toolCodeWindow(c, key+"-output", output, open, "", a.secondaryInk())
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
				toolCodeWindow(c, key+"-output", tailLines(text, 6), false, "", a.secondaryInk())
			}
			return
		}
		items = []any{item}
	}
	if len(items) == 0 {
		fieldNote(c, "暂无记录", a.secondaryInk())
		return
	}
	// A stopped service keeps its full card only when the tool card is open:
	// a list that asked for finished services would otherwise fill the chat
	// with one card per dead process.
	type resourceEntry struct {
		index int
		item  any
	}
	shown := make([]resourceEntry, 0, len(items))
	hidden := 0
	for n, raw := range items {
		if kind == "service" && !open {
			item, ok := raw.(map[string]any)
			if ok && !serviceIsActive(str(item, "status")) {
				hidden++
				continue
			}
		}
		shown = append(shown, resourceEntry{index: n, item: raw})
	}
	ui.Column(c).FillWidth().MinWidth(0).Gap(gapField).Children(func() {
		for _, entry := range shown {
			n, raw := entry.index, entry.item
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			ui.Column(c.Key(fmt.Sprintf("%s-resource-%d", key, n))).FillWidth().MinWidth(0).Padding(8, 10).Gap(gapTight).Border(1, c.Theme().Border).Radius(radiusCard).Background(c.Theme().Surface).Children(func() {
				title := str(item, "name")
				if title == "" {
					title = str(item, "alias")
				}
				if title == "" {
					title = str(item, "id")
				}
				ui.Row(c).FillWidth().MinWidth(0).Gap(gapInline).AlignItems(ui.Center).Children(func() {
					ui.Text(c, title).FontSize(fsSmall).Bold().Grow(1).MinWidth(0).SingleLine()
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
						chip(c, resourceStatus(status), toneColor(c, status, a.secondaryInk()), c.Theme().Background)
					}
				})
				resourceMetaChips(c, item, title)
				resourceTextLines(c, item, a.secondaryInk())
				resourceDetailLine(c, item, a.secondaryInk())
			})
		}
		if hidden > 0 {
			fieldNote(c, fmt.Sprintf("另有 %d 个已停止的服务，展开卡片查看", hidden), a.secondaryInk())
		}
	})
}

// serviceIsActive reports a service the card shows without expanding it.
func serviceIsActive(status string) bool {
	return status == "running" || status == "starting"
}

// 资源卡把工具的字段分三类画，而不是一个字段一行：短值排成一行标签，长文本压成
// 一行省略，时间与毫秒数这类零碎值并为一行。三个字段表在包加载时切一次，
// 不再每帧对同一句话做 Fields。
var (
	resourceMetaFields   = strings.Fields("host port username riskLevel")
	resourceTextFields   = strings.Fields("description command cwd instruction error")
	resourceDetailFields = strings.Fields("schedule nextRunAt pid timeoutSeconds outputBytes")
)

// riskLevelText 把后端的 low/high 译成中文；原始值仍然参与颜色判断，
// 所以没有落进这张表的值会原样显示，不会变成空白。
var riskLevelText = map[string]string{"low": "低", "high": "高", "medium": "中"}

// resourceFieldValue 把字段渲染成一行文本。schedule、nextRunAt 和字节数在这里
// 统一转换，三类卡片和 toolFields 因此不会各写一份格式。先判字段是否存在，
// 所以 nextRunAt 为 0 能显示成“—”，而缺失的字段什么都不画。
func resourceFieldValue(k string, item map[string]any) string {
	raw, exists := item[k]
	if !exists || raw == nil {
		return ""
	}
	switch k {
	case "schedule":
		encoded, _ := json.Marshal(raw)
		var schedule backend.ScheduledTaskSchedule
		if json.Unmarshal(encoded, &schedule) == nil {
			return scheduleText(schedule)
		}
	case "nextRunAt":
		if ms := int64(num(item, k)); ms > 0 {
			return time.UnixMilli(ms).Format("01-02 15:04:05")
		}
		return "—"
	case "totalBytes", "bytes", "outputBytes":
		if size := int64(num(item, k)); size > 0 {
			return fmtBytes(size)
		}
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

// resourceMetaChips 把短字段排成一行标签：端口、用户、风险级别以前各占一行。
// 和标题重复的值直接跳过，ssh 节点的 host 通常就等于别名，没必要再写一遍。
func resourceMetaChips(c *ui.Context, item map[string]any, title string) {
	ui.Row(c).FillWidth().MinWidth(0).Wrap().Gap(gapTight).AlignItems(ui.Center).Children(func() {
		for _, k := range resourceMetaFields {
			value := resourceFieldValue(k, item)
			if value == "" || value == title {
				continue
			}
			label := toolFieldLabels[k]
			if label == "" {
				label = k
			}
			if k == "riskLevel" {
				if zh := riskLevelText[strings.ToLower(value)]; zh != "" {
					value = zh
				}
				// 风险是这一行里唯一需要靠颜色区分的值，low 绿、high 红。
				pill(c, label+" "+value, pillToneOf(str(item, k)))
				continue
			}
			neutralChip(c, label+" "+value)
		}
	})
}

// resourceTextLines 给长文本一行位置并带省略号，全文放进悬停提示：服务器的说明、
// 服务的启动命令以前会把卡片撑开好几行。
func resourceTextLines(c *ui.Context, item map[string]any, ink ...ui.Color) {
	for _, k := range resourceTextFields {
		value := resourceFieldValue(k, item)
		if value == "" {
			continue
		}
		label := toolFieldLabels[k]
		if label == "" {
			label = k
		}
		color := uikit.SecondaryInk(c, ink)
		if k == "error" {
			color = c.Theme().Danger
		}
		ui.Row(c).FillWidth().MinWidth(0).Gap(gapField).AlignItems(ui.Center).Children(func() {
			// 标签列固定宽，并且不许折行：工作目录、任务内容这类四个字的标签会
			// 折成两行，把一整张紧凑卡片又拉高一行。
			ui.Text(c, label+":").Width(64).Shrink(0).FontSize(fsTiny).TextColor(color).NoWrap()
			uikit.Tooltip(c, ui.Text(c, value).FontSize(fsSmall).TextColor(color).Grow(1).MinWidth(0).SingleLine(), value)
		})
	}
}

// resourceDetailLine 把时间、PID、大小这类零碎值并成一行，用 · 分隔，
// 不再一个字段占一整行。
func resourceDetailLine(c *ui.Context, item map[string]any, ink ...ui.Color) {
	parts := make([]string, 0, len(resourceDetailFields))
	for _, k := range resourceDetailFields {
		value := resourceFieldValue(k, item)
		if value == "" {
			continue
		}
		label := toolFieldLabels[k]
		if label == "" {
			label = k
		}
		parts = append(parts, label+" "+value)
	}
	if len(parts) == 0 {
		return
	}
	ui.Text(c, strings.Join(parts, " · ")).FontSize(fsTiny).TextColor(uikit.SecondaryInk(c, ink)).SingleLine()
}

type splitDiffRow struct {
	left, right             string
	removed, added          bool
	leftNumber, rightNumber int
	leftOrder, rightOrder   int
	leftPresent             bool
	rightPresent            bool
	gap                     int
}

type diffDisplayLine struct {
	text    string
	number  int
	order   int
	changed bool
	gap     int
}

// isHunkHeader reports a unified-diff hunk line ("@@ -90,7 +90,7 @@"), which
// the card drops: every row already carries its own number, so the header only
// repeats bookkeeping the reader cannot use.
func isHunkHeader(text string) bool {
	return strings.HasPrefix(text, "@@ -") && strings.Contains(text, " +")
}

func splitDiffRows(lines []diffVisibleLine) []splitDiffRow {
	// Tabs are expanded once while deriving the memoized display rows, rather
	// than in every code line on every frame.
	for n := range lines {
		lines[n].text = strings.ReplaceAll(lines[n].text, "\t", "    ")
	}
	var rows []splitDiffRow
	for i := 0; i < len(lines); {
		l := lines[i]
		if l.gap > 0 {
			text := fmt.Sprintf("… %d 行未变更 …", l.gap)
			rows = append(rows, splitDiffRow{left: text, right: text, leftOrder: i, rightOrder: i, leftPresent: true, rightPresent: true, gap: l.gap})
			i++
			continue
		}
		if strings.HasPrefix(l.text, "-") && !strings.HasPrefix(l.text, "---") {
			type orderedLine struct {
				text  string
				order int
			}
			var removed, added []orderedLine
			for i < len(lines) && lines[i].gap == 0 && strings.HasPrefix(lines[i].text, "-") && !strings.HasPrefix(lines[i].text, "---") {
				removed = append(removed, orderedLine{text: lines[i].text, order: i})
				i++
			}
			for i < len(lines) && lines[i].gap == 0 && strings.HasPrefix(lines[i].text, "+") && !strings.HasPrefix(lines[i].text, "+++") {
				added = append(added, orderedLine{text: lines[i].text, order: i})
				i++
			}
			count := max(len(removed), len(added))
			for n := 0; n < count; n++ {
				row := splitDiffRow{}
				if n < len(removed) {
					row.left = removed[n].text
					row.leftOrder = removed[n].order
					row.leftPresent = true
					row.removed = true
				}
				if n < len(added) {
					row.right = added[n].text
					row.rightOrder = added[n].order
					row.rightPresent = true
					row.added = true
				}
				rows = append(rows, row)
			}
			continue
		}
		if strings.HasPrefix(l.text, "+") && !strings.HasPrefix(l.text, "+++") {
			rows = append(rows, splitDiffRow{right: l.text, rightOrder: i, rightPresent: true, added: true})
		} else {
			rows = append(rows, splitDiffRow{left: l.text, right: l.text, leftOrder: i, rightOrder: i, leftPresent: true, rightPresent: true})
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
		if isHunkHeader(row.left) {
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
		if row.leftPresent && oldLine > 0 {
			row.leftNumber = oldLine
			oldLine++
		}
		if row.rightPresent && newLine > 0 {
			row.rightNumber = newLine
			newLine++
		}
	}
	return rows
}

func singleColumnDiffRows(rows []splitDiffRow) []diffDisplayLine {
	visible := make([]diffDisplayLine, 0, len(rows)*2)
	for _, row := range rows {
		if row.gap > 0 {
			visible = append(visible, diffDisplayLine{text: row.left, order: row.leftOrder, gap: row.gap})
			continue
		}
		// The hunk header is dropped here rather than while splitting, because
		// splitting still needs its line numbers to label the rows below it.
		if isHunkHeader(row.left) {
			continue
		}
		if row.removed || row.added {
			if row.removed && row.leftPresent {
				visible = append(visible, diffDisplayLine{text: row.left, number: row.leftNumber, order: row.leftOrder, changed: true})
			}
			if row.added && row.rightPresent {
				visible = append(visible, diffDisplayLine{text: row.right, number: row.rightNumber, order: row.rightOrder, changed: true})
			}
			continue
		}
		if row.leftPresent {
			visible = append(visible, diffDisplayLine{text: row.left, number: row.leftNumber, order: row.leftOrder})
		} else if row.rightPresent {
			visible = append(visible, diffDisplayLine{text: row.right, number: row.rightNumber, order: row.rightOrder})
		}
	}
	sort.SliceStable(visible, func(i, j int) bool { return visible[i].order < visible[j].order })
	return visible
}

// diffColors matches the legacy diff palette, independently of status colors.
func diffColors(t *ui.Theme, changed, removed bool) (ui.Color, ui.Color) {
	if !changed {
		if t.Dark {
			return ui.Transparent, ui.Hex("#d4d4d4")
		}
		return ui.Transparent, ui.Hex("#3a3f45")
	}
	if t.Dark {
		if removed {
			return ui.RGBA(232, 84, 84, 0.10), ui.Hex("#f0b5b5")
		}
		return ui.RGBA(78, 200, 126, 0.10), ui.Hex("#8fd4b4")
	}
	if removed {
		return ui.RGBA(220, 38, 38, 0.09), ui.Hex("#a33a3a")
	}
	return ui.RGBA(22, 163, 74, 0.10), ui.Hex("#14683c")
}

func diffLine(c *ui.Context, text string, changed bool, number int, ink ...ui.Color) {
	fill, color := diffColors(c.Theme(), changed, strings.HasPrefix(text, "-"))
	ui.Row(c).FillWidth().MinWidth(0).Shrink(0).MinHeight(22).Padding(1, 8).AlignItems(ui.Start).Background(fill).Children(func() {
		if text == "" {
			text = " "
		}
		if number > 0 {
			ui.Text(c, strconv.Itoa(number)).Width(42).Shrink(0).Font(monoFamily).FontSize(fsTiny).TextColor(uikit.SecondaryInk(c, ink)).Unselectable()
		}
		ui.Text(c, text).Font(monoFamily).FontSize(readingCodeSize).TextColor(color).Grow(1).MinWidth(0).Selectable()
	})
}

func toolSourceWindow(c *ui.Context, key, text, path string, start int, ink ...ui.Color) {
	body := func() {
		row := ui.Row(c).AlignItems(ui.Start).Padding(2, 8, 4, 0).Gap(gapSection)
		gutter := memoAt[lineMemo](row, "line-numbers")
		row.Children(func() {
			ui.Text(c, gutter.get(lineKey{text, start}, lineNumberColumn)).Font(monoFamily).FontSize(readingCodeSize).TextColor(uikit.SecondaryInk(c, ink)).FixedLineHeight(20).Unselectable()
			ui.RichText(c, codeSpans(c, text, path, ink...)...).Font(monoFamily).FontSize(readingCodeSize).FixedLineHeight(20).Selectable().NoWrap()
		})
	}
	ui.Box(c.Key(key)).FillWidth().MaxHeight(150).Clip().Children(body)
}

// lineNumberColumn builds the number column of a source window: every line of
// the text, counted up from the first number the window shows.
func lineNumberColumn(key lineKey) string {
	count := strings.Count(strings.TrimSuffix(key.text, "\n"), "\n") + 1
	var b strings.Builder
	b.Grow(count * 4)
	for n := 0; n < count; n++ {
		if n > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(key.start + n))
	}
	return b.String()
}

// Terminal heading and transcript have different roles, especially on light
// surfaces. Secondary ink already supplies the readable transcript tone;
// fading it again would wash out the website's muted ink in the light preset.
// secondary 是调用方的次要文字色：Theme.TextMuted 现在只作占位淡色。
func commandLineInk(t *ui.Theme, secondary ...ui.Color) ui.Color {
	if t.Dark {
		return uikit.SecondaryInkOf(t, secondary...)
	}
	return t.Text.Mix(uikit.SecondaryInkOf(t, secondary...), 0.30)
}
func terminalOutputInk(t *ui.Theme, secondary ...ui.Color) ui.Color {
	return uikit.SecondaryInkOf(t, secondary...)
}
