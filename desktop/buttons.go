// SPDX-License-Identifier: GPL-3.0-only
package desktop

// This file is the single source of truth for pressable surfaces. Pages call
// flat / primary / dangerButton / ghost / iconAction and never restyle
// ui.Button(Base) themselves, so height, radius, hairline border, hover, press
// and disabled treatment stay identical everywhere.

import (
	"ally-dev/desktop/uikit"

	"github.com/egoist/mygo/ui"
)

// buttonKind selects the visual weight of a button.
type buttonKind int

const (
	btnSecondary buttonKind = iota // surface fill + hairline border: 编辑 / 取消
	btnPrimary                     // accent fill: 保存 / 确认
	btnDanger                      // danger label with a tinted border: 删除
	btnGhost                       // chrome on hover only: composer tools
)

// Button metrics moved to the kit with the implementation; buttonKind stays here
// because the call sites read as words (flat / primary / dangerButton), not as
// the kit's variant constants.

// button builds every labelled button in the app.
func button(c *ui.Context, key any, label string, kind buttonKind, small bool, icon string) ui.Element {
	e, _ := buttonParts(c, key, label, kind, small, icon)
	return e
}

// iconImage 把图标名解析成可绘制的图像。uikit 不认识这个应用的图标表，
// 所以名字到图像的映射留在这里。
func iconImage(name string) *ui.SVG {
	if name == "" {
		return nil
	}
	if original := originalRailIcons[name]; original != nil {
		return original
	}
	return icons[name]
}

func uikitVariant(kind buttonKind) uikit.ButtonVariant {
	switch kind {
	case btnPrimary:
		return uikit.ButtonPrimary
	case btnDanger:
		return uikit.ButtonDanger
	case btnGhost:
		return uikit.ButtonGhost
	}
	return uikit.ButtonSecondary
}

// buttonParts builds that same button and also hands back its label, for the few
// callers whose label colour is not the button's own (the context meter turns
// amber and red as the model window fills).
func buttonParts(c *ui.Context, key any, label string, kind buttonKind, small bool, icon string) (ui.Element, ui.Element) {
	return uikit.Button(c, key, label, uikitVariant(kind), small, iconImage(icon))
}

// iconTone picks how loud a square, label-less button may get.
type iconTone int

const (
	iconQuiet    iconTone = iota // muted at rest, text on hover
	iconNormal                   // text at rest
	iconDanger                   // muted at rest, danger on hover
	iconAccent                   // half accent at rest, full accent on hover: a control whose meaning is the accent
	iconAccentOn                 // full accent at rest: the same control, engaged
)

// iconButtonSized is the only icon-button factory: same hover fill, radius and
// fade as `button`, at whatever square size the owner needs.
func iconButtonSized(c *ui.Context, id, tip, name string, size float32, tone iconTone, fn func()) ui.Element {
	return uikit.IconButton(c, id, tip, iconImage(name), size, uikitTone(tone), fn)
}

func uikitTone(tone iconTone) uikit.IconTone {
	switch tone {
	case iconNormal:
		return uikit.IconNormal
	case iconDanger:
		return uikit.IconDanger
	case iconAccent:
		return uikit.IconAccent
	case iconAccentOn:
		return uikit.IconAccentOn
	}
	return uikit.IconQuiet
}

// addButton is the app's one "add" affordance: the header's add-workspace, both
// rail adds, the composer's new session and the explorer's new file all call it,
// so "＋" looks the same anywhere. It is a bare glyph — no fill, edge, gradient or
// lift — because a chip painted onto the chrome outweighs an "add" and reads as a
// box around the icon; only the stroke carries the state, and it holds the full
// accent at rest (a washed-out tint disappeared next to the rest of the chrome)
// before brightening under the pointer.
func addButton(c *ui.Context, id, tip, name string, size float32, fn func()) ui.Element {
	t := c.Theme()
	b := uikit.Tooltip(c, ui.ButtonBase(c.Key(id)).Size(size, size).Center().Radius(radiusControl).
		Label(tip), tip).Cursor(ui.CursorPointer)
	// A nil fn leaves the click to Element.Menu, which opens the owner's own menu.
	if fn != nil {
		b.OnClick(fn)
	}
	ink := t.Accent
	switch {
	case b.IsDisabled():
		b.Opacity(0.45)
	case b.Hovered():
		ink = t.AccentHover
	}
	painted := fadeColor(b, "add-ink", ink)
	b.Background(ui.Transparent).TextColor(painted)
	return b.Children(func() { iconGlyph(c, name, size*0.6).TextColor(painted) })
}
