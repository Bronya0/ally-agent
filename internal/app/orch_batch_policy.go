// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section 11: Tool batch policy (was batch_policy.go); Section 10: Edit batch plan
// (was file_edit_plan.go)
// App-owned tool-batch conflict detection: stage-local mutation barriers, the
// ask/suggest exclusive-barrier rule, and
// semantic dedup of equivalent tool calls.
//
// planLocalEditBatch 把一个 edit 调用摊成多少次写入、每个写入
// 落在哪条路径（localMutationTarget）——冲突检测与真正落盘用的是同一份计划。

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"

	toolshared "ally-dev/internal/tools/shared"
	openai "github.com/sashabaranov/go-openai"
)

// isFileMutationTool classifies side effects for conflict detection and plan
// accounting only. Execution order is determined exclusively by stage.
func isFileMutationTool(name string) bool {
	switch normalizeToolName(name) {
	case "edit", "create", "delete", "remote_edit", "remote_create_file", "remote_delete_path", "remote_transfer":
		return true
	}
	return false
}

// planWorkTools are the tools whose successful call means the run did work a
// plan could have moved past. Reads (read, grep, list_files, web_fetch, …) leave
// the plan where it is and must not make the end-of-run plan check fire, and the
// file-mutation tools are read from the mutation classifier above instead of being
// repeated here, so a new mutation tool cannot be forgotten in one of two lists.
var planWorkTools = map[string]bool{
	"command":            true,
	"remote_run_command": true,
}

// toolDidPlanWork reports whether a successful call of this tool is work the plan
// should reflect. It is the single predicate behind the end-of-run plan check.
func toolDidPlanWork(name string) bool {
	normalized := normalizeToolName(name)
	return isFileMutationTool(normalized) || planWorkTools[normalized]
}

func detectWriteBatchConflicts(cfg ConfigState, calls []openai.ToolCall) map[int]error {
	type targetRef struct {
		index   int
		display string
	}
	groups := map[string][]targetRef{}
	for i, call := range calls {
		if !isFileMutationTool(call.Function.Name) {
			continue
		}
		for _, target := range fileMutationTargets(cfg, call.Function.Name, call.Function.Arguments) {
			stage, _ := toolCallStage(call)
			key := fmt.Sprintf("%d\x00%s", stage, target.key)
			groups[key] = append(groups[key], targetRef{index: i, display: target.display})
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
			conflicts[ref.index] = codedToolError("E_WRITE_BATCH_CONFLICT", fmt.Errorf("another file mutation for %s appears earlier in the same stage; only the first one executes. This call was skipped — wait for that mutation's result, then re-read the file (or reuse its returned version) and re-send this change in a later response if it is still needed", refs[0].display))
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
	// whose results the model has not seen yet. `wait` is an ordinary staged call and does not own its batch.
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
	// The plan tool takes one writer per stage. Two writes in one stage —
	// most plausibly a report plus the steps rewrite it provoked — run in the same
	// concurrent pool, so which one lands last is a coin flip: a steps that lands
	// after a finish rewinds the position that report had just moved, the loser's
	// intent silently disappears, and the two tool results contradict each other.
	// Keep the first write and reject the rest by name, the same shape as the
	// same-path write rule above. Read-only plan calls are not writers: several
	// reads are harmless, and identical ones are already collapsed by the dedup
	// below.
	firstPlanWrites := map[int64]int{}
	for i, call := range calls {
		if normalizeToolName(call.Function.Name) != planToolName || !planBatchWriteSource(call.Function.Arguments) {
			continue
		}
		stage, _ := toolCallStage(call)
		firstPlanWrite, found := firstPlanWrites[stage]
		if !found {
			firstPlanWrites[stage] = i
			continue
		}
		conflicts[i] = codedToolError("E_PLAN_BATCH_CONFLICT", fmt.Errorf("a stage accepts one plan write, and another plan write (toolCallIndex %d) comes earlier in this stage; this call was skipped, and that earlier call may itself have been refused — read its result before sending this update in a later response", firstPlanWrite))
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
		args := call.Function.Arguments
		if stage, err := toolCallStage(call); err == nil && toolshared.IsBuiltinTool(normalizeToolName(call.Function.Name)) {
			// Omitted stage and explicit 0 are the same scheduling intent.
			// This canonical copy is only a key; execution keeps the original JSON.
			var object map[string]json.RawMessage
			if json.Unmarshal([]byte(args), &object) == nil && object != nil {
				object["stage"] = json.RawMessage(fmt.Sprint(stage))
				canonical, _ := json.Marshal(object)
				args = string(canonical)
			}
		}
		key := normalizeToolName(call.Function.Name) + "\x00" + normalizeToolArgsForDedup(args)
		first, ok := seen[key]
		if !ok {
			seen[key] = i
			continue
		}
		conflicts[i] = codedToolError("E_DUPLICATE_TOOL_CALL", fmt.Errorf("this tool call is a semantic duplicate of toolCallIndex %d in the same batch (same function and equivalent arguments after JSON normalization) and was skipped; reuse that result instead of re-running the identical call", first))
	}
	return conflicts
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
// normalized first for the same reason as isFileMutationTool: every branch
// below compares against the canonical lowercase tool names. The result is a set:
// one path, one target (see dedupeMutationTargets).
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
	if name == "remote_transfer" {
		var transArgs struct {
			Target     string `json:"target"`
			Action     string `json:"action"`
			LocalPath  string `json:"localPath"`
			RemotePath string `json:"remotePath"`
		}
		if json.Unmarshal([]byte(arguments), &transArgs) != nil {
			return nil
		}
		action := strings.ToLower(strings.TrimSpace(transArgs.Action))
		switch action {
		case "upload":
			target := strings.TrimSpace(transArgs.Target)
			cleanPath := path.Clean(strings.ReplaceAll(strings.TrimSpace(transArgs.RemotePath), "\\", "/"))
			if cleanPath != "" && cleanPath != "." {
				return []fileMutationTarget{{
					key:     remoteMutationKey(target, cleanPath),
					display: target + " · " + cleanPath,
				}}
			}
		case "download":
			if target, ok := localMutationTarget(cfg, transArgs.LocalPath); ok {
				return []fileMutationTarget{target}
			}
		}
		return nil
	}
	// delete tools take a `paths` list; single-file tools (create, remote_edit,
	// remote_create_file) carry one `path`. The two shapes are read on separate
	// branches: a gate-rejected `path` on a delete call must never mint a phantom
	// target, while a single-path tool must never read as "touches nothing"
	// (which would silently disable the same-path write guard; a batch delete that
	// lists two overlapping paths is normalised inside the call itself, see
	// planDeleteExecution).
	var args struct {
		Target string   `json:"target"`
		Path   string   `json:"path"`
		Paths  []string `json:"paths"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return nil
	}
	var paths []string
	switch name {
	case "delete", "remote_delete_path":
		paths = mutationArgumentPaths("", args.Paths)
	default:
		paths = mutationArgumentPaths(args.Path, args.Paths)
	}
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
		return dedupeMutationTargets(targets)
	}
	for _, p := range paths {
		target, ok := localMutationTarget(cfg, p)
		if !ok {
			continue
		}
		targets = append(targets, target)
	}
	return dedupeMutationTargets(targets)
}

// dedupeMutationTargets keeps one target per identity key. A call that names the
// same path twice — including two spellings of it — touches that path once, so a
// repeated key must not reach the conflict grouping: it would read as the call
// conflicting with itself, while a duplicated list is that call's own rule's
// business (planDeleteExecution).
func dedupeMutationTargets(targets []fileMutationTarget) []fileMutationTarget {
	seen := make(map[string]bool, len(targets))
	unique := make([]fileMutationTarget, 0, len(targets))
	for _, target := range targets {
		if seen[target.key] {
			continue
		}
		seen[target.key] = true
		unique = append(unique, target)
	}
	return unique
}

// remoteMutationKey 是远端变更目标的唯一键：批次写冲突判定与一次远端删除调用
// 内部的重复/包含归一（planDeleteExecution）共用它，所以两个判据认的是同一个身份。
func remoteMutationKey(target, relPath string) string {
	cleanPath := path.Clean(strings.ReplaceAll(strings.TrimSpace(relPath), "\\", "/"))
	return "remote:" + strings.TrimSpace(target) + ":" + cleanPath
}

// mutationArgumentPaths 枚举「这次调用会动哪些路径」，供上面的写冲突判定使用：
// 单文件工具走 single，删除类工具走 list。它与 resolveDeletePathList 的契约
// 不同（那个要拒绝畸形请求，这个只做尽力而为的取样）：畸形请求由执行器自己拒，
// 但这里要把能看出来的目标都算进去，否则两个非法别名可以绕开同批写守卫。
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

// Section 10: Edit batch plan (was file_edit_plan.go)
// The single normalization boundary shared by outer tool-batch conflict
// detection and the local edit executor. AGENTS.md locks this as the only
// place that canonicalizes an edit request for both layers.

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
		if len(files) == 0 {
			return localEditBatchPlan{}, validateModelEditToolRequest(files)
		}
		// Independent model calls share an atomic stage, not a call budget.
		for _, file := range files {
			if err := validateModelEditToolRequest([]FileTextEdits{file}); err != nil {
				return localEditBatchPlan{}, err
			}
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
		// Execution resolves the write path exactly like create/delete do, so a
		// symlinked directory inside the workspace cannot carry the write
		// outside it (SafeWriteFile creates its temp file in filepath.Dir(path),
		// i.e. in the link's target). Conflict analysis stays lexical — it only
		// needs a stable identity, and the path guards belong to the executor.
		var resolved string
		if mode == localEditPlanForExecution {
			writePath, resolveErr := resolveWritableFilePath(roots, file.Path)
			if resolveErr != nil {
				return localEditBatchPlan{}, fmt.Errorf("file %d (%s): %w", i+1, file.Path, resolveErr)
			}
			resolved = writePath
		} else {
			resolved, _ = safeJoin(roots, file.Path)
		}
		target, targetOK := localMutationTargetFromRoots(roots, file.Path)
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
		for _, file := range plan.Files {
			if err := validateModelEditToolRequest([]FileTextEdits{file.Edit}); err != nil {
				return localEditBatchPlan{}, err
			}
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
	return fileMutationTarget{localMutationKey(absPath), filepath.ToSlash(filePath)}, true
}

// localMutationKey 是本地变更目标的唯一键：批次写冲突判定（detectWriteBatchConflicts）
// 与一次删除调用内部的重复/包含判定共用它，所以「同一批里写两次同一个文件」与
// 「一次删除里列两次同一个路径」认的是同一个身份。
func localMutationKey(absPath string) string {
	absPath, _ = filepath.Abs(absPath)
	keyPath := filepath.ToSlash(filepath.Clean(absPath))
	if goruntime.GOOS == "windows" {
		keyPath = strings.ToLower(keyPath)
	}
	return "local:" + keyPath
}
