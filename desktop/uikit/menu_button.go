package uikit

import "github.com/egoist/mygo/ui"

// MenuOptions styles a MenuBase popup without replacing its input, focus,
// scrolling or submenu behavior. Zero anchors keep MyGo's default placement.
type MenuOptions struct {
	Width, MaxHeight, MaxHeightPercent float32
	At, Self                           ui.Anchor
	Gap                                float32
	Label                              string
}

// MenuButton owns an optional local open flag on its stable wrapper. The
// callbacks style MyGo's trigger and build its items; neither may toggle the
// trigger with OnClick. Returned elements/parts are build-local, never retained.
func MenuButton(c *ui.Context, open *bool, o MenuOptions, trigger func(ui.Element), build func(ui.MenuParts)) ui.Element {
	// Inherit the parent's cross-axis alignment. Forcing Start here lifts
	// menu triggers above their neighbors in header and composer toolbars.
	root := ui.Row(c).MinWidth(0).Shrink(0)
	if open == nil {
		open = ui.Local(root, "menu-open", func() bool { return false })
	}
	root.Children(func() {
		m := ui.MenuBase(c.Key("trigger"), open)
		trigger(m.Trigger)
		// MenuBase reads Disabled before the caller styles its trigger. Close
		// against the final state too, especially when a running task locks it.
		if m.Trigger.IsDisabled() {
			*open = false
		}
		m.Popup(func(panel ui.Element) {
			MenuPanel(c, panel, o)
			panel.Transition(PopupTransition())
			if o.At != 0 || o.Self != 0 {
				panel.AttachTo(m.Trigger, o.At, o.Self)
			}
			if o.Gap != 0 {
				if o.At == ui.AnchorTopLeft || o.At == ui.AnchorTopRight || o.At == ui.AnchorTopStart || o.At == ui.AnchorTopEnd {
					panel.Margin(0, 0, o.Gap, 0)
				} else {
					panel.Margin(o.Gap, 0, 0, 0)
				}
			}
			build(m)
		})
	})
	return root
}

// MenuPanel is the shared chrome of root menus, submenus and information
// popovers. MenuBase supplies the popup's scrolling and window-edge flipping.
func MenuPanel(c *ui.Context, panel ui.Element, o MenuOptions) {
	height := o.MaxHeight
	if height == 0 {
		height = 420
	}
	panel.Width(o.Width).MaxWidthPercent(90).MaxHeight(height).Padding(5).
		Background(c.Theme().Surface).Border(1, c.Theme().Border).Radius(RadiusCard)
	if o.MaxHeightPercent > 0 {
		panel.MaxHeightPercent(o.MaxHeightPercent)
	}
	if o.Label != "" {
		panel.Label(o.Label)
	}
}

// MenuRow styles a MyGo menu item, leaving its layout content to the caller.
// Selection is a subtle fill and accent ink, never an extra leading check.
func MenuRow(c *ui.Context, item ui.Element, label string, selected bool) ui.Element {
	fill := ui.Transparent
	if item.Highlighted() || item.Hovered() {
		fill = c.Theme().SurfaceHover
	}
	if selected {
		fill = SelectedFill(c.Theme().Accent)
	}
	return item.FillWidth().MinHeight(32).Padding(GapInline, PadCard).
		Justify(ui.Start).AlignItems(ui.Center).Radius(RadiusSmall).
		Label(label).Cursor(ui.CursorPointer).Background(fill)
}

type MenuItemOptions struct {
	Choice, Selected, Danger bool
	Subtitle, Trailing       string
	Icon                     *ui.SVG
	FontSize                 float32
}

// MenuItem uses MenuBase's item identity, roles, navigation and activation.
// Only the label, optional icon and supporting text are drawn by the kit.
func MenuItem(c *ui.Context, m ui.MenuParts, key any, label string, o MenuItemOptions, ink ...ui.Color) ui.Element {
	c.Key(key)
	var item ui.Element
	if o.Choice {
		item = m.RadioItem(o.Selected)
	} else {
		item = m.Item()
	}
	return MenuItemOn(c, item, label, o, ink...)
}

// MenuItemOn shares row content with buttons in non-menu information popovers.
func MenuItemOn(c *ui.Context, item ui.Element, label string, o MenuItemOptions, ink ...ui.Color) ui.Element {
	MenuRow(c, item, label, o.Selected).Gap(GapField)
	secondary := SecondaryInk(c, ink)
	fontSize := o.FontSize
	if fontSize == 0 {
		fontSize = FontBody
	}
	item.Description(joinMenuDescription(o.Subtitle, o.Trailing))
	item.Children(func() {
		if o.Icon != nil {
			ui.Icon(c, o.Icon).Size(15, 15).Shrink(0).TextColor(secondary)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			text := ui.Text(c, label).FontSize(fontSize).SingleLine()
			if o.Danger {
				text.TextColor(c.Theme().Danger)
			} else if o.Selected {
				text.TextColor(c.Theme().Accent).FontWeight(600)
			}
			if o.Subtitle != "" {
				ui.Text(c, o.Subtitle).FontSize(FontTiny).TextColor(secondary).SingleLine()
			}
		})
		if o.Trailing != "" {
			ui.Text(c, o.Trailing).FontSize(FontTiny).TextColor(secondary).SingleLine().Shrink(0)
		}
	})
	return item
}

func joinMenuDescription(subtitle, trailing string) string {
	if subtitle == "" {
		return trailing
	}
	if trailing == "" {
		return subtitle
	}
	return subtitle + " · " + trailing
}
