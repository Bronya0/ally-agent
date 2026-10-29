package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestTranscriptDisclosureIcons(t *testing.T) {
	for _, tc := range []struct{ name, icon, title string }{
		{"command", "terminal", "执行命令 1 次"},
		{"read", "search", "读取 1 次"},
		{"grep", "search", "搜索 1 次"},
		{"list", "search", "罗列 1 次"},
	} {
		a := &desktop{}
		msg := groupCommand("icon", tc.name, "success")
		conv := &conversation{ID: "icon", Messages: []map[string]any{msg}}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			ui.Column(c).FillWidth().Padding(24).Children(func() { a.messageRow(c, conv, 0) })
		}, 700, 300)
		icon, ok := tt.Find("折叠图标 " + tc.icon)
		if !ok || icon.W != 15 || icon.H != 15 {
			t.Fatalf("%s missing icon slot: %v", tc.name, icon)
		}
		if tc.title != "" {
			label, ok := tt.Find(tc.title)
			if !ok || label.X < icon.X+icon.W {
				t.Fatalf("%s icon is not before title", tc.name)
			}
			tt.Move(label.X+2, label.Y+2)
			tt.Advance(200 * time.Millisecond)
			after, _ := tt.Find(tc.title)
			if label != after {
				t.Fatalf("hover moved disclosure title: %v -> %v", label, after)
			}
		}
	}
}

func TestSuggestionsAlignWithReplyBody(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "suggest"}
	msg := map[string]any{"name": "suggest", "toolCallId": "suggest", "status": "success", "result": map[string]any{"items": []any{"建议一", "建议二"}}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).FillWidth().Padding(24).Children(func() {
			ui.Text(c, "回复正文")
			a.toolCard(c, conv, 0, msg)
		})
	}, 700, 200)
	reply, ok := tt.Find("回复正文")
	if !ok {
		t.Fatal("missing reply")
	}
	suggestion, ok := tt.Find("建议一")
	if !ok || suggestion.X != reply.X {
		t.Fatalf("suggestions indented from reply: %v vs %v", suggestion, reply)
	}
}
