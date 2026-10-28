// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"testing"
)

func TestRuntimeToolEnvelopePresentation(t *testing.T) {
	a := demoDesktop()
	tm := a.current()
	a.applyToolEvent(tm, "tool:result", map[string]any{"toolCallId": "cmd-envelope", "name": "command", "args": `{"command":"go version"}`, "result": `{"ok":true,"data":{"output":"go version test-output","exitCode":0,"truncated":false,"warnings":["visible warning"]}}`})
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	if !tt.HasText("go version test-output") || !tt.HasText("visible warning") {
		t.Fatalf("missing payload: %q", tt.Texts())
	}
	if tt.HasText(`"ok": true`) {
		t.Fatal("protocol envelope leaked into terminal")
	}
}

func TestToolAliasesAndStreamingContent(t *testing.T) {
	if !isFoldableReadTool(map[string]any{"name": "list_files"}) {
		t.Fatal("list_files must fold")
	}
	if describeTool("replace_exact") != describeTool("edit") {
		t.Fatal("edit alias mismatch")
	}
	args := parseToolArgs(`{"path":"a.go","content":"first\nsecond`)
	if str(args, "content") != "first\nsecond" || str(args, "path") != "a.go" {
		t.Fatalf("stream prefix lost: %#v", args)
	}
}

func TestSubagentEventsPreserveMessageIdentity(t *testing.T) {
	a := demoDesktop()
	tm := a.current()
	a.applySubagentEvent("sub:spawn", map[string]any{"sessionId": tm.ID, "id": "child-1", "role": "研究员", "maxSteps": 8, "description": "inspect source"})
	a.applySubagentEvent("sub:step", map[string]any{"sessionId": tm.ID, "id": "child-1", "step": 2})
	for _, state := range []string{"sub:tool:start", "sub:tool:result"} {
		a.applySubagentEvent(state, map[string]any{"sessionId": tm.ID, "id": "child-1", "toolCallId": "child-read", "name": "read"})
	}
	row := tm.Messages[len(tm.Messages)-1]
	if str(row, "role") != "subagent" || str(row, "subagentRole") != "研究员" || num(row, "steps") != 2 || len(list(row, "toolCalls")) != 1 {
		t.Fatalf("broken child identity: %#v", row)
	}
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	if !tt.HasText("研究员") || !tt.HasText("2/8 步") {
		t.Fatalf("missing child card %q", tt.Texts())
	}
}

func TestGeneratedToolImagesAndDeleteSlots(t *testing.T) {
	a := demoDesktop()
	tm := a.current()
	a.applyToolEvent(tm, "tool:result", map[string]any{"toolCallId": "image-1", "name": "image_edit", "result": `{"ok":true,"data":{"imageRef":"image:test/1","width":512,"height":512}}`})
	card := tm.Messages[len(tm.Messages)-1]
	if len(messageAttachmentList(card)) != 1 {
		t.Fatal("tool-owned image lost")
	}
	tm.Messages = append(tm.Messages, map[string]any{"role": "tool_call", "name": "delete", "status": "success", "toolCallId": "del", "result": `{"ok":true,"data":{"paths":[{"path":"absent.go","ok":true,"absent":true},{"path":"protected.go","ok":false,"error":"denied"}],"failedCount":1}}`})
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	if !tt.HasText("ok (absent)") || !tt.HasText("denied") {
		t.Fatalf("delete outcomes lost: %q", tt.Texts())
	}
}

func TestToolVisualPreview(t *testing.T) {
	if os.Getenv("ALLY_TOOL_VISUAL") != "1" {
		t.Skip("optional preview")
	}
	a := demoDesktop()
	tm := a.current()
	tm.Messages = []map[string]any{{"role": "user", "content": "测试工具调用、代码预览与中文字体。"}}
	fixtures := []map[string]any{
		{"name": "command", "args": `{"command":"go test ./..."}`, "result": `{"ok":true,"data":{"output":"PASS\nall packages passed","exitCode":0,"durationMs":842}}`},
		{"name": "edit", "args": `{"path":"internal/app/main.go"}`, "result": `{"ok":true,"data":{"diff":"@@ -1,3 +1,3 @@\n package app\n-old value\n+new value\n context","warnings":["注意：文件包含其他修改"]}}`},
		{"name": "create", "args": `{"path":"hello.go","content":"package main\n\nfunc main() {\n    println(\"hello\")\n}"}`},
		{"name": "service", "args": `{"action":"list"}`, "result": `{"ok":true,"data":{"services":[{"id":"svc_1","name":"预览服务","status":"running","command":"go run .","cwd":"D:/coding/project","pid":1280}]}}`},
	}
	for n, m := range fixtures {
		m["role"] = "tool_call"
		m["status"] = "success"
		m["toolCallId"] = fmt.Sprintf("visual-%d", n)
		tm.Messages = append(tm.Messages, m)
	}
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	var output bytes.Buffer
	if err := png.Encode(&output, tt.Image()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("TOOL_PREVIEW=" + base64.StdEncoding.EncodeToString(output.Bytes()))
}

func TestToolExpansionAndSuggestionInteraction(t *testing.T) {
	a := demoDesktop()
	tm := a.current()
	tm.Messages = []map[string]any{
		{"role": "tool_call", "name": "command", "status": "success", "toolCallId": "expand-command", "result": `{"ok":true,"data":{"output":"first-line\nsecond-line\nthird-line\nfourth-line\nfifth-line\nsixth-line"}}`},
		{"role": "tool_call", "name": "suggest", "status": "success", "toolCallId": "suggest-1", "result": `{"ok":true,"data":{"items":["检查构建"]}}`},
	}
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	if tt.HasText("first-line") {
		t.Fatal("collapsed terminal should show tail")
	}
	if err := tt.Click("工具详情 expand-command"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tm.uiState().ToolOpen["expand-command"] || !tt.HasText("first-line") {
		t.Fatal("terminal did not expand")
	}
	if err := tt.Click("检查构建"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tm.Draft != "检查构建" {
		t.Fatal("suggestion did not populate composer")
	}
	tt.SetSize(900, 620)
	tt.Frame()
	tt.SetSize(1500, 950)
	tt.Frame()
}

func TestAskResolvedSelectionsAndResourceOutput(t *testing.T) {
	a := demoDesktop()
	tm := a.current()
	tm.Messages = []map[string]any{
		{"role": "tool_call", "name": "ask", "status": "success", "toolCallId": "asked", "args": `{"questions":[{"id":"q","question":"选择实现方式"}]}`, "result": `{"ok":true,"data":{"answers":[{"questionId":"q","selections":[{"label":"原生控件","recommended":true}]}]}}`},
		{"role": "tool_call", "name": "service", "status": "success", "toolCallId": "logs", "args": `{"action":"read"}`, "result": `{"ok":true,"data":{"id":"svc_1","output":"service-log-content"}}`},
	}
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	for _, text := range []string{"选择实现方式", "原生控件", "service-log-content"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %s, got %q", text, tt.Texts())
		}
	}
}

func TestSplitDiffKeepsIndependentLineNumbers(t *testing.T) {
	rows := splitDiffRows([]diffVisibleLine{{text: "@@ -8,2 +12,3 @@"}, {text: "-old"}, {text: "+new"}, {text: "+extra"}, {text: " context"}})
	if len(rows) != 4 || rows[1].leftNumber != 8 || rows[1].rightNumber != 12 || rows[2].leftNumber != 0 || rows[2].rightNumber != 13 || rows[3].leftNumber != 9 || rows[3].rightNumber != 14 {
		t.Fatalf("wrong split gutters: %#v", rows)
	}
}

func TestToolSummaryFeedbackRegressions(t *testing.T) {
	a := demoDesktop()
	tm := a.current()
	tm.Messages = nil
	a.applyToolEvent(tm, "tool:start", map[string]any{"toolCallId": "service-1", "name": "service", "args": `{"action":"top"}`})
	a.applyToolEvent(tm, "tool:result", map[string]any{"toolCallId": "service-1", "result": `{"ok":true,"data":{}}`})
	a.applyToolEvent(tm, "tool:update", map[string]any{"toolCallId": "service-1"})
	if toolState(tm.Messages[0]) != "success" {
		t.Fatal("late update restarted completed service tool")
	}
	tm.Messages = append(tm.Messages,
		map[string]any{"role": "tool_call", "name": "calculate", "status": "success", "toolCallId": "calc", "args": `{"expression":"1+1"}`, "result": `{"ok":true,"data":{"value":2,"text":"2"}}`},
		map[string]any{"role": "tool_call", "name": "mcp__example__search", "mcpServer": "example", "mcpTool": "search", "status": "success", "toolCallId": "mcp"},
		map[string]any{"role": "tool_call", "name": "suggest", "status": "success", "toolCallId": "suggest", "result": `{"ok":true,"data":{"items":["继续检查"]}}`})
	tt := ui.NewTester(a.view, 1280, 1000)
	tt.Frame()
	if !tt.HasText("= 2") || !tt.HasText("Called MCP") || !tt.HasText("example/search") {
		t.Fatalf("missing tool summaries: %q", tt.Texts())
	}
	if tt.HasText("工具详情 suggest") {
		t.Fatal("suggest should render options without a tool header")
	}
	if !tt.HasText("Ally Native") {
		t.Fatal("welcome disappeared after messages")
	}
}
