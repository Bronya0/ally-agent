// The gallery is a development tool for checking the native kit in a window.
// Run it with go run ./desktop/uikit/_gallery.
package main

import (
	"log"
	"time"

	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/richtext"
)

type gallery struct {
	dark, checked, enabled         bool
	accent                         string
	text, query, group             string
	level                          float64
	tab, radio, nativeKitTab       int
	date                           time.Time
	values                         []string
	treeOpen                       bool
	table                          ui.ListState
	selectValue, password, decimal string
	lo, hi, number                 float64
	rating                         int
	color                          ui.Color
	modal, accordion, collapse     bool
	editor                         *editor.State
	diff                           *editor.DiffState
	doc                            *richtext.Document
	buffer                         *ui.TextBuffer
	list                           ui.ListState
	grid                           ui.GridState
	outline                        ui.OutlineState[string]
	findOpen, popover, reveal      bool
	findIndex                      int
	checkState                     ui.CheckState
	sidebar                        string
	sectionOpen                    bool
	outlineTable                   ui.OutlineState[string]
	native                         nativeGallery
	scroll                         ui.ScrollState
}

var galleryAccentOptions = func() []uikit.Option {
	themes := uikit.BuiltinThemes()
	options := make([]uikit.Option, 0, len(themes))
	for _, theme := range themes {
		options = append(options, uikit.Option{Value: string(theme.ID), Label: theme.Name})
	}
	return options
}()

func galleryTheme(dark bool, accent string) *ui.Theme {
	return uikit.BuiltinTheme(uikit.ThemeID(accent), dark)
}

func (g *gallery) view(c *ui.Context) {
	theme := galleryTheme(g.dark, g.accent)
	c.SetTheme(theme)
	c.Root().Background(theme.Background)
	ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Ally UI Kit").FontSize(uikit.FontPageTitle).Bold()
			ui.Spacer(c)
			ui.Text(c, "主题").FontSize(uikit.FontSmall).TextColor(theme.Text)
			uikit.SelectField(c.Key("accent"), &g.accent, galleryAccentOptions).Label("主题").Width(120)
			uikit.Switch(c.Key("theme"), &g.dark, "深色主题")
			ui.Text(c, "深色主题").FontSize(uikit.FontSmall).TextColor(theme.Text)
		})
		ui.Scroll(c.Key("comparison-scroll")).TrackScroll(&g.scroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(18).Children(func() {
				ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(16).Children(func() {
					ui.Text(c, "MyGo 原生").FontSize(uikit.FontHeading).Bold()
					ui.Text(c, "Ally UI Kit").FontSize(uikit.FontHeading).Bold()
				})
				comparisonRow(c, func() { g.native.actions(c) }, func() {
					uikit.Card(c, "actions", uikit.PadForm, uikit.RadiusCard, func() {
						uikit.SectionTitle(c, "按钮与反馈")
						ui.Row(c).Wrap().Gap(8).Children(func() {
							uikit.SplitButton(c.Key("split-button"), "分裂按钮")
							uikit.PrimarySplitButton(c.Key("primary-split"), "主要分裂按钮")
							uikit.Action(c.Key("primary"), "主要操作", uikit.ActionOptions{Tone: uikit.Primary, Style: uikit.Secondary}).OnClick(func() { uikit.Message(c, uikit.Success, "操作完成", 0) })
							uikit.Action(c.Key("notification"), "显示通知", uikit.ActionOptions{Style: uikit.Tertiary}).OnClick(func() {
								uikit.Notification(c, uikit.NotificationOptions{Title: "连接成功", Description: "服务器已连接，可以继续工作。", Tone: uikit.Success, Timeout: -1})
							})
							uikit.Action(c.Key("neutral"), "次要操作", uikit.ActionOptions{Style: uikit.Tertiary})
							uikit.Action(c.Key("text"), "文字操作", uikit.ActionOptions{Style: uikit.Quaternary})
							uikit.Action(c.Key("danger"), "危险操作", uikit.ActionOptions{Tone: uikit.Error, Style: uikit.Ghost})
							uikit.Action(c.Key("disabled"), "不可用", uikit.ActionOptions{Disabled: true})
						})
						ui.Row(c).Wrap().Gap(8).Children(func() {
							for _, tone := range []uikit.Tone{uikit.Primary, uikit.Success, uikit.Warning, uikit.Error} {
								uikit.ColorTag(c.Key(tone), "状态标签", uikit.TagOptions{Tone: tone})
							}
						})
						uikit.Notice(c, uikit.NoticeInfo, "提示信息跟随当前主题。")
					})
				})
				comparisonRow(c, func() { g.native.fields(c) }, func() {
					uikit.Card(c, "fields", uikit.PadForm, uikit.RadiusCard, func() {
						uikit.SectionTitle(c, "表单与选择")
						uikit.Input(c.Key("input"), &g.text).Label("输入").Placeholder("输入文字").FillWidth()
						uikit.InputStatus(c.Key("error"), &g.text, uikit.FieldError).Label("错误状态").FillWidth()
						uikit.InputStatus(c.Key("warning"), &g.text, uikit.FieldWarning).Label("警告状态").FillWidth()
						uikit.FieldNote(c, "字段验证信息显示在输入框附近。")
						uikit.TextArea(c.Key("area"), &g.text, "多行文本", 70).Label("多行输入")
						uikit.PasswordInputStatus(c, "password", "密码", &g.password, uikit.FieldNormal, "输入密码")
						uikit.SearchField(c.Key("search"), &g.query).Label("搜索").FillWidth()
						uikit.SelectFieldStatus(c.Key("select"), &g.selectValue, []uikit.Option{{Value: "a", Label: "选项 A"}, {Value: "b", Label: "选项 B"}, {Value: "c", Label: "禁用", Disabled: true}}, uikit.FieldNormal).Label("选择").FillWidth()
						uikit.ComboboxField(c.Key("combo"), g.group, []string{"开发", "测试", "生产"}, "分组", "选择或输入", func(v string) { g.group = v }).FillWidth()
						uikit.Autocomplete(c.Key("complete"), &g.text, []string{"Ally", "MyGo", "Native"}).Label("自动补全").FillWidth()
						uikit.TokenField(c.Key("tokens"), &g.values, []string{"开发", "测试", "生产"}).Label("标签输入").FillWidth()
						uikit.TextInputBufferStatus(c.Key("buffer"), g.buffer, uikit.FieldNormal).Label("缓冲输入").FillWidth()
						uikit.TextAreaBufferStatus(c.Key("area-buffer"), g.buffer, uikit.FieldNormal).Label("缓冲多行输入").Height(60).FillWidth()
						uikit.EditableText(c.Key("editable"), &g.text).Label("可编辑文字")
						ui.Row(c).Wrap().Gap(8).Children(func() {
							uikit.DateInput(c.Key("date"), &g.date).Label("日期")
							uikit.TimeInput(c.Key("time"), &g.date).Label("时间")
							uikit.NumberInput(c, "number", "数量", &g.number, 0, 100, 1)
							uikit.DecimalInput(c.Key("decimal"), &g.decimal, "0", "100", "0.1").Label("精确数值")
						})
						ui.Row(c).Wrap().Gap(16).Children(func() {
							uikit.Checkbox(c.Key("check"), &g.checked, "复选框")
							uikit.CheckboxState(c.Key("check-state"), &g.checkState, "三态复选框")
							ui.Text(c, "开关").FontSize(uikit.FontBody)
							uikit.Switch(c.Key("switch"), &g.enabled, "开关")
						})
						uikit.Radios(c.Key("radio"), &g.radio, []uikit.RadioOption[int]{{Value: 0, Label: "选项一"}, {Value: 1, Label: "选项二"}}, true)
						uikit.Radios(c.Key("plain-radio"), &g.radio, []uikit.RadioOption[int]{{Value: 0, Label: "普通单选"}, {Value: 1, Label: "另一选项"}}, false)
						uikit.Toggle(c.Key("toggle"), &g.enabled, "切换按钮")
						uikit.Slider(c.Key("slider"), &g.level, 0, 1).Label("调整进度").FillWidth()
						uikit.StepSlider(c.Key("steps"), &g.number, 0, 100, 5).Label("步进滑块").FillWidth()
						uikit.Stepper(c.Key("stepper"), &g.number, 0, 100, 1).Label("步进器")
						uikit.Progress(c.Key("progress"), g.level).Label("进度").FillWidth()
						uikit.RangeSlider(c.Key("range"), &g.lo, &g.hi, 0, 100, 1).Label("范围").FillWidth()
						uikit.Rating(c.Key("rating"), &g.rating, 5).Label("评分")
						uikit.ColorWell(c.Key("color"), &g.color).Label("颜色")
						uikit.ColorPicker(c.Key("color-picker"), &g.color).Label("颜色选择器")
						uikit.Calendar(c.Key("calendar"), &g.date).Label("日历")
					})
				})
				comparisonRow(c, func() { g.native.form(c) }, func() {
					uikit.FormCard(c, "form", "表单布局", "标题、说明和控件随空间换行。", func() {
						uikit.FormRow(c, "row", "显示名称", "用于识别此项目。", 260, func() ui.Element { return uikit.Input(c.Key("value"), &g.text).FillWidth() })
					})
				})
				comparisonRow(c, func() { g.native.navigation(c) }, func() {
					uikit.Card(c, "navigation", uikit.PadForm, uikit.RadiusCard, func() {
						uikit.SectionTitle(c, "导航与菜单")
						uikit.Tabs(c.Key("tabs"), &g.tab, "概览", "设置", "历史")
						uikit.TabsWithOptions(c.Key("native-tabs"), &g.nativeKitTab, uikit.TabOptions{Style: uikit.TabNative}, "概览", "设置", "历史")
						uikit.Segmented(c.Key("segments"), &g.radio, "日", "周")
						uikit.Breadcrumbs(c.Key("breadcrumbs"), []string{"首页", "组件", "画廊"}, &g.tab)
						uikit.Toolbar(c.Key("toolbar"), func() { uikit.Action(c.Key("tool"), "工具栏", uikit.ActionOptions{Style: uikit.Quaternary}) })
						uikit.Sidebar(c.Key("sidebar"), &g.sidebar, func() {
							uikit.SidebarSection(c.Key("section"), "导航", &g.sectionOpen, func() {
								uikit.SidebarItem(c.Key("first"), "first", nil, "概览")
								uikit.SidebarItem(c.Key("second"), "second", nil, "设置")
							})
						}).Height(100)
						uikit.Collapsible(c.Key("collapse"), "折叠内容", &g.collapse, func() { uikit.Muted(c, "使用原生键盘与展开行为。", uikit.FontSmall) })
						uikit.Accordion(c.Key("accordion"), func() {
							uikit.AccordionItem(c.Key("item"), "手风琴", &g.accordion, func() { uikit.Label(c, "这里是展开的内容。") })
						})
						uikit.Action(c.Key("open-dialog"), "打开对话框", uikit.ActionOptions{Style: uikit.Tertiary}).OnClick(func() { g.modal = true })
						anchor := uikit.Action(c.Key("open-popover"), "打开浮层", uikit.ActionOptions{Style: uikit.Tertiary})
						anchor.OnClick(func() { g.popover = !g.popover })
						uikit.Popover(c.Key("popover"), anchor, &g.popover, func() { uikit.Label(c, "浮层内容") })
						uikit.Tooltip(c, uikit.Action(c.Key("tooltip"), "悬停提示", uikit.ActionOptions{Style: uikit.Tertiary}), "支持鼠标和键盘焦点")
						uikit.Switch(c.Key("reveal-switch"), &g.reveal, "展开动画")
						uikit.Reveal(c.Key("reveal"), g.reveal, func() { uikit.Notice(c, uikit.NoticeInfo, "展开的内容") })
						uikit.MenuButton(c.Key("menu"), nil, uikit.MenuOptions{Width: 180}, func(trigger ui.Element) {
							uikit.ButtonOn(c, trigger, "打开菜单", uikit.ButtonSecondary, false, nil, uikit.ActionOptions{})
						}, func(menu ui.MenuParts) {
							uikit.MenuItem(c, menu, "one", "菜单项目", uikit.MenuItemOptions{}).OnClick(func() { uikit.Message(c, uikit.Info, "已选择", 0) })
							uikit.MenuItem(c, menu, "disabled", "不可用", uikit.MenuItemOptions{}).Disabled(true)
						})
					})
				})
				comparisonRow(c, func() { g.native.data(c) }, func() {
					uikit.Card(c, "data", uikit.PadForm, uikit.RadiusCard, func() {
						uikit.SectionTitle(c, "列表与数据")
						uikit.Tree(c.Key("tree"), func() {
							uikit.TreeItem(c.Key("folder"), "工作区", &g.treeOpen, func() {
								uikit.TreeItem(c.Key("file"), "示例文件", nil, nil)
							})
						})
						columns := []ui.TableColumn{{Title: "名称"}, {Title: "状态", Width: 120}}
						rows := [][2]string{{"示例任务", "进行中"}, {"连接测试", "已完成"}, {"历史记录", "已保存"}}
						uikit.Table(c.Key("table"), &g.table, columns, len(rows), func(row, col int) {
							ui.Text(c, rows[row][col]).SingleLine()
						}).Label("示例表格").FillWidth().Height(160)
						uikit.List(c.Key("list"), &g.list, len(rows), func(i int) { uikit.Label(c, rows[i][0]) }).Label("虚拟列表").FillWidth().Height(100)
						uikit.GridView(c.Key("grid"), &g.grid, len(rows), 100, 40, func(i int) { uikit.ColorTag(c.Key(i), rows[i][0], uikit.TagOptions{Tone: uikit.Primary}) }).Label("网格列表").FillWidth().Height(100)
						uikit.Outline(c.Key("outline"), &g.outline, []string{"文件夹"}, func(s string) []string {
							if s == "文件夹" {
								return []string{"文件"}
							}
							return nil
						}, func(s string) { uikit.Label(c, s) }).Label("大纲").Height(80).FillWidth()
						uikit.OutlineTable(c.Key("outline-table"), &g.outlineTable, columns, []string{"文件夹"}, func(s string) []string {
							if s == "文件夹" {
								return []string{"文件"}
							}
							return nil
						}, func(s string, col int) {
							if col == 0 {
								uikit.Label(c, s)
							} else {
								uikit.Label(c, "就绪")
							}
						}).Label("大纲表格").FillWidth().Height(100)
					})
				})
				comparisonRow(c, func() { g.native.more(c) }, func() {
					uikit.Card(c, "more", uikit.PadForm, uikit.RadiusCard, func() {
						uikit.SectionTitle(c, "状态与数据")
						ui.Row(c).Wrap().Gap(8).Children(func() {
							for _, tone := range []uikit.Tone{uikit.Default, uikit.Primary, uikit.Info, uikit.Success, uikit.Warning, uikit.Error} {
								uikit.Action(c.Key(tone), "操作", uikit.ActionOptions{Tone: tone, Style: uikit.Secondary})
							}
							uikit.Action(c.Key("loading"), "加载中", uikit.ActionOptions{Loading: true})
						})
						ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
							uikit.Avatar(c.Key("avatar"), "Ally", nil)
							uikit.Badge(c.Key("badge"), "8")
							uikit.Spinner(c.Key("spinner")).Size(18, 18)
							uikit.Meter(c.Key("meter"), g.level, 0, 1, nil).Width(120).Label("仪表")
						})
						uikit.Notice(c, uikit.NoticeInfo, "普通提示")
						uikit.InlineError(c, "example-error", "错误信息示例")
						uikit.KV(c, "版本", "MyGo v0.10.0")
						uikit.Divider(c)
						uikit.EmptyState(c, "暂无数据", "这里展示空状态。")
						uikit.BusyRow(c, "正在加载")
						uikit.StatCard(c, "stat", "任务", "12", "本周完成", uikit.PadCard, uikit.RadiusCard)
						uikit.RowCard(c, "row-card", uikit.PadCard, uikit.RadiusCard, func() { uikit.Label(c, "悬停行卡片") })
						uikit.SelectableRow(c, "selected-row", true, 34, 10, uikit.RadiusSmall, func() { uikit.Label(c, "选中行") })
					})
				})
				comparisonRow(c, func() { g.native.content(c) }, func() {
					uikit.Card(c, "content", uikit.PadForm, uikit.RadiusCard, func() {
						uikit.SectionTitle(c, "内容与编辑")
						ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
							uikit.Icon(c.Key("icon"), galleryIcon).Size(32, 32).Label("图标")
							uikit.Image(c.Key("image"), galleryIcon).Size(32, 32).Label("图像")
							uikit.Link(c.Key("link"), "MyGo 文档", "https://mygo.run/zh/docs/ui")
						})
						uikit.Markdown(c.Key("markdown"), "**Markdown** 支持格式化内容。", nil)
						uikit.RichText(c.Key("rich"), ui.Span{Text: "原生富文本"})
						uikit.CodeEditor(c.Key("editor"), g.editor).Label("代码编辑器").Height(110).FillWidth()
						uikit.DiffViewer(c.Key("diff"), g.diff).Label("差异查看器").Height(130).FillWidth()
						uikit.RichTextEditor(c.Key("rich-editor"), g.doc).Label("富文本编辑器").Height(80).FillWidth()
						uikit.MarkdownStream(c.Key("stream"), "流式 **Markdown**", nil)
						x := chart.NewBand("一", "二", "三").Padding(0.3, 0.15)
						y := chart.NewLinear(0, 100)
						uikit.Chart(c.Key("chart"), chart.Axes{Left: y.Ticks(3), Bottom: x.Ticks()}, func(area ui.Element) {
							for i, v := range []float64{35, 70, 55} {
								chart.Rect(c, x.Band(i), y.Span(0, v)).Background(theme.Accent)
							}
						}).Height(160).FillWidth().Label("示例图表")
						uikit.FindBar(c.Key("find"), &g.findOpen, &g.query, 1, &g.findIndex)
						uikit.Action(c.Key("find-open"), "显示查找栏", uikit.ActionOptions{Style: uikit.Tertiary}).OnClick(func() { g.findOpen = true })
					})
				})
				ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(16).Children(func() {
					ui.Column(c).JustifySelf(ui.Stretch).MinWidth(0).Children(func() { g.native.unadapted(c) })
					ui.Box(c)
				})
			})
		})
	})
	uikit.Modal(c.Key("dialog"), &g.modal, func() {
		ui.Column(c).Gap(12).Children(func() {
			uikit.SectionTitle(c, "对话框")
			uikit.Label(c, "这是由 MyGo 管理焦点与关闭行为的对话框。")
			uikit.Action(c.Key("close"), "关闭", uikit.ActionOptions{Tone: uikit.Primary}).OnClick(func() { g.modal = false })
		})
	})
	uikit.Messages(c)
}

// comparisonRow makes each matched section share one grid row, so different
// natural heights do not make later sections drift out of alignment.
func comparisonRow(c *ui.Context, left, right func()) {
	ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(16).Children(func() {
		ui.Column(c).JustifySelf(ui.Stretch).MinWidth(0).Children(left)
		ui.Column(c).JustifySelf(ui.Stretch).MinWidth(0).Children(right)
	})
}

func newGallery() *gallery {
	g := &gallery{accent: string(uikit.ThemeBlue), level: 0.55, lo: 20, hi: 80, number: 5, decimal: "1.5", selectValue: "a", rating: 3, color: ui.Hex("#6395ee"), date: time.Now(), editor: &editor.State{Buffer: editor.NewBuffer("package main\n\nfunc main() {}")}, diff: &editor.DiffState{Old: editor.NewBuffer("before\n"), New: editor.NewBuffer("after\n")}, doc: &richtext.Document{}, buffer: ui.NewTextBuffer("可编辑文本"), sectionOpen: true}
	g.native.init()
	return g
}

func main() {
	g := newGallery()
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{Title: "Ally UI Kit", Width: 1200, Height: 800, Content: ui.View(g.view)})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
