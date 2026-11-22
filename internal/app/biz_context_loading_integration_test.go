// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	openai "github.com/sashabaranov/go-openai"
)

func TestFastCompactRefreshesSourcesAndKeepsLoadingChoices(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	a := newIsolatedChatTestApp(t)
	a.modelHistoryCodec = nil
	cfg := a.config.Clone()
	cfg.DisabledContext = []string{"memory_index", "user_instructions"}
	a.config = cfg
	const sid = "refresh-local"
	writeTestFile(t, cfg.Workspace, "AGENTS.md", "project-rule-v1")
	a.buildSystemContextMessages(sid, cfg, []SkillDefinition{{Name: "old-skill", Description: "old", Source: "project"}})
	a.buildToolsForSession(sid, cfg)
	a.saveHistory(sid, fastCompactFixture())

	writeTestFile(t, cfg.Workspace, "AGENTS.md", "project-rule-v2")
	writeTestFile(t, cfg.Workspace, "added-file.txt", "new file")
	latest := cfg.Clone()
	latest.DisabledContext = []string{"project_instructions", "workspace_map", "skills"}
	latest.DisabledTools = []string{"grep"}
	a.config = latest
	result, err := a.FastCompactSession(sid)
	if err != nil || result["changed"] != true {
		t.Fatalf("local rewrite failed: result=%v error=%v", result, err)
	}
	if intFromAny(result["tokensAfter"]) != a.GetContextBreakdown(sid, cfg.Workspace).Total {
		t.Fatal("post-compaction total does not match the rebuilt prefix")
	}
	if _, exists := a.sessionSystemPrompts[sid]; exists {
		t.Fatal("compaction kept old system prompt")
	}
	if _, exists := a.sessionSkills[sid]; exists {
		t.Fatal("compaction kept old skill definitions")
	}
	if _, exists := a.sessionToolsets[sid]; exists {
		t.Fatal("compaction kept old tool schemas")
	}
	if _, exists := a.sessionWorkspaceMaps[sid]; exists {
		t.Fatal("compaction kept old workspace map")
	}
	rebuilt := a.buildSystemContextMessages(sid, latest, []SkillDefinition{{Name: "new-skill", Description: "new", Source: "project"}})
	if len(rebuilt) != 2 || !strings.Contains(rebuilt[0].Content, "project-rule-v2") || strings.Contains(rebuilt[0].Content, "project-rule-v1") || !strings.Contains(rebuilt[0].Content, "new-skill") || strings.Contains(rebuilt[0].Content, "old-skill") {
		t.Fatal("rebuilt sources used old content or adopted later loading switches")
	}
	if !strings.Contains(rebuilt[1].Content, "added-file.txt") {
		t.Fatal("rebuilt workspace map reused stale scan")
	}
	foundGrep := false
	for _, tool := range a.buildToolsForSession(sid, latest) {
		foundGrep = foundGrep || tool.Function != nil && tool.Function.Name == "grep"
	}
	if !foundGrep {
		t.Fatal("compaction adopted later tool-disable choices")
	}
}

func TestMcpCatalogLoadingChoiceControlsInjectionAndStatsNotExecution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	a := newIsolatedChatTestApp(t)
	m := NewMcpManager("", nil)
	calls := 0
	c := client.NewClient(mcpRegistryTransport{send: func(_ context.Context, req transport.JSONRPCRequest) (string, error) {
		if req.Method != "tools/call" {
			t.Fatalf("unexpected RPC: %s", req.Method)
		}
		calls++
		return `{"content":[{"type":"text","text":"used"}]}`, nil
	}}, client.WithSession())
	m.clients["demo"] = &McpClientHandle{ServerName: "demo", Status: "connected", Client: c,
		ToolDefs: []McpDiscoveredTool{{ServerName: "demo", Name: "lookup", Description: "Look up an item", Schema: map[string]any{"type": "object"}}}}
	a.mcpManager = m
	off := a.config.Clone()
	off.DisabledContext = []string{"mcp_catalog"}
	a.config = off
	ctx := withBuiltinToolAvailability(t.Context(), a.buildToolsForSession("off", off))
	if messages := a.ensureMcpCatalog(ctx, off, nil); len(messages) != 0 || a.GetContextBreakdown("off", off.Workspace).MCPCatalog != 0 {
		t.Fatal("disabled catalog was injected or counted")
	}
	for _, call := range []struct{ name, args string }{
		{"ToolSearch", `{"query":["demo.lookup"]}`},
		{"ToolExec", `{"name":"demo.lookup","arguments":{}}`},
	} {
		if result := a.executeTool(ctx, off, "off", call.name, []byte(call.args)); !result.OK {
			t.Fatalf("catalog switch changed tool permission: %+v", result)
		}
	}
	if calls != 1 {
		t.Fatalf("direct tool call count = %d, want 1", calls)
	}
	on := off.Clone()
	on.DisabledContext = []string{}
	a.config = on
	old := a.contextSettingsForSession("off", on, false)
	if old.ContextEnabled("mcp_catalog") || a.GetContextBreakdown("off", off.Workspace).MCPCatalog != 0 {
		t.Fatal("existing session adopted a later catalog switch")
	}
	onCtx := withBuiltinToolAvailability(t.Context(), a.buildToolsForSession("on", on))
	catalog := a.ensureMcpCatalog(onCtx, on, nil)
	if len(catalog) != 1 || !isMcpCatalogMessage(catalog[0]) || a.GetContextBreakdown("on", on.Workspace).MCPCatalog != messageTokens(catalog[0]) {
		t.Fatal("enabled catalog injection and preview stats disagree")
	}
	a.saveHistory("on", []openai.ChatCompletionMessage{catalog[0]})
	a.config = off
	if kept := a.ensureMcpCatalog(onCtx, off, catalog); len(kept) != 1 || kept[0].Content != catalog[0].Content {
		t.Fatal("changing the switch rewrote a historical catalog")
	}
	a.refreshSessionPromptPrefix("on")
	frozen := a.contextSettingsForSession("on", off, false)
	if !frozen.ContextEnabled("mcp_catalog") || len(a.ensureMcpCatalog(onCtx, frozen, nil)) != 1 {
		t.Fatal("compaction did not retain the original catalog loading choice")
	}
}
