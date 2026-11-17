// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
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
