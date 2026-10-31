package desktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"reflect"
	"testing"
)

func TestMCPEditorPreservesArguments(t *testing.T) {
	for _, args := range [][]string{
		{"C:/Program Files/mcp/server.js"},
		{"--config", "a,b", "", " hello ", `C:\目录\a.js`, `quoted "argument"`},
	} {
		raw, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"srv": mcpServerEntry{Command: "node", Args: args}}})
		if err != nil {
			t.Fatal(err)
		}
		a := &desktop{mcpText: string(raw)}
		a.openMCPEditor("srv")
		got, err := splitArgs(a.mcpF.Args)
		if err != nil || !reflect.DeepEqual(got, args) {
			t.Fatalf("arguments changed: %q -> %q, err=%v", args, got, err)
		}
	}
	for _, raw := range []string{`["unfinished"`, `[1]`} {
		if _, err := splitArgs(raw); err == nil {
			t.Fatalf("invalid JSON args accepted: %s", raw)
		}
	}
	for _, raw := range []string{"-y, @playwright/mcp", "-y @playwright/mcp"} {
		got, err := splitArgs(raw)
		if err != nil || !reflect.DeepEqual(got, []string{"-y", "@playwright/mcp"}) {
			t.Fatalf("simple args compatibility lost: %q, %v", got, err)
		}
	}
}

func TestMCPEditorUsesTheConnectionTransport(t *testing.T) {
	for _, tc := range []struct {
		entry mcpServerEntry
		want  string
	}{
		{mcpServerEntry{Command: "node"}, "stdio"},
		{mcpServerEntry{URL: "https://example.invalid/mcp"}, "streamable-http"},
		{mcpServerEntry{Transport: "sse", URL: "https://example.invalid/sse"}, "sse"},
		{mcpServerEntry{Transport: "http", URL: "https://example.invalid/mcp"}, "streamable-http"},
		{mcpServerEntry{Transport: "rest", URL: "https://example.invalid/mcp"}, "streamable-http"},
	} {
		raw, _ := json.Marshal(map[string]any{"mcpServers": map[string]mcpServerEntry{"srv": tc.entry}})
		a := &desktop{mcpText: string(raw)}
		a.openMCPEditor("srv")
		if a.mcpF.Transport != tc.want || a.mcpF.URL != tc.entry.URL {
			t.Fatalf("wrong editor transport/url: %+v", a.mcpF)
		}
	}
}

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
