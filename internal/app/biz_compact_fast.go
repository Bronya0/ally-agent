// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// These legacy markers are still recognized when loading older histories.
const fastCompactToolMarker = "[Fast-compacted tool result; partial excerpt, not the full output. Do not repeat mutations.]\n"
const fastCompactReasoningMarker = "[Earlier reasoning excerpt; incomplete]\n"
const fastCompactAssistantMarker = "[Earlier assistant reply; partial excerpt]\n"
const fastCompactHistoryMarker = "[历史对话（已压缩）]\n"
const fastCompactRecordMarker = "[工具执行记录（已压缩）]\n"

// The record is ordinary user-role reference text on the wire. Keeping its
// original speakers in JSON also lets snapshot recovery rebuild real UI rows,
// without displaying an application-generated record as a new user question.
type fastCompactEntry struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	Status     string `json:"status,omitempty"`
}

type fastCompactCandidate struct {
	history       []openai.ChatCompletionMessage
	cutoff        int
	tailStart     int
	messageChange bool
	result        map[string]any
}

func isFastCompactHistory(m openai.ChatCompletionMessage) bool {
	return m.Role == openai.ChatMessageRoleUser && strings.HasPrefix(m.Content, fastCompactHistoryMarker)
}

func decodeFastCompactHistory(m openai.ChatCompletionMessage) ([]fastCompactEntry, bool) {
	if !isFastCompactHistory(m) {
		return nil, false
	}
	var entries []fastCompactEntry
	if json.Unmarshal([]byte(strings.TrimPrefix(m.Content, fastCompactHistoryMarker)), &entries) != nil {
		return nil, false
	}
	return entries, true
}

func encodeFastCompactHistory(entries []fastCompactEntry) openai.ChatCompletionMessage {
	data, _ := json.Marshal(entries) // Entries contain only strings.
	return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: fastCompactHistoryMarker + string(data)}
}

// User deletion addresses the visible transcript, including original questions
// inside a compacted reference record. Never match assistant/tool text there as
// if it were the user's question, or count the wrapper as another user input.
func locateHistoryUser(messages []openai.ChatCompletionMessage, ordinal int, expected string) (messageIndex, entryIndex int) {
	messageIndex, entryIndex = -1, -1
	seen := 0
	match := func(content string) bool {
		if expected != "" {
			return strings.Contains(strings.TrimSpace(content), expected)
		}
		return seen == ordinal
	}
	for i, m := range messages {
		if entries, ok := decodeFastCompactHistory(m); ok {
			for j, entry := range entries {
				if entry.Role != openai.ChatMessageRoleUser {
					continue
				}
				if match(entry.Content) {
					messageIndex, entryIndex = i, j
					if expected == "" {
						return
					}
				}
				seen++
			}
		} else if m.Role == openai.ChatMessageRoleUser {
			if match(textFromMessage(m)) {
				messageIndex, entryIndex = i, -1
				if expected == "" {
					return
				}
			}
			seen++
		}
	}
	return
}

func fastCompactAnswerText(content string) string {
	if before, _, found := strings.Cut(content, "\n\n"+fastCompactReasoningMarker); found {
		return before
	}
	if strings.HasPrefix(content, fastCompactReasoningMarker) {
		return ""
	}
	return content
}

func (a *App) prepareFastCompactHistory(ctx context.Context, sessionID string, history []openai.ChatCompletionMessage, tokensBefore int) (*fastCompactCandidate, error) {
	candidate, err := a.projectFastCompactHistory(ctx, sessionID, history)
	if err != nil {
		return nil, err
	}
	before, after := computeLiveBreakdown(history), computeLiveBreakdown(candidate.history)
	if after.Total >= before.Total && !a.reasoningStash.hasOldCompactReasoning(sessionID, history, candidate.cutoff) {
		candidate.history = cloneChatMessages(history)
		candidate.tailStart = candidate.cutoff
		candidate.messageChange = false
		candidate.result["changed"] = false
		candidate.result["toolSummaries"] = map[string]any{}
		candidate.result["toolRecords"] = []any{}
		after = before
	}
	prefix := a.getContextBreakdown(sessionID, "")
	candidate.result["tokensBefore"] = prefix.SystemPrompt + prefix.ToolSchemas + before.Total
	candidate.result["tokensAfter"] = prefix.SystemPrompt + prefix.ToolSchemas + after.Total
	return candidate, nil
}

// Pure projection shared by explicit compaction and persistence. It does not
// publish history, clear replay/read caches, or refresh session prompt snapshots.
func (a *App) projectFastCompactHistory(ctx context.Context, sessionID string, history []openai.ChatCompletionMessage) (*fastCompactCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cutoff := inferHistoryTaskStart(history)
	if boundary := a.historyTaskFor(sessionID, history); boundary != nil {
		cutoff = boundary.Start
	}
	metadata := a.reasoningStash.toolHistoryMetadata(openAIResponsesPromptCacheKey(sessionID))
	statuses := a.reasoningStash.toolStatuses(openAIResponsesPromptCacheKey(sessionID))
	tools := map[string]any{}
	toolEntries := map[int]fastCompactEntry{}
	calls := map[string][]openai.ToolCall{}
	reasoningCount, assistantCount, argumentCount := 0, 0, 0
	latestPlanID := ""
	for _, m := range history {
		for _, call := range m.ToolCalls {
			if call.Function.Name == planToolName {
				latestPlanID = call.ID
			}
		}
	}
	out := make([]openai.ChatCompletionMessage, 0, len(history))
	var entries []fastCompactEntry
	flush := func() {
		if len(entries) == 0 {
			return
		}
		out = append(out, encodeFastCompactHistory(entries))
		entries = nil
	}
	for i, m := range history[:cutoff] {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if isFastCompactHistory(m) {
			flush()
			out = append(out, m)
			continue
		}
		switch m.Role {
		case openai.ChatMessageRoleUser, openai.ChatMessageRoleAssistant:
			content := messageText(m)
			if m.Role == openai.ChatMessageRoleAssistant {
				content = fastCompactAnswerText(content)
				assistantCount++
				if m.ReasoningContent != "" {
					reasoningCount++
				}
			}
			if content != "" {
				entries = append(entries, fastCompactEntry{Role: m.Role, Content: content})
			}
			for _, call := range m.ToolCalls {
				calls[call.ID] = append(calls[call.ID], call)
				argumentCount++
			}
		case openai.ChatMessageRoleTool:
			pending := calls[m.ToolCallID]
			if len(pending) == 0 {
				continue
			} // Normal hygiene owns malformed/orphaned tool messages.
			call := pending[0]
			calls[m.ToolCallID] = pending[1:]
			if len(pending) == 1 {
				delete(calls, m.ToolCallID)
			}
			meta := metadata[toolStatusIdentity(m)]
			if meta.Purpose == "" {
				meta.Purpose = toolHistoryPurpose(call, "")
			}
			status := "unknown"
			if failed, known := statuses[toolStatusIdentity(m)]; known {
				status = "success"
				if failed {
					status = "error"
				}
			}
			if meta.Status != "" {
				status = meta.Status
			}
			if meta.Headers == "" {
				// Legacy records have no producer provenance. Keep a known formatted
				// first header only; never scan a body for more header-looking lines.
				header := firstToolHistoryLine(m.Content)
				if strings.HasPrefix(header, "// "+normalizeToolName(call.Function.Name)+" |") {
					meta.Headers = boundedToolHistoryHeaders([]string{header})
				}
			}
			if meta.Headers == "" {
				meta.Headers = fmt.Sprintf("// %s | status=%s", normalizeToolName(call.Function.Name), status)
			}
			content := fastCompactRecordMarker
			if meta.Purpose != "" {
				content += "目的：" + meta.Purpose + "\n"
			}
			content += meta.Headers
			if m.ToolCallID == latestPlanID && m.Content != toolResultPlaceholder {
				content += "\n\n" + m.Content // Latest structured plan is durable task state.
			}
			entry := fastCompactEntry{Role: "tool_call", Content: content, Name: call.Function.Name, ToolCallID: call.ID, Purpose: meta.Purpose, Status: status}
			entries = append(entries, entry)
			toolEntries[i] = entry
			tools[call.ID] = content
		}
	}
	flush()
	tailStart := len(out)
	out = append(out, cloneChatMessages(history[cutoff:])...)
	changed := cutoff > 0 && (assistantCount > 0 || argumentCount > 0)
	// A user-only prefix has no tool/prompt state to compact. Preserve it verbatim.
	if !changed {
		out = cloneChatMessages(history)
		tailStart = cutoff
	}
	// Match UI cards by occurrence as well as ID. Legacy providers could reuse
	// an ID, including in the retained task, so an ID-only edit can hit it too.
	toolRecords := []any{}
	occurrences := map[string]int{}
	if changed {
		for i := len(history) - 1; i >= 0; i-- {
			m := history[i]
			if m.Role != openai.ChatMessageRoleTool {
				continue
			}
			occurrences[m.ToolCallID]++
			if entry, ok := toolEntries[i]; ok {
				toolRecords = append(toolRecords, map[string]any{"id": m.ToolCallID, "fromEnd": occurrences[m.ToolCallID], "content": entry.Content, "purpose": entry.Purpose})
			}
		}
	}
	return &fastCompactCandidate{history: out, cutoff: cutoff, tailStart: tailStart, messageChange: changed, result: map[string]any{
		"tier": "fast", "changed": changed, "keepRecent": 1,
		"toolSummaries": tools, "toolRecords": toolRecords, "reasoningCount": reasoningCount,
		"toolArgumentsCount": argumentCount, "assistantCount": assistantCount,
	}}, ctx.Err()
}

// commitFastCompactHistory applies a candidate only after its caller accepts
// the rewrite. Automatic compaction can discard an insufficient candidate and
// summarize the untouched history instead.
func (a *App) commitFastCompactHistory(sessionID string, history []openai.ChatCompletionMessage, candidate *fastCompactCandidate) map[string]any {
	if !candidate.messageChange {
		return candidate.result
	}
	a.reasoningStash.retainFastCompactTail(sessionID, history, candidate.history, candidate.cutoff)
	result := candidate.result
	// Rewrites must invalidate measurement anchors even when the message
	// count stays unchanged. Read-cache hits must stop hiding old files too.
	a.clearContextAnchor(sessionID)
	a.invalidateSessionReadCache(sessionID)
	if candidate.tailStart < len(candidate.history) {
		m := candidate.history[candidate.tailStart]
		a.mu.Lock()
		a.historyCacheStateLocked(sessionID).task = &historyTaskBoundary{Start: candidate.tailStart, Key: historyTaskKey(m)}
		a.mu.Unlock()
	}
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
