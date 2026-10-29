package uikit

import "github.com/egoist/mygo/ui"

// TabStyle selects the two approved navigation treatments.
type TabStyle uint8

const (
	TabLine      TabStyle = iota // A: straight underline; secondary level is text only.
	TabSegmented                 // C: compact track; secondary level uses soft independent fills.
)
const DefaultTabStyle = TabSegmented

type TabOptions struct {
	Style     TabStyle
	Secondary bool
}

func Tabs(c *ui.Context, selected *int, labels ...string) ui.Element {
	return TabsWithOptions(c, selected, TabOptions{Style: DefaultTabStyle}, labels...)
}

func TabsWithOptions(c *ui.Context, selected *int, o TabOptions, labels ...string) ui.Element {
	parts := ui.TabsBase(c, selected, len(labels))
	t := c.Theme()
	track := parts.List.AlignSelf(ui.Start).Shrink(0)
	if o.Style == TabSegmented && !o.Secondary {
		track.Padding(3).Gap(2).Radius(7).Border(1, t.Border).Background(t.SurfaceHover)
	} else if o.Style == TabLine && !o.Secondary {
		track.Gap(14).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	} else {
		track.Gap(4)
	}
	track.Children(func() {
		for i, label := range labels {
			active := i == *selected
			tab := parts.Tab(i).Shrink(0).Height(28).Padding(0, 12).Cursor(ui.CursorPointer).Label(label)
			fill, ink, edge := ui.Transparent, t.TextMuted, ui.Transparent
			if o.Style == TabSegmented {
				tab.Radius(4)
				if active {
					fill = SelectedFill(t.Accent)
					ink = t.Accent
				}
				if !active && tab.Hovered() {
					fill = t.SurfaceHover
					ink = t.Text
				}
			} else {
				tab.Radius(0).Height(36).Padding(0, 4)
				if o.Secondary {
					tab.Height(28).Padding(0, 3)
				} else {
					tab.BorderWidth(0, 0, 2, 0)
				}
				if active {
					ink = t.Accent
					if !o.Secondary {
						edge = t.Accent
					}
				} else if tab.Hovered() {
					ink = t.Text
				}
				tab.BorderColor(fade(tab, "tab-edge", edge))
			}
			ink = fade(tab, "tab-ink", ink)
			tab.Background(fade(tab, "tab-fill", fill)).TextColor(ink)
			// Constant weight avoids labels changing width while navigating.
			tab.Children(func() {
				ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(ink).SingleLine().Unselectable().Cursor(ui.CursorPointer)
			})
		}
	})
	return track
}
