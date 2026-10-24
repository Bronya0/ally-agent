// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	toolshared "ally-dev/internal/tools/shared"
	openai "github.com/sashabaranov/go-openai"
)

// toolCallStage reads scheduling metadata without changing the tool arguments.
// MCP calls do not participate in the stage parameter contract (they use 0).
func toolCallStage(call openai.ToolCall) (int64, error) {
	if !toolshared.IsBuiltinTool(normalizeToolName(call.Function.Name)) {
		return 0, nil
	}
	var args map[string]json.RawMessage
	if call.Function.Arguments == "" {
		return 0, nil
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return 0, codedToolError("E_BAD_ARGS", fmt.Errorf("invalid tool arguments: %w", err))
	}
	raw, exists := args["stage"]
	if !exists {
		return 0, nil
	}
	var stage int64
	if string(raw) == "null" || json.Unmarshal(raw, &stage) != nil || stage < 0 || stage > toolshared.MaxToolStage {
		return 0, codedToolError("E_BAD_ARGS", fmt.Errorf("stage must be an integer between 0 and %d", toolshared.MaxToolStage))
	}
	return stage, nil
}

// A successfully returned command can still have failed as an operation. Keep
// its structured output intact; the stage barrier must nevertheless stop.
func toolStageSucceeded(result toolResult) bool {
	// Deduplication did not execute another operation. Its original call still
	// determines whether the stage succeeded; a real failure there is not masked.
	if result.ErrorCode == "E_DUPLICATE_TOOL_CALL" {
		return true
	}
	if !result.OK {
		return false
	}
	switch data := result.Data.(type) {
	case CommandResult:
		return data.ExitCode == 0 && !data.TimedOut && !data.Cancelled && !data.SandboxDenied && !data.PromotedToService
	case *CommandResult:
		return data != nil && toolStageSucceeded(toolResult{OK: true, Data: *data})
	case DeletePathsResult:
		return data.FailedCount == 0
	case *DeletePathsResult:
		return data != nil && data.FailedCount == 0
	case BatchReadResult:
		if len(data.Missing) > 0 {
			return false
		}
		for _, file := range data.Files {
			if file.Error != "" {
				return false
			}
		}
	case *BatchReadResult:
		return data != nil && toolStageSucceeded(toolResult{OK: true, Data: *data})
	}
	return true
}

// executeStagedToolBatch is the only scheduler for main and child agents.
// A stage drains completely, including calls queued behind the concurrency
// limit. Failures block later stages; the local edit executor joins same-stage
// edits at one prepare/commit boundary. Other completed work is not rolled back.
// Every call receives one result. Only execution cancellation stops queued work.
func (a *App) executeStagedToolBatch(ctx context.Context, cfg ConfigState, calls []openai.ToolCall, conflicts map[int]error,
	execute func(context.Context, int, openai.ToolCall) toolResult,
	publish func(int, openai.ToolCall, toolResult, int64),
	editBatch ...func(context.Context, ConfigState, []openai.ToolCall) []toolResult,
) {
	groups := map[int64][]int{}
	invalid := map[int]error{}
	for i, call := range calls {
		stage, err := toolCallStage(call)
		if err != nil {
			invalid[i] = codedToolError("E_BAD_ARGS", fmt.Errorf("toolCallIndex %d (%s): %w", i, call.Function.Name, err))
			continue
		}
		groups[stage] = append(groups[stage], i)
	}
	if len(invalid) > 0 {
		for i, call := range calls {
			err := invalid[i]
			if err == nil {
				err = codedToolError("E_STAGE_BLOCKED", fmt.Errorf("batch not executed: another call has invalid stage arguments"))
			}
			publish(i, call, toolErrorResult(err), 0)
		}
		return
	}
	stages := make([]int64, 0, len(groups))
	for stage := range groups {
		stages = append(stages, stage)
	}
	sort.Slice(stages, func(i, j int) bool { return stages[i] < stages[j] })
	sem := make(chan struct{}, clampToolConcurrency(cfg.ToolConcurrency))
	blocked := false
	for _, stage := range stages {
		indices := groups[stage]
		if blocked {
			for _, i := range indices {
				err := conflicts[i]
				if err == nil {
					err = codedToolError("E_STAGE_BLOCKED", fmt.Errorf("skipped: previous stage failed"))
				}
				publish(i, calls[i], toolErrorResult(err), 0)
			}
			continue
		}
		budget := &batchValidationBudget{remaining: autoValidationTimeout}
		results := make([]toolResult, len(indices))
		durations := make([]int64, len(indices))
		published := make([]bool, len(indices))
		var editSlots []int
		editPosition := map[int]int{}
		if len(editBatch) > 0 {
			for slot, index := range indices {
				if normalizeToolName(calls[index].Function.Name) == "edit" && conflicts[index] == nil {
					editPosition[slot] = len(editSlots)
					editSlots = append(editSlots, index)
				}
			}
		}
		var editOnce sync.Once
		var joined []toolResult
		var editDuration int64
		var wg sync.WaitGroup
		for slot, index := range indices {
			wg.Add(1)
			go func(slot, index int) {
				defer wg.Done()
				// Calls without local write validation retain immediate UI updates.
				defer func() {
					if !results[slot].OK || len(localMutationPathsForValidation(calls[index])) == 0 {
						publish(index, calls[index], results[slot], durations[slot])
						published[slot] = true
					}
				}()
				if err := conflicts[index]; err != nil {
					results[slot] = toolErrorResult(err)
					return
				}
				if pos, ok := editPosition[slot]; ok && len(editSlots) > 1 {
					editOnce.Do(func() {
						joined = make([]toolResult, len(editSlots))
						select {
						case sem <- struct{}{}:
							defer func() { <-sem }()
						case <-ctx.Done():
							for i := range joined {
								joined[i] = toolErrorResult(codedToolError("E_CANCELLED", ctx.Err()))
							}
							return
						}
						batch := make([]openai.ToolCall, len(editSlots))
						for i, index := range editSlots {
							batch[i] = calls[index]
						}
						started := time.Now()
						joined = editBatch[0](ctx, cfg, batch)
						editDuration = time.Since(started).Milliseconds()
					})
					results[slot] = joined[pos]
					durations[slot] = editDuration
					return
				}
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					results[slot] = toolErrorResult(codedToolError("E_CANCELLED", ctx.Err()))
					return
				}
				if ctx.Err() != nil {
					results[slot] = toolErrorResult(codedToolError("E_CANCELLED", ctx.Err()))
					return
				}
				started := time.Now()
				// Validate successful local writes together after all peers finish;
				// validators must never race another write in the same stage.
				callCtx := context.WithValue(ctx, batchValidationPathsContextKey{}, []string{})
				results[slot] = execute(callCtx, index, calls[index])
				durations[slot] = time.Since(started).Milliseconds()
			}(slot, index)
		}
		wg.Wait()
		stageCalls := make([]openai.ToolCall, len(indices))
		skip := map[int]bool{}
		for slot, index := range indices {
			stageCalls[slot] = calls[index]
			skip[slot] = !results[slot].OK
		}
		roots, _ := workspaceRoots(cfg)
		validationPlan := planBatchValidation(roots, stageCalls, skip)
		for slot, index := range indices {
			if paths := validationPlan[slot]; len(paths) > 0 {
				started := time.Now()
				checkCtx := context.WithValue(ctx, batchValidationBudgetContextKey{}, budget)
				results[slot].Data = attachValidation(results[slot].Data, a.validateChangedFilesForCall(checkCtx, cfg, paths))
				durations[slot] += time.Since(started).Milliseconds()
			}
			if !published[slot] {
				publish(index, calls[index], results[slot], durations[slot])
			}
			if !toolStageSucceeded(results[slot]) {
				blocked = true
			}
		}
	}
}
