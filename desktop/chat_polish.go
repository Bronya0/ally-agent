// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

func toolWarning(c *ui.Context, text string, ink ...ui.Color) {
	if strings.TrimSpace(text) == "" {
		return
	}
	full := strings.TrimSpace(text)
	line := strings.Join(strings.Fields(full), " ")
	ui.Row(c).FillWidth().MinWidth(0).Padding(5, 10).Radius(radiusSmall).
		Background(ui.RGBA(217, 164, 65, 0.06)).Children(func() {
		uikit.Tooltip(c, ui.Text(c, line).Grow(1).MinWidth(0).SingleLine().FontSize(fsSmall).TextColor(c.Theme().Warning.Mix(uikit.SecondaryInk(c, ink), 0.5)), full)
	})
}

func gitUnifiedDiff(c *ui.Context, diff string, ink ...ui.Color) {
	oldLine, newLine := 0, 0
	inHunk := false
	ui.Column(c).FillWidth().MinWidth(0).Children(func() {
		for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
			if strings.HasPrefix(line, "@@") {
				inHunk = true
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					oldLine, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(fields[1], "-"), ",")[0])
					newLine, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(fields[2], "+"), ",")[0])
				}
				ui.Text(c, line).FillWidth().Padding(4, 8).Font(monoFamily).FontSize(fsTiny).TextColor(uikit.SecondaryInk(c, ink))
				continue
			}
			if !inHunk && (strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++")) {
				continue
			}
			number, changed := newLine, false
			switch {
			case strings.HasPrefix(line, "-"):
				number = oldLine
				oldLine++
				changed = true
			case strings.HasPrefix(line, "+"):
				newLine++
				changed = true
			case strings.HasPrefix(line, " "):
				oldLine++
				newLine++
			default:
				number = 0
			}
			ui.Row(c).FillWidth().MinWidth(0).Shrink(0).Children(func() { diffCell(c, line, changed, number, ink...) })
		}
	})
}

func (a *desktop) activateWorkspaceTab(index int) {
	if index < 0 || index >= len(a.tabs) || a.closing {
		return
	}
	if a.page == "settings" {
		a.commitSettingsOnLeave()
	}
	if a.page == "models" {
		a.cancelOAuthWhenLeaving()
	}
	a.active, a.page = index, "chat"
	a.refreshFiles()
	a.refreshSessionFooter(a.tabs[index])
	a.refreshGit(a.tabs[index])
}

func (a *desktop) globalShortcuts(c *ui.Context) {
	if !a.ready || a.closing {
		return
	}
	c.OnShortcut(ui.Cmd, ui.KeyN, a.newSession)
	c.OnShortcut(ui.Cmd, ui.KeyT, func() {
		root := a.config.Workspace
		if t := a.current(); t != nil && !strings.HasPrefix(filepath.Base(t.Workspace), "ally-temp-") {
			root = t.Workspace
		}
		a.newConversation(root)
	})
	c.OnShortcut(ui.Cmd, ui.KeyW, func() { a.closeWorkspaceTab(a.active) })
	c.OnShortcut(ui.Ctrl, ui.KeyTab, func() {
		if len(a.tabs) > 0 {
			a.activateWorkspaceTab((a.active + 1) % len(a.tabs))
		}
	})
	c.OnShortcut(ui.Ctrl|ui.Shift, ui.KeyTab, func() {
		if len(a.tabs) > 0 {
			a.activateWorkspaceTab((a.active + len(a.tabs) - 1) % len(a.tabs))
		}
	})
	if current := a.current(); current != nil && current.Draft == "" && a.page == "chat" {
		file := a.activeFile()
		if file == nil || file.Editor == nil || !file.Editor.Focused() {
			c.OnShortcut(ui.Cmd, ui.KeyLeft, func() { a.activateWorkspaceTab((a.active + len(a.tabs) - 1) % len(a.tabs)) })
			c.OnShortcut(ui.Cmd, ui.KeyRight, func() { a.activateWorkspaceTab((a.active + 1) % len(a.tabs)) })
		}
	}
	for i, key := range []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9} {
		c.OnShortcut(ui.Alt, key, func() { a.activateWorkspaceTab(i) })
	}
}

func restoreRoundStats(t *conversation) {
	st := t.uiState()
	for i, msg := range t.Messages {
		raw, ok := msg["roundStats"].(map[string]any)
		if !ok {
			if num(msg, "roundDurationMs") > 0 || str(msg, "roundDurationText") != "" {
				raw = map[string]any{"durationMs": num(msg, "roundDurationMs"), "inputTokens": num(msg, "runInputTokens"), "outputTokens": num(msg, "runOutputTokens"), "cacheHit": num(msg, "cacheHit"), "cacheMiss": num(msg, "cacheMiss")}
			} else if num(msg, "roundDoneAt") > 0 {
				raw = map[string]any{}
			} else {
				continue
			}
		}
		st.Round[i] = &roundStat{DurationMS: int64(num(raw, "durationMs")), InputTokens: int(num(raw, "inputTokens")), OutputTokens: int(num(raw, "outputTokens")), CacheHit: int(num(raw, "cacheHit")), CacheMiss: int(num(raw, "cacheMiss")), Done: true}
	}
}
