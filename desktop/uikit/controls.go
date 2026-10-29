// Package uikit provides theme-driven native MyGo components, independent of Ally.
package uikit

import (
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

const ControlHeight float32 = 34
const Radius float32 = 8

// SelectedTint is shared by option rows and choice controls. SelectedStroke
// remains available for surfaces that need an explicit accent boundary.
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
const chromeFade = HoverDuration

var chevronDown = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16"><path d="m4.5 6 3.5 3.5L11.5 6" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
var eyeIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16"><path d="M1.3 8s2.3-4 6.7-4 6.7 4 6.7 4-2.3 4-6.7 4S1.3 8 1.3 8Z" fill="none" stroke="currentColor" stroke-width="1.2"/><circle cx="8" cy="8" r="2" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>`))
var eyeOffIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16"><path d="M1.3 8s2.3-4 6.7-4 6.7 4 6.7 4-2.3 4-6.7 4S1.3 8 1.3 8Z M2 2l12 12" fill="none" stroke="currentColor" stroke-width="1.2"/><circle cx="8" cy="8" r="2" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>`))

func PopupTransition() ui.ElementTransition {
	return ui.ElementTransition{Duration: OverlayDuration, Enter: &ui.Motion{Y: 6, Scale: 0.985}, Exit: &ui.Motion{Y: 4, Scale: 0.985}}
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
func option(c *ui.Context, item ui.Element, text string, selected bool, ink ...ui.Color) {
	t := c.Theme()
	fill := ui.Transparent
	item.Height(34).Padding(5, 10).Radius(6).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).TextColor(SecondaryInk(c, ink))
	if item.Highlighted() || item.Hovered() {
		fill = t.SurfaceHover
	}
	if selected {
		fill = SelectedFill(t.Accent)
	}
	item.Background(fade(item, "option-fill", fill))
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
func Select(c *ui.Context, value *string, options []string, ink ...ui.Color) ui.Element {
	list := make([]Option, 0, len(options))
	for _, text := range options {
		list = append(list, Option{Value: text, Label: text})
	}
	return SelectField(c, value, list, ink...)
}

// SelectField is a select with per-option labels.
func SelectField(c *ui.Context, value *string, options []Option, ink ...ui.Color) ui.Element {
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
			ui.Icon(c, chevronDown).Size(13, 13).TextColor(SecondaryInk(c, ink))
		})
	})
	selectPopup(c, sel, value, options, ink...)
	return InputChrome(c, b)
}

// CompactSelect keeps the toolbar trigger quiet while sharing the form select's
// popup and option styling. Locked controls show the same value and geometry,
// but use an inert button instead of a select so neither pointer nor keyboard
// input can open a popup. Separate keyed children discard an open select when
// locking, rather than restoring its popup after the operation finishes.
func CompactSelect(c *ui.Context, value *string, options []string, locked bool, ink ...ui.Color) ui.Element {
	var sel ui.SelectParts[string]
	var b ui.Element
	ui.Row(c).MinWidth(0).Shrink(1).Children(func() {
		if locked {
			b = ui.ButtonBase(c.Key("locked"))
		} else {
			sel = ui.SelectBase(c.Key("select"), value)
			b = sel.Trigger
		}
		color := SecondaryInk(c, ink)
		b.Height(ButtonHeightSmall).Padding(1, 6).MinWidth(0).Shrink(1).Gap(7).
			Radius(Radius).Background(ui.Transparent).TextColor(color).Label(*value).
			Cursor(ui.CursorPointer)
		if locked {
			b.Cursor(ui.CursorDefault)
		}
		b.Children(func() {
			ui.Text(c, *value).FontSize(FontBody).SingleLine().Unselectable().TextColor(color)
			ui.Icon(c, chevronDown).Size(13, 13).Shrink(0).TextColor(color)
		})
	})
	if !locked {
		list := make([]Option, 0, len(options))
		for _, text := range options {
			list = append(list, Option{Value: text, Label: text})
		}
		if panel := selectPopup(c, sel, value, list, ink...); panel.Valid() {
			panel.MinWidth(160).MaxWidthPercent(90)
		}
	}
	return b
}

func selectPopup(c *ui.Context, sel ui.SelectParts[string], value *string, options []Option, ink ...ui.Color) ui.Element {
	return sel.Popup(func(panel ui.Element) {
		Popup(c, panel)
		ui.Scroll(c).MaxHeight(300).Children(func() {
			for _, item := range options {
				option(c, sel.Item(item.Value), item.Label, item.Value == *value, SecondaryInk(c, ink))
			}
		})
	})
}

func Combobox(c *ui.Context, value *string, options []string, ink ...ui.Color) ui.Element {
	return ComboboxField(c, value, options, "", "", ink...)
}

func ComboboxField(c *ui.Context, value *string, options []string, label, placeholder string, ink ...ui.Color) ui.Element {
	var cb ui.ComboboxParts
	root := ui.Row(c).FillWidth().Height(ControlHeight).AlignItems(ui.Center).Gap(0)
	root = InputChrome(c, root)
	root.Children(func() {
		cb = ui.ComboboxBase(c, value)
		cb.Input.Height(ControlHeight).FillHeight().Grow(1).MinWidth(0).Padding(5, 10).
			FontSize(14).Border(0, ui.Transparent).Background(ui.Transparent).Radius(0).TextColor(c.Theme().Text).
			Label(label).Placeholder(placeholder)
		toggle := Tooltip(c, ui.Box(c).Size(32, ControlHeight).Shrink(0).Center().Cursor(ui.CursorPointer).Label("展开选项"), "选择或输入")
		toggle.OnClick(func() {
			cb.Input.Focus()
			cb.SetOpen(true)
		})
		toggle.Children(func() {
			ui.Icon(c, chevronDown).Size(13, 13).TextColor(SecondaryInk(c, ink))
		})
	})
	cb.Popup(func(panel ui.Element) {
		Popup(c, panel)
		ui.Scroll(c).MaxHeight(300).Children(func() {
			for _, text := range options {
				if !cb.Filtering() || strings.Contains(strings.ToLower(text), strings.ToLower(*value)) {
					option(c, cb.Item(text), text, text == *value, SecondaryInk(c, ink))
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
// accent appears once in the strip rather than twice. secondary 传调用方的次要
// 文字色（Theme.TextMuted 是占位淡色）。
func TabInk(t *ui.Theme, active bool, secondary ...ui.Color) ui.Color {
	if active {
		return t.Text
	}
	return SecondaryInkOf(t, secondary...).Mix(t.Text, 0.22)
}

// TabUnderline is the legacy workspace-header treatment. Page navigation uses
// TabsWithOptions in tabs.go; its straight underline never inherits this radius.
func TabUnderline(e ui.Element, active bool, radius float32, t *ui.Theme) ui.Element {
	e.Radius(radius).BorderWidth(0, 0, 2, 0)
	if active {
		e.BorderColor(t.Accent)
	} else {
		e.BorderColor(ui.Transparent)
	}
	return e
}

// OptionGroup is the compact neutral track shared by groups of choices.
func OptionGroup(c *ui.Context) ui.Element {
	return ui.Row(c).AlignSelf(ui.Start).MaxWidthPercent(100).Wrap().Padding(3).Gap(3).
		Radius(Radius).Background(c.Theme().SurfaceHover.Alpha(0.45)).Border(1, c.Theme().Border)
}

// OptionChrome distinguishes selection with a soft fill and stronger ink;
// keyboard focus keeps the native focus ring rather than a permanent outline.
func OptionChrome(c *ui.Context, e ui.Element, selected bool, tone ui.Color, secondary ...ui.Color) ui.Color {
	t := c.Theme()
	fill, ink := ui.Transparent, SecondaryInkOf(t, secondary...)
	if selected {
		fill, ink = SelectedFill(tone), tone.Mix(t.Text, 0.20)
	} else if e.Hovered() {
		fill, ink = t.SurfaceHover, t.Text
	}
	e.Height(28).Padding(0, 12).Radius(6).Border(0, ui.Transparent).Cursor(ui.CursorPointer).
		Background(e.AnimateColor("option-fill", fill, ui.Animation{Duration: chromeFade, Ease: ui.EaseInOut})).TextColor(ink)
	return fade(e, "option-ink", ink)
}

// Segmented keeps MyGo's radio-group, focus and keyboard behavior.
func Segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	return SegmentedInk(c, selected, nil, labels...)
}

// SegmentedInk is Segmented with the caller's secondary-text tone.
func SegmentedInk(c *ui.Context, selected *int, ink []ui.Color, labels ...string) ui.Element {
	parts := ui.SegmentedBase(c, selected, len(labels))
	t := c.Theme()
	el := parts.Track.Height(36).AlignSelf(ui.Start).Padding(3).Gap(3).Radius(Radius).
		Background(t.SurfaceHover.Alpha(0.45)).Border(1, t.Border).AlignItems(ui.Stretch).Cursor(ui.CursorPointer)
	if len(labels) > 0 {
		el.Label(labels[0])
	}
	el.Children(func() {
		for i, label := range labels {
			active := i == *selected
			segment := parts.Segment(i).MinWidth(62).Label(label)
			optionInk := OptionChrome(c, segment, active, t.Accent, ink...)
			segment.Children(func() {
				text := ui.Text(c, label).FontSize(13).TextColor(optionInk).SingleLine().Unselectable().Cursor(ui.CursorPointer)
				if active {
					text.FontWeight(600)
				}
			})
		}
	})
	return el
}
func Tag(c *ui.Context, text string, fg, fill ui.Color) ui.Element {
	return colorTag(c, text, TagOptions{Small: true}, fg, fill, fg.Alpha(0.22), 22, 3)
}
func FieldLabel(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(FontSection).TextColor(c.Theme().Text)
}
func FieldNote(c *ui.Context, text string, ink ...ui.Color) {
	if text != "" {
		ui.Text(c, text).FontSize(12).TextColor(SecondaryInk(c, ink)).LineHeight(1.5)
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
	return e.MinWidth(0).Border(1, border).Background(fade(e, "chrome-fill", background)).Radius(Radius).FontSize(14).TextColor(c.Theme().Text)
}
func Input(c *ui.Context, value *string) ui.Element {
	return InputChrome(c, ui.TextInput(c, value)).Height(ControlHeight)
}
func Area(c *ui.Context, value *string) ui.Element { return InputChrome(c, ui.TextArea(c, value)) }

// PasswordInput keeps the text in MyGo's input, with visibility local to this control.
func PasswordInput(c *ui.Context, key, label string, value *string, placeholder ...string) ui.Element {
	return passwordInputInk(c, key, label, value, nil, placeholder...)
}

// PasswordInputInk is PasswordInput with the caller's secondary-text tone.
func PasswordInputInk(c *ui.Context, key, label string, value *string, ink ui.Color, placeholder ...string) ui.Element {
	return passwordInputInk(c, key, label, value, []ui.Color{ink}, placeholder...)
}

func passwordInputInk(c *ui.Context, key, label string, value *string, tone []ui.Color, placeholder ...string) ui.Element {
	ink := SecondaryInk(c, tone)
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
		button := Tooltip(c, ui.ButtonBase(c.Key("visibility")).Size(32, ControlHeight).Shrink(0).Center().Cursor(ui.CursorPointer).Label(action), action)
		button.Children(func() { ui.Icon(c, icon).Size(15, 15).TextColor(ink) })
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
func NumberInput(c *ui.Context, key, label string, value *float64, lo, hi, step float64, ink ...ui.Color) ui.Element {
	secondary := SecondaryInk(c, ink)
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
			button := Tooltip(c, ui.ButtonBase(c.Key(item.name)).Size(24, 28).Shrink(0).Center().Radius(5).Cursor(ui.CursorPointer).
				Label(item.name+" "+label), item.name).Disabled(item.delta < 0 && *value <= lo || item.delta > 0 && *value >= hi)
			fill := ui.Transparent
			if button.Hovered() {
				fill = c.Theme().Surface
			}
			button.Background(fade(button, "step-fill", fill))
			button.Children(func() { ui.Text(c, item.text).FontSize(15).TextColor(secondary).Unselectable() })
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
func NumberStepper(c *ui.Context, key, label string, value *string, lo, hi, step, fallback int, ink ...ui.Color) ui.Element {
	secondary := SecondaryInk(c, ink)
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
				button := Action(c, control.label, ActionOptions{Style: Tertiary, Small: true}).Size(26, 28).Padding(0).Border(0, ui.Transparent).
					Background(ui.Transparent).Radius(6).FontSize(15).TextColor(secondary).
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
