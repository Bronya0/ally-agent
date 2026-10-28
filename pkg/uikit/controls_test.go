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
