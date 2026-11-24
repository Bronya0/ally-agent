package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func compactToolView(a *desktop, msg map[string]any) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.toolCard(c, &conversation{ID: "summary"}, 0, msg) })
	}, 900, 500)
}
func TestMCPShowsToolIdentityStatusAndDuration(t *testing.T) {
	for _, state := range []string{"success", "error"} {
		msg := map[string]any{"name": "mcp__example", "toolCallId": "mcp", "status": state, "mcpServer": "example", "mcpTool": "lookup", "durationMs": 12, "error": "HIDDEN_ERROR", "result": map[string]any{"text": "HIDDEN_OUTPUT", "warnings": []any{"HIDDEN_WARNING"}}}
		tt := compactToolView(&desktop{}, msg)
		for _, s := range []string{"HIDDEN_OUTPUT", "HIDDEN_ERROR", "HIDDEN_WARNING", "折叠图标"} {
			if tt.HasText(s) {
				t.Fatalf("MCP leaked result: %v", tt.Texts())
			}
		}
		if !tt.HasText("<1s") {
			t.Fatal("MCP lost timing")
		}
		if !tt.HasText("example/lookup") {
			t.Fatal("MCP lost tool identity")
		}
		want := "已完成"
		if state == "error" {
			want = "失败"
		}
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("missing %s", want)
		}
	}
}

// 运行中的 ToolExec 卡片直接指向远端工具：等结果事件带来的元数据才知道调的是谁，
// 卡片就会先写 ToolExec 再变成 server/tool，同一个调用看着像换了两次。
func TestToolExecCardNamesItsTargetBeforeTheResult(t *testing.T) {
	const done = `{"name":"github.create_issue","arguments":{"title":"Bug","repo":"a/b"},"stage":0}`
	for _, tc := range []struct {
		name   string
		args   string
		status string
		extra  map[string]any
		want   string
	}{
		{"arguments still arriving keep the neutral label", `{"name":"github.crea`, "running", nil, "MCP"},
		{"a complete envelope names the target", done, "running", nil, "github/create_issue"},
		{"the result keeps the same identity", done, "success", map[string]any{"mcpServer": "github", "mcpTool": "create_issue"}, "github/create_issue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := map[string]any{"name": "ToolExec", "toolCallId": "exec", "status": tc.status, "args": tc.args, "streaming": tc.status == "running", "argsStreaming": tc.status == "running"}
			for key, value := range tc.extra {
				msg[key] = value
			}
			if tc.status == "success" {
				msg["result"] = map[string]any{"text": "HIDDEN_OUTPUT"}
			}
			text := strings.Join(compactToolView(&desktop{}, msg).Texts(), " ")
			if strings.Contains(text, "ToolExec") {
				t.Fatalf("card shows the envelope name: %s", text)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("card = %s, want %q", text, tc.want)
			}
			if strings.Contains(text, "HIDDEN_OUTPUT") {
				t.Fatalf("card leaked the remote output: %s", text)
			}
			if tc.status == "running" && tc.want != "MCP" {
				// The label already names the target, so the summary is what the
				// remote tool receives, not a second copy of the envelope.
				if !strings.Contains(text, `"title":"Bug"`) || strings.Contains(text, `"arguments"`) {
					t.Fatalf("summary is not the target arguments: %s", text)
				}
			}
		})
	}
}

func TestToolSearchShowsBatchQueriesWithoutSchemas(t *testing.T) {
	const args = `{"query":["tavily.tavily_search","zhihu.zhihu_search"]}`
	msg := map[string]any{
		"name": "ToolSearch", "status": "success", "toolCallId": "search", "args": args,
		"result": map[string]any{"text": "fixture MCP input schema"},
	}
	tt := compactToolView(&desktop{}, msg)
	text := strings.Join(tt.Texts(), " ")
	if !strings.Contains(text, "tavily.tavily_search") || !strings.Contains(text, "zhihu.zhihu_search") ||
		strings.Contains(text, "fixture MCP input schema") {
		t.Fatalf("unexpected search card content: %s", text)
	}
	if !retainMCPDisplaySummary(msg) || len(toolResult(msg)) != 0 || str(msg, "args") != args {
		t.Fatalf("display projection must keep query parameters and drop the schema: %#v", msg)
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
	for _, name := range []string{"fetch", "http_request", "web_fetch"} {
		msg := map[string]any{"name": name, "toolCallId": "web", "status": "success", "args": `{"url":"https://example.com"}`, "result": map[string]any{"durationMs": 37, "status": 200}}
		tt := compactToolView(&desktop{}, msg)
		timing, ok := tt.Find("<1s")
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
	if !tt.HasText("(每日巡检)") {
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

func TestTransferSummary(t *testing.T) {
	args := map[string]any{"action": "download", "files": []any{map[string]any{"localPath": "dist/a", "remotePath": "/srv/a"}}}
	if got := toolArgSummary("remote_transfer", args); got != "/srv/a → dist/a" {
		t.Fatalf("direction: %q", got)
	}
}
