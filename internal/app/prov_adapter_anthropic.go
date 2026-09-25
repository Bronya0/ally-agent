// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"ally-dev/internal/tools/schemautil"
	"ally-dev/internal/tools/toolcall"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	legacyopenai "github.com/sashabaranov/go-openai"
)

func (a *App) streamAnthropicMessages(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	baseURL := baseURLForAPIFormat(cfg)
	// 关闭 SDK 内置重试,改用本模块统一的重试循环以便发出 run:retry 事件。
	clientOptions := []anthropicoption.RequestOption{
		anthropicoption.WithAPIKey(cfg.APIKey),
		anthropicoption.WithBaseURL(baseURL),
		anthropicoption.WithMaxRetries(0),
		anthropicoption.WithHTTPClient(modelHTTPClient(cfg, true, 0)),
	}
	for key, value := range sessionAffinityHeaders(cfg) {
		clientOptions = append(clientOptions, anthropicoption.WithHeader(key, value))
	}
	client := anthropic.NewClient(clientOptions...)

	replay := a.reasoningStash.get(reasoningReplayKey(cfg, model))
	// One identity check for the whole request: the official endpoint is the one
	// that validates thinking signatures and the one whose cache-control ttl
	// field is written out (see buildAnthropicMessages and
	// markAnthropicPromptCacheBreakpoints).
	officialEndpoint := isOfficialAnthropicEndpoint(cfg)
	system, anthropicMessages := buildAnthropicMessages(messages, replay, officialEndpoint)
	if len(anthropicMessages) == 0 || anthropicMessages[0].Role != anthropic.MessageParamRoleUser {
		anthropicMessages = append([]anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("..."))}, anthropicMessages...)
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		Messages:  anthropicMessages,
		MaxTokens: int64(cfg.MaxTokens),
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	if len(tools) > 0 {
		params.Tools = convertToolsToAnthropic(tools)
	}
	// Prompt-cache breakpoints: one on the last tool definition, one on the last
	// system block, one on the last content block of the last message. The
	// markers themselves are understood by official Anthropic and by
	// Anthropic-compatible reverse proxies/gateways; the ttl field is only
	// written for the official endpoint (see
	// markAnthropicPromptCacheBreakpoints).
	markAnthropicPromptCacheBreakpoints(&params, officialEndpoint)
	// Thinking configuration for Anthropic:
	// - For adaptive models (Claude 4.6+/5+): thinking: { type: "adaptive" } and output_config.effort.
	// - For budget models (Claude 3.7 Sonnet): thinking: { type: "enabled", budget_tokens: N } without output_config.effort
	//   (passing output_config.effort on 3.7 causes a 400 error).
	// - For effort "off": thinking: { type: "disabled" }.
	thinkingEnabled := configureAnthropicThinking(&params, model, cfg.ReasoningEffort, cfg.MaxTokens)

	maxRetries := effectiveLLMRetries(cfg)
	var assistant strings.Builder
	var reasoning strings.Builder
	// thinkingBlocks captures this response's thinking/redacted_thinking
	// blocks in order, with signatures, for replay on the next request.
	thinkingBlocks := []anthropicThinkingBlock{}
	// blockThinkingIdx maps the stream content-block index of a thinking
	// block to its index in thinkingBlocks, so thinking_delta and
	// signature_delta events append to the right block.
	blockThinkingIdx := map[int64]int{}
	toolCalls := []legacyopenai.ToolCall{}
	toolIndexByBlock := map[int64]int{}
	toolEventGate := newModelToolCallEventGate(func(event modelStreamEvent) {
		emitModelStreamEvent(onEvent, event)
	})
	var usage *modelUsage
	// usageState carries the raw Anthropic counters across message_start /
	// message_delta events (see anthropicUsageState).
	usageState := &anthropicUsageState{}
	var stopReason string

	for attempt := 0; attempt <= maxRetries; attempt++ {
		assistant.Reset()
		reasoning.Reset()
		thinkingBlocks = thinkingBlocks[:0]
		blockThinkingIdx = map[int64]int{}
		toolCalls = toolCalls[:0]
		toolIndexByBlock = map[int64]int{}
		usage = nil
		usageState = &anthropicUsageState{}
		stopReason = ""
		// Anthropic requires the interleaved-thinking beta for extended thinking
		// to continue across tool calls inside one turn (pi:
		// anthropic-messages.ts betas, kimi: anthropic/requester.ts baseline).
		// Adaptive-thinking models interleave by default, so the flag is only
		// sent for budget-based models.
		streamOptions := []anthropicoption.RequestOption{}
		if beta := anthropicInterleavedThinkingBeta(thinkingEnabled, len(tools) > 0, model); beta != "" {
			streamOptions = append(streamOptions, anthropicoption.WithHeader("anthropic-beta", beta))
		}
		stream := client.Messages.NewStreaming(ctx, params, streamOptions...)
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "message_start":
				ev := event.AsMessageStart()
				usageState.mergeStart(ev.Message.Usage)
			case "message_delta":
				ev := event.AsMessageDelta()
				usageState.mergeDelta(ev.Usage)
				stopReason = string(ev.Delta.StopReason)
			case "content_block_start":
				ev := event.AsContentBlockStart()
				block := ev.ContentBlock
				switch block.Type {
				case "text":
					if block.Text != "" {
						assistant.WriteString(block.Text)
						emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: block.Text})
					}
				case "thinking":
					blockThinkingIdx[ev.Index] = len(thinkingBlocks)
					thinkingBlocks = append(thinkingBlocks, anthropicThinkingBlock{Signature: block.Signature})
					if block.Thinking != "" {
						reasoning.WriteString(block.Thinking)
						emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: block.Thinking})
					}
				case "redacted_thinking":
					blockThinkingIdx[ev.Index] = len(thinkingBlocks)
					thinkingBlocks = append(thinkingBlocks, anthropicThinkingBlock{Data: block.Data})
				case "tool_use":
					idx := len(toolCalls)
					toolIndexByBlock[ev.Index] = idx
					args := ""
					if block.Input != nil {
						if raw, err := json.Marshal(block.Input); err == nil && string(raw) != "null" && string(raw) != "{}" {
							args = string(raw)
						}
					}
					toolCalls = append(toolCalls, legacyopenai.ToolCall{
						ID:   block.ID,
						Type: legacyopenai.ToolTypeFunction,
						Function: legacyopenai.FunctionCall{
							Name:      block.Name,
							Arguments: args,
						},
					})
					toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
				}
			case "content_block_delta":
				ev := event.AsContentBlockDelta()
				delta := ev.Delta
				switch delta.Type {
				case "text_delta":
					if delta.Text != "" {
						assistant.WriteString(delta.Text)
						emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: delta.Text})
					}
				case "thinking_delta":
					if delta.Thinking != "" {
						reasoning.WriteString(delta.Thinking)
						if idx, ok := blockThinkingIdx[ev.Index]; ok {
							thinkingBlocks[idx].Thinking += delta.Thinking
						}
						emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: delta.Thinking})
					}
				case "signature_delta":
					// Relays differ on the wire shape: most send incremental chunks,
					// some re-send the whole signature in every delta. Merging with the
					// shared delta rule (id/name merging uses the same one) keeps a
					// cumulative re-send from producing a doubled signature that the
					// provider then rejects as modified.
					if idx, ok := blockThinkingIdx[ev.Index]; ok {
						thinkingBlocks[idx].Signature = toolcall.MergeRepeatedDelta(thinkingBlocks[idx].Signature, delta.Signature)
					}
				case "input_json_delta":
					if idx, ok := toolIndexByBlock[ev.Index]; ok {
						toolCalls[idx].Function.Arguments += delta.PartialJSON
						toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
					}
				}
			}
		}
		streamErr := stream.Err()
		stream.Close()
		if streamErr == nil && strings.TrimSpace(stopReason) != "" {
			break
		}
		if streamErr == nil {
			streamErr = errors.New("stream ended without terminal event")
		}
		// 适配器只重试"尚未产出任何输出"的失败(与 streamOpenAIChat /
		// streamOpenAIResponses 的重试守卫一致):无输出则重试不会造成
		// 重复;已经产生内容的中断交给上层 runChat 做整轮重试,避免把
		// 半截输出拼进下一次请求。
		if assistant.Len() == 0 && reasoning.Len() == 0 && len(toolCalls) == 0 &&
			ctx.Err() == nil && attempt < maxRetries && shouldRetryLLMError(streamErr) {
			wait := llmRetryDelayForError(attempt+1, streamErr)
			emitLLMRetryEvent(onEvent, attempt+1, maxRetries, streamErr, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		return nil, streamErr
	}
	usage = usageState.modelUsage()
	for i := range toolCalls {
		if strings.TrimSpace(toolCalls[i].Function.Arguments) == "" {
			toolCalls[i].Function.Arguments = "{}"
		}
	}
	// Record this response's thinking blocks as one turn of the session
	// ledger, keyed by the turn's tool-call ids: every later request replays
	// them at the front of this turn's own assistant message, so the prefix
	// stays byte-identical across agent steps. Non-thinking responses record
	// no turn; earlier turns keep theirs.
	if len(thinkingBlocks) > 0 && len(toolCalls) > 0 {
		a.reasoningStash.appendTurn(reasoningReplayKey(cfg, model), reasoningTurn{
			callIDs:   toolCallIDsOf(toolCalls),
			anthropic: thinkingBlocks,
		})
	}
	return &modelStreamResult{
		Content:          assistant.String(),
		Reasoning:        reasoning.String(),
		ReasoningPresent: len(thinkingBlocks) > 0,
		ToolCalls:        normalizeToolCalls(toolCalls),
		Usage:            usage,
		StopReason:       stopReason,
	}, nil
}

// anthropicInterleavedThinkingBeta returns the beta flag Anthropic requires for
// extended thinking to continue between tool calls inside one assistant turn. pi
// sends interleaved-thinking-2025-05-14 for any thinking request on a
// non-adaptive model (anthropic-messages.ts betas); Ally limits it to tool turns,
// which is where interleaving can actually happen, and never sends it to
// adaptive-thinking models, which interleave by default.
func anthropicInterleavedThinkingBeta(thinkingEnabled, hasTools bool, model string) string {
	if !thinkingEnabled || !hasTools || isAnthropicAdaptiveThinkingModel(model) {
		return ""
	}
	return "interleaved-thinking-2025-05-14"
}

func isAnthropicAdaptiveThinkingModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "sonnet-4.6") || strings.Contains(m, "sonnet-4-6") ||
		strings.Contains(m, "opus-4.6") || strings.Contains(m, "opus-4-6") ||
		strings.Contains(m, "opus-4.7") || strings.Contains(m, "opus-4-7") ||
		strings.Contains(m, "opus-4.8") || strings.Contains(m, "opus-4-8") ||
		strings.Contains(m, "sonnet-5") || strings.Contains(m, "opus-5") ||
		strings.Contains(m, "haiku-5") || strings.Contains(m, "fable") ||
		strings.Contains(m, "mythos") || strings.HasPrefix(m, "claude-5")
}

// configureAnthropicThinking applies the thinking configuration for the
// selected effort level and reports whether extended thinking is enabled for
// this request (the caller uses that to decide on the interleaved-thinking
// beta). It returns false for an explicit "off" and for "auto": auto sends no
// thinking field at all, so the model's own default applies.
func configureAnthropicThinking(params *anthropic.MessageNewParams, model, rawEffort string, maxTokens int) bool {
	raw := strings.ToLower(strings.TrimSpace(rawEffort))
	if normalizeReasoningEffort(raw) == reasoningEffortOff {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{Type: "disabled"}}
		return false
	}
	effort := normalizeReasoningEffort(rawEffort)
	if effort == "" || effort == reasoningEffortAuto {
		return false
	}
	// display: "summarized" is what makes the model stream its thinking text:
	// pi sets it explicitly on both branches so Opus 4.7 / Mythos behave like the
	// older Claude 4 models (anthropic-messages.ts:1127/1135/1147), and the SDK
	// documents summarized as the value that returns thinking normally instead of
	// a signature-only ("omitted") response.
	if isAnthropicAdaptiveThinkingModel(model) {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{
			Type:    "adaptive",
			Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized,
		}}
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(effort)}
	} else {
		// Budget-tokens thinking for Claude 3.7 and non-adaptive models.
		// Platform docs: minimum budget_tokens is 1024, and budget_tokens must be
		// strictly less than max_tokens.
		budget := int64(4096)
		switch effort {
		case "low":
			budget = 1024
		case "medium":
			budget = 4096
		case "high", "xhigh", "max":
			budget = 32000
		}
		// The thinking budget and the visible answer share max_tokens, so a budget
		// is capped to leave the answer room that is always kept free (pi:
		// clampThinkingBudgetToAnswerRoom with MIN_ANSWER_TOKENS = 1024,
		// simple-options.ts:64-72). When the configured cap cannot satisfy both
		// the floor and the reserved answer room there is no valid
		// extended-thinking configuration, so thinking is left unset instead of
		// sending a request the API rejects.
		if maxTokens < minAnthropicThinkingBudget+minAnthropicAnswerTokens {
			log.Printf("[llm] max_tokens=%d is too small for Anthropic extended thinking; leaving thinking unset", maxTokens)
			return false
		}
		if room := int64(maxTokens - minAnthropicAnswerTokens); budget > room {
			budget = room
		}
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfEnabled: &anthropic.ThinkingConfigEnabledParam{
			Type:         "enabled",
			BudgetTokens: budget,
			Display:      anthropic.ThinkingConfigEnabledDisplaySummarized,
		}}
	}
	return true
}

const (
	// minAnthropicThinkingBudget is Anthropic's documented floor for
	// budget_tokens (budget_tokens must additionally stay below max_tokens).
	minAnthropicThinkingBudget = 1024
	// minAnthropicAnswerTokens is the output room always kept for the visible
	// answer when a thinking budget shares the response ceiling (pi:
	// MIN_ANSWER_TOKENS, simple-options.ts:64).
	minAnthropicAnswerTokens = 1024
)

func anthropicStopReasonError(reason string, hasOutput bool) error {
	switch strings.TrimSpace(reason) {
	case "", "end_turn", "tool_use", "stop_sequence", "pause_turn":
		return nil
	case "max_tokens":
		return errors.New("Anthropic response reached the Max Tokens limit; increase Max Tokens or shorten the conversation")
	case "refusal":
		return errors.New("Anthropic refused the request")
	case "model_context_window_exceeded":
		return errors.New("Anthropic stopped because the model context window was exceeded")
	default:
		if hasOutput {
			return nil
		}
		return fmt.Errorf("Anthropic stopped with unsupported reason %q", reason)
	}
}

func buildAnthropicMessages(messages []legacyopenai.ChatCompletionMessage, replay *sessionReasoningPayload, officialEndpoint bool) (string, []anthropic.MessageParam) {
	systemParts := []string{}
	out := []anthropic.MessageParam{}

	appendMessage := func(role anthropic.MessageParamRole, blocks []anthropic.ContentBlockParamUnion) {
		if len(blocks) == 0 {
			return
		}
		// Anthropic Messages API requires roles to alternate strictly (user ⇄ assistant).
		// When multiple same-role messages appear consecutively (for example: user question ->
		// cancelledTurnMarker -> new user prompt, or tool results followed by user cancellation/input),
		// merge their content blocks into the preceding same-role message instead of emitting
		// adjacent same-role messages that fail Anthropic validation or get discarded by gateways.
		if len(out) > 0 && out[len(out)-1].Role == role {
			out[len(out)-1].Content = append(out[len(out)-1].Content, blocks...)
			return
		}
		if role == anthropic.MessageParamRoleUser {
			out = append(out, anthropic.NewUserMessage(blocks...))
		} else {
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		}
	}

	hasSeenTurn := false
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		switch m.Role {
		case legacyopenai.ChatMessageRoleSystem:
			text := messageText(m)
			if text == "" {
				continue
			}
			if !hasSeenTurn {
				systemParts = append(systemParts, text)
			} else {
				// Mid-conversation system turns (compaction summaries, steering reminders)
				// must not be hoisted into the top-level system prompt, which would corrupt
				// the primary prompt-cache breakpoint. Emit as a chronological <system>...</system>
				// block in a user message (merged with adjacent user messages to preserve alternating roles).
				wrapped := fmt.Sprintf("<system>\n%s\n</system>", text)
				appendMessage(anthropic.MessageParamRoleUser, []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock(wrapped),
				})
			}
		case legacyopenai.ChatMessageRoleUser:
			hasSeenTurn = true
			blocks := anthropicBlocksFromMessage(m)
			appendMessage(anthropic.MessageParamRoleUser, blocks)
		case legacyopenai.ChatMessageRoleAssistant:
			hasSeenTurn = true
			blocks := []anthropic.ContentBlockParamUnion{}
			// The turn's captured thinking blocks replay at the front of its own
			// assistant message — on every request that still carries the turn —
			// so the request prefix stays byte-identical across agent steps.
			// Anthropic requires thinking blocks to precede the tool_use blocks
			// of the same message and accepts omitting them entirely, which is
			// why a turn without a ledger entry simply emits none. The ledger is
			// model-scoped: blocks captured from another model are never
			// replayed, because Anthropic validates the signature against the
			// model that produced it.
			if turn := replay.turnForAny(toolCallIDsOf(m.ToolCalls)); turn != nil {
				blocks = append(blocks, anthropicThinkingBlockParams(turn.anthropic, officialEndpoint)...)
			}
			if text := messageText(m); text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(text))
			}
			for _, call := range m.ToolCalls {
				toolID := toolcall.ForAnthropic(toolcall.Effective(call.ID, call.Function.Name))
				blocks = append(blocks, anthropic.NewToolUseBlock(toolID, toolcall.DecodeArguments(call.Function.Arguments), call.Function.Name))
			}
			appendMessage(anthropic.MessageParamRoleAssistant, blocks)
		case legacyopenai.ChatMessageRoleTool:
			blocks := []anthropic.ContentBlockParamUnion{}
			for i < len(messages) && messages[i].Role == legacyopenai.ChatMessageRoleTool {
				toolMsg := messages[i]
				toolID := strings.TrimSpace(toolMsg.ToolCallID)
				if toolID == "" {
					toolID = "tool_call"
				}
				toolID = toolcall.ForAnthropic(toolID)
				blocks = append(blocks, anthropic.NewToolResultBlock(toolID, toolMsg.Content, anthropicToolResultIsError(toolMsg.Content)))
				i++
			}
			i--
			appendMessage(anthropic.MessageParamRoleUser, blocks)
		}
	}
	return strings.Join(systemParts, "\n\n"), out
}

// markAnthropicPromptCacheBreakpoints places explicit prompt-cache breakpoints:
// one on the last tool definition and one on the last system block (together
// they cache tools+system, reusable across runs while those bytes stay
// stable), and one on the last content block of the last message that accepts
// one (caches the stable request prefix so it grows incrementally across agent
// steps). A block that cannot carry a marker is skipped and the search keeps
// going, so an uncacheable tail block never leaves that breakpoint unset.
// Anthropic allows up to 4 breakpoints; 3 are used.
//
// The ttl is written out only for the official endpoint: "5m" is the
// documented default, so omitting it leaves the cache lifetime identical
// everywhere else, while a compatible gateway keeps receiving only the fields
// it knows. The marker itself is always built through the SDK constructor,
// because `cache_control` is tagged omitzero and a zero-value
// CacheControlEphemeralParam (empty type AND empty ttl) is dropped from the
// request altogether — that would silently lose the breakpoint instead of
// sending one without a ttl.
func markAnthropicPromptCacheBreakpoints(params *anthropic.MessageNewParams, officialEndpoint bool) {
	cc := anthropic.NewCacheControlEphemeralParam()
	if officialEndpoint {
		cc.TTL = anthropic.CacheControlEphemeralTTLTTL5m
	}
	if len(params.Tools) > 0 {
		lastIdx := len(params.Tools) - 1
		if params.Tools[lastIdx].OfTool != nil {
			params.Tools[lastIdx].OfTool.CacheControl = cc
		}
	}
	if len(params.System) > 0 {
		lastIdx := len(params.System) - 1
		params.System[lastIdx].CacheControl = cc
	}
	for i := len(params.Messages) - 1; i >= 0; i-- {
		msg := params.Messages[i]
		for j := len(msg.Content) - 1; j >= 0; j-- {
			if setAnthropicBlockCacheControl(msg.Content[j], cc) {
				return
			}
		}
	}
}

// setAnthropicBlockCacheControl writes the breakpoint marker onto a content block
// and reports whether the block accepts one. The adapter builds text, tool_use,
// tool_result and image blocks; a replayed thinking / redacted_thinking block is
// the one shape with no cache_control field, and the caller keeps scanning for a
// block that has one.
func setAnthropicBlockCacheControl(block anthropic.ContentBlockParamUnion, cc anthropic.CacheControlEphemeralParam) bool {
	switch {
	case block.OfText != nil:
		block.OfText.CacheControl = cc
	case block.OfToolResult != nil:
		block.OfToolResult.CacheControl = cc
	case block.OfToolUse != nil:
		block.OfToolUse.CacheControl = cc
	case block.OfImage != nil:
		block.OfImage.CacheControl = cc
	default:
		return false
	}
	return true
}

func anthropicToolResultIsError(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil && obj != nil {
		if okVal, exists := obj["ok"]; exists {
			if b, isBool := okVal.(bool); isBool {
				return !b
			}
		}
		if isErr, exists := obj["isError"]; exists {
			if b, isBool := isErr.(bool); isBool {
				return b
			}
		}
		if isErr, exists := obj["is_error"]; exists {
			if b, isBool := isErr.(bool); isBool {
				return b
			}
		}
		if _, hasErr := obj["error"]; hasErr {
			if okVal, hasOK := obj["ok"]; !hasOK || okVal == false {
				return true
			}
		}
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "error:") ||
		strings.HasPrefix(lower, "mcp call failed:") ||
		strings.HasPrefix(lower, "unknown tool:") ||
		strings.HasPrefix(lower, "tool execution failed:") ||
		strings.HasPrefix(lower, "failed:") ||
		strings.HasPrefix(lower, "错误:") ||
		strings.HasPrefix(lower, "错误：") ||
		strings.HasPrefix(lower, "未知工具:") ||
		strings.HasPrefix(lower, "未知工具：") ||
		strings.HasPrefix(lower, "执行失败:") ||
		strings.HasPrefix(lower, "执行失败：") ||
		strings.HasPrefix(lower, "调用失败:") ||
		strings.HasPrefix(lower, "调用失败：")
}

func anthropicBlocksFromMessage(m legacyopenai.ChatCompletionMessage) []anthropic.ContentBlockParamUnion {
	blocks := []anthropic.ContentBlockParamUnion{}
	hasMultiText := false
	for _, part := range m.MultiContent {
		if part.Type == legacyopenai.ChatMessagePartTypeText && strings.TrimSpace(part.Text) != "" {
			hasMultiText = true
			break
		}
	}
	if !hasMultiText && strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Content))
	}
	for _, part := range m.MultiContent {
		switch part.Type {
		case legacyopenai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				blocks = append(blocks, anthropic.NewTextBlock(part.Text))
			}
		case legacyopenai.ChatMessagePartTypeImageURL:
			if part.ImageURL == nil {
				continue
			}
			mediaType, data, ok := splitImageDataURL(part.ImageURL.URL)
			if !ok {
				continue
			}
			blocks = append(blocks, anthropic.NewImageBlock(anthropic.Base64ImageSourceParam{
				MediaType: anthropic.Base64ImageSourceMediaType(mediaType),
				Data:      data,
			}))
		}
	}
	return blocks
}

func convertToolsToAnthropic(tools []legacyopenai.Tool) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		t := anthropic.ToolParam{
			Name:        tool.Function.Name,
			InputSchema: anthropicInputSchema(schemautil.Map(tool.Function.Parameters)),
		}
		if strings.TrimSpace(tool.Function.Description) != "" {
			t.Description = anthropic.String(tool.Function.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &t})
	}
	return out
}

func anthropicInputSchema(schema map[string]any) anthropic.ToolInputSchemaParam {
	result := anthropic.ToolInputSchemaParam{
		// The Messages API requires input_schema.type = "object". The SDK field
		// would otherwise serialize empty, which gateways reject with 400
		// "tools.N.custom.input_schema.type: Field required".
		Type: "object",
	}
	if props, ok := schema["properties"]; ok {
		result.Properties = props
	} else {
		result.Properties = map[string]any{}
	}
	result.Required = schemautil.StringSlice(schema["required"])
	result.ExtraFields = map[string]any{}
	for key, value := range schema {
		switch key {
		case "type", "properties", "required":
			continue
		default:
			result.ExtraFields[key] = value
		}
	}
	if len(result.ExtraFields) == 0 {
		result.ExtraFields = nil
	}
	return result
}

// anthropicUsageState accumulates the raw Anthropic usage counters across
// message_start / message_delta events. message_delta.usage fields are
// cumulative: a present field overwrites, an absent field keeps the previous
// value — never add (that would double count). Kimi-code overwrites each
// present field (anthropic.ts:872-888) and pi keeps an accumulator and
// recomputes (anthropic-messages.ts:716-743); keeping the raw counters here
// lets every field merge by presence even though the shared modelUsage only
// stores derived hit/miss totals. Presence uses the SDK's respjson.Field
// because a Go zero value cannot distinguish "0 tokens" from "field absent".
type anthropicUsageState struct {
	seen          bool
	input         int64
	output        int64
	cacheRead     int64
	cacheCreation int64
}

func (s *anthropicUsageState) mergeStart(usage anthropic.Usage) {
	if s == nil {
		return
	}
	s.seen = true
	s.input = usage.InputTokens
	s.output = usage.OutputTokens
	s.cacheRead = usage.CacheReadInputTokens
	s.cacheCreation = usage.CacheCreationInputTokens
}

func (s *anthropicUsageState) mergeDelta(usage anthropic.MessageDeltaUsage) {
	if s == nil {
		return
	}
	s.seen = true
	if usage.OutputTokens > 0 {
		s.output = usage.OutputTokens
	}
	if usage.JSON.InputTokens.Valid() {
		s.input = usage.InputTokens
	}
	if usage.JSON.CacheReadInputTokens.Valid() {
		s.cacheRead = usage.CacheReadInputTokens
	}
	if usage.JSON.CacheCreationInputTokens.Valid() {
		s.cacheCreation = usage.CacheCreationInputTokens
	}
}

func (s *anthropicUsageState) modelUsage() *modelUsage {
	if s == nil || !s.seen {
		return nil
	}
	input := s.input + s.cacheCreation + s.cacheRead
	if input <= 0 && s.output <= 0 {
		return nil
	}
	return &modelUsage{
		PromptTokens:     int(input),
		CompletionTokens: int(s.output),
		CacheHitTokens:   int(s.cacheRead),
		CacheMissTokens:  int(s.input + s.cacheCreation),
		CacheWriteTokens: int(s.cacheCreation),
	}
}
