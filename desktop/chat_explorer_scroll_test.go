package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestExplorerTogglePreservesChatScroll(t *testing.T) {
	for _, reading := range []bool{false, true} {
		a := &desktop{prefs: defaultPrefs()}
		conv := &conversation{ID: "explorer-scroll", Workspace: t.TempDir(), Messages: restoredHistory(60)}
		a.showSession(conv)
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			ui.Column(c).Fill().Children(func() { a.chatPage(c, 900) })
		}, 900, 500)
		tt.Frame()
		if reading {
			tt.Scroll(450, 200, 0, -200)
			tt.Frame()
		}
		first, _ := conv.uiState().List.Visible()
		for _, open := range []bool{true, false} {
			a.explorer = open
			tt.Frame()
			tt.Frame()
			if reading {
				got, _ := conv.uiState().List.Visible()
				if conv.uiState().Follow || got != first {
					t.Fatalf("open=%v: reader moved from row %d to %d, follow=%v", open, first, got, conv.uiState().Follow)
				}
			} else if !conv.uiState().Follow || !conv.uiState().List.AtEnd() || conv.Scroll.Y < conv.Scroll.MaxY-2 {
				t.Fatalf("open=%v: left bottom: follow=%v y=%v max=%v", open, conv.uiState().Follow, conv.Scroll.Y, conv.Scroll.MaxY)
			}
		}
	}
}
