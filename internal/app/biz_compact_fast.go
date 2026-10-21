// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

const defaultFastCompactKeepRecentRounds = 4
const fastCompactExcerptRunes = 384
const fastCompactToolMarker = "[Fast-compacted tool result; partial excerpt, not the full output. Do not repeat mutations.]\n"
const fastCompactReasoningMarker = "[Earlier reasoning excerpt; incomplete]\n"

// fastCompactCutoff retains complete assistant/tool batches, even when one batch
// has parallel calls. Counting individual results could split a signed turn.
func fastCompactCutoff(history []openai.ChatCompletionMessage, keep int) int {
	if keep <= 0 {
		return 0
	}
	seen := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == openai.ChatMessageRoleAssistant && len(history[i].ToolCalls) > 0 {
			seen++
			if seen == keep {
				return i
			}
		}
	}
	return 0
}

// An excerpt is deterministic and labelled as incomplete; it is not an LLM
// summary and must not claim that omitted evidence was checked or succeeded.
func fastCompactExcerpt(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= fastCompactExcerptRunes {
		return string(runes)
	}
	return string(runes[:fastCompactExcerptRunes*2/3]) + "\n[… omitted …]\n" + string(runes[len(runes)-fastCompactExcerptRunes/3:])
}

func (a *App) fastCompactHistory(ctx context.Context, sessionID string, history []openai.ChatCompletionMessage, tokensBefore int) (map[string]any, error) {
	cutoff := fastCompactCutoff(history, defaultFastCompactKeepRecentRounds)
	out := cloneChatMessages(history)
	tools := map[string]string{}
	reasoningCount := 0
	// Keep the latest structured plan even if it lies before the retained tail.
	latestPlanID := ""
	for _, m := range history {
		for _, call := range m.ToolCalls {
			if call.Function.Name == planToolName {
				latestPlanID = call.ID
			}
		}
	}
	for i := 0; i < cutoff; i++ {
		m := &out[i]
		if m.Role == openai.ChatMessageRoleTool && m.ToolCallID != latestPlanID && m.Content != toolResultPlaceholder && !strings.HasPrefix(m.Content, fastCompactToolMarker) {
			short := fastCompactToolMarker + fastCompactExcerpt(m.Content)
			// Never grow an already small result, including UTF-8-heavy output.
			if estimateTokensFromText(short) < estimateTokensFromText(m.Content) {
				m.Content = short
				tools[m.ToolCallID] = short
			}
		}
		if m.Role == openai.ChatMessageRoleAssistant && m.ReasoningContent != "" {
			// Signed thinking may never be edited and replayed as if it were the
			// original. Move the local excerpt into ordinary assistant text and
			// discard this old turn's protocol artifacts below.
			excerpt := fastCompactReasoningMarker + fastCompactExcerpt(m.ReasoningContent)
			content := strings.TrimSpace(m.Content + "\n\n" + excerpt)
			if estimateTokensFromText(content) < estimateTokensFromText(m.Content)+reasoningTokens(*m) {
				m.Content = content
				m.ReasoningContent = ""
				reasoningCount++
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	replayChanged := a.reasoningStash.retainFastCompactTail(sessionID, history, out, cutoff)
	changed := len(tools) > 0 || reasoningCount > 0 || replayChanged
	if changed {
		// Message count is unchanged, so the measurement anchor cannot detect
		// this rewrite itself. Read-cache hits must also stop hiding old files.
		a.clearContextAnchor(sessionID)
		a.invalidateSessionReadCache(sessionID)
		a.saveHistory(sessionID, out)
	}
	tokensAfter := tokensBefore
	if changed {
		tokensAfter = a.getContextBreakdown(sessionID, "").Total
	}
	return map[string]any{
		"tier": "fast", "changed": changed,
		"keepRecent":    defaultFastCompactKeepRecentRounds,
		"toolSummaries": tools, "reasoningCount": reasoningCount,
		"tokensBefore": tokensBefore, "tokensAfter": tokensAfter,
	}, nil
}
