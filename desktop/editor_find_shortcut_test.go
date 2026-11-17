// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// TestEditorFindTakesTheShortcutWhileAFileIsOpen: the transcript search used to
// open behind the file panel, where it could not be seen until the document was
// closed. With an editor on screen, Ctrl+F belongs to that editor.
func TestEditorFindTakesTheShortcutWhileAFileIsOpen(t *testing.T) {
	workspace := t.TempDir()
	conv := &conversation{ID: "editor-find", Workspace: workspace}
	file := &workspaceFile{Path: "demo.go", Editor: &editor.State{Buffer: editor.NewBuffer("demo demo\n")}}
	a := &desktop{ready: true, page: "chat", explorer: true, tabs: []*conversation{conv}}
	a.workspaceFiles = map[string]*workspaceFile{workspaceKey(workspace): file}

	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.globalShortcuts(c)
		editor.View(c.Key("editor-demo"), file.Editor).Grow(1).MinWidth(0)
	}, 900, 500)
	tester.Key(ui.Cmd, ui.KeyF)
	tester.Frame()

	if conv.uiState().Search.Open {
		t.Fatal("Ctrl+F opened the transcript search behind the file panel")
	}
	if a.chatSearchFocus != "" {
		t.Fatalf("focus = %q, want the editor's own find field", a.chatSearchFocus)
	}
	// The bar took the keyboard: what is typed goes into its query, and the
	// editor reports the matches of that query.
	tester.Type("demo")
	tester.Frame()
	if count, _ := file.Editor.FindMatches(); count != 2 {
		t.Fatalf("find matches = %d, want 2: the editor's find bar did not open", count)
	}
}

// TestPreviewPanelKeepsTheTranscriptSearch: a preview draws no editor, so the
// key must not be lost to a find bar built behind it; the transcript search
// still opens there, as it does without a file panel.
func TestPreviewPanelKeepsTheTranscriptSearch(t *testing.T) {
	workspace := t.TempDir()
	conv := &conversation{ID: "preview-find", Workspace: workspace}
	file := &workspaceFile{Path: "shot.png", PreviewPath: "shot.png", Editor: &editor.State{Buffer: editor.NewBuffer("")}}
	a := &desktop{ready: true, page: "chat", explorer: true, tabs: []*conversation{conv}}
	a.workspaceFiles = map[string]*workspaceFile{workspaceKey(workspace): file}

	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.globalShortcuts(c)
		if conv.uiState().Search.Open {
			a.chatSearchBar(c, conv)
		}
		a.composer(c, conv)
	}, 900, 500)
	tester.Key(ui.Cmd, ui.KeyF)

	if !conv.uiState().Search.Open {
		t.Fatal("Ctrl+F must still open the transcript search over a preview")
	}
}
