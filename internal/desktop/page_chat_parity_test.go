package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The collapsed diff shows changed rows with context instead of a hard
// truncation, and the full view opens on demand.
func TestDiffVisibleLinesCollapsesUnchanged(t *testing.T) {
	var lines []string
	lines = append(lines, "--- a/x", "+++ b/x")
	for i := 0; i < 40; i++ {
		lines = append(lines, " context")
	}
	lines = append(lines, "-old", "+new")
	for i := 0; i < 40; i++ {
		lines = append(lines, " context")
	}
	shown := diffVisibleLines(lines, diffHeadLimit, false)
	if len(shown) >= len(lines) {
		t.Fatalf("collapsed view should hide rows: %d vs %d", len(shown), len(lines))
	}
	gaps := 0
	for _, dl := range shown {
		gaps += dl.gap
	}
	if gaps == 0 {
		t.Fatal("collapsed view should summarize hidden rows as gaps")
	}
	full := diffVisibleLines(lines, diffHeadLimit, true)
	if len(full) != len(lines) {
		t.Fatalf("full view should render every row: %d vs %d", len(full), len(lines))
	}
}

// A failed tool card must say so: it must never keep the completion verb.
func TestToolCardTitleMentionsFailure(t *testing.T) {
	if v := describeTool("edit").failedVerb; !strings.Contains(v, "失败") {
		t.Fatalf("edit failed verb should mention failure, got %q", v)
	}
	if v := describeTool("mcp__demo__tool").failedVerb; !strings.Contains(v, "失败") {
		t.Fatalf("mcp failed verb should mention failure, got %q", v)
	}
	if v := describeTool("mystery_tool").failedVerb; !strings.Contains(v, "失败") {
		t.Fatalf("unknown failed verb should mention failure, got %q", v)
	}
	a := demoDesktop()
	tm := a.current()
	tm.Messages = append(tm.Messages, map[string]any{
		"role": "tool_call", "name": "edit", "toolCallId": "fail-1",
		"status": "error", "body": "boom",
	})
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	if !tt.HasText("编辑失败") {
		t.Fatalf("failed card should read 编辑失败, texts=%q", tt.Texts())
	}
	if tt.HasText("已编辑") {
		t.Fatalf("failed card must not claim 已编辑, texts=%q", tt.Texts())
	}
}
