package desktop

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
	"time"
)

func TestUserBubbleHoverActions(t *testing.T) {
	for _, busy := range []bool{false, true} {
		a := &desktop{}
		content := strings.Repeat("完整提问复制测试 ", 70)
		conv := &conversation{ID: "actions", Busy: busy, Messages: []map[string]any{{"role": "user", "content": content}}}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			ui.Column(c).Fill().Selectable().Children(func() { a.messageRow(c, conv, 0, newTranscriptRows(conv)) })
		}, 700, 500)
		bubble, _ := tt.Find("用户消息")
		actions, ok := tt.Find("用户消息操作")
		if !ok || actions.Y < bubble.Y+bubble.H || actions.X+actions.W != bubble.X+bubble.W {
			t.Fatalf("actions must be below the bubble, aligned to its right: %v / %v", bubble, actions)
		}
		before := append([]byte(nil), tt.Image().Pix...)
		tt.Move(bubble.X+bubble.W/2, bubble.Y+bubble.H/2)
		tt.Advance(200 * time.Millisecond)
		after, _ := tt.Find("用户消息")
		afterActions, _ := tt.Find("用户消息操作")
		if bubble != after || actions != afterActions {
			t.Fatalf("hover shifted the message or actions: %v -> %v; %v -> %v", bubble, after, actions, afterActions)
		}
		if bytes.Equal(before, tt.Image().Pix) {
			t.Fatal("hover did not reveal action buttons")
		}
		copy, _ := tt.Find("复制提问")
		tt.Move(copy.X+copy.W/2, copy.Y+copy.H/2)
		tt.Advance(200 * time.Millisecond)
		if err := tt.Click("复制提问"); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != content {
			t.Fatalf("copy did not preserve the full folded prompt: %q", tt.Clipboard())
		}
		if err := tt.Click("删除该提问及之后"); err != nil {
			t.Fatal(err)
		}
		if busy {
			if a.deletePrompt != nil {
				t.Fatal("busy conversation offered deletion")
			}
		} else if a.deletePrompt == nil || a.deletePrompt.MessageIndex != 0 {
			t.Fatal("delete did not open existing confirmation")
		}
	}
}
