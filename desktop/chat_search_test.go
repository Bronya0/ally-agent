// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func searchTranscript(messages ...map[string]any) *conversation {
	return &conversation{ID: "search", Messages: messages}
}

func searchMessage(role, content string) map[string]any {
	return map[string]any{"role": role, "content": content}
}

// The bar searches the questions and the answers alike, in the order they
// appear, so its arrows walk the transcript.
func TestChatSearchFindsPromptsAndAnswers(t *testing.T) {
	t1 := searchTranscript(
		searchMessage("user", "How do I run the tests?"),
		searchMessage("assistant", "Run `go test ./...` to run every test."),
	)
	s := &chatSearch{Query: "run", MatchCase: true}
	matches := chatSearchCompute(t1, s)
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2: %+v", len(matches), matches)
	}
	if matches[0].Message != 0 || matches[1].Message != 1 {
		t.Fatalf("matches point at messages %d, %d, want 0, 1", matches[0].Message, matches[1].Message)
	}
}

func TestChatSearchCaseAndWholeWord(t *testing.T) {
	conv := searchTranscript(searchMessage("user", "Run the runner, run."))
	cases := []struct {
		name  string
		state chatSearch
		want  int
	}{
		{"case sensitive", chatSearch{Query: "Run", MatchCase: true}, 1},
		{"case insensitive", chatSearch{Query: "Run"}, 3},
		// Whole words drop the "Run" inside "runner", whatever the case rules.
		{"whole words only", chatSearch{Query: "Run", WholeWord: true}, 2},
		{"whole word excludes runner", chatSearch{Query: "run", WholeWord: true}, 2},
	}
	for _, tc := range cases {
		if got := len(chatSearchCompute(conv, &tc.state)); got != tc.want {
			t.Errorf("%s: matches = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestChatSearchRegularExpression(t *testing.T) {
	conv := searchTranscript(searchMessage("assistant", "alpha beta gamma"))
	s := &chatSearch{Query: `a\w+a`, Regexp: true}
	// "alpha" and "gamma" hold an a...a run; "beta" does not.
	if got := len(chatSearchCompute(conv, s)); got != 2 {
		t.Fatalf("regexp matches = %d, want 2", got)
	}
	invalid := &chatSearch{Query: "a(", Regexp: true}
	if _, err := invalid.chatSearchPattern(); err == nil {
		t.Fatal("an unbalanced group must not compile")
	}
	if got := len(chatSearchCompute(conv, invalid)); got != 0 {
		t.Fatalf("invalid regexp found %d matches", got)
	}
}

// Enter and the arrows walk the hits and wrap around; the memo keeps a second
// frame from rescanning the transcript.
func TestChatSearchStepsWrapAndMemoise(t *testing.T) {
	conv := searchTranscript(
		searchMessage("user", "one two"),
		searchMessage("assistant", "two three"),
	)
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	st := conv.uiState()
	st.Search = chatSearch{Open: true, Query: "two"}
	matches := a.chatSearchMatches(conv)
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(matches))
	}
	first := st.Search.cache
	if again := a.chatSearchMatches(conv); &again[0] != &first[0] {
		t.Fatal("a second frame rescanned the transcript instead of reusing the memo")
	}
	a.chatSearchStep(conv, 1)
	if st.Search.Current != 1 {
		t.Fatalf("current = %d, want 1", st.Search.Current)
	}
	a.chatSearchStep(conv, 1)
	if st.Search.Current != 0 {
		t.Fatalf("current = %d, want a wrap to 0", st.Search.Current)
	}
	a.chatSearchStep(conv, -1)
	if st.Search.Current != 1 {
		t.Fatalf("current = %d, want a wrap back to 1", st.Search.Current)
	}
	if status := a.chatSearchStatus(conv); status != "2 of 2" {
		t.Fatalf("status = %q, want 2 of 2", status)
	}
}

func TestChatSearchScanBudgetIsVisible(t *testing.T) {
	conv := searchTranscript(searchMessage("assistant", strings.Repeat("x", chatSearchMaxBytes+32)+"needle"))
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	conv.uiState().Search = chatSearch{Open: true, Query: "needle"}
	if got := len(a.chatSearchMatches(conv)); got != 0 {
		t.Fatalf("matches after the scan budget = %d, want 0", got)
	}
	if got := a.chatSearchState(conv); got != chatSearchNoHit {
		t.Fatalf("state = %v, want no hit", got)
	}
	if !conv.uiState().Search.scanTruncated {
		t.Fatal("the budget cut the scan short without saying so")
	}
	conv.Messages[0]["content"] = "needle"
	invalidateChatSearch(conv)
	conv.uiState().Search.lastCompute = time.Now().Add(-chatSearchRestream)
	if got := len(a.chatSearchMatches(conv)); got != 1 {
		t.Fatalf("matches in a new, short transcript = %d, want 1", got)
	}
	if conv.uiState().Search.scanTruncated {
		t.Fatal("a short transcript reported a truncated scan")
	}
}

func TestChatSearchCacheInvalidatesOnSameLengthEdit(t *testing.T) {
	conv := searchTranscript(searchMessage("assistant", "needle"))
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	search := &conv.uiState().Search
	search.Open, search.Query = true, "needle"
	if got := len(a.chatSearchMatches(conv)); got != 1 {
		t.Fatalf("initial matches = %d, want 1", got)
	}
	conv.Messages[0]["content"] = "xxxxxx"
	invalidateChatSearch(conv)
	search.frameKey = ""
	search.lastCompute = time.Now().Add(-chatSearchRestream)
	if got := len(a.chatSearchMatches(conv)); got != 0 {
		t.Fatalf("matches after same-length replacement = %d, want 0", got)
	}
}

func TestChatSearchRestreamThrottleSurvivesInvalidation(t *testing.T) {
	conv := searchTranscript(searchMessage("assistant", "needle"))
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	search := &conv.uiState().Search
	search.Open, search.Query = true, "needle"
	if got := len(a.chatSearchMatches(conv)); got != 1 {
		t.Fatalf("initial matches = %d, want 1", got)
	}
	conv.Messages[0]["content"] = "xxxxxx"
	invalidateChatSearch(conv)
	if got := len(a.chatSearchMatches(conv)); got != 1 {
		t.Fatalf("immediate streaming matches = %d, want prior cached result during throttle", got)
	}
	search.lastCompute = time.Now().Add(-chatSearchRestream)
	search.frameKey = ""
	if got := len(a.chatSearchMatches(conv)); got != 0 {
		t.Fatalf("matches after throttle expires = %d, want 0", got)
	}
}

// A reply that keeps growing changes only its last message, so the frames of one
// stream rescan that message instead of the whole transcript, while the hits of
// the earlier messages stay.
func TestChatSearchTailGrowthRespectsBudgetAfterEmptyTail(t *testing.T) {
	conv := searchTranscript(
		searchMessage("user", strings.Repeat("x", chatSearchMaxBytes-16)),
		searchMessage("assistant", ""),
	)
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	s := &conv.uiState().Search
	s.Open, s.Query = true, "needle"
	if got := len(a.chatSearchMatches(conv)); got != 0 {
		t.Fatalf("initial matches = %d, want 0", got)
	}
	if s.scanPrefix != chatSearchMaxBytes-16 {
		t.Fatalf("scan prefix = %d, want %d", s.scanPrefix, chatSearchMaxBytes-16)
	}
	conv.Messages[1]["content"] = strings.Repeat("y", 16) + "needle"
	invalidateChatSearchTail(conv)
	s.lastCompute = time.Now().Add(-chatSearchRestream)
	if got := len(a.chatSearchMatches(conv)); got != 0 {
		t.Fatalf("matches beyond the transcript byte budget = %d, want 0", got)
	}
	if !s.scanTruncated {
		t.Fatal("tail growth beyond the remaining budget was not reported as truncated")
	}
}

func TestChatSearchRescansOnlyTheGrowingTail(t *testing.T) {
	conv := searchTranscript(
		searchMessage("user", "needle one"),
		searchMessage("assistant", "needle two"),
	)
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	s := &conv.uiState().Search
	s.Open, s.Query = true, "needle"
	if got := len(a.chatSearchMatches(conv)); got != 2 {
		t.Fatalf("matches = %d, want 2", got)
	}
	if s.scanCount != 2 {
		t.Fatalf("scan covered %d messages, want 2", s.scanCount)
	}
	// The stream grows the last message: only that one is read again.
	conv.Messages[1]["content"] = "needle two needle three"
	invalidateChatSearchTail(conv)
	s.lastCompute = time.Now().Add(-chatSearchRestream)
	matches := a.chatSearchMatches(conv)
	if len(matches) != 3 {
		t.Fatalf("matches after the tail grew = %d, want 3", len(matches))
	}
	if matches[0].Message != 0 {
		t.Fatalf("the first message's hit was dropped: %+v", matches[0])
	}
	// A change that moves the shape of the transcript is rescanned whole.
	conv.Messages = append(conv.Messages, searchMessage("user", "and needle four"))
	invalidateChatSearch(conv)
	s.lastCompute = time.Now().Add(-chatSearchRestream)
	if got := len(a.chatSearchMatches(conv)); got != 4 {
		t.Fatalf("matches after an append = %d, want 4", got)
	}
}

func TestChatSearchStateWithoutQueryOrRegexp(t *testing.T) {
	conv := searchTranscript(searchMessage("user", "hello"))
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	st := conv.uiState()
	st.Search = chatSearch{Open: true}
	if got := a.chatSearchState(conv); got != chatSearchIdle {
		t.Fatalf("state = %v, want idle without a query", got)
	}
	st.Search = chatSearch{Open: true, Query: "zzz"}
	if got := a.chatSearchState(conv); got != chatSearchNoHit {
		t.Fatalf("state = %v, want no hit", got)
	}
	st.Search = chatSearch{Open: true, Query: "a(", Regexp: true}
	if got := a.chatSearchState(conv); got != chatSearchBadPattern {
		t.Fatalf("state = %v, want a bad pattern distinct from no hit", got)
	}
}

// A block that holds a hit is drawn by the bar as highlighted text; a block
// without one, and a code block, keep the Markdown renderer's own layout.
func TestChatSearchHighlightsMatchedBlocks(t *testing.T) {
	src := "plain paragraph\n\n# a needle heading\n\n```go\nneedle := 1\n```\n"
	doc := parser.Parse(src)
	s := &chatSearch{Open: true, Query: "needle"}
	s.cache = chatSearchCompute(searchTranscript(searchMessage("assistant", src)), s)
	var drawn []int
	for i := 0; i < doc.Len(); i++ {
		if text, spans, base, ok := chatSearchBlockSpans(doc, i, 0, s); ok {
			drawn = append(drawn, i)
			if text != "a needle heading" || len(spans) != 1 {
				t.Fatalf("block %d = %q with %d spans", i, text, len(spans))
			}
			// The base must land on the run the search computed over the whole
			// message, or the pointed hit is never recognised as current.
			if want := strings.Index(src, "needle"); base+spans[0][0] != want {
				t.Fatalf("heading run starts at %d, want %d", base+spans[0][0], want)
			}
		}
	}
	if len(drawn) != 1 {
		t.Fatalf("blocks drawn by the bar = %v, want just the heading: a code block keeps its own drawing", drawn)
	}
	if kind := doc.Block(drawn[0]).Kind; kind != parser.Heading {
		t.Fatalf("block %d is kind %v, want the heading", drawn[0], kind)
	}
	if _, _, _, ok := chatSearchBlockSpans(doc, 0, 0, s); ok {
		t.Fatal("a paragraph without the query must keep the renderer's layout")
	}
	formatted := parser.Parse("**needle** in bold")
	for i := 0; i < formatted.Len(); i++ {
		if _, _, _, ok := chatSearchBlockSpans(formatted, i, 0, s); ok {
			t.Fatal("formatted Markdown must keep the renderer's layout rather than expose source syntax")
		}
	}
}

// List items and quotes hold paragraphs, so their text is marked too while the
// list's own marker and indentation stay in place.
func TestChatSearchHighlightsListAndQuoteText(t *testing.T) {
	src := "- a needle item\n\n> quoted needle\n"
	doc := parser.Parse(src)
	s := &chatSearch{Open: true, Query: "needle"}
	s.cache = chatSearchCompute(searchTranscript(searchMessage("assistant", src)), s)
	marked := 0
	for i := 0; i < doc.Len(); i++ {
		text, spans, base, ok := chatSearchBlockSpans(doc, i, 0, s)
		if !ok {
			continue
		}
		marked++
		if len(spans) != 1 || text[spans[0][0]:spans[0][1]] != "needle" {
			t.Fatalf("block %d = %q spans %v", i, text, spans)
		}
		if want := strings.Index(src, "needle"); base+spans[0][0] != want && marked == 1 {
			t.Fatalf("first run starts at %d, want %d", base+spans[0][0], want)
		}
	}
	if marked != 2 {
		t.Fatalf("marked %d blocks, want the list item and the quote", marked)
	}
}

// A block's runs are reported with the offset of the block's text in the
// message, which is what tells the run the bar points at from the others.
func TestChatSearchBlockSpansCarryTheirMessageOffset(t *testing.T) {
	src := "intro \n\nneedle here"
	doc := parser.Parse(src)
	s := &chatSearch{Open: true, Query: "needle"}
	s.cache = chatSearchCompute(searchTranscript(searchMessage("assistant", src)), s)
	found := false
	for i := 0; i < doc.Len(); i++ {
		text, spans, base, ok := chatSearchBlockSpans(doc, i, 0, s)
		if !ok {
			continue
		}
		found = true
		if text != "needle here" || len(spans) != 1 || spans[0][0] != 0 {
			t.Fatalf("text = %q spans = %v", text, spans)
		}
		if src[base+spans[0][0]:] != text {
			t.Fatalf("base %d does not point at %q in the message", base, text)
		}
	}
	if !found {
		t.Fatal("no block was claimed for the query")
	}
}

// The bar's pointer selects one run of one message, which is the one drawn solid.
func TestChatSearchCurrentPicksTheSelectedRun(t *testing.T) {
	conv := searchTranscript(
		searchMessage("user", "needle and needle"),
		searchMessage("assistant", "another needle"),
	)
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	conv.uiState().Search = chatSearch{Open: true, Query: "needle"}
	first, ok := a.chatSearchCurrent(conv)
	if !ok || first.Message != 0 || first.Start != 0 {
		t.Fatalf("current = %+v, want the first run of the first message", first)
	}
	a.chatSearchStep(conv, 1)
	second, _ := a.chatSearchCurrent(conv)
	if second.Message != 0 || second.Start <= first.Start {
		t.Fatalf("current = %+v, want the second run of the same message", second)
	}
	a.chatSearchStep(conv, 1)
	third, _ := a.chatSearchCurrent(conv)
	if third.Message != 1 {
		t.Fatalf("current = %+v, want the run in the second message", third)
	}
}

// Ctrl+F opens the bar on the chat page, and Escape closes it again with the
// caret back in the composer.
// A case-insensitive match used to index a lowercased copy, whose length differs
// for a few runes from the original: the spans must address the original text.
func TestChatSearchCaseInsensitiveKeepsOriginalOffsets(t *testing.T) {
	cases := []struct {
		name, text, query string
		wantStart         int
	}{
		// U+0130 lowercases into a longer rune.
		{"dotted I", "\u0130x", "x", len("\u0130")},
		// U+023A lowercases from two bytes to three: its end used to pass the text.
		{"long lowercase", "\u023ax", "x", len("\u023a")},
		{"plain", "aXa", "xa", 1},
	}
	for _, tc := range cases {
		s := &chatSearch{Query: tc.query}
		spans := chatSearchSpans(tc.text, s, nil, chatSearchMaxMatches)
		if len(spans) != 1 {
			t.Fatalf("%s: spans = %v, want one", tc.name, spans)
		}
		if spans[0][0] != tc.wantStart || spans[0][1] > len(tc.text) {
			t.Fatalf("%s: span = %v, want start %d within %q", tc.name, spans[0], tc.wantStart, tc.text)
		}
		if got := tc.text[spans[0][0]:spans[0][1]]; !strings.EqualFold(got, tc.query) {
			t.Fatalf("%s: span covers %q, which does not match %q", tc.name, got, tc.query)
		}
	}
}

// The block offset must skip exactly the space the text dropped, whatever kind of
// space opens the block.
func TestChatSearchBlockBaseSkipsTheSameSpace(t *testing.T) {
	src := "\u00a0needle here"
	doc := parser.Parse(src)
	s := &chatSearch{Query: "needle"}
	s.cache = chatSearchCompute(searchTranscript(searchMessage("assistant", src)), s)
	for i := 0; i < doc.Len(); i++ {
		text, spans, base, ok := chatSearchBlockSpans(doc, i, 0, s)
		if !ok {
			continue
		}
		if text != "needle here" || spans[0][0] != 0 {
			t.Fatalf("text = %q spans = %v", text, spans)
		}
		if !strings.HasPrefix(src[base:], text) {
			t.Fatalf("base %d does not point at %q", base, text)
		}
		return
	}
	t.Fatal("no block was claimed for the query")
}

// The cap must not read as a total: "400 of 400" would claim the transcript ends
// there.
func TestChatSearchSpanCollectionHonorsLimit(t *testing.T) {
	s := &chatSearch{Query: "a"}
	text := strings.Repeat("a", chatSearchMaxMatches*10)
	spans := chatSearchSpans(text, s, nil, 7)
	if len(spans) != 7 {
		t.Fatalf("collected %d spans, want bounded limit 7", len(spans))
	}
	regexpSearch := &chatSearch{Query: "a|", Regexp: true}
	re, err := regexpSearch.chatSearchPattern()
	if err != nil {
		t.Fatal(err)
	}
	spans = chatSearchSpans(text, regexpSearch, re, 5)
	if len(spans) != 5 {
		t.Fatalf("regexp collected %d non-empty spans, want limit 5", len(spans))
	}
}

func TestChatSearchStatusMarksTheHitCap(t *testing.T) {
	var messages []map[string]any
	for i := 0; i < chatSearchMaxMatches+10; i++ {
		messages = append(messages, searchMessage("user", "needle"))
	}
	conv := searchTranscript(messages...)
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	conv.uiState().Search = chatSearch{Open: true, Query: "needle"}
	if status := a.chatSearchStatus(conv); status != "1 of 400+" {
		t.Fatalf("status = %q, want the cap marked", status)
	}
}

// Closing the bar releases what it cached, and reopening searches again rather
// than reusing the released state.
func TestCloseChatSearchReleasesItsCaches(t *testing.T) {
	conv := searchTranscript(searchMessage("assistant", "needle"))
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	conv.uiState().Search = chatSearch{Open: true, Query: "needle"}
	if got := len(a.chatSearchMatches(conv)); got != 1 {
		t.Fatalf("matches = %d, want 1", got)
	}
	a.closeChatSearch(conv)
	st := &conv.uiState().Search
	if st.cache != nil || st.byMessage != nil || st.spanCache != nil || st.codeCache != nil || st.pattern != nil || st.literal != nil || st.literalPrefix != nil || st.literalStarts != nil {
		t.Fatalf("closing kept caches: %+v", st)
	}
	st.Open = true
	if got := len(a.chatSearchMatches(conv)); got != 1 {
		t.Fatalf("matches after reopening = %d, want 1", got)
	}
}

// Escape must still cancel a run while the bar is closed: the window keeps only
// the first handler of a chord, so the bar's Escape has to forward it.
func TestEscapeCancelsRunWhenSearchClosed(t *testing.T) {
	conv := &conversation{ID: "esc-cancel", Workspace: t.TempDir()}
	conv.Busy = true
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.globalShortcuts(c)
		a.composer(c, conv)
	}, 800, 400)
	tester.Key(0, ui.KeyEscape)
	if !conv.CancelRequested {
		t.Fatal("Escape no longer cancels the running reply")
	}
}

// Escape closes the bar first when it is open, without cancelling the run.
func TestEscapeClosesSearchBeforeCancelling(t *testing.T) {
	conv := &conversation{ID: "esc-search", Workspace: t.TempDir()}
	conv.Busy = true
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	conv.uiState().Search.Open = true
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.globalShortcuts(c)
		a.composer(c, conv)
	}, 800, 400)
	tester.Key(0, ui.KeyEscape)
	if conv.uiState().Search.Open {
		t.Fatal("Escape did not close the find bar")
	}
	if conv.CancelRequested {
		t.Fatal("closing the bar also cancelled the run")
	}
}

// A conversation can arrive with its Markdown options already started, as a new
// session creates them; the per-message code hooks must be started as well rather
// than written to a nil map, which panicked the frame after a session switch.
func TestMarkdownOptionsStartsWithExistingMap(t *testing.T) {
	conv := &conversation{ID: "fresh-map", Workspace: t.TempDir(),
		MD:       map[int]*markdown.Options{},
		Messages: []map[string]any{searchMessage("assistant", "```go\nx := 1\n```\n")}}
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	if options := a.markdownOptions(conv, 0); options == nil || conv.MDCodeBlock[0] == nil {
		t.Fatal("the theme's code-block hook was not recorded")
	}
	// The bar's wrapper needs that recorded hook on every later frame too.
	conv.uiState().Search = chatSearch{Open: true, Query: "x"}
	if options := a.markdownOptions(conv, 0); options.CodeBlock == nil {
		t.Fatal("the code block lost its hook while the bar was open")
	}
}

// A code block's runs are reported per line, with the offset of each line in the
// message so the pointed hit can be told from the others.
func TestChatSearchCodeLinesReportsRunsAndOffsets(t *testing.T) {
	src := "intro\n\n```go\nx := needle\n// needle here\n```\n"
	doc := parser.Parse(src)
	index := -1
	for i := 0; i < doc.Len(); i++ {
		if doc.Block(i).Kind == parser.CodeBlock {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("no code block was parsed")
	}
	s := &chatSearch{Open: true, Query: "needle", frameDoc: doc}
	// The bar collects the message's hits before it draws: the block reads them to
	// skip a code block that holds none.
	s.cache = chatSearchCompute(&conversation{Messages: []map[string]any{searchMessage("assistant", src)}}, s)
	lines, starts, spans, ok := chatSearchCodeLines(s, 0, &markdown.CodeBlockView{Index: index})
	if !ok || len(lines) != 2 || len(spans) != 2 || len(starts) != 2 {
		t.Fatalf("lines=%q spans=%v ok=%v", lines, spans, ok)
	}
	if spans[0][0][0] != len("x := ") || spans[1][0][0] != len("// ") {
		t.Fatalf("spans = %v, want the run after each prefix", spans)
	}
	if !strings.HasPrefix(src[starts[1]+spans[1][0][0]:], "needle here") {
		t.Fatalf("line 1 start %d does not address its run in the message", starts[1])
	}
}

// A line the parser padded for a tab it consumed must still report its source
// offset, or the run the bar points at is looked for in the wrong place.
func TestChatSearchCodePadCountsOnlyPadding(t *testing.T) {
	src := "```\n\tif x {\n```\n"
	at := strings.Index(src, "if x {\n")
	if got := chatSearchCodePad(src, "if x {", at); got != 0 {
		t.Fatalf("pad = %d, want 0 for an unpadded line", got)
	}
	if got := chatSearchCodePad(src, "    if x {", at); got != 4 {
		t.Fatalf("pad = %d, want the four spaces the source does not hold", got)
	}
	if got := chatSearchCodePad(src, "something else", at); got != 0 {
		t.Fatalf("pad = %d, want 0 when the line is not the source's", got)
	}
}

// A block the bar should not redraw keeps the renderer's own drawing: no hit,
// another message, or a block too large to rescan every frame.
func TestChatSearchCodeLinesDeclinesWhatItCannotDraw(t *testing.T) {
	doc := parser.Parse("```go\nx := needle\n```\n")
	index := 1
	if doc.Block(index).Kind != parser.CodeBlock {
		t.Fatalf("block %d is %v", index, doc.Block(index).Kind)
	}
	block := &markdown.CodeBlockView{Index: index}
	if _, _, _, ok := chatSearchCodeLines(&chatSearch{Open: true, Query: "zzz", frameDoc: doc}, 0, block); ok {
		t.Fatal("a block without the query must keep the renderer's drawing")
	}
	// A hit in the prose of the same message is not a hit in this block.
	prose := parser.Parse("a needle mentions code\n\n```go\nx := 1\n```\n")
	s := &chatSearch{Open: true, Query: "needle", frameDoc: prose}
	s.cache = []chatSearchMatch{{Message: 0, Start: strings.Index(prose.Source(), "needle")}}
	if _, _, _, ok := chatSearchCodeLines(s, 0, &markdown.CodeBlockView{Index: 3}); ok {
		t.Fatal("a block that holds no hit must keep the renderer's drawing")
	}
	if _, _, _, ok := chatSearchCodeLines(&chatSearch{Open: true, Query: "needle", frameDoc: doc}, 1, block); ok {
		t.Fatal("a document of another message must not be used")
	}
	if _, _, _, ok := chatSearchCodeLines(&chatSearch{Open: true, Query: "needle"}, 0, block); ok {
		t.Fatal("a block without a document must keep the renderer's drawing")
	}
	big := parser.Parse("```\n" + strings.Repeat("a", chatSearchCodeMaxBytes+1) + "\n```\n")
	bigSearch := &chatSearch{Open: true, Query: "a", frameDoc: big}
	bigSearch.cache = []chatSearchMatch{{Message: 0, Start: 4, End: 5}}
	if _, _, _, ok := chatSearchCodeLines(bigSearch, 0, &markdown.CodeBlockView{Index: 1}); ok {
		t.Fatal("a block past the byte cap must keep the renderer's drawing")
	}
}

// Beyond the highlight cap the original renderer must keep the whole block.
func TestChatSearchCodeLinesFallsBackPastTheRunCap(t *testing.T) {
	var src strings.Builder
	src.WriteString("```\n")
	for i := 0; i < chatSearchCodeMaxSpans+20; i++ {
		src.WriteString("a\n")
	}
	src.WriteString("```\n")
	doc := parser.Parse(src.String())
	s := &chatSearch{Open: true, Query: "a", frameDoc: doc}
	s.cache = chatSearchCompute(searchTranscript(searchMessage("assistant", src.String())), s)
	if _, _, _, ok := chatSearchCodeLines(s, 0, &markdown.CodeBlockView{Index: 1}); ok {
		t.Fatal("a block past the highlight cap must keep its native renderer")
	}
}

// A wrapper installed by one frame must not wrap the wrapper of the frame before:
// markdownOptions runs every frame, so a chain would grow for as long as the bar
// stays open and keep costing once it closed.
func TestChatSearchCodeHookDoesNotNestAcrossFrames(t *testing.T) {
	content := "the needle\n\n```go\nx := 1\n```\n"
	conv := &conversation{ID: "hook-depth", Workspace: t.TempDir(),
		Messages: []map[string]any{searchMessage("assistant", content)}}
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	conv.uiState().Search = chatSearch{Open: true, Query: "needle"}
	if options := a.markdownOptions(conv, 0); options == nil {
		t.Fatal("no options were built")
	}
	calls := 0
	conv.MDCodeBlock[0] = func(c *ui.Context, block *markdown.CodeBlockView) { calls++ }
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		markdown.View(c, content, a.markdownOptions(conv, 0))
	}, 700, 500)
	for i := 0; i < 3; i++ {
		tester.Frame()
	}
	// A chain would make each frame fall back once per wrapper installed before it,
	// so the cost of a later batch of frames would exceed an earlier one.
	before := calls
	for i := 0; i < 3; i++ {
		tester.Frame()
	}
	first := calls - before
	before = calls
	for i := 0; i < 3; i++ {
		tester.Frame()
	}
	if second := calls - before; second != first {
		t.Fatalf("fallbacks per 3 frames went from %d to %d: the hook was wrapped again every frame", first, second)
	} else if first == 0 {
		t.Fatal("the theme's own hook never ran, so the wrapper was not built around it")
	}
	a.closeChatSearch(conv)
	if got, want := reflect.ValueOf(a.markdownOptions(conv, 0).CodeBlock).Pointer(), reflect.ValueOf(conv.MDCodeBlock[0]).Pointer(); got != want {
		t.Fatal("closing the bar did not restore the theme's own code-block hook")
	}
}

// While the bar is open, a code block that holds a hit is drawn by the bar with
// the run marked; the message still renders, and a block without one falls back
// to the renderer.
func TestChatSearchDrawsCodeBlocksWithHits(t *testing.T) {
	content := "run this:\n\n```go\nx := needle\n```\n"
	conv := &conversation{ID: "code-search", Workspace: t.TempDir(),
		Messages: []map[string]any{searchMessage("assistant", content)}}
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	conv.uiState().Search = chatSearch{Open: true, Query: "needle"}
	for _, tc := range []struct {
		query string
		draw  bool
	}{{"needle", true}, {"zzz", false}} {
		conv.uiState().Search.Query = tc.query
		invalidateChatSearch(conv)
		drew := false
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			options := a.markdownOptions(conv, 0)
			inner := options.CodeBlock
			options.CodeBlock = func(ctx *ui.Context, block *markdown.CodeBlockView) {
				if a.chatSearchCodeMark(ctx, conv, 0, block) {
					drew = true
					return
				}
				inner(ctx, block)
			}
			markdown.View(c, content, options)
		}, 700, 500)
		tester.Frame()
		if drew != tc.draw {
			t.Fatalf("query %q: code block drawn = %v, want %v", tc.query, drew, tc.draw)
		}
		s := &conv.uiState().Search
		if tc.draw {
			// The line holding the hit is the anchor the jump scrolls to, so the bar
			// lands on that line rather than the top of the block.
			if !s.blockBound || s.blockHandle == nil || s.blockMatch.Message != 0 {
				t.Fatalf("query %q: the drawn block bound no anchor", tc.query)
			}
			if s.blockFrac <= 0 {
				t.Fatalf("query %q: block fraction = %v, want the run's place in its line", tc.query, s.blockFrac)
			}
		} else if s.blockBound {
			t.Fatalf("query %q: a block without a hit bound an anchor", tc.query)
		}
		s.blockBound, s.blockHandle = false, nil
	}
}

// Two messages whose blocks start at the same offsets must not share cached
// spans: the second one would be sliced with the first one's offsets.
func TestChatSearchBlockSpansCacheIsPerMessage(t *testing.T) {
	s := &chatSearch{Query: "needle"}
	first := parser.Parse("needle")
	second := parser.Parse("needle and a longer tail")
	s.cache = chatSearchCompute(searchTranscript(searchMessage("assistant", first.Source()), searchMessage("assistant", second.Source())), s)
	for i := 0; i < first.Len(); i++ {
		if _, _, _, ok := chatSearchBlockSpans(first, i, 0, s); ok {
			break
		}
	}
	for i := 0; i < second.Len(); i++ {
		text, spans, _, ok := chatSearchBlockSpans(second, i, 1, s)
		if !ok {
			continue
		}
		if spans[0][1] > len(text) {
			t.Fatalf("spans %v do not fit %q", spans, text)
		}
		return
	}
	t.Fatal("the second message's block was not claimed")
}

func TestChatSearchOpensOnShortcutAndCloses(t *testing.T) {
	workspace := t.TempDir()
	conv := &conversation{ID: "search-ui", Workspace: workspace, Messages: []map[string]any{
		{"role": "user", "content": "find me"},
		{"role": "assistant", "content": "found it"},
	}}
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{conv}}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.globalShortcuts(c)
		if conv.uiState().Search.Open {
			a.chatSearchBar(c, conv)
		}
		a.composer(c, conv)
	}, 900, 500)
	tester.Key(ui.Cmd, ui.KeyF)
	if !conv.uiState().Search.Open {
		t.Fatal("Ctrl+F did not open the transcript search")
	}
	if a.chatSearchFocus != conv.ID+"-search-query" {
		t.Fatalf("focus = %q, want the search field", a.chatSearchFocus)
	}
	tester.Type("find")
	if status := a.chatSearchStatus(conv); status != "1 of 1" {
		t.Fatalf("status = %q, want 1 of 1 after typing", status)
	}
	tester.Key(0, ui.KeyEscape)
	if conv.uiState().Search.Open {
		t.Fatal("Escape did not close the transcript search")
	}
	// The composer takes the caret back: the bar released its own field, and the
	// engine's map order must not leave the window with no focus.
	tester.Frame()
	if a.composerFocus != conv.ID || a.focusBack != "" {
		t.Fatalf("composer focus = %q (pending %q), want the conversation back", a.composerFocus, a.focusBack)
	}
}
