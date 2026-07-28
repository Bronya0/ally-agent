package app

// Section 9: Edit (was file_edit.go)
// App-owned edit orchestration that binds internal/tools/edit diff/apply
// algorithms to workspace path resolution, fileOpsMu serialization, atomic
// writes with best-effort rollback, and the E_VERSION_MISMATCH guard.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ally-dev/internal/tools/edit"
	"ally-dev/internal/tools/read"
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
		applied, replacements, err := edit.ApplyBatchTextChanges(text, toEditChanges(file.Changes))
		if err != nil {
			return MultiEditResult{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, err)
		}
		after := encodeLineEnding(applied.Content, ending)
		beforeLines, _ := splitLines(text)
		afterLines, _ := splitLines(applied.Content)
		diff := edit.GenerateEditDiffPreview(text, applied.Content, maxToolOutput)
		added, removed := 0, 0
		if diff != "" {
			added, removed = edit.CountEditDiffStats(diff, beforeLines, afterLines)
		} else {
			added, removed = edit.ApproximateLineDelta(beforeLines, afterLines)
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
				FirstChanged:      applied.FirstChangedLine,
				LastChanged:       applied.LastChangedLine,
				Warnings:          applied.Warnings,
				Classification:    classification,
				ChangedLinesBlock: edit.BuildLineNumberContextBlock(applied.Content, applied.FirstChangedLine, applied.LastChangedLine, splitLines),
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

	var result *edit.Result
	replacements := 0
	switch plan.mode {
	case "lines":
		result, replacements, err = edit.ApplyLineRangeReplacement(text, plan.startLine, plan.endLine, plan.newText, splitLines)
	case "batch_strings":
		result, replacements, err = edit.ApplyBatchTextChanges(text, toEditChanges(plan.changes))
	default:
		result, replacements, err = edit.ApplyStringReplacements(text, toEditOperations(plan.ops))
	}
	if err != nil {
		return EditResult{}, err
	}

	updated := result.Content
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
	diff := edit.GenerateEditDiffPreview(text, updated, maxToolOutput)
	added := 0
	removed := 0
	if diff != "" {
		added, removed = edit.CountEditDiffStats(diff, beforeLines, afterLines)
	} else {
		added, removed = edit.ApproximateLineDelta(beforeLines, afterLines)
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

	changedBlock := edit.BuildLineNumberContextBlock(updated, result.FirstChangedLine, result.LastChangedLine, splitLines)

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
		FirstChanged:      result.FirstChangedLine,
		LastChanged:       result.LastChangedLine,
		Warnings:          result.Warnings,
		Classification:    classification,
		ChangedLinesBlock: changedBlock,
	}, nil
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
		if err := edit.ValidateBatchTextChanges(toEditChanges(file.Changes)); err != nil {
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
	return read.ValidateVersion(version)
}

func isSHA256Hex(value string) bool {
	return read.IsSHA256Hex(value)
}

func isValidVersion(value string) bool {
	return read.IsValidVersion(value)
}

func validateBatchTextChanges(changes []TextChange) error {
	return edit.ValidateBatchTextChanges(toEditChanges(changes))
}

func normalizeEditRequest(req EditRequest) (editPlan, error) {
	plan, err := edit.NormalizeEditRequest(edit.PlanRequest{
		Path:         req.Path,
		OldString:    req.OldString,
		NewString:    req.NewString,
		ReplaceAll:   req.ReplaceAll,
		StartLine:    req.StartLine,
		EndLine:      req.EndLine,
		NewText:      req.NewText,
		Edits:        toEditOperations(req.Edits),
		BatchChanges: toEditChanges(req.BatchChanges),
	})
	if err != nil {
		return editPlan{}, err
	}
	ep := editPlan{mode: planModeString(plan.Mode), newText: plan.NewText, startLine: plan.StartLine, endLine: plan.EndLine}
	ep.ops = fromEditOperations(plan.Ops)
	ep.changes = fromEditChanges(plan.Changes)
	return ep, nil
}

func planModeString(m edit.PlanMode) string {
	switch m {
	case edit.PlanModeBatchStrings:
		return "batch_strings"
	case edit.PlanModeLines:
		return "lines"
	default:
		return "strings"
	}
}

func toEditChanges(in []TextChange) []edit.TextChange {
	if len(in) == 0 {
		return nil
	}
	out := make([]edit.TextChange, len(in))
	for i, c := range in {
		out[i] = edit.TextChange{OldText: c.OldText, NewText: c.NewText}
	}
	return out
}

func fromEditChanges(in []edit.TextChange) []TextChange {
	if len(in) == 0 {
		return nil
	}
	out := make([]TextChange, len(in))
	for i, c := range in {
		out[i] = TextChange{OldText: c.OldText, NewText: c.NewText}
	}
	return out
}

func toEditOperations(in []EditOperation) []edit.EditOperation {
	if len(in) == 0 {
		return nil
	}
	out := make([]edit.EditOperation, len(in))
	for i, o := range in {
		out[i] = edit.EditOperation{OldString: o.OldString, NewString: o.NewString, ReplaceAll: o.ReplaceAll}
	}
	return out
}

func fromEditOperations(in []edit.EditOperation) []EditOperation {
	if len(in) == 0 {
		return nil
	}
	out := make([]EditOperation, len(in))
	for i, o := range in {
		out[i] = EditOperation{OldString: o.OldString, NewString: o.NewString, ReplaceAll: o.ReplaceAll}
	}
	return out
}

func normalizeEditString(s string) string {
	return edit.NormalizeEditString(s)
}
