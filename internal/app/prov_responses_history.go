// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ally-dev/internal/tools/toolcall"
	"github.com/openai/openai-go/v3/packages/param"
	oaresp "github.com/openai/openai-go/v3/responses"
	openai "github.com/sashabaranov/go-openai"
)

// Native Responses artifacts never enter the agent/UI message representation.
// Match against both the assistant ordinal and its visible projection. Identical
// answers in different turns stay distinct; rewritten histories cannot borrow a
// different turn's opaque state. System and ephemeral plan-check messages do not
// affect the ordinal. History rewrites already clear this session's stash.
func responsesMessageKey(ordinal int, m openai.ChatCompletionMessage) string {
	projection := struct {
		Ordinal int
		Content string
		Calls   []openai.ToolCall
	}{ordinal, messageText(m), normalizeToolCalls(m.ToolCalls)}
	b, _ := json.Marshal(projection)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (p *sessionReasoningPayload) nativeResponses(ordinal int, m openai.ChatCompletionMessage) []json.RawMessage {
	if p == nil {
		return nil
	}
	return p.responsesNative[responsesMessageKey(ordinal, m)]
}

func (s *reasoningStash) appendResponses(key, messageKey string, output []json.RawMessage) {
	if s == nil || len(output) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[key]
	if entry == nil {
		if s.entries == nil {
			s.entries = make(map[string]*reasoningStashEntry)
		}
		s.evictLocked(key)
		entry = &reasoningStashEntry{}
		s.entries[key] = entry
	}
	next := &sessionReasoningPayload{responsesNative: make(map[string][]json.RawMessage)}
	if entry.payload != nil {
		next.turns = entry.payload.turns
		for k, v := range entry.payload.responsesNative {
			next.responsesNative[k] = v
		}
	}
	next.responsesNative[messageKey] = output
	s.clock++
	entry.lastUsed = s.clock
	entry.payload = next
}

func (a *App) captureResponsesOutput(cfg ConfigState, model string, messages []openai.ChatCompletionMessage, result *modelStreamResult) {
	if result == nil || result.StopReason != "stop" || len(result.responsesOutput) == 0 || reasoningReplayScope(cfg) == "" {
		return
	}
	ordinal := 0
	for _, m := range messages {
		if m.Role == openai.ChatMessageRoleAssistant {
			ordinal++
		}
	}
	output := make([]json.RawMessage, len(result.responsesOutput))
	callIndex := 0
	for i, raw := range result.responsesOutput {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			return
		}
		var kind string
		_ = json.Unmarshal(item["type"], &kind)
		if kind == "function_call" {
			if callIndex >= len(result.ToolCalls) {
				return
			}
			call := result.ToolCalls[callIndex]
			callIndex++
			item["call_id"], _ = json.Marshal(toolcall.ForResponsesCall(call.ID))
			item["name"], _ = json.Marshal(call.Function.Name)
			item["arguments"], _ = json.Marshal(call.Function.Arguments)
			raw, _ = json.Marshal(item)
		}
		output[i] = append(json.RawMessage(nil), raw...)
	}
	if callIndex != len(result.ToolCalls) || !responsesOutputReplayable(output) {
		return
	}
	m := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: result.Content, ToolCalls: result.ToolCalls}
	if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
		return
	}
	a.reasoningStash.appendResponses(reasoningReplayKey(cfg, model), responsesMessageKey(ordinal, m), output)
}

// An encrypted reasoning item needs its original following item's id. Relays
// sometimes omit it; keep the legacy all-or-nothing fallback for those turns.
func responsesOutputReplayable(output []json.RawMessage) bool {
	for i, raw := range output {
		var item struct{ Type, ID string }
		if json.Unmarshal(raw, &item) != nil {
			return false
		}
		if item.ID != "" && !toolcall.IsSafeResponsesItemID(item.ID) {
			return false
		}
		if item.Type != "reasoning" {
			continue
		}
		if !toolcall.IsSafeResponsesItemID(item.ID) || i+1 >= len(output) {
			return false
		}
		var follower struct{ ID string }
		if json.Unmarshal(output[i+1], &follower) != nil || !toolcall.IsSafeResponsesItemID(follower.ID) {
			return false
		}
	}
	return true
}

func nativeResponsesInput(output []json.RawMessage) oaresp.ResponseInputParam {
	input := make(oaresp.ResponseInputParam, 0, len(output))
	for _, raw := range output {
		// Override avoids losing phase, namespace, new fields or output-text
		// content variants through the SDK's input/output union conversion.
		input = append(input, param.Override[oaresp.ResponseInputItemUnionParam](raw))
	}
	return input
}

type responsesOutputCapture struct {
	items   map[string]json.RawMessage
	indexes map[string]int64
	order   []string
}

func (c *responsesOutputCapture) observe(kind string, raw []byte) {
	if kind == "response.completed" || kind == "response.incomplete" {
		var payload struct {
			Response struct {
				Output []json.RawMessage `json:"output"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &payload) == nil && len(payload.Response.Output) > 0 {
			*c = responsesOutputCapture{}
			for i, item := range payload.Response.Output {
				c.put(int64(i), item)
			}
		}
		return
	}
	if kind != "response.output_item.done" {
		return
	}
	var payload struct {
		Index int64           `json:"output_index"`
		Item  json.RawMessage `json:"item"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		c.put(payload.Index, payload.Item)
	}
}

func (c *responsesOutputCapture) put(index int64, raw json.RawMessage) {
	var item struct{ ID, Type string }
	if json.Unmarshal(raw, &item) != nil || item.Type == "" {
		return
	}
	key := item.ID
	if key == "" {
		key = fmt.Sprintf("index:%d", index)
	}
	if c.items == nil {
		c.items = make(map[string]json.RawMessage)
		c.indexes = make(map[string]int64)
	}
	if _, ok := c.items[key]; !ok {
		c.order = append(c.order, key)
	}
	c.items[key] = append(json.RawMessage(nil), raw...)
	c.indexes[key] = index
}

// Preserve event output_index even when item.done events are missing. The
// compacted slice position is not a locator for an id-less streamed call.
func (c *responsesOutputCapture) indexedOutput() ([]json.RawMessage, []int64) {
	sort.SliceStable(c.order, func(i, j int) bool { return c.indexes[c.order[i]] < c.indexes[c.order[j]] })
	var output []json.RawMessage
	var indexes []int64
	for _, key := range c.order {
		output = append(output, c.items[key])
		indexes = append(indexes, c.indexes[key])
	}
	return output, indexes
}

func (c *responsesOutputCapture) output() []json.RawMessage {
	output, _ := c.indexedOutput()
	return output
}

// Decode the nested response directly. Some SDK stream-union versions lose
// fields of the nested object; final output, failed codes and incomplete reasons
// must be as authoritative as usage, which is already decoded from raw JSON.
func responsesTerminal(raw []byte) oaresp.Response {
	var payload struct {
		Response oaresp.Response `json:"response"`
	}
	_ = json.Unmarshal(raw, &payload)
	return payload.Response
}
