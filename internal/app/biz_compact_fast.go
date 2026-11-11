// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

const defaultFastCompactKeepRecentRounds = 4
const fastCompactExcerptRunes = 384
const fastCompactAssistantTokenBudget = 8192
const fastCompactToolMarker = "[Fast-compacted tool result; partial excerpt, not the full output. Do not repeat mutations.]\n"
const fastCompactReasoningMarker = "[Earlier reasoning excerpt; incomplete]\n"
const fastCompactAssistantMarker = "[Earlier assistant reply; partial excerpt]\n"
const compactImagePlaceholder = "[Earlier image omitted during compaction]"

type fastCompactCandidate struct {
	history       []openai.ChatCompletionMessage
	cutoff        int
	messageChange bool
	result        map[string]any
}

type fastCompactAssistantEdit struct {
	Before  string `json:"before"`
	After   string `json:"after"`
	FromEnd int    `json:"fromEnd"`
}

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
	// A conversation without enough tool batches still accumulates long answers.
	// Fall back to its latest complete, non-tool assistant replies.
	seen = 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == openai.ChatMessageRoleAssistant && len(history[i].ToolCalls) == 0 && strings.TrimSpace(history[i].Content) != "" {
			seen++
			if seen == keep {
				return i
			}
		}
	}
	return 0
}

// Earlier fast compactions stored reasoning excerpts inside ordinary text;
// those suffixes were never shown as answer text in the UI.
func fastCompactAnswerText(content string) string {
	if before, _, found := strings.Cut(content, "\n\n"+fastCompactReasoningMarker); found {
		return before
	}
	if strings.HasPrefix(content, fastCompactReasoningMarker) {
		return ""
	}
	return content
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

// compactOldImages replaces a percentage of the oldest image blocks with text
// placeholders, preserving message order and surrounding text.
func compactOldImages(messages []openai.ChatCompletionMessage, percent int) int {
	imageCount := 0
	for i := range messages {
		for _, part := range messages[i].MultiContent {
			if part.Type == openai.ChatMessagePartTypeImageURL {
				imageCount++
			}
		}
	}
	if imageCount == 0 || percent <= 0 {
		return 0
	}
	if percent > 100 {
		percent = 100
	}
	toReplace := imageCount * percent / 100
	replaced := 0
	for i := range messages {
		parts := messages[i].MultiContent
		if len(parts) == 0 || replaced >= toReplace {
			continue
		}
		out := make([]openai.ChatMessagePart, 0, len(parts))
		for _, part := range parts {
			if part.Type == openai.ChatMessagePartTypeImageURL && replaced < toReplace {
				part = openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: compactImagePlaceholder}
				replaced++
			}
			out = append(out, part)
		}
		messages[i].MultiContent = out
	}
	return replaced
}

func (a *App) prepareFastCompactHistory(ctx context.Context, sessionID string, history []openai.ChatCompletionMessage, tokensBefore int) (*fastCompactCandidate, error) {
	cutoff := fastCompactCutoff(history, defaultFastCompactKeepRecentRounds)
	out := cloneChatMessages(history)
	imageCount := compactOldImages(out, 80)
	tools := map[string]string{}
	reasoningCount := 0
	toolArgumentsCount := 0
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
		// Keep call/result pairing, but discard old input bodies (including code).
		// An empty JSON object remains valid on every tool-call wire protocol.
		for j := range m.ToolCalls {
			if m.ToolCalls[j].Function.Arguments != "{}" {
				m.ToolCalls[j].Function.Arguments = "{}"
				toolArgumentsCount++
			}
		}
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
		if m.Role == openai.ChatMessageRoleAssistant && m.Content != "" && !strings.HasPrefix(m.Content, fastCompactAssistantMarker) {
			short := fastCompactAssistantMarker + fastCompactExcerpt(m.Content)
			if estimateTokensFromText(short) < estimateTokensFromText(m.Content) {
				m.Content = short
			}
		}
	}
	// Per-message excerpts alone still grow with conversation length. Keep the
	// newest old answer text within one budget; empty bodies are removed by the
	// ordinary history sanitizer, while tool calls keep their identity/order.
	remaining := fastCompactAssistantTokenBudget
	for i := cutoff - 1; i >= 0; i-- {
		m := &out[i]
		if m.Role != openai.ChatMessageRoleAssistant {
			continue
		}
		cost := estimateTokensFromText(m.Content)
		if cost > remaining {
			m.Content = ""
			m.ReasoningContent = ""
		} else {
			remaining -= cost
		}
	}
	assistantCount := 0
	assistantEdits := []fastCompactAssistantEdit{}
	occurrences := map[string]int{}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != openai.ChatMessageRoleAssistant {
			continue
		}
		before := fastCompactAnswerText(history[i].Content)
		if before != "" {
			occurrences[before]++
		}
		if i < cutoff && history[i].Content != out[i].Content {
			assistantCount++
			if before != "" {
				assistantEdits = append(assistantEdits, fastCompactAssistantEdit{Before: before, After: out[i].Content, FromEnd: occurrences[before]})
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	messageChange := len(tools) > 0 || reasoningCount > 0 || assistantCount > 0 || toolArgumentsCount > 0 || imageCount > 0
	tokensAfter := tokensBefore
	if messageChange {
		// Compare like with like: the provider's measured count cannot be
		// compared with a local estimate after a history rewrite.
		prefix := a.getContextBreakdown(sessionID, "")
		before := computeLiveBreakdown(history)
		after := computeLiveBreakdown(out)
		tokensBefore = prefix.SystemPrompt + prefix.ToolSchemas + before.Total
		tokensAfter = prefix.SystemPrompt + prefix.ToolSchemas + after.Total
	}
	return &fastCompactCandidate{
		history:       out,
		cutoff:        cutoff,
		messageChange: messageChange,
		result: map[string]any{
			"tier": "fast", "changed": messageChange,
			"keepRecent":         defaultFastCompactKeepRecentRounds,
			"toolSummaries":      tools,
			"reasoningCount":     reasoningCount,
			"toolArgumentsCount": toolArgumentsCount,
			"imagesCompacted":    imageCount,
			"assistantEdits":     assistantEdits,
			"assistantCount":     assistantCount,
			"tokensBefore":       tokensBefore,
			"tokensAfter":        tokensAfter,
		},
	}, nil
}

// commitFastCompactHistory applies a candidate only after its caller accepts
// the rewrite. Automatic compaction can discard an insufficient candidate and
// summarize the untouched history instead.
func (a *App) commitFastCompactHistory(sessionID string, history []openai.ChatCompletionMessage, candidate *fastCompactCandidate) map[string]any {
	replayChanged := a.reasoningStash.retainFastCompactTail(sessionID, history, candidate.history, candidate.cutoff)
	changed := candidate.messageChange || replayChanged
	result := candidate.result
	result["changed"] = changed
	if !changed {
		return result
	}
	// Rewrites must invalidate measurement anchors even when the message
	// count stays unchanged. Read-cache hits must stop hiding old files too.
	a.clearContextAnchor(sessionID)
	a.invalidateSessionReadCache(sessionID)
	a.saveHistory(sessionID, candidate.history)
	// Save recalculates the same live message categories for a running session.
	after := computeLiveBreakdown(candidate.history)
	a.mu.Lock()
	if _, live := a.liveBreakdown[sessionID]; live {
		a.liveBreakdown[sessionID] = after
	}
	a.mu.Unlock()

	// Refresh comparable totals after the measurement anchor has been dropped.
	prefix := a.getContextBreakdown(sessionID, "")
	before := computeLiveBreakdown(history)
	result["tokensBefore"] = prefix.SystemPrompt + prefix.ToolSchemas + before.Total
	result["tokensAfter"] = prefix.SystemPrompt + prefix.ToolSchemas + after.Total
	return result
}

func (a *App) fastCompactHistory(ctx context.Context, sessionID string, history []openai.ChatCompletionMessage, tokensBefore int) (map[string]any, error) {
	candidate, err := a.prepareFastCompactHistory(ctx, sessionID, history, tokensBefore)
	if err != nil {
		return nil, err
	}
	return a.commitFastCompactHistory(sessionID, history, candidate), nil
}

func fastCompactLeavesLessThanHalf(tokensBefore, tokensAfter int) bool {
	return tokensBefore > 0 && tokensAfter >= 0 && int64(tokensAfter)*2 < int64(tokensBefore)
}
