// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/resources"
	"encoding/base64"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"strings"
)

func toolHTMLDocument(html string) string {
	script, _ := resources.Assets.ReadFile("preview/echarts.min.js")
	font, _ := resources.Assets.ReadFile("fonts/Inter-400.ttf")
	return `<meta charset="utf-8"><style>@font-face{font-family:Inter;src:url(data:font/ttf;base64,` + base64.StdEncoding.EncodeToString(font) + `)}html{color-scheme:dark}body{margin:12px;background:#1b1b1b;color:#c9c9cc;font-family:Inter,'Microsoft YaHei',sans-serif}*{box-sizing:border-box}</style><script>` + strings.ReplaceAll(string(script), "</script", `<\/script`) + `</script>` + html
}

// Create the WebView on event/session ingestion, never in a render pass.
// A separate untrusted origin keeps generated HTML away from application bindings.
func (a *desktop) loadHTMLPreview(t *conversation, m map[string]any) {
	if a.win == nil {
		return
	}
	html := str(parseToolArgs(str(m, "args")), "html")
	if html == "" {
		html = str(toolResult(m), "html")
	}
	if html == "" {
		return
	}
	key := t.ID + ":" + str(m, "toolCallId")
	if a.htmlPreviews == nil {
		a.htmlPreviews = map[string]*mygo.WebView{}
	}
	if existing := a.htmlPreviews[key]; existing != nil && !existing.IsDestroyed() {
		return
	}
	// Chromium surfaces are expensive; keep at most four live previews.
	if len(a.htmlPreviews) >= 4 {
		for oldKey, oldView := range a.htmlPreviews {
			oldView.Destroy()
			delete(a.htmlPreviews, oldKey)
			break
		}
	}
	preview, err := a.win.NewWebView(mygo.WebViewOptions{BackgroundColor: "#1b1b1b"})
	if err != nil {
		m["previewError"] = err.Error()
		return
	}
	a.htmlPreviews[key] = preview
	preview.Page().OnWillNavigate(func(e *mygo.NavigateEvent) { e.PreventDefault() })
	preview.Page().SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
	preview.Page().LoadHTML(toolHTMLDocument(html), "https://ally-tool-preview.invalid/")
}

func (a *desktop) toolHTMLView(c *ui.Context, t *conversation, m map[string]any) {
	key := t.ID + ":" + str(m, "toolCallId")
	if view := a.htmlPreviews[key]; view != nil && !view.IsDestroyed() {
		ui.WebView(c.Key("html-preview-"+key), view).FillWidth().Height(420).Radius(radiusSmall).Border(1, c.Theme().Border)
	} else if message := str(m, "previewError"); message != "" {
		notice(c, "error", message)
	} else {
		flat(c, "加载交互预览").OnClick(func() { a.loadHTMLPreview(t, m) })
	}
}

func (a *desktop) releaseSessionHTMLPreviews(sessionID string) {
	prefix := sessionID + ":"
	for key, view := range a.htmlPreviews {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if view != nil && !view.IsDestroyed() {
			view.Destroy()
		}
		delete(a.htmlPreviews, key)
	}
}

func (a *desktop) releaseAllHTMLPreviews() {
	for key, view := range a.htmlPreviews {
		if view != nil && !view.IsDestroyed() {
			view.Destroy()
		}
		delete(a.htmlPreviews, key)
	}
	a.htmlPreviews = nil
}
