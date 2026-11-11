package desktop

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func newChatScrollTester(t *testing.T) (*conversation, *ui.Tester) {
	t.Helper()
	a := &desktop{}
	conv := &conversation{ID: "scroll", Workspace: t.TempDir(), Messages: restoredHistory(60)}
	a.showSession(conv)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		width, _ := c.Size()
		a.chatMessages(c, conv, width)
	}, 900, 400)
	tt.Frame()
	if conv.Scroll.MaxY <= 0 || conv.Scroll.Y < conv.Scroll.MaxY-2 {
		t.Fatalf("transcript did not start at the bottom: y=%v max=%v", conv.Scroll.Y, conv.Scroll.MaxY)
	}
	return conv, tt
}

// Mouse wheels glide over later frames; unlike touchpads, their first event
// need not change the offset before the transcript builds again.
func TestChatMouseWheelLeavesLatestMessage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		x, dy   float32
		precise bool
	}{
		{name: "left-inset", x: 20, dy: -120},
		{name: "message-center", x: 450, dy: -120},
		{name: "right-inset", x: 880, dy: -120},
		{name: "tiny-wheel", x: 450, dy: -1},
		{name: "touchpad", x: 450, dy: -120, precise: true},
		{name: "tiny-touchpad", x: 450, dy: -1, precise: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, tt := newChatScrollTester(t)
			before := conv.Scroll.Y
			scroll := tt.Wheel
			if tc.precise {
				scroll = tt.Scroll
			}
			scroll(tc.x, 200, 0, tc.dy)
			if conv.uiState().Follow || conv.uiState().FollowPending {
				t.Fatal("first upward event did not stop following")
			}
			tt.Advance(200 * time.Millisecond)
			if conv.uiState().Follow || conv.Scroll.Y >= before+tc.dy/2 {
				t.Fatalf("scroll was overridden by following: y=%v before=%v follow=%v", conv.Scroll.Y, before, conv.uiState().Follow)
			}
			position := conv.Scroll.Y
			tt.Frame()
			if conv.uiState().Follow || conv.Scroll.Y != position {
				t.Fatal("next frame stole the reader's scroll position")
			}
			scroll(tc.x, 200, 0, -tc.dy)
			tt.Advance(200 * time.Millisecond)
			if !conv.uiState().Follow || conv.Scroll.Y < conv.Scroll.MaxY-2 {
				t.Fatalf("scrolling back to bottom did not resume following: y=%v max=%v follow=%v", conv.Scroll.Y, conv.Scroll.MaxY, conv.uiState().Follow)
			}
		})
	}
}

func TestChatScrollAfterStreamingGrowth(t *testing.T) {
	for _, precise := range []bool{false, true} {
		name := "mouse-wheel"
		if precise {
			name = "touchpad"
		}
		t.Run(name, func(t *testing.T) {
			conv, tt := newChatScrollTester(t)
			beforeGrowth := conv.Scroll.Y
			conv.Messages[len(conv.Messages)-1]["content"] = strings.Repeat("streamed words ", 200)
			tt.Frame()
			if !conv.uiState().Follow || conv.Scroll.Y <= beforeGrowth || conv.Scroll.Y < conv.Scroll.MaxY-2 {
				t.Fatalf("streaming growth did not stay pinned: y=%v before=%v max=%v", conv.Scroll.Y, beforeGrowth, conv.Scroll.MaxY)
			}
			// Tester.Frame settles multiple renders. Recreate the baseline from
			// before growth, as a window can receive input just after layout
			// pins the new bottom but before the next render records its offset.
			conv.uiState().LastScrollY = beforeGrowth
			beforeScroll := conv.Scroll.Y
			scroll := tt.Wheel
			if precise {
				scroll = tt.Scroll
			}
			scroll(450, 200, 0, -1)
			tt.Advance(200 * time.Millisecond)
			if conv.uiState().Follow || conv.Scroll.Y >= beforeScroll-0.5 {
				t.Fatalf("first scroll after growth was overridden: y=%v before=%v follow=%v", conv.Scroll.Y, beforeScroll, conv.uiState().Follow)
			}
			position := conv.Scroll.Y
			last := conv.Messages[len(conv.Messages)-1]
			last["content"] = str(last, "content") + strings.Repeat(" more streamed words", 200)
			tt.Frame()
			if conv.uiState().Follow || conv.Scroll.Y != position {
				t.Fatalf("later streaming stole the reader's position: y=%v want=%v follow=%v", conv.Scroll.Y, position, conv.uiState().Follow)
			}
			followLatest(conv)
			tt.Frame()
			if !conv.uiState().Follow || conv.Scroll.Y < conv.Scroll.MaxY-2 {
				t.Fatalf("jumping to latest did not resume following: y=%v max=%v follow=%v", conv.Scroll.Y, conv.Scroll.MaxY, conv.uiState().Follow)
			}
		})
	}
}
