package uikit

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Tone is shared by actions, tags, selection and messages.
type Tone uint8

const (
	Default Tone = iota
	Primary
	Info
	Success
	Warning
	Error
)

func ToneColor(c *ui.Context, tone Tone) ui.Color {
	return ToneColorForTheme(c.Theme(), tone)
}

func ToneColorForTheme(t *ui.Theme, tone Tone) ui.Color {
	switch tone {
	case Primary:
		return t.Accent
	case Info:
		if t.Dark {
			return ui.Hex("#8da8f0")
		}
		return ui.Hex("#4869c8")
	case Success:
		if t.Dark {
			return ui.Hex("#72c9a5")
		}
		return ui.Hex("#127b56")
	case Warning:
		if t.Dark {
			return ui.Hex("#dfb36d")
		}
		return ui.Hex("#9b6218")
	case Error:
		if t.Dark {
			return ui.Hex("#ed96ab")
		}
		return ui.Hex("#be405f")
	}
	return t.Text
}

type ActionStyle uint8

const (
	Secondary ActionStyle = iota
	Tertiary
	Ghost
	Quaternary // Text at rest, soft fill on hover or selection.
)

type ActionOptions struct {
	Tone                                    Tone
	Style                                   ActionStyle
	Small, Round, Circle, Disabled, Loading bool
	Selected                                bool
	Icon                                    *ui.SVG
}

// Action uses ButtonBase for activation, keyboard and accessibility.
func Action(c *ui.Context, label string, o ActionOptions) ui.Element {
	b, _ := actionParts(c, label, o)
	return b
}
func actionParts(c *ui.Context, label string, o ActionOptions) (ui.Element, ui.Element) {
	b := ui.ButtonBase(c).Shrink(0).Label(label).Disabled(o.Disabled || o.Loading).Cursor(ui.CursorPointer)
	h := float32(34)
	if o.Small {
		h = 28
	}
	r := float32(3)
	if o.Round || o.Circle {
		r = h / 2
	}
	ink := ToneColor(c, o.Tone)
	fill := ink.Alpha(0.13)
	edge := ui.Transparent
	if o.Tone == Default {
		fill = neutralActionFill(c.Theme())
	}
	if o.Style == Tertiary {
		fill = neutralActionFill(c.Theme())
		if o.Tone == Default {
			ink = c.Theme().Text
			if c.Theme().Dark {
				ink = SecondaryInkOf(c.Theme()).Mix(c.Theme().Text, 0.55)
			}
		}
	}
	if o.Style == Ghost {
		fill = ui.Transparent
		edge = ink.Alpha(0.65)
		if o.Tone == Default {
			edge = c.Theme().Border
		}
	}
	if o.Style == Quaternary {
		fill = ui.Transparent
		edge = ui.Transparent
	}
	if o.Selected {
		fill = ink.Alpha(0.14)
	}
	if !o.Disabled && !o.Loading {
		if b.Hovered() {
			fill = ink.Alpha(0.18)
		}
		if b.Pressed() {
			fill = ink.Alpha(0.24)
		}
	}
	ink = fade(b, "action-ink", ink)
	b.Height(h).Padding(0, 14).Gap(7).Radius(r).Border(1, edge).Background(fade(b, "action-fill", fill)).TextColor(ink)
	if o.Circle {
		b.Width(h).Padding(0)
	}
	var text ui.Element
	b.Children(func() {
		if o.Loading {
			ui.Spinner(c).Size(14, 14).TextColor(ink)
		} else if o.Icon != nil {
			ui.Icon(c, o.Icon).Size(16, 16).TextColor(ink)
		}
		if label != "" && !o.Circle {
			text = ui.Text(c, label).FontSize(14).TextColor(ink).SingleLine().Unselectable()
		}
	})
	return b, text
}

type TagOptions struct {
	Tone                        Tone
	Round, Borderless, Disabled bool
	Small                       bool
	Compact                     bool
	Icon                        *ui.SVG
	OnClose                     func()
}

func ColorTag(c *ui.Context, label string, o TagOptions) ui.Element {
	ink := ToneColor(c, o.Tone)
	h := float32(26)
	if o.Small {
		h = 22
	}
	if o.Compact {
		h = 18
	}
	r := float32(3)
	if o.Round {
		r = h / 2
	}
	fill, edge := ink.Alpha(0.10), ink.Alpha(0.25)
	if o.Tone == Default {
		fill = c.Theme().Text.Alpha(0.04)
		edge = c.Theme().Border
	}
	return colorTag(c, label, o, ink, fill, edge, h, r)
}
func colorTag(c *ui.Context, label string, o TagOptions, ink, fill, edge ui.Color, h, r float32) ui.Element {
	pad, font := float32(7), float32(13)
	if o.Compact {
		pad, font = 5, 11
	}
	e := ui.Row(c).Height(h).Padding(0, pad).Gap(5).AlignItems(ui.Center).Radius(r).Background(fill).Disabled(o.Disabled)
	if !o.Borderless {
		e.Border(1, edge)
	}
	return e.Children(func() {
		if o.Icon != nil {
			ui.Icon(c, o.Icon).Size(14, 14).TextColor(ink)
		}
		ui.Text(c, label).FontSize(font).TextColor(ink).SingleLine()
		if o.OnClose != nil {
			ui.ButtonBase(c.Key("close")).Size(16, 16).Label("移除 " + label).Cursor(ui.CursorPointer).OnClick(o.OnClose).Children(func() { ui.Text(c, "×").TextColor(ink) })
		}
	})
}

// RadioOptions supports native radios or a joined button group.
type RadioOption[T comparable] struct {
	Value    T
	Label    string
	Disabled bool
}

func Radios[T comparable](c *ui.Context, selected *T, options []RadioOption[T], buttons bool) ui.Element {
	group := ui.RadioGroup(c, func() {
		for i, o := range options {
			active := *selected == o.Value
			b := ui.RadioBase(c.Key(i), selected, o.Value).Disabled(o.Disabled).Label(o.Label).Cursor(ui.CursorPointer).AlignItems(ui.Center)
			ink := c.Theme().Text
			if buttons {
				edge := c.Theme().Border
				if active {
					ink = c.Theme().Accent
					edge = ink
				} else if b.Hovered() {
					ink = c.Theme().Accent
				}
				ink = fade(b, "radio-ink", ink)
				b.Height(34).Padding(0, 14).Border(1, fade(b, "radio-edge", edge)).Radius(0).Background(ui.Transparent)
				b.Children(func() { ui.Text(c, o.Label).FontSize(14).TextColor(ink).SingleLine().Unselectable() })
			} else {
				b.Gap(8).Height(28)
				edge := c.Theme().Border
				if active || b.Hovered() {
					edge = c.Theme().Accent
				}
				b.Children(func() {
					ui.Box(c).Size(16, 16).Shrink(0).Radius(8).Border(1, fade(b, "radio-edge", edge)).Center().Children(func() {
						if active {
							ui.Box(c).Size(8, 8).Radius(4).Background(c.Theme().Accent)
						}
					})
					ui.Text(c, o.Label).FontSize(14).TextColor(ink).Unselectable()
				})
			}
		}
	}).Row().Wrap()
	if buttons {
		group.Gap(-1)
	} else {
		group.Gap(16)
	}
	return group
}

// SwitchOptions customizes the native switch without replacing its input or animation.
type SwitchOptions struct {
	Small, Square, Disabled, Loading bool
	Tone                             Tone
}

func SwitchWithOptions(c *ui.Context, on *bool, label string, o SwitchOptions) ui.Element {
	w, h := float32(40), float32(22)
	if o.Small {
		w, h = 32, 18
	}
	radius := h / 2
	if o.Square {
		radius = 3
	}
	e := ui.Switch(c, on).Shrink(0).Size(w, h).Radius(radius).Label(label).Cursor(ui.CursorPointer).Disabled(o.Disabled || o.Loading)
	if o.Tone != Default && *on {
		e.Background(fade(e, "switch-tone", ToneColor(c, o.Tone)))
	}
	if o.Loading {
		e.Center().Children(func() { ui.Spinner(c).Size(12, 12).TextColor(c.Theme().Text) })
	}
	return e
}
func Switch(c *ui.Context, on *bool, label string) ui.Element {
	return SwitchWithOptions(c, on, label, SwitchOptions{})
}
func Message(c *ui.Context, tone Tone, text string, timeout time.Duration) string {
	kinds := []string{"info", "info", "info", "success", "warning", "error"}
	kind := "info"
	if int(tone) < len(kinds) {
		kind = kinds[tone]
	}
	return c.AddToast(ui.Toast{Title: text, Type: kind, Timeout: timeout})
}

// Messages installs one native toast viewport per window. MyGo owns timeouts,
// hover pause, dismissal and accessible announcements.
func Messages(c *ui.Context) ui.Element {
	w, _ := c.Size()
	return ui.ToastViewportBase(c, func(v ui.Element, items []ui.Toast) {
		v.Padding(16).Justify(ui.Start).AlignItems(ui.Center).Gap(10)
		for _, item := range items {
			tone := Info
			mark := "i"
			switch item.Type {
			case "success":
				tone = Success
				mark = "✓"
			case "error":
				tone = Error
				mark = "×"
			case "warning":
				tone = Warning
				mark = "!"
			}
			toast := ui.ToastBase(c, item)
			toast.Root.Transition(messageTransition()).Row().AlignSelf(ui.Center).MaxWidth(max(float32(100), w-32)).Padding(12, 18).Gap(10).AlignItems(ui.Center).Radius(4).Background(c.Theme().Surface).Shadow(0, 4, 18, 0, ui.RGBA(0, 0, 0, 0.18))
			toast.Root.Children(func() {
				if item.Type == "loading" {
					ui.Spinner(c).Size(16, 16).TextColor(c.Theme().Accent)
				} else {
					ui.Box(c).Size(18, 18).Shrink(0).Radius(9).Center().Background(ToneColor(c, tone)).Children(func() { ui.Text(c, mark).FontSize(12).TextColor(ui.Hex("#ffffff")) })
				}
				ui.Text(c, item.Title).MinWidth(0).FontSize(14).TextColor(c.Theme().Text).MaxWidth(max(float32(60), w-110))
				if item.Action != "" {
					toast.ActionButton().Children(func() { ui.Text(c, item.Action).TextColor(c.Theme().Accent) })
				}
			})
		}
	})
}

// SwitchNative is the compatibility entry for fluent call sites naming Label later.
func SwitchNative(c *ui.Context, on *bool) ui.Element { return Switch(c, on, "") }

// Choice is a callback-driven option used for model fields represented by strings.
func Choice(c *ui.Context, text string, selected bool, tone ui.Color, swatch bool) ui.Element {
	b := ui.ButtonBase(c).Height(28).Padding(3, 9).Radius(6).
		Label(text).Cursor(ui.CursorPointer)
	ink := OptionChrome(c, b, selected, tone)
	return b.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if swatch {
				ui.Box(c).Size(9, 9).Shrink(0).Radius(5).Background(tone)
			}
			glyph := ui.Text(c, text).FontSize(13).TextColor(ink).SingleLine()
			if selected {
				glyph.FontWeight(600)
			}
		})
	})
}

// The native toast viewport stays mounted when empty, allowing the last toast
// to finish its exit snapshot without retaining app-owned elements or timers.
func messageTransition() ui.ElementTransition {
	return ui.ElementTransition{Duration: OverlayDuration, Ease: ui.EaseInOut, Position: true, Enter: &ui.Motion{Y: -4}, Exit: &ui.Motion{Y: -4}}
}

func neutralActionFill(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.Text.Alpha(0.085)
	}
	return t.Text.Alpha(0.055)
}
