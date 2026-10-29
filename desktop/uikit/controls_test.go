package uikit

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"testing"
)

func TestSelectAndTabs(t *testing.T) {
	value := "API Key"
	tab := 0
	tt := ui.NewTester(func(c *ui.Context) {
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

func TestSegmentedSelection(t *testing.T) {
	for _, light := range []bool{false, true} {
		t.Run(fmt.Sprint("light=", light), func(t *testing.T) {
			selected, changes := 0, 0
			tt := ui.NewTester(func(c *ui.Context) {
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

func TestSegmentedVisual(t *testing.T) {
	if os.Getenv("ALLY_SEGMENTED_VISUAL") == "" {
		t.Skip("optional visual snapshot")
	}
	appearance, proxy := 0, 1
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			for _, light := range []bool{false, true} {
				theme := ui.DarkTheme()
				if light {
					theme = ui.LightTheme()
				}
				theme.Accent = ui.Hex("#d4a574")
				c.SetTheme(theme)
				ui.Column(c.Key(light)).FillWidth().Padding(20).Gap(12).Background(theme.Surface).Children(func() {
					Segmented(c.Key("appearance"), &appearance, "深色", "浅色")
					Segmented(c.Key("proxy"), &proxy, "关闭代理", "跟随系统代理", "手动代理")
				})
			}
		})
	}, 520, 280)
	tt.Frame()
	var b bytes.Buffer
	if err := png.Encode(&b, tt.Image()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("UI_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
}

func TestControlVisual(t *testing.T) {
	if os.Getenv("ALLY_UI_VISUAL") == "" {
		t.Skip("optional visual snapshot")
	}
	value := "API Key 认证"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(ui.DarkTheme())
		c.DisableAnimations()
		ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
			FieldLabel(c, "认证方式")
			Select(c.Key("auth"), &value, []string{"API Key 认证", "ChatGPT 登录"}).FillWidth().Label("认证方式选择")
		})
	}, 720, 260)
	tt.Frame()
	if err := tt.Click("认证方式选择"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	var b bytes.Buffer
	if err := png.Encode(&b, tt.Image()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("UI_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
}

func TestFormFieldsHaveGap(t *testing.T) {
	left, right := "", ""
	tt := ui.NewTester(func(c *ui.Context) {
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

func TestNumberInputFillsWidthAndSteps(t *testing.T) {
	value := 8192.0
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(ui.DarkTheme())
		ui.Column(c).Width(300).Children(func() { NumberInput(c, "number", "Max Tokens", &value, 0, 10000000, 1).FillWidth() })
	}, 360, 120)
	input, ok := tt.Find("Max Tokens")
	if !ok {
		t.Fatal("missing input")
	}
	minus, ok := tt.Find("减少 Max Tokens")
	if !ok {
		t.Fatal("missing decrement")
	}
	plus, ok := tt.Find("增加 Max Tokens")
	if !ok {
		t.Fatal("missing increment")
	}
	if input.W < 200 || minus.X < input.X+input.W-1 || plus.X < minus.X {
		t.Fatalf("number layout: input=%+v minus=%+v plus=%+v", input, minus, plus)
	}
	if err := tt.Click("增加 Max Tokens"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if value != 8193 {
		t.Fatalf("increment: %v", value)
	}
	if err := tt.Click("减少 Max Tokens"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if value != 8192 {
		t.Fatalf("decrement: %v", value)
	}
	if err := tt.Click("Max Tokens"); err != nil {
		t.Fatal(err)
	}
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

func TestPasswordInputVisibilityToggle(t *testing.T) {
	secret := "secret-value"
	tt := ui.NewTester(func(c *ui.Context) {
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
	tt := ui.NewTester(func(c *ui.Context) {
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
