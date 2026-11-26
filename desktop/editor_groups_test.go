// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestEditorGroupsKeepMultipleFileBuffers(t *testing.T) {
	root := t.TempDir()
	first := &workspaceFile{Path: "first.go", Editor: &editor.State{Buffer: editor.NewBuffer("package first")}}
	second := &workspaceFile{Path: "second.go", Editor: &editor.State{Buffer: editor.NewBuffer("package second")}}
	third := &workspaceFile{Path: "third.go", Editor: &editor.State{Buffer: editor.NewBuffer("package third")}}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): first},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(first.Path): first, editorDocumentKey(second.Path): second, editorDocumentKey(third.Path): third}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
			Groups: map[int]*editorGroupState{0: {Tabs: []string{first.Path, second.Path}, Active: first.Path}},
			Layout: &editorLayoutNode{Group: 0},
		}},
	}

	a.splitEditorGroup(root, splitRight)
	if !a.editorStates[workspaceKey(root)].Split || a.editorGroupIndex(root) != 1 || a.activeFile() != nil {
		t.Fatal("splitting did not create an empty, active second group")
	}
	a.selectEditorDocument(root, third.Path, 1, true)
	if a.activeFile() != third || len(a.editorStates[workspaceKey(root)].Groups[0].Tabs) != 2 {
		t.Fatal("opening into the second group changed the first group's tabs")
	}

	second.Editor.Buffer.Set("package second\n// retained edit")
	a.selectEditorDocument(root, second.Path, 0, false)
	a.openLoadedEditorDocument(root, third.Path, 1)
	if a.editorGroupIndex(root) != 0 || a.activeFile() != second || !second.Editor.Buffer.Modified() || third.Editor.Buffer.String() != "package third" {
		t.Fatal("switching editor groups lost an independent file buffer")
	}
}

func TestEditorGroupInteractionSelectsFileOpenTarget(t *testing.T) {
	state := &workspaceEditorState{
		Groups:      map[int]*editorGroupState{0: {}, 1: {}},
		Layout:      &editorLayoutNode{Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		ActiveGroup: 1,
	}
	activateEditorGroup(state, 0, false)
	if state.ActiveGroup != 1 {
		t.Fatal("a non-interacting group changed the file-open target")
	}
	activateEditorGroup(state, 0, true)
	if state.ActiveGroup != 0 {
		t.Fatal("interacting with the first editor group did not make it the file-open target")
	}
}

func TestEditorLayoutSupportsAllSplitDirections(t *testing.T) {
	for _, direction := range []editorSplitDirection{splitLeft, splitRight, splitUp, splitDown} {
		root := t.TempDir()
		a := &desktop{editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
			Groups: map[int]*editorGroupState{0: {}},
			Layout: &editorLayoutNode{Group: 0},
		}}}
		a.splitEditorGroup(root, direction)
		node := a.editorStates[workspaceKey(root)].Layout
		if node.Direction != direction || len(layoutGroupIDs(node)) != 2 {
			t.Fatalf("direction %d did not form a two-leaf split", direction)
		}
		if direction == splitLeft || direction == splitUp {
			if node.First.Group != 1 || node.Second.Group != 0 {
				t.Fatalf("direction %d placed the new group on the wrong side", direction)
			}
		} else if node.First.Group != 0 || node.Second.Group != 1 {
			t.Fatalf("direction %d placed the new group on the wrong side", direction)
		}
	}
}

func TestEditorLayoutNodeIDsAreNotReusedAfterCollapse(t *testing.T) {
	root := t.TempDir()
	a := &desktop{editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
		Groups: map[int]*editorGroupState{0: {}},
		Layout: &editorLayoutNode{Group: 0},
	}}}
	a.splitEditorGroup(root, splitRight)
	oldID := a.editorStates[workspaceKey(root)].Layout.ID
	if oldID == 0 {
		t.Fatal("new split node has no identity")
	}
	a.closeEditorGroup(root, 1)
	a.splitEditorGroup(root, splitRight)
	newID := a.editorStates[workspaceKey(root)].Layout.ID
	if newID == 0 || newID == oldID {
		t.Fatalf("recreated split reused node identity: old=%d new=%d", oldID, newID)
	}
}

func TestEditorLayoutSupportsNestedDirectionsAndLeafClosure(t *testing.T) {
	root := t.TempDir()
	a := &desktop{editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
		Groups: map[int]*editorGroupState{0: {}},
		Layout: &editorLayoutNode{Group: 0},
	}}}
	state := a.editorStates[workspaceKey(root)]
	a.splitEditorGroup(root, splitRight)
	a.splitEditorGroup(root, splitDown)
	if state.Layout.Direction != splitRight || state.Layout.Second.Direction != splitDown || len(layoutGroupIDs(state.Layout)) != 3 {
		t.Fatal("directional split did not create a nested three-pane layout")
	}
	a.closeEditorGroup(root, 1)
	if len(state.Groups) != 2 || state.Layout.Second == nil || !state.Layout.Second.leaf() || state.Layout.Second.Group != 2 || state.ActiveGroup != 2 {
		t.Fatalf("closing a nested leaf left groups=%v active=%d layout=%+v", len(state.Groups), state.ActiveGroup, state.Layout)
	}
	a.closeEditorGroup(root, 2)
	if state.Split || len(state.Groups) != 1 || !state.Layout.leaf() || state.Layout.Group != 0 {
		t.Fatal("closing the remaining split did not restore the root leaf")
	}
}

func TestClosingGroupWithNoActiveFileSelectsMergedTab(t *testing.T) {
	root := t.TempDir()
	file := &workspaceFile{Path: "retained.txt", Editor: &editor.State{Buffer: editor.NewBuffer("content")}}
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {},
			1: {Tabs: []string{file.Path}},
		},
		Layout:      &editorLayoutNode{Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		NextGroup:   2,
		ActiveGroup: 1,
		Split:       true,
	}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		editorStates:               map[string]*workspaceEditorState{workspaceKey(root): state},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(file.Path): file}},
	}
	if file := a.editorDocument(root, "retained.txt"); file == nil {
		t.Fatal("test document was not registered")
	}
	a.closeEditorGroup(root, 1)
	if state.Groups[0].Active != file.Path || a.activeFile() != file {
		t.Fatalf("merged tab is not active after closing the group: activePath=%q file=%p", state.Groups[0].Active, a.activeFile())
	}
}

func TestClosingInactiveEditorGroupPreservesActiveDocument(t *testing.T) {
	root := t.TempDir()
	active := &workspaceFile{Path: "active.txt", Editor: &editor.State{Buffer: editor.NewBuffer("active")}}
	inactive := &workspaceFile{Path: "inactive.txt", Editor: &editor.State{Buffer: editor.NewBuffer("inactive")}}
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{active.Path}, Active: active.Path},
			1: {Tabs: []string{inactive.Path}, Active: inactive.Path},
		},
		Layout:      &editorLayoutNode{Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		ActiveGroup: 0,
		Split:       true,
	}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): active},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(active.Path): active, editorDocumentKey(inactive.Path): inactive}},
		editorStates:               map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	a.closeEditorGroup(root, 1)
	if state.ActiveGroup != 0 || state.Groups[0].Active != active.Path || a.activeFile() != active {
		t.Fatalf("closing the inactive group changed the visible file: group=%d path=%q file=%p", state.ActiveGroup, state.Groups[0].Active, a.activeFile())
	}
}

func TestClosingEditorGroupMergesTabsWithoutReleasingFiles(t *testing.T) {
	root := t.TempDir()
	first := &workspaceFile{Path: "first.txt", Editor: &editor.State{Buffer: editor.NewBuffer("first")}}
	second := &workspaceFile{Path: "second.txt", Editor: &editor.State{Buffer: editor.NewBuffer("second")}}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): second},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(first.Path): first, editorDocumentKey(second.Path): second}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
			Groups: map[int]*editorGroupState{
				0: {Tabs: []string{first.Path}, Active: first.Path},
				1: {Tabs: []string{second.Path}, Active: second.Path},
			},
			Layout:      &editorLayoutNode{Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}, SplitSize: 400},
			NextGroup:   2,
			ActiveGroup: 1,
			Split:       true,
		}},
	}

	a.closeEditorGroup(root, 1)
	state := a.editorStates[workspaceKey(root)]
	if state.Split || state.ActiveGroup != 0 || state.Groups[0].Active != second.Path || len(state.Groups[0].Tabs) != 2 {
		t.Fatal("closing a group did not merge its tabs into the remaining group")
	}
	if a.activeFile() != second || len(a.editorDocumentsByWorkspace[workspaceKey(root)]) != 2 {
		t.Fatal("closing a group released an open file")
	}
}

func TestClosingOneEditorDocumentKeepsOtherTabs(t *testing.T) {
	root := t.TempDir()
	first := &workspaceFile{Path: "first.txt", Editor: &editor.State{Buffer: editor.NewBuffer("first")}}
	second := &workspaceFile{Path: "second.txt", Editor: &editor.State{Buffer: editor.NewBuffer("second")}}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): second},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(first.Path): first, editorDocumentKey(second.Path): second}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
			Groups: map[int]*editorGroupState{0: {Tabs: []string{first.Path, second.Path}, Active: first.Path}},
			Layout: &editorLayoutNode{Group: 0},
		}},
	}

	a.closeEditorTab(root, first.Path, 0)
	if a.editorDocument(root, first.Path) != nil || a.editorDocument(root, second.Path) != second {
		t.Fatal("closing one document released the wrong file")
	}
	if a.activeFile() != second || len(a.editorStates[workspaceKey(root)].Groups[0].Tabs) != 1 {
		t.Fatal("closing one document did not select the remaining tab")
	}
}

func TestTabStripTreatsReorderAsActiveDrag(t *testing.T) {
	strip := &tabStrip{reorderFrom: "file.txt"}
	if !strip.dragActive() {
		t.Fatal("a reorder drag was not recognized")
	}
	strip.reorderFrom = ""
	strip.gesture.Panning = true
	if !strip.dragActive() {
		t.Fatal("a pan gesture was not recognized")
	}
	strip.gesture.Panning = false
	if strip.dragActive() {
		t.Fatal("an idle strip was treated as dragging")
	}
}

func TestEditorTabCanMoveToEveryRelativePosition(t *testing.T) {
	original := []string{"a.txt", "b.txt", "c.txt", "d.txt"}
	positions := make([]tabPosition, len(original))
	x := float32(-180) // exercise a scrolled strip as well as its visible tabs
	for i, path := range original {
		positions[i] = tabPosition{id: editorDocumentKey(path), x: x, width: 100}
		x += 108
	}
	for from, path := range original {
		centers := make([]float32, 0, len(original)-1)
		virtualX := positions[0].x
		for i := range original {
			if i == from {
				continue
			}
			centers = append(centers, virtualX+positions[i].width/2)
			virtualX += positions[i].width
		}
		for destination := range original {
			var pointer float32
			switch {
			case destination == 0:
				pointer = centers[0] - 24
			case destination == len(original)-1:
				pointer = centers[len(centers)-1] + 24
			default:
				pointer = (centers[destination-1] + centers[destination]) / 2
			}
			got := editorTabInsertionIndex(original, positions, editorDocumentKey(path), pointer, tabReorderHysteresis)
			if got != destination {
				t.Fatalf("from=%d pointer=%v insertion=%d, want %d", from, pointer, got, destination)
			}
			moved := slices.Clone(original)
			changed := moveEditorTabToIndex(&moved, editorDocumentKey(path), got)
			if changed != (from != destination) || editorDocumentKey(moved[destination]) != editorDocumentKey(path) {
				t.Fatalf("from=%d destination=%d changed=%v order=%v", from, destination, changed, moved)
			}
		}
	}
	if got := editorTabDropTarget(original, editorDocumentKey("b.txt"), len(original)-1); got != tabDropAtEnd {
		t.Fatalf("end drop target=%q, want end marker", got)
	}
}

func TestEditorFileLoadsAreIndependentByPath(t *testing.T) {
	root := t.TempDir()
	a := &desktop{}
	first, firstCtx := a.beginEditorFileLoad(root, "first.txt")
	second, secondCtx := a.beginEditorFileLoad(root, "second.txt")
	if firstCtx.Err() != nil || secondCtx.Err() != nil || !a.finishEditorFileLoad(first) || !a.finishEditorFileLoad(second) {
		t.Fatal("opening a different path cancelled an in-flight file read")
	}
	old, oldCtx := a.beginEditorFileLoad(root, "same.txt")
	latest, latestCtx := a.beginEditorFileLoad(root, "same.txt")
	if oldCtx.Err() == nil || latestCtx.Err() != nil || a.finishEditorFileLoad(old) || !a.finishEditorFileLoad(latest) {
		t.Fatal("a repeated read did not cancel only its stale predecessor")
	}
	pending, pendingCtx := a.beginEditorFileLoad(root, "pending.txt")
	a.cancelEditorFileLoads(root)
	if pendingCtx.Err() == nil || a.finishEditorFileLoad(pending) {
		t.Fatal("closing a workspace left an editor file read active")
	}
}

func TestEditorLayoutModesCanBeSelected(t *testing.T) {
	root := t.TempDir()
	a := &desktop{editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
		Groups: map[int]*editorGroupState{0: {}},
		Layout: &editorLayoutNode{Group: 0},
	}}}
	state := a.editorState(root)
	if state.ChatVisible || state.ChatOnly {
		t.Fatal("the editor should be full-width by default")
	}
	activateChatContent(state, true)
	a.showSinglePane(root)
	if !state.ChatOnly || state.ChatVisible {
		t.Fatal("single-pane mode did not preserve the selected session")
	}
	a.showChatAndEditor(root)
	if !state.ChatOnly || !state.ChatVisible {
		t.Fatal("side-by-side layout did not preserve the selected session")
	}
	activateEditorGroup(state, 0, true)
	if state.ChatOnly {
		t.Fatal("editing in a pane did not select file content as the single-pane target")
	}
	a.showSinglePane(root)
	if state.ChatOnly || state.ChatVisible {
		t.Fatal("single-pane mode did not restore the last edited file")
	}
	activateChatContent(state, true)
	a.showSinglePane(root)
	if !state.ChatOnly || state.ChatVisible {
		t.Fatal("single-pane mode did not restore the selected session")
	}
	a.showEditorOnly(root)
	if state.ChatOnly || state.ChatVisible {
		t.Fatal("full-width editor layout was not restored")
	}
}

func TestShowingChatKeepsEditorTabsOpen(t *testing.T) {
	root := t.TempDir()
	file := &workspaceFile{Path: "kept.txt", Editor: &editor.State{Buffer: editor.NewBuffer("retained")}}
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{0: {Tabs: []string{file.Path}, Active: file.Path}},
		Layout: &editorLayoutNode{Group: 0},
	}
	a := &desktop{
		explorer:                   true,
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): file},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(file.Path): file}},
		editorStates:               map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	state.ChatOnly = true
	a.showSinglePane(root)
	if !a.explorer || a.editorDocument(root, file.Path) != file || len(state.Groups[0].Tabs) != 1 {
		t.Fatal("showing chat hid the file tree or closed an editor document")
	}
	a.showEditorOnly(root)
	if !a.explorer || a.editorDocument(root, file.Path) != file || state.Groups[0].Active != file.Path {
		t.Fatal("returning to the editor did not preserve the open file and active tab")
	}
}

func TestOpeningFileFromChatOnlyReturnsToEditor(t *testing.T) {
	root := t.TempDir()
	file := &workspaceFile{Path: "reopen.txt", Editor: &editor.State{Buffer: editor.NewBuffer("content")}}
	state := &workspaceEditorState{
		Groups:   map[int]*editorGroupState{0: {Tabs: []string{file.Path}, Active: file.Path}},
		Layout:   &editorLayoutNode{Group: 0},
		ChatOnly: true,
	}
	a := &desktop{
		explorer:                   true,
		tabs:                       []*conversation{{Workspace: root}},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(file.Path): file}},
		editorStates:               map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	a.openFile(file.Path)
	if state.ChatOnly || a.activeFile() != file {
		t.Fatalf("opening a file did not return to its editor: chatOnly=%v active=%p", state.ChatOnly, a.activeFile())
	}
}

func TestOpenedFileRendersWithoutChatByDefault(t *testing.T) {
	root := t.TempDir()
	conv := &conversation{ID: "editor-only-chat", Workspace: root, Messages: []map[string]any{{"role": "user", "content": "HIDDEN_CHAT_MARKER"}}}
	file := &workspaceFile{Path: "whole-file.txt", Editor: &editor.State{Buffer: editor.NewBuffer("WHOLE_FILE_MARKER")}}
	a := &desktop{
		page:                       "chat",
		explorer:                   true,
		prefs:                      defaultPrefs(),
		tabs:                       []*conversation{conv},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): file},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(file.Path): file}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
			Groups: map[int]*editorGroupState{0: {Tabs: []string{file.Path}, Active: file.Path}},
			Layout: &editorLayoutNode{Group: 0},
		}},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		a.chatPage(c, 1200)
	}, 1500, 850)
	tester.Frame()
	if !tester.HasText("whole-file.txt") || tester.HasText("HIDDEN_CHAT_MARKER") {
		t.Fatalf("file-only mode should show the editor tab and hide chat: file=%v chat=%v", tester.HasText("whole-file.txt"), tester.HasText("HIDDEN_CHAT_MARKER"))
	}
}

func TestChatOnlyModeRendersChatWithFileTreeStillOpen(t *testing.T) {
	root := t.TempDir()
	conv := &conversation{ID: "chat-only", Workspace: root, Messages: []map[string]any{{"role": "user", "content": "CHAT_ONLY_MARKER"}}}
	file := &workspaceFile{Path: "hidden-editor.txt", Editor: &editor.State{Buffer: editor.NewBuffer("EDITOR_ONLY_MARKER")}}
	a := &desktop{
		page:                       "chat",
		explorer:                   true,
		prefs:                      defaultPrefs(),
		tabs:                       []*conversation{conv},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): file},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(file.Path): file}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
			Groups:   map[int]*editorGroupState{0: {Tabs: []string{file.Path}, Active: file.Path}},
			Layout:   &editorLayoutNode{Group: 0},
			ChatOnly: true,
		}},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		a.chatPage(c, 1200)
	}, 1500, 850)
	tester.Frame()
	if !tester.HasText("CHAT_ONLY_MARKER") || !a.explorer || a.editorDocument(root, file.Path) != file {
		t.Fatalf("chat-only mode should keep the file tree and open document: chat=%v explorer=%v file=%v", tester.HasText("CHAT_ONLY_MARKER"), a.explorer, a.editorDocument(root, file.Path) == file)
	}
}

func TestChatAndOpenedFileRenderTogether(t *testing.T) {
	root := t.TempDir()
	conv := &conversation{ID: "split-chat", Workspace: root, Messages: []map[string]any{{"role": "user", "content": "CHAT_SPLIT_MARKER"}}}
	file := &workspaceFile{Path: "editor.txt", Editor: &editor.State{Buffer: editor.NewBuffer("EDITOR_SPLIT_MARKER")}}
	a := &desktop{
		page:                       "chat",
		explorer:                   true,
		prefs:                      defaultPrefs(),
		tabs:                       []*conversation{conv},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): file},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(file.Path): file}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): {
			Groups:      map[int]*editorGroupState{0: {Tabs: []string{file.Path}, Active: file.Path}},
			Layout:      &editorLayoutNode{Group: 0},
			ChatVisible: true,
		}},
	}
	a.splitEditorGroup(root, splitRight)
	a.splitEditorGroup(root, splitDown)
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		a.chatPage(c, 1200)
	}, 1500, 850)
	tester.Frame()
	chatVisible := tester.HasText("CHAT_SPLIT_MARKER")
	fileVisible := tester.HasText("editor.txt")
	if !chatVisible || !fileVisible {
		t.Fatalf("chat and opened file were not both visible: chat=%v file=%v", chatVisible, fileVisible)
	}
}
