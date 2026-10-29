package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func restoredHistory(n int) []map[string]any {
	messages := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		messages = append(messages, map[string]any{"role": "user", "content": fmt.Sprintf("question %d %s", i, strings.Repeat("word ", 20))})
	}
	return messages
}

// A history loaded from disk carries no scroll offset, and a scroll container
// shows its start at 0: restoring a session has to land on the newest message
// instead of the oldest one.
func TestRestoredSessionOpensAtNewestMessage(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	conv := &conversation{ID: "restore", Workspace: "ws", Messages: restoredHistory(60)}
	a.showSession(conv)
	a.tabs, a.active = []*conversation{conv}, 0
	st := conv.uiState()
	if !st.Follow || !st.FollowPending || st.VisibleStart != 0 {
		t.Fatalf("restored conversation does not follow its newest message: follow=%v pending=%v start=%d", st.Follow, st.FollowPending, st.VisibleStart)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		a.chatMessages(c, conv, 900)
	}, 900, 400)
	tt.Frame()
	tt.Frame()
	if conv.Scroll.MaxY <= 0 {
		t.Fatalf("history did not overflow the viewport: maxY=%v", conv.Scroll.MaxY)
	}
	if conv.Scroll.Y < conv.Scroll.MaxY-2 {
		t.Fatalf("restored transcript opened at %v of %v", conv.Scroll.Y, conv.Scroll.MaxY)
	}
}

// Restoring a conversation the user left scrolled up must keep that place: only
// a transcript without any position gets pinned to its end.
func TestRestoredSessionKeepsPositionTheUserLeft(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "kept", Workspace: "ws", Messages: restoredHistory(60)}
	conv.Scroll.Y = 120
	st := conv.uiState()
	st.Follow = false
	st.VisibleStart = 5
	a.showSession(conv)
	if st.FollowPending || conv.Scroll.Y != 120 || st.VisibleStart != 5 {
		t.Fatalf("restore moved a scrolled transcript: y=%v pending=%v start=%d", conv.Scroll.Y, st.FollowPending, st.VisibleStart)
	}
}
