// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
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
			ui.Text(c, short(name, 46)).FontSize(fsSmall).Grow(1).Tooltip(file.Path)
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
			iconAction(c, "editor-close", "关闭编辑器", "close", 24, func() { a.closeFile() })
		})
		if file.Image != nil {
			ui.Box(c).Grow(1).MinHeight(0).Padding(20).Center().Children(func() {
				ui.Image(c, file.Image).Fill().Fit(ui.Contain)
			})
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
	width := a.prefs.TreeWidth
	if width < 150 || width > 600 {
		width = 270
	}
	// Unselectable keeps the window-wide selectable text layer from claiming
	// right-clicks: without it every secondary click opens the root text menu
	// and neither a row menu nor the blank-area menu ever sees RightClicked.
	ui.Column(c).Width(width).Shrink(0).Unselectable().Background(a.currentPalette().panel).BorderWidth(0, 0, 0, 1).BorderColor(c.Theme().Border).Children(func() {
		c.OnShortcut(ui.Cmd, ui.KeyV, func() { a.pasteFiles() })
		title := "文件"
		tip := ""
		if t := a.current(); t != nil {
			title = workspaceLabel(t.Workspace)
			tip = t.Workspace
		}
		ui.Row(c).Height(30).Padding(0, padRow).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			ui.Text(c, short(title, 24)).FontSize(fsSmall).Grow(1).Tooltip(tip)
			iconAction(c, "explorer-refresh", "刷新", "refresh", 22, func() { a.refreshFiles() })
			iconAction(c, "explorer-help", "文件浏览器帮助", "info", 22, func() { a.filesF.HelpOpen = !a.filesF.HelpOpen })
			iconAction(c, "explorer-close", "关闭文件栏", "close", 22, func() { a.explorer = false })
		})
		// The tree itself owns the blank-area menu; a row builds its own menu, so
		// the blank one only appears where there is no file or folder under the
		// pointer. Item order mirrors the Vue explorer's empty-tree menu.
		tree := ui.Scroll(c).Grow(1).MinHeight(0)
		var area ui.Element
		tree.Children(func() {
			// Filling the viewport makes the empty space below the last row
			// part of this column, so a right-click there belongs to the tree
			// and not to the scrolling surface.
			area = ui.Column(c).Padding(3, 0).FillWidth().FillHeight()
			if len(a.files) == 0 {
				area.Children(func() { fieldNote(c, "目录为空") })
			}
			area.Children(func() { a.fileRows(c, a.files, 0) })
		})
		// ContextMenu is the system's context menu: it opens where the secondary
		// button is, and from the keyboard on the menu key or Shift+F10, so the
		// open flag and the hit test a popover needed are gone.
		area.ContextMenu(func(m *ui.Menu) {
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
			if m.Item("刷新").Chosen() {
				a.refreshFiles()
			}
		})
		if a.filesF.HelpOpen {
			ui.Column(c).Padding(8, 10).Gap(2).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
				fieldNote(c, "文件浏览器帮助")
				for _, line := range []string{
					"单击文件打开，单击目录展开",
					"右键：新建 / 重命名 / 删除 / 复制路径 / 属性",
					"Ctrl+V 粘贴系统剪贴板里的文件到工作区",
					"Ctrl+S 保存当前文件（带版本校验）",
					"Ctrl+Space 补全，悬停查看符号信息，F12 / ⌘或Ctrl+点击跳转 Go 定义",
					"Markdown 可切换预览，图片直接预览",
				} {
					fieldNote(c, "· "+line)
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

func (a *desktop) fileRows(c *ui.Context, entries []backend.FileEntry, depth int) {
	services := c.Services()
	p := a.currentPalette()
	dirColor := p.text.Mix(p.muted, 0.25)
	fileColor := p.muted.Mix(p.text, 0.25)
	for _, entry := range entries {
		entry := entry
		path := strings.ReplaceAll(entry.Path, "\\", "/")
		name := entry.Name
		if name == "" {
			name = filepath.Base(path)
		}
		hidden := strings.HasPrefix(name, ".")
		row := ui.Row(c.Key("file-"+path)).Height(23).Padding(0, 6+float32(depth)*14).Gap(gapInline).Radius(radiusSmall).AlignItems(ui.Center)
		if row.Hovered() {
			row.Background(c.Theme().SurfaceHover)
		}
		if hidden {
			row.Opacity(0.55)
		}
		row.Children(func() {
			if entry.Dir {
				arrow := "chevron"
				if a.fileOpen[path] {
					arrow = "chevrondown"
				}
				iconGlyph(c, arrow, 10).TextColor(dirColor)
			} else {
				ui.Box(c).Width(10)
			}
			ui.Text(c, name).FontSize(fsSmall).SingleLine().TextColor(func() ui.Color {
				if entry.Dir {
					return dirColor
				}
				return fileColor
			}())
			if entry.Symlink {
				fieldNote(c, "→")
			}
		})
		row.Tooltip(path)
		if row.Clicked() {
			if entry.Dir {
				a.toggleFolder(path)
			} else {
				a.openFile(entry.Path)
			}
		}
		// ContextMenu replaces the open flag, the right-click hit test and the
		// closing of the blank-area menu: the system opens one menu at a time.
		row.ContextMenu(func(m *ui.Menu) {
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
		if entry.Dir && a.fileOpen[path] {
			a.fileRows(c, a.fileChildren[path], depth+1)
		}
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
					a.status = "已重命名"
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
			a.win.Update(func() { a.refreshFiles(); a.status = "已创建 " + full })
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
	a.loadFolderChildren(path)
}

// loadFolderChildren lists one directory into fileChildren.
func (a *desktop) loadFolderChildren(path string) {
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
		result, err := a.core.ListFiles(backend.ListFilesRequest{Workspace: root, Path: path, MaxDepth: 1, Limit: 1000, IncludeHidden: true})
		a.win.Update(func() {
			if epoch != a.fileEpoch {
				return
			}
			delete(a.fileLoading, path)
			if err != nil {
				a.notify(err.Error())
				return
			}
			if a.fileChildren == nil {
				a.fileChildren = map[string][]backend.FileEntry{}
			}
			a.fileChildren[path] = result.Entries
			if result.Truncated {
				a.status = "目录过大，仅显示前 1000 项"
			}
		})
	}()
}

// pasteFiles copies files from the OS clipboard into the current directory.
func (a *desktop) pasteFiles() {
	t := a.current()
	if t == nil {
		return
	}
	target := ""
	if file := a.activeFile(); file != nil {
		target = fileParent(file.Path)
	}
	go func() {
		sources, err := a.core.ReadClipboardFiles()
		if err != nil {
			a.win.Update(func() { a.status = "读取剪贴板失败（可能被其他程序占用，稍后再试）" })
			return
		}
		if len(sources) == 0 {
			a.win.Update(func() { a.status = "剪贴板中没有文件" })
			return
		}
		result, err := a.core.CopyFilesIntoWorkspace(backend.CopyFilesIntoWorkspaceRequest{Workspace: t.Workspace, TargetDir: target, Sources: sources})
		a.win.Update(func() {
			if err != nil {
				a.notify(err.Error())
				return
			}
			if len(result.Failed) > 0 {
				a.status = fmt.Sprintf("已复制 %d 项，%d 项失败", len(result.Copied), len(result.Failed))
			} else {
				a.status = fmt.Sprintf("已复制 %d 个项目到工作区", len(result.Copied))
			}
			a.refreshFiles()
		})
	}()
}

// ---- File info -----------------------------------------------------------

func (a *desktop) openFileInfo(path string) {
	t := a.current()
	if t == nil {
		return
	}
	a.filesF.InfoOpen = true
	a.filesF.InfoPath = path
	a.filesF.InfoBusy = true
	a.filesF.InfoError = ""
	go func() {
		info, err := a.core.GetWorkspaceFileInfoAt(backend.WorkspacePathRequest{Workspace: t.Workspace, Path: path})
		a.win.Update(func() {
			a.filesF.InfoBusy = false
			if err != nil {
				a.filesF.InfoError = err.Error()
				return
			}
			a.filesF.Info = info
		})
	}()
}

func (a *desktop) fileInfoDialog(c *ui.Context) {
	if !a.filesF.InfoOpen {
		return
	}
	info := a.filesF.Info
	modalCard(c, &a.filesF.InfoOpen, 640, func() {
		modalTitle(c, "文件信息", a.filesF.InfoPath)
		if a.filesF.InfoBusy {
			busyRow(c, "正在计算，请稍候…")
			return
		}
		if a.filesF.InfoError != "" {
			notice(c, "error", "读取文件信息失败："+a.filesF.InfoError)
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
		if info.IsDir {
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
		modalActions(c, func() { flat(c, "关闭").OnClick(func() { a.filesF.InfoOpen = false }) })
	})
}

func (a *desktop) fileInfoSection(c *ui.Context, title string, rows [][2]string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, strings.ToUpper(title)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
		divider(c)
		for _, row := range rows {
			if row[1] == "" {
				continue
			}
			row := row
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(96).Children(func() { fieldLabel(c, row[0]) })
				ui.Text(c, row[1]).Font(monoFamily).FontSize(fsTiny).Selectable().Grow(1).SingleLine().Tooltip(row[1])
				iconAction(c, "copy-"+title+row[0], "复制", "copy", 18, func() {
					c.WriteClipboard(row[1])
					c.Toast("已复制")
				})
			})
		}
	})
}

// ---- Git diff ------------------------------------------------------------

func (a *desktop) loadGitDiff(t *conversation, repo string) {
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
				flat(c, "刷新").Disabled(a.filesF.GitBusy).OnClick(func() { a.loadGitDiff(t, a.filesF.GitRepo) })
				iconAction(c, "git-diff-close", "关闭", "close", 24, func() {
					a.core.CancelGitDiff()
					a.filesF.GitOpen = false
				})
			})
			if a.filesF.GitBusy {
				busyRow(c, "正在加载 diff…")
				return
			}
			if data.Error != "" {
				notice(c, "error", data.Error)
				return
			}
			if !data.IsRepo {
				emptyState(c, "当前目录不是 Git 仓库", "")
				return
			}
			if len(data.Files) == 0 {
				emptyState(c, "当前没有 Git 改动", "")
				return
			}
			ui.Row(c).Grow(1).MinHeight(0).Gap(gapSection).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).WidthPercent(30).MinWidth(120).MinHeight(0).Children(func() {
					ui.Column(c).Gap(1).Children(func() {
						a.gitTreeRows(c, buildGitTree(data.Files), data.Files, 0)
					})
				})
				ui.Scroll(c).Grow(1).MinWidth(0).Children(func() {
					ui.Column(c).Gap(gapInline).Children(func() {
						if a.filesF.GitFile < 0 || a.filesF.GitFile >= len(data.Files) {
							fieldNote(c, "选择一个文件查看 diff。")
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
							fieldNote(c, "无文本 diff。")
							return
						}
						gitUnifiedDiff(c, file.Diff)
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
		}
	}
	order(root)
	return root.children
}
func (a *desktop) gitTreeRows(c *ui.Context, nodes []*gitTreeNode, files []backend.GitDiffFile, depth int) {
	for _, node := range nodes {
		folder := node.index < 0
		closed := a.popupFlag("git-folder-" + node.path)
		row := ui.Row(c.Key("git-tree-"+node.path)).FillWidth().MinWidth(0).Height(25).Padding(0, 6+float32(depth)*14).Gap(6).AlignItems(ui.Center).Radius(4).Cursor(ui.CursorPointer).Label(node.name)
		current := !folder && a.filesF.GitFile == node.index
		ink := c.Theme().Text
		switch {
		case current:
			row.Background(selectionFill(c.Theme().Accent))
			ink = c.Theme().Accent
		case row.Hovered():
			row.Background(c.Theme().SurfaceHover)
		}
		row.Children(func() {
			if folder {
				glyph := "chevrondown"
				if *closed {
					glyph = "chevron"
				}
				iconGlyph(c, glyph, 10).TextColor(c.Theme().TextMuted)
			} else {
				ui.Box(c).Width(10)
			}
			name := ui.Text(c, node.name).FontSize(fsSmall).TextColor(ink).SingleLine().MinWidth(0).Grow(1).Cursor(ui.CursorPointer)
			if current {
				name.FontWeight(600)
			}
			if !folder {
				file := files[node.index]
				ui.Text(c, gitStatusLetter(file.Status)).FontSize(fsTiny).TextColor(colWarning())
				if file.Added > 0 {
					ui.Textf(c, "+%d", file.Added).FontSize(fsTiny).TextColor(colSuccess())
				}
				if file.Deleted > 0 {
					ui.Textf(c, "-%d", file.Deleted).FontSize(fsTiny).TextColor(colDanger())
				}
			}
		})
		if row.Clicked() {
			if folder {
				*closed = !*closed
			} else {
				a.filesF.GitFile = node.index
			}
		}
		if folder && !*closed {
			a.gitTreeRows(c, node.children, files, depth+1)
		}
	}
}
