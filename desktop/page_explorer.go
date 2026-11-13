// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
	"errors"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func fileParent(path string) string {
	return strings.Trim(strings.ReplaceAll(filepath.ToSlash(filepath.Dir(path)), "\\", "/"), "/")
}

func (a *desktop) filePanel(c *ui.Context) {
	if a.current() == nil {
		return
	}
	a.fileTree(c)
}

func normalizeExplorerPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	return strings.TrimPrefix(path, "./")
}

func (a *desktop) fileEditor(c *ui.Context, file *workspaceFile) {
	kind := workspaceFileKindForPath(file.Path)
	ui.Column(c).Grow(1).MinWidth(0).BorderWidth(0, 0, 0, 1).BorderColor(c.Theme().Border).Children(func() {
		c.OnShortcut(ui.Cmd, ui.KeyS, func() { a.saveFile() })
		if kind.hasMediaPlayer() {
			a.workspaceMediaShortcuts(c, file)
		}
		ui.Row(c).Height(34).Padding(0, padRow).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			name := file.Path
			if file.Editor.Buffer.Modified() {
				name += " ●"
			}
			uikit.Tooltip(c, ui.Text(c, name).FontSize(fsSmall).Grow(1).SingleLine().MinWidth(0), file.Path)
			if kind.hasMediaPlayer() {
				a.workspaceMediaToolbar(c, file)
			}
			if kind.hasMarkupToggle() {
				label := locales.Text("预览")
				if file.MarkupPreview {
					label = locales.Text("编辑源码")
				}
				flat(c, label).OnClick(func() { a.toggleWorkspaceFileMarkupPreview(file) })
			}
			flat(c, locales.Text("刷新")).Disabled(file.Saving).OnClick(func() {
				if t := a.current(); t != nil {
					root := t.Workspace
					reload := func(discardConfirmed bool) { a.loadFileReplacing(root, file.Path, file, discardConfirmed) }
					if file.Editor.Buffer.Modified() {
						a.confirmDiscardFile(root, file, func() { reload(true) })
					} else {
						reload(false)
					}
				}
			})
			label := locales.Text("保存")
			if file.Saving {
				label = locales.Text("保存中…")
			}
			flat(c, label).Disabled(kind.isPreviewOnly() || file.Saving || !file.Editor.Buffer.Modified()).OnClick(func() { a.saveFile() })
			flat(c, locales.Text("系统打开")).OnClick(func() { a.openSystemFile(file.Path) })
			iconAction(c, "editor-close", locales.Text("关闭编辑器"), "close", 24, func() { a.closeFile() })
		})
		if file.PreviewError != "" {
			ui.Box(c).Grow(1).MinHeight(0).Padding(padPage).Center().Children(func() {
				ui.Column(c).FillWidth().MaxWidth(560).AlignSelf(ui.Center).Gap(gapSection).Children(func() {
					ui.Text(c, locales.Text("无法预览此文件")).FontSize(fsPageTitle).Bold().TextAlign(ui.Center).FillWidth()
					fieldNote(c, file.PreviewError, a.secondaryInk())
					ui.Row(c).Justify(ui.Center).Children(func() {
						flat(c, locales.Text("用系统应用打开")).OnClick(func() { a.openSystemFile(file.Path) })
					})
				})
			})
			return
		}
		if file.PreviewPath != "" || file.PreviewHTML != "" || file.Document != nil || file.MarkupPreview {
			a.workspaceFilePreviewView(c, file)
			return
		}
		file.Editor.ReadOnly = a.closing || file.Saving
		editor.View(c.Key("editor-"+file.Path), file.Editor).Grow(1).MinHeight(0).FillWidth()
	})
}

func (a *desktop) fileTree(c *ui.Context) {
	if a.fileScroll == nil {
		a.fileScroll = &ui.ScrollState{}
	}
	p := a.currentPalette()
	ink := p.muted.Mix(p.text, 0.25)
	width := a.prefs.TreeWidth
	if width < 150 || width > 600 {
		width = 270
	}
	// Unselectable keeps the window-wide selectable text layer from claiming
	// right-clicks: without it every secondary click opens the root text menu
	// and neither a row menu nor the blank-area menu ever sees RightClicked.
	// MyGo Split owns the separator; a left border here doubles its width.
	ui.Column(c).Width(width).FillHeight().Shrink(0).Unselectable().Background(colChrome(a)).Children(func() {
		services := c.Services()
		c.OnShortcut(ui.Cmd, ui.KeyV, func() { a.pasteFiles(services, "") })
		ui.Row(c).Height(34).Padding(0, padRow).Gap(3).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			search := uikit.InputChrome(c, ui.SearchField(c.Key("explorer-search"), &a.filesF.Search.Input)).
				Placeholder("").Label(locales.Text("搜索工作区文件")).Height(26).Padding(0, 6).FontSize(fsSmall).Grow(1).
				OnSubmit(func() { a.searchFiles(a.filesF.Search.Input) })
			uikit.Tooltip(c, search, locales.Text("输入文件名后回车搜索，清空后回车还原"))
			uikit.IconButton(c, "explorer-refresh", locales.Text("刷新"), iconImage("refresh"), 22, uikit.IconQuiet, func() { a.searchFiles(a.filesF.Search.Input) }, ink)
			uikit.IconButton(c, "explorer-close", locales.Text("关闭文件栏"), iconImage("close"), 22, uikit.IconQuiet, func() { a.setExplorerOpen(false) }, ink)
		})
		if a.filesF.Search.Busy {
			fieldNote(c, locales.Text("正在搜索…"), a.secondaryInk())
		} else if a.filesF.Search.Note != "" {
			fieldNote(c, a.filesF.Search.Note, a.secondaryInk())
		}
		// Put the fallback menu on the viewport: the content column can be
		// shorter than it, even with FillHeight in a scrolling container.
		// A row's own menu takes precedence over this ancestor's menu.
		a.loadOpenedFileDirs()
		// The zero OutlineState chooses its first row; a tree starts on none.
		if !a.fileOutlineReady {
			a.fileOutlineReady = true
			a.fileFocus = -1
		}
		a.fileOutline.List.Selected = &a.fileFocus
		a.syncFileRowChoice()
		if a.fileOutline.Prefix == nil {
			// A row draws its own indentation, arrow and tint, as the tree did:
			// an Outline's rows would otherwise pad them and fill the chosen one
			// with the accent color.
			a.fileOutline.Prefix = func(ui.OutlineRow) {}
		}
		a.fileOutline.List.Label = func(row int) string {
			if row < 0 || row >= a.fileOutline.Rows() {
				return ""
			}
			return a.fileIndex.nodes[a.fileOutline.Item(row)].name
		}
		// Outline owns the scroll place and the keyboard, so a frame builds
		// only the rows in view instead of every expanded entry, and the tree
		// costs the same at any size.
		area := ui.Column(c.Key("explorer-tree-"+a.fileRoot)).Grow(1).MinHeight(0).FillWidth().Padding(3, 0)
		var tree ui.Element
		area.Children(func() {
			if len(a.files) == 0 && a.filesF.Search.Query == "" {
				fieldNote(c, locales.Text("目录为空"), a.secondaryInk())
			}
			tree = ui.Outline(c, &a.fileOutline, a.fileIndex.roots, a.fileTreeKids, func(path string) {
				a.fileTreeRow(c, path, p)
			}).FillWidth().Grow(1).MinHeight(0).TrackScroll(a.fileScroll).Focusable().Label(locales.Text("工作区文件列表"))
		})
		// Enter opens the row the tree is on, as a double click would, and the
		// shortcuts act on it: the row content cannot take either itself.
		if tree.Submitted() {
			a.activateFileRow(a.fileChosenPath())
		}
		tree.OnShortcut(ui.Cmd, ui.KeyC, func() { a.copyFileChosen(services) })
		tree.OnShortcut(ui.Cmd, ui.KeyV, func() { a.pasteBesideFileChosen(services) })
		uikit.ContextMenu(area, func(m *ui.Menu) {
			if m.Item(locales.Text("新建文件")).Chosen() {
				a.openFilePrompt(false, "")
			}
			if m.Item(locales.Text("新建目录")).Chosen() {
				a.openFilePrompt(true, "")
			}
			if m.Item(locales.Text("打开目录")).Chosen() {
				a.openInManager("")
			}
			if m.Item(locales.Text("复制绝对路径")).Chosen() {
				if t := a.current(); t != nil && t.Workspace != "" {
					full, err := filepath.Abs(t.Workspace)
					if err != nil {
						a.notifyError(locales.Text("复制工作区路径失败：") + err.Error())
					} else {
						services.WriteClipboard(full)
					}
				}
			}
			m.Separator()
			if m.Item(locales.Text("粘贴文件")).Chosen() {
				a.pasteFiles(services, "")
			}
			m.Separator()
			if m.Item(locales.Text("刷新")).Chosen() {
				a.refreshFiles(true)
			}
		})
		uikit.Reveal(c.Key("explorer-help-reveal"), a.filesF.HelpOpen, func() {
			ui.Column(c).Padding(8, 10).Gap(2).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
				fieldNote(c, locales.Text("文件浏览器帮助"), a.secondaryInk())
				for _, line := range []string{
					locales.Text("输入文件名后回车搜索；清空后回车还原目录树"),
					locales.Text("单击文件打开，单击目录展开"),
					locales.Text("右键文件：复制文件 / 复制路径 / 重命名 / 删除 / 属性"),
					locales.Text("右键空白处：复制工作区绝对路径 / 粘贴文件；同名文件自动另存副本"),
					locales.Text("右键目录：粘贴文件；同名文件自动另存副本"),
					locales.Text("Ctrl+C 复制聚焦文件，Ctrl+V 粘贴到聚焦目录（文件行则为其父目录）"),
					locales.Text("Ctrl+S 保存当前文件（带版本校验）"),
					locales.Text("Ctrl+Space 补全，悬停查看符号信息，F12 / ⌘或Ctrl+点击跳转 Go 定义"),
					locales.Text("Markdown 可切换预览，图片直接预览"),
				} {
					fieldNote(c, "· "+line, a.secondaryInk())
				}
			})
		})
		ui.Row(c).Height(26).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Padding(0, padRow).Children(func() {
			uikit.IconButton(c, "explorer-open", locales.Text("在文件管理器中打开工作区"), iconImage("folderopen"), 22, uikit.IconQuiet, func() {
				if t := a.current(); t != nil {
					p := t.Workspace
					a.job(func() error { return a.core.OpenPathInFileManager(p) })
				}
			}, ink)
			ui.Spacer(c)
			uikit.IconButton(c, "explorer-help", locales.Text("文件浏览器帮助"), iconImage("info"), 22, uikit.IconQuiet, func() { a.filesF.HelpOpen = !a.filesF.HelpOpen }, ink)
			addButton(c, "explorer-new-file", locales.Text("新建文件"), "plus", 22, func() { a.openFilePrompt(false, "") })
		})
	})
	a.filePromptDialog(c)
	a.fileInfoDialog(c)
}

// fileTreeEntry is one row of the explorer tree: the entry a listing gave, plus
// the identity the tree keys it by, normalized once as the listing arrives so
// no frame rewrites a path.
type fileTreeEntry struct {
	entry backend.FileEntry
	path  string
	name  string
	// depth is how deep the path sits: its separators, which is where the
	// outline indents it to.
	depth int
}

func newFileTreeEntry(entry backend.FileEntry) fileTreeEntry {
	path := strings.ReplaceAll(entry.Path, "\\", "/")
	name := entry.Name
	if name == "" {
		name = filepath.Base(path)
	}
	if entry.Symlink {
		name += " →"
	}
	return fileTreeEntry{entry: entry, path: path, name: name, depth: strings.Count(path, "/")}
}

// explorerIndex is what the tree asks for without touching a listing: the paths
// Outline lists at the top and under each directory, and each path's row. It is
// rebuilt when a listing arrives, never in a frame.
type explorerIndex struct {
	roots []string
	kids  map[string][]string
	nodes map[string]fileTreeEntry
}

// emptyFileKids keeps the arrow of a directory whose listing is still on its
// way: Outline shows one for every item whose children are not nil.
var emptyFileKids = []string{}

// syncFileRowChoice puts the tree's chosen row on the file the editor shows,
// so the row a shortcut or Enter acts on is the file being read. A row the user
// moved to with the keys stays there until another file opens, and a file
// inside a collapsed directory is looked for again once the rows change.
func (a *desktop) syncFileRowChoice() {
	activePath := ""
	if file := a.activeFile(); file != nil {
		activePath = file.Path
	}
	if a.filesF.InfoOpen {
		activePath = a.filesF.InfoPath
	}
	if activePath == "" {
		return
	}
	if activePath == a.fileChoice && (a.fileFocus >= 0 || a.fileChoiceRows == a.fileOutline.Rows()) {
		return
	}
	a.fileChoice, a.fileChoiceRows = activePath, a.fileOutline.Rows()
	for row := 0; row < a.fileOutline.Rows(); row++ {
		if a.fileOutline.Item(row) == activePath {
			a.fileOutline.List.Choose(row)
			return
		}
	}
}

// fileChosenPath is the row the tree is on, empty when it is on none.
func (a *desktop) fileChosenPath() string {
	if a.fileFocus < 0 || a.fileFocus >= a.fileOutline.Rows() {
		return ""
	}
	return a.fileOutline.Item(a.fileFocus)
}

// activateFileRow does what a click on a row does: a file opens, a directory
// expands or collapses.
func (a *desktop) activateFileRow(path string) {
	node, ok := a.fileIndex.nodes[path]
	if !ok {
		return
	}
	if node.entry.Dir {
		a.toggleFolder(path)
		return
	}
	a.openFile(node.entry.Path)
}

// copyFileChosen copies the file the tree is on, or the file the editor shows
// when the tree has no row of its own.
func (a *desktop) copyFileChosen(services ui.Services) {
	if path := a.fileChosenPath(); path != "" {
		if node, ok := a.fileIndex.nodes[path]; ok && !node.entry.Dir {
			a.copyExplorerFile(services, path)
		}
		return
	}
	if file := a.activeFile(); file != nil {
		a.copyExplorerFile(services, file.Path)
	}
}

// pasteBesideFileChosen pastes into the directory the tree is on, or beside the
// file it is on, and into the workspace root with no row chosen.
func (a *desktop) pasteBesideFileChosen(services ui.Services) {
	path := a.fileChosenPath()
	if path == "" {
		a.pasteFiles(services, "")
		return
	}
	if node, ok := a.fileIndex.nodes[path]; ok && node.entry.Dir {
		a.pasteFiles(services, path)
		return
	}
	a.pasteFiles(services, fileParent(path))
}

// reindexFileTree rebuilds the derived view of the listings. Listings land a
// few times per user action, so this stays out of the frame; every writer of
// files or fileChildren calls it.
func (a *desktop) reindexFileTree() {
	index := explorerIndex{
		roots: make([]string, 0, len(a.files)),
		kids:  make(map[string][]string, len(a.fileChildren)),
		nodes: make(map[string]fileTreeEntry, len(a.files)),
	}
	for _, entry := range a.files {
		node := newFileTreeEntry(entry)
		index.roots = append(index.roots, node.path)
		index.nodes[node.path] = node
	}
	for parent, entries := range a.fileChildren {
		paths := make([]string, 0, len(entries))
		for _, entry := range entries {
			node := newFileTreeEntry(entry)
			paths = append(paths, node.path)
			index.nodes[node.path] = node
		}
		index.kids[parent] = paths
	}
	a.fileIndex = index
}

// fileTreeKids is Outline's children function: a directory's listed paths, or
// an empty list for one whose listing is still on its way, so its arrow stays.
// Files have none. It never reads disk: loadOpenedFileDirs starts the reads.
func (a *desktop) fileTreeKids(path string) []string {
	if kids, loaded := a.fileIndex.kids[path]; loaded {
		return kids
	}
	if node, ok := a.fileIndex.nodes[path]; ok && node.entry.Dir {
		return emptyFileKids
	}
	return nil
}

// loadOpenedFileDirs lists the expanded directories that have no listing yet.
// Outline asks children for every row in view, expanded or not, so a read there
// would list every directory on screen; the expansion set decides instead.
func (a *desktop) loadOpenedFileDirs() {
	if a.fileOutline.Open.Len() == 0 {
		return
	}
	for path := range a.fileOutline.Open.All() {
		if _, loaded := a.fileIndex.kids[path]; loaded {
			continue
		}
		if !a.fileBranchOpen(path) {
			continue
		}
		a.loadFolderChildren(path)
	}
}

// fileTreeRow draws one row: the indent, the arrow, the name, what a click
// does, the tooltip and the menu. It draws the indent and the arrow itself so
// the row keeps the tree's look: 23 pixels high, four plus sixteen a level, a
// 16 pixel arrow, a 2 pixel gap, and the quiet tint of a chosen row across the
// width, on the panel color rather than the list's accent fill.
func (a *desktop) fileTreeRow(c *ui.Context, path string, p palette) {
	node, ok := a.fileIndex.nodes[path]
	if !ok {
		return
	}
	entry, services, t := node.entry, c.Services(), c.Theme()
	open := entry.Dir && a.fileOutline.Open.Has(path)
	selected := a.fileChosenPath() == path
	ink := t.Text
	if p.dark && !selected {
		ink = ui.Hex("#cccccc")
	}
	row := ui.Row(c).FillWidth().Height(23).Gap(t.Space(0.5)).AlignItems(ui.Center).
		PaddingStart(t.Space(1 + 4*float32(node.depth))).PaddingEnd(t.Space(2)).Radius(t.Radius)
	if selected {
		// The list fills the chosen row with the accent; the panel color under
		// the tree's own tint hides it, so the row keeps the look it had.
		base, tint := p.bg, selectionFill(t.Accent)
		row.Draw(func(painter *ui.Painter, r ui.Rect) {
			painter.Fill(r, base, 0)
			painter.Fill(r, tint, t.Radius)
		})
	}
	if strings.HasPrefix(entry.Name, ".") {
		row.Opacity(0.55)
	}
	uikit.Tooltip(c, row, path)
	// The arrow keeps the muted ink of every other row: the chosen row is
	// only tinted, so there is no accent for the accent text to sit on.
	arrowInk := t.TextMuted
	row.Children(func() {
		arrow := ui.Box(c).Size(t.Space(4), t.Space(4)).Shrink(0)
		if entry.Dir {
			arrow.Draw(func(painter *ui.Painter, r ui.Rect) {
				var chevron ui.Path
				if open {
					chevron.MoveTo(r.X+r.W/4, r.Y+r.H*3/8).LineTo(r.X+r.W/2, r.Y+r.H*5/8).LineTo(r.X+r.W*3/4, r.Y+r.H*3/8)
				} else {
					chevron.MoveTo(r.X+r.W*3/8, r.Y+r.H/4).LineTo(r.X+r.W*5/8, r.Y+r.H/2).LineTo(r.X+r.W*3/8, r.Y+r.H*3/4)
				}
				painter.StrokePath(&chevron, 1.5, arrowInk)
			})
		}
		ui.Text(c, node.name).FontSize(fsSmall).SingleLine().TextColor(ink)
	})
	// The list chooses the row on a click, so the content asks whether it was
	// clicked rather than taking the click the choice needs.
	if row.RowClicked() {
		a.activateFileRow(path)
	}
	// The shared menu keeps row-specific actions ahead of the viewport's.
	uikit.ContextMenu(row, func(m *ui.Menu) {
		if entry.Dir {
			if m.Item(locales.Text("新建文件")).Chosen() {
				a.openFilePrompt(false, path)
			}
			if m.Item(locales.Text("新建目录")).Chosen() {
				a.openFilePrompt(true, path)
			}
		}
		if m.Item(locales.Text("打开目录")).Chosen() {
			if entry.Dir {
				a.openInManager(path)
			} else {
				a.openInManager(fileParent(path))
			}
		}
		m.Separator()
		if !entry.Dir {
			if m.Item(locales.Text("复制文件")).Chosen() {
				a.copyExplorerFile(services, path)
			}
		}
		if m.Item(locales.Text("粘贴文件")).Chosen() {
			target := path
			if !entry.Dir {
				target = fileParent(path)
			}
			a.pasteFiles(services, target)
		}
		m.Separator()
		if m.Item(locales.Text("复制相对路径")).Chosen() {
			services.WriteClipboard(path)
		}
		if m.Item(locales.Text("复制完整路径")).Chosen() {
			full := path
			if t := a.current(); t != nil {
				full = filepath.Join(t.Workspace, filepath.FromSlash(path))
			}
			services.WriteClipboard(full)
		}
		if m.Item(locales.Text("属性")).Chosen() {
			a.openFileInfo(entry.Path)
		}
		m.Separator()
		if m.Item(locales.Text("重命名")).Chosen() {
			a.openRenamePrompt(path)
		}
		if m.Item(locales.Text("删除")).Chosen() {
			a.deleteFileEntry(entry)
		}
		if entry.Dir {
			// Mirror the Vue explorer: a directory row can refresh just itself.
			if m.Item(locales.Text("刷新")).Chosen() {
				a.refreshFolder(path)
			}
		}
	})
}

func (a *desktop) openInManager(path string) {
	if t := a.current(); t != nil {
		req := backend.WorkspacePathRequest{Workspace: t.Workspace, Path: path}
		a.job(func() error { return a.core.OpenWorkspacePathInFileManagerAt(req) })
	}
}

func (a *desktop) openFilePrompt(dir bool, parent string) {
	a.filesF.NewOpen = true
	a.filesF.NewDir = dir
	a.filesF.NewParent = parent
	a.filesF.NewName = ""
	a.filesF.Rename = false
	a.filesF.RenameOf = ""
	a.filesF.PromptErr = ""
}

// openRenamePrompt targets the right-clicked node itself: the Vue explorer
// renames that row, never whichever document happens to be open.
func (a *desktop) openRenamePrompt(path string) {
	a.filesF.NewOpen = true
	a.filesF.NewDir = false
	a.filesF.NewParent = fileParent(path)
	a.filesF.NewName = filepath.Base(filepath.FromSlash(path))
	a.filesF.Rename = true
	a.filesF.RenameOf = path
	a.filesF.PromptErr = ""
}

func (a *desktop) filePromptDialog(c *ui.Context) {
	if !a.filesF.NewOpen {
		return
	}
	title := locales.Text("新建文件")
	if a.filesF.Rename {
		title = locales.Text("重命名")
	} else if a.filesF.NewDir {
		title = locales.Text("新建目录")
	}
	modalCard(c, &a.filesF.NewOpen, widthModalSmall, func() {
		modalTitle(c, title, locales.Text("名称不含路径分隔符。"))
		value := a.filesF.NewName
		name := uikit.Input(c.Key("file-prompt-name"), &value).Label(locales.Text("文件名称")).FillWidth().AutoFocus()
		// Enter in the name field submits, as a desktop dialog does: the button is
		// not the only way to confirm. The field's own value is the newest one at
		// that point, so it is copied into the dialog state before submitting.
		name.OnSubmit(func() {
			a.filesF.NewName = value
			a.submitFilePrompt()
		})
		a.filesF.NewName = value
		inlineError(c, "file-prompt-error", a.filesF.PromptErr)
		modalActions(c, func() {
			flat(c, locales.Text("取消")).OnClick(func() { a.filesF.NewOpen = false })
			primary(c, map[bool]string{true: locales.Text("保存"), false: locales.Text("创建")}[a.filesF.Rename]).OnClick(func() { a.submitFilePrompt() })
		})
	})
}

func (a *desktop) submitFilePrompt() {
	t := a.current()
	if t == nil {
		return
	}
	name := strings.TrimSpace(a.filesF.NewName)
	if name == "" || strings.ContainsAny(name, `/\`) {
		a.filesF.PromptErr = locales.Text("名称无效，请输入不含路径的名称")
		return
	}
	workspace := t.Workspace
	if a.filesF.Rename {
		path := strings.TrimSpace(a.filesF.RenameOf)
		if path == "" {
			// Nothing named the node, so fall back to the open document.
			if old := a.activeFile(); old != nil {
				path = old.Path
			}
		}
		if path == "" {
			a.filesF.NewOpen = false
			return
		}
		root := t.Workspace
		a.filesF.NewOpen = false
		a.job(func() error {
			_, err := a.core.RenamePath(backend.RenamePathRequest{Workspace: root, Path: path, NewName: name})
			if err == nil {
				a.win.Update(func() {
					a.explorerPathChanged(root, path)
					a.notify(locales.Text("已重命名"))
				})
			}
			return err
		})
		return
	}
	parent := a.filesF.NewParent
	dir := a.filesF.NewDir
	a.filesF.NewOpen = false
	full := name
	if parent != "" {
		full = parent + "/" + name
	}
	a.job(func() error {
		var err error
		if dir {
			err = a.core.CreateDirectory(backend.CreateDirectoryRequest{Workspace: workspace, Path: full})
		} else {
			_, err = a.core.CreateFile(backend.CreateFileRequest{Workspace: workspace, Path: full})
		}
		if err == nil {
			a.win.Update(func() { a.refreshWorkspaceFiles(workspace); a.notify(locales.Text("已创建 ") + full) })
		}
		return err
	})
}

func (a *desktop) deleteFileEntry(entry backend.FileEntry) {
	t := a.current()
	if t == nil {
		return
	}
	kind := locales.Text("文件")
	if entry.Dir {
		kind = locales.Text("目录")
	}
	detail := locales.Text("确定删除") + kind + "「" + entry.Name + locales.Text("」吗？此操作不可恢复。")
	workspace := t.Workspace
	a.confirmAction(locales.Text("确认删除"), detail, func() error {
		return a.core.DeletePath(backend.DeletePathRequest{Workspace: workspace, Paths: []string{entry.Path}, Recursive: entry.Dir})
	}, func() {
		a.explorerPathChanged(workspace, entry.Path)
		a.notifySuccess(locales.Text("已删除") + kind + "「" + entry.Name + "」")
	})
}

// Async file operations belong to their captured workspace even if another tab
// is active when they finish. A renamed directory also invalidates descendants.
func (a *desktop) explorerPathChanged(workspace, path string) {
	if file := a.workspaceFiles[workspaceKey(workspace)]; file != nil && workspacePathContains(path, file.Path) {
		a.closeWorkspaceFile(workspace)
	}
	a.refreshWorkspaceFiles(workspace)
}

func (a *desktop) refreshWorkspaceFiles(workspace string) {
	if current := a.current(); current != nil && sameWorkspace(current.Workspace, workspace) {
		a.refreshFiles()
	}
}

// workspacePathContains reports whether child is the same workspace-relative
// path as parent or lies beneath it at a path-segment boundary.
func workspacePathContains(parent, child string) bool {
	parent = filepath.Clean(filepath.FromSlash(parent))
	child = filepath.Clean(filepath.FromSlash(child))
	if parent == "." {
		return !filepath.IsAbs(child)
	}
	if child == "." {
		return false
	}
	parentParts := strings.Split(filepath.ToSlash(parent), "/")
	childParts := strings.Split(filepath.ToSlash(child), "/")
	if len(parentParts) > len(childParts) {
		return false
	}
	caseInsensitive := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	for i, part := range parentParts {
		if caseInsensitive {
			if !strings.EqualFold(part, childParts[i]) {
				return false
			}
		} else if part != childParts[i] {
			return false
		}
	}
	return true
}

func (a *desktop) toggleFolder(path string) {
	if a.fileOutline.Open.Has(path) {
		a.fileOutline.Open.Remove(path)
		return
	}
	a.fileOutline.Open.Add(path)
	if _, loaded := a.fileIndex.kids[path]; loaded {
		return
	}
	a.loadFolderChildren(path)
}

// prepareFileTree retains visible rows and disclosure state during a refresh of
// the same workspace. Collapsed listings are invalidated for their next open.
func (a *desktop) prepareFileTree(root string) {
	a.selectFileTree(root)
	for path := range a.fileChildren {
		if !a.fileOutline.Open.Has(path) {
			delete(a.fileChildren, path)
		}
	}
	a.fileLoading = map[string]bool{}
	a.reindexFileTree()
}

// applyFileTreeEntries refreshes expanded descendants lazily, using the current
// disclosure state so a user collapse during an in-flight refresh is respected.
func (a *desktop) applyFileTreeEntries(parent string, entries []backend.FileEntry) {
	prefix := ""
	if parent != "" {
		prefix = parent + "/"
	}
	dirs := map[string]bool{}
	for _, entry := range entries {
		if entry.Dir {
			dirs[strings.ReplaceAll(entry.Path, "\\", "/")] = true
		}
	}
	for path := range a.fileOutline.Open.All() {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		child, _, _ := strings.Cut(strings.TrimPrefix(path, prefix), "/")
		if !dirs[prefix+child] {
			a.fileOutline.Open.Remove(path)
			delete(a.fileChildren, path)
			delete(a.fileLoading, path)
		}
	}
	if parent == "" {
		a.files = entries
	} else {
		if a.fileChildren == nil {
			a.fileChildren = map[string][]backend.FileEntry{}
		}
		a.fileChildren[parent] = entries
	}
	a.reindexFileTree()
	for path := range dirs {
		if a.fileBranchOpen(path) {
			a.loadFolderChildren(path)
		}
	}
}

// A remembered expansion below a collapsed ancestor is not visible and must
// not cause background reads, including when an older parent read completes.
func (a *desktop) fileBranchOpen(path string) bool {
	for {
		if !a.fileOutline.Open.Has(path) {
			return false
		}
		index := strings.LastIndexByte(path, '/')
		if index < 0 {
			return true
		}
		path = path[:index]
	}
}

// refreshFolder reloads one directory in place and keeps the tree expanded,
// the way the Vue explorer refreshes a single node instead of the whole tree.
func (a *desktop) refreshFolder(path string) {
	if a.filesF.Search.Query != "" {
		a.refreshFiles()
		return
	}
	if path == "" {
		a.refreshFiles()
		return
	}
	if a.fileChildren == nil {
		a.fileChildren = map[string][]backend.FileEntry{}
	}
	a.fileOutline.Open.Add(path)
	delete(a.fileChildren, path)
	a.reindexFileTree()
	a.loadFolderChildren(path, true)
}

// loadFolderChildren lists one directory into fileChildren.
func (a *desktop) loadFolderChildren(path string, manual ...bool) {
	if a.core == nil || a.fileLoading[path] || a.filesF.Search.Query != "" {
		return
	}
	t := a.current()
	if t == nil {
		return
	}
	if a.fileLoading == nil {
		a.fileLoading = map[string]bool{}
	}
	a.fileLoading[path] = true
	root, epoch := t.Workspace, a.fileEpoch
	go func() {
		result, err := a.core.ListFiles(backend.ListFilesRequest{Workspace: root, Path: path, MaxDepth: 1, Limit: 1000, IncludeHidden: true, IncludeIgnored: true})
		a.win.Update(func() {
			if epoch != a.fileEpoch || !a.fileLoading[path] {
				return
			}
			delete(a.fileLoading, path)
			if err != nil {
				// Stop frame-driven retries. Reopening the directory or refreshing
				// explicitly starts another read.
				a.fileOutline.Open.Remove(path)
				a.notifyError(err.Error())
				return
			}
			a.applyFileTreeEntries(path, result.Entries)
			if len(manual) > 0 && manual[0] {
				a.notifySuccess(locales.Text("目录已刷新"))
			}
			if result.Truncated {
				a.notify(locales.Text("目录过大，仅显示前 1000 项"))
			}
		})
	}()
}

// copyExplorerFile publishes native file representations rather than path text,
// allowing Explorer/Finder and other applications to paste the actual file.
func (a *desktop) copyExplorerFile(services ui.Services, path string) {
	t := a.current()
	if t == nil {
		return
	}
	data, err := transfer.FileData(filepath.Join(t.Workspace, filepath.FromSlash(path)))
	if err != nil {
		a.notifyError(locales.Text("复制文件失败：") + err.Error())
		return
	}
	services.WriteClipboardData(data)
	a.notifySuccess(locales.Text("文件已复制"))
}

// pasteFiles captures the clipboard and destination on the UI thread. The
// background copy stays pinned to that workspace even if the user switches tabs.
func (a *desktop) pasteFiles(services ui.Services, target string) {
	t := a.current()
	if t == nil {
		return
	}
	sources, err := services.ReadClipboardData(transfer.FileList, transfer.URIList).Files()
	if err != nil && !errors.Is(err, transfer.ErrFormat) {
		a.notifyError(locales.Text("读取剪贴板失败：") + err.Error())
		return
	}
	if len(sources) == 0 {
		a.notify(locales.Text("剪贴板中没有文件"))
		return
	}
	if a.core == nil {
		return
	}
	req := backend.CopyFilesIntoWorkspaceRequest{Workspace: t.Workspace, TargetDir: target, Sources: sources, FilesOnly: true}
	win := a.win
	go func() {
		result, err := a.core.CopyFilesIntoWorkspace(req)
		if win == nil {
			return
		}
		win.Update(func() {
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			if len(result.Failed) > 0 {
				a.notifyError(fmt.Sprintf(locales.Text("已复制 %d 个文件，%d 项失败"), len(result.Copied), len(result.Failed)))
			} else if len(result.Copied) == 0 {
				a.notify(locales.Text("剪贴板中没有可复制的文件（不支持目录）"))
			} else {
				a.notifySuccess(fmt.Sprintf(locales.Text("已粘贴 %d 个文件"), len(result.Copied)))
			}
			if current := a.current(); current != nil && workspaceKey(current.Workspace) == workspaceKey(req.Workspace) {
				if req.TargetDir == "" {
					a.refreshFiles()
				} else {
					a.refreshFolder(req.TargetDir)
				}
			}
		})
	}()
}

// ---- File info -----------------------------------------------------------

func (a *desktop) openFileInfo(path string) {
	a.openFileInfoWithNote(path, "")
}

func (a *desktop) openFileInfoWithNote(path, note string) {
	t := a.current()
	if t == nil {
		return
	}
	// The modal covers the preview without retiring its page or playback position.
	a.hideFilePreview()
	a.closeFileInfo()
	a.filesF.InfoOpen = true
	a.filesF.InfoPath = path
	a.filesF.InfoWorkspace = t.Workspace
	a.filesF.InfoNote = note
	a.filesF.InfoDetails = false
	a.filesF.Info = backend.WorkspaceFileInfo{}
	a.loadFileInfo(false)
}

func (a *desktop) closeFileInfo() {
	if a.filesF.InfoCancel != nil {
		a.filesF.InfoCancel()
		a.filesF.InfoCancel = nil
	}
	a.filesF.InfoGeneration++
	a.filesF.InfoOpen = false
	a.filesF.Info = backend.WorkspaceFileInfo{}
}

func (a *desktop) loadFileInfo(details bool) {
	if a.filesF.InfoCancel != nil {
		a.filesF.InfoCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.filesF.InfoCancel = cancel
	a.filesF.InfoGeneration++
	generation := a.filesF.InfoGeneration
	req := backend.WorkspacePathRequest{Workspace: a.filesF.InfoWorkspace, Path: a.filesF.InfoPath}
	a.filesF.InfoBusy = true
	a.filesF.InfoError = ""
	go func() {
		defer cancel()
		info, err := a.core.InspectWorkspaceFile(ctx, req, details)
		if ctx.Err() != nil {
			return
		}
		a.win.Update(func() {
			if generation != a.filesF.InfoGeneration || !a.filesF.InfoOpen || a.closing {
				return
			}
			a.filesF.InfoBusy = false
			if err != nil {
				a.filesF.InfoError = err.Error()
				return
			}
			a.filesF.Info = info
			a.filesF.InfoDetails = details
		})
	}()
}

func (a *desktop) fileInfoDialog(c *ui.Context) {
	if !a.filesF.InfoOpen {
		return
	}
	info := a.filesF.Info
	dialog := modalCard(c, &a.filesF.InfoOpen, 640, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.Text("文件信息")).FontSize(fsDialogTitle).Bold()
			ui.Spacer(c)
			iconAction(c, "file-info-close", locales.Text("关闭属性"), "close", 24, a.closeFileInfo)
		})
		fieldNote(c, a.filesF.InfoPath, a.secondaryInk())
		if a.filesF.InfoNote != "" {
			fieldNote(c, a.filesF.InfoNote, a.secondaryInk())
		}
		if a.filesF.InfoBusy {
			busyRow(c, locales.Text("正在读取文件信息…"), a.secondaryInk())
			return
		}
		if a.filesF.InfoError != "" {
			notice(c, "error", locales.Text("读取文件信息失败：")+a.filesF.InfoError, a.secondaryInk())
			return
		}
		a.fileInfoSection(c, locales.Text("基本信息"), [][2]string{
			{locales.Text("相对路径"), info.Path},
			{locales.Text("完整路径"), info.Absolute},
			{locales.Text("大小"), fmt.Sprintf("%s (%d B)", fmtBytes(info.Size), info.Size)},
			{locales.Text("类型"), strings.TrimPrefix(info.Extension, ".")},
		})
		a.fileInfoSection(c, locales.Text("时间"), [][2]string{
			{locales.Text("修改时间"), info.ModTime},
			{locales.Text("创建时间"), info.BirthTime},
		})
		if info.IsDir && a.filesF.InfoDetails {
			a.fileInfoSection(c, locales.Text("目录统计"), [][2]string{
				{locales.Text("子目录数"), fmt.Sprint(info.DirCount)},
				{locales.Text("文件数"), fmt.Sprint(info.FileCount)},
				{locales.Text("总大小"), fmtBytes(info.TotalSize)},
			})
		}
		if info.SHA256 != "" {
			a.fileInfoSection(c, locales.Text("校验和"), [][2]string{
				{"MD5", info.MD5},
				{"SHA-256", info.SHA256},
				{"CRC32", info.CRC32},
			})
		}
		a.fileInfoSection(c, locales.Text("存储与权限"), [][2]string{
			{locales.Text("权限"), info.Mode},
			{locales.Text("占用空间"), fmtBytes(info.AllocSize)},
			{locales.Text("块数"), fmt.Sprint(info.Blocks)},
			{locales.Text("块大小"), fmtBytes(info.BlockSize)},
			{locales.Text("链接目标"), info.LinkTarget},
		})
		modalActions(c, func() {
			if !a.filesF.InfoDetails {
				label := locales.Text("计算校验和")
				if info.IsDir {
					label = locales.Text("统计目录")
				}
				flat(c, label).OnClick(func() { a.loadFileInfo(true) })
			}
			if !info.IsDir {
				req := backend.WorkspacePathRequest{Workspace: a.filesF.InfoWorkspace, Path: a.filesF.InfoPath}
				flat(c, locales.Text("系统应用打开")).OnClick(func() { a.job(func() error { return a.core.OpenWorkspaceFile(req) }) })
			}
			flat(c, locales.Text("关闭")).OnClick(a.closeFileInfo)
		})
	})
	if dialog.Dismissed() {
		a.closeFileInfo()
	}
}

func (a *desktop) fileInfoSection(c *ui.Context, title string, rows [][2]string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, strings.ToUpper(title)).FontSize(fsTiny).TextColor(a.secondaryInk())
		divider(c)
		for _, row := range rows {
			if row[1] == "" {
				continue
			}
			row := row
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(96).Children(func() { fieldLabel(c, row[0]) })
				uikit.Tooltip(c, ui.Text(c, row[1]).Font(monoFamily).FontSize(fsTiny).Selectable().Grow(1).SingleLine(), row[1])
				iconAction(c, "copy-"+title+row[0], locales.Text("复制"), "copy", 18, func() {
					c.WriteClipboard(row[1])
					c.Toast(locales.Text("已复制"))
				})
			})
		}
	})
}

// ---- Git diff ------------------------------------------------------------

func (a *desktop) loadGitDiff(t *conversation, repo string, manual ...bool) {
	if t == nil {
		return
	}
	a.filesF.GitBusy = true
	workspace := t.Workspace
	go func() {
		result := a.core.GetGitDiff(workspace, repo)
		a.win.Update(func() {
			a.filesF.GitBusy = false
			a.filesF.GitData = result
			a.filesF.GitTree = buildGitTree(result.Files)
			if len(manual) > 0 && manual[0] {
				if result.Error != "" {
					a.notifyError(result.Error)
				} else {
					a.notifySuccess(locales.Text("Git 改动已刷新"))
				}
			}
		})
	}()
}

func (a *desktop) gitDiffDialog(c *ui.Context) {
	if !a.filesF.GitOpen {
		return
	}
	ui.DialogBase(c, &a.filesF.GitOpen, func(backdrop, panel ui.Element) {
		modalBackdrop(backdrop)
		w, h := c.Size()
		modalPanelChrome(c, panel, w*0.9)
		panel.Width(w * 0.9).Height(h * 0.85)
		ui.Column(c).Fill().Gap(gapSection).Selectable().Children(func() {
			t := a.current()
			data := a.filesF.GitData
			ui.Row(c).AlignItems(ui.Center).Gap(gapField).Wrap().Children(func() {
				ui.Text(c, locales.Text("Git 改动")).FontSize(fsDialogTitle).Bold()
				if data.Branch != "" {
					neutralChip(c, data.Branch)
				}
				if data.Truncated {
					statusBadge(c, locales.Text("已截断"), "warn")
				}
				ui.Spacer(c)
				flat(c, locales.Text("刷新")).Disabled(a.filesF.GitBusy).OnClick(func() { a.loadGitDiff(t, a.filesF.GitRepo, true) })
				iconAction(c, "git-diff-close", locales.Text("关闭"), "close", 24, func() {
					a.core.CancelGitDiff()
					a.filesF.GitOpen = false
				})
			})
			if a.filesF.GitBusy {
				busyRow(c, locales.Text("正在加载 diff…"), a.secondaryInk())
				return
			}
			if data.Error != "" {
				notice(c, "error", data.Error, a.secondaryInk())
				return
			}
			if !data.IsRepo {
				emptyState(c, locales.Text("当前目录不是 Git 仓库"), "", a.secondaryInk())
				return
			}
			if len(data.Files) == 0 {
				emptyState(c, locales.Text("当前没有 Git 改动"), "", a.secondaryInk())
				return
			}
			ui.Row(c).Grow(1).MinHeight(0).Gap(gapSection).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).WidthPercent(30).MinWidth(120).MinHeight(0).Children(func() {
					ui.Tree(c.Key("git-tree"), func() {
						a.gitTreeRows(c, a.filesF.GitTree, data.Files)
					})
				})
				ui.Scroll(c).Grow(1).MinWidth(0).Children(func() {
					ui.Column(c).Gap(gapInline).Children(func() {
						if a.filesF.GitFile < 0 || a.filesF.GitFile >= len(data.Files) {
							fieldNote(c, locales.Text("选择一个文件查看 diff。"), a.secondaryInk())
							return
						}
						file := data.Files[a.filesF.GitFile]
						ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
							ui.Text(c, file.Path).Font(monoFamily).FontSize(fsSmall).Grow(1).SingleLine()
							if file.Binary {
								statusBadge(c, locales.Text("二进制"), "warn")
							}
							if file.Truncated {
								statusBadge(c, locales.Text("已截断"), "warn")
							}
						})
						if file.Diff == "" {
							fieldNote(c, locales.Text("无文本 diff。"), a.secondaryInk())
							return
						}
						gitUnifiedDiff(c, file.Diff, a.secondaryInk())
					})
				})
			})
		})
	})
}

func gitStatusLetter(status string) string {
	switch strings.ToUpper(status) {
	case "ADDED", "A":
		return "A"
	case "DELETED", "D":
		return "D"
	case "MODIFIED", "M":
		return "M"
	case "RENAMED", "R":
		return "R"
	case "UNTRACKED", "?":
		return "?"
	}
	if status == "" {
		return "M"
	}
	return status[:1]
}

type gitTreeNode struct {
	name, path string
	status     string
	index      int
	children   []*gitTreeNode
}

func buildGitTree(files []backend.GitDiffFile) []*gitTreeNode {
	root := &gitTreeNode{index: -1}
	for index, file := range files {
		parts := strings.Split(strings.ReplaceAll(file.Path, "\\", "/"), "/")
		node := root
		for n, name := range parts {
			path := strings.Join(parts[:n+1], "/")
			var child *gitTreeNode
			for _, existing := range node.children {
				if existing.path == path {
					child = existing
					break
				}
			}
			if child == nil {
				child = &gitTreeNode{name: name, path: path, index: -1}
				node.children = append(node.children, child)
			}
			if n == len(parts)-1 {
				child.index = index
				child.status = gitStatusLetter(file.Status)
			}
			node = child
		}
	}
	var order func(*gitTreeNode)
	order = func(n *gitTreeNode) {
		sort.SliceStable(n.children, func(i, j int) bool {
			a, b := n.children[i], n.children[j]
			if (a.index < 0) != (b.index < 0) {
				return a.index < 0
			}
			return a.name < b.name
		})
		for _, child := range n.children {
			order(child)
			if n.status == "" {
				n.status = child.status
			} else if n.status != child.status {
				n.status = "M"
			}
		}
	}
	order(root)
	return root.children
}
func (a *desktop) gitTreeRows(c *ui.Context, nodes []*gitTreeNode, files []backend.GitDiffFile) {
	for _, node := range nodes {
		node := node
		folder := node.index < 0
		// Git folders start expanded, retaining their state across diff reloads.
		open := a.popupFlag("git-folder-open-" + node.path)
		initialized := a.popupFlag("git-folder-initialized-" + node.path)
		if !*initialized {
			*open, *initialized = true, true
		}
		var expanded *bool
		var children func()
		added, deleted := 0, 0
		if folder {
			expanded = open
			children = func() { a.gitTreeRows(c, node.children, files) }
		} else {
			added, deleted = files[node.index].Added, files[node.index].Deleted
		}
		row := uikit.GitTreeItem(c.Key("git-tree-"+node.path), node.name, node.status, added, deleted, expanded, children).
			Height(25).FontSize(fsSmall)
		uikit.TreeSelection(c, row, !folder && a.filesF.GitFile == node.index)
		row.OnClick(func() {
			if folder {
				*open = !*open
			} else {
				a.filesF.GitFile = node.index
			}
		})
	}
}
