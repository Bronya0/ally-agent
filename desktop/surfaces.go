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

// overlayPanel is the floating picker used by the composer and the model menu:
// same material as a card, plus the shadow and max height an overlay needs.
func overlayPanel(c *ui.Context, key any, width, height float32, build func()) ui.Element {
	e := ui.Column(c.Key(key)).FillWidth().Gap(gapTight).Radius(radiusCard).Clip()
	if width > 0 {
		e.Width(width)
	}
	if height > 0 {
		e.Height(height)
	}
	paintSurface(e, surfaceFill(c), surfaceBorder(c))
	e.Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.28))
	return e.Children(build)
}

// hoverRow paints the shared hover / selected tint on a row that already lays
// out its own content, without touching its padding or height.
func hoverRow(c *ui.Context, e ui.Element, selected bool) ui.Element {
	fill := ui.Transparent
	switch {
	case selected:
		fill = c.Theme().SurfaceHover
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
func pillColor(c *ui.Context, tone pillTone) ui.Color {
	switch tone {
	case pillAccent:
		return c.Theme().Accent
	case pillOK:
		return toneColor(c, "ok")
	case pillWarn:
		return toneColor(c, "warn")
	case pillDanger:
		return toneColor(c, "error")
	case pillInfo:
		return toneColor(c, "info")
	}
	return c.Theme().TextMuted
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
