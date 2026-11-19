package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestUserMessageLongTextStaysInContentColumn(t *testing.T) {
	for _, content := range []string{strings.Repeat("1", 300), strings.Repeat("! ", 150), strings.Repeat("1", 700)} {
		a := &desktop{}
		conv := &conversation{ID: "long-prompt"}
		conv.uiState().PromptOpen = map[int]bool{0: true}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			ui.Column(c).Width(600).Children(func() { a.userMessage(c, conv, 0, map[string]any{"content": content}) })
		}, 600, 500)
		bubble, ok := tt.Find("用户消息")
		if !ok || bubble.X < 0 || bubble.X+bubble.W > 601 || bubble.W > 511 || bubble.H < 60 {
			t.Fatalf("long user message did not wrap inside the content column: %+v", bubble)
		}
	}
}
