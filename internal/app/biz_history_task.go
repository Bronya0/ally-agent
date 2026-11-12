// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"crypto/sha256"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// Storage-only provenance for the latest main task. Injected user messages and
// tool image inputs must never move its boundary. The key rejects stale indices
// after repairs, truncation or a full-summary replacement.
type historyTaskBoundary struct {
	Start int    `json:"start"`
	Key   string `json:"key"`
}

func historyTaskKey(m openai.ChatCompletionMessage) string {
	if m.Role != openai.ChatMessageRoleUser || isImageInjectionMessage(&m) {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(messageText(m))))
}

func validHistoryTask(b *historyTaskBoundary, history []openai.ChatCompletionMessage) bool {
	return b != nil && b.Start >= 0 && b.Start < len(history) && b.Key != "" && historyTaskKey(history[b.Start]) == b.Key
}

// Repairs can remove messages before the boundary. An earlier occurrence of the
// same input is a conservative fallback: it retains extra history, never cuts
// away a later part of this task. Generated records are not candidate inputs.
func resolveHistoryTask(b *historyTaskBoundary, history []openai.ChatCompletionMessage) *historyTaskBoundary {
	if validHistoryTask(b, history) {
		copy := *b
		return &copy
	}
	if b != nil && b.Key != "" {
		for i, m := range history {
			if !isFastCompactHistory(m) && historyTaskKey(m) == b.Key {
				return &historyTaskBoundary{Start: i, Key: b.Key}
			}
		}
	}
	return nil
}

func projectHistoryTask(b *historyTaskBoundary, source, projected []openai.ChatCompletionMessage, origins []int) *historyTaskBoundary {
	if !validHistoryTask(b, source) {
		return nil
	}
	for i, origin := range origins {
		if origin == b.Start && historyTaskKey(projected[i]) == b.Key {
			return &historyTaskBoundary{Start: i, Key: b.Key}
		}
	}
	return nil
}

func (a *App) historyTaskFor(sessionID string, history []openai.ChatCompletionMessage) *historyTaskBoundary {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.historyCacheStates[sessionID]
	if state == nil {
		return nil
	}
	return resolveHistoryTask(state.task, history)
}

// Called once after assembling a main run, before any queued inputs or tool
// steps. The existing saved history is the prefix, not part of this new task.
func (a *App) beginHistoryTask(sessionID string, messages []openai.ChatCompletionMessage) {
	previous := a.loadSessionHistoryCopy(sessionID)
	history := sanitizeHistoryMessages(messages)
	start := len(previous)
	if start >= len(history) || historyTaskKey(history[start]) == "" {
		start = inferHistoryTaskStart(history)
	}
	if start < 0 || start >= len(history) || historyTaskKey(history[start]) == "" {
		return
	}
	a.mu.Lock()
	a.historyCacheStateLocked(sessionID).task = &historyTaskBoundary{Start: start, Key: historyTaskKey(history[start])}
	a.mu.Unlock()
}

// Legacy histories have no task provenance. Only a user input following a
// complete assistant answer starts another task; consecutive inputs, tool
// results, images and notices stay with the current one. Ambiguity retains more.
func inferHistoryTaskStart(history []openai.ChatCompletionMessage) int {
	start := -1
	answered := false
	for i, m := range history {
		if isFastCompactHistory(m) {
			continue
		}
		switch m.Role {
		case openai.ChatMessageRoleAssistant:
			answered = len(m.ToolCalls) == 0 && messageText(m) != ""
		case openai.ChatMessageRoleUser:
			if isImageInjectionMessage(&m) {
				continue
			}
			if _, _, notice := parseTurnNotice(m); notice {
				continue
			}
			if start < 0 || answered {
				start = i
			}
			answered = false
		}
	}
	if start < 0 {
		return 0
	}
	return start
}
