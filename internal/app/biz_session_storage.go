// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"maps"

	openai "github.com/sashabaranov/go-openai"
)

// Only the disk copy is changed. Snapshot saves can happen while a main task
// runs; its native calls/reports remain untouched, as does the live UI evidence.
func (a *App) sessionSnapshotForStorage(snapshot SessionSnapshot) SessionSnapshot {
	history := a.loadSessionHistoryCopy(snapshot.ID)
	if len(history) == 0 {
		return snapshot
	}
	candidate, err := a.projectFastCompactHistory(context.Background(), snapshot.ID, history)
	if err != nil {
		return snapshot
	}
	edits := map[string]map[int]fastCompactEntry{}
	occurrences := map[string]int{}
	for i := len(candidate.history) - 1; i >= 0; i-- {
		m := candidate.history[i]
		if m.Role == openai.ChatMessageRoleTool {
			occurrences[m.ToolCallID]++
		}
		if entries, ok := decodeFastCompactHistory(m); ok {
			for j := len(entries) - 1; j >= 0; j-- {
				entry := entries[j]
				if entry.Role != "tool_call" {
					continue
				}
				occurrences[entry.ToolCallID]++
				if edits[entry.ToolCallID] == nil {
					edits[entry.ToolCallID] = map[int]fastCompactEntry{}
				}
				edits[entry.ToolCallID][occurrences[entry.ToolCallID]] = entry
			}
		}
	}
	if len(edits) == 0 {
		return snapshot
	}
	rows := append([]map[string]any(nil), snapshot.Messages...)
	clear(occurrences)
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row["role"] != "tool_call" {
			continue
		}
		id, _ := row["toolCallId"].(string)
		occurrences[id]++
		entry, ok := edits[id][occurrences[id]]
		if !ok {
			continue
		}
		copy := maps.Clone(row)
		delete(copy, "args")
		delete(copy, "content")
		copy["body"], copy["purpose"], copy["fastCompacted"] = entry.Content, entry.Purpose, true
		result := map[string]any{"_text": entry.Content}
		copy["result"] = result
		// Keep scalar execution outcomes even for legacy records without a
		// producer status. No result body or nested payload is copied.
		if outcome, ok := row["result"].(map[string]any); ok {
			for _, key := range []string{"ok", "exitCode", "timedOut", "cancelled", "errorCode"} {
				switch value := outcome[key].(type) {
				case string, bool, float64, int, int64:
					result[key] = value
				}
			}
		}
		if entry.Status != "" && entry.Status != "unknown" {
			copy["status"] = entry.Status
		}
		rows[i] = copy
	}
	snapshot.Messages = rows
	return snapshot
}
