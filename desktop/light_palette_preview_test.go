package desktop

import (
	"ally-dev/desktop/uikit"
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"testing"
)

func TestLightPalettePreview(t *testing.T) {
	if os.Getenv("ALLY_LIGHT_PREVIEW") == "" {
		t.Skip("visual preview")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			for _, index := range []int{0, 2, 3, 4, 5} {
				a := &desktop{}
				a.prefs.Palette = index
				a.prefs.Appearance = "light"
				theme := a.theme()
				c.SetTheme(theme)
				c.DisableAnimations()
				ui.Column(c.Key(index)).FillWidth().Padding(14).Gap(10).Background(theme.Background).Children(func() {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, palettes[index].name).Width(70).TextColor(theme.Text)
						flat(c, "导入")
						flat(c, "刷新")
						primary(c, "添加")
						n := 0
						uikit.Segmented(c.Key("mode"), &n, "已选择", "未选择")
					})
					ui.Row(c).FillWidth().Padding(8).Gap(14).AlignItems(ui.Center).Background(theme.Surface).Border(1, theme.Border).Radius(7).Children(func() {
						ui.Text(c, "context7").Grow(1).TextColor(theme.Text)
						statusBadge(c, "已连接", "ok")
						on := true
						uikit.Switch(c, &on, "启用")
						cardFlat(c, "编辑")
						cardDanger(c, "删除")
					})
				})
			}
		})
	}, 800, 650)
	var b bytes.Buffer
	if err := png.Encode(&b, tt.Image()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("LIGHT_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
}
