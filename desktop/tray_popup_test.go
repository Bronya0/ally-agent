// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDrawnTrayMenuChoosesEachActionOnce(t *testing.T) {
	for action, label := range []string{"打开主窗口", "检查更新", "退出 Ally"} {
		t.Run(label, func(t *testing.T) {
			open := true
			var calls [3]int
			width, height := uikit.TrayMenuInitialSize(3, 2)
			tt := ui.NewTester(func(c *ui.Context) {
				c.DisableAnimations()
				if choice := renderTrayMenu(c, &open); choice >= 0 {
					calls[choice]++
				}
			}, width, height)
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if open || tt.HasText(label) {
				t.Fatal("choosing a tray action left the custom menu open")
			}
			for i, count := range calls {
				want := 0
				if i == action {
					want = 1
				}
				if count != want {
					t.Fatalf("choice %d called action %d %d times, want %d", action, i, count, want)
				}
			}
		})
	}
}

func TestDrawnTrayMenuKeyboardAndDismissal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		keys   []ui.Key
		choice int
	}{
		{"first", []ui.Key{ui.KeyHome, ui.KeyEnter}, 0},
		{"update", []ui.Key{ui.KeyHome, ui.KeyDown, ui.KeyEnter}, 1},
		{"last", []ui.Key{ui.KeyEnd, ui.KeySpace}, 2},
		{"escape", []ui.Key{ui.KeyEscape}, -1},
		{"tab", []ui.Key{ui.KeyTab}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			open, choice := true, -1
			width, height := uikit.TrayMenuInitialSize(3, 2)
			tt := ui.NewTester(func(c *ui.Context) {
				c.DisableAnimations()
				if got := renderTrayMenu(c, &open); got >= 0 {
					choice = got
				}
			}, width, height)
			for _, key := range tc.keys {
				tt.Key(0, key)
			}
			tt.Frame()
			if open || choice != tc.choice {
				t.Fatalf("keyboard dismissal: open=%v choice=%d, want false/%d", open, choice, tc.choice)
			}
		})
	}
	open := true
	width, height := uikit.TrayMenuInitialSize(3, 2)
	tt := ui.NewTester(func(c *ui.Context) { renderTrayMenu(c, &open) }, width, height)
	tt.ClickAt(1, 1) // transparent shadow margin, outside the menu surface
	if open {
		t.Fatal("pressing outside the menu surface did not dismiss it")
	}
}

func TestDrawnTrayMenuCompactLayoutAndHover(t *testing.T) {
	open := true
	width, height := uikit.TrayMenuInitialSize(3, 2)
	tt := ui.NewTester(func(c *ui.Context) { renderTrayMenu(c, &open) }, width, height)
	menu, found := tt.AccessByRole(ui.RoleMenu, "Ally")
	if !found || menu.Bounds.X != 8 || menu.Bounds.Y != 8 || menu.Bounds.W != 140 || menu.Bounds.H != 94 {
		t.Fatalf("unexpected compact menu footprint: %+v", menu.Bounds)
	}
	var rows [3]ui.Rect
	for i, label := range []string{"打开主窗口", "检查更新", "退出 Ally"} {
		node, found := tt.AccessByRole(ui.RoleMenuItem, label)
		if !found || node.Bounds.H != 28 || node.Bounds.W != 130 {
			t.Fatalf("missing or incorrectly sized action %q: %+v", label, node.Bounds)
		}
		rows[i] = node.Bounds
		if i > 0 && rows[i].Y-rows[i-1].Y != 29 {
			t.Fatal("action groups must have a single-pixel, non-interactive separator")
		}
	}
	// The root must remain transparent; only the menu and its soft shadow draw.
	if got := tt.Image().RGBAAt(0, 0); got.A != 0 {
		t.Fatalf("transparent flyout inherited an opaque window background: %+v", got)
	}
	x, y := int(rows[1].X+rows[1].W-8), int(rows[1].Y+rows[1].H/2)
	before := tt.Image().RGBAAt(x, y)
	tt.Move(float32(x), float32(y))
	tt.Advance(70 * time.Millisecond)
	mid := tt.Image().RGBAAt(x, y)
	tt.Advance(200 * time.Millisecond)
	after := tt.Image().RGBAAt(x, y)
	if before == mid || mid == after || after != (color.RGBA{R: 237, G: 237, B: 237, A: 255}) {
		t.Fatalf("hover did not fade to the neutral reference fill: before=%+v mid=%+v after=%+v", before, mid, after)
	}
	for i, label := range []string{"打开主窗口", "检查更新", "退出 Ally"} {
		node, _ := tt.AccessByRole(ui.RoleMenuItem, label)
		if node.Bounds != rows[i] {
			t.Fatal("hover moved the menu's labels or changed row geometry")
		}
	}
}

func TestDrawnTrayMenuDPIKeepsCompactDIPSize(t *testing.T) {
	useTestLocale(t, "zh-Hans")
	for _, scale := range []float32{1, 1.25, 1.5, 2} {
		open := true
		var fontSize float32
		width, height := uikit.TrayMenuInitialSize(3, 2)
		tt := ui.NewTester(func(c *ui.Context) {
			renderTrayMenu(c, &open)
			fontSize = c.Theme().FontSize
		}, width, height)
		tt.SetScale(scale)
		menu, found := tt.AccessByRole(ui.RoleMenu, "Ally")
		compact := ui.Rect{X: 8, Y: 8, W: 140, H: 94}
		if !found || menu.Bounds != compact || fontSize != 12 {
			t.Fatalf("DPI %g changed the logical menu size or font: menu=%+v font=%g", scale, menu.Bounds, fontSize)
		}
		// Text accessibility scaling is distinct from monitor DPI. It still
		// enlarges text, and resetting it restores the compact menu footprint.
		tt.SetPreferences(ui.Preferences{TextScale: 1.5, ReduceMotion: true})
		if fontSize != 18 {
			t.Fatalf("DPI %g lost the system large-text preference: font=%g", scale, fontSize)
		}
		tt.SetPreferences(ui.Preferences{TextScale: 1, ReduceMotion: true})
		menu, found = tt.AccessByRole(ui.RoleMenu, "Ally")
		if !found || menu.Bounds != compact || fontSize != 12 {
			t.Fatalf("DPI %g did not restore compact typography: menu=%+v font=%g", scale, menu.Bounds, fontSize)
		}
		if err := tt.Click("检查更新"); err != nil {
			t.Fatal(err)
		}
		if open {
			t.Fatalf("DPI %g broke tray action selection", scale)
		}
	}
}

func TestDrawnTrayMenuFitsTranslatedLabelsAndLargerText(t *testing.T) {
	for _, tag := range []string{"zh-Hans", "en", "ja", "ko"} {
		t.Run(tag, func(t *testing.T) {
			useTestLocale(t, tag)
			open := true
			var labelWidths [3]float32
			tt := ui.NewTester(func(c *ui.Context) {
				renderTrayMenu(c, &open)
				for i, source := range []string{"打开主窗口", "检查更新", "退出 Ally"} {
					labelWidths[i], _ = c.MeasureText(0, ui.Span{Text: locales.Text(source), Font: c.Theme().Font, Size: c.Theme().FontSize})
				}
			}, 500, 240)
			tt.SetLocale(tag)
			tt.SetPreferences(ui.Preferences{TextScale: 1.5, ReduceMotion: true})
			menu, found := tt.AccessByRole(ui.RoleMenu, "Ally")
			if !found || menu.Bounds.X < 0 || menu.Bounds.Y < 0 || menu.Bounds.X+menu.Bounds.W > 500 || menu.Bounds.Y+menu.Bounds.H > 240 {
				t.Fatalf("translated menu exceeded its window: %+v", menu.Bounds)
			}
			for i, source := range []string{"打开主窗口", "检查更新", "退出 Ally"} {
				label := locales.Text(source)
				row, found := tt.AccessByRole(ui.RoleMenuItem, label)
				// Row padding (16), the icon (16) and its gap (6) leave the
				// real shaped label this much space; Find(label) finds the row.
				if !found || row.Bounds.W-38 < labelWidths[i] || row.Bounds.Y+row.Bounds.H > menu.Bounds.Y+menu.Bounds.H-4 {
					t.Fatalf("translated label %q does not fit: label width=%v row=%+v menu=%+v", label, labelWidths[i], row.Bounds, menu.Bounds)
				}
			}
			if err := tt.Click(locales.Text("检查更新")); err != nil {
				t.Fatal(err)
			}
			if open {
				t.Fatal("translated menu did not retain action selection")
			}
		})
	}
}

func TestStaleTrayMenuCannotQueueAgainstANewPopup(t *testing.T) {
	old := &trayMenuState{open: true}
	current := &trayMenuState{open: true}
	a := &desktop{trayMenu: current}
	a.finishTrayMenu(old, 2)
	if old.pending || a.trayMenu != current || !current.open {
		t.Fatal("stale popup callback affected its replacement")
	}
	a.closeTrayMenu()
	if a.trayMenu != nil || current.open {
		t.Fatal("closing the menu did not clear its state")
	}
	a.closeTrayMenu() // repeated dismissal is harmless
}
