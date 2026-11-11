// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

const readingFontSize float32 = 16
const readingCodeSize float32 = 14.5

// Prose stays neutral while emphasis follows the selected palette.
type markdownColors struct {
	text, muted, heading, strong, code, codeBG, link, panel, border ui.Color
	syntax                                                          *editor.Theme
}
type markdownColorPair struct{ dark, light markdownColors }

func proseColors(text, muted, heading, strong, code, tint, link, panel, border string) markdownColors {
	colors := markdownColors{text: ui.Hex(text), muted: ui.Hex(muted), heading: ui.Hex(heading), strong: ui.Hex(strong), code: ui.Hex(code), codeBG: ui.Hex(tint).Alpha(0.045), link: ui.Hex(link), panel: ui.Hex(panel), border: ui.Hex(border)}
	colors.syntax = readingSyntax(colors)
	// Keep body text neutral and use the palette accent for reading emphasis.
	colors.heading = colors.link
	colors.code = colors.link
	// Nonzero RGB prevents Markdown theme resolution from substituting a background.
	colors.codeBG = ui.Hex("#ffffff").Alpha(0)
	if colors.panel.R < 128 {
		colors.text = ui.Hex("#cccccc")
		colors.muted = ui.Hex("#969da7")
	} else {
		colors.text = ui.Hex("#343a43")
		colors.muted = ui.Hex("#68717e")
	}

	return colors
}

func readingSyntax(colors markdownColors) *editor.Theme {
	syntax := editor.DefaultTheme(&ui.Theme{Dark: colors.panel.R < 128, Background: colors.panel, Text: colors.text, TextMuted: colors.muted, Accent: colors.link, Border: colors.border})
	syntax.Tokens[editor.TokenKeyword].Color = colors.link
	syntax.Tokens[editor.TokenType].Color = colors.heading
	syntax.Tokens[editor.TokenFunction].Color = colors.strong
	syntax.Tokens[editor.TokenNumber].Color = colors.code
	syntax.Tokens[editor.TokenComment].Color = colors.muted
	return syntax
}

// markdownPalettes holds the reading colours per palette index: the same value
// native-ui.json stores as "palette", so it must stay the same length and order
// as palettes.
var markdownPalettes = []markdownColorPair{
	{proseColors("#c9cbd0", "#9498a1", "#d7deed", "#d0d7e5", "#bac8df", "#8da5df", "#98b3e8", "#202228", "#30343e"), proseColors("#303642", "#6b7482", "#26354e", "#34435d", "#485d7c", "#597cb5", "#365fc4", "#eff2f7", "#d9dfea")},
	{proseColors("#cacdd4", "#959ba6", "#d9e0ee", "#d0d8e8", "#bdcbe2", "#829fdf", "#9bb6ed", "#20232a", "#303743"), proseColors("#303640", "#6d7582", "#28364e", "#34445f", "#4a5f80", "#6b8bc4", "#365ebd", "#eef2f8", "#d9e0eb")},
	{proseColors("#cecbc5", "#9b968d", "#dfd6c7", "#d8cfbf", "#c9bda7", "#c39b65", "#d2ae79", "#25221e", "#39332b"), proseColors("#3b362f", "#7b7267", "#4e3e29", "#564632", "#735f43", "#b08a51", "#8a6029", "#f7f3ec", "#e4dbcc")},
	{proseColors("#c5cdd2", "#8d9ca7", "#cee0e8", "#c9d9e1", "#b0cbd9", "#63b3d8", "#85bfd9", "#1d252b", "#2d3942"), proseColors("#2e3a41", "#687b85", "#264654", "#34505d", "#446b7a", "#4793b1", "#246b8e", "#edf4f7", "#d5e2e8")},
	{proseColors("#c6cec8", "#8e9e94", "#d0e0d5", "#cbdacf", "#b3cbbb", "#6fbf8f", "#8ac9a2", "#1e2721", "#303d34"), proseColors("#303b33", "#6a7d6e", "#2c4935", "#38533f", "#4a6c54", "#6b9e79", "#346e47", "#eff5f0", "#d8e4da")},
	{proseColors("#cdc9d5", "#9a91aa", "#ddd5ec", "#d5cde3", "#c6b9db", "#a68bea", "#bba2e6", "#25212d", "#393144"), proseColors("#38323f", "#7b6f88", "#49365c", "#514361", "#6b557f", "#9a7eb6", "#79529d", "#f4eff8", "#e1d8e9")},
	{proseColors("#d7dedc", "#9aa6a3", "#d8e5da", "#ccdace", "#b8cfbc", "#9dbb9f", "#9dbb9f", "#212122", "#303031"), proseColors("#202d28", "#59665f", "#202d28", "#202d28", "#25624a", "#25624a", "#25624a", "#fcfcf9", "#dce2d8")},
	{proseColors("#c9cbd0", "#9498a1", "#d7deed", "#d0d7e5", "#bac8df", "#e1e5ee", "#e1e5ee", "#202228", "#30343e"), proseColors("#303642", "#6b7482", "#26354e", "#34435d", "#485d7c", "#0f1115", "#0f1115", "#eff2f7", "#d9dfea")},
}

func (a *desktop) readingColors() markdownColors {
	index := a.prefs.Palette
	if index < 0 || index >= len(markdownPalettes) {
		index = defaultPalette
	}
	if index == paletteOcean {
		index = paletteBlue // Retired ocean preference follows blue.
	}
	colors := markdownPalettes[index].light
	if a.currentPalette().dark {
		colors = markdownPalettes[index].dark
		colors.text = ui.Hex("#d7dedc")
		colors.muted = ui.Hex("#9aa6a3")
		colors.heading = ui.Hex("#e5e5e5")
		seed := []string{"#8da5df", "#8da5df", "#e0a458", "#5b9cf6", "#57c98a", "#a78bfa", "#9dbb9f", "#e1e5ee"}[index]
		accent := ui.Hex(seed)
		colors.strong = ui.Hex("#d4d4d4").Mix(accent, 0.35)
		colors.code = accent.Mix(ui.Hex("#ffffff"), 0.28)
		colors.link = accent.Mix(ui.Hex("#ffffff"), 0.18)
		if index == paletteAmber {
			colors.heading = ui.Hex("#d7dedc")
			colors.strong = ui.Hex("#c4b894")
			colors.code = ui.Hex("#d4a574")
			colors.link = ui.Hex("#f0b66c")
		}
	} else {
		colors.text = ui.Hex("#252a37")
		colors.muted = ui.Hex("#3a4154")
		colors.heading = ui.Hex("#0f1219")
		colors.strong = colors.heading
		accent := []string{"#245fc2", "#245fc2", "#92530f", "#245fc2", "#217747", "#7042bd", "#25624a", "#0f1115"}[index]
		colors.code = ui.Hex(accent)
		colors.link = colors.code
		if index == paletteMeadow {
			p := a.currentPalette()
			colors.text, colors.muted = p.text, p.muted
			colors.heading, colors.strong = p.text, p.text
			colors.panel, colors.border = p.panel, p.line
		}
	}
	return colors
}

func readingSizes(body, code float64) (float32, float32) {
	if body == 0 {
		body = float64(readingFontSize)
	}
	if code == 0 {
		code = float64(readingCodeSize)
	}
	return float32(clampView(body, 16)), float32(clampView(code, 14.5))
}

// Reuse MyGo's entire code-block renderer (copy, syntax, scrolling and selection).
// A nested native view gives fenced code its own size without shrinking inline code.
func (a *desktop) readingMarkdownOptions(scope ...string) *markdown.Options {
	return a.readingMarkdownOptionsForSource(nil, scope...)
}

// source, when supplied, must be the text passed to the outer Markdown view.
// Completed code blocks then reuse their text while that source is unchanged.
func (a *desktop) readingMarkdownOptionsForSource(source func() string, scope ...string) *markdown.Options {
	options := &markdown.Options{Theme: &markdown.Theme{}, WrapCode: true}
	previewScope := fmt.Sprintf("markdown:%p", options)
	if len(scope) > 0 {
		previewScope = scope[0]
	}
	options.Code = map[string]func(*ui.Context, *markdown.CodeBlockView){
		"mermaid": func(c *ui.Context, block *markdown.CodeBlockView) {
			a.mermaidView(c, block, previewScope)
		},
	}
	type cachedBlock struct {
		input, source, language string
		extensions              parser.Extensions
		ready, open             bool
		fromSource              bool
		options                 markdown.Options
		theme                   markdown.Theme
	}
	options.CodeBlock = func(c *ui.Context, block *markdown.CodeBlockView) {
		holder := ui.Column(c.Key(fmt.Sprintf("reading-code-%d", block.Index))).FillWidth()
		cached := memoAt[cachedBlock](holder, "reading-code")
		input, code := "", ""
		fromSource := source != nil
		if fromSource {
			input = source()
		} else {
			code = block.Code()
			input = code
		}
		// Main conversation views show their source immediately, including open
		// fences. Other viewers retain Code() because they may reveal more of an
		// unchanged source across frames through markdown.Stream.
		if !cached.ready || cached.input != input || cached.fromSource != fromSource || cached.language != block.Language || cached.extensions != options.Extensions || cached.open != block.Open {
			if fromSource {
				code = block.Code()
			}
			cached.language, cached.extensions, cached.ready, cached.fromSource, cached.open = block.Language, options.Extensions, true, fromSource, block.Open
			fence := "~~~"
			for strings.Contains(code, fence) {
				fence += "~"
			}
			// Lines adds a newline even to an incomplete last line. Remove that
			// synthetic byte so subsequent tokens really append to the source.
			if fromSource && block.Open && !strings.HasSuffix(input, "\n") {
				code = strings.TrimSuffix(code, "\n")
			}
			cached.source = fence + block.Language + "\n" + code
			// Keep a growing fence open so the nested Markdown parser and syntax
			// highlighter can reuse the unchanged prefix on the next token.
			if !fromSource || !block.Open {
				cached.source += "\n" + fence
			}
		}
		cached.input = input
		cached.options.Theme = &cached.theme
		cached.theme = *options.Theme
		_, codeSize := readingSizes(a.config.MessageFontSize, a.config.CodeFontSize)
		cached.theme.CodeSize = codeSize / options.Theme.FontSize
		cached.theme.CodeLineHeight = 1.5
		cached.options.WrapCode = options.WrapCode
		cached.options.ShareSelection = true
		holder.Children(func() {
			markdown.View(c, cached.source, &cached.options).FillWidth()
		})
	}
	return options
}

// readingHeadingScales are the reading theme's heading sizes, relative to its body
// size, and readingLineHeight its line height. They live here because a block the
// find bar draws stands in for a Markdown one and must measure the same.
var readingHeadingScales = [...]float32{1.55, 1.32, 1.12, 1, 1, 1}

const readingLineHeight = 1.6

// readingCodeLineHeight is the reading theme's code line height, shared with the
// code blocks the find bar draws itself.
const readingCodeLineHeight = 1.5

func (a *desktop) configureReadingMarkdown(options *markdown.Options) {
	colors := a.readingColors()
	th := options.Theme
	th.Font, th.CodeFont = nativeFontFamily(), nativeFontFamily()
	th.FontSize, _ = readingSizes(a.config.MessageFontSize, a.config.CodeFontSize)
	th.CodeSize = 1
	th.Text, th.Muted, th.Link = colors.text, colors.muted, colors.link
	th.Strong, th.Code, th.CodeBackground = colors.strong, colors.code, colors.codeBG
	th.StrongWeight, th.LineHeight, th.CodeLineHeight = 600, readingLineHeight, readingCodeLineHeight
	th.BlockSpacing, th.ItemSpacing = 10/th.FontSize, 6/th.FontSize
	th.TableCellPadding = [2]float32{12 / th.FontSize, 8 / th.FontSize}
	th.TableBorder, th.TableHeader = colors.border, colors.panel
	th.CodeBlockBackground, th.CodeBlockBorder = colors.panel, colors.border
	th.CodeColors = colors.syntax
	th.QuoteText, th.QuoteBar, th.Rule = colors.muted, colors.border, colors.border
	for n, size := range readingHeadingScales {
		color := colors.heading
		if n == 1 && !a.currentPalette().dark {
			color = colors.text
		}
		th.Headings[n] = markdown.HeadingStyle{Size: size, Weight: 600, Color: color, Top: 1, Bottom: 0.5}
	}

}
