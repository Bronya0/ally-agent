// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// chatSearch is the transcript's find bar: the editor's bar without replacing,
// searching the messages' text instead of a buffer. The matches are message
// ranges. Plain prose and code project these ranges into native highlighted
// text; formatted inline content retains the Markdown renderer's layout.
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
	patternKey       string
	pattern          *regexp.Regexp
	patternErr       error
	literal          []rune
	literalPrefix    []int
	literalStarts    []int
	literalQuery     string
	literalMatchCase bool
	// codeCache memoises what a code block of one message resolved to, and
	// cacheQuery says which query it was filled for: a comparable key keeps the
	// frame from formatting a string per block, and one generation field replaces
	// the query inside it.
	codeCache  map[chatSearchCodeKey]chatSearchCode
	cacheQuery string
	queryKey   memoOf[chatSearchQueryOptions, string]
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
	scanTruncated    bool
	highlightSkipped bool
	// candidateTruncated distinguishes regexp candidate exhaustion from bytes.
	candidateTruncated bool
	// scanCount, scanPrefix and scanBytes describe the scan behind cache: how many
	// messages it covered, the bytes before the last of them, and the bytes in all.
	// A reply that keeps growing edits only its last message, so the frames during
	// one stream rescan that message instead of the whole transcript.
	scanCount  int
	scanPrefix int
	scanBytes  int
	tailOnly   bool
	appendOnly bool
	// frameDoc and frameDocMessage are the document and message the block hook
	// was last asked about, so the code-block hook that runs right after it can
	// address the block's lines without parsing the message again.
	frameDoc        *parser.Document
	frameDocMessage int
	// markdownCache keeps bounded parser projections for messages already scanned.
	markdownCache map[int]chatSearchMarkdownCacheEntry
}

// chatSearchMatch is one hit: the message it is in and the byte range of the
// matched text within that message.
type chatSearchMatch struct {
	Message    int
	Start, End int
}

type chatSearchMarkdownCacheEntry struct {
	source     string
	projection chatSearchMarkdown
}

// chatSearchMaxMatches and chatSearchMaxBytes bound a query's synchronous scan.
const (
	chatSearchMaxMatches         = 400
	chatSearchMaxBytes           = 4 << 20
	chatSearchMaxRegexCandidates = 8192
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
	chatSearchCodeMaxLines = 1000
)

// openChatSearch opens the bar and puts the caret in its field.
func (a *desktop) openChatSearch(t *conversation) {
	if t == nil || a.closing || a.page != "chat" {
		return
	}
	st := t.uiState()
	st.CmdOpen, st.Mention.Open, st.SessionsOpen = false, false, false
	st.Search.Open = true
	a.chatSearchFocus = t.ID + "-search-query"
	// The composer has to look unfocused now, or closing the bar could not give its
	// focus back: the engine re-focuses a bound field when its value changes.
	a.composerFocus, a.focusBack = "", ""
	scheduleChatSearchReveal(&st.Search)
}

func (a *desktop) closeChatSearch(t *conversation) {
	if t == nil {
		return
	}
	st := t.uiState()
	s := &st.Search
	s.Open = false
	s.reveal, s.waited, s.waitedWindow = false, 0, 0
	// The bar's per-frame caches describe a transcript that is no longer searched.
	// Row handles stay for this page and are recycled as it changes; the engine
	// keeps each registered binding until window close.
	s.blockBound = false
	s.byMessage, s.cache, s.cacheKey = nil, nil, ""
	s.codeCache = nil
	s.markdownCache = nil
	s.highlightSkipped = false
	s.frameDoc = nil
	s.patternKey, s.pattern, s.patternErr = "", nil, nil
	s.literal, s.literalPrefix, s.literalStarts, s.literalQuery = nil, nil, nil, ""
	s.queryKey = memoOf[chatSearchQueryOptions, string]{}
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
	scheduleChatSearchReveal(s)
}

func scheduleChatSearchReveal(s *chatSearch) {
	s.reveal, s.waited, s.waitedWindow = true, 0, 0
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
	s.byMessage = nil
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
	if s.appendOnly && !s.tailOnly && s.cacheKey != "" && s.lastQuery == query && !s.scanTruncated &&
		s.scanCount <= len(t.Messages) && len(s.cache) < chatSearchMaxMatches {
		out := append([]chatSearchMatch(nil), s.cache...)
		prefix, total, truncated := s.scanPrefix, s.scanBytes, false
		for i := s.scanCount; i < len(t.Messages); i++ {
			prefix = total
			if isToolRecord(t.Messages[i]) {
				continue
			}
			content := messageDisplayContent(t.Messages[i])
			if content == "" {
				continue
			}
			out, truncated = chatSearchScanMessage(t.Messages[i], i, s, chatSearchMaxBytes-total, out)
			total = prefix + min(len(content), chatSearchMaxBytes-prefix)
			if truncated || len(out) >= chatSearchMaxMatches {
				break
			}
		}
		s.scanCount, s.scanPrefix, s.scanBytes, s.appendOnly = len(t.Messages), prefix, total, false
		return out, truncated
	}
	if s.tailOnly && s.cacheKey != "" && s.lastQuery == query && !s.scanTruncated &&
		s.scanCount == len(t.Messages) && s.scanCount > 0 {
		out, truncated := chatSearchScanTail(t, s, s.scanCount-1)
		s.scanBytes = s.scanPrefix + len(messageDisplayContent(t.Messages[s.scanCount-1]))
		s.tailOnly = false
		return out, truncated
	}
	out, truncated, prefix, total := chatSearchScanAll(t, s)
	s.scanCount, s.scanPrefix, s.scanBytes, s.tailOnly, s.appendOnly = len(t.Messages), prefix, total, false, false
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

type chatSearchQueryOptions struct {
	query                        string
	matchCase, wholeWord, regexp bool
}

func chatSearchQueryKey(s *chatSearch) string {
	return s.queryKey.get(chatSearchQueryOptions{s.Query, s.MatchCase, s.WholeWord, s.Regexp}, func(key chatSearchQueryOptions) string {
		return fmt.Sprintf("%s\x00%t\x00%t\x00%t", key.query, key.matchCase, key.wholeWord, key.regexp)
	})
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
	s.codeCache = nil
	s.markdownCache = nil
	// The shape may have changed, so the next scan starts from the beginning.
	s.scanCount, s.scanPrefix, s.scanBytes, s.tailOnly, s.appendOnly = 0, 0, 0, false, false
}

// Tool records append without changing earlier searchable message content.
func invalidateChatSearchAppend(t *conversation) {
	if t == nil || t.UI == nil {
		return
	}
	s := &t.UI.Search
	if s.tailOnly || (s.scanCount != len(t.Messages) && !s.appendOnly) {
		invalidateChatSearch(t)
		return
	}
	s.transcriptRevision++
	s.frameKey, s.codeCache = "", nil
	s.appendOnly = true
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
	s.codeCache = nil
	if s.markdownCache != nil && len(t.Messages) > 0 {
		delete(s.markdownCache, len(t.Messages)-1)
	}
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
	s.literal, s.literalPrefix = nil, nil
	s.literalStarts = nil
	s.literalQuery, s.literalMatchCase = s.Query, s.MatchCase
	if strings.TrimSpace(s.Query) == "" {
		return nil, nil
	}
	if !s.Regexp {
		s.literal = []rune(s.Query)
		if !s.MatchCase {
			for i, r := range s.literal {
				s.literal[i] = chatSearchFoldRune(r)
			}
		}
		s.literalPrefix = make([]int, len(s.literal))
		s.literalStarts = make([]int, len(s.literal))
		for i, matched := 1, 0; i < len(s.literal); i++ {
			for matched > 0 && s.literal[i] != s.literal[matched] {
				matched = s.literalPrefix[matched-1]
			}
			if s.literal[i] == s.literal[matched] {
				matched++
			}
			s.literalPrefix[i] = matched
		}
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
	s.candidateTruncated, s.highlightSkipped = false, false
	if strings.TrimSpace(s.Query) == "" {
		return nil, false, 0, 0
	}
	if _, err := s.chatSearchPattern(); err != nil {
		return nil, false, 0, 0
	}
	for i, m := range t.Messages {
		prefix = total
		if isToolRecord(m) {
			continue
		}
		content := messageDisplayContent(m)
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
	if isToolRecord(m) {
		return out, false
	}
	source := messageDisplayContent(m)
	re, err := s.chatSearchPattern()
	if err != nil || source == "" || budget <= 0 {
		return out, budget <= 0 && source != ""
	}
	truncated := len(source) > budget
	if truncated {
		// Parsing only the source that fits keeps both parser work and search work
		// inside the transcript-wide byte budget.
		source = source[:budget]
		for len(source) > 0 && !utf8.ValidString(source) {
			source = source[:len(source)-1]
		}
	}
	if len(source) == 0 {
		return out, true
	}
	entry, cached := s.markdownCache[index]
	if !cached || entry.source != source {
		entry = chatSearchMarkdownCacheEntry{source: source, projection: chatSearchMarkdownText(source)}
		if s.markdownCache == nil {
			s.markdownCache = make(map[int]chatSearchMarkdownCacheEntry)
		}
		s.markdownCache[index] = entry
	}
	projection := entry.projection
	spans, limited := chatSearchSpansBounded(projection.text, s, re, chatSearchMaxMatches-len(out))
	s.candidateTruncated = limited
	s.highlightSkipped = s.highlightSkipped || projection.incomplete
	mapped := make([]chatSearchMatch, 0, len(spans))
	codeHits := make(map[int]int)
	for _, span := range spans {
		start, end, ok := chatSearchMarkdownSourceRange(projection, span[0], span[1])
		if !ok {
			continue
		}
		match := chatSearchMatch{Message: index, Start: start, End: end}
		mapped = append(mapped, match)
		for blockIndex, block := range projection.blocks {
			if !hasCodeSearchRun(block) || block.start >= end || block.end <= start {
				continue
			}
			codeHits[blockIndex]++
		}
	}
	for _, match := range mapped {
		highlightable := true
		for blockIndex, block := range projection.blocks {
			intersects := block.start < match.End && block.end > match.Start
			if hasCodeSearchRun(block) && intersects && codeHits[blockIndex] > chatSearchCodeMaxSpans {
				highlightable = false
				break
			}
			if truncated && intersects && ((block.codeBlock && block.end >= len(source)-2) || (block.tableCell && block.tableTail)) {
				highlightable = false
				break
			}
		}
		if !highlightable {
			s.highlightSkipped = true
			continue
		}
		out = append(out, match)
		if len(out) >= chatSearchMaxMatches {
			return out, false
		}
	}
	return out, truncated || limited
}

// chatSearchSpans finds the byte ranges of the query in one message. The offsets
// always address the original text: a lowercased copy changes length for a few
// runes, which sliced the wrong bytes or even panicked.
func chatSearchSpans(text string, s *chatSearch, re *regexp.Regexp, limit int) [][2]int {
	spans, _ := chatSearchSpansBounded(text, s, re, limit)
	return spans
}

func chatSearchSpansBounded(text string, s *chatSearch, re *regexp.Regexp, limit int) ([][2]int, bool) {
	if limit <= 0 {
		return nil, false
	}
	if re != nil {
		var spans [][2]int
		// Search the original string so ^, $ and word boundaries retain their
		// context. Bound candidates too: zero-width or rejected whole-word hits
		// must not allocate a result for every byte in a large message.
		matches := re.FindAllStringIndex(text, chatSearchMaxRegexCandidates+1)
		for i, loc := range matches {
			if i == chatSearchMaxRegexCandidates {
				return spans, true
			}
			start, end := loc[0], loc[1]
			if start < end && (!s.WholeWord || chatSearchWordBoundary(text, start, end)) {
				spans = append(spans, [2]int{start, end})
				if len(spans) == limit {
					return spans, false
				}
			}
		}
		return spans, false
	}
	if s.Query == "" {
		return nil, false
	}
	var spans [][2]int
	if s.MatchCase {
		for from := 0; from < len(text) && len(spans) < limit; {
			at := strings.Index(text[from:], s.Query)
			if at < 0 {
				break
			}
			start := from + at
			end := start + len(s.Query)
			if !s.WholeWord || chatSearchWordBoundary(text, start, end) {
				spans = append(spans, [2]int{start, end})
			}
			from = end
		}
		return spans, false
	}
	if s.literal == nil || s.literalQuery != s.Query || s.literalMatchCase != s.MatchCase {
		s.chatSearchPattern()
	}
	needle := s.literal
	if len(needle) == 0 {
		return nil, false
	}
	// A prefix table avoids restarting a long near-match at every text rune.
	// Keep source byte positions in a bounded ring instead of lowercasing text,
	// which changes byte lengths for some Unicode characters.
	starts := s.literalStarts
	matched, count := 0, 0
	for at, r := range text {
		starts[count%len(starts)] = at
		count++
		r = chatSearchFoldRune(r)
		for matched > 0 && r != needle[matched] {
			matched = s.literalPrefix[matched-1]
		}
		if r == needle[matched] {
			matched++
		}
		if matched == len(needle) {
			start := starts[(count-len(needle))%len(starts)]
			_, size := utf8.DecodeRuneInString(text[at:])
			end := at + size
			if !s.WholeWord || chatSearchWordBoundary(text, start, end) {
				spans = append(spans, [2]int{start, end})
				if len(spans) == limit {
					break
				}
			}
			matched = 0 // Match the non-overlapping ranges used by regexp.FindAll.
		}
	}
	return spans, false
}

func chatSearchFoldRune(r rune) rune {
	canonical := r
	for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
		canonical = min(canonical, next)
	}
	return canonical
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
		return locales.Text("正则有误")
	}
	matches := a.chatSearchMatches(t)
	withSkipped := func(status string) string {
		if s.highlightSkipped {
			return status + "（" + locales.Text("已跳过") + "）"
		}
		return status
	}
	if s.candidateTruncated {
		if len(matches) == 0 {
			return withSkipped(locales.Text("未找到（正则候选过多，搜索已截断）"))
		}
		return withSkipped(fmt.Sprintf(locales.Text("%d of %d（正则候选过多，搜索已截断）"), min(max(s.Current, 0), len(matches)-1)+1, len(matches)))
	}
	if len(matches) == 0 {
		if s.scanTruncated {
			return withSkipped(fmt.Sprintf(locales.Text("未找到（仅搜索前 %d MiB）"), chatSearchMaxBytes>>20))
		}
		return withSkipped(locales.Text("无结果"))
	}
	current := min(max(s.Current, 0), len(matches)-1) + 1
	if s.scanTruncated {
		return withSkipped(fmt.Sprintf(locales.Text("%d of %d（仅搜索前 %d MiB）"), current, len(matches), chatSearchMaxBytes>>20))
	}
	if len(matches) >= chatSearchMaxMatches {
		// Say so rather than passing the cap off as the total.
		return withSkipped(fmt.Sprintf("%d of %d+", current, len(matches)))
	}
	return withSkipped(fmt.Sprintf("%d of %d", current, len(matches)))
}

// chatSearchBar draws the bar above the transcript.
func (a *desktop) chatSearchBar(c *ui.Context, t *conversation) {
	s := &t.uiState().Search
	th := c.Theme()
	bar := ui.Row(c.Key(t.ID+"-search-bar")).Label(locales.Text("对话查找")).AlignItems(ui.Center).Gap(th.Space(1)).Wrap().
		Padding(th.Space(1.5)).Background(th.Surface).Border(1, th.Border).Radius(th.Radius).
		Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.18)).Shrink(0)
	bar.Children(func() {
		field := ui.TextInputBase(c.Key(t.ID+"-search-query"), &s.Query).
			Width(th.Space(50)).MaxWidthPercent(100).MinWidth(80).Placeholder(locales.Text("在对话中查找")).Padding(th.Space(1), th.Space(2)).
			Background(th.Background).Border(1, th.Border).Radius(th.Radius-2).FontSize(th.FontSize-1).
			Label(locales.Text("在对话中查找")).FocusBind(&a.chatSearchFocus, t.ID+"-search-query")
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
			s.Current = 0
			s.frameKey = ""
			scheduleChatSearchReveal(s)
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
			if b.Changed() {
				s.Current = 0
				s.frameKey = ""
				scheduleChatSearchReveal(s)
			}
		}
		toggle(&s.MatchCase, "Aa", locales.Text("区分大小写"))
		toggle(&s.WholeWord, "ab", locales.Text("全词匹配"))
		toggle(&s.Regexp, ".*", locales.Text("使用正则表达式"))
		ui.Text(c, a.chatSearchStatus(t)).FontSize(th.FontSize - 2).TextColor(a.secondaryInk()).
			FontFeatures("tnum").MinWidth(th.Space(16)).Role(ui.RoleStatus)
		mutedIconAction(c, t.ID+"-search-prev", locales.Text("上一个匹配"), "arrowup", 24, func() { a.chatSearchStep(t, -1) }).Shrink(0)
		mutedIconAction(c, t.ID+"-search-next", locales.Text("下一个匹配"), "arrowdown", 24, func() { a.chatSearchStep(t, 1) }).Shrink(0)
		mutedIconAction(c, t.ID+"-search-close", locales.Text("关闭查找（Esc）"), "close", 24, func() { a.closeChatSearch(t) }).Shrink(0)
		if hit, ok := a.chatSearchCurrent(t); ok && hit.Message >= 0 && hit.Message < len(t.Messages) {
			before, match, after := chatSearchExcerpt(messageDisplayContent(t.Messages[hit.Message]), hit.Start, hit.End)
			if match != "" {
				ui.RichText(c, ui.Span{Text: before}, ui.Span{Text: match, Background: th.Accent, Color: th.Background, Radius: 3}, ui.Span{Text: after}).
					FontSize(th.FontSize - 2).TextColor(a.secondaryInk()).MaxWidthPercent(100)
			}
		}
	})
}

// chatSearchExcerpt reads only a bounded amount around a hit, including for a
// very large regexp match. Offsets may be stale while a message is streaming.
func chatSearchExcerpt(text string, start, end int) (string, string, string) {
	if start < 0 || end <= start || end > len(text) || !utf8.RuneStart(text[start]) || (end < len(text) && !utf8.RuneStart(text[end])) {
		return "", "", ""
	}
	left := start
	for n := 0; n < 20 && left > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(text[:left])
		left -= size
	}
	middle := start
	for n := 0; n < 48 && middle < end; n++ {
		_, size := utf8.DecodeRuneInString(text[middle:end])
		middle += size
	}
	right := end
	for n := 0; n < 20 && right < len(text); n++ {
		_, size := utf8.DecodeRuneInString(text[right:])
		right += size
	}
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return ' '
			}
			return r
		}, s)
	}
	before, match, after := clean(text[left:start]), clean(text[start:middle]), clean(text[end:right])
	if left > 0 {
		before = "…" + before
	}
	if middle < end {
		match += "…"
	}
	if right < len(text) {
		after += "…"
	}
	return before, match, after
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

// chatSearchBlock overlays search backgrounds on the visible inline runs of
// paragraph and heading blocks. List markers and quote indentation remain in the
// native renderer; inline emphasis, code and links are rebuilt as rich text/link
// children. Blocks containing unsupported inline objects are left untouched.
func (a *desktop) chatSearchBlock(t *conversation, message int) func(c *ui.Context, doc *parser.Document, index int) bool {
	return func(c *ui.Context, doc *parser.Document, index int) bool {
		s := &t.uiState().Search
		// The code-block hook of this message runs right after this one, so it can
		// take the block's lines from the document instead of parsing again.
		s.frameDoc, s.frameDocMessage = doc, message
		if doc.Block(index).Kind == parser.Table {
			return a.chatSearchTable(c, t, message, doc, index)
		}
		text, spans, sources, runs, base, ok := chatSearchBlockProjection(doc, index, message, s)
		if !ok {
			return false
		}
		a.chatSearchMark(c, t, message, doc.Block(index), text, spans, sources, runs, base)
		return true
	}
}

// chatSearchBlockSpans reports the visible text and matched runs of a paragraph,
// heading or table cell, with the source offset needed to identify the current
// match. Code blocks use their own line-based projection.
func chatSearchBlockSpans(doc *parser.Document, index, message int, s *chatSearch) (string, [][2]int, int, bool) {
	text, spans, _, _, base, ok := chatSearchBlockProjection(doc, index, message, s)
	return text, spans, base, ok
}

func chatSearchBlockProjection(doc *parser.Document, index, message int, s *chatSearch) (string, [][2]int, []int, []chatSearchMarkdownRun, int, bool) {
	block := doc.Block(index)
	if block == nil || (block.Kind != parser.Paragraph && block.Kind != parser.Heading && block.Kind != parser.TableCell) {
		return "", nil, nil, nil, 0, false
	}
	projection, found := chatSearchBlockProjectionFor(doc, index, message, s)
	if !found || projection.text == "" {
		return "", nil, nil, nil, 0, false
	}
	spans, sources := chatSearchMarkdownBlockSpans(projection, message, s.cache)
	if len(spans) == 0 {
		return "", nil, nil, nil, 0, false
	}
	base := sources[0] - spans[0][0]
	return projection.text, spans, sources, projection.runs, base, true
}

func chatSearchBlockProjectionFor(doc *parser.Document, index, message int, s *chatSearch) (chatSearchMarkdownBlock, bool) {
	block := doc.Block(index)
	if entry, ok := s.markdownCache[message]; ok {
		for _, candidate := range entry.projection.blocks {
			if candidate.start == block.Start && candidate.end == block.End {
				return candidate, true
			}
		}
	}
	// Direct unit callers may supply matches without passing through the scan.
	return chatSearchMarkdownInlineBlock(doc, index)
}

func hasCodeSearchRun(block chatSearchMarkdownBlock) bool {
	return block.codeBlock
}

func chatSearchMarkdownBlockSpans(projection chatSearchMarkdownBlock, message int, matches []chatSearchMatch) ([][2]int, []int) {
	var spans [][2]int
	var sources []int
	for _, hit := range matches {
		if hit.Message != message {
			continue
		}
		visibleStart, visibleEnd := -1, -1
		for _, run := range projection.runs {
			if run.sourceStart >= hit.End || run.sourceEnd <= hit.Start {
				continue
			}
			left, right := max(hit.Start, run.sourceStart), min(hit.End, run.sourceEnd)
			vs, ve := run.visibleStart, run.visibleEnd
			if run.visibleEnd-run.visibleStart == run.sourceEnd-run.sourceStart {
				vs += left - run.sourceStart
				ve = run.visibleStart + right - run.sourceStart
			}
			if visibleStart < 0 || vs < visibleStart {
				visibleStart = vs
			}
			visibleEnd = max(visibleEnd, ve)
		}
		if visibleStart >= 0 && visibleEnd > visibleStart {
			spans = append(spans, [2]int{visibleStart, visibleEnd})
			sources = append(sources, hit.Start)
		}
	}
	return spans, sources
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

// syncCacheQuery drops what the caches hold when the query changed: they are
// keyed by block alone, so the query is a generation rather than part of a key.
func (s *chatSearch) syncCacheQuery() {
	if key := chatSearchQueryKey(s); s.cacheQuery != key {
		s.cacheQuery = key
		s.codeCache = nil
	}
}

// chatSearchMark draws text with the matched runs highlighted. The run the bar
// points at carries the accent solid, so the current hit is obvious at a glance
// and the others stay a tint of it rather than VS Code's two yellows.
//
// The text takes the Markdown theme's own font, size, line height and colour: a
// block that measured differently would shift the transcript the moment the bar
// opened, and the scroll that follows would look like a second, rigid jump.
func (a *desktop) chatSearchMark(c *ui.Context, t *conversation, message int, block *parser.Block, text string, spans [][2]int, sources []int, runs []chatSearchMarkdownRun, base int) {
	a.chatSearchMarkWeight(c, t, message, block, text, spans, sources, runs, base, 0)
}

func (a *desktop) chatSearchMarkWeight(c *ui.Context, t *conversation, message int, block *parser.Block, text string, spans [][2]int, sources []int, runs []chatSearchMarkdownRun, base, weightOverride int) {
	p := a.currentPalette()
	current, hasCurrent := a.chatSearchCurrent(t)
	size, weight, ink := a.chatSearchTextStyle(block)
	if weightOverride > 0 {
		weight = weightOverride
	}
	if hasCurrent && current.Message != message {
		hasCurrent = false
	}
	colors := a.readingColors()
	codeFont, codeSize := nativeFontFamily(), size
	if messageOptions := t.MD[message]; messageOptions != nil && messageOptions.Theme != nil {
		if messageOptions.Theme.CodeFont != "" {
			codeFont = messageOptions.Theme.CodeFont
		}
		codeSize = messageOptions.Theme.FontSize * messageOptions.Theme.CodeSize
	}
	pointed, pointedAt := -1, 0
	view := ui.RichText(c).FillWidth().Font(nativeFontFamily()).FontSize(size).
		LineHeight(readingLineHeight).TextColor(ink)
	if weight > 0 {
		view.FontWeight(weight)
	}
	if block != nil && block.Kind == parser.Heading {
		view.Role(ui.RoleHeading).Level(min(max(block.Level, 1), 6))
	}
	view.Children(func() {
		for _, mapped := range runs {
			start, end := mapped.visibleStart, mapped.visibleEnd
			if start < 0 || end > len(text) || start >= end {
				continue
			}
			parts := make([]ui.Span, 0, 4)
			at := start
			for i, hit := range spans {
				left, right := max(start, hit[0]), min(end, hit[1])
				if left >= right {
					continue
				}
				if left > at {
					parts = append(parts, chatSearchStyledSpan(text[at:left], mapped, colors, codeFont, codeSize, ui.Span{}))
				}
				mark := ui.Span{Background: p.accent.Alpha(0.38)}
				if hasCurrent && i < len(sources) && current.Start == sources[i] {
					mark.Background, mark.Color = p.accent, c.Theme().Background
					pointed, pointedAt = i, left
				}
				parts = append(parts, chatSearchStyledSpan(text[left:right], mapped, colors, codeFont, codeSize, mark))
				at = right
			}
			if at < end {
				parts = append(parts, chatSearchStyledSpan(text[at:end], mapped, colors, codeFont, codeSize, ui.Span{}))
			}
			if len(parts) == 0 {
				continue
			}
			if mapped.link {
				a.chatSearchLink(c, t, message, mapped.linkDest, mapped.linkPending, func() { ui.RichText(c, parts...) })
			} else {
				ui.RichText(c, parts...)
			}
		}
	})
	if pointed < 0 {
		return
	}
	// Anchor the block that actually contains the selected visible run.
	s := &t.uiState().Search
	if s.blockHandle == nil {
		s.blockHandle = &ui.Handle{}
	}
	s.blockMatch, s.blockBound = current, true
	s.blockFrac = 0
	if len(text) > 0 {
		s.blockFrac = float32(pointedAt) / float32(len(text))
	}
	view.Bind(s.blockHandle)
	if block != nil && block.Kind == parser.TableCell && s.reveal {
		view.ScrollIntoView()
	}
}

func (a *desktop) chatSearchLink(c *ui.Context, t *conversation, message int, dest string, pending bool, children func()) {
	link := ui.Link(c, "", "")
	if pending {
		// A destination still being written is not clickable in the native renderer.
	} else if strings.HasPrefix(dest, "#") {
		if link.Clicked() && t.MD[message] != nil {
			t.MD[message].Anchor = dest
			t.MD[message].AnchorVersion++
		}
	} else if !strings.ContainsAny(dest, "\\\x00") && !strings.HasPrefix(dest, "//") {
		if i := strings.IndexAny(dest, ":/?#"); i < 0 || dest[i] != ':' {
			if c.Router() != nil {
				link = ui.Link(c, "", dest)
			}
		} else if ui.SafeToOpen(dest) {
			link = ui.Link(c, "", dest)
		}
	}
	link.TextColor(a.readingColors().link).Children(children)
}

func chatSearchStyledSpan(text string, mapped chatSearchMarkdownRun, colors markdownColors, codeFont string, codeSize float32, mark ui.Span) ui.Span {
	span := ui.Span{Text: text, Weight: 0}
	if mapped.bold {
		span.Weight, span.Color = 600, colors.strong
	}
	span.Italic, span.Strikethrough = mapped.italic, mapped.strike
	if mapped.code {
		span.Font, span.Size, span.Color = codeFont, codeSize, colors.code
		span.Background = colors.codeBG
	}
	if mapped.link {
		span.Color, span.Underline = colors.link, true
	}
	if mark.Background.A > 0 {
		span.Background, span.Radius = mark.Background, mark.Radius
		if mark.Color.A > 0 {
			span.Color = mark.Color
		}
	}
	return span
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
// Hit blocks retain token colours and the copy control. Their custom text runs
// do not have the native code view's per-line Markdown selection map; closing the
// search restores the full native code renderer.
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
	codeFont := "monospace"
	if options := t.MD[message]; options != nil && options.Theme != nil && options.Theme.CodeFont != "" {
		codeFont = options.Theme.CodeFont
	}
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
	var tokenizer editor.Tokenizer
	if language := editor.LanguageNamed(block.Language); language != nil && block.Language != "" {
		tokenizer = language.Tokenizer
	}
	var tokenState uint64
	var plainToken []editor.Token
	if tokenizer == nil {
		plainToken = make([]editor.Token, 1)
	}
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
				tokens := []editor.Token(nil)
				if tokenizer != nil {
					tokens, tokenState = tokenizer.Tokenize(line, tokenState, tokens)
				} else if len(line) > 0 {
					// Unlabelled/plain code blocks have no syntax tokenizer, but they
					// still need a text token so search spans get overlaid.
					plainToken[0] = editor.Token{End: len(line), Kind: editor.TokenText}
					tokens = plainToken
				}
				at, pointedAt := 0, -1
				appendSyntax := func(text string, color ui.Color, weight int, italic bool) {
					if text != "" {
						parts = append(parts, ui.Span{Text: text, Color: color, Weight: weight, Italic: italic})
					}
				}
				for _, token := range tokens {
					end := min(token.End, len(line))
					if end <= at {
						continue
					}
					style := colors.syntax.Tokens[token.Kind]
					color := colors.syntax.TokenColor(token.Kind)
					weight := 0
					if style.Bold {
						weight = 600
					}
					italic := style.Italic
					tokenAt := at
					for _, span := range spans[i] {
						left, right := max(tokenAt, span[0]), min(end, span[1])
						if left >= right {
							continue
						}
						appendSyntax(line[tokenAt:left], color, weight, italic)
						run := ui.Span{Text: line[left:right], Background: p.accent.Alpha(0.38), Weight: weight, Italic: italic}
						if hasCurrent && current.Start == starts[i]+left {
							run.Background, run.Color = p.accent, c.Theme().Background
							pointedAt = left
						}
						parts = append(parts, run)
						tokenAt = right
					}
					appendSyntax(line[tokenAt:end], color, weight, italic)
					at = end
				}
				if at < len(line) {
					appendSyntax(line[at:], colors.text, 0, false)
				}
				if len(parts) == 0 {
					// A blank line still holds the row: the block keeps its height.
					parts = append(parts, ui.Span{Text: " "})
				}
				view := ui.RichText(c.Key(i), parts...).FillWidth().Font(codeFont).FontSize(codeSize).
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
	if end-start > chatSearchCodeMaxBytes || end <= start {
		return chatSearchCode{}
	}
	n := doc.LineCount(index)
	if n > chatSearchCodeMaxLines {
		return chatSearchCode{}
	}
	hits := 0
	for _, hit := range s.cache {
		if hit.Message == message && hit.Start < end && hit.End > start {
			hits++
		}
	}
	if hits > chatSearchCodeMaxSpans {
		return chatSearchCode{}
	}
	src := doc.Source()
	out := chatSearchCode{lines: make([]string, 0, n), starts: make([]int, 0, n), spans: make([][][2]int, 0, n)}
	size := 0
	hits = 0
	for k := 0; k < n; k++ {
		line, at := doc.Line(index, k)
		if size += len(line); size > chatSearchCodeMaxBytes {
			return chatSearchCode{}
		}
		// A line the parser padded for a tab it consumed is longer than its source,
		// so the offsets taken from it are shifted: report where the line starts in
		// the source, not where the padded one does.
		at -= chatSearchCodePad(src, line, at)
		found := chatSearchRangeSpans(s.cache, message, at, at+len(line))
		out.lines, out.starts, out.spans = append(out.lines, line), append(out.starts, at), append(out.spans, found)
		hits += len(found)
	}
	if hits == 0 {
		return chatSearchCode{}
	}
	out.ok = true
	return out
}

// Project the authoritative message matches onto a rendered range. Rescanning
// individual lines or trimmed prose would change regexp anchors and boundaries,
// miss multiline matches and highlight runs beyond the transcript's scan cap.
func chatSearchRangeSpans(matches []chatSearchMatch, message, start, end int) [][2]int {
	var spans [][2]int
	for _, hit := range matches {
		if hit.Message != message || hit.Start >= end || hit.End <= start {
			continue
		}
		left, right := max(hit.Start, start), min(hit.End, end)
		if left < right {
			spans = append(spans, [2]int{left - start, right - start})
		}
	}
	return spans
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
		return locales.Text("代码")
	}
	return locales.Text("代码") + ", " + language
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
func (a *desktop) chatSearchReveal(c *ui.Context, t *conversation) {
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
	if !st.built.holds(target.Message) {
		// The list has not built this row: scroll to it and let the next frame
		// lay out the block that holds the hit. This wait has its own budget, so
		// reaching the row never eats the one the anchor needs.
		if !a.awaitChatSearchFrame(c, &s.waitedWindow) {
			a.endChatSearchJump(s)
			return
		}
		if row := t.rowOfMessage(target.Message); row >= 0 {
			st.List.ScrollTo(row, ui.Start)
		}
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
	st.Follow = false
	t.Scroll.Y = max(float32(0), t.Scroll.Y+delta)
	a.endChatSearchJump(&st.Search)
}
