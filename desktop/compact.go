// SPDX-License-Identifier: GPL-3.0-only
package desktop

// This file owns everything a compaction does to the screen: the live summary
// preview, the transcript replacement that the backend's history rewrite
// implies, and the local-trim edits. Both hosts get the same three things from
// the backend (compact:start / compact:delta / compact:done, then the result),
// so every path funnels through the helpers here — the transcript can never be
// left showing a conversation the next request no longer contains.

import (
	"ally-dev/locales"
	"fmt"
	"maps"
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	// compactPreviewFlag marks the assistant row that streams the summary. It is
	// UI-only state (never persisted to the model history) and it is what tells a
	// preview apart from a real answer when the compaction ends.
	compactPreviewFlag = "compactPreview"
	// fastCompactChip labels a tool card reduced to purpose/result metadata.
	fastCompactChip = "已快速压缩"
	// transcriptSummaryFlag marks the single row a full-summary compaction left in
	// place of the conversation, so the next stream delta does not grow into it.
	transcriptSummaryFlag = "transcriptSummary"
)

// compactStartNote reports the scale of the summary request up front, so the
// user knows how much history is being rewritten while it runs.
func compactStartNote(p map[string]any) string {
	if messages := int(num(p, "messages")); messages > 0 {
		return fmt.Sprintf(locales.Text("正在压缩对话上下文（%d 条消息，约 %s tokens）…"), messages, fmtK(int(num(p, "tokensBefore"))))
	}
	return locales.Text("正在压缩对话上下文…")
}

// compactPreviewIndex returns the row currently streaming the summary, if any.
func compactPreviewIndex(t *conversation) int {
	for i := len(t.Messages) - 1; i >= 0; i-- {
		if boolOf(t.Messages[i], compactPreviewFlag) {
			return i
		}
	}
	return -1
}

// dropCompactPreview removes a half-written summary. A failed or cancelled
// compaction must not leave its preview behind as if it had been applied.
func dropCompactPreview(t *conversation) {
	invalidateChatSearch(t)
	kept := t.Messages[:0]
	for _, m := range t.Messages {
		if boolOf(m, compactPreviewFlag) {
			continue
		}
		kept = append(kept, m)
	}
	clear(t.Messages[len(kept):])
	t.Messages = kept
	t.resetTranscriptLookup()
	t.markHistory()
	t.groupChanges = nil
	releaseStaleStream(t)
}

// compactDelta appends one summary fragment to the live preview row, so the
// summary reads exactly like a normal answer while it is produced instead of a
// frozen timer. The first body fragment ends the thinking phase, as it does for
// a run.
func (a *desktop) compactDelta(t *conversation, content string, reasoningLen int) {
	st := t.uiState()
	if reasoningLen > 0 {
		st.CompactThinking += reasoningLen
	}
	if content != "" {
		index := compactPreviewIndex(t)
		if index < 0 {
			invalidateChatSearchTail(t)
			row := map[string]any{"role": "assistant", "content": "", compactPreviewFlag: true}
			t.Messages = append(t.Messages, row)
			index = len(t.Messages) - 1
		} else if index == len(t.Messages)-1 {
			invalidateChatSearchTail(t)
		} else {
			invalidateChatSearch(t)
		}
		row := t.Messages[index]
		before := str(row, "content")
		row["content"] = appendStreamChunk(st, index, before, content)
		if before == "" {
			t.markHistoryAt(index)
		}
		st.CompactThinking = 0
	}
}

// compactThinkingTokens is the estimated token count behind the thinking label,
// matching the run's own wording (4 bytes per token, rounded up).
func compactThinkingTokens(st *chatUI) int {
	if st.CompactThinking <= 0 {
		return 0
	}
	return (st.CompactThinking + 3) / 4
}

// replaceTranscript swaps the whole conversation for the summary the backend
// just wrote to the history. Every per-index view cache has to go with it: the
// old indices describe messages that no longer exist.
func replaceTranscript(t *conversation, summary string) {
	invalidateChatSearch(t)
	t.Messages = []map[string]any{{"role": "assistant", "content": summary, transcriptSummaryFlag: true}}
	t.resetTranscriptLookup()
	t.MD = nil
	t.MDCodeBlock = nil
	// The row set changed even when the old length was also 1; drop the cached
	// ownership/block-starts answer instead of reusing it.
	t.markHistory()
	st := t.uiState()
	st.Stream = nil
	t.groupChanges = nil
	st.Round = map[int]*roundStat{}
	st.RoundAt = map[int]int{}
	st.PromptOpen = map[int]bool{}
	pruneTranscriptHandles(t, 0, 0)
	st.PromptHandles = map[int]*ui.Handle{}
	st.ToolOpen = map[string]bool{}
	st.CommandDetails = nil
	st.GroupOpen = map[string]bool{}
	st.PendingPrompt = false
	followLatest(t)
}

// applyCompactionResult mirrors the backend's rewrite. A summary tier replaced
// the history with one message, so the transcript becomes that message; the fast
// tier trimmed older rows in place, so positions are preserved.
func (a *desktop) applyCompactionResult(t *conversation, result map[string]any) {
	if result == nil {
		return
	}
	if strings.EqualFold(str(result, "tier"), "fast") {
		if !boolOf(result, "changed") {
			dropCompactPreview(t)
			return
		}
		applyFastCompaction(t, result)
		invalidateChatSearch(t)
		t.Messages = append(t.Messages, map[string]any{"role": "notice", "tone": "info", "content": fastCompactNote(result)})
		dropCompactPreview(t)
		return
	}
	if summary := str(result, "summary"); summary != "" {
		replaceTranscript(t, summary)
		return
	}
	dropCompactPreview(t)
}

// fastCompactNote renders the local-trim report: what was trimmed, what was
// kept, and that no model was called.
func fastCompactNote(result map[string]any) string {
	tools := 0
	if records, ok := result["toolRecords"].([]any); ok {
		tools = len(records)
	} else if summaries, ok := result["toolSummaries"].(map[string]any); ok {
		tools = len(summaries)
	}
	return fmt.Sprintf(locales.Text("已将 %d 条旧工具记录压缩为目的与结果元信息，移除旧推理。用户消息、助手回复正文与最新计划保留；最近一轮完整任务（含子代理报告）保持不变。不调用模型。"), tools)
}

// fastCompactNoop reports that only the retained task or compacted records remain.
func fastCompactNoop(result map[string]any) string {
	return locales.Text("没有可快速压缩的旧内容（保留最近一轮完整任务）")
}

// compactToastText is the one compaction report both the manual and the
// automatic path show, so the two never disagree about what just happened.
func compactToastText(before, after int) string {
	return fmt.Sprintf(locales.Text("上下文已压缩：%s → %s tokens"), fmtK(before), fmtK(after))
}

// applyFastCompaction keeps the visible user/assistant transcript and replaces
// only old tool cards with their purpose and producer-owned result metadata.
func applyFastCompaction(t *conversation, result map[string]any) {
	defer releaseStaleStream(t)
	releaseCommandDetails(t)
	invalidateChatSearch(t)
	summaries, _ := result["toolSummaries"].(map[string]any)
	records, exact := result["toolRecords"].([]any)
	edits := map[string]map[int]map[string]any{}
	for _, raw := range records {
		if record, ok := raw.(map[string]any); ok {
			id := str(record, "id")
			if edits[id] == nil {
				edits[id] = map[int]map[string]any{}
			}
			edits[id][int(num(record, "fromEnd"))] = record
		}
	}
	if summaries == nil && !exact {
		return
	}
	occurrences := map[string]int{}
	for i := len(t.Messages) - 1; i >= 0; i-- {
		m := t.Messages[i]
		if str(m, "role") != "tool_call" {
			continue
		}
		id := str(m, "toolCallId")
		if id == "" {
			continue
		}
		occurrences[id]++
		summary, _ := summaries[id].(string)
		purpose := ""
		if exact {
			record := edits[id][occurrences[id]]
			summary, purpose = str(record, "content"), str(record, "purpose")
		}
		if summary == "" {
			continue
		}
		if !exact {
			purpose = toolPurpose(m, parseToolArgs(str(m, "args")))
		}
		// Unwrapping an envelope can merge metadata into its nested data map.
		// Detach it before reading the outcome so the original evidence is unchanged.
		source := maps.Clone(m)
		detachToolResultEnvelope(source)
		normalizeToolMessage(source)
		state := toolState(source)
		outcome := toolOutcomeSummary(toolResult(source))
		outcome["_text"] = summary
		// Drop original arguments/output, but compaction must not turn a failed
		// or cancelled operation into success.
		row := map[string]any{
			"role": "tool_call", "name": str(m, "name"), "toolCallId": id,
			"title": str(m, "title"), "status": state,
			"purpose": purpose,
			"body":    summary, "result": outcome, "fastCompacted": true,
		}
		for _, key := range []string{"error", "errorCode"} {
			if value, ok := m[key]; ok {
				row[key] = value
			}
		}
		if v := str(m, "mcpServer"); v != "" {
			row["mcpServer"] = v
		}
		if v := str(m, "mcpTool"); v != "" {
			row["mcpTool"] = v
		}
		if v, ok := m["durationMs"]; ok {
			row["durationMs"] = v
		}
		t.Messages[i] = row
	}
	// Rows were replaced in place: same positions, same group membership, new
	// bodies. The cached history blocks are rebuilt for their new shape.
	t.markHistory()
}

// Local rewrites invalidate provider usage; both numbers are text estimates.
func compactionToast(result map[string]any) string {
	text := compactToastText(int(num(result, "tokensBefore")), int(num(result, "tokensAfter")))
	if str(result, "tier") == "fast" {
		text += locales.Text("（本地估算）")
	}
	return text
}
