package desktop

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

func TestCancellationIsPlainResult(t *testing.T) {
	for _, m := range []map[string]any{
		{"content": "已取消", "error": true},
		{"content": "用户已取消", "error": true},
		{"status": "cancelled", "content": "已取消", "error": true},
	} {
		a := &desktop{}
		tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.assistantMessage(c, &conversation{}, 0, m) }, 400, 100)
		if texts := strings.Join(tt.Texts(), " "); texts != "用户已取消" {
			t.Fatalf("cancellation display: %q", texts)
		}
	}
	for _, m := range []map[string]any{{"status": "cancelled"}, {"status": "error", "result": map[string]any{"cancelled": true}}} {
		if got := toolState(m); got != "cancelled" {
			t.Fatalf("state %s for %v", got, m)
		}
	}
	for _, text := range []string{"连接失败", "请求超时，后续任务已取消", "context canceled"} {
		if userCancelledMessage(map[string]any{"content": text, "error": true}) {
			t.Fatalf("real error mistaken for user cancellation: %s", text)
		}
	}
}
