// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"ally-dev/internal/documentpreview"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/google/uuid"
	xhtml "golang.org/x/net/html"
)

const maxMarkupPreviewBytes = 2 << 20
const maxDocumentPreviewHTML = 2 << 20

var errMarkupPreviewLimit = errors.New("预览内容超过大小上限")

type workspaceMediaOptions struct {
	Source  string `json:"source"`
	Token   string `json:"token"`
	Single  bool   `json:"single"`
	Enhance bool   `json:"enhance"`
	Start   bool   `json:"start"`
}

// Consume the one-shot playback intent for either navigation or an in-page swap.
func (a *desktop) mediaPlayerOptions(file *workspaceFile, token string) (workspaceMediaOptions, bool) {
	kind := workspaceFileKindForPath(file.Path)
	if !kind.hasMediaPlayer() {
		return workspaceMediaOptions{}, false
	}
	tab := a.current()
	if tab == nil || a.core == nil {
		return workspaceMediaOptions{}, false
	}
	source, err := a.core.GetWorkspaceMediaURL(backend.WorkspacePathRequest{Workspace: tab.Workspace, Path: file.Path})
	if err != nil || strings.TrimSpace(source) == "" {
		return workspaceMediaOptions{}, false
	}
	media := a.workspaceMedia()
	options := workspaceMediaOptions{Source: source, Token: token, Single: media.mode == mediaModeSingle, Enhance: media.enhance && kind == workspaceFileImage, Start: media.autoplay}
	// Keep the pending play intent until the new player confirms play or pause.
	media.playing = media.autoplay
	media.autoplay = false
	return options, true
}

func mediaPlayerDocument(file *workspaceFile, options workspaceMediaOptions) string {
	page := mediaPreviewHTML(filepath.Base(file.Path), options.Source, workspaceFileKindForPath(file.Path))
	jsText := func(source string) string {
		encoded, _ := json.Marshal(source)
		return string(encoded)
	}
	script := strings.NewReplacer("TOKEN", jsText(options.Token), "SINGLE", fmt.Sprint(options.Single), "ENHANCE", fmt.Sprint(options.Enhance), "START", fmt.Sprint(options.Start),
		"ERR_NOT_IMAGE", jsText(locales.Text("当前页面不是图片预览")), "ERR_IMAGE_TIMEOUT", jsText(locales.Text("图片加载超时")), "ERR_IMAGE_DECODE", jsText(locales.Text("图片无法解码"))).Replace(workspaceMediaScript)
	return strings.Replace(page, "</body>", script+"</body>", 1)
}

func mediaPreviewHTML(name, source string, kind workspaceFileKind) string {
	// Only the media origin is reachable: the page reads nothing else.
	mediaSource := "'none'"
	if parsed, err := url.Parse(source); err == nil && parsed.Host != "" {
		mediaSource = parsed.Scheme + "://" + parsed.Host
	}
	// Zero-minimum grid tracks keep intrinsic media dimensions from expanding
	// the viewport; object-fit scales the full frame inside that bounded area.
	style := `html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#1b1b1b;color-scheme:dark}body{display:grid;grid-template-columns:minmax(0,1fr);grid-template-rows:minmax(0,1fr);place-items:center}video,img{display:block;width:100%;height:100%;min-width:0;min-height:0;object-fit:contain}svg{position:absolute;width:0;height:0}`
	player := `<video controls playsinline preload="metadata" src="` + html.EscapeString(source) + `"></video>`
	if kind == workspaceFileImage {
		player = `<img alt="" src="` + html.EscapeString(source) + `">`
	}
	if kind == workspaceFileAudio {
		style = `html,body{margin:0;height:100%;background:#1b1b1b;color-scheme:dark}body{display:grid;place-items:center;padding:0 24px;box-sizing:border-box}audio{width:100%;max-width:560px}`
		player = `<audio controls preload="metadata" src="` + html.EscapeString(source) + `"></audio>`
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; media-src ` + mediaSource + `; img-src ` + mediaSource + `; script-src 'unsafe-inline'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(name) + `</title><style>` + style + `</style></head><body><svg xmlns="http://www.w3.org/2000/svg"><filter id="sharpen" color-interpolation-filters="sRGB"><feConvolveMatrix order="3" kernelMatrix="0 -.12 0 -.12 1.48 -.12 0 -.12 0" preserveAlpha="true"/></filter></svg>` + player + `</body></html>`
}

func emptyFileEditor() *editor.State {
	return &editor.State{Buffer: editor.NewBuffer(""), Font: ui.Font{Family: nativeFontFamily(), Size: fsEditor}}
}

func documentPreviewNote(doc *documentpreview.Document) string {
	switch {
	case doc == nil || len(doc.Paragraphs) == 0:
		return locales.Text("没有可预览的正文，可用“系统打开”查看完整文档。")
	case doc.Truncated:
		return locales.Text("预览内容已截断，可用“系统打开”查看完整文档。")
	default:
		return locales.Text("正文提取预览，不包含图片、页眉页脚和原版分页。")
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
				emptyState(c, locales.Text("没有可预览的正文"), locales.Text("可用“系统打开”查看完整文档。"), a.secondaryInk())
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
		return "", fmt.Errorf("%s", locales.Text("HTML 预览超过 2 MB 限制"))
	}
	// Parse before injecting policy: text search can mistake a comment or a
	// script before <head> for the document head and let active content run first.
	document, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return "", fmt.Errorf(locales.Text("解析 HTML 预览失败：%w"), err)
	}
	head := findHTMLHead(document)
	if head == nil {
		return "", fmt.Errorf("%s", locales.Text("HTML 预览缺少 head 节点"))
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
	// The app's dark appearance reaches the web view as prefers-color-scheme and
	// a dark canvas, which repaints a document written for a browser: one that
	// sets only its text colour ends up dark-on-dark. Hold the preview to the
	// light scheme a browser gives it, unless the page asks for dark alone.
	if !documentWantsDarkOnly(document) {
		pinLightColorScheme(document)
	}
	rendered := limitedPreviewBuffer{limit: maxDocumentPreviewHTML}
	if err := xhtml.Render(&rendered, document); err != nil {
		if errors.Is(err, errMarkupPreviewLimit) {
			return "", fmt.Errorf("%s", locales.Text("HTML 预览结果超过 2 MB 限制"))
		}
		return "", fmt.Errorf(locales.Text("序列化 HTML 预览失败：%w"), err)
	}
	return rendered.String(), nil
}

func findHTMLRoot(document *xhtml.Node) *xhtml.Node {
	for node := document.FirstChild; node != nil; node = node.NextSibling {
		if node.Type == xhtml.ElementNode && node.Data == "html" && node.Namespace == "" {
			return node
		}
	}
	return nil
}

func findHTMLHead(document *xhtml.Node) *xhtml.Node {
	root := findHTMLRoot(document)
	if root == nil {
		return nil
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode && child.Data == "head" && child.Namespace == "" {
			return child
		}
	}
	return nil
}

// pinLightColorScheme keeps the page on the light scheme: an inline style
// outranks its own color-scheme rules, so the canvas and the default text colour
// stay those of a browser in light appearance.
func pinLightColorScheme(document *xhtml.Node) {
	root := findHTMLRoot(document)
	if root == nil {
		return
	}
	const scheme = "color-scheme:light"
	for i := range root.Attr {
		if root.Attr[i].Key != "style" || root.Attr[i].Namespace != "" {
			continue
		}
		if existing := strings.TrimSpace(root.Attr[i].Val); existing != "" {
			root.Attr[i].Val = strings.TrimSuffix(existing, ";") + ";" + scheme
		} else {
			root.Attr[i].Val = scheme
		}
		return
	}
	root.Attr = append(root.Attr, xhtml.Attribute{Key: "style", Val: scheme})
}

// documentWantsDarkOnly reports whether the page declares the dark scheme and
// nothing else: it paints its own dark canvas and keeps it.
func documentWantsDarkOnly(document *xhtml.Node) bool {
	head := findHTMLHead(document)
	if head == nil {
		return false
	}
	dark, light := false, false
	for node := head.FirstChild; node != nil; node = node.NextSibling {
		if node.Type != xhtml.ElementNode || node.Data != "meta" {
			continue
		}
		name, content := "", ""
		for _, attr := range node.Attr {
			switch attr.Key {
			case "name":
				name = strings.ToLower(strings.TrimSpace(attr.Val))
			case "content":
				content = strings.ToLower(attr.Val)
			}
		}
		if name != "color-scheme" {
			continue
		}
		for _, token := range strings.FieldsFunc(content, func(r rune) bool { return r == ' ' || r == ',' }) {
			switch token {
			case "dark":
				dark = true
			case "light":
				light = true
			}
		}
	}
	return dark && !light
}

func svgPreviewHTML(name, source string) (string, error) {
	if len(source) > maxMarkupPreviewBytes {
		return "", fmt.Errorf("%s", locales.Text("SVG 预览超过 2 MB 限制"))
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
		return "", fmt.Errorf("%s", locales.Text("文件不存在"))
	}
	if file.PreviewHTML != "" {
		return file.PreviewHTML, nil
	}
	if !file.MarkupPreview || file.Editor == nil || file.Editor.Buffer == nil {
		return "", fmt.Errorf("%s", locales.Text("文件没有 HTML 预览"))
	}
	if file.Editor.Buffer.Len() > maxMarkupPreviewBytes {
		return "", fmt.Errorf("%s", locales.Text("HTML 预览超过 2 MB 限制"))
	}
	source := file.Editor.Buffer.String()
	if ext := strings.ToLower(filepath.Ext(file.Path)); ext == ".html" || ext == ".htm" {
		return htmlFilePreview(source)
	}
	return "", fmt.Errorf("%s", locales.Text("此文件类型没有 WebView 预览"))
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
			fieldNote(c, locales.Text("Markdown 预览超过 2 MB 限制，可用“系统打开”查看全文。"), a.secondaryInk())
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

type workspacePreviewSurface interface {
	Native() ui.NativeWebView
	IsDestroyed() bool
	Destroy()
	Reset()
	LoadHTML(string)
	LoadFile(string) error
}

type mountedWorkspacePreview struct {
	key            string
	surface        workspacePreviewSurface
	autoplay       bool
	stale          bool
	retainingImage bool
}

type scheduledWorkspacePreview struct {
	key      string
	autoplay bool
	timer    *time.Timer
}

// workspacePreviewLifecycle is the sole owner of file-preview resources.
// Resource pairs live together so a path/key cannot drift from its canceler or
// native surface. All methods run on the UI thread except timer callbacks, which
// publish their state changes through Window.Update.
type workspacePreviewLifecycle struct {
	pending   *pendingWorkspaceFileLoad
	mounted   map[string]*mountedWorkspacePreview
	scheduled *scheduledWorkspacePreview
}

func (l *workspacePreviewLifecycle) beginLoad(workspace, path string) (context.Context, *pendingWorkspaceFileLoad) {
	l.cancelScheduled()
	l.cancelLoad("")
	l.suspendMounted(workspace, path)
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
	autoplay bool,
	visible func() bool,
	create func() (workspacePreviewSurface, error),
	load func(workspacePreviewSurface) error,
	fail func(error),
) {
	if l.scheduled != nil {
		if l.scheduled.key == key && l.scheduled.autoplay == autoplay {
			return
		}
		l.cancelScheduled()
	}
	request := &scheduledWorkspacePreview{key: key, autoplay: autoplay}
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
			if err := l.mount(request.key, request.autoplay, create, load); err != nil {
				fail(err)
			}
		})
	})
}

// Each open workspace owns one surface. Changing the visible workspace does
// not retire another workspace's page or playback position.
func (l *workspacePreviewLifecycle) mount(key string, autoplay bool, create func() (workspacePreviewSurface, error), load func(workspacePreviewSurface) error) error {
	workspace, _, _ := strings.Cut(key, "\x00")
	mounted := l.mounted[workspace]
	if l.mountedFor(key) != nil && mounted.autoplay == autoplay {
		return nil
	}
	if mounted != nil && (mounted.autoplay != autoplay || mounted.surface == nil || mounted.surface.IsDestroyed()) {
		l.releaseMountedWorkspace(workspace)
		mounted = nil
	}
	var surface workspacePreviewSurface
	if mounted != nil {
		surface = mounted.surface
		if !mounted.stale && l.retainedImageFor(key) == nil {
			surface.Reset()
		}
	} else {
		var err error
		surface, err = create()
		if err != nil {
			if surface != nil {
				surface.Destroy()
			}
			return err
		}
	}
	if surface == nil {
		return errors.New(locales.Text("预览未创建 WebView"))
	}
	if err := load(surface); err != nil {
		surface.Destroy()
		delete(l.mounted, workspace)
		return err
	}
	if l.mounted == nil {
		l.mounted = make(map[string]*mountedWorkspacePreview)
	}
	l.mounted[workspace] = &mountedWorkspacePreview{key: key, surface: surface, autoplay: autoplay}
	return nil
}

func (l *workspacePreviewLifecycle) mountedEntryFor(key string) *mountedWorkspacePreview {
	workspace, _, _ := strings.Cut(key, "\x00")
	mounted := l.mounted[workspace]
	if mounted == nil || mounted.stale || mounted.key != key {
		return nil
	}
	return mounted
}

func (l *workspacePreviewLifecycle) mountedFor(key string) workspacePreviewSurface {
	mounted := l.mountedEntryFor(key)
	if mounted == nil || mounted.surface == nil || mounted.surface.IsDestroyed() {
		return nil
	}
	return mounted.surface
}

// Keep the committed image visible while its replacement is loaded. This is
// only a drawing fallback: mountedFor still rejects stale execution targets.
func (l *workspacePreviewLifecycle) retainedImageFor(key string) workspacePreviewSurface {
	workspace, path, _ := strings.Cut(key, "\x00")
	mounted := l.mounted[workspace]
	if mounted == nil || mounted.stale && !mounted.retainingImage || mounted.surface == nil || mounted.surface.IsDestroyed() {
		return nil
	}
	_, previous, _ := strings.Cut(mounted.key, "\x00")
	if workspaceFileKindForPath(path) != workspaceFileImage || workspaceFileKindForPath(previous) != workspaceFileImage {
		return nil
	}
	return mounted.surface
}

func (l *workspacePreviewLifecycle) loadHTML(key, source string) {
	if surface := l.mountedFor(key); surface != nil {
		surface.LoadHTML(source)
	}
}

func (l *workspacePreviewLifecycle) releaseMounted() {
	for workspace := range l.mounted {
		l.releaseMountedWorkspace(workspace)
	}
	l.mounted = nil
}

// Images retain their last frame; other replacements stop the old decoder.
func (l *workspacePreviewLifecycle) suspendMounted(workspace, path string) {
	mounted := l.mounted[workspaceKey(workspace)]
	if mounted == nil {
		return
	}
	retain := l.retainedImageFor(workspaceKey(workspace)+"\x00"+path) != nil
	if mounted.stale && (!mounted.retainingImage || retain) {
		return
	}
	mounted.stale, mounted.retainingImage = true, retain
	if !retain && mounted.surface != nil && !mounted.surface.IsDestroyed() {
		mounted.surface.Reset()
	}
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
	key := workspaceKey(workspace)
	mounted := l.mounted[key]
	if mounted == nil {
		return
	}
	if mounted.surface != nil && !mounted.surface.IsDestroyed() {
		mounted.surface.Destroy()
	}
	delete(l.mounted, key)
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
	if a.win == nil || a.closing || a.mainWindowHidden || a.win.IsMinimized() || !a.explorer || a.filesF.InfoOpen || a.page != "chat" || file == nil || file.PreviewError != "" || a.activeFile() != file {
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
	autoplay := previewAutoplayAllowed(workspaceFileKindForPath(file.Path).hasMediaPlayer())
	a.workspacePreview.schedule(a.win.Update, key, autoplay,
		func() bool {
			return !a.closing && !a.mainWindowHidden && !a.win.IsMinimized() && a.explorer && !a.filesF.InfoOpen && a.page == "chat" &&
				a.activeFile() == file &&
				(file.PreviewPath != "" || file.PreviewHTML != "" || file.Document != nil || file.MarkupPreview)
		},
		func() (workspacePreviewSurface, error) {
			return a.newNativePreview(autoplay, filePreviewBackground)
		},
		func(surface workspacePreviewSurface) error {
			document := file.PreviewHTML
			if file.PreviewPath == "" && document == "" {
				var err error
				document, err = workspaceFilePreviewHTML(file)
				if err != nil {
					return err
				}
			}
			switch {
			case file.PreviewPath != "":
				token := uuid.NewString()
				if options, ok := a.mediaPlayerOptions(file, token); ok {
					native, ok := surface.(*nativePreview)
					if !ok {
						return errors.New(locales.Text("预览类型不支持媒体控制"))
					}
					if workspaceFileKindForPath(file.Path) == workspaceFileImage && native.imageDocument && native.state.ready {
						native.replaceImage(options)
					} else {
						surface.LoadHTML(mediaPlayerDocument(file, options))
						native.imageDocument = workspaceFileKindForPath(file.Path) == workspaceFileImage
					}
					a.attachWorkspaceMedia(native, file, token)
				} else if err := surface.LoadFile(file.PreviewPath); err != nil {
					return err
				}
			default:
				surface.LoadHTML(document)
			}
			file.PreviewError = ""
			if native, ok := surface.(*nativePreview); ok {
				a.watchWorkspacePreviewFailure(native, file, key)
			}
			return nil
		},
		func(err error) { file.PreviewError = err.Error() },
	)
}
func (a *desktop) watchWorkspacePreviewFailure(surface *nativePreview, file *workspaceFile, key string) {
	surface.onFailure = func(err error) {
		workspace, _, _ := strings.Cut(key, "\x00")
		mounted := a.workspacePreview.mountedEntryFor(key)
		if a.closing || a.workspaceFiles[workspace] != file || mounted == nil || mounted.surface != surface {
			return
		}
		file.PreviewError = err.Error()
		media := a.mediaForWorkspace(workspace)
		media.playing, media.autoplay = false, false
	}
}

// Explicitly closing a preview releases only the current workspace's surface.
func (a *desktop) releaseFilePreview() {
	if tab := a.current(); tab != nil {
		a.workspacePreview.releaseWorkspace(tab.Workspace)
		delete(a.mediaPlayback, workspaceKey(tab.Workspace))
	}
}

func (a *desktop) releaseAllFilePreviews() {
	a.workspacePreview.release()
	a.mediaPlayback = nil
}

// Hidden pages keep their native surface and document. Only pending creation
// is canceled; MyGo stops placing the view while its workspace is not visible.
func (a *desktop) hideFilePreview() {
	a.workspacePreview.cancelScheduled()
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
	surface := a.workspacePreview.mountedFor(key)
	if surface == nil {
		surface = a.workspacePreview.retainedImageFor(key)
	}
	if surface != nil {
		ui.WebView(c.Key("workspace-file-preview"), surface.Native()).Grow(1).MinHeight(0).Fill()
		return
	}
	ui.Box(c.Key("workspace-file-preview-loading")).Grow(1).MinHeight(0).Center().Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(gapField).Children(func() {
			ui.Spinner(c.Key("workspace-file-preview-spinner")).Size(20, 20).TextColor(c.Theme().Accent)
			ui.Text(c, locales.Text("正在加载预览…")).FontSize(fsSmall).TextColor(a.secondaryInk())
		})
	})
}
