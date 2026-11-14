// SPDX-License-Identifier: GPL-3.0-only
package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
)

type fakePluginSurface struct {
	eval                   func(context.Context, string) (any, error)
	fail                   func(*mygo.LoadError)
	gone                   func(string)
	destroyed              bool
	destroys, unsubscribed int
}

func (s *fakePluginSurface) EvalContext(ctx context.Context, code string) (any, error) {
	if s.eval != nil {
		return s.eval(ctx, code)
	}
	return "complete", nil
}
func (s *fakePluginSurface) OnDidFailLoad(fn func(*mygo.LoadError)) func() {
	s.fail = fn
	return func() { s.unsubscribed++; s.fail = nil }
}
func (s *fakePluginSurface) OnRenderProcessGone(fn func(string)) func() {
	s.gone = fn
	return func() { s.unsubscribed++; s.gone = nil }
}
func (s *fakePluginSurface) IsDestroyed() bool { return s.destroyed }
func (s *fakePluginSurface) Destroy()          { s.destroyed = true; s.destroys++ }

func queuedPluginHost(t *testing.T) (*Host, backend.PluginInfo, string, chan func()) {
	t.Helper()
	h, info, workspace := pluginTestHost(t)
	queue := make(chan func(), 64)
	h.update = func(fn func()) { queue <- fn }
	t.Cleanup(h.Close)
	return h, info, workspace, queue
}
func pumpPluginUI(t *testing.T, queue chan func(), done func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for !done() {
		select {
		case fn := <-queue:
			fn()
		case <-deadline.C:
			t.Fatal("UI callback did not arrive")
		}
	}
}
func fakeServerPage(t *testing.T, h *Host, info backend.PluginInfo, workspace string, surface *fakePluginSurface) *page {
	t.Helper()
	p, err := h.newServer(info, workspace, Theme{})
	if err != nil {
		t.Fatal(err)
	}
	p.surface = surface
	return p
}

func TestPluginFailureRecoveryWithoutWindow(t *testing.T) {
	for _, name := range []string{"load", "renderer", "late-create", "destroyed"} {
		t.Run(name, func(t *testing.T) {
			h, info, workspace, queue := queuedPluginHost(t)
			surface := &fakePluginSurface{}
			if name == "late-create" {
				surface.eval = func(ctx context.Context, code string) (any, error) {
					if code == "document.readyState" {
						deadline, bounded := ctx.Deadline()
						if !bounded || time.Until(deadline) > 5*time.Second {
							return nil, errors.New("unbounded probe")
						}
						return nil, context.DeadlineExceeded
					}
					return nil, nil
				}
			}
			p := fakeServerPage(t, h, info, workspace, surface)
			h.pages = map[string]*page{info.ID: p}
			h.watchPage(p)
			switch name {
			case "load":
				surface.fail(&mygo.LoadError{Description: "failed"})
			case "renderer":
				surface.gone("crashed")
			case "destroyed":
				surface.destroyed = true
				if view, err := h.Page(info.ID); view != nil || err == nil {
					t.Fatal("destroyed view returned")
				}
				h.sync(Input{Page: info.Mode, Plugins: []backend.PluginInfo{info}, Workspace: workspace})
			}
			pumpPluginUI(t, queue, func() bool { return h.failureRevision.id == info.ID && h.retiring[info.ID] == nil })
			if _, err := h.Page(info.ID); err == nil {
				t.Fatal("failure not presented")
			}
			if !surface.destroyed || surface.unsubscribed != 2 {
				t.Fatal("failure leaked view/subscriptions")
			}
			created := 0
			h.create = func(i backend.PluginInfo, w string, state Theme) (*page, error) {
				created++
				return fakeServerPage(t, h, i, w, &fakePluginSurface{}), nil
			}
			input := Input{Page: info.Mode, Plugins: []backend.PluginInfo{info}, Workspace: workspace}
			h.sync(input)
			if created != 0 {
				t.Fatal("failed page automatically retried")
			}
			h.Reload(info.ID)
			h.sync(input)
			if created != 1 || h.pages[info.ID] == nil || h.failure != "" {
				t.Fatal("reload did not rebuild")
			}
		})
	}
}

func TestPluginFailureInvalidatesOnInstallationChanges(t *testing.T) {
	for _, failure := range []string{"creation", "runtime"} {
		for _, change := range []string{"version", "entry", "reinstall"} {
			t.Run(failure+"/"+change, func(t *testing.T) {
				h, info, workspace, queue := queuedPluginHost(t)
				attempts, repaired := 0, false
				h.create = func(i backend.PluginInfo, w string, state Theme) (*page, error) {
					attempts++
					if failure == "creation" && !repaired {
						return nil, errors.New("controller creation failed")
					}
					return fakeServerPage(t, h, i, w, &fakePluginSurface{}), nil
				}
				input := Input{Page: info.Mode, Workspace: workspace, Plugins: []backend.PluginInfo{info}}
				h.sync(input)
				if failure == "runtime" {
					h.failPage(h.pages[info.ID], errors.New("renderer failed"))
					pumpPluginUI(t, queue, func() bool { return h.retiring[info.ID] == nil })
				}
				if _, err := h.Page(info.ID); err == nil || attempts != 1 {
					t.Fatal("initial failure was not cached")
				}
				// Label/theme/visibility changes must not trigger a retry of the
				// same failed installation.
				input.Plugins[0].Name = "renamed plugin"
				input.Hidden = true
				h.sync(input)
				input.Hidden = false
				h.sync(input)
				if _, err := h.Page(info.ID); err == nil || attempts != 1 {
					t.Fatal("ordinary updates retried the failed revision")
				}
				repaired = true
				input.Page, input.Hidden = "plugins", true
				switch change {
				case "version":
					input.Plugins[0].UpdatedAt += "-new-installation"
				case "entry":
					input.Plugins[0].Entry = "fixed-entry.js"
				case "reinstall":
					removed := input
					removed.Plugins = nil
					h.sync(removed)
				}
				h.sync(input)
				if _, err := h.Page(info.ID); err != nil || attempts != 1 {
					t.Fatal("installation change kept an old error or created a hidden page")
				}
				input.Page, input.Hidden = info.Mode, false
				h.sync(input)
				p := h.pages[info.ID]
				if attempts != 2 || p == nil || revisionOf(p.info) != revisionOf(input.Plugins[0]) || h.failure != "" {
					t.Fatal("repaired installation did not mount automatically")
				}
				h.sync(input)
				if attempts != 2 {
					t.Fatal("unchanged repaired page was recreated")
				}
			})
		}
	}
}

func TestPluginStaleCallbacksAndNotifyAreDiscarded(t *testing.T) {
	h, info, workspace, queue := queuedPluginHost(t)
	oldSurface := &fakePluginSurface{}
	old := fakeServerPage(t, h, info, workspace, oldSurface)
	h.pages = map[string]*page{info.ID: old}
	h.watchPage(old)
	lateFailure := oldSurface.fail
	newPage := fakeServerPage(t, h, info, workspace, &fakePluginSurface{})
	h.pages[info.ID] = newPage
	count := 0
	h.notify = func(string, string) { count++ }
	lateFailure(&mygo.LoadError{Description: "old failure"})
	(<-queue)()
	h.deliverNotify(old, "old", "info")
	if count != 0 || h.failure != "" || h.pages[info.ID] != newPage {
		t.Fatal("old instance affected successor")
	}
	h.deliverNotify(newPage, "current", "info")
	if count != 1 {
		t.Fatal("live notification lost")
	}
	old.closeResources()
	h.pages[info.ID] = old
	h.deliverNotify(old, "closed", "info")
	old.mu.Lock()
	old.closed = false
	old.retiring = true
	old.mu.Unlock()
	h.deliverNotify(old, "retiring", "info")
	if count != 1 {
		t.Fatal("closed/retiring notification delivered")
	}
	h.pages[info.ID] = newPage
}

func TestPluginHiddenWindowDoesNotActLikeNavigation(t *testing.T) {
	for _, keepAlive := range []bool{false, true} {
		t.Run(map[bool]string{false: "destroy-on-navigation", true: "keep-alive"}[keepAlive], func(t *testing.T) {
			h, info, workspace, _ := queuedPluginHost(t)
			info.KeepAlive = keepAlive
			surface := &fakePluginSurface{}
			p := fakeServerPage(t, h, info, workspace, surface)
			h.pages = map[string]*page{info.ID: p}
			input := Input{Page: info.Mode, Workspace: workspace, Plugins: []backend.PluginInfo{info}, Hidden: true}
			h.sync(input)
			if h.pages[info.ID] != p || p.state.Active || p.retiring {
				t.Fatal("window hide discarded current plugin")
			}
			input.Hidden = false
			h.sync(input)
			if !p.state.Active {
				t.Fatal("window show did not reactivate")
			}
			input.Hidden = true
			input.Page = "chat"
			h.sync(input)
			if (h.pages[info.ID] != nil) != keepAlive {
				t.Fatal("navigation did not honor keepAlive")
			}
		})
	}
	h, info, workspace, _ := queuedPluginHost(t)
	created := 0
	h.create = func(backend.PluginInfo, string, Theme) (*page, error) {
		created++
		return nil, errors.New("unexpected create")
	}
	h.sync(Input{Page: info.Mode, Workspace: workspace, Plugins: []backend.PluginInfo{info}, Hidden: true})
	if created != 0 {
		t.Fatal("hidden window created plugin")
	}
}

func TestPluginRetirementAllowsOnlyBoundedCleanupAndSerializesReload(t *testing.T) {
	h, info, workspace, queue := queuedPluginHost(t)
	finish := make(chan struct{})
	surface := &fakePluginSurface{eval: func(ctx context.Context, code string) (any, error) {
		select {
		case <-finish:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	p := fakeServerPage(t, h, info, workspace, surface)
	h.pages = map[string]*page{info.ID: p}
	h.Reload(info.ID)
	if p.ctx.Err() == nil || p.closed || !p.retiring {
		t.Fatal("retirement did not immediately cancel lifecycle")
	}
	for _, method := range []string{"PluginHTTPRequest", "notify", "ReadWorkspaceFileAt"} {
		if code, body := pluginTestRPC(t, p, method, map[string]string{"url": "https://example.com"}); code != 410 {
			t.Fatal(method, code, body)
		}
	}
	if code, body := pluginTestRPC(t, p, "PluginStoreSet", info.ID, `{"saved":true}`); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := pluginTestRPC(t, p, "PluginStoreGet", info.ID); code != 200 || !strings.Contains(body, "saved") {
		t.Fatal(code, body)
	}
	if code, body := pluginTestRPC(t, p, "LogFrontendError", "cleanup log"); code != 200 {
		t.Fatal(code, body)
	}
	for i := 0; i < 5; i++ {
		if code, body := pluginTestRPC(t, p, "PluginStoreGet", info.ID); code != 200 {
			t.Fatal(code, body)
		}
	}
	if code, _ := pluginTestRPC(t, p, "PluginStoreGet", info.ID); code != 410 {
		t.Fatal("unbounded cleanup", code)
	}
	created := 0
	h.create = func(i backend.PluginInfo, w string, state Theme) (*page, error) {
		created++
		return fakeServerPage(t, h, i, w, &fakePluginSurface{}), nil
	}
	input := Input{Page: info.Mode, Workspace: workspace, Plugins: []backend.PluginInfo{info}}
	h.sync(input)
	if created != 0 {
		t.Fatal("successor raced old cleanup")
	}
	close(finish)
	pumpPluginUI(t, queue, func() bool { return h.retiring[info.ID] == nil })
	h.sync(input)
	if created != 1 || !surface.destroyed {
		t.Fatal("successor not built after retirement")
	}
}

func TestPluginCloseCancelsProbesAndCleanupIdempotently(t *testing.T) {
	h, info, workspace, queue := queuedPluginHost(t)
	entered := make(chan struct{}, 2)
	canceled := make(chan struct{}, 2)
	surface := &fakePluginSurface{eval: func(ctx context.Context, code string) (any, error) {
		entered <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return nil, ctx.Err()
	}}
	p := fakeServerPage(t, h, info, workspace, surface)
	h.pages = map[string]*page{info.ID: p}
	h.watchPage(p)
	<-entered
	h.Reload(info.ID)
	<-entered
	h.Close()
	h.Close()
	for i := 0; i < 2; i++ {
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("close left evaluation alive")
		}
	}
	if surface.destroys != 1 || surface.unsubscribed != 2 {
		t.Fatal("close not idempotent")
	}
	pumpPluginUI(t, queue, func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.closed })
}
