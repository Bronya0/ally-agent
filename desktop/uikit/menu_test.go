package uikit

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestContextMenuFitsLabelsAndShortcuts(t *testing.T) {
	for _, size := range []float32{12, 16, 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			const label = "复制完整路径与文件名称"
			var labelWidth, shortcutWidth float32
			chosen := false
			tt := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				theme.FontSize = size
				c.SetTheme(theme)
				labelWidth, _ = c.MeasureText(0, ui.Span{Text: label, Size: size})
				shortcutWidth, _ = c.MeasureText(0, ui.Span{Text: "Ctrl+Shift+C", Size: size})
				anchor := ui.Box(c.Key("menu-area")).Fill().Label("menu-area")
				ContextMenu(anchor, func(m *ui.Menu) {
					if m.Item(label).Shortcut(ui.Ctrl|ui.Shift, ui.KeyC).Chosen() {
						chosen = true
					}
					m.Item("新建文件")
					m.Item("新建目录")
					m.Item("刷新")
				})
			}, 700, 400)
			// Near the edge, the menu must move inward instead of clipping.
			tt.RightClickAt(690, 390)
			row, ok := tt.Find(label)
			if !ok {
				t.Fatal("menu did not open")
			}
			if row.W < labelWidth+shortcutWidth+16 || row.X < 0 || row.X+row.W > 700 || row.Y+row.H > 400 {
				t.Fatalf("label/shortcut do not fit: row=%+v text=%v shortcut=%v", row, labelWidth, shortcutWidth)
			}
			tt.Key(0, ui.KeyEscape)
			if tt.HasText(label) {
				t.Fatal("Escape did not dismiss menu")
			}
			tt.RightClickAt(100, 100)
			tt.Key(0, ui.KeyHome)
			tt.Key(0, ui.KeyEnter)
			if !chosen || tt.HasText(label) {
				t.Fatal("keyboard selection did not run action and close menu")
			}
			tt.RightClickAt(100, 100)
			tt.ClickAt(10, 10)
			if tt.HasText(label) {
				t.Fatal("outside click did not dismiss menu")
			}
		})
	}
}
