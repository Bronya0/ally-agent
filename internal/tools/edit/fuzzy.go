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
	"golang.org/x/text/unicode/norm"
)

// Character-level fallback for exact-text edits.
//
// This is the last resort used only when an exact match fails. It normalizes the
// character differences a copy/paste out of rendered text introduces (smart
// quotes, Unicode dashes, special spaces) plus trailing whitespace on each line,
// locates candidate source text. Only canonical Unicode differences authorize
// an automatic replacement; punctuation/whitespace differences require an
// exact retry using the original candidate:
//
//   - A match is accepted only when it is unique in normalized space.
//     Normalization maps different characters onto the same one, so ambiguity
//     is more likely, not less -- the uniqueness check errs on the safe side.
//   - Only the matched region is rewritten. Everything outside it keeps its
//     original bytes, including the rest of the touched line and the trailing
//     whitespace the per-line strip removed for comparison.
//   - NFC accepts canonically equivalent Unicode spellings. Boundaries map only
//     at complete normalization segments; no splice can split a combining
//     sequence. Compatibility forms (NFKC), escapes and entities are left alone.
//   - Unchanged anchors keep original bytes only when canonically equivalent.
//     Different quote/space/dash context is reported for an exact retry: its
//     intended meaning cannot be inferred from normalized punctuation.
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
//   - canonically equivalent Unicode sequences (NFC)
//   - strips trailing whitespace from every line
//   - smart quotes to ASCII quotes
//   - Unicode dashes/hyphens to ASCII hyphen
//   - special spaces (NBSP, various narrow spaces, ideographic space) to space
//
// Newlines are preserved exactly. Normalization segments let the match map
// back to original bytes without a file-sized offset table.
func NormalizeForFuzzyMatch(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = normalizeFuzzyLine(line)
	}
	return strings.Join(lines, "\n")
}

func normalizeFuzzyLine(line string) string {
	return strings.Map(normalizeFuzzyRune, norm.NFC.String(strings.TrimRightFunc(line, unicode.IsSpace)))
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
	if strings.Index(normContent[idx+1:], normOld) >= 0 {
		return normalizedMatch{}, false, toolerrors.New("E_MULTI_MATCH", fmt.Errorf(
			"change %d oldText occurs more than once after Unicode normalization; include more surrounding text to make it unique", changeIndex))
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
		// Refuse partial normalization segments rather than splice inside a
		// combining sequence or rewrite the surrounding block.
		return normalizedMatch{}, false, toolerrors.New("E_NO_MATCH", fmt.Errorf(
			"change %d normalized match could not be mapped back onto the original bytes; use the attached current snapshot, or re-read if none is attached, and include more surrounding text", changeIndex))
	}

	// Identical context is an anchor, not a request to change it. Preserve its
	// canonical spelling, but refuse ambiguous punctuation/whitespace changes.
	sourceStart, sourceEnd := origMatchStart, origMatchEnd
	if norm.NFC.String(content[sourceStart:sourceEnd]) != norm.NFC.String(oldText) {
		return normalizedMatch{}, false, fuzzySourceMismatch(content, sourceStart, sourceEnd, changeIndex)
	}
	prefix, suffix := commonTextEdges(oldText, newText)
	if prefix > 0 {
		offset, ok := normalizedSourceOffset(oldText, prefix)
		if !ok {
			return normalizedMatch{}, false, fuzzySourceMismatch(content, sourceStart, sourceEnd, changeIndex)
		}
		start, ok := originalNormalizedOffset(content, normContent, idx+offset)
		if !ok || start < origMatchStart || start > origMatchEnd {
			return normalizedMatch{}, false, fuzzySourceMismatch(content, sourceStart, sourceEnd, changeIndex)
		}
		origMatchStart = start
	}
	if suffix > 0 {
		offset, ok := normalizedSourceOffset(oldText, len(oldText)-suffix)
		if !ok {
			return normalizedMatch{}, false, fuzzySourceMismatch(content, sourceStart, sourceEnd, changeIndex)
		}
		end, ok := originalNormalizedOffset(content, normContent, idx+offset)
		if !ok || end < origMatchStart || end > origMatchEnd {
			return normalizedMatch{}, false, fuzzySourceMismatch(content, sourceStart, sourceEnd, changeIndex)
		}
		origMatchEnd = end
	}
	newText = newText[prefix : len(newText)-suffix]

	// Return only the matched bytes, so disjoint edits on the same line do not
	// conflict. The batch assembler preserves all surrounding original bytes.
	return normalizedMatch{start: origMatchStart, end: origMatchEnd, newText: newText}, true, nil
}

func fuzzySourceMismatch(content string, start, end, changeIndex int) error {
	details := &MatchErrorDetails{
		ChangeIndex: changeIndex,
		MatchType:   "normalized",
		MatchCount:  1,
		Candidates:  []MatchCandidate{matchCandidateAtLine(content, start, end, lineIndexOfOffset(content, start)+1)},
		Recovery:    "Read this candidate and copy its actual characters into oldText. Decide explicitly which quotes, spaces or dashes newText should preserve or change; do not guess from normalized text.",
	}
	boundMatchErrorDetails(details)
	return toolerrors.NewWithDetails("E_NO_MATCH", fmt.Errorf("change %d has a normalized candidate but its exact source or replacement boundary is uncertain; copy the original candidate and retry", changeIndex), details)
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

// mapNormOffsetInLine accepts only complete NFC segment boundaries. Whitespace
// omitted during comparison stays outside the replacement.
func mapNormOffsetInLine(origLine string, normOff int) (int, bool) {
	origLine = strings.TrimRightFunc(origLine, unicode.IsSpace)
	var iter norm.Iter
	iter.InitString(norm.NFC, origLine)
	normLen := 0
	for !iter.Done() {
		if normLen == normOff {
			return iter.Pos(), true
		}
		normLen += len(strings.Map(normalizeFuzzyRune, string(iter.Next())))
	}
	if normLen == normOff {
		return len(origLine), true
	}
	return 0, false
}

func originalNormalizedOffset(content, normalized string, offset int) (int, bool) {
	line := lineIndexOfOffset(normalized, offset)
	start := lineStartOffset(content, line)
	inLine, ok := mapNormOffsetInLine(content[start:lineContentEnd(content, line)], offset-lineStartOffset(normalized, line))
	return start + inLine, ok
}

// normalizedSourceOffset maps an unchanged anchor boundary into comparison
// space. Boundaries inside combining sequences or beyond stripped trailing
// whitespace cannot map back to the same bytes and are refused.
func normalizedSourceOffset(text string, offset int) (int, bool) {
	line := lineIndexOfOffset(text, offset)
	start := lineStartOffset(text, line)
	original := text[start:lineContentEnd(text, line)]
	trimmed := strings.TrimRightFunc(original, unicode.IsSpace)
	var iter norm.Iter
	iter.InitString(norm.NFC, trimmed)
	normalized := len(NormalizeForFuzzyMatch(text[:start]))
	for !iter.Done() {
		if iter.Pos() == offset-start {
			return normalized, true
		}
		normalized += len(strings.Map(normalizeFuzzyRune, string(iter.Next())))
	}
	return normalized, offset-start == len(trimmed)
}

// commonTextEdges returns disjoint identical prefixes/suffixes on rune boundaries.
func commonTextEdges(a, b string) (prefix, suffix int) {
	for prefix < len(a) && prefix < len(b) {
		ar, as := utf8.DecodeRuneInString(a[prefix:])
		br, bs := utf8.DecodeRuneInString(b[prefix:])
		if ar != br || as != bs {
			break
		}
		prefix += as
	}
	for len(a)-suffix > prefix && len(b)-suffix > prefix {
		ar, as := utf8.DecodeLastRuneInString(a[:len(a)-suffix])
		br, bs := utf8.DecodeLastRuneInString(b[:len(b)-suffix])
		if ar != br || as != bs {
			break
		}
		suffix += as
	}
	return prefix, suffix
}
