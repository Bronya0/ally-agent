package uikit

import "github.com/egoist/mygo/ui"

type tooltipIdentity struct{ marker byte }

// Tooltip adds the shared animated tooltip, returning its anchor for chaining.
func Tooltip(c *ui.Context, anchor ui.Element, text string) ui.Element {
	tooltip(c, anchor, text, false)
	return anchor
}
func RightTooltip(c *ui.Context, anchor ui.Element, text string) ui.Element {
	return tooltip(c, anchor, text, true)
}
func tooltip(c *ui.Context, anchor ui.Element, text string, right bool) ui.Element {
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
		if right {
			tip.AttachTo(anchor, ui.AnchorRight, ui.AnchorLeft).Margin(0, 0, 0, 8)
		} else {
			tip.AttachTo(anchor, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, 6, 0)
		}
		tip.Children(func() { ui.Text(c, text).FontSize(12).TextColor(ink).LineHeight(1.4).Unselectable().PassThrough() })
	})
}
