// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ally-dev/internal/tools/schemautil"

	legacyopenai "github.com/sashabaranov/go-openai"
)

func (a *App) streamOpenAIChat(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	clientCfg := legacyopenai.DefaultConfig(cfg.APIKey)
	clientCfg.BaseURL = baseURLForAPIFormat(cfg)
	clientCfg.HTTPClient = modelHTTPClient(cfg, true, 0)
	// Request rewrite: the Chat adapter always installs the rewriting transport,
	// because two of its normalizations apply to every request — `content` must
	// be a present key on tool-call/tool messages (see
	// chatRequestRewriteTransport) and OpenRouter-style sticky-session headers
	// are attached there. The reasoning backfill (an explicit, possibly empty
	// reasoning field on every assistant message) is written for every endpoint
	// except the official OpenAI API, and only while the selected level leaves
	// thinking on: without it DeepSeek V4 / Kimi K3 reject a request whose
	// assistant messages omit the field, while "off" has nothing to hand back
	// (chatReasoningBackfillKey).
	replayKey := reasoningReplayKey(cfg, model)
	var turnDetails map[string]json.RawMessage
	if payload := a.reasoningStash.get(replayKey); payload != nil {
		turnDetails = payload.chatDetailsByCallID()
	}
	reasoningKey := chatReasoningBackfillKey(cfg)
	// "off" reaches a compatible endpoint as the stop-thinking field the body
	// rewrite adds below; the official endpoint spells it as an effort value
	// instead (see reasoningWireForAdapter).
	reasoningWire := reasoningWireForAdapter(cfg, apiFormatOpenAIChat, cfg.ReasoningEffort)
	disableThinking := reasoningWire.DisableThinking && modelSupportsReasoningEffort(cfg, model)
	streamDone := &sseDoneWatcher{}
	base := modelHTTPClient(cfg, true, 0)
	rt := base.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	base.Transport = &chatRequestRewriteTransport{
		base:            rt,
		reasoningKey:    reasoningKey,
		turnDetails:     turnDetails,
		headers:         sessionAffinityHeaders(cfg),
		promptCacheKey:  openAIChatPromptCacheKey(cfg),
		streamDone:      streamDone,
		disableThinking: disableThinking,
	}
	clientCfg.HTTPClient = base
	client := legacyopenai.NewClientWithConfig(clientCfg)

	streamReq := legacyopenai.ChatCompletionRequest{
		Model:         model,
		Messages:      messages,
		StreamOptions: &legacyopenai.StreamOptions{IncludeUsage: true},
	}
	// Route the token limit to the field the target provider accepts. Both
	// fields are `omitempty`, so only the selected one is serialized — never
	// both. "auto" automatically routes to max_completion_tokens for OpenAI
	// o-series and newer models (which reject max_tokens with a 400 error),
	// and uses max_tokens for other models. Explicit "max_tokens" or
	// "max_completion_tokens" overrides auto-detection.
	if shouldUseMaxCompletionTokens(cfg.TokenParam, model) {
		streamReq.MaxCompletionTokens = cfg.MaxTokens
	} else {
		streamReq.MaxTokens = cfg.MaxTokens
	}
	// Thinking strength: send an effort value only when a level was picked AND
	// the model accepts the parameter. The normalized selection is sent
	// unchanged — xhigh and max are declared values of the OpenAI SDK enum
	// (shared.ReasoningEffortXhigh / ReasoningEffortMax) — while "auto" and
	// non-reasoning models send nothing (see modelSupportsReasoningEffort and
	// reasoningWireForAdapter).
	if reasoningWire.Effort != "" && modelSupportsReasoningEffort(cfg, model) {
		streamReq.ReasoningEffort = reasoningWire.Effort
	}
	if len(tools) > 0 {
		streamReq.Tools = normalizeToolsForOpenAIChat(tools)
	}
	// Note: Do NOT set streamReq.ToolChoice = "none" when len(tools) == 0.
	// The official OpenAI Chat Completions API specification (and compatible
	// gateways like Azure, Groq, DeepSeek) strictly rejects requests with
	// HTTP 400 ("'tool_choice' cannot be set without 'tools'") when tools is
	// omitted or empty. When no tools are provided, models cannot execute tools anyway.

	maxRetries := effectiveLLMRetries(cfg)
	result, emitted, err := a.openAIChatStreamAttempt(ctx, cfg, client, streamReq, streamDone, onEvent)
	// 只重试"尚未产出任何输出"的失败:建流失败,或消费阶段在产出内容前
	// 失败(中转常以 HTTP 200 建流,再以流内 {"error":...} 事件返回 529
	// overloaded 之类瞬时错误,错误信息只有文案、不带状态码)。此时重试
	// 无重复输出风险;已产出内容的中断交给上层 runChat 做整轮重试。
	for attempt := 1; err != nil && !emitted && ctx.Err() == nil && attempt <= maxRetries && shouldRetryLLMError(err); attempt++ {
		wait := llmRetryDelay(attempt)
		emitLLMRetryEvent(onEvent, attempt, maxRetries, err, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		result, emitted, err = a.openAIChatStreamAttempt(ctx, cfg, client, streamReq, streamDone, onEvent)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// openAIChatStreamAttempt 执行一次完整的 OpenAI Chat 建流与消费。第二个
// 返回值报告本次尝试是否已产出输出(assistant/reasoning/toolCalls 任一非空),
// 调用方据此判定适配器内重试是否安全(无输出则重试不会造成重复)。
func (a *App) openAIChatStreamAttempt(ctx context.Context, cfg ConfigState, client *legacyopenai.Client, streamReq legacyopenai.ChatCompletionRequest, streamDone *sseDoneWatcher, onEvent func(modelStreamEvent)) (*modelStreamResult, bool, error) {
	// The terminator watcher is owned by the caller and therefore survives this
	// attempt's retries. A `[DONE]` that a failed attempt read off the wire must
	// not be inherited by the next one: the retry's connection can be cut
	// mid-stream, and a stale terminator would make that truncated response pass
	// the {finish_reason, [DONE]} check and be persisted as a complete turn.
	streamDone.reset()
	stream, err := client.CreateChatCompletionStream(ctx, streamReq)
	if err != nil && isStreamOptionsRejectedError(err) {
		streamReq.StreamOptions = nil
		log.Printf("[llm] gateway rejected stream_options; retrying without it: %v", err)
		stream, err = client.CreateChatCompletionStream(ctx, streamReq)
	}
	if err != nil {
		return nil, false, err
	}
	defer stream.Close()

	var assistant strings.Builder
	var reasoning strings.Builder
	// mergedDetails accumulates OpenRouter-style reasoning_details across the
	// stream so the next request of this tool loop can replay them.
	mergedDetails := []map[string]any{}
	// reasoningPresent: the provider explicitly emitted a reasoning field
	// (possibly empty). go-openai unmarshals an absent field and an explicit
	// empty string identically, so this only turns true once non-empty
	// reasoning text arrives; the wire field for the next request is filled by
	// the rewrite transport instead (see chatReasoningBackfillKey).
	reasoningPresent := false
	var reasoningState struct {
		tag      string
		openTag  string
		closeTag string
		inTag    bool
		partial  string
	}
	if cfg.ReasoningTag != "" && cfg.ReasoningTag != "reasoning_content" {
		reasoningState.tag = cfg.ReasoningTag
		reasoningState.openTag = "<" + cfg.ReasoningTag + ">"
		reasoningState.closeTag = "</" + cfg.ReasoningTag + ">"
	}
	// toolAcc 把流式增量归入对应调用;它必须在整个流期间保持存活,否则只带
	// index、不带 id 的增量无法跨块匹配。id 表先由对话历史播种:中转跨回合复用
	// 同一个 id 时会被改写成唯一 id,而不是让下一个请求带上重复的 tool_call id。
	toolAcc := newToolCallAccumulator(nil)
	toolAcc.seedConversation(streamReq.Messages)
	toolCalls := []legacyopenai.ToolCall{}
	toolEventGate := newModelToolCallEventGate(func(event modelStreamEvent) {
		emitModelStreamEvent(onEvent, event)
	})
	var usage *modelUsage
	gotFinishReason := false
	stopReason := ""
	hasOutput := func() bool {
		return assistant.Len() > 0 || reasoning.Len() > 0 || len(toolCalls) > 0
	}
	for {
		raw, err := stream.RecvRaw()
		if errors.Is(err, io.EOF) {
			// 终止判定与另外两个适配器一致:必须收到显式终止标记。go-openai 把
			// `[DONE]` 与"连接在帧边界被切断"归一成同一个 io.EOF,所以这里同时
			// 接受真实的 `[DONE]`(由 sseDoneReadCloser 记录)与 finish_reason;
			// 两者都没有就是半截流。已产出内容时返回 hasOutput()=true,适配器内
			// 不再重试（避免重复输出）,由上层 runChat 整轮重试。
			if !gotFinishReason && (streamDone == nil || !streamDone.Done()) {
				return nil, hasOutput(), errChatStreamNoFinishReason
			}
			break
		}
		if err != nil {
			// 流内错误事件(如中转的 529 overloaded):错误信息只有文案、
			// 不带状态码。是否重试由调用方按"是否已产出输出"统一判定。
			return nil, hasOutput(), err
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var resp legacyopenai.ChatCompletionStreamResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, hasOutput(), wrapLLMStreamEventDecode(fmt.Errorf("decode chat stream event: %w", err))
		}
		if resp.Usage != nil {
			usage = modelUsageFromLegacy(resp.Usage, raw)
		} else if len(resp.Choices) > 0 && len(raw) > 0 {
			if choiceUsage := extractChoiceUsageFromRaw(raw); choiceUsage != nil {
				usage = modelUsageFromLegacy(choiceUsage, raw)
			}
		}
		if len(resp.Choices) == 0 {
			continue
		}
		delta := resp.Choices[0].Delta
		if resp.Choices[0].FinishReason != "" {
			gotFinishReason = true
			stopReason = string(resp.Choices[0].FinishReason)
		}
		// Structured reasoning (OpenRouter reasoning_details, vLLM reasoning)
		// arrives in every chunk: the text feeds the thinking panel, the detail
		// objects are merged for replay on the next request of this loop.
		streamReasoningText, streamReasoningDetails := parseStreamReasoning(raw)
		for _, detail := range streamReasoningDetails {
			mergedDetails = appendChatReasoningDetail(mergedDetails, detail)
		}
		if reasoningState.tag != "" {
			// Parse content-level reasoning tags embedded in delta.Content
			// (e.g. <sink>...</sink> or any configured <tag>...</tag>).
			text := delta.Content
			if reasoningState.partial != "" {
				text = reasoningState.partial + text
				reasoningState.partial = ""
			}
			remaining := text
			for len(remaining) > 0 {
				if reasoningState.inTag {
					// Look for close tag.
					idx := strings.Index(remaining, reasoningState.closeTag)
					if idx >= 0 {
						reasoning.WriteString(remaining[:idx])
						emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: remaining[:idx]})
						remaining = remaining[idx+len(reasoningState.closeTag):]
						reasoningState.inTag = false
					} else {
						// Check if remaining ends with a partial close tag.
						overlap := partialTagMatch(remaining, reasoningState.closeTag)
						if overlap > 0 {
							reasoning.WriteString(remaining[:len(remaining)-overlap])
							emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: remaining[:len(remaining)-overlap]})
							reasoningState.partial = remaining[len(remaining)-overlap:]
							remaining = ""
						} else {
							reasoning.WriteString(remaining)
							emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: remaining})
							remaining = ""
						}
					}
				} else {
					// Look for open tag.
					idx := strings.Index(remaining, reasoningState.openTag)
					if idx >= 0 {
						if idx > 0 {
							assistant.WriteString(remaining[:idx])
							emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: remaining[:idx]})
						}
						remaining = remaining[idx+len(reasoningState.openTag):]
						reasoningState.inTag = true
					} else {
						// Check if remaining ends with a partial open tag.
						overlap := partialTagMatch(remaining, reasoningState.openTag)
						if overlap > 0 && overlap < len(reasoningState.openTag) {
							assistant.WriteString(remaining[:len(remaining)-overlap])
							emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: remaining[:len(remaining)-overlap]})
							reasoningState.partial = remaining[len(remaining)-overlap:]
							remaining = ""
						} else {
							assistant.WriteString(remaining)
							emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: remaining})
							remaining = ""
						}
					}
				}
			}
		} else {
			// Original behavior: use delta.Content and delta.ReasoningContent separately.
			if delta.Content != "" {
				assistant.WriteString(delta.Content)
				emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: delta.Content})
			}
			reasoningText := delta.ReasoningContent
			if reasoningText == "" {
				reasoningText = streamReasoningText
			}
			if reasoningText != "" {
				reasoning.WriteString(reasoningText)
				reasoningPresent = true
				emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: reasoningText})
			}
		}
		if len(delta.ToolCalls) > 0 {
			toolCalls = toolAcc.merge(delta.ToolCalls)
			toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
		}
	}

	// Flush any residual partial tag content left in the streaming parser.
	if reasoningState.tag != "" && reasoningState.partial != "" {
		if reasoningState.inTag {
			reasoning.WriteString(reasoningState.partial)
			emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: reasoningState.partial})
		} else {
			assistant.WriteString(reasoningState.partial)
			emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: reasoningState.partial})
		}
	}

	// Record this response's OpenRouter-style reasoning details as one turn of
	// the session ledger, keyed by the turn's tool-call ids: every later
	// request replays them on this turn's own assistant message, so the
	// message prefix stays byte-identical across agent steps (the key is
	// model-scoped). A response without details records no turn; earlier
	// turns keep theirs.
	if detailsJSON := chatReasoningDetailsJSON(mergedDetails); len(detailsJSON) > 0 && len(toolCalls) > 0 {
		a.reasoningStash.appendTurn(reasoningReplayKey(cfg, streamReq.Model), reasoningTurn{
			callIDs:     toolCallIDsOf(toolCalls),
			chatDetails: detailsJSON,
		})
	}

	if notes := toolAcc.diagnostics(); len(notes) > 0 {
		log.Printf("[toolcall] %s", strings.Join(notes, "; "))
	}

	return &modelStreamResult{
		Content:          assistant.String(),
		Reasoning:        reasoning.String(),
		ReasoningPresent: reasoningPresent || reasoning.Len() > 0,
		ToolCalls:        normalizeToolCalls(toolCalls),
		Usage:            usage,
		StopReason:       stopReason,
	}, hasOutput(), nil
}

// isStreamOptionsRejectedError 判定建流失败是否因为网关不支持 stream_options
// 参数。这类网关在 400 报错文本里转述被拒绝的参数名；判定收紧为：400 语义
// + 文本提及该字段名。纯文本命中而没有任何 400 语义时不再降级，避免把无关
// 报错误判为参数被拒。文本来自 provider/中继，无法完全脱离字符串匹配。
func isStreamOptionsRejectedError(err error) bool {
	if err == nil || !isProvider400Error(err) {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "stream_options")
}

// openAIChatPromptCacheKey returns the session-sticky prompt_cache_key for a
// Chat Completions request, or "" when the field must not be sent. pi attaches
// it only when the request goes to api.openai.com (openai-completions.ts:810-814)
// — the official endpoint is the one place the field is documented, and a
// compatible gateway may answer an unknown top-level parameter with 400. The
// value is the same hashed session key the Responses adapter uses, so no raw
// session id leaves the client.
func openAIChatPromptCacheKey(cfg ConfigState) string {
	if !isOfficialOpenAIEndpoint(cfg) {
		return ""
	}
	return strings.TrimSpace(cfg.responsesPromptCacheKey)
}

func normalizeToolsForOpenAIChat(tools []legacyopenai.Tool) []legacyopenai.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]legacyopenai.Tool, len(tools))
	for i, t := range tools {
		out[i] = t
		if t.Function == nil {
			continue
		}
		// A function tool must always carry a parameters object: the field is
		// required by the API, and the Anthropic / Responses converters build the
		// same default through schemautil.Map. Routing all three adapters through
		// one path means a tool declared without parameters can never serialize
		// into a request that omits the key (schemautil.Map maps nil to
		// {"type":"object","properties":{}}).
		fn := *t.Function
		fn.Parameters = schemautil.Map(t.Function.Parameters)
		out[i].Function = &fn
	}
	return out
}

func extractChoiceUsageFromRaw(raw []byte) *legacyopenai.Usage {
	var chunk struct {
		Choices []struct {
			Usage *legacyopenai.Usage `json:"usage"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &chunk) == nil && len(chunk.Choices) > 0 && chunk.Choices[0].Usage != nil {
		return chunk.Choices[0].Usage
	}
	return nil
}

// modelUsageFromLegacy maps the OpenAI-compatible usage struct. DeepSeek returns
// cache counters as top-level prompt_cache_{hit,miss}_tokens (not parsed by the
// sashabaranov struct), and Moonshot (Kimi) returns top-level cached_tokens (or
// choices[0].usage.cached_tokens in streaming chunks), so raw is re-scanned to
// recover them when present; OpenAI/MiMo carry them nested under
// prompt_tokens_details.cached_tokens.
func modelUsageFromLegacy(usage *legacyopenai.Usage, raw []byte) *modelUsage {
	if usage == nil {
		return nil
	}
	hit, miss := 0, 0
	if usage.PromptTokensDetails != nil {
		hit = usage.PromptTokensDetails.CachedTokens
	}
	// DeepSeek 与 Moonshot 顶层字段（sashabaranov 不解析，从原始 JSON 补取）。
	// 支持顶层 usage 与 choices[0].usage（Moonshot 流式专有格式）。
	var extra struct {
		Usage *struct {
			PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
			CachedTokens          int `json:"cached_tokens"`
		} `json:"usage"`
		Choices []struct {
			Usage *struct {
				PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
				PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
				CachedTokens          int `json:"cached_tokens"`
			} `json:"usage"`
		} `json:"choices"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &extra) == nil {
		target := extra.Usage
		if target == nil && len(extra.Choices) > 0 {
			target = extra.Choices[0].Usage
		}
		if target != nil {
			if target.PromptCacheHitTokens > 0 || target.PromptCacheMissTokens > 0 {
				hit = target.PromptCacheHitTokens
				miss = target.PromptCacheMissTokens
			} else if target.CachedTokens > 0 {
				hit = target.CachedTokens
			}
		}
	}
	if miss == 0 && hit > 0 && usage.PromptTokens > hit {
		miss = usage.PromptTokens - hit
	}
	return &modelUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CacheHitTokens:   hit,
		CacheMissTokens:  miss,
	}
}
