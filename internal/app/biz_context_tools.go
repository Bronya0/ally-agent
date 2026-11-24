// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"sort"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// ContextToolUsage counts only records still carried by the model's context.
// CallTokens includes the arguments and call envelope, already in AssistantMsgs;
// ResultTokens is a subdivision of ToolResults, not an additional category.
type ContextToolUsage struct {
	Name         string `json:"name"`
	Calls        int    `json:"calls"`
	CallTokens   int    `json:"callTokens"`
	ResultTokens int    `json:"resultTokens"`
}

type toolSchemaStats struct {
	total int
	parts []ContextBreakdownPart // immutable after publication
}

func estimateModelToolSchemaStats(tools []openai.Tool) toolSchemaStats {
	if len(tools) == 0 {
		return toolSchemaStats{}
	}
	projected := modelToolSchemaProjection(tools)
	data, _ := json.Marshal(projected)
	stats := toolSchemaStats{total: estimateTokensFromText(string(data))}
	weight := 0
	for _, tool := range projected {
		data, _ := json.Marshal(tool)
		tokens := estimateTokensFromText(string(data))
		name := "unknown"
		if tool.Function != nil {
			name = tool.Function.Name
		}
		stats.parts = append(stats.parts, ContextBreakdownPart{Label: name, Tokens: tokens})
		weight += tokens
	}
	// Apportion array overhead and per-item rounding to keep parts equal to the
	// existing whole-array estimate, without changing the context total.
	if weight > 0 {
		cumulative, allocated := 0, 0
		for i := range stats.parts {
			cumulative += stats.parts[i].Tokens
			next := int(int64(cumulative) * int64(stats.total) / int64(weight))
			stats.parts[i].Tokens = next - allocated
			allocated = next
		}
	}
	sort.Slice(stats.parts, func(i, j int) bool {
		if stats.parts[i].Tokens != stats.parts[j].Tokens {
			return stats.parts[i].Tokens > stats.parts[j].Tokens
		}
		return stats.parts[i].Label < stats.parts[j].Label
	})
	return stats
}

func toolCallTokens(call openai.ToolCall) int {
	return estimateTokensFromText(call.ID) + estimateTokensFromText(string(call.Type)) +
		estimateTokensFromText(call.Function.Name) + estimateTokensFromText(call.Function.Arguments)
}

func contextToolName(call openai.ToolCall) string {
	name := call.Function.Name
	if name == "ToolExec" {
		// Decode just the envelope name once per newly appended call. Target
		// arguments remain opaque; no output or argument bodies are retained.
		var envelope struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &envelope) == nil && envelope.Name != "" {
			return envelope.Name
		}
	}
	if name == "" {
		return "unknown"
	}
	return strings.Clone(name)
}

func (acc *liveBreakdownAccumulator) addToolCall(call openai.ToolCall, tokens int) {
	if acc.toolUsage == nil {
		acc.toolUsage = make(map[string]ContextToolUsage)
	}
	if acc.pending == nil {
		acc.pending = make(map[string][]string)
	}
	name := contextToolName(call)
	usage := acc.toolUsage[name]
	usage.Name = name
	usage.Calls++
	usage.CallTokens += tokens
	acc.toolUsage[name] = usage
	acc.toolsChanged = true
	if call.ID != "" {
		acc.pending[call.ID] = append(acc.pending[call.ID], name)
	}
}

func (acc *liveBreakdownAccumulator) addToolResult(message openai.ChatCompletionMessage, tokens int) {
	name := message.Name
	if pending := acc.pending[message.ToolCallID]; len(pending) > 0 {
		name = pending[0]
		pending[0] = ""
		if len(pending) == 1 {
			delete(acc.pending, message.ToolCallID)
		} else {
			acc.pending[message.ToolCallID] = pending[1:]
		}
	}
	if name == "" {
		name = "unknown"
	}
	if acc.toolUsage == nil {
		acc.toolUsage = make(map[string]ContextToolUsage)
	}
	usage := acc.toolUsage[name]
	usage.Name = name
	usage.ResultTokens += tokens
	acc.toolUsage[name] = usage
	acc.toolsChanged = true
}

func sortedContextToolUsage(usage map[string]ContextToolUsage) []ContextToolUsage {
	if len(usage) == 0 {
		return nil
	}
	// Each update publishes a new small numeric snapshot. Later appends cannot
	// mutate the slice read by a concurrent footer poll or API response.
	items := make([]ContextToolUsage, 0, len(usage))
	for _, item := range usage {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if at, bt := a.CallTokens+a.ResultTokens, b.CallTokens+b.ResultTokens; at != bt {
			return at > bt
		}
		return a.Name < b.Name
	})
	return items
}
