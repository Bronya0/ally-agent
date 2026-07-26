package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type preparedFileEdit struct {
	path    string
	display string
	before  []byte
	after   []byte
	perm    os.FileMode
	result  EditResult
}

func (a *App) editFilesWithConfig(cfg ConfigState, files []FileTextEdits) (MultiEditResult, error) {
	plan, err := planLocalEditBatch(cfg, files, localEditPlanForExecution)
	if err != nil {
		return MultiEditResult{}, err
	}
	prepared := make([]preparedFileEdit, 0, len(plan.Files))
	for i, filePlan := range plan.Files {
		file := filePlan.Edit
		resolved := filePlan.ResolvedPath
		before, info, err := readTextFile(resolved)

		if err != nil {
			return MultiEditResult{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, err)
		}
		beforeVersion := hashVersion(before)
		if !strings.EqualFold(file.Version, beforeVersion) {
			return MultiEditResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("file %s expected version %s, current %s; re-read all affected files before retrying", file.Path, file.Version, beforeVersion))
		}
		text, ending := normalizeText(before)
		applied, replacements, err := applyBatchTextChanges(text, file.Changes)
		if err != nil {
			return MultiEditResult{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, err)
		}
		after := encodeLineEnding(applied.content, ending)
		beforeLines, _ := splitLines(text)
		afterLines, _ := splitLines(applied.content)
		diff := generateEditDiffPreview(text, applied.content, maxToolOutput)
		added, removed := 0, 0
		if diff != "" {
			added, removed = countEditDiffStats(diff, beforeLines, afterLines)
		} else {
			added, removed = approximateLineDelta(beforeLines, afterLines)
		}
		classification := "edit"
		if bytes.Equal(after, before) {
			classification = "noop"
		} else if len(after) > len(before) {
			classification = "addition"
		} else if len(after) < len(before) {
			classification = "deletion"
		}
		display := filepath.ToSlash(file.Path)
		prepared = append(prepared, preparedFileEdit{
			path:    resolved,
			display: display,
			before:  before,
			after:   after,
			perm:    info.Mode().Perm(),
			result: EditResult{
				Path:              display,
				BeforeSHA256:      hashBytes(before),
				AfterSHA256:       hashBytes(after),
				BeforeVersion:     beforeVersion,
				Version:           hashVersion(after),
				BeforeBytes:       len(before),
				AfterBytes:        len(after),
				Replacements:      replacements,
				AddedLines:        added,
				RemovedLines:      removed,
				LineEnding:        ending,
				Summary:           fmt.Sprintf("%s updated: %d -> %d bytes", display, len(before), len(after)),
				Diff:              diff,
				FirstChanged:      applied.firstChangedLine,
				LastChanged:       applied.lastChangedLine,
				Warnings:          applied.warnings,
				Classification:    classification,
				ChangedLinesBlock: buildLineNumberContextBlock(applied.content, applied.firstChangedLine, applied.lastChangedLine),
			},
		})
	}
	committed := make([]int, 0, len(prepared))
	rollback := func() error {
		var rollbackErrors []string
		for i := len(committed) - 1; i >= 0; i-- {
			item := prepared[committed[i]]
			if err := safeWriteFile(item.path, item.before, item.perm); err != nil {
				rollbackErrors = append(rollbackErrors, item.display+": "+err.Error())
			}
		}
		if len(rollbackErrors) > 0 {
			return errors.New(strings.Join(rollbackErrors, "; "))
		}
		return nil
	}
	for i, item := range prepared {
		if bytes.Equal(item.before, item.after) {
			continue
		}
		current, _, err := readTextFile(item.path)
		if err != nil || !strings.EqualFold(hashVersion(current), item.result.BeforeVersion) {
			rollbackErr := rollback()
			msg := fmt.Sprintf("file changed before commit: %s", item.display)
			if rollbackErr != nil {
				msg += "; rollback errors: " + rollbackErr.Error()
			}
			return MultiEditResult{}, codedToolError("E_VERSION_MISMATCH", errors.New(msg))
		}
		if err := safeWriteFile(item.path, item.after, item.perm); err != nil {
			rollbackErr := rollback()
			msg := fmt.Sprintf("failed to commit %s: %v", item.display, err)
			if rollbackErr != nil {
				msg += "; rollback errors: " + rollbackErr.Error()
			}
			return MultiEditResult{}, codedToolError("E_EDIT_COMMIT", errors.New(msg))
		}
		committed = append(committed, i)
	}
	result := MultiEditResult{Files: make([]EditResult, 0, len(prepared)), FileCount: len(prepared)}
	var diffs []string
	for _, item := range prepared {
		result.Files = append(result.Files, item.result)
		result.Replacements += item.result.Replacements
		result.AddedLines += item.result.AddedLines
		result.RemovedLines += item.result.RemovedLines
		for _, warning := range item.result.Warnings {
			result.Warnings = append(result.Warnings, item.display+": "+warning)
		}
		if item.result.Diff != "" {
			diffs = append(diffs, "### "+item.display+"\n"+item.result.Diff)
		}
	}
	result.Summary = fmt.Sprintf("updated %d file(s) with %d replacement(s)", result.FileCount, result.Replacements)
	if result.Replacements == 0 {
		result.Summary = fmt.Sprintf("no content changes needed in %d file(s)", result.FileCount)
	}
	result.Diff = strings.Join(diffs, "\n\n")
	return result, nil
}

func (a *App) editWithConfig(cfg ConfigState, req EditRequest) (EditResult, error) {
	plan, err := normalizeEditRequest(req)
	if err != nil {
		return EditResult{}, err
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return EditResult{}, err
	}
	path, err := safeJoin(roots, req.Path)
	if err != nil {
		return EditResult{}, err
	}
	data, _, err := readTextFile(path)
	if err != nil {
		return EditResult{}, err
	}
	beforeHash := hashBytes(data)
	beforeVersion := hashVersion(data)
	if req.Version != "" {
		if err := validateVersion(req.Version); err != nil {
			return EditResult{}, err
		}
		if !strings.EqualFold(req.Version, beforeVersion) {
			return EditResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("version %s does not match current file version %s. Re-read the file and retry", req.Version, beforeVersion))
		}
	}
	if req.ExpectedSHA256 != "" && req.ExpectedSHA256 != beforeHash {
		return EditResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("expectedSha256 %s does not match current file hash %s. Re-read the file and retry with fresh text", req.ExpectedSHA256, beforeHash))
	}
	text, ending := normalizeText(data)

	var result *editResult
	replacements := 0
	switch plan.mode {
	case "lines":
		result, replacements, err = applyLineRangeReplacement(text, plan.startLine, plan.endLine, plan.newText)
	case "batch_strings":
		result, replacements, err = applyBatchTextChanges(text, plan.changes)
	default:
		result, replacements, err = applyStringReplacements(text, plan.ops)
	}
	if err != nil {
		return EditResult{}, err
	}

	updated := result.content
	encoded := encodeLineEnding(updated, ending)
	after := encoded
	if text != updated {
		if err := safeWriteFile(path, encoded, modeOf(path)); err != nil {
			return EditResult{}, err
		}
		after, _, err = readTextFile(path)
		if err != nil {
			return EditResult{}, err
		}
	}

	beforeLines, _ := splitLines(text)
	afterLines, _ := splitLines(updated)
	diff := generateEditDiffPreview(text, updated, maxToolOutput)
	added := 0
	removed := 0
	if diff != "" {
		added, removed = countEditDiffStats(diff, beforeLines, afterLines)
	} else {
		added, removed = approximateLineDelta(beforeLines, afterLines)
	}
	if text == updated {
		added, removed = 0, 0
	}

	// Classify the edit
	classification := "edit"
	if text == updated {
		classification = "noop"
	} else if len(updated) > len(text) {
		classification = "addition"
	} else if len(updated) < len(text) {
		classification = "deletion"
	}

	changedBlock := buildLineNumberContextBlock(updated, result.firstChangedLine, result.lastChangedLine)

	return EditResult{
		Path:              filepath.ToSlash(req.Path),
		BeforeSHA256:      beforeHash,
		AfterSHA256:       hashBytes(after),
		BeforeVersion:     beforeVersion,
		Version:           hashVersion(after),
		BeforeBytes:       len(data),
		AfterBytes:        len(after),
		Replacements:      replacements,
		AddedLines:        added,
		RemovedLines:      removed,
		LineEnding:        ending,
		Summary:           fmt.Sprintf("%s updated: %d -> %d bytes", filepath.ToSlash(req.Path), len(data), len(after)),
		Diff:              diff,
		FirstChanged:      result.firstChangedLine,
		LastChanged:       result.lastChangedLine,
		Warnings:          result.warnings,
		Classification:    classification,
		ChangedLinesBlock: changedBlock,
	}, nil
}

func validateStringEditRequest(path, oldString, newString string) error {
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

func validateModelEditToolRequest(files []FileTextEdits) error {
	if len(files) == 0 {
		return codedToolError("E_BAD_EDIT", errors.New("files must contain at least one file edit"))
	}
	if len(files) > 20 {
		return codedToolError("E_BAD_EDIT", errors.New("files supports at most 20 files per call"))
	}
	totalChanges := 0
	for i, file := range files {
		if strings.TrimSpace(file.Path) == "" {
			return codedToolError("E_BAD_EDIT", fmt.Errorf("file %d requires a non-empty path", i+1))
		}
		if err := validateVersion(file.Version); err != nil {
			return fmt.Errorf("file %d: %w", i+1, err)
		}
		if err := validateBatchTextChanges(file.Changes); err != nil {
			return fmt.Errorf("file %d: %w", i+1, err)
		}
		totalChanges += len(file.Changes)
	}
	if totalChanges > 200 {
		return codedToolError("E_BAD_EDIT", errors.New("edit supports at most 200 total changes per call"))
	}
	return nil
}

func validateVersion(version string) error {
	if strings.TrimSpace(version) == "" {
		return codedToolError("E_VERSION_REQUIRED", errors.New("version is required; read the file with read and pass its version"))
	}
	if !isValidVersion(version) {
		return codedToolError("E_BAD_VERSION", errors.New("version must be exactly 12 Crockford Base32 characters"))
	}
	return nil
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func isValidVersion(value string) bool {
	if len(value) != 12 {
		return false
	}
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	for _, c := range strings.ToLower(value) {
		if !strings.ContainsRune(alphabet, c) {
			return false
		}
	}
	return true
}

func validateBatchTextChanges(changes []TextChange) error {
	if len(changes) == 0 {
		return codedToolError("E_BAD_EDIT", errors.New("changes must contain at least one replacement"))
	}
	if len(changes) > 50 {
		return codedToolError("E_BAD_EDIT", errors.New("changes supports at most 50 replacements"))
	}
	for i, change := range changes {
		if change.OldText == "" {
			return codedToolError("E_BAD_EDIT", fmt.Errorf("change %d oldText must be non-empty", i+1))
		}
	}
	return nil
}

func normalizeEditRequest(req EditRequest) (editPlan, error) {
	if strings.TrimSpace(req.Path) == "" {
		return editPlan{}, errors.New("edit request requires a non-empty \"path\" string")
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
		return editPlan{}, errors.New("[E_BAD_EDIT] use exactly one edit form")
	}
	if hasBatchChanges {
		if err := validateBatchTextChanges(req.BatchChanges); err != nil {
			return editPlan{}, err
		}
		return editPlan{mode: "batch_strings", changes: append([]TextChange(nil), req.BatchChanges...)}, nil
	}
	if hasLineEdit {
		if req.StartLine < 1 {
			return editPlan{}, errors.New("[E_BAD_EDIT] line-range edit requires startLine >= 1")
		}
		if req.NewText == nil {
			return editPlan{}, errors.New("[E_BAD_EDIT] line-range edit requires \"newText\"; use an empty string to delete selected lines")
		}
		if req.ReplaceAll {
			return editPlan{}, errors.New("[E_BAD_EDIT] replaceAll is only valid with exact-string edits")
		}
		return editPlan{
			mode:      "lines",
			startLine: req.StartLine,
			endLine:   req.EndLine,
			newText:   *req.NewText,
		}, nil
	}
	if len(req.Edits) > 0 {
		if len(req.Edits) > 50 {
			return editPlan{}, errors.New("[E_BAD_EDIT] edits supports at most 50 replacements per call")
		}
		ops := make([]EditOperation, len(req.Edits))
		for i, op := range req.Edits {
			if err := validateStringEditRequest(req.Path, op.OldString, op.NewString); err != nil {
				return editPlan{}, fmt.Errorf("edit %d/%d failed validation: %w", i+1, len(req.Edits), err)
			}
			ops[i] = op
		}
		return editPlan{mode: "strings", ops: ops}, nil
	}
	if err := validateStringEditRequest(req.Path, req.OldString, req.NewString); err != nil {
		return editPlan{}, err
	}
	return editPlan{mode: "strings", ops: []EditOperation{{
		OldString:  req.OldString,
		NewString:  req.NewString,
		ReplaceAll: req.ReplaceAll,
	}}}, nil
}

func normalizeEditString(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func applyLineRangeReplacement(content string, startLine, endLine int, newText string) (*editResult, int, error) {
	if startLine < 1 {
		return nil, 0, errors.New("[E_RANGE_OOB] startLine must be >= 1")
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

	normalizedNewText := normalizeEditString(newText)
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

	changedRange := computeChangedLineRange(content, updated)
	return &editResult{
		content:          updated,
		firstChangedLine: changedRange.firstChangedLine,
		lastChangedLine:  changedRange.lastChangedLine,
	}, 1, nil
}

func shouldKeepTrailingNewline(originalTrailing bool, replacementTrailing bool, endLine, originalLineCount int) bool {
	if endLine == originalLineCount {
		return originalTrailing || replacementTrailing
	}
	return originalTrailing
}

func applyStringReplacement(content, oldString, newString string, replaceAll bool) (*editResult, int, error) {
	oldString = normalizeEditString(oldString)
	newString = normalizeEditString(newString)
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
	changedRange := computeChangedLineRange(content, result)
	return &editResult{
		content:          result,
		firstChangedLine: changedRange.firstChangedLine,
		lastChangedLine:  changedRange.lastChangedLine,
	}, replacements, nil
}

func applyStringReplacements(content string, ops []EditOperation) (*editResult, int, error) {
	if len(ops) == 0 {
		return nil, 0, errors.New("[E_BAD_EDIT] edit request requires at least one replacement")
	}
	updated := content
	totalReplacements := 0
	var warnings []string
	for i, op := range ops {
		result, replacements, err := applyStringReplacement(updated, op.OldString, op.NewString, op.ReplaceAll)
		if err != nil {
			if len(ops) == 1 {
				return nil, 0, err
			}
			return nil, 0, fmt.Errorf("edit %d/%d failed: %w", i+1, len(ops), err)
		}
		updated = result.content
		totalReplacements += replacements
		warnings = append(warnings, result.warnings...)
	}
	changedRange := computeChangedLineRange(content, updated)
	return &editResult{
		content:          updated,
		firstChangedLine: changedRange.firstChangedLine,
		lastChangedLine:  changedRange.lastChangedLine,
		warnings:         warnings,
	}, totalReplacements, nil
}

func exactMatchLineNumbers(content, needle string, limit int) []int {
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

func formatMatchLines(lines []int, total int) string {
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

// indentationInsensitiveMatches is deliberately narrow: it only considers
// multi-line, whole-line blocks and ignores spaces/tabs at the beginning of
// each line. Line bodies, line count, and trailing-newline shape must still
// match exactly. Callers must require exactly one result before editing.
func indentationInsensitiveMatches(content, oldText string, limit int) []indentationMatch {
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
	matches := make([]indentationMatch, 0, min(limit, 4))
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

func reindentReplacementForMatch(content, oldText, newText string, match indentationMatch) (string, bool) {
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

func applyBatchTextChanges(content string, changes []TextChange) (*editResult, int, error) {
	if err := validateBatchTextChanges(changes); err != nil {
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
		oldText := normalizeEditString(change.OldText)
		newText := normalizeEditString(change.NewText)
		if oldText == newText {
			ignoredNoops++
			continue
		}
		count := strings.Count(content, oldText)
		switch {
		case count > 1:
			lines := exactMatchLineNumbers(content, oldText, 8)
			return nil, 0, codedToolError("E_MULTI_MATCH", fmt.Errorf("change %d oldText occurs %d times%s; include more surrounding text to make it unique", i+1, count, formatMatchLines(lines, count)))
		case count == 1:
			start := strings.Index(content, oldText)
			located = append(located, locatedChange{index: i, start: start, end: start + len(oldText), newText: newText})
			continue
		}

		indentMatches := indentationInsensitiveMatches(content, oldText, 9)
		switch len(indentMatches) {
		case 0:
			return nil, 0, codedToolError("E_NO_MATCH", fmt.Errorf("change %d oldText was not found in the current file; re-read and copy exact raw content", i+1))
		case 1:
			match := indentMatches[0]
			adjustedNewText, ok := reindentReplacementForMatch(content, oldText, newText, match)
			if !ok {
				return nil, 0, codedToolError("E_NO_MATCH", fmt.Errorf("change %d matched only after indentation normalization, but newText could not be safely rebased to the file's actual indentation", i+1))
			}
			located = append(located, locatedChange{index: i, start: match.start, end: match.end, newText: adjustedNewText})
			warnings = append(warnings, fmt.Sprintf("change %d matched uniquely after ignoring leading indentation; newText was rebased to the file's actual indentation", i+1))
		default:
			lines := make([]int, len(indentMatches))
			for j, match := range indentMatches {
				lines[j] = match.line
			}
			return nil, 0, codedToolError("E_MULTI_MATCH", fmt.Errorf("change %d oldText has multiple indentation-insensitive matches%s; include more surrounding text to make it unique", i+1, formatMatchLines(lines, len(indentMatches))))
		}
	}
	if ignoredNoops > 0 {
		warnings = append(warnings, fmt.Sprintf("ignored %d no-op change(s) whose oldText and newText were identical", ignoredNoops))
	}
	if len(located) == 0 {
		return &editResult{content: content, warnings: warnings}, 0, nil
	}
	sort.Slice(located, func(i, j int) bool {
		if located[i].start == located[j].start {
			return located[i].end < located[j].end
		}
		return located[i].start < located[j].start
	})
	for i := 1; i < len(located); i++ {
		if located[i].start < located[i-1].end {
			return nil, 0, codedToolError("E_OVERLAPPING_CHANGES", fmt.Errorf("changes %d and %d match overlapping source text; merge them into one replacement", located[i-1].index+1, located[i].index+1))
		}
	}
	updated := content
	for i := len(located) - 1; i >= 0; i-- {
		change := located[i]
		updated = updated[:change.start] + change.newText + updated[change.end:]
	}
	if updated == content {
		return nil, 0, codedToolError("E_NOOP", errors.New("changes produced no content changes"))
	}
	changedRange := computeChangedLineRange(content, updated)
	return &editResult{
		content:          updated,
		firstChangedLine: changedRange.firstChangedLine,
		lastChangedLine:  changedRange.lastChangedLine,
		warnings:         warnings,
	}, len(located), nil
}

func buildLineNumberContextBlock(result string, firstLine, lastLine int) string {
	if firstLine <= 0 || lastLine <= 0 {
		return ""
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
		b.WriteString(formatNumberedLine(lineNum, lines[lineNum-1], width))
	}
	if b.Len() > changedLineTextBudgetBytes {
		return "Changed lines omitted; use read for follow-up edits."
	}
	return b.String()
}

func countEditDiffStats(diff string, beforeLines, afterLines []string) (int, int) {
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

func approximateLineDelta(beforeLines, afterLines []string) (int, int) {
	if len(afterLines) > len(beforeLines) {
		return len(afterLines) - len(beforeLines), 0
	}
	if len(beforeLines) > len(afterLines) {
		return 0, len(beforeLines) - len(afterLines)
	}
	return 0, 0
}
