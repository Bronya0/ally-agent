package desktop

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestDiffMemoReplacesRowsAndResetsExpansion(t *testing.T) {
	var memo diffMemo
	memo.display("-old\n+new")
	memo.extra = 120
	lines := memo.display("-before\n+after")
	if len(lines) != 2 || lines[1].text != "+after" || !lines[1].changed || memo.extra != 0 {
		t.Fatalf("stale diff: %+v extra=%d", lines, memo.extra)
	}
}

// Collapsing a card must drop the rows it had shown, or every later collapse
// keeps the expanded node count.
func TestDiffExpansionResetsWhenCardCollapses(t *testing.T) {
	var memo diffMemo
	if memo.limit(true) != 12 {
		t.Fatal("compact limit")
	}
	memo.extra = 120
	if memo.limit(true) != 132 {
		t.Fatal("show more lost")
	}
	if memo.limit(false) != 120 || memo.extra != 0 {
		t.Fatal("opening card must reset expansion")
	}
	memo.extra = 240
	if memo.limit(true) != 12 || memo.extra != 0 {
		t.Fatal("collapsing card must reset expansion")
	}
}

func TestEditHeaderOpensEditorWithoutResettingDiffExpansion(t *testing.T) {
	conv := &conversation{ID: "edit", Workspace: t.TempDir()}
	file := &workspaceFile{Path: "edited.go", Editor: &editor.State{Buffer: editor.NewBuffer("package edited")}}
	a := &desktop{tabs: []*conversation{conv}, prefs: defaultPrefs(), workspaceFiles: map[string]*workspaceFile{workspaceKey(conv.Workspace): file}}
	var diff strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&diff, "+change-%02d\n", i)
	}
	msg := map[string]any{"name": "edit", "toolCallId": "edit-call", "status": "success", "args": `{"files":[{"path":"edited.go"}]}`, "result": map[string]any{"diff": strings.TrimSuffix(diff.String(), "\n")}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.toolCard(c, conv, 0, msg) })
	}, 900, 1000)
	if tt.HasText("+change-29") {
		t.Fatal("diff initially rendered beyond its compact range")
	}
	if err := tt.Click("Show more 18 rows"); err != nil {
		t.Fatalf("%v; texts=%v", err, tt.Texts())
	}
	if !tt.HasText("+change-29") {
		t.Fatal("diff expansion stopped working")
	}
	if err := tt.Click("工具详情 edit-call"); err != nil {
		t.Fatal(err)
	}
	if !a.explorer || a.activeFile() != file || len(conv.uiState().ToolOpen) != 0 || !tt.HasText("+change-29") {
		t.Fatal("header did not open the retained editor independently of diff expansion")
	}
	if err := tt.Click("Show less"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("+change-29") {
		t.Fatal("diff collapse stopped working")
	}
	// An old frame's callback must not interpret its relative path in a new tab.
	a.tabs = append(a.tabs, &conversation{ID: "other", Workspace: t.TempDir()})
	a.active = 1
	a.explorer = false
	a.openToolFile(conv, "edited.go")
	if a.explorer || a.activeFile() != nil {
		t.Fatal("queued file navigation changed a different workspace")
	}
}

func TestCreatePreviewStaysBoundedWhileStreamingAndIgnoresHeaderClicks(t *testing.T) {
	conv := &conversation{ID: "create", Workspace: t.TempDir()}
	a := &desktop{tabs: []*conversation{conv}, prefs: defaultPrefs()}
	msg := map[string]any{"name": "create", "toolCallId": "create-call", "status": "running"}
	setContent := func(content string) {
		raw, _ := json.Marshal(map[string]any{"path": "new.txt", "content": content})
		msg["args"] = string(raw)
	}
	setContent("old-line\nline-2\nline-3\nline-4\nline-5\nline-6\nline-7\n\n")
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.toolCard(c, conv, 0, msg) })
	}, 900, 500)
	for _, state := range []string{"running", "success"} {
		msg["status"] = state
		tt.Frame()
		if !tt.HasText("line-2\nline-3\nline-4\nline-5\nline-6\nline-7") || !tt.HasText("2\n3\n4\n5\n6\n7") {
			t.Fatalf("%s preview lost its fixed tail or source line numbers: %v", state, tt.Texts())
		}
		if err := tt.Click("工具详情 create-call"); err != nil {
			t.Fatal(err)
		}
		if len(conv.uiState().ToolOpen) != 0 || a.explorer || !tt.HasText("line-2\nline-3\nline-4\nline-5\nline-6\nline-7") {
			t.Fatal("create header allocated expansion state or opened a file")
		}
	}
	setContent("old-line\nline-2\nline-3\nline-4\nline-5\nline-6\nline-7\nline-8")
	tt.Frame()
	if !tt.HasText("line-3\nline-4\nline-5\nline-6\nline-7\nline-8") {
		t.Fatal("new streamed content did not advance the fixed tail")
	}
}

func TestMultiEditFileLinkOpensItsOwnPath(t *testing.T) {
	conv := &conversation{ID: "multi-edit", Workspace: t.TempDir()}
	file := &workspaceFile{Path: "second.go", Editor: &editor.State{Buffer: editor.NewBuffer("package second")}}
	a := &desktop{tabs: []*conversation{conv}, prefs: defaultPrefs(), workspaceFiles: map[string]*workspaceFile{workspaceKey(conv.Workspace): file}}
	msg := map[string]any{"name": "edit", "toolCallId": "multi-call", "status": "success", "result": map[string]any{"files": []any{
		map[string]any{"path": "first.go", "diff": "+first"},
		map[string]any{"path": "second.go", "diff": "+second"},
	}}}
	// Choosing the already-open second file cancels an older pending switch.
	ctx, _ := a.workspacePreview.beginLoad(conv.Workspace, "first.go")
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.toolCard(c, conv, 0, msg) })
	}, 900, 500)
	if err := tt.Click("second.go"); err != nil {
		t.Fatal(err)
	}
	if !a.explorer || a.activeFile() != file || ctx.Err() == nil || len(conv.uiState().ToolOpen) != 0 {
		t.Fatal("file link did not choose its own editor or cancelled the wrong load")
	}
}
