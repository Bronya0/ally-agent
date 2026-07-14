package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// McpServerConfig represents a single MCP server config (Claude Desktop / kimi-code format).
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
	mu         sync.RWMutex
	clients    map[string]*McpClientHandle
	toolLookup map[string]mcpToolRef
	workDir    string
	listener   func(tools []McpDiscoveredTool)
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
			continue
		}
		for name, srv := range cfg.McpServers {
			if srv.Enabled != nil && !*srv.Enabled {
				continue
			}
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
		wg.Add(1)
		name, cfg := name, cfg
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

	mcpClient, discovered, err := initializeMcpClient(ctx, name, cfg)
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

func initializeMcpClient(ctx context.Context, name string, cfg McpServerConfig) (*client.Client, []McpDiscoveredTool, error) {
	mcpClient, err := newMcpClient(ctx, cfg)
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

	toolsResult, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		mcpClient.Close()
		return nil, nil, fmt.Errorf("list tools failed: %w", err)
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

func newMcpClient(ctx context.Context, cfg McpServerConfig) (*client.Client, error) {
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
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		mcpClient, err = client.NewStdioMCPClient(cfg.Command, env, cfg.Args...)
		if err != nil {
			return nil, fmt.Errorf("stdio spawn failed: %w", err)
		}
	case "sse":
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, errors.New("sse MCP server requires url")
		}
		mcpClient, err = client.NewSSEMCPClient(cfg.URL, client.WithHeaders(cfg.Headers))
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

// toolSchemaToMap converts MCP ToolInputSchema to a map[string]any for OpenAI tool registration.
func toolSchemaToMap(schema mcp.ToolInputSchema) map[string]any {
	return map[string]any{
		"type":       schema.Type,
		"properties": schema.Properties,
		"required":   schema.Required,
	}
}

func (m *McpManager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	result, err := m.callToolOnce(ctx, serverName, toolName, args)
	if err == nil {
		return result, nil
	}
	if !isMcpInvalidSessionError(err) {
		return "", err
	}

	if reconnectErr := m.reconnectServer(ctx, serverName); reconnectErr != nil {
		return "", fmt.Errorf("MCP call failed: %w; reconnect failed: %w", err, reconnectErr)
	}
	return m.callToolOnce(ctx, serverName, toolName, args)
}

func (m *McpManager) callToolOnce(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	m.mu.RLock()
	handle, ok := m.clients[serverName]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("MCP server %s not found", serverName)
	}
	status := handle.Status
	handleErr := handle.Error
	mcpClient := handle.Client
	m.mu.RUnlock()
	if status != "connected" {
		return "", fmt.Errorf("MCP server %s status: %s/%s", serverName, status, handleErr)
	}
	if mcpClient == nil {
		return "", fmt.Errorf("MCP server %s has no active client", serverName)
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}
	result, err := mcpClient.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("MCP call failed: %w", err)
	}

	var parts []string
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			parts = append(parts, textContent.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func (m *McpManager) reconnectServer(ctx context.Context, serverName string) error {
	m.mu.Lock()
	handle, ok := m.clients[serverName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("MCP server %s not found", serverName)
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

	mcpClient, discovered, err := initializeMcpClient(ctx, serverName, cfg)
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
