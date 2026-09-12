// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestToolSchemaToMapProducesProviderSafeSchema(t *testing.T) {
	schema := mcp.ToolInputSchema{
		Type:       "object",
		Properties: map[string]any{},
		Defs: map[string]any{
			"options": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   nil,
			},
		},
		AdditionalProperties: false,
	}

	got := toolSchemaToMap(schema)
	if got["type"] != "object" {
		t.Fatalf("type = %#v, want object", got["type"])
	}
	if _, ok := got["properties"].(map[string]any); !ok {
		t.Fatalf("properties must be an object, got %#v", got["properties"])
	}
	if required, exists := got["required"]; exists && required == nil {
		t.Fatal("required must be omitted or an array, not null")
	}
	if got["additionalProperties"] != false {
		t.Fatalf("additionalProperties was not preserved: %#v", got)
	}
	defs, ok := got["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("$defs was not preserved: %#v", got)
	}
	nested := defs["options"].(map[string]any)
	if required, exists := nested["required"]; exists && required == nil {
		t.Fatal("nested required must be omitted or an array, not null")
	}
}

func TestMcpTransportName(t *testing.T) {
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
		if got := mcpTransportName(tt.cfg); got != tt.want {
			t.Fatalf("mcpTransportName(%#v) = %q, want %q", tt.cfg, got, tt.want)
		}
	}
}

func TestLoadConfigsKeepsDisabledServersAndWarnsOnBrokenJSON(t *testing.T) {
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
	// Disabled servers must stay in the load result so the status list can
	// show configured-but-off entries.
	if _, ok := configs["disabled"]; !ok {
		t.Fatal("disabled MCP server must stay visible in LoadConfigs")
	}
	if _, ok := configs["enabled"]; !ok {
		t.Fatal("enabled MCP server should be loaded")
	}

	// A broken mcp.json must surface a warning instead of silently unloading
	// every server.
	writeTestFile(t, home, ".ally_agent/mcp.json", `{ broken json`)
	var warnings []string
	manager.SetWarningHandler(func(message string) { warnings = append(warnings, message) })
	if _, err := manager.LoadConfigs(); err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Fatal("broken mcp.json must produce a warning")
	}
}

func TestStartAllRegistersDisabledServersWithoutConnecting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestFile(t, home, ".ally_agent/mcp.json", `{
  "mcpServers": {
    "off": { "enabled": false, "url": "https://example.com/off" }
  }
}`)

	manager := NewMcpManager(t.TempDir(), nil)
	if err := manager.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	statuses := manager.GetServerStatuses()
	if len(statuses) != 1 {
		t.Fatalf("expected exactly one server status, got %+v", statuses)
	}
	if statuses[0]["status"] != "disabled" {
		t.Fatalf("disabled server must be registered as disabled, got %+v", statuses[0])
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

func TestMcpManagerReconcileKeepsUnchangedServers(t *testing.T) {
	enabled := true
	disabled := false
	configFile := filepath.Join(t.TempDir(), "mcp.json")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(configFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"mcpServers":{
		"a":{"command":"x","enabled":true},
		"b":{"command":"y","enabled":false}
	}}`)
	manager := NewMcpManager(t.TempDir(), func(tools []McpDiscoveredTool) {})
	manager.configPaths = []string{configFile}
	// Simulated running state: a connected with the same config, b disabled
	// with the same config, and a leftover server "old" whose config entry is
	// gone. Handles carry no real clients so nothing external is touched.
	manager.clients["a"] = &McpClientHandle{ServerName: "a", Config: McpServerConfig{Command: "x", Enabled: &enabled}, Status: "connected"}
	bHandle := &McpClientHandle{ServerName: "b", Config: McpServerConfig{Command: "y", Enabled: &disabled}, Status: "disabled"}
	manager.clients["b"] = bHandle
	manager.clients["old"] = &McpClientHandle{ServerName: "old", Status: "connected"}
	manager.replaceToolLookupLocked("old", []McpDiscoveredTool{{ServerName: "old", Name: "tool", FunctionName: "mcp__old__tool"}})

	changed, err := manager.ReconcileConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("only the removed server should change state, got %d", changed)
	}
	if _, exists := manager.clients["old"]; exists {
		t.Fatal("removed server must be disconnected")
	}
	if _, exists := manager.toolLookup["mcp__old__tool"]; exists {
		t.Fatal("removed server's tool lookup must be dropped")
	}
	if manager.clients["b"] != bHandle {
		t.Fatal("unchanged server must keep its handle without teardown")
	}
	if manager.clients["a"] == nil || manager.clients["a"].Status != "connected" {
		t.Fatalf("unchanged server must keep its live handle, got %#v", manager.clients["a"])
	}

	// Change a to disabled and add c (disabled): both register disabled
	// handles without any connection attempt; b stays untouched.
	write(`{"mcpServers":{
		"a":{"command":"x","enabled":false},
		"b":{"command":"y","enabled":false},
		"c":{"command":"z","enabled":false}
	}}`)
	changed, err = manager.ReconcileConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if changed != 2 {
		t.Fatalf("a (changed) and c (added) should change state, got %d", changed)
	}
	if manager.clients["a"] == nil || manager.clients["a"].Status != "disabled" {
		t.Fatalf("changed server should re-register as disabled, got %#v", manager.clients["a"])
	}
	if manager.clients["b"] != bHandle {
		t.Fatal("untouched server must keep its handle across reconciles")
	}
	if manager.clients["c"] == nil || manager.clients["c"].Status != "disabled" {
		t.Fatalf("added disabled server should register a disabled handle, got %#v", manager.clients["c"])
	}
}

func TestMcpServerConfigEqualNormalizesEnabledFlag(t *testing.T) {
	enabled := true
	a := McpServerConfig{Command: "x"}
	b := McpServerConfig{Command: "x", Enabled: &enabled}
	if !mcpServerConfigEqual(a, b) {
		t.Fatal("omitted enabled must equal explicit enabled:true")
	}
	off := false
	if mcpServerConfigEqual(a, McpServerConfig{Command: "x", Enabled: &off}) {
		t.Fatal("omitted enabled must differ from explicit enabled:false")
	}
	if mcpServerConfigEqual(McpServerConfig{Command: "x"}, McpServerConfig{Command: "y"}) {
		t.Fatal("different commands must not be equal")
	}
}

// disabledTools 只影响注入过滤：相等性比较必须忽略它，否则勾选一个工具
// 就会触发 reconcile 断开重连（stdio 场景重启子进程）。
func TestMcpServerConfigEqualIgnoresDisabledTools(t *testing.T) {
	base := McpServerConfig{Command: "x"}
	withList := McpServerConfig{Command: "x", DisabledTools: []string{"write_file"}}
	if !mcpServerConfigEqual(base, withList) {
		t.Fatal("disabledTools change must not count as a connection-relevant config change")
	}
	if !mcpServerConfigEqual(
		McpServerConfig{Command: "x", DisabledTools: []string{"a", "b"}},
		McpServerConfig{Command: "x", DisabledTools: []string{"b"}},
	) {
		t.Fatal("different disabledTools must stay equal for connection semantics")
	}
}

// 注入过滤：黑名单内工具不进 GetEnabledTools，但 GetAllTools（清单层）仍全量可见；
// 黑名单按「对端原始工具名」匹配，未列出的工具（含对端新增）默认启用。
func TestGetEnabledToolsFiltersDisabledTools(t *testing.T) {
	manager := NewMcpManager(t.TempDir(), nil)
	manager.clients = map[string]*McpClientHandle{
		"fs": {
			ServerName: "fs",
			Status:     "connected",
			Config:     McpServerConfig{Command: "x", DisabledTools: []string{" write_file ", "", "write_file"}},
			ToolDefs: []McpDiscoveredTool{
				{ServerName: "fs", Name: "read_file", FunctionName: "mcp__fs__read_file"},
				{ServerName: "fs", Name: "write_file", FunctionName: "mcp__fs__write_file"},
			},
		},
	}
	// disabled server: 全部工具都不注入也不出现在任一视图
	manager.clients["off"] = &McpClientHandle{ServerName: "off", Status: "disabled"}

	enabled := manager.GetEnabledTools()
	if len(enabled) != 1 || enabled[0].Name != "read_file" {
		t.Fatalf("enabled view must drop blacklisted tools, got %#v", enabled)
	}
	all := manager.GetAllTools()
	if len(all) != 2 {
		t.Fatalf("inventory view must stay all-visible, got %#v", all)
	}
	if !manager.IsToolDisabled("fs", "write_file") || manager.IsToolDisabled("fs", "read_file") {
		t.Fatal("IsToolDisabled must match the normalized blacklist only")
	}
	if manager.IsToolDisabled("unknown-server", "anything") {
		t.Fatal("unknown server tools default to enabled")
	}
}

// reconcile 兼容语义：勾选变化原地生效（连接保持、handle 指针不变）；
// 新增 disabledTools 条目不影响 changed 计数以外的语义。
func TestReconcileConfigsAppliesDisabledToolsInPlace(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "mcp.json")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(configFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"mcpServers":{"a":{"command":"x"}}}`)
	manager := NewMcpManager(t.TempDir(), func(tools []McpDiscoveredTool) {})
	manager.configPaths = []string{configFile}
	handle := &McpClientHandle{ServerName: "a", Config: McpServerConfig{Command: "x"}, Status: "connected"}
	manager.clients["a"] = handle

	write(`{"mcpServers":{"a":{"command":"x","disabledTools":["write_file"]}}}`)
	changed, err := manager.ReconcileConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("in-place tool toggle should count as touched, got %d", changed)
	}
	if manager.clients["a"] != handle || handle.Status != "connected" {
		t.Fatal("tool-only change must keep the live handle without reconnect")
	}
	if !manager.IsToolDisabled("a", "write_file") {
		t.Fatalf("handle config must carry the new blacklist, got %#v", handle.Config)
	}

	// 未勾选的服务器再次 reconcile：无变化时不 touch。
	changed, err = manager.ReconcileConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Fatalf("reconciling an unchanged config must be a no-op, got %d", changed)
	}
}

func TestIsMcpRecoverableError(t *testing.T) {
	recoverableCases := []string{
		"invalid session ID: 123",
		"write: broken pipe",
		"read: closed pipe",
		"read tcp 127.0.0.1: connection reset by peer",
		"dial tcp 127.0.0.1: connection refused",
		"unexpected EOF",
		"transport is closing",
		"client is closed",
	}
	for _, msg := range recoverableCases {
		if !isMcpRecoverableError(errors.New(msg)) {
			t.Fatalf("expected error %q to be recoverable", msg)
		}
	}

	nonRecoverableCases := []string{
		"validation failed: parameter 'foo' is required",
		"tool not found: sample_tool",
		"unsupported operation",
		"permission denied",
	}
	for _, msg := range nonRecoverableCases {
		if isMcpRecoverableError(errors.New(msg)) {
			t.Fatalf("expected error %q to not be recoverable", msg)
		}
	}
}

func TestMcpToolResultTextRendersAllSupportedContentTypes(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Annotated: mcp.Annotated{}, Type: "text", Text: "hello"},
			mcp.ImageContent{Annotated: mcp.Annotated{}, Type: "image", Data: "AAAA", MIMEType: "image/png"},
			mcp.AudioContent{Annotated: mcp.Annotated{}, Type: "audio", Data: "BBBB", MIMEType: "audio/wav"},
			mcp.ResourceLink{Annotated: mcp.Annotated{}, Type: "resource_link", URI: "file:///tmp/a.csv", Name: "a.csv"},
			mcp.EmbeddedResource{
				Annotated: mcp.Annotated{},
				Type:      "embedded resource",
				Resource:  mcp.TextResourceContents{URI: "file:///tmp/b.txt", MIMEType: "text/plain", Text: "resource body"},
			},
			mcp.EmbeddedResource{
				Annotated: mcp.Annotated{},
				Type:      "embedded resource",
				Resource:  mcp.BlobResourceContents{URI: "file:///tmp/c.bin", MIMEType: "application/octet-stream", Blob: "CCCC"},
			},
		},
		StructuredContent: map[string]any{"rows": 1},
	}

	got := mcpToolResultText(result)
	for _, want := range []string{
		"hello",
		"[image content: image/png, 4 bytes base64 data omitted]",
		"[audio content: audio/wav, 4 bytes base64 data omitted]",
		"[resource link: a.csv (file:///tmp/a.csv)]",
		"resource body",
		"[embedded resource file:///tmp/c.bin: application/octet-stream, 4 bytes base64 data omitted]",
		`structured output: {"rows":1}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "AAAA") || strings.Contains(got, "BBBB") || strings.Contains(got, "CCCC") {
		t.Fatalf("base64 payloads must not leak into model output:\n%s", got)
	}
}

func TestMcpToolResultTextStructuredOnlyServerIsNotEmpty(t *testing.T) {
	result := &mcp.CallToolResult{
		Content:           nil,
		StructuredContent: map[string]any{"status": "ok"},
	}
	got := mcpToolResultText(result)
	if got != `structured output: {"status":"ok"}` {
		t.Fatalf("structured-only result = %q", got)
	}
}
