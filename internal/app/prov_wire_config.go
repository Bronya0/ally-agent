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
	// cacheRetentionShort / cacheRetentionLong are the two levels the user can
	// pick for how long a provider should keep the request prefix cached (see
	// ConfigState.CacheRetention).
	cacheRetentionShort = "short"
	cacheRetentionLong  = "long"
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

// normalizeCacheRetention keeps the stored retention level inside the
// documented set. Anything else — including the empty field of a config.json
// written before this option existed — falls back to "short", which sends no
// retention field at all and leaves the provider default in place.
func normalizeCacheRetention(value string) string {
	if strings.ToLower(strings.TrimSpace(value)) == cacheRetentionLong {
		return cacheRetentionLong
	}
	return cacheRetentionShort
}

// ── Prompt-cache retention ──
//
// One user-facing level, three wire spellings, and the spelling is decided here
// only — the rule the thinking levels already follow (reasoningWireForAdapter)
// and the shape pi uses (ai/src/api/openai-responses.ts getPromptCacheOptions /
// getPromptCacheRetention, ai/src/api/anthropic-messages.ts getCacheControl):
// Anthropic expresses retention as cache_control.ttl, OpenAI as
// prompt_cache_retention on models before GPT-5.6 and as
// prompt_cache_options.ttl from GPT-5.6 on.

// cacheRetentionRequestsLong reports whether the user asked for extended
// retention.
func cacheRetentionRequestsLong(cfg ConfigState) bool {
	return normalizeCacheRetention(cfg.CacheRetention) == cacheRetentionLong
}

// anthropicCacheControlTTL is the `cache_control.ttl` value: "1h" for extended
// retention, "5m" otherwise. The short value is written out explicitly (it is
// Anthropic's documented default) so every request carries the same breakpoint
// marker whatever the setting is.
func anthropicCacheControlTTL(cfg ConfigState) string {
	if cacheRetentionRequestsLong(cfg) {
		return "1h"
	}
	return "5m"
}

// openAIExtendedCacheRetention is the `prompt_cache_retention` value, or "" when
// the field must not be sent. Extended retention is how a prefix is kept alive on
// models before GPT-5.6; from GPT-5.6 on, prompt_cache_options.ttl replaced the
// field (the SDK documents it as deprecated), so the two never travel together —
// the same split pi makes with its explicit-prompt-cache compat flag. As with the
// other OpenAI-only request fields, the official endpoint is the gate: a
// compatible gateway may answer an unknown top-level parameter with 400.
func openAIExtendedCacheRetention(cfg ConfigState, model string) string {
	if !cacheRetentionRequestsLong(cfg) || !isOfficialOpenAIEndpoint(cfg) {
		return ""
	}
	if modelUsesPromptCacheOptionsTTL(model) {
		return ""
	}
	return "24h"
}

// openAIPromptCacheOptionsTTL is the `prompt_cache_options.ttl` value, or "" when
// the field must not be sent: only GPT-5.6 and later understand it, and only the
// Responses API carries it. "30m" is the sole supported value and also the
// provider default, sent to state the intent explicitly (pi does the same).
func openAIPromptCacheOptionsTTL(cfg ConfigState, model string) string {
	if !cacheRetentionRequestsLong(cfg) || !supportsOpenAIPromptCacheOptions(cfg, model) {
		return ""
	}
	return "30m"
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
