// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type fakeWorkspacePreview struct {
	loads, resets, destroys int
	body                    string
}

func (p *fakeWorkspacePreview) Native() ui.NativeWebView { return nil }
func (p *fakeWorkspacePreview) IsDestroyed() bool        { return p.destroys != 0 }
func (p *fakeWorkspacePreview) Destroy()                 { p.destroys++; p.body = "" }
func (p *fakeWorkspacePreview) Reset()                   { p.resets++; p.body = "" }
func (p *fakeWorkspacePreview) LoadHTML(body string)     { p.loads++; p.body = body }
func (p *fakeWorkspacePreview) LoadFile(path string) error {
	p.LoadHTML(path)
	return nil
}

func workspacePreviewTestKey(root, path string) string {
	return workspaceKey(root) + "\x00" + path
}

func TestWorkspacePreviewReusesCompatibleFileKinds(t *testing.T) {
	root := t.TempDir()
	var life workspacePreviewLifecycle
	creates := 0
	create := func() (workspacePreviewSurface, error) {
		creates++
		return &fakeWorkspacePreview{}, nil
	}
	for _, path := range []string{"one.mp4", "picture.jpg", "report.pdf", "page.html"} {
		_, pending := life.beginLoad(root, path)
		life.finishLoad(pending)
		policy := previewAutoplayAllowed(workspaceFileKindForPath(path).hasMediaPlayer())
		if err := life.mount(workspacePreviewTestKey(root, path), policy, create,
			func(p workspacePreviewSurface) error { p.LoadHTML(path); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	want := 1
	if !previewAutoplayAllowed(false) {
		want = 2 // Other platforms separate media playback from document pages.
	}
	if creates != want {
		t.Fatalf("compatible file types rebuilt WebView %d times, want %d", creates, want)
	}
	life.release()
}

func TestWorkspacePreviewReusesSwitchAndRefresh(t *testing.T) {
	root := t.TempDir()
	var life workspacePreviewLifecycle
	var created []*fakeWorkspacePreview
	create := func() (workspacePreviewSurface, error) {
		p := &fakeWorkspacePreview{}
		created = append(created, p)
		return p, nil
	}
	body := "first.mp4"
	load := func(p workspacePreviewSurface) error { p.LoadHTML(body); return nil }
	first := workspacePreviewTestKey(root, body)
	if err := life.mount(first, true, create, load); err != nil {
		t.Fatal(err)
	}
	if err := life.mount(first, true, create, load); err != nil || created[0].loads != 1 {
		t.Fatal("unchanged file reloaded the page", err)
	}
	for _, path := range []string{"second.mp4", "second.mp4"} {
		ctx, pending := life.beginLoad(root, path)
		if len(created) != 1 || created[0].destroys != 0 || created[0].body != "" || life.mountedFor(first) != nil {
			t.Fatal("switch did not retire content while retaining the native slot")
		}
		if !life.finishLoad(pending) || ctx.Err() == nil {
			t.Fatal("finished load retained its context")
		}
		body = path
		if err := life.mount(workspacePreviewTestKey(root, path), true, create, load); err != nil {
			t.Fatal(err)
		}
	}
	if len(created) != 1 || created[0].loads != 3 || created[0].resets != 2 || created[0].destroys != 0 {
		t.Fatalf("switch/refresh churned native resources: %+v", created)
	}
	life.release()
	if created[0].destroys != 1 || life.mounted != nil {
		t.Fatal("closing preview retained its native slot")
	}
}

func TestWorkspacePreviewRapidSwitchKeepsLatestLoad(t *testing.T) {
	root := t.TempDir()
	p := &fakeWorkspacePreview{}
	life := workspacePreviewLifecycle{mounted: &mountedWorkspacePreview{key: workspacePreviewTestKey(root, "first.mp4"), surface: p, autoplay: true}}
	firstCtx, first := life.beginLoad(root, "second.mp4")
	_, second := life.beginLoad(root, "third.mp4")
	if firstCtx.Err() == nil || life.finishLoad(first) || !life.finishLoad(second) || p.resets != 1 || p.destroys != 0 {
		t.Fatal("rapid selection retained a stale load or churned the native slot")
	}
	creates := 0
	if err := life.mount(workspacePreviewTestKey(root, "third.mp4"), true,
		func() (workspacePreviewSurface, error) { creates++; return nil, nil },
		func(surface workspacePreviewSurface) error { surface.LoadHTML("third.mp4"); return nil }); err != nil {
		t.Fatal(err)
	}
	if creates != 0 || p.loads != 1 || p.body != "third.mp4" {
		t.Fatal("latest file was not loaded into the retained slot")
	}
	life.release()
}

func TestWorkspacePreviewDoesNotReuseAcrossWorkspaceOrPolicy(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	var life workspacePreviewLifecycle
	var created []*fakeWorkspacePreview
	create := func() (workspacePreviewSurface, error) {
		p := &fakeWorkspacePreview{}
		created = append(created, p)
		return p, nil
	}
	load := func(p workspacePreviewSurface) error { p.LoadHTML("preview"); return nil }
	for _, tc := range []struct {
		root, path string
		autoplay   bool
	}{{root, "one.mp4", true}, {other, "two.mp4", true}, {other, "page.html", false}} {
		if err := life.mount(workspacePreviewTestKey(tc.root, tc.path), tc.autoplay, create, load); err != nil {
			t.Fatal(err)
		}
	}
	if len(created) != 3 || created[0].destroys != 1 || created[1].destroys != 1 || created[2].destroys != 0 {
		t.Fatal("preview reused a different workspace or immutable playback policy")
	}
	life.releaseWorkspace(root)
	if created[2].destroys != 0 {
		t.Fatal("releasing another workspace destroyed the active preview")
	}
	life.releaseWorkspace(other)
	if created[2].destroys != 1 || life.mounted != nil {
		t.Fatal("active workspace release retained its preview")
	}
}

func TestWorkspacePreviewLoadFailureReleasesReusedSlot(t *testing.T) {
	root := t.TempDir()
	p := &fakeWorkspacePreview{}
	life := workspacePreviewLifecycle{mounted: &mountedWorkspacePreview{key: workspacePreviewTestKey(root, "one.mp4"), surface: p, autoplay: true}}
	failure := errors.New("invalid preview")
	err := life.mount(workspacePreviewTestKey(root, "two.mp4"), true,
		func() (workspacePreviewSurface, error) { t.Fatal("recreated compatible view"); return nil, nil },
		func(workspacePreviewSurface) error { return failure })
	if !errors.Is(err, failure) || p.destroys != 1 || life.mounted != nil {
		t.Fatal("failed replacement retained its native view")
	}
}

func TestWorkspacePreviewQueuedCreationCannotReviveAfterRelease(t *testing.T) {
	for _, closePreview := range []bool{false, true} {
		var life workspacePreviewLifecycle
		updates := make(chan func(), 1)
		creates := 0
		visible := true
		life.schedule(func(fn func()) { updates <- fn }, "workspace\x00file.mp4", true,
			func() bool { return visible },
			func() (workspacePreviewSurface, error) { creates++; return &fakeWorkspacePreview{}, nil },
			func(workspacePreviewSurface) error { return nil },
			func(err error) { t.Fatal(err) })
		var update func()
		select {
		case update = <-updates:
		case <-time.After(time.Second):
			life.release()
			t.Fatal("preview timer did not publish its update")
		}
		if closePreview {
			life.release()
		} else {
			visible = false
		}
		update()
		if creates != 0 || life.mounted != nil || life.scheduled != nil {
			t.Fatal("retired or invisible preview was recreated by a queued callback")
		}
	}
}

func TestHiddenPreviewGraceStopsContentThenReleasesSlots(t *testing.T) {
	root := t.TempDir()
	fileView := &fakeWorkspacePreview{body: "playing video"}
	chartView := &fakePreviewSurface{}
	a := &desktop{tabs: []*conversation{{Workspace: root}}}
	a.media.playing, a.media.autoplay = true, true
	a.workspacePreview.mounted = &mountedWorkspacePreview{key: workspacePreviewTestKey(root, "one.mp4"), surface: fileView, autoplay: true}
	a.previews.entries = map[string]*previewEntry{"chart": {request: previewRequest{Key: "chart"}, surface: chartView}}
	a.releaseHiddenWindowResources()
	if fileView.resets != 1 || fileView.body != "" || fileView.destroys != 0 || chartView.destroys != 0 || a.media.playing || a.media.autoplay {
		t.Fatal("quick hide did not stop content while retaining bounded native slots")
	}
	// These are the native cleanup operations run once the grace period expires.
	a.releaseAllHTMLPreviews()
	a.releaseFilePreview()
	if fileView.destroys != 1 || chartView.destroys != 1 || a.workspacePreview.mounted != nil || len(a.previews.entries) != 0 {
		t.Fatal("hidden-window cleanup retained native slots")
	}
}

func TestNativePreviewResetRetiresCallbacksAndDocument(t *testing.T) {
	url := storePreviewDocument(strings.Repeat("preview", 1000))
	mediaOff, titleOff := 0, 0
	p := &nativePreview{documentURL: url, mediaToken: "old-page", state: previewLoadState{ready: true, err: errors.New("old")},
		offMedia: func() { mediaOff++ }, offTitle: func() { titleOff++ }, timer: time.NewTimer(time.Hour)}
	p.Reset()
	if _, ok := previewDocuments.Load(strings.TrimPrefix(url, "ally-preview://document/")); ok {
		t.Fatal("reset retained the old protocol document")
	}
	if mediaOff != 1 || titleOff != 0 || p.offMedia != nil || p.mediaToken != "" || p.documentURL != "" || p.timer != nil || p.state.ready || p.state.err != nil {
		t.Fatal("reset retained media callbacks, state or document data")
	}
	p.Destroy()
	p.Destroy()
	if mediaOff != 1 || titleOff != 1 || p.offTitle != nil {
		t.Fatal("destroy did not retire the base subscription exactly once")
	}
}

func TestPreviewRetryReusesLiveSurface(t *testing.T) {
	req := previewRequest{Key: "retry", Source: "chart"}
	p := &fakePreviewSurface{}
	pool := previewPool{entries: map[string]*previewEntry{req.Key: {request: req, surface: p, err: errors.New("timeout")}}}
	pool.retry(req)
	if p.loads != 1 || p.destroys != 0 || pool.entries[req.Key].surface != p || pool.entries[req.Key].err != nil {
		t.Fatal("retry unnecessarily recreated a live view")
	}
	pool.retry(previewRequest{Key: req.Key, Source: "retired source"})
	if p.loads != 1 {
		t.Fatal("retired retry changed a different preview request")
	}
	p.Destroy()
	pool.retry(req)
	if pool.entries[req.Key] != nil || p.destroys != 1 {
		t.Fatal("retry kept a destroyed view")
	}
}
