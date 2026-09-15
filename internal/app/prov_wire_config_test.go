// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3.0. See the LICENSE file for details.
package app

import "testing"

func TestNormalizeReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"":            reasoningEffortAuto,
		"auto":        reasoningEffortAuto,
		"Auto":        reasoningEffortAuto,
		"default":     reasoningEffortAuto,
		"unset":       reasoningEffortAuto,
		"off":         reasoningEffortOff,
		"OFF":         reasoningEffortOff,
		"none":        reasoningEffortOff,
		"disabled":    reasoningEffortOff,
		"nothinking":  reasoningEffortOff,
		"no-think":    reasoningEffortOff,
		"NO_THINK":    reasoningEffortOff,
		"low":         reasoningEffortLow,
		"LOW":         reasoningEffortLow,
		"medium":      reasoningEffortMedium,
		"med":         reasoningEffortMedium,
		"high":        reasoningEffortHigh,
		"xhigh":       reasoningEffortXHigh,
		"X-HIGH":      reasoningEffortXHigh,
		"extra_high":  reasoningEffortXHigh,
		"extremehigh": reasoningEffortXHigh,
		"max":         reasoningEffortMax,
		"maximum":     reasoningEffortMax,
		"bogus":       reasoningEffortAuto,
		"high effort": reasoningEffortAuto,
	}
	for in, want := range cases {
		if got := normalizeReasoningEffort(in); got != want {
			t.Errorf("normalizeReasoningEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeAPIFormat(t *testing.T) {
	cases := map[string]string{
		"":                   apiFormatOpenAIChat,
		"openai":             apiFormatOpenAIChat,
		"openai_compatible":  apiFormatOpenAIChat,
		"openai_chat":        apiFormatOpenAIChat,
		"chat":               apiFormatOpenAIChat,
		"chat_completions":   apiFormatOpenAIChat,
		"chat_completion":    apiFormatOpenAIChat,
		"OpenAI-Chat":        apiFormatOpenAIChat,
		"bogus":              apiFormatOpenAIChat,
		"openai_responses":   apiFormatOpenAIResponses,
		"responses":          apiFormatOpenAIResponses,
		"response":           apiFormatOpenAIResponses,
		"anthropic":          apiFormatAnthropicMessages,
		"anthropic_messages": apiFormatAnthropicMessages,
		"claude":             apiFormatAnthropicMessages,
		"claude_messages":    apiFormatAnthropicMessages,
		"messages":           apiFormatAnthropicMessages,
	}
	for in, want := range cases {
		if got := normalizeAPIFormat(in); got != want {
			t.Errorf("normalizeAPIFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReasoningWireForAdapter(t *testing.T) {
	compatible := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: "https://api.deepseek.com"}
	official := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: openAIOfficialAPIBaseURL}
	cases := []struct {
		name   string
		cfg    ConfigState
		format string
		effort string
		want   reasoningWirePlan
	}{
		{"empty sends nothing", compatible, apiFormatOpenAIChat, "", reasoningWirePlan{}},
		{"auto leaves it to the provider", compatible, apiFormatOpenAIChat, reasoningEffortAuto, reasoningWirePlan{}},
		{
			"off asks a compatible Chat endpoint to stop thinking",
			compatible, apiFormatOpenAIChat, reasoningEffortOff, reasoningWirePlan{DisableThinking: true},
		},
		{
			"off aliases resolve to the same plan",
			compatible, apiFormatOpenAIChat, "disabled", reasoningWirePlan{DisableThinking: true},
		},
		{
			"off is spelled as an effort on the Responses wire",
			compatible, apiFormatOpenAIResponses, reasoningEffortOff, reasoningWirePlan{Effort: reasoningEffortOffWireValue},
		},
		{
			"off is spelled as an effort on the official Chat endpoint",
			official, apiFormatOpenAIChat, reasoningEffortOff, reasoningWirePlan{Effort: reasoningEffortOffWireValue},
		},
		{"levels pass through", compatible, apiFormatOpenAIChat, reasoningEffortLow, reasoningWirePlan{Effort: reasoningEffortLow}},
		{"xhigh passes through", compatible, apiFormatOpenAIResponses, reasoningEffortXHigh, reasoningWirePlan{Effort: reasoningEffortXHigh}},
		{"max passes through", official, apiFormatOpenAIChat, reasoningEffortMax, reasoningWirePlan{Effort: reasoningEffortMax}},
	}
	for _, c := range cases {
		if got := reasoningWireForAdapter(c.cfg, c.format, c.effort); got != c.want {
			t.Errorf("%s: reasoningWireForAdapter(%q, %q) = %+v, want %+v", c.name, c.format, c.effort, got, c.want)
		}
	}
}
