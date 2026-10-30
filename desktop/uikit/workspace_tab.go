package uikit

import "github.com/egoist/mygo/ui"

// WorkspaceTab is the quiet title-bar tab. Status and close slots are always
// present, so loading, hover and closing availability never move the label.
// tip is what hovering reveals: the label keeps only the workspace's last
// segment, so the tip carries the full path.
func WorkspaceTab(c *ui.Context, label, tip string, active, busy, canClose bool, closeIcon *ui.SVG, activate, close func(), gesture *TabGesture, secondary ...ui.Color) ui.Element {
	t := c.Theme()
	row := ui.ButtonBase(c).Height(44).Shrink(0).MinWidth(64).MaxWidth(220).Padding(0, 7).Gap(5).AlignItems(ui.Center).Radius(0).Cursor(ui.CursorPointer).Label(label).OnClick(activate)
	if gesture != nil {
		// The entire tab, including its status slot and padding, pans the strip.
		gesture.Watch(row)
	}
	fill := ui.Transparent
	if row.Hovered() {
		fill = t.SurfaceHover
	}
	row.Background(fade(row, "workspace-fill", fill))
	edge := ui.Transparent
	if active {
		edge = t.Accent
	}
	row.BorderWidth(0, 0, 2, 0).BorderColor(fade(row, "workspace-line", edge))
	row.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if !active {
			p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, t.Border, 0)
		}
	})
	showClose := row.Hovered() || row.FocusWithin()
	row.Children(func() {
		dot := ui.Transparent
		if busy {
			dot = t.Accent
		}
		ui.Box(c.Key("status")).Size(6, 6).Shrink(0).Radius(3).Background(fade(row, "workspace-status", dot))
		button := ui.Row(c.Key("title")).Grow(1).MinWidth(0).FillHeight().Center()
		ink := SecondaryInkOf(t, secondary...)
		if active || row.Hovered() {
			ink = t.Text
		}
		ink = fade(row, "workspace-ink", ink)

		button.Children(func() { ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(ink).SingleLine().Unselectable() })
		hover := tip
		if hover == "" {
			hover = label
		}
		if busy {
			// The path keeps its own line: a prefixed path wraps unreadably.
			hover = "运行中\n" + hover
		}
		Tooltip(c, row, hover)
		end := IconButton(c, "close", "关闭标签", closeIcon, 20, IconQuiet, close, SecondaryInkOf(t, secondary...)).Shrink(0).Disabled(!canClose || busy)
		opacity := float32(0)
		if canClose && showClose {
			opacity = 1
		}
		end.Opacity(end.Animate("visibility", opacity, HoverDuration))
	})
	return row
}
