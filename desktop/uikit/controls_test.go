package uikit

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSelectAndTabs(t *testing.T) {
	value := "API Key"
	tab := 0
	tt := chineseTester(func(c *ui.Context) {
		c.SetTheme(ui.DarkTheme())
		ui.Column(c).Children(func() {
			Select(c.Key("select"), &value, []string{"API Key", "OAuth"}).Label("Authentication")
			Tabs(c.Key("tabs"), &tab, "One", "Two")
		})
	}, 500, 400)
	tt.Frame()
	if err := tt.Click("Authentication"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("OAuth"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if value != "OAuth" {
		t.Fatal("option did not choose value")
	}
	before, _ := tt.Find("One")
	if err := tt.Click("Two"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	after, _ := tt.Find("One")
	if tab != 1 || before.Y != after.Y {
		t.Fatal("tab selection moved baseline")
	}
}

func TestNativeControlsKeepInputBehavior(t *testing.T) {
	checked, query, level := false, "", float64(0.5)
	tt := chineseTester(func(c *ui.Context) {
		ui.Column(c).Gap(8).Children(func() {
			Checkbox(c.Key("check"), &checked, "启用")
			SearchField(c.Key("search"), &query).Label("查找").Width(200)
			Slider(c.Key("slider"), &level, 0, 1).Label("强度").Width(200)
			Progress(c.Key("progress"), level).Label("进度").Width(200)
		})
	}, 360, 250)
	if err := tt.Click("启用"); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("checkbox did not change")
	}
	if err := tt.Click("查找"); err != nil {
		t.Fatal(err)
	}
	tt.Type("hello")
	if query != "hello" {
		t.Fatalf("search = %q", query)
	}
	if err := tt.Click("强度"); err != nil {
		t.Fatal(err)
	}
	old := level
	tt.Key(0, ui.KeyRight)
	if level <= old {
		t.Fatalf("slider keyboard did not increase: %v -> %v", old, level)
	}
	if _, ok := tt.AccessByRole(ui.RoleProgress, "进度"); !ok {
		t.Fatal("progress accessibility role missing")
	}
}

func TestAutocompletePreservesTypingAndSelection(t *testing.T) {
	value := ""
	tt := chineseTester(func(c *ui.Context) {
		Autocomplete(c.Key("autocomplete"), &value, []string{"Alpha", "Beta"}).Label("补全").Width(200)
	}, 350, 250)
	if err := tt.Click("补全"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Be")
	if value != "Be" {
		t.Fatalf("typed value = %q", value)
	}
	if err := tt.Click("Beta"); err != nil {
		t.Fatal(err)
	}
	if value != "Beta" {
		t.Fatalf("selected value = %q", value)
	}
}

func TestSelectSkipsDisabledOption(t *testing.T) {
	value := "first"
	tt := chineseTester(func(c *ui.Context) {
		SelectField(c.Key("choice"), &value, []Option{{Value: "first", Label: "第一项"}, {Value: "locked", Label: "不可选", Disabled: true}, {Value: "last", Label: "最后一项"}}).Label("选择")
	}, 360, 300)
	if err := tt.Click("选择"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("不可选"); err != nil {
		t.Fatal(err)
	}
	if value != "first" {
		t.Fatalf("disabled option selected: %q", value)
	}
	if !tt.HasText("最后一项") {
		if err := tt.Click("选择"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.Click("最后一项"); err != nil {
		t.Fatal(err)
	}
	if value != "last" {
		t.Fatalf("selection = %q", value)
	}
}

func TestSegmentedSelection(t *testing.T) {
	for _, light := range []bool{false, true} {
		t.Run(fmt.Sprint("light=", light), func(t *testing.T) {
			selected, changes := 0, 0
			tt := chineseTester(func(c *ui.Context) {
				theme := ui.DarkTheme()
				if light {
					theme = ui.LightTheme()
				}
				c.SetTheme(theme)
				ui.Column(c).Padding(16).Children(func() {
					if Segmented(c.Key("mode"), &selected, "关闭代理", "跟随系统代理", "手动代理").Changed() {
						changes++
					}
				})
			}, 480, 100)
			tt.Frame()
			before, _ := tt.Find("跟随系统代理")
			if err := tt.Click("跟随系统代理"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if selected != 1 || changes != 1 {
				t.Fatalf("click: selected=%d changes=%d", selected, changes)
			}
			after, _ := tt.Find("跟随系统代理")
			if before != after {
				t.Fatalf("selection changed geometry: %v -> %v", before, after)
			}
			tt.Key(0, ui.KeyRight)
			tt.Frame()
			if selected != 2 || changes != 2 {
				t.Fatalf("keyboard: selected=%d changes=%d", selected, changes)
			}
			for range 3 {
				tt.Frame()
			}
			if selected != 2 || changes != 2 {
				t.Fatal("selection did not persist")
			}
		})
	}
}

func TestFormFieldsHaveGap(t *testing.T) {
	left, right := "", ""
	tt := chineseTester(func(c *ui.Context) {
		ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(14).Children(func() {
			ui.Column(c).JustifySelf(ui.Stretch).Children(func() { Input(c.Key("left"), &left).FillWidth().Label("left-field") })
			ui.Column(c).JustifySelf(ui.Stretch).Children(func() { Input(c.Key("right"), &right).FillWidth().Label("right-field") })
		})
	}, 600, 200)
	tt.Frame()
	a, ok := tt.Find("left-field")
	if !ok {
		t.Fatal("missing left")
	}
	b, ok := tt.Find("right-field")
	if !ok {
		t.Fatal("missing right")
	}
	if b.X < a.X+a.W+13 {
		t.Fatalf("fields touch: %#v %#v", a, b)
	}
}

func TestNumberInputNativeEditingAndSteps(t *testing.T) {
	value := 8192.0
	tt := chineseTester(func(c *ui.Context) {
		c.SetTheme(ui.DarkTheme())
		ui.Column(c).Width(300).Children(func() { NumberInput(c, "number", "Max Tokens", &value, 0, 10000000, 1).FillWidth() })
	}, 360, 120)
	input, ok := tt.Find("Max Tokens")
	if !ok {
		t.Fatal("missing input")
	}
	minus, ok := tt.Find("Decrease")
	if !ok {
		t.Fatal("missing decrement")
	}
	plus, ok := tt.Find("Increase")
	if !ok {
		t.Fatal("missing increment")
	}
	if minus.X <= input.X || plus.X < minus.X || plus.X+plus.W > input.X+input.W+1 {
		t.Fatalf("number layout: input=%+v minus=%+v plus=%+v", input, minus, plus)
	}
	if err := tt.Click("Increase"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if value != 8193 {
		t.Fatalf("increment: %v", value)
	}
	if err := tt.Click("Decrease"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if value != 8192 {
		t.Fatalf("decrement: %v", value)
	}
	tt.ClickAt(input.X+10, input.Y+input.H/2)
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if value != 8193 {
		t.Fatalf("keyboard increment: %v", value)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("12345")
	tt.Frame()
	if value != 12345 {
		t.Fatalf("typed value: %v", value)
	}
}

func TestComboboxFieldEditsAndSelects(t *testing.T) {
	value := "33"
	tt := chineseTester(func(c *ui.Context) {
		ComboboxField(c.Key("group"), value, []string{"33", "运维"}, "分组", "", func(next string) { value = next }).Width(260)
	}, 360, 200)
	if err := tt.Click("分组"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("自定义")
	if value != "自定义" {
		t.Fatalf("typed = %q", value)
	}
	if !tt.HasText("无匹配选项") {
		t.Fatal("missing empty result feedback")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	if value != "" {
		t.Fatalf("cleared = %q", value)
	}
	tt.Key(0, ui.KeyDown)
	if err := tt.Click("运维"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if value != "运维" {
		t.Fatalf("selected = %q", value)
	}
}

func TestPasswordInputVisibilityToggle(t *testing.T) {
	secret := "secret-value"
	tt := chineseTester(func(c *ui.Context) {
		c.SetTheme(ui.DarkTheme())
		ui.Column(c).Width(300).Children(func() { PasswordInput(c, "password", "API Key 1", &secret).FillWidth() })
	}, 360, 120)
	input, ok := tt.Find("API Key 1")
	if !ok {
		t.Fatal("missing password input")
	}
	toggle, ok := tt.Find("显示 API Key 1")
	if !ok {
		t.Fatal("missing visibility toggle")
	}
	if toggle.X < input.X+input.W-1 || toggle.X+toggle.W > 302 {
		t.Fatalf("password layout: input=%+v toggle=%+v", input, toggle)
	}
	if err := tt.Click("显示 API Key 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("隐藏 API Key 1"); !ok {
		t.Fatal("show did not update toggle")
	}
	if secret != "secret-value" {
		t.Fatal("visibility changed value")
	}
	if err := tt.Click("隐藏 API Key 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("显示 API Key 1"); !ok {
		t.Fatal("hide did not update toggle")
	}
}

func TestPasswordInputReopenStartsHidden(t *testing.T) {
	secret, open := "secret", true
	tt := chineseTester(func(c *ui.Context) {
		if open {
			PasswordInput(c, "secret", "密钥", &secret).Width(200)
		}
	}, 300, 100)
	if err := tt.Click("显示 密钥"); err != nil {
		t.Fatal(err)
	}
	open = false
	tt.Frame()
	open = true
	tt.Frame()
	if _, ok := tt.Find("显示 密钥"); !ok {
		t.Fatal("reopened password is still visible")
	}
}
