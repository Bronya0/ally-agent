// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGroupHeaderUsesLatestDescriptionsAndClipsWithinWindow(t *testing.T) {
	a := &desktop{}
	longPurpose := strings.Repeat("检查文件末尾内容", 12)
	makeCall := func(id, name, purpose string) map[string]any {
		m := groupCommand(id, name, "success")
		m["purpose"] = purpose
		return m
	}
	conv := &conversation{ID: "latest", Messages: []map[string]any{
		makeCall("old", "read", "较早的读取说明"),
		makeCall("search", "grep", "定位调用入口"),
		makeCall("list", "list", "查看目录结构"),
		makeCall("cmd", "command", "测试项目"),
		makeCall("new", "read", longPurpose),
	}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.toolActivityGroupCard(c, conv, 0) })
	}, 900, 500)
	if tt.HasText("较早的读取说明") || tt.HasText(longPurpose) || !tt.HasText(short(longPurpose, 32)) {
		t.Fatalf("header did not use latest bounded description: %v", tt.Texts())
	}
	for _, width := range []int{900, 420} {
		tt.SetSize(width, 500)
		tt.Frame()
		header, ok := tt.Find("工具分组 latest-readgroup-0")
		if !ok {
			t.Fatal("missing group header")
		}
		for _, text := range []string{short(longPurpose, 32), "定位调用入口", "查看目录结构", "测试项目"} {
			box, ok := tt.Find(text)
			if !ok || box.X < header.X || box.X+box.W > header.X+header.W+1 {
				t.Fatalf("description outside header: %q %v header=%v", text, box, header)
			}
		}
	}
	conv.Messages[4]["purpose"] = "更新后的读取说明"
	tt.Frame()
	if !tt.HasText("更新后的读取说明") || tt.HasText(short(longPurpose, 32)) {
		t.Fatal("description update retained an old header")
	}
	conv.Messages = append(conv.Messages, makeCall("last", "command", "执行最后验证"))
	tt.Frame()
	if !tt.HasText("执行最后验证") || tt.HasText("测试项目") {
		t.Fatal("appended call did not replace its category description")
	}
	if err := tt.Click("工具分组 latest-readgroup-0"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, name := range []string{"Read", "Grep", "List", "Command"} {
		if tt.HasText(name) {
			t.Fatalf("expanded call retained tool name %q", name)
		}
	}
}

func TestPurposeGroupDisclosesCallsBeforeFilesAndOutput(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "purposes", Messages: []map[string]any{
		{"role": "tool_call", "name": "read", "toolCallId": "read", "status": "success", "args": `{"purpose":"查看主流程与配置入口","files":[{"path":"main.go"},{"path":"config.go"}]}`, "result": map[string]any{"files": []any{map[string]any{"path": "main.go", "startLine": 1, "endLine": 40}, map[string]any{"path": "config.go", "startLine": 1, "endLine": 30}}}},
		{"role": "tool_call", "name": "command", "toolCallId": "cmd", "status": "success", "args": `{"purpose":"运行工具调用回归测试","command":"go test ./internal/app"}`, "result": map[string]any{"exitCode": 0, "durationMs": 32}},
	}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.toolActivityGroupCard(c, conv, 0) })
	}, 800, 600)
	if !tt.HasText("查看主流程与配置入口") || !tt.HasText("运行工具调用回归测试") {
		t.Fatal("collapsed group lost latest descriptions")
	}
	if err := tt.Click("工具分组 purposes-readgroup-0"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, purpose := range []string{"查看主流程与配置入口", "运行工具调用回归测试"} {
		count := 0
		for _, text := range tt.Texts() {
			if text == purpose {
				count++
			}
		}
		if count != 2 {
			t.Fatalf("each call must have its header description and one expanded row: %v", tt.Texts())
		}
	}
	if tt.HasText("main.go") || tt.HasText("config.go") || tt.HasText("go test ./internal/app") {
		t.Fatalf("call details leaked: %v", tt.Texts())
	}
	if err := tt.Click("工具目的 read"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("main.go (40 lines)") || !tt.HasText("config.go (30 lines)") {
		t.Fatalf("batch details missing: %v", tt.Texts())
	}
	if err := tt.Click("工具目的 cmd-purpose"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("go test ./internal/app") || tt.HasText("RESULT_OUTPUT") {
		t.Fatal("command disclosure lost command/output boundary")
	}
	if err := tt.Click("命令结果 cmd Ran go test ./internal/app"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	detail := conv.uiState().CommandDetails["cmd"]
	if detail == nil {
		t.Fatal("command click did not use existing result loader")
	}
	// The test has no live host; simulate its successful asynchronous reply.
	detail.Error = ""
	if !publishCommandDetail(conv, conv.ID, "cmd", detail, "RESULT_OUTPUT", nil) {
		t.Fatal("could not publish demand-loaded output")
	}
	tt.Frame()
	if !tt.HasText("RESULT_OUTPUT") {
		t.Fatalf("command output did not expand: %v", tt.Texts())
	}
	if err := tt.Click("工具分组 purposes-readgroup-0"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if conv.uiState().ToolOpen["read"] || conv.uiState().ToolOpen["cmd-purpose"] || conv.uiState().ToolOpen["cmd"] || len(conv.uiState().CommandDetails) != 0 {
		t.Fatal("group collapse retained child details")
	}
	if err := tt.Click("工具分组 purposes-readgroup-0"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("main.go") || tt.HasText("go test ./internal/app") || tt.HasText("RESULT_OUTPUT") {
		t.Fatal("reopening group automatically expanded details")
	}
}

func TestPurposeSitsInlineInIndependentHeaders(t *testing.T) {
	for _, name := range []string{"edit", "create", "service", "remote_read", "http_request", "screenshot", "mcp__server__tool"} {
		t.Run(name, func(t *testing.T) {
			purpose := "确认工具目的在当前行内显示并保持完整内容可供查看"
			args, _ := json.Marshal(map[string]any{"purpose": purpose, "path": "backend-local/detection/services.py", "action": "list"})
			msg := map[string]any{"name": name, "toolCallId": "header", "purposeField": "purpose", "args": string(args), "status": "success"}
			a := &desktop{}
			tt := compactToolView(a, msg)
			for _, width := range []int{900, 480} {
				tt.SetSize(width, 500)
				tt.Frame()
				header, ok := tt.Find("工具详情 header")
				if !ok {
					t.Fatal("header missing")
				}
				text, ok := tt.Find(purpose)
				if !ok || text.W < 90 || text.Y < header.Y-1 || text.Y+text.H > header.Y+header.H+1 || text.X < header.X || text.X+text.W > header.X+header.W+1 {
					t.Fatalf("purpose is not on the header row at %d: text=%v header=%v texts=%v", width, text, header, tt.Texts())
				}
			}
		})
	}
	// The facts stay left; the reason sits at the row's right edge.
	editArgs, _ := json.Marshal(map[string]any{"purpose": "补上标题行说明", "path": "desktop/tool_cards.go"})
	tt := compactToolView(&desktop{}, map[string]any{"name": "edit", "toolCallId": "order", "status": "success", "args": string(editArgs)})
	header, hasHeader := tt.Find("工具详情 order")
	verb, hasVerb := tt.Find("Edited")
	reason, hasReason := tt.Find("补上标题行说明")
	target, hasTarget := tt.Find("(desktop/tool_cards.go)")
	if !hasHeader || !hasVerb || !hasReason || !hasTarget || !(verb.X < target.X && target.X < reason.X) || reason.X+reason.W < header.X+header.W-40 {
		t.Fatalf("facts stay left and the reason keeps the right edge: %v %v %v %v", verb, target, reason, header)
	}
	a := &desktop{}
	conv := &conversation{ID: "events"}
	a.applyToolEvent(conv, "tool:start", map[string]any{"toolCallId": "call", "name": "read", "args": `{"purpose":"读取相关配置","files":[{"path":"a.go"}]}`, "purposeField": "purpose"})
	a.applyToolEvent(conv, "tool:result", map[string]any{"toolCallId": "call", "name": "read", "result": map[string]any{"files": []any{map[string]any{"path": "a.go"}}}})
	applyFastCompaction(conv, map[string]any{"toolSummaries": map[string]any{"call": "bounded excerpt"}})
	if toolPurpose(conv.Messages[0], nil) != "读取相关配置" {
		t.Fatal("compaction discarded call purpose")
	}
	saved := snapshot(conv)
	if toolPurpose(saved.Messages[0], nil) != "读取相关配置" {
		t.Fatal("snapshot discarded call purpose")
	}
	// Business purpose on an old MCP call is not the user-facing purpose.
	if toolPurpose(map[string]any{"name": "mcp__old"}, map[string]any{"purpose": "server value"}) != "" {
		t.Fatal("MCP business field shown as metadata")
	}
	msg := map[string]any{"name": "mcp__new", "purposeField": "_purpose"}
	if toolPurpose(msg, map[string]any{"purpose": "business", "_purpose": "user-facing"}) != "user-facing" {
		t.Fatal("colliding MCP purpose was lost")
	}
}

func TestPurposeStreamingUsesChangedArguments(t *testing.T) {
	a := &desktop{}
	msg := groupCommand("live", "command", "running")
	msg["args"] = `{"purpose":"读取`
	conv := &conversation{ID: "stream", Messages: []map[string]any{msg}}
	conv.uiState().ToolOpen["stream-commandgroup-0"] = true
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.toolActivityGroupCard(c, conv, 0) })
	}, 600, 300)
	msg["args"] = `{"purpose":"读取项目配置","command":"cat config.go"}`
	tt.Frame()
	if !tt.HasText("读取项目配置") || tt.HasText("cat config.go") {
		t.Fatalf("stream purpose stale or detail auto-expanded: %s", strings.Join(tt.Texts(), ","))
	}
}

func TestGroupHeaderKeepsDescriptionWhileNextCallStreams(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "stable"}
	emit := func(event, id, name, args string, streaming ...bool) {
		value := event != "tool:result"
		if len(streaming) > 0 {
			value = streaming[0]
		}
		a.applyToolEvent(conv, event, map[string]any{"toolCallId": id, "name": name, "args": args, "argsStreaming": value})
	}
	emit("tool:start", "first", "read", `{"purpose":"`)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.toolActivityGroupCard(c, conv, 0) })
	}, 800, 400)
	if tt.HasText(groupPurposeFallbacks[0]) {
		t.Fatal("first streamed call flashed a temporary fallback")
	}
	emit("tool:update", "first", "read", `{"purpose":"检查配置入口","files":`)
	tt.Frame()
	if tt.HasText("检查配置入口") {
		t.Fatal("streaming purpose leaked into the folded header")
	}
	emit("tool:update", "first", "read", `{"purpose":"检查配置入口","files":[]}`, false)
	tt.Frame()
	if !tt.HasText("检查配置入口") {
		t.Fatal("finalized purpose missing before execution completes")
	}
	emit("tool:result", "first", "read", `{"purpose":"检查配置入口","files":[]}`)
	tt.Frame()
	for _, args := range []string{``, `{"purpose":`, `{"purpose":"检查后续`} {
		emit("tool:update", "second", "read", args)
		tt.Frame()
		if !tt.HasText("检查配置入口") || tt.HasText(groupPurposeFallbacks[0]) {
			t.Fatalf("partial arguments replaced the previous description: %v", tt.Texts())
		}
	}
	emit("tool:update", "second", "read", `{"purpose":"检查后续配置","files":`)
	tt.Frame()
	if !tt.HasText("检查配置入口") || tt.HasText("检查后续配置") {
		t.Fatal("complete purpose was published during argument streaming")
	}
	emit("tool:update", "second", "read", `{"purpose":"检查后续配置","files":[]}`, false)
	tt.Frame()
	if !tt.HasText("检查后续配置") || tt.HasText("检查配置入口") {
		t.Fatal("ready purpose did not replace the previous description")
	}
	// A new category must not insert a temporary label and shift its neighbors.
	emit("tool:start", "command", "command", `{"purpose":"`)
	tt.Frame()
	if tt.HasText(groupPurposeFallbacks[3]) || !tt.HasText("检查后续配置") {
		t.Fatal("new category flashed a fallback")
	}
	emit("tool:result", "command", "command", `{}`)
	tt.Frame()
	if !tt.HasText(groupPurposeFallbacks[3]) {
		t.Fatal("completed legacy call lost its fallback")
	}
}
