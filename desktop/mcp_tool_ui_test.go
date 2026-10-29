package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestMCPNativeAndJSONToolsOpenChecklist(t *testing.T) {
	native := []map[string]any{{"name": "read_file", "description": "Read a file"}, {"name": "write_file", "description": "Write a file"}}
	for _, tools := range []any{native, []any{native[0], native[1]}} {
		a := &desktop{mcpText: `{"mcpServers":{"server":{"command":"test"}}}`, mcpServers: []map[string]any{{"name": "server", "status": "connected", "toolCount": 2, "tools": tools}}}
		a.mcpF.ToolOpen = map[string]bool{}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			ui.Column(c).Fill().Children(func() { a.mcpServerList(c) })
		}, 800, 350)
		if err := tt.Click("工具 2/2"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		for _, name := range []string{"read_file", "write_file"} {
			if _, ok := tt.Find(name); !ok {
				t.Fatalf("missing tool checkbox: %s", name)
			}
		}
	}
}

func TestMCPCheckboxWritesNewValueAndSurvivesRebuild(t *testing.T) {
	a := &desktop{}
	doc := &mcpConfigDoc{Servers: map[string]mcpServerEntry{"server": {DisabledTools: []string{"read_file", "unknown"}}}}
	status := map[string]any{"tools": []map[string]any{{"name": "read_file"}, {"name": "write_file"}}}
	tt := ui.NewTester(func(c *ui.Context) { a.mcpToolChecklist(c, "server", doc.Servers["server"], doc, status) }, 700, 300)
	for _, step := range []struct {
		name string
		want []string
	}{
		{"read_file", []string{"unknown"}},
		{"read_file", []string{"read_file", "unknown"}},
		{"write_file", []string{"read_file", "unknown", "write_file"}},
	} {
		if err := tt.Click(step.name); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Frame()
		got := doc.Servers["server"].DisabledTools
		if len(got) != len(step.want) {
			t.Fatalf("disabled tools %v want %v", got, step.want)
		}
		for i := range got {
			if got[i] != step.want[i] {
				t.Fatalf("disabled tools %v want %v", got, step.want)
			}
		}
	}
}
