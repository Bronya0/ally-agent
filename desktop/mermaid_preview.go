// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/resources"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Decode the embedded script once, only when a visible preview first needs it.
var mermaidScript = sync.OnceValue(func() string {
	data, err := resources.Assets.ReadFile("preview/mermaid.min.js")
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(string(data), "</script", `<\/script`)
})

func mermaidDocument(source string, dark bool) string {
	theme, bg, ink := "default", "#ffffff", "#303642"
	variables := "{}"
	if dark {
		theme, bg, ink = "base", "#1b1b1b", "#c9c9cc"
		variables = `{darkMode:true,background:"#1b1b1b",primaryColor:"#263d63",primaryTextColor:"#e2e8f0",primaryBorderColor:"#7da7e0",secondaryColor:"#28564a",secondaryTextColor:"#e2e8f0",tertiaryColor:"#644b2c",tertiaryTextColor:"#e2e8f0",lineColor:"#9cacc4",textColor:"#e2e8f0",mainBkg:"#263d63",nodeBorder:"#7da7e0",clusterBkg:"#252d3a",clusterBorder:"#657c9d",edgeLabelBackground:"#252d3a",actorBkg:"#263d63",actorTextColor:"#e2e8f0",actorBorder:"#7da7e0",signalColor:"#b6c6df",signalTextColor:"#e2e8f0"}`
	}
	encoded, _ := json.Marshal(source)
	return `<html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'"><style>html{color-scheme:` + map[bool]string{true: "dark", false: "light"}[dark] + `}body{margin:16px;background:` + bg + `;color:` + ink + `;font-family:'Microsoft YaHei',sans-serif}#diagram{display:flex;justify-content:center;min-height:120px}svg{max-width:100%;height:auto}pre{white-space:pre-wrap;overflow-wrap:anywhere}</style></head><body><div id="diagram" role="img" aria-label="Mermaid 图表"></div><pre id="error" role="alert" hidden></pre><script>` + mermaidScript() + `</script><script>
mermaid.initialize({startOnLoad:false,securityLevel:'strict',theme:'` + theme + `',themeVariables:` + variables + `,flowchart:{htmlLabels:false},suppressErrorRendering:true,maxTextSize:100000});
(async()=>{try{const result=await mermaid.render('ally-diagram',` + string(encoded) + `);document.getElementById('diagram').innerHTML=result.svg;}catch(error){document.getElementById('diagram').remove();const out=document.getElementById('error');out.hidden=false;out.textContent='图表渲染失败，请检查 Mermaid 语法：\n'+String(error.message||error);}finally{window.__allyPreviewReady?.();}})();
</script></body></html>`
}

func (a *desktop) mermaidView(c *ui.Context, block *markdown.CodeBlockView, scope string) {
	if block.Open {
		markdown.DefaultCodeBlock(c, block)
		return
	}
	key := fmt.Sprintf("%s:%d", scope, block.Index)
	ui.Column(c.Key("mermaid-" + key)).FillWidth().MinWidth(0).Gap(gapInline).Children(func() {
		container := ui.Row(c.Key("toolbar")).FillWidth().Gap(gapInline).AlignItems(ui.Center)
		source := ui.Local(container, "source", func() bool { return false })
		container.Children(func() {
			ui.Text(c, "mermaid").FontSize(fsSmall).TextColor(a.secondaryInk())
			ui.Spacer(c)
			label := "查看源码"
			if *source {
				label = "查看图表"
			}
			flat(c, label).OnClick(func() { *source = !*source })
			entry := a.previews.entries[key]
			ready := false
			if entry != nil && entry.request.Source == block.Code() && entry.request.Dark == a.currentPalette().dark && entry.surface != nil && !entry.surface.IsDestroyed() {
				ready, _ = entry.surface.Status()
			}
			flat(c, "导出 SVG").Disabled(!ready).OnClick(func() { a.exportMermaidSVG(key) })
			block.CopyButton(c)
		})
		if *source {
			block.Body(c)
			return
		}
		a.previewView(c, previewRequest{Key: key, Kind: "mermaid", Source: block.Code(), Dark: a.currentPalette().dark})
	})
}

func (a *desktop) exportMermaidSVG(key string) {
	entry := a.previews.entries[key]
	if entry == nil || entry.request.Kind != "mermaid" {
		return
	}
	preview, ok := entry.surface.(*nativePreview)
	if !ok || preview.IsDestroyed() {
		return
	}
	page := preview.view.Page()
	a.job(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		value, err := page.EvalContext(ctx, `(()=>{const svg=document.querySelector('#diagram svg');return svg ? new XMLSerializer().serializeToString(svg) : '';})()`)
		if err != nil {
			return err
		}
		svg, _ := value.(string)
		if !strings.Contains(svg, "<svg") {
			return errors.New("图表尚未渲染完成")
		}
		path, err := a.core.ExportTextFile("diagram.svg", svg, "SVG", "*.svg")
		if err == nil && path != "" {
			a.win.Update(func() { a.notifySuccess("已导出：" + path) })
		}
		return err
	})
}
