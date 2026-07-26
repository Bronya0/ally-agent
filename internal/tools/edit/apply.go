package edit

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	toolerrors "ally-dev/internal/tools/shared"
)

// TextChange is one exact replacement applied by the edit engine.
type TextChange struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
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
		if change.OldText == "" {
			return toolerrors.New("E_BAD_EDIT", fmt.Errorf("change %d oldText must be non-empty", i+1))
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

// ExactMatchLineNumbers returns the 1-based line numbers where needle occurs
// in content, up to limit matches.
func ExactMatchLineNumbers(content, needle string, limit int) []int {
	if needle == "" || limit <= 0 {
		return nil
	}
	lines := make([]int, 0, limit)
	offset := 0
	for len(lines) < limit {
		rel := strings.Index(content[offset:], needle)
		if rel < 0 {
			break
		}
		start := offset + rel
		lines = append(lines, 1+strings.Count(content[:start], "\n"))
		offset = start + len(needle)
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

func textLineSpans(content string) []textLineSpan {
	if content == "" {
		return nil
	}
	spans := make([]textLineSpan, 0, strings.Count(content, "\n")+1)
	start := 0
	for start < len(content) {
		rel := strings.IndexByte(content[start:], '\n')
		if rel < 0 {
			spans = append(spans, textLineSpan{start: start, end: len(content)})
			break
		}
		end := start + rel
		spans = append(spans, textLineSpan{start: start, end: end, hasNewline: true})
		start = end + 1
	}
	return spans
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
	contentLines := textLineSpans(content)
	if len(contentLines) < len(oldLines) {
		return nil
	}
	matches := make([]indentationMatch, 0, minInt(limit, 4))
	for first := 0; first+len(oldLines) <= len(contentLines) && len(matches) < limit; first++ {
		matched := true
		for j, oldLine := range oldLines {
			span := contentLines[first+j]
			candidate := content[span.start:span.end]
			if strings.TrimLeft(candidate, " \t") != strings.TrimLeft(oldLine, " \t") {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		last := contentLines[first+len(oldLines)-1]
		if oldTrailing && !last.hasNewline {
			continue
		}
		end := last.end
		if oldTrailing {
			end++
		}
		matches = append(matches, indentationMatch{start: contentLines[first].start, end: end, line: first + 1})
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
	spans := textLineSpans(content)
	firstLine := match.line - 1
	if firstLine < 0 || firstLine+len(oldLines) > len(spans) {
		return "", false
	}
	adjusted := make([]string, len(newLines))
	for i, line := range newLines {
		oldIndent := leadingHorizontalWhitespace(oldLines[i])
		actualLine := content[spans[firstLine+i].start:spans[firstLine+i].end]
		actualIndent := leadingHorizontalWhitespace(actualLine)
		newIndent := leadingHorizontalWhitespace(line)
		if !strings.HasPrefix(newIndent, oldIndent) {
			return "", false
		}
		body := strings.TrimLeft(line, " \t")
		if body == "" {
			adjusted[i] = line
			continue
		}
		adjusted[i] = actualIndent + newIndent[len(oldIndent):] + body
	}
	result := strings.Join(adjusted, "\n")
	if newTrailing {
		result += "\n"
	}
	return result, true
}

// ApplyBatchTextChanges applies a batched set of exact replacements to content.
// Each oldText must occur exactly once (after optional indentation-insensitive
// matching for multi-line whole-line blocks), changes must not overlap, and
// the result must differ from content.
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
	for i, change := range changes {
		oldText := NormalizeEditString(change.OldText)
		newText := NormalizeEditString(change.NewText)
		if oldText == newText {
			ignoredNoops++
			continue
		}
		count := strings.Count(content, oldText)
		switch {
		case count > 1:
			lines := ExactMatchLineNumbers(content, oldText, 8)
			return nil, 0, toolerrors.New("E_MULTI_MATCH", fmt.Errorf("change %d oldText occurs %d times%s; include more surrounding text to make it unique", i+1, count, FormatMatchLines(lines, count)))
		case count == 1:
			start := strings.Index(content, oldText)
			located = append(located, locatedChange{index: i, start: start, end: start + len(oldText), newText: newText})
			continue
		}

		indentMatches := IndentationInsensitiveMatches(content, oldText, 9)
		switch len(indentMatches) {
		case 0:
			return nil, 0, toolerrors.New("E_NO_MATCH", fmt.Errorf("change %d oldText was not found in the current file; re-read and copy exact raw content", i+1))
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
	updated := content
	for i := len(located) - 1; i >= 0; i-- {
		change := located[i]
		updated = updated[:change.start] + change.newText + updated[change.end:]
	}
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

// BuildLineNumberContextBlock returns a numbered-line preview of the changed
// region around [firstLine, lastLine] in result. Used by edit results to give
// the model a compact view of what changed.
func BuildLineNumberContextBlock(result string, firstLine, lastLine int, splitLines func(string) ([]string, bool)) string {
	if firstLine <= 0 || lastLine <= 0 {
		return ""
	}
	if splitLines == nil {
		splitLines = defaultSplitLines
	}
	lines, _ := splitLines(result)
	if len(lines) == 0 {
		return ""
	}
	start := firstLine - 2
	if start < 1 {
		start = 1
	}
	end := lastLine + 2
	if end > len(lines) {
		end = len(lines)
	}
	if end < start || end-start+1 > changedLineMaxOutputLines {
		return "Changed lines omitted; use read for follow-up edits."
	}
	width := len(strconv.Itoa(end))
	var b strings.Builder
	fmt.Fprintf(&b, "--- Changed lines %d-%d ---", start, end)
	for lineNum := start; lineNum <= end; lineNum++ {
		b.WriteString("\n")
		b.WriteString(FormatNumberedLine(lineNum, lines[lineNum-1], width))
	}
	if b.Len() > changedLineTextBudgetBytes {
		return "Changed lines omitted; use read for follow-up edits."
	}
	return b.String()
}

// FormatNumberedLine returns "<lineNum>: <line>".
func FormatNumberedLine(lineNum int, line string, width int) string {
	return strconv.Itoa(lineNum) + ": " + line
}

// CountEditDiffStats tallies added/removed lines from a unified diff string.
func CountEditDiffStats(diff string, beforeLines, afterLines []string) (int, int) {
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
	if len(afterLines) > len(beforeLines) {
		return len(afterLines) - len(beforeLines), 0
	}
	if len(beforeLines) > len(afterLines) {
		return 0, len(beforeLines) - len(afterLines)
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
