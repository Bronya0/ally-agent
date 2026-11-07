// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	toolshared "ally-dev/internal/tools/shared"
	"maps"
)

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
	if raw, ok := m["result"].(map[string]any); ok {
		if data, ok := raw["data"].(map[string]any); ok {
			copy := maps.Clone(raw)
			copy["data"] = maps.Clone(data)
			m["result"] = copy
		}
	}
	if name == "command" || name == "remote_run_command" {
		retainCommandSummary(m)
		if name == "remote_run_command" {
			m["result"].(map[string]any)["resultOmitted"] = true
		}
		delete(m, "content")
		return true
	}
	result := toolResult(m)
	summary := snapshotReadMetadata(result)
	switch name {
	case "read", "remote_read", "batch_read":
		for _, key := range []string{"files", "missing"} {
			if files := list(result, key); len(files) > 0 {
				kept := make([]any, 0, len(files))
				for _, raw := range files {
					if file, ok := raw.(map[string]any); ok {
						kept = append(kept, snapshotReadMetadata(file))
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
	summary["resultOmitted"] = true
	m["result"] = summary
	delete(m, "body")
	delete(m, "content")
	if text := str(m, "error"); text != "" {
		m["error"] = short(text, 300)
	}
	return true
}

func snapshotReadMetadata(result map[string]any) map[string]any {
	kept := make(map[string]any)
	for _, key := range []string{"ok", "cancelled", "errorCode", "path", "startLine", "endLine", "totalLines", "emptyRange", "count", "hits", "truncated", "durationMs"} {
		switch value := result[key].(type) {
		case string, bool, int, int64, float32, float64:
			kept[key] = value
		}
	}
	if text := str(result, "error"); text != "" {
		kept["error"] = short(text, 300)
	}
	return kept
}
