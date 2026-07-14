package main

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizedMcpTransport(t *testing.T) {
	tests := []struct {
		cfg  McpServerConfig
		want string
	}{
		{cfg: McpServerConfig{Command: "npx"}, want: "stdio"},
		{cfg: McpServerConfig{Transport: "sse", URL: "https://example.com/sse"}, want: "sse"},
		{cfg: McpServerConfig{URL: "https://example.com/mcp"}, want: "streamable-http"},
		{cfg: McpServerConfig{Transport: "http", URL: "https://example.com/mcp"}, want: "streamable-http"},
	}
	for _, tt := range tests {
		if got := normalizedMcpTransport(tt.cfg); got != tt.want {
			t.Fatalf("normalizedMcpTransport(%#v) = %q, want %q", tt.cfg, got, tt.want)
		}
	}
}

func TestLoadConfigsSkipsDisabledServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestFile(t, home, ".ally_agent/mcp.json", `{
  "mcpServers": {
    "disabled": { "enabled": false, "url": "https://example.com/disabled" },
    "enabled": { "enabled": true, "url": "https://example.com/enabled" }
  }
}`)

	manager := NewMcpManager(t.TempDir(), nil)
	configs, err := manager.LoadConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := configs["disabled"]; ok {
		t.Fatal("disabled MCP server should not be loaded")
	}
	if _, ok := configs["enabled"]; !ok {
		t.Fatal("enabled MCP server should be loaded")
	}
}

func TestMcpToolFunctionNameIsSafeForChineseServerNames(t *testing.T) {
	name := mcpToolFunctionName("知乎全网搜索", "search")
	if !strings.HasPrefix(name, "mcp__zhihu_web_search_") {
		t.Fatalf("unexpected function name prefix: %s", name)
	}
	if len(name) > 64 {
		t.Fatalf("function name too long: %d %s", len(name), name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		t.Fatalf("function name contains invalid rune %q in %s", r, name)
	}
}

func TestGetAllToolsReturnsDeterministicOrder(t *testing.T) {
	manager := NewMcpManager(t.TempDir(), nil)
	manager.clients = map[string]*McpClientHandle{
		"z-server": {
			Status: "connected",
			ToolDefs: []McpDiscoveredTool{
				{ServerName: "z-server", Name: "beta", FunctionName: "mcp__z__beta"},
				{ServerName: "z-server", Name: "alpha", FunctionName: "mcp__z__alpha"},
			},
		},
		"a-server": {
			Status:   "connected",
			ToolDefs: []McpDiscoveredTool{{ServerName: "a-server", Name: "search", FunctionName: "mcp__a__search"}},
		},
	}

	tools := manager.GetAllTools()
	got := make([]string, 0, len(tools))
	for _, tool := range tools {
		got = append(got, tool.ServerName+"/"+tool.Name)
	}
	want := []string{"a-server/search", "z-server/alpha", "z-server/beta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected MCP tool order: got %v want %v", got, want)
	}
}

func TestIsMcpInvalidSessionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "zhihu sse invalid session id",
			err:  errors.New(`MCP call failed: transport error: request failed with status 400: {"jsonrpc":"2.0","id":null,"error":{"code":-32602,"message":"Invalid session ID"}}`),
			want: true,
		},
		{
			name: "session expired",
			err:  errors.New("remote MCP session expired"),
			want: true,
		},
		{
			name: "ordinary validation error",
			err:  errors.New("MCP call failed: invalid query parameter"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMcpInvalidSessionError(tc.err); got != tc.want {
				t.Fatalf("isMcpInvalidSessionError() = %v, want %v", got, tc.want)
			}
		})
	}
}
