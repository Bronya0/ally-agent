// SPDX-License-Identifier: GPL-3.0-only
// Package pluginhost contains the optional legacy web-plugin host. It does not
// participate in native page rendering except through the explicit Host API.
package pluginhost

import (
	"ally-dev/locales"
	"context"
	"errors"
	"reflect"
	"runtime"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
)

type Input struct {
	Page, Workspace string
	Plugins         []backend.PluginInfo
	Theme           Theme
	Hidden          bool
}

type pluginRevision struct{ id, updatedAt, entry string }

func revisionOf(info backend.PluginInfo) pluginRevision {
	return pluginRevision{info.ID, info.UpdatedAt, info.Entry}
}

// Host owns every native view, local listener, subscription and cleanup timer.
// Public methods run on the UI thread. Notify is also delivered on that thread.
type Host struct {
	core            *backend.App
	win             *mygo.Window
	notify          func(string, string)
	pages           map[string]*page
	timer           *time.Timer
	tick            uint64
	pending         Input
	closed          bool
	retiring        map[string]*page
	update          func(func()) // UI dispatcher seam for window-free tests.
	create          func(backend.PluginInfo, string, Theme) (*page, error)
	failureRevision pluginRevision
	failure         string
}

func New(core *backend.App, win *mygo.Window, notify func(string, string)) *Host {
	if notify == nil {
		notify = func(string, string) {}
	}
	return &Host{core: core, win: win, notify: notify}
}

// Update coalesces multi-pass builds; native views are reconciled after rendering.
func (h *Host) Update(input Input) {
	if h.closed || reflect.DeepEqual(h.pending, input) {
		return
	}
	if input.Plugins != nil {
		input.Plugins = append(make([]backend.PluginInfo, 0, len(input.Plugins)), input.Plugins...)
	}
	h.pending = input
	h.schedule()
}
func (h *Host) post(fn func()) {
	if h.update != nil {
		h.update(fn)
	} else if h.win != nil {
		h.win.Update(fn)
	}
}

func (h *Host) deliverNotify(p *page, value, kind string) {
	p.mu.Lock()
	live := !p.closed && !p.retiring
	p.mu.Unlock()
	if !h.closed && live && h.pages[p.info.ID] == p {
		h.notify(value, kind)
	}
}

func (h *Host) schedule() {
	if h.win == nil || h.closed || h.timer != nil {
		return
	}
	h.timer = time.AfterFunc(30*time.Millisecond, func() {
		h.win.Update(func() {
			h.timer = nil
			if !h.closed {
				h.sync(h.pending)
			}
		})
	})
}
func (h *Host) Page(id string) (*mygo.WebView, error) {
	if p := h.pages[id]; p != nil && p.surface != nil {
		if !p.surface.IsDestroyed() {
			return p.view, nil
		}
		h.schedule()
		return nil, errors.New(locales.Text("插件页面已失效，请重新加载"))
	}
	if h.failureRevision.id == id && h.failure != "" {
		return nil, errors.New(h.failure)
	}
	return nil, nil
}
func (h *Host) Reload(id string) {
	if h.closed {
		return
	}
	if h.failureRevision.id == id {
		h.failureRevision, h.failure = pluginRevision{}, ""
	}
	if p := h.pages[id]; p != nil {
		h.destroyPage(p)
		delete(h.pages, id)
	}
	h.schedule()
}

func (h *Host) newPage(info backend.PluginInfo, workspace string, state Theme) (*page, error) {
	p, err := h.newServer(info, workspace, state)
	if err != nil {
		return nil, err
	}
	pageOptions := mygo.PageOptions{PinchZoom: true}
	// A plugin can create the first WebView2 page. Its process-wide playback
	// policy must already allow later native media previews to continue playing.
	if runtime.GOOS == "windows" {
		pageOptions.Autoplay = mygo.AutoplayAllow
	}
	p.view, err = h.win.NewWebView(mygo.WebViewOptions{URL: p.url + "/", BackgroundColor: state.Tokens["--ally-surface-content"], Page: pageOptions})
	if err != nil {
		p.closeResources()
		return nil, err
	}
	p.surface = nativePluginSurface{p.view}
	p.off = append(p.off, p.view.Page().OnWillNavigate(func(e *mygo.NavigateEvent) { e.PreventDefault() }))
	p.view.Page().SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
	return p, nil
}

// Only this narrow adapter touches WebView APIs; lifecycle tests need no window.
type pluginSurface interface {
	EvalContext(context.Context, string) (any, error)
	OnDidFailLoad(func(*mygo.LoadError)) func()
	OnRenderProcessGone(func(string)) func()
	IsDestroyed() bool
	Destroy()
}
type nativePluginSurface struct{ *mygo.WebView }

func (s nativePluginSurface) EvalContext(ctx context.Context, code string) (any, error) {
	return s.Page().EvalContext(ctx, code)
}
func (s nativePluginSurface) OnDidFailLoad(fn func(*mygo.LoadError)) func() {
	return s.Page().OnDidFailLoad(fn)
}
func (s nativePluginSurface) OnRenderProcessGone(fn func(string)) func() {
	return s.Page().OnRenderProcessGone(fn)
}

func (p *page) stopServer() {
	p.mu.Lock()
	p.closed = true
	if p.cancel != nil {
		p.cancel()
	}
	if p.retireCancel != nil {
		p.retireCancel()
	}
	p.mu.Unlock()
	if p.server != nil {
		_ = p.server.Close()
	}
}
func (p *page) destroyView() {
	for _, off := range p.off {
		off()
	}
	p.off = nil
	if p.surface != nil && !p.surface.IsDestroyed() {
		p.surface.Destroy()
	}
}
func (p *page) closeResources() {
	p.stopServer()
	p.destroyView()
}
func (h *Host) watchPage(p *page) {
	if p.surface == nil {
		return
	}
	fail := func(err error) { h.post(func() { h.failPage(p, err) }) }
	p.off = append(p.off,
		p.surface.OnDidFailLoad(func(err *mygo.LoadError) { fail(errors.New(locales.Text("页面加载失败：") + err.Error())) }),
		p.surface.OnRenderProcessGone(func(reason string) { fail(errors.New(locales.Text("页面进程已退出：") + reason)) }),
	)
	// WebView2 can fail after NewWebView returns without a load-failure event.
	// Probe asynchronously, with a hard bound, and never mutate UI state here.
	surface, lifetime := p.surface, p.ctx
	go func() {
		ctx, cancel := context.WithTimeout(lifetime, 5*time.Second)
		defer cancel()
		value, err := surface.EvalContext(ctx, "document.readyState")
		if lifetime.Err() != nil {
			return
		}
		if err != nil {
			fail(err)
		} else if value == nil {
			fail(errors.New(locales.Text("页面健康检查无响应")))
		}
	}()
}
func (h *Host) failPage(p *page, err error) {
	p.mu.Lock()
	live := !p.closed && !p.retiring
	p.mu.Unlock()
	if h.closed || !live || h.pages[p.info.ID] != p {
		return
	}
	h.failureRevision, h.failure = revisionOf(p.info), locales.Text("插件页面失效：")+err.Error()
	h.destroyPage(p)
	delete(h.pages, p.info.ID)
}
func (h *Host) destroyPage(p *page) {
	p.mu.Lock()
	if p.closed || p.retiring {
		p.mu.Unlock()
		return
	}
	p.retiring = true
	if p.cancel != nil {
		p.cancel() // Abort ordinary RPC immediately, not after JS cleanup.
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	p.retireCancel = cancel
	p.mu.Unlock()
	if h.retiring == nil {
		h.retiring = make(map[string]*page)
	}
	// Bound native realms: only the newest retirement may wait for JavaScript.
	// Keep drained IDs tracked until their already-admitted storage calls finish.
	for _, old := range h.retiring {
		if old != p {
			old.closeResources()
		}
	}
	h.retiring[p.info.ID] = p
	surface := p.surface
	liveView := surface != nil && !surface.IsDestroyed()
	if !liveView {
		p.closeResources()
	}
	go func() {
		defer cancel()
		if liveView {
			_, _ = surface.EvalContext(ctx, "return await window.allyDispose?.();")
		}
		p.stopServer()
		h.post(func() { p.destroyView() })
		// A synchronous storage call may still be leaving the install/delete lock.
		// Destroy the realm at the deadline, but do not create its successor yet.
		p.rpcs.Wait()
		h.post(func() {
			if h.retiring[p.info.ID] == p {
				delete(h.retiring, p.info.ID)
			}
			if !h.closed {
				h.schedule()
			}
		})
	}()
}
func (h *Host) Close() {
	if h.closed {
		return
	}
	h.closed = true
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	for _, p := range h.retiring {
		p.closeResources()
	}
	h.retiring = nil
	for _, p := range h.pages {
		p.closeResources()
	}
	h.pages = nil
	h.pending = Input{}
}

func (h *Host) sync(input Input) {
	if h.closed {
		return
	}
	var current *backend.PluginInfo
	for i := range input.Plugins {
		p := &input.Plugins[i]
		if p.Mode == input.Page && p.Enabled && p.Valid {
			current = p
			break
		}
	}
	workspace, state := input.Workspace, input.Theme
	if h.pages == nil {
		h.pages = map[string]*page{}
	}
	for id, p := range h.pages {
		if p.surface != nil && p.surface.IsDestroyed() {
			h.failPage(p, errors.New(locales.Text("页面已销毁")))
			continue
		}
		valid := false
		for _, info := range input.Plugins {
			if info.Enabled && info.Valid && revisionOf(info) == revisionOf(p.info) {
				valid = true
				break
			}
		}
		active := current != nil && current.ID == id
		if !valid || workspace != p.workspace || (!active && !p.info.KeepAlive) {
			h.destroyPage(p)
			delete(h.pages, id)
			continue
		}
		next := state
		next.Active = active && !input.Hidden
		p.mu.Lock()
		same := reflect.DeepEqual(p.state, next)
		p.mu.Unlock()
		if !same {
			p.setState(next)
		}
	}
	// Installation changes invalidate failures even while another page is open.
	// Keep errors for the same revision so ordinary frames do not retry it.
	if h.failure != "" {
		present := false
		for _, info := range input.Plugins {
			if revisionOf(info) == h.failureRevision {
				present = true
				break
			}
		}
		if !present {
			h.failureRevision, h.failure = pluginRevision{}, ""
		}
	}
	if current == nil || input.Hidden || (h.failureRevision == revisionOf(*current) && h.failure != "") {
		return
	}
	h.tick++
	if p := h.pages[current.ID]; p != nil {
		p.used = h.tick
		return
	}
	if h.retiring[current.ID] != nil {
		return // Old cleanup cannot race a new instance with the same store ID.
	}
	if len(h.pages) >= maxPluginViews {
		var oldest *page
		for _, p := range h.pages {
			if oldest == nil || p.used < oldest.used {
				oldest = p
			}
		}
		h.destroyPage(oldest)
		delete(h.pages, oldest.info.ID)
	}
	state.Active = true
	h.failureRevision, h.failure = pluginRevision{}, ""
	create := h.create
	if create == nil {
		create = h.newPage
	}
	p, err := create(*current, workspace, state)
	if err != nil {
		h.failureRevision, h.failure = revisionOf(*current), locales.Text("插件页面创建失败：")+err.Error()
		return
	}
	p.used = h.tick
	h.pages[current.ID] = p
	h.watchPage(p)
}
