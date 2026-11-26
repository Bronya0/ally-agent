// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"slices"
	"testing"
	"time"

	"ally-dev/locales"
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

func TestEditorDockDirectionUsesNearestPaneEdge(t *testing.T) {
	for _, tc := range []struct {
		x, y float32
		want editorSplitDirection
	}{
		{20, 200, splitLeft},
		{380, 200, splitRight},
		{200, 20, splitUp},
		{200, 380, splitDown},
	} {
		got, ok := editorDockDirection(tc.x, tc.y, 400, 400)
		if !ok || got != tc.want {
			t.Fatalf("position (%v,%v) => (%v,%v), want %v", tc.x, tc.y, got, ok, tc.want)
		}
	}
	if _, ok := editorDockDirection(200, 200, 400, 400); ok {
		t.Fatal("center of pane should not be a docking target")
	}
}

func TestEditorTabDragDocksAtFrameEdge(t *testing.T) {
	root := t.TempDir()
	first := &workspaceFile{Path: "first.go", Editor: &editor.State{Buffer: editor.NewBuffer("package first")}}
	second := &workspaceFile{Path: "second.go", Editor: &editor.State{Buffer: editor.NewBuffer("package second")}}
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{first.Path}, Active: first.Path},
			1: {Tabs: []string{second.Path}, Active: second.Path},
		},
		Layout:    &editorLayoutNode{ID: 1, Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}, SplitSize: 600},
		NextGroup: 2, NextLayoutID: 2, ActiveGroup: 0,
	}
	a := &desktop{
		workspaceFiles: map[string]*workspaceFile{workspaceKey(root): first},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {
			editorDocumentKey(first.Path): first, editorDocumentKey(second.Path): second,
		}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.editorLayoutView(c, root, state.Layout)
	}, 1200, 700)
	tester.Frame()
	tab, ok := tester.Find(first.Path)
	if !ok {
		t.Fatal("missing source file tab")
	}
	x, y := tab.X+tab.W/2, tab.Y+tab.H/2
	tester.Press(x, y)
	tester.Advance(16 * time.Millisecond)
	tester.Move(x, y+20)
	tester.Advance(16 * time.Millisecond)
	if !state.PaneDrag.Active {
		t.Fatal("dragging the file tab did not start docking")
	}
	tester.Move(4, 350)
	tester.Advance(16 * time.Millisecond)
	if !state.PaneDrag.Drop || state.PaneDrag.Direction != splitLeft {
		t.Fatalf("drag did not select the editor frame's left edge: drop=%v direction=%d", state.PaneDrag.Drop, state.PaneDrag.Direction)
	}
	tester.Release(4, 350)
	tester.Advance(16 * time.Millisecond)
	if state.Layout.Direction != splitLeft || state.Layout.First.Group != 0 || state.Layout.Second.Group != 1 {
		t.Fatalf("release did not dock the file tab at the editor frame's left edge: %+v", state.Layout)
	}
}

func TestSingleFileTabCanDockToEveryFrameEdge(t *testing.T) {
	for _, tc := range []struct {
		name      string
		x, y      float32
		direction editorSplitDirection
	}{
		{"left", 4, 350, splitLeft},
		{"right", 1196, 350, splitRight},
		{"up", 600, 4, splitUp},
		{"down", 600, 696, splitDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := &workspaceFile{Path: "file.go", Editor: &editor.State{Buffer: editor.NewBuffer("package file")}}
			state := &workspaceEditorState{
				Groups: map[int]*editorGroupState{0: {Tabs: []string{file.Path}, Active: file.Path}},
				Layout: &editorLayoutNode{Group: 0},
			}
			a := &desktop{
				workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): file},
				editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(file.Path): file}},
				editorStates:               map[string]*workspaceEditorState{workspaceKey(root): state},
			}
			tester := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				a.editorLayoutView(c, root, state.Layout)
			}, 1200, 700)
			tester.Frame()
			tab, ok := tester.Find(file.Path)
			if !ok {
				t.Fatal("missing file tab")
			}
			x, y := tab.X+tab.W/2, tab.Y+tab.H/2
			tester.Press(x, y)
			tester.Advance(16 * time.Millisecond)
			if tc.direction == splitUp {
				tester.Move(x, y-14)
			} else {
				tester.Move(x, y+14)
			}
			tester.Advance(16 * time.Millisecond)
			tester.Move(tc.x, tc.y)
			tester.Advance(16 * time.Millisecond)
			if !state.PaneDrag.Drop || state.PaneDrag.Direction != tc.direction {
				t.Fatalf("edge drop chose drop=%v direction=%d, want direction=%d", state.PaneDrag.Drop, state.PaneDrag.Direction, tc.direction)
			}
			tester.Release(tc.x, tc.y)
			tester.Advance(16 * time.Millisecond)
			if state.Layout.Direction != tc.direction || len(layoutGroupIDs(state.Layout)) != 2 {
				t.Fatalf("edge drop produced direction=%d layout=%+v", state.Layout.Direction, state.Layout)
			}
			if tc.direction == splitLeft || tc.direction == splitUp {
				if state.Layout.First.Group != 0 || state.Layout.Second.Group != 1 {
					t.Fatalf("dragged editor was not placed at %s: %+v", tc.name, state.Layout)
				}
			} else if state.Layout.First.Group != 1 || state.Layout.Second.Group != 0 {
				t.Fatalf("dragged editor was not placed at %s: %+v", tc.name, state.Layout)
			}
			if state.ActiveGroup != 0 {
				t.Fatalf("dragged editor group lost focus: active=%d", state.ActiveGroup)
			}
		})
	}
}

func TestDraggingTabIntoExistingGroupMergesIt(t *testing.T) {
	root := t.TempDir()
	move := &workspaceFile{Path: "move.go", Editor: &editor.State{Buffer: editor.NewBuffer("package move")}}
	keep := &workspaceFile{Path: "keep.go", Editor: &editor.State{Buffer: editor.NewBuffer("package keep")}}
	target := &workspaceFile{Path: "target.go", Editor: &editor.State{Buffer: editor.NewBuffer("package target")}}
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{move.Path, keep.Path}, Active: move.Path},
			1: {Tabs: []string{target.Path}, Active: target.Path},
		},
		Layout:    &editorLayoutNode{ID: 1, Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}, SplitSize: 600},
		NextGroup: 2, NextLayoutID: 2, ActiveGroup: 0, ChatVisible: true,
	}
	a := &desktop{
		workspaceFiles: map[string]*workspaceFile{workspaceKey(root): move},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {
			editorDocumentKey(move.Path): move, editorDocumentKey(keep.Path): keep, editorDocumentKey(target.Path): target,
		}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.editorLayoutView(c, root, state.Layout)
	}, 1200, 700)
	tester.Frame()
	tab, ok := tester.Find(move.Path)
	if !ok {
		t.Fatal("missing movable file tab")
	}
	x, y := tab.X+tab.W/2, tab.Y+tab.H/2
	tester.Press(x, y)
	tester.Advance(16 * time.Millisecond)
	tester.Move(x, y+20)
	tester.Advance(16 * time.Millisecond)
	tester.Move(900, 350)
	tester.Advance(16 * time.Millisecond)
	if !state.PaneDrag.Merge || state.PaneDrag.Drop || state.PaneDrag.TargetGroup != 1 {
		t.Fatalf("drop over the right pane center was not marked for merge: %+v", state.PaneDrag)
	}
	tester.Release(900, 350)
	tester.Advance(16 * time.Millisecond)
	if len(state.Groups[0].Tabs) != 1 || state.Groups[0].Tabs[0] != keep.Path || state.Groups[0].Active != keep.Path {
		t.Fatalf("source group did not keep its other tab: %+v", state.Groups[0])
	}
	if len(state.Groups[1].Tabs) != 2 || state.Groups[1].Tabs[1] != move.Path || state.Groups[1].Active != move.Path {
		t.Fatalf("dragged tab did not merge into the target group: %+v", state.Groups[1])
	}
	if !state.ChatVisible || state.ActiveGroup != 1 {
		t.Fatalf("tab merge changed chat layout or focus: chatVisible=%v active=%d drag=%+v layout=%+v", state.ChatVisible, state.ActiveGroup, state.PaneDrag, state.Layout)
	}
}

func TestMergingLastTabRemovesEmptySourceGroup(t *testing.T) {
	root := t.TempDir()
	move := &workspaceFile{Path: "move.go", Editor: &editor.State{Buffer: editor.NewBuffer("package move")}}
	target := &workspaceFile{Path: "target.go", Editor: &editor.State{Buffer: editor.NewBuffer("package target")}}
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{move.Path}, Active: move.Path},
			1: {Tabs: []string{target.Path}, Active: target.Path},
		},
		Layout:    &editorLayoutNode{ID: 1, Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		NextGroup: 2, NextLayoutID: 2, ActiveGroup: 0, ChatVisible: true,
	}
	a := &desktop{
		workspaceFiles: map[string]*workspaceFile{workspaceKey(root): move},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {
			editorDocumentKey(move.Path): move, editorDocumentKey(target.Path): target,
		}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	a.mergeEditorTabIntoGroup(root, 0, 1, move.Path)
	if len(state.Groups) != 1 || state.Groups[1].Active != move.Path || len(state.Groups[1].Tabs) != 2 || state.Layout.Group != 1 {
		t.Fatalf("merge left an empty group or lost the moved tab: groups=%+v layout=%+v", state.Groups, state.Layout)
	}
	if state.ActiveGroup != 1 || !state.ChatVisible || !state.ActiveGroupPinned {
		t.Fatalf("merge did not retain target focus and chat layout: active=%d chatVisible=%v pinned=%v", state.ActiveGroup, state.ChatVisible, state.ActiveGroupPinned)
	}
}

func TestDockingLeavesChatAndOtherEditorGroupInPlace(t *testing.T) {
	root := t.TempDir()
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{"left.go"}, Active: "left.go"},
			1: {Tabs: []string{"right.go"}, Active: "right.go"},
		},
		Layout:    &editorLayoutNode{ID: 1, Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		NextGroup: 2, NextLayoutID: 2, ChatVisible: true, ActiveGroup: 1,
	}
	a := &desktop{editorStates: map[string]*workspaceEditorState{workspaceKey(root): state}}
	a.dockEditorGroupToEdge(root, 0, splitUp)
	if state.Layout.Direction != splitUp || state.Layout.First.Group != 0 || state.Layout.Second.Group != 1 {
		t.Fatalf("source group was not docked above target: %+v", state.Layout)
	}
	if state.ActiveGroup != 0 || !state.ChatVisible || state.ChatOnly {
		t.Fatalf("docking changed chat or editor selection unexpectedly: active=%d chatVisible=%v chatOnly=%v", state.ActiveGroup, state.ChatVisible, state.ChatOnly)
	}
	if len(state.Groups[0].Tabs) != 1 || state.Groups[0].Tabs[0] != "left.go" || len(state.Groups[1].Tabs) != 1 || state.Groups[1].Tabs[0] != "right.go" {
		t.Fatal("docking moved or lost editor tabs")
	}
}

func TestDockingOneTabLeavesOtherTabsInTheirOriginalGroup(t *testing.T) {
	root := t.TempDir()
	file := &workspaceFile{Path: "move.go", Editor: &editor.State{Buffer: editor.NewBuffer("package move")}}
	keep := &workspaceFile{Path: "keep.go", Editor: &editor.State{Buffer: editor.NewBuffer("package keep")}}
	other := &workspaceFile{Path: "other.go", Editor: &editor.State{Buffer: editor.NewBuffer("package other")}}
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{file.Path, keep.Path}, Active: file.Path},
			1: {Tabs: []string{other.Path}, Active: other.Path},
		},
		Layout:    &editorLayoutNode{ID: 1, Direction: splitDown, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		NextGroup: 2, NextLayoutID: 2, ActiveGroup: 0, ChatVisible: true,
	}
	a := &desktop{
		workspaceFiles: map[string]*workspaceFile{workspaceKey(root): file},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {
			editorDocumentKey(file.Path): file, editorDocumentKey(keep.Path): keep, editorDocumentKey(other.Path): other,
		}},
		editorStates: map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	a.dockEditorTabToEdge(root, 0, file.Path, splitRight)
	if state.Layout.Direction != splitRight || state.Layout.First.Direction != splitDown || state.Layout.Second.Group != 2 {
		t.Fatalf("file tab was not docked at the frame's right edge: %+v", state.Layout)
	}
	if len(state.Groups[0].Tabs) != 1 || state.Groups[0].Tabs[0] != keep.Path || state.Groups[0].Active != keep.Path {
		t.Fatalf("remaining source tabs were not preserved: %+v", state.Groups[0])
	}
	if state.Groups[2].Active != file.Path || len(state.Groups[2].Tabs) != 1 || state.ActiveGroup != 2 {
		t.Fatalf("dragged tab was not opened in its own active group: %+v active=%d", state.Groups[2], state.ActiveGroup)
	}
	if !state.ChatVisible {
		t.Fatal("docking a file tab changed the chat layout")
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

func TestClosingGroupWithNoActiveFileReleasesItsFiles(t *testing.T) {
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
	if state.Groups[0].Active != "" || a.activeFile() != nil || a.editorDocument(root, file.Path) != nil || file.Editor != nil {
		t.Fatalf("closed group file was retained: activePath=%q activeFile=%p doc=%p editor=%p", state.Groups[0].Active, a.activeFile(), a.editorDocument(root, file.Path), file.Editor)
	}
}

func TestSingleEditorGroupCanCloseAllOpenFiles(t *testing.T) {
	root := t.TempDir()
	first := &workspaceFile{Path: "first.go", Editor: &editor.State{Buffer: editor.NewBuffer("package first")}}
	second := &workspaceFile{Path: "second.go", Editor: &editor.State{Buffer: editor.NewBuffer("package second")}}
	state := &workspaceEditorState{
		Groups:    map[int]*editorGroupState{0: {Tabs: []string{first.Path, second.Path}, Active: second.Path}},
		Layout:    &editorLayoutNode{Group: 0},
		NextGroup: 1,
	}
	a := &desktop{
		page:                       "chat",
		explorer:                   true,
		prefs:                      defaultPrefs(),
		tabs:                       []*conversation{{Workspace: root}},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): second},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(first.Path): first, editorDocumentKey(second.Path): second}},
		editorStates:               map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		a.editorLayoutView(c, root, state.Layout)
	}, 1200, 700)
	tester.Frame()
	if _, ok := tester.Find(locales.Text("关闭编辑器组")); !ok {
		t.Fatal("single editor group has no close action")
	}
	a.closeEditorGroup(root, 0)
	if len(state.Groups) != 1 || !state.Layout.leaf() || state.Layout.Group != 1 || len(state.Groups[1].Tabs) != 0 || state.Groups[1].Active != "" {
		t.Fatalf("closing the only group did not replace it with an empty default pane: %+v", state)
	}
	if state.ChatVisible || !state.ChatOnly {
		t.Fatalf("closing the only group did not return to the conversation: chatVisible=%v chatOnly=%v", state.ChatVisible, state.ChatOnly)
	}
	if a.editorDocument(root, first.Path) != nil || a.editorDocument(root, second.Path) != nil || first.Editor != nil || second.Editor != nil || a.activeFile() != nil {
		t.Fatal("closing the only group retained an open file")
	}
}

func TestClosingLastEditorTabAlsoClosesItsGroup(t *testing.T) {
	root := t.TempDir()
	file := &workspaceFile{Path: "last.go", Editor: &editor.State{Buffer: editor.NewBuffer("package last")}}
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{0: {Tabs: []string{file.Path}, Active: file.Path}},
		Layout: &editorLayoutNode{Group: 0}, NextGroup: 1,
	}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): file},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(file.Path): file}},
		editorStates:               map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	a.closeEditorTab(root, file.Path, 0)
	if len(state.Groups) != 1 || state.Layout.Group != 1 || state.Groups[1].Active != "" || len(state.Groups[1].Tabs) != 0 {
		t.Fatalf("closing the final file left the old editor group open: %+v", state)
	}
	if a.editorDocument(root, file.Path) != nil || file.Editor != nil || a.activeFile() != nil || !state.ChatOnly {
		t.Fatal("closing the final file did not release it and return to chat")
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
	if state.ActiveGroup != 0 || state.Groups[0].Active != active.Path || a.activeFile() != active || a.editorDocument(root, inactive.Path) != nil {
		t.Fatalf("closing the inactive group changed the visible file or retained its file: group=%d path=%q active=%p inactive=%p", state.ActiveGroup, state.Groups[0].Active, a.activeFile(), a.editorDocument(root, inactive.Path))
	}
}

func TestClosingEditorGroupClosesItsFiles(t *testing.T) {
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
	if state.Split || state.ActiveGroup != 0 || state.Groups[0].Active != first.Path || len(state.Groups[0].Tabs) != 1 || state.Groups[0].Tabs[0] != first.Path {
		t.Fatal("closing a group changed the remaining group's tabs")
	}
	if a.activeFile() != first || a.editorDocument(root, second.Path) != nil || second.Editor != nil || len(a.editorDocumentsByWorkspace[workspaceKey(root)]) != 1 {
		t.Fatal("closing a group did not release its file while preserving the remaining one")
	}
}

func TestClosingGroupKeepsFilesStillOpenInAnotherGroup(t *testing.T) {
	root := t.TempDir()
	shared := &workspaceFile{Path: "shared.txt", Editor: &editor.State{Buffer: editor.NewBuffer("shared")}}
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{shared.Path}, Active: shared.Path},
			1: {Tabs: []string{shared.Path}, Active: shared.Path},
		},
		Layout:      &editorLayoutNode{Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		ActiveGroup: 1,
	}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		workspaceFiles:             map[string]*workspaceFile{workspaceKey(root): shared},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{workspaceKey(root): {editorDocumentKey(shared.Path): shared}},
		editorStates:               map[string]*workspaceEditorState{workspaceKey(root): state},
	}
	a.closeEditorGroup(root, 1)
	if len(state.Groups) != 1 || state.Groups[0].Active != shared.Path || a.editorDocument(root, shared.Path) != shared || shared.Editor == nil {
		t.Fatal("closing one group released a document that remains open in another")
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
	a.toggleChatEditorLayout(root)
	if !state.ChatOnly || !state.ChatVisible {
		t.Fatal("layout toggle did not enable side-by-side while preserving the selected session")
	}
	a.toggleChatEditorLayout(root)
	if !state.ChatOnly || state.ChatVisible {
		t.Fatal("layout toggle did not return to single-pane session view")
	}
	activateEditorGroup(state, 0, true)
	if state.ChatOnly {
		t.Fatal("editing in a pane did not select file content as the single-pane target")
	}
	a.toggleChatEditorLayout(root)
	if state.ChatOnly || !state.ChatVisible {
		t.Fatal("layout toggle did not enable side-by-side with the editor selected")
	}
	activateChatContent(state, true)
	a.toggleChatEditorLayout(root)
	if !state.ChatOnly || state.ChatVisible {
		t.Fatal("layout toggle did not preserve the last selected session in single-pane mode")
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
	a.toggleChatEditorLayout(root)
	if !state.ChatVisible || !a.explorer || a.editorDocument(root, file.Path) != file || len(state.Groups[0].Tabs) != 1 {
		t.Fatal("switching to side-by-side hid the file tree or closed an editor document")
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
