// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"path/filepath"
	"strings"
	"time"

	"ally-dev/internal/documentpreview"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	xhtml "golang.org/x/net/html"
)

const maxMarkupPreviewBytes = 2 << 20
const maxDocumentPreviewHTML = 2 << 20

var errMarkupPreviewLimit = errors.New("预览内容超过大小上限")

func emptyFileEditor() *editor.State {
	return &editor.State{Buffer: editor.NewBuffer(""), Font: ui.Font{Family: nativeFontFamily(), Size: fsEditor}}
}

func documentPreviewNote(doc *documentpreview.Document) string {
	switch {
	case doc == nil || len(doc.Paragraphs) == 0:
		return "没有可预览的正文，可用“系统打开”查看完整文档。"
	case doc.Truncated:
		return "预览内容已截断，可用“系统打开”查看完整文档。"
	default:
		return "正文提取预览，不包含图片、页眉页脚和原版分页。"
	}
}

func documentHeadingSize(level int) float32 {
	switch {
	case level <= 1:
		return fsPageTitle
	case level == 2:
		return fsHeading
	case level == 3:
		return fsSubtitle
	default:
		return fsBody
	}
}

// A Word preview draws the paragraphs the document package extracted: heading
// levels and text, no markup, so it needs no web view.
func (a *desktop) documentFilePreviewView(c *ui.Context, file *workspaceFile) {
	doc := file.Document
	ui.Column(c.Key("file-preview-document")).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).Padding(gapSection, padPage).FillWidth().Children(func() {
			fieldNote(c, documentPreviewNote(doc), a.secondaryInk())
		})
		if doc == nil || len(doc.Paragraphs) == 0 {
			ui.Box(c).Grow(1).MinHeight(0).Center().Children(func() {
				emptyState(c, "没有可预览的正文", "可用“系统打开”查看完整文档。", a.secondaryInk())
			})
			return
		}
		// The reader allows 2000 paragraphs, so build only the rows on screen.
		ui.List(c.Key("file-preview-document-body"), nil, len(doc.Paragraphs), func(i int) {
			paragraph := doc.Paragraphs[i]
			text := strings.TrimRight(paragraph.Text, " \t\r\n")
			row := ui.Column(c.Key(i)).FillWidth().Padding(0, padPage, gapField, padPage)
			if paragraph.Heading > 0 {
				row.Padding(gapSection, padPage, gapField/2, padPage)
			}
			row.Children(func() {
				if text == "" {
					ui.Box(c).Height(gapField)
					return
				}
				if paragraph.Heading > 0 {
					ui.Text(c, text).FillWidth().LineHeight(1.3).FontSize(documentHeadingSize(paragraph.Heading)).Bold()
					return
				}
				ui.Text(c, text).FillWidth().LineHeight(1.6)
			})
		}).Grow(1).MinHeight(0).FillWidth()
	})
}

type limitedPreviewBuffer struct {
	bytes.Buffer
	limit int
}

func (w *limitedPreviewBuffer) Write(p []byte) (int, error) {
	remaining := w.limit - w.Len()
	if remaining <= 0 {
		return 0, errMarkupPreviewLimit
	}
	if len(p) > remaining {
		n, _ := w.Buffer.Write(p[:remaining])
		return n, errMarkupPreviewLimit
	}
	return w.Buffer.Write(p)
}

func htmlFilePreview(source string) (string, error) {
	if len(source) > maxMarkupPreviewBytes {
		return "", fmt.Errorf("HTML 预览超过 2 MB 限制")
	}
	// Parse before injecting policy: text search can mistake a comment or a
	// script before <head> for the document head and let active content run first.
	document, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return "", fmt.Errorf("解析 HTML 预览失败：%w", err)
	}
	head := findHTMLHead(document)
	if head == nil {
		return "", fmt.Errorf("HTML 预览缺少 head 节点")
	}
	policy := "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; media-src data: blob:; font-src data:; connect-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'"
	metas := []*xhtml.Node{
		{Type: xhtml.ElementNode, Data: "meta", Attr: []xhtml.Attribute{{Key: "charset", Val: "utf-8"}}},
		{Type: xhtml.ElementNode, Data: "meta", Attr: []xhtml.Attribute{{Key: "http-equiv", Val: "Content-Security-Policy"}, {Key: "content", Val: policy}}},
		{Type: xhtml.ElementNode, Data: "meta", Attr: []xhtml.Attribute{{Key: "name", Val: "viewport"}, {Key: "content", Val: "width=device-width,initial-scale=1"}}},
	}
	first := head.FirstChild
	for _, meta := range metas {
		if first == nil {
			head.AppendChild(meta)
		} else {
			head.InsertBefore(meta, first)
		}
	}
	rendered := limitedPreviewBuffer{limit: maxDocumentPreviewHTML}
	if err := xhtml.Render(&rendered, document); err != nil {
		if errors.Is(err, errMarkupPreviewLimit) {
			return "", fmt.Errorf("HTML 预览结果超过 2 MB 限制")
		}
		return "", fmt.Errorf("序列化 HTML 预览失败：%w", err)
	}
	return rendered.String(), nil
}

func findHTMLHead(document *xhtml.Node) *xhtml.Node {
	for node := document.FirstChild; node != nil; node = node.NextSibling {
		if node.Type != xhtml.ElementNode || node.Data != "html" || node.Namespace != "" {
			continue
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == xhtml.ElementNode && child.Data == "head" && child.Namespace == "" {
				return child
			}
		}
	}
	return nil
}

func svgPreviewHTML(name, source string) (string, error) {
	if len(source) > maxMarkupPreviewBytes {
		return "", fmt.Errorf("SVG 预览超过 2 MB 限制")
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(filepath.Base(name)) + `</title><style>html,body{margin:0;min-height:100%;color-scheme:light dark;background:#fff}body{min-height:100vh;display:grid;place-items:center}svg{display:block;max-width:100%;max-height:100vh;width:auto;height:auto}@media(prefers-color-scheme:dark){html,body{background:#202020}}</style></head><body>` + source + `</body></html>`, nil
}

func (a *desktop) toggleWorkspaceFileMarkupPreview(file *workspaceFile) {
	if file == nil || file.Editor == nil || file.Editor.Buffer == nil {
		return
	}
	file.MarkupPreview = !file.MarkupPreview
	file.PreviewError = ""
	if !file.MarkupPreview {
		a.releaseFilePreview()
	}
}

func workspaceFilePreviewHTML(file *workspaceFile) (string, error) {
	if file == nil {
		return "", fmt.Errorf("文件不存在")
	}
	if file.PreviewHTML != "" {
		return file.PreviewHTML, nil
	}
	if !file.MarkupPreview || file.Editor == nil || file.Editor.Buffer == nil {
		return "", fmt.Errorf("文件没有 HTML 预览")
	}
	if file.Editor.Buffer.Len() > maxMarkupPreviewBytes {
		return "", fmt.Errorf("HTML 预览超过 2 MB 限制")
	}
	source := file.Editor.Buffer.String()
	if ext := strings.ToLower(filepath.Ext(file.Path)); ext == ".html" || ext == ".htm" {
		return htmlFilePreview(source)
	}
	return "", fmt.Errorf("此文件类型没有 WebView 预览")
}

func markdownPreviewSource(box ui.Element, file *workspaceFile) string {
	if file.Editor == nil || file.Editor.Buffer == nil {
		return ""
	}
	type sourceCache struct {
		// buffer identifies which file the text came from: two files opened in
		// turn share this element and can both sit at version 1, so the version
		// alone would keep showing the previous file's text.
		buffer  *editor.Buffer
		version uint64
		text    string
	}
	// The view runs every frame; the buffer only changes while the editor shows.
	cache := ui.Local(box, "markdown-source", func() sourceCache { return sourceCache{} })
	if cache == nil {
		return ""
	}
	buffer := file.Editor.Buffer
	if version := buffer.Version(); cache.buffer != buffer || cache.version != version {
		cache.buffer, cache.version, cache.text = buffer, version, buffer.String()
	}
	return cache.text
}

// A markdown preview draws in the window: no web view, no HTML round trip, so
// it keeps the app theme and works where WebView2 is missing.
func (a *desktop) markdownFilePreviewView(c *ui.Context, file *workspaceFile) {
	scroll := ui.Scroll(c.Key("file-preview-markdown")).Grow(1).MinHeight(0).FillWidth()
	source := markdownPreviewSource(scroll, file)
	if len(source) > maxMarkupPreviewBytes {
		ui.Box(c.Key("file-preview-markdown-limit")).Grow(1).MinHeight(0).Padding(padPage).Center().Children(func() {
			fieldNote(c, "Markdown 预览超过 2 MB 限制，可用“系统打开”查看全文。", a.secondaryInk())
		})
		return
	}
	if file.MD == nil {
		file.MD = a.readingMarkdownOptions("file-preview:" + file.Path)
	}
	a.configureReadingMarkdown(file.MD)
	scroll.Children(func() {
		ui.Column(c).FillWidth().Padding(padPage).Children(func() {
			markdown.View(c.Key("file-preview-markdown-body"), source, file.MD).FillWidth().MinWidth(0)
		})
	})
}

type pendingWorkspaceFileLoad struct {
	workspace string
	path      string
	cancel    context.CancelFunc
}

type mountedWorkspacePreview struct {
	key     string
	surface *nativePreview
}

type scheduledWorkspacePreview struct {
	key   string
	timer *time.Timer
}

// workspacePreviewLifecycle is the sole owner of file-preview resources.
// Resource pairs live together so a path/key cannot drift from its canceler or
// native surface. All methods run on the UI thread except timer callbacks, which
// publish their state changes through Window.Update.
type workspacePreviewLifecycle struct {
	pending   *pendingWorkspaceFileLoad
	mounted   *mountedWorkspacePreview
	scheduled *scheduledWorkspacePreview
}

func (l *workspacePreviewLifecycle) beginLoad(workspace, path string) (context.Context, *pendingWorkspaceFileLoad) {
	l.release()
	ctx, cancel := context.WithCancel(context.Background())
	load := &pendingWorkspaceFileLoad{
		workspace: workspaceKey(workspace),
		path:      normalizeExplorerPath(path),
		cancel:    cancel,
	}
	l.pending = load
	return ctx, load
}

func (l *workspacePreviewLifecycle) finishLoad(load *pendingWorkspaceFileLoad) bool {
	if load == nil || l.pending != load {
		return false
	}
	l.pending = nil
	load.cancel()
	return true
}

func (l *workspacePreviewLifecycle) cancelLoad(workspace string) {
	if l.pending == nil || workspace != "" && l.pending.workspace != workspaceKey(workspace) {
		return
	}
	load := l.pending
	l.pending = nil
	load.cancel()
}

func (l *workspacePreviewLifecycle) loadingWorkspace(workspace string) bool {
	return l.pending != nil && l.pending.workspace == workspaceKey(workspace)
}

func (l *workspacePreviewLifecycle) schedule(
	update func(func()),
	key string,
	visible func() bool,
	create func() (*nativePreview, error),
	fail func(error),
) {
	if l.scheduled != nil {
		if l.scheduled.key == key {
			return
		}
		l.cancelScheduled()
	}
	request := &scheduledWorkspacePreview{key: key}
	l.scheduled = request
	request.timer = time.AfterFunc(16*time.Millisecond, func() {
		update(func() {
			if l.scheduled != request {
				return
			}
			l.scheduled = nil
			if !visible() {
				return
			}
			l.releaseMounted()
			surface, err := create()
			if err != nil {
				fail(err)
				return
			}
			if surface == nil {
				fail(errors.New("预览未创建 WebView"))
				return
			}
			l.mounted = &mountedWorkspacePreview{key: request.key, surface: surface}
		})
	})
}

func (l *workspacePreviewLifecycle) mountedFor(key string) *nativePreview {
	if l.mounted == nil || l.mounted.key != key || l.mounted.surface == nil || l.mounted.surface.IsDestroyed() {
		return nil
	}
	return l.mounted.surface
}

func (l *workspacePreviewLifecycle) loadHTML(key, source string) {
	if surface := l.mountedFor(key); surface != nil {
		surface.LoadHTML(source)
	}
}

func (l *workspacePreviewLifecycle) releaseMounted() {
	if l.mounted == nil {
		return
	}
	if l.mounted.surface != nil && !l.mounted.surface.IsDestroyed() {
		l.mounted.surface.Destroy()
	}
	l.mounted = nil
}

func (l *workspacePreviewLifecycle) cancelScheduled() {
	if l.scheduled == nil {
		return
	}
	request := l.scheduled
	l.scheduled = nil
	if request.timer != nil {
		request.timer.Stop()
	}
}

func (l *workspacePreviewLifecycle) releaseMountedWorkspace(workspace string) {
	if l.mounted != nil && strings.HasPrefix(l.mounted.key, workspaceKey(workspace)+"\x00") {
		l.releaseMounted()
	}
}

func (l *workspacePreviewLifecycle) cancelScheduledWorkspace(workspace string) {
	if l.scheduled == nil || !strings.HasPrefix(l.scheduled.key, workspaceKey(workspace)+"\x00") {
		return
	}
	l.cancelScheduled()
}

func (l *workspacePreviewLifecycle) releaseWorkspace(workspace string) {
	key := workspaceKey(workspace)
	if l.pending != nil && l.pending.workspace == key {
		l.cancelLoad(key)
	}
	l.cancelScheduledWorkspace(key)
	l.releaseMountedWorkspace(key)
}

func (l *workspacePreviewLifecycle) release() {
	l.cancelScheduled()
	l.cancelLoad("")
	l.releaseMounted()
}

func (a *desktop) workspaceFilePreviewKey(file *workspaceFile) string {
	if file == nil {
		return ""
	}
	tab := a.current()
	if tab == nil {
		return ""
	}
	return workspaceKey(tab.Workspace) + "\x00" + normalizeExplorerPath(file.Path)
}

func (a *desktop) requestWorkspaceFilePreview(file *workspaceFile) {
	if a.win == nil || a.closing || !a.explorer || a.filesF.InfoOpen || a.page != "chat" || file == nil || a.activeFile() != file {
		return
	}
	// Markdown and Word previews draw in the window, so they never own a web view.
	switch workspaceFileKindForPath(file.Path) {
	case workspaceFileMarkdown, workspaceFileDocument:
		return
	}
	if file.PreviewPath == "" && file.PreviewHTML == "" && file.Document == nil && !file.MarkupPreview {
		return
	}
	if tab := a.current(); tab != nil && a.workspacePreview.loadingWorkspace(tab.Workspace) {
		return
	}
	key := a.workspaceFilePreviewKey(file)
	if a.workspacePreview.mountedFor(key) != nil {
		return
	}
	a.workspacePreview.schedule(a.win.Update, key,
		func() bool {
			return !a.closing && a.explorer && !a.filesF.InfoOpen && a.page == "chat" &&
				a.activeFile() == file &&
				(file.PreviewPath != "" || file.PreviewHTML != "" || file.Document != nil || file.MarkupPreview)
		},
		func() (*nativePreview, error) {
			document := file.PreviewHTML
			if file.PreviewPath == "" && document == "" {
				var err error
				document, err = workspaceFilePreviewHTML(file)
				if err != nil {
					return nil, err
				}
			}
			surface, err := a.newNativePreview()
			if err != nil {
				return nil, err
			}
			if file.PreviewPath != "" {
				err = surface.LoadFile(file.PreviewPath)
			} else {
				surface.LoadHTML(document)
			}
			if err != nil {
				surface.Destroy()
				return nil, err
			}
			file.PreviewError = ""
			return surface, nil
		},
		func(err error) { file.PreviewError = err.Error() },
	)
}
func (a *desktop) releaseFilePreview() {
	a.workspacePreview.release()
}

func (a *desktop) workspaceFilePreviewView(c *ui.Context, file *workspaceFile) {
	switch workspaceFileKindForPath(file.Path) {
	case workspaceFileMarkdown:
		a.markdownFilePreviewView(c, file)
		return
	case workspaceFileDocument:
		a.documentFilePreviewView(c, file)
		return
	}
	a.requestWorkspaceFilePreview(file)
	key := a.workspaceFilePreviewKey(file)
	if surface := a.workspacePreview.mountedFor(key); surface != nil {
		ui.WebView(c.Key("workspace-file-preview"), surface.Native()).Grow(1).MinHeight(0).Fill()
		return
	}
	ui.Box(c.Key("workspace-file-preview-loading")).Grow(1).MinHeight(0).Center().Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(gapField).Children(func() {
			ui.Spinner(c.Key("workspace-file-preview-spinner")).Size(20, 20).TextColor(c.Theme().Accent)
			ui.Text(c, "正在加载预览…").FontSize(fsSmall).TextColor(a.secondaryInk())
		})
	})
}
