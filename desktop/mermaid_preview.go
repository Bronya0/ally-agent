// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/resources"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Resources are embedded and loaded once, rather than read in a view pass.
var mermaidScript = func() string {
	data, err := resources.Assets.ReadFile("preview/mermaid.min.js")
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(string(data), "</script", `<\/script`)
}()

func mermaidDocument(source string, dark bool) string {
	theme, bg, ink := "default", "#ffffff", "#303642"
	if dark {
		theme, bg, ink = "dark", "#1b1b1b", "#c9c9cc"
	}
	encoded, _ := json.Marshal(source)
	return `<html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'"><style>html{color-scheme:` + map[bool]string{true: "dark", false: "light"}[dark] + `}body{margin:16px;background:` + bg + `;color:` + ink + `;font-family:'Microsoft YaHei',sans-serif}#diagram{display:flex;justify-content:center;min-height:120px}svg{max-width:100%;height:auto}pre{white-space:pre-wrap;overflow-wrap:anywhere}</style></head><body><div id="diagram" role="img" aria-label="Mermaid 图表"></div><pre id="error" role="alert" hidden></pre><script>` + mermaidScript + `</script><script>
mermaid.initialize({startOnLoad:false,securityLevel:'strict',theme:'` + theme + `',flowchart:{htmlLabels:false},suppressErrorRendering:true,maxTextSize:100000});
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
			block.CopyButton(c)
		})
		if *source {
			block.Body(c)
			return
		}
		a.previewView(c, previewRequest{Key: key, Kind: "mermaid", Source: block.Code(), Dark: a.currentPalette().dark})
	})
}
