// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Temporary probe: render a message through the same options the chat uses, at
// a large size, so the glyphs of inline code can be compared with the body.
func TestProbeMessageFonts(t *testing.T) {
	conv := &conversation{ID: "probe", Workspace: `C:\work`, Messages: []map[string]any{{"role": "assistant", "content": ""}}}
	a := &desktop{}
	opts := a.markdownOptions(conv, 0)
	opts.Theme.FontSize = 34

	const body = "正文 abcdefghij 0123456789 MaxHeight 和 `MaxHeight(280)` 与 `contextBreakdownHeight = 420`\n\n" +
		"```go\nfunc contextUsage() { go build ./... }\n```\n"

	view := func(c *ui.Context) {
		ui.Column(c).Padding(20).Gap(10).Children(func() {
			markdown.View(c, body, opts).FillWidth()
		})
	}
	tester := ui.NewTester(view, 1500, 320)
	img := tester.Image()
	f, err := os.Create("../.tmp/message-font.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
