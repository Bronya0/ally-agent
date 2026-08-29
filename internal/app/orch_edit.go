// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section 9: Edit (was file_edit.go)
// App-owned edit orchestration that binds internal/tools/edit diff/apply
// algorithms to workspace path resolution, fileOpsMu serialization, atomic
// writes with best-effort rollback, and the E_VERSION_MISMATCH guard.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ally-dev/internal/tools/edit"
	"ally-dev/internal/tools/read"

	openai "github.com/sashabaranov/go-openai"
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
		beforeHash, beforeVersion := hashBytesAndVersion(before)
		if !strings.EqualFold(file.Version, beforeVersion) {
			return MultiEditResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("file %s expected version %s, current %s; re-read all affected files before retrying", file.Path, file.Version, beforeVersion))
		}
		text, ending, hadBOM := normalizeText(before)
		applied, replacements, err := edit.ApplyBatchTextChanges(text, toEditChanges(file.Changes))
		if err != nil {
			return MultiEditResult{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, err)
		}
		after := encodeText(applied.Content, ending, hadBOM)
		diff := edit.GenerateEditDiffPreview(text, applied.Content, maxToolOutput)
		added, removed := 0, 0
		if diff != "" {
			added, removed = edit.CountEditDiffStats(diff)
		} else {
			added, removed = edit.ApproximateLineDeltaContent(text, applied.Content)
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
		afterHash, afterVersion := hashBytesAndVersion(after)
		prepared = append(prepared, preparedFileEdit{
			path:    resolved,
			display: display,
			before:  before,
			after:   after,
			perm:    info.Mode().Perm(),
			result: EditResult{
				Path:              display,
				BeforeSHA256:      beforeHash,
				AfterSHA256:       afterHash,
				BeforeVersion:     beforeVersion,
				Version:           afterVersion,
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
	beforeHash, beforeVersion := hashBytesAndVersion(data)
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
	text, ending, hadBOM := normalizeText(data)

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
	encoded := encodeText(updated, ending, hadBOM)
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

	diff := edit.GenerateEditDiffPreview(text, updated, maxToolOutput)
	added := 0
	removed := 0
	if diff != "" {
		added, removed = edit.CountEditDiffStats(diff)
	} else {
		added, removed = edit.ApproximateLineDeltaContent(text, updated)
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
	afterHash, afterVersion := hashBytesAndVersion(after)

	return EditResult{
		Path:              filepath.ToSlash(req.Path),
		BeforeSHA256:      beforeHash,
		AfterSHA256:       afterHash,
		BeforeVersion:     beforeVersion,
		Version:           afterVersion,
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
		return codedToolError("E_BAD_EDIT", errors.New("edit requires top-level path, version, and changes (exactly one file per call)"))
	}
	if len(files) > 20 {
		return codedToolError("E_BAD_EDIT", errors.New("edit supports at most 20 files per call"))
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

// salvageEditRequest recovers the complete prefix of edit tool arguments whose
// JSON was cut off mid-stream (provider stream interrupted before the closing
// brackets arrived). The flat model-facing request {path, version, changes} is
// recovered field by field: complete changes decode exactly as they would on a
// healthy stream, while the truncated tail is dropped and counted. A change
// object that itself was cut is incomplete and must be dropped — its
// oldText/newText pair cannot be told apart from an intentional partial edit.
// The salvaged request still has to pass validateModelEditToolRequest and the
// full edit contract before anything is written, so recovery never lowers the
// validation bar. ok is true only when at least one complete change was
// recovered.
func salvageEditRequest(raw []byte) (file FileTextEdits, dropped int, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return file, 0, false
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			break
		}
		name, _ := key.(string)
		switch name {
		case "path", "version":
			value, err := dec.Token()
			if err != nil {
				return file, dropped, len(file.Changes) > 0
			}
			if s, isString := value.(string); isString {
				if name == "path" {
					file.Path = s
				} else {
					file.Version = s
				}
			}
		case "changes":
			tok, err := dec.Token()
			if err != nil || tok != json.Delim('[') {
				return file, dropped, len(file.Changes) > 0
			}
			for dec.More() {
				var change TextChange
				if err := dec.Decode(&change); err == nil {
					file.Changes = append(file.Changes, change)
					continue
				}
				dropped++
				return file, dropped, len(file.Changes) > 0
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return file, dropped, len(file.Changes) > 0
			}
		}
	}
	return file, dropped, len(file.Changes) > 0
}

// prepareToolCallsForExecution separates the arguments persisted/replayed to
// providers from the arguments used for this execution. A truncated edit keeps
// its raw byte prefix for salvageEditRequest, while the assistant tool_call is
// rewritten to the recovered valid JSON so the next model request cannot be
// rejected for malformed arguments. Other invalid calls use the explicit
// truncation marker on both paths.
func prepareToolCallsForExecution(toolCalls []openai.ToolCall) ([]openai.ToolCall, []string, bool) {
	prepared := cloneToolCalls(toolCalls)
	executionArgs := make([]string, len(prepared))
	salvaged := false
	for i := range prepared {
		raw := prepared[i].Function.Arguments
		executionArgs[i] = raw
		if strings.TrimSpace(raw) == "" {
			prepared[i].Function.Arguments = "{}"
			executionArgs[i] = "{}"
			continue
		}
		if json.Valid([]byte(raw)) {
			continue
		}
		if normalizeToolName(prepared[i].Function.Name) == "edit" {
			// The salvaged flat request is persisted back as the canonical
			// single-file form so the next model request carries valid
			// arguments and the history teaches the flat shape.
			if file, _, ok := salvageEditRequest([]byte(raw)); ok {
				salvagedFiles := []FileTextEdits{file}
				if safe, err := json.Marshal(file); err == nil && validateModelEditToolRequest(salvagedFiles) == nil {
					prepared[i].Function.Arguments = string(safe)
					salvaged = true
					continue
				}
			}
		}
		prepared[i].Function.Arguments = truncatedToolCallArguments
		executionArgs[i] = truncatedToolCallArguments
	}
	return prepared, executionArgs, salvaged
}

func salvageChanges(files []FileTextEdits) int {
	total := 0
	for _, file := range files {
		total += len(file.Changes)
	}
	return total
}

// editSalvageWarning is the brief note attached to a salvaged edit result so
// both the UI (yellow warning row) and the model (compacted warnings field)
// know the tail of the request was dropped and the remaining changes must be
// re-read and resent.
func editSalvageWarning(applied, dropped int) string {
	if dropped > 0 {
		return fmt.Sprintf("参数流被截断：已应用 %d 个完整改动，尾部 %d 个残缺改动已丢弃；请重新 read 后补发剩余改动", applied, dropped)
	}
	return fmt.Sprintf("参数流在末尾被截断，%d 个改动已全部完整恢复并应用", applied)
}

// attachEditSalvageWarning appends the salvage note to a successful edit
// result without disturbing the existing warnings.
func attachEditSalvageWarning(data any, applied, dropped int) any {
	warning := editSalvageWarning(applied, dropped)
	switch result := data.(type) {
	case MultiEditResult:
		result.Warnings = append(result.Warnings, warning)
		return result
	case *MultiEditResult:
		if result != nil {
			result.Warnings = append(result.Warnings, warning)
		}
		return result
	}
	return data
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
		out[i] = edit.TextChange{
			OldText:    c.OldText,
			LineRange:  c.LineRange,
			NewText:    c.NewText,
			ReplaceAll: c.ReplaceAll,
		}
	}
	return out
}

func fromEditChanges(in []edit.TextChange) []TextChange {
	if len(in) == 0 {
		return nil
	}
	out := make([]TextChange, len(in))
	for i, c := range in {
		out[i] = TextChange{OldText: c.OldText, LineRange: c.LineRange, NewText: c.NewText, ReplaceAll: c.ReplaceAll}
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
