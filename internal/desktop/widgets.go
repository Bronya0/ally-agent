// SPDX-License-Identifier: GPL-3.0-only
package desktop

// This file is the reusable component layer. Every page composes these
// widgets; colours, sizes and spacing stay in ui_theme.go so the whole native
// UI can be re-skinned or ported by touching two files.

import (
	"fmt"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// ---- Text ----------------------------------------------------------------

func label(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(fsBody)
}

func muted(c *ui.Context, s string, size float32) ui.Element {
	return ui.Text(c, s).FontSize(size).TextColor(c.Theme().TextMuted)
}

const monoFamily = "Cascadia Mono, Consolas, monospace"

func mono(c *ui.Context, s string, size float32) ui.Element {
	return ui.Text(c, s).Font(monoFamily).FontSize(size).Selectable()
}

func sectionTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(fsSection).Bold()
}

// ---- Buttons -------------------------------------------------------------

func flat(c *ui.Context, s string) ui.Element {
	return ui.Button(c, s).Background(ui.Transparent).Border(0, c.Theme().Border).Radius(radiusControl).FontSize(fsSmall).Cursor(ui.CursorPointer)
}

func flatKey(c *ui.Context, key any, s string) ui.Element {
	return flat(c.Key(key), s)
}

func ghost(c *ui.Context, s string) ui.Element {
	return ui.Button(c, s).FontSize(fsSmall).Radius(radiusControl).Cursor(ui.CursorPointer)
}

func primary(c *ui.Context, s string) ui.Element {
	return ui.PrimaryButton(c, s).FontSize(fsSmall).Radius(radiusControl).Cursor(ui.CursorPointer)
}

func dangerButton(c *ui.Context, s string) ui.Element {
	return ui.Button(c, s).FontSize(fsSmall).Radius(radiusControl).TextColor(colDanger()).Cursor(ui.CursorPointer)
}

func iconButton(c *ui.Context, id, tip, name string, fn func()) ui.Element {
	b := ui.ButtonBase(c.Key(id)).Size(30, 30).Center().Radius(radiusControl).Label(tip).Tooltip(tip).Cursor(ui.CursorPointer).OnClick(fn)
	if b.Hovered() {
		b.Background(c.Theme().SurfaceHover)
	}
	return b.Children(func() { iconGlyph(c, name, 17) })
}

func iconAction(c *ui.Context, id, tip, name string, size float32, fn func()) ui.Element {
	b := ui.ButtonBase(c.Key(id)).Size(size, size).Center().Radius(radiusSmall).Label(tip).Tooltip(tip).Cursor(ui.CursorPointer).OnClick(fn)
	if b.Hovered() {
		b.Background(c.Theme().SurfaceHover)
	}
	return b.Children(func() { iconGlyph(c, name, size*0.62) })
}

func railIcon(c *ui.Context, name string, size float32) ui.Element {
	if icon := originalRailIcons[name]; icon != nil {
		return ui.Icon(c, icon).Size(size, size).Shrink(0)
	}
	return iconGlyph(c, name, size).Shrink(0)
}

// railNavigationButton keeps the collapsed icon slot separate from the
// expanded label row, matching main's 20px glyph and centered 34px badge.
func railNavigationButton(c *ui.Context, id, label, iconName string, expanded, active bool, trailing func()) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(id)).FillWidth().Height(heightRailItem).Justify(ui.Start).Radius(radiusControl).Label(label).Cursor(ui.CursorPointer)
	if !expanded {
		b.Height(heightRailItemCollapsed).Center().Tooltip(label)
	}
	if active {
		b.TextColor(t.Accent)
	}
	highlighted := active || b.Hovered()
	if expanded && highlighted {
		b.Background(t.SurfaceHover)
	}
	return b.Children(func() {
		if !expanded {
			badge := ui.Row(c).Size(sizeRailBadge, sizeRailBadge).Shrink(0).Center().Radius(radiusRailBadge)
			if highlighted {
				badge.Background(t.SurfaceHover)
			}
			badge.Children(func() { railIcon(c, iconName, sizeRailIconCollapsed) })
			return
		}
		ui.Row(c).FillWidth().Padding(0, 8).Gap(10).AlignItems(ui.Center).Children(func() {
			railIcon(c, iconName, sizeRailIcon)
			ui.Text(c, label).FontSize(12.5).SingleLine()
			if trailing != nil {
				trailing()
			}
		})
	})
}

func railToggleButton(c *ui.Context, id, tip, name string, fn func()) ui.Element {
	b := ui.ButtonBase(c.Key(id)).Size(sizeRailBadge, sizeRailBadge).AlignSelf(ui.Center).Center().Radius(radiusRailBadge).
		Label(tip).Tooltip(tip).TextColor(c.Theme().TextMuted).Cursor(ui.CursorPointer).OnClick(fn)
	if b.Hovered() {
		b.Background(c.Theme().SurfaceHover)
	}
	return b.Children(func() { iconGlyph(c, name, sizeRailToggleIcon).Shrink(0) })
}

func iconGlyph(c *ui.Context, name string, size float32) ui.Element {
	icon := icons[name]
	if original := originalRailIcons[name]; original != nil {
		icon = original
	}
	e := ui.Icon(c, icon)
	if e.Valid() {
		e.Size(size, size)
	}
	return e
}

// ---- Surfaces ------------------------------------------------------------

func card(c *ui.Context, key any, build func()) ui.Element {
	return ui.Column(c.Key(key)).Padding(padCard).Gap(gapField).Border(1, c.Theme().Border).
		Background(c.Theme().Surface).Radius(radiusCard).Children(build)
}

// rowCard is the hover-highlighted list row the original used for plugins,
// skills, SSH nodes and model cards.
func rowCard(c *ui.Context, key any, build func()) ui.Element {
	e := ui.Column(c.Key(key)).Padding(padCard).Gap(gapField).Border(1, c.Theme().Border).
		Background(c.Theme().Surface).Radius(radiusCard)
	if e.Hovered() {
		e.Background(c.Theme().SurfaceHover)
	}
	return e.Children(build)
}

// selectableRow is a single-line row with hover and selected states.
func selectableRow(c *ui.Context, key any, selected bool, height float32, build func()) ui.Element {
	e := ui.Row(c.Key(key)).Height(height).Padding(0, padRow).Gap(gapInline).Radius(radiusSmall).AlignItems(ui.Center)
	if selected {
		e.Background(c.Theme().SurfaceHover)
	} else if e.Hovered() {
		e.Background(c.Theme().SurfaceHover)
	}
	return e.Children(build)
}

func divider(c *ui.Context) { ui.Box(c).Height(1).FillWidth().Background(c.Theme().Border) }

func pageHeader(c *ui.Context, title, subtitle string, actions func()) {
	ui.Row(c).Gap(gapSection).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(fsPageTitle).Bold()
			if subtitle != "" {
				muted(c, subtitle, fsSmall)
			}
		})
		ui.Spacer(c)
		if actions != nil {
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(actions)
		}
	})
	divider(c)
}

func sectionHeader(c *ui.Context, title string, actions func()) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		sectionTitle(c, title)
		ui.Spacer(c)
		if actions != nil {
			ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(actions)
		}
	})
}

func emptyState(c *ui.Context, title, detail string) {
	ui.Column(c).Fill().Center().Gap(gapInline).Children(func() {
		ui.Text(c, title).FontSize(15).TextColor(c.Theme().TextMuted)
		if detail != "" {
			ui.Text(c, detail).FontSize(fsSmall).TextColor(faint)
		}
	})
}

func busyRow(c *ui.Context, s string) {
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c).Size(14, 14)
		muted(c, s, fsSmall)
	})
}

// notice is the coloured banner used for errors, warnings and hints.
func notice(c *ui.Context, tone, message string) {
	if message == "" {
		return
	}
	fg := c.Theme().TextMuted
	fill := c.Theme().SurfaceHover
	bar := c.Theme().Border
	switch tone {
	case "error":
		fg, fill, bar = colDanger(), ui.RGBA(232, 84, 84, 0.10), colDanger()
	case "warn":
		fg, fill, bar = colWarning(), ui.RGBA(217, 164, 65, 0.10), colWarning()
	case "ok":
		fg, fill, bar = colSuccess(), ui.RGBA(78, 200, 126, 0.10), colSuccess()
	case "info":
		fg, fill, bar = c.Theme().Accent, ui.RGBA(141, 165, 223, 0.10), c.Theme().Accent
	}
	ui.Row(c).FillWidth().Padding(7, 10).Gap(gapField).Radius(radiusSmall).Background(fill).
		BorderWidth(0, 0, 0, 3).BorderColor(bar).Children(func() {
		ui.Text(c, message).FontSize(fsSmall).TextColor(fg).LineHeight(1.5)
	})
}

// ---- Controls ------------------------------------------------------------

func fieldLabel(c *ui.Context, s string) {
	ui.Text(c, s).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
}

func fieldNote(c *ui.Context, s string) {
	if s != "" {
		ui.Text(c, s).FontSize(fsTiny).TextColor(c.Theme().TextMuted).LineHeight(1.5)
	}
}

func fieldRow(c *ui.Context, id, name string, value *string, password bool) {
	ui.Column(c.Key("field-" + id)).Gap(gapTight).Children(func() {
		fieldLabel(c, name)
		e := ui.TextInput(c, value).FillWidth().Label(name)
		if password {
			e.Password()
		}
	})
}

func textField(c *ui.Context, id, placeholder, name string, value *string) ui.Element {
	return ui.TextInput(c.Key(id), value).Placeholder(placeholder).Label(name).FillWidth()
}

func numberField(c *ui.Context, id, name string, value *float64, lo, hi, step float64) ui.Element {
	return ui.NumberInput(c.Key(id), value, lo, hi, step).Label(name).Width(150)
}

func switchRow(c *ui.Context, id, name, note string, value *bool) ui.Element {
	var sw ui.Element
	ui.Column(c.Key("switch-" + id)).Gap(2).Children(func() {
		ui.Row(c).Gap(gapSection).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				label(c, name)
				fieldNote(c, note)
			})
			sw = ui.Switch(c, value).Label(name)
		})
	})
	return sw
}

func checkRow(c *ui.Context, id, name, note string, value *bool) {
	ui.Column(c.Key("check-" + id)).Gap(2).Children(func() {
		ui.Checkbox(c, value, name)
		fieldNote(c, note)
	})
}

func selectField(c *ui.Context, id, name string, value *string, options []string, width float32) ui.Element {
	e := ui.Select(c.Key(id), value, options).Label(name)
	if width > 0 {
		e.Width(width)
	}
	return e
}

func textArea(c *ui.Context, id, name, placeholder string, value *string, height float32) ui.Element {
	return ui.TextArea(c.Key(id), value).Label(name).Placeholder(placeholder).Height(height)
}

// tabsBar wraps the native tab widget with the page-level sizing convention.
func tabsBar(c *ui.Context, selected *int, labels ...string) ui.Element {
	return ui.Tabs(c.Key("tabs-"+strings.Join(labels, "|")), selected, labels...).Label(labels[0])
}

// tabScroller handles ordinary mouse wheels as horizontal motion. Moving the
// scrollbar below its clipped parent keeps the native scroll behavior without
// drawing or exposing a draggable thumb over the tab underline.
func tabScroller(c *ui.Context) ui.Element {
	scroller := ui.ScrollHorizontal(c).ScrollbarInsets(0, 0, -20, 0)
	state := ui.Local[ui.ScrollState](scroller, "tab-scroll", func() ui.ScrollState { return ui.ScrollState{} })
	return scroller.TrackScroll(state).HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputScroll {
			return false
		}
		delta := ev.DY
		if ev.DX != 0 {
			delta = ev.DX
		}
		if delta == 0 {
			return false
		}
		state.X = max(0, min(state.MaxX, state.X+delta))
		return true
	})
}

func scrollTabs(c *ui.Context, selected *int, labels ...string) bool {
	changed := false
	ui.Box(c).FillWidth().Clip().Children(func() {
		tabScroller(c).FillWidth().Children(func() { changed = tabsBar(c, selected, labels...).Changed() })
	})
	return changed
}

func segmented(c *ui.Context, id string, selected *int, labels ...string) ui.Element {
	return ui.Segmented(c.Key(id), selected, labels...).Label(labels[0])
}

// ---- Pills and status ----------------------------------------------------

func chip(c *ui.Context, text string, fg, fill ui.Color) ui.Element {
	return ui.Box(c).Padding(1, 7).Radius(9).Background(fill).Children(func() {
		ui.Text(c, text).FontSize(fsTiny).TextColor(fg).NoWrap()
	})
}

func neutralChip(c *ui.Context, text string) ui.Element {
	return chip(c, text, c.Theme().TextMuted, c.Theme().SurfaceHover)
}

func accentChip(c *ui.Context, text string) ui.Element {
	return chip(c, text, c.Theme().Accent, c.Theme().Surface.Mix(c.Theme().Accent, 0.14))
}

func statusTone(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "connected", "success", "completed", "done", "ok", "approved", "signedin":
		return "ok"
	case "warn", "warning", "connecting", "running", "pending", "in_progress", "waiting", "queued":
		return "warn"
	case "error", "failed", "failure", "timeout", "invalid", "cancelled", "canceled":
		return "error"
	}
	return "idle"
}

func toneColor(c *ui.Context, status string) ui.Color {
	switch statusTone(status) {
	case "ok":
		if !c.Theme().Dark {
			return ui.Hex("#19864b")
		}
		return colSuccess()
	case "warn":
		if !c.Theme().Dark {
			return ui.Hex("#a76a16")
		}
		return colWarning()
	case "error":
		if !c.Theme().Dark {
			return ui.Hex("#c63e3e")
		}
		return colDanger()
	}
	return c.Theme().TextMuted
}

func statusDot(c *ui.Context, status string) ui.Element {
	return ui.Box(c).Size(7, 7).Radius(4).Background(toneColor(c, status))
}

func statusBadge(c *ui.Context, text, status string) ui.Element {
	fg := toneColor(c, status)
	return chip(c, text, fg, c.Theme().Surface.Mix(fg, 0.12))
}

// ---- Dialog scaffolding --------------------------------------------------

// modalTransition is the single enter/exit animation for every modal: the
// panel fades in while rising 8px, and sinks back out on close. One place
// owns it so all dialogs open and close the same way; transform+opacity only,
// so no layout is disturbed. ReduceMotion is honoured by the framework.
func modalTransition() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: 160 * time.Millisecond,
		Enter:    &ui.Motion{Y: 8},
		Exit:     &ui.Motion{Y: 8},
	}
}

// modalBackdrop dims the window behind every dialog.
func modalBackdrop(backdrop ui.Element) {
	backdrop.Background(ui.RGBA(0, 0, 0, 0.48))
}

// modalPanelChrome styles a dialog panel; favours a shared look with one
// call. The Transition tracks the panel by its position among the overlay
// children, which is stable: each dialog's panel is its overlay's content.
func modalPanelChrome(c *ui.Context, panel ui.Element, width float32) {
	w, h := c.Size()
	panel.Width(width).MaxWidth(max(200, w-48)).MaxHeight(max(180, h-48)).Padding(padPage).
		Background(c.Theme().Surface).Border(1, c.Theme().Border).Radius(radiusCard).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.25)).
		Transition(modalTransition())
}

func modalCard(c *ui.Context, open *bool, width float32, build func()) ui.Element {
	_, h := c.Size()
	return ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		modalBackdrop(backdrop)
		modalPanelChrome(c, panel, width)
		ui.Scroll(c).MaxHeight(max(120, h-92)).Children(func() { ui.Column(c).Gap(gapSection).Children(build) })
	})
}

// formDialog pins the title and actions while only the form body scrolls.
func formDialog(c *ui.Context, open *bool, width, preferredHeight float32, title, subtitle string, body, actions func(), onClose ...func()) ui.Element {
	_, h := c.Size()
	dialog := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		modalBackdrop(backdrop)
		modalPanelChrome(c, panel, width)
		panel.Height(min(preferredHeight, max(240, h-48)))
		ui.Column(c).Fill().Children(func() {
			ui.Row(c).Shrink(0).Padding(16, 20, 12).Gap(gapField).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, title).FontSize(17).Bold()
					if subtitle != "" {
						fieldNote(c, subtitle)
					}
				})
				iconAction(c, "dialog-close-"+title, "关闭", "close", 24, func() {
					*open = false
					if len(onClose) > 0 {
						onClose[0]()
					}
				})
			})
			ui.Scroll(c).Grow(1).MinHeight(0).Children(func() { ui.Column(c).Padding(0, 20, 12).Gap(gapSection).Children(body) })
			ui.Row(c).Shrink(0).Padding(12, 20, 16).Gap(gapField).Justify(ui.End).Children(actions)
		})
	})
	if dialog.Dismissed() && len(onClose) > 0 {
		onClose[0]()
	}
	return dialog
}

func modalTitle(c *ui.Context, title, subtitle string) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(17).Bold()
			if subtitle != "" {
				fieldNote(c, subtitle)
			}
		})
		ui.Spacer(c)
	})
}

func modalActions(c *ui.Context, build func()) {
	ui.Row(c).Gap(gapField).Justify(ui.End).Children(build)
}

// ---- Composite layout helpers -------------------------------------------

// twoColumn lays out label/control pairs in two even columns, the shape most
// settings and editor dialogs use.
func twoColumn(c *ui.Context, left, right func()) {
	ui.Row(c).Gap(gapSection).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Grow(1).Basis(0).Gap(gapField).Children(left)
		ui.Column(c).Grow(1).Basis(0).Gap(gapField).Children(right)
	})
}

func kv(c *ui.Context, key, value string) {
	ui.Row(c).Gap(gapField).Children(func() {
		ui.Box(c).Width(96).Children(func() { ui.Text(c, key).FontSize(fsSmall).TextColor(c.Theme().TextMuted) })
		ui.Text(c, value).FontSize(fsSmall).Selectable()
	})
}

func helpText(c *ui.Context, s string) { fieldNote(c, s) }

func progressBar(c *ui.Context, id, name string, value float64) {
	v := value
	if v < 0 || v > 1 {
		v = -1
	}
	ui.Progress(c.Key(id), v).Label(name)
}

func statCard(c *ui.Context, id, title, big, detail string) {
	card(c, "stat-"+id, func() {
		ui.Text(c, title).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
		ui.Text(c, big).FontSize(22).Bold()
		if detail != "" {
			ui.Text(c, detail).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
		}
	})
}

// codeBlock renders read-only monospaced content with an optional line cap.
// The 150px collapsed cap mirrors main's .tool-body so collapsed previews
// stop at the same height in both clients.
func codeBlock(c *ui.Context, id, content string, maxHeight float32) {
	if maxHeight <= 0 || maxHeight > 150 {
		maxHeight = 150
	}
	e := ui.Text(c.Key(id), content).Font(monoFamily).FontSize(fsSmall).
		Selectable().FillWidth().Padding(8, 10).Background(c.Theme().Background).Radius(radiusSmall)
	e.MaxHeight(maxHeight).Clip()
}

// inlineError is the recurring red detail line under failed operations.
func inlineError(c *ui.Context, id, message string) {
	if message == "" {
		return
	}
	ui.Text(c.Key(id), message).FontSize(fsTiny).TextColor(colDanger()).MaxLines(4)
}

func spacerRow(c *ui.Context, build func()) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		build()
		ui.Spacer(c)
	})
}

func badgeText(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(fsTiny).TextColor(c.Theme().Accent)
}

func fmtCountLabel(n int, unit string) string { return fmt.Sprintf("%d %s", n, unit) }

// ---- Shared derivations --------------------------------------------------

// mcpSummary is the one-line MCP status shown on the welcome card and in the
// composer tooltips.
func mcpSummary(servers []map[string]any) string {
	connected, tools := 0, 0
	for _, s := range servers {
		if str(s, "status") == "connected" {
			connected++
		}
		switch n := s["toolCount"].(type) {
		case int:
			tools += n
		case float64:
			tools += int(n)
		}
	}
	if len(servers) == 0 {
		return "无 MCP 服务"
	}
	return fmt.Sprintf("%d/%d 个服务，%d 个工具", connected, len(servers), tools)
}

func enabledToolCount(tools []backend.ToolDefinitionSummary) int {
	n := 0
	for _, tool := range tools {
		if tool.Enabled {
			n++
		}
	}
	return n
}

func tokenCount(n int) string { return fmtK(n) }

// mygoMessage asks a blocking yes/no question and returns true for the second
// button. It must be called from a goroutine, never from a view.
func mygoMessage(title, detail, cancel, confirm string) (bool, error) {
	result, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type:         mygo.MessageWarning,
		Title:        title,
		Message:      title,
		Detail:       detail,
		Buttons:      []string{cancel, confirm},
		CancelButton: 0,
	})
	if err != nil {
		return false, err
	}
	return result.Button == 1, nil
}

// popoverPanel and popoverItem keep application menus consistent with the
// selected palette instead of relying on the operating system's menu theme.
func popoverPanel(c *ui.Context, anchor ui.Element, open *bool, width float32, build func()) {
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		popoverChrome(c, panel, width)
		ui.Column(c).FillWidth().Children(build)
	})
}

// popoverChrome styles a popover panel; cascading flyouts share it with popoverPanel.
func popoverChrome(c *ui.Context, panel ui.Element, width float32) {
	panel.Width(width).MaxWidthPercent(90).MaxHeight(420).Padding(5).Background(c.Theme().Surface).Border(1, c.Theme().Border).Radius(radiusCard)
}

func popoverItem(c *ui.Context, key, text, detail string, selected bool, action func()) {
	b := ui.ButtonBase(c.Key(key)).FillWidth().MinHeight(32).Padding(6, 10).Justify(ui.Start).Radius(radiusSmall).Label(text).Cursor(ui.CursorPointer).OnClick(action)
	if selected || b.Hovered() {
		b.Background(c.Theme().SurfaceHover)
	}
	b.Children(func() {
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			ui.Text(c, text).FontSize(fsBody).SingleLine()
			if detail != "" {
				ui.Text(c, detail).FontSize(fsTiny).TextColor(c.Theme().TextMuted).SingleLine()
			}
		})
	})
}

// resizeDivider keeps the drag target independent of either pane's contents.
func (a *desktop) resizeDivider(c *ui.Context, key string, width *float32, direction, minimum, maximum float32, dragging *bool) {
	edge := ui.Box(c.Key(key)).Width(5).FillHeight().Shrink(0).Cursor(ui.CursorResizeEW)
	if edge.Hovered() {
		edge.Background(c.Theme().Accent.Alpha(0.35))
	}
	if dx, _, held := edge.Dragged(); held {
		*dragging = true
		if dx != 0 {
			*width = max(minimum, min(maximum, *width+direction*dx))
		}
	} else if *dragging {
		*dragging = false
		a.savePrefs()
	}
}

func windowAction(c *ui.Context, id, tip, icon string, action func()) ui.Element {
	b := ui.ButtonBase(c.Key(id)).Size(46, 44).Center().Label(tip).Tooltip(tip).Cursor(ui.CursorPointer).OnClick(action)
	if b.Hovered() {
		b.Background(c.Theme().SurfaceHover)
	}
	return b.Children(func() { iconGlyph(c, icon, 17) })
}

func footerBadge(c *ui.Context, id, name, value string, color ui.Color, tip string, action func()) ui.Element {
	b := ui.ButtonBase(c.Key(id)).Height(22).Padding(1, 6).Radius(11).Border(1, c.Theme().Border).Background(c.Theme().Surface).Label(tip).Tooltip(tip).Cursor(ui.CursorPointer).OnClick(action)
	if b.Hovered() {
		b.Background(c.Theme().SurfaceHover)
	}
	b.Children(func() {
		ui.Row(c).Gap(4).Children(func() {
			ui.Box(c).Padding(1, 4).Radius(6).Background(c.Theme().Surface.Mix(color, 0.18)).Children(func() { ui.Text(c, name).FontSize(fsTiny).Bold().TextColor(color) })
			ui.Text(c, value).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
		})
	})
	return b
}

func (a *desktop) popupFlag(key string) *bool {
	if a.popupFlags == nil {
		a.popupFlags = map[string]*bool{}
	}
	if a.popupFlags[key] == nil {
		a.popupFlags[key] = new(bool)
	}
	return a.popupFlags[key]
}

func (a *desktop) linkMenu(c *ui.Context, key string, links [][2]string) {
	open := a.popupFlag(key)
	btn := flat(c.Key(key+"-button"), "市场 ▾").OnClick(func() { *open = !*open })
	services := c.Services()
	popoverPanel(c, btn, open, 240, func() {
		for i, entry := range links {
			entry := entry
			popoverItem(c, fmt.Sprintf("%s-%d", key, i), entry[0], "", false, func() { *open = false; services.OpenURL(entry[1]) })
		}
	})
}
