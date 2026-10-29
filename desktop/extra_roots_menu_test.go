package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestExtraRootsMenuOpensAboveComposer(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "extra", Workspace: "C:/work/project"}
	conv.uiState().ExtraRootsOpen = true
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() { ui.Box(c).Absolute().Left(30).Bottom(24).Children(func() { a.extraRootsButton(c, conv) }) })
	}, 700, 450)
	anchor, ok := tt.Find("附加目录")
	if !ok {
		t.Fatal("missing anchor")
	}
	panel, ok := tt.Find("附加工作区菜单")
	if !ok {
		t.Fatal("missing menu")
	}
	if panel.Y+panel.H > anchor.Y || panel.Y < 0 {
		t.Fatalf("menu must open upward: menu=%v anchor=%v", panel, anchor)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if conv.uiState().ExtraRootsOpen {
		t.Fatal("Escape did not close menu")
	}
}
