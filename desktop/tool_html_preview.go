// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/resources"
	"encoding/base64"
	"github.com/egoist/mygo/ui"
	"strings"
)

func toolHTMLDocument(html string) string {
	script, _ := resources.Assets.ReadFile("preview/echarts.min.js")
	font, _ := resources.Assets.ReadFile("fonts/Inter-400.ttf")
	return `<meta charset="utf-8"><style>@font-face{font-family:Inter;src:url(data:font/ttf;base64,` + base64.StdEncoding.EncodeToString(font) + `)}html{color-scheme:dark}body{margin:12px;background:#1b1b1b;color:#c9c9cc;font-family:Inter,'Microsoft YaHei',sans-serif}*{box-sizing:border-box}</style><script>` + strings.ReplaceAll(string(script), "</script", `<\/script`) + `</script>` + html
}

func (a *desktop) toolHTMLView(c *ui.Context, t *conversation, m map[string]any) {
	html := str(parseToolArgs(str(m, "args")), "html")
	if html == "" {
		html = str(toolResult(m), "html")
	}
	if html == "" {
		return
	}
	a.previewView(c, previewRequest{Key: t.ID + ":" + str(m, "toolCallId"), Kind: "html", Source: html})
}
