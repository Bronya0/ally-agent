// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package edit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"ally-dev/internal/tools/read"
	toolerrors "ally-dev/internal/tools/shared"
)

// TextChange replaces a non-empty source snippet in the original snapshot.
type TextChange struct {
	OldText    string `json:"oldText"`
	NewText    string `json:"newText"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
}

const (
	maxMatchDiagnosticCandidates   = 3
	maxMatchDiagnosticPreviewBytes = 512
	maxMatchDiagnosticTotalBytes   = 4 * 1024
)

// MatchCandidate is one bounded location hint for an ambiguous edit. Preview
// is sampled around the match and never contains more than 512 UTF-8 bytes.
type MatchCandidate struct {
	Line                   int    `json:"line"`
	StartLine              int    `json:"startLine"`
	EndLine                int    `json:"endLine"`
	Preview                string `json:"preview"`
	PreviewTruncatedBefore bool   `json:"previewTruncatedBefore,omitempty"`
	PreviewTruncatedAfter  bool   `json:"previewTruncatedAfter,omitempty"`
}

// MatchErrorDetails helps the model recover from an ambiguous exact edit
// without returning the full file or guessing which occurrence to modify.
type MatchErrorDetails struct {
	ChangeIndex         int              `json:"changeIndex"`
	MatchType           string           `json:"matchType"`
	MatchCount          int              `json:"matchCount"`
	Candidates          []MatchCandidate `json:"candidates"`
	CandidatesTruncated bool             `json:"candidatesTruncated"`
	Recovery            string           `json:"recovery"`
}

// Result captures the output of one edit application: the new content, the
// first/last changed line, and any non-fatal warnings.
type Result struct {
	Content          string
	FirstChangedLine int
	LastChangedLine  int
	Warnings         []string
}

// NormalizeEditString converts Windows CRLF to Unix LF.
func NormalizeEditString(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// ValidateBatchTextChanges checks the model-facing batched changes form.
func ValidateBatchTextChanges(changes []TextChange) error {
	if len(changes) == 0 {
		return toolerrors.New("E_BAD_EDIT", errors.New("changes must contain at least one replacement"))
	}
	if len(changes) > 50 {
		return toolerrors.New("E_BAD_EDIT", errors.New("changes supports at most 50 replacements"))
	}
	for i, change := range changes {
		if change.OldText == "" {
			return toolerrors.New("E_BAD_EDIT", fmt.Errorf("change %d requires non-empty oldText copied from the current file", i+1))
		}
	}
	return nil
}

// stripLineNumberPrefixes removes the "N: " prefixes the read tool prints. It
// only fires when every non-empty line carries one, so text whose own lines
// start with something like "12: " is never rewritten by accident. The label
// names the copied range for the warning.
func stripLineNumberPrefixes(text string) (string, string) {
	lines := strings.Split(text, "\n")
	stripped := make([]string, len(lines))
	first, last := 0, 0
	seen := false
	for i, line := range lines {
		if line == "" {
			continue
		}
		digits := 0
		for digits < len(line) && line[digits] >= '0' && line[digits] <= '9' {
			digits++
		}
		if digits == 0 || digits >= len(line) || line[digits] != ':' {
			return "", ""
		}
		number, err := strconv.Atoi(line[:digits])
		if err != nil {
			return "", ""
		}
		if !seen {
			first, seen = number, true
		}
		last = number
		stripped[i] = strings.TrimPrefix(line[digits+1:], " ")
	}
	if !seen {
		return "", ""
	}
	label := strconv.Itoa(first)
	if last != first {
		label = fmt.Sprintf("%d-%d", first, last)
	}
	return strings.Join(stripped, "\n"), label
}

// exactMatch records an exact match offset and its 1-based source line.
type exactMatch struct {
	offset int
	line   int
}

// scanExactMatches counts non-overlapping occurrences, then checks a lone
// candidate for a second, overlapping location before declaring it unique.
// matches is bounded when limit is positive. The line
// number is advanced incrementally so diagnostics do not repeatedly scan from
// byte 0 for each candidate.
func scanExactMatches(content, needle string, limit int) ([]exactMatch, int) {
	if needle == "" {
		return nil, 0
	}
	capacity := maxMatchDiagnosticCandidates
	if limit > 0 && limit < capacity {
		capacity = limit
	}
	matches := make([]exactMatch, 0, capacity)
	count := 0
	from, line := 0, 1
	for from <= len(content)-len(needle) {
		rel := strings.Index(content[from:], needle)
		if rel < 0 {
			break
		}
		start := from + rel
		matchLine := line + strings.Count(content[from:start], "\n")
		count++
		if limit <= 0 || len(matches) < limit {
			matches = append(matches, exactMatch{offset: start, line: matchLine})
		}
		from = start + len(needle)
		line = matchLine + strings.Count(content[start:from], "\n")
	}
	if count == 1 {
		first := matches[0]
		_, size := utf8.DecodeRuneInString(content[first.offset:])
		if rel := strings.Index(content[first.offset+size:], needle); rel >= 0 {
			start := first.offset + size + rel
			count = 2
			if limit <= 0 || len(matches) < limit {
				matches = append(matches, exactMatch{offset: start, line: first.line + strings.Count(content[first.offset:start], "\n")})
			}
		}
	}
	return matches, count
}

// FormatMatchLines renders a human-readable "at lines X, Y" suffix.
func FormatMatchLines(lines []int, total int) string {
	if len(lines) == 0 {
		return ""
	}
	parts := make([]string, len(lines))
	for i, line := range lines {
		parts[i] = strconv.Itoa(line)
	}
	suffix := ""
	if total > len(lines) {
		suffix = fmt.Sprintf(", and %d more", total-len(lines))
	}
	return " at lines " + strings.Join(parts, ", ") + suffix
}

func exactMatchDiagnosticsAtMatches(content, oldText string, changeIndex, count int, matches []exactMatch) ([]int, *MatchErrorDetails) {
	lines := make([]int, len(matches))
	candidates := make([]MatchCandidate, 0, len(matches))
	for i, match := range matches {
		lines[i] = match.line
		candidates = append(candidates, matchCandidateAtLine(content, match.offset, match.offset+len(oldText), match.line))
	}
	details := &MatchErrorDetails{
		ChangeIndex:         changeIndex,
		MatchType:           "exact",
		MatchCount:          count,
		Candidates:          candidates,
		CandidatesTruncated: count > len(candidates),
		Recovery:            "Use a candidate preview or read the intended target lines, then include enough surrounding text in oldText to make the target unique. Use replaceAll only when every exact occurrence should change. Do not guess which occurrence is intended.",
	}
	boundMatchErrorDetails(details)
	return lines, details
}

func utf8Window(content string, start, end, focusStart, focusEnd, limit int) (string, bool, bool) {
	if start < 0 {
		start = 0
	}
	if end > len(content) {
		end = len(content)
	}
	if focusStart < start {
		focusStart = start
	}
	if focusEnd < focusStart {
		focusEnd = focusStart
	}
	if focusEnd > end {
		focusEnd = end
	}
	if end <= start || limit <= 0 {
		return "", start < focusStart, focusEnd < end
	}
	if end-start <= limit {
		return content[start:end], false, false
	}
	windowStart := focusStart - (limit-(focusEnd-focusStart))/2
	if focusEnd-focusStart >= limit {
		windowStart = focusStart
	}
	if windowStart < start {
		windowStart = start
	}
	if windowStart+limit > end {
		windowStart = end - limit
	}
	windowEnd := windowStart + limit
	for windowStart < windowEnd && windowStart < len(content) && !utf8.RuneStart(content[windowStart]) {
		windowStart++
	}
	for windowEnd > windowStart && windowEnd < len(content) && !utf8.RuneStart(content[windowEnd]) {
		windowEnd--
	}
	return content[windowStart:windowEnd], windowStart > start, windowEnd < end
}

func matchCandidateAtLine(content string, start, end, line int) MatchCandidate {
	lineStart := strings.LastIndexByte(content[:start], '\n') + 1
	previewStart := lineStart
	if lineStart > 0 {
		previewStart = strings.LastIndexByte(content[:lineStart-1], '\n') + 1
	}
	lineEnd := end
	if rel := strings.IndexByte(content[lineEnd:], '\n'); rel >= 0 {
		lineEnd += rel + 1
		if rel = strings.IndexByte(content[lineEnd:], '\n'); rel >= 0 {
			lineEnd += rel
		} else {
			lineEnd = len(content)
		}
	} else {
		lineEnd = len(content)
	}
	startLine := line
	if previewStart < lineStart {
		startLine--
	}
	endLine := startLine + strings.Count(content[previewStart:lineEnd], "\n")
	if lineEnd > previewStart && lineEnd <= len(content) && content[lineEnd-1] == '\n' {
		endLine--
	}
	if endLine < startLine {
		endLine = startLine
	}
	preview, truncatedBefore, truncatedAfter := utf8Window(content, previewStart, lineEnd, start, end, maxMatchDiagnosticPreviewBytes)
	return MatchCandidate{
		Line:                   line,
		StartLine:              startLine,
		EndLine:                endLine,
		Preview:                preview,
		PreviewTruncatedBefore: truncatedBefore,
		PreviewTruncatedAfter:  truncatedAfter,
	}
}

func boundMatchErrorDetails(details *MatchErrorDetails) {
	if details == nil {
		return
	}
	for {
		raw, err := json.Marshal(details)
		if err == nil && len(raw) <= maxMatchDiagnosticTotalBytes {
			return
		}
		longest := -1
		for i := range details.Candidates {
			if longest < 0 || len(details.Candidates[i].Preview) > len(details.Candidates[longest].Preview) {
				longest = i
			}
		}
		if longest < 0 || len(details.Candidates[longest].Preview) == 0 {
			return
		}
		candidate := &details.Candidates[longest]
		newLimit := len(candidate.Preview) / 2
		preview, before, after := utf8Window(candidate.Preview, 0, len(candidate.Preview), len(candidate.Preview)/2, len(candidate.Preview)/2, newLimit)
		candidate.Preview = preview
		candidate.PreviewTruncatedBefore = candidate.PreviewTruncatedBefore || before
		candidate.PreviewTruncatedAfter = candidate.PreviewTruncatedAfter || after
	}
}

type indentationMatch struct {
	start int
	end   int
	line  int
}

type textLineSpan struct {
	start      int
	end        int
	hasNewline bool
}

func nextTextLineSpan(content string, start int) (textLineSpan, bool) {
	if start < 0 || start >= len(content) {
		return textLineSpan{}, false
	}
	if rel := strings.IndexByte(content[start:], '\n'); rel >= 0 {
		return textLineSpan{start: start, end: start + rel, hasNewline: true}, true
	}
	return textLineSpan{start: start, end: len(content)}, true
}

// IndentationInsensitiveMatches is deliberately narrow: it only considers
// multi-line, whole-line blocks and ignores spaces/tabs at the beginning of
// each line. Line bodies, line count, and trailing-newline shape must still
// match exactly. Callers must require exactly one result before editing.
func IndentationInsensitiveMatches(content, oldText string, limit int) []indentationMatch {
	if limit <= 0 || !strings.Contains(oldText, "\n") {
		return nil
	}
	oldTrailing := strings.HasSuffix(oldText, "\n")
	oldLines := strings.Split(oldText, "\n")
	if oldTrailing {
		oldLines = oldLines[:len(oldLines)-1]
	}
	if len(oldLines) < 2 {
		return nil
	}
	matches := make([]indentationMatch, 0, minInt(limit, 4))
	firstOffset := 0
	firstLine := 1
	for firstOffset < len(content) && len(matches) < limit {
		firstSpan, ok := nextTextLineSpan(content, firstOffset)
		if !ok {
			break
		}
		cursor := firstOffset
		matched := true
		var last textLineSpan
		for _, oldLine := range oldLines {
			span, exists := nextTextLineSpan(content, cursor)
			if !exists || strings.TrimLeft(content[span.start:span.end], " \t") != strings.TrimLeft(oldLine, " \t") {
				matched = false
				break
			}
			last = span
			cursor = span.end
			if span.hasNewline {
				cursor++
			}
		}
		if matched && (!oldTrailing || last.hasNewline) {
			end := last.end
			if oldTrailing {
				end++
			}
			matches = append(matches, indentationMatch{start: firstOffset, end: end, line: firstLine})
		}
		firstOffset = firstSpan.end
		if firstSpan.hasNewline {
			firstOffset++
		}
		firstLine++
	}
	return matches
}

func leadingHorizontalWhitespace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// ReindentReplacementForMatch rebases newText's indentation to the file's
// actual indentation at the matched location. Returns ok=false if the
// rebase cannot be performed safely.
func ReindentReplacementForMatch(content, oldText, newText string, match indentationMatch) (string, bool) {
	oldTrailing := strings.HasSuffix(oldText, "\n")
	oldLines := strings.Split(oldText, "\n")
	if oldTrailing {
		oldLines = oldLines[:len(oldLines)-1]
	}
	newTrailing := strings.HasSuffix(newText, "\n")
	newLines := strings.Split(newText, "\n")
	if newTrailing {
		newLines = newLines[:len(newLines)-1]
	}
	if oldTrailing != newTrailing || len(oldLines) == 0 || len(oldLines) != len(newLines) || match.start < 0 || match.end > len(content) {
		return "", false
	}
	adjusted := make([]string, len(newLines))
	cursor := match.start
	for i, line := range newLines {
		span, ok := nextTextLineSpan(content, cursor)
		if !ok || span.end > match.end {
			return "", false
		}
		oldIndent := leadingHorizontalWhitespace(oldLines[i])
		actualLine := content[span.start:span.end]
		actualIndent := leadingHorizontalWhitespace(actualLine)
		newIndent := leadingHorizontalWhitespace(line)
		if !strings.HasPrefix(newIndent, oldIndent) {
			return "", false
		}
		body := strings.TrimLeft(line, " \t")
		if body == "" {
			adjusted[i] = line
		} else {
			adjusted[i] = actualIndent + newIndent[len(oldIndent):] + body
		}
		cursor = span.end
		if span.hasNewline && cursor < match.end {
			cursor++
		}
	}
	if cursor != match.end {
		return "", false
	}
	result := strings.Join(adjusted, "\n")
	if newTrailing {
		result += "\n"
	}
	return result, true
}

// ApplyBatchTextChanges locates all source snippets in one immutable snapshot,
// validates overlaps, then assembles the result. Local and SSH callers share
// this engine; later changes never search text introduced by earlier changes.
func ApplyBatchTextChanges(content string, changes []TextChange) (*Result, int, error) {
	if err := ValidateBatchTextChanges(changes); err != nil {
		return nil, 0, err
	}
	type locatedChange struct {
		index   int
		start   int
		end     int
		newText string
		repeat  string // nonempty only for a replaceAll stream
	}
	located := make([]locatedChange, 0, len(changes))
	warnings := make([]string, 0, 2)
	ignoredNoops := 0
	// Unicode-normalized snapshot for the fuzzy fallback, computed lazily and
	// reused across changes (derived from the immutable content snapshot).
	var normContent string
	normComputed := false
	for i, change := range changes {
		newText := NormalizeEditString(change.NewText)
		if len(newText) > read.MaxReadBytes {
			return nil, 0, toolerrors.New("E_FILE_TOO_LARGE", errors.New("replacement exceeds the 32 MiB text limit"))
		}
		source := content
		oldText := NormalizeEditString(change.OldText)
		if oldText == newText {
			ignoredNoops++
			continue
		}
		if change.ReplaceAll {
			if start := strings.Index(source, oldText); start >= 0 {
				located = append(located, locatedChange{index: i, start: start, end: start + len(oldText), newText: newText, repeat: oldText})
				continue
			}
		} else {
			matches, count := scanExactMatches(source, oldText, maxMatchDiagnosticCandidates)
			if count > 1 {
				lines, details := exactMatchDiagnosticsAtMatches(content, oldText, i+1, count, matches)
				return nil, 0, toolerrors.NewWithDetails("E_MULTI_MATCH", fmt.Errorf("change %d oldText has at least %d matches%s in the file; short snippets often match multiple places, so include surrounding text to make the target unique, or set replaceAll=true only to replace every exact occurrence", i+1, count, FormatMatchLines(lines, count)), details)
			}
			if count > 0 {
				match := matches[0]
				located = append(located, locatedChange{index: i, start: match.offset, end: match.offset + len(oldText), newText: newText})
				continue
			}
		}

		if change.ReplaceAll {
			return nil, 0, toolerrors.New("E_NO_MATCH", fmt.Errorf("change %d oldText has no exact match in the file; re-read the target lines and copy the source text", i+1))
		}
		indentMatches := IndentationInsensitiveMatches(source, oldText, 9)
		switch len(indentMatches) {
		case 0:
			// Canonical Unicode fallback: character normalization can locate
			// original candidates, but punctuation/space differences require an
			// exact retry. Ambiguity never authorizes an automatic replacement.
			if !normComputed {
				normContent = NormalizeForFuzzyMatch(source)
				normComputed = true
			}
			match, found, fuzzyErr := fuzzyLocateInNormalized(source, normContent, oldText, newText, i+1)
			if fuzzyErr != nil {
				return nil, 0, fuzzyErr
			}
			if !found {
				// Last resort: the snippet was copied out of read output with its
				// line-number prefixes. Dropping those prefixes is the only edit made;
				// anything but one exact match still fails exactly as before.
				if stripped, prefixes := stripLineNumberPrefixes(oldText); prefixes != "" {
					if matches, count := scanExactMatches(source, stripped, 2); count == 1 {
						if strippedNew, newPrefixes := stripLineNumberPrefixes(newText); newPrefixes != "" {
							newText = strippedNew
						}
						located = append(located, locatedChange{index: i, start: matches[0].offset, end: matches[0].offset + len(stripped), newText: newText})
						warnings = append(warnings, fmt.Sprintf("change %d ignored line-number prefixes (%s) copied from read output; the file itself was matched without them", i+1, prefixes))
						continue
					}
				}
				return nil, 0, toolerrors.New("E_NO_MATCH", fmt.Errorf("change %d oldText was not found in the file; re-read the target lines and copy exact source text without displayed N: line prefixes", i+1))
			}
			located = append(located, locatedChange{index: i, start: match.start, end: match.end, newText: match.newText})
			warnings = append(warnings, fmt.Sprintf("change %d matched uniquely after Unicode normalization; unchanged context kept its original bytes", i+1))
		case 1:
			match := indentMatches[0]
			adjustedNewText, ok := ReindentReplacementForMatch(source, oldText, newText, match)
			if !ok {
				return nil, 0, toolerrors.New("E_NO_MATCH", fmt.Errorf("change %d matched only after indentation normalization, but newText could not be safely rebased to the file's actual indentation", i+1))
			}
			located = append(located, locatedChange{index: i, start: match.start, end: match.end, newText: adjustedNewText})
			warnings = append(warnings, fmt.Sprintf("change %d matched uniquely after ignoring leading indentation; newText was rebased to the file's actual indentation", i+1))
		default:
			lines := make([]int, len(indentMatches))
			for j, match := range indentMatches {
				lines[j] = match.line
			}
			return nil, 0, toolerrors.New("E_MULTI_MATCH", fmt.Errorf("change %d oldText has multiple indentation-insensitive matches%s; include more surrounding text to make it unique", i+1, FormatMatchLines(lines, len(indentMatches))))
		}
	}
	if ignoredNoops > 0 {
		warnings = append(warnings, fmt.Sprintf("ignored %d no-op change(s) whose oldText and newText were identical", ignoredNoops))
	}
	if len(located) == 0 {
		return &Result{Content: content, Warnings: warnings}, 0, nil
	}
	// Merge at most 50 match streams. A million replaceAll matches still keep
	// only one cursor per change. Validate overlaps and final size before allocating.
	walk := func(visit func(locatedChange)) (int, int64, error) {
		streams := append([]locatedChange(nil), located...)
		cursor, previous, count := 0, 0, 0
		size := int64(len(content))
		for len(streams) > 0 {
			first := 0
			for i := 1; i < len(streams); i++ {
				if streams[i].start < streams[first].start || (streams[i].start == streams[first].start && streams[i].end < streams[first].end) {
					first = i
				}
			}
			c := streams[first]
			if c.start < cursor {
				return 0, 0, toolerrors.New("E_OVERLAPPING_CHANGES", fmt.Errorf("changes %d and %d match overlapping source text; merge them into one replacement", previous+1, c.index+1))
			}
			if visit != nil {
				visit(c)
			}
			size += int64(len(c.newText)) - int64(c.end-c.start)
			cursor, previous = c.end, c.index
			count++
			if c.repeat != "" {
				if next := strings.Index(content[c.end:], c.repeat); next >= 0 {
					c.start = c.end + next
					c.end = c.start + len(c.repeat)
					streams[first] = c
					continue
				}
			}
			streams[first] = streams[len(streams)-1]
			streams = streams[:len(streams)-1]
		}
		return count, size, nil
	}
	replacements, outputLen, err := walk(nil)
	if err != nil {
		return nil, 0, err
	}
	if outputLen > read.MaxReadBytes {
		return nil, 0, toolerrors.New("E_FILE_TOO_LARGE", errors.New("edited file exceeds the 32 MiB text limit; split the generated data into separate files"))
	}
	var builder strings.Builder
	// Assemble the immutable snapshot once. Applying each replacement with
	// updated[:start] + ... + updated[end:] repeatedly copied the full file and
	// made a batch of M edits approach O(M*N) time and allocation volume.
	builder.Grow(int(outputLen))
	cursor := 0
	_, _, _ = walk(func(change locatedChange) {
		builder.WriteString(content[cursor:change.start])
		builder.WriteString(change.newText)
		cursor = change.end
	})
	builder.WriteString(content[cursor:])
	updated := builder.String()
	if updated == content {
		return nil, 0, toolerrors.New("E_NOOP", errors.New("changes produced no content changes"))
	}
	changedRange := ComputeChangedLineRange(content, updated)
	return &Result{
		Content:          updated,
		FirstChangedLine: changedRange.FirstChangedLine,
		LastChangedLine:  changedRange.LastChangedLine,
		Warnings:         warnings,
	}, replacements, nil
}

func visibleLineCount(content string) int {
	if content == "" {
		return 0
	}
	count := strings.Count(content, "\n") + 1
	if strings.HasSuffix(content, "\n") {
		count--
	}
	return count
}

// FormatNumberedLine returns "<lineNum>: <line>".
func FormatNumberedLine(lineNum int, line string, width int) string {
	return strconv.Itoa(lineNum) + ": " + line
}

// extractLineRange returns only the requested visible lines and the actual
// ending line. It deliberately avoids strings.Split on the whole edited file;
// changed-line previews are capped at a handful of lines.
func extractLineRange(content string, startLine, endLine int) ([]string, int) {
	if startLine < 1 || endLine < startLine {
		return nil, 0
	}
	offset, line := 0, 1
	for line < startLine {
		rel := strings.IndexByte(content[offset:], '\n')
		if rel < 0 {
			return nil, 0
		}
		offset += rel + 1
		line++
	}
	if offset >= len(content) {
		return nil, 0
	}
	lines := make([]string, 0, minInt(endLine-startLine+1, changedLineMaxOutputLines))
	actualEnd := startLine - 1
	for line <= endLine && offset < len(content) {
		rel := strings.IndexByte(content[offset:], '\n')
		if rel < 0 {
			lines = append(lines, content[offset:])
			actualEnd = line
			break
		}
		lineEnd := offset + rel
		lines = append(lines, content[offset:lineEnd])
		actualEnd = line
		offset = lineEnd + 1
		line++
	}
	return lines, actualEnd
}

// BuildLineNumberContextBlockBounded is the production preview path. It scans
// only up to the changed region and never builds a slice for every file line.
func BuildLineNumberContextBlockBounded(result string, firstLine, lastLine int) string {
	if firstLine <= 0 || lastLine <= 0 {
		return ""
	}
	start := firstLine - 2
	if start < 1 {
		start = 1
	}
	end := lastLine + 2
	if end < start || end-start+1 > changedLineMaxOutputLines {
		return "Changed lines omitted; use read for follow-up edits."
	}
	lines, actualEnd := extractLineRange(result, start, end)
	if len(lines) == 0 || actualEnd < start {
		return ""
	}
	end = actualEnd
	width := len(strconv.Itoa(end))
	var b strings.Builder
	fmt.Fprintf(&b, "--- Changed lines %d-%d ---", start, end)
	for i, line := range lines {
		b.WriteString("\n")
		b.WriteString(FormatNumberedLine(start+i, line, width))
	}
	if b.Len() > changedLineTextBudgetBytes {
		return "Changed lines omitted; use read for follow-up edits."
	}
	return b.String()
}
