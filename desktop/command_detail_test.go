// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

func TestLocalCommandEventRetainsMetadataOnly(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "events"}
	a.applyToolEvent(conv, "tool:start", map[string]any{"toolCallId": "cmd", "name": "command", "args": `{"command":"go test"}`})
	a.applyToolEvent(conv, "tool:result", map[string]any{"toolCallId": "cmd", "name": "command", "result": `{"ok":true,"data":{"output":"FULL_OUTPUT_MARKER","exitCode":1,"durationMs":12,"outputFilePath":"full.log"}}`})
	msg := conv.Messages[0]
	encoded, _ := json.Marshal(msg)
	if strings.Contains(string(encoded), "FULL_OUTPUT_MARKER") || strings.Contains(string(encoded), "full.log") {
		t.Fatal("transcript retained output payload")
	}
	if int(num(toolResult(msg), "exitCode")) != 1 {
		t.Fatal("exit metadata lost")
	}
	if len(conv.uiState().CommandDetails) != 0 {
		t.Fatal("result event eagerly loaded closed details")
	}
	saved, _ := json.Marshal(snapshot(conv))
	if strings.Contains(string(saved), "FULL_OUTPUT_MARKER") {
		t.Fatal("UI snapshot persisted output")
	}
}

func TestCommandDetailUsesContextAndDropsLateResponse(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "context"}
	msg := groupCommand("call", "command", "success")
	conv.uiState().ToolOpen["call"] = true
	detail := &commandDetail{Loading: true}
	conv.uiState().CommandDetails = map[string]*commandDetail{"call": detail}
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Padding(20).Children(func() { a.toolCard(c, conv, 0, msg) }) }, 800, 500)
	if tt.HasText("OUTPUT-call") {
		t.Fatal("expanded detail used the full frontend result")
	}
	if !publishCommandDetail(conv, "context", "call", detail, "<ally-command>CONTEXT_ONLY</ally-command>", nil) {
		t.Fatal("current response was not accepted")
	}
	tt.Frame()
	if !tt.HasText("CONTEXT_ONLY") || tt.HasText("OUTPUT-call") {
		t.Fatalf("wrong result source: %v", tt.Texts())
	}
	if err := tt.Click("工具详情 call"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(conv.uiState().CommandDetails) != 0 {
		t.Fatal("collapse retained result cache")
	}
	if publishCommandDetail(conv, "context", "call", detail, "LATE_RESULT", nil) {
		t.Fatal("closed detail accepted late result")
	}
	if tt.HasText("CONTEXT_ONLY") {
		t.Fatal("closed detail retained output component")
	}
}

func TestLocalReadSearchListChipsAreEmpty(t *testing.T) {
	result := map[string]any{"totalLines": 100, "count": 50, "nextStartLine": 11, "matches": []any{"hit"}, "truncated": true}
	for _, kind := range []string{"read", "batch_read", "grep", "list", "glob"} {
		if chips := toolChips(kind, "success", nil, result); len(chips) != 0 {
			t.Fatalf("%s retained badges: %v", kind, chips)
		}
	}
}

func TestCommandGroupCollapseReleasesChildResults(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "release", Messages: []map[string]any{groupCommand("child", "command", "success")}}
	st := conv.uiState()
	st.ToolOpen["release-commandgroup-0"] = true
	st.ToolOpen["child"] = true
	st.CommandDetails = map[string]*commandDetail{"child": {Result: map[string]any{"_text": "CACHED_RESULT"}}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		ui.Column(c).Children(func() { a.commandGroupCard(c, conv, 0) })
	}, 800, 500)
	if !tt.HasText("CACHED_RESULT") {
		t.Fatal("test requires expanded child")
	}
	if err := tt.Click("命令分组 release-commandgroup-0"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(st.CommandDetails) != 0 || st.ToolOpen["child"] {
		t.Fatal("group retained expanded child output")
	}
}

func TestCommandEventProjectionDoesNotQueueOrMutateFullPayload(t *testing.T) {
	original := map[string]any{"name": "Bash", "result": `{"ok":true,"data":{"output":"DO_NOT_QUEUE","exitCode":0}}`}
	displayed := toolEventForDisplay("tool:result", original)
	encoded, _ := json.Marshal(displayed)
	if strings.Contains(string(encoded), "DO_NOT_QUEUE") {
		t.Fatal("full payload reached UI queue")
	}
	if !strings.Contains(str(original, "result"), "DO_NOT_QUEUE") {
		t.Fatal("projection mutated backend event")
	}
	if str(commandContextResult(`{"ok":false,"error":"context failure"}`), "_text") != "context failure" {
		t.Fatal("failed command context lost error")
	}
}
