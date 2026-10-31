package uikit

import "github.com/egoist/mygo/ui"

// WorkspaceTab is the quiet title-bar tab. Status and close slots are always
// present, so loading, hover and closing availability never move the label.
// tip is what hovering reveals: the label keeps only the workspace's last
// segment, so the tip carries the full path.
func WorkspaceTab(c *ui.Context, label, tip string, active, busy, canClose bool, closeIcon *ui.SVG, activate, close func(), gesture *TabGesture, secondary ...ui.Color) ui.Element {
	t := c.Theme()
	row := ui.ButtonBase(c).Height(44).Shrink(0).MinWidth(64).MaxWidth(220).Padding(0, 12).Gap(5).AlignItems(ui.Center).Radius(0).Cursor(ui.CursorPointer).Label(label).OnClick(activate)
	if gesture != nil {
		// The entire tab, including its status slot and padding, pans the strip.
		gesture.Watch(row)
	}
	fill := ui.Transparent
	if row.Hovered() {
		// A browser tab hover is stronger than the surrounding frame tint.
		// Use an opaque theme-derived shade so light mode cannot fade to a
		// paler surface than the title bar itself.
		fill = t.Surface.Mix(t.Accent, 0.28)
		if t.Dark {
			fill = t.Surface.Mix(t.Accent, 0.22)
		}
	}
	if active {
		fill = ui.Transparent
	}
	fill = fade(row, "workspace-fill", fill)
	// Selection changes the silhouette immediately, so its fill must change
	// in the same frame. Only the detached hover background should fade.
	if active {
		fill = t.Background
	}
	row.Background(ui.Transparent).Border(0, ui.Transparent).Draw(func(p *ui.Painter, r ui.Rect) {
		if active {
			paintWorkspaceTab(p, r, fill)
		} else {
			p.Fill(ui.Rect{X: r.X + 4, Y: r.Y + 6, W: r.W - 8, H: r.H - 12}, fill, 8)
			p.Fill(ui.Rect{X: r.X + r.W - 1, Y: r.Y + 14, W: 1, H: 16}, t.Border, 0)
		}
	})
	showClose := active || row.Hovered() || row.FocusWithin()
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

// Keep the top corners and the outward feet inside the tab's own bounds so
// neighboring tabs never paint over its curves or intercept their input.
func paintWorkspaceTab(p *ui.Painter, r ui.Rect, fill ui.Color) {
	const k float32 = 0.55228475
	const foot float32 = 6
	const corner float32 = 10
	x, right, top, bottom := r.X, r.X+r.W, r.Y+6, r.Y+r.H
	var path ui.Path
	path.MoveTo(x, bottom).
		CubeTo(x+k*foot, bottom, x+foot, bottom-foot+k*foot, x+foot, bottom-foot).
		LineTo(x+foot, top+corner).
		CubeTo(x+foot, top+corner-k*corner, x+foot+corner-k*corner, top, x+foot+corner, top).
		LineTo(right-foot-corner, top).
		CubeTo(right-foot-corner+k*corner, top, right-foot, top+corner-k*corner, right-foot, top+corner).
		LineTo(right-foot, bottom-foot).
		CubeTo(right-foot, bottom-foot+k*foot, right-k*foot, bottom, right, bottom).
		Close()
	p.FillPath(&path, fill)
}
