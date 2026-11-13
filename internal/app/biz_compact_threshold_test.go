// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestCompactThresholdLimitModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  ConfigState
		want int
	}{
		{"legacy default", ConfigState{}, 600_000},
		{"legacy ratio", ConfigState{ContextWindow: 128_000, CompactThreshold: .35, CompactThresholdTokens: 1}, 44_800},
		{"explicit ratio", ConfigState{CompactThresholdMode: "ratio", ContextWindow: 200_000, CompactThreshold: .5, CompactThresholdTokens: 1}, 100_000},
		{"tokens ignore window and ratio", ConfigState{CompactThresholdMode: "tokens", ContextWindow: 10_000, CompactThreshold: .2, CompactThresholdTokens: 80_000}, 80_000},
		{"tokens missing window", ConfigState{CompactThresholdMode: "tokens", CompactThresholdTokens: 80_000}, 80_000},
		{"tokens default", ConfigState{CompactThresholdMode: "tokens"}, DefaultCompactThresholdTokens},
		{"tokens invalid", ConfigState{CompactThresholdMode: "tokens", CompactThresholdTokens: -1}, DefaultCompactThresholdTokens},
		{"tokens minimum", ConfigState{CompactThresholdMode: "tokens", CompactThresholdTokens: 1}, 1},
		{"tokens maximum", ConfigState{CompactThresholdMode: "tokens", CompactThresholdTokens: MaxCompactThresholdTokens + 1}, MaxCompactThresholdTokens},
		{"unknown mode uses ratio", ConfigState{CompactThresholdMode: "unknown", CompactThreshold: .35, CompactThresholdTokens: 1}, 350_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := compactThresholdLimit(tc.cfg); got != tc.want {
				t.Fatalf("limit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCompactThresholdConfigPersistence(t *testing.T) {
	a := configTransactionApp(t)
	if err := os.WriteFile(a.configPath, []byte(`{"compactThreshold":0.35}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := readConfigFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	a.config = loadedConfig(legacy)
	if a.config.CompactThresholdMode != CompactThresholdModeRatio || compactThresholdLimit(a.config) != 350_000 {
		t.Fatal("legacy config must retain its percentage threshold")
	}
	for _, tc := range []struct {
		req   ConfigState
		mode  string
		limit int
	}{
		{ConfigState{CompactThresholdMode: " TOKENS ", CompactThresholdTokens: 82_345}, "tokens", 82_345},
		{ConfigState{CompactTimeoutSeconds: 240}, "tokens", 82_345}, // Omitted fields preserve the selection.
		{ConfigState{CompactThresholdMode: "ratio"}, "ratio", 350_000},
		{ConfigState{CompactThresholdMode: "tokens"}, "tokens", 82_345}, // Switching back retains both values.
	} {
		if err := a.SaveConfig(tc.req); err != nil {
			t.Fatal(err)
		}
		loaded, err := readConfigFile(a.configPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, cfg := range []ConfigState{loadedConfig(loaded), a.effectiveConfig(ConfigState{})} {
			if cfg.CompactThresholdMode != tc.mode || cfg.CompactThresholdTokens != 82_345 || compactThresholdLimit(cfg) != tc.limit {
				t.Fatalf("threshold selection did not survive save/load/request merge: %+v", cfg)
			}
		}
	}
	// An explicit request mode overrides the stored mode without replacing values.
	if got := compactThresholdLimit(a.effectiveConfig(ConfigState{CompactThresholdMode: "ratio"})); got != 350_000 {
		t.Fatalf("request ratio override = %d", got)
	}
}

func TestRunChatUsesSelectedCompactThreshold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		mode            string
		tokens, window  int
		wantCompactions int
	}{
		{"ratio ignores token threshold", "ratio", 1, 1_000_000, 0},
		{"tokens trigger below ratio", "tokens", 1, 1_000_000, 1},
		{"tokens ignore ratio threshold", "tokens", 1_000_000, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newIsolatedChatTestApp(t)
			var summaries atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				response := "done"
				if strings.Contains(string(body), overflowCompactPromptMarker) {
					summaries.Add(1)
					response = overflowSummaryText
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, sseChatChunk(response), sseChatFinishChunk("stop"), sseDone)
			}))
			defer server.Close()
			if err := a.SaveConfig(ConfigState{CompactThresholdMode: tc.mode, CompactThresholdTokens: tc.tokens, AutoCompactMethod: "summary"}); err != nil {
				t.Fatal(err)
			}
			const sid = "threshold-run"
			a.saveHistory(sid, []openai.ChatCompletionMessage{
				{Role: "user", Content: "first question"}, {Role: "assistant", Content: "first answer"},
				{Role: "user", Content: "second question"}, {Role: "assistant", Content: "second answer"},
			})
			recorder := &compactRecorder{done: make(chan struct{})}
			a.events = recorder
			if _, err := a.StartChat(ChatRequest{SessionID: sid, Message: "continue", Config: ConfigState{
				APIFormat: apiFormatOpenAIChat, BaseURL: server.URL, APIKeys: []string{"test-key"},
				Model: "test-model", MaxTokens: 64, ContextWindow: tc.window,
			}}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-recorder.done:
			case <-time.After(15 * time.Second):
				t.Fatal("run did not finish")
			}
			if recorder.endStatus() != "run:done" || int(summaries.Load()) != tc.wantCompactions || len(recorder.compactedEvents()) != tc.wantCompactions {
				t.Fatalf("status=%s summaries=%d events=%v errors=%v", recorder.endStatus(), summaries.Load(), recorder.compactedEvents(), recorder.errorEvents())
			}
		})
	}
}
