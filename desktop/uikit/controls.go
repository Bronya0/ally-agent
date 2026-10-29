// Package uikit provides theme-driven native MyGo components, independent of Ally.
package uikit

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const ControlHeight float32 = 34
const Radius float32 = 8

// SelectedTint and SelectedStroke are the one selected look shared by every
// control that reports a choice: an option row, a tab, a chip. The tint has to
// read at a glance against a plain panel, and a selected control always carries
// accent ink (plus a check where there is room), so a choice never depends on
// the fill alone.
const (
	SelectedTint   float32 = 0.18
	SelectedStroke float32 = 0.72
)

// SelectedFill is the fill of a selected control, in its own accent.
func SelectedFill(accent ui.Color) ui.Color { return accent.Alpha(SelectedTint) }

// SelectedStrokeColor is the hairline of a selected control, in its own accent.
func SelectedStrokeColor(accent ui.Color) ui.Color { return accent.Alpha(SelectedStroke) }

// chromeFade is the shared hover and focus colour transition for form controls,
// matching the desktop's own widgets so nothing snaps when it lights up.
const chromeFade = 140 * time.Millisecond

var chevronDown = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16"><path d="m4.5 6 3.5 3.5L11.5 6" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
var eyeIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16"><path d="M1.3 8s2.3-4 6.7-4 6.7 4 6.7 4-2.3 4-6.7 4S1.3 8 1.3 8Z" fill="none" stroke="currentColor" stroke-width="1.2"/><circle cx="8" cy="8" r="2" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>`))
var eyeOffIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16"><path d="M1.3 8s2.3-4 6.7-4 6.7 4 6.7 4-2.3 4-6.7 4S1.3 8 1.3 8Z M2 2l12 12" fill="none" stroke="currentColor" stroke-width="1.2"/><circle cx="8" cy="8" r="2" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>`))

func PopupTransition() ui.ElementTransition {
	return ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{Y: 6, Scale: 0.985}, Exit: &ui.Motion{Y: 4, Scale: 0.985}}
}

// chromeHoverMix is the only place an edge is allowed to brighten: a hovered
// control leans this far toward the text colour. Resting edges stay on the theme
// border so no card or input reads as a bright stroke on a dark panel.
const chromeHoverMix float32 = 0.18

// hairline is the shared resting edge: the theme border itself, so panels,
// inputs, buttons and menus read as the same material instead of nine shades of
// grey — and none of them is brighter than the panel it sits on.
func hairline(t *ui.Theme) ui.Color { return t.Border }

func Popup(c *ui.Context, panel ui.Element) {
	panel.Padding(6).Gap(2).Radius(10).Border(1, hairline(c.Theme())).Background(c.Theme().Surface).
		Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.25)).Transition(PopupTransition()).MaxHeight(320)
}
func option(c *ui.Context, item ui.Element, text string, selected bool) {
	t := c.Theme()
	item.Height(34).Padding(5, 10).Radius(6).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).TextColor(t.Text)
	if item.Highlighted() || item.Hovered() {
		item.Background(t.SurfaceHover)
	}
	if selected {
		item.Background(SelectedFill(t.Accent))
	}
	item.Children(func() {
		label := text
		if label == "" {
			label = "自动"
		}
		ink := t.Text
		if selected {
			ink = t.Accent
		}
		choice := ui.Text(c, label).FontSize(14).SingleLine().Grow(1).Cursor(ui.CursorPointer).TextColor(ink)
		if selected {
			choice.FontWeight(600)
			ui.Text(c, "✓").FontSize(12).TextColor(t.Accent).Cursor(ui.CursorPointer)
		}
	})
}

// Option is one choice in a labelled select: Value is what the field writes,
// Label is what the user reads. They stay separate so a config value such as
// "recent" never leaks into the UI as if it were a label.
type Option struct {
	Value string
	Label string
}

// Select is a select whose options read exactly as their values.
func Select(c *ui.Context, value *string, options []string) ui.Element {
	list := make([]Option, 0, len(options))
	for _, text := range options {
		list = append(list, Option{Value: text, Label: text})
	}
	return SelectField(c, value, list)
}

// SelectField is a select with per-option labels.
func SelectField(c *ui.Context, value *string, options []Option) ui.Element {
	sel := ui.SelectBase(c, value)
	b := sel.Trigger.Height(ControlHeight).Padding(4, 10).Gap(8).AlignItems(ui.Center).Radius(Radius).Border(1, hairline(c.Theme())).Background(c.Theme().SurfaceHover).Cursor(ui.CursorPointer)
	b.Children(func() {
		label := *value
		for _, item := range options {
			if item.Value == *value {
				label = item.Label
				break
			}
		}
		if label == "" {
			label = "自动"
		}
		ui.Text(c, label).FontSize(14).SingleLine().Grow(1).MinWidth(0).Cursor(ui.CursorPointer)
		ui.Box(c).Size(18, 18).Shrink(0).Center().Children(func() {
			ui.Icon(c, chevronDown).Size(13, 13).TextColor(c.Theme().TextMuted)
		})
	})
	sel.Popup(func(panel ui.Element) {
		Popup(c, panel)
		ui.Scroll(c).MaxHeight(300).Children(func() {
			for _, item := range options {
				option(c, sel.Item(item.Value), item.Label, item.Value == *value)
			}
		})
	})
	return InputChrome(c, b)
}
func Combobox(c *ui.Context, value *string, options []string) ui.Element {
	return ComboboxField(c, value, options, "", "")
}

func ComboboxField(c *ui.Context, value *string, options []string, label, placeholder string) ui.Element {
	var cb ui.ComboboxParts
	root := ui.Row(c).FillWidth().Height(ControlHeight).AlignItems(ui.Center).Gap(0)
	root = InputChrome(c, root)
	root.Children(func() {
		cb = ui.ComboboxBase(c, value)
		cb.Input.Height(ControlHeight).FillHeight().Grow(1).MinWidth(0).Padding(5, 10).
			FontSize(14).Border(0, ui.Transparent).Background(ui.Transparent).Radius(0).TextColor(c.Theme().Text).
			Label(label).Placeholder(placeholder)
		toggle := ui.Box(c).Size(32, ControlHeight).Shrink(0).Center().Cursor(ui.CursorPointer).Label("展开选项").Tooltip("选择或输入")
		toggle.OnClick(func() {
			cb.Input.Focus()
			cb.SetOpen(true)
		})
		toggle.Children(func() {
			ui.Icon(c, chevronDown).Size(13, 13).TextColor(c.Theme().TextMuted)
		})
	})
	cb.Popup(func(panel ui.Element) {
		Popup(c, panel)
		ui.Scroll(c).MaxHeight(300).Children(func() {
			for _, text := range options {
				if !cb.Filtering() || strings.Contains(strings.ToLower(text), strings.ToLower(*value)) {
					option(c, cb.Item(text), text, text == *value)
				}
			}
		})
	})
	if text, ok := cb.Chosen(); ok {
		*value = text
		cb.Input.SetChanged()
	}
	return root
}

// TabRadius is the radius of one tab in a strip. The underline follows it, which
// is why the line draws in at both ends instead of running edge to edge.
const TabRadius float32 = 6

// TabInk is the label colour of one tab: the text colour when chosen, a shade
// darker than it otherwise. Weight, not colour, marks the chosen tab, so the
// accent appears once in the strip rather than twice.
func TabInk(t *ui.Theme, active bool) ui.Color {
	if active {
		return t.Text
	}
	return t.TextMuted.Mix(t.Text, 0.22)
}

// TabUnderline gives one tab the strip look the app uses everywhere: a rounded
// box whose bottom edge carries the accent, so the line curves away at both ends.
// No fill is involved: a fill behind the label stacked a third blue on the accent
// ink and the accent edge, and its rounded box fought the line where the two met.
//
// Size, content and hover stay with the caller: the settings strip holds a plain
// label, while the header's workspace strip also holds a running dot and a close
// button, and both wear this same chrome.
func TabUnderline(e ui.Element, active bool, radius float32, t *ui.Theme) ui.Element {
	e.Radius(radius).BorderWidth(0, 0, 2, 0)
	if active {
		e.BorderColor(t.Accent)
	} else {
		e.BorderColor(ui.Transparent)
	}
	return e
}

func Tabs(c *ui.Context, selected *int, labels ...string) ui.Element {
	tabs := ui.TabsBase(c, selected, len(labels))
	tabs.List.Gap(4)
	tabs.List.Children(func() {
		for n, label := range labels {
			active := n == *selected
			tab := tabs.Tab(n).Height(30).Padding(4, 10).Cursor(ui.CursorPointer)
			TabUnderline(tab, active, TabRadius, c.Theme())
			fill, textColor := ui.Transparent, TabInk(c.Theme(), active)
			if !active && tab.Hovered() {
				fill, textColor = c.Theme().SurfaceHover, c.Theme().Text
			}
			tab.Background(tab.AnimateColor("tab-fill", fill, ui.Animation{Duration: chromeFade, Ease: ui.EaseInOut}))
			edge := ui.Transparent
			if active {
				edge = c.Theme().Accent
			}
			tab.BorderColor(tab.AnimateColor("tab-border", edge, ui.Animation{Duration: chromeFade, Ease: ui.EaseInOut}))
			tab.TextColor(textColor)
			tab.Children(func() {
				text := ui.Text(c, label).FontSize(13).TextColor(textColor).SingleLine().Cursor(ui.CursorPointer)
				if active {
					text.FontWeight(600)
				}
			})
		}
	})
	return tabs.List
}

// Segmented keeps MyGo's radio-group, focus and keyboard behavior while giving
// the current choice a visible fill, outline and check, in every theme.
func Segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	parts := ui.SegmentedBase(c, selected, len(labels))
	t := c.Theme()
	el := parts.Track.Height(38).Padding(3).Gap(3).Radius(Radius).Background(t.Background).Border(1, t.Border).AlignItems(ui.Stretch).Cursor(ui.CursorPointer)
	if len(labels) > 0 {
		el.Label(labels[0])
	}
	el.Children(func() {
		for i, label := range labels {
			active := i == *selected
			segment := parts.Segment(i).Height(30).Padding(0, 12).Radius(6).Cursor(ui.CursorPointer).Label(label).Gap(6)
			fill, edge, ink := ui.Transparent, ui.Transparent, t.TextMuted
			if active {
				fill, edge, ink = t.Surface.Mix(t.Accent, 0.20), SelectedStrokeColor(t.Accent), t.Text
			} else if segment.Hovered() {
				fill, ink = t.SurfaceHover, t.Text
			}
			segment.Background(fill).Border(1, edge).TextColor(ink)
			segment.Children(func() {
				mark := ""
				if active {
					mark = "✓"
				}
				ui.Text(c, mark).Width(13).FontSize(12).TextColor(t.Accent).Unselectable().Cursor(ui.CursorPointer)
				text := ui.Text(c, label).FontSize(13).TextColor(ink).SingleLine().Unselectable().Cursor(ui.CursorPointer)
				if active {
					text.FontWeight(600)
				}
			})
		}
	})
	return el
}
func Tag(c *ui.Context, text string, fg, fill ui.Color) ui.Element {
	return ui.Badge(c, text).TextColor(fg).Background(fill)
}
func FieldLabel(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(FontSection).TextColor(c.Theme().Text)
}
func FieldNote(c *ui.Context, text string) {
	if text != "" {
		ui.Text(c, text).FontSize(12).TextColor(c.Theme().TextMuted).LineHeight(1.5)
	}
}
func TextField(c *ui.Context, value *string, placeholder string) ui.Element {
	return Input(c, value).FillWidth().FontSize(14).Placeholder(placeholder)
}
func TextArea(c *ui.Context, value *string, placeholder string, height float32) ui.Element {
	return Area(c, value).FillWidth().Shrink(0).FontSize(14).Placeholder(placeholder).Height(height)
}
func DialogPanel(c *ui.Context, panel ui.Element, width float32) {
	w, h := c.Size()
	panel.Width(min(max(width, w*0.55), w*0.92)).MaxWidth(max(200, w-24)).MaxHeight(max(180, h*0.9)).Padding(14).
		Background(c.Theme().Surface).Border(1, hairline(c.Theme())).Radius(8).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.25)).Transition(PopupTransition())
}

// InputChrome gives every form control a visible boundary and focus state.
func InputChrome(c *ui.Context, e ui.Element) ui.Element {
	border := hairline(c.Theme())
	background := c.Theme().SurfaceHover
	if e.Hovered() {
		border = c.Theme().Border.Mix(c.Theme().Text, chromeHoverMix)
	}
	if e.FocusWithin() || e.Focused() {
		border = c.Theme().Accent
	}
	border = e.AnimateColor("chrome-border", border, ui.Animation{Duration: chromeFade, Ease: ui.EaseInOut})
	return e.MinWidth(0).Border(1, border).Background(background).Radius(Radius).FontSize(14).TextColor(c.Theme().Text)
}
func Input(c *ui.Context, value *string) ui.Element {
	return InputChrome(c, ui.TextInput(c, value)).Height(ControlHeight)
}
func Area(c *ui.Context, value *string) ui.Element { return InputChrome(c, ui.TextArea(c, value)) }

// PasswordInput keeps the text in MyGo's input, with visibility local to this control.
func PasswordInput(c *ui.Context, key, label string, value *string, placeholder ...string) ui.Element {
	root := InputChrome(c, ui.Row(c.Key(key)).Height(ControlHeight).AlignItems(ui.Center).Gap(0))
	visible := ui.Local(root, "visible", func() bool { return false })
	root.Children(func() {
		input := ui.TextInputBase(c.Key("input"), value).Grow(1).MinWidth(0).FillHeight().Padding(5, 10).
			Border(0, ui.Transparent).Background(ui.Transparent).FontSize(14).TextColor(c.Theme().Text).Label(label)
		if len(placeholder) > 0 {
			input.Placeholder(placeholder[0])
		}
		if !*visible {
			input.Password()
		}
		action, icon := "显示 "+label, eyeIcon
		if *visible {
			action, icon = "隐藏 "+label, eyeOffIcon
		}
		button := ui.ButtonBase(c.Key("visibility")).Size(32, ControlHeight).Shrink(0).Center().Cursor(ui.CursorPointer).Label(action).Tooltip(action)
		button.Children(func() { ui.Icon(c, icon).Size(15, 15).TextColor(c.Theme().TextMuted) })
		if button.Clicked() {
			*visible = !*visible
		}
	})
	return root
}

type numberInputState struct {
	text string
	last float64
}

// MyGo v0.7.1 NumberInput fixes the inner input width and does not expose its
// parts. Compose its native text and button controls to fill available width.
func NumberInput(c *ui.Context, key, label string, value *float64, lo, hi, step float64) ui.Element {
	root := InputChrome(c, ui.Row(c.Key(key)).Height(ControlHeight).AlignItems(ui.Center).Gap(0))
	decimals := 0
	if s := strconv.FormatFloat(step, 'f', -1, 64); strings.Contains(s, ".") {
		decimals = len(s) - strings.IndexByte(s, '.') - 1
	}
	format := func(v float64) string { return strconv.FormatFloat(v, 'f', decimals, 64) }
	state := ui.Local(root, "number", func() numberInputState { return numberInputState{text: format(*value), last: *value} })
	if state.last != *value {
		state.last, state.text = *value, format(*value)
	}
	set := func(v float64) {
		v = math.Max(lo, math.Min(hi, v))
		p := math.Pow10(decimals)
		v = math.Round(v*p) / p
		if *value != v {
			*value = v
			root.SetChanged()
		}
		state.last, state.text = *value, format(*value)
	}
	root.Children(func() {
		input := ui.TextInputBase(c.Key("input"), &state.text).Grow(1).MinWidth(0).FillHeight().Padding(5, 10).
			Border(0, ui.Transparent).Background(ui.Transparent).FontSize(14).TextColor(c.Theme().Text).Label(label)
		if input.Changed() {
			if v, err := strconv.ParseFloat(strings.TrimSpace(state.text), 64); err == nil && !math.IsNaN(v) && v >= lo && v <= hi {
				if *value != v {
					*value = v
					state.last = v
					root.SetChanged()
				}
			}
		}
		if input.Shortcut(0, ui.KeyUp) {
			set(*value + step)
		}
		if input.Shortcut(0, ui.KeyDown) {
			set(*value - step)
		}
		if !input.Focused() {
			state.text = format(*value)
		}
		for _, item := range []struct {
			text, name string
			delta      float64
		}{{"−", "减少", -step}, {"+", "增加", step}} {
			item := item
			button := ui.ButtonBase(c.Key(item.name)).Size(24, 28).Shrink(0).Center().Radius(5).Cursor(ui.CursorPointer).
				Label(item.name + " " + label).Tooltip(item.name).Disabled(item.delta < 0 && *value <= lo || item.delta > 0 && *value >= hi)
			if button.Hovered() {
				button.Background(c.Theme().Surface)
			}
			button.Children(func() { ui.Text(c, item.text).FontSize(15).TextColor(c.Theme().TextMuted).Unselectable() })
			if button.Clicked() {
				set(*value + item.delta)
			}
		}
		ui.Box(c).Width(5).Shrink(0)
	})
	return root
}

// NumberStepper builds a filled numeric field with compact, borderless controls.
// The string binding lets callers keep invalid intermediate edits until save-time validation.
func NumberStepper(c *ui.Context, key, label string, value *string, lo, hi, step, fallback int) ui.Element {
	root := ui.Row(c).FillWidth().Height(ControlHeight).AlignItems(ui.Center).Gap(0)
	root = InputChrome(c, root)
	root.Children(func() {
		ui.TextInputBase(c.Key(key), value).Height(ControlHeight).FillHeight().Grow(1).MinWidth(0).
			Padding(5, 10).FontSize(14).Border(0, ui.Transparent).Background(ui.Transparent).Radius(0).
			TextColor(c.Theme().Text).Label(label)

		current, err := strconv.Atoi(strings.TrimSpace(*value))
		if err != nil || current < lo || current > hi {
			current = fallback
		}
		controls := ui.Row(c).Shrink(0).Gap(2).Padding(0, 5).AlignItems(ui.Center)
		controls.Children(func() {
			for _, control := range []struct {
				label string
				delta int
				name  string
			}{{"−", -step, "减少"}, {"+", step, "增加"}} {
				control := control
				next := current + control.delta
				disabled := next < lo || next > hi
				button := ui.Button(c, control.label).Size(26, 28).Padding(0).Border(0, ui.Transparent).
					Background(ui.Transparent).Radius(6).FontSize(15).TextColor(c.Theme().TextMuted).
					Disabled(disabled).Label(control.name)
				if button.Hovered() && !disabled {
					button.Background(c.Theme().Surface)
					button.TextColor(c.Theme().Text)
				}
				button.OnClick(func() {
					*value = strconv.Itoa(next)
				})
			}
		})
	})
	return root
}
