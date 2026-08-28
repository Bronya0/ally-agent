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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	openai "github.com/sashabaranov/go-openai"
)

// McpServerConfig represents a single MCP server config (Claude Desktop format).
type McpServerConfig struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
}

type McpServersConfig struct {
	McpServers map[string]McpServerConfig `json:"mcpServers"`
}

type McpDiscoveredTool struct {
	ServerName   string
	Name         string
	FunctionName string
	Description  string
	Schema       map[string]any // JSON Schema for OpenAI tool registration
}

type McpClientHandle struct {
	ServerName string
	Config     McpServerConfig
	Client     *client.Client
	ToolDefs   []McpDiscoveredTool
	Status     string // "connected", "connecting", "failed", "disabled"
	Error      string
}

type McpManager struct {
	mu            sync.RWMutex
	reconnectLocks sync.Map // serverName -> *sync.Mutex，per-server 重连互斥
	clients       map[string]*McpClientHandle
	toolLookup    map[string]mcpToolRef
	workDir       string
	listener      func(tools []McpDiscoveredTool)
	warnHandler   func(message string)
	networkConfig func() ConfigState
}

// SetWarningHandler wires the config:warning sink so a broken mcp.json is
// reported instead of silently unloading every server.
func (m *McpManager) SetWarningHandler(handler func(message string)) {
	m.mu.Lock()
	m.warnHandler = handler
	m.mu.Unlock()
}

func (m *McpManager) warnf(format string, args ...any) {
	m.mu.RLock()
	handler := m.warnHandler
	m.mu.RUnlock()
	if handler != nil {
		handler(fmt.Sprintf(format, args...))
	}
}

func (m *McpManager) SetNetworkConfigProvider(provider func() ConfigState) {
	m.mu.Lock()
	m.networkConfig = provider
	m.mu.Unlock()
}

func (m *McpManager) currentNetworkConfig() ConfigState {
	m.mu.RLock()
	provider := m.networkConfig
	m.mu.RUnlock()
	if provider != nil {
		return provider()
	}
	return ConfigState{}
}

type mcpToolRef struct {
	ServerName string
	ToolName   string
}

func NewMcpManager(workDir string, listener func(tools []McpDiscoveredTool)) *McpManager {
	return &McpManager{
		clients:    make(map[string]*McpClientHandle),
		toolLookup: make(map[string]mcpToolRef),
		workDir:    workDir,
		listener:   listener,
	}
}

func mcpJsonPaths(workDir string) []string {
	return []string{mcpUserConfigPath()}
}

func mcpUserConfigPath() string {
	return filepath.Join(appDataDir(), "mcp.json")
}

func (m *McpManager) LoadConfigs() (map[string]McpServerConfig, error) {
	merged := make(map[string]McpServerConfig)
	for _, path := range mcpJsonPaths(m.workDir) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg McpServersConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			// 手改 mcp.json 写坏时不能无声吞掉，否则所有服务器"消失"而
			// 用户只看到一片空白。走 config:warning 提示。
			m.warnf("mcp.json parse failed; all MCP servers are unloaded: %v", err)
			continue
		}
		// Disabled servers stay in the result so StartAll registers a
		// "disabled" handle and the status list shows configured-but-off
		// entries instead of silently hiding them.
		for name, srv := range cfg.McpServers {
			merged[name] = srv
		}
	}
	return merged, nil
}

func (m *McpManager) StartAll(ctx context.Context) error {
	configs, err := m.LoadConfigs()
	if err != nil {
		return err
	}
	if len(configs) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	for name, cfg := range configs {
		name, cfg := name, cfg
		if cfg.Enabled != nil && !*cfg.Enabled {
			m.mu.Lock()
			m.clients[name] = &McpClientHandle{ServerName: name, Config: cfg, Status: "disabled"}
			m.mu.Unlock()
			m.notifyChange()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.connectOne(ctx, name, cfg)
		}()
	}
	wg.Wait()
	return nil
}

func (m *McpManager) connectOne(ctx context.Context, name string, cfg McpServerConfig) {
	handle := &McpClientHandle{ServerName: name, Config: cfg, Status: "connecting"}
	m.mu.Lock()
	m.clients[name] = handle
	m.mu.Unlock()
	m.notifyChange()

	mcpClient, discovered, err := m.initializeMcpClient(ctx, name, cfg)
	if err != nil {
		m.mu.Lock()
		handle.Status = "failed"
		handle.Error = err.Error()
		m.mu.Unlock()
		m.notifyChange()
		return
	}

	m.mu.Lock()
	handle.Client = mcpClient
	handle.ToolDefs = discovered
	handle.Status = "connected"
	handle.Error = ""
	m.replaceToolLookupLocked(name, discovered)
	m.mu.Unlock()
	m.notifyChange()
}

func (m *McpManager) initializeMcpClient(ctx context.Context, name string, cfg McpServerConfig) (*client.Client, []McpDiscoveredTool, error) {
	mcpClient, err := m.newMcpClient(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "ally",
				Version: "1.0.0",
			},
		},
	}

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if _, err := mcpClient.Initialize(initCtx, initReq); err != nil {
		mcpClient.Close()
		return nil, nil, fmt.Errorf("init failed: %w", err)
	}

	// ListTools carries the same bounded timeout as Initialize: a server that
	// completes the handshake but then hangs must not pin connectOne in
	// "connecting" forever.
	toolsCtx, toolsCancel := context.WithTimeout(ctx, 30*time.Second)
	defer toolsCancel()
	toolsResult, err := mcpClient.ListTools(toolsCtx, mcp.ListToolsRequest{})
	if err != nil {
		mcpClient.Close()
		return nil, nil, fmt.Errorf("list tools failed (timed out or refused after 30s): %w", err)
	}

	var discovered []McpDiscoveredTool
	for _, tool := range toolsResult.Tools {
		schema := toolSchemaToMap(tool.InputSchema)
		discovered = append(discovered, McpDiscoveredTool{
			ServerName:   name,
			Name:         tool.Name,
			FunctionName: mcpToolFunctionName(name, tool.Name),
			Description:  tool.Description,
			Schema:       schema,
		})
	}
	return mcpClient, discovered, nil
}

func (m *McpManager) newMcpClient(ctx context.Context, cfg McpServerConfig) (*client.Client, error) {
	transportName := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if transportName == "" && cfg.Command != "" {
		transportName = "stdio"
	}
	if transportName == "" && cfg.URL != "" {
		transportName = "streamable-http"
	}

	var mcpClient *client.Client
	var err error
	switch transportName {
	case "stdio":
		if strings.TrimSpace(cfg.Command) == "" {
			return nil, errors.New("stdio MCP server requires command")
		}
		env := proxyEnvironment(m.currentNetworkConfig(), os.Environ())
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		// NewStdioMCPClient spawns the subprocess with its own exec.Cmd and
		// does not set SysProcAttr — on Windows this would flash a console
		// window for every stdio MCP server (npx / python / node …) on every
		// reconnect. Use WithCommandFunc to take ownership of Cmd creation,
		// apply hideCommandWindow(), and join the process into a
		// KILL_ON_JOB_CLOSE Job Object so grandchildren do not outlive the
		// client (same orphan protection as the service tool).
		mcpClient, err = client.NewStdioMCPClientWithOptions(cfg.Command, env, cfg.Args,
			transport.WithCommandFunc(func(ctx context.Context, command string, cmdEnv []string, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Env = cmdEnv
				hideCommandWindow(cmd)
				watchMcpProcessJob(cmd, prepareServiceCommand(cmd))
				return cmd, nil
			}),
		)
		if err != nil {
			return nil, fmt.Errorf("stdio spawn failed: %w", err)
		}
	case "sse":
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, errors.New("sse MCP server requires url")
		}
		httpClient := proxyHTTPClient(m.currentNetworkConfig(), true, 0)
		mcpClient, err = client.NewSSEMCPClient(cfg.URL, client.WithHeaders(cfg.Headers), client.WithHTTPClient(httpClient))
		if err != nil {
			return nil, fmt.Errorf("sse client failed: %w", err)
		}
	case "streamable-http", "http", "rest":
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, errors.New("http MCP server requires url")
		}
		mcpClient, err = client.NewStreamableHttpClient(
			cfg.URL,
			transport.WithHTTPHeaders(cfg.Headers),
			transport.WithHTTPTimeout(60*time.Second),
			transport.WithHTTPBasicClient(proxyHTTPClient(m.currentNetworkConfig(), true, 60*time.Second)),
		)
		if err != nil {
			return nil, fmt.Errorf("http client failed: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", cfg.Transport)
	}
	if err := mcpClient.Start(ctx); err != nil {
		_ = mcpClient.Close()
		return nil, fmt.Errorf("%s start failed: %w", transportName, err)
	}
	return mcpClient, nil
}

// watchMcpProcessJob gives stdio MCP subprocess trees the same orphan
// protection as the service tool. The transport owns Start/Wait, so the job
// is registered once the process handle appears (Windows Job Object with
// KILL_ON_JOB_CLOSE; no-op elsewhere) and unregistered after the root process
// dies — closing the handle then also kills any surviving grandchildren.
// Polling reads cmd.Process once after Start and never touches
// cmd.ProcessState, avoiding races with the transport's Wait.
func watchMcpProcessJob(cmd *exec.Cmd, job uintptr) {
	if job == 0 {
		return
	}
	go func() {
		var pid int
		for i := 0; i < 100; i++ {
			if p := cmd.Process; p != nil {
				pid = p.Pid
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if pid == 0 {
			discardProcessJob(job)
			return
		}
		_ = registerProcessJob(pid, job)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if !isProcessAlive(pid) {
				unregisterProcessJob(pid)
				return
			}
		}
	}()
}

// toolSchemaToMap converts an MCP input schema to provider-safe JSON Schema.
// Marshaling through mcp-go preserves $defs/additionalProperties and turns nil
// slices/maps into valid []/{} values. The recursive normalization also removes
// invalid null schema keywords sometimes emitted by compatible MCP servers.
func toolSchemaToMap(schema mcp.ToolInputSchema) map[string]any {
	raw, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	normalizeMcpSchemaNode(result, true)
	return result
}

func normalizeMcpSchemaNode(node map[string]any, root bool) {
	if root {
		if typ, ok := node["type"].(string); !ok || strings.TrimSpace(typ) == "" {
			node["type"] = "object"
		}
	}
	if node["type"] == "object" {
		if _, ok := node["properties"].(map[string]any); !ok {
			node["properties"] = map[string]any{}
		}
	}

	if required, ok := normalizedMcpRequired(node["required"]); ok && len(required) > 0 {
		node["required"] = required
	} else {
		delete(node, "required")
	}
	if node["additionalProperties"] == nil {
		delete(node, "additionalProperties")
	}

	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		children, ok := node[key].(map[string]any)
		if !ok {
			if key != "properties" || node["type"] != "object" {
				delete(node, key)
			}
			continue
		}
		for _, child := range children {
			if childSchema, ok := child.(map[string]any); ok {
				normalizeMcpSchemaNode(childSchema, false)
			}
		}
	}
	for _, key := range []string{"additionalProperties", "items", "contains", "not", "if", "then", "else", "propertyNames", "unevaluatedProperties", "unevaluatedItems"} {
		if child, ok := node[key].(map[string]any); ok {
			normalizeMcpSchemaNode(child, false)
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		items, ok := node[key].([]any)
		if !ok {
			if node[key] == nil {
				delete(node, key)
			}
			continue
		}
		for _, item := range items {
			if child, ok := item.(map[string]any); ok {
				normalizeMcpSchemaNode(child, false)
			}
		}
	}
}

func normalizedMcpRequired(value any) ([]string, bool) {
	switch required := value.(type) {
	case []string:
		return required, true
	case []any:
		out := make([]string, 0, len(required))
		for _, item := range required {
			name, ok := item.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return nil, false
			}
			out = append(out, name)
		}
		return out, true
	default:
		return nil, false
	}
}

func (m *McpManager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	result, failedClient, err := m.callToolOnce(ctx, serverName, toolName, args)
	if err == nil {
		return result, nil
	}
	if !isMcpInvalidSessionError(err) {
		return "", err
	}

	if reconnectErr := m.reconnectServer(ctx, serverName, failedClient); reconnectErr != nil {
		return "", fmt.Errorf("MCP call failed: %w; reconnect failed: %w", err, reconnectErr)
	}
	result, _, err = m.callToolOnce(ctx, serverName, toolName, args)
	return result, err
}

func (m *McpManager) callToolOnce(ctx context.Context, serverName, toolName string, args map[string]any) (string, *client.Client, error) {
	m.mu.RLock()
	handle, ok := m.clients[serverName]
	if !ok {
		m.mu.RUnlock()
		return "", nil, fmt.Errorf("MCP server %s not found", serverName)
	}
	status := handle.Status
	handleErr := handle.Error
	mcpClient := handle.Client
	m.mu.RUnlock()
	if status != "connected" {
		return "", mcpClient, fmt.Errorf("MCP server %s status: %s/%s", serverName, status, handleErr)
	}
	if mcpClient == nil {
		return "", nil, fmt.Errorf("MCP server %s has no active client", serverName)
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}
	result, err := mcpClient.CallTool(ctx, req)
	if err != nil {
		return "", mcpClient, fmt.Errorf("MCP call failed: %w", err)
	}

	var parts []string
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			parts = append(parts, textContent.Text)
		}
	}
	return strings.Join(parts, "\n"), mcpClient, nil
}

func (m *McpManager) reconnectServer(ctx context.Context, serverName string, failedClient *client.Client) error {
	// Per-server lock: one server's slow reconnect (up to the 30s handshake
	// timeout) must not serialize reconnects of unrelated servers.
	lock, _ := m.reconnectLocks.LoadOrStore(serverName, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()

	m.mu.Lock()
	handle, ok := m.clients[serverName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("MCP server %s not found", serverName)
	}
	if handle.Status == "connected" && handle.Client != nil && handle.Client != failedClient {
		m.mu.Unlock()
		return nil
	}
	cfg := handle.Config
	oldClient := handle.Client
	handle.Status = "connecting"
	handle.Error = "reconnecting after invalid session"
	handle.Client = nil
	m.mu.Unlock()
	m.notifyChange()

	if oldClient != nil {
		_ = oldClient.Close()
	}

	mcpClient, discovered, err := m.initializeMcpClient(ctx, serverName, cfg)
	m.mu.Lock()
	current, ok := m.clients[serverName]
	if !ok {
		m.mu.Unlock()
		if mcpClient != nil {
			_ = mcpClient.Close()
		}
		return fmt.Errorf("MCP server %s removed during reconnect", serverName)
	}
	if err != nil {
		current.Status = "failed"
		current.Error = err.Error()
		m.mu.Unlock()
		m.notifyChange()
		return err
	}
	current.Client = mcpClient
	current.ToolDefs = discovered
	current.Status = "connected"
	current.Error = ""
	m.replaceToolLookupLocked(serverName, discovered)
	m.mu.Unlock()
	m.notifyChange()
	return nil
}

func (m *McpManager) replaceToolLookupLocked(serverName string, discovered []McpDiscoveredTool) {
	for functionName, ref := range m.toolLookup {
		if ref.ServerName == serverName {
			delete(m.toolLookup, functionName)
		}
	}
	for _, tool := range discovered {
		m.toolLookup[tool.FunctionName] = mcpToolRef{ServerName: tool.ServerName, ToolName: tool.Name}
	}
}

func isMcpInvalidSessionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid session id") ||
		strings.Contains(msg, "invalid session") ||
		strings.Contains(msg, "session not found") ||
		strings.Contains(msg, "session expired")
}

func (m *McpManager) CallToolByFunctionName(ctx context.Context, functionName string, args map[string]any) (string, error) {
	m.mu.RLock()
	ref, ok := m.toolLookup[functionName]
	m.mu.RUnlock()
	if !ok {
		serverName, toolName, ok := parseLegacyMcpFunctionName(functionName)
		if !ok {
			return "", fmt.Errorf("MCP tool function %s not found", functionName)
		}
		ref = mcpToolRef{ServerName: serverName, ToolName: toolName}
	}
	return m.CallTool(ctx, ref.ServerName, ref.ToolName, args)
}

func (m *McpManager) DescribeFunctionTool(functionName string) (mcpToolRef, bool) {
	m.mu.RLock()
	ref, ok := m.toolLookup[functionName]
	m.mu.RUnlock()
	if ok {
		return ref, true
	}
	serverName, toolName, ok := parseLegacyMcpFunctionName(functionName)
	if !ok {
		return mcpToolRef{}, false
	}
	return mcpToolRef{ServerName: serverName, ToolName: toolName}, true
}

func (m *McpManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, handle := range m.clients {
		if handle.Client != nil {
			handle.Client.Close()
		}
	}
	m.clients = make(map[string]*McpClientHandle)
	m.toolLookup = make(map[string]mcpToolRef)
}

func (m *McpManager) GetAllTools() []McpDiscoveredTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []McpDiscoveredTool
	for _, handle := range m.clients {
		if handle.Status == "connected" {
			all = append(all, handle.ToolDefs...)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].ServerName != all[j].ServerName {
			return all[i].ServerName < all[j].ServerName
		}
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		return all[i].FunctionName < all[j].FunctionName
	})
	return all
}

func mcpToolFunctionName(serverName, toolName string) string {
	return "mcp__" + safeMcpFunctionPart(serverName) + "__" + safeMcpFunctionPart(toolName)
}

func safeMcpFunctionPart(value string) string {
	aliases := map[string]string{
		"知乎搜索":   "zhihu_search",
		"知乎全网搜索": "zhihu_web_search",
	}
	base := aliases[value]
	if base == "" {
		var b strings.Builder
		lastSep := false
		for _, r := range strings.ToLower(value) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
				lastSep = false
			} else if r == '_' || r == '-' {
				if !lastSep {
					b.WriteByte('_')
					lastSep = true
				}
			}
		}
		base = strings.Trim(b.String(), "_-")
		if base == "" {
			base = "mcp"
		}
	}
	if len(base) > 18 {
		base = base[:18]
		base = strings.Trim(base, "_-")
	}
	sum := sha256.Sum256([]byte(value))
	return base + "_" + hex.EncodeToString(sum[:])[:6]
}

func parseLegacyMcpFunctionName(name string) (string, string, bool) {
	parts := strings.SplitN(name, "__", 3)
	if len(parts) != 3 || parts[0] != "mcp" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func (m *McpManager) GetServerStatuses() []map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []map[string]any
	for name, handle := range m.clients {
		result = append(result, map[string]any{
			"name":      name,
			"status":    handle.Status,
			"error":     handle.Error,
			"toolCount": len(handle.ToolDefs),
			"transport": normalizedMcpTransport(handle.Config),
		})
	}
	return result
}

func normalizedMcpTransport(cfg McpServerConfig) string {
	name := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if name == "" && cfg.Command != "" {
		return "stdio"
	}
	if name == "" && cfg.URL != "" {
		return "streamable-http"
	}
	if name == "http" || name == "rest" {
		return "streamable-http"
	}
	return name
}

func (m *McpManager) notifyChange() {
	if m.listener == nil {
		return
	}
	m.listener(m.GetAllTools())
}

// ── MCP tool exposure and frontend bindings ──────────────────

// buildToolsWithMcp combines static tools with dynamically discovered MCP tools.
func (a *App) buildToolsWithMcp() []openai.Tool {
	tools := chatTools()
	if a.mcpManager == nil {
		return tools
	}
	mcpTools := a.mcpManager.GetAllTools()
	for _, dt := range mcpTools {
		name := dt.FunctionName
		if name == "" {
			name = mcpToolFunctionName(dt.ServerName, dt.Name)
		}
		desc := strings.TrimSpace(dt.Description)
		if desc == "" {
			desc = fmt.Sprintf("MCP tool %s from %s", dt.Name, dt.ServerName)
		} else {
			desc = fmt.Sprintf("[%s] %s", dt.ServerName, desc)
		}
		params := dt.Schema
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, rawFunctionTool(name, desc, params))
	}
	return tools
}

func (a *App) buildToolsForConfig(cfg ConfigState) []openai.Tool {
	return a.buildToolsWithMcp()
}

func (a *App) GetMcpServers() []map[string]any {
	if a.mcpManager == nil {
		return nil
	}
	return a.mcpManager.GetServerStatuses()
}

func (a *App) ListTools() []ToolDefinitionSummary {
	tools := make([]ToolDefinitionSummary, 0, len(chatTools()))
	for _, tool := range chatTools() {
		if tool.Function == nil {
			continue
		}
		tools = append(tools, ToolDefinitionSummary{
			Name:        tool.Function.Name,
			Description: strings.TrimSpace(tool.Function.Description),
			Source:      "built-in",
		})
	}
	if a.mcpManager != nil {
		mcpTools := a.mcpManager.GetAllTools()
		sort.Slice(mcpTools, func(i, j int) bool {
			if mcpTools[i].ServerName == mcpTools[j].ServerName {
				return mcpTools[i].Name < mcpTools[j].Name
			}
			return mcpTools[i].ServerName < mcpTools[j].ServerName
		})
		for _, tool := range mcpTools {
			name := tool.FunctionName
			if name == "" {
				name = mcpToolFunctionName(tool.ServerName, tool.Name)
			}
			description := strings.TrimSpace(tool.Description)
			if description == "" {
				description = fmt.Sprintf("MCP tool %s from %s", tool.Name, tool.ServerName)
			}
			tools = append(tools, ToolDefinitionSummary{
				Name:        name,
				Description: description,
				Source:      "mcp",
				Server:      tool.ServerName,
			})
		}
	}
	return tools
}

func (a *App) emitMcpStatus() {
	if a.ctx == nil || a.mcpManager == nil {
		return
	}
	a.emit("mcp:status", map[string]any{"servers": a.mcpManager.GetServerStatuses()})
}

func (a *App) GetMcpConfig() (string, error) {
	path := mcpUserConfigPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "{\n  \"mcpServers\": {}\n}", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (a *App) SaveMcpConfig(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{\"mcpServers\":{}}"
	}
	var cfg McpServersConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return fmt.Errorf("invalid MCP JSON: %w", err)
	}
	if cfg.McpServers == nil {
		cfg.McpServers = map[string]McpServerConfig{}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := mcpUserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (a *App) RestartMcpServers() error {
	cfg, err := a.getConfig()
	if err != nil {
		return err
	}
	root, err := workspaceRoot(cfg)
	if err != nil {
		return err
	}
	if a.ctx == nil {
		return errors.New("application context is not ready")
	}
	if a.mcpManager != nil {
		a.mcpManager.Shutdown()
	}
	manager := NewMcpManager(root, func(tools []McpDiscoveredTool) {
		a.emitMcpStatus()
		a.invalidateContextStaticCache()
	})
	manager.SetNetworkConfigProvider(func() ConfigState { return a.effectiveConfig(ConfigState{}) })
	manager.SetWarningHandler(func(message string) {
		a.emit("config:warning", map[string]any{"field": "mcp", "message": message})
	})
	a.mcpManager = manager
	err = manager.StartAll(a.ctx)
	a.emitMcpStatus()
	return err
}

// ── MCP tool execution ───────────────────────────────────────

func (a *App) executeMcpTool(ctx context.Context, serverName, toolName string, args map[string]any) (any, error) {
	if a.mcpManager == nil {
		return nil, fmt.Errorf("MCP not initialized")
	}
	result, err := a.mcpManager.CallTool(ctx, serverName, toolName, args)
	if err != nil {
		return nil, fmt.Errorf("MCP tool %s/%s failed: %w", serverName, toolName, err)
	}
	return map[string]any{"output": result}, nil
}

func (a *App) executeMcpFunctionTool(ctx context.Context, functionName string, args map[string]any) (any, error) {
	if a.mcpManager == nil {
		return nil, fmt.Errorf("MCP not initialized")
	}
	result, err := a.mcpManager.CallToolByFunctionName(ctx, functionName, args)
	if err != nil {
		return nil, fmt.Errorf("MCP tool %s failed: %w", functionName, err)
	}
	return map[string]any{"output": result}, nil
}

func (a *App) mcpToolEventMeta(functionName string) map[string]any {
	if a.mcpManager == nil || !strings.HasPrefix(functionName, "mcp__") {
		return nil
	}
	ref, ok := a.mcpManager.DescribeFunctionTool(functionName)
	if !ok {
		return nil
	}
	return map[string]any{
		"mcpServer": ref.ServerName,
		"mcpTool":   ref.ToolName,
	}
}

func mergeToolEventMeta(event map[string]any, meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return event
	}
	for key, value := range meta {
		event[key] = value
	}
	return event
}
