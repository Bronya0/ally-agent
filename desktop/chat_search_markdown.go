// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/markdown/parser"
)

// chatSearchMarkdownRun maps a visible UTF-8 byte range back to its source
// range. Non-exact runs (for example a soft break) map as a single unit.
type chatSearchMarkdownRun struct {
	visibleStart, visibleEnd int
	sourceStart, sourceEnd   int
	linkDest                 string
	link, linkPending        bool
	bold, italic, strike     bool
	code                     bool
}

type chatSearchMarkdownBlock struct {
	start, end           int
	visibleStart         int
	text                 string
	runs                 []chatSearchMarkdownRun
	codeBlock, tableCell bool
	tableTail            bool
}

type chatSearchMarkdown struct {
	text       string
	blocks     []chatSearchMarkdownBlock
	incomplete bool
}

// chatSearchMarkdownText projects only text that the regular Markdown renderer
// shows in paragraphs, headings, table cells and code blocks. Blocks with images, HTML, math or
// footnote references are left to the native renderer rather than flattened.
// Code fences remain handled by the existing code-block path.
func chatSearchMarkdownText(src string) chatSearchMarkdown {
	doc := parser.Parse(src)
	tableSearchable := make(map[int]bool)
	for i := 1; i < doc.Len(); i++ {
		if doc.Block(i).Kind != parser.Table {
			continue
		}
		searchable := true
		for row := i + 1; row < doc.Block(i).Next && searchable; row = doc.Block(row).Next {
			for cell := row + 1; cell < doc.Block(row).Next; cell = doc.Block(cell).Next {
				if _, ok := chatSearchMarkdownInlineBlock(doc, cell); !ok {
					searchable = false
					break
				}
			}
		}
		tableSearchable[i] = searchable
	}
	out := chatSearchMarkdown{}
	var text strings.Builder
	for i := 1; i < doc.Len(); i++ {
		b := doc.Block(i)
		var block chatSearchMarkdownBlock
		var ok bool
		switch b.Kind {
		case parser.Paragraph, parser.Heading:
			block, ok = chatSearchMarkdownInlineBlock(doc, i)
			if !ok {
				out.incomplete = true
			}
		case parser.TableCell:
			row := b.Parent
			if row < 0 || row >= doc.Len() {
				continue
			}
			table := doc.Block(row).Parent
			if !tableSearchable[table] {
				out.incomplete = true
				continue
			}
			block, ok = chatSearchMarkdownInlineBlock(doc, i)
			if ok {
				block.tableCell = true
				block.tableTail = chatSearchMarkdownTableAtTail(doc, table)
			}
		case parser.CodeBlock:
			block, ok = chatSearchMarkdownCodeBlock(doc, i)
			if !ok {
				out.incomplete = true
			}
		default:
			if b.Kind == parser.HTMLBlock || b.Kind == parser.MathBlock {
				out.incomplete = true
			}
			continue
		}
		if !ok || block.text == "" {
			continue
		}
		if text.Len() > 0 {
			text.WriteByte('\n')
		}
		block.visibleStart = text.Len()
		text.WriteString(block.text)
		out.blocks = append(out.blocks, block)
	}
	out.text = text.String()
	return out
}

func chatSearchMarkdownTableAtTail(doc *parser.Document, index int) bool {
	block := doc.Block(index)
	tail := doc.Source()[block.End:]
	if strings.Trim(tail, " |\t\r\n") != "" {
		return false
	}
	breaks := strings.Count(tail, "\n") + strings.Count(tail, "\r") - strings.Count(tail, "\r\n")
	return breaks < 2
}

func chatSearchMarkdownCodeBlock(doc *parser.Document, index int) (chatSearchMarkdownBlock, bool) {
	b := doc.Block(index)
	if b.End-b.Start > chatSearchCodeMaxBytes || doc.LineCount(index) > chatSearchCodeMaxLines {
		return chatSearchMarkdownBlock{}, false
	}
	block := chatSearchMarkdownBlock{start: b.Start, end: b.End, codeBlock: true}
	var text strings.Builder
	for lineIndex := 0; lineIndex < doc.LineCount(index); lineIndex++ {
		line, sourceStart := doc.Line(index, lineIndex)
		sourceStart -= chatSearchCodePad(doc.Source(), line, sourceStart)
		if lineIndex > 0 {
			text.WriteByte('\n')
		}
		start := text.Len()
		text.WriteString(line)
		if line != "" {
			block.runs = append(block.runs, chatSearchMarkdownRun{visibleStart: start, visibleEnd: text.Len(), sourceStart: sourceStart, sourceEnd: sourceStart + len(line), code: true})
		}
	}
	block.text = text.String()
	return block, true
}

func chatSearchMarkdownInlineBlock(doc *parser.Document, index int) (chatSearchMarkdownBlock, bool) {
	b := doc.Block(index)
	in := doc.Inlines(index)
	var text strings.Builder
	var runs []chatSearchMarkdownRun
	var walk func(int, int, chatSearchMarkdownRun) bool
	walk = func(from, to int, style chatSearchMarkdownRun) bool {
		for k := from; k < to; k = in[k].Next {
			n := &in[k]
			switch n.Kind {
			case parser.Text, parser.Mention, parser.Reference:
				if n.Start < 0 || n.End < n.Start || n.End > len(doc.Source()) {
					return false
				}
				// These tokens are visible literally only when their source is the
				// same bytes. Refuse transformed entities rather than guess offsets.
				if doc.Source()[n.Start:n.End] != n.Text {
					return false
				}
				appendSearchMarkdownRun(&text, &runs, n.Text, n.Start, n.End, style)
			case parser.Code:
				if n.Start < 0 || n.End > len(doc.Source()) || n.End < n.Start {
					return false
				}
				raw := doc.Source()[n.Start:n.End]
				open := 0
				for open < len(raw) && raw[open] == '`' {
					open++
				}
				if open == 0 || open+len(n.Text) > len(raw) || raw[open:open+len(n.Text)] != n.Text {
					return false
				}
				codeStyle := style
				codeStyle.code = true
				appendSearchMarkdownRun(&text, &runs, n.Text, n.Start+open, n.Start+open+len(n.Text), codeStyle)
			case parser.SoftBreak:
				appendSearchMarkdownRun(&text, &runs, " ", n.Start, n.End, style)
			case parser.LineBreak:
				appendSearchMarkdownRun(&text, &runs, "\n", n.Start, n.End, style)
			case parser.Emphasis, parser.Strong, parser.Strikethrough, parser.Link:
				childStyle := style
				switch n.Kind {
				case parser.Emphasis:
					childStyle.italic = true
				case parser.Strong:
					childStyle.bold = true
				case parser.Strikethrough:
					childStyle.strike = true
				case parser.Link:
					childStyle.linkDest, childStyle.link, childStyle.linkPending = n.Dest, true, n.Pending
				}
				if !walk(k+1, n.Next, childStyle) {
					return false
				}
			case parser.Image, parser.HTML, parser.Math, parser.FootnoteRef:
				// Keep the native block when its inline content cannot be recreated
				// faithfully by the overlay.
				return false
			default:
				return false
			}
		}
		return true
	}
	if !walk(0, len(in), chatSearchMarkdownRun{}) {
		return chatSearchMarkdownBlock{}, false
	}
	visible := text.String()
	lead := 0
	for lead < len(visible) {
		r, size := utf8.DecodeRuneInString(visible[lead:])
		if !unicode.IsSpace(r) {
			break
		}
		lead += size
	}
	end := len(visible)
	for end > lead {
		r, size := utf8.DecodeLastRuneInString(visible[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	if lead > 0 || end < len(visible) {
		trimmed := make([]chatSearchMarkdownRun, 0, len(runs))
		for _, run := range runs {
			left, right := max(lead, run.visibleStart), min(end, run.visibleEnd)
			if left >= right {
				continue
			}
			if run.visibleEnd-run.visibleStart == run.sourceEnd-run.sourceStart {
				run.sourceStart += left - run.visibleStart
				run.sourceEnd = run.sourceStart + right - left
			}
			run.visibleStart, run.visibleEnd = left-lead, right-lead
			trimmed = append(trimmed, run)
		}
		runs = trimmed
		visible = visible[lead:end]
	}
	return chatSearchMarkdownBlock{start: b.Start, end: b.End, text: visible, runs: runs}, true
}

func appendSearchMarkdownRun(text *strings.Builder, runs *[]chatSearchMarkdownRun, visible string, srcStart, srcEnd int, style chatSearchMarkdownRun) {
	if visible == "" {
		return
	}
	start := text.Len()
	text.WriteString(visible)
	style.visibleStart, style.visibleEnd = start, text.Len()
	style.sourceStart, style.sourceEnd = srcStart, srcEnd
	*runs = append(*runs, style)
}

// chatSearchMarkdownSourceRange maps a visible match to the enclosing source
// range. A match may cross formatting boundaries; its source range then includes
// hidden delimiters, which are never themselves searchable.
func chatSearchMarkdownSourceRange(p chatSearchMarkdown, start, end int) (int, int, bool) {
	if start < 0 || end <= start || end > len(p.text) || !utf8.RuneStart(p.text[start]) || (end < len(p.text) && !utf8.RuneStart(p.text[end])) {
		return 0, 0, false
	}
	sourceStart, sourceEnd := -1, -1
	for _, b := range p.blocks {
		for _, r := range b.runs {
			r.visibleStart += b.visibleStart
			r.visibleEnd += b.visibleStart
			if r.visibleStart >= end || r.visibleEnd <= start {
				continue
			}
			// Exact visible/source runs may be sliced to the matching subsection;
			// synthesized whitespace maps to its complete source token.
			left, right := max(start, r.visibleStart), min(end, r.visibleEnd)
			rs, re := r.sourceStart, r.sourceEnd
			if r.visibleEnd-r.visibleStart == r.sourceEnd-r.sourceStart {
				rs += left - r.visibleStart
				re = r.sourceStart + right - r.visibleStart
			}
			if sourceStart < 0 || rs < sourceStart {
				sourceStart = rs
			}
			if re > sourceEnd {
				sourceEnd = re
			}
		}
	}
	return sourceStart, sourceEnd, sourceStart >= 0 && sourceEnd > sourceStart
}
