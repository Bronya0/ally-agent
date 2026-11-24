// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"reflect"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestContextToolUsageIncrementalAndReset(t *testing.T) {
	call := func(id, name, args string) openai.ToolCall {
		return openai.ToolCall{ID: id, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: name, Arguments: args}}
	}
	read := call("repeat", "read", `{"files":["/work/main.go"]}`)
	command := call("repeat", "command", `{"command":"go test ./..."}`)
	mcp := call("mcp", "ToolExec", `{"name":"docs.lookup","arguments":{"query":"中文"}}`)
	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", Content: "checking", ReasoningContent: "think", ToolCalls: []openai.ToolCall{read, command, mcp}},
		{Role: "tool", ToolCallID: "mcp", Content: "MCP result"},
		{Role: "tool", ToolCallID: "repeat", Content: strings.Repeat("read output", 100)},
		{Role: "tool", ToolCallID: "repeat", Content: "command output"},
	}
	acc := newLiveBreakdownAccumulator(messages[:2])
	first := acc.breakdown
	old := append([]ContextToolUsage(nil), first.ToolUsage...)
	got := acc.update(messages)
	if !reflect.DeepEqual(first.ToolUsage, old) {
		t.Fatal("append mutated an already published snapshot")
	}
	wantResults := map[string]int{
		"docs.lookup": messageTokens(messages[2]),
		"read":        messageTokens(messages[3]),
		"command":     messageTokens(messages[4]),
	}
	resultSum, callSum := 0, 0
	for i, item := range got.ToolUsage {
		if item.Calls != 1 || item.CallTokens <= 0 || item.ResultTokens != wantResults[item.Name] {
			t.Fatalf("wrong tool attribution: %+v", got.ToolUsage)
		}
		resultSum += item.ResultTokens
		callSum += item.CallTokens
		if i > 0 && got.ToolUsage[i-1].CallTokens+got.ToolUsage[i-1].ResultTokens < item.CallTokens+item.ResultTokens {
			t.Fatal("tools are not ordered by context cost")
		}
	}
	if len(got.ToolUsage) != 3 || len(acc.pending) != 0 || resultSum != got.ToolResults ||
		callSum != got.AssistantMsgs-estimateTokensFromText(messages[1].Content) {
		t.Fatalf("tool subdivisions changed category accounting: %+v", got)
	}
	if got.Total != got.UserMessages+got.AssistantMsgs+got.ToolResults+got.Reasoning ||
		!reflect.DeepEqual(got, computeLiveBreakdown(messages)) {
		t.Fatal("live and restored accounting differ or double-count tools")
	}
	if allocs := testing.AllocsPerRun(100, func() { acc.update(messages) }); allocs != 0 {
		t.Fatalf("unchanged history allocates: %g", allocs)
	}
	// A same-length rewrite must drop the previous counts and call-ID mappings.
	rewritten := append([]openai.ChatCompletionMessage(nil), messages...)
	rewritten[1].ToolCalls = []openai.ToolCall{call("new", "edit", `{}`)}
	for i := 2; i < len(rewritten); i++ {
		rewritten[i] = openai.ChatCompletionMessage{Role: "assistant", Content: "summary"}
	}
	acc.reset(rewritten)
	if usage := acc.breakdown.ToolUsage; len(usage) != 1 || usage[0].Name != "edit" || usage[0].Calls != 1 || usage[0].ResultTokens != 0 {
		t.Fatalf("rewrite retained old tool usage: %+v", usage)
	}
}

func TestContextToolUsageFallbackAndIsolation(t *testing.T) {
	app := footerStatsTestApp(t)
	const sid = "tool-breakdown"
	history := []openai.ChatCompletionMessage{
		{Role: "tool", Content: "orphan"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "invalid", Function: openai.FunctionCall{Name: "ToolExec", Arguments: `{"name":`}}}},
		{Role: "tool", ToolCallID: "invalid", Content: "bad arguments"},
	}
	fallback := computeLiveBreakdown(history)
	byName := map[string]ContextToolUsage{}
	for _, item := range fallback.ToolUsage {
		byName[item.Name] = item
	}
	if len(byName) != 2 || byName["unknown"].ResultTokens != messageTokens(history[0]) || byName["ToolExec"].Calls != 1 ||
		byName["ToolExec"].ResultTokens != messageTokens(history[2]) {
		t.Fatalf("fallback attribution lost results: %+v", byName)
	}
	// Persisted history passes through the existing hygiene rules, so exercise
	// its cache with a properly paired call and a user turn.
	history = []openai.ChatCompletionMessage{
		{Role: "user", Content: "read"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "read", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: `{"files":["/work/main.go"]}`}}}},
		{Role: "tool", ToolCallID: "read", Content: "package main"},
	}
	app.saveHistory(sid, history)
	app.mu.Lock()
	delete(app.liveBreakdown, sid)
	app.mu.Unlock()
	got := app.getContextBreakdown(sid, app.config.Workspace)
	if len(got.ToolUsage) != 1 || got.ToolUsage[0].Name != "read" || got.ToolUsage[0].ResultTokens != messageTokens(history[2]) || len(got.ToolSchemaParts) == 0 {
		t.Fatalf("missing saved tool usage or schema parts: %+v", got)
	}
	got.ToolUsage[0].ResultTokens = -1
	got.ToolSchemaParts[0].Tokens = -1
	again := app.getContextBreakdown(sid, app.config.Workspace)
	if again.ToolUsage[0].ResultTokens < 0 || again.ToolSchemaParts[0].Tokens < 0 {
		t.Fatal("caller mutated cached statistics")
	}
	if _, ok := app.sessionToolsets[sid]; ok {
		t.Fatal("statistics preview froze the session toolset")
	}
}

func TestContextToolSchemaPartsMatchDeclarations(t *testing.T) {
	for _, tools := range [][]openai.Tool{chatTools(), withToolPurposes(chatTools()), withToolPurposes(chatTools()[:3])} {
		stats := estimateToolSchemas(tools)
		names := map[string]bool{}
		for _, tool := range tools {
			names[tool.Function.Name] = true
		}
		sum := 0
		for _, part := range stats.parts {
			if !names[part.Label] || part.Tokens <= 0 {
				t.Fatalf("unexpected schema subdivision: %+v", stats.parts)
			}
			delete(names, part.Label)
			sum += part.Tokens
		}
		if len(names) != 0 || sum != stats.total || stats.total != estimateModelToolSchemas(tools) {
			t.Fatalf("schema parts do not match actual declarations: %+v", stats)
		}
	}
}
