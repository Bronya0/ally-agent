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
// ask/wait singleton rule, and semantic dedup of equivalent tool calls.

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

func isOrderedFileMutationTool(name string) bool {
	switch name {
	case "edit", "create", "delete", "remote_edit", "remote_create_file", "remote_delete_path":
		return true
	default:
		return false
	}
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
		if len(refs) < 2 {
			continue
		}
		display := refs[0].display
		err := codedToolError("E_WRITE_BATCH_CONFLICT", fmt.Errorf("multiple file mutations in the same tool batch target %s; no mutation for this path was executed. Send one write, wait for its result, then re-read before the next write", display))
		for _, ref := range refs {
			conflicts[ref.index] = err
		}
	}
	return conflicts
}

func detectToolBatchConflicts(cfg ConfigState, calls []openai.ToolCall) map[int]error {
	conflicts := detectWriteBatchConflicts(cfg, calls)
	if len(calls) <= 1 {
		return conflicts
	}
	barriers := []struct {
		name string
		code string
	}{
		{name: "ask", code: "E_ASK_BATCH_CONFLICT"},
		{name: "wait", code: "E_WAIT_BATCH_CONFLICT"},
		{name: "suggest", code: "E_SUGGEST_BATCH_CONFLICT"},
	}
	for _, barrier := range barriers {
		found := false
		for _, call := range calls {
			if call.Function.Name == barrier.name {
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
		key := call.Function.Name + "\x00" + normalizeToolArgsForDedup(call.Function.Arguments)
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

func fileMutationTargets(cfg ConfigState, name, arguments string) []fileMutationTarget {
	if name == "edit" {
		var req ModelEditToolRequest
		if json.Unmarshal([]byte(arguments), &req) != nil {
			return nil
		}
		plan, err := planLocalEditBatch(cfg, req.Files, localEditPlanForConflict)
		if err != nil {
			return nil
		}
		return plan.Targets
	}
	if name == "remote_edit" {
		var req RemoteEditRequest
		if json.Unmarshal([]byte(arguments), &req) != nil {
			return nil
		}
		result := make([]fileMutationTarget, 0, len(req.Files))
		for _, file := range req.Files {
			cleanPath := path.Clean(strings.ReplaceAll(strings.TrimSpace(file.Path), "\\", "/"))
			result = append(result, fileMutationTarget{"remote:" + strings.TrimSpace(req.Target) + ":" + cleanPath, strings.TrimSpace(req.Target) + " · " + cleanPath})
		}
		return result
	}
	var args struct {
		Target string `json:"target"`
		Path   string `json:"path"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil || strings.TrimSpace(args.Path) == "" {
		return nil
	}
	if strings.HasPrefix(name, "remote_") {
		target := strings.TrimSpace(args.Target)
		cleanPath := path.Clean(strings.ReplaceAll(strings.TrimSpace(args.Path), "\\", "/"))
		return []fileMutationTarget{{"remote:" + target + ":" + cleanPath, target + " · " + cleanPath}}
	}
	target, ok := localMutationTarget(cfg, args.Path)
	if !ok {
		return nil
	}
	return []fileMutationTarget{target}
}
