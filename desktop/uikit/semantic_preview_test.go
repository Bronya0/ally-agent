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

func TestSemanticPreview(t *testing.T) {
	if os.Getenv("ALLY_SEMANTIC_PREVIEW") == "" {
		t.Skip("visual gallery")
	}
	for _, dark := range []bool{false, true} {
		selected, on, off := 0, true, false
		tt := ui.NewTester(func(c *ui.Context) {
			theme := ui.LightTheme()
			if dark {
				theme = ui.DarkTheme()
			}
			theme.Accent = ui.Hex("#18a079")
			if dark {
				theme.Accent = ui.Hex("#63cba6")
			}
			c.SetTheme(theme)
			c.DisableAnimations()
			ui.Column(c).Fill().Padding(24).Gap(16).Background(theme.Background).Children(func() {
				ui.Text(c, "Buttons · Secondary / Tertiary / Ghost / Quaternary").TextColor(theme.Text)
				for style := Secondary; style <= Quaternary; style++ {
					ui.Row(c.Key(style)).Gap(12).Children(func() {
						for tone := Default; tone <= Error; tone++ {
							Action(c.Key(tone), []string{"Default", "Primary", "Info", "Success", "Warning", "Error"}[tone], ActionOptions{Tone: tone, Style: style})
						}
					})
				}
				ui.Row(c).Gap(12).Children(func() {
					Action(c.Key("selected"), "Selected", ActionOptions{Tone: Primary, Style: Quaternary, Selected: true})
					Action(c, "Round", ActionOptions{Tone: Success, Round: true})
					Action(c, "Loading", ActionOptions{Tone: Info, Loading: true})
					Action(c, "Disabled", ActionOptions{Tone: Primary, Disabled: true})
				})
				ui.Text(c, "Tags").TextColor(theme.Text)
				for _, round := range []bool{false, true} {
					ui.Row(c.Key(round)).Gap(12).Children(func() {
						for tone := Default; tone <= Error; tone++ {
							ColorTag(c.Key(tone), []string{"Default", "Primary", "Info", "Success", "Warning", "Error"}[tone], TagOptions{Tone: tone, Round: round, OnClose: func() {}})
						}
					})
				}
				opts := []RadioOption[int]{{0, "Rock’n’Roll Star", false}, {1, "Shakermaker", false}, {2, "Live Forever", true}}
				Radios(c.Key("radios"), &selected, opts, false)
				Radios(c.Key("buttons"), &selected, opts, true)
				ui.Row(c).Gap(16).Children(func() {
					Switch(c.Key("on"), &on, "On")
					Switch(c.Key("off"), &off, "Off")
					Switch(c.Key("disabled"), &on, "Disabled").Disabled(true)
				})
			})
			Messages(c)
		}, 800, 640)
		var b bytes.Buffer
		if err := png.Encode(&b, tt.Image()); err != nil {
			t.Fatal(err)
		}
		fmt.Println("SEMANTIC_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
	}
	for _, dark := range []bool{false, true} {
		sent := false
		tt := ui.NewTester(func(c *ui.Context) {
			theme := ui.LightTheme()
			if dark {
				theme = ui.DarkTheme()
			}
			c.SetTheme(theme)
			ui.Box(c).Fill().Background(theme.Background)
			if !sent {
				sent = true
				c.AddToast(ui.Toast{Title: "保存成功，配置已更新", Type: "success", Timeout: -1})
				c.AddToast(ui.Toast{Title: "连接失败，请检查服务器地址", Type: "error", Timeout: -1})
				c.AddToast(ui.Toast{Title: "正在测试连接…", Type: "loading", Timeout: -1})
			}
			Messages(c)
		}, 640, 250)
		tt.Frame()
		var b bytes.Buffer
		if err := png.Encode(&b, tt.Image()); err != nil {
			t.Fatal(err)
		}
		fmt.Println("SEMANTIC_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
	}

}
