// SPDX-License-Identifier: GPL-3.0-only
package desktop

// This file owns non-pressable chrome: panels, list rows, badges and banners.
// Pages compose these instead of writing Background / Border / Radius by hand,
// so every surface shares one hairline, one radius family and one hover fade —
// and everything here is a thin layer over a MyGo component, not a re-draw.

import (
	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo/ui"
)

// surfaceBorder is the shared resting edge for panels, cards and chips: the same
// line colour the dividers use, so every surface is one material and no edge is
// brighter than the panel it sits on.
func surfaceBorder(c *ui.Context) ui.Color {
	return c.Theme().Border
}
func surfaceFill(c *ui.Context) ui.Color { return c.Theme().Surface }

// paintSurface applies the shared fill + hairline, fading both so a hover never
// snaps. Distinct animation keys keep the two properties independent.
func paintSurface(e ui.Element, fill, border ui.Color) ui.Element {
	return e.Background(fadeColor(e, "surface-fill", fill)).Border(1, fadeColor(e, "surface-border", border))
}

// surfacePanel is the shared card: panel fill, hairline border, card radius.
func surfacePanel(c *ui.Context, key any, padding, radius float32, build func()) ui.Element {
	e := ui.Column(c.Key(key)).Padding(padding).Gap(gapField).Radius(radius)
	paintSurface(e, surfaceFill(c), surfaceBorder(c))
	return e.Children(build)
}

// Composer pickers share native dismissal and float above the input without
// changing transcript layout. The whole composer anchor includes its trigger.
func composerPopover(c *ui.Context, anchor ui.Element, open *bool, label string, height float32, build func(ui.Element)) {
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		width := chatMaxWidth - 40
		bounds := anchor.Bounds()
		if bounds.W > 40 {
			width = min(width, bounds.W-40)
		}
		popoverChrome(c, panel, width)
		panel.Column().Label(label).Padding(0).Gap(gapTight).Height(height).MaxHeightPercent(85).Clip().KeepFocus().
			AttachTo(anchor, ui.AnchorTopLeft, ui.AnchorBottomLeft).Left(20).Margin(0, 0, 6, 0).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.28)).Transition(uikit.PopupTransition())
		// The header stays fixed while the list scrolls within available space.
		if bounds.Y >= 110 {
			panel.MaxHeight(bounds.Y - 10)
		} else {
			_, windowHeight := c.Size()
			below := windowHeight - bounds.Y - bounds.H - 10
			if below >= 110 {
				panel.AttachTo(anchor, ui.AnchorBottomLeft, ui.AnchorTopLeft).Margin(6, 0, 0, 0).MaxHeight(below)
			} else {
				panel.AttachTo(anchor, ui.AnchorBottomLeft, ui.AnchorBottomLeft).Margin(0)
			}
		}
		build(panel)
	})
}

// hoverRow paints the shared hover / selected tint on a row that already lays
// out its own content, without touching its padding or height. A selected row
// takes the accent tint the other one-of-N lists use: on the panel fill the
// hover tint is barely a shade lighter, which read as "nothing is selected" in
// a picker the arrow keys drive.
func hoverRow(c *ui.Context, e ui.Element, selected bool) ui.Element {
	fill := ui.Transparent
	switch {
	case selected:
		fill = selectionFill(c.Theme().Accent)
	case e.Hovered():
		fill = c.Theme().SurfaceHover
	}
	return e.Background(fadeColor(e, "row-fill", fill))
}

// ---- Badges --------------------------------------------------------------

// pillTone is the semantic colour of a badge or status pill.
type pillTone int

const (
	pillNeutral pillTone = iota
	pillAccent
	pillOK
	pillWarn
	pillDanger
	pillInfo
)

// pillColor resolves a tone against the active theme.
func pillColor(c *ui.Context, tone pillTone, ink ...ui.Color) ui.Color {
	switch tone {
	case pillAccent:
		return c.Theme().Accent
	case pillOK:
		return toneColor(c, "ok", ink...)
	case pillWarn:
		return toneColor(c, "warn", ink...)
	case pillDanger:
		return toneColor(c, "error", ink...)
	case pillInfo:
		return toneColor(c, "info", ink...)
	}
	return uikit.SecondaryInk(c, ink)
}

// pill is the single badge: a capsule tinted from its tone, so statuses read
// the same on every page.
func pill(c *ui.Context, text string, tone pillTone) ui.Element {
	semantic := uikit.Default
	switch tone {
	case pillAccent:
		semantic = uikit.Primary
	case pillOK:
		semantic = uikit.Success
	case pillWarn:
		semantic = uikit.Warning
	case pillDanger:
		semantic = uikit.Error
	case pillInfo:
		semantic = uikit.Info
	}
	return uikit.ColorTag(c, text, uikit.TagOptions{Tone: semantic, Small: true, Round: true, Borderless: true})
}

// pillDot is the tiny state dot used next to a pill or a row label.
func pillDot(c *ui.Context, tone pillTone) ui.Element {
	return ui.Box(c).Size(7, 7).Shrink(0).Radius(4).Background(pillDotColor(c, tone))
}

// Indicators are small solid marks, not text: keep light-mode fills clear
// instead of reusing the deeper ink required for readable status labels.
func pillDotColor(c *ui.Context, tone pillTone) ui.Color {
	if c.Theme().Dark {
		return pillColor(c, tone)
	}
	switch tone {
	case pillOK:
		return ui.Hex("#25a66b")
	case pillWarn:
		return ui.Hex("#dfa029")
	case pillDanger:
		return ui.Hex("#df5656")
	case pillAccent, pillInfo:
		return c.Theme().Accent
	default:
		return ui.Hex("#aab0bb")
	}
}
