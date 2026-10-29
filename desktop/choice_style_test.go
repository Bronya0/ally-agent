package desktop

import (
	"ally-dev/desktop/uikit"
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestChoiceGroupsWrap(t *testing.T) {
	for _, appearance := range []string{"dark", "light"} {
		t.Run(appearance, func(t *testing.T) {
			a := &desktop{}
			a.prefs.Appearance = appearance
			if appearance == "light" {
				a.settings.AppearanceMode = 1
			}
			selected := 1
			labels := []string{"大眼睛", "小机器人", "GPT 龙娘", "Claude 橙花娘", "DeepSeek 女仆"}
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				ui.Column(c).Fill().Padding(16).Gap(12).Background(c.Theme().Background).Children(func() {
					uikit.Segmented(c.Key("appearance"), &a.settings.AppearanceMode, "深色", "浅色")
					uikit.OptionGroup(c.Key("palettes")).Children(func() {
						for i, label := range []string{"琥珀", "海洋", "森林", "紫罗兰"} {
							choiceSwatch(c, fmt.Sprint(i), label, i == 1, palettes[i+2].accent)
						}
					})
					uikit.OptionGroup(c.Key("avatars")).Children(func() {
						for i, label := range labels {
							choiceButton(c, fmt.Sprint(i), label, i == selected, c.Theme().Accent).OnClick(func() { selected = i })
						}
					})
				})
			}, 380, 240)
			for _, label := range labels {
				r, ok := tt.Find(label)
				if !ok || r.X < 16 || r.X+r.W > 365 {
					t.Fatalf("choice outside content: %s %v", label, r)
				}
			}
			if err := tt.Click(labels[4]); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if selected != 4 {
				t.Fatal("choice did not select")
			}
			if os.Getenv("ALLY_CHOICES_VISUAL") != "" {
				var b bytes.Buffer
				if err := png.Encode(&b, tt.Image()); err != nil {
					t.Fatal(err)
				}
				fmt.Println("CHOICE_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
			}
		})
	}
}
