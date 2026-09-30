// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section 11: Tool batch policy (was batch_policy.go)
// App-owned tool-batch conflict detection: same-path mutation barriers, the
// ask/suggest exclusive-barrier rule, the deferred tail phase (`wait`), and
// semantic dedup of equivalent tool calls.

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// toolBatchPhase says when a tool runs inside one model response's batch. A tool
// whose meaning depends on the rest of the batch is declared here instead of
// racing it in the concurrent pool.
type toolBatchPhase int

const (
	// batchPhaseParallel (the default for anything missing from the table) runs
	// in the concurrent pool with its peers.
	batchPhaseParallel toolBatchPhase = iota
	// batchPhaseOrderedMutation: file writes — serial in tool-call order, with
	// the directory-level validation pass afterwards.
	batchPhaseOrderedMutation
	// batchPhaseDeferredSerial: runs after both phases above, serial in tool-call
	// order. `wait` is the only member today: a pause is a sequencing detail,
	// not a reason to discard the rest of the batch (that is what the exclusive
	// barriers in detectToolBatchConflicts do), and its result still reaches the
	// model because a pause never ends the run.
	batchPhaseDeferredSerial
)

// toolBatchPhases is the single declaration of the tools that do NOT run in the
// default parallel phase. Adding a deferred or ordered tool is one line here:
// both agent loops (runChat and executeSubagent) classify every call through
// toolBatchPhaseFor, so no call site needs to know the new name.
var toolBatchPhases = map[string]toolBatchPhase{
	"edit":               batchPhaseOrderedMutation,
	"create":             batchPhaseOrderedMutation,
	"delete":             batchPhaseOrderedMutation,
	"remote_edit":        batchPhaseOrderedMutation,
	"remote_create_file": batchPhaseOrderedMutation,
	"remote_delete_path": batchPhaseOrderedMutation,
	"wait":               batchPhaseDeferredSerial,
}

// toolBatchPhaseFor classifies a tool name. The name is normalized here instead
// of at each call site: the caller passes what the model or a relay produced
// (`Edit`), while executeTool normalizes at its own boundary, so a raw variant
// used to be classified as a non-mutating tool and silently lost both write
// ordering and the ask/suggest barrier.
func toolBatchPhaseFor(name string) toolBatchPhase {
	if phase, ok := toolBatchPhases[normalizeToolName(name)]; ok {
		return phase
	}
	return batchPhaseParallel
}

func isOrderedFileMutationTool(name string) bool {
	return toolBatchPhaseFor(name) == batchPhaseOrderedMutation
}

func isDeferredSerialTool(name string) bool {
	return toolBatchPhaseFor(name) == batchPhaseDeferredSerial
}

// planWorkTools are the tools whose successful call means the run did work a
// plan could have moved past. Reads (read, grep, list_files, web_fetch, …) leave
// the plan where it is and must not make the end-of-run plan check fire, and the
// file-mutation tools are read from the phase table above instead of being
// repeated here, so a new mutation tool cannot be forgotten in one of two lists.
var planWorkTools = map[string]bool{
	"command":            true,
	"remote_run_command": true,
}

// toolDidPlanWork reports whether a successful call of this tool is work the plan
// should reflect. It is the single predicate behind the end-of-run plan check.
func toolDidPlanWork(name string) bool {
	normalized := normalizeToolName(name)
	return isOrderedFileMutationTool(normalized) || planWorkTools[normalized]
}

func detectWriteBatchConflicts(cfg ConfigState, calls []openai.ToolCall) map[int]error {
	type targetRef struct {
		index   int
		display string
	}
	groups := map[string][]targetRef{}
	for i, call := range calls {
		if !isOrderedFileMutationTool(call.Function.Name) {
			continue
		}
		for _, target := range fileMutationTargets(cfg, call.Function.Name, call.Function.Arguments) {
			groups[target.key] = append(groups[target.key], targetRef{index: i, display: target.display})
		}
	}
	conflicts := map[int]error{}
	for _, refs := range groups {
		// Refs are appended in tool-call index order, so refs[0] is the
		// earliest mutation for this path. It executes; later same-path calls
		// are skipped and reported, mirroring E_DUPLICATE_TOOL_CALL: the model
		// sees which call won and re-sends the skipped change in a later
		// response once it knows the path's new state.
		for _, ref := range refs[1:] {
			conflicts[ref.index] = codedToolError("E_WRITE_BATCH_CONFLICT", fmt.Errorf("another file mutation for %s appears earlier in this tool batch; only the first one executes. This call was skipped — wait for that mutation's result, then re-read the file (or reuse its returned version) and re-send this change in a later response if it is still needed", refs[0].display))
		}
	}
	return conflicts
}

// planBatchWriteSource reports whether a plan call would change the plan. It
// asks the same classifier the handler runs (a blank finish is not a source), so
// the batch rule cannot drift from the tool vocabulary and a call that only reads
// the plan back never owns the batch.
func planBatchWriteSource(arguments string) bool {
	var req PlanRequest
	if json.Unmarshal([]byte(arguments), &req) != nil {
		return false
	}
	action, _, err := classifyPlanRequest(req)
	return err == nil && action != planActionRead
}

func detectToolBatchConflicts(cfg ConfigState, calls []openai.ToolCall) map[int]error {
	conflicts := detectWriteBatchConflicts(cfg, calls)
	if len(calls) <= 1 {
		return conflicts
	}
	// ask and suggest own their batch: ask parks the run on a human answer and
	// suggest ends the run outright, so neither may share a response with calls
	// whose results the model has not seen yet. `wait` is deliberately absent — it
	// is deferred instead of rejected (see isDeferredSerialTool).
	exclusiveBarriers := []struct {
		name string
		code string
	}{
		{name: "ask", code: "E_ASK_BATCH_CONFLICT"},
		{name: "suggest", code: "E_SUGGEST_BATCH_CONFLICT"},
	}
	for _, barrier := range exclusiveBarriers {
		found := false
		for _, call := range calls {
			if normalizeToolName(call.Function.Name) == barrier.name {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		err := codedToolError(barrier.code, fmt.Errorf("%s must be the only tool call in its batch; no tool in this batch was executed", barrier.name))
		for i := range calls {
			conflicts[i] = err
		}
		return conflicts
	}
	// The plan tool takes one writer per batch. Two writes in one response —
	// most plausibly a report plus the steps rewrite it provoked — run in the same
	// concurrent pool, so which one lands last is a coin flip: a steps that lands
	// after a finish rewinds the position that report had just moved, the loser's
	// intent silently disappears, and the two tool results contradict each other.
	// Keep the first write and reject the rest by name, the same shape as the
	// same-path write rule above. Read-only plan calls are not writers: several
	// reads are harmless, and identical ones are already collapsed by the dedup
	// below.
	firstPlanWrite := -1
	for i, call := range calls {
		if normalizeToolName(call.Function.Name) != planToolName || !planBatchWriteSource(call.Function.Arguments) {
			continue
		}
		if firstPlanWrite < 0 {
			firstPlanWrite = i
			continue
		}
		conflicts[i] = codedToolError("E_PLAN_BATCH_CONFLICT", fmt.Errorf("a batch accepts one plan write, and another plan write (toolCallIndex %d) comes earlier in this one; this call was skipped, and that earlier call may itself have been refused — read its result before sending this update in a later response", firstPlanWrite))
	}
	// Deduplicate calls with semantically identical arguments within the same batch.
	// Models occasionally emit two or more tool calls that mean the same thing but
	// differ in JSON serialization: field order ({"url":"X","method":"GET"} vs
	// {"method":"GET","url":"X"}), whitespace ({"url": "X"} vs {"url":"X"}), or
	// default-value fields ({"url":"X"} vs {"url":"X","method":"GET"} when GET is
	// the default). Running them again wastes resources, races on side effects,
	// and complicates the model's own reasoning. Keep the first occurrence; reject
	// the rest with E_DUPLICATE_TOOL_CALL so the model can see the dedup happened
	// and stop retrying.
	//
	// The dedup key normalizes the arguments by parsing the JSON (when parseable)
	// and reserializing with sorted keys and no extra whitespace. This catches
	// field-order and whitespace differences for free. Default-value normalization
	// is intentionally NOT done here because it would require per-tool knowledge
	// and could mask legitimately different intents; the UI is responsible for
	// making any remaining differences visible.
	seen := map[string]int{}
	for i, call := range calls {
		if _, conflict := conflicts[i]; conflict {
			continue
		}
		key := normalizeToolName(call.Function.Name) + "\x00" + normalizeToolArgsForDedup(call.Function.Arguments)
		first, ok := seen[key]
		if !ok {
			seen[key] = i
			continue
		}
		conflicts[i] = codedToolError("E_DUPLICATE_TOOL_CALL", fmt.Errorf("this tool call is a semantic duplicate of toolCallIndex %d in the same batch (same function and equivalent arguments after JSON normalization) and was skipped; reuse that result instead of re-running the identical call", first))
	}
	return conflicts
}

// conflictSkippedCallIndexes returns the tool-call indexes that the batch
// conflict policy rejects before execution; planning passes must not let
// those calls own validation units they will never run.
func conflictSkippedCallIndexes(conflicts map[int]error) map[int]bool {
	skip := make(map[int]bool, len(conflicts))
	for idx := range conflicts {
		skip[idx] = true
	}
	return skip
}

// normalizeToolArgsForDedup returns a canonical form of a tool-call arguments
// JSON string used only for deduplication. It parses the JSON and reserializes
// it with sorted keys and no extra whitespace, so that field-order differences
// and whitespace differences are treated as identical. If the input is not
// valid JSON, the raw string is returned unchanged so non-JSON or malformed
// arguments still dedup on exact bytes.
func normalizeToolArgsForDedup(args string) string {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return trimmed
	}
	canonical, err := json.Marshal(sortedJSON(parsed))
	if err != nil {
		return trimmed
	}
	return string(canonical)
}

// sortedJSON recursively reorders object keys in a parsed JSON value so that
// json.Marshal produces a stable canonical form. Arrays keep their order, since
// argument arrays are typically order-sensitive (e.g. edit.files, ask.questions).
func sortedJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			out[k] = sortedJSON(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = sortedJSON(item)
		}
		return out
	default:
		return v
	}
}

type fileMutationTarget struct{ key, display string }

// fileMutationTargets resolves the paths one mutation call would touch. name is
// normalized first for the same reason as isOrderedFileMutationTool: every branch
// below compares against the canonical lowercase tool names.
func fileMutationTargets(cfg ConfigState, name, arguments string) []fileMutationTarget {
	name = normalizeToolName(name)
	if name == "edit" {
		// The flat model-facing request carries exactly one file; conflict
		// detection and execution share the same plan boundary.
		var req FileTextEdits
		if json.Unmarshal([]byte(arguments), &req) != nil {
			return nil
		}
		plan, err := planLocalEditBatch(cfg, []FileTextEdits{req}, localEditPlanForConflict)
		if err != nil {
			return nil
		}
		return plan.Targets
	}
	// remote_edit is flat and single-file; the two delete tools also take a
	// `paths` list, so the generic remote_*/local branches below read both
	// spellings and return one target per entry (which is what the same-path
	// write guard needs: a batch delete of two overlapping paths is caught in
	// the call itself, see checkDeletePathList).
	var args struct {
		Target string   `json:"target"`
		Path   string   `json:"path"`
		Paths  []string `json:"paths"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return nil
	}
	paths := mutationArgumentPaths(args.Path, args.Paths)
	if len(paths) == 0 {
		return nil
	}
	targets := make([]fileMutationTarget, 0, len(paths))
	if strings.HasPrefix(name, "remote_") {
		target := strings.TrimSpace(args.Target)
		for _, p := range paths {
			cleanPath := path.Clean(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
			targets = append(targets, fileMutationTarget{remoteMutationKey(target, cleanPath), target + " · " + cleanPath})
		}
		return targets
	}
	for _, p := range paths {
		target, ok := localMutationTarget(cfg, p)
		if !ok {
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

// remoteMutationKey 是远端变更目标的唯一键：批次写冲突判定与一次远端删除调用
// 内部的重复/包含判定共用它，所以两个判据认的是同一个身份。
func remoteMutationKey(target, relPath string) string {
	cleanPath := path.Clean(strings.ReplaceAll(strings.TrimSpace(relPath), "\\", "/"))
	return "remote:" + strings.TrimSpace(target) + ":" + cleanPath
}

// mutationArgumentPaths 枚举「这次调用会动哪些路径」，供上面的写冲突判定使用：
// 编辑类工具只写 path，删除类工具两个写法都认。它与 resolveDeletePathList 的
// 契约不同（那个要拒绝畸形请求，这个只做尽力而为的取样）：畸形请求由执行器
// 自己拒，但这里要把能看出来的目标都算进去，否则两个非法别名可以绕开同批写
// 守卫。
func mutationArgumentPaths(single string, list []string) []string {
	paths := make([]string, 0, len(list)+1)
	if strings.TrimSpace(single) != "" {
		paths = append(paths, single)
	}
	for _, candidate := range list {
		if strings.TrimSpace(candidate) != "" {
			paths = append(paths, candidate)
		}
	}
	return paths
}
