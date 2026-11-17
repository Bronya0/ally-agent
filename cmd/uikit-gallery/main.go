// The gallery is a development tool for checking the native kit in a window.
// Run it with go run ./cmd/uikit-gallery.
package main

import (
	"log"
	"time"

	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type gallery struct {
	dark, checked, enabled bool
	text, query, group     string
	level                  float64
	tab, radio             int
	date                   time.Time
	values                 []string
	treeOpen               bool
	table                  ui.ListState
}

func (g *gallery) view(c *ui.Context) {
	theme := ui.LightTheme()
	if g.dark {
		theme = ui.DarkTheme()
	}
	c.SetTheme(theme)
	c.Root().Background(theme.Background)
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(24).Gap(18).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Ally UI Kit").FontSize(uikit.FontPageTitle).Bold()
				ui.Spacer(c)
				ui.Text(c, "深色主题").FontSize(uikit.FontSmall).TextColor(theme.Text)
				uikit.Switch(c.Key("theme"), &g.dark, "深色主题")
			})
			uikit.Card(c, "actions", uikit.PadForm, uikit.RadiusCard, func() {
				uikit.SectionTitle(c, "按钮与反馈")
				ui.Row(c).Wrap().Gap(8).Children(func() {
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
			uikit.Card(c, "fields", uikit.PadForm, uikit.RadiusCard, func() {
				uikit.SectionTitle(c, "表单与选择")
				uikit.Input(c.Key("input"), &g.text).Label("输入").Placeholder("输入文字").FillWidth()
				uikit.InputStatus(c.Key("error"), &g.text, uikit.FieldError).Label("错误状态").FillWidth()
				uikit.FieldNote(c, "字段验证信息显示在输入框附近。")
				uikit.SearchField(c.Key("search"), &g.query).Label("搜索").FillWidth()
				uikit.ComboboxField(c.Key("combo"), g.group, []string{"开发", "测试", "生产"}, "分组", "选择或输入", func(v string) { g.group = v }).FillWidth()
				uikit.Autocomplete(c.Key("complete"), &g.text, []string{"Ally", "MyGo", "Native"}).Label("自动补全").FillWidth()
				uikit.TokenField(c.Key("tokens"), &g.values, []string{"开发", "测试", "生产"}).Label("标签输入").FillWidth()
				ui.Row(c).Gap(8).Children(func() {
					uikit.DateInput(c.Key("date"), &g.date).Label("日期")
					uikit.TimeInput(c.Key("time"), &g.date).Label("时间")
				})
				ui.Row(c).Gap(16).Children(func() {
					uikit.Checkbox(c.Key("check"), &g.checked, "复选框")
					ui.Text(c, "开关").FontSize(uikit.FontBody)
					uikit.Switch(c.Key("switch"), &g.enabled, "开关")
				})
				uikit.Radios(c.Key("radio"), &g.radio, []uikit.RadioOption[int]{{Value: 0, Label: "选项一"}, {Value: 1, Label: "选项二"}}, true)
				uikit.Slider(c.Key("slider"), &g.level, 0, 1).Label("调整进度").FillWidth()
				uikit.Progress(c.Key("progress"), g.level).Label("进度").FillWidth()
			})
			uikit.Card(c, "navigation", uikit.PadForm, uikit.RadiusCard, func() {
				uikit.SectionTitle(c, "导航与菜单")
				uikit.Tabs(c.Key("tabs"), &g.tab, "概览", "设置", "历史")
				uikit.MenuButton(c.Key("menu"), nil, uikit.MenuOptions{Width: 180}, func(trigger ui.Element) {
					uikit.ButtonOn(c, trigger, "打开菜单", uikit.ButtonSecondary, false, nil, uikit.ActionOptions{})
				}, func(menu ui.MenuParts) {
					uikit.MenuItem(c, menu, "one", "菜单项目", uikit.MenuItemOptions{}).OnClick(func() { uikit.Message(c, uikit.Info, "已选择", 0) })
					uikit.MenuItem(c, menu, "disabled", "不可用", uikit.MenuItemOptions{}).Disabled(true)
				})
			})
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
			})
		})
	})
	uikit.Messages(c)
}

func main() {
	g := &gallery{level: 0.55, date: time.Now()}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{Title: "Ally UI Kit", Width: 820, Height: 760, Content: ui.View(g.view)})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
