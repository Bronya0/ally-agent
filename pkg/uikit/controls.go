// Package uikit provides theme-driven native MyGo components, independent of Ally.
package uikit

import (
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const ControlHeight float32 = 34
const Radius float32 = 8

// chromeFade is the shared hover and focus colour transition for form controls,
// matching the desktop's own widgets so nothing snaps when it lights up.
const chromeFade = 140 * time.Millisecond

var chevronDown = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16"><path d="m4.5 6 3.5 3.5L11.5 6" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>`))

func PopupTransition() ui.ElementTransition {
	return ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{Y: 6, Scale: 0.985}, Exit: &ui.Motion{Y: 4, Scale: 0.985}}
}
func Popup(c *ui.Context, panel ui.Element) {
	panel.Padding(6).Gap(2).Radius(10).Border(1, c.Theme().Border.Mix(c.Theme().Text, 0.08)).Background(c.Theme().Surface).
		Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.25)).Transition(PopupTransition()).MaxHeight(320)
}
func Button(c *ui.Context, text string) ui.Element {
	b := ui.Button(c, text).Border(1, c.Theme().Border.Mix(c.Theme().Text, 0.2)).Height(ControlHeight).Padding(4, 10).Radius(Radius).FontSize(13).Cursor(ui.CursorPointer)
	fill := ui.Transparent
	if b.Hovered() {
		fill = c.Theme().SurfaceHover
	}
	b.Background(b.AnimateColor("fill", fill, ui.Animation{Duration: chromeFade, Ease: ui.EaseInOut}))
	return b
}
func option(c *ui.Context, item ui.Element, text string, selected bool) {
	item.Height(34).Padding(5, 10).Radius(6).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).TextColor(c.Theme().Text)
	if item.Highlighted() || item.Hovered() {
		item.Background(c.Theme().SurfaceHover)
	}
	if selected {
		item.Background(c.Theme().SurfaceHover).TextColor(c.Theme().Accent)
	}
	item.Children(func() {
		label := text
		if label == "" {
			label = "自动"
		}
		ui.Text(c, label).FontSize(14).SingleLine().Grow(1).Cursor(ui.CursorPointer)
		if selected {
			ui.Text(c, "✓").FontSize(12).TextColor(c.Theme().Accent).Cursor(ui.CursorPointer)
		}
	})
}
func Select(c *ui.Context, value *string, options []string) ui.Element {
	sel := ui.SelectBase(c, value)
	b := sel.Trigger.Height(ControlHeight).Padding(4, 10).Gap(8).AlignItems(ui.Center).Radius(Radius).Border(1, c.Theme().Border).Background(c.Theme().SurfaceHover).Cursor(ui.CursorPointer)
	b.Children(func() {
		label := *value
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
			for _, text := range options {
				option(c, sel.Item(text), text, text == *value)
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
func Tabs(c *ui.Context, selected *int, labels ...string) ui.Element {
	tabs := ui.TabsBase(c, selected, len(labels))
	tabs.List.Gap(4)
	tabs.List.Children(func() {
		for n, label := range labels {
			inactiveText := c.Theme().TextMuted.Mix(c.Theme().Text, 0.22)
			tab := tabs.Tab(n).Height(30).Padding(4, 10).Radius(4).BorderWidth(0, 0, 2, 0).BorderColor(ui.Transparent).Cursor(ui.CursorPointer).TextColor(inactiveText)
			if n == *selected {
				tab.Background(c.Theme().Accent.Alpha(0.12)).TextColor(c.Theme().Accent).BorderWidth(0, 0, 2, 0).BorderColor(c.Theme().Accent)
			} else if tab.Hovered() {
				tab.Background(c.Theme().SurfaceHover)
			}
			textColor := inactiveText
			if n == *selected {
				textColor = c.Theme().Accent
			}
			tab.Children(func() { ui.Text(c, label).FontSize(13).TextColor(textColor).SingleLine().Cursor(ui.CursorPointer) })
		}
	})
	return tabs.List
}
func Segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	return Tabs(c, selected, labels...)
}
func Card(c *ui.Context) ui.Element {
	return ui.Column(c).Padding(10).Gap(8).Border(1, c.Theme().Border).Background(c.Theme().Surface).Radius(8)
}

func Tag(c *ui.Context, text string, fg, fill ui.Color) ui.Element {
	return ui.Box(c).Padding(1, 7).Radius(9).Background(fill).Children(func() { ui.Text(c, text).FontSize(12).TextColor(fg).NoWrap() })
}
func FieldLabel(c *ui.Context, text string) { ui.Text(c, text).FontSize(13).TextColor(c.Theme().Text) }
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
		Background(c.Theme().Surface).Border(1, c.Theme().Border).Radius(8).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.25)).Transition(PopupTransition())
}

// InputChrome gives every form control a visible boundary and focus state.
func InputChrome(c *ui.Context, e ui.Element) ui.Element {
	border := c.Theme().Border
	background := c.Theme().SurfaceHover
	if e.Hovered() {
		border = c.Theme().Border.Mix(c.Theme().Text, 0.28)
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
func NumberInput(c *ui.Context, key, label string, value *float64, lo, hi, step float64) ui.Element {
	return InputChrome(c, ui.NumberInput(c.Key(key), value, lo, hi, step).Label(label).Height(ControlHeight))
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
