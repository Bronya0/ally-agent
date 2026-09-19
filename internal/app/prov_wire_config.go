// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3.0. See the LICENSE file for details.
package app

import "strings"

const (
	apiFormatOpenAIChat        = "openai_chat"
	apiFormatOpenAIResponses   = "openai_responses"
	apiFormatAnthropicMessages = "anthropic_messages"
	// openAIOfficialAPIBaseURL is the single source of truth for the official
	// OpenAI endpoint: both adapters gate their OpenAI-only request fields on it
	// (see isOfficialOpenAIEndpoint).
	openAIOfficialAPIBaseURL      = "https://api.openai.com/v1"
	defaultOpenAIResponsesURL     = openAIOfficialAPIBaseURL
	defaultAnthropicMessagesURL   = "https://api.anthropic.com"
	tokenParamAuto                = "auto"
	tokenParamMaxTokens           = "max_tokens"
	tokenParamMaxCompletionTokens = "max_completion_tokens"
	reasoningEffortAuto           = "auto"
	// reasoningEffortOff is the explicit "stop thinking" level (the UI's 关闭思考),
	// as opposed to auto, which leaves the decision to the provider. How it reaches
	// the provider depends on the protocol: reasoningWireForAdapter owns the Chat
	// and Responses wires, configureAnthropicThinking the Anthropic one.
	reasoningEffortOff = "off"
	// reasoningEffortOffWireValue is the effort value that turns thinking off on an
	// OpenAI effort field. DeepSeek documents "none" as 关闭思考模式 for the
	// Responses API, and the official endpoint uses the same enum on its newest
	// reasoning models; a provider that accepts neither answers 400, which is the
	// same contract the other levels follow.
	reasoningEffortOffWireValue = "none"
	reasoningEffortLow          = "low"
	reasoningEffortMedium       = "medium"
	reasoningEffortHigh         = "high"
	reasoningEffortXHigh        = "xhigh"
	reasoningEffortMax          = "max"
)

// Provider-facing defaults: the fallback model, its endpoint, and the
// reasoning dialect tag the request rewrite falls back to
// (see chatReasoningBackfillKey).
const (
	defaultModel        = "deepseek-v4-flash"
	defaultBaseURL      = "https://api.deepseek.com"
	defaultReasoningTag = "reasoning_content"
)

// normalizeReasoningEffort accepts any supported spelling (with case, dash,
// space or underscore separators) and returns the canonical lowercase level.
// Unknown values fall back to "auto" so a stale or mistyped config never
// injects an unsupported parameter into a request.
func normalizeReasoningEffort(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.NewReplacer("-", "", "_", "", " ", "").Replace(v)
	switch v {
	case "auto", "default", "unset", "":
		return reasoningEffortAuto
	case "off", "none", "disabled", "nothinking", "nothink":
		return reasoningEffortOff
	case "low":
		return reasoningEffortLow
	case "medium", "med":
		return reasoningEffortMedium
	case "high":
		return reasoningEffortHigh
	case "xhigh", "extrahigh", "extremehigh":
		return reasoningEffortXHigh
	case "max", "maximum", "maximal":
		return reasoningEffortMax
	default:
		return reasoningEffortAuto
	}
}

// reasoningWirePlan is what the selected thinking level puts on a non-Anthropic
// wire. It is the single mapping for every level, so an adapter never decides a
// spelling of its own.
type reasoningWirePlan struct {
	// Effort is the value for the protocol's effort field (Chat:
	// reasoning_effort, Responses: reasoning.effort); "" means the field must not
	// be sent at all.
	Effort string
	// DisableThinking adds the stop-thinking field —
	// `thinking: {"type": "disabled"}` — to a Chat Completions body.
	DisableThinking bool
}

// reasoningWireForAdapter maps the configured level to the fields this request
// carries. Every level is preserved unchanged, including xhigh and max; the
// provider is responsible for rejecting a level it does not support.
//
// "off" is the one level with no shared spelling, so it is resolved per wire:
//   - Responses: effort "none" — DeepSeek documents it as the way to turn
//     thinking off, and it is the only reasoning field that protocol has.
//   - Chat on a compatible endpoint: `thinking: {"type": "disabled"}`, the field
//     DeepSeek documents and whose own sample passes through extra_body, because
//     the OpenAI Chat schema has no such field (the request rewrite adds it).
//   - Chat on the official OpenAI API: its own enum value instead, since that
//     endpoint rejects an unknown field outright.
func reasoningWireForAdapter(cfg ConfigState, apiFormat, effort string) reasoningWirePlan {
	level := normalizeReasoningEffort(effort)
	switch level {
	case reasoningEffortAuto:
		return reasoningWirePlan{}
	case reasoningEffortOff:
		if normalizeAPIFormat(apiFormat) == apiFormatOpenAIResponses || isOfficialOpenAIEndpoint(cfg) {
			return reasoningWirePlan{Effort: reasoningEffortOffWireValue}
		}
		return reasoningWirePlan{DisableThinking: true}
	}
	return reasoningWirePlan{Effort: level}
}

func normalizeTokenParam(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.NewReplacer("-", "_", " ", "_").Replace(v)
	switch v {
	case tokenParamMaxCompletionTokens, "max_completion_token", "completion_tokens", "completion":
		return tokenParamMaxCompletionTokens
	case tokenParamMaxTokens, "max_token", "tokens", "legacy":
		return tokenParamMaxTokens
	default:
		return tokenParamAuto
	}
}

func normalizeAPIFormat(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.NewReplacer("-", "_", " ", "_").Replace(v)
	switch v {
	case "", "openai", "openai_compatible", "openai_chat", "chat", "chat_completions", "chat_completion":
		return apiFormatOpenAIChat
	case "openai_responses", "responses", "response":
		return apiFormatOpenAIResponses
	case "anthropic", "anthropic_messages", "claude", "claude_messages", "messages":
		return apiFormatAnthropicMessages
	default:
		return apiFormatOpenAIChat
	}
}

func defaultBaseURLForAPIFormat(format string) string {
	switch normalizeAPIFormat(format) {
	case apiFormatOpenAIResponses:
		return defaultOpenAIResponsesURL
	case apiFormatAnthropicMessages:
		return defaultAnthropicMessagesURL
	default:
		return defaultBaseURL
	}
}

func baseURLForAPIFormat(cfg ConfigState) string {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = defaultBaseURLForAPIFormat(cfg.APIFormat)
	}
	base = strings.TrimRight(base, "/")
	if normalizeAPIFormat(cfg.APIFormat) == apiFormatAnthropicMessages && strings.HasSuffix(strings.ToLower(base), "/v1") {
		base = base[:len(base)-3]
	}
	return base
}

func defaultMaxTokensForAPIFormat(format string) int {
	// Unified fallback for every API format: one number, no per-format
	// branching. 131072 (128K) covers current catalog entries; relay
	// endpoints typically clamp server-side.
	return 131072
}
