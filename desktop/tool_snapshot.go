// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	toolshared "ally-dev/internal/tools/shared"
	"encoding/json"
	"maps"
	"strings"
	"unicode/utf8"
)

const createPreviewMaxBytes = 8 << 10

func isMCPDisplayTool(m map[string]any) bool {
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	return str(m, "mcpServer") != "" || str(m, "mcpTool") != "" || strings.HasPrefix(name, "mcp")
}

// MCP cards show only status and timing. The runtime keeps the complete result
// in its own context; never mutate the event or the model's copy here.
func retainMCPDisplaySummary(m map[string]any) bool {
	if !isMCPDisplayTool(m) || boolOf(m, "fastCompacted") {
		return false
	}
	if _, ok := m["result"]; !ok {
		return false
	}
	detachToolResultEnvelope(m)
	result := toolResult(m)
	summary := readCardMetadata(result, false)
	switch elapsed := result["elapsedMs"].(type) {
	case int, int64, float32, float64:
		summary["elapsedMs"] = elapsed
	}
	if userCancelledMessage(result) {
		summary["cancelled"] = true
	}
	m["result"] = summary
	delete(m, "body")
	return true
}

// A saved observation keeps only what its historical card displays. Build the
// projection before the deep clone, without touching live result maps; envelope
// unwrapping otherwise merges fields into the original nested data map.
// A true return means result owns only detached maps/slices and scalar values;
// the snapshot can transfer it directly instead of recursively cloning it again.
func retainSnapshotToolSummary(m map[string]any) bool {
	name := readGrepGroupName(m)
	if !toolshared.OmitToolResultOnDisk(name) {
		return false
	}
	if name != "command" && name != "remote_run_command" && boolOf(m, "fastCompacted") {
		return false // an explicit, bounded compaction excerpt is already a summary
	}
	detachToolResultEnvelope(m)
	if name == "command" || name == "remote_run_command" {
		retainCommandSummary(m)
		if name == "remote_run_command" {
			m["result"].(map[string]any)["resultOmitted"] = true
		}
		delete(m, "content")
		return true
	}
	summary := readCardSummary(name, toolResult(m), true)
	summary["resultOmitted"] = true
	m["result"] = summary
	delete(m, "body")
	delete(m, "content")
	if text := str(m, "error"); text != "" {
		m["error"] = short(text, 300)
	}
	return true
}

// Finished create cards only show the last few lines; clicking opens the file
// through the workspace editor. Retain that preview in UI arguments instead of
// keeping another whole-file copy beside the backend's original tool call.
func retainCreateDisplayPreview(m map[string]any) {
	name := readGrepGroupName(m)
	if (name != "create" && name != "remote_create_file") || isMCPDisplayTool(m) {
		return
	}
	switch toolState(m) {
	case "success", "error", "cancelled":
	default:
		return
	}
	var args map[string]any
	if json.Unmarshal([]byte(str(m, "args")), &args) != nil {
		return // partial or malformed arguments remain available to the old view
	}
	content := str(args, "content")
	if len(content) <= createPreviewMaxBytes {
		return
	}
	start := max(1, strings.Count(strings.TrimRight(content, "\n"), "\n")+2-createPreviewLines)
	if boolOf(m, "createPreviewTrimmed") {
		start += max(0, int(num(m, "createPreviewStartLine"))-1)
	}
	preview := tailLines(content, createPreviewLines)
	if len(preview) > createPreviewMaxBytes {
		cut := len(preview) - createPreviewMaxBytes + len("…")
		for cut < len(preview) && !utf8.RuneStart(preview[cut]) {
			cut++
		}
		start += strings.Count(preview[:cut], "\n")
		preview = "…" + preview[cut:]
	}
	args["content"] = preview
	encoded, err := json.Marshal(args)
	if err != nil {
		return
	}
	m["args"] = string(encoded)
	m["createPreviewStartLine"], m["createPreviewTrimmed"] = start, true
}

// Unwrapping merges envelope metadata into data. A projection must detach that
// map first: a backend event may still own the original evidence.
func detachToolResultEnvelope(m map[string]any) {
	if raw, ok := m["result"].(map[string]any); ok {
		if data, ok := raw["data"].(map[string]any); ok {
			copy := maps.Clone(raw)
			copy["data"] = maps.Clone(data)
			m["result"] = copy
		}
	}
}

// The read/search/list group only displays this metadata. Share its projection
// with disk snapshots, keeping live UI errors complete and explicit compaction
// excerpts intact. The backend's model history is outside this UI copy.
func retainReadDisplaySummary(m map[string]any) {
	if !isFoldableReadTool(m) || boolOf(m, "fastCompacted") || str(m, "mcpServer") != "" || str(m, "mcpTool") != "" {
		return
	}
	if raw, exists := m["result"]; exists {
		// Structured backend payloads are decoded by dispatchEvent before the
		// ingestion fallback projects them. Do not guess at unknown wire types.
		switch raw.(type) {
		case string, map[string]any, nil:
		default:
			return
		}
	} else if str(m, "body") == "" {
		return // an argument/status update must not invent an empty result
	}
	detachToolResultEnvelope(m)
	m["result"] = readCardSummary(readGrepGroupName(m), toolResult(m), false)
	delete(m, "body")
	delete(m, "content")
}

func readCardSummary(name string, result map[string]any, forDisk bool) map[string]any {
	summary := readCardMetadata(result, forDisk)
	switch name {
	case "read", "remote_read", "batch_read":
		for _, key := range []string{"files", "missing"} {
			if files := list(result, key); len(files) > 0 {
				kept := make([]any, 0, len(files))
				for _, raw := range files {
					if file, ok := raw.(map[string]any); ok {
						kept = append(kept, readCardMetadata(file, forDisk))
					}
				}
				summary[key] = kept
			}
		}
	case "grep":
		if hits := toolGroupGrepHits(result); hits >= 0 {
			summary["hits"] = hits
		}
	case "list", "glob":
		if _, exists := summary["count"]; !exists {
			count := len(list(result, "entries"))
			if count == 0 {
				count = len(list(result, "matches"))
			}
			summary["count"] = count
		}
	}
	return summary
}

func readCardMetadata(result map[string]any, forDisk bool) map[string]any {
	kept := make(map[string]any)
	for _, key := range []string{"ok", "cancelled", "errorCode", "path", "startLine", "endLine", "totalLines", "emptyRange", "count", "hits", "truncated", "durationMs"} {
		switch value := result[key].(type) {
		case string, bool, int, int64, float32, float64:
			kept[key] = value
		}
	}
	if text := str(result, "error"); text != "" {
		if forDisk {
			text = short(text, 300)
		}
		kept["error"] = text
	}
	return kept
}
