package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// The SDK message has no private status field. Keep execution metadata in a
// session-scoped application ledger, never in provider-visible text or fields.
// Include content identity because relays may reuse call IDs in later turns.
func toolStatusIdentity(m openai.ChatCompletionMessage) string {
	return fmt.Sprintf("%s:%x", m.ToolCallID, sha256.Sum256([]byte(m.Content)))
}

func toolStatusScope(scope string) string {
	return reasoningStashScope(scope) + "\x1ftool-status"
}

func (s *reasoningStash) toolStatuses(scope string) map[string]bool {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[toolStatusScope(scope)]
	if entry == nil || entry.payload == nil {
		return nil
	}
	s.clock++
	entry.lastUsed = s.clock
	statuses := make(map[string]bool, len(entry.payload.toolResults))
	for key, failed := range entry.payload.toolResults {
		statuses[key] = failed
	}
	return statuses
}

func (s *reasoningStash) setToolStatuses(scope string, statuses map[string]bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := toolStatusScope(scope)
	if s.entries[key] == nil {
		s.evictLocked(key)
	}
	s.clock++
	s.entries[key] = &reasoningStashEntry{lastUsed: s.clock, payload: &sessionReasoningPayload{toolResults: statuses}}
}

func (s *reasoningStash) recordToolStatus(scope string, m openai.ChatCompletionMessage, result toolResult) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := toolStatusScope(scope)
	entry := s.entries[key]
	if entry == nil {
		s.evictLocked(key)
		entry = &reasoningStashEntry{payload: &sessionReasoningPayload{toolResults: map[string]bool{}}}
		s.entries[key] = entry
	}
	s.clock++
	entry.lastUsed = s.clock
	entry.payload.toolResults[toolStatusIdentity(m)] = !result.OK
}

const storedToolStatusField = "ally_tool_status_v1"

// Project identities through the same hygiene transforms so omitted/compacted
// text does not lose its execution status after restart.
func (a *App) toolStatusesForStorage(sessionID string, history []openai.ChatCompletionMessage) map[string]bool {
	statuses := a.reasoningStash.toolStatuses(openAIResponsesPromptCacheKey(sessionID))
	if len(statuses) == 0 {
		return nil
	}
	stored := map[string]bool{}
	for _, profile := range []historyProfile{historyProfileDisk, historyProfileProtected} {
		projected, origins := sanitizeHistoryMessagesWithOrigins(history, profile)
		for i, m := range projected {
			if m.Role != openai.ChatMessageRoleTool {
				continue
			}
			if failed, ok := statuses[toolStatusIdentity(history[origins[i]])]; ok {
				stored[toolStatusIdentity(m)] = failed
			}
		}
	}
	return stored
}

func (a *App) modelHistoryFromStorage(sessionID string, raw []byte, disk []openai.ChatCompletionMessage) ([]openai.ChatCompletionMessage, func()) {
	history, publish := a.modelHistoryPayloadFromStorage(sessionID, raw, disk)
	var rows []map[string]json.RawMessage
	var stored map[string]bool
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || json.Unmarshal(rows[0][storedToolStatusField], &stored) != nil {
		return history, publish
	}
	statuses := map[string]bool{}
	for _, m := range history {
		if m.Role == openai.ChatMessageRoleTool {
			key := toolStatusIdentity(m)
			if failed, ok := stored[key]; ok {
				statuses[key] = failed
			}
		}
	}
	return history, func() {
		if publish != nil {
			publish()
		}
		a.reasoningStash.setToolStatuses(openAIResponsesPromptCacheKey(sessionID), statuses)
	}
}
