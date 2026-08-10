package edit

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	toolerrors "ally-dev/internal/tools/shared"
)

// Unicode normalization fallback for exact-text edits.
//
// This file implements a deliberately conservative "fuzzy" matching fallback
// used only when an exact match fails. It is modeled on pi's edit-diff
// normalization, tightened to Ally's safety rules:
//
//   - Normalization only locates the source; only the matched region is
//     rewritten (with the model's newText). Every byte outside the matched
//     region — including other characters on the touched lines — keeps its
//     original bytes.
//   - A match is accepted only when it is unique in normalized space.
//     Normalization maps different characters onto the same one, so ambiguity
//     is more likely, not less — the uniqueness check errs on the safe side.
//   - Leading indentation is intentionally NOT normalized here; the existing
//     indentation-insensitive fallback owns that dimension. This fallback
//     covers character-level differences: full-width/smart quotes, Unicode
//     dashes, special spaces, NFKC compatibility forms, and trailing
//     whitespace.
//   - Inputs are expected to be LF-normalized (callers normalize CRLF before
//     calling); the pure layer does not restore line endings.

const (
	smartSingleQuoteRunes = "\u2018\u2019\u201A\u201B" // ' ' ‚ ‛
	smartDoubleQuoteRunes = "\u201C\u201D\u201E\u201F" // " " „ ‟
	dashRunes             = "\u2010\u2011\u2012\u2013\u2014\u2015\u2212"
)

// NormalizeForFuzzyMatch normalizes text for comparison:
//   - NFKC compatibility decomposition (full-width ASCII, ligatures, etc.)
//   - strips trailing whitespace from every line
//   - smart/full-width quotes to ASCII quotes
//   - Unicode dashes/hyphens to ASCII hyphen
//   - special spaces (NBSP, various narrow spaces, ideographic space) to space
//
// Newlines are preserved exactly, so normalized text has the same line count
// as the input. This is the only place that normalization happens; the result
// is used both for locating the source block and for rebuilding that block.
func NormalizeForFuzzyMatch(text string) string {
	text = norm.NFKC.String(text)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	return strings.Map(normalizeFuzzyRune, strings.Join(lines, "\n"))
}

func normalizeFuzzyRune(r rune) rune {
	switch {
	case strings.ContainsRune(smartSingleQuoteRunes, r):
		return '\''
	case strings.ContainsRune(smartDoubleQuoteRunes, r):
		return '"'
	case strings.ContainsRune(dashRunes, r):
		return '-'
	case r == '\u00A0' || (r >= '\u2002' && r <= '\u200A') || r == '\u202F' || r == '\u205F' || r == '\u3000':
		return ' '
	}
	return r
}

// normalizedMatch locates a unique normalized match and maps it back to byte
// offsets in the original content.
type normalizedMatch struct {
	start   int    // byte offset in the original content
	end     int    // byte offset in the original content (exclusive)
	newText string // replacement for content[start:end]
}

// fuzzyLocateInNormalized finds the unique occurrence of oldText in the
// normalized content and maps the replacement back to byte offsets in the
// original content. Returns:
//
//	match, true, nil  — unique normalized match
//	zero, false, nil  — no match in normalized space
//	zero, false, err  — multiple matches in normalized space (E_MULTI_MATCH)
func fuzzyLocateInNormalized(content, normContent, oldText, newText string, changeIndex int) (normalizedMatch, bool, error) {
	normOld := NormalizeForFuzzyMatch(oldText)
	if normOld == "" {
		return normalizedMatch{}, false, nil
	}

	idx := strings.Index(normContent, normOld)
	if idx < 0 {
		return normalizedMatch{}, false, nil
	}
	if strings.Count(normContent, normOld) > 1 {
		return normalizedMatch{}, false, toolerrors.New("E_MULTI_MATCH", fmt.Errorf(
			"change %d oldText occurs more than once after Unicode normalization (quotes/dashes/spaces); include more surrounding text to make it unique", changeIndex))
	}

	// Replacement region in normalized space is the full normOld occurrence.
	// normContent and content share the same newline layout, so line numbers
	// transfer directly.
	repStart, repEnd := idx, idx+len(normOld)
	endsWithNewline := repEnd > repStart && normContent[repEnd-1] == '\n'
	startLine := lineIndexOfOffset(normContent, repStart)
	endLine := lineIndexOfOffset(normContent, repEnd)
	if endsWithNewline {
		// The trailing newline belongs to the line before it; the replacement
		// consumes that line's terminating newline as well.
		endLine = lineIndexOfOffset(normContent, repEnd-1)
	}

	origBlockStart := lineStartOffset(content, startLine)
	origBlockEnd := lineEndOffsetInclusive(content, endLine)
	if origBlockStart > origBlockEnd || origBlockEnd > len(content) {
		return normalizedMatch{}, false, nil
	}

	// Map the match boundaries back to original byte offsets inside their
	// lines so that everything outside the match keeps its original bytes.
	origStartInLine, startOK := mapNormOffsetInLine(
		content[lineStartOffset(content, startLine):lineContentEnd(content, startLine)],
		normContent[lineStartOffset(normContent, startLine):lineContentEnd(normContent, startLine)],
		repStart-lineStartOffset(normContent, startLine),
	)
	origMatchStart := lineStartOffset(content, startLine) + origStartInLine

	var origMatchEnd int
	endOK := true
	if endsWithNewline {
		// The replacement consumes the whole end line including its newline.
		origMatchEnd = origBlockEnd
	} else {
		origEndInLine, ok := mapNormOffsetInLine(
			content[lineStartOffset(content, endLine):lineContentEnd(content, endLine)],
			normContent[lineStartOffset(normContent, endLine):lineContentEnd(normContent, endLine)],
			repEnd-lineStartOffset(normContent, endLine),
		)
		endOK = ok
		origMatchEnd = lineStartOffset(content, endLine) + origEndInLine
	}

	if !startOK || !endOK || origMatchStart > origMatchEnd || origMatchEnd > origBlockEnd {
		// The boundary cannot be mapped exactly (a rune's NFKC expansion or
		// composition crosses the match boundary — extremely rare). Fall back
		// to rewriting the whole touched block from the normalized content;
		// the match is still unique, so the edit remains safe.
		block := normContent[lineStartOffset(normContent, startLine):lineEndOffsetInclusive(normContent, endLine)]
		replaced := strings.Replace(block, normOld, newText, 1)
		return normalizedMatch{start: origBlockStart, end: origBlockEnd, newText: replaced}, true, nil
	}

	// Splice: original bytes before the match, the new text, then the
	// original bytes after the match (including the end line's newline).
	assembled := content[origBlockStart:origMatchStart] + newText + content[origMatchEnd:origBlockEnd]
	return normalizedMatch{start: origBlockStart, end: origBlockEnd, newText: assembled}, true, nil
}

// lineIndexOfOffset returns the 0-based line number containing byte offset off.
// An offset pointing at a '\n' belongs to the line that the newline terminates.
func lineIndexOfOffset(text string, off int) int {
	if off > len(text) {
		off = len(text)
	}
	return strings.Count(text[:off], "\n")
}

// lineStartOffset returns the byte offset of the start of the 0-based line.
func lineStartOffset(text string, line int) int {
	if line <= 0 {
		return 0
	}
	offset := 0
	for current := 0; current < line; current++ {
		rel := strings.IndexByte(text[offset:], '\n')
		if rel < 0 {
			return len(text)
		}
		offset += rel + 1
	}
	if offset > len(text) {
		return len(text)
	}
	return offset
}

// lineEndOffsetInclusive returns the byte offset just past the end of the
// 0-based line, including its terminating newline when present.
func lineEndOffsetInclusive(text string, line int) int {
	start := lineStartOffset(text, line)
	if start >= len(text) {
		return len(text)
	}
	if rel := strings.IndexByte(text[start:], '\n'); rel >= 0 {
		return start + rel + 1
	}
	return len(text)
}

// lineContentEnd returns the byte offset just past the content of the 0-based
// line, excluding its terminating newline.
func lineContentEnd(text string, line int) int {
	end := lineEndOffsetInclusive(text, line)
	if end > lineStartOffset(text, line) && text[end-1] == '\n' {
		return end - 1
	}
	return end
}

// mapNormOffsetInLine maps a byte offset in the normalized form of one line
// back to a byte offset in the original line. normLine must be
// NormalizeForFuzzyMatch(origLine). Returns ok=false when the boundary cannot
// be mapped exactly — for example when NFKC composition crosses the boundary
// or a rune's expansion contains the boundary — so callers can fall back to
// the whole-block rewrite.
//
// The mapping walks the original line rune by rune, accumulating the
// normalized length of the prefix. Slicing only ever happens at rune
// boundaries, so invalid UTF-8 prefixes (which would corrupt NFKC) never
// occur. A whitespace rune contributes zero while it is the last rune of the
// prefix, matching the per-line trailing-whitespace strip. The result is
// verified against normLine before it is accepted.
func mapNormOffsetInLine(origLine, normLine string, normOff int) (int, bool) {
	if normOff <= 0 {
		return 0, true
	}
	if normOff >= len(normLine) {
		return len(origLine), true
	}
	normLen := 0
	i := 0
	for i < len(origLine) {
		r, size := utf8.DecodeRuneInString(origLine[i:])
		if !unicode.IsSpace(r) {
			normLen += len(strings.Map(normalizeFuzzyRune, norm.NFKC.String(string(r))))
		}
		i += size
		if normLen == normOff {
			if NormalizeForFuzzyMatch(origLine[:i]) == normLine[:normOff] {
				return i, true
			}
			return 0, false
		}
		if normLen > normOff {
			return 0, false
		}
	}
	return 0, false
}
