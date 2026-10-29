// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"errors"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// Retain only the same bounded string that is appended to the model messages.
// Tool events arrive before a parallel batch has committed its full history.
func (a *App) rememberCommandContext(sessionID, callID, content string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.liveCommandContext == nil {
		a.liveCommandContext = map[string]map[string]string{}
	}
	if a.liveCommandContext[sessionID] == nil {
		a.liveCommandContext[sessionID] = map[string]string{}
	}
	a.liveCommandContext[sessionID][callID] = content
}

// GetCommandContextResult reads the retained model projection by call ID.
// Never follow output/log paths or load the frontend snapshot's full payload.
func (a *App) GetCommandContextResult(sessionID, callID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(callID) == "" {
		return "", errors.New("缺少会话或命令标识")
	}
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	a.mu.Lock()
	if live, active := a.liveCommandContext[sessionID]; active {
		content, found := live[callID]
		a.mu.Unlock()
		if found {
			return content, nil
		}
		return "", errors.New("当前上下文中已无此命令结果")
	}
	history, loaded := a.histories[sessionID]
	if loaded {
		content, found := commandContextContent(history, callID)
		a.mu.Unlock()
		if found {
			return content, nil
		}
	} else {
		a.mu.Unlock()
		history, _, _ = a.sessionHistorySnapshot(sessionID)
		if content, found := commandContextContent(history, callID); found {
			return content, nil
		}
	}
	return "", errors.New("当前上下文中已无此命令结果")
}

func commandContextContent(history []openai.ChatCompletionMessage, callID string) (string, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == openai.ChatMessageRoleTool && history[i].ToolCallID == callID {
			return history[i].Content, true
		}
	}
	return "", false
}

// Called when an active context is built or rewritten by compaction.
func (a *App) syncCommandContext(sessionID string, messages []openai.ChatCompletionMessage) {
	results := map[string]string{}
	commands := map[string]bool{}
	for _, m := range messages {
		for _, call := range m.ToolCalls {
			if call.Function.Name == "command" || call.Function.Name == "Bash" {
				commands[call.ID] = true
			}
		}
	}
	for _, m := range messages {
		if m.Role == openai.ChatMessageRoleTool && commands[m.ToolCallID] {
			results[m.ToolCallID] = m.Content
		}
	}
	a.mu.Lock()
	if a.liveCommandContext == nil {
		a.liveCommandContext = map[string]map[string]string{}
	}
	a.liveCommandContext[sessionID] = results
	a.mu.Unlock()
}
