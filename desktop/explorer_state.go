// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// Only open workspaces retain tree data. Search results are transient and never
// replace the remembered ordinary tree; late reads use the existing fileEpoch.
type explorerTreeState struct {
	files    []backend.FileEntry
	children map[string][]backend.FileEntry
	open     []string
	scroll   *ui.ScrollState
}

func (a *desktop) rememberFileTree() {
	if a.fileRoot == "" || a.filesF.Search.Query != "" {
		return
	}
	if a.workspaceTrees == nil {
		a.workspaceTrees = make(map[string]*explorerTreeState)
	}
	open := make([]string, 0, a.fileOutline.Open.Len())
	for path := range a.fileOutline.Open.All() {
		open = append(open, path)
	}
	a.workspaceTrees[a.fileRoot] = &explorerTreeState{
		files: a.files, children: a.fileChildren, open: open, scroll: a.fileScroll,
	}
}

// resetFileOutline forgets the tree's expansion and the row it is on. The same
// state is its own after a workspace switch or a new search, where the old
// paths mean nothing.
func (a *desktop) resetFileOutline() {
	a.fileOutline = ui.OutlineState[string]{}
	a.fileFocus, a.fileChoice, a.fileChoiceRows = -1, "", 0
}

func (a *desktop) selectFileTree(root string) {
	key := ""
	if root != "" {
		key = workspaceKey(root)
	}
	if key == a.fileRoot {
		return
	}
	a.rememberFileTree()
	a.resetFileExplorer()
	a.hideFilePreview()
	state := a.workspaceTrees[key]
	if state == nil {
		state = &explorerTreeState{children: map[string][]backend.FileEntry{}, scroll: &ui.ScrollState{}}
	}
	a.fileRoot = key
	a.files, a.fileChildren, a.fileScroll = state.files, state.children, state.scroll
	a.resetFileOutline()
	for _, path := range state.open {
		a.fileOutline.Open.Add(path)
	}
	a.reindexFileTree()
}

func (a *desktop) releaseWorkspaceFileTree(root string) {
	key := workspaceKey(root)
	delete(a.workspaceTrees, key)
	if a.fileRoot != key {
		return
	}
	// Clear the active aliases too, so selecting the remaining tab cannot put
	// the just-closed workspace back into the retained map.
	a.resetFileExplorer()
	a.fileRoot = ""
	a.files, a.fileChildren, a.fileScroll = nil, nil, nil
	a.resetFileOutline()
	a.fileIndex = explorerIndex{}
}
