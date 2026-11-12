// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// Opaque/signed reasoning is not included in the plain-message token estimate.
// Removing it can be useful even when wrapping short visible replies costs a
// few extra estimated tokens. Plain text alone still must not grow on compact.
func (s *reasoningStash) hasOldCompactReasoning(sessionID string, history []openai.ChatCompletionMessage, cutoff int) bool {
	if s == nil {
		return false
	}
	keys := map[string]bool{}
	callIDs := map[string]bool{}
	ordinal := 0
	for _, m := range history[:cutoff] {
		if m.Role != openai.ChatMessageRoleAssistant {
			continue
		}
		keys[responsesMessageKey(ordinal, m)] = true
		ordinal++
		for _, call := range m.ToolCalls {
			callIDs[call.ID] = true
		}
	}
	prefix := reasoningStashScope(openAIResponsesPromptCacheKey(sessionID)) + "\x1f"
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.entries {
		if !strings.HasPrefix(key, prefix) || entry.payload == nil {
			continue
		}
		for _, turn := range entry.payload.turns {
			for _, id := range turn.callIDs {
				if callIDs[id] {
					return true
				}
			}
		}
		for messageKey, output := range entry.payload.responsesNative {
			if !keys[messageKey] {
				continue
			}
			for _, raw := range output {
				var item struct{ Type string }
				if json.Unmarshal(raw, &item) == nil && item.Type == "reasoning" {
					return true
				}
			}
		}
	}
	return false
}

// Old native items are removed as a unit with their protocol calls. Only the
// untouched task can replay signed/encrypted data. Rebase its assistant ordinal
// after the old assistants became user-role historical reference records.
func (s *reasoningStash) retainFastCompactTail(sessionID string, history, compacted []openai.ChatCompletionMessage, cutoff int) bool {
	if s == nil || cutoff <= 0 {
		return false
	}
	tailStart := len(compacted) - (len(history) - cutoff)
	if tailStart < 0 {
		return false
	}
	callIDs := map[string]bool{}
	nativeKeys := map[string]string{}
	statusesKeys := map[string]string{}
	oldOrdinal, newOrdinal := 0, 0
	for _, m := range compacted[:tailStart] {
		if m.Role == openai.ChatMessageRoleAssistant {
			newOrdinal++
		}
	}
	for i, m := range history {
		if i >= cutoff {
			next := compacted[tailStart+i-cutoff]
			if m.Role == openai.ChatMessageRoleAssistant {
				for _, call := range m.ToolCalls {
					callIDs[call.ID] = true
				}
				nativeKeys[responsesMessageKey(oldOrdinal, m)] = responsesMessageKey(newOrdinal, next)
				newOrdinal++
			}
			if m.Role == openai.ChatMessageRoleTool {
				statusesKeys[toolStatusIdentity(m)] = toolStatusIdentity(next)
			}
		}
		if m.Role == openai.ChatMessageRoleAssistant {
			oldOrdinal++
		}
	}
	prefix := reasoningStashScope(openAIResponsesPromptCacheKey(sessionID)) + "\x1f"
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for key, entry := range s.entries {
		if !strings.HasPrefix(key, prefix) || entry.payload == nil {
			continue
		}
		previous := entry.payload
		next := &sessionReasoningPayload{}
		if previous.toolResults != nil {
			next.toolResults = map[string]bool{}
			next.toolMetadata = map[string]toolHistoryMetadata{}
			for source, dest := range statusesKeys {
				if failed, ok := previous.toolResults[source]; ok {
					next.toolResults[dest] = failed
				}
				if meta, ok := previous.toolMetadata[source]; ok {
					next.toolMetadata[dest] = meta
				}
			}
			changed = changed || len(next.toolResults) != len(previous.toolResults)
		}
		for _, turn := range previous.turns {
			keep := len(turn.callIDs) > 0
			for _, id := range turn.callIDs {
				keep = keep && callIDs[id]
			}
			if keep {
				next.turns = append(next.turns, turn)
			} else {
				changed = true
			}
		}
		if len(previous.responsesNative) > 0 {
			next.responsesNative = map[string][]json.RawMessage{}
		}
		for messageKey, output := range previous.responsesNative {
			if dest, ok := nativeKeys[messageKey]; ok {
				next.responsesNative[dest] = output
				changed = changed || dest != messageKey
			} else {
				changed = true
			}
		}
		entry.payload = next
	}
	return changed
}
