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
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	toolerrors "ally-dev/internal/tools/shared"
)

// TextChange is one replacement applied by the edit engine. Exactly one of
// OldText or LineRange identifies the source snapshot region. ReplaceAll is
// only valid with OldText and replaces every non-overlapping exact match.
type TextChange struct {
	OldText    string `json:"oldText,omitempty"`
	LineRange  string `json:"lineRange,omitempty"`
	NewText    string `json:"newText"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
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

// EditOperation is a legacy single-string edit operation.
type EditOperation struct {
	OldString  string `json:"oldString"`
	NewString  string `json:"newString"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
}

// Result captures the output of one edit application: the new content, the
// first/last changed line, and any non-fatal warnings.
type Result struct {
	Content          string
	FirstChangedLine int
	LastChangedLine  int
	Warnings         []string
}

// PlanMode selects which validation level NormalizeEditRequest enforces.
type PlanMode int

const (
	// PlanModeStrings uses the legacy exact-string edits form.
	PlanModeStrings PlanMode = iota
	// PlanModeBatchStrings uses the model-facing batched changes form.
	PlanModeBatchStrings
	// PlanModeLines uses the line-range replacement form.
	PlanModeLines
)

// Plan is the normalized form of an EditRequest.
type Plan struct {
	Mode      PlanMode
	Ops       []EditOperation
	Changes   []TextChange
	StartLine int
	EndLine   int
	NewText   string
}

// NormalizeEditString converts Windows CRLF to Unix LF.
func NormalizeEditString(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// ValidateStringEditRequest checks the legacy single-string edit form.
func ValidateStringEditRequest(path, oldString, newString string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("edit request requires a non-empty \"path\" string")
	}
	if oldString == "" {
		return errors.New("[E_BAD_EDIT] edit request requires a non-empty \"oldString\"")
	}
	if oldString == newString {
		return errors.New("[E_NOOP] oldString and newString are identical")
	}
	return nil
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
		hasOldText := change.OldText != ""
		hasLineRange := strings.TrimSpace(change.LineRange) != ""
		if hasOldText == hasLineRange {
			return toolerrors.New("E_BAD_EDIT", fmt.Errorf("change %d must use exactly one source: oldText for a small exact replacement, or lineRange for a larger whole-line replacement", i+1))
		}
		if hasLineRange {
			if _, _, err := ParseLineRange(change.LineRange); err != nil {
				return toolerrors.New("E_BAD_EDIT", fmt.Errorf("change %d: %w", i+1, err))
			}
		}
	}
	return nil
}

// PlanRequest is the model-facing input shape.
type PlanRequest struct {
	Path         string
	OldString    string
	NewString    string
	ReplaceAll   bool
	StartLine    int
	EndLine      int
	NewText      *string
	Edits        []EditOperation
	BatchChanges []TextChange
}

// NormalizeEditRequest validates the request and returns the normalized plan.
// Exactly one edit form must be used; mixing returns an error.
func NormalizeEditRequest(req PlanRequest) (Plan, error) {
	if strings.TrimSpace(req.Path) == "" {
		return Plan{}, errors.New("edit request requires a non-empty \"path\" string")
	}
	hasSingleEdit := req.OldString != "" || req.NewString != ""
	hasLineEdit := req.StartLine != 0 || req.EndLine != 0 || req.NewText != nil
	hasBatchChanges := len(req.BatchChanges) > 0
	modes := 0
	if hasSingleEdit {
		modes++
	}
	if len(req.Edits) > 0 {
		modes++
	}
	if hasLineEdit {
		modes++
	}
	if hasBatchChanges {
		modes++
	}
	if modes > 1 {
		return Plan{}, errors.New("[E_BAD_EDIT] use exactly one edit form")
	}
	if hasBatchChanges {
		if err := ValidateBatchTextChanges(req.BatchChanges); err != nil {
			return Plan{}, err
		}
		return Plan{Mode: PlanModeBatchStrings, Changes: append([]TextChange(nil), req.BatchChanges...)}, nil
	}
	if hasLineEdit {
		if req.StartLine < 1 {
			return Plan{}, errors.New("[E_BAD_EDIT] line-range edit requires startLine >= 1")
		}
		if req.NewText == nil {
			return Plan{}, errors.New("[E_BAD_EDIT] line-range edit requires \"newText\"; use an empty string to delete selected lines")
		}
		if req.ReplaceAll {
			return Plan{}, errors.New("[E_BAD_EDIT] replaceAll is only valid with exact-string edits")
		}
		return Plan{
			Mode:      PlanModeLines,
			StartLine: req.StartLine,
			EndLine:   req.EndLine,
			NewText:   *req.NewText,
		}, nil
	}
	if len(req.Edits) > 0 {
		if len(req.Edits) > 50 {
			return Plan{}, errors.New("[E_BAD_EDIT] edits supports at most 50 replacements per call")
		}
		ops := make([]EditOperation, len(req.Edits))
		for i, op := range req.Edits {
			if err := ValidateStringEditRequest(req.Path, op.OldString, op.NewString); err != nil {
				return Plan{}, fmt.Errorf("edit %d/%d failed validation: %w", i+1, len(req.Edits), err)
			}
			ops[i] = op
		}
		return Plan{Mode: PlanModeStrings, Ops: ops}, nil
	}
	if err := ValidateStringEditRequest(req.Path, req.OldString, req.NewString); err != nil {
		return Plan{}, err
	}
	return Plan{Mode: PlanModeStrings, Ops: []EditOperation{{
		OldString:  req.OldString,
		NewString:  req.NewString,
		ReplaceAll: req.ReplaceAll,
	}}}, nil
}

// ApplyLineRangeReplacement replaces lines [startLine, endLine] of content
// with newText. endLine <= 0 means same as startLine.
func ApplyLineRangeReplacement(content string, startLine, endLine int, newText string, splitLines func(string) ([]string, bool)) (*Result, int, error) {
	if startLine < 1 {
		return nil, 0, errors.New("[E_RANGE_OOB] startLine must be >= 1")
	}
	if splitLines == nil {
		splitLines = defaultSplitLines
	}
	lines, trailingNewline := splitLines(content)
	if len(lines) == 0 {
		return nil, 0, errors.New("[E_RANGE_OOB] startLine 1 does not exist (file has 0 lines)")
	}
	if startLine > len(lines) {
		return nil, 0, fmt.Errorf("[E_RANGE_OOB] startLine %d does not exist (file has %d lines)", startLine, len(lines))
	}
	if endLine <= 0 {
		endLine = startLine
	}
	if endLine < startLine || endLine > len(lines) {
		return nil, 0, fmt.Errorf("[E_RANGE_OOB] endLine %d is outside %d-%d", endLine, startLine, len(lines))
	}

	normalizedNewText := NormalizeEditString(newText)
	replacementLines, replacementTrailingNewline := splitLines(normalizedNewText)
	updatedLines := make([]string, 0, len(lines)-(endLine-startLine+1)+len(replacementLines))
	updatedLines = append(updatedLines, lines[:startLine-1]...)
	updatedLines = append(updatedLines, replacementLines...)
	updatedLines = append(updatedLines, lines[endLine:]...)

	updated := strings.Join(updatedLines, "\n")
	if len(updatedLines) > 0 && shouldKeepTrailingNewline(trailingNewline, replacementTrailingNewline, endLine, len(lines)) {
		updated += "\n"
	}
	if content == updated {
		return nil, 0, errors.New("[E_NOOP] line-range edit produced no content changes")
	}
	if len(content) > 0 && len(updated) == 0 {
		return nil, 0, errors.New("[E_WOULD_EMPTY] Refusing to empty a non-empty file through edit.")
	}

	changedRange := ComputeChangedLineRange(content, updated)
	return &Result{
		Content:          updated,
		FirstChangedLine: changedRange.FirstChangedLine,
		LastChangedLine:  changedRange.LastChangedLine,
	}, 1, nil
}

func shouldKeepTrailingNewline(originalTrailing bool, replacementTrailing bool, endLine, originalLineCount int) bool {
	if endLine == originalLineCount {
		return originalTrailing || replacementTrailing
	}
	return originalTrailing
}

// ApplyStringReplacement applies one exact-string replacement to content.
func ApplyStringReplacement(content, oldString, newString string, replaceAll bool) (*Result, int, error) {
	oldString = NormalizeEditString(oldString)
	newString = NormalizeEditString(newString)
	if oldString == "" {
		return nil, 0, errors.New("[E_BAD_EDIT] oldString cannot be empty")
	}
	if oldString == newString {
		return nil, 0, errors.New("[E_NOOP] oldString and newString are identical")
	}
	count := strings.Count(content, oldString)
	if count == 0 {
		return nil, 0, errors.New("[E_NO_MATCH] oldString was not found in the current file. Re-read the file and copy exact raw text.")
	}
	if count > 1 && !replaceAll {
		return nil, 0, fmt.Errorf("[E_MULTI_MATCH] oldString found %d times. Include more surrounding context to make it unique, or set replaceAll=true.", count)
	}
	replacements := 1
	result := content
	if replaceAll {
		replacements = count
		result = strings.ReplaceAll(content, oldString, newString)
	} else {
		result = strings.Replace(content, oldString, newString, 1)
	}
	if len(content) > 0 && len(result) == 0 {
		return nil, 0, errors.New("[E_WOULD_EMPTY] Refusing to empty a non-empty file through edit.")
	}
	changedRange := ComputeChangedLineRange(content, result)
	return &Result{
		Content:          result,
		FirstChangedLine: changedRange.FirstChangedLine,
		LastChangedLine:  changedRange.LastChangedLine,
	}, replacements, nil
}

// ApplyStringReplacements applies a sequence of exact-string replacements.
func ApplyStringReplacements(content string, ops []EditOperation) (*Result, int, error) {
	if len(ops) == 0 {
		return nil, 0, errors.New("[E_BAD_EDIT] edit request requires at least one replacement")
	}
	updated := content
	totalReplacements := 0
	var warnings []string
	for i, op := range ops {
		result, replacements, err := ApplyStringReplacement(updated, op.OldString, op.NewString, op.ReplaceAll)
		if err != nil {
			if len(ops) == 1 {
				return nil, 0, err
			}
			return nil, 0, fmt.Errorf("edit %d/%d failed: %w", i+1, len(ops), err)
		}
		updated = result.Content
		totalReplacements += replacements
		warnings = append(warnings, result.Warnings...)
	}
	changedRange := ComputeChangedLineRange(content, updated)
	return &Result{
		Content:          updated,
		FirstChangedLine: changedRange.FirstChangedLine,
		LastChangedLine:  changedRange.LastChangedLine,
		Warnings:         warnings,
	}, totalReplacements, nil
}

// exactMatch records an exact match offset and its 1-based source line.
type exactMatch struct {
	offset int
	line   int
}

// scanExactMatches performs one non-overlapping scan. matches is bounded when
// limit is positive, while count always contains the exact total. The line
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
	return matches, count
}

func exactMatches(content, needle string, limit int) []exactMatch {
	matches, _ := scanExactMatches(content, needle, limit)
	return matches
}

// ExactMatchLineNumbers returns the 1-based line numbers where needle occurs
// in content, up to limit matches.
func ExactMatchLineNumbers(content, needle string, limit int) []int {
	matches := exactMatches(content, needle, limit)
	lines := make([]int, len(matches))
	for i, match := range matches {
		lines[i] = match.line
	}
	return lines
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
		Recovery:            "Use a candidate preview or read only its startLine-endLine range, then retry with more exact surrounding text. Do not choose an occurrence by position alone.",
	}
	boundMatchErrorDetails(details)
	return lines, details
}

func exactMatchDiagnostics(content, oldText string, changeIndex, count int) ([]int, *MatchErrorDetails) {
	matches, _ := scanExactMatches(content, oldText, maxMatchDiagnosticCandidates)
	return exactMatchDiagnosticsAtMatches(content, oldText, changeIndex, count, matches)
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

func matchCandidate(content string, start, end int) MatchCandidate {
	line := 1 + strings.Count(content[:start], "\n")
	return matchCandidateAtLine(content, start, end, line)
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

func exactMatchErrorDetails(content, oldText string, changeIndex, count int) *MatchErrorDetails {
	_, details := exactMatchDiagnostics(content, oldText, changeIndex, count)
	return details
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
		if span.hasNewline {
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

// ParseLineRange parses an inclusive 1-based A-B range. Reversed and
// shorthand forms are rejected rather than guessed because this is a write
// target, not a display request.
func ParseLineRange(value string) (int, int, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid lineRange %q; use inclusive A-B form such as \"12-24\"", value)
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || start < 1 {
		return 0, 0, fmt.Errorf("invalid lineRange %q; start line must be a positive integer", value)
	}
	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || end < 1 {
		return 0, 0, fmt.Errorf("invalid lineRange %q; end line must be a positive integer", value)
	}
	if end < start {
		return 0, 0, fmt.Errorf("invalid lineRange %q; end line must not precede start line", value)
	}
	return start, end, nil
}

// ApplyBatchTextChanges applies exact-string and whole-line replacements to
// one immutable snapshot. Every source is located before any mutation, then
// replacements are applied backwards. Thus all lineRange values always refer
// to the line numbers returned by read for the supplied version; callers never
// adjust later ranges for earlier insertions or deletions.
func ApplyBatchTextChanges(content string, changes []TextChange) (*Result, int, error) {
	if err := ValidateBatchTextChanges(changes); err != nil {
		return nil, 0, err
	}
	type locatedChange struct {
		index   int
		start   int
		end     int
		newText string
	}
	located := make([]locatedChange, 0, len(changes))
	warnings := make([]string, 0, 2)
	ignoredNoops := 0
	// Unicode-normalized snapshot for the fuzzy fallback, computed lazily and
	// reused across changes (derived from the immutable content snapshot).
	var normContent string
	normComputed := false
	// Exact-text edits do not need a line index. Build it lazily only when the
	// batch actually contains a lineRange change, avoiding an O(N) scan and one
	// offset slice allocation for the common exact-string path.
	var index *lineIndex
	for i, change := range changes {
		newText := NormalizeEditString(change.NewText)
		if strings.TrimSpace(change.LineRange) != "" {
			if change.ReplaceAll {
				warnings = append(warnings, fmt.Sprintf("change %d ignored replace_all because it only applies to oldText; lineRange was executed normally", i+1))
			}
			if index == nil {
				index = buildLineIndex(content)
			}
			startLine, endLine, parseErr := ParseLineRange(change.LineRange)
			if parseErr != nil {
				return nil, 0, toolerrors.New("E_BAD_EDIT", fmt.Errorf("change %d: %w", i+1, parseErr))
			}
			start, end, rangeErr := lineRangeOffsetsWithIndex(index, len(content), startLine, endLine)
			if rangeErr != nil {
				return nil, 0, toolerrors.New("E_RANGE_OOB", fmt.Errorf("change %d lineRange %q: %w; re-read and use numbered lines from the current version", i+1, change.LineRange, rangeErr))
			}
			replacement := lineRangeReplacement(content, end, newText)
			if content[start:end] == replacement {
				ignoredNoops++
				continue
			}
			located = append(located, locatedChange{index: i, start: start, end: end, newText: replacement})
			continue
		}

		oldText := NormalizeEditString(change.OldText)
		if oldText == newText {
			ignoredNoops++
			continue
		}
		if change.ReplaceAll {
			// Stream replacements directly without retaining every match
			// offset: a replace-all over a large file must not allocate a
			// slice proportional to the match count.
			count := 0
			for from := 0; from <= len(content)-len(oldText); {
				rel := strings.Index(content[from:], oldText)
				if rel < 0 {
					break
				}
				start := from + rel
				located = append(located, locatedChange{index: i, start: start, end: start + len(oldText), newText: newText})
				count++
				from = start + len(oldText)
			}
			if count > 0 {
				continue
			}
		} else {
			matches, count := scanExactMatches(content, oldText, maxMatchDiagnosticCandidates)
			if count > 1 {
				lines, details := exactMatchDiagnosticsAtMatches(content, oldText, i+1, count, matches)
				return nil, 0, toolerrors.NewWithDetails("E_MULTI_MATCH", fmt.Errorf("change %d oldText occurs %d times%s; inspect one bounded candidate and include more surrounding text to make it unique, or set replace_all=true to replace every exact occurrence", i+1, count, FormatMatchLines(lines, count)), details)
			}
			if count > 0 {
				match := matches[0]
				located = append(located, locatedChange{index: i, start: match.offset, end: match.offset + len(oldText), newText: newText})
				continue
			}
		}

		indentMatches := IndentationInsensitiveMatches(content, oldText, 9)
		switch len(indentMatches) {
		case 0:
			// Unicode normalization fallback: exact and indentation-insensitive
			// matching both failed. Normalize quotes/dashes/spaces and retry
			// against the same immutable snapshot. Only a unique normalized
			// match is accepted; ambiguity is reported instead of guessed.
			// replace_all keeps its exact-match-only contract and skips this
			// fallback (replacing "every normalized occurrence" is ambiguous).
			if !change.ReplaceAll {
				if !normComputed {
					normContent = NormalizeForFuzzyMatch(content)
					normComputed = true
				}
				match, found, fuzzyErr := fuzzyLocateInNormalized(content, normContent, oldText, newText, i+1)
				if fuzzyErr != nil {
					return nil, 0, fuzzyErr
				}
				if !found {
					return nil, 0, toolerrors.New("E_NO_MATCH", fmt.Errorf("change %d oldText was not found in the current file; re-read and copy exact source text without the displayed N: line prefixes", i+1))
				}
				located = append(located, locatedChange{index: i, start: match.start, end: match.end, newText: match.newText})
				warnings = append(warnings, fmt.Sprintf("change %d matched uniquely after Unicode normalization (quotes/dashes/spaces); the touched block was rewritten in normalized form", i+1))
			} else {
				return nil, 0, toolerrors.New("E_NO_MATCH", fmt.Errorf("change %d oldText was not found in the current file; re-read and copy exact source text without the displayed N: line prefixes", i+1))
			}
		case 1:
			match := indentMatches[0]
			adjustedNewText, ok := ReindentReplacementForMatch(content, oldText, newText, match)
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
	sort.Slice(located, func(i, j int) bool {
		if located[i].start == located[j].start {
			return located[i].end < located[j].end
		}
		return located[i].start < located[j].start
	})
	for i := 1; i < len(located); i++ {
		if located[i].start < located[i-1].end {
			return nil, 0, toolerrors.New("E_OVERLAPPING_CHANGES", fmt.Errorf("changes %d and %d match overlapping source text; merge them into one replacement", located[i-1].index+1, located[i].index+1))
		}
	}
	var builder strings.Builder
	// Assemble the immutable snapshot once. Applying each replacement with
	// updated[:start] + ... + updated[end:] repeatedly copied the full file and
	// made a batch of M edits approach O(M*N) time and allocation volume.
	outputLen := len(content)
	for _, change := range located {
		outputLen += len(change.newText) - (change.end - change.start)
	}
	builder.Grow(outputLen)
	cursor := 0
	for _, change := range located {
		builder.WriteString(content[cursor:change.start])
		builder.WriteString(change.newText)
		cursor = change.end
	}
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
	}, len(located), nil
}

// lineRangeOffsetsWithIndex is the index-backed variant of lineRangeOffsets.
// Bounds are validated against the cached line count; offsets are O(1) lookups
// into the precomputed starts slice instead of O(N) re-scans of the content.
// contentLen is the byte length of the snapshot the index was built from; we
// don't store it in the index to keep the struct light.
func lineRangeOffsetsWithIndex(index *lineIndex, contentLen int, startLine, endLine int) (int, int, error) {
	total := index.total()
	if total == 0 {
		return 0, 0, errors.New("file has 0 lines")
	}
	if startLine < 1 || startLine > total {
		return 0, 0, fmt.Errorf("start line %d is outside 1-%d", startLine, total)
	}
	if endLine < startLine || endLine > total {
		return 0, 0, fmt.Errorf("end line %d is outside %d-%d", endLine, startLine, total)
	}
	start := index.offsetForLine(startLine)
	end := contentLen
	if endLine < total {
		end = index.offsetForLine(endLine + 1)
	}
	return start, end, nil
}

// lineRangeOffsets locates an inclusive whole-line range in the immutable
// original snapshot. For non-final ranges, end includes the selected range's
// terminating newline so an empty newText performs whole-line deletion.
func lineRangeOffsets(content string, startLine, endLine int) (int, int, error) {
	total := visibleLineCount(content)
	if total == 0 {
		return 0, 0, errors.New("file has 0 lines")
	}
	if startLine < 1 || startLine > total {
		return 0, 0, fmt.Errorf("start line %d is outside 1-%d", startLine, total)
	}
	if endLine < startLine || endLine > total {
		return 0, 0, fmt.Errorf("end line %d is outside %d-%d", endLine, startLine, total)
	}
	start := byteOffsetForLine(content, startLine)
	end := len(content)
	if endLine < total {
		end = byteOffsetForLine(content, endLine+1)
	}
	return start, end, nil
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

func byteOffsetForLine(content string, line int) int {
	if line <= 1 {
		return 0
	}
	offset := 0
	for current := 1; current < line; current++ {
		rel := strings.IndexByte(content[offset:], '\n')
		if rel < 0 {
			return len(content)
		}
		offset += rel + 1
	}
	return offset
}

// lineIndex caches the byte offset of every line start in a snapshot. Building
// it is one O(N) scan over the content; afterwards offsetForLine and
// lineAtOffset are O(1). ApplyBatchTextChanges uses it so that N line-range
// edits on a large file cost O(N + edits) instead of O(N * edits), where the
// previous per-call byteOffsetForLine re-scanned from byte 0 every time.
type lineIndex struct {
	// starts[i] is the byte offset of the start of line i+1. starts[0] is
	// always 0 (line 1 starts at byte 0). The slice has length visibleLineCount.
	starts []int
}

// buildLineIndex scans content once and records the byte offset of the start
// of every visible line. The trailing newline of the last line is not a new
// line start.
func buildLineIndex(content string) *lineIndex {
	total := visibleLineCount(content)
	if total == 0 {
		return &lineIndex{starts: nil}
	}
	starts := make([]int, total)
	starts[0] = 0
	line := 1
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			line++
			if line > total {
				break
			}
			starts[line-1] = i + 1
		}
	}
	return &lineIndex{starts: starts}
}

// offsetForLine returns the byte offset of the start of the given 1-based
// line. line must be in [1, total]; callers should check bounds first.
func (li *lineIndex) offsetForLine(line int) int {
	if li == nil || line < 1 || line > len(li.starts) {
		return 0
	}
	return li.starts[line-1]
}

// total returns the number of visible lines in the indexed snapshot.
func (li *lineIndex) total() int {
	if li == nil {
		return 0
	}
	return len(li.starts)
}

// lineAtOffset returns the 1-based line number containing the given byte
// offset. Uses binary search over line starts — O(log N).
func (li *lineIndex) lineAtOffset(offset int) int {
	if li == nil || len(li.starts) == 0 {
		return 1
	}
	if offset <= 0 {
		return 1
	}
	// Find the last line start <= offset. sort.Search returns the first index
	// where starts[i] > offset, so the line number is that index (1-based
	// line = index + 1 only when starts[index] <= offset; here we want the
	// preceding index).
	idx := sort.Search(len(li.starts), func(i int) bool {
		return li.starts[i] > offset
	})
	if idx == 0 {
		return 1
	}
	return idx
}

// lineRangeReplacement adds only the separator needed before an untouched
// following line. newText never contains read's numeric prefixes.
func lineRangeReplacement(content string, end int, newText string) string {
	if newText == "" || end == len(content) || strings.HasSuffix(newText, "\n") {
		if end == len(content) && strings.HasSuffix(content, "\n") && newText != "" && !strings.HasSuffix(newText, "\n") {
			return newText + "\n"
		}
		return newText
	}
	return newText + "\n"
}

// BuildLineNumberContextBlock is kept as a compatibility wrapper. The bounded
// implementation avoids splitting the entire result file just to show a few
// changed lines; splitLines is intentionally ignored.
func BuildLineNumberContextBlock(result string, firstLine, lastLine int, splitLines func(string) ([]string, bool)) string {
	return BuildLineNumberContextBlockBounded(result, firstLine, lastLine)
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

// CountEditDiffStats tallies added/removed lines from a unified diff string.
func CountEditDiffStats(diff string) (int, int) {
	// The localized truncated-diff path emits an accurate span-total marker
	// (computed via multiset difference) alongside the shown lines. It already
	// reflects the true change size for the whole span, so return it directly
	// instead of tallying +/- prefixes, which only cover the truncated prefix of
	// the span and would otherwise count thousands of unchanged lines as
	// removed/added.
	if idx := strings.Index(diff, "[diff truncated: "); idx >= 0 {
		var spanRemoved, spanAdded int
		if _, err := fmt.Sscanf(diff[idx:], "[diff truncated: %d removed and %d added lines in span]", &spanRemoved, &spanAdded); err == nil {
			return spanAdded, spanRemoved
		}
	}
	added, removed := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			added++
		} else if strings.HasPrefix(line, "-") {
			removed++
		}
	}
	// Legacy marker from older builds carried omitted counts on top of the
	// shown lines. Keep supporting it for diffs produced before this change.
	if idx := strings.Index(diff, "[diff truncated: omitted "); idx >= 0 {
		var omittedRemoved, omittedAdded int
		if _, err := fmt.Sscanf(diff[idx:], "[diff truncated: omitted %d removed and %d added lines]", &omittedRemoved, &omittedAdded); err == nil {
			added += omittedAdded
			removed += omittedRemoved
		}
	}
	return added, removed
}

// ApproximateLineDelta is a fallback when no diff is available: it counts
// the difference in line counts.
func ApproximateLineDelta(beforeLines, afterLines []string) (int, int) {
	return approximateLineDeltaCounts(len(beforeLines), len(afterLines))
}

// ApproximateLineDeltaContent computes the fallback line delta without
// materializing strings.Split slices for the entire file.
func ApproximateLineDeltaContent(before, after string) (int, int) {
	return approximateLineDeltaCounts(visibleLineCount(before), visibleLineCount(after))
}

func approximateLineDeltaCounts(beforeLines, afterLines int) (int, int) {
	if afterLines > beforeLines {
		return afterLines - beforeLines, 0
	}
	if beforeLines > afterLines {
		return 0, beforeLines - afterLines
	}
	return 0, 0
}

func defaultSplitLines(text string) ([]string, bool) {
	if text == "" {
		return nil, false
	}
	lines := strings.Split(text, "\n")
	trailing := strings.HasSuffix(text, "\n")
	if trailing {
		lines = lines[:len(lines)-1]
	}
	return lines, trailing
}
