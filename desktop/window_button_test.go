package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestCaptionButtonsPaintFullHeightWithoutWhiteBacking(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := ui.LightTheme()
		if dark {
			theme = ui.DarkTheme()
		}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(theme)
			ui.Row(c).Height(48).Background(theme.Surface).Children(func() {
				windowButton(c, "min", "最小化", "min", false, func() {})
				windowButton(c, "max", "最大化", "max", false, func() {})
				windowButton(c, "close", "关闭窗口", "close", true, func() {})
			})
		}, 300, 100)
		close, _ := tt.Find("关闭窗口")
		if close.Y != 0 || close.H != 48 {
			t.Fatalf("caption does not fill title bar: %v", close)
		}
		tt.Move(close.X+close.W/2, close.Y+close.H/2)
		for i := 0; i < 12; i++ {
			tt.Advance(16 * time.Millisecond)
			if dark {
				for _, label := range []string{"最小化", "最大化", "关闭窗口"} {
					r, _ := tt.Find(label)
					for _, y := range []int{0, int(r.H) - 1} {
						v := tt.Image().RGBAAt(int(r.X+2), y)
						if v.R > 180 && v.G > 180 && v.B > 180 {
							t.Fatalf("white backing during hover: %s frame=%d color=%v", label, i, v)
						}
					}
				}
			}
		}
		tt.Move(250, 80)
		tt.Advance(200 * time.Millisecond)
		for _, label := range []string{"最小化", "最大化", "关闭窗口"} {
			r, _ := tt.Find(label)
			v := tt.Image().RGBAAt(int(r.X+2), 0)
			if v.R != theme.Surface.R || v.G != theme.Surface.G || v.B != theme.Surface.B {
				t.Fatalf("caption did not restore surface: %v", v)
			}
		}
	}
}
