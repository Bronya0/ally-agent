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

// The transcript is not necessarily at its final size when it first opens:
// wrapping, the restored draft and the composer can change the viewport later.
func TestRestoredSessionFollowsBottomAfterLayoutChanges(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	conv := &conversation{ID: "restore-layout", Workspace: "ws", Messages: restoredHistory(40)}
	conv.Messages = append(conv.Messages, map[string]any{
		"role": "assistant", "content": "## Result\n\n" + strings.Repeat("A paragraph that wraps as the chat gets narrower. ", 30) + "\n\n```go\nfunc main() {\n    println(\"done\")\n}\n```",
	})
	a.showSession(conv)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		width, _ := c.Size()
		ui.Column(c).Fill().Children(func() { a.chatColumn(c, width) })
	}, 900, 700)
	assertBottom := func() {
		t.Helper()
		if conv.Scroll.MaxY <= 0 || conv.Scroll.Y < conv.Scroll.MaxY-2 || !conv.uiState().Follow {
			t.Fatalf("restored transcript left the bottom after layout: y=%v maxY=%v follow=%v", conv.Scroll.Y, conv.Scroll.MaxY, conv.uiState().Follow)
		}
	}
	tt.Frame()
	assertBottom()
	tt.SetSize(600, 450)
	assertBottom()
	conv.Draft = strings.Repeat("restored draft\n", 5)
	tt.Frame()
	assertBottom()
	tt.SetSize(1000, 800)
	assertBottom()
}

// A larger viewport can clamp the offset to zero. That is layout, not the
// reader scrolling up, and following must resume when the viewport shrinks.
func TestRestoredSessionFollowsThroughNonOverflowingLayout(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "restore-fit", Workspace: "ws", Messages: restoredHistory(6)}
	a.showSession(conv)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		a.chatMessages(c, conv, 900)
	}, 900, 200)
	tt.SetSize(900, 1600)
	if conv.Scroll.MaxY != 0 || !conv.uiState().Follow {
		t.Fatalf("fitting the transcript stopped following: maxY=%v follow=%v", conv.Scroll.MaxY, conv.uiState().Follow)
	}
	tt.SetSize(900, 200)
	if conv.Scroll.MaxY <= 0 || conv.Scroll.Y < conv.Scroll.MaxY-2 || !conv.uiState().Follow {
		t.Fatalf("shrinking the viewport lost the bottom: y=%v maxY=%v follow=%v", conv.Scroll.Y, conv.Scroll.MaxY, conv.uiState().Follow)
	}
}

func TestRestoredSessionDoesNotFollowAfterReaderScrollsUp(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "restore-reader", Workspace: "ws", Messages: restoredHistory(60)}
	a.showSession(conv)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		width, _ := c.Size()
		a.chatMessages(c, conv, width)
	}, 900, 400)
	tt.Scroll(450, 200, 0, -150)
	before := conv.Scroll.Y
	if conv.uiState().Follow || before >= conv.Scroll.MaxY-2 {
		t.Fatal("scrolling up did not stop following the transcript")
	}
	tt.SetSize(600, 300)
	if conv.uiState().Follow || conv.Scroll.Y != before {
		t.Fatalf("layout stole the reader's scroll position: y=%v want=%v follow=%v", conv.Scroll.Y, before, conv.uiState().Follow)
	}
	followLatest(conv)
	tt.Frame()
	if !conv.uiState().Follow || conv.Scroll.Y < conv.Scroll.MaxY-2 {
		t.Fatalf("jumping back to latest did not resume following: y=%v maxY=%v", conv.Scroll.Y, conv.Scroll.MaxY)
	}
}
