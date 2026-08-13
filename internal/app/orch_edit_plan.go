// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section 10: Edit batch plan (was file_edit_plan.go)
// The single normalization boundary shared by outer tool-batch conflict
// detection and the local edit executor. AGENTS.md locks this as the only
// place that canonicalizes an edit request for both layers.

import (
	"fmt"
	"path/filepath"
	"strings"

	goruntime "runtime"
)

// localEditPlanMode selects how much validation is required while constructing
// the canonical view of one model-facing edit request. Conflict analysis only
// needs physical targets and must remain conservative even for an invalid
// request; execution additionally enforces the complete edit contract.
type localEditPlanMode uint8

const (
	localEditPlanForConflict localEditPlanMode = iota
	localEditPlanForExecution
)

type localEditFilePlan struct {
	Edit         FileTextEdits
	ResolvedPath string
	Target       fileMutationTarget
}

type localEditBatchPlan struct {
	Files   []localEditFilePlan
	Targets []fileMutationTarget
}

// planLocalEditBatch is the single normalization boundary shared by outer
// tool-batch conflict detection and the local edit executor. Repeated aliases
// of one physical path become one file plan and preserve the first display
// path. All changes remain relative to the same original version snapshot.
func planLocalEditBatch(cfg ConfigState, files []FileTextEdits, mode localEditPlanMode) (localEditBatchPlan, error) {
	if mode == localEditPlanForExecution {
		if err := validateModelEditToolRequest(files); err != nil {
			return localEditBatchPlan{}, err
		}
	}

	roots, err := workspaceRoots(cfg)
	if err != nil {
		return localEditBatchPlan{}, err
	}
	plan := localEditBatchPlan{
		Files:   make([]localEditFilePlan, 0, len(files)),
		Targets: make([]fileMutationTarget, 0, len(files)),
	}
	byTarget := make(map[string]int, len(files))
	for i, file := range files {
		resolved, resolveErr := safeJoin(roots, file.Path)
		target, targetOK := localMutationTargetFromRoots(roots, file.Path)
		if mode == localEditPlanForExecution && resolveErr != nil {
			return localEditBatchPlan{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, resolveErr)
		}
		if !targetOK {
			// An invalid target has no executable identity. Conflict analysis
			// leaves argument/path validation to executeTool, matching the
			// previous behavior while avoiding a guessed mutation key.
			continue
		}
		if existingIndex, exists := byTarget[target.key]; exists {
			existing := &plan.Files[existingIndex]
			if mode == localEditPlanForExecution && !strings.EqualFold(existing.Edit.Version, file.Version) {
				return localEditBatchPlan{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("duplicate edit entries for %s use different versions (%s and %s); re-read the file and submit one version", file.Path, existing.Edit.Version, file.Version))
			}
			existing.Edit.Changes = append(existing.Edit.Changes, file.Changes...)
			continue
		}
		byTarget[target.key] = len(plan.Files)
		plan.Targets = append(plan.Targets, target)
		plan.Files = append(plan.Files, localEditFilePlan{
			Edit: FileTextEdits{
				Path:    file.Path,
				Version: file.Version,
				Changes: append([]TextChange(nil), file.Changes...),
			},
			ResolvedPath: resolved,
			Target:       target,
		})
	}

	if mode == localEditPlanForExecution {
		merged := make([]FileTextEdits, len(plan.Files))
		for i := range plan.Files {
			merged[i] = plan.Files[i].Edit
		}
		if err := validateModelEditToolRequest(merged); err != nil {
			return localEditBatchPlan{}, err
		}
	}
	return plan, nil
}

func localMutationTarget(cfg ConfigState, filePath string) (fileMutationTarget, bool) {
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return fileMutationTarget{}, false
	}
	return localMutationTargetFromRoots(roots, filePath)
}

// localMutationTargetFromRoots creates a conservative physical identity for
// conflict detection. If safeJoin rejects the path, the executor will still
// reject it later; using the cleaned absolute candidate here prevents two
// invalid aliases from bypassing the same-batch write guard.
func localMutationTargetFromRoots(roots []string, filePath string) (fileMutationTarget, bool) {
	if len(roots) == 0 || strings.TrimSpace(filePath) == "" {
		return fileMutationTarget{}, false
	}
	root := roots[0]
	absPath, err := safeJoin(roots, filePath)
	if err != nil {
		if filepath.IsAbs(filePath) {
			absPath = filepath.Clean(filePath)
		} else {
			absPath = filepath.Join(root, filePath)
		}
	}
	absPath, _ = filepath.Abs(absPath)
	absPath = filepath.Clean(absPath)
	keyPath := filepath.ToSlash(absPath)
	if goruntime.GOOS == "windows" {
		keyPath = strings.ToLower(keyPath)
	}
	return fileMutationTarget{"local:" + keyPath, filepath.ToSlash(filePath)}, true
}
