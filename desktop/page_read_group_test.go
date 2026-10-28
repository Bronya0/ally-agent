package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Consecutive read/grep/list calls fold into one group row; a lone call and
// calls separated by other roles render as their own cards.
func TestReadGrepGroupFoldsConsecutive(t *testing.T) {
	read := func(id, args string) map[string]any {
		return map[string]any{"role": "tool_call", "name": "read", "toolCallId": id, "status": "success", "args": args}
	}
	grep := func(id, args string) map[string]any {
		return map[string]any{"role": "tool_call", "name": "grep", "toolCallId": id, "status": "success", "args": args}
	}
	list := func(id, args string) map[string]any {
		return map[string]any{"role": "tool_call", "name": "list", "toolCallId": id, "status": "success", "args": args}
	}
	a := demoDesktop()
	tm := a.current()
	tm.Messages = append(tm.Messages,
		read("r1", `{"path":"a.go"}`),
		grep("g1", `{"pattern":"foo"}`),
		list("l1", `{"path":"."}`),
		map[string]any{"role": "assistant", "content": "done"},
		read("r2", `{"path":"b.go"}`),
		map[string]any{"role": "tool_call", "name": "edit", "toolCallId": "e1", "status": "success", "args": `{"path":"c.go"}`},
	)
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	// The first three fold into one row: counts join with "，", no per-call verbs.
	if !tt.HasText("已读取 1 次，搜索 1 次，罗列 1 次") {
		t.Fatalf("group row missing, texts=%q", tt.Texts())
	}
	if tt.HasText("已读取 a.go") {
		t.Fatalf("folded calls must not render as cards, texts=%q", tt.Texts())
	}
	// The lone read after the assistant message folds alone; edit is untouched.
	if !tt.HasText("已读取 1 次") {
		t.Fatalf("lone read should fold alone, texts=%q", tt.Texts())
	}
	if !tt.HasText("Edited") {
		t.Fatalf("edit card should still render, texts=%q", tt.Texts())
	}
}
