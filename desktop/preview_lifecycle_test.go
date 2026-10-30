// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"strings"
	"testing"
)

type fakePreviewSurface struct {
	loads, destroys int
	request         previewRequest
}

func (p *fakePreviewSurface) Load(r previewRequest)    { p.loads++; p.request = r }
func (p *fakePreviewSurface) Destroy()                 { p.destroys++ }
func (p *fakePreviewSurface) IsDestroyed() bool        { return p.destroys > 0 }
func (p *fakePreviewSurface) Native() ui.NativeWebView { return nil }
func (p *fakePreviewSurface) Status() (bool, error)    { return true, nil }

func TestPreviewLifecycleRetainsTransfersAndDestroys(t *testing.T) {
	var pool previewPool
	var created []*fakePreviewSurface
	create := func() (previewSurface, error) {
		p := &fakePreviewSurface{}
		created = append(created, p)
		return p, nil
	}
	first := previewRequest{Key: "session:1", Kind: "mermaid", Source: "flowchart TD; A-->B"}
	second := previewRequest{Key: "session:2", Kind: "html", Source: "<p>chart</p>"}
	pool.sync([]previewRequest{first}, create)
	pool.sync([]previewRequest{first}, create)
	if len(created) != 1 || created[0].loads != 1 {
		t.Fatal("unchanged preview recreated/reloaded")
	}
	first.Source = "flowchart TD; B-->C"
	pool.sync([]previewRequest{first}, create)
	if len(created) != 1 || created[0].loads != 2 {
		t.Fatal("updated content did not reuse surface")
	}
	pool.sync([]previewRequest{second}, create)
	if len(created) != 1 || created[0].request != second || created[0].destroys != 0 {
		t.Fatal("outgoing surface was not transferred")
	}
	pool.sync([]previewRequest{first, second}, create)
	if len(created) != 2 {
		t.Fatal("simultaneous previews did not get separate surfaces")
	}
	pool.release("other:")
	if len(pool.entries) != 2 {
		t.Fatal("released unrelated session")
	}
	pool.release("session:")
	for _, p := range created {
		if p.destroys != 1 {
			t.Fatal("session close did not destroy exactly once")
		}
	}
	pool.sync(nil, create)
	if len(pool.entries) != 0 {
		t.Fatal("idle pool retained surfaces")
	}
}

func TestPreviewBudgetStableAndLeavingViewDestroys(t *testing.T) {
	var pool previewPool
	var requests []previewRequest
	for i := 0; i < 10; i++ {
		requests = append(requests, previewRequest{Key: fmt.Sprint(i), Source: "chart"})
	}
	requests = append(requests[:1], requests...)
	targets := previewTargets(requests)
	if len(targets) != maxLivePreviews {
		t.Fatal("wrong preview budget")
	}
	created := 0
	var surfaces []*fakePreviewSurface
	create := func() (previewSurface, error) {
		created++
		p := &fakePreviewSurface{}
		surfaces = append(surfaces, p)
		return p, nil
	}
	pool.sync(targets, create)
	if !pool.matches(previewTargets(requests)) {
		t.Fatal("capped demands would cause reconciliation loop")
	}
	pool.sync(targets, create)
	if created != maxLivePreviews {
		t.Fatal("visible overflow churned surfaces")
	}
	pool.sync(nil, create)
	for _, p := range surfaces {
		if p.destroys != 1 {
			t.Fatal("hidden preview not destroyed")
		}
	}
}

func TestPreviewOverflowShowsDeferredMessage(t *testing.T) {
	a := &desktop{}
	tt := ui.NewTester(func(c *ui.Context) {
		a.previewRequests = nil
		ui.Row(c).Fill().Children(func() {
			for i := 0; i <= maxLivePreviews; i++ {
				ui.Column(c.Key(fmt.Sprint(i))).Width(150).Children(func() {
					a.previewView(c, previewRequest{Key: fmt.Sprint(i)})
				})
			}
		})
	}, 1000, 500)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("同屏最多加载 4 个预览，滚动后自动加载"); err != nil {
		t.Fatal("overflow preview must explain why it is waiting:", err)
	}
	if previewDeferred(a.previewRequests[1:], "4") {
		t.Fatal("preview did not become eligible after an earlier block left view")
	}
}

func TestPreviewCreationFailureDoesNotRetryEveryFrame(t *testing.T) {
	var pool previewPool
	targets := []previewRequest{{Key: "failed"}}
	attempts := 0
	create := func() (previewSurface, error) { attempts++; return nil, errors.New("unavailable") }
	pool.sync(targets, create)
	if !pool.matches(targets) {
		t.Fatal("creation failure schedules unbounded retries")
	}
	pool.sync(targets, create)
	if attempts != 1 {
		t.Fatal("failed preview retried without user action")
	}
}

func TestMermaidDocumentEscapesSourceAndUsesLocalEngine(t *testing.T) {
	source := `flowchart TD; A["</script><script>alert(1)</script>"]`
	for _, dark := range []bool{false, true} {
		doc := mermaidDocument(source, dark)
		if strings.Contains(doc, source) || strings.Contains(doc, "<script>alert(1)") {
			t.Fatal("source escaped script context")
		}
		for _, required := range []string{"mermaid.initialize", "mermaid.render", "securityLevel:'strict'", "connect-src 'none'", "out.textContent=", "htmlLabels:false"} {
			if !strings.Contains(doc, required) {
				t.Fatalf("missing %s", required)
			}
		}
		theme := "default"
		if dark {
			theme = "base"
		}
		if !strings.Contains(doc, "theme:'"+theme+"'") {
			t.Fatal("wrong Mermaid theme")
		}
	}
}

func TestPreviewDemandOnlyAfterVisibleGeometry(t *testing.T) {
	a := &desktop{}
	show := true
	req := previewRequest{Key: "session:visible", Kind: "mermaid", Source: "flowchart TD; A-->B"}
	tt := ui.NewTester(func(c *ui.Context) {
		a.previewRequests = a.previewRequests[:0]
		ui.Column(c).Fill().Children(func() {
			if show {
				a.previewView(c, req)
			}
		})
	}, 800, 500)
	tt.Frame()
	if len(a.previewRequests) != 1 || a.previewRequests[0] != req {
		t.Fatal("visible placeholder did not request preview")
	}
	show = false
	tt.Frame()
	if len(a.previewRequests) != 0 {
		t.Fatal("hidden preview retained demand")
	}
}

func TestMermaidMarkdownOpenFenceAndSourceToggle(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	options := a.readingMarkdownOptions("session:markdown")
	a.configureReadingMarkdown(options)
	source := "```mermaid\nflowchart TD; A-->B\n"
	tt := ui.NewTester(func(c *ui.Context) {
		a.previewRequests = a.previewRequests[:0]
		ui.Column(c).Fill().Children(func() { markdown.View(c, source, options) })
	}, 800, 600)
	tt.Frame()
	if len(a.previewRequests) != 0 {
		t.Fatal("open fence requested heavy renderer")
	}
	source += "```"
	tt.Frame()
	tt.Frame()
	if len(a.previewRequests) != 1 {
		t.Fatal("closed fence did not request Mermaid preview")
	}
	if err := tt.Click("查看源码"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.previewRequests) != 0 {
		t.Fatal("source view retained browser demand")
	}
	if err := tt.Click("查看图表"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if len(a.previewRequests) != 1 {
		t.Fatal("preview did not resume after source toggle")
	}
}

func TestPreviewDemandFollowsScrollVisibility(t *testing.T) {
	a := &desktop{}
	var scroll ui.ScrollState
	req := previewRequest{Key: "session:scrolled", Kind: "mermaid", Source: "flowchart TD; A-->B"}
	tt := ui.NewTester(func(c *ui.Context) {
		a.previewRequests = a.previewRequests[:0]
		ui.Scroll(c).Fill().TrackScroll(&scroll).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				ui.Box(c).Height(900)
				a.previewView(c, req)
			})
		})
	}, 800, 500)
	tt.Frame()
	if len(a.previewRequests) != 0 {
		t.Fatal("offscreen block requested browser")
	}
	scroll.Y = 900
	tt.Frame()
	tt.Frame()
	if len(a.previewRequests) != 1 {
		t.Fatal("scrolling into view did not request browser")
	}
	scroll.Y = 0
	tt.Frame()
	tt.Frame()
	if len(a.previewRequests) != 0 {
		t.Fatal("scrolling out retained browser demand")
	}
}

func TestPreviewReadinessIgnoresRetiredLoads(t *testing.T) {
	var state previewLoadState
	old := state.begin()
	current := state.begin()
	if state.complete(old) || state.ready {
		t.Fatal("late completion revealed reused surface")
	}
	if !state.complete(current) || !state.ready {
		t.Fatal("current render did not reveal surface")
	}
	if state.complete(current) {
		t.Fatal("duplicate completion was not ignored")
	}
	token := state.begin()
	state.err = errors.New("timeout")
	if state.complete(token) {
		t.Fatal("late completion dismissed timeout")
	}
	token = state.begin()
	if state.err != nil || state.ready {
		t.Fatal("new load retained previous state")
	}
	state.generation++ // destroy invalidates callbacks
	if state.complete(token) {
		t.Fatal("destroyed surface accepted callback")
	}
}

func TestPreviewReadinessDocuments(t *testing.T) {
	for _, mermaid := range []bool{true, false} {
		document := "<head><meta charset=\"utf-8\"></head><body>chart</body>"
		ready := previewReadyDocument(document, "test-token", mermaid)
		for _, required := range []string{"document.fonts.ready", "requestAnimationFrame", "setTimeout(once,120)", "document.title=\"test-token\""} {
			if !strings.Contains(ready, required) {
				t.Fatalf("missing readiness step: %s", required)
			}
		}
		if strings.Contains(ready, "addEventListener('load'") == mermaid {
			t.Fatal("Mermaid must wait for async renderer, HTML for page load")
		}
	}
}

func TestPreviewLoadingKeepsFixedGeometry(t *testing.T) {
	a := &desktop{}
	req := previewRequest{Key: "loading"}
	var bounds ui.Rect
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			a.previewView(c, req)
			marker := ui.Box(c.Key("after")).Height(20)
			bounds = marker.Bounds()
		})
	}, 800, 600)
	tt.Frame()
	if err := tt.Click("正在加载图表预览"); err != nil {
		t.Fatal("loading indicator is missing:", err)
	}
	before := bounds.Y
	a.previews.entries = map[string]*previewEntry{"loading": {request: req, err: errors.New("failed")}}
	tt.Frame()
	tt.Frame()
	if bounds.Y != before {
		t.Fatalf("loading/error changed neighboring layout: %v -> %v", before, bounds.Y)
	}
}
