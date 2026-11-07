// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// chatSearch is the transcript's find bar: the editor's bar without replacing,
// searching the messages' text instead of a buffer. The matches are message
// ranges, so a hit can be counted, listed and scrolled to, but not painted
// inside the rendered Markdown, which has no highlight hook.
type chatSearch struct {
	Open      bool
	Query     string
	MatchCase bool
	WholeWord bool
	Regexp    bool
	// Current indexes the match the arrows and Enter selected.
	Current int
	// reveal asks the next frame to scroll the current match into view.
	reveal bool
	// handles anchor the messages that drew this frame, so a match can be
	// scrolled to by its bounds; blockHandle anchors the block that holds the
	// match the bar points at, which is closer than the message when a long
	// message hides the hit below its own first screen.
	handles     map[int]*ui.Handle
	blockHandle *ui.Handle
	// blockMatch is the hit blockHandle was bound for, so a handle left over from
	// the previous hit never scrolls to the wrong place.
	blockMatch chatSearchMatch
	blockBound bool
	// blockFrac is where the pointed run sits in its block, as a fraction of the
	// block's text, so the scroll can reach the line without a per-line layout.
	blockFrac float32
	// byMessage answers "does this message hold a hit" in constant time: the
	// transcript asks it for every visible message on every frame.
	byMessage map[int]bool
	// pattern memoises the compiled query and the spans already found in a block:
	// both were recomputed for every block of every frame while the bar was open.
	patternKey string
	pattern    *regexp.Regexp
	patternErr error
	// spanCache and codeCache memoise what a block of one message resolved to, and
	// cacheQuery says which query they were filled for: a comparable key keeps the
	// frame from formatting a string per block, and one generation field replaces
	// the query inside it.
	spanCache  map[chatSearchBlockKey][][2]int
	codeCache  map[chatSearchCodeKey]chatSearchCode
	cacheQuery string
	// lastQuery and lastCompute tell a new query, which is searched at once, from
	// a transcript still streaming, which is re-searched at most this often.
	lastQuery   string
	lastCompute time.Time
	// frameKey is the transcript key of the frame being built, so the many callers
	// that ask about hits do not each walk every message again.
	frameKey string
	// transcriptRevision changes only at message mutation sites, keeping the
	// unchanged-frame cache check constant-time.
	transcriptRevision uint64
	// waited counts the frames a reveal spent waiting for the layout of the hit it
	// scrolls to, and waitedWindow those spent opening the page the hit is in. Both
	// are bounded: a hit in a row the transcript does not draw would otherwise keep
	// asking for frames forever.
	waited, waitedWindow int
	cache                []chatSearchMatch
	cacheKey             string
	// scanTruncated means the synchronous scan hit its byte budget before the end
	// of the transcript; the status must not present a partial search as complete.
	scanTruncated bool
	// scanCount, scanPrefix and scanBytes describe the scan behind cache: how many
	// messages it covered, the bytes before the last of them, and the bytes in all.
	// A reply that keeps growing edits only its last message, so the frames during
	// one stream rescan that message instead of the whole transcript.
	scanCount  int
	scanPrefix int
	scanBytes  int
	tailOnly   bool
	// frameDoc and frameDocMessage are the document and message the block hook
	// was last asked about, so the code-block hook that runs right after it can
	// address the block's lines without parsing the message again.
	frameDoc        *parser.Document
	frameDocMessage int
}

// chatSearchMatch is one hit: the message it is in and the byte range of the
// matched text within that message.
type chatSearchMatch struct {
	Message    int
	Start, End int
}

// chatSearchMaxMatches and chatSearchMaxBytes bound a query's synchronous scan.
const (
	chatSearchMaxMatches = 400
	chatSearchMaxBytes   = 4 << 20
)

// chatSearchWaitFrames bounds how many frames a jump waits for the layout of the
// row or block it scrolls to.
const chatSearchWaitFrames = 3

// chatSearchNextFrame is how soon the bar asks for the frame that lays out the
// highlight it is about to scroll to.
const chatSearchNextFrame = 8 * time.Millisecond

// chatSearchCodeMaxBytes and chatSearchCodeMaxSpans bound the work a code block
// drawn by the bar can ask of one frame: past them the block keeps the renderer's
// own coloured drawing, and its hits stay findable without a mark.
const (
	chatSearchCodeMaxBytes = 64 << 10
	chatSearchCodeMaxSpans = 200
)

// openChatSearch opens the bar and puts the caret in its field.
func (a *desktop) openChatSearch(t *conversation) {
	if t == nil || a.closing || a.page != "chat" {
		return
	}
	st := t.uiState()
	st.Search.Open = true
	a.chatSearchFocus = t.ID + "-search-query"
	// The composer has to look unfocused now, or closing the bar could not give its
	// focus back: the engine re-focuses a bound field when its value changes.
	a.composerFocus, a.focusBack = "", ""
	st.Search.reveal, st.Search.waited = true, 0
}

func (a *desktop) closeChatSearch(t *conversation) {
	if t == nil {
		return
	}
	st := t.uiState()
	s := &st.Search
	s.Open = false
	s.reveal = false
	// The bar's per-frame caches describe a transcript that is no longer searched.
	// The handles stay: they are pruned by the transcript's window every frame, and
	// a handle handed back to the engine is never reclaimed before the window close.
	s.blockBound = false
	s.spanCache, s.byMessage, s.cache, s.cacheKey = nil, nil, nil, ""
	s.codeCache = nil
	s.frameDoc = nil
	a.chatSearchFocus = ""
	// Both focus fields change in the frame the bar closes, and the engine walks
	// them in map order: releasing the bar's field can clear the focus the composer
	// was granted in the same pass. The composer asks for the caret again in the
	// next frame, when that release is behind it.
	a.composerFocus, a.focusBack = "", t.ID
}

// chatSearchStep moves to the next or previous hit, round the ends.
func (a *desktop) chatSearchStep(t *conversation, delta int) {
	if t == nil {
		return
	}
	s := &t.uiState().Search
	matches := a.chatSearchMatches(t)
	if len(matches) == 0 {
		return
	}
	if s.Current < 0 || s.Current >= len(matches) {
		s.Current = 0
	} else {
		s.Current = (s.Current + delta + len(matches)) % len(matches)
	}
	s.reveal, s.waited = true, 0
}

// chatSearchMatches returns the hits of the bar's query, memoised by the query
// and the shape of the transcript.
func (a *desktop) chatSearchMatches(t *conversation) []chatSearchMatch {
	s := &t.uiState().Search
	key := s.frameKey
	if key == "" {
		key = chatSearchCacheKey(t, s)
		s.frameKey = key
	}
	if key == s.cacheKey {
		return s.cache
	}
	// A query the user is editing is searched at once; a reply still streaming
	// changes the transcript every frame, and rescanning all of it each frame
	// spent the whole frame budget on the bar.
	if s.cacheKey != "" && s.lastQuery == chatSearchQueryKey(s) && time.Since(s.lastCompute) < chatSearchRestream {
		return s.cache
	}
	s.cache, s.scanTruncated = a.chatSearchScan(t, s)
	s.cacheKey = key
	s.byMessage, s.spanCache = nil, nil
	s.byMessage = make(map[int]bool, len(s.cache))
	for _, m := range s.cache {
		s.byMessage[m.Message] = true
	}
	s.lastQuery, s.lastCompute = chatSearchQueryKey(s), time.Now()
	if s.Current >= len(s.cache) {
		s.Current = max(0, len(s.cache)-1)
	}
	return s.cache
}

// chatSearchRestream is how often a transcript that changed under an unchanged
// query is searched again.
const chatSearchRestream = 150 * time.Millisecond

// chatSearchScan answers a query over the transcript. A stream that keeps growing
// edits only the last message, so when nothing else changed the earlier hits are
// kept and one message is read again; any other change rescans everything.
func (a *desktop) chatSearchScan(t *conversation, s *chatSearch) ([]chatSearchMatch, bool) {
	query := chatSearchQueryKey(s)
	if s.tailOnly && s.cacheKey != "" && s.lastQuery == query && !s.scanTruncated &&
		s.scanCount == len(t.Messages) && s.scanCount > 0 {
		out, truncated := chatSearchScanTail(t, s, s.scanCount-1)
		s.scanBytes = s.scanPrefix + len(str(t.Messages[s.scanCount-1], "content"))
		s.tailOnly = false
		return out, truncated
	}
	out, truncated, prefix, total := chatSearchScanAll(t, s)
	s.scanCount, s.scanPrefix, s.scanBytes, s.tailOnly = len(t.Messages), prefix, total, false
	return out, truncated
}

// chatSearchScanTail keeps the hits of the messages before index last, which the
// stream did not touch, and reads that one message again.
func chatSearchScanTail(t *conversation, s *chatSearch, last int) ([]chatSearchMatch, bool) {
	out := make([]chatSearchMatch, 0, len(s.cache)+4)
	for _, m := range s.cache {
		if m.Message < last {
			out = append(out, m)
		}
	}
	budget := chatSearchMaxBytes - s.scanPrefix
	if budget <= 0 {
		return out, true
	}
	matches, truncated := chatSearchScanMessage(t.Messages[last], last, s, budget, out)
	return matches, truncated
}

func chatSearchQueryKey(s *chatSearch) string {
	return fmt.Sprintf("%s\x00%t\x00%t\x00%t", s.Query, s.MatchCase, s.WholeWord, s.Regexp)
}

// chatSearchCacheKey combines the query with the transcript revision, so edits
// invalidate matches without scanning message contents on every frame.
func chatSearchCacheKey(t *conversation, s *chatSearch) string {
	return fmt.Sprintf("%s\x00%d", chatSearchQueryKey(s), s.transcriptRevision)
}

// invalidateChatSearch is called alongside transcript mutations; unchanged
// frames then reuse the key without walking or hashing message contents.
func invalidateChatSearch(t *conversation) {
	if t == nil || t.UI == nil {
		return
	}
	s := &t.UI.Search
	s.transcriptRevision++
	s.frameKey = ""
	s.spanCache = nil
	s.codeCache = nil
	// The shape may have changed, so the next scan starts from the beginning.
	s.scanCount, s.scanPrefix, s.scanBytes, s.tailOnly = 0, 0, 0, false
}

// invalidateChatSearchTail is invalidateChatSearch for a reply that only grew its
// last message: the earlier hits stay valid, and the next scan reads one message
// instead of the whole transcript every 150 ms of streaming.
func invalidateChatSearchTail(t *conversation) {
	if t == nil || t.UI == nil {
		return
	}
	s := &t.UI.Search
	s.transcriptRevision++
	s.frameKey = ""
	s.spanCache = nil
	s.codeCache = nil
	s.tailOnly = true
}

// chatSearchPattern compiles the query when the regexp toggle is on, and
// reports an expression that is not one so the bar can say so instead of
// showing "无结果". It compiles once per query: the highlight of every block of
// every frame asks for it.
func (s *chatSearch) chatSearchPattern() (*regexp.Regexp, error) {
	key := chatSearchQueryKey(s)
	if key == s.patternKey {
		return s.pattern, s.patternErr
	}
	s.patternKey, s.pattern, s.patternErr = key, nil, nil
	if !s.Regexp || strings.TrimSpace(s.Query) == "" {
		return nil, nil
	}
	pattern := s.Query
	if !s.MatchCase {
		pattern = "(?i)" + pattern
	}
	s.pattern, s.patternErr = regexp.Compile(pattern)
	return s.pattern, s.patternErr
}

func chatSearchCompute(t *conversation, s *chatSearch) []chatSearchMatch {
	matches, _ := chatSearchComputeBounded(t, s)
	return matches
}

// chatSearchComputeBounded is chatSearchScanAll without the byte accounting, for
// callers that only want the hits.
func chatSearchComputeBounded(t *conversation, s *chatSearch) ([]chatSearchMatch, bool) {
	out, truncated, _, _ := chatSearchScanAll(t, s)
	return out, truncated
}

// chatSearchScanAll scans at most chatSearchMaxBytes on the UI thread and reports
// the bytes it read before the last message that held text and in total, so a
// later tail scan can stay inside the same budget. It never splits a message's
// offsets: a visible result is always complete for its message, and later
// messages may be outside the budget.
func chatSearchScanAll(t *conversation, s *chatSearch) (out []chatSearchMatch, truncated bool, prefix, total int) {
	if strings.TrimSpace(s.Query) == "" {
		return nil, false, 0, 0
	}
	if _, err := s.chatSearchPattern(); err != nil {
		return nil, false, 0, 0
	}
	for i, m := range t.Messages {
		prefix = total
		content := str(m, "content")
		if content == "" {
			continue
		}
		out, truncated = chatSearchScanMessage(m, i, s, chatSearchMaxBytes-total, out)
		total = prefix + min(len(content), chatSearchMaxBytes-prefix)
		if truncated || len(out) >= chatSearchMaxMatches {
			return out, truncated, prefix, total
		}
	}
	return out, false, prefix, total
}

// chatSearchScanMessage appends the hits of one message to out, reading at most
// budget bytes of it.
func chatSearchScanMessage(m map[string]any, index int, s *chatSearch, budget int, out []chatSearchMatch) ([]chatSearchMatch, bool) {
	text := str(m, "content")
	re, err := s.chatSearchPattern()
	if err != nil || text == "" || budget <= 0 {
		return out, budget <= 0 && text != ""
	}
	truncated := len(text) > budget
	if truncated {
		// Search as much of this message as fits, but do not cut a UTF-8 rune
		// or allow the byte budget to be exceeded.
		text = text[:budget]
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	if len(text) == 0 {
		return out, true
	}
	for _, span := range chatSearchSpans(text, s, re, chatSearchMaxMatches-len(out)) {
		out = append(out, chatSearchMatch{Message: index, Start: span[0], End: span[1]})
		if len(out) >= chatSearchMaxMatches {
			return out, false
		}
	}
	return out, truncated
}

// chatSearchSpans finds the byte ranges of the query in one message. The offsets
// always address the original text: a lowercased copy changes length for a few
// runes, which sliced the wrong bytes or even panicked.
func chatSearchSpans(text string, s *chatSearch, re *regexp.Regexp, limit int) [][2]int {
	if limit <= 0 {
		return nil
	}
	if re != nil {
		var spans [][2]int
		for from := 0; from <= len(text) && len(spans) < limit; {
			loc := re.FindStringIndex(text[from:])
			if loc == nil {
				break
			}
			start, end := from+loc[0], from+loc[1]
			if start < end && (!s.WholeWord || chatSearchWordBoundary(text, start, end)) {
				spans = append(spans, [2]int{start, end})
			}
			if end > from {
				from = end
			} else {
				_, size := utf8.DecodeRuneInString(text[from:])
				from += max(1, size) // progress past an empty regexp match
			}
		}
		return spans
	}
	needle := s.Query
	if needle == "" {
		return nil
	}
	var spans [][2]int
	for from := 0; from < len(text) && len(spans) < limit; {
		end, ok := chatSearchMatchAt(text, from, needle, s.MatchCase)
		if !ok {
			_, size := utf8.DecodeRuneInString(text[from:])
			from += max(1, size)
			continue
		}
		if !s.WholeWord || chatSearchWordBoundary(text, from, end) {
			spans = append(spans, [2]int{from, end})
		}
		from = end
	}
	return spans
}

// chatSearchMatchAt reports where the needle ends when it starts at text[from:],
// comparing rune by rune so a case-insensitive match never moves the offsets.
func chatSearchMatchAt(text string, from int, needle string, matchCase bool) (int, bool) {
	at := from
	for _, want := range needle {
		if at >= len(text) {
			return 0, false
		}
		got, size := utf8.DecodeRuneInString(text[at:])
		if got != want {
			if matchCase || !chatSearchSameRune(got, want) {
				return 0, false
			}
		}
		at += size
	}
	return at, true
}

func chatSearchSameRune(got, want rune) bool {
	return unicode.ToLower(got) == unicode.ToLower(want) || unicode.ToUpper(got) == unicode.ToUpper(want)
}

// chatSearchWordBoundary reports whether the range stands as a word of its own.
func chatSearchWordBoundary(text string, start, end int) bool {
	if start > 0 {
		if r, _ := utf8.DecodeLastRuneInString(text[:start]); chatSearchWordRune(r) {
			return false
		}
	}
	if end < len(text) {
		if r, _ := utf8.DecodeRuneInString(text[end:]); chatSearchWordRune(r) {
			return false
		}
	}
	return true
}

func chatSearchWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// chatSearchState is what the bar has to say about a query, apart from the
// wording it puts on it: tests assert this, so that rewording the label cannot
// break them.
type chatSearchState int

const (
	chatSearchIdle chatSearchState = iota
	chatSearchBadPattern
	chatSearchNoHit
	chatSearchHasHits
)

// chatSearchState reports what the bar is about to say. It compiles the query and
// searches the transcript, so the label and the highlight answer the same frame.
func (a *desktop) chatSearchState(t *conversation) chatSearchState {
	s := &t.uiState().Search
	if strings.TrimSpace(s.Query) == "" {
		return chatSearchIdle
	}
	if _, err := s.chatSearchPattern(); err != nil {
		return chatSearchBadPattern
	}
	if len(a.chatSearchMatches(t)) == 0 {
		return chatSearchNoHit
	}
	return chatSearchHasHits
}

// chatSearchStatus says what was found, as the editor's bar does: nothing
// without a query, "正则有误" for an expression that is not one, and the
// position of the current match otherwise.
func (a *desktop) chatSearchStatus(t *conversation) string {
	s := &t.uiState().Search
	switch a.chatSearchState(t) {
	case chatSearchIdle:
		return ""
	case chatSearchBadPattern:
		return "正则有误"
	}
	matches := a.chatSearchMatches(t)
	if len(matches) == 0 {
		if s.scanTruncated {
			return fmt.Sprintf("未找到（仅搜索前 %d MiB）", chatSearchMaxBytes>>20)
		}
		return "无结果"
	}
	current := min(max(s.Current, 0), len(matches)-1) + 1
	if s.scanTruncated {
		return fmt.Sprintf("%d of %d（仅搜索前 %d MiB）", current, len(matches), chatSearchMaxBytes>>20)
	}
	if len(matches) >= chatSearchMaxMatches {
		// Say so rather than passing the cap off as the total.
		return fmt.Sprintf("%d of %d+", current, len(matches))
	}
	return fmt.Sprintf("%d of %d", current, len(matches))
}

// chatSearchBar draws the bar above the transcript.
func (a *desktop) chatSearchBar(c *ui.Context, t *conversation) {
	s := &t.uiState().Search
	th := c.Theme()
	bar := ui.Row(c.Key(t.ID+"-search-bar")).Label("对话查找").AlignItems(ui.Center).Gap(th.Space(1)).
		Padding(th.Space(1.5)).Background(th.Surface).Border(1, th.Border).Radius(th.Radius).
		Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.18)).Shrink(0)
	bar.Children(func() {
		field := ui.TextInputBase(c.Key(t.ID+"-search-query"), &s.Query).
			Width(th.Space(50)).Placeholder("在对话中查找").Padding(th.Space(1), th.Space(2)).
			Background(th.Background).Border(1, th.Border).Radius(th.Radius-2).FontSize(th.FontSize-1).
			Label("在对话中查找").FocusBind(&a.chatSearchFocus, t.ID+"-search-query")
		field.OnTextKey(func(k ui.TextKey) bool {
			switch k.Key {
			case ui.KeyEscape:
				a.closeChatSearch(t)
				return true
			case ui.KeyEnter:
				delta := 1
				if k.Mods&ui.Shift != 0 {
					delta = -1
				}
				a.chatSearchStep(t, delta)
				return true
			}
			return false
		})
		if field.Changed() {
			s.Current, s.waited = 0, 0
			s.reveal = true
		}
		toggle := func(on *bool, label, tip string) {
			b := ui.ToggleBase(c, on).Cursor(ui.CursorPointer).Padding(th.Space(0.5), th.Space(1.5)).Radius(th.Radius - 2).Label(tip).Tooltip(tip).KeepFocus().MinWidth(0)
			switch {
			case *on:
				b.Background(th.Accent.Alpha(0.2)).TextColor(th.Accent)
			case b.Hovered():
				// The pointer has to say a toggle is under it: a bare glyph in the
				// bar looked like a label.
				b.Background(th.Surface.Mix(th.Text, 0.10)).TextColor(th.Text)
			default:
				b.TextColor(a.secondaryInk())
			}
			b.Children(func() { ui.Text(c, label).FontSize(th.FontSize - 1) })
		}
		toggle(&s.MatchCase, "Aa", "区分大小写")
		toggle(&s.WholeWord, "ab", "全词匹配")
		toggle(&s.Regexp, ".*", "使用正则表达式")
		ui.Text(c, a.chatSearchStatus(t)).FontSize(th.FontSize - 2).TextColor(a.secondaryInk()).
			FontFeatures("tnum").MinWidth(th.Space(16)).Role(ui.RoleStatus)
		mutedIconAction(c, t.ID+"-search-prev", "上一个匹配", "arrowup", 24, func() { a.chatSearchStep(t, -1) }).Shrink(0)
		mutedIconAction(c, t.ID+"-search-next", "下一个匹配", "arrowdown", 24, func() { a.chatSearchStep(t, 1) }).Shrink(0)
		mutedIconAction(c, t.ID+"-search-close", "关闭查找（Esc）", "close", 24, func() { a.closeChatSearch(t) }).Shrink(0)
	})
}

// chatSearchMessageMatch reports whether a message holds a hit, so a message
// without one keeps its Markdown untouched.
func (a *desktop) chatSearchMessageMatch(t *conversation, index int) bool {
	s := &t.uiState().Search
	a.chatSearchMatches(t)
	return s.byMessage[index]
}

// beginChatSearchFrame keys the transcript once for the frame the transcript is
// about to build: every visible message asks whether it holds a hit, and the key
// walks all of them.
func (a *desktop) beginChatSearchFrame(t *conversation) {
	s := &t.uiState().Search
	s.frameKey = ""
	if s.Open {
		a.chatSearchMatches(t)
	}
}

// chatSearchBlock replaces the paragraph and heading blocks that hold a hit with
// their text, matched runs carrying a background. The hook also reaches the
// paragraphs of list items and quotes, which keep their markers and indentation
// because only the text is replaced. Markdown offers no hook to mark ranges
// inside its own layout, so those blocks give up their inline styling (bold,
// links, inline code) while the bar is open; every other block, and every message
// without a hit, renders exactly as always. A replaced block is not recorded in
// the view's line map, so "Copy as Markdown" while the bar is open leaves the
// paragraphs it drew out; closing the bar restores the whole map.
func (a *desktop) chatSearchBlock(t *conversation, message int) func(c *ui.Context, doc *parser.Document, index int) bool {
	return func(c *ui.Context, doc *parser.Document, index int) bool {
		s := &t.uiState().Search
		// The code-block hook of this message runs right after this one, so it can
		// take the block's lines from the document instead of parsing again.
		s.frameDoc, s.frameDocMessage = doc, message
		text, spans, base, ok := chatSearchBlockSpans(doc, index, message, s)
		if !ok {
			return false
		}
		a.chatSearchMark(c, t, message, doc.Block(index), text, spans, base)
		return true
	}
}

// chatSearchBlockSpans reports the text of a block the bar draws itself, where the
// matched runs are in it, and where that text starts in the message, so a run can
// be told apart from the one the bar points at. Only prose qualifies here: a code
// block is drawn by its own hook (see chatSearchCodeHook), and a table cell keeps
// the renderer's drawing, since the table builds its cells without asking the
// hook. A match in either is found and scrolled to; a table cell is not marked.
func chatSearchBlockSpans(doc *parser.Document, index, message int, s *chatSearch) (string, [][2]int, int, bool) {
	block := doc.Block(index)
	if block == nil || (block.Kind != parser.Paragraph && block.Kind != parser.Heading) {
		return "", nil, 0, false
	}
	// Do not replace formatted inline content with raw Markdown source. Plain
	// prose and headings can be highlighted without exposing markup syntax.
	for _, in := range doc.Inlines(index) {
		if in.Kind != parser.Text && in.Kind != parser.SoftBreak && in.Kind != parser.LineBreak {
			return "", nil, 0, false
		}
	}
	src := doc.Source()
	if block.Start < 0 || block.End > len(src) || block.Start >= block.End {
		return "", nil, 0, false
	}
	raw := src[block.Start:block.End]
	if block.Kind == parser.Heading {
		// Heading markers are syntax, not visible text; keep the byte offset
		// aligned with the source so the selected hit is still identified.
		at := 0
		for at < len(raw) && raw[at] == '#' {
			at++
		}
		if at > 0 && at < len(raw) && (raw[at] == ' ' || raw[at] == '\t') {
			for at < len(raw) && (raw[at] == ' ' || raw[at] == '\t') {
				at++
			}
			raw = raw[at:]
			blockStartOffset := block.Start + at
			return chatSearchBlockText(raw, message, blockStartOffset, block.End, s)
		}
	}
	return chatSearchBlockText(raw, message, block.Start, block.End, s)
}

// chatSearchBlockKey names one block of one message; chatSearchCodeKey adds the
// block's line count, since a code block's draw is made of its lines.
type chatSearchBlockKey struct {
	Message, Start, End int
}

type chatSearchCodeKey struct {
	chatSearchBlockKey
	Lines int
}

func chatSearchBlockText(raw string, message, start, end int, s *chatSearch) (string, [][2]int, int, bool) {
	// The same space the text drops must be the space the offset skips, or a block
	// opening with a form feed points the current run at the wrong place.
	lead := 0
	for lead < len(raw) {
		r, size := utf8.DecodeRuneInString(raw[lead:])
		if !unicode.IsSpace(r) {
			break
		}
		lead += size
	}
	text := strings.TrimRightFunc(raw[lead:], unicode.IsSpace)
	if text == "" {
		return "", nil, 0, false
	}
	s.syncCacheQuery()
	key := chatSearchBlockKey{Message: message, Start: start, End: end}
	if spans, ok := s.spanCache[key]; ok {
		return text, spans, start + lead, len(spans) > 0
	}
	re, err := s.chatSearchPattern()
	if err != nil {
		return "", nil, 0, false
	}
	spans := chatSearchSpans(text, s, re, chatSearchMaxMatches)
	if s.spanCache == nil {
		s.spanCache = map[chatSearchBlockKey][][2]int{}
	}
	s.spanCache[key] = spans
	if len(spans) == 0 {
		return "", nil, 0, false
	}
	return text, spans, start + lead, true
}

// syncCacheQuery drops what the caches hold when the query changed: they are
// keyed by block alone, so the query is a generation rather than part of a key.
func (s *chatSearch) syncCacheQuery() {
	if key := chatSearchQueryKey(s); s.cacheQuery != key {
		s.cacheQuery = key
		s.spanCache, s.codeCache = nil, nil
	}
}

// chatSearchMark draws text with the matched runs highlighted. The run the bar
// points at carries the accent solid, so the current hit is obvious at a glance
// and the others stay a tint of it rather than VS Code's two yellows.
//
// The text takes the Markdown theme's own font, size, line height and colour: a
// block that measured differently would shift the transcript the moment the bar
// opened, and the scroll that follows would look like a second, rigid jump.
func (a *desktop) chatSearchMark(c *ui.Context, t *conversation, message int, block *parser.Block, text string, spans [][2]int, base int) {
	p := a.currentPalette()
	current, hasCurrent := a.chatSearchCurrent(t)
	size, weight, ink := a.chatSearchTextStyle(block)
	if hasCurrent && current.Message != message {
		hasCurrent = false
	}
	// The pointed run carries the accent solid, the others a tint of it, so the
	// current hit reads at a glance without VS Code's two yellows.
	pointed := -1
	parts := make([]ui.Span, 0, len(spans)*2+1)
	at := 0
	for i, span := range spans {
		if span[0] < at || span[1] > len(text) {
			continue
		}
		if span[0] > at {
			parts = append(parts, ui.Span{Text: text[at:span[0]]})
		}
		run := ui.Span{Text: text[span[0]:span[1]], Background: p.accent.Alpha(0.38), Radius: 3}
		if hasCurrent && current.Start == base+span[0] {
			run.Background, run.Color, run.Weight = p.accent, c.Theme().Background, 600
			pointed = i
		}
		parts = append(parts, run)
		at = span[1]
	}
	if at < len(text) {
		parts = append(parts, ui.Span{Text: text[at:]})
	}
	view := ui.RichText(c, parts...).FillWidth().Font(nativeFontFamily()).FontSize(size).
		LineHeight(readingLineHeight).TextColor(ink)
	if weight > 0 {
		view.FontWeight(weight)
	}
	if block != nil && block.Kind == parser.Heading {
		// The replaced block stays a heading for a screen reader, as the renderer's
		// own element is.
		view.Role(ui.RoleHeading).Level(min(max(block.Level, 1), 6))
	}
	if pointed < 0 {
		return
	}
	// Anchor the block the pointed run is in, and remember how far into its text
	// the run sits: the scroll reaches the line without a per-line layout.
	s := &t.uiState().Search
	if s.blockHandle == nil {
		s.blockHandle = &ui.Handle{}
	}
	s.blockMatch, s.blockBound = current, true
	s.blockFrac = 0
	if len(text) > 0 {
		s.blockFrac = float32(spans[pointed][0]) / float32(len(text))
	}
	view.Bind(s.blockHandle)
}

// chatSearchTextStyle mirrors the reading theme, so a replaced block measures as
// the one it stands in for.
func (a *desktop) chatSearchTextStyle(block *parser.Block) (size float32, weight int, ink ui.Color) {
	colors := a.readingColors()
	base, _ := readingSizes(a.config.MessageFontSize, a.config.CodeFontSize)
	size, ink = base, colors.text
	if block != nil && block.Kind == parser.Heading {
		level := min(max(block.Level, 1), len(readingHeadingScales))
		size, weight = base*readingHeadingScales[level-1], 600
		// The theme colours every heading with the heading ink but the second level
		// in a light palette, which takes the body ink; mirror that exactly so the
		// block does not change colour when the bar opens.
		ink = colors.heading
		if level == 2 && !a.currentPalette().dark {
			ink = colors.text
		}
	}
	return size, weight, ink
}

// chatSearchCodeHook wraps the reading theme's code-block renderer: a block that
// holds a hit is drawn by the bar with the matched runs marked, and every other
// block — and every block once the bar closes — keeps the renderer's own syntax
// colouring, copy button and wrapping. fallback is what the theme installed.
//
// The drawn block carries no syntax colouring and is not recorded for the view's
// "Copy as Markdown" line map: both come back with the renderer as soon as the
// hit moves on or the bar closes.
func (a *desktop) chatSearchCodeHook(t *conversation, message int, fallback func(*ui.Context, *markdown.CodeBlockView)) func(*ui.Context, *markdown.CodeBlockView) {
	return func(c *ui.Context, block *markdown.CodeBlockView) {
		if a.chatSearchCodeMark(c, t, message, block) {
			return
		}
		if fallback != nil {
			fallback(c, block)
		}
	}
}

// chatSearchCodeMark draws a code block whose lines are taken from the document
// the block hook just saw, and reports whether it did. A block with no hit, one
// too large to rescan every frame, or a query that does not compile keeps the
// renderer's own drawing: the bar still finds and scrolls to those hits.
func (a *desktop) chatSearchCodeMark(c *ui.Context, t *conversation, message int, block *markdown.CodeBlockView) bool {
	s := &t.uiState().Search
	lines, starts, spans, ok := chatSearchCodeLines(s, message, block)
	if !ok {
		return false
	}
	p := a.currentPalette()
	colors := a.readingColors()
	base, codeSize := readingSizes(a.config.MessageFontSize, a.config.CodeFontSize)
	// The reading theme keeps MyGo's default code-block paddings, in ems of the
	// body size, so the block measures as the one it stands in for.
	pad := 0.85 * base
	current, hasCurrent := a.chatSearchCurrent(t)
	if hasCurrent && current.Message != message {
		hasCurrent = false
	}
	box := ui.Column(c).Background(colors.panel).Border(1, colors.border).
		Radius(c.Theme().Radius + 2).AlignSelf(ui.Stretch).Clip()
	box.Role(ui.RoleGroup).Label(chatSearchCodeLabel(block.Language))
	box.Children(func() {
		head := ui.Row(c).AlignItems(ui.Center).Padding(pad*0.35, pad*0.4, 0, pad)
		head.Children(func() {
			if block.Language != "" {
				ui.Text(c, block.Language).FontSize(base * 0.8).TextColor(colors.muted).Unselectable()
			}
			ui.Spacer(c)
			block.CopyButton(c)
		})
		ui.Column(c).Padding(pad*0.25, pad, pad, pad).Children(func() {
			for i, line := range lines {
				parts := make([]ui.Span, 0, len(spans[i])*2+1)
				at, pointedAt := 0, -1
				for _, span := range spans[i] {
					if span[0] < at || span[1] > len(line) {
						continue
					}
					if span[0] > at {
						parts = append(parts, ui.Span{Text: line[at:span[0]]})
					}
					run := ui.Span{Text: line[span[0]:span[1]], Background: p.accent.Alpha(0.38), Radius: 3}
					if hasCurrent && current.Start == starts[i]+span[0] {
						run.Background, run.Color, run.Weight = p.accent, c.Theme().Background, 600
						pointedAt = span[0]
					}
					parts = append(parts, run)
					at = span[1]
				}
				if at < len(line) {
					parts = append(parts, ui.Span{Text: line[at:]})
				}
				if len(parts) == 0 {
					// A blank line still holds the row: the block keeps its height.
					parts = append(parts, ui.Span{Text: " "})
				}
				view := ui.RichText(c, parts...).FillWidth().Font(nativeFontFamily()).FontSize(codeSize).
					LineHeight(readingCodeLineHeight).TextColor(colors.text)
				if pointedAt >= 0 {
					// Anchor the very line the hit is on, and how far into it the run sits:
					// a jump then lands on the line instead of the top of the block.
					if s.blockHandle == nil {
						s.blockHandle = &ui.Handle{}
					}
					s.blockMatch, s.blockBound = current, true
					s.blockFrac = 0
					if len(line) > 0 {
						s.blockFrac = float32(pointedAt) / float32(len(line))
					}
					view.Bind(s.blockHandle)
				}
			}
		})
	})
	return true
}

// chatSearchCode is what the bar keeps of a code block it draws.
type chatSearchCode struct {
	lines  []string
	starts []int
	spans  [][][2]int
	ok     bool
}

// chatSearchCodeLines returns the lines of a code block, the runs of the query in
// each, and the offset of each line in the message. It reports false for a block
// the bar should not redraw: no document, no query, no hit, or one too large to
// rescan on every frame. The answer is memoised by the block's range, so the
// frames of a still transcript render from the cache instead of rescanning.
func chatSearchCodeLines(s *chatSearch, message int, block *markdown.CodeBlockView) (lines []string, starts []int, spans [][][2]int, ok bool) {
	doc := s.frameDoc
	if s.frameDocMessage != message || doc == nil || block == nil || block.Index < 1 || block.Index >= doc.Len() {
		return nil, nil, nil, false
	}
	if strings.TrimSpace(s.Query) == "" {
		return nil, nil, nil, false
	}
	range0 := doc.Block(block.Index)
	if range0 == nil {
		return nil, nil, nil, false
	}
	s.syncCacheQuery()
	key := chatSearchCodeKey{chatSearchBlockKey: chatSearchBlockKey{Message: message, Start: range0.Start, End: range0.End}, Lines: doc.LineCount(block.Index)}
	if cached, hit := s.codeCache[key]; hit {
		return cached.lines, cached.starts, cached.spans, cached.ok
	}
	cached := chatSearchCodeAt(doc, block.Index, s, message, range0.Start, range0.End)
	if s.codeCache == nil {
		s.codeCache = map[chatSearchCodeKey]chatSearchCode{}
	}
	s.codeCache[key] = cached
	return cached.lines, cached.starts, cached.spans, cached.ok
}

// chatSearchCodeAt scans the lines of code block index for the query. It draws
// nothing when the block holds no hit, or when the block or its runs past the
// caps would make the draw cost more than it is worth: a block drawn with only
// its first lines would hide the rest of the code.
func chatSearchCodeAt(doc *parser.Document, index int, s *chatSearch, message, start, end int) chatSearchCode {
	// Only a block that holds a hit is worth reading: a message of many code
	// blocks then costs one pass over its hits, not a scan of every block.
	if !chatSearchBlockHasHit(s, message, start, end) {
		return chatSearchCode{}
	}
	re, err := s.chatSearchPattern()
	if err != nil {
		return chatSearchCode{}
	}
	n := doc.LineCount(index)
	src := doc.Source()
	out := chatSearchCode{lines: make([]string, 0, n), starts: make([]int, 0, n), spans: make([][][2]int, 0, n)}
	size, hits := 0, 0
	for k := 0; k < n; k++ {
		line, at := doc.Line(index, k)
		if size += len(line); size > chatSearchCodeMaxBytes {
			return chatSearchCode{}
		}
		// A line the parser padded for a tab it consumed is longer than its source,
		// so the offsets taken from it are shifted: report where the line starts in
		// the source, not where the padded one does.
		at -= chatSearchCodePad(src, line, at)
		found := chatSearchSpans(line, s, re, chatSearchCodeMaxSpans-hits)
		out.lines, out.starts, out.spans = append(out.lines, line), append(out.starts, at), append(out.spans, found)
		hits += len(found)
	}
	if hits == 0 {
		return chatSearchCode{}
	}
	out.ok = true
	return out
}

// chatSearchCodePad is how many spaces Document.Line put in front of a line that
// the source does not hold, from the tab it consumed.
func chatSearchCodePad(src, line string, at int) int {
	if at < 0 || at > len(src) {
		return 0
	}
	rest := src[at:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSuffix(rest, "\r")
	if !strings.HasSuffix(line, rest) {
		return 0
	}
	return len(line) - len(rest)
}

// chatSearchCodeLabel names a code block for a screen reader, as the renderer does.
func chatSearchCodeLabel(language string) string {
	if language == "" {
		return "Code"
	}
	return "Code, " + language
}

// chatSearchBlockHasHit reports whether one of the message's hits starts in the
// byte range of a block.
func chatSearchBlockHasHit(s *chatSearch, message, start, end int) bool {
	for _, m := range s.cache {
		if m.Message == message && m.Start >= start && m.Start < end {
			return true
		}
	}
	return false
}

// chatSearchCurrent returns the hit the bar points at.
func (a *desktop) chatSearchCurrent(t *conversation) (chatSearchMatch, bool) {
	s := &t.uiState().Search
	matches := a.chatSearchMatches(t)
	if len(matches) == 0 {
		return chatSearchMatch{}, false
	}
	return matches[min(max(s.Current, 0), len(matches)-1)], true
}

// chatSearchReveal scrolls the frame's current match into view, once the row
// that holds it has bounds from the last frame. A jump is one scroll: the bar waits
// for the layout it needs, bounded, and then scrolls to the best anchor it has.
func (a *desktop) chatSearchReveal(c *ui.Context, t *conversation, start, end int) {
	st := t.uiState()
	s := &st.Search
	if !s.Open || !s.reveal {
		return
	}
	matches := a.chatSearchMatches(t)
	if len(matches) == 0 {
		s.reveal = false
		return
	}
	target := matches[min(max(s.Current, 0), len(matches)-1)]
	if target.Message < start || target.Message >= end {
		// The message is outside the window: open the page around it and let the
		// next frame scroll to the row this one builds. This wait has its own
		// budget, so opening the page never eats the one the anchor needs.
		if !a.awaitChatSearchFrame(c, &s.waitedWindow) {
			a.endChatSearchJump(s)
			return
		}
		st.VisibleStart = max(0, target.Message-min(40, a.messageWindowSize()/2)) + 1
		st.Follow, st.FollowPending = false, false
		t.Scroll.Y = 0
		return
	}
	viewport := t.Scroll.Bounds(c)
	// The block that drew the hit is the precise anchor: a long reply hides the
	// match far below its own first screen. It only counts when it was bound for
	// this very hit, and only once a frame has laid it out.
	if viewport.H > 0 && s.blockBound && s.blockMatch == target {
		if bounds := s.blockHandle.Bounds(c); bounds.H > 0 {
			a.scrollChatSearch(t, st, bounds.Y+bounds.H*s.blockFrac-viewport.Y-40)
			return
		}
	}
	// The window redraws only when something asks it to, so the wait asks for the
	// frame that lays the anchor out instead of hanging on the next keystroke.
	if a.awaitChatSearchFrame(c, &s.waited) {
		return
	}
	a.endChatSearchJump(s)
	if viewport.H <= 0 {
		return
	}
	// A hit in a row the transcript folds away never gets a block anchor: the row
	// itself is the closest the bar can get.
	if handle := s.handles[target.Message]; handle != nil {
		if bounds := handle.Bounds(c); bounds.H > 0 {
			a.scrollChatSearch(t, st, bounds.Y-viewport.Y-40)
		}
	}
}

// awaitChatSearchFrame asks for the next frame while a jump still has frames to
// spend waiting for layout, and reports whether it did.
func (a *desktop) awaitChatSearchFrame(c *ui.Context, waited *int) bool {
	if *waited >= chatSearchWaitFrames {
		return false
	}
	*waited++
	c.After(chatSearchNextFrame)
	return true
}

// endChatSearchJump stops asking for frames for a jump that reached its limit.
func (a *desktop) endChatSearchJump(s *chatSearch) {
	s.reveal, s.waited, s.waitedWindow = false, 0, 0
}

// scrollChatSearch moves the transcript by delta, leaving the hit just below the
// top edge, and ends the jump.
func (a *desktop) scrollChatSearch(t *conversation, st *chatUI, delta float32) {
	st.Follow, st.FollowPending = false, false
	t.Scroll.Y = max(float32(0), t.Scroll.Y+delta)
	a.endChatSearchJump(&st.Search)
}
