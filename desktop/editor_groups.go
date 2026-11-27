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

const editorDockDragSlop float32 = 10

type editorPaneDrag struct {
	Active, Pending bool
	Source          int
	FilePath        string
	PointerX        float32
	PointerY        float32
	Drop, Merge     bool
	TargetGroup     int
	Direction       editorSplitDirection
}

type editorGroupBounds struct {
	X, Y, W, H float32
}

type workspaceEditorState struct {
	Groups            map[int]*editorGroupState
	Layout            *editorLayoutNode
	NextGroup         int
	NextLayoutID      uint64
	ActiveGroup       int
	ActiveGroupPinned bool
	Split             bool
	ChatVisible       bool
	ChatOnly          bool
	ChatWidth         float32
	PreviewGroup      int
	PreviewFile       string
	PreviewGroupSet   bool
	PaneDrag          editorPaneDrag
	PaneBounds        map[int]editorGroupBounds
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

func (a *desktop) workspacePreviewFileAllowed(root string, file *workspaceFile, group int) bool {
	state := a.editorStates[workspaceKey(root)]
	if state == nil || file == nil || state.Groups[group] == nil ||
		!editorLayoutHasGroup(state.Layout, group) || editorDocumentKey(state.Groups[group].Active) != editorDocumentKey(file.Path) {
		return false
	}
	if state.PreviewGroupSet {
		return state.PreviewGroup == group && editorDocumentKey(state.PreviewFile) == editorDocumentKey(file.Path)
	}
	return state.ActiveGroup == group
}

func (a *desktop) workspaceMediaPreviewVisible(file *workspaceFile) bool {
	t := a.current()
	if t == nil || file == nil || !a.explorer || a.page != "chat" || a.mainWindowHidden || a.win != nil && a.win.IsMinimized() {
		return false
	}
	state := a.editorStates[workspaceKey(t.Workspace)]
	if state == nil {
		return false
	}
	group := a.editorGroupContaining(t.Workspace, file.Path)
	if state.PreviewGroupSet && editorDocumentKey(state.PreviewFile) == editorDocumentKey(file.Path) {
		group = state.PreviewGroup
	}
	if !a.workspacePreviewFileAllowed(t.Workspace, file, group) {
		return false
	}
	return a.workspacePreview.mountedFor(a.workspaceFilePreviewKey(file)) != nil
}

func (a *desktop) reconcileWorkspacePreviewGroup(root string) {
	state := a.editorStates[workspaceKey(root)]
	if state == nil || !state.PreviewGroupSet {
		return
	}
	if state.Groups[state.PreviewGroup] != nil && editorLayoutHasGroup(state.Layout, state.PreviewGroup) &&
		editorGroupContainsDocument(state.Groups[state.PreviewGroup], state.PreviewFile) {
		return
	}
	if group := a.editorGroupContaining(root, state.PreviewFile); group >= 0 {
		state.PreviewGroup = group
		return
	}
	state.PreviewGroupSet, state.PreviewFile = false, ""
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

func (a *desktop) prepareWorkspacePreviewSelection(root string, file *workspaceFile, group int) {
	state := a.editorState(root)
	if fileNeedsWebView(file) {
		a.workspacePreview.suspendMountedExcept(root, file.Path)
		state.PreviewGroup, state.PreviewFile, state.PreviewGroupSet = group, file.Path, true
		return
	}
	if !state.PreviewGroupSet || state.PreviewGroup != group {
		if !state.PreviewGroupSet {
			a.workspacePreview.suspendMounted(root, "")
		}
		return
	}
	preview := a.editorDocument(root, state.PreviewFile)
	if preview != nil && workspaceFileKindForPath(preview.Path).hasMediaPlayer() {
		a.pauseWorkspaceMediaPreview(root, preview)
		return
	}
	a.workspacePreview.suspendMounted(root, "")
	state.PreviewGroupSet, state.PreviewFile = false, ""
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
	state.ActiveGroupPinned = false
	state.ChatOnly = false
	if path == "" {
		a.prepareWorkspacePreviewSelection(root, nil, group)
		state.Groups[group].Active = ""
		delete(a.workspaceFiles, workspaceKey(root))
		return
	}
	file := a.editorDocument(root, path)
	if file == nil || add && !a.addEditorDocumentToGroup(root, path, group) {
		return
	}
	state.Groups[group].Active = file.Path
	a.prepareWorkspacePreviewSelection(root, file, group)
	key := workspaceKey(root)
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
				a.prepareWorkspacePreviewSelection(root, file, group)
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

func (a *desktop) toggleChatEditorLayout(root string) {
	state := a.editorState(root)
	state.ChatVisible = !state.ChatVisible
}

func (a *desktop) showEditorOnly(root string) {
	state := a.editorState(root)
	state.ChatOnly = false
	state.ChatVisible = false
}

func nextEditorLayoutID(state *workspaceEditorState) uint64 {
	id := state.NextLayoutID
	if id == 0 {
		id = 1
	}
	state.NextLayoutID = id + 1
	return id
}

func newEditorSplitNode(state *workspaceEditorState, direction editorSplitDirection, target, added int) *editorLayoutNode {
	node := &editorLayoutNode{ID: nextEditorLayoutID(state), Direction: direction, SplitSize: 480}
	targetLeaf, addedLeaf := &editorLayoutNode{Group: target}, &editorLayoutNode{Group: added}
	if direction == splitLeft || direction == splitUp {
		node.First, node.Second = addedLeaf, targetLeaf
	} else {
		node.First, node.Second = targetLeaf, addedLeaf
	}
	return node
}

func editorDockDirection(x, y, width, height float32) (editorSplitDirection, bool) {
	if width <= 0 || height <= 0 {
		return 0, false
	}
	edges := []struct {
		distance  float32
		direction editorSplitDirection
	}{
		{x / width, splitLeft},
		{(width - x) / width, splitRight},
		{y / height, splitUp},
		{(height - y) / height, splitDown},
	}
	nearest := edges[0]
	for _, edge := range edges[1:] {
		if edge.distance < nearest.distance {
			nearest = edge
		}
	}
	return nearest.direction, nearest.distance <= 0.25
}

func addEditorGroup(state *workspaceEditorState) int {
	group := state.NextGroup
	for state.Groups[group] != nil {
		group++
	}
	state.NextGroup = group + 1
	state.Groups[group] = &editorGroupState{}
	return group
}

func (a *desktop) splitEditorGroup(root string, direction editorSplitDirection) {
	state := a.editorState(root)
	active := a.editorGroupIndex(root)
	newGroup := addEditorGroup(state)
	node := newEditorSplitNode(state, direction, active, newGroup)
	if !replaceEditorLayoutLeaf(&state.Layout, active, node) {
		delete(state.Groups, newGroup)
		return
	}
	state.Split = true
	state.ActiveGroup = newGroup
}

func (a *desktop) dockEditorTabToEdge(root string, source int, path string, direction editorSplitDirection) {
	state := a.editorState(root)
	pane := state.Groups[source]
	file := a.editorDocument(root, path)
	if pane == nil || file == nil || !editorLayoutHasGroup(state.Layout, source) {
		return
	}
	found := false
	for _, tab := range pane.Tabs {
		if editorDocumentKey(tab) == editorDocumentKey(path) {
			found = true
			break
		}
	}
	if !found {
		return
	}
	if len(pane.Tabs) == 1 {
		a.dockEditorGroupToEdge(root, source, direction)
		return
	}
	filtered := pane.Tabs[:0]
	for _, tab := range pane.Tabs {
		if editorDocumentKey(tab) != editorDocumentKey(path) {
			filtered = append(filtered, tab)
		}
	}
	pane.Tabs = filtered
	if editorDocumentKey(pane.Active) == editorDocumentKey(path) {
		pane.Active = pane.Tabs[0]
	}
	newGroup := addEditorGroup(state)
	state.Groups[newGroup] = &editorGroupState{Tabs: []string{file.Path}, Active: file.Path}
	node := &editorLayoutNode{ID: nextEditorLayoutID(state), Direction: direction, SplitSize: 480}
	fileLeaf := &editorLayoutNode{Group: newGroup}
	if direction == splitLeft || direction == splitUp {
		node.First, node.Second = fileLeaf, state.Layout
	} else {
		node.First, node.Second = state.Layout, fileLeaf
	}
	state.Layout = node
	state.ActiveGroup = newGroup
	state.ChatOnly = false
	state.Split = true
	a.selectEditorDocument(root, file.Path, newGroup, false)
	state.ActiveGroupPinned = true
}

func (a *desktop) mergeEditorTabIntoGroup(root string, source, target int, path string) {
	state := a.editorState(root)
	sourcePane, targetPane := state.Groups[source], state.Groups[target]
	file := a.editorDocument(root, path)
	if source == target || sourcePane == nil || targetPane == nil || file == nil ||
		!editorLayoutHasGroup(state.Layout, source) || !editorLayoutHasGroup(state.Layout, target) {
		return
	}
	found, alreadyOpen := false, false
	for _, tab := range sourcePane.Tabs {
		found = found || editorDocumentKey(tab) == editorDocumentKey(path)
	}
	for _, tab := range targetPane.Tabs {
		alreadyOpen = alreadyOpen || editorDocumentKey(tab) == editorDocumentKey(path)
	}
	if !found {
		return
	}
	if !alreadyOpen {
		targetPane.Tabs = append(targetPane.Tabs, file.Path)
	}
	filtered := sourcePane.Tabs[:0]
	for _, tab := range sourcePane.Tabs {
		if editorDocumentKey(tab) != editorDocumentKey(path) {
			filtered = append(filtered, tab)
		}
	}
	sourcePane.Tabs = filtered
	if editorDocumentKey(sourcePane.Active) == editorDocumentKey(path) {
		sourcePane.Active = ""
		if len(sourcePane.Tabs) > 0 {
			sourcePane.Active = sourcePane.Tabs[0]
		}
	}
	targetPane.Active = file.Path
	if len(sourcePane.Tabs) == 0 {
		if removeEditorLayoutLeaf(&state.Layout, source) {
			delete(state.Groups, source)
		}
	}
	state.ActiveGroup = target
	state.ChatOnly = false
	state.Split = len(state.Groups) > 1
	a.selectEditorDocument(root, file.Path, target, false)
	state.ActiveGroupPinned = true
}

func (a *desktop) dockEditorGroupToEdge(root string, source int, direction editorSplitDirection) {
	state := a.editorState(root)
	if state.Groups[source] == nil || !editorLayoutHasGroup(state.Layout, source) {
		return
	}
	groups := layoutGroupIDs(state.Layout)
	if len(groups) == 1 {
		newGroup := addEditorGroup(state)
		node := newEditorSplitNode(state, direction, newGroup, source)
		if !replaceEditorLayoutLeaf(&state.Layout, source, node) {
			delete(state.Groups, newGroup)
			return
		}
	} else {
		if !removeEditorLayoutLeaf(&state.Layout, source) || state.Layout == nil {
			return
		}
		node := &editorLayoutNode{ID: nextEditorLayoutID(state), Direction: direction, SplitSize: 480}
		sourceLeaf := &editorLayoutNode{Group: source}
		if direction == splitLeft || direction == splitUp {
			node.First, node.Second = sourceLeaf, state.Layout
		} else {
			node.First, node.Second = state.Layout, sourceLeaf
		}
		state.Layout = node
	}
	state.ActiveGroup = source
	state.ChatOnly = false
	state.Split = len(state.Groups) > 1
	a.selectEditorDocument(root, state.Groups[source].Active, source, false)
	state.ActiveGroupPinned = true
}

func (a *desktop) closeEditorGroup(root string, group int) {
	state := a.editorState(root)
	closed := state.Groups[group]
	if closed == nil || !editorLayoutHasGroup(state.Layout, group) {
		return
	}
	paths := append([]string(nil), closed.Tabs...)
	dirty := make([]*workspaceFile, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		key := editorDocumentKey(path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if a.documentInOtherGroup(root, path, group) {
			continue
		}
		file := a.editorDocument(root, path)
		if file == nil {
			continue
		}
		if file.Saving {
			a.notify(locales.Text("文件正在保存，请稍后关闭"))
			return
		}
		if file.Editor != nil && file.Editor.Buffer != nil && file.Editor.Buffer.Modified() {
			dirty = append(dirty, file)
		}
	}
	var confirmNext func(int)
	confirmNext = func(index int) {
		if state.Groups[group] != closed || !editorLayoutHasGroup(state.Layout, group) || !sameEditorGroupTabs(closed, paths) {
			return
		}
		for index < len(dirty) {
			file := dirty[index]
			if !a.workspaceFileCurrent(root, file) {
				return
			}
			if file.Saving {
				a.notify(locales.Text("文件正在保存，请稍后关闭"))
				return
			}
			if file.Editor != nil && file.Editor.Buffer != nil && file.Editor.Buffer.Modified() {
				a.confirmDiscardFile(root, file, func() { confirmNext(index + 1) })
				return
			}
			index++
		}
		a.closeEditorGroupNow(root, group, closed, paths)
	}
	confirmNext(0)
}

func sameEditorGroupTabs(group *editorGroupState, paths []string) bool {
	if group == nil || len(group.Tabs) != len(paths) {
		return false
	}
	for i, path := range paths {
		if editorDocumentKey(group.Tabs[i]) != editorDocumentKey(path) {
			return false
		}
	}
	return true
}

func (a *desktop) closeEditorGroupNow(root string, group int, closed *editorGroupState, paths []string) {
	state := a.editorStates[workspaceKey(root)]
	if state == nil || state.Groups[group] != closed ||
		!editorLayoutHasGroup(state.Layout, group) || !sameEditorGroupTabs(closed, paths) {
		return
	}
	for _, path := range paths {
		if a.documentInOtherGroup(root, path, group) {
			continue
		}
		if file := a.editorDocument(root, path); file != nil && file.Saving {
			a.notify(locales.Text("文件正在保存，请稍后关闭"))
			return
		}
	}
	if len(state.Groups) == 1 {
		delete(state.Groups, group)
		state.Split = false
		newGroup := addEditorGroup(state)
		state.Layout = &editorLayoutNode{Group: newGroup}
		state.ActiveGroup = newGroup
		state.ActiveGroupPinned = false
		state.PaneDrag = editorPaneDrag{}
		state.ChatVisible = false
		for _, path := range paths {
			if file := a.editorDocument(root, path); file != nil {
				a.releaseWorkspaceDocument(root, file)
			}
		}
		a.selectEditorDocument(root, "", newGroup, false)
		state.ChatOnly = true
		return
	}
	wasActive := state.ActiveGroup == group
	if !removeEditorLayoutLeaf(&state.Layout, group) {
		return
	}
	delete(state.Groups, group)
	for _, path := range paths {
		if !a.documentInOtherGroup(root, path, group) {
			if file := a.editorDocument(root, path); file != nil {
				a.releaseWorkspaceDocument(root, file)
			}
		}
	}
	a.reconcileWorkspacePreviewGroup(root)
	state.Split = len(state.Groups) > 1
	activeGroup := state.ActiveGroup
	if wasActive || state.Groups[activeGroup] == nil || !editorLayoutHasGroup(state.Layout, activeGroup) {
		activeGroup = firstEditorLayoutGroup(state.Layout)
	}
	if state.Groups[activeGroup].Active == "" {
		for _, candidate := range layoutGroupIDs(state.Layout) {
			if state.Groups[candidate].Active != "" {
				activeGroup = candidate
				break
			}
		}
	}
	state.ActiveGroup = activeGroup
	state.ActiveGroupPinned = false
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
		a.reconcileWorkspacePreviewGroup(root)
		if len(g.Tabs) == 0 {
			a.closeEditorGroup(root, group)
			return
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
