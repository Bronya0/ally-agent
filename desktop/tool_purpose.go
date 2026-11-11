// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"strings"

	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"github.com/egoist/mygo/ui"
)

var groupToolIcons = [4]string{"book", "search", "list", "terminal"}
var groupPurposeFallbacks = [4]string{"读取文件", "搜索内容", "列出目录", "执行命令"}

type groupPurposeKey struct {
	args, purpose, field, name, state string
	streaming                         bool
}
type groupPurposeLabel struct{ full, short string }

func toolGroupCategory(name string) int {
	switch name {
	case "grep":
		return 1
	case "list", "glob":
		return 2
	case "command":
		return 3
	default:
		return 0
	}
}

func toolPurpose(m, args map[string]any) string {
	if purpose := str(m, "purpose"); purpose != "" {
		return purpose
	}
	field := str(m, "purposeField")
	if field == "" {
		// Old MCP business parameters must not become presentation metadata.
		if strings.HasPrefix(str(m, "name"), "mcp") || str(m, "mcpServer") != "" {
			return ""
		}
		field = "purpose"
	}
	return strings.TrimSpace(str(args, field))
}

func (a *desktop) toolPurposeText(c *ui.Context, key, purpose string) {
	// SingleLine clips to available width; the complete text stays on hover.
	text := ui.Text(c.Key(key), short(purpose, 72)).FontSize(fsTool).TextColor(a.secondaryInk()).SingleLine().MinWidth(0).Grow(1).Shrink(1)
	uikit.Tooltip(c, text, purpose)
}

func (a *desktop) toolPurposeDisclosure(c *ui.Context, key, prefix, name, purpose, state string, open bool) ui.Element {
	button := ui.ButtonBase(c.Key(key+"-row")).FillWidth().MinWidth(0).Height(24).Padding(1, 8, 1, 16).Gap(gapInline).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).Label(locales.Text("工具目的 ") + key)
	button.Children(func() {
		ui.Text(c, prefix).Font(monoFamily).FontSize(fsSmall).TextColor(a.secondaryInk()).Shrink(0)
		disclosureIcon(c, groupToolIcons[toolGroupCategory(name)], a.secondaryInk())
		a.toolPurposeText(c, key+"-text", purpose)
		if state == "running" || state == "pending" || state == "error" || state == "cancelled" {
			toolStatusIcon(c, state)
		}
		caret := "chevron"
		if open {
			caret = "chevrondown"
		}
		iconGlyph(c, caret, sizeCaretRow).Shrink(0).TextColor(a.secondaryInk())
	})
	return button
}

func (a *desktop) toolGroupReadEntry(c *ui.Context, t *conversation, entry toolGroupEntry, prefix string) {
	key := toolCardKey(t, entry.index, entry.message)
	open := t.uiState().ToolOpen[key]
	title := entry.purpose
	if title == "" {
		title = entry.title
		if entry.summary != "" {
			title += " (" + entry.summary + ")"
		}
	}
	if title == "" {
		title = entry.verb
	}
	a.toolPurposeDisclosure(c, key, prefix, entry.name, title, entry.state, open).OnClick(func() { t.uiState().ToolOpen[key] = !open })
	if !open {
		return
	}
	ui.Column(c.Key(key+"-read-details")).FillWidth().MinWidth(0).Padding(0, 8, 0, 38).Gap(2).Children(func() {
		if boolOf(entry.message, "fastCompacted") {
			toolGroupCompactedChip(c, entry.message)
		}
		result := toolResult(entry.message)
		files := list(result, "files")
		missing := list(result, "missing")
		if len(files) == 0 && len(missing) == 0 {
			files = list(entry.args, "files")
		}
		for i, raw := range files {
			file, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			text := str(file, "path")
			if summary := toolGroupReadSummary(file); summary != "" {
				text += " (" + summary + ")"
			}
			if err := str(file, "error"); err != "" {
				text += " · " + err
			}
			uikit.Tooltip(c, ui.Text(c.Key(fmt.Sprintf("%s-file-%d", key, i)), text).FontSize(fsTool).TextColor(a.secondaryInk()).FillWidth().MinWidth(0).SingleLine(), text)
		}
		for i, raw := range missing {
			file, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			text := str(file, "path") + " · " + str(file, "error")
			uikit.ErrorText(c.Key(fmt.Sprintf("%s-missing-%d", key, i)), text).FontSize(fsTool).FillWidth().MinWidth(0)
		}
		if len(files) == 0 && entry.title != "" {
			a.toolPurposeText(c, key+"-target", entry.title)
		}
		if entry.detail != "" {
			uikit.ErrorText(c, entry.detail).FontSize(fsTool).FillWidth().MinWidth(0)
		}
	})
}
