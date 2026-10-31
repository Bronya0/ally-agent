package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func compactToolView(a *desktop, msg map[string]any) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.toolCard(c, &conversation{ID: "summary"}, 0, msg) })
	}, 900, 500)
}
func TestMCPOnlyShowsStatusAndDuration(t *testing.T) {
	for _, state := range []string{"success", "error"} {
		msg := map[string]any{"name": "mcp__example", "toolCallId": "mcp", "status": state, "durationMs": 12, "error": "HIDDEN_ERROR", "result": map[string]any{"text": "HIDDEN_OUTPUT", "warnings": []any{"HIDDEN_WARNING"}}}
		tt := compactToolView(&desktop{}, msg)
		for _, s := range []string{"HIDDEN_OUTPUT", "HIDDEN_ERROR", "HIDDEN_WARNING", "折叠图标"} {
			if tt.HasText(s) {
				t.Fatalf("MCP leaked result: %v", tt.Texts())
			}
		}
		if !tt.HasText("12ms") {
			t.Fatal("MCP lost timing")
		}
		want := "成功"
		if state == "error" {
			want = "失败"
		}
		if !tt.HasText(want) {
			t.Fatalf("missing %s", want)
		}
	}
}
func TestToolMetadataStaysOnHeader(t *testing.T) {
	for _, name := range []string{"screenshot", "image_generate", "image_edit"} {
		msg := map[string]any{"name": name, "toolCallId": "image", "status": "success", "result": map[string]any{"width": 800, "height": 600, "bytes": 2048}}
		tt := compactToolView(&desktop{}, msg)
		header, _ := tt.Find("工具详情 image")
		size, ok := tt.Find("800 × 600")
		if !ok || size.Y < header.Y || size.Y+size.H > header.Y+header.H {
			t.Fatalf("%s size outside header: %v header %v", name, size, header)
		}
		if !tt.HasText(fmtBytes(2048)) || tt.HasText("宽度:") || tt.HasText("折叠图标") {
			t.Fatalf("image metadata is not compact: %v", tt.Texts())
		}
	}
	for _, name := range []string{"http_request", "web_fetch"} {
		msg := map[string]any{"name": name, "toolCallId": "web", "status": "success", "args": `{"url":"https://example.com"}`, "result": map[string]any{"durationMs": 37, "status": 200}}
		tt := compactToolView(&desktop{}, msg)
		timing, ok := tt.Find("37ms")
		header, _ := tt.Find("工具详情 web")
		if !ok || timing.X > 400 || timing.Y < header.Y || timing.Y+timing.H > header.Y+header.H {
			t.Fatalf("%s timing hidden or pushed away: %v", name, timing)
		}
	}
}
func TestScreenshotEnumerationHasNoBodyAndNoMisleadingTruncation(t *testing.T) {
	msg := map[string]any{"name": "screenshot", "toolCallId": "list", "status": "success", "args": `{"action":"list"}`, "result": map[string]any{"windows": []any{map[string]any{"title": "HIDDEN_WINDOW"}}, "total": 3, "truncated": true}}
	tt := compactToolView(&desktop{}, msg)
	if tt.HasText("HIDDEN_WINDOW") || tt.HasText("已截断") || !tt.HasText("1 / 3 个窗口") {
		t.Fatalf("misleading screenshot list: %v", tt.Texts())
	}
}
func TestDeletedTaskNameAppearsInSummary(t *testing.T) {
	msg := map[string]any{"name": "scheduled_task", "toolCallId": "task", "status": "success", "args": `{"action":"delete","id":"task-id"}`, "result": map[string]any{"deleted": "task-id", "deletedName": "每日巡检"}}
	tt := compactToolView(&desktop{}, msg)
	if !tt.HasText("delete · 每日巡检") {
		t.Fatalf("missing task identity: %v", tt.Texts())
	}
}
func TestChatWidthIsBoundedAndResponsive(t *testing.T) {
	conv := &conversation{ID: "width", Messages: []map[string]any{{"role": "assistant", "content": "正文"}}}
	a := &desktop{tabs: []*conversation{conv}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() {
			// Measure the actual chat column through the input; it keeps 20 DIP padding.
			a.chatColumn(c, 2600)
		})
	}, 2600, 700)
	input, ok := tt.Find("消息输入框")
	if !ok || input.W > chatMaxWidth || input.X < 600 {
		t.Fatalf("wide chat is not centered/constrained: %v", input)
	}
	viewport, ok := tt.Find("聊天滚动区域")
	if !ok || viewport.X != 0 || viewport.W != 2600 {
		t.Fatalf("scrollbar viewport does not span the wide chat panel: %v", viewport)
	}
	tt.SetSize(800, 700)
	input, ok = tt.Find("消息输入框")
	if !ok || input.X < 0 || input.X+input.W > 800 || input.W < 700 {
		t.Fatalf("narrow chat does not use available space: %v", input)
	}
	viewport, ok = tt.Find("聊天滚动区域")
	if !ok || viewport.X != 0 || viewport.W != 800 {
		t.Fatalf("scrollbar viewport does not follow the narrow chat panel: %v", viewport)
	}
}
