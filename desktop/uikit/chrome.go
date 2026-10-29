package uikit

// 这一层是应用里所有非交互的骨架：卡片、分隔线、提示条、空状态、统计块。
//
// 它们只吃 ui.Theme 和调用方给的尺寸，不碰任何业务状态，所以任何页面
// （以及别的 Go 项目）都能直接用。尺寸由调用方传入：同一套组件在侧栏和
// 设置页需要的留白不一样，把它钉死在组件里只会逼调用点再改回去。

import "github.com/egoist/mygo/ui"

// 排版与间距档位。和 ControlHeight、Radius 一起构成这套组件的外部尺度。
const (
	FontPageTitle float32 = 20
	FontBody      float32 = 14
	FontSection   float32 = 13
	FontSmall     float32 = 13
	FontTiny      float32 = 12

	GapTight   float32 = 4
	GapInline  float32 = 6
	GapField   float32 = 8
	GapSection float32 = 12
	PadRow     float32 = 8
	PadCard    float32 = 10

	RadiusCard  float32 = 8
	RadiusSmall float32 = 5
)

// WarningInk 是警告色。ui.Theme 只带 Success 和 Danger 两个语义色，警告这一档
// 没有，所以这里按深浅主题各给一个能读的值。
func WarningInk(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#d9a441")
	}
	return ui.Hex("#9b6218")
}

// FaintInk 比 TextMuted 再淡一档，用于说明文字和占位提示。
func FaintInk(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#6f6f74")
	}
	return ui.Hex("#8b929e")
}

// fade 让颜色变化吃掉一段时间，避免悬停时硬切。元素必须带 c.Key：动画状态
// 挂在节点上，每帧重建又不带键的节点什么也平滑不了。
func fade(e ui.Element, key string, target ui.Color) ui.Color {
	return e.AnimateColor(key, target, ui.Animation{Duration: chromeFade, Ease: ui.EaseInOut})
}

// ---- 文本 ----------------------------------------------------------------

// Label 是正文颜色的一行字。
func Label(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(FontBody)
}

// Muted 是比正文淡一档的说明文字。
func Muted(c *ui.Context, s string, size float32) ui.Element {
	return ui.Text(c, s).FontSize(size).TextColor(c.Theme().TextMuted)
}

// SectionTitle 是小节标题：比正文小一号但加粗。
func SectionTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(FontSection).Bold()
}

// ---- 骨架 ----------------------------------------------------------------

// Divider 是卡片之间、工具栏和内容之间的分隔线。
func Divider(c *ui.Context) {
	ui.Box(c).Height(1).FillWidth().Background(c.Theme().Border)
}

// Card 是统一的卡片：面板底色 + 一条跟分隔线同色的细边 + 卡片圆角。底色和
// 描边都走渐变动画，悬停或主题切换都不会硬跳。
func Card(c *ui.Context, key any, padding, radius float32, build func()) ui.Element {
	e := ui.Column(c.Key(key)).Padding(padding).Gap(GapField).Radius(radius)
	e.Background(fade(e, "card-fill", c.Theme().Surface)).
		Border(1, fade(e, "card-border", c.Theme().Border))
	return e.Children(build)
}

// RowCard 是可悬停高亮的列表行卡片。
func RowCard(c *ui.Context, key any, padding, radius float32, build func()) ui.Element {
	e := ui.Column(c.Key(key)).Padding(padding).Gap(GapInline).Radius(radius)
	fill := c.Theme().Surface
	if e.Hovered() {
		fill = c.Theme().SurfaceHover
	}
	e.Background(fade(e, "rowcard-fill", fill)).Border(1, fade(e, "rowcard-border", c.Theme().Border))
	return e.Children(build)
}

// HoverRow 只给已经自己排好版的行上底色，不动它的内边距和高度。
func HoverRow(c *ui.Context, e ui.Element, selected bool) ui.Element {
	fill := ui.Transparent
	if selected || e.Hovered() {
		fill = c.Theme().SurfaceHover
	}
	return e.Background(fade(e, "hoverrow-fill", fill))
}

// SelectableRow 是带悬停和选中态的单行。
func SelectableRow(c *ui.Context, key any, selected bool, height, padding, radius float32, build func()) ui.Element {
	e := ui.Row(c.Key(key)).Height(height).Padding(0, padding).Gap(GapField).Radius(radius).AlignItems(ui.Center)
	HoverRow(c, e, selected)
	return e.Children(build)
}

// ---- 页头与小节 ----------------------------------------------------------

// PageHeader 是页面顶部：标题、可选的副标题，右边一组操作，下方一条分隔线。
func PageHeader(c *ui.Context, title, subtitle string, actions func()) {
	ui.Row(c).Gap(GapSection).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(FontPageTitle).Bold()
			if subtitle != "" {
				Muted(c, subtitle, FontSmall)
			}
		})
		ui.Spacer(c)
		if actions != nil {
			ui.Row(c).Gap(GapField).AlignItems(ui.Center).Children(actions)
		}
	})
	Divider(c)
}

// SectionHeader 是小节标题，右边一组操作。
func SectionHeader(c *ui.Context, title string, actions func()) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		SectionTitle(c, title)
		ui.Spacer(c)
		if actions != nil {
			ui.Row(c).Gap(GapInline).AlignItems(ui.Center).Children(actions)
		}
	})
}

// EmptyState 是一整块空白区域里的居中提示。
func EmptyState(c *ui.Context, title, detail string) {
	ui.Column(c).Fill().Center().Gap(GapInline).Children(func() {
		ui.Text(c, title).FontSize(15).TextColor(c.Theme().TextMuted)
		if detail != "" {
			ui.Text(c, detail).FontSize(FontSmall).TextColor(FaintInk(c.Theme()))
		}
	})
}

// BusyRow 是一个转圈加一行说明。
func BusyRow(c *ui.Context, s string) {
	ui.Row(c).Gap(GapField).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c).Size(14, 14)
		Muted(c, s, FontSmall)
	})
}

// ---- 提示条 --------------------------------------------------------------

// NoticeTone 是提示条的语气。
type NoticeTone int

const (
	NoticeNeutral NoticeTone = iota
	NoticeInfo
	NoticeOK
	NoticeWarn
	NoticeDanger
)

// Notice 是彩色提示条，左边一道竖线表明语气。message 为空时什么都不画，
// 这样调用点可以无条件调用它。
func Notice(c *ui.Context, tone NoticeTone, message string, accentBar bool) {
	if message == "" {
		return
	}
	t := c.Theme()
	fg, fill, bar := t.TextMuted, t.SurfaceHover, t.Border
	switch tone {
	case NoticeInfo:
		fg, fill, bar = t.Accent, t.Accent.Alpha(0.10), t.Accent
	case NoticeOK:
		fg, fill, bar = t.Success, t.Success.Alpha(0.10), t.Success
	case NoticeWarn:
		warn := WarningInk(t)
		fg, fill, bar = warn, warn.Alpha(0.10), warn
	case NoticeDanger:
		fg, fill, bar = t.Danger, t.Danger.Alpha(0.10), t.Danger
	}
	row := ui.Row(c).FillWidth().Padding(7, 10).Gap(GapField).Radius(RadiusSmall).Background(fill)
	if accentBar {
		row.BorderWidth(0, 0, 0, 3).BorderColor(bar)
	}
	row.Children(func() {
		ui.Text(c, message).FontSize(FontSmall).TextColor(fg).LineHeight(1.5)
	})
}

// InlineError 是一行红字，用于表单字段下面。
func InlineError(c *ui.Context, id, message string) {
	if message == "" {
		return
	}
	ui.Text(c, message).FontSize(FontSmall).TextColor(c.Theme().Danger).Key(id)
}

// ---- 统计块 --------------------------------------------------------------

// StatCard 是一个数字加一行说明的统计块。
func StatCard(c *ui.Context, key any, title, big, detail string, padding, radius float32) ui.Element {
	return Card(c, key, padding, radius, func() {
		Muted(c, title, FontSmall)
		ui.Text(c, big).FontSize(22).Bold()
		if detail != "" {
			ui.Text(c, detail).FontSize(FontTiny).TextColor(c.Theme().TextMuted)
		}
	})
}

// KV 是一行「名：值」，名字淡、值正常。
func KV(c *ui.Context, key, value string) {
	ui.Row(c).Gap(GapField).AlignItems(ui.Center).Children(func() {
		ui.Text(c, key).FontSize(FontSmall).TextColor(c.Theme().TextMuted)
		ui.Text(c, value).FontSize(FontSmall)
	})
}
