package uikit

import "github.com/egoist/mygo/ui"

type tooltipIdentity struct{ marker byte }

// TooltipPlacement chooses the preferred side of an anchor. MyGo can flip
// the tooltip to the opposite side when there is insufficient space.
type TooltipPlacement uint8

const (
	TooltipTop TooltipPlacement = iota
	TooltipBottom
	TooltipLeft
	TooltipRight
)

// Tooltip adds the shared animated tooltip, returning its anchor for chaining.
func Tooltip(c *ui.Context, anchor ui.Element, text string) ui.Element {
	return TooltipWithPlacement(c, anchor, text, TooltipTop)
}

// RightTooltip keeps the existing right-side shortcut.
func RightTooltip(c *ui.Context, anchor ui.Element, text string) ui.Element {
	return TooltipWithPlacement(c, anchor, text, TooltipRight)
}

// TooltipWithPlacement shows a tooltip on the preferred side of anchor.
func TooltipWithPlacement(c *ui.Context, anchor ui.Element, text string, placement TooltipPlacement) ui.Element {
	tooltip(c, anchor, text, placement)
	return anchor
}
func tooltip(c *ui.Context, anchor ui.Element, text string, placement TooltipPlacement) ui.Element {
	if text == "" {
		return ui.Element{}
	}
	// Keep the native overlay parent mounted before the first tooltip appears.
	ui.Overlay(c, func() {})
	identity := ui.Local(anchor, "tooltip-identity", func() *tooltipIdentity { return &tooltipIdentity{marker: 1} })
	anchor.Description(text)
	return ui.TooltipBase(c, anchor, func(tip ui.Element) {
		fill, ink := ui.Hex("#27272b"), ui.Hex("#f4f4f5")
		if c.Theme().Dark {
			fill, ink = ui.Hex("#ededf0"), ui.Hex("#27272b")
		}
		tip.Key(*identity).Transition(ui.ElementTransition{Duration: OverlayDuration, Position: true, Enter: &ui.Motion{}, Exit: &ui.Motion{}}).
			MaxWidth(320).Padding(5, 9).Radius(4).Background(fill).TextColor(ink).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.18))
		switch placement {
		case TooltipRight:
			tip.AttachTo(anchor, ui.AnchorRight, ui.AnchorLeft).Margin(0, 0, 0, 8)
		case TooltipLeft:
			tip.AttachTo(anchor, ui.AnchorLeft, ui.AnchorRight).Margin(0, 8, 0, 0)
		case TooltipBottom:
			tip.AttachTo(anchor, ui.AnchorBottom, ui.AnchorTop).Margin(6, 0, 0, 0)
		default:
			tip.AttachTo(anchor, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, 6, 0)
		}
		tip.Children(func() {
			ui.Text(c, text).FontSize(FontTiny).TextColor(ink).LineHeight(1.4).Unselectable().PassThrough()
		})
	})
}
