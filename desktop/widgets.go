// SPDX-License-Identifier: GPL-3.0-only
package desktop

// This file is the reusable component layer. Every page composes these
// widgets; colours, sizes and spacing stay in ui_theme.go so the whole native
// UI can be re-skinned or ported by touching two files.

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// ---- Text ----------------------------------------------------------------

func label(c *ui.Context, s string) ui.Element {
	return uikit.Label(c, s)
}

func muted(c *ui.Context, s string, size float32) ui.Element {
	return uikit.Muted(c, s, size)
}

var monoFamily = nativeMonoFontFamily()

func mono(c *ui.Context, s string, size float32) ui.Element {
	return ui.Text(c, s).Font(monoFamily).FontSize(size).Selectable()
}

func sectionTitle(c *ui.Context, s string) ui.Element {
	return uikit.SectionTitle(c, s)
}

// ---- Buttons -------------------------------------------------------------
// Everything below is a thin alias over buttons.go, so a call site reads as
// what the button means and the look stays in one place.

func flat(c *ui.Context, s string) ui.Element {
	return button(c, nil, s, btnSecondary, false, "")
}

func flatKey(c *ui.Context, key any, s string) ui.Element {
	return button(c, key, s, btnSecondary, false, "")
}

func ghost(c *ui.Context, s string) ui.Element {
	return button(c, nil, s, btnGhost, false, "")
}

func primary(c *ui.Context, s string) ui.Element {
	return button(c, nil, s, btnPrimary, false, "")
}

func dangerButton(c *ui.Context, s string) ui.Element {
	return button(c, nil, s, btnDanger, false, "")
}

func dangerIconAction(c *ui.Context, id, tip, name string, size float32, fn func()) ui.Element {
	return iconButtonSized(c, id, tip, name, size, iconDanger, fn)
}

// ---- Selection -----------------------------------------------------------
// Every "this one is chosen" control is built from these three: a chip, a
// colour swatch and a vertical row. They paint their own fill, edge and label —
// which is the whole point. flat() / primary() already painted their label, so a
// caller that sets TextColor or Background afterwards changed nothing, and a
// chosen item looked exactly like an unchosen one.

// choiceButton is one chip in a set of mutually exclusive choices (avatar
// styles, risk levels, game kinds).
func choiceButton(c *ui.Context, key, text string, selected bool, tone ui.Color) ui.Element {
	return choiceChip(c, key, text, selected, tone, false)
}

// choiceSwatch is choiceButton for colour swatches: the label keeps the colour
// it selects, so a palette chip previews its accent while unselected.
func choiceSwatch(c *ui.Context, key, text string, selected bool, tone ui.Color) ui.Element {
	return choiceChip(c, key, text, selected, tone, true)
}

func choiceChip(c *ui.Context, key, text string, selected bool, tone ui.Color, toneInk bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Height(heightControl).Padding(3, 9).Radius(radiusControl).
		Label(text).Cursor(ui.CursorPointer)
	fill, ink, edge := t.Surface, t.TextMuted, surfaceBorder(c)
	if toneInk {
		ink = tone
	}
	switch {
	case selected:
		fill, ink, edge = selectionFill(tone), tone, selectionEdge(tone)
	case b.Hovered():
		fill, ink, edge = t.SurfaceHover, t.Text, t.Border.Mix(t.Text, edgeHoverMix)
	}
	b.Border(1, fadeColor(b, "choice-edge", edge)).Background(fadeColor(b, "choice-fill", fill))
	b.TextColor(ink)
	return b.Children(func() {
		ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
			// A chosen chip says so with tint, ink and weight — never a check mark:
			// the mark doubled the signal and left a stray glyph in every chip that
			// had to reserve room for it.
			glyph := ui.Text(c, text).FontSize(fsSmall).TextColor(ink).SingleLine()
			if selected {
				glyph.FontWeight(600)
			}
		})
	})
}

// selectionRow is the vertical-list form of the selected look: accent tint,
// accent ink, bold label and a reserved accent rail, so the text never shifts
// when the choice changes. Used by the settings rail and any other one-of-N list.
func selectionRow(c *ui.Context, key any, text string, selected bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).FillWidth().Height(heightControl+2).Padding(0, 8).
		Justify(ui.Start).Radius(radiusControl).Label(text).Cursor(ui.CursorPointer)
	fill, ink := ui.Transparent, t.TextMuted
	switch {
	case selected:
		fill, ink = selectionFill(t.Accent), t.Accent
	case b.Hovered():
		fill, ink = t.SurfaceHover, t.Text
	}
	b.Background(fadeColor(b, "row-fill", fill)).TextColor(ink)
	return b.Children(func() {
		ui.Row(c).FillWidth().Gap(gapField).AlignItems(ui.Center).Children(func() {
			rail := ui.Box(c).Width(3).Height(13).Shrink(0).Radius(2)
			if selected {
				rail.Background(ink)
			}
			glyph := ui.Text(c, text).FontSize(fsSmall).TextColor(ink).SingleLine()
			if selected {
				glyph.FontWeight(600)
			}
		})
	})
}

func iconButton(c *ui.Context, id, tip, name string, fn func()) ui.Element {
	return iconButtonSized(c, id, tip, name, 30, iconNormal, fn)
}

func iconAction(c *ui.Context, id, tip, name string, size float32, fn func()) ui.Element {
	return iconButtonSized(c, id, tip, name, size, iconNormal, fn)
}

func mutedIconAction(c *ui.Context, id, tip, name string, size float32, fn func()) ui.Element {
	return iconButtonSized(c, id, tip, name, size, iconQuiet, fn)
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
	return navItem(c, id, label, iconName, expanded, active, trailing)
}

func railToggleButton(c *ui.Context, id, tip, name string, fn func()) ui.Element {
	b := ui.ButtonBase(c.Key(id)).Size(sizeRailBadge, sizeRailBadge).AlignSelf(ui.Center).Center().Radius(radiusRailBadge).
		Label(tip).Tooltip(tip).TextColor(c.Theme().TextMuted).Cursor(ui.CursorPointer).OnClick(fn)
	fill := ui.Transparent
	if b.Hovered() {
		fill = c.Theme().SurfaceHover
	}
	b.Background(fadeColor(b, "fill", fill))
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
	return uikit.Card(c, key, padCard, radiusCard, build)
}

// rowCard is the hover-highlighted list row the original used for plugins,
// skills, SSH nodes and model cards.
func rowCard(c *ui.Context, key any, build func()) ui.Element {
	return uikit.RowCard(c, key, padRow, radiusCard, build)
}

// selectableRow is a single-line row with hover and selected states.
func selectableRow(c *ui.Context, key any, selected bool, height float32, build func()) ui.Element {
	return uikit.SelectableRow(c, key, selected, height, padRow, radiusSmall, build)
}

func divider(c *ui.Context) { uikit.Divider(c) }

func pageHeader(c *ui.Context, title, subtitle string, actions func()) {
	uikit.PageHeader(c, title, subtitle, actions)
}

func sectionHeader(c *ui.Context, title string, actions func()) {
	uikit.SectionHeader(c, title, actions)
}

func emptyState(c *ui.Context, title, detail string) {
	uikit.EmptyState(c, title, detail)
}

func busyRow(c *ui.Context, s string) {
	uikit.BusyRow(c, s)
}

// noticeTone maps the tone words the call sites use onto the kit's tones.
func noticeTone(tone string) uikit.NoticeTone {
	switch tone {
	case "error":
		return uikit.NoticeDanger
	case "warn":
		return uikit.NoticeWarn
	case "ok":
		return uikit.NoticeOK
	case "info":
		return uikit.NoticeInfo
	}
	return uikit.NoticeNeutral
}

// notice is the coloured banner used for errors, warnings and hints.
func notice(c *ui.Context, tone, message string) {
	uikit.Notice(c, noticeTone(tone), message, true)
}

func noticeNoBar(c *ui.Context, tone, message string) {
	uikit.Notice(c, noticeTone(tone), message, false)
}

// ---- Controls ------------------------------------------------------------

func fieldLabel(c *ui.Context, s string) { uikit.FieldLabel(c, s) }
func fieldNote(c *ui.Context, s string)  { uikit.FieldNote(c, s) }

// fieldRow is a labelled text field. ui.Field owns the label: it names the
// control for assistive technology and focuses it when clicked, which a hand
// written Text above the input never did.
func fieldRow(c *ui.Context, id, name string, value *string, password bool) {
	ui.Field(c.Key("field-"+id), name, func() {
		if password {
			uikit.PasswordInput(c, id, name, value, fieldExample(id, name)).FillWidth()
		} else {
			uikit.Input(c, value).FillWidth().MinWidth(0).Placeholder(fieldExample(id, name)).FontSize(fsBody)
		}
	}).JustifySelf(ui.Stretch).AlignSelf(ui.Stretch).MinWidth(0).Shrink(0)
}

func textField(c *ui.Context, id, placeholder, name string, value *string) ui.Element {
	return uikit.TextField(c.Key(id), value, placeholder).Label(name)
}

func numberField(c *ui.Context, id, name string, value *float64, lo, hi, step float64) ui.Element {
	return uikit.NumberInput(c, id, name, value, lo, hi, step).Width(150)
}

func switchRow(c *ui.Context, id, name, note string, value *bool) ui.Element {
	var sw ui.Element
	ui.Column(c.Key("switch-" + id)).Shrink(0).Gap(6).Children(func() {
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
	e := uikit.Select(c.Key(id), value, options).Label(name)
	if width > 0 {
		e.Width(width)
	}
	return e
}

func textArea(c *ui.Context, id, name, placeholder string, value *string, height float32) ui.Element {
	return uikit.TextArea(c.Key(id), value, placeholder, height).Label(name)
}

// tabsBar wraps the native tab widget with the page-level sizing convention.
func tabsBar(c *ui.Context, selected *int, labels ...string) ui.Element {
	return uikit.Tabs(c.Key("tabs-"+strings.Join(labels, "|")), selected, labels...).Label(labels[0]).Cursor(ui.CursorPointer)
}

// tabScroller scrolls the tab strip sideways. ScrollHorizontal already turns the
// wheel into horizontal motion, so this only keeps the scrollbar below its
// clipped parent: native scrolling without a draggable thumb over the underline.
func tabScroller(c *ui.Context) ui.Element {
	return ui.ScrollHorizontal(c).ScrollbarInsets(0, 0, -20, 0)
}

func scrollTabs(c *ui.Context, selected *int, labels ...string) bool {
	changed := false
	ui.Box(c).FillWidth().Clip().Children(func() {
		tabScroller(c).FillWidth().Children(func() { changed = tabsBar(c, selected, labels...).Changed() })
	})
	return changed
}

func segmented(c *ui.Context, id string, selected *int, labels ...string) ui.Element {
	return uikit.Segmented(c.Key(id), selected, labels...).Label(labels[0]).Cursor(ui.CursorPointer)
}

// ---- Pills and status ----------------------------------------------------

func chip(c *ui.Context, text string, fg, fill ui.Color) ui.Element {
	return uikit.Tag(c, text, fg, fill)
}

func neutralChip(c *ui.Context, text string) ui.Element {
	return pill(c, text, pillNeutral)
}

func accentChip(c *ui.Context, text string) ui.Element {
	return pill(c, text, pillAccent)
}

func statusTone(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "connected", "success", "completed", "done", "ok", "approved", "signedin", "low":
		return "ok"
	case "warn", "warning", "connecting", "running", "pending", "in_progress", "waiting", "queued":
		return "warn"
	case "error", "failed", "failure", "timeout", "invalid", "cancelled", "canceled", "high", "danger", "denied":
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
	return pillDot(c, pillToneOf(status))
}

// pillToneOf maps a status string onto the shared badge tones.
func pillToneOf(status string) pillTone {
	switch statusTone(status) {
	case "ok":
		return pillOK
	case "warn":
		return pillWarn
	case "error":
		return pillDanger
	}
	return pillNeutral
}

func statusBadge(c *ui.Context, text, status string) ui.Element {
	return pill(c, text, pillToneOf(status))
}

// ---- Dialog scaffolding --------------------------------------------------

// modalTransition is the single enter/exit animation for every modal: the
// panel fades in while rising 8px, and sinks back out on close. One place
// owns it so all dialogs open and close the same way; transform+opacity only,
// so no layout is disturbed. ReduceMotion is honoured by the framework.
func modalTransition() ui.ElementTransition { return uikit.PopupTransition() }

// modalBackdrop dims the window behind every dialog.
func modalBackdrop(backdrop ui.Element) {
	backdrop.Background(ui.RGBA(0, 0, 0, 0.48)).Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
}

// modalPanelChrome styles a dialog panel; favours a shared look with one
// call. The Transition tracks the panel by its position among the overlay
// children, which is stable: each dialog's panel is its overlay's content.
func modalPanelChrome(c *ui.Context, panel ui.Element, width float32) {
	uikit.DialogPanel(c, panel, width)
}

func modalCard(c *ui.Context, open *bool, width float32, build func()) ui.Element {
	_, h := c.Size()
	return ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		modalBackdrop(backdrop)
		modalPanelChrome(c, panel, width)
		ui.Scroll(c).MaxHeight(max(120, h-92)).Children(func() { ui.Column(c).Gap(gapSection).Selectable().Children(build) })
	})
}

// formDialog pins the title and actions while only the form body scrolls.
func formDialog(c *ui.Context, open *bool, width, preferredHeight float32, title, subtitle string, body, actions func(), onClose ...func()) ui.Element {
	_, h := c.Size()
	dialog := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		modalBackdrop(backdrop)
		modalPanelChrome(c, panel, width)
		panel.Height(min(max(preferredHeight, h*0.65), max(240, h*0.88)))
		ui.Column(c).Fill().Selectable().Children(func() {
			ui.Row(c).Shrink(0).Padding(8, 8, 10).Gap(gapField).Children(func() {
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
			ui.Scroll(c).Grow(1).MinHeight(0).Children(func() { ui.Column(c).FillWidth().MinWidth(0).Padding(0, 8, 10).Gap(16).Children(body) })
			ui.Row(c).Shrink(0).Padding(10, 8, 8).Gap(gapField).Justify(ui.End).Children(actions)
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
	toolCodeWindow(c, id, content, true)
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
	popoverItemStyle(c, key, text, detail, selected, nil, action)
}

func popoverItemColor(c *ui.Context, key, text, detail string, selected bool, color ui.Color, action func()) {
	popoverItemStyle(c, key, text, detail, selected, &color, action)
}

// popoverItemDanger is the menu entry for a destructive action: same row as
// every other item, danger-colored label, so menus delete alike everywhere.
func popoverItemDanger(c *ui.Context, key, text string, action func()) {
	popoverItemColor(c, key, text, "", false, colDanger(), action)
}

func popoverItemStyle(c *ui.Context, key, text, detail string, selected bool, color *ui.Color, action func()) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).FillWidth().MinHeight(32).Padding(6, 10).Justify(ui.Start).Radius(radiusSmall).Label(text).Cursor(ui.CursorPointer).OnClick(action)
	switch {
	case selected:
		b.Background(selectionFill(t.Accent))
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			// Selection is tint + accent ink + weight, not a trailing check:
			// the mark pulled the eye off the label it was confirming.
			label := ui.Text(c, text).FontSize(fsBody).SingleLine()
			if color != nil {
				label.TextColor(*color)
			} else if selected {
				label.TextColor(t.Accent).FontWeight(600)
			}
			if detail != "" {
				ui.Text(c, detail).FontSize(fsTiny).TextColor(t.TextMuted).SingleLine()
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
	return windowButton(c, id, tip, icon, strings.HasPrefix(tip, "关闭"), action)
}

func footerBadge(c *ui.Context, id, name, value string, color ui.Color, tip string, action func()) ui.Element {
	b := ui.ButtonBase(c.Key(id)).Height(22).Padding(1, 6).Radius(11).Label(tip).Tooltip(tip).Cursor(ui.CursorPointer).OnClick(action)
	paintSurface(b, surfaceFill(c), surfaceBorder(c))
	if b.Hovered() {
		b.Background(fadeColor(b, "surface-fill", c.Theme().SurfaceHover))
	}
	b.Children(func() {
		ui.Row(c).Gap(4).Children(func() {
			ui.Box(c).Padding(1, 4).Radius(6).Background(c.Theme().Surface.Mix(color, 0.18)).Children(func() { ui.Text(c, name).FontSize(fsTiny).Bold().TextColor(color) })
			ui.Text(c, value).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
		})
	})
	return b
}

// closePopups clears every popup flag whose key starts with prefix, so opening
// one row's menu closes the menu that was already open.
func (a *desktop) closePopups(prefix string) {
	for key, flag := range a.popupFlags {
		if strings.HasPrefix(key, prefix) {
			*flag = false
		}
	}
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

func fieldExample(id, name string) string {
	switch {
	case strings.Contains(id, "url"):
		return "https://api.example.com/v1"
	case strings.Contains(id, "key"):
		return "sk-…"
	case strings.Contains(id, "user"):
		return "root"
	case strings.Contains(id, "alias"):
		return "例如：开发服务器"
	case strings.Contains(id, "host"):
		return "192.168.1.10"
	case strings.Contains(id, "port"):
		return "22"
	case strings.Contains(id, "command"):
		return "npx -y @modelcontextprotocol/server-filesystem"
	case strings.Contains(id, "name"):
		return "例如：我的配置"
	case strings.Contains(id, "model"):
		return "provider/model-name"
	default:
		return "请输入" + name
	}
}
