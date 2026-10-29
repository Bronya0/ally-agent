// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

const readingFontSize float32 = 16
const readingCodeSize float32 = 14.5

// Each accent owns both appearance variants. Prose stays neutral; headings,
// emphasis and inline code receive only a restrained hint of the selected hue.
type markdownColors struct {
	text, muted, heading, strong, code, codeBG, link, panel, border ui.Color
	syntax                                                          *editor.Theme
}
type markdownColorPair struct{ dark, light markdownColors }

func proseColors(text, muted, heading, strong, code, tint, link, panel, border string) markdownColors {
	colors := markdownColors{text: ui.Hex(text), muted: ui.Hex(muted), heading: ui.Hex(heading), strong: ui.Hex(strong), code: ui.Hex(code), codeBG: ui.Hex(tint).Alpha(0.045), link: ui.Hex(link), panel: ui.Hex(panel), border: ui.Hex(border)}
	colors.syntax = editor.DefaultTheme(&ui.Theme{Dark: colors.panel.R < 128, Background: colors.panel, Text: colors.text, TextMuted: colors.muted, Accent: colors.link, Border: colors.border})
	colors.syntax.Tokens[editor.TokenKeyword].Color = colors.link
	colors.syntax.Tokens[editor.TokenType].Color = colors.heading
	colors.syntax.Tokens[editor.TokenFunction].Color = colors.strong
	colors.syntax.Tokens[editor.TokenNumber].Color = colors.code
	colors.syntax.Tokens[editor.TokenComment].Color = colors.muted
	return colors
}

var markdownPalettes = []markdownColorPair{
	{proseColors("#c9cbd0", "#9498a1", "#d7deed", "#d0d7e5", "#bac8df", "#8da5df", "#98b3e8", "#202228", "#30343e"), proseColors("#303642", "#6b7482", "#26354e", "#34435d", "#485d7c", "#597cb5", "#365fc4", "#eff2f7", "#d9dfea")},
	{proseColors("#cacdd4", "#959ba6", "#d9e0ee", "#d0d8e8", "#bdcbe2", "#829fdf", "#9bb6ed", "#20232a", "#303743"), proseColors("#303640", "#6d7582", "#28364e", "#34445f", "#4a5f80", "#6b8bc4", "#365ebd", "#eef2f8", "#d9e0eb")},
	{proseColors("#cecbc5", "#9b968d", "#dfd6c7", "#d8cfbf", "#c9bda7", "#c39b65", "#d2ae79", "#25221e", "#39332b"), proseColors("#3b362f", "#7b7267", "#4e3e29", "#564632", "#735f43", "#b08a51", "#8a6029", "#f7f3ec", "#e4dbcc")},
	{proseColors("#c5cdd2", "#8d9ca7", "#cee0e8", "#c9d9e1", "#b0cbd9", "#63b3d8", "#85bfd9", "#1d252b", "#2d3942"), proseColors("#2e3a41", "#687b85", "#264654", "#34505d", "#446b7a", "#4793b1", "#246b8e", "#edf4f7", "#d5e2e8")},
	{proseColors("#c6cec8", "#8e9e94", "#d0e0d5", "#cbdacf", "#b3cbbb", "#6fbf8f", "#8ac9a2", "#1e2721", "#303d34"), proseColors("#303b33", "#6a7d6e", "#2c4935", "#38533f", "#4a6c54", "#6b9e79", "#346e47", "#eff5f0", "#d8e4da")},
	{proseColors("#cdc9d5", "#9a91aa", "#ddd5ec", "#d5cde3", "#c6b9db", "#a68bea", "#bba2e6", "#25212d", "#393144"), proseColors("#38323f", "#7b6f88", "#49365c", "#514361", "#6b557f", "#9a7eb6", "#79529d", "#f4eff8", "#e1d8e9")},
}

func (a *desktop) readingColors() markdownColors {
	index := a.prefs.Palette
	if index < 0 || index >= len(markdownPalettes) {
		index = 2
	}
	if a.currentPalette().dark {
		return markdownPalettes[index].dark
	}
	return markdownPalettes[index].light
}

func readingSizes(body, code float64) (float32, float32) {
	// Upgrade the previous standard sizes while retaining other user choices.
	if body == 0 || body == 15.5 {
		body = float64(readingFontSize)
	}
	if code == 0 || code == 14 {
		code = float64(readingCodeSize)
	}
	return float32(clampView(body, 16)), float32(clampView(code, 14.5))
}

// Reuse MyGo's entire code-block renderer (copy, syntax, scrolling and selection).
// A nested native view gives fenced code its own size without shrinking inline code.
func (a *desktop) readingMarkdownOptions() *markdown.Options {
	options := &markdown.Options{Theme: &markdown.Theme{}, WrapCode: true}
	type cachedBlock struct {
		code, source, language string
		options                markdown.Options
		theme                  markdown.Theme
	}
	blocks := map[int]*cachedBlock{}
	options.CodeBlock = func(c *ui.Context, block *markdown.CodeBlockView) {
		cached := blocks[block.Index]
		if cached == nil {
			if len(blocks) >= 32 {
				blocks = map[int]*cachedBlock{}
			}
			cached = &cachedBlock{}
			cached.options.Theme = &cached.theme
			blocks[block.Index] = cached
		}
		code := block.Code()
		if cached.source == "" || cached.code != code || cached.language != block.Language {
			cached.code, cached.language = code, block.Language
			fence := "~~~"
			for strings.Contains(code, fence) {
				fence += "~"
			}
			cached.source = fence + block.Language + string(rune(10)) + code + string(rune(10)) + fence
		}
		cached.theme = *options.Theme
		_, codeSize := readingSizes(a.config.MessageFontSize, a.config.CodeFontSize)
		cached.theme.CodeSize = codeSize / options.Theme.FontSize
		cached.theme.CodeLineHeight = 1.5
		cached.options.WrapCode = options.WrapCode
		cached.options.ShareSelection = true
		markdown.View(c.Key(fmt.Sprintf("reading-code-%d", block.Index)), cached.source, &cached.options).FillWidth()
	}
	return options
}

func (a *desktop) configureReadingMarkdown(options *markdown.Options) {
	colors := a.readingColors()
	th := options.Theme
	th.Font, th.CodeFont = nativeFontFamily(), nativeFontFamily()
	th.FontSize, _ = readingSizes(a.config.MessageFontSize, a.config.CodeFontSize)
	th.CodeSize = 1
	th.Text, th.Muted, th.Link = colors.text, colors.muted, colors.link
	th.Strong, th.Code, th.CodeBackground = colors.strong, colors.code, colors.codeBG
	th.StrongWeight, th.LineHeight, th.CodeLineHeight = 600, 1.6, 1.5
	th.BlockSpacing, th.ItemSpacing = 10/th.FontSize, 6/th.FontSize
	th.TableCellPadding = [2]float32{12 / th.FontSize, 8 / th.FontSize}
	th.TableBorder, th.TableHeader = colors.border, colors.panel
	th.CodeBlockBackground, th.CodeBlockBorder = colors.panel, colors.border
	th.CodeColors = colors.syntax
	th.QuoteText, th.QuoteBar, th.Rule = colors.muted, colors.border, colors.border
	for n, size := range []float32{1.4, 1.25, 1.125, 1, 1, 1} {
		th.Headings[n] = markdown.HeadingStyle{Size: size, Weight: 600, Color: colors.heading, Top: 1, Bottom: 0.5}
	}

}
