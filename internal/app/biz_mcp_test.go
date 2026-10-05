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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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

// MCP 工具不入参数闸门（没有内置声明可校验），所以一个拼错或多传的参数只能
// 靠这条警告露面；声明里没有 properties 的工具则保持安静，否则每个参数都会被点名。
func TestMcpUnknownArgWarningsNamesUndeclaredArguments(t *testing.T) {
	app := NewApp()
	manager := NewMcpManager(t.TempDir(), nil)
	manager.clients = map[string]*McpClientHandle{
		"fs": {
			ServerName: "fs",
			Status:     "connected",
			Config:     McpServerConfig{Command: "x"},
			ToolDefs: []McpDiscoveredTool{
				{
					ServerName:   "fs",
					Name:         "read_file",
					FunctionName: "mcp__fs__read_file",
					Schema: map[string]any{
						"type":       "object",
						"properties": map[string]any{"path": map[string]any{"type": "string"}},
					},
				},
				{ServerName: "fs", Name: "probe", FunctionName: "mcp__fs__probe"},
			},
		},
	}
	app.mcpManager = manager

	warnings := app.mcpUnknownArgWarnings("mcp__fs__read_file", map[string]any{"path": "a.txt", "recursive": true, "dept": 2})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "dept, recursive") {
		t.Fatalf("undeclared MCP arguments must be named together, got %#v", warnings)
	}
	if got := app.mcpUnknownArgWarnings("mcp__fs__read_file", map[string]any{"path": "a.txt"}); len(got) != 0 {
		t.Fatalf("declared arguments must stay silent, got %#v", got)
	}
	if got := app.mcpUnknownArgWarnings("mcp__fs__probe", map[string]any{"anything": 1}); len(got) != 0 {
		t.Fatalf("a tool without declared properties must not flag every argument, got %#v", got)
	}
	if got := app.mcpUnknownArgWarnings("mcp__missing__tool", map[string]any{"x": 1}); len(got) != 0 {
		t.Fatalf("a tool outside the current set must stay silent, got %#v", got)
	}
}

// 注入过滤：黑名单内工具不进 GetEnabledTools，但 GetAllTools（清单层）仍全量可见；
// 黑名单按「对端原始工具名」匹配，未列出的工具（含对端新增）默认启用。
// 连接状态不参与过滤：failed 服务端已发现的工具照常注入（连接无状态，调用时
// 按需重连兜底），只有用户显式关掉的 disabled server 才整体排除。
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
		// 远端断流后的形态：清单保留，状态 failed——工具必须仍在模型视图里，
		// 否则冻结的会话工具集从此缺它，模型再也调不到，只能重启应用。
		"dead": {
			ServerName: "dead",
			Status:     "failed",
			Error:      "MCP connection lost: stream error: stream ID 1; INTERNAL_ERROR; received from peer",
			ToolDefs:   []McpDiscoveredTool{{ServerName: "dead", Name: "search", FunctionName: "mcp__dead__search"}},
		},
	}
	// disabled server: 全部工具都不注入也不出现在任一视图
	manager.clients["off"] = &McpClientHandle{ServerName: "off", Status: "disabled"}

	enabled := manager.GetEnabledTools()
	if len(enabled) != 2 || enabled[0].ServerName != "dead" || enabled[1].Name != "read_file" {
		t.Fatalf("enabled view must keep failed-server tools and drop blacklisted ones, got %#v", enabled)
	}
	all := manager.GetAllTools()
	if len(all) != 3 {
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

func TestIsMcpTransportFailure(t *testing.T) {
	// mcp-go 自己的哨兵错误必须命中：手写子串清单曾经漏掉它们，于是 stdio
	// 子进程死了、HTTP 会话 404 过期了都不重连（重连形同虚设）。
	sentinels := []error{
		transport.ErrTransportClosed,
		transport.ErrSessionTerminated,
		fmt.Errorf("MCP call failed: %w", transport.ErrTransportClosed),
		fmt.Errorf("transport error: failed to send request: %w", transport.ErrSessionTerminated),
	}
	for _, err := range sentinels {
		if !isMcpTransportFailure(err) {
			t.Fatalf("expected sentinel error %v to count as a transport failure", err)
		}
	}

	recoverableCases := []string{
		"invalid session ID: 123",
		// The transport error that motivated the marker list: a full JSON-RPC
		// payload, not just the bare marker substring.
		`MCP call failed: transport error: request failed with status 400: {"jsonrpc":"2.0","id":null,"error":{"code":-32602,"message":"Invalid session ID"}}`,
		"remote MCP session expired",
		"write: broken pipe",
		"read: closed pipe",
		"read tcp 127.0.0.1: connection reset by peer",
		"dial tcp 127.0.0.1: connection refused",
		"unexpected EOF",
		"transport is closing",
		"client is closed",
		// 两个 mcp-go 没有导出哨兵、只能按文案认的真实错误。
		"transport error: transport closed",
		"transport error: connection has been closed",
	}
	for _, msg := range recoverableCases {
		if !isMcpTransportFailure(errors.New(msg)) {
			t.Fatalf("expected error %q to be a transport failure", msg)
		}
	}

	// 标准 JSON-RPC 错误码：服务端收到了请求、明确拒绝，与链路无关，绝不重连。
	protocolErrors := []error{
		mcp.ErrInvalidParams,
		mcp.ErrMethodNotFound,
		fmt.Errorf("%w: missing required field", mcp.ErrInvalidParams),
	}
	for _, err := range protocolErrors {
		if isMcpTransportFailure(err) {
			t.Fatalf("protocol error %v must not count as a transport failure", err)
		}
	}

	nonRecoverableCases := []string{
		"validation failed: parameter 'foo' is required",
		"tool not found: sample_tool",
		"unsupported operation",
		"permission denied",
	}
	for _, msg := range nonRecoverableCases {
		if isMcpTransportFailure(errors.New(msg)) {
			t.Fatalf("expected error %q to not be a transport failure", msg)
		}
	}
	if isMcpTransportFailure(nil) {
		t.Fatal("a nil error must never be a transport failure")
	}
}

func TestMcpCallFailureClassification(t *testing.T) {
	// 服务端状态决定"能不能靠重连自愈"：failed 可以（上一次连接死了），
	// disabled / connecting 不行——前者是用户明确关掉的，后者已有连接在飞。
	cases := []struct {
		status string
		want   mcpCallFailure
	}{
		{"failed", mcpCallReconnectable},
		{"disabled", mcpCallUnavailable},
		{"connecting", mcpCallUnavailable},
	}
	for _, tc := range cases {
		manager := NewMcpManager("", nil)
		manager.clients["srv"] = &McpClientHandle{ServerName: "srv", Status: tc.status}
		outcome, err := manager.callToolOnce(context.Background(), "srv", "tool", nil)
		if err == nil {
			t.Fatalf("status %q must fail the call", tc.status)
		}
		if outcome.failure != tc.want {
			t.Fatalf("status %q: got failure %d want %d", tc.status, outcome.failure, tc.want)
		}
	}

	manager := NewMcpManager("", nil)
	outcome, err := manager.callToolOnce(context.Background(), "missing", "tool", nil)
	if err == nil || outcome.failure != mcpCallUnavailable {
		t.Fatalf("unknown server must be unavailable, got %#v / %v", outcome, err)
	}
}

func TestCallToolDoesNotRetryToolLevelError(t *testing.T) {
	// 工具自己回的错误文案里带 "connection refused"（下游连不上，很常见）时
	// 绝不能重连：旧实现会杀掉并重启整个 MCP 服务端、再把工具跑第二遍。
	var calls atomic.Int64
	mcpServer := server.NewMCPServer("test-server", "1.0.0", server.WithToolCapabilities(true))
	mcpServer.AddTool(mcp.NewTool("flaky"), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return mcp.NewToolResultError("downstream write failed: connection refused"), nil
	})

	mcpClient, err := client.NewInProcessClient(mcpServer)
	if err != nil {
		t.Fatalf("in-process client: %v", err)
	}
	defer mcpClient.Close()
	if err := mcpClient.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{Name: "ally-test", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(context.Background(), initRequest); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	manager := NewMcpManager("", nil)
	handle := &McpClientHandle{ServerName: "srv", Client: mcpClient, Status: "connected", token: &mcpConnToken{}}
	manager.clients["srv"] = handle

	_, err = manager.CallTool(context.Background(), "srv", "flaky", map[string]any{})
	if err == nil {
		t.Fatal("the tool's isError result must surface as an error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("the server's error text must reach the caller, got %v", err)
	}
	if strings.Contains(err.Error(), "reconnect") {
		t.Fatalf("a tool-level error must not trigger a reconnect, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the tool must run exactly once, ran %d times", got)
	}
	if handle.Status != "connected" || handle.Client != mcpClient {
		t.Fatalf("handle must stay untouched, got %+v", handle)
	}
}

func TestHandleConnectionLostRequiresCurrentConnection(t *testing.T) {
	manager := NewMcpManager("", nil)
	token := &mcpConnToken{}
	handle := &McpClientHandle{
		ServerName: "srv",
		Status:     "connected",
		token:      token,
		ToolDefs:   []McpDiscoveredTool{{ServerName: "srv", Name: "t"}},
	}
	manager.clients["srv"] = handle

	// 旧连接（token 已被新连接替换）的死亡报告不能打翻当前这条。
	manager.handleConnectionLost("srv", &mcpConnToken{}, "stale process exited")
	if handle.Status != "connected" {
		t.Fatalf("stale token must not flip the live handle, got %q", handle.Status)
	}

	// 重连中（状态已改成 connecting）也是同一种"正常关闭"，不能当成故障。
	handle.Status = "connecting"
	manager.handleConnectionLost("srv", token, "expected close")
	if handle.Status != "connecting" || handle.Error != "" {
		t.Fatalf("a reconnect in flight must not be reported as a failure, got %q/%q", handle.Status, handle.Error)
	}

	// 当前连接的意外退出：翻 failed，工具清单保留——失败服务端的工具照常注入，
	// 但下一次调用要靠它把连接救回来。
	handle.Status = "connected"
	manager.handleConnectionLost("srv", token, "MCP server process exited (exit status 1)")
	if handle.Status != "failed" || !strings.Contains(handle.Error, "exit status 1") {
		t.Fatalf("unexpected loss must be recorded, got %q/%q", handle.Status, handle.Error)
	}
	if len(handle.ToolDefs) != 1 {
		t.Fatal("tool defs must survive so the next call can heal the connection")
	}
}

func TestMcpStdioDir(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name    string
		workDir string
		cwd     string
		want    string
	}{
		{"默认用工作区根", root, "", root},
		{"相对 cwd 相对工作区根解析", root, "sub/dir", filepath.Join(root, "sub", "dir")},
		{"绝对 cwd 原样使用", root, "/opt/mcp", "/opt/mcp"},
		{"没有工作区根且没写 cwd 时不设目录", "", "", ""},
		{"没有工作区根时绝对 cwd 仍可用", "", "/opt/mcp", "/opt/mcp"},
	}
	for _, tc := range cases {
		manager := NewMcpManager(tc.workDir, nil)
		if got := manager.mcpStdioDir(McpServerConfig{Cwd: tc.cwd}); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestMcpTimeoutResolution(t *testing.T) {
	if got := mcpStartupTimeout(McpServerConfig{}); got != mcpDefaultStartupTimeout {
		t.Fatalf("default startup timeout: got %s", got)
	}
	if got := mcpToolCallTimeout(McpServerConfig{}); got != mcpDefaultToolCallTimeout {
		t.Fatalf("default tool timeout: got %s", got)
	}
	if got := mcpStartupTimeout(McpServerConfig{StartupTimeoutSec: 90}); got != 90*time.Second {
		t.Fatalf("per-server startup timeout: got %s", got)
	}
	if got := mcpToolCallTimeout(McpServerConfig{ToolTimeoutSec: 600}); got != 10*time.Minute {
		t.Fatalf("per-server tool timeout: got %s", got)
	}
	// 非正数回退默认值；天文数字截断到上限，而不是溢出成负数（那等于立即超时）。
	if got := mcpToolCallTimeout(McpServerConfig{ToolTimeoutSec: -5}); got != mcpDefaultToolCallTimeout {
		t.Fatalf("non-positive override must fall back: got %s", got)
	}
	if got := mcpToolCallTimeout(McpServerConfig{ToolTimeoutSec: 1e18}); got != mcpMaxTimeout {
		t.Fatalf("absurd override must clamp to the max: got %s", got)
	}
}

func TestNormalizeMcpConfigJSONPreservesUnknownFields(t *testing.T) {
	raw := `{
  "$schema": "https://example.com/mcp.schema.json",
  "mcpServers": {
    "files": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem"],
      "cwd": "./data",
      "toolTimeoutSec": 600,
      "vendorOnly": {"nested": [1, 2]},
      "env": {"A": "b"}
    }
  }
}`
	data, err := normalizeMcpConfigJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	// 落盘排版要能看：RawMessage 若不重新缩进，整份 server 清单会挤成一长行。
	if !strings.Contains(string(data), "\n    \"files\"") {
		t.Fatalf("落盘后的 server 条目应保持缩进，实际：\n%s", data)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["$schema"]; !ok {
		t.Fatalf("顶层未知键必须原样保留: %s", data)
	}
	var servers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(doc["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	entry := servers["files"]
	for _, key := range []string{"command", "args", "cwd", "toolTimeoutSec", "vendorOnly", "env"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("字段 %q 在保存后丢失: %+v", key, entry)
		}
	}
	entryRaw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var parsed McpServerConfig
	if err := json.Unmarshal(entryRaw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Cwd != "./data" || parsed.ToolTimeoutSec != 600 {
		t.Fatalf("新增字段必须能被解析回来: %+v", parsed)
	}

	// 校验不能放松：结构写错要当场报错（静默写成空配置比报错更糟）。
	for _, bad := range []string{
		`{"mcpServers": []}`,
		`{"mcpServers": {"bad": 3}}`,
		`{"mcpServers": {"bad": null}}`,
		`{"mcpServers": {"bad": {"args": "not-a-list"}}}`,
	} {
		if _, err := normalizeMcpConfigJSON(bad); err == nil {
			t.Fatalf("expected %s to be rejected", bad)
		}
	}

	// 空输入 / mcpServers 为 null：落一份合法的空配置，别让面板打开时一片空白。
	for _, empty := range []string{"   ", `{"mcpServers": null}`, `{}`} {
		data, err := normalizeMcpConfigJSON(empty)
		if err != nil {
			t.Fatalf("empty input %q must not fail: %v", empty, err)
		}
		if !strings.Contains(string(data), `"mcpServers": {}`) {
			t.Fatalf("empty input %q must persist an mcpServers object: %s", empty, data)
		}
	}
}

// mcpStdioTestServerScript 是一个最小可用的 MCP stdio 服务端（python3）：只认
// initialize / tools/list / tools/call，其中 tools/call 回报自己的工作目录与
// pid —— 前者用来钉住"stdio 服务端跑在工作区根"，后者用来模拟子进程暴死。
const mcpStdioTestServerScript = `import json, os, sys

def out(msg):
    sys.stdout.write(json.dumps(msg) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except Exception:
        continue
    method = msg.get("method")
    rid = msg.get("id")
    if method == "initialize":
        params = msg.get("params") or {}
        out({"jsonrpc": "2.0", "id": rid, "result": {
            "protocolVersion": params.get("protocolVersion", "2025-06-18"),
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "ally-test", "version": "1.0.0"}}})
    elif method == "tools/list":
        out({"jsonrpc": "2.0", "id": rid, "result": {"tools": [{
            "name": "where",
            "description": "report cwd and pid",
            "inputSchema": {"type": "object", "properties": {}}}]}})
    elif method == "tools/call":
        out({"jsonrpc": "2.0", "id": rid, "result": {"content": [
            {"type": "text", "text": "cwd=%s pid=%d env_secret=%s env_path=%d" % (
                os.getcwd(), os.getpid(),
                os.environ.get("ALLY_MCP_TEST_SECRET", "-"),
                1 if os.environ.get("PATH") else 0)}]}})
    elif rid is not None:
        out({"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": "method not found"}})
`

func mcpTestPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("python3/python is not available")
	return ""
}

func mcpHandleStatus(manager *McpManager, name string) string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if handle, ok := manager.clients[name]; ok {
		return handle.Status
	}
	return ""
}

func waitForMcpHandleStatus(t *testing.T, manager *McpManager, name, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := mcpHandleStatus(manager, name); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("MCP 状态未在 10s 内变成 %q，当前 %q", want, mcpHandleStatus(manager, name))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMcpStdioServerUsesWorkspaceDirAndHealsAfterCrash(t *testing.T) {
	python := mcpTestPython(t)
	script := filepath.Join(t.TempDir(), "server.py")
	if err := os.WriteFile(script, []byte(mcpStdioTestServerScript), 0o700); err != nil {
		t.Fatal(err)
	}

	// 工作区根 = stdio 服务端的默认工作目录（服务端不该继承 Ally 自己的 cwd）。
	workspace := t.TempDir()
	wantDir, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewMcpManager(workspace, nil)
	defer manager.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	manager.connectOne(ctx, "echo", McpServerConfig{Command: python, Args: []string{script}})
	if got := mcpHandleStatus(manager, "echo"); got != "connected" {
		t.Fatalf("真正的 stdio 服务端应连上，got status %q", got)
	}

	out, err := manager.CallTool(ctx, "echo", "where", map[string]any{})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if !strings.Contains(out, wantDir) {
		t.Fatalf("stdio 服务端的工作目录应为工作区根：got %q want %q", out, wantDir)
	}

	pid := 0
	for _, field := range strings.Fields(out) {
		if value, ok := strings.CutPrefix(field, "pid="); ok {
			pid, _ = strconv.Atoi(value)
		}
	}
	if pid <= 0 {
		t.Fatalf("测试服务端没报出 pid: %q", out)
	}

	// 子进程暴死：守护必须发现并把状态翻成 failed（旧实现只会静默地一直显示
	// "已连接"，直到用户下一次调用）。
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	waitForMcpHandleStatus(t, manager, "echo", "failed")

	// 下一次调用先重连再试（对齐 ZCode 的调用前按需重连）：连接自己救回来，
	// 而不是把"连接已失效"一直丢给模型。
	out, err = manager.CallTool(ctx, "echo", "where", map[string]any{})
	if err != nil {
		t.Fatalf("调用前重连自愈失败: %v", err)
	}
	if !strings.Contains(out, wantDir) {
		t.Fatalf("重连后的调用应正常返回：got %q", out)
	}
	if got := mcpHandleStatus(manager, "echo"); got != "connected" {
		t.Fatalf("重连后状态应回到 connected，got %q", got)
	}
}

// mcpTestEnvValue 返回环境变量在 []string 形态里的最后一个取值：os/exec 对同名
// 键保留最后一个，而这正是「显式声明覆盖白名单」依赖的语义。
func mcpTestEnvValue(env []string, key string) (string, bool) {
	value, found := "", false
	for _, item := range env {
		if k, v, ok := strings.Cut(item, "="); ok && k == key {
			value, found = v, true
		}
	}
	return value, found
}

func TestMcpStdioFilterEnvKeepsOnlyBaselineVariables(t *testing.T) {
	// 过滤本身与来源无关，所以直接喂一份候选环境：共享环境是进程内 memoize 的，
	// 测试里 t.Setenv 也改不动它（登录 shell 只探一次）。
	base := []string{
		"PATH=/usr/bin",
		"HOME=/tmp/ally-home",
		"ANTHROPIC_API_KEY=sk-must-not-leak",
		"GITHUB_TOKEN=gh-must-not-leak",
		"MALFORMED",
	}
	env := mcpStdioFilterEnv(base)
	for key, want := range map[string]string{"PATH": "/usr/bin", "HOME": "/tmp/ally-home"} {
		if got, ok := mcpTestEnvValue(env, key); !ok || got != want {
			t.Fatalf("%s 是跑子进程的必要变量，必须保留：got %q ok=%v", key, got, ok)
		}
	}
	// Ally 自己进程里的密钥不该顺手交给第三方服务端（旧实现是全量继承）。
	for _, key := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN"} {
		if _, ok := mcpTestEnvValue(env, key); ok {
			t.Fatalf("%s 不该出现在 stdio 服务端的环境里：%v", key, env)
		}
	}
	if len(env) != 2 {
		t.Fatalf("白名单外的键（含没有等号的畸形项）都不该留下：%v", env)
	}
}

func TestMcpStdioEnvUsesSharedCommandEnvironment(t *testing.T) {
	// stdio 服务端的 PATH 必须与 command / 后台服务子进程同源：登录 shell 的
	// PATH 增强只在 commandEnvironment 里做过一次（从 Dock 启动时父进程的 PATH
	// 很短，配置里写的 npx / node 会「命令找不到」）。
	cfg := ConfigState{}
	want, ok := mcpTestEnvValue(commandEnvironment(cfg), "PATH")
	if !ok {
		t.Fatal("共享环境里应当有 PATH")
	}
	got, ok := mcpTestEnvValue(mcpStdioBaseEnv(cfg), "PATH")
	if !ok || got != want {
		t.Fatalf("stdio 的 PATH 必须取自共享环境：got %q want %q", got, want)
	}
	// 增强后的 PATH 与原始进程环境不同时，得真的看到「不同」：两边都取自
	// os.Environ() 的旧实现会让这条失败（登录 shell 这次没贡献新条目时这条就没
	// 牙，所以真正守门的是下面那条端到端）。
	if raw, ok := mcpTestEnvValue(os.Environ(), "PATH"); ok && raw != want && got == raw {
		t.Fatal("stdio 的 PATH 仍取自原始进程环境，没接上登录 shell 增强")
	}
	// 白名单过滤对共享环境同样生效：进程里的密钥一样不给。
	t.Setenv("ALLY_MCP_TEST_SECRET", "must-not-leak")
	if _, leaked := mcpTestEnvValue(mcpStdioBaseEnv(cfg), "ALLY_MCP_TEST_SECRET"); leaked {
		t.Fatal("共享环境里的非白名单变量不该漏给服务端")
	}
}

func TestMcpStdioEnvLetsExplicitEnvOverrideBaseline(t *testing.T) {
	env := mcpStdioEnvFromBase(
		[]string{"PATH=/usr/bin", "GITHUB_TOKEN=inherited-must-lose"},
		McpServerConfig{Env: map[string]string{
			"PATH":         "/opt/bin",
			"GITHUB_TOKEN": "declared-wins",
			"EXTRA":        "1",
		}},
	)
	if got, _ := mcpTestEnvValue(env, "PATH"); got != "/opt/bin" {
		t.Fatalf("显式 PATH 应压过基础环境里的 PATH：got %q", got)
	}
	if got, _ := mcpTestEnvValue(env, "GITHUB_TOKEN"); got != "declared-wins" {
		t.Fatalf("服务端显式声明的变量应被采纳：got %q", got)
	}
	if got, ok := mcpTestEnvValue(env, "EXTRA"); !ok || got != "1" {
		t.Fatalf("白名单外的新键只能靠显式声明带进来：got %q ok=%v", got, ok)
	}
}

func TestMcpStdioEnvKeepsProxyVariables(t *testing.T) {
	// 代理变量必须在过滤**之后**注入：白名单里没有代理键名，顺序反了就会被整批
	// 滤掉，配了代理的用户会静默变成直连（第三方服务端绕过用户设的路由）。
	cfg := ConfigState{ProxyMode: proxyModeManual, ProxyURL: "http://127.0.0.1:7890"}
	env := mcpStdioBaseEnv(cfg)
	if got, ok := mcpTestEnvValue(env, "HTTPS_PROXY"); !ok || got != "http://127.0.0.1:7890" {
		t.Fatalf("stdio 服务端必须拿到配置里的代理：got %q ok=%v", got, ok)
	}
	if _, ok := mcpTestEnvValue(env, "HTTP_PROXY"); !ok {
		t.Fatal("http_proxy 一族要一起下发，只给一个会让部分运行时不认")
	}
	if _, ok := mcpTestEnvValue(env, "NO_PROXY"); !ok {
		t.Fatal("NO_PROXY 也该跟着下发，否则本地地址会被硬塞进代理")
	}
}

func TestMcpStdioEnvFiltersAtTheCallSite(t *testing.T) {
	// 不依赖 python 的那一半：newMcpClient 交给子进程的就是这个函数的结果，这
	// 里钉住它的形状——进程里的密钥一定不在，服务端显式声明的键一定在，条数也被
	// 白名单 + 显式声明夹住（零值配置没有代理，因而不会多出代理键）。
	t.Setenv("ALLY_MCP_TEST_SECRET", "must-not-leak")
	cfg := ConfigState{}
	srv := McpServerConfig{Env: map[string]string{"EXPLICIT_ONLY": "1"}}
	env := mcpStdioEnv(cfg, srv)

	if _, leaked := mcpTestEnvValue(env, "ALLY_MCP_TEST_SECRET"); leaked {
		t.Fatalf("进程里的密钥不该进入子进程环境：%v", env)
	}
	if got, ok := mcpTestEnvValue(env, "EXPLICIT_ONLY"); !ok || got != "1" {
		t.Fatalf("服务端显式声明的变量必须带上：got %q ok=%v", got, ok)
	}
	if limit := len(mcpStdioEnvAllowlist) + len(srv.Env); len(env) > limit {
		t.Fatalf("环境条数应被白名单 + 显式声明夹住：got %d > %d", len(env), limit)
	}
}

func TestMcpSSETransportHonorsAllowPrivateNetworkSwitch(t *testing.T) {
	// sse 与 streamable-http 是同一次改动的两处，必须一起跟随开关。
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()

	manager := NewMcpManager("", nil)
	defer manager.Shutdown()
	manager.SetNetworkConfigProvider(func() ConfigState {
		return ConfigState{AllowPrivateNetwork: boolPtr(false)}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := McpServerConfig{URL: server.URL, Transport: "sse", StartupTimeoutSec: 2}
	if _, err := manager.initializeMcpClient(ctx, "sse", cfg); err == nil {
		t.Fatal("allowPrivateNetwork=false 时本地 sse 服务端必须连不上")
	} else if !strings.Contains(err.Error(), "private or local network") {
		t.Fatalf("这次失败应来自私网围栏，got %v", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("被围栏拦下的连接不该发出请求，got %d 次", got)
	}
}

func TestMcpStdioServerDoesNotInheritAllyEnvironment(t *testing.T) {
	python := mcpTestPython(t)
	script := filepath.Join(t.TempDir(), "server.py")
	if err := os.WriteFile(script, []byte(mcpStdioTestServerScript), 0o700); err != nil {
		t.Fatal(err)
	}
	// 端到端钉住调用点：真起一个 stdio 服务端，问它自己看见了什么。
	t.Setenv("ALLY_MCP_TEST_SECRET", "leaked-secret-value")

	manager := NewMcpManager(t.TempDir(), nil)
	defer manager.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	manager.connectOne(ctx, "env", McpServerConfig{Command: python, Args: []string{script}})

	out, err := manager.CallTool(ctx, "env", "where", map[string]any{})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if strings.Contains(out, "leaked-secret-value") {
		t.Fatalf("stdio 服务端不该继承 Ally 的进程私密变量：%q", out)
	}
	if !strings.Contains(out, "env_path=1") {
		t.Fatalf("PATH 这类进程级变量必须保留，否则 npx/node 起不来：%q", out)
	}
}

func TestMcpServerHonorsAllowPrivateNetworkSwitch(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"ally-test","version":"1.0.0"}}}`))
	}))
	defer server.Close()

	manager := NewMcpManager("", nil)
	defer manager.Shutdown()
	cfg := McpServerConfig{URL: server.URL, Transport: "streamable-http", StartupTimeoutSec: 2}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 与 http_request 同一个开关：关掉后本地/内网服务端必须连不上，而且一个请求
	// 都不该真的发出去（有请求到达就说明围栏没生效）。
	manager.SetNetworkConfigProvider(func() ConfigState {
		return ConfigState{AllowPrivateNetwork: boolPtr(false)}
	})
	if _, err := manager.initializeMcpClient(ctx, "net", cfg); err == nil {
		t.Fatal("allowPrivateNetwork=false 时本地 MCP 服务端必须连不上")
	} else if !strings.Contains(err.Error(), "private or local network") {
		t.Fatalf("这次失败应来自私网围栏，got %v", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("被围栏拦下的连接不该发出请求，got %d 次", got)
	}

	// 打开开关后同一个地址必须能连出去（握手是否成功取决于假服务端，这里只钉
	// "请求确实到达"，证明开关是判据）。
	manager.SetNetworkConfigProvider(func() ConfigState {
		return ConfigState{AllowPrivateNetwork: boolPtr(true)}
	})
	if established, err := manager.initializeMcpClient(ctx, "net", cfg); err == nil {
		_ = established.client.Close()
	}
	if hits.Load() == 0 {
		t.Fatal("allowPrivateNetwork=true 时 MCP 请求应到达服务端")
	}
}

func TestCommitConnectionRecordsLossDuringHandshake(t *testing.T) {
	// 握手刚成功、进程就被杀的窗口：登记时必须如实落 failed，而不是把一条
	// 已经没了的连接写成 connected（那正是"面板永远显示已连接"的者毛病）。
	manager := NewMcpManager("", nil)
	deadToken := &mcpConnToken{}
	deadToken.markLost("MCP server process exited (exit status 1)")
	deadHandle := &McpClientHandle{ServerName: "dead", Status: "connecting"}
	manager.mu.Lock()
	manager.commitConnectionLocked(deadHandle, mcpEstablished{token: deadToken})
	manager.mu.Unlock()
	if deadHandle.Status != "failed" || !strings.Contains(deadHandle.Error, "exit status 1") {
		t.Fatalf("已死的连接不能被登记成 %q/%q", deadHandle.Status, deadHandle.Error)
	}

	// 活着的照常登记，工具映射跟着一起写入。
	liveToken := &mcpConnToken{}
	liveHandle := &McpClientHandle{ServerName: "live", Status: "connecting"}
	manager.mu.Lock()
	manager.commitConnectionLocked(liveHandle, mcpEstablished{
		tools: []McpDiscoveredTool{{ServerName: "live", Name: "t", FunctionName: "mcp__live__t"}},
		token: liveToken,
	})
	manager.mu.Unlock()
	if liveHandle.Status != "connected" || liveHandle.Error != "" || liveHandle.token != liveToken {
		t.Fatalf("正常连接应登记为 connected，got %q/%q", liveHandle.Status, liveHandle.Error)
	}
	if _, ok := manager.toolLookup["mcp__live__t"]; !ok {
		t.Fatal("工具映射必须跟着登记一起写入")
	}
}

func TestReconcileRetriesFailedServerInBackground(t *testing.T) {
	enabled := true
	const missingBinary = "ally-mcp-test-definitely-missing-binary"
	configFile := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(configFile, []byte(`{"mcpServers":{"a":{"command":"`+missingBinary+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewMcpManager(t.TempDir(), func(tools []McpDiscoveredTool) {})
	manager.configPaths = []string{configFile}
	manager.clients["a"] = &McpClientHandle{
		ServerName: "a",
		Config:     McpServerConfig{Command: missingBinary, Enabled: &enabled},
		Status:     "failed",
		Error:      "boom",
	}

	// 配置没变的 failed 服务端也要重试（把状态翻回 connected，省掉下一次调用
	// 的按需重连延迟），但绝不能同步等拨号——一次失败的拨号要跑满 4 次尝试，
	// 面板的"保存"会被拖成分钟级。
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = manager.ReconcileConfigs(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("failed 服务端的重试不能阻塞 ReconcileConfigs 的调用方")
	}
	waitForMcpHandleStatus(t, manager, "a", "connecting")
}

func TestToolRefreshCoalescesBurstAndKeepsPending(t *testing.T) {
	manager := NewMcpManager("", nil)
	var calls atomic.Int64
	refreshing := make(chan struct{}, 4)
	release := make(chan struct{}, 4)
	manager.toolRefresher = func(string, *mcpConnToken) {
		calls.Add(1)
		refreshing <- struct{}{}
		<-release
	}

	// 一次突发里的三条通知合成一轮。
	for i := 0; i < 3; i++ {
		manager.markToolsListDirty("srv", &mcpConnToken{})
	}
	<-refreshing
	// 刷新进行中又来的通知不能丢：跑完必须再补一轮。
	manager.markToolsListDirty("srv", &mcpConnToken{})
	release <- struct{}{}
	<-refreshing
	if got := calls.Load(); got != 2 {
		t.Fatalf("突发三条 + 期间一条应合成两轮，got %d", got)
	}
	release <- struct{}{}

	// 收尾：没有新通知时 running 必须回落，否则这个槽永久卡住（后续通知再也
	// 不会触发刷新）。
	deadline := time.Now().Add(3 * time.Second)
	for {
		manager.refreshMu.Lock()
		running := manager.refreshSlots["srv"].running
		manager.refreshMu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("刷新循环未收尾：running 一直是 true")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("收尾后不该再触发刷新，got %d", got)
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
