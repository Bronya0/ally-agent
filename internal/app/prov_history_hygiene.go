// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3.0. See the LICENSE file for details.
package app

import (
	"fmt"
	"strings"

	"ally-dev/internal/tools/toolcall"
	openai "github.com/sashabaranov/go-openai"
)

// historyProfile selects how much of a message list a consumer keeps.
type historyProfile int

const (
	// historyProfileMemory keeps the full model-facing history: a running session
	// replays it verbatim, so nothing may be trimmed.
	historyProfileMemory historyProfile = iota
	// historyProfileDisk is the persistence profile used by saveHistory. It drops
	// what a restarted session cannot use: image payloads, the reasoning text of
	// turns whose tool loop is over, and the tool output itself (stale file
	// contents and command output dominate the file, and the model can re-run a
	// tool on demand). Message structure — roles, the tool_call/tool_result
	// pairing, and the call arguments needed to re-run — stays intact so the
	// restored history remains a valid protocol sequence.
	historyProfileDisk
)

// toolResultPlaceholder replaces tool output that is no longer in the model's
// context — either because the disk profile never persisted it (history profile)
// or because micro-compaction cleared it to reclaim tokens mid-run. It keeps the
// message non-empty (validators reject an empty tool message) and tells the model
// how to get the data back without inviting a re-run of a mutating tool: an idempotent
// read/inspection can simply be repeated, while `command`/`edit`/`write`/`create` must not.
const toolResultPlaceholder = "(tool result omitted to save context; re-read the file or re-run read-only tools if you need it again — do not re-run commands, edits, or writes)"

func sanitizeHistoryMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	return sanitizeHistoryMessagesFor(messages, historyProfileMemory)
}

// sanitizeHistoryMessagesForDisk is the persistence variant: images are not
// persisted (base64 payloads would bloat disk history and restart replay
// would re-send them), so attachment images collapse to their text and read
// image injections are dropped wholesale. See historyProfileDisk for the rest.
func sanitizeHistoryMessagesForDisk(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	return sanitizeHistoryMessagesFor(messages, historyProfileDisk)
}

func sanitizeHistoryMessagesFor(messages []openai.ChatCompletionMessage, profile historyProfile) []openai.ChatCompletionMessage {
	flattenImages := profile == historyProfileDisk
	filtered := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, original := range messages {
		// Synthesized image-injection messages from the read tool survive in
		// the in-memory history (the session should keep seeing them until the
		// process exits) but are never persisted: their NUL-prefixed marker is
		// not JSON-portable and the base64 payloads do not go to disk.
		if flattenImages && isImageInjectionMessage(&original) {
			continue
		}
		if original.Role == openai.ChatMessageRoleSystem {
			continue
		}
		m := original
		if flattenImages && len(m.MultiContent) > 0 {
			m.Content = textFromMultiContent(m.MultiContent)
			m.MultiContent = nil
		}
		if profile == historyProfileDisk {
			// Reasoning text is provider-protocol replay state for the tool loop
			// that produced it (DeepSeek/Kimi pass it back inside the loop,
			// Anthropic signs it). A restarted process starts a new loop, so the
			// old thoughts only cost bytes and input tokens.
			m.ReasoningContent = ""
			if m.Role == openai.ChatMessageRoleTool {
				m.Content = toolResultPlaceholder
			}
		}
		if strings.TrimSpace(m.Content) == "" && len(m.MultiContent) == 0 && len(m.ToolCalls) == 0 && m.Role != openai.ChatMessageRoleTool {
			continue
		}
		m.ToolCalls = append([]openai.ToolCall(nil), m.ToolCalls...)
		// Histories written before the truncation repair may still carry a
		// tool call whose arguments were cut off mid-stream, or a function
		// name repeated N times by a relay that re-sent the name in every
		// streaming delta, or a name that is the concatenation of two
		// different tool names produced by a relay that sent multiple
		// tool_calls with the same Index; providers that parse tool_calls
		// server-side would keep rejecting every request for the session,
		// so repair them on load as well.
		for i := range m.ToolCalls {
			m.ToolCalls[i].Function.Name = collapseRepeatedName(m.ToolCalls[i].Function.Name)
			m.ToolCalls[i].Function.Arguments = toolcall.RepairTruncatedArguments(m.ToolCalls[i].Function.Arguments)
		}
		// 丢弃拼接工具名的 tool_call 和截断参数标记的 tool_call：
		// 两者都不会通过服务商校验，repairDanglingToolCalls 会自动清除
		// 对应的孤儿 tool 结果消息。
		if len(m.ToolCalls) > 0 {
			kept := make([]openai.ToolCall, 0, len(m.ToolCalls))
			for _, call := range m.ToolCalls {
				if isConcatenatedKnownToolNames(call.Function.Name) {
					continue
				}
				if toolcall.IsTruncatedArguments(call.Function.Arguments) {
					continue
				}
				kept = append(kept, call)
			}
			m.ToolCalls = kept
		}
		filtered = append(filtered, m)
	}
	return repairDanglingToolCalls(filtered)
}

// repairDanglingToolCalls enforces the tool-call pairing invariant: every
// assistant tool_call must be followed by exactly one tool message carrying
// its ID, and every tool message must answer a still-pending tool_call.
// Histories written when a run panicked between appending the assistant
// tool_calls message and appending its tool results (or by older builds that
// hit the same window) otherwise poison the session permanently — providers
// reject every request with 400 because the dangling tool_calls can never be
// answered. Unanswered tool_calls are stripped (the assistant text is kept);
// orphan and duplicate tool messages are dropped.
//
// Calls are tracked per declared position instead of per ID. A relay can return
// two tool_calls sharing one ID, and the old ID-keyed table consumed the entry
// on the first result: the second result was dropped as a duplicate *and* the
// entry was gone, so the call that was never answered stayed in the history — a
// permanent 400 that this function was supposed to repair.
func repairDanglingToolCalls(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	// pending maps toolCallID -> the declared calls still awaiting a result, in
	// declaration order. Entries are consumed as results arrive.
	pending := make(map[string][]pendingToolCall)
	// closeTurn strips every still-pending (never answered) tool_call from the
	// message that declared it. It runs whenever the current assistant turn
	// ends: the next assistant/user message, or the end of the history.
	closeTurn := func() {
		if len(pending) == 0 {
			return
		}
		strip := make(map[int]map[int]bool, len(pending))
		for _, queue := range pending {
			for _, call := range queue {
				if strip[call.msgIndex] == nil {
					strip[call.msgIndex] = map[int]bool{}
				}
				strip[call.msgIndex][call.callIndex] = true
			}
		}
		for msgIndex, callIndexes := range strip {
			kept := make([]openai.ToolCall, 0, len(out[msgIndex].ToolCalls))
			for i, call := range out[msgIndex].ToolCalls {
				if !callIndexes[i] {
					kept = append(kept, call)
				}
			}
			updated := out[msgIndex]
			updated.ToolCalls = kept
			out[msgIndex] = updated
		}
		pending = make(map[string][]pendingToolCall)
	}
	for _, m := range messages {
		switch m.Role {
		case openai.ChatMessageRoleAssistant:
			closeTurn()
			out = append(out, m)
			assistantIndex := len(out) - 1
			for i, call := range m.ToolCalls {
				pending[call.ID] = append(pending[call.ID], pendingToolCall{msgIndex: assistantIndex, callIndex: i})
			}
		case openai.ChatMessageRoleTool:
			queue := pending[m.ToolCallID]
			if len(queue) == 0 {
				// Orphan tool result: no pending call, or one more result than the
				// turn declared. Providers reject these too.
				continue
			}
			// One result answers the oldest call still waiting for this ID.
			if len(queue) == 1 {
				delete(pending, m.ToolCallID)
			} else {
				pending[m.ToolCallID] = queue[1:]
			}
			out = append(out, m)
		default:
			closeTurn()
			out = append(out, m)
		}
	}
	closeTurn()
	// An assistant message whose tool_calls all dangled and whose text is
	// empty carries no information; drop it entirely.
	final := make([]openai.ChatCompletionMessage, 0, len(out))
	for _, m := range out {
		if m.Role == openai.ChatMessageRoleAssistant && len(m.ToolCalls) == 0 && strings.TrimSpace(m.Content) == "" {
			continue
		}
		final = append(final, m)
	}
	return final
}

// pendingToolCall locates one declared tool_call: the assistant message holding
// it (an index into the message list being built) and its position inside that
// message's ToolCalls slice.
type pendingToolCall struct {
	msgIndex  int
	callIndex int
}

func textFromMultiContent(parts []openai.ChatMessagePart) string {
	var b strings.Builder
	imageCount := 0
	for _, part := range parts {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(part.Text)
			}
		case openai.ChatMessagePartTypeImageURL:
			imageCount++
		}
	}
	if imageCount > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%d image attachment(s) omitted from saved history]", imageCount)
	}
	return b.String()
}

// stripReasoningContent returns a copy of messages with ReasoningContent cleared
// on all assistant messages. Used during cross-model switches to avoid sending
// irrelevant reasoning traces from previous models and prevent 400 signature rejections.
func stripReasoningContent(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return messages
	}
	out := make([]openai.ChatCompletionMessage, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].Role == openai.ChatMessageRoleAssistant && out[i].ReasoningContent != "" {
			out[i].ReasoningContent = ""
		}
	}
	return out
}
