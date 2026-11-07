// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const maxLivePreviews = 4

type previewRequest struct {
	Key, Kind, Source string
	Dark              bool
}

type previewSurface interface {
	Load(previewRequest)
	Destroy()
	IsDestroyed() bool
	Native() ui.NativeWebView
	Status() (ready bool, err error)
}

type previewEntry struct {
	request previewRequest
	surface previewSurface
	err     error
}

type previewPool struct{ entries map[string]*previewEntry }

func (e *previewEntry) matches(req previewRequest) bool {
	if e == nil || e.request != req {
		return false
	}
	if e.surface == nil || !e.surface.IsDestroyed() {
		return true
	}
	_, err := e.surface.Status()
	return err != nil // Failed realms stay failed until an explicit retry.
}

// Visible requests arrive in document order. Bound the set before reconciling,
// so a fifth visible block cannot repeatedly evict/recreate another preview.
func previewTargets(requests []previewRequest) []previewRequest {
	targets := make([]previewRequest, 0, maxLivePreviews)
	for _, req := range requests {
		duplicate := false
		for _, target := range targets {
			if target.Key == req.Key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			targets = append(targets, req)
		}
		if len(targets) == maxLivePreviews {
			break
		}
	}
	return targets
}

func previewDeferred(requests []previewRequest, key string) bool {
	for _, req := range previewTargets(requests) {
		if req.Key == key {
			return false
		}
	}
	return true
}

func (p *previewPool) matches(targets []previewRequest) bool {
	if len(targets) != len(p.entries) {
		return false
	}
	for _, req := range targets {
		entry := p.entries[req.Key]
		if !entry.matches(req) {
			return false
		}
	}
	return true
}

// Runs only outside view construction, on the UI thread. Reuse retained views
// without reloading; transfer outgoing views to incoming blocks before destroying
// surplus instances. No hidden/idle pool survives when previews leave the view.
func (p *previewPool) sync(targets []previewRequest, create func() (previewSurface, error)) {
	next := make(map[string]*previewEntry, len(targets))
	wanted := make(map[string]bool, len(targets))
	for _, req := range targets {
		wanted[req.Key] = true
	}
	var spare []previewSurface
	for key, entry := range p.entries {
		if entry.surface == nil || entry.surface.IsDestroyed() {
			continue
		}
		if !wanted[key] {
			spare = append(spare, entry.surface)
		}
	}
	for _, req := range targets {
		entry := p.entries[req.Key]
		if entry.matches(req) {
			next[req.Key] = entry
			continue
		}
		var surface previewSurface
		if entry != nil && entry.surface != nil && !entry.surface.IsDestroyed() {
			surface = entry.surface
		}
		if surface == nil && len(spare) > 0 {
			surface, spare = spare[len(spare)-1], spare[:len(spare)-1]
		}
		var err error
		if surface == nil {
			surface, err = create()
		}
		if surface != nil {
			surface.Load(req)
		}
		next[req.Key] = &previewEntry{request: req, surface: surface, err: err}
	}
	for _, surface := range spare {
		surface.Destroy()
	}
	p.entries = next
}

func (p *previewPool) retry(req previewRequest) {
	entry := p.entries[req.Key]
	if entry == nil || entry.request != req {
		return
	}
	if entry.surface != nil && !entry.surface.IsDestroyed() {
		entry.err = nil
		entry.surface.Load(req)
		return
	}
	delete(p.entries, req.Key)
}

func (p *previewPool) release(prefix string) {
	for key, entry := range p.entries {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if entry.surface != nil && !entry.surface.IsDestroyed() {
			entry.surface.Destroy()
		}
		delete(p.entries, key)
	}
}

type previewLoadState struct {
	generation uint64
	ready      bool
	err        error
}

func (s *previewLoadState) begin() string {
	s.generation++
	s.ready, s.err = false, nil
	return fmt.Sprintf("ally-preview-ready:%d", s.generation)
}

func (s *previewLoadState) complete(token string) bool {
	if token != fmt.Sprintf("ally-preview-ready:%d", s.generation) || s.ready || s.err != nil {
		return false
	}
	s.ready = true
	return true
}

// Keep native failure handling testable without creating an OS window.
type previewBrowser interface {
	EvalContext(context.Context, string) (any, error)
	OnDidFailLoad(func(*mygo.LoadError)) func()
	OnRenderProcessGone(func(string)) func()
	LoadURL(string) error
	Destroy()
	IsDestroyed() bool
}

type nativePreviewBrowser struct{ *mygo.WebView }

func (b nativePreviewBrowser) EvalContext(ctx context.Context, code string) (any, error) {
	return b.Page().EvalContext(ctx, code)
}
func (b nativePreviewBrowser) OnDidFailLoad(fn func(*mygo.LoadError)) func() {
	return b.Page().OnDidFailLoad(fn)
}
func (b nativePreviewBrowser) OnRenderProcessGone(fn func(string)) func() {
	return b.Page().OnRenderProcessGone(fn)
}
func (b nativePreviewBrowser) LoadURL(raw string) error { return b.Page().LoadURL(raw) }

type nativePreview struct {
	view        *mygo.WebView
	browser     previewBrowser
	state       previewLoadState
	timer       *time.Timer
	update      func(func())
	offTitle    func()
	offMedia    func()
	offFailure  []func()
	probeCancel context.CancelFunc
	onFailure   func(error)
	documentURL string
	mediaToken  string
}

func (p *nativePreview) Native() ui.NativeWebView { return p.view }
func (p *nativePreview) IsDestroyed() bool {
	return p.browser == nil || p.browser.IsDestroyed()
}
func (p *nativePreview) Status() (bool, error) { return p.state.ready, p.state.err }
func (p *nativePreview) LoadFile(path string) error {
	p.retireContent()
	p.state.begin()
	if err := p.view.Page().LoadFile(path); err != nil {
		return err
	}
	raw := (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}).String()
	p.watchPage(raw, true)
	return nil
}
func (p *nativePreview) LoadHTML(source string) {
	p.retireContent()
	p.state.begin()
	p.view.Page().LoadHTML(source, "")
	p.watchPage("about:blank", true)
}

func (p *nativePreview) watchPage(expectedURL string, readyOnProbe bool) {
	generation, browser, update := p.state.generation, p.browser, p.update
	p.offFailure = append(p.offFailure,
		browser.OnDidFailLoad(func(err *mygo.LoadError) {
			if err == nil || err.URL != "" && err.URL != expectedURL {
				return
			}
			failure := fmt.Errorf("预览加载失败：%s", err.Description)
			update(func() { p.failContent(generation, failure, false) })
		}),
		browser.OnRenderProcessGone(func(reason string) {
			update(func() { p.failContent(generation, fmt.Errorf("预览进程已退出：%s，请重试", reason), true) })
		}),
	)
	// Windows may fail to create its controller after NewWebView returned.
	// A single bounded probe catches this; it is not a polling loop.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	p.probeCancel = cancel
	go func() {
		value, err := browser.EvalContext(ctx, "document.readyState")
		canceled := ctx.Err() == context.Canceled
		cancel()
		if canceled {
			return
		}
		update(func() {
			if p.state.generation != generation || p.IsDestroyed() || p.state.err != nil {
				return
			}
			p.probeCancel = nil
			if err != nil || value == nil {
				if err == nil {
					err = fmt.Errorf("预览健康检查无响应")
				}
				p.failContent(generation, fmt.Errorf("预览不可用：%w，请重试", err), true)
			} else if readyOnProbe {
				p.state.ready = true
			}
		})
	}()
}

func (p *nativePreview) failContent(generation uint64, err error, broken bool) {
	if p.state.generation != generation || p.IsDestroyed() || p.state.err != nil {
		return
	}
	failed := p.onFailure
	if broken {
		p.Destroy()
	} else {
		p.Reset() // Stop failed content while keeping a healthy native slot.
	}
	p.state.ready, p.state.err = false, err
	if failed != nil {
		failed(err)
	}
}
func (p *nativePreview) retireContent() {
	if p.probeCancel != nil {
		p.probeCancel()
		p.probeCancel = nil
	}
	for _, off := range p.offFailure {
		off()
	}
	p.offFailure = nil
	p.onFailure = nil
	p.mediaToken = ""
	if p.offMedia != nil {
		p.offMedia()
		p.offMedia = nil
	}
	p.state.generation++
	p.state.ready, p.state.err = false, nil
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	releasePreviewDocument(p.documentURL)
	p.documentURL = ""
}

func (p *nativePreview) Reset() {
	p.retireContent()
	if !p.IsDestroyed() {
		// LoadURL also clears MyGo's remembered LoadHTML body, and navigation
		// stops the old media, requests and decoder without rebuilding WebView.
		_ = p.browser.LoadURL("about:blank")
	}
}

func (p *nativePreview) Destroy() {
	p.retireContent()
	if p.offTitle != nil {
		p.offTitle()
		p.offTitle = nil
	}
	if p.browser != nil {
		p.browser.Destroy()
		p.browser = nil
	}
	p.view = nil
}

func (p *nativePreview) Load(req previewRequest) {
	p.retireContent()
	token := p.state.begin()
	generation := p.state.generation
	p.timer = time.AfterFunc(20*time.Second, func() {
		p.update(func() {
			if p.IsDestroyed() || p.state.generation != generation || p.state.ready {
				return
			}
			p.failContent(generation, fmt.Errorf("预览加载超时，请重试"), false)
		})
	})
	document := ""
	if req.Kind == "mermaid" {
		document = mermaidDocument(req.Source, req.Dark)
	} else {
		document = toolHTMLDocument(req.Source)
	}
	p.documentURL = storePreviewDocument(previewReadyDocument(document, token, req.Kind == "mermaid"))
	if err := p.browser.LoadURL(p.documentURL); err != nil {
		p.failContent(generation, err, false)
		return
	}
	p.watchPage(p.documentURL, false)
}

func (a *desktop) newPreviewSurface() (previewSurface, error) {
	return a.newNativePreview(false)
}

func previewAutoplayAllowed(media bool) bool {
	return media || runtime.GOOS == "windows"
}

func (a *desktop) newNativePreview(autoplay bool) (*nativePreview, error) {
	page := mygo.PageOptions{PinchZoom: true, Untrusted: true}
	// WebView2 freezes this policy at its first page, even if that page is
	// not a media preview. Other platforms only allow it for media players.
	if previewAutoplayAllowed(autoplay) {
		page.Autoplay = mygo.AutoplayAllow
	}
	view, err := a.win.NewWebView(mygo.WebViewOptions{BackgroundColor: "#1b1b1b", Page: page})
	if err != nil {
		return nil, err
	}
	view.Page().OnWillNavigate(func(e *mygo.NavigateEvent) { e.PreventDefault() })
	view.Page().SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
	p := &nativePreview{view: view, browser: nativePreviewBrowser{view}, update: a.win.Update}
	p.offTitle = view.Page().OnPageTitleUpdated(func(e *mygo.TitleEvent) {
		e.PreventDefault()
		token := e.Title
		p.update(func() {
			if !p.IsDestroyed() && p.state.complete(token) && p.timer != nil {
				p.timer.Stop()
				p.timer = nil
			}
		})
	})
	return p, nil
}

func (a *desktop) schedulePreviewSync() {
	if a.win == nil || a.closing || a.mainWindowHidden || a.win.IsMinimized() || a.previewTimer != nil {
		return
	}
	if a.previews.matches(previewTargets(a.previewRequests)) {
		return
	}
	generation, win := a.previewGeneration, a.win
	// Coalesce multi-pass builds and rapid scrolling; never create/destroy a
	// native surface during construction or use a Context from the timer.
	a.previewTimer = time.AfterFunc(120*time.Millisecond, func() {
		win.Update(func() {
			if generation != a.previewGeneration || a.closing {
				return
			}
			a.previewTimer = nil
			if a.mainWindowHidden || a.win.IsMinimized() {
				return
			}
			a.previews.sync(previewTargets(a.previewRequests), a.newPreviewSurface)
		})
	})
}

func (a *desktop) previewView(c *ui.Context, req previewRequest) {
	box := ui.Column(c.Key("preview-" + req.Key)).FillWidth().Height(420).MinWidth(0)
	// InView is the previous committed geometry. One follow-up frame lets new
	// placeholders acquire geometry without eagerly allocating offscreen views.
	initial := ui.Local(box, "measured", func() bool { return false })
	if !*initial {
		*initial = true
		c.After(16 * time.Millisecond)
	}
	if box.InView() {
		a.previewRequests = append(a.previewRequests, req)
	}
	box.Children(func() {
		entry := a.previews.entries[req.Key]
		ready := false
		var loadErr error
		if entry != nil && entry.request == req {
			loadErr = entry.err
			if entry.surface != nil {
				ready, loadErr = entry.surface.Status()
			}
			if entry.surface != nil && !entry.surface.IsDestroyed() {
				if loadErr == nil {
					// Keep the page mounted behind the opaque native cover so it
					// can lay out at its final size before becoming visible.
					ui.WebView(c.Key("surface"), entry.surface.Native()).Fill().Radius(radiusSmall).Border(1, c.Theme().Border)
				}
			}
		}
		if loadErr != nil {
			notice(c, "error", loadErr.Error(), a.secondaryInk())
			flat(c, "重试预览").OnClick(func() { a.previews.retry(req) })
		} else if !ready {
			if box.InView() && previewDeferred(a.previewRequests, req.Key) {
				ui.Text(c, "同屏最多加载 4 个预览，滚动后自动加载").FontSize(fsSmall).TextColor(a.secondaryInk())
				return
			}
			ui.Column(c.Key("loading")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				Center().Gap(gapField).Background(c.Theme().Surface).Radius(radiusSmall).Border(1, c.Theme().Border).
				Label("正在加载图表预览").Children(func() {
				ui.Spinner(c.Key("spinner")).Size(20, 20).TextColor(c.Theme().Accent)
				ui.Text(c, "正在加载图表…").FontSize(fsSmall).TextColor(a.secondaryInk())
			})
		}
	})
}

func (a *desktop) releaseSessionHTMLPreviews(sessionID string) {
	prefix := sessionID + ":"
	a.previews.release(prefix)
	requests := a.previewRequests[:0]
	for _, req := range a.previewRequests {
		if !strings.HasPrefix(req.Key, prefix) {
			requests = append(requests, req)
		}
	}
	clear(a.previewRequests[len(requests):])
	a.previewRequests = requests
}

func (a *desktop) releaseAllHTMLPreviews() {
	a.previewGeneration++
	if a.previewTimer != nil {
		a.previewTimer.Stop()
		a.previewTimer = nil
	}
	a.previewRequests = nil
	a.previews.release("")
}
