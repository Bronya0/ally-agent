package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"testing"
)

func groupCommand(id, name, status string) map[string]any {
	return map[string]any{"role": "tool_call", "name": name, "toolCallId": id, "status": status, "args": fmt.Sprintf("{\"command\":\"echo %s\"}", id), "result": map[string]any{"stdout": "OUTPUT-" + id, "exitCode": 0}}
}

func TestLocalCommandGroupPreservesCardsOnExpansion(t *testing.T) {
	for _, count := range []int{1, 3} {
		a := &desktop{}
		conv := &conversation{ID: "commands"}
		for i := 0; i < count; i++ {
			conv.Messages = append(conv.Messages, groupCommand(fmt.Sprint(i), "command", "success"))
		}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			ui.Column(c).FillWidth().Padding(20).Children(func() {
				for i := range conv.Messages {
					a.messageRow(c, conv, i, newTranscriptRows(conv))
				}
			})
		}, 700, 700)
		title := fmt.Sprintf("执行命令 %d 次", count)
		if !tt.HasText(title) || tt.HasText("OUTPUT-") {
			t.Fatalf("group not collapsed: %v", tt.Texts())
		}
		if err := tt.Click("命令分组 commands-commandgroup-0"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		for i := 0; i < count; i++ {
			if tt.HasText("OUTPUT-"+fmt.Sprint(i)) || !tt.HasText("echo "+fmt.Sprint(i)) {
				t.Fatalf("expanded group must show commands without results: %v", tt.Texts())
			}
		}
		if err := tt.Click("命令分组 commands-commandgroup-0"); err != nil {
			t.Fatal(err)
		}
		if tt.HasText("OUTPUT-") {
			t.Fatal("group did not collapse again")
		}
	}
}

func TestCommandGroupsStopAtRemoteAndProse(t *testing.T) {
	for _, mid := range []map[string]any{
		groupCommand("remote", "remote_run_command", "success"),
		{"role": "assistant", "content": "分隔正文"},
	} {
		a := &desktop{}
		conv := &conversation{ID: "boundaries", Messages: []map[string]any{groupCommand("first", "Bash", "success"), mid, groupCommand("last", "command", "error")}}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			ui.Column(c).FillWidth().Padding(20).Children(func() {
				for i := range conv.Messages {
					a.messageRow(c, conv, i, newTranscriptRows(conv))
				}
			})
		}, 700, 600)
		n := 0
		for _, s := range tt.Texts() {
			if s == "执行命令 1 次" {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("merged across nonfoldable message: %v", tt.Texts())
		}
		if str(mid, "name") == "remote_run_command" && !tt.HasText("OUTPUT-remote") {
			t.Fatal("remote command was folded")
		}
	}
}

func TestCommandGroupStaysCollapsedThroughoutExecution(t *testing.T) {
	a := &desktop{}
	msg := groupCommand("live", "command", "pending")
	conv := &conversation{ID: "lifecycle", Messages: []map[string]any{msg}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(20).Children(func() {
			rows := newTranscriptRows(conv)
			for i := range conv.Messages {
				a.messageRow(c, conv, i, rows)
			}
		})
	}, 700, 700)
	check := func(title string) {
		t.Helper()
		if !tt.HasText(title) || tt.HasText("echo live") || tt.HasText("OUTPUT-") {
			t.Fatalf("command escaped its collapsed group: %v", tt.Texts())
		}
	}
	check("执行命令 1 次")
	for _, state := range []string{"running", "success", "error", "cancelled"} {
		msg["status"] = state
		tt.Frame()
		check("执行命令 1 次")
	}
	conv.Messages = append(conv.Messages, groupCommand("next", "Bash", "running"))
	tt.Frame()
	check("执行命令 2 次")
	// The header's accessible label gains a running hint, so click the visible
	// count instead: what this asserts is that a live group can be expanded.
	if err := tt.Click("执行命令 2 次"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("echo next") {
		t.Fatal("expanded group does not show live command")
	}
	conv.Messages[1]["status"] = "success"
	tt.Frame()
	if !tt.HasText("echo next") || tt.HasText("OUTPUT-next") {
		t.Fatal("completion changed expanded group")
	}
}

func mixedReadTool(id, path string) map[string]any {
	return map[string]any{
		"role": "tool_call", "name": "read", "toolCallId": id, "status": "success",
		"args":   `{"path":"` + path + `"}`,
		"result": map[string]any{"files": []any{map[string]any{"path": path, "startLine": 1, "endLine": 40, "totalLines": 40}}},
	}
}

// Reads and commands used to fold into separate cards. One batch of work now
// stays one group, whichever tool it starts with, and collapsing it must still
// release the commands it expanded.
func TestReadsAndCommandsShareOneGroup(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "mixed", Messages: []map[string]any{
		mixedReadTool("mixed-read", "main.go"),
		groupCommand("mixed-cmd", "command", "success"),
	}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(20).Children(func() {
			rows := newTranscriptRows(conv)
			for i := range conv.Messages {
				a.messageRow(c, conv, i, rows)
			}
		})
	}, 700, 600)
	if !tt.HasText("读取 1 次") || !tt.HasText("执行命令 1 次") {
		t.Fatalf("mixed group header: %v", tt.Texts())
	}
	if tt.HasText("main.go") || tt.HasText("echo mixed-cmd") {
		t.Fatalf("collapsed group leaked entries: %v", tt.Texts())
	}
	if err := tt.Click("工具分组 mixed-readgroup-0"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("main.go") || !tt.HasText("echo mixed-cmd") || !tt.HasText("(40 lines)") {
		t.Fatalf("expanded entries: %v", tt.Texts())
	}
	conv.uiState().ToolOpen["mixed-cmd"] = true
	conv.uiState().CommandDetails = map[string]*commandDetail{"mixed-cmd": {Result: map[string]any{"stdout": "OUTPUT-mixed"}}}
	tt.Frame()
	if !tt.HasText("OUTPUT-mixed") {
		t.Fatalf("expanded command output: %v", tt.Texts())
	}
	// A grouped command keeps its own fixed-height scroll window: expanding it must
	// not spill the whole output into the group at once.
	if _, ok := tt.Find("工具输出 tool-out-1"); !ok {
		t.Fatal("grouped command lost its fixed-height output window")
	}
	if err := tt.Click("工具分组 mixed-readgroup-0"); err != nil {
		t.Fatal(err)
	}
	if conv.uiState().ToolOpen["mixed-cmd"] {
		t.Fatal("collapsing the group left its command open")
	}
}
