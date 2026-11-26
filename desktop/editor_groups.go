// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"context"
	"path/filepath"
	"strings"

	"ally-dev/locales"
)

type pendingEditorFileLoad struct {
	key    string
	ctx    context.Context
	cancel context.CancelFunc
}

type editorGroupState struct {
	Tabs   []string
	Active string
}

type editorSplitDirection uint8

type editorLayoutAction uint8

const (
	layoutSingle editorLayoutAction = iota
	layoutChatAndEditor
	layoutSplit
)

const (
	splitLeft editorSplitDirection = iota
	splitRight
	splitUp
	splitDown
)

type editorLayoutNode struct {
	ID        uint64
	Group     int
	Direction editorSplitDirection
	First     *editorLayoutNode
	Second    *editorLayoutNode
	SplitSize float32
}

func (n *editorLayoutNode) leaf() bool { return n != nil && n.First == nil && n.Second == nil }

type workspaceEditorState struct {
	Groups       map[int]*editorGroupState
	Layout       *editorLayoutNode
	NextGroup    int
	NextLayoutID uint64
	ActiveGroup  int
	Split        bool
	ChatVisible  bool
	ChatOnly     bool
	ChatWidth    float32
}

func editorFileLoadKey(root, path string) string {
	return workspaceKey(root) + "\x00" + editorDocumentKey(path)
}

func (a *desktop) beginEditorFileLoad(root, path string) (*pendingEditorFileLoad, context.Context) {
	key := editorFileLoadKey(root, path)
	if a.editorFileLoads == nil {
		a.editorFileLoads = make(map[string]*pendingEditorFileLoad)
	}
	if previous := a.editorFileLoads[key]; previous != nil {
		delete(a.editorFileLoads, key)
		previous.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	load := &pendingEditorFileLoad{key: key, ctx: ctx, cancel: cancel}
	a.editorFileLoads[key] = load
	return load, ctx
}

func (a *desktop) finishEditorFileLoad(load *pendingEditorFileLoad) bool {
	if load == nil || a.editorFileLoads[load.key] != load || load.ctx.Err() != nil {
		return false
	}
	delete(a.editorFileLoads, load.key)
	load.cancel()
	return true
}

func (a *desktop) cancelEditorFileLoad(root, path string) {
	key := editorFileLoadKey(root, path)
	if load := a.editorFileLoads[key]; load != nil {
		delete(a.editorFileLoads, key)
		load.cancel()
	}
}

func (a *desktop) cancelEditorFileLoads(root string) {
	prefix := workspaceKey(root) + "\x00"
	for key, load := range a.editorFileLoads {
		if root == "" || strings.HasPrefix(key, prefix) {
			delete(a.editorFileLoads, key)
			load.cancel()
		}
	}
}

func editorDocumentKey(path string) string {
	return strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
}

func (a *desktop) editorState(root string) *workspaceEditorState {
	key := workspaceKey(root)
	if a.editorStates == nil {
		a.editorStates = make(map[string]*workspaceEditorState)
	}
	state := a.editorStates[key]
	if state == nil {
		state = &workspaceEditorState{
			Groups:       map[int]*editorGroupState{0: {}},
			Layout:       &editorLayoutNode{Group: 0},
			NextGroup:    1,
			NextLayoutID: 1,
			ChatWidth:    620,
		}
		a.editorStates[key] = state
		if current := a.workspaceFiles[key]; current != nil {
			docs := a.editorDocuments(root)
			docs[editorDocumentKey(current.Path)] = current
			state.Groups[0] = &editorGroupState{Tabs: []string{current.Path}, Active: current.Path}
		}
	}
	return state
}

func (a *desktop) editorDocuments(root string) map[string]*workspaceFile {
	key := workspaceKey(root)
	if a.editorDocumentsByWorkspace == nil {
		a.editorDocumentsByWorkspace = make(map[string]map[string]*workspaceFile)
	}
	docs := a.editorDocumentsByWorkspace[key]
	if docs == nil {
		docs = make(map[string]*workspaceFile)
		a.editorDocumentsByWorkspace[key] = docs
	}
	if current := a.workspaceFiles[key]; current != nil {
		docs[editorDocumentKey(current.Path)] = current
	}
	return docs
}

func (a *desktop) editorDocument(root, path string) *workspaceFile {
	if path == "" {
		return nil
	}
	key := workspaceKey(root)
	if docs := a.editorDocumentsByWorkspace[key]; docs != nil {
		return docs[editorDocumentKey(path)]
	}
	if file := a.workspaceFiles[key]; file != nil && editorDocumentKey(file.Path) == editorDocumentKey(path) {
		return file
	}
	return nil
}

func activateEditorGroup(state *workspaceEditorState, group int, interacting bool) {
	if state != nil && interacting && state.Groups[group] != nil && editorLayoutHasGroup(state.Layout, group) {
		state.ActiveGroup = group
		state.ChatOnly = false
	}
}

func activateChatContent(state *workspaceEditorState, interacting bool) {
	if state != nil && interacting {
		state.ChatOnly = true
	}
}

func editorLayoutHasGroup(node *editorLayoutNode, group int) bool {
	if node == nil {
		return false
	}
	if node.leaf() {
		return node.Group == group
	}
	return editorLayoutHasGroup(node.First, group) || editorLayoutHasGroup(node.Second, group)
}

func editorLayoutGroups(node *editorLayoutNode, groups *[]int) {
	if node == nil {
		return
	}
	if node.leaf() {
		*groups = append(*groups, node.Group)
		return
	}
	editorLayoutGroups(node.First, groups)
	editorLayoutGroups(node.Second, groups)
}

func layoutGroupIDs(node *editorLayoutNode) []int {
	groups := make([]int, 0, 4)
	editorLayoutGroups(node, &groups)
	return groups
}

func replaceEditorLayoutLeaf(node **editorLayoutNode, group int, replacement *editorLayoutNode) bool {
	if *node == nil {
		return false
	}
	if (*node).leaf() {
		if (*node).Group != group {
			return false
		}
		*node = replacement
		return true
	}
	return replaceEditorLayoutLeaf(&(*node).First, group, replacement) || replaceEditorLayoutLeaf(&(*node).Second, group, replacement)
}

func editorGroupContainsDocument(group *editorGroupState, path string) bool {
	if group == nil || path == "" {
		return false
	}
	for _, tab := range group.Tabs {
		if editorDocumentKey(tab) == editorDocumentKey(path) {
			return true
		}
	}
	return false
}

func (a *desktop) editorGroupContaining(root, path string) int {
	state := a.editorStates[workspaceKey(root)]
	if state == nil || path == "" {
		return -1
	}
	if editorGroupContainsDocument(state.Groups[state.ActiveGroup], path) && editorLayoutHasGroup(state.Layout, state.ActiveGroup) {
		return state.ActiveGroup
	}
	for _, group := range layoutGroupIDs(state.Layout) {
		if editorGroupContainsDocument(state.Groups[group], path) {
			return group
		}
	}
	return -1
}

func (a *desktop) editorGroupIndex(root string) int {
	state := a.editorStates[workspaceKey(root)]
	if state != nil && state.Groups[state.ActiveGroup] != nil && editorLayoutHasGroup(state.Layout, state.ActiveGroup) {
		return state.ActiveGroup
	}
	if state != nil {
		if group := firstEditorLayoutGroup(state.Layout); group >= 0 {
			return group
		}
	}
	return 0
}

func editorTabInsertionIndex(paths []string, positions []tabPosition, draggedID string, pointerX, hysteresis float32) int {
	if len(paths) < 2 || len(paths) != len(positions) || draggedID == "" {
		return -1
	}
	from := -1
	for i, path := range paths {
		if editorDocumentKey(path) == draggedID {
			from = i
			break
		}
	}
	if from < 0 {
		return -1
	}
	current, destination := from, 0
	leftBarrier, rightBarrier := float32(-1<<30), float32(1<<30)
	virtualIndex, virtualX := 0, positions[0].x
	for i, path := range paths {
		if editorDocumentKey(path) == draggedID {
			continue
		}
		mid := virtualX + positions[i].width/2
		if virtualIndex == current-1 {
			leftBarrier = mid
		}
		if virtualIndex == current {
			rightBarrier = mid
		}
		if pointerX >= mid {
			destination++
		}
		virtualX += positions[i].width
		virtualIndex++
	}
	if destination > current && pointerX < rightBarrier+hysteresis {
		destination = current
	} else if destination < current && pointerX > leftBarrier-hysteresis {
		destination = current
	}
	return destination
}

func editorTabDropTarget(paths []string, draggedID string, destination int) string {
	from := -1
	for i, path := range paths {
		if editorDocumentKey(path) == draggedID {
			from = i
			break
		}
	}
	if from < 0 || destination < 0 || destination >= len(paths) {
		return ""
	}
	if destination == from {
		return draggedID
	}
	if destination < from {
		return editorDocumentKey(paths[destination])
	}
	if destination+1 < len(paths) {
		return editorDocumentKey(paths[destination+1])
	}
	return tabDropAtEnd
}

func moveEditorTabToIndex(paths *[]string, draggedID string, destination int) bool {
	tabs := *paths
	from := -1
	for i, path := range tabs {
		if editorDocumentKey(path) == draggedID {
			from = i
			break
		}
	}
	if from < 0 || destination < 0 || destination >= len(tabs) || from == destination {
		return false
	}
	dragged := tabs[from]
	if from < destination {
		copy(tabs[from:destination], tabs[from+1:destination+1])
	} else {
		copy(tabs[destination+1:from+1], tabs[destination:from])
	}
	tabs[destination] = dragged
	return true
}

func (a *desktop) addEditorDocumentToGroup(root, path string, group int) bool {
	state := a.editorState(root)
	if state.Groups[group] == nil || !editorLayoutHasGroup(state.Layout, group) {
		group = a.editorGroupIndex(root)
	}
	file := a.editorDocument(root, path)
	if file == nil || state.Groups[group] == nil {
		return false
	}
	pane := state.Groups[group]
	for _, existing := range pane.Tabs {
		if editorDocumentKey(existing) == editorDocumentKey(path) {
			return true
		}
	}
	pane.Tabs = append(pane.Tabs, file.Path)
	return true
}

func (a *desktop) selectEditorDocument(root, path string, group int, add bool) {
	state := a.editorState(root)
	if state.Groups[group] == nil || !editorLayoutHasGroup(state.Layout, group) {
		group = a.editorGroupIndex(root)
	}
	if state.Groups[group] == nil {
		return
	}
	state.ActiveGroup = group
	state.ChatOnly = false
	if path == "" {
		state.Groups[group].Active = ""
		if a.workspaceFiles[workspaceKey(root)] != nil {
			a.workspacePreview.suspendMounted(root, "")
		}
		delete(a.workspaceFiles, workspaceKey(root))
		return
	}
	file := a.editorDocument(root, path)
	if file == nil || add && !a.addEditorDocumentToGroup(root, path, group) {
		return
	}
	state.Groups[group].Active = file.Path
	key := workspaceKey(root)
	if current := a.workspaceFiles[key]; current != file {
		a.workspacePreview.suspendMounted(root, file.Path)
	}
	if a.workspaceFiles == nil {
		a.workspaceFiles = make(map[string]*workspaceFile)
	}
	a.workspaceFiles[key] = file
}

func (a *desktop) openLoadedEditorDocument(root, path string, group int) {
	state := a.editorState(root)
	if state.Groups[group] == nil || !editorLayoutHasGroup(state.Layout, group) {
		group = a.editorGroupIndex(root)
	}
	if state.ActiveGroup != group {
		if a.addEditorDocumentToGroup(root, path, group) {
			if file := a.editorDocument(root, path); file != nil {
				state.Groups[group].Active = file.Path
			}
		}
		return
	}
	a.selectEditorDocument(root, path, group, true)
}

func (a *desktop) focusEditorGroup(root string, group int) {
	state := a.editorState(root)
	if state.Groups[group] == nil || !editorLayoutHasGroup(state.Layout, group) {
		return
	}
	if path := state.Groups[group].Active; path != "" {
		a.selectEditorDocument(root, path, group, false)
	} else {
		a.selectEditorDocument(root, "", group, false)
	}
}

func removeEditorLayoutLeaf(node **editorLayoutNode, group int) bool {
	if *node == nil || (*node).leaf() {
		return false
	}
	if (*node).First.leaf() && (*node).First.Group == group {
		*node = (*node).Second
		return true
	}
	if (*node).Second.leaf() && (*node).Second.Group == group {
		*node = (*node).First
		return true
	}
	return removeEditorLayoutLeaf(&(*node).First, group) || removeEditorLayoutLeaf(&(*node).Second, group)
}

func firstEditorLayoutGroup(node *editorLayoutNode) int {
	if node == nil {
		return -1
	}
	if node.leaf() {
		return node.Group
	}
	if group := firstEditorLayoutGroup(node.First); group >= 0 {
		return group
	}
	return firstEditorLayoutGroup(node.Second)
}

func (a *desktop) showSinglePane(root string) {
	a.editorState(root).ChatVisible = false
}

func (a *desktop) showEditorOnly(root string) {
	state := a.editorState(root)
	state.ChatOnly = false
	state.ChatVisible = false
}

func (a *desktop) showChatAndEditor(root string) {
	a.editorState(root).ChatVisible = true
}

func (a *desktop) splitEditorGroup(root string, direction editorSplitDirection) {
	state := a.editorState(root)
	active := a.editorGroupIndex(root)
	newGroup := state.NextGroup
	for state.Groups[newGroup] != nil {
		newGroup++
	}
	state.NextGroup = newGroup + 1
	layoutID := state.NextLayoutID
	if layoutID == 0 {
		layoutID = 1
	}
	state.NextLayoutID = layoutID + 1
	state.Groups[newGroup] = &editorGroupState{}
	activeLeaf := &editorLayoutNode{Group: active}
	newLeaf := &editorLayoutNode{Group: newGroup}
	node := &editorLayoutNode{ID: layoutID, Direction: direction, SplitSize: 480}
	if direction == splitLeft || direction == splitUp {
		node.First, node.Second = newLeaf, activeLeaf
	} else {
		node.First, node.Second = activeLeaf, newLeaf
	}
	if !replaceEditorLayoutLeaf(&state.Layout, active, node) {
		delete(state.Groups, newGroup)
		return
	}
	state.Split = true
	state.ActiveGroup = newGroup
}

func (a *desktop) closeEditorGroup(root string, group int) {
	state := a.editorState(root)
	if state.Groups[group] == nil || len(state.Groups) <= 1 {
		return
	}
	closed := state.Groups[group]
	wasActive := state.ActiveGroup == group
	if !removeEditorLayoutLeaf(&state.Layout, group) {
		return
	}
	fallback := firstEditorLayoutGroup(state.Layout)
	target := state.Groups[fallback]
	for _, path := range closed.Tabs {
		found := false
		for _, existing := range target.Tabs {
			if editorDocumentKey(existing) == editorDocumentKey(path) {
				found = true
				break
			}
		}
		if !found {
			target.Tabs = append(target.Tabs, path)
		}
	}
	if wasActive {
		if closed.Active != "" {
			target.Active = closed.Active
		} else if target.Active == "" && len(target.Tabs) > 0 {
			target.Active = target.Tabs[0]
		}
	}
	delete(state.Groups, group)
	state.Split = len(state.Groups) > 1
	if state.ActiveGroup == group {
		state.ActiveGroup = fallback
	}
	activeGroup := state.ActiveGroup
	if state.Groups[activeGroup] == nil || !editorLayoutHasGroup(state.Layout, activeGroup) {
		activeGroup = fallback
		state.ActiveGroup = fallback
	}
	a.selectEditorDocument(root, state.Groups[activeGroup].Active, activeGroup, false)
}

func (a *desktop) closeEditorTab(root, path string, group int) {
	file := a.editorDocument(root, path)
	if file == nil {
		return
	}
	close := func() {
		state := a.editorState(root)
		g := state.Groups[group]
		if g == nil || !editorLayoutHasGroup(state.Layout, group) {
			return
		}
		filtered := g.Tabs[:0]
		for _, tab := range g.Tabs {
			if editorDocumentKey(tab) != editorDocumentKey(path) {
				filtered = append(filtered, tab)
			}
		}
		g.Tabs = filtered
		if editorDocumentKey(g.Active) == editorDocumentKey(path) {
			g.Active = ""
			if len(g.Tabs) > 0 {
				g.Active = g.Tabs[0]
			}
		}
		stillOpen := false
		for _, pane := range state.Groups {
			for _, tab := range pane.Tabs {
				if editorDocumentKey(tab) == editorDocumentKey(path) {
					stillOpen = true
				}
			}
		}
		if !stillOpen {
			a.releaseWorkspaceDocument(root, file)
		}
		if state.ActiveGroup == group && g.Active == "" {
			for _, candidate := range layoutGroupIDs(state.Layout) {
				if state.Groups[candidate].Active != "" {
					state.ActiveGroup = candidate
					break
				}
			}
		}
		if current := state.Groups[state.ActiveGroup].Active; current != "" {
			a.selectEditorDocument(root, current, state.ActiveGroup, false)
		} else {
			a.selectEditorDocument(root, "", state.ActiveGroup, false)
		}
	}
	if file.Saving {
		a.notify(locales.Text("文件正在保存，请稍后关闭"))
		return
	}
	if referencedElsewhere := a.documentInOtherGroup(root, path, group); referencedElsewhere {
		close()
		return
	}
	if file.Editor != nil && file.Editor.Buffer != nil && file.Editor.Buffer.Modified() {
		a.confirmDiscardFile(root, file, close)
		return
	}
	close()
}

func (a *desktop) closeEditorDocumentEverywhere(root string, file *workspaceFile) {
	if file == nil {
		return
	}
	if file.Saving {
		a.notify(locales.Text("文件正在保存，请稍后关闭"))
		return
	}
	close := func() {
		a.releaseWorkspaceDocument(root, file)
		state := a.editorStates[workspaceKey(root)]
		if state == nil {
			return
		}
		if state.Groups[state.ActiveGroup] == nil || !editorLayoutHasGroup(state.Layout, state.ActiveGroup) {
			state.ActiveGroup = firstEditorLayoutGroup(state.Layout)
		}
		if state.ActiveGroup >= 0 && state.Groups[state.ActiveGroup].Active == "" {
			for _, candidate := range layoutGroupIDs(state.Layout) {
				if state.Groups[candidate].Active != "" {
					state.ActiveGroup = candidate
					break
				}
			}
		}
		current := state.Groups[state.ActiveGroup].Active
		a.selectEditorDocument(root, current, state.ActiveGroup, false)
	}
	if file.Editor != nil && file.Editor.Buffer != nil && file.Editor.Buffer.Modified() {
		a.confirmDiscardFile(root, file, close)
		return
	}
	close()
}

func (a *desktop) documentInOtherGroup(root, path string, group int) bool {
	state := a.editorStates[workspaceKey(root)]
	if state == nil {
		return false
	}
	for index, pane := range state.Groups {
		if index == group {
			continue
		}
		for _, tab := range pane.Tabs {
			if editorDocumentKey(tab) == editorDocumentKey(path) {
				return true
			}
		}
	}
	return false
}

func (a *desktop) hasEditorFiles(root string) bool {
	state := a.editorStates[workspaceKey(root)]
	if state == nil {
		return a.workspaceFiles[workspaceKey(root)] != nil
	}
	for _, group := range layoutGroupIDs(state.Layout) {
		if pane := state.Groups[group]; pane != nil && pane.Active != "" {
			return true
		}
	}
	return false
}

func (a *desktop) allWorkspaceFiles() []*workspaceFile {
	seen := make(map[*workspaceFile]struct{})
	files := make([]*workspaceFile, 0, len(a.workspaceFiles))
	add := func(file *workspaceFile) {
		if file == nil {
			return
		}
		if _, ok := seen[file]; ok {
			return
		}
		seen[file] = struct{}{}
		files = append(files, file)
	}
	for _, file := range a.workspaceFiles {
		add(file)
	}
	for _, docs := range a.editorDocumentsByWorkspace {
		for _, file := range docs {
			add(file)
		}
	}
	return files
}
