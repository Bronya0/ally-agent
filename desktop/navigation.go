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

// windowCloseHover is the platform-standard red for the close control.
var windowCloseHover = ui.Hex("#e81123")

// windowButton is a caption-bar control (minimize / maximize / close).
func windowButton(c *ui.Context, id, tip, icon string, close bool, action func(), ink ...ui.Color) ui.Element {
	t := c.Theme()
	b := uikit.Tooltip(c, ui.ButtonBase(c.Key(id)).Width(46).FillHeight().MinHeight(headerBarHeight).Shrink(0).Padding(0).Border(0, ui.Transparent).Radius(0).Center().Label(tip), tip).
		Cursor(ui.CursorPointer).OnClick(action)
	// Caption controls must always paint an opaque surface. A transparent
	// resting target can expose stale/native backing pixels during hover fades.
	fill, color := t.Surface, uikit.SecondaryInk(c, ink)
	if b.Hovered() {
		fill, color = t.SurfaceHover, t.Text
		if close {
			fill, color = windowCloseHover, ui.Hex("#ffffff")
		}
	}
	color = fadeColor(b, "caption-ink", color)
	b.Background(fadeColor(b, "fill", fill)).TextColor(color).DrawOver(func(p *ui.Painter, r ui.Rect) {
		p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, t.Border, 0)
	})
	return b.Children(func() { iconGlyph(c, icon, 17).TextColor(color) })
}

// navItem is one sidebar entry: a 34px row when the rail is expanded, a square
// badge when it is collapsed. Active entries get the accent tint and ink.
func navItem(c *ui.Context, id, label, iconName string, expanded, active bool, trailing func(), ink ...ui.Color) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(id)).Height(heightRailItem).Justify(ui.Start).
		Radius(radiusControl).Label(label).Cursor(ui.CursorPointer)
	if expanded {
		b.FillWidth()
	} else {
		// A square badge, not a row: the collapsed rail is as wide as the entry is
		// tall, so an active entry reads as a square. Sizing both sides from one
		// token keeps it that way whatever the rail width becomes.
		b.Size(heightRailItemCollapsed, heightRailItemCollapsed).Center()
		uikit.RightTooltip(c, b, label)
	}
	fill, color := ui.Transparent, uikit.SecondaryInk(c, ink)
	switch {
	case active:
		fill, color = t.Accent.Alpha(0.09), t.Accent
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
		ui.Row(c).FillWidth().Padding(0, railContentInset(c)).Gap(10).AlignItems(ui.Center).Children(func() {
			railIcon(c, iconName, sizeRailIcon).TextColor(color)
			text := ui.Text(c, label).FontSize(12.5).TextColor(color).SingleLine()
			if active {
				text.FontWeight(600)
			}
			if trailing != nil {
				text.Grow(1).MinWidth(0)
				trailing()
			}
		})
	})
}

// Wider rails give their content breathing room without pinning it to the edge.
func railContentInset(c *ui.Context) float32 {
	width := ui.Local(c.Root(), "rail-content-width", func() float32 { return 200 })
	return railInsetForWidth(*width)
}

func railInsetForWidth(width float32) float32 {
	return 8 + max(float32(0), min(float32(24), (width-200)*0.15))
}
