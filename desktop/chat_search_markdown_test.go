// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestChatSearchMarkdownProjectionUsesVisibleInlineText(t *testing.T) {
	src := "**bold** and *italic* with `code` and [link label](https://hidden.invalid)\nnext"
	p := chatSearchMarkdownText(src)
	want := "bold and italic with code and link label next"
	if p.text != want {
		t.Fatalf("visible text = %q, want %q", p.text, want)
	}
	for _, hidden := range []string{"https://hidden.invalid", "**", "`"} {
		if strings.Contains(p.text, hidden) {
			t.Fatalf("projection exposed Markdown syntax or destination %q: %q", hidden, p.text)
		}
	}
	for _, query := range []string{"bold", "italic", "code", "link label"} {
		start := strings.Index(p.text, query)
		gotStart, gotEnd, ok := chatSearchMarkdownSourceRange(p, start, start+len(query))
		if !ok || src[gotStart:gotEnd] != query {
			t.Errorf("%q maps to source [%d:%d] (%q), ok=%v", query, gotStart, gotEnd, src[gotStart:gotEnd], ok)
		}
	}
	if strings.Contains(p.text, "hidden.invalid") {
		t.Fatal("link destination must not be searchable")
	}
	wantStyles := map[string]chatSearchMarkdownRun{
		"bold":   {bold: true},
		"italic": {italic: true},
		"code":   {code: true},
	}
	for _, run := range p.blocks[0].runs {
		value := p.text[run.visibleStart:run.visibleEnd]
		if want, ok := wantStyles[value]; ok && (run.bold != want.bold || run.italic != want.italic || run.code != want.code) {
			t.Errorf("%q style = %+v, want %+v", value, run, want)
		}
		if value != "code" && run.code {
			t.Errorf("%q unexpectedly inherited inline code styling", value)
		}
		if value == "link label" && run.linkDest != "https://hidden.invalid" {
			t.Errorf("link label destination = %q", run.linkDest)
		}
	}
}

func TestChatSearchMarkdownProjectionSkipsUnhighlightableBlocks(t *testing.T) {
	src := "plain needle\n\n![needle](image.png)\n\n<div>needle</div>\n\n\\(needle\\)\n\nlast needle"
	p := chatSearchMarkdownText(src)
	if strings.Contains(p.text, "![") || strings.Contains(p.text, "<div>") || strings.Contains(p.text, "\\(needle\\)") {
		t.Fatalf("unsupported syntax leaked into visible projection: %q", p.text)
	}
	if strings.Count(p.text, "needle") != 2 {
		t.Fatalf("projection contains %d needle hits, want only the two plain-text blocks: %q", strings.Count(p.text, "needle"), p.text)
	}
}

func TestChatSearchScansVisibleMarkdownAndMapsToSource(t *testing.T) {
	src := "**needle** [needle label](https://hidden.invalid)\n\n![needle image](asset.png)"
	conv := searchTranscript(searchMessage("assistant", src))
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"needle", 2}, // bold text and link label; image alt is intentionally excluded
		{"label", 1},
		{"hidden.invalid", 0},
		{"**", 0},
		{"needle image", 0},
	} {
		s := &chatSearch{Query: tc.query, MatchCase: true}
		matches := chatSearchCompute(conv, s)
		if len(matches) != tc.want {
			t.Errorf("query %q: matches=%d want=%d (%+v)", tc.query, len(matches), tc.want, matches)
		}
		if tc.query == "needle" && len(matches) > 0 && src[matches[0].Start:matches[0].End] != "needle" {
			t.Errorf("mapped match covers %q, want visible source text", src[matches[0].Start:matches[0].End])
		}
	}
}

func TestChatSearchExcludesToolCardContent(t *testing.T) {
	conv := searchTranscript(
		searchMessage("assistant", "visible needle"),
		map[string]any{"role": "tool_call", "toolCallId": "call-1", "content": "needle in tool result", "args": map[string]any{"q": "needle"}},
	)
	s := &chatSearch{Query: "needle", MatchCase: true}
	matches := chatSearchCompute(conv, s)
	if len(matches) != 1 || matches[0].Message != 0 {
		t.Fatalf("matches = %+v, want only the visible assistant message", matches)
	}
}

func TestChatSearchCodeHighlightChangesWithQueryAcrossFrames(t *testing.T) {
	content := "```python\none, two = succ(0), succ(succ(0))\nreturn a if b == 0 else succ(plus(a, b - 1))\n```\n"
	conv := &conversation{ID: "search-code-query-change", Workspace: t.TempDir(), Messages: []map[string]any{searchMessage("assistant", content)}}
	a := &desktop{ready: true, page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}}
	s := &conv.uiState().Search
	s.Open, s.Query = true, "succ(succ(0))"
	a.chatSearchMatches(conv)
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		markdown.View(c, content, a.markdownOptions(conv, 0))
	}, 700, 400)
	tester.Frame()
	firstStart := strings.Index(content, "succ(succ(0))")
	if s.blockMatch.Start != firstStart {
		t.Fatalf("first query current=%+v, want source start %d", s.blockMatch, firstStart)
	}
	s.Query, s.frameKey = "else succ(plus(a, b - 1))", ""
	a.chatSearchMatches(conv)
	tester.Frame()
	secondStart := strings.Index(content, "else succ(plus(a, b - 1))")
	if s.blockMatch.Start != secondStart {
		t.Fatalf("after query change current=%+v, want source start %d", s.blockMatch, secondStart)
	}
}

func TestChatSearchQueryChangeUpdatesCodeHighlightInSameFrame(t *testing.T) {
	content := "```python\none, two = succ(0), succ(succ(0))\nreturn a if b == 0 else succ(plus(a, b - 1))\n```\n"
	conv := &conversation{ID: "search-code-query-same-frame", Workspace: t.TempDir(), Messages: []map[string]any{searchMessage("assistant", content)}}
	a := &desktop{ready: true, page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}}
	s := &conv.uiState().Search
	s.Open, s.Query = true, "succ(succ(0))"
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.beginChatSearchFrame(conv)
		a.chatSearchBar(c, conv)
		markdown.View(c, content, a.markdownOptions(conv, 0))
	}, 800, 400)
	tester.Frame()
	firstStart := strings.Index(content, "succ(succ(0))")
	if s.blockMatch.Start != firstStart {
		t.Fatalf("initial block match=%+v, want source start %d", s.blockMatch, firstStart)
	}
	if err := tester.Click("在对话中查找"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("else succ(plus(a, b - 1))")
	secondStart := strings.Index(content, "else succ(plus(a, b - 1))")
	if s.Query != "else succ(plus(a, b - 1))" || s.blockMatch.Start != secondStart {
		t.Fatalf("after query replacement query=%q current=%+v want source start %d", s.Query, s.blockMatch, secondStart)
	}
}

func TestChatSearchHighlightPreservesLinkInteraction(t *testing.T) {
	content := "**bold** [needle link](https://go.dev) and `code`"
	conv := &conversation{ID: "search-link", Workspace: t.TempDir(), Messages: []map[string]any{searchMessage("assistant", content)}}
	a := &desktop{ready: true, page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}}
	conv.uiState().Search = chatSearch{Open: true, Query: "needle"}
	a.chatSearchMatches(conv)
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		markdown.View(c, content, a.markdownOptions(conv, 0))
	}, 700, 300)
	if err := tester.Click("needle link"); err != nil {
		t.Fatalf("click search result: %v; rendered text=%q", err, tester.Texts())
	}
	if got := tester.OpenedURLs(); !slices.Equal(got, []string{"https://go.dev"}) {
		t.Fatalf("search-highlighted link opened %q, want https://go.dev", got)
	}
}

func TestChatSearchMarkdownProjectionIncludesTableCells(t *testing.T) {
	src := "| Name | Value |\n| --- | --- |\n| **needle** | [needle label](https://hidden.invalid) |\n"
	projection := chatSearchMarkdownText(src)
	if got := strings.Count(projection.text, "needle"); got != 2 {
		t.Fatalf("table projection contains %d needle occurrences, want 2: %q", got, projection.text)
	}
	conv := searchTranscript(searchMessage("assistant", src))
	s := &chatSearch{Query: "needle", MatchCase: true}
	matches := chatSearchCompute(conv, s)
	s.cache = matches
	if len(matches) != 2 {
		t.Fatalf("table search matches = %+v, want header/body cells", matches)
	}
	for _, match := range matches {
		if got := src[match.Start:match.End]; got != "needle" {
			t.Errorf("match maps to %q, want visible cell text", got)
		}
	}
	doc := parser.Parse(src)
	highlighted := 0
	for i := 0; i < doc.Len(); i++ {
		if doc.Block(i).Kind != parser.TableCell {
			continue
		}
		text, spans, base, ok := chatSearchBlockSpans(doc, i, 0, s)
		if !ok {
			continue
		}
		highlighted++
		if len(spans) != 1 || text[spans[0][0]:spans[0][1]] != "needle" || src[base+spans[0][0]:base+spans[0][1]] != "needle" {
			t.Errorf("table cell %d highlight = %q spans=%v base=%d", i, text, spans, base)
		}
	}
	if highlighted != 2 {
		t.Fatalf("highlighted table cells = %d, want 2", highlighted)
	}
}

func TestChatSearchOmitsMatchesThatCannotBeHighlighted(t *testing.T) {
	for _, tc := range []struct {
		name, content string
	}{
		{"mixed-table", "| value | image |\n| --- | --- |\n| needle | ![alt](image.png) |\n"},
		{"oversized-code", "```\n" + strings.Repeat("needle\n", chatSearchCodeMaxLines+1) + "```\n"},
		{"oversized-code-bytes", "```\n" + strings.Repeat("x", chatSearchCodeMaxBytes) + " needle\n```\n"},
		{"too-many-code-hits", "```\n" + strings.Repeat("needle\n", chatSearchCodeMaxSpans+1) + "```\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &chatSearch{Query: "needle", MatchCase: true}
			matches, incomplete := chatSearchComputeBounded(searchTranscript(searchMessage("assistant", tc.content)), s)
			if len(matches) != 0 || incomplete || !s.highlightSkipped {
				t.Fatalf("matches=%+v incomplete=%v highlightSkipped=%v; unhighlightable results must be omitted and reported distinctly", matches, incomplete, s.highlightSkipped)
			}
		})
	}
}

func TestChatSearchDoesNotCountTruncatedTableOrCodeTail(t *testing.T) {
	for _, tc := range []struct {
		name, content, prefix string
	}{
		{"table", "| A |\n| --- |\n| needle |\n" + strings.Repeat("tail", 32), "| A |\n| --- |\n| needle |\n"},
		{"code", "```go\nneedle\n" + strings.Repeat("tail\n", 32) + "```\n", "```go\nneedle\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.HasPrefix(tc.content, tc.prefix) {
				t.Fatal("bad fixture prefix")
			}
			msg := searchMessage("assistant", tc.content)
			s := &chatSearch{Query: "needle", MatchCase: true}
			matches, incomplete := chatSearchScanMessage(msg, 0, s, len(tc.prefix), nil)
			if len(matches) != 0 || !incomplete {
				t.Fatalf("matches=%+v incomplete=%v; truncated tail should not contribute unhighlightable hits", matches, incomplete)
			}
		})
	}
}

func TestChatSearchHighlightsAndRevealsTableCellMatches(t *testing.T) {
	content := "| Name | Value |\n| --- | --- |\n| alpha | **needle target** |\n| beta | [needle link](https://go.dev) |\n"
	conv := &conversation{ID: "search-table", Workspace: t.TempDir(), Messages: []map[string]any{searchMessage("assistant", content)}}
	a := &desktop{ready: true, page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}}
	s := &conv.uiState().Search
	s.Open, s.Query = true, "needle"
	matches := a.chatSearchMatches(conv)
	if len(matches) != 2 {
		t.Fatalf("table matches = %+v, want two cells", matches)
	}
	options := a.markdownOptions(conv, 0)
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		markdown.View(c, content, options)
	}, 700, 300)
	tester.Frame()
	if s.blockMatch != matches[0] || !s.blockBound {
		t.Fatalf("table hit anchor = %+v bound=%v, want first match %+v", s.blockMatch, s.blockBound, matches[0])
	}
	a.chatSearchStep(conv, 1)
	tester.Frame()
	if s.blockMatch != matches[1] || !s.blockBound {
		t.Fatalf("next table hit anchor = %+v bound=%v, want second match %+v", s.blockMatch, s.blockBound, matches[1])
	}
	texts := strings.Join(tester.Texts(), " ")
	if !strings.Contains(texts, "needle target") || !strings.Contains(texts, "needle link") {
		t.Fatalf("table content missing from rendered text: %q", texts)
	}

}

func TestChatSearchMarkdownProjectionMapsSoftBreak(t *testing.T) {
	src := "alpha\nbeta"
	p := chatSearchMarkdownText(src)
	if p.text != "alpha beta" {
		t.Fatalf("soft break projection = %q, want %q", p.text, "alpha beta")
	}
	start := strings.Index(p.text, "alpha beta")
	gotStart, gotEnd, ok := chatSearchMarkdownSourceRange(p, start, start+len("alpha beta"))
	if !ok || src[gotStart:gotEnd] != src {
		t.Fatalf("cross-break range = [%d:%d] (%q), ok=%v", gotStart, gotEnd, src[gotStart:gotEnd], ok)
	}
}
