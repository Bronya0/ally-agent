// SPDX-License-Identifier: GPL-3.0-only
// Package pluginhost contains the optional legacy web-plugin host. It does not
// participate in native page rendering except through the explicit Host API.
package pluginhost

import (
	"context"
	"errors"
	"reflect"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
)

type Input struct {
	Page, Workspace string
	Plugins         []backend.PluginInfo
	Theme           Theme
}

// Host owns every native view, local listener, subscription and cleanup timer.
// Public methods run on the UI thread. Notify is also delivered on that thread.
type Host struct {
	core               *backend.App
	win                *mygo.Window
	notify             func(string, string)
	pages              map[string]*page
	timer              *time.Timer
	tick               uint64
	pending            Input
	closed             bool
	retiring           *page
	failureID, failure string
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
	if p := h.pages[id]; p != nil && p.view != nil && !p.view.IsDestroyed() {
		return p.view, nil
	}
	if h.failureID == id && h.failure != "" {
		return nil, errors.New(h.failure)
	}
	return nil, nil
}
func (h *Host) Reload(id string) {
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
	p.view, err = h.win.NewWebView(mygo.WebViewOptions{URL: p.url + "/", BackgroundColor: state.Tokens["--ally-surface-content"], Page: mygo.PageOptions{PinchZoom: true}})
	if err != nil {
		_ = p.server.Close()
		return nil, err
	}
	p.view.Page().OnWillNavigate(func(e *mygo.NavigateEvent) { e.PreventDefault() })
	p.view.Page().SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
	return p, nil
}

func (p *page) closeResources() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	select {
	case p.changed <- struct{}{}:
	default:
	}
	_ = p.server.Close()
	if p.view != nil && !p.view.IsDestroyed() {
		p.view.Destroy()
	}
}
func (h *Host) destroyPage(p *page) {
	// Only one retiring realm may wait for JS cleanup, even during rapid reloads.
	if p.view == nil {
		p.closeResources()
		return
	}
	if old := h.retiring; old != nil && old != p {
		old.closeResources()
	}
	h.retiring = p
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	select {
	case p.changed <- struct{}{}:
	default:
	}
	// Do not wait for JavaScript on the UI thread. Give mount's cleanup a bounded
	// chance to run, then destroy the entire JS realm even if the plugin stalls.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if !p.view.IsDestroyed() {
			_, _ = p.view.Page().EvalContext(ctx, "window.allyDispose?.();")
		}
		_ = p.server.Close()
		h.win.Update(func() {
			p.closeResources()
			if h.retiring == p {
				h.retiring = nil
			}
		})
	}()
}
func (h *Host) Close() {
	h.closed = true
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	if p := h.retiring; p != nil {
		p.closeResources()
		h.retiring = nil
	}
	for _, p := range h.pages {
		p.closeResources()
	}
	h.pages = nil
	h.pending = Input{}
}

func (h *Host) sync(input Input) {
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
		valid := false
		for _, info := range input.Plugins {
			if info.ID == id && info.Enabled && info.Valid && info.UpdatedAt == p.info.UpdatedAt && info.Entry == p.info.Entry {
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
		next.Active = active
		p.mu.Lock()
		same := reflect.DeepEqual(p.state, next)
		p.mu.Unlock()
		if !same {
			p.setState(next)
		}
	}
	if current == nil {
		return
	}
	h.tick++
	if p := h.pages[current.ID]; p != nil {
		p.used = h.tick
		return
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
	h.failureID, h.failure = "", ""
	p, err := h.newPage(*current, workspace, state)
	if err != nil {
		h.failureID, h.failure = current.ID, "插件页面创建失败："+err.Error()
		return
	}
	p.used = h.tick
	h.pages[current.ID] = p
}
