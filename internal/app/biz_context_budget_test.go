// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	openai "github.com/sashabaranov/go-openai"
)

func TestAutomaticContextBudgetChecksTheWireBeforeCallingProvider(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prefix   int
		messages []openai.ChatCompletionMessage
		reject   bool
	}{
		{"exact limit", 0, []openai.ChatCompletionMessage{{Role: "system", Content: strings.Repeat("x", AutomaticContextBudgetBytes)}}, false},
		{"system overflow", 0, []openai.ChatCompletionMessage{{Role: "system", Content: strings.Repeat("x", AutomaticContextBudgetBytes+1)}}, true},
		{"combined prefix", 0, []openai.ChatCompletionMessage{{Role: "system", Content: strings.Repeat("x", AutomaticContextBudgetBytes/2)}, {Role: "system", Content: strings.Repeat("y", AutomaticContextBudgetBytes/2+1)}}, true},
		{"MCP catalog", 0, []openai.ChatCompletionMessage{{Role: "system", Content: "rules"}, {Role: "user", Content: mcpCatalogContent(strings.Repeat("x", AutomaticContextBudgetBytes), true)}}, true},
		{"child environment", 2, []openai.ChatCompletionMessage{{Role: "system", Content: "rules"}, {Role: "user", Content: strings.Repeat("x", AutomaticContextBudgetBytes)}, {Role: "user", Content: "task"}}, true},
		{"history is not automatic context", 0, []openai.ChatCompletionMessage{{Role: "system", Content: "rules"}, {Role: "user", Content: strings.Repeat("history", AutomaticContextBudgetBytes/7+1)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSubagentReportHarness(t, subagentTextReply("accepted"))
			a := newIsolatedChatTestApp(t)
			cfg := subagentReportBaseConfig(h.server.URL, a.config.Workspace)
			ctx := context.WithValue(t.Context(), automaticContextPrefixKey{}, tc.prefix)
			resp, err := a.streamModelResponse(ctx, cfg, cfg.Model, tc.messages, nil, nil)
			if tc.reject {
				if err == nil || len(h.recorded()) != 0 {
					t.Fatalf("oversized automatic context reached provider: error=%v requests=%d", err, len(h.recorded()))
				}
				if classifyLLMError(err) == llmErrorKindContextTooLong || retryModelTurn(err, true) {
					t.Fatalf("local budget rejection must not trigger provider recovery: %v", err)
				}
				return
			}
			if err != nil || resp.Content != "accepted" || len(h.recorded()) != 1 {
				t.Fatalf("valid request was rejected: response=%+v error=%v", resp, err)
			}
			if got := h.recorded()[0].Messages; len(got) != len(tc.messages) || got[len(got)-1].Content != tc.messages[len(tc.messages)-1].Content {
				t.Fatal("accepted content was silently shortened")
			}
		})
	}
}

func TestAutomaticContextSourceBuildersStayBoundedAndDetectable(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for i := 0; i < 5; i++ {
		writeTestFile(t, root, fmt.Sprintf("module-%d/AGENTS.md", i), strings.Repeat("规", maxAgentsMdPromptBytes/3))
	}
	skills := make([]SkillDefinition, 300)
	for i := range skills {
		skills[i] = SkillDefinition{Name: fmt.Sprintf("skill-%d", i), Source: "project", Description: strings.Repeat("描", skillListingFieldLimit), WhenToUse: strings.Repeat("用", skillListingFieldLimit)}
	}
	m := NewMcpManager("", nil)
	defs := make([]McpDiscoveredTool, 700)
	for i := range defs {
		defs[i] = McpDiscoveredTool{ServerName: "demo", Name: fmt.Sprintf("tool-%d", i), Description: strings.Repeat("查", 128)}
	}
	m.clients["demo"] = &McpClientHandle{ServerName: "demo", Status: "connected", ToolDefs: defs}
	a := newIsolatedChatTestApp(t)
	for _, tc := range []struct {
		name, body string
	}{
		{"merged instructions", loadAgentsMd(root)},
		{"skill listing", buildSkillListingMeta(skills)},
		{"MCP catalog", m.toolCatalog(true)},
		{"custom instructions", joinSystemPromptParts(a.buildSystemPromptParts(nil, "", nil, strings.Repeat("自", AutomaticContextBudgetBytes), "", []string{"user_profile", "memory_index"}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !utf8.ValidString(tc.body) || len(tc.body) <= AutomaticContextBudgetBytes || len(tc.body) > AutomaticContextBudgetBytes+16*1024 {
				t.Fatalf("source preview lost overflow or is unbounded: bytes=%d valid=%v", len(tc.body), utf8.ValidString(tc.body))
			}
			if err := validateAutomaticContext(t.Context(), []openai.ChatCompletionMessage{{Role: "system", Content: tc.body}}); err == nil {
				t.Fatal("bounded oversized preview could be sent as a truncated prompt")
			}
		})
	}
}
