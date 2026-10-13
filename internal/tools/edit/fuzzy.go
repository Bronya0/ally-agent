// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package edit

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	toolerrors "ally-dev/internal/tools/shared"
)

// Character-level fallback for exact-text edits.
//
// This is the last resort used only when an exact match fails. It normalizes the
// character differences a copy/paste out of rendered text introduces (smart
// quotes, Unicode dashes, special spaces) plus trailing whitespace on each line,
// locates the source with that, and rewrites only the matched bytes:
//
//   - A match is accepted only when it is unique in normalized space.
//     Normalization maps different characters onto the same one, so ambiguity
//     is more likely, not less -- the uniqueness check errs on the safe side.
//   - Only the matched region is rewritten. Everything outside it keeps its
//     original bytes, including the rest of the touched line and the trailing
//     whitespace the per-line strip removed for comparison.
//   - The map is character for character (one rune in, one rune out), so a
//     normalized byte offset always maps back to a rune boundary in the
//     original text. An earlier NFKC compatibility layer could not promise that,
//     and the byte arithmetic it needed was the fragile part of this file; what
//     it bought was full-width and ligature forms, which do not appear in code.
//   - Leading indentation is deliberately NOT normalized here; the
//     indentation-insensitive fallback owns that dimension.
//   - Inputs are expected to be LF-normalized (callers normalize CRLF before
//     calling); the pure layer does not restore line endings.

const (
	smartSingleQuoteRunes = "\u2018\u2019\u201A\u201B" // ' ' ‚ ‛
	smartDoubleQuoteRunes = "\u201C\u201D\u201E\u201F" // " " „ ‟
	dashRunes             = "\u2010\u2011\u2012\u2013\u2014\u2015\u2212"
)

// NormalizeForFuzzyMatch normalizes text for comparison:
//   - strips trailing whitespace from every line
//   - smart/full-width quotes to ASCII quotes
//   - Unicode dashes/hyphens to ASCII hyphen
//   - special spaces (NBSP, various narrow spaces, ideographic space) to space
//
// Newlines are preserved exactly, so normalized text has the same line count as
// the input, and every rune maps to exactly one rune. That is what lets
// mapNormOffsetInLine translate an offset back exactly. This is the only place
// that normalization happens; the result is used both for locating the source
// block and for rebuilding that block.
func NormalizeForFuzzyMatch(text string) string {
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

	// Map the match boundaries back onto original byte offsets inside their lines so
	// that everything outside the match keeps its original bytes.
	startInLine, startOK := mapNormOffsetInLine(
		content[lineStartOffset(content, startLine):lineContentEnd(content, startLine)],
		repStart-lineStartOffset(normContent, startLine),
	)
	origMatchStart := lineStartOffset(content, startLine) + startInLine

	origMatchEnd := origBlockEnd
	endOK := true
	if !endsWithNewline {
		var endInLine int
		endInLine, endOK = mapNormOffsetInLine(
			content[lineStartOffset(content, endLine):lineContentEnd(content, endLine)],
			repEnd-lineStartOffset(normContent, endLine),
		)
		origMatchEnd = lineStartOffset(content, endLine) + endInLine
	}

	if !startOK || !endOK || origMatchStart > origMatchEnd || origMatchEnd > origBlockEnd {
		// A boundary that does not land on the original bytes. Normalization being
		// character for character, the mapping is exact and this cannot happen; refusing
		// keeps it that way instead of splicing at an offset nobody verified. The
		// alternative, rewriting the whole touched block from normalized content, would
		// silently replace bytes the model never mentioned.
		return normalizedMatch{}, false, toolerrors.New("E_NO_MATCH", fmt.Errorf(
			"change %d normalized match could not be mapped back onto the original bytes; re-read the file and include more surrounding text", changeIndex))
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

// mapNormOffsetInLine maps a byte offset in the normalized form of one line back to
// a byte offset in the original line, and reports whether it could be placed there.
// The offset comes from a strings.Index over normalized text, so it lands on a rune
// boundary.
//
// Normalization is character for character, so the walk is exact: advance one
// original rune at a time while accumulating the normalized byte length and stop at
// the boundary whose normalized prefix is normOff bytes long. Reaching the end of the
// normalized line that way is how a match running to the end of the line maps back to
// the end of the line's content: the per-line trailing-whitespace strip cut the
// normalized line short, so whitespace the model never mentioned keeps its bytes.
//
// An offset past that point cannot be placed, and the caller refuses the change rather
// than splicing at a guessed position.
func mapNormOffsetInLine(origLine string, normOff int) (int, bool) {
	normLen := 0
	for i := 0; i < len(origLine); {
		if normLen == normOff {
			return i, true
		}
		r, size := utf8.DecodeRuneInString(origLine[i:])
		normLen += len(string(normalizeFuzzyRune(r)))
		i += size
	}
	// The walk consumed the line and landed on normOff exactly: the match ran to the
	// end of the normalized line, so it ends where the original line's bytes end.
	if normLen == normOff {
		return len(origLine), true
	}
	return 0, false
}
