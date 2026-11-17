package main

import (
	"time"

	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"
)

var galleryIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="14" fill="#4d86e8"/><path d="M9 17l5 5 9-12" fill="none" stroke="white" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/></svg>`))

// Native controls have their own state, so manipulating one side does not
// change the value displayed by its counterpart.
type nativeGallery struct {
	text, query, selected, decimal                 string
	values                                         []string
	checked, enabled, treeOpen, collapse           bool
	checkState                                     ui.CheckState
	level, number, lo, hi                          float64
	tab, radio, rating                             int
	date                                           time.Time
	color                                          ui.Color
	buffer                                         *ui.TextBuffer
	editor                                         *editor.State
	table                                          ui.ListState
	split, splitPercent, vertical, verticalPercent float32
	accordion, sectionOpen                         bool
	sidebar                                        string
	list                                           ui.ListState
	grid                                           ui.GridState
	outline, outlineTable                          ui.OutlineState[string]
	diff                                           *editor.DiffState
	doc                                            *richtext.Document
	modal, alert, popover, findOpen                bool
	findIndex                                      int
	router                                         *ui.Router
}

func (n *nativeGallery) init() {
	n.level, n.number, n.lo, n.hi = .55, 5, 20, 80
	n.selected, n.decimal, n.rating = "选项 A", "1.5", 3
	n.date, n.color = time.Now(), ui.Hex("#6395ee")
	n.buffer = ui.NewTextBuffer("可编辑文本")
	n.editor = &editor.State{Buffer: editor.NewBuffer("package main\n\nfunc main() {}")}
	n.diff = &editor.DiffState{Old: editor.NewBuffer("before\n"), New: editor.NewBuffer("after\n")}
	n.doc = &richtext.Document{}
	n.router = ui.NewRouter("/home")
	n.split, n.splitPercent, n.vertical, n.verticalPercent = 140, 50, 60, 50
	n.sectionOpen = true
}

func (n *nativeGallery) form(c *ui.Context) {
	uikit.Card(c, "native-form", uikit.PadForm, uikit.RadiusCard, func() {
		uikit.SectionTitle(c, "原生表单布局")
		ui.Form(c.Key("form"), func() {
			ui.Field(c.Key("name"), "显示名称", func() { ui.TextInput(c.Key("input"), &n.text).FillWidth() })
			ui.Fieldset(c.Key("fieldset"), "分组", func() { ui.Checkbox(c.Key("enabled"), &n.enabled, "启用") })
		})
	})
}

func (n *nativeGallery) more(c *ui.Context) {
	uikit.Card(c, "native-more", uikit.PadForm, uikit.RadiusCard, func() {
		uikit.SectionTitle(c, "原生状态与数据")
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c.Key("avatar"), "Ally", nil)
			ui.Badge(c.Key("badge"), "8")
			ui.Spinner(c.Key("spinner")).Size(18, 18)
			ui.Meter(c.Key("meter"), n.level, 0, 1, nil).Width(120).Label("原生仪表")
		})
		ui.Divider(c)
	})
}

// These layout widgets deliberately remain direct MyGo APIs in the kit.
// Show them after the comparisons, leaving the right-hand cell empty.
func (n *nativeGallery) unadapted(c *ui.Context) {
	uikit.Card(c, "native-only", uikit.PadForm, uikit.RadiusCard, func() {
		uikit.SectionTitle(c, "原生布局：uikit 未封装")
		uikit.FieldNote(c, "这些基础布局直接使用 MyGo；拖动分隔线可调整大小。")
		panel := func(label string) func() {
			return func() {
				ui.Box(c).Fill().Padding(8).Background(c.Theme().SurfaceHover).Children(func() { ui.Text(c, label) })
			}
		}
		ui.Text(c, "Split · 固定宽度")
		ui.Split(c.Key("split"), &n.split, panel("左"), panel("右")).FillWidth().Height(90)
		ui.Text(c, "SplitPercent · 百分比宽度")
		ui.SplitPercent(c.Key("split-percent"), &n.splitPercent, panel("左"), panel("右")).FillWidth().Height(90)
		ui.Text(c, "SplitVertical · 固定高度")
		ui.SplitVertical(c.Key("split-vertical"), &n.vertical, panel("上"), panel("下")).FillWidth().Height(130)
		ui.Text(c, "SplitVerticalPercent · 百分比高度")
		ui.SplitVerticalPercent(c.Key("split-vertical-percent"), &n.verticalPercent, panel("上"), panel("下")).FillWidth().Height(130)
		ui.Text(c, "ScrollHorizontal · 横向滚动")
		ui.ScrollHorizontal(c.Key("horizontal")).FillWidth().Height(52).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				for i := 0; i < 12; i++ {
					ui.Textf(c.Key(i), "项目 %d", i+1).Padding(8).Background(c.Theme().SurfaceHover)
				}
			})
		})
		ui.Text(c, "ScrollBoth · 双向滚动")
		ui.ScrollBoth(c.Key("both")).FillWidth().Height(90).Children(func() {
			ui.Column(c).Width(700).Children(func() {
				for i := 0; i < 8; i++ {
					ui.Textf(c.Key(i), "第 %d 行：双向滚动区域", i+1)
				}
			})
		})
	})
}

func (n *nativeGallery) actions(c *ui.Context) {
	uikit.Card(c, "native-actions", uikit.PadForm, uikit.RadiusCard, func() {
		uikit.SectionTitle(c, "按钮与反馈")
		ui.Row(c).Wrap().Gap(8).Children(func() {
			split := ui.SplitButton(c.Key("split-button"), "原生分裂按钮")
			split.Arrow.PopoverMenuButton(func(m *ui.Menu) { m.Item("其他操作") })
			primary := ui.PrimarySplitButton(c.Key("primary-split"), "原生主要分裂按钮")
			primary.Arrow.PopoverMenuButton(func(m *ui.Menu) { m.Item("其他操作") })
			ui.PrimaryButton(c.Key("primary"), "原生主要操作")
			ui.Button(c.Key("button"), "次要操作")
			ui.Button(c.Key("disabled"), "不可用").Disabled(true)
		})
		ui.Badge(c.Key("badge"), "状态标签")
	})
}

func (n *nativeGallery) fields(c *ui.Context) {
	uikit.Card(c, "native-fields", uikit.PadForm, uikit.RadiusCard, func() {
		uikit.SectionTitle(c, "表单与选择")
		ui.TextInput(c.Key("input"), &n.text).Label("原生输入").Placeholder("输入文字").FillWidth()
		ui.TextArea(c.Key("area"), &n.text).Label("原生多行输入").Height(70).FillWidth()
		ui.TextInput(c.Key("password"), &n.text).Label("原生密码").Password().FillWidth()
		ui.SearchField(c.Key("search"), &n.query).Label("原生搜索").FillWidth()
		ui.Select(c.Key("select"), &n.selected, []string{"选项 A", "选项 B"}).Label("原生选择").FillWidth()
		ui.Combobox(c.Key("combo"), &n.selected, []string{"开发", "测试", "生产"}).Label("原生组合框").FillWidth()
		ui.Autocomplete(c.Key("complete"), &n.text, []string{"Ally", "MyGo", "Native"}).Label("原生自动补全").FillWidth()
		ui.TokenField(c.Key("tokens"), &n.values, []string{"开发", "测试", "生产"}).Label("原生标签输入").FillWidth()
		ui.TextInputBuffer(c.Key("buffer"), n.buffer).Label("原生缓冲输入").FillWidth()
		ui.TextAreaBuffer(c.Key("area-buffer"), n.buffer).Label("原生缓冲多行输入").Height(60).FillWidth()
		ui.EditableText(c.Key("editable"), &n.text).Label("原生可编辑文字")
		ui.Row(c).Wrap().Gap(8).Children(func() {
			ui.DateInput(c.Key("date"), &n.date).Label("原生日期")
			ui.TimeInput(c.Key("time"), &n.date).Label("原生时间")
			ui.NumberInput(c.Key("number"), &n.number, 0, 100, 1).Label("原生数量")
			ui.DecimalInput(c.Key("decimal"), &n.decimal, "0", "100", "0.1").Label("原生精确数值")
		})
		ui.Row(c).Wrap().Gap(16).Children(func() {
			ui.Checkbox(c.Key("check"), &n.checked, "原生复选框")
			ui.CheckboxState(c.Key("check-state"), &n.checkState, "原生三态复选框")
			ui.Switch(c.Key("switch"), &n.enabled).Label("原生开关")
		})
		ui.RadioGroup(c.Key("radio"), func() {
			ui.Radio(c.Key("one"), &n.radio, 0, "选项一")
			ui.Radio(c.Key("two"), &n.radio, 1, "选项二")
		})
		ui.CheckboxGroup(c.Key("checks"), "复选组", func() { ui.Checkbox(c.Key("member"), &n.checked, "项目") })
		ui.ToggleGroup(c.Key("toggles"), func() { ui.Toggle(c.Key("toggle-item"), &n.enabled, "切换组") })
		ui.Toggle(c.Key("toggle"), &n.enabled, "原生切换按钮")
		ui.Slider(c.Key("slider"), &n.level, 0, 1).Label("原生滑块").FillWidth()
		ui.StepSlider(c.Key("steps"), &n.number, 0, 100, 5).Label("原生步进滑块").FillWidth()
		ui.Stepper(c.Key("stepper"), &n.number, 0, 100, 1).Label("原生步进器")
		ui.Progress(c.Key("progress"), n.level).Label("原生进度").FillWidth()
		ui.RangeSlider(c.Key("range"), &n.lo, &n.hi, 0, 100, 1).Label("原生范围").FillWidth()
		ui.Rating(c.Key("rating"), &n.rating, 5).Label("原生评分")
		ui.ColorWell(c.Key("color"), &n.color).Label("原生颜色")
		ui.ColorPicker(c.Key("color-picker"), &n.color).Label("原生颜色选择器")
		ui.Calendar(c.Key("calendar"), &n.date).Label("原生日历")
	})
}

func (n *nativeGallery) navigation(c *ui.Context) {
	uikit.Card(c, "native-navigation", uikit.PadForm, uikit.RadiusCard, func() {
		uikit.SectionTitle(c, "导航与菜单")
		ui.Tabs(c.Key("tabs"), &n.tab, "概览", "设置", "历史")
		ui.Segmented(c.Key("segmented"), &n.radio, "日", "周")
		ui.Breadcrumbs(c.Key("breadcrumbs"), []string{"首页", "组件", "画廊"}, &n.tab)
		ui.Toolbar(c.Key("toolbar"), func() { ui.Button(c.Key("tool"), "工具栏") })
		ui.Row(c).Gap(8).Children(func() {
			ui.BackButton(c.Key("back"), n.router)
			ui.ForwardButton(c.Key("forward"), n.router)
			ui.Button(c.Key("navigate"), "前往下一页").OnClick(func() { n.router.Push("/next") })
		})
		ui.Sidebar(c.Key("sidebar"), &n.sidebar, func() {
			ui.SidebarSection(c.Key("section"), "导航", &n.sectionOpen, func() {
				ui.SidebarItem(c.Key("first"), "first", nil, "概览")
				ui.SidebarItem(c.Key("second"), "second", nil, "设置")
			})
		}).Height(100)
		ui.Collapsible(c.Key("collapse"), "原生折叠内容", &n.collapse, func() { ui.Text(c, "使用原生键盘与展开行为。") })
		ui.Accordion(c.Key("accordion"), func() {
			ui.AccordionItem(c.Key("item"), "原生手风琴", &n.accordion, func() { ui.Text(c, "这里是展开的内容。") })
		})
		ui.MenuButton(c.Key("menu"), "原生菜单", func(m *ui.Menu) { m.Item("菜单项目"); m.Item("不可用").Disabled(true) })
		ui.PopoverMenuButton(c.Key("popover-menu"), "原生弹出菜单", func(m *ui.Menu) { m.Item("菜单项目") })
		ui.Button(c.Key("dialog-button"), "原生对话框").OnClick(func() { n.modal = true })
		ui.Modal(c.Key("dialog"), &n.modal, func() {
			ui.Text(c, "原生对话框")
			ui.Button(c.Key("close"), "关闭").OnClick(func() { n.modal = false })
		})
		ui.Button(c.Key("alert-button"), "原生确认框").OnClick(func() { n.alert = true })
		ui.AlertDialog(c.Key("alert"), &n.alert, "确认", "这是原生确认框。", "取消", "确定")
		anchor := ui.Button(c.Key("popover-button"), "原生浮层").OnClick(func() { n.popover = !n.popover })
		ui.Popover(c.Key("popover"), anchor, &n.popover, func() { ui.Text(c, "原生浮层内容") })
		ui.Button(c.Key("tooltip"), "原生提示").Tooltip("原生工具提示")
		ui.Button(c.Key("context"), "右键显示原生菜单").PopoverMenu(func(m *ui.Menu) { m.Item("原生菜单项") })
	})
}

func (n *nativeGallery) data(c *ui.Context) {
	uikit.Card(c, "native-data", uikit.PadForm, uikit.RadiusCard, func() {
		uikit.SectionTitle(c, "列表与数据")
		ui.Tree(c.Key("tree"), func() {
			ui.TreeItem(c.Key("folder"), "工作区", &n.treeOpen, func() { ui.TreeItem(c.Key("file"), "示例文件", nil, nil) })
		})
		columns := []ui.TableColumn{{Title: "名称"}, {Title: "状态", Width: 120}}
		rows := [][2]string{{"示例任务", "进行中"}, {"连接测试", "已完成"}, {"历史记录", "已保存"}}
		ui.Table(c.Key("table"), &n.table, columns, len(rows), func(row, col int) { ui.Text(c, rows[row][col]).SingleLine() }).Label("原生表格").Height(160).FillWidth()
		ui.List(c.Key("list"), &n.list, len(rows), func(i int) { ui.Text(c, rows[i][0]) }).Label("原生虚拟列表").FillWidth().Height(100)
		ui.GridView(c.Key("grid"), &n.grid, len(rows), 100, 40, func(i int) { ui.Badge(c.Key(i), rows[i][0]) }).Label("原生网格列表").FillWidth().Height(100)
		children := func(s string) []string {
			if s == "文件夹" {
				return []string{"文件"}
			}
			return nil
		}
		ui.Outline(c.Key("outline"), &n.outline, []string{"文件夹"}, children, func(s string) { ui.Text(c, s) }).Label("原生大纲").FillWidth().Height(80)
		ui.OutlineTable(c.Key("outline-table"), &n.outlineTable, columns, []string{"文件夹"}, children, func(s string, col int) {
			if col == 0 {
				ui.Text(c, s)
			} else {
				ui.Text(c, "就绪")
			}
		}).Label("原生大纲表格").FillWidth().Height(100)
	})
}

func (n *nativeGallery) content(c *ui.Context) {
	uikit.Card(c, "native-content", uikit.PadForm, uikit.RadiusCard, func() {
		uikit.SectionTitle(c, "内容与编辑")
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c.Key("icon"), galleryIcon).Size(32, 32).Label("原生图标")
			ui.Image(c.Key("image"), galleryIcon).Size(32, 32).Label("原生图像")
			ui.Link(c.Key("link"), "MyGo 文档", "https://mygo.run/zh/docs/ui")
		})
		markdown.View(c.Key("markdown"), "**Markdown** 支持格式化内容。", nil)
		ui.RichText(c.Key("rich"), ui.Span{Text: "原生富文本"})
		editor.View(c.Key("editor"), n.editor).Label("原生代码编辑器").Height(110).FillWidth()
		editor.DiffView(c.Key("diff"), n.diff).Label("原生差异查看器").Height(130).FillWidth()
		richtext.Editor(c.Key("rich-editor"), n.doc).Label("原生富文本编辑器").Height(80).FillWidth()
		markdown.Stream(c.Key("stream"), "流式 **Markdown**", nil)
		x := chart.NewBand("一", "二", "三").Padding(0.3, 0.15)
		y := chart.NewLinear(0, 100)
		chart.Plot(c.Key("chart"), chart.Axes{Left: y.Ticks(3), Bottom: x.Ticks()}, func(area ui.Element) {
			for i, v := range []float64{35, 70, 55} {
				chart.Rect(c, x.Band(i), y.Span(0, v)).Background(c.Theme().Accent)
			}
		}).Height(160).FillWidth().Label("原生图表")
		ui.FindBar(c.Key("find"), &n.findOpen, &n.query, 1, &n.findIndex)
		ui.Button(c.Key("find-open"), "显示原生查找栏").OnClick(func() { n.findOpen = true })
	})
}
