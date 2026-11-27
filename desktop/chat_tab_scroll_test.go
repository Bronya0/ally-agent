package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A hidden transcript keeps receiving events, but its last pixel offset was
// measured before the answer grew. Returning must respect the reader's intent.
func TestChatReturnAfterBackgroundGrowth(t *testing.T) {
	for _, tc := range []struct {
		name                string
		reading, busy, page bool
	}{
		{name: "completed"},
		{name: "still-running", busy: true},
		{name: "reading-history", reading: true},
		{name: "other-page", page: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := &conversation{ID: "first", Workspace: t.TempDir(), Messages: restoredHistory(20), Busy: true}
			second := &conversation{ID: "second", Workspace: t.TempDir(), Messages: restoredHistory(6)}
			a := &desktop{page: "chat", tabs: []*conversation{first, second}}
			followLatest(first)
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				if a.page != "chat" {
					ui.Text(c, "Other page")
					return
				}
				width, _ := c.Size()
				a.chatMessages(c, a.current(), width)
			}, 900, 400)
			tt.Frame()
			if !first.uiState().List.AtEnd() || first.Scroll.MaxY <= 0 {
				t.Fatal("first tab did not start at the end of an overflowing transcript")
			}
			if tc.reading {
				tt.Scroll(450, 200, 0, -200)
				tt.Frame()
			}
			row, _ := first.uiState().List.Visible()
			if tc.page {
				a.page = "other"
			} else {
				a.activateTab(1)
			}
			tt.Frame()
			tt.Frame()
			a.applyEvent("run:delta", map[string]any{"sessionId": first.ID, "content": strings.Repeat("background answer\n\n", 40)})
			first.Busy = tc.busy
			if !tc.busy {
				first.uiState().Round[len(first.Messages)-1] = &roundStat{Done: true}
			}
			tt.Frame()
			if tc.page {
				a.page = "chat"
			} else {
				a.activateTab(0)
			}
			tt.Frame()
			tt.Frame()
			if tc.reading {
				got, _ := first.uiState().List.Visible()
				if first.uiState().Follow || got != row {
					t.Fatalf("returning moved the reader from row %d to %d, follow=%v", row, got, first.uiState().Follow)
				}
			} else if !first.uiState().Follow || !first.uiState().List.AtEnd() || first.Scroll.Y < first.Scroll.MaxY-2 {
				t.Fatalf("returning missed the latest answer: follow=%v y=%v max=%v", first.uiState().Follow, first.Scroll.Y, first.Scroll.MaxY)
			}
		})
	}
}
