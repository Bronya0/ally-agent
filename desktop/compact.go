// SPDX-License-Identifier: GPL-3.0-only
package desktop

// This file owns everything a compaction does to the screen: the live summary
// preview, the transcript replacement that the backend's history rewrite
// implies, and the local-trim edits. Both hosts get the same three things from
// the backend (compact:start / compact:delta / compact:done, then the result),
// so every path funnels through the helpers here — the transcript can never be
// left showing a conversation the next request no longer contains.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	// compactPreviewFlag marks the assistant row that streams the summary. It is
	// UI-only state (never persisted to the model history) and it is what tells a
	// preview apart from a real answer when the compaction ends.
	compactPreviewFlag = "compactPreview"
	// fastCompactChip labels a tool card whose result was replaced by an excerpt.
	fastCompactChip = "已快速压缩" // fastCompactReasoningMarker is the backend's suffix for an old turn whose
	// thinking was folded into its answer text; it is never shown as answer text.
	fastCompactReasoningMarker = "[Earlier reasoning excerpt; incomplete]\n"
	// transcriptSummaryFlag marks the single row a full-summary compaction left in
	// place of the conversation, so the next stream delta does not grow into it.
	transcriptSummaryFlag = "transcriptSummary"
)

// compactStartNote reports the scale of the summary request up front, so the
// user knows how much history is being rewritten while it runs.
func compactStartNote(p map[string]any) string {
	if messages := int(num(p, "messages")); messages > 0 {
		return fmt.Sprintf("正在压缩对话上下文（%d 条消息，约 %s tokens）…", messages, fmtK(int(num(p, "tokensBefore"))))
	}
	return "正在压缩对话上下文…"
}

// compactPreviewRow returns the row currently streaming the summary, if any.
func compactPreviewRow(t *conversation) map[string]any {
	for i := len(t.Messages) - 1; i >= 0; i-- {
		if boolOf(t.Messages[i], compactPreviewFlag) {
			return t.Messages[i]
		}
	}
	return nil
}

// dropCompactPreview removes a half-written summary. A failed or cancelled
// compaction must not leave its preview behind as if it had been applied.
func dropCompactPreview(t *conversation) {
	kept := t.Messages[:0]
	for _, m := range t.Messages {
		if boolOf(m, compactPreviewFlag) {
			continue
		}
		kept = append(kept, m)
	}
	t.Messages = kept
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
		row := compactPreviewRow(t)
		if row == nil {
			row = map[string]any{"role": "assistant", "content": "", compactPreviewFlag: true}
			t.Messages = append(t.Messages, row)
		}
		row["content"] = str(row, "content") + content
		st.CompactThinking = 0
	}
	if st.Follow {
		st.FollowPending = true
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
	t.Messages = []map[string]any{{"role": "assistant", "content": summary, transcriptSummaryFlag: true}}
	t.MD = nil
	st := t.uiState()
	st.Round = map[int]*roundStat{}
	st.RoundAt = map[int]int{}
	st.PromptOpen = map[int]bool{}
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
	keep := int(num(result, "keepRecent"))
	if keep <= 0 {
		keep = 4
	}
	tools := 0
	if summaries, ok := result["toolSummaries"].(map[string]any); ok {
		tools = len(summaries)
	}
	return fmt.Sprintf("已裁剪 %d 条旧回复、%d 条旧工具结果和 %d 条旧思考内容。保留最近 %d 轮工具调用及后续消息；不足时保留最近 %d 条回复。旧回复只留首尾摘录，总计最多约 8k tokens，超额清除更早正文。旧工具调用参数已清空。用户消息与最新计划保留，不调用模型。",
		int(num(result, "assistantCount")), tools, int(num(result, "reasoningCount")), keep, keep)
}

// fastCompactNoop is the "nothing to trim" report of a manual local trim.
func fastCompactNoop(result map[string]any) string {
	keep := int(num(result, "keepRecent"))
	if keep <= 0 {
		keep = 4
	}
	return fmt.Sprintf("没有可快速压缩的旧内容（保留最近 %d 轮工具调用或回复）", keep)
}

// compactToastText is the one compaction report both the manual and the
// automatic path show, so the two never disagree about what just happened.
func compactToastText(before, after int) string {
	return fmt.Sprintf("上下文已压缩：%s → %s tokens", fmtK(before), fmtK(after))
}

// applyFastCompaction mirrors the local trim the backend just wrote to the
// history. Tool cards whose result was replaced show the excerpt, and shortened
// answers keep their position with the shorter text. Answers are matched by
// exact original text plus its occurrence counted from the end, because a UI row
// carries no call id and repeated answers are indistinguishable by text alone.
func applyFastCompaction(t *conversation, result map[string]any) {
	releaseCommandDetails(t)
	edits := map[string]map[int]string{}
	if raw, ok := result["assistantEdits"].([]any); ok {
		for _, item := range raw {
			edit, ok := item.(map[string]any)
			if !ok {
				continue
			}
			before := str(edit, "before")
			if before == "" {
				continue
			}
			if edits[before] == nil {
				edits[before] = map[int]string{}
			}
			edits[before][int(num(edit, "fromEnd"))] = str(edit, "after")
		}
	}
	occurrences := map[string]int{}
	shortened := map[int]string{}
	for i := len(t.Messages) - 1; i >= 0; i-- {
		m := t.Messages[i]
		if str(m, "role") != "assistant" || m["error"] != nil || m["welcome"] != nil || boolOf(m, "transientTurn") {
			continue
		}
		content := str(m, "content")
		before := content
		if strings.HasPrefix(content, fastCompactReasoningMarker) {
			before = ""
		} else if cut, _, found := strings.Cut(content, "\n\n"+fastCompactReasoningMarker); found {
			before = cut
		}
		matches := edits[before]
		if matches == nil {
			continue
		}
		occurrences[before]++
		if after, ok := matches[occurrences[before]]; ok {
			shortened[i] = after
		}
	}
	for i, after := range shortened {
		t.Messages[i]["content"] = after
		t.Messages[i]["fastCompacted"] = true
	}
	summaries, _ := result["toolSummaries"].(map[string]any)
	if summaries == nil {
		return
	}
	for i, m := range t.Messages {
		if str(m, "role") != "tool_call" {
			continue
		}
		id := str(m, "toolCallId")
		if id == "" {
			continue
		}
		summary, ok := summaries[id].(string)
		if !ok || summary == "" {
			continue
		}
		// The original arguments and result are gone from the history, so the
		// card keeps only its identity plus the excerpt it was replaced with.
		row := map[string]any{
			"role": "tool_call", "name": str(m, "name"), "toolCallId": id,
			"title": str(m, "title"), "status": "success",
			"body": summary, "result": summary, "fastCompacted": true,
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
}

// Local rewrites invalidate provider usage; both numbers are text estimates.
func compactionToast(result map[string]any) string {
	text := compactToastText(int(num(result, "tokensBefore")), int(num(result, "tokensAfter")))
	if str(result, "tier") == "fast" {
		text += "（本地估算）"
	}
	return text
}
