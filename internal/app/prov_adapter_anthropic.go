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
	"regexp"
	"strconv"
	"strings"
	"time"

	"ally-dev/internal/tools/schemautil"
	"ally-dev/internal/tools/toolcall"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	legacyopenai "github.com/sashabaranov/go-openai"
)

func (a *App) streamAnthropicMessages(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	baseURL := baseURLForAPIFormat(cfg)
	// 「关闭思考」的落法：三条线共用同一套厂商判定（anthropicOffPlanFor）。这条端点的
	// 开关是顶层 reasoning.effort 时（DeepSeek），在请求体里补一个 JSON 键 —— SDK 没有
	// 这个字段（MessageNewParams 只带 Thinking / OutputConfig），键与值每次都一样，所以
	// 前缀仍然稳定。
	offPlan := anthropicOffPlanFor(cfg, model)
	// 关闭 SDK 内置重试,改用本模块统一的重试循环以便发出 run:retry 事件。
	clientOptions := []anthropicoption.RequestOption{
		anthropicoption.WithAPIKey(cfg.APIKey),
		anthropicoption.WithBaseURL(baseURL),
		anthropicoption.WithMaxRetries(0),
		anthropicoption.WithHTTPClient(modelHTTPClient(cfg, true, 0)),
	}
	if offPlan.reasoningEffort != "" {
		clientOptions = append(clientOptions, anthropicoption.WithJSONSet("reasoning", map[string]string{"effort": offPlan.reasoningEffort}))
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
	// system block, one on the last content block of the last message, plus the
	// top-level marker for the endpoints that only honour that form. The ttl field is
	// only written for the official endpoint (see
	// markAnthropicPromptCacheBreakpoints).
	markAnthropicPromptCacheBreakpoints(&params, cfg)
	// Thinking configuration comes from the endpoint-family table
	// (anthropicThinkingFamilies): Claude's adaptive models take
	// output_config.effort, Claude 3.7 / 4.5 the documented budget_tokens shape
	// (output_config.effort on 3.7 causes a 400), and the Anthropic-compatible
	// endpoints (DeepSeek, Kimi, 千问, MiniMax) whichever of the two they document.
	// "关闭思考" comes from offPlan above (see anthropicOffPlanFor).
	thinkingEnabled := configureAnthropicThinking(&params, model, cfg.ReasoningEffort, cfg.MaxTokens, offPlan)

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

// ── Anthropic extended thinking: one row per endpoint family ────────────────

// anthropicThinkingShape is the thinking block an endpoint documents. The three
// shapes are mutually exclusive by construction — a family cannot be both adaptive
// and unable to carry a thinking block — which is exactly the impossible combination
// separate booleans would have allowed.
type anthropicThinkingShape string

const (
	// anthropicShapeAdaptive: thinking {type: "adaptive"} with the level in
	// output_config.effort (Claude 4.6+, MiniMax).
	anthropicShapeAdaptive anthropicThinkingShape = "adaptive"
	// anthropicShapeEnabled: thinking {type: "enabled"} — the switch, with the budget
	// travelling in budget_tokens only where the family's docs declare that field
	// (Claude 3.7 / 4.5, DeepSeek, 智谱) and left out where they deprecate it (千问; see
	// switchOnly).
	anthropicShapeEnabled anthropicThinkingShape = "enabled"
	// anthropicShapeNone: the endpoint documents no thinking parameter at all, so the
	// level can only travel in output_config.effort (Kimi K3 always reasons and its
	// endpoint rejects the parameter).
	anthropicShapeNone anthropicThinkingShape = "none"
	// anthropicShapeUndocumented: the endpoint documents no thinking configuration on
	// any wire — neither a thinking block nor output_config.effort — so nothing is
	// written and the level cannot travel anywhere (MiMo).
	anthropicShapeUndocumented anthropicThinkingShape = "undocumented"
)

// anthropicThinkingFamily describes how one Anthropic-compatible endpoint spells
// extended thinking and where the selected level travels. The decision belongs to
// the endpoint family — the API the request actually lands on — and not to the shape
// of the model id: families disagree on the thinking block they accept and on the
// field that carries the level (output_config.effort or budget_tokens).
//
// The zero value means "no row matched", which configureAnthropicThinking reads as
// "use the Claude budget fallback".
type anthropicThinkingFamily struct {
	// name labels the row; the tests name an offending row with it.
	name string
	// matches reports whether a model id belongs to this family.
	matches func(model string) bool
	// shape is the thinking block this endpoint documents.
	shape anthropicThinkingShape
	// display is true when the family documents thinking.display: "summarized" is
	// what makes Claude stream its thinking text instead of answering with a
	// signature only. An endpoint whose docs never mention the field is not handed
	// one.
	display bool
	// switchOnly is true when the family documents the thinking switch but not the
	// budget (千问 marks budget_tokens 即将废弃 and points new integrations at
	// output_config.effort). Its `enabled` block then goes out as {type: "enabled"}
	// alone — written by anthropicEnabledThinking itself, because the SDK tags
	// budget_tokens as required and the typed param can never produce that shape.
	switchOnly bool
	// effort maps a canonical level onto this family's output_config.effort value,
	// or returns "" when the level must not be sent at all. An undocumented row carries
	// none: there is no field for it to fill.
	effort func(model, level string) string
}

// anthropicEffortVerbatim passes a canonical level through unchanged: the family
// accepts every level we can produce, so there is nothing to translate.
func anthropicEffortVerbatim(_, level string) string { return level }

// anthropicEffortFromVendorTable resolves the level through the shared vendor
// table (reasoningEffortFamilies) — the same one the Chat and Responses wires use.
// These vendors document the same enum on their Anthropic-compatible endpoint as on
// their OpenAI-compatible one, so the accepted levels, the documented translations
// (medium→high, …) and the out-of-range clamp are already written down there;
// repeating them here would be a second source of truth that can drift.
func anthropicEffortFromVendorTable(model, level string) string {
	return reasoningEffortForModel(model, level)
}

// anthropicThinkingFamilies is the per-endpoint-family table for extended thinking,
// most specific row first. A model that matches no row keeps the Claude budget
// shape (the fallback at the end of configureAnthropicThinking), because an
// unrecognized id on an Anthropic-protocol entry is most likely a relay serving a
// Claude model.
//
// Reading the spelling from this table instead of from the model id alone is what
// keeps a chosen level from being dropped: 智谱 / DeepSeek / Kimi / 千问 all serve an
// Anthropic-compatible endpoint, but they carry the level in output_config.effort —
// DeepSeek ignores budget_tokens outright, 千问 marks it deprecated, Kimi K3 refuses
// the thinking parameter entirely. Sending them budget_tokens only left the level
// out of the request: the provider ran its own default (usually the slowest and most
// expensive one) while the UI kept showing the selection.
var anthropicThinkingFamilies = []anthropicThinkingFamily{
	{
		// Claude 4.6+/5: adaptive thinking, the level in output_config.effort, and
		// display: "summarized" so the model streams its thinking text instead of a
		// signature-only ("omitted") response.
		name:    "claude-adaptive",
		matches: isAnthropicAdaptiveThinkingModel,
		shape:   anthropicShapeAdaptive,
		display: true,
		effort:  anthropicEffortVerbatim,
	},
	{
		// MiniMax documents thinking.type as `disabled` / `adaptive` only, so the
		// `enabled` + budget_tokens form is rejected there, and it carries the full
		// low/medium/high/xhigh/max effort enum. display is undocumented for it and
		// therefore left unset.
		name:    "minimax",
		matches: vendorModelPrefix("minimax"),
		shape:   anthropicShapeAdaptive,
		display: false,
		effort:  anthropicEffortVerbatim,
	},
	{
		// DeepSeek documents thinking (the endpoint accepts the block but ignores
		// budget_tokens) and carries the strength in output_config.effort
		// (low/high/max). display is not documented.
		name:    "deepseek",
		matches: vendorModelPrefix("deepseek"),
		shape:   anthropicShapeEnabled,
		effort:  anthropicEffortFromVendorTable,
	},
	{
		// Kimi / Moonshot：这个 Messages 端点只服务它当前的推理模型（doc Kimi §4 的 model
		// enum 只有一个型号），那代模型始终推理、不接受 thinking 参数，所以只写
		// output_config.effort（low/high/max，见 reasoningEffortFamilies 的 moonshot 行）。
		// 按厂商（而不是型号）判：端点自己已经把可服务的模型限定在当前一代。
		name:    "moonshot",
		matches: vendorModelPrefix("kimi", "moonshot"),
		shape:   anthropicShapeNone,
		effort:  anthropicEffortFromVendorTable,
	},
	{
		// 千问 documents thinking {type: "enabled"/"disabled"} and the full
		// output_config.effort enum, so the level goes out verbatim; display is not
		// documented. budget_tokens 有写但已标"即将废弃，新接入建议改用 output_config.effort"，
		// 所以开关照发、预算不发（switchOnly）：文档没要求两者同时出现，而继续发一个它宣告要
		// 移除的字段，只会在它真移除的那天换回 400。
		name:       "qwen",
		matches:    vendorModelPrefix("qwen"),
		shape:      anthropicShapeEnabled,
		switchOnly: true,
		effort:     anthropicEffortVerbatim,
	},
	{
		// 智谱 serves three protocols from one host（Chat / Responses / Anthropic，
		// docs/provider-api-fields.md 智谱 §12.4）and carries the thinking strength in
		// reasoning_effort / output_config.effort; budget_tokens is not a 智谱 field
		// anywhere. Its own Anthropic page publishes no field-level list, so the request
		// keeps the documented thinking block and adds the level in the one field the
		// vendor does document — the Claude fallback left the level out of the body
		// entirely and let 智谱 run its own default (max). The row is keyed on the vendor,
		// so every generation carries the level; an older one whose docs predate the field
		// receives a key it does not declare — the trade the effort table spells out.
		// display is not a 智谱 field either. budget_tokens 留在块里：文档对它这个端点没给
		// 字段清单，而这条线是 Claude 形状的端点 —— Anthropic 自家 schema 把 budget_tokens 标成
		// 必填（SDK 的 api:"required"），这个端点的用途正是让 Claude 形状的客户端接进来，所以
		// 「按 Anthropic schema 校验」比「严格拒绝未知字段」更可能；删掉它只在后者那种端点上更
		// 安全，留着则在前者那种端点上更安全（一个对方不认识的键换来的是整个请求 400）。
		name:    "zhipu",
		matches: vendorModelPrefix("glm"),
		shape:   anthropicShapeEnabled,
		effort:  anthropicEffortFromVendorTable,
	},
	{
		// MiMo documents no thinking configuration on either wire: its Messages parameter
		// table is model / messages / max_tokens / system / temperature / top_p / stream /
		// stop_sequences and its §6 states that no reasoning_effort / thinking /
		// enable_thinking field exists on the site (it does document reasoning_content on
		// the response side, which needs no request field). Writing a block or an
		// output_config there would only add keys the endpoint never declared.
		name:    "mimo",
		matches: vendorModelPrefix("mimo"),
		shape:   anthropicShapeUndocumented,
	},
}

// anthropicThinkingFamilyFor returns the family row classifying this model. The zero
// value means no row matched, which configureAnthropicThinking reads as "use the
// Claude budget fallback".
func anthropicThinkingFamilyFor(model string) anthropicThinkingFamily {
	for _, family := range anthropicThinkingFamilies {
		if family.matches != nil && family.matches(model) {
			return family
		}
	}
	return anthropicThinkingFamily{}
}

// anthropicAdaptiveMajor / anthropicAdaptiveMinor 是第一个支持 adaptive thinking 的 Claude
// 世代：4.6 引入 adaptive，而从 4.7 起手写形态（thinking.type=enabled + budget_tokens）直接
// 400；所以这既是「能自适应」的起点，也是「只能自适应」的起点。
const (
	anthropicAdaptiveMajor = 4
	anthropicAdaptiveMinor = 6
)

// isAnthropicAdaptiveThinkingModel reports whether this id names a Claude generation whose
// extended thinking is the adaptive shape. It is the "claude-adaptive" row of
// anthropicThinkingFamilies, i.e. which Claude ids skip the budget fallback.
//
// 判据读的是版本号（anthropicAdaptiveMajor/Minor），而不是一张型号清单：清单只回答「写它那天
// 已经存在的模型」，Anthropic 的下一代就会落进 budget 兜底、直接 400 —— 而那恰好是丢掉手写形
// 态的那一代。型号名外面的包装（us.anthropic.claude-opus-4-6-v1 这类 Bedrock 名、
// claude-opus-4-6@eu 这类区域名）已在 baseModelName 里去掉，版本号的两种书写顺序由
// claudeVersion 读。
func isAnthropicAdaptiveThinkingModel(model string) bool {
	m := baseModelName(model)
	// 认 claude 这个品牌：这条线上还有别家的行（智谱 glm-、DeepSeek、Kimi、千问、MiniMax、
	// MiMo），其中带数字的名字不能被读成 Claude 的版本号。
	i := strings.Index(m, "claude")
	if i < 0 {
		return false
	}
	major, minor, ok := claudeVersion(m[i:])
	if !ok {
		// 读不出版本号（claude-fable / claude-mythos 这类无版本别名）指向当前一代，而当前这一代
		// 正是只接受 adaptive 的那代，所以按 adaptive 处理。
		return true
	}
	return major > anthropicAdaptiveMajor ||
		(major == anthropicAdaptiveMajor && minor >= anthropicAdaptiveMinor)
}

// claudeVersionPattern 读 Claude 型号名里的世代号：第一段数字是主版本，"." / "-" 后面的单个
// 数字是次版本（4.6 / 3.7 / 5.1）；那后面若是一长串数字则是日期戳而不是次版本 ——
// claude-sonnet-4-20250514 是 4.0，所以次版本只认一位数字。
var claudeVersionPattern = regexp.MustCompile(`([0-9]+)(?:[.-]([0-9])(?:[^0-9]|$))?`)

// claudeVersion 从（claude 品牌起的）型号名里读出世代号，ok=false 表示这段里没有版本号。
func claudeVersion(id string) (major, minor int, ok bool) {
	match := claudeVersionPattern.FindStringSubmatch(id)
	if match == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(match[1])
	if match[2] != "" {
		minor, _ = strconv.Atoi(match[2])
	}
	return major, minor, true
}

// anthropicInterleavedThinkingBeta returns the beta flag Anthropic requires for
// extended thinking to continue between tool calls inside one assistant turn. pi
// sends interleaved-thinking-2025-05-14 for any thinking request on a non-adaptive
// model (anthropic-messages.ts betas); Ally limits it to tool turns, which is where
// interleaving can actually happen.
func anthropicInterleavedThinkingBeta(thinkingEnabled, hasTools bool, model string) string {
	if !thinkingEnabled || !hasTools {
		return ""
	}
	// The decision belongs to the same table that spells the thinking block for this
	// request: an adaptive family interleaves by default, and a family that writes no
	// thinking block (Kimi K3) or no thinking configuration at all (MiMo) gives the flag
	// nothing to apply to — the header would be meaningless there.
	family := anthropicThinkingFamilyFor(model)
	if family.shape == anthropicShapeAdaptive || family.shape == anthropicShapeNone || family.shape == anthropicShapeUndocumented {
		return ""
	}
	return "interleaved-thinking-2025-05-14"
}

// anthropicThinkingBudget resolves the budget_tokens value for the `enabled` shape:
// the API requires 1024 <= budget_tokens < max_tokens, and the thinking budget shares
// the max_tokens ceiling with the visible answer, so a budget is capped to leave the
// answer room that is always kept free (pi: clampThinkingBudgetToAnswerRoom with
// MIN_ANSWER_TOKENS = 1024, simple-options.ts:64-72). ok=false means the configured
// cap cannot satisfy both the floor and the reserved answer room — there is no valid
// extended-thinking configuration then, so the caller leaves thinking unset instead
// of sending a request the API rejects.
func anthropicThinkingBudget(effort string, maxTokens int) (int64, bool) {
	if maxTokens < minAnthropicThinkingBudget+minAnthropicAnswerTokens {
		return 0, false
	}
	budget := int64(4096)
	switch effort {
	case "low":
		budget = 1024
	case "medium":
		budget = 4096
	case "high", "xhigh", "max":
		budget = 32000
	}
	if room := int64(maxTokens - minAnthropicAnswerTokens); budget > room {
		budget = room
	}
	return budget, true
}

// anthropicEnabledThinking builds the `enabled` thinking block for the shapes that
// carry one, and reports whether the configured output cap can hold it (see
// anthropicThinkingBudget). display is written only where the family documents the
// field; a switchOnly family gets the block without budget_tokens, and needs no budget
// resolved for it — the floor and the reserved answer room exist only for the shape
// that actually sends one.
func anthropicEnabledThinking(effort string, maxTokens int, display, switchOnly bool) (anthropic.ThinkingConfigParamUnion, bool) {
	if switchOnly {
		return anthropicEnabledSwitchOnlyBlock(display), true
	}
	budget, ok := anthropicThinkingBudget(effort, maxTokens)
	if !ok {
		return anthropic.ThinkingConfigParamUnion{}, false
	}
	enabledParam := &anthropic.ThinkingConfigEnabledParam{Type: "enabled", BudgetTokens: budget}
	if display {
		enabledParam.Display = anthropic.ThinkingConfigEnabledDisplaySummarized
	}
	return anthropic.ThinkingConfigParamUnion{OfEnabled: enabledParam}, true
}

// anthropicEnabledSwitchOnlyBlock returns the `enabled` thinking block without
// budget_tokens, for the families whose docs document the switch but deprecate the
// budget (千问). The SDK tags budget_tokens as required, so the typed param always
// serializes one; param.Override carries these bytes instead. They are written out
// verbatim — the same bytes on every call, so the provider's prompt-cache prefix stays
// stable — and display follows the same rule as the typed shape.
func anthropicEnabledSwitchOnlyBlock(display bool) anthropic.ThinkingConfigParamUnion {
	raw := json.RawMessage(`{"type":"enabled"}`)
	if display {
		raw = json.RawMessage(`{"display":"summarized","type":"enabled"}`)
	}
	return param.Override[anthropic.ThinkingConfigParamUnion](raw)
}

// anthropicOffPlan is the Messages wire's answer for the "关闭思考" level. The decision
// itself belongs to the shared vendor tables (reasoningOffHasNoEffect and
// reasoningEffortFamily.offIsSubstitute); this struct only names the field that carries
// it on this wire, so the adapter and the request-body addition cannot disagree.
type anthropicOffPlan struct {
	// disabledThinking writes thinking: {"type": "disabled"} — what most endpoints
	// document as the switch（千问 §4 的 thinking.type、GLM-5.2 的 thinking.type=disabled）。
	disabledThinking bool
	// reasoningEffort is the value for the top-level `reasoning` object, which the SDK
	// has no field for (see isDeepSeekAnthropicEndpoint).
	reasoningEffort string
	// outputEffort is the value for output_config.effort, used when the model cannot
	// stop thinking: its lowest documented level（Kimi K3 / GLM-5.3 / MiniMax M3.1）。
	outputEffort string
}

// anthropicOffPlanFor resolves the "关闭思考" level for one Messages endpoint. It is the
// single source of that decision on this wire: the caller adds the JSON key from the
// plan and configureAnthropicThinking writes the thinking configuration from it.
func anthropicOffPlanFor(cfg ConfigState, model string) anthropicOffPlan {
	switch {
	case reasoningOffHasNoEffect(model):
		// 这家站点根本没有这个开关（MiMo）：写出去只是发一个对方没声明的键，模型照旧思考。
		// 还有档位可退的厂商不走这里（下面 offIsSubstitute 那条）。
		return anthropicOffPlan{}
	case isDeepSeekAnthropicEndpoint(cfg):
		return anthropicOffPlan{reasoningEffort: reasoningEffortOffWireValue}
	}
	if family, ok := reasoningEffortFamilyFor(model); ok && family.offIsSubstitute() {
		return anthropicOffPlan{outputEffort: family.offWireValue()}
	}
	return anthropicOffPlan{disabledThinking: true}
}

// configureAnthropicThinking applies the thinking configuration for the selected
// effort level and reports whether this request carries a thinking configuration for
// the model (the caller uses that to decide on the interleaved-thinking beta). It
// returns false for an explicit "off" and for "auto": auto sends no thinking field
// at all, so the model's own default applies.
func configureAnthropicThinking(params *anthropic.MessageNewParams, model, rawEffort string, maxTokens int, off anthropicOffPlan) bool {
	raw := strings.ToLower(strings.TrimSpace(rawEffort))
	if normalizeReasoningEffort(raw) == reasoningEffortOff {
		// 用户点了「关闭」，这个要求就交给端点 —— 但写法用这家能接受的那一种
		// (anthropicOffPlanFor)。DeepSeek 的端点用 reasoning.effort，那个键已由调用方补进
		// 请求体，这里不再写 thinking 块。剩下的（含 kimi-k2.7-code 这类只收 enabled 的
		// 模型）照实写 thinking:{"type":"disabled"}：端点按自己的契约报 400，用户看到原文
		// 后自己换档位，比静默改档更诚实。
		switch {
		case off.reasoningEffort != "":
		case off.outputEffort != "":
			params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(off.outputEffort)}
		case off.disabledThinking:
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{Type: "disabled"}}
		}
		return false
	}
	effort := normalizeReasoningEffort(rawEffort)
	if effort == "" || effort == reasoningEffortAuto {
		return false
	}
	family := anthropicThinkingFamilyFor(model)
	switch family.shape {
	case anthropicShapeUndocumented:
		// 这家文档里没有思考配置（MiMo）:块和 output_config 都不在它的参数表里,档位无处
		// 可去,一个字段都不发 —— 发出去只是多一个对方没声明的键。
		return false
	case anthropicShapeAdaptive:
		adaptiveParam := &anthropic.ThinkingConfigAdaptiveParam{Type: "adaptive"}
		if family.display {
			adaptiveParam.Display = anthropic.ThinkingConfigAdaptiveDisplaySummarized
		}
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: adaptiveParam}
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(family.effort(model, effort))}
		return true
	case anthropicShapeEnabled, anthropicShapeNone:
		// Both carry the level in output_config.effort. An empty value means the vendor
		// table has no row for this model, i.e. it declares no effort field at all: the
		// level is dropped, since a value the vendor never declared only buys a 400 — the
		// same rule the Chat and Responses wires follow. A shape that documents a thinking
		// block still writes that block: what the endpoint accepts and which level it
		// declares are two separate questions (智谱 takes its vendor row on every
		// generation, so its block always comes with the level; Kimi has no block to fall
		// back on, so an empty value means nothing is sent at all).
		value := ""
		if family.effort != nil {
			value = family.effort(model, effort)
		}
		if value != "" {
			params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(value)}
		} else if family.shape == anthropicShapeNone {
			return false
		}
		if family.shape == anthropicShapeNone {
			return true
		}
		thinking, ok := anthropicEnabledThinking(effort, maxTokens, family.display, family.switchOnly)
		if !ok {
			log.Printf("[llm] max_tokens=%d is too small for %s extended thinking; leaving thinking unset", maxTokens, family.name)
			return false
		}
		params.Thinking = thinking
		return true
	}
	// Fallback: the Claude budget shape, which is what the official API and the
	// Claude-compatible relays that copy it accept. Passing output_config.effort here
	// would be a 400 on Claude 3.7.
	thinking, ok := anthropicEnabledThinking(effort, maxTokens, true, false)
	if !ok {
		log.Printf("[llm] max_tokens=%d is too small for Anthropic extended thinking; leaving thinking unset", maxTokens)
		return false
	}
	params.Thinking = thinking
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
			merged := append(out[len(out)-1].Content, blocks...)
			if role == anthropic.MessageParamRoleUser {
				merged = anthropicOrderUserBlocks(merged)
			}
			out[len(out)-1].Content = merged
			return
		}
		if role == anthropic.MessageParamRoleUser {
			out = append(out, anthropic.NewUserMessage(anthropicOrderUserBlocks(blocks)...))
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
// Anthropic allows up to 4 breakpoints; 3 are used (4 where the top-level marker
// is spelled out too, which is exactly the limit).
//
// The top-level marker is written only for the endpoints that document that form
// (anthropicTopLevelCacheControlEndpoint): Moonshot's Messages endpoint documents
// cache_control as 仅顶层传入时生效、不传时本次请求只尝试读取缓存（5m 档）不写入 ——
// 只带嵌套标记时它一个缓存条目都不写，提示词缓存就这样静默关了。其余兼容平台把标记声明在
// system / tools / 内容块里（MiniMax、千问），或干脆忽略 cache_control（DeepSeek），
// 多发一个顶层键只是发了对方没要的字段；智谱全章没有 cache_control 字段（§9.1 隐式缓存、
// 智能识别重复上下文，无需手动配置），那三个嵌套标记对它也是空转。官方端点只带嵌套标记：
// 顶层标记在那边只是重复最后一个块的那个。
//
// The ttl is written out only for the official endpoint: "5m" is the
// documented default, so omitting it leaves the cache lifetime identical
// everywhere else, while a compatible gateway keeps receiving only the fields
// it knows. Every marker is always built through the SDK constructor,
// because `cache_control` is tagged omitzero and a zero-value
// CacheControlEphemeralParam (empty type AND empty ttl) is dropped from the
// request altogether — that would silently lose the breakpoint instead of
// sending one without a ttl.
func markAnthropicPromptCacheBreakpoints(params *anthropic.MessageNewParams, cfg ConfigState) {
	officialEndpoint := isOfficialAnthropicEndpoint(cfg)
	cc := anthropic.NewCacheControlEphemeralParam()
	if officialEndpoint {
		cc.TTL = anthropic.CacheControlEphemeralTTLTTL5m
	} else if anthropicTopLevelCacheControlEndpoint(cfg) {
		params.CacheControl = cc
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

// anthropicTopLevelCacheControlEndpoint reports whether this endpoint documents the
// top-level cache_control form. Moonshot's Messages endpoint is the one that states it
// outright: 上下文缓存写入选项…仅顶层传入时生效，messages 消息体内的 cache_control
// 标记会被忽略；不传时本次请求只尝试读取缓存（5m 档）不写入 (docs/provider-api-fields.md
// Kimi §4) — a request without it writes no cache entry at all, which is why the block
// markers alone left prompt caching switched off there. The other compatible platforms
// document the marker inside system / tools / content blocks instead (MiniMax, 千问) or
// ignore cache_control outright (DeepSeek), so nothing extra is added for them; 智谱
// documents no cache_control field at all（§9.1 隐式缓存，无需手动配置）, so it gets
// nothing extra either.
func anthropicTopLevelCacheControlEndpoint(cfg ConfigState) bool {
	return strings.Contains(strings.ToLower(baseURLForAPIFormat(cfg)), "moonshot")
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

// anthropicOrderUserBlocks pins the Messages API rule that a message following
// tool_use blocks must begin with its tool_result blocks. Merging a mid-turn
// <system> reminder into the tool-result turn would otherwise leave
// [text, tool_result] and the request is rejected with "…must begin with a
// matching number of tool_result blocks". Already-ordered input is returned
// untouched, and the partition is stable, so the request bytes stay identical
// across agent steps.
func anthropicOrderUserBlocks(blocks []anthropic.ContentBlockParamUnion) []anthropic.ContentBlockParamUnion {
	seenOther := false
	needsReorder := false
	for _, block := range blocks {
		if block.OfToolResult != nil {
			if seenOther {
				needsReorder = true
				break
			}
			continue
		}
		seenOther = true
	}
	if !needsReorder {
		return blocks
	}
	ordered := make([]anthropic.ContentBlockParamUnion, 0, len(blocks))
	for _, block := range blocks {
		if block.OfToolResult != nil {
			ordered = append(ordered, block)
		}
	}
	for _, block := range blocks {
		if block.OfToolResult == nil {
			ordered = append(ordered, block)
		}
	}
	return ordered
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
