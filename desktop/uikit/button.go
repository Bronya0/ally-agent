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
	// ButtonSecondary 兼容旧调用：中性次次要按钮。
	ButtonSecondary ButtonVariant = iota
	// ButtonPrimary 使用主题色次要按钮。
	ButtonPrimary
	// ButtonDanger 使用危险色次要按钮。
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
	if key != nil {
		c = c.Key(key)
	}
	o := ActionOptions{Style: Tertiary, Small: small, Icon: icon}
	switch variant {
	case ButtonPrimary:
		o.Style = Secondary
		o.Tone = Primary
	case ButtonDanger:
		o.Style = Secondary
		o.Tone = Error
	case ButtonGhost:
		o.Style = Tertiary
	}
	b, text := actionParts(c, label, o)
	if variant == ButtonGhost {
		b.Background(ui.Transparent).Border(0, ui.Transparent)
	}
	return b, text
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
func IconButton(c *ui.Context, id, tip string, icon *ui.SVG, size float32, tone IconTone, fn func(), ink ...ui.Color) ui.Element {
	t := c.Theme()
	muted := SecondaryInk(c, ink)
	color, hoverColor := muted, t.Text
	fill, hoverFill := ui.Transparent, t.SurfaceHover
	switch tone {
	case IconNormal:
		color, hoverColor = t.Text, t.Text
	case IconDanger:
		color, hoverColor = muted, t.Danger
		hoverFill = t.Danger.Alpha(0.14)
	case IconAccent:
		color, hoverColor, hoverFill = t.Accent.Alpha(0.55), t.Accent, ui.Transparent
	case IconAccentOn:
		color, hoverColor, hoverFill = t.Accent, t.AccentHover, ui.Transparent
	}
	b := Tooltip(c, ui.ButtonBase(c.Key(id)).Size(size, size).Center().Radius(RadiusSmall).Label(tip), tip).Cursor(ui.CursorPointer).OnClick(fn)
	if !b.IsDisabled() && b.Hovered() {
		color = hoverColor
		fill = hoverFill
	}
	color = fade(b, "label", color)
	b.Background(fade(b, "fill", fill)).TextColor(color)
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
