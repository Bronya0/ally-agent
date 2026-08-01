package app

import "testing"

func TestNormalizeReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"":            reasoningEffortAuto,
		"auto":        reasoningEffortAuto,
		"Auto":        reasoningEffortAuto,
		"default":     reasoningEffortAuto,
		"unset":       reasoningEffortAuto,
		"off":         reasoningEffortAuto,
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

func TestReasoningEffortForAdapter(t *testing.T) {
	cases := []struct {
		apiFormat string
		effort    string
		want      string
	}{
		{apiFormatOpenAIChat, "", ""},
		{apiFormatOpenAIChat, reasoningEffortAuto, ""},
		{apiFormatOpenAIChat, reasoningEffortLow, reasoningEffortLow},
		{apiFormatOpenAIChat, reasoningEffortMedium, reasoningEffortMedium},
		{apiFormatOpenAIChat, reasoningEffortHigh, reasoningEffortHigh},
		{apiFormatOpenAIChat, reasoningEffortXHigh, reasoningEffortHigh},
		{apiFormatOpenAIChat, reasoningEffortMax, reasoningEffortHigh},
		{apiFormatOpenAIResponses, reasoningEffortMax, reasoningEffortHigh},
		{apiFormatAnthropicMessages, "", ""},
		{apiFormatAnthropicMessages, reasoningEffortLow, reasoningEffortLow},
		{apiFormatAnthropicMessages, reasoningEffortXHigh, reasoningEffortXHigh},
		{apiFormatAnthropicMessages, reasoningEffortMax, reasoningEffortMax},
	}
	for _, c := range cases {
		if got := reasoningEffortForAdapter(c.apiFormat, c.effort); got != c.want {
			t.Errorf("reasoningEffortForAdapter(%q, %q) = %q, want %q", c.apiFormat, c.effort, got, c.want)
		}
	}
}
