// SPDX-License-Identifier: GPL-3.0-only
package desktop

// This file owns navigation chrome: title-bar controls, sidebar entries and
// workspace tabs. They all share one active treatment (accent tint + accent
// ink), one hover tint and the same fade, so a page never assembles its own
// selected state.

import (
	"ally-dev/desktop/uikit"

	"github.com/egoist/mygo/ui"
)

// headerTabRadius is the radius of a workspace tab in the header. It is larger
// than the settings strip's because the tab is taller: the same proportion of
// the height keeps the underline's curve looking alike in both places.
const headerTabRadius float32 = 8

// windowCloseHover is the platform-standard red for the close control.
var windowCloseHover = ui.Hex("#e81123")

// windowButton is a caption-bar control (minimize / maximize / close).
func windowButton(c *ui.Context, id, tip, icon string, close bool, action func()) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(id)).Size(46, 44).Radius(0).Center().Label(tip).Tooltip(tip).
		Cursor(ui.CursorPointer).OnClick(action)
	fill, color := ui.Transparent, t.TextMuted
	if b.Hovered() {
		fill, color = t.SurfaceHover, t.Text
		if close {
			fill, color = windowCloseHover, ui.Hex("#ffffff")
		}
	}
	b.Background(fadeColor(b, "fill", fill)).TextColor(color)
	return b.Children(func() { iconGlyph(c, icon, 17).TextColor(color) })
}

// navItem is one sidebar entry: a 34px row when the rail is expanded, a square
// badge when it is collapsed. Active entries get the accent tint and ink.
func navItem(c *ui.Context, id, label, iconName string, expanded, active bool, trailing func()) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(id)).Height(heightRailItem).Justify(ui.Start).
		Radius(radiusControl).Label(label).Cursor(ui.CursorPointer)
	if expanded {
		b.FillWidth()
	} else {
		// A square badge, not a row: the collapsed rail is as wide as the entry is
		// tall, so an active entry reads as a square. Sizing both sides from one
		// token keeps it that way whatever the rail width becomes.
		b.Size(heightRailItemCollapsed, heightRailItemCollapsed).Center().Tooltip(label)
	}
	fill, color := ui.Transparent, t.TextMuted
	switch {
	case active:
		fill, color = selectionFill(t.Accent), t.Accent
	case b.Hovered():
		fill, color = t.SurfaceHover, t.Text
	}
	b.Background(fadeColor(b, "fill", fill)).TextColor(color)
	return b.Children(func() {
		if !expanded {
			ui.Row(c).Size(sizeRailBadge, sizeRailBadge).Shrink(0).Center().Radius(radiusRailBadge).Children(func() {
				railIcon(c, iconName, sizeRailIconCollapsed).TextColor(color)
			})
			return
		}
		ui.Row(c).FillWidth().Padding(0, 8).Gap(10).AlignItems(ui.Center).Children(func() {
			railIcon(c, iconName, sizeRailIcon).TextColor(color)
			text := ui.Text(c, label).FontSize(12.5).TextColor(color).SingleLine()
			if active {
				text.FontWeight(600)
			}
			if trailing != nil {
				trailing()
			}
		})
	})
}

// tabButton is one workspace tab; the active underline stays with the row that
// owns the tab strip, this only paints the label and its hover tint.
func tabButton(c *ui.Context, key any, label string, active bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Grow(1).Height(40).Padding(0, 2).Radius(radiusSmall).
		Label(label).Cursor(ui.CursorPointer)
	// The resting label matches the settings strip's unselected tab, and the
	// active one gains weight instead of tint: the accent already sits in the
	// underline, so colouring the text too would say the same thing twice.
	fill, color := ui.Transparent, uikit.TabInk(t, active)
	if active {
		color = t.Text
	} else if b.Hovered() {
		fill, color = t.SurfaceHover, t.Text
	}
	b.Background(fadeColor(b, "fill", fill)).TextColor(color)
	return b.Children(func() {
		text := ui.Text(c, label).FontSize(12.5).TextColor(color).SingleLine()
		if active {
			text.FontWeight(600)
		}
	})
}
