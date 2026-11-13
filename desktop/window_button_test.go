package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCaptionButtonsFillTitleBarHeight(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := ui.LightTheme()
		if dark {
			theme = ui.DarkTheme()
		}
		background := theme.Surface.Mix(theme.Accent, 0.10)
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(theme)
			ui.Row(c).Height(48).Background(background).Children(func() {
				windowButton(c, "min", "最小化", "min", background, false, func() {})
				windowButton(c, "max", "最大化", "max", background, false, func() {})
				windowButton(c, "close", "关闭窗口", "close", background, true, func() {})
			})
		}, 300, 100)
		close, _ := tt.Find("关闭窗口")
		if close.Y != 0 || close.H != 48 {
			t.Fatalf("caption does not fill title bar: %v", close)
		}
	}
}
