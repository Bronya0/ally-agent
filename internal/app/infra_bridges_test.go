// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

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

// TestNormalizeAPIFormat pins the alias buckets the frontend mirrors
// (modelConfigIO.mjs normalizeAPIFormat). Both tables have to be pinned on their
// own side: a spelling only one of them folds is not "tolerated", it is silently
// rewritten on the side that missed it.
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

func TestMergeConfigReasoningEffort(t *testing.T) {
	// Empty overlay preserves the base value.
	base := ConfigState{ReasoningEffort: reasoningEffortHigh}
	got := mergeConfig(base, ConfigState{})
	if got.ReasoningEffort != reasoningEffortHigh {
		t.Fatalf("empty overlay changed reasoningEffort: got %q, want %q", got.ReasoningEffort, reasoningEffortHigh)
	}

	// A non-empty overlay replaces and normalizes the value.
	got = mergeConfig(base, ConfigState{ReasoningEffort: "x-high"})
	if got.ReasoningEffort != reasoningEffortXHigh {
		t.Fatalf("overlay reasoningEffort not normalized: got %q, want %q", got.ReasoningEffort, reasoningEffortXHigh)
	}

	// Model entries are normalized on merge.
	got = mergeConfig(ConfigState{}, ConfigState{Models: []ModelConfig{{ReasoningEffort: "MAX"}}})
	if len(got.Models) != 1 || got.Models[0].ReasoningEffort != reasoningEffortMax {
		t.Fatalf("model reasoningEffort not normalized on merge: got %#v", got.Models)
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

func TestLimitedBufferSpillMatchesCompleteOutput(t *testing.T) {
	const limit = 64
	chunks := []string{
		"构建输出 第一行\n",
		"构建输出 第二行 包含较多中文内容\n",
		"third line with ascii only\n",
		"最后一行 ⚙ done\n",
	}
	buf := &limitedBuffer{limit: limit}
	writer := &teeWriter{primary: buf, spillDir: t.TempDir()}
	buf.onTruncate = writer.startSpill

	for _, chunk := range chunks {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if writer.spill == nil {
		t.Fatal("overflowing writes must create the spill sink")
	}
	spillPath := writer.spill.Name()
	if err := writer.spill.Close(); err != nil {
		t.Fatal(err)
	}

	// The capped buffer keeps a UTF-8-valid prefix: a byte-sliced truncation
	// would leave a dangling multi-byte sequence, which downstream decoding
	// misreads as GBK mojibake.
	kept := buf.String()
	if !utf8.ValidString(kept) {
		t.Fatalf("buffered prefix must stay valid UTF-8: %q", kept)
	}
	if !buf.truncated {
		t.Fatal("overflow must set the truncated flag")
	}

	// The spill file is the byte-exact complete output: no chunk duplicated
	// (the overflowing chunk is written once, not once by the prefix capture
	// and again in full) and none lost.
	spilled, err := os.ReadFile(spillPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(chunks, "")
	if string(spilled) != want {
		t.Fatalf("spill file must equal the complete output:\n got %q\nwant %q", spilled, want)
	}
	if !strings.HasPrefix(want, kept) {
		t.Fatalf("buffered content must be a prefix of the full output, got %q", kept)
	}
}
