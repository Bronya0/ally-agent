// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// Retain exact reasoning payloads for the untouched tail only. Clearing the
// entire ledger would lose recent signed/encrypted reasoning; leaving it intact
// would silently replay the old thoughts the user just asked us to compact.
// Snapshots are replaced copy-on-write, matching appendTurn/appendResponses.
func (s *reasoningStash) retainFastCompactTail(sessionID string, history, compacted []openai.ChatCompletionMessage, cutoff int) bool {
	if s == nil || cutoff <= 0 {
		return false
	}
	callIDs := map[string]bool{}
	nativeKeys := map[string]string{}
	type nativeRewrite struct {
		key, excerpt string
		replacement  *string
	}
	rewrites := map[string]nativeRewrite{}
	ordinal := 0
	retainedOrdinal := 0
	for i, m := range history {
		if m.Role != openai.ChatMessageRoleAssistant {
			continue
		}
		kept := strings.TrimSpace(compacted[i].Content) != "" || len(compacted[i].MultiContent) > 0 || len(compacted[i].ToolCalls) > 0
		if i >= cutoff {
			for _, call := range m.ToolCalls {
				callIDs[call.ID] = true
			}
			nativeKeys[responsesMessageKey(ordinal, m)] = responsesMessageKey(retainedOrdinal, compacted[i])
		} else if kept {
			excerpt := ""
			if m.ReasoningContent != compacted[i].ReasoningContent {
				if start := strings.LastIndex(compacted[i].Content, fastCompactReasoningMarker); start >= 0 {
					excerpt = compacted[i].Content[start:]
				}
			}
			rewrite := nativeRewrite{key: responsesMessageKey(retainedOrdinal, compacted[i]), excerpt: excerpt}
			if fastCompactAnswerText(m.Content) != fastCompactAnswerText(compacted[i].Content) {
				// Native Responses output must not replay the original long answer
				// after its ordinary projection has been shortened.
				replacement := compacted[i].Content
				rewrite.replacement, rewrite.excerpt = &replacement, ""
			}
			rewrites[responsesMessageKey(ordinal, m)] = rewrite
		}
		ordinal++
		if kept {
			retainedOrdinal++
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
		next := &sessionReasoningPayload{responsesNative: map[string][]json.RawMessage{}}
		for _, turn := range entry.payload.turns {
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
		for messageKey, output := range entry.payload.responsesNative {
			if nextKey, ok := nativeKeys[messageKey]; ok {
				next.responsesNative[nextKey] = output
				changed = changed || nextKey != messageKey
			} else if rewrite, ok := rewrites[messageKey]; ok {
				// Preserve native answer ordering/phase and function-call identity.
				// Shortened answer text replaces native message content too.
				clean, removed := compactNativeResponses(output, rewrite.excerpt, rewrite.replacement)
				if len(clean) > 0 {
					next.responsesNative[rewrite.key] = clean
				}
				changed = changed || removed || rewrite.key != messageKey
			} else {
				changed = true
			}
		}
		entry.payload = next
	}
	return changed
}

func compactNativeResponses(output []json.RawMessage, excerpt string, replacement *string) ([]json.RawMessage, bool) {
	var clean []json.RawMessage
	removed := false
	replaced := false
	if excerpt != "" {
		text, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "content": excerpt})
		clean = append(clean, text)
	}
	for _, raw := range output {
		var item struct{ Type string }
		if json.Unmarshal(raw, &item) != nil {
			// Unusable replay must fall back to the visible history.
			return nil, true
		}
		if item.Type == "reasoning" {
			removed = true
			continue
		}
		if item.Type == "message" && replacement != nil {
			removed = true
			if replaced || *replacement == "" {
				continue
			}
			var message map[string]json.RawMessage
			if json.Unmarshal(raw, &message) != nil {
				return nil, true
			}
			message["content"], _ = json.Marshal([]map[string]any{{"type": "output_text", "text": *replacement, "annotations": []any{}}})
			raw, _ = json.Marshal(message)
			replaced = true
		}
		clean = append(clean, raw)
	}
	if replacement != nil && *replacement != "" && !replaced {
		text, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "content": *replacement})
		clean = append([]json.RawMessage{text}, clean...)
		removed = true
	}
	return clean, removed || excerpt != ""
}
