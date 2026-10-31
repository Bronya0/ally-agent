// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
	"errors"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
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

func isPreviewImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

func (a *desktop) fileEditor(c *ui.Context, file *workspaceFile) {
	ui.Column(c).Grow(1).MinWidth(0).BorderWidth(0, 0, 0, 1).BorderColor(c.Theme().Border).Children(func() {
		c.OnShortcut(ui.Cmd, ui.KeyS, func() { a.saveFile() })
		ui.Row(c).Height(34).Padding(0, padRow).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			name := file.Path
			if file.Editor.Buffer.Modified() {
				name += " ●"
			}
			uikit.Tooltip(c, ui.Text(c, short(name, 46)).FontSize(fsSmall).Grow(1), file.Path)
			ext := strings.ToLower(filepath.Ext(file.Path))
			if ext == ".md" || ext == ".html" || ext == ".htm" {
				label := "预览"
				if a.filesF.Preview {
					label = "编辑源码"
				}
				flat(c, label).OnClick(func() { a.filesF.Preview = !a.filesF.Preview })
			}
			flat(c, "刷新").Disabled(file.Saving).OnClick(func() {
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
			label := "保存"
			if file.Saving {
				label = "保存中…"
			}
			flat(c, label).Disabled(file.Image != nil || file.Saving || !file.Editor.Buffer.Modified()).OnClick(func() { a.saveFile() })
			if file.Document != nil {
				flat(c, "系统打开").OnClick(func() { a.openSystemFile(file.Path) })
			}
			iconAction(c, "editor-close", "关闭编辑器", "close", 24, func() { a.closeFile() })
		})
		if file.Image != nil {
			ui.Box(c).Grow(1).MinHeight(0).Padding(20).Center().Children(func() {
				ui.Image(c, file.Image).Fill().Fit(ui.Contain)
			})
			return
		}
		if file.Document != nil {
			a.documentFileView(c, file)
			return
		}
		if a.filesF.Preview && strings.HasSuffix(strings.ToLower(file.Path), ".md") {
			// Read the buffer once per edit, not once per frame: Buffer.String()
			// builds the whole text again, and frames come with every animation.
			if v := file.Editor.Buffer.Version(); !file.PreviewReady || v != file.PreviewVersion {
				file.PreviewText, file.PreviewVersion, file.PreviewReady = file.Editor.Buffer.String(), v, true
			}
			ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).Padding(padPage).Children(func() {
					if file.PreviewMarkdown == nil {
						file.PreviewMarkdown = a.readingMarkdownOptions()
					}
					a.configureReadingMarkdown(file.PreviewMarkdown)
					markdown.View(c, file.PreviewText, file.PreviewMarkdown)
				})
			})
			return
		}
		file.Editor.ReadOnly = a.closing || file.Saving
		editor.View(c.Key("editor-"+file.Path), file.Editor).Grow(1).MinHeight(0).FillWidth()
	})
}

func (a *desktop) fileTree(c *ui.Context) {
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
		title := "文件"
		tip := ""
		if t := a.current(); t != nil {
			title = workspaceLabel(t.Workspace)
			tip = t.Workspace
		}
		ui.Row(c).Height(30).Padding(0, padRow).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			uikit.Tooltip(c, ui.Text(c, short(title, 24)).FontSize(fsSmall).TextColor(ink).Grow(1), tip)
			uikit.IconButton(c, "explorer-refresh", "刷新", iconImage("refresh"), 22, uikit.IconQuiet, func() { a.refreshFiles(true) }, ink)
			uikit.IconButton(c, "explorer-help", "文件浏览器帮助", iconImage("info"), 22, uikit.IconQuiet, func() { a.filesF.HelpOpen = !a.filesF.HelpOpen }, ink)
			uikit.IconButton(c, "explorer-close", "关闭文件栏", iconImage("close"), 22, uikit.IconQuiet, func() { a.explorer = false }, ink)
		})
		// Put the fallback menu on the viewport: the content column can be
		// shorter than it, even with FillHeight in a scrolling container.
		// A row's own menu takes precedence over this ancestor's menu.
		tree := ui.Scroll(c.Key("explorer-tree")).Grow(1).MinHeight(0).Focusable().Label("工作区文件列表")
		tree.Children(func() {
			area := ui.Column(c).Padding(3, 0).FillWidth()
			if len(a.files) == 0 {
				area.Children(func() { fieldNote(c, "目录为空", a.secondaryInk()) })
			}
			area.Children(func() { ui.Tree(c.Key("workspace-tree"), func() { a.fileRows(c, a.files) }).FillWidth() })
		})
		uikit.ContextMenu(tree, func(m *ui.Menu) {
			if m.Item("新建文件").Chosen() {
				a.openFilePrompt(false, "")
			}
			if m.Item("新建目录").Chosen() {
				a.openFilePrompt(true, "")
			}
			if m.Item("打开目录").Chosen() {
				a.openInManager("")
			}
			m.Separator()
			if m.Item("粘贴文件").Chosen() {
				a.pasteFiles(services, "")
			}
			m.Separator()
			if m.Item("刷新").Chosen() {
				a.refreshFiles(true)
			}
		})
		if a.filesF.HelpOpen {
			ui.Column(c).Padding(8, 10).Gap(2).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
				fieldNote(c, "文件浏览器帮助", a.secondaryInk())
				for _, line := range []string{
					"单击文件打开，单击目录展开",
					"右键文件：复制文件 / 复制路径 / 重命名 / 删除 / 属性",
					"右键空白处或目录：粘贴文件；同名文件自动另存副本",
					"Ctrl+C 复制聚焦文件，Ctrl+V 粘贴到聚焦目录（文件行则为其父目录）",
					"Ctrl+S 保存当前文件（带版本校验）",
					"Ctrl+Space 补全，悬停查看符号信息，F12 / ⌘或Ctrl+点击跳转 Go 定义",
					"Markdown 可切换预览，图片直接预览",
				} {
					fieldNote(c, "· "+line, a.secondaryInk())
				}
			})
		}
		ui.Row(c).Height(26).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Padding(0, padRow).Children(func() {
			iconAction(c, "explorer-open", "在文件管理器中打开工作区", "folderopen", 22, func() {
				if t := a.current(); t != nil {
					p := t.Workspace
					a.job(func() error { return a.core.OpenPathInFileManager(p) })
				}
			})
			ui.Spacer(c)
			addButton(c, "explorer-new-file", "新建文件", "plus", 22, func() { a.openFilePrompt(false, "") })
		})
	})
	a.filePromptDialog(c)
	a.fileInfoDialog(c)
}

func (a *desktop) fileRows(c *ui.Context, entries []backend.FileEntry) {
	services := c.Services()
	if a.fileOpen == nil {
		a.fileOpen = map[string]bool{}
	}
	for _, entry := range entries {
		entry := entry
		path := strings.ReplaceAll(entry.Path, "\\", "/")
		name := entry.Name
		if name == "" {
			name = filepath.Base(path)
		}
		if entry.Symlink {
			name += " →"
		}
		open := a.fileOpen[path]
		var children func()
		var expanded *bool
		if entry.Dir {
			expanded = &open
			children = func() { a.fileRows(c, a.fileChildren[path]) }
		}
		row := ui.TreeItem(c.Key("file-"+path), name, expanded, children).
			Height(23).FontSize(fsSmall).Label(entry.Name)
		if a.currentPalette().dark {
			row.TextColor(ui.Hex("#cccccc"))
		}
		selected := false
		if file := a.activeFile(); file != nil {
			selected = file.Path == entry.Path
		}
		if a.filesF.InfoOpen {
			selected = a.filesF.InfoPath == entry.Path
		}
		uikit.TreeSelection(c, row, selected)
		if strings.HasPrefix(entry.Name, ".") {
			row.Opacity(0.55)
		}
		uikit.Tooltip(c, row, path)
		// Only an actual arrow/key expansion starts a lazy directory read.
		// MyGo owns focus, keyboard navigation and the disclosure control.
		if open != a.fileOpen[path] {
			a.fileOpen[path] = open
			if open {
				a.loadFolderChildren(path)
			}
		}
		target := fileParent(path)
		if entry.Dir {
			target = path
		}
		row.OnShortcut(ui.Cmd, ui.KeyV, func() { a.pasteFiles(services, target) })
		if !entry.Dir {
			row.OnShortcut(ui.Cmd, ui.KeyC, func() { a.copyExplorerFile(services, path) })
		}
		row.OnClick(func() {
			if entry.Dir {
				a.toggleFolder(path)
			} else {
				a.openFile(entry.Path)
			}
		})
		// The shared menu keeps row-specific actions ahead of the viewport's.
		uikit.ContextMenu(row, func(m *ui.Menu) {
			if entry.Dir {
				if m.Item("新建文件").Chosen() {
					a.openFilePrompt(false, path)
				}
				if m.Item("新建目录").Chosen() {
					a.openFilePrompt(true, path)
				}
			}
			if m.Item("打开目录").Chosen() {
				if entry.Dir {
					a.openInManager(path)
				} else {
					a.openInManager(fileParent(path))
				}
			}
			m.Separator()
			if !entry.Dir {
				if m.Item("复制文件").Chosen() {
					a.copyExplorerFile(services, path)
				}
			}
			if m.Item("粘贴文件").Chosen() {
				a.pasteFiles(services, target)
			}
			m.Separator()
			if m.Item("复制相对路径").Chosen() {
				services.WriteClipboard(path)
			}
			if m.Item("复制完整路径").Chosen() {
				full := path
				if t := a.current(); t != nil {
					full = filepath.Join(t.Workspace, filepath.FromSlash(path))
				}
				services.WriteClipboard(full)
			}
			if m.Item("属性").Chosen() {
				a.openFileInfo(entry.Path)
			}
			m.Separator()
			if m.Item("重命名").Chosen() {
				a.openRenamePrompt(path)
			}
			if m.Item("删除").Chosen() {
				a.deleteFileEntry(entry)
			}
			if entry.Dir {
				// Mirror the Vue explorer: a directory row can refresh just itself.
				if m.Item("刷新").Chosen() {
					a.refreshFolder(path)
				}
			}
		})
	}
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
	title := "新建文件"
	if a.filesF.Rename {
		title = "重命名"
	} else if a.filesF.NewDir {
		title = "新建目录"
	}
	modalCard(c, &a.filesF.NewOpen, widthModalSmall, func() {
		modalTitle(c, title, "名称不含路径分隔符。")
		value := a.filesF.NewName
		uikit.Input(c.Key("file-prompt-name"), &value).Label(title).FillWidth().AutoFocus()
		a.filesF.NewName = value
		inlineError(c, "file-prompt-error", a.filesF.PromptErr)
		modalActions(c, func() {
			flat(c, "取消").OnClick(func() { a.filesF.NewOpen = false })
			primary(c, map[bool]string{true: "保存", false: "创建"}[a.filesF.Rename]).OnClick(func() { a.submitFilePrompt() })
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
		a.filesF.PromptErr = "名称无效，请输入不含路径的名称"
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
					// The open document would keep pointing at a path that no longer
					// exists, so release it (asking first if it has edits).
					if file := a.activeFile(); file != nil && strings.EqualFold(filepath.ToSlash(file.Path), filepath.ToSlash(path)) {
						a.closeFile()
					}
					a.refreshFiles()
					a.notify("已重命名")
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
			a.win.Update(func() { a.refreshFiles(); a.notify("已创建 " + full) })
		}
		return err
	})
}

func (a *desktop) deleteFileEntry(entry backend.FileEntry) {
	t := a.current()
	if t == nil {
		return
	}
	kind := "文件"
	if entry.Dir {
		kind = "目录"
	}
	detail := "确定删除" + kind + "「" + entry.Name + "」吗？此操作不可恢复。"
	a.confirmAction("确认删除", detail, func() error {
		return a.core.DeletePath(backend.DeletePathRequest{Workspace: t.Workspace, Paths: []string{entry.Path}, Recursive: entry.Dir})
	}, func() {
		if file := a.activeFile(); file != nil && strings.HasPrefix(file.Path, entry.Path) {
			a.closeFile()
		}
		a.refreshFiles()
	})
}

func (a *desktop) toggleFolder(path string) {
	if a.fileOpen == nil {
		a.fileOpen = map[string]bool{}
	}
	a.fileOpen[path] = !a.fileOpen[path]
	if !a.fileOpen[path] {
		return
	}
	if _, loaded := a.fileChildren[path]; loaded {
		return
	}
	a.loadFolderChildren(path)
}

// refreshFolder reloads one directory in place and keeps the tree expanded,
// the way the Vue explorer refreshes a single node instead of the whole tree.
func (a *desktop) refreshFolder(path string) {
	if path == "" {
		a.refreshFiles()
		return
	}
	if a.fileOpen == nil {
		a.fileOpen = map[string]bool{}
	}
	if a.fileChildren == nil {
		a.fileChildren = map[string][]backend.FileEntry{}
	}
	a.fileOpen[path] = true
	delete(a.fileChildren, path)
	a.loadFolderChildren(path, true)
}

// loadFolderChildren lists one directory into fileChildren.
func (a *desktop) loadFolderChildren(path string, manual ...bool) {
	if a.core == nil || a.fileLoading[path] {
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
			if epoch != a.fileEpoch {
				return
			}
			delete(a.fileLoading, path)
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			if a.fileChildren == nil {
				a.fileChildren = map[string][]backend.FileEntry{}
			}
			a.fileChildren[path] = result.Entries
			if len(manual) > 0 && manual[0] {
				a.notifySuccess("目录已刷新")
			}
			if result.Truncated {
				a.notify("目录过大，仅显示前 1000 项")
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
		a.notifyError("复制文件失败：" + err.Error())
		return
	}
	services.WriteClipboardData(data)
	a.notifySuccess("文件已复制")
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
		a.notifyError("读取剪贴板失败：" + err.Error())
		return
	}
	if len(sources) == 0 {
		a.notify("剪贴板中没有文件")
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
				a.notifyError(fmt.Sprintf("已复制 %d 个文件，%d 项失败", len(result.Copied), len(result.Failed)))
			} else if len(result.Copied) == 0 {
				a.notify("剪贴板中没有可复制的文件（不支持目录）")
			} else {
				a.notifySuccess(fmt.Sprintf("已粘贴 %d 个文件", len(result.Copied)))
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
			ui.Text(c, "文件信息").FontSize(17).Bold()
			ui.Spacer(c)
			iconAction(c, "file-info-close", "关闭属性", "close", 24, a.closeFileInfo)
		})
		fieldNote(c, a.filesF.InfoPath, a.secondaryInk())
		if a.filesF.InfoNote != "" {
			fieldNote(c, a.filesF.InfoNote, a.secondaryInk())
		}
		if a.filesF.InfoBusy {
			busyRow(c, "正在读取文件信息…", a.secondaryInk())
			return
		}
		if a.filesF.InfoError != "" {
			notice(c, "error", "读取文件信息失败："+a.filesF.InfoError, a.secondaryInk())
			return
		}
		a.fileInfoSection(c, "基本信息", [][2]string{
			{"相对路径", info.Path},
			{"完整路径", info.Absolute},
			{"大小", fmt.Sprintf("%s (%d B)", fmtBytes(info.Size), info.Size)},
			{"类型", strings.TrimPrefix(info.Extension, ".")},
		})
		a.fileInfoSection(c, "时间", [][2]string{
			{"修改时间", info.ModTime},
			{"创建时间", info.BirthTime},
		})
		if info.IsDir && a.filesF.InfoDetails {
			a.fileInfoSection(c, "目录统计", [][2]string{
				{"子目录数", fmt.Sprint(info.DirCount)},
				{"文件数", fmt.Sprint(info.FileCount)},
				{"总大小", fmtBytes(info.TotalSize)},
			})
		}
		if info.SHA256 != "" {
			a.fileInfoSection(c, "校验和", [][2]string{
				{"MD5", info.MD5},
				{"SHA-256", info.SHA256},
				{"CRC32", info.CRC32},
			})
		}
		a.fileInfoSection(c, "存储与权限", [][2]string{
			{"权限", info.Mode},
			{"占用空间", fmtBytes(info.AllocSize)},
			{"块数", fmt.Sprint(info.Blocks)},
			{"块大小", fmtBytes(info.BlockSize)},
			{"链接目标", info.LinkTarget},
		})
		modalActions(c, func() {
			if !a.filesF.InfoDetails {
				label := "计算校验和"
				if info.IsDir {
					label = "统计目录"
				}
				flat(c, label).OnClick(func() { a.loadFileInfo(true) })
			}
			if !info.IsDir {
				req := backend.WorkspacePathRequest{Workspace: a.filesF.InfoWorkspace, Path: a.filesF.InfoPath}
				flat(c, "系统应用打开").OnClick(func() { a.job(func() error { return a.core.OpenWorkspaceFile(req) }) })
			}
			flat(c, "关闭").OnClick(a.closeFileInfo)
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
				iconAction(c, "copy-"+title+row[0], "复制", "copy", 18, func() {
					c.WriteClipboard(row[1])
					c.Toast("已复制")
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
			if len(manual) > 0 && manual[0] {
				if result.Error != "" {
					a.notifyError(result.Error)
				} else {
					a.notifySuccess("Git 改动已刷新")
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
				ui.Text(c, "Git 改动").FontSize(17).Bold()
				if data.Branch != "" {
					neutralChip(c, data.Branch)
				}
				if data.Truncated {
					statusBadge(c, "已截断", "warn")
				}
				ui.Spacer(c)
				flat(c, "刷新").Disabled(a.filesF.GitBusy).OnClick(func() { a.loadGitDiff(t, a.filesF.GitRepo, true) })
				iconAction(c, "git-diff-close", "关闭", "close", 24, func() {
					a.core.CancelGitDiff()
					a.filesF.GitOpen = false
				})
			})
			if a.filesF.GitBusy {
				busyRow(c, "正在加载 diff…", a.secondaryInk())
				return
			}
			if data.Error != "" {
				notice(c, "error", data.Error, a.secondaryInk())
				return
			}
			if !data.IsRepo {
				emptyState(c, "当前目录不是 Git 仓库", "", a.secondaryInk())
				return
			}
			if len(data.Files) == 0 {
				emptyState(c, "当前没有 Git 改动", "", a.secondaryInk())
				return
			}
			ui.Row(c).Grow(1).MinHeight(0).Gap(gapSection).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).WidthPercent(30).MinWidth(120).MinHeight(0).Children(func() {
					ui.Tree(c.Key("git-tree"), func() {
						a.gitTreeRows(c, buildGitTree(data.Files), data.Files)
					})
				})
				ui.Scroll(c).Grow(1).MinWidth(0).Children(func() {
					ui.Column(c).Gap(gapInline).Children(func() {
						if a.filesF.GitFile < 0 || a.filesF.GitFile >= len(data.Files) {
							fieldNote(c, "选择一个文件查看 diff。", a.secondaryInk())
							return
						}
						file := data.Files[a.filesF.GitFile]
						ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
							ui.Text(c, file.Path).Font(monoFamily).FontSize(fsSmall).Grow(1).SingleLine()
							if file.Binary {
								statusBadge(c, "二进制", "warn")
							}
							if file.Truncated {
								statusBadge(c, "已截断", "warn")
							}
						})
						if file.Diff == "" {
							fieldNote(c, "无文本 diff。", a.secondaryInk())
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
