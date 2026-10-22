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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ally-dev/internal/tools/edit"
	"ally-dev/internal/tools/read"
	"ally-dev/internal/tools/toolcall"

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

const maxEditRecoveryBytes = 8 * 1024

// editRecoveryError keeps the read payload out of Error(), JSON and UI events.
// Only the model renderer consumes it, in the original failed tool result.
type editRecoveryError struct {
	err  error
	read BatchReadResult
}

func (e *editRecoveryError) Error() string { return e.err.Error() }
func (e *editRecoveryError) Unwrap() error { return e.err }

// withEditRecovery is called only after edit has read a text snapshot and passed
// the write fence. Reuse read's guards and formatting, without another disk read
// or the session read cache (recovery must actually show the source).
func withEditRecovery(cfg ConfigState, path string, data []byte, size int64, err error) error {
	switch toolErrorCode(err) {
	case "E_VERSION_MISMATCH", "E_NO_MATCH", "E_MULTI_MATCH", "E_RANGE_OOB", "E_BAD_ARGS":
		// E_BAD_ARGS from ApplyBatchTextChanges is its truncated-line guard.
	default:
		return err
	}
	if size > maxEditRecoveryBytes || len(data) > maxEditRecoveryBytes {
		return err
	}
	readPath, guardErr := resolveReadFilePath(cfg, path)
	if guardErr != nil || documentExtensionInRead(readPath) {
		return err
	}
	snapshot, readErr := buildTextReadResult(displayPathForConfig(cfg, readPath), data, size, ReadFileRequest{})
	if readErr != nil || snapshot.Truncated || len(snapshot.TruncatedLines) > 0 {
		return err
	}
	recovery := BatchReadResult{Files: []BatchReadResultItem{batchReadResultItem(ReadFileRequest{}, snapshot)}}
	if len(renderReadResultForModel(recovery)) > maxEditRecoveryBytes {
		return err
	}
	return &editRecoveryError{err: err, read: recovery}
}

// toEditChanges converts the model-facing change shape into the engine's own. The
// wire struct carries newText as a plain string, and so does the engine: an empty
// string means "delete this text", which is why the declaration marks newText
// required and the argument gate refuses a change that leaves it out.

func (a *App) editFilesWithConfig(cfg ConfigState, files []FileTextEdits) (MultiEditResult, error) {
	plan, err := planLocalEditBatch(cfg, files, localEditPlanForExecution)
	if err != nil {
		return MultiEditResult{}, err
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return MultiEditResult{}, err
	}
	spec := fileMutationSpec(cfg, roots)
	prepared := make([]preparedFileEdit, 0, len(plan.Files))
	for i, filePlan := range plan.Files {
		file := filePlan.Edit
		resolved := filePlan.ResolvedPath
		if _, err := resolveReadFilePath(cfg, resolved); err != nil {
			return MultiEditResult{}, err
		}
		before, info, err := readTextFile(resolved)

		if err != nil {
			return MultiEditResult{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, err)
		}
		beforeHash, beforeVersion := hashBytesAndVersion(before)
		if !strings.EqualFold(file.Version, beforeVersion) {
			err := codedToolError("E_VERSION_MISMATCH", fmt.Errorf("file %s expected version %s, current %s; use the current source and version before retrying (read again if no snapshot is attached)", file.Path, file.Version, beforeVersion))
			return MultiEditResult{}, withEditRecovery(cfg, resolved, before, info.Size(), err)
		}
		text, ending, hadBOM := normalizeText(before)
		applied, replacements, err := edit.ApplyBatchTextChanges(text, toEditChanges(file.Changes))
		if err != nil {
			err = fmt.Errorf("file %d (%s): %w", i+1, file.Path, err)
			return MultiEditResult{}, withEditRecovery(cfg, resolved, before, info.Size(), err)
		}
		after := encodeText(applied.Content, ending, hadBOM)
		diff, removed, added := edit.GenerateEditDiffPreviewWithCounts(text, applied.Content, maxToolOutput)
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
				ChangedLinesBlock: edit.BuildLineNumberContextBlockBounded(applied.Content, applied.FirstChangedLine, applied.LastChangedLine),
			},
		})
	}
	committed := make([]int, 0, len(prepared))
	rollback := func() error {
		var rollbackErrors []string
		for i := len(committed) - 1; i >= 0; i-- {
			item := prepared[committed[i]]
			if err := sandboxedWriteFile(spec, roots, item.path, item.before, item.perm); err != nil {
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
		current, info, err := readTextFile(item.path)
		if err != nil || !strings.EqualFold(hashVersion(current), item.result.BeforeVersion) {
			rollbackErr := rollback()
			msg := fmt.Sprintf("file changed before commit: %s; use the current source and version before retrying (read again if no snapshot is attached)", item.display)
			if rollbackErr != nil {
				msg += "; rollback errors: " + rollbackErr.Error()
			}
			commitErr := codedToolError("E_VERSION_MISMATCH", errors.New(msg))
			if err == nil {
				commitErr = withEditRecovery(cfg, item.path, current, info.Size(), commitErr)
			}
			return MultiEditResult{}, commitErr
		}
		if err := sandboxedWriteFile(spec, roots, item.path, item.after, item.perm); err != nil {
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

// prepareToolCallsForExecution separates the arguments persisted/replayed to
// providers from the arguments used for this execution. Stream-truncated
// arguments are replaced with the explicit truncation marker on both paths:
// providers that parse tool_calls arguments server-side reject malformed
// JSON with a 400, and the executor turns the marker into E_TRUNCATED_ARGS so
// the model resends the complete call.
func prepareToolCallsForExecution(toolCalls []openai.ToolCall) ([]openai.ToolCall, []string) {
	prepared := cloneToolCalls(toolCalls)
	executionArgs := make([]string, len(prepared))
	for i := range prepared {
		raw := toolcall.CleanArguments(prepared[i].Function.Arguments)
		if strings.TrimSpace(raw) == "" {
			raw = "{}"
		}
		// A truncated argument string must never reach a tool or stay in
		// history: the marker is the single contract for that (see
		// toolcall.RepairTruncatedArguments).
		raw = toolcall.RepairTruncatedArguments(raw)
		prepared[i].Function.Arguments = raw
		executionArgs[i] = raw
	}
	return prepared, executionArgs
}

func validateVersion(version string) error {
	return read.ValidateVersion(version)
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
