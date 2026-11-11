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
	open     map[string]bool
	children map[string][]backend.FileEntry
	scroll   *ui.ScrollState
}

func (a *desktop) rememberFileTree() {
	if a.fileRoot == "" || a.filesF.Search.Query != "" {
		return
	}
	if a.workspaceTrees == nil {
		a.workspaceTrees = make(map[string]*explorerTreeState)
	}
	a.workspaceTrees[a.fileRoot] = &explorerTreeState{
		files: a.files, open: a.fileOpen, children: a.fileChildren, scroll: a.fileScroll,
	}
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
		state = &explorerTreeState{open: make(map[string]bool), children: make(map[string][]backend.FileEntry), scroll: &ui.ScrollState{}}
	}
	a.fileRoot = key
	a.files, a.fileOpen, a.fileChildren, a.fileScroll = state.files, state.open, state.children, state.scroll
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
	a.files, a.fileOpen, a.fileChildren, a.fileScroll = nil, nil, nil, nil
}
