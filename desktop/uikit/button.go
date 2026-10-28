package uikit

// 按钮层：应用里所有可点的方块都从这里出。
//
// 一个按钮的样子只在三种状态下变化——静止、悬停、按下，加一个禁用态，其它
// 什么都不做。调用点只说明"这是什么语气"，不碰颜色和间距，这样换主题或调整
// 描边只需要改这一处。

import "github.com/egoist/mygo/ui"

// ButtonVariant 是按钮的语气。
type ButtonVariant int

const (
	// ButtonSecondary 是次要操作：面板底色 + 一条细边。
	ButtonSecondary ButtonVariant = iota
	// ButtonPrimary 是主操作：主题色描边，底色几乎透明。
	ButtonPrimary
	// ButtonDanger 是危险操作：红色字，红色淡边。
	ButtonDanger
	// ButtonGhost 平时只有字，悬停才浮出底色。
	ButtonGhost
)

// ButtonHeight 对齐输入框高度，ButtonHeightSmall 用于列表行和工具栏。
const (
	ButtonHeight      float32 = ControlHeight
	ButtonHeightSmall float32 = 28
	ButtonFont        float32 = 13
	ButtonFontSmall   float32 = 12
	ButtonPadX        float32 = 12
	ButtonPadXSmall   float32 = 8
)

// 主按钮是描边而非实心，重量就落在文字上：这些把主题色往正文色提一档，
// 深浅两套主题下对比度都对（往白色混在浅色主题上会糊）。
const (
	primaryInkMix      float32 = 0.14
	primaryInkHoverMix float32 = 0.30
)

// Button 是带字按钮的唯一工厂。第二个返回值是标签元素，给那些标签颜色不
// 走按钮本身配色的调用点（上下文用量条会随窗口填充度转黄、转红）。
//
// 图标可为 nil；有图标时画在标签前面，跟着文字一起变色。
func Button(c *ui.Context, key any, label string, variant ButtonVariant, small bool, icon *ui.SVG) (ui.Element, ui.Element) {
	t := c.Theme()
	height, font, padX := ButtonHeight, ButtonFont, ButtonPadX
	if small {
		height, font, padX = ButtonHeightSmall, ButtonFontSmall, ButtonPadXSmall
	}
	ctx := c
	if key != nil {
		ctx = c.Key(key)
	}
	b := ui.ButtonBase(ctx).Height(height).Padding(0, padX).Radius(Radius).Gap(GapInline).
		Center().Label(label).Cursor(ui.CursorPointer)
	fill, border, fg := t.Surface, t.Border, t.Text
	hoverFill, hoverBorder, hoverFG := t.SurfaceHover, t.Border.Mix(t.Text, chromeHoverMix), fg
	pressedFill := t.SurfacePressed
	switch variant {
	case ButtonPrimary:
		fill, border = t.Accent.Alpha(0.07), t.Accent.Alpha(0.55)
		fg = t.Accent.Mix(t.Text, primaryInkMix)
		hoverFill, hoverBorder = t.Accent.Alpha(0.16), t.Accent
		hoverFG = t.Accent.Mix(t.Text, primaryInkHoverMix)
		pressedFill = t.Accent.Alpha(0.22)
	case ButtonDanger:
		fg, hoverFG = t.Danger, t.Danger
		border, hoverBorder = t.Danger.Alpha(0.42), t.Danger.Alpha(0.62)
		hoverFill, pressedFill = t.Danger.Alpha(0.12), t.Danger.Alpha(0.18)
	case ButtonGhost:
		fill, border, fg = ui.Transparent, ui.Transparent, t.TextMuted
		hoverFill, hoverBorder, hoverFG = t.SurfaceHover, ui.Transparent, t.Text
	}
	switch {
	case b.IsDisabled():
		b.Opacity(0.45)
	case b.Pressed():
		fill = pressedFill
	case b.Hovered():
		fill, border, fg = hoverFill, hoverBorder, hoverFG
	}
	fg = fade(b, "label", fg)
	b.Border(1, fade(b, "border", border)).Background(fade(b, "fill", fill)).TextColor(fg)
	var labelEl ui.Element
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(font+3, font+3).TextColor(fg)
		}
		labelEl = ui.Text(c, label).FontSize(font).TextColor(fg).SingleLine()
	})
	return b, labelEl
}

// IconTone 决定一个无字方块按钮能有多响。
type IconTone int

const (
	// IconQuiet 静止时淡，悬停转正文色。
	IconQuiet IconTone = iota
	// IconNormal 静止就是正文色。
	IconNormal
	// IconDanger 静止淡，悬停转红。
	IconDanger
	// IconAccent 静止半主题色，悬停满主题色。
	IconAccent
	// IconAccentOn 静止就是满主题色：同一个控件的"已开启"。
	IconAccentOn
)

// IconButton 是唯一无字方块按钮工厂：和 Button 同一套圆角、悬停渐变和
// 图标缩放比例，尺寸由调用方定。
func IconButton(c *ui.Context, id, tip string, icon *ui.SVG, size float32, tone IconTone, fn func()) ui.Element {
	t := c.Theme()
	color, hoverColor := t.TextMuted, t.Text
	fill, hoverFill := ui.Transparent, t.SurfaceHover
	switch tone {
	case IconNormal:
		color, hoverColor = t.Text, t.Text
	case IconDanger:
		color, hoverColor = t.TextMuted, t.Danger
		hoverFill = t.Danger.Alpha(0.14)
	case IconAccent:
		color, hoverColor, hoverFill = t.Accent.Alpha(0.55), t.Accent, ui.Transparent
	case IconAccentOn:
		color, hoverColor, hoverFill = t.Accent, t.AccentHover, ui.Transparent
	}
	b := ui.ButtonBase(c.Key(id)).Size(size, size).Center().Radius(RadiusSmall).Label(tip).
		Tooltip(tip).Cursor(ui.CursorPointer).OnClick(fn)
	if !b.IsDisabled() && b.Hovered() {
		color = hoverColor
		fill = hoverFill
	}
	b.Background(fade(b, "fill", fill)).TextColor(fade(b, "label", color))
	if icon != nil {
		b.Children(func() {
			e := ui.Icon(c, icon)
			if e.Valid() {
				e.Size(size*0.62, size*0.62)
			}
			e.TextColor(color)
		})
	}
	return b
}
