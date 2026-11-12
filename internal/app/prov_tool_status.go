package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

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

func (s *reasoningStash) toolHistoryMetadata(scope string) map[string]toolHistoryMetadata {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	metadata := map[string]toolHistoryMetadata{}
	if entry := s.entries[toolStatusScope(scope)]; entry != nil && entry.payload != nil {
		for key, value := range entry.payload.toolMetadata {
			metadata[key] = value
		}
	}
	return metadata
}

func (s *reasoningStash) setToolStatuses(scope string, statuses map[string]bool, metadata ...map[string]toolHistoryMetadata) {
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
	payload := &sessionReasoningPayload{toolResults: statuses}
	if len(metadata) > 0 {
		payload.toolMetadata = metadata[0]
	}
	s.entries[key] = &reasoningStashEntry{lastUsed: s.clock, payload: payload}
}

func (s *reasoningStash) recordToolStatus(scope string, m openai.ChatCompletionMessage, result toolResult, metadata ...toolHistoryMetadata) {
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
	if len(metadata) > 0 {
		if entry.payload.toolMetadata == nil {
			entry.payload.toolMetadata = map[string]toolHistoryMetadata{}
		}
		entry.payload.toolMetadata[toolStatusIdentity(m)] = metadata[0]
	}
}

const storedToolStatusField = "ally_tool_status_v1"

// Project identities through the same hygiene transforms so omitted/compacted
// text does not lose its execution status after restart.
func (a *App) toolHistoryForStorage(sessionID string, history []openai.ChatCompletionMessage) (map[string]bool, map[string]toolHistoryMetadata) {
	statuses := a.reasoningStash.toolStatuses(openAIResponsesPromptCacheKey(sessionID))
	metadata := a.reasoningStash.toolHistoryMetadata(openAIResponsesPromptCacheKey(sessionID))
	if len(statuses) == 0 && len(metadata) == 0 {
		return nil, nil
	}
	stored := map[string]bool{}
	storedMeta := map[string]toolHistoryMetadata{}
	for _, profile := range []historyProfile{historyProfileDisk, historyProfileProtected} {
		projected, origins := sanitizeHistoryMessagesWithOrigins(history, profile)
		for i, m := range projected {
			if m.Role != openai.ChatMessageRoleTool {
				continue
			}
			if failed, ok := statuses[toolStatusIdentity(history[origins[i]])]; ok {
				stored[toolStatusIdentity(m)] = failed
			}
			if meta, ok := metadata[toolStatusIdentity(history[origins[i]])]; ok {
				storedMeta[toolStatusIdentity(m)] = meta
			}
		}
	}
	return stored, storedMeta
}

func (a *App) modelHistoryFromStorage(sessionID string, raw []byte, disk []openai.ChatCompletionMessage) ([]openai.ChatCompletionMessage, func()) {
	history, publish := a.modelHistoryPayloadFromStorage(sessionID, raw, disk)
	var rows []modelHistoryMetadata
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return history, publish
	}
	stored := rows[0]
	if stored.ToolStatuses == nil && stored.ToolMetadata == nil && stored.Task == nil {
		return history, publish
	}
	statuses := map[string]bool{}
	metadata := map[string]toolHistoryMetadata{}
	for _, m := range history {
		if m.Role == openai.ChatMessageRoleTool {
			key := toolStatusIdentity(m)
			if failed, ok := stored.ToolStatuses[key]; ok {
				statuses[key] = failed
			}
			if meta, ok := stored.ToolMetadata[key]; ok {
				// Stored metadata may come from an older client or a malformed file.
				meta.Purpose = strings.Clone(utf8Prefix(meta.Purpose, maxToolHistoryPurposeBytes))
				meta.Headers = strings.Clone(utf8Prefix(meta.Headers, maxToolHistoryHeaderBytes))
				switch meta.Status {
				case "success", "error", "cancelled":
				default:
					meta.Status = ""
				}
				metadata[key] = meta
			}
		}
	}
	var boundary *historyTaskBoundary
	projected, origins := sanitizeHistoryMessagesWithOrigins(history, historyProfileDisk)
	if validHistoryTask(stored.Task, projected) {
		boundary = &historyTaskBoundary{Start: origins[stored.Task.Start], Key: stored.Task.Key}
	}
	return history, func() {
		if publish != nil {
			publish()
		}
		a.reasoningStash.setToolStatuses(openAIResponsesPromptCacheKey(sessionID), statuses, metadata)
		a.historyCacheStateLocked(sessionID).task = boundary
	}
}
