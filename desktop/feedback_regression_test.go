package desktop

import (
	backend "ally-dev/internal/app"
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestWorkspaceInputRecallAndDedup(t *testing.T) {
	a := demoDesktop()
	s := a.current()
	s.Messages = nil
	s.uiState().WorkspaceHistory = []string{"older question", "latest question", "older question"}
	value, ok := navigateInputHistory(s, "", true)
	if !ok || value != "older question" {
		t.Fatalf("workspace recall: %q", value)
	}
	value, ok = navigateInputHistory(s, value, true)
	if !ok || value != "latest question" {
		t.Fatalf("workspace recall previous: %q", value)
	}
}
func TestChatWindowBoundsAndCaches(t *testing.T) {
	a := demoDesktop()
	s := a.current()
	s.Messages = nil
	for n := 0; n < 1000; n++ {
		s.Messages = append(s.Messages, map[string]any{"role": "assistant", "content": fmt.Sprintf("message %d", n)})
	}
	start, end := chatMessageWindow(s)
	if start != 860 || end != 1000 {
		t.Fatalf("window %d:%d", start, end)
	}
	a.markdownOptions(s, 0)
	tt := ui.NewTester(a.view, 1100, 800)
	tt.Frame()
	if len(s.MD) > 140 {
		t.Fatalf("markdown cache grew to %d", len(s.MD))
	}
	s.uiState().VisibleStart = 721
	start, end = chatMessageWindow(s)
	if start != 720 || end-start > 140 {
		t.Fatal("older page is unbounded")
	}
}
func TestRoundStatsFollowSuggestions(t *testing.T) {
	s := demoDesktop().current()
	s.Messages = []map[string]any{{"role": "assistant"}, {"role": "tool_call", "name": "suggest"}, {"role": "user"}, {"role": "assistant"}}
	if roundStatsIndex(s, 0) != 1 {
		t.Fatal("statistics must follow suggestions within the round")
	}
}
func TestCompletedPlanCollapses(t *testing.T) {
	a := demoDesktop()
	s := a.current()
	a.applyPlanUpdate(map[string]any{"sessionId": s.ID, "plan": []backend.PlanStep{{Title: "done", Status: "done"}}})
	if !s.uiState().PlanCollapsed {
		t.Fatal("completed plan stayed expanded")
	}
}

func TestGitDirectoryCollapse(t *testing.T) {
	a := demoDesktop()
	a.explorer = false
	a.filesF.GitOpen = true
	a.filesF.GitFile = 0
	a.filesF.GitData = backend.GitDiffResult{IsRepo: true, Files: []backend.GitDiffFile{{Path: "internal/app/main.go", Status: "modified", Diff: "@@ -1 +1 @@\n-old\n+new"}, {Path: "internal/app/other.go", Status: "added"}}}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if !tt.HasText("main.go") || !tt.HasText("other.go") {
		t.Fatalf("missing grouped files: %q", tt.Texts())
	}
	if err := tt.Click("internal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !*a.popupFlag("git-folder-internal") || tt.HasText("other.go") {
		t.Fatal("directory did not collapse")
	}
}

func TestSubagentReportRequiresClick(t *testing.T) {
	a := demoDesktop()
	s := a.current()
	s.Messages = []map[string]any{
		{"role": "tool_call", "name": "subagent", "status": "success", "toolCallId": "parent", "result": `{"ok":true,"data":{"agentId":"child","summary":"PRIVATE_REPORT"}}`},
		{"role": "subagent", "subId": "child", "parentToolCallId": "parent", "subagentRole": "reviewer", "status": "success", "summary": "PRIVATE_REPORT"},
	}
	tt := ui.NewTester(a.view, 1200, 900)
	tt.Frame()
	if tt.HasText("PRIVATE_REPORT") {
		t.Fatal("report visible without click")
	}
	if err := tt.Click("报告 ›"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("PRIVATE_REPORT") {
		t.Fatal("report did not open")
	}
}
func TestModelDefaultsMatchVue(t *testing.T) {
	a := demoDesktop()
	a.editModel(-1)
	d := a.modelDraft
	if d.TokenParam != "auto" || d.ReasoningTag != "reasoning_content" || d.ReasoningEffort != "max" || d.MaxTokens != 131072 || d.ContextWindow != 1000000 || d.Model != "deepseek-v4-flash" {
		t.Fatalf("defaults %#v", d)
	}
}

func TestTaskScopeFiltersCurrentWorkspace(t *testing.T) {
	a := demoDesktop()
	root := a.current().Workspace
	a.services = []backend.ServiceInfo{{ID: "local", Workspace: root}, {ID: "other", Workspace: "D:/another-workspace"}}
	a.tasksF.Scope = 0
	if got := a.filteredServices(root); len(got) != 1 || got[0].ID != "local" {
		t.Fatalf("current scope: %#v", got)
	}
	a.tasksF.Scope = 1
	if len(a.filteredServices(root)) != 2 {
		t.Fatal("all scope excluded services")
	}
}

func TestContextButtonsFitPopup(t *testing.T) {
	a := demoDesktop()
	s := a.current()
	s.uiState().FooterLoaded = true
	s.uiState().ContextOpen = true
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	labels := []string{"压缩上下文", "压缩上下文(快速)", "压缩教训", "更新图谱"}
	var bounds []ui.Rect
	for _, label := range labels {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("missing %s", label)
		}
		bounds = append(bounds, r)
	}
	for i, a := range bounds {
		for j, b := range bounds {
			if j > i && a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				t.Fatalf("buttons overlap: %#v %#v", a, b)
			}
		}
	}
}

func TestFormPolishPreview(t *testing.T) {
	if os.Getenv("ALLY_FORM_VISUAL") == "" {
		t.Skip("optional visual snapshot")
	}
	a := demoDesktop()
	a.page = "mcp"
	a.openMCPEditor("")
	a.mcpF.EditorOpen = true
	tt := ui.NewTester(a.view, 1000, 760)
	tt.Frame()
	var b bytes.Buffer
	if err := png.Encode(&b, tt.Image()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("FORM_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
}

func TestSessionsHoverFollowsPointer(t *testing.T) {
	a := demoDesktop()
	s := a.current()
	s.uiState().SessionsOpen = true
	a.sessions = []backend.SessionIndexEntry{{ID: "first", Title: "Hover first", Workspace: s.Workspace, UpdatedAt: 2}, {ID: "second", Title: "Hover second", Workspace: s.Workspace, UpdatedAt: 1}}
	tt := ui.NewTester(a.view, 1200, 850)
	tt.Frame()
	r, ok := tt.Find("Hover second")
	if !ok {
		t.Fatal("missing second session")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if s.uiState().SessionsIndex != 1 {
		t.Fatal("session highlight did not follow pointer")
	}
}

func TestAccentDoesNotTintBackground(t *testing.T) {
	a := demoDesktop()
	a.prefs.Appearance = "dark"
	a.prefs.Palette = 0
	base := a.currentPalette()
	for _, idx := range []int{2, 3, 4, 5} {
		a.prefs.Palette = idx
		p := a.currentPalette()
		if p.bg != base.bg || p.panel != base.panel || p.text != base.text {
			t.Fatal("accent changed neutral surfaces")
		}
	}
	a.prefs.Appearance = "light"
	if a.currentPalette().dark {
		t.Fatal("accent overrode light mode")
	}
}
// The conversation diff renders in place: no nested scroll pane and no fold
// button. Long lines wrap inside their own column, which is what keeps the
// page's own scrolling responsive.
func TestDiffRendersInlineWithoutFold(t *testing.T) {
	a := demoDesktop()
	long := strings.Repeat("right-side-content ", 40)
	lines := []string{"@@ -1,82 +1,82 @@", "-old", "+" + long}
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf(" context %d", i))
	}
	diff := strings.Join(lines, "\n")
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() { a.diffBlock(c, "regression-diff", diff, true) })
	}, 800, 600)
	tt.Frame()
	if _, ok := tt.Find("diff pane 1"); ok {
		t.Fatal("diff must not own a nested scroll pane")
	}
	if tt.HasText("显示完整 diff") || tt.HasText("收起 diff") {
		t.Fatal("diff must not fold behind a toggle")
	}
	body, ok := tt.Find("+" + long)
	if !ok {
		t.Fatal("diff body missing")
	}
	if body.W > 800 {
		t.Fatalf("wrapped code should stay inside the card, got width %.1f", body.W)
	}
}

func TestExpandedMCPResultHasBoundedViewport(t *testing.T) {
	a := demoDesktop()
	s := a.current()
	s.uiState().ToolOpen["mcp-tall"] = true
	first := map[string]any{"role": "tool_call", "name": "mcp__server__search", "toolCallId": "mcp-tall", "status": "success", "result": strings.Repeat("long result content\n", 1000)}
	next := map[string]any{"role": "tool_call", "name": "calculate", "toolCallId": "after-tall", "status": "success"}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() { a.toolCard(c, s, 0, first); a.toolCard(c, s, 1, next) })
	}, 800, 600)
	tt.Frame()
	body, ok := tt.Find("工具输出 tool-body-0")
	if !ok || body.H != 240 {
		t.Fatalf("unbounded result: %#v", body)
	}
	row, ok := tt.Find("工具详情 after-tall")
	if !ok || row.Y < body.Y+body.H {
		t.Fatal("result overlapped next tool")
	}
}
