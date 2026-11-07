package desktop

import (
	backend "ally-dev/internal/app"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

type explorerSearch struct {
	Input, Query, Note string
	Busy               bool
	Cancel             context.CancelFunc
}

// resetFileSearch invalidates both searches and ordinary lazy reads so a late
// response cannot restore results after closing or switching workspaces.
func (a *desktop) resetFileSearch() {
	s := &a.filesF.Search
	if s.Cancel != nil {
		s.Cancel()
	}
	if s.Query != "" {
		a.fileRoot = ""
		a.files = nil
		a.fileOpen = nil
		a.fileChildren = nil
		a.fileScroll = nil
	}
	*s = explorerSearch{}
	a.fileEpoch++
	a.fileLoading = nil
}

// Reset transient search/help state and invalidate pending reads. Workspace
// tree ownership is handled separately by selectFileTree.
func (a *desktop) resetFileExplorer() {
	a.resetFileSearch()
	a.filesF.HelpOpen = false
}

func (a *desktop) searchFiles(query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		a.resetFileSearch()
		a.refreshFiles()
		return
	}
	t := a.current()
	if t == nil || a.core == nil || a.win == nil {
		return
	}
	s := &a.filesF.Search
	a.rememberFileTree()
	if s.Query == "" {
		a.fileScroll = &ui.ScrollState{}
	}
	if s.Cancel != nil {
		s.Cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Cancel, s.Query, s.Busy, s.Note = cancel, query, true, ""
	a.fileEpoch++
	epoch, root := a.fileEpoch, t.Workspace
	a.fileRoot = workspaceKey(root)
	a.files, a.fileChildren, a.fileOpen = nil, nil, nil
	a.fileLoading = nil
	go func() {
		defer cancel()
		result, err := a.core.SearchExplorerFiles(ctx, root, query)
		if ctx.Err() != nil {
			return
		}
		entries, children, opened := explorerSearchTree(result.Entries)
		a.win.Update(func() {
			if epoch != a.fileEpoch || a.closing {
				return
			}
			s := &a.filesF.Search
			s.Cancel, s.Busy = nil, false
			a.files, a.fileChildren, a.fileOpen = entries, children, opened
			s.Note = fmt.Sprintf("找到 %d 个文件", len(result.Entries))
			if result.Truncated {
				s.Note += "（结果受限，请缩小范围）"
			}
			if err != nil {
				s.Note = "搜索未完成：" + err.Error()
			}
		})
	}()
}

// Materialize only matching leaves and their ancestors; existing TreeItem rows,
// menus, selection and expansion behavior are shared with the ordinary tree.
func explorerSearchTree(files []backend.FileEntry) ([]backend.FileEntry, map[string][]backend.FileEntry, map[string]bool) {
	children := map[string][]backend.FileEntry{}
	opened := map[string]bool{}
	seen := map[string]bool{}
	for _, file := range files {
		path := strings.ReplaceAll(file.Path, "\\", "/")
		parts := strings.Split(path, "/")
		parent := ""
		for i, name := range parts {
			if name == "" || name == "." || name == ".." {
				break
			}
			current := name
			if parent != "" {
				current = parent + "/" + name
			}
			dir := i < len(parts)-1
			if !seen[current] {
				entry := file
				entry.Path, entry.Name, entry.Dir = current, name, dir
				children[parent] = append(children[parent], entry)
				seen[current] = true
			}
			if dir {
				opened[current] = true
			}
			parent = current
		}
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool {
			a, b := children[parent][i], children[parent][j]
			if a.Dir != b.Dir {
				return a.Dir
			}
			return a.Name < b.Name
		})
	}
	root := children[""]
	delete(children, "")
	return root, children, opened
}
