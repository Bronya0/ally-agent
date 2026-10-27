package desktop

import (
	"math"
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// The MCP tab strip must not move when the selected tab changes: the notes
// that differ per tab sit below it, so the strip's Y stays put.
func TestMCPTabStripStaysPutWhenSwitching(t *testing.T) {
	a := demoDesktop()
	a.page = "mcp"
	a.mcpText = `{"mcpServers":{"demo":{"command":"server"}}}`
	a.toolInventory = []backend.ToolDefinitionSummary{
		{Name: "search", Source: "builtin", Enabled: true},
		{Name: "read", Source: "builtin", Enabled: true, Protected: true},
	}
	tt := ui.NewTester(a.view, 1266, 843)
	tt.Frame()
	before, ok := tt.Find("Ally 内置工具")
	if !ok {
		t.Fatal("builtin tab label missing")
	}
	if err := tt.Click("Ally 内置工具"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if a.mcpF.Tab != 1 {
		t.Fatalf("tab did not switch: %d", a.mcpF.Tab)
	}
	after, ok := tt.Find("Ally 内置工具")
	if !ok {
		t.Fatal("builtin tab label vanished after switching")
	}
	if math.Abs(float64(after.Y-before.Y)) > 0.5 {
		t.Fatalf("tab strip jumped when switching: %.1f -> %.1f", before.Y, after.Y)
	}
}
