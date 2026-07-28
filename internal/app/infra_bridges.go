package app

import (
	"strings"

	calculatetool "ally-dev/internal/tools/calculate"
	"ally-dev/internal/tools/grep"
)

type CalculateRequest = calculatetool.Request
type CalculateResult = calculatetool.Result

// Grep type aliases keep the historical app-package names in the Wails
// bindings while the implementations live in internal/tools/grep.
type GrepRequest = grep.Request
type GrepMatch = grep.Match
type GrepResult = grep.Result

const (
	apiFormatOpenAIChat           = "openai_chat"
	apiFormatOpenAIResponses      = "openai_responses"
	apiFormatAnthropicMessages    = "anthropic_messages"
	defaultOpenAIResponsesURL     = "https://api.openai.com/v1"
	defaultAnthropicMessagesURL   = "https://api.anthropic.com"
	tokenParamAuto                = "auto"
	tokenParamMaxTokens           = "max_tokens"
	tokenParamMaxCompletionTokens = "max_completion_tokens"
)

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
	if normalizeAPIFormat(format) == apiFormatAnthropicMessages {
		return 8192
	}
	return 128000
}

func calculateExpression(req CalculateRequest) (CalculateResult, error) {
	return calculatetool.Evaluate(req)
}
