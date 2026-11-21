// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	openai "github.com/sashabaranov/go-openai"
)

type mcpRegistryTransport struct {
	transport.Interface
	send func(context.Context, transport.JSONRPCRequest) (string, error)
}

func (tr mcpRegistryTransport) SendRequest(ctx context.Context, req transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	body, err := tr.send(ctx, req)
	if err != nil {
		return nil, err
	}
	return &transport.JSONRPCResponse{Result: json.RawMessage(body)}, nil
}

func TestMcpRegistryPaginationSearchAndLiveSchema(t *testing.T) {
	m := NewMcpManager("", nil)
	app := &App{mcpManager: m}
	cfg := ConfigState{}
	var lists, calls int
	var arguments any
	c := client.NewClient(mcpRegistryTransport{send: func(_ context.Context, req transport.JSONRPCRequest) (string, error) {
		switch req.Method {
		case "tools/list":
			lists++
			if lists == 1 {
				return `{"nextCursor":"page-2","tools":[{"name":"compare","description":"  Compare\n values.  ","inputSchema":{"type":"object","allOf":[{"required":["value"]}],"properties":{"value":{"type":"integer"},"stage":{"type":"integer"}}}}]}`, nil
			}
			if lists == 2 {
				if req.Params.(map[string]string)["cursor"] != "page-2" {
					return "", fmt.Errorf("missing page cursor: %v", req.Params)
				}
				return `{"tools":[{"name":"add","title":"Add a value","inputSchema":{"type":"object"}},{"name":"hidden","inputSchema":{"type":"object"}}]}`, nil
			}
			return `{"tools":[{"name":"compare","description":"Compare values.","inputSchema":{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}}}]}`, nil
		case "tools/call":
			calls++
			raw, _ := json.Marshal(req.Params)
			arguments = string(raw)
			return `{"structuredContent":{"matched":true},"content":[{"type":"image","mimeType":"image/png","data":"AAAA"}]}`, nil
		default:
			return "", fmt.Errorf("unexpected RPC %s", req.Method)
		}
	}}, client.WithSession())
	tools, err := listMcpTools(t.Context(), "demo", c)
	if err != nil || len(tools) != 3 {
		t.Fatalf("paginated discovery = %v, %v", tools, err)
	}
	token := &mcpConnToken{}
	m.clients["demo"] = &McpClientHandle{ServerName: "demo", Status: "connected", Client: c,
		token: token, ToolDefs: tools, Config: McpServerConfig{DisabledTools: []string{"hidden"}}}
	ctx := withBuiltinToolAvailability(t.Context(), app.buildToolsForConfig(cfg))
	execute := func(ctx context.Context, name, raw string) toolResult {
		return app.executeTool(ctx, cfg, "", name, []byte(raw))
	}
	catalog := app.ensureMcpCatalog(ctx, cfg, nil)
	lookup := execute(ctx, "ToolSearch", `{"query":["demo.compare","demo.add","demo.hidden","demo.Compare"]}`)
	raw := compactToolResultForModel("ToolSearch", lookup, "")
	if !lookup.OK || !strings.Contains(raw, `"allOf":[{"required":["value"]}]`) ||
		!strings.Contains(raw, `"title":"Add a value"`) || !strings.Contains(raw, "E_MCP_DISABLED") || !strings.Contains(raw, "E_MCP_NOT_FOUND") || lists != 2 || calls != 0 {
		t.Fatalf("batch lookup = %s (lists=%d calls=%d)", raw, lists, calls)
	}
	if result := execute(ctx, "ToolExec", `{"name":"demo.compare","arguments":{}}`); result.ErrorCode != "E_BAD_ARGS" || calls != 0 {
		t.Fatalf("root allOf not enforced: %+v", result)
	}
	if result := execute(ctx, "ToolExec", `{"name":"demo.compare","arguments":{"value":9007199254740993,"stage":6},"stage":1}`); !result.OK ||
		!strings.Contains(compactToolResultForModel("ToolExec", result, ""), "matched") ||
		!strings.Contains(compactToolResultForModel("ToolExec", result, ""), "image/png") ||
		!strings.Contains(fmt.Sprint(arguments), `"arguments":{"stage":6,"value":9007199254740993}`) {
		t.Fatalf("direct execution = %+v, forwarded = %s", result, arguments)
	}
	m.refreshServerTools("demo", token)
	if result := execute(ctx, "ToolExec", `{"name":"demo.compare","arguments":{"value":1}}`); result.ErrorCode != "E_BAD_ARGS" || calls != 1 {
		t.Fatalf("old schema accepted after refresh: %+v", result)
	}
	if result := execute(ctx, "ToolExec", `{"name":"demo.add","arguments":{}}`); result.ErrorCode != "E_MCP_NOT_FOUND" || calls != 1 {
		t.Fatalf("removed tool remained callable: %+v", result)
	}
	if result := execute(ctx, "ToolExec", `{"name":"demo.hidden","arguments":{}}`); result.ErrorCode != "E_MCP_DISABLED" {
		t.Fatalf("blacklisted tool error: %+v", result)
	}
	searchOnly := withBuiltinToolAvailability(ctx, []openai.Tool{rawFunctionTool("ToolSearch", "", nil)})
	if result := execute(searchOnly, "ToolSearch", `{"query":["demo.compare"]}`); result.ErrorCode != "E_MCP_PERMISSION" {
		t.Fatalf("restricted agent looked up a tool: %+v", result)
	}
	if result := execute(searchOnly, "ToolExec", `{"name":"demo.compare","arguments":{"value":"x"}}`); result.ErrorCode != "E_TOOL_DISABLED" {
		t.Fatalf("restricted agent executed a tool: %+v", result)
	}
	// A refreshed pool does not append a second catalog: the conversation keeps
	// the directory it started with, and the registry rejects the removed tool.
	if next := app.ensureMcpCatalog(ctx, cfg, catalog); len(next) != 1 || !reflect.DeepEqual(next, catalog) {
		t.Fatalf("refresh appended another catalog: %v", next)
	}
}

func TestMcpExecParallelTargetsAndStages(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	entered := make(chan string, 3)
	release := make(chan struct{})
	var calls atomic.Int32
	c := client.NewClient(mcpRegistryTransport{send: func(ctx context.Context, req transport.JSONRPCRequest) (string, error) {
		body, _ := json.Marshal(req.Params)
		calls.Add(1)
		entered <- string(body)
		select {
		case <-release:
			return `{"content":[{"type":"text","text":"ok"}]}`, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}, client.WithSession())
	m := NewMcpManager("", nil)
	app := &App{mcpManager: m}
	// These raw pairs would collide if the server segment were concatenated unescaped.
	for server, tool := range map[string]string{"a.b": "c", "a": "b.c"} {
		m.clients[server] = &McpClientHandle{ServerName: server, Status: "connected", Client: c,
			ToolDefs: []McpDiscoveredTool{{ServerName: server, Name: tool, Schema: map[string]any{"type": "object"}}}}
	}
	cfg := ConfigState{ToolConcurrency: 2}
	ctx = withBuiltinToolAvailability(ctx, app.buildToolsForConfig(cfg))
	var batch []openai.ToolCall
	for _, args := range []string{
		`{"name":"a%2Eb.c","arguments":{"purpose":"business","stage":9},"purpose":"one"}`,
		`{"name":"a.b.c","arguments":{"purpose":"business","stage":9}}`,
		`{"name":"a%2Eb.c","arguments":{"stage":9,"purpose":"business"},"stage":0,"purpose":"two"}`,
		`{"name":"a%2Eb.c","arguments":{"stage":9,"purpose":"business"},"stage":1}`,
	} {
		batch = append(batch, openai.ToolCall{Function: openai.FunctionCall{Name: "ToolExec", Arguments: args}})
	}
	var results [4]toolResult
	done := make(chan struct{})
	go func() {
		defer close(done)
		conflicts := detectToolBatchConflicts(cfg, batch, toolPurposeFields(ctx))
		app.executeStagedToolBatch(ctx, cfg, batch, conflicts, func(callCtx context.Context, _ int, call openai.ToolCall) toolResult {
			return app.executeTool(callCtx, cfg, "", call.Function.Name, []byte(call.Function.Arguments))
		}, func(i int, _ openai.ToolCall, result toolResult, _ int64) { results[i] = result })
	}()
	var firstStage []string
	for range 2 {
		select {
		case name := <-entered:
			firstStage = append(firstStage, name)
		case <-ctx.Done():
			t.Fatal("both stage-zero targets did not start concurrently")
		}
	}
	close(release)
	<-done
	targets := strings.Join(firstStage, "\n")
	if !strings.Contains(targets, `"name":"c"`) || !strings.Contains(targets, `"name":"b.c"`) ||
		calls.Load() != 3 || !results[0].OK || !results[1].OK || results[2].ErrorCode != "E_DUPLICATE_TOOL_CALL" || !results[3].OK {
		t.Fatalf("first stage = %v, results = %+v, calls = %d", firstStage, results, calls.Load())
	}
}

func TestMcpCatalogPreservesRequestPrefix(t *testing.T) {
	m := NewMcpManager("", nil)
	app := &App{mcpManager: m}
	cfg := ConfigState{}
	tools := app.buildToolsForSession("chat", cfg)
	ctx := withBuiltinToolAvailability(t.Context(), tools)
	m.clients["srv"] = &McpClientHandle{ServerName: "srv", Status: "connected", ToolDefs: []McpDiscoveredTool{
		{ServerName: "srv", Name: "lookup", Description: "Find an item </mcp_tools>"},
	}}
	initial := []openai.ChatCompletionMessage{{Role: "system", Content: "base"}, {Role: "user", Content: "first"}}
	initial = app.ensureMcpCatalog(ctx, cfg, initial)
	if len(initial) != 3 || strings.Count(initial[2].Content, "</mcp_tools>") != 1 {
		t.Fatalf("external text escaped its catalog block: %v", initial)
	}
	initial = append(initial, openai.ChatCompletionMessage{Role: "assistant", Content: "response"})
	saved := sanitizeHistoryMessagesForDisk(initial)
	m.mu.Lock()
	m.clients["srv"].Config.DisabledTools = []string{"lookup"}
	m.catalogReady = false
	m.mu.Unlock()
	next := append([]openai.ChatCompletionMessage{initial[0]}, saved...)
	next = append(next, openai.ChatCompletionMessage{Role: "user", Content: "second"})
	next = app.ensureMcpCatalog(ctx, cfg, next)
	if len(next) != len(initial)+1 || !reflect.DeepEqual(next[:len(initial)], initial) || next[len(next)-1].Content != "second" {
		t.Fatalf("a catalog change re-sent the catalog or rewrote history: %v", next)
	}
	if result := app.executeTool(ctx, cfg, "", "ToolExec", []byte(`{"name":"srv.lookup","arguments":{}}`)); result.ErrorCode != "E_MCP_DISABLED" {
		t.Fatalf("historical catalog authorized disabled tool: %+v", result)
	}
	freshTools := app.buildToolsForConfig(cfg)
	if !reflect.DeepEqual(tools, freshTools) {
		t.Fatal("registry changes altered the wire tools")
	}
	for _, auth := range []string{"", authChatGPTOAuth} {
		cfg.AuthType = auth
		before := buildOpenAIResponsesRequest(cfg, "test-model", initial, tools, nil)
		after := buildOpenAIResponsesRequest(cfg, "test-model", next, freshTools, nil)
		if !reflect.DeepEqual(before.Tools, after.Tools) {
			t.Fatalf("Responses tool prefix changed for auth %q", auth)
		}
		_, beforeInput := buildOpenAIResponsesInput(initial, nil)
		_, afterInput := buildOpenAIResponsesInput(next, nil)
		if len(afterInput) <= len(beforeInput) || !reflect.DeepEqual(afterInput[:len(beforeInput)], beforeInput) {
			t.Fatalf("Responses history prefix changed for auth %q", auth)
		}
	}
	beforeSystem, beforeAnthropic := buildAnthropicMessages(initial, nil, true)
	afterSystem, afterAnthropic := buildAnthropicMessages(next, nil, true)
	if beforeSystem != afterSystem || len(afterAnthropic) <= len(beforeAnthropic) ||
		!reflect.DeepEqual(afterAnthropic[:len(beforeAnthropic)], beforeAnthropic) {
		t.Fatal("Anthropic history prefix changed")
	}
}

// ToolSearch resolves exact IDs and bare server_ids only; nothing matches on
// description or partial names, and a batch comes back in the caller's order.
func TestMcpToolSearchResolvesExactIDsAndServers(t *testing.T) {
	m := NewMcpManager("", nil)
	app := &App{mcpManager: m}
	cfg := ConfigState{}
	defs := make([]McpDiscoveredTool, 0, 13)
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("issue%d", i)
		defs = append(defs, McpDiscoveredTool{ServerName: "demo", Name: name, FunctionName: "mcp__demo__" + name,
			Description: "Handle an issue record.", Schema: map[string]any{"type": "object"}})
	}
	defs = append(defs, McpDiscoveredTool{ServerName: "demo", Name: "compare", FunctionName: "mcp__demo__compare",
		Description: "Compare values.", Schema: map[string]any{"type": "object"}})
	m.clients["demo"] = &McpClientHandle{ServerName: "demo", Status: "connected", ToolDefs: defs}
	ctx := withBuiltinToolAvailability(t.Context(), app.buildToolsForConfig(cfg))

	search := func(raw string) []mcpSearchMatch {
		t.Helper()
		res := app.executeTool(ctx, cfg, "", "ToolSearch", []byte(raw))
		if !res.OK {
			t.Fatalf("search %s failed: %+v", raw, res)
		}
		payload, ok := res.Data.(map[string]any)
		if !ok {
			t.Fatalf("search %s returned %T", raw, res.Data)
		}
		tools, _ := payload["tools"].([]mcpSearchMatch)
		return tools
	}

	// Exact IDs come back in the caller's order, each carrying its definition.
	got := search(`{"query":["demo.issue3","demo.compare"]}`)
	if len(got) != 2 || got[0].Name != "demo.issue3" || got[1].Name != "demo.compare" {
		t.Fatalf("exact batch lost its order: %+v", got)
	}
	if got[1].Definition == nil {
		t.Fatalf("an exact hit must carry its definition: %+v", got[1])
	}

	// A bare server_id returns the whole group, not a page of it.
	got = search(`{"query":["demo"]}`)
	if len(got) != len(defs) || got[0].Name != "demo.compare" {
		t.Fatalf("server_id returned %d of %d tools: %+v", len(got), len(defs), got)
	}

	// No fuzzy path: a description word is a miss, and a miss is a failed call —
	// the same verdict ToolExec gives an unusable id, not a successful result
	// carrying an error entry.
	if res := app.executeTool(ctx, cfg, "", "ToolSearch", []byte(`{"query":["issue"]}`)); res.OK || res.ErrorCode != "E_MCP_NOT_FOUND" || !strings.Contains(res.Error, "server_id") {
		t.Fatalf("a description word must not match: %+v", res)
	}

	// A disabled tool keeps its own code instead of degrading to a miss.
	m.clients["demo"].Config.DisabledTools = []string{"compare"}
	if res := app.executeTool(ctx, cfg, "", "ToolSearch", []byte(`{"query":["demo.compare"]}`)); res.OK || res.ErrorCode != "E_MCP_DISABLED" {
		t.Fatalf("a disabled tool must report E_MCP_DISABLED: %+v", res)
	}
}
