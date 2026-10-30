// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
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
		if entry == nil || entry.request != req || (entry.surface != nil && entry.surface.IsDestroyed()) {
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
		if entry != nil && entry.request == req && (entry.surface == nil || !entry.surface.IsDestroyed()) {
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

type nativePreview struct {
	view        *mygo.WebView
	state       previewLoadState
	timer       *time.Timer
	update      func(func())
	offTitle    func()
	documentURL string
}

func (p *nativePreview) Native() ui.NativeWebView { return p.view }
func (p *nativePreview) IsDestroyed() bool        { return p.view.IsDestroyed() }
func (p *nativePreview) Status() (bool, error)    { return p.state.ready, p.state.err }
func (p *nativePreview) Destroy() {
	p.state.generation++
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if p.offTitle != nil {
		p.offTitle()
		p.offTitle = nil
	}
	releasePreviewDocument(p.documentURL)
	p.documentURL = ""
	p.view.Destroy()
}
func (p *nativePreview) Load(req previewRequest) {
	token := p.state.begin()
	if p.timer != nil {
		p.timer.Stop()
	}
	generation := p.state.generation
	p.timer = time.AfterFunc(20*time.Second, func() {
		p.update(func() {
			if p.IsDestroyed() || p.state.generation != generation || p.state.ready {
				return
			}
			p.state.err = fmt.Errorf("预览加载超时，请重试")
			p.timer = nil
		})
	})
	document := ""
	if req.Kind == "mermaid" {
		document = mermaidDocument(req.Source, req.Dark)
	} else {
		document = toolHTMLDocument(req.Source)
	}
	releasePreviewDocument(p.documentURL)
	p.documentURL = storePreviewDocument(previewReadyDocument(document, token, req.Kind == "mermaid"))
	p.view.Page().LoadURL(p.documentURL)
}

func (a *desktop) newPreviewSurface() (previewSurface, error) {
	view, err := a.win.NewWebView(mygo.WebViewOptions{BackgroundColor: "#1b1b1b", Page: mygo.PageOptions{PinchZoom: true}})
	if err != nil {
		return nil, err
	}
	view.Page().OnWillNavigate(func(e *mygo.NavigateEvent) { e.PreventDefault() })
	view.Page().SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
	p := &nativePreview{view: view, update: a.win.Update}
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
	if a.win == nil || a.closing || a.previewTimer != nil {
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
			if entry.surface != nil && !entry.surface.IsDestroyed() {
				ready, loadErr = entry.surface.Status()
				if loadErr == nil {
					// Keep the page mounted behind the opaque native cover so it
					// can lay out at its final size before becoming visible.
					ui.WebView(c.Key("surface"), entry.surface.Native()).Fill().Radius(radiusSmall).Border(1, c.Theme().Border)
				}
			}
		}
		if loadErr != nil {
			notice(c, "error", loadErr.Error(), a.secondaryInk())
			flat(c, "重试预览").OnClick(func() {
				if entry.surface != nil && !entry.surface.IsDestroyed() {
					entry.surface.Destroy()
				}
				delete(a.previews.entries, req.Key)
			})
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
