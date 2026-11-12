// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ally-dev/desktop/pluginhost"
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
)

type fakePreviewBrowser struct {
	eval                   func(context.Context, string) (any, error)
	fail                   func(*mygo.LoadError)
	gone                   func(string)
	destroys, unsubscribed int
	url                    string
}

func (b *fakePreviewBrowser) EvalContext(ctx context.Context, code string) (any, error) {
	if b.eval != nil {
		return b.eval(ctx, code)
	}
	return "complete", nil
}
func (b *fakePreviewBrowser) OnDidFailLoad(fn func(*mygo.LoadError)) func() {
	b.fail = fn
	return func() { b.unsubscribed++; b.fail = nil }
}
func (b *fakePreviewBrowser) OnRenderProcessGone(fn func(string)) func() {
	b.gone = fn
	return func() { b.unsubscribed++; b.gone = nil }
}
func (b *fakePreviewBrowser) LoadURL(raw string) error { b.url = raw; return nil }
func (b *fakePreviewBrowser) Destroy()                 { b.destroys++ }
func (b *fakePreviewBrowser) IsDestroyed() bool        { return b.destroys != 0 }

func queuedPreview(t *testing.T, b *fakePreviewBrowser) (*nativePreview, chan func()) {
	t.Helper()
	queue := make(chan func(), 32)
	p := &nativePreview{browser: b, update: func(fn func()) { queue <- fn }}
	p.state.begin()
	t.Cleanup(p.Destroy)
	return p, queue
}
func pumpPreview(t *testing.T, queue chan func(), done func() bool) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for !done() {
		select {
		case fn := <-queue:
			fn()
		case <-timer.C:
			t.Fatal("preview update did not arrive")
		}
	}
}

func TestImageReplacementDropsLateResultsWithoutNavigation(t *testing.T) {
	type evaluation struct {
		ctx   context.Context
		reply chan error
	}
	calls := make(chan evaluation, 2)
	b := &fakePreviewBrowser{eval: func(ctx context.Context, _ string) (any, error) {
		call := evaluation{ctx: ctx, reply: make(chan error, 1)}
		calls <- call
		return true, <-call.reply
	}}
	p, queue := queuedPreview(t, b)
	p.state.ready, p.imageDocument = true, true
	nextCall := func() evaluation {
		select {
		case call := <-calls:
			return call
		case <-time.After(time.Second):
			t.Fatal("image replacement did not reach the browser")
			return evaluation{}
		}
	}
	p.replaceImage(workspaceMediaOptions{Source: "first.png", Token: "first"})
	first := nextCall()
	p.replaceImage(workspaceMediaOptions{Source: "second.png", Token: "second"})
	second := nextCall()
	if first.ctx.Err() == nil {
		t.Fatal("previous image evaluation was not canceled")
	}
	first.reply <- errors.New("late decode failure")
	second.reply <- nil
	pumpPreview(t, queue, func() bool { return p.probeCancel == nil })
	if p.state.err != nil || !p.state.ready || !p.imageDocument || b.url != "" || b.destroys != 0 {
		t.Fatal("replacement navigated, cleared the frame or accepted a stale failure")
	}
}

func TestImageReplacementReportsDecodeFailure(t *testing.T) {
	b := &fakePreviewBrowser{eval: func(context.Context, string) (any, error) {
		return nil, errors.New("invalid image")
	}}
	p, queue := queuedPreview(t, b)
	p.state.ready, p.imageDocument = true, true
	p.replaceImage(workspaceMediaOptions{Source: "corrupt.png", Token: "corrupt"})
	called := 0
	p.onFailure = func(error) { called++ }
	pumpPreview(t, queue, func() bool { return p.state.err != nil })
	if called != 1 || p.state.ready || p.imageDocument || b.destroys != 0 || b.url != "about:blank" {
		t.Fatal("decode failure did not retire the failed content and report its error")
	}
}

func TestPreviewFailureStopsContentAndRetainsHealthySlot(t *testing.T) {
	b := &fakePreviewBrowser{}
	p, queue := queuedPreview(t, b)
	p.watchPage("about:blank", true)
	pumpPreview(t, queue, func() bool { return p.state.ready })
	p.mediaToken = "old-player"
	document := storePreviewDocument("large old document")
	p.documentURL = document
	p.timer = time.NewTimer(time.Hour)
	called := 0
	p.onFailure = func(error) { called++ }
	b.fail(&mygo.LoadError{URL: "about:blank", Description: "failed"})
	pumpPreview(t, queue, func() bool { return p.state.err != nil })
	if p.IsDestroyed() || b.destroys != 0 || b.url != "about:blank" || p.state.ready || called != 1 {
		t.Fatal("ordinary load failure did not stop content and retain the slot")
	}
	if p.mediaToken != "" || p.timer != nil || p.onFailure != nil || b.unsubscribed != 2 {
		t.Fatal("failure retained callbacks, timer or media identity")
	}
	if _, ok := previewDocuments.Load(strings.TrimPrefix(document, "ally-preview://document/")); ok {
		t.Fatal("failed document remained registered")
	}
}

func TestPreviewRendererFailureRequiresExplicitRebuild(t *testing.T) {
	b := &fakePreviewBrowser{}
	p, queue := queuedPreview(t, b)
	p.watchPage("about:blank", true)
	pumpPreview(t, queue, func() bool { return p.state.ready })
	req := previewRequest{Key: "failed-chart", Source: "chart"}
	pool := previewPool{entries: map[string]*previewEntry{req.Key: {request: req, surface: p}}}
	b.gone("crashed")
	pumpPreview(t, queue, func() bool { return p.state.err != nil })
	if !p.IsDestroyed() || b.destroys != 1 || !pool.matches([]previewRequest{req}) {
		t.Fatal("broken realm was not destroyed or failure causes automatic rebuilds")
	}
	creates := 0
	create := func() (previewSurface, error) { creates++; return &fakePreviewSurface{}, nil }
	pool.sync([]previewRequest{req}, create)
	if creates != 0 {
		t.Fatal("failed chart retried without user action")
	}
	pool.retry(req)
	pool.sync([]previewRequest{req}, create)
	if creates != 1 || pool.entries[req.Key].surface == p {
		t.Fatal("retry did not replace broken realm")
	}
	pool.release("")
}

func TestPreviewLateCreationFailureHasBoundedProbe(t *testing.T) {
	b := &fakePreviewBrowser{eval: func(ctx context.Context, _ string) (any, error) {
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) > 5*time.Second {
			return nil, errors.New("probe not bounded")
		}
		return nil, context.DeadlineExceeded
	}}
	p, queue := queuedPreview(t, b)
	p.watchPage("about:blank", true)
	pumpPreview(t, queue, func() bool { return p.state.err != nil })
	if !p.IsDestroyed() || p.state.ready || p.probeCancel != nil || b.unsubscribed != 2 {
		t.Fatal("late controller failure leaked its realm/probe/subscriptions")
	}
}

func TestPreviewRetiredFailureCannotAffectReusedPage(t *testing.T) {
	b := &fakePreviewBrowser{}
	p, queue := queuedPreview(t, b)
	p.watchPage("old", true)
	pumpPreview(t, queue, func() bool { return p.state.ready })
	oldFail, oldGone := b.fail, b.gone
	oldFail(&mygo.LoadError{URL: "old", Description: "queued failure"})
	p.Reset()
	p.state.begin()
	p.watchPage("new", true)
	oldGone("old process result")
	b.fail(&mygo.LoadError{URL: "old", Description: "wrong document"})
	pumpPreview(t, queue, func() bool { return p.state.ready })
	for len(queue) > 0 {
		(<-queue)()
	}
	if p.IsDestroyed() || p.state.err != nil {
		t.Fatal("old callback invalidated reused page")
	}
}

func TestPreviewResetCancelsProbeAndDestroyIsIdempotent(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	b := &fakePreviewBrowser{eval: func(ctx context.Context, _ string) (any, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}}
	p, _ := queuedPreview(t, b)
	p.watchPage("old", true)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	p.Reset()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("reset left probe running")
	}
	p.Destroy()
	p.Destroy()
	if b.destroys != 1 || b.unsubscribed != 2 {
		t.Fatal("destroy leaked or double released resources")
	}
}

func TestWorkspacePreviewFailurePublishesOnlyToCurrentFile(t *testing.T) {
	root := t.TempDir()
	file := &workspaceFile{Path: "one.mp3"}
	b := &fakePreviewBrowser{}
	p, _ := queuedPreview(t, b)
	a := &desktop{tabs: []*conversation{{Workspace: root}}, workspaceFiles: map[string]*workspaceFile{workspaceKey(root): file}}
	key := a.workspaceFilePreviewKey(file)
	a.workspacePreview.mounted = map[string]*mountedWorkspacePreview{workspaceKey(root): {key: key, surface: p}}
	a.watchWorkspacePreviewFailure(p, file, key)
	old := p.onFailure
	media := a.workspaceMedia()
	media.playing, media.autoplay = true, true
	p.failContent(p.state.generation, errors.New("renderer failed"), true)
	if file.PreviewError == "" || media.playing || media.autoplay {
		t.Fatal("file failure remained blank/playing")
	}
	replacement := &workspaceFile{Path: "two.mp3"}
	a.workspaceFiles[workspaceKey(root)] = replacement
	file.PreviewError = ""
	old(errors.New("late failure"))
	if replacement.PreviewError != "" || file.PreviewError != "" {
		t.Fatal("retired file callback modified current state")
	}
}

func TestPluginSyncIncludesWindowVisibility(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), pluginHost: pluginhost.New(nil, nil, nil), plugins: []backend.PluginInfo{{ID: "test", Mode: "plugin:test", Enabled: true, Valid: true}}, page: "plugin:test"}
	defer a.pluginHost.Close()
	a.syncPluginHostVisibility(false)
	visible := a.pluginSyncKey
	a.syncPluginHostVisibility(true)
	if !a.pluginSyncKey.hidden || a.pluginSyncKey == visible {
		t.Fatal("hide did not invalidate plugin sync key")
	}
	a.syncPluginHostVisibility(false)
	if a.pluginSyncKey != visible {
		t.Fatal("restore did not return to visible state")
	}
}
