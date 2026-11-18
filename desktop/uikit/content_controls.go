package uikit

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"
)

// The host owns the persistent editor/document states and any cached options.
// These adapters do not parse or create background work outside the widget.
func Markdown(c *ui.Context, source string, options *markdown.Options) ui.Element {
	return markdown.View(c, source, options)
}

func MarkdownStream(c *ui.Context, source string, options *markdown.Options) ui.Element {
	return markdown.Stream(c, source, options)
}

func CodeEditor(c *ui.Context, state *editor.State) ui.Element     { return editor.View(c, state) }
func DiffViewer(c *ui.Context, state *editor.DiffState) ui.Element { return editor.DiffView(c, state) }
func RichTextEditor(c *ui.Context, doc *richtext.Document, options ...richtext.EditorOptions) ui.Element {
	return richtext.Editor(c, doc, options...)
}
func Chart(c *ui.Context, axes chart.Axes, build func(ui.Element)) ui.Element {
	return chart.Plot(c, axes, build)
}
