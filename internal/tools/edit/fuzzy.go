package edit

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	toolerrors "ally-dev/internal/tools/shared"
)

// Unicode normalization fallback for exact-text edits.
//
// This file implements a deliberately conservative "fuzzy" matching fallback
// used only when an exact match fails. It is modeled on pi's edit-diff
// normalization, tightened to Ally's safety rules:
//
//   - Normalization only locates the source; the matched lines are rewritten
//     from the normalized block plus the model's newText, while every line
//     outside the touched block keeps its original bytes.
//   - A match is accepted only when it is unique in normalized space.
//     Normalization maps different characters onto the same one, so ambiguity
//     is more likely, not less — the uniqueness check errs on the safe side.
//   - Leading indentation is intentionally NOT normalized here; the existing
//     indentation-insensitive fallback owns that dimension. This fallback
//     covers character-level differences: full-width/smart quotes, Unicode
//     dashes, special spaces, NFKC compatibility forms, and trailing
//     whitespace.

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
// normalized content and maps the touched line block back to the original
// content. Returns:
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

	// Map the match range to the touched line block. normContent and content
	// have identical newline layout, so line numbers transfer directly.
	normEnd := idx + len(normOld)
	if normEnd > idx && normContent[normEnd-1] == '\n' {
		normEnd-- // ends exactly at a line break: do not drag the next line in
	}
	startLine := lineIndexOfOffset(normContent, idx)
	endLine := lineIndexOfOffset(normContent, normEnd)

	normLineStart := lineStartOffset(normContent, startLine)
	normLineEnd := lineEndOffsetInclusive(normContent, endLine)
	if normLineStart > normLineEnd || normLineEnd > len(normContent) {
		return normalizedMatch{}, false, nil
	}

	block := normContent[normLineStart:normLineEnd]
	replaced := strings.Replace(block, normOld, newText, 1)

	origStart := lineStartOffset(content, startLine)
	origEnd := lineEndOffsetInclusive(content, endLine)
	if origStart > origEnd || origEnd > len(content) {
		return normalizedMatch{}, false, nil
	}

	return normalizedMatch{start: origStart, end: origEnd, newText: replaced}, true, nil
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
