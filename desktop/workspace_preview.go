// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ally-dev/internal/documentpreview"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	xhtml "golang.org/x/net/html"
)

const maxMarkupPreviewBytes = 2 << 20
const maxDocumentPreviewHTML = 2 << 20

var errMarkupPreviewLimit = errors.New("预览内容超过大小上限")

var markdownImageTag = regexp.MustCompile(`(?i)<img\b[^>]*>`)

func emptyFileEditor() *editor.State {
	return &editor.State{Buffer: editor.NewBuffer(""), Font: ui.Font{Family: "Inter", Size: fsEditor}}
}

func documentPreviewHTML(name string, doc *documentpreview.Document, sheetName string) string {
	var body strings.Builder
	if doc.Truncated {
		body.WriteString(`<p class="note">预览内容已截断，可用系统应用查看完整文档。</p>`)
	}
	if len(doc.Sheets) > 0 {
		body.WriteString(`<p class="note">表格仅显示已保存的单元格结果，不含图表与复杂格式。</p>`)
		var sheet *documentpreview.Sheet
		for i := range doc.Sheets {
			if doc.Sheets[i].Name == sheetName {
				sheet = &doc.Sheets[i]
				break
			}
		}
		if sheet == nil {
			sheet = &doc.Sheets[0]
		}
		body.WriteString(`<table><thead><tr><th>行</th>`)
		for column := 0; column < sheet.Columns; column++ {
			fmt.Fprintf(&body, `<th>%s</th>`, documentpreview.ColumnName(column))
		}
		body.WriteString(`</tr></thead><tbody>`)
		truncated := false
		for _, row := range sheet.Rows {
			body.WriteString(`<tr>`)
			fmt.Fprintf(&body, `<th scope="row">%d</th>`, row.Number)
			for column := 0; column < sheet.Columns; column++ {
				value := ""
				if column < len(row.Values) {
					value = row.Values[column]
				}
				writeDocumentCell(&body, value)
			}
			body.WriteString(`</tr>`)
			if body.Len() >= maxDocumentPreviewHTML {
				truncated = true
				break
			}
		}
		body.WriteString(`</tbody></table>`)
		if truncated {
			body.WriteString(`<p class="note">表格预览达到大小上限，后续内容未显示。</p>`)
		}
		return previewHTML(filepath.Base(name), body.String())
	}

	if len(doc.Paragraphs) == 0 {
		body.WriteString(`<p class="note">没有可预览的正文，可用系统应用查看完整文档。</p>`)
	} else {
		body.WriteString(`<p class="note">正文提取预览，不包含图片、页眉页脚和原版分页。</p>`)
	}
	truncated := false
	for _, paragraph := range doc.Paragraphs {
		level := paragraph.Heading
		if level > 0 {
			if level > 6 {
				level = 6
			}
			fmt.Fprintf(&body, `<h%d>`, level)
			writeDocumentCell(&body, paragraph.Text)
			fmt.Fprintf(&body, `</h%d>`, level)
		} else {
			body.WriteString(`<p>`)
			writeDocumentCell(&body, paragraph.Text)
			body.WriteString(`</p>`)
		}
		if body.Len() >= maxDocumentPreviewHTML {
			truncated = true
			break
		}
	}
	if truncated {
		body.WriteString(`<p class="note">文档预览达到大小上限，后续内容未显示。</p>`)
	}
	return previewHTML(filepath.Base(name), body.String())
}

func writeDocumentCell(body *strings.Builder, value string) {
	if len(value) > 16<<10 {
		value = value[:16<<10]
	}
	body.WriteString(html.EscapeString(value))
}

func previewHTML(title, body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:; connect-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + `</title><style>
:root{color-scheme:light dark;font:15px/1.65 system-ui,"Segoe UI",sans-serif}body{margin:0 auto;padding:24px;max-width:1000px;color:#242424;background:#fff}h1{font-size:1.35rem;margin:0 0 1.5rem}h1,h2,h3{line-height:1.3}p{white-space:pre-wrap}table{border-collapse:collapse;width:max-content;max-width:100%;font-size:13px}th,td{border:1px solid #bbb;padding:5px 9px;text-align:left;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere}th{background:#f1f1f1;position:sticky;top:0}.note{color:#777;font-size:.9rem}@media(prefers-color-scheme:dark){body{color:#dedede;background:#202020}th,td{border-color:#555}th{background:#333}.note{color:#aaa}}
</style></head><body><h1>` + html.EscapeString(title) + `</h1>` + body + `</body></html>`
}

func markdownPreviewHTML(source string) (string, error) {
	if len(source) > maxMarkupPreviewBytes {
		return "", fmt.Errorf("Markdown 预览超过 2 MB 限制")
	}
	body := limitedPreviewBuffer{limit: maxDocumentPreviewHTML}
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err := parser.Convert([]byte(source), &body); err != nil {
		if errors.Is(err, errMarkupPreviewLimit) {
			return "", fmt.Errorf("Markdown 渲染结果超过 2 MB 限制")
		}
		return "", err
	}
	content := markdownImageTag.ReplaceAllString(body.String(), `<span class="note">[图片未加载]</span>`)
	return previewHTML("Markdown 预览", content), nil
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
	if file.Document != nil {
		return documentPreviewHTML(file.Path, file.Document, file.DocumentSheet), nil
	}
	if !file.MarkupPreview || file.Editor == nil || file.Editor.Buffer == nil {
		return "", fmt.Errorf("文件没有 HTML 预览")
	}
	if file.Editor.Buffer.Len() > maxMarkupPreviewBytes {
		return "", fmt.Errorf("HTML/Markdown 预览超过 2 MB 限制")
	}
	source := file.Editor.Buffer.String()
	switch strings.ToLower(filepath.Ext(file.Path)) {
	case ".md", ".markdown":
		return markdownPreviewHTML(source)
	case ".html", ".htm":
		return htmlFilePreview(source)
	default:
		return "", fmt.Errorf("此文件类型没有 WebView 预览")
	}
}

func (a *desktop) selectWorkspaceDocumentSheet(file *workspaceFile, sheet string) {
	if file == nil || file.Document == nil || file.DocumentSheet == sheet {
		return
	}
	file.DocumentSheet = sheet
	a.workspacePreview.loadHTML(a.workspaceFilePreviewKey(file), documentPreviewHTML(file.Path, file.Document, sheet))
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
