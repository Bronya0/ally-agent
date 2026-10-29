package uikit

import (
	"math"

	"github.com/egoist/mygo/ui"
)

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
	// Gesture, if set, collects the press on a tab, so the strip around the
	// tabs can pan with it: MyGo's wheel and press only know the axes an
	// element scrolls, and a strip overflowing sideways has to be dragged
	// by its owner.
	Gesture *TabGesture
}

// tabPanSlop is how far a press moves before it pans the strip instead of
// choosing a tab, as MyGo's dragStart: a click that shakes still clicks.
const tabPanSlop = 4

// TabGesture is the press on a tab strip: how far it moved since the last
// frame, and whether it moved far enough to pan the strip rather than
// choose the tab it pressed. Its owner keeps one per strip, so a press
// reads across the frames it lasts.
type TabGesture struct {
	// DX and DY are how far the pointer moved, in DIPs, since the last
	// frame while a tab is pressed.
	DX, DY float32
	// Panning reports a press that moved far enough to pan the strip: the
	// tab it ends on is not chosen.
	Panning bool
	// Released reports, in the frame the panning press lets go, that the
	// tab under it reports a click the strip drops.
	Released bool
	moved    float32
	pressed  bool
}

// Watch takes a tab's press, if it has one, and keeps the gesture. A strip
// whose tabs are not uikit's own calls it for each.
func (g *TabGesture) Watch(tab ui.Element) {
	if dx, dy, ok := tab.Dragged(); ok {
		if !g.pressed {
			// A new press: what the last one moved says nothing of it.
			*g = TabGesture{pressed: true}
		}
		g.DX, g.DY = dx, dy
		g.moved += float32(math.Hypot(float64(dx), float64(dy)))
		if g.moved >= tabPanSlop {
			g.Panning = true
		}
		return
	}
	if _, _, ok := tab.DragEnded(); ok {
		// DragEnded reports the panning press once, where the tab also
		// reports the click it ends with.
		g.pressed, g.Released = false, g.Panning
		g.DX, g.DY = 0, 0
	}
}

func Tabs(c *ui.Context, selected *int, labels ...string) ui.Element {
	return TabsWithOptions(c, selected, TabOptions{Style: DefaultTabStyle}, labels...)
}

// TabsSecondary 显式传次要文字色：Theme.TextMuted 是占位淡色。
func TabsSecondary(c *ui.Context, secondary ui.Color, selected *int, o TabOptions, labels ...string) ui.Element {
	return tabsWithInk(c, selected, o, secondary, labels...)
}

func TabsWithOptions(c *ui.Context, selected *int, o TabOptions, labels ...string) ui.Element {
	return tabsWithInk(c, selected, o, SecondaryInkOf(c.Theme()), labels...)
}

func tabsWithInk(c *ui.Context, selected *int, o TabOptions, secondary ui.Color, labels ...string) ui.Element {
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
			if o.Gesture != nil {
				o.Gesture.Watch(tab)
			}
			fill, ink, edge := ui.Transparent, secondary, ui.Transparent
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
