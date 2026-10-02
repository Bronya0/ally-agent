// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
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
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// Cwd 是 stdio 服务端的工作目录：留空用工作区根，相对路径相对工作区根
	// 解析，绝对路径原样使用（不展开 ~）。服务端因此不继承 Ally 自己的 cwd
	// ——从 Finder 启动时那是 "/"，相对参数、`./server`、npx 找本地
	// package.json 都会失效。
	Cwd       string            `json:"cwd,omitempty"`
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	// StartupTimeoutSec / ToolTimeoutSec 覆盖默认的握手（30s）与单次工具调用
	// （5min）超时；0 或缺席即用默认值，超过上限按上限截断。
	StartupTimeoutSec float64 `json:"startupTimeoutSec,omitempty"`
	ToolTimeoutSec    float64 `json:"toolTimeoutSec,omitempty"`
	Enabled           *bool   `json:"enabled,omitempty"`
	// DisabledTools 是按「对端原始工具名」记录的注入黑名单：名单内的工具
	// 不进入模型请求（buildToolsWithMcp 过滤），但清单/状态接口仍可见。
	// 对端新增工具默认启用；对端删除工具后残留条目 inert 保留，不自动清理，
	// 以便对端临时下线再恢复时用户的勾选偏好不丢。
	DisabledTools []string `json:"disabledTools,omitempty"`
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

// mcpConnToken 是一次连接尝试的身份，同时记着"这次连接还没被登记就死了"。
// 进程守护与传输层的断线回调都带着 token 回来核对，它必须做到两件事：
//   - 每次尝试各铸一个：同一个 token 在多次重试间复用的话，前一次尝试里那个
//     被杀掉的进程迟到的死亡报告会命中后来成功的那条连接，把好连接打翻；
//   - 顺手记下丢失事件：握手刚成功、进程就死的窗口里，登记时才能如实落
//     failed，而不是把一条已经没了的连接写成 connected。
type mcpConnToken struct {
	mu      sync.Mutex
	lost    bool
	lostWhy string
}

func (t *mcpConnToken) markLost(cause string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.lost {
		t.lost = true
		t.lostWhy = cause
	}
}

func (t *mcpConnToken) lostCause() (string, bool) {
	if t == nil {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lostWhy, t.lost
}

type McpClientHandle struct {
	ServerName string
	Config     McpServerConfig
	Client     *client.Client
	ToolDefs   []McpDiscoveredTool
	Status     string // "connected", "connecting", "failed", "disabled"
	Error      string
	// token 是当前 Client 的连接身份，见 mcpConnToken。
	token *mcpConnToken
}

type McpManager struct {
	mu             sync.RWMutex
	reconnectLocks sync.Map // serverName -> *sync.Mutex，per-server 重连互斥
	clients        map[string]*McpClientHandle
	toolLookup     map[string]mcpToolRef
	workDir        string // 构造后不再改，读它无需加锁
	listener       func(tools []McpDiscoveredTool)
	warnHandler    func(message string)
	networkConfig  func() ConfigState
	// toolRefresher 是"通知触发的工具清单重拉"动作，默认 refreshServerTools；
	// 与 listener/warnHandler 同一种注入风格，测试可替换。
	toolRefresher func(serverName string, token *mcpConnToken)
	// refreshMu/refreshSlots 收口"服务端通知工具清单变化"后的重拉：每个 server
	// 同时只跑一次 tools/list，期间到达的通知合并进下一轮（pending），不丢也
	// 不叠加。
	refreshMu    sync.Mutex
	refreshSlots map[string]*mcpToolRefreshSlot
	// configPaths overrides mcpJsonPaths when set; hermetic tests point it at
	// a temp file so reconcile tests never touch the real user config.
	configPaths []string
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
	m := &McpManager{
		clients:      make(map[string]*McpClientHandle),
		toolLookup:   make(map[string]mcpToolRef),
		refreshSlots: make(map[string]*mcpToolRefreshSlot),
		workDir:      workDir,
		listener:     listener,
	}
	m.toolRefresher = m.refreshServerTools
	return m
}

func mcpJsonPaths(workDir string) []string {
	return []string{mcpUserConfigPath()}
}

func mcpUserConfigPath() string {
	return filepath.Join(appDataDir(), "mcp.json")
}

func (m *McpManager) LoadConfigs() (map[string]McpServerConfig, error) {
	merged := make(map[string]McpServerConfig)
	paths := m.configPaths
	if len(paths) == 0 {
		paths = mcpJsonPaths(m.workDir)
	}
	for _, path := range paths {
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
			srv.DisabledTools = normalizeDisabledTools(srv.DisabledTools)
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

func mcpServerConfigEqual(a, b McpServerConfig) bool {
	// Normalize the omitted-vs-explicit enabled flag: both mean enabled, and a
	// cosmetic JSON rewrite must not tear down a live connection.
	if a.Enabled == nil {
		enabled := true
		a.Enabled = &enabled
	}
	if b.Enabled == nil {
		enabled := true
		b.Enabled = &enabled
	}
	// disabledTools 只影响注入过滤，不影响连接本身：勾选变化必须走
	// ReconcileConfigs 的原地更新路径，绝不能触发断开重连（stdio 会重启子进程）。
	a.DisabledTools = nil
	b.DisabledTools = nil
	return reflect.DeepEqual(a, b)
}

// normalizeDisabledTools 归一化注入黑名单：去空白、丢空项、按首次出现去重。
// 空结果归 nil，避免空切片与缺席在配置序列化上产生无意义差异。
func normalizeDisabledTools(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func disabledToolsEqual(a, b []string) bool {
	return slices.Equal(normalizeDisabledTools(a), normalizeDisabledTools(b))
}

func disabledToolSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, name := range normalizeDisabledTools(values) {
		set[name] = true
	}
	return set
}

// ReconcileConfigs converges the running servers onto the persisted config
// without touching servers whose configuration is unchanged: removed servers
// are disconnected, added or changed servers reconnect, unchanged servers
// keep their live connections and tool registrations. Returns how many
// servers changed connection state.
func (m *McpManager) ReconcileConfigs(ctx context.Context) (int, error) {
	configs, err := m.LoadConfigs()
	if err != nil {
		return 0, err
	}
	touched := map[string]bool{}

	m.mu.Lock()
	var stale []string
	var failedRetries []string
	var closing []*client.Client
	for name, handle := range m.clients {
		cfg, ok := configs[name]
		if !ok || !mcpServerConfigEqual(handle.Config, cfg) {
			stale = append(stale, name)
			if handle.Client != nil {
				closing = append(closing, handle.Client)
			}
			continue
		}
		// 配置没变但上次连接失败的：它不会自愈，用户点保存/切换勾选就是显式的
		// 重试信号（对齐 codex / kimi 的"配置变更即重连"）。这种重试放后台做：
		// 一次失败的拨号要跑满 4 次尝试（最长可到分钟级），同步等会把面板的
		// "保存"拖成一直转圈。必须重试的原因：failed 服务端的工具不参与注入，
		// 新会话里模型再也调不到它，否则只能靠重启应用才能回来。
		if handle.Status == "failed" {
			failedRetries = append(failedRetries, name)
			touched[name] = true
			continue
		}
		// 连接语义未变但勾选变了：原地替换 disabledTools，保持 live 连接，
		// 只让下一次 buildToolsWithMcp 的注入过滤生效。touched 驱动上层
		// 失效上下文缓存并发 status。
		if !disabledToolsEqual(handle.Config.DisabledTools, cfg.DisabledTools) {
			handle.Config.DisabledTools = cfg.DisabledTools
			touched[name] = true
		}
	}
	for _, name := range stale {
		delete(m.clients, name)
		m.replaceToolLookupLocked(name, nil)
		touched[name] = true
	}
	m.mu.Unlock()
	// 刷新槽按 server 名长期保留，删除服务端时顺手清掉（它由 refreshMu 守护，
	// 与 markToolsListDirty 同源）。
	if len(stale) > 0 {
		m.refreshMu.Lock()
		for _, name := range stale {
			delete(m.refreshSlots, name)
		}
		m.refreshMu.Unlock()
	}
	// 失败重试走后台：connectOne 会先把状态置 connecting，结果照常经
	// notifyChange 推给界面，调用方（面板保存）立刻返回。
	for _, name := range failedRetries {
		cfg, ok := configs[name]
		if !ok {
			continue
		}
		name, cfg := name, cfg
		go func() {
			m.connectOne(ctx, name, cfg)
		}()
	}
	// Close 在锁外执行：stdio Close 可能等待子进程退出，持锁调用会拖住整个
	// manager（与 reconnectServer 的锁外 Close 保持对称）。
	for _, staleClient := range closing {
		_ = staleClient.Close()
	}
	if len(stale) > 0 {
		m.notifyChange()
	}

	// (Re)connect everything missing or changed, mirroring StartAll: disabled
	// configs register a disabled handle instead of a connection attempt.
	var wg sync.WaitGroup
	for name, cfg := range configs {
		name, cfg := name, cfg
		m.mu.RLock()
		_, live := m.clients[name]
		m.mu.RUnlock()
		if live {
			continue
		}
		touched[name] = true
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
	return len(touched), nil
}

func (m *McpManager) connectOne(ctx context.Context, name string, cfg McpServerConfig) {
	// 拨号互斥与重连共用：同名服务端任何时刻只允许一次拨号在飞，否则并发拨号
	// 会互相覆盖记录，输的那次连出来的子进程再也没人 Close（常驻孤儿）。
	unlock := m.lockServerDial(name)
	defer unlock()

	handle := &McpClientHandle{ServerName: name, Config: cfg, Status: "connecting"}
	m.mu.Lock()
	m.clients[name] = handle
	m.mu.Unlock()
	m.notifyChange()

	established, err := m.initializeMcpClientWithRetry(ctx, name, cfg)
	if err != nil {
		m.mu.Lock()
		handle.Status = "failed"
		handle.Error = err.Error()
		m.mu.Unlock()
		m.notifyChange()
		return
	}

	m.mu.Lock()
	m.commitConnectionLocked(handle, established)
	m.mu.Unlock()
	m.notifyChange()
}

// mcpEstablished 是一次成功建立的连接：客户端、工具清单与这条连接的身份。
type mcpEstablished struct {
	client *client.Client
	tools  []McpDiscoveredTool
	token  *mcpConnToken
}

// commitConnectionLocked 把一次成功建立的连接登记到 handle 上（调用方持 m.mu）。
// 登记前先看这条连接是不是已经死了：握手刚成功、进程就被杀的窗口里，得如实落
// failed，而不是把一条已经没了的连接写成 connected（那就回到了"面板永远显示
// 已连接"的老毛病）。
func (m *McpManager) commitConnectionLocked(handle *McpClientHandle, established mcpEstablished) {
	handle.Client = established.client
	handle.ToolDefs = established.tools
	handle.token = established.token
	m.replaceToolLookupLocked(handle.ServerName, established.tools)
	if cause, lost := established.token.lostCause(); lost {
		handle.Status = "failed"
		handle.Error = cause
		return
	}
	handle.Status = "connected"
	handle.Error = ""
}

// lockServerDial 取得某个服务端的拨号互斥（首连与重连共用），返回释放函数。
func (m *McpManager) lockServerDial(serverName string) func() {
	lock, _ := m.reconnectLocks.LoadOrStore(serverName, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

// initializeMcpClient 建立一条连接：铸本次尝试的身份 → 起客户端 → 握手 → 拉
// 工具清单。失败时它负责把已经起来的客户端关掉。
func (m *McpManager) initializeMcpClient(ctx context.Context, name string, cfg McpServerConfig) (mcpEstablished, error) {
	// 每次尝试各自铸一个身份，见 mcpConnToken。
	token := &mcpConnToken{}
	mcpClient, err := m.newMcpClient(ctx, name, cfg, token)
	if err != nil {
		return mcpEstablished{}, err
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

	initCtx, cancel := context.WithTimeout(ctx, mcpStartupTimeout(cfg))
	defer cancel()

	if _, err := mcpClient.Initialize(initCtx, initReq); err != nil {
		mcpClient.Close()
		return mcpEstablished{}, fmt.Errorf("init failed: %w", err)
	}

	// ListTools 与握手同档超时：握手成功却卡在清单拉取的服务端，不能把
	// connectOne 永久钉在 "connecting" 上（超时可 per-server 覆盖）。
	toolsCtx, toolsCancel := context.WithTimeout(ctx, mcpStartupTimeout(cfg))
	defer toolsCancel()
	discovered, err := listMcpTools(toolsCtx, name, mcpClient)
	if err != nil {
		mcpClient.Close()
		return mcpEstablished{}, fmt.Errorf("list tools failed (timed out or refused after %s): %w", mcpStartupTimeout(cfg), err)
	}
	return mcpEstablished{client: mcpClient, tools: discovered, token: token}, nil
}

// listMcpTools 拉取一个服务端的完整工具清单，三条路径共用：连接握手、通知
// 触发的重拉。nextCursor 分页要跟着走（分页服务端第一页只有一部分）；页数上限
// 守住"永远返回 cursor"的服务端。
func listMcpTools(ctx context.Context, serverName string, mcpClient *client.Client) ([]McpDiscoveredTool, error) {
	var discovered []McpDiscoveredTool
	var cursor mcp.Cursor
	for page := 0; ; page++ {
		listReq := mcp.ListToolsRequest{}
		if cursor != "" {
			listReq.Params.Cursor = cursor
		}
		toolsResult, err := mcpClient.ListTools(ctx, listReq)
		if err != nil {
			return nil, err
		}
		for _, tool := range toolsResult.Tools {
			discovered = append(discovered, McpDiscoveredTool{
				ServerName:   serverName,
				Name:         tool.Name,
				FunctionName: mcpToolFunctionName(serverName, tool.Name),
				Description:  tool.Description,
				Schema:       toolSchemaToMap(tool.InputSchema),
			})
		}
		if toolsResult.NextCursor == "" || page >= maxMcpListToolsPages {
			break
		}
		cursor = toolsResult.NextCursor
	}
	return discovered, nil
}

// maxMcpListToolsPages bounds the ListTools pagination loop against servers
// that always return a nextCursor.
const maxMcpListToolsPages = 100

// 连接失败的固定重试策略：三条连接路径（启动 StartAll、配置变更
// ReconcileConfigs、调用期 reconnectServer）共用，覆盖网络瞬时波动，
// 避免一次失败就定格在 failed 直到手动刷新。
const (
	mcpConnectRetries    = 3
	mcpConnectRetryDelay = 3 * time.Second
)

// initializeMcpClientWithRetry wraps initializeMcpClient with a fixed number
// of retries and a fixed delay between attempts. ctx cancellation (app exit,
// server removed) aborts the wait immediately; the returned error carries the
// last attempt's cause.
func (m *McpManager) initializeMcpClientWithRetry(ctx context.Context, name string, cfg McpServerConfig) (mcpEstablished, error) {
	var lastErr error
	for attempt := 0; attempt <= mcpConnectRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return mcpEstablished{}, lastErr
			case <-time.After(mcpConnectRetryDelay):
			}
		}
		established, err := m.initializeMcpClient(ctx, name, cfg)
		if err == nil {
			return established, nil
		}
		lastErr = err
	}
	return mcpEstablished{}, fmt.Errorf("connect failed after %d retries: %w", mcpConnectRetries, lastErr)
}

func (m *McpManager) newMcpClient(ctx context.Context, name string, cfg McpServerConfig, token *mcpConnToken) (*client.Client, error) {
	transportName := mcpTransportName(cfg)
	var mcpClient *client.Client
	var err error
	// stdio 专有：cmd 与 job 必须等 transport 的 Start 真正跑完再读（Start 里才
	// spawn 进程）。两者由同一个 goroutine 按序写入、按序读取，无数据竞争。
	var stdioCmd *exec.Cmd
	var stdioJob uintptr
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
		// reconnect, and on Unix the wrapper's descendants would outlive the
		// root process. Use WithCommandFunc to take ownership of Cmd creation:
		// set the working directory, hide the console window, join the process
		// into a KILL_ON_JOB_CLOSE Job Object, and watch it for exit (same
		// orphan protection as the service tool).
		mcpClient, err = client.NewStdioMCPClientWithOptions(cfg.Command, env, cfg.Args,
			transport.WithCommandFunc(func(ctx context.Context, command string, cmdEnv []string, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Env = cmdEnv
				cmd.Dir = m.mcpStdioDir(cfg)
				hideCommandWindow(cmd)
				stdioJob = prepareServiceCommand(cmd)
				stdioCmd = cmd
				return cmd, nil
			}),
		)
		if err != nil {
			discardProcessJob(stdioJob)
			return nil, fmt.Errorf("stdio spawn failed: %w", err)
		}
		drainMcpStderr(mcpClient)
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
		// 超时只经 basic client 设定：WithHTTPTimeout 与 WithHTTPBasicClient
		// 不能并用——后者直接替换 client，会把前者的赋值整个丢掉。用工具调用
		// 超时（per-server 可覆盖）而不是写死的 60s：否则 http 服务端的
		// toolTimeoutSec 形同虚设，长任务到 60s 必断。
		mcpClient, err = client.NewStreamableHttpClient(
			cfg.URL,
			transport.WithHTTPHeaders(cfg.Headers),
			transport.WithHTTPBasicClient(proxyHTTPClient(m.currentNetworkConfig(), true, mcpToolCallTimeout(cfg))),
		)
		if err != nil {
			return nil, fmt.Errorf("http client failed: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", cfg.Transport)
	}
	if err := mcpClient.Start(ctx); err != nil {
		discardProcessJob(stdioJob)
		_ = mcpClient.Close()
		return nil, fmt.Errorf("%s start failed: %w", transportName, err)
	}
	if stdioCmd != nil {
		m.watchStdioProcess(name, stdioCmd, stdioJob, token)
	}
	// 服务端运行期增删工具只会通过这条通知告知（插件加载、权限变化）。回调
	// 跑在 transport 的读取循环里，所以里面只能置脏标记，真正的 tools/list
	// 必须另起 goroutine。
	mcpClient.OnNotification(func(notification mcp.JSONRPCNotification) {
		if notification.Method == mcpMethodToolListChanged {
			m.markToolsListDirty(name, token)
		}
	})
	mcpClient.OnConnectionLost(func(err error) {
		m.handleConnectionLost(name, token, "MCP connection lost: "+err.Error())
	})
	return mcpClient, nil
}

// mcpStdioDir 决定 stdio 服务端的工作目录：显式 cwd 优先（相对路径相对工作区
// 根解析，绝对路径原样，不展开 ~），否则用工作区根；两者都空时返回 ""，交给
// exec 继承当前目录。
func (m *McpManager) mcpStdioDir(cfg McpServerConfig) string {
	dir := strings.TrimSpace(cfg.Cwd)
	if dir == "" {
		return m.workDir
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	base := strings.TrimSpace(m.workDir)
	if base == "" {
		return ""
	}
	return filepath.Join(base, dir)
}

// watchStdioProcess 守护一个 stdio MCP 子进程：等它退出，清理可能脱管的孙
// 进程，并在"这仍是当前那条连接"时把状态翻成 failed（下一次调用会重连自愈）。
//
// 退出判据用 os.Process.Wait 而不是轮询 kill(pid,0)：Unix 上子进程退出后会
// 先变成僵尸，kill(pid,0) 依旧返回成功，只有 wait4 能看出它已经不在了
// （对齐 codex 的进程句柄语义）。代价是 mcp-go 自己的 Close→cmd.Wait() 会拿到
// ECHILD：它只把该错误返回给调用方，而所有调用点都忽略 Close 的返回值，既不
// 会 panic 也不会漏杀进程；进程已死时反而让 Close 立即返回，不必再等满
// 2s+3s+3s 的优雅期。
func (m *McpManager) watchStdioProcess(serverName string, cmd *exec.Cmd, job uintptr, token *mcpConnToken) {
	proc := cmd.Process
	if proc == nil {
		discardProcessJob(job)
		return
	}
	pid := proc.Pid
	go func() {
		if err := registerProcessJob(pid, job); err != nil {
			// 注册失败（例如进程已被别的 job 接管）：同样要关掉 handle。
			discardProcessJob(job)
		}
		state, err := proc.Wait()
		unregisterProcessJob(pid)
		// 先翻状态再回收残留进程组：状态要尽快诚实，那 1s 的宽限期不该拖住
		// 界面（两者互不依赖）。
		m.handleConnectionLost(serverName, token, mcpProcessExitCause(state, err))
		reapProcessGroupLeftovers(pid)
	}()
}

func mcpProcessExitCause(state *os.ProcessState, err error) string {
	switch {
	case state != nil:
		return fmt.Sprintf("MCP server process exited (%s)", state)
	case err != nil:
		return fmt.Sprintf("MCP server process exited: %v", err)
	default:
		return "MCP server process exited"
	}
}

// handleConnectionLost 把一次"连接意外结束"（子进程退出、SSE 断流）记进状态。
// 判定必须同时满足"记录还是那一条"与"状态仍是 connected"：重连会把状态先改成
// connecting、删除会先把记录摘掉、退出会整体换掉 map——这些正常路径都不该被
// 当成故障，更不能让旧连接的死亡打翻刚建好的新连接。
//
// 工具清单与 client 都保留：失败服务端本就不参与工具注入，而"下一次调用先
// 重连再试"要靠它们把连接救回来（对齐 ZCode 的调用前按需重连）。client 不在
// 这里关闭——本函数可能就跑在传输层的回调/读取路径上，同步关闭有自锁风险；
// 它持有的管道与句柄会在下一次重连、配置调和或退出时随 Close 一起释放。
func (m *McpManager) handleConnectionLost(serverName string, token *mcpConnToken, cause string) {
	// 先记进 token：连接可能还没被登记到 handle 上（握手刚成功、进程就死），
	// 登记那一步要靠它把结果落成 failed。
	token.markLost(cause)

	m.mu.Lock()
	handle, ok := m.clients[serverName]
	if !ok || handle.token != token || handle.Status != "connected" {
		m.mu.Unlock()
		return
	}
	handle.Status = "failed"
	handle.Error = cause
	m.mu.Unlock()
	m.notifyChange()
}

// mcpMethodToolListChanged 是服务端"工具清单变了"的通知方法名。
const mcpMethodToolListChanged = "notifications/tools/list_changed"

// mcpToolRefreshDebounce 合并突发通知：一次清单变化常连发多条，隔一下再拉，
// 避免对着同一个服务端连打 tools/list。
const mcpToolRefreshDebounce = 500 * time.Millisecond

// mcpToolRefreshSlot 是每个 server 的刷新槽：running = 已有一轮在跑，
// pending = 跑的过程中又来了通知（下一轮必须再拉一次，不能丢）。
type mcpToolRefreshSlot struct {
	running bool
	pending bool
	token   *mcpConnToken
}

// markToolsListDirty 处理服务端通知：置脏 + 保证每个 server 同时只有一轮刷新。
// 通知回调跑在 transport 的读取循环里，这里绝不允许做同步的 tools/list。
func (m *McpManager) markToolsListDirty(serverName string, token *mcpConnToken) {
	m.refreshMu.Lock()
	if m.refreshSlots == nil {
		m.refreshSlots = map[string]*mcpToolRefreshSlot{}
	}
	slot, ok := m.refreshSlots[serverName]
	if !ok {
		slot = &mcpToolRefreshSlot{}
		m.refreshSlots[serverName] = slot
	}
	slot.pending = true
	slot.token = token
	if slot.running {
		m.refreshMu.Unlock()
		return
	}
	slot.running = true
	refresh := m.toolRefresher
	m.refreshMu.Unlock()
	go m.runToolRefresh(serverName, slot, refresh)
}

// runToolRefresh 是一轮"防抖 + 不丢"的刷新循环：refresh 收到的是最新一次通知
// 的连接身份，连接被换掉时 refreshServerTools 会自行放弃。
func (m *McpManager) runToolRefresh(serverName string, slot *mcpToolRefreshSlot, refresh func(string, *mcpConnToken)) {
	for {
		time.Sleep(mcpToolRefreshDebounce)
		m.refreshMu.Lock()
		slot.pending = false
		token := slot.token
		m.refreshMu.Unlock()

		refresh(serverName, token)

		m.refreshMu.Lock()
		if !slot.pending {
			slot.running = false
			m.refreshMu.Unlock()
			return
		}
		m.refreshMu.Unlock()
	}
}

// refreshServerTools 重拉一个已连接服务端的工具清单。连接已被替换（token 不
// 匹配）或当前不是 connected 时直接放弃——那不是这条连接的活。拉取失败不做
// 任何状态变更：一次临时失败不该把一个好好的连接翻成 failed，等下一次通知或
// 重连即可。
//
// 已冻结的会话工具集（sessionToolsets）不会因此变化，新工具在下一个会话可见
// ——冻结是提示词前缀缓存的前提，不能被一次通知破掉。
func (m *McpManager) refreshServerTools(serverName string, token *mcpConnToken) {
	m.mu.RLock()
	handle, ok := m.clients[serverName]
	if !ok || handle.token != token || handle.Status != "connected" || handle.Client == nil {
		m.mu.RUnlock()
		return
	}
	mcpClient := handle.Client
	cfg := handle.Config
	m.mu.RUnlock()

	// 通知没有调用方 ctx：自己带一个与握手同档的上限，别让拉取悬空。
	ctx, cancel := context.WithTimeout(context.Background(), mcpStartupTimeout(cfg))
	defer cancel()
	discovered, err := listMcpTools(ctx, serverName, mcpClient)
	if err != nil {
		return
	}
	m.mu.Lock()
	if current, ok := m.clients[serverName]; ok && current.token == token && current.Client == mcpClient && current.Status == "connected" {
		current.ToolDefs = discovered
		m.replaceToolLookupLocked(serverName, discovered)
	}
	m.mu.Unlock()
	m.notifyChange()
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

const (
	// mcpDefaultToolCallTimeout limits unbounded MCP calls so a hung external
	// server (or long CAD modeling phase) does not freeze the run indefinitely.
	mcpDefaultToolCallTimeout = 5 * time.Minute
	// mcpDefaultStartupTimeout 约束握手与随后的 tools/list：一条连接得先"能
	// 用"，才有资格进入模型请求。
	mcpDefaultStartupTimeout = 30 * time.Second
	// mcpMaxTimeout 是 per-server 覆盖值的上限：小时级足够，同时挡住误填的
	// 天文数字（time.Duration 溢出会变成负数，等于立即超时）。
	mcpMaxTimeout = 12 * time.Hour
)

// mcpTimeoutFromSeconds 是配置里的秒数换算超时的唯一入口：非正数用默认值，
// 超过上限一律截断（超时是资源约束，不该被配置写成无限）。
func mcpTimeoutFromSeconds(seconds float64, fallback time.Duration) time.Duration {
	// 写成 !(seconds > 0) 而不是 seconds <= 0：NaN 两种都比不出来，落进后面
	// 的换算会得到一个实现定义的时长（可能是极大的负数 = 立即超时）。
	if !(seconds > 0) {
		return fallback
	}
	if seconds > mcpMaxTimeout.Seconds() {
		return mcpMaxTimeout
	}
	return time.Duration(seconds * float64(time.Second))
}

// mcpStartupTimeout / mcpToolCallTimeout 是两档超时的唯一出处（连接建立、工具
// 调用各一档），都支持 per-server 覆盖。
func mcpStartupTimeout(cfg McpServerConfig) time.Duration {
	return mcpTimeoutFromSeconds(cfg.StartupTimeoutSec, mcpDefaultStartupTimeout)
}

func mcpToolCallTimeout(cfg McpServerConfig) time.Duration {
	return mcpTimeoutFromSeconds(cfg.ToolTimeoutSec, mcpDefaultToolCallTimeout)
}

// mcpCallFailure 说明一次 MCP 调用失败的性质，决定"是否值得重连后再试一次"。
// 只有连接本身不可用才允许重连；服务端已经跑过工具再回错误的情况必须原样
// 上报——重试会让有副作用的工具发生第二次（对齐 codex：运行期只重试
// tools/list，工具调用绝不自动重放）。
type mcpCallFailure int

const (
	mcpCallSucceeded mcpCallFailure = iota
	// mcpCallReconnectable：传输层断了（子进程死了、管道关了、会话 404 过期）
	// 或上一次连接已经定格在 failed —— 值得先重连再试一次。
	mcpCallReconnectable
	// mcpCallToolFailed：服务端跑过这个工具并回了错误结果（isError:true）。
	mcpCallToolFailed
	// mcpCallUnavailable：服务端被停用、仍在连接中、工具不存在，或调用超时
	// 取消（服务端可能还在跑）——没有可重连的连接，重试也不安全。
	mcpCallUnavailable
)

// mcpCallOutcome 是一次 callToolOnce 的结果信封：文本、当时用的 client
// （重连时用来核对是不是同一条连接）与失败归类。
type mcpCallOutcome struct {
	text    string
	client  *client.Client
	failure mcpCallFailure
}

func (m *McpManager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, mcpToolCallTimeout(m.serverConfig(serverName)))
	defer cancel()

	outcome, err := m.callToolOnce(callCtx, serverName, toolName, args)
	if err == nil {
		return outcome.text, nil
	}
	if outcome.failure != mcpCallReconnectable {
		return "", err
	}

	// 连接不可用了：重连一次再试。重试的是连接，至多重放一次调用——服务端
	// 已经执行过工具并明确报错（isError / 协议错误码）的一律不走到这里。
	if reconnectErr := m.reconnectServer(ctx, serverName, outcome.client); reconnectErr != nil {
		return "", fmt.Errorf("MCP call failed: %w; reconnect failed: %w", err, reconnectErr)
	}
	retry, retryErr := m.callToolOnce(callCtx, serverName, toolName, args)
	return retry.text, retryErr
}

// serverConfig 取某个服务端的当前配置（超时、cwd、env 都从这里读）；服务端已
// 被移除时返回零值配置，即走默认超时。
func (m *McpManager) serverConfig(serverName string) McpServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if handle, ok := m.clients[serverName]; ok {
		return handle.Config
	}
	return McpServerConfig{}
}

func (m *McpManager) callToolOnce(ctx context.Context, serverName, toolName string, args map[string]any) (mcpCallOutcome, error) {
	m.mu.RLock()
	handle, ok := m.clients[serverName]
	if !ok {
		m.mu.RUnlock()
		return mcpCallOutcome{failure: mcpCallUnavailable}, fmt.Errorf("MCP server %s not found", serverName)
	}
	status := handle.Status
	handleErr := handle.Error
	mcpClient := handle.Client
	m.mu.RUnlock()
	if status != "connected" {
		// failed 是"上一次连接已经死了"，允许调用前重连自愈；disabled 与
		// connecting 都不该由一次工具调用拉起来（后者已有连接在飞）。
		failure := mcpCallUnavailable
		if status == "failed" {
			failure = mcpCallReconnectable
		}
		return mcpCallOutcome{client: mcpClient, failure: failure},
			fmt.Errorf("MCP server %s status: %s/%s", serverName, status, handleErr)
	}
	if mcpClient == nil {
		return mcpCallOutcome{failure: mcpCallUnavailable}, fmt.Errorf("MCP server %s has no active client", serverName)
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}
	result, err := mcpClient.CallTool(ctx, req)
	if err != nil {
		failure := mcpCallUnavailable
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			// 超时/取消：服务端可能还在跑这个工具，重试就是把副作用放两遍。
		case isMcpTransportFailure(err):
			failure = mcpCallReconnectable
		}
		return mcpCallOutcome{client: mcpClient, failure: failure}, fmt.Errorf("MCP call failed: %w", err)
	}

	outText := mcpToolResultText(result)
	if result.IsError {
		if strings.TrimSpace(outText) == "" {
			outText = "MCP tool reported failure (isError: true)"
		}
		// 服务端确实执行了这个工具：错误文案再像网络故障也不能重连或重试。
		// 旧的纯子串判据下，一个回 "connection refused" 的工具会让 Ally 杀掉
		// 并重启整个 MCP 服务端、再把工具跑第二遍。
		return mcpCallOutcome{client: mcpClient, failure: mcpCallToolFailed},
			fmt.Errorf("MCP tool %s error: %s", toolName, outText)
	}
	return mcpCallOutcome{text: outText, client: mcpClient, failure: mcpCallSucceeded}, nil
}

// mcpToolResultText renders every supported MCP content block into
// model-readable text, preserving the server's content order. Text passes
// through unchanged; binary blocks (image/audio/blob) become metadata
// placeholders — base64 payloads are pure token noise for a text model;
// embedded text resources inline their text; structured output (2025-06-18
// spec) serializes as JSON so structured-only servers no longer read as empty.
func mcpToolResultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, content := range result.Content {
		switch typed := content.(type) {
		case mcp.TextContent:
			if typed.Text != "" {
				parts = append(parts, typed.Text)
			}
		case mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[image content: %s, %d bytes base64 data omitted]", typed.MIMEType, len(typed.Data)))
		case mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio content: %s, %d bytes base64 data omitted]", typed.MIMEType, len(typed.Data)))
		case mcp.ResourceLink:
			link := typed.URI
			if name := strings.TrimSpace(typed.Name); name != "" {
				link = name + " (" + typed.URI + ")"
			}
			parts = append(parts, "[resource link: "+link+"]")
		case mcp.EmbeddedResource:
			switch resource := typed.Resource.(type) {
			case mcp.TextResourceContents:
				if resource.Text != "" {
					parts = append(parts, resource.Text)
				}
			case mcp.BlobResourceContents:
				parts = append(parts, fmt.Sprintf("[embedded resource %s: %s, %d bytes base64 data omitted]", resource.URI, resource.MIMEType, len(resource.Blob)))
			}
		}
	}
	if result.StructuredContent != nil {
		if raw, err := json.Marshal(result.StructuredContent); err == nil {
			parts = append(parts, "structured output: "+string(raw))
		}
	}
	return strings.Join(parts, "\n")
}

func (m *McpManager) reconnectServer(ctx context.Context, serverName string, failedClient *client.Client) error {
	// 拨号互斥（与首连共用）：一台服务端的慢重连不该串住别的服务端，但同名
	// 服务端不能有两次拨号同时在飞（见 connectOne）。
	unlock := m.lockServerDial(serverName)
	defer unlock()

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

	established, err := m.initializeMcpClientWithRetry(ctx, serverName, cfg)
	m.mu.Lock()
	current, ok := m.clients[serverName]
	// 记录被换掉时不能往上写（配置调和重建了这台服务端）：否则我们刚连出来的
	// 客户端会挂在别人的记录上，或者反过来变成再也没人关闭的孤儿进程。
	if !ok || current != handle {
		m.mu.Unlock()
		if established.client != nil {
			_ = established.client.Close()
		}
		return fmt.Errorf("MCP server %s was replaced during reconnect", serverName)
	}
	if err != nil {
		current.Status = "failed"
		current.Error = err.Error()
		m.mu.Unlock()
		m.notifyChange()
		return err
	}
	m.commitConnectionLocked(current, established)
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

// mcpTransportFailureSentinels 是 mcp-go 自己导出的"连接已不可用"哨兵错误。
// 判据必须优先走 errors.Is：手写子串清单会漏——曾经漏掉 stdio 的
// "transport closed" 与 streamable-http 的 "session terminated (404)"，于是
// 子进程死了、会话过期了都只会把错误丢给模型，自动重连形同虚设。
var mcpTransportFailureSentinels = []error{
	transport.ErrTransportClosed,   // stdio：子进程退出 / 管道关闭
	transport.ErrSessionTerminated, // streamable-http：404 会话已失效
}

// mcpTransportFailureMarkers 是兜底文案特征：覆盖 mcp-go 没有导出哨兵的传输
// 错误（SSE 的 "connection has been closed"）与中转服务端自述的会话文案。
var mcpTransportFailureMarkers = []string{
	"invalid session id",
	"invalid session",
	"session not found",
	"session expired",
	"session terminated (404)",
	"transport closed",
	"transport is closing",
	"client is closed",
	"connection has been closed",
	"closed pipe",
	"broken pipe",
	"connection reset",
	"connection refused",
	"eof",
}

// mcpProtocolErrorSentinels 是 mcp-go 把标准 JSON-RPC 错误码映射出来的哨兵：
// 它们说明"服务端收到了这个请求并明确拒绝"，与链路无关，绝不重连。
// （非标准错误码 mcp-go 只留一个字符串错误，分不出来源——所以下面的文案兜底
// 仍然要覆盖它，中转服务端的"会话失效"就是这么报的。）
var mcpProtocolErrorSentinels = []error{
	mcp.ErrParseError,
	mcp.ErrInvalidRequest,
	mcp.ErrMethodNotFound,
	mcp.ErrInvalidParams,
}

// isMcpTransportFailure 判断一个错误是否意味着当前连接已经不能用了。
// 只能喂给它链路层/协议层的错误：服务端执行工具后回的 isError 结果绝不能进
// 这里（见 mcpCallToolFailed）。
func isMcpTransportFailure(err error) bool {
	if err == nil {
		return false
	}
	for _, sentinel := range mcpProtocolErrorSentinels {
		if errors.Is(err, sentinel) {
			return false
		}
	}
	for _, sentinel := range mcpTransportFailureSentinels {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range mcpTransportFailureMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
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
	closing := make([]*client.Client, 0, len(m.clients))
	for _, handle := range m.clients {
		if handle.Client != nil {
			closing = append(closing, handle.Client)
		}
	}
	m.clients = make(map[string]*McpClientHandle)
	m.toolLookup = make(map[string]mcpToolRef)
	m.mu.Unlock()
	// Close 必须在锁外、且并行：stdio 的 Close 含 2s 优雅 + 3s+3s 强杀等待，
	// 持锁逐个关会把整个 manager（状态推送、工具清单）卡住数秒到数十秒（与
	// ReconcileConfigs / reconnectServer 的锁外 Close 对齐；退出时 N 个服务端
	// 也不该串成 8s×N）。
	var wg sync.WaitGroup
	for _, mcpClient := range closing {
		wg.Add(1)
		go func(c *client.Client) {
			defer wg.Done()
			_ = c.Close()
		}(mcpClient)
	}
	wg.Wait()
}

// GetAllTools returns every discovered tool from connected servers regardless
// of the per-server injection blacklist — the inventory/status layer stays
// all-visible so the UI can offer toggles for hidden tools.
func (m *McpManager) GetAllTools() []McpDiscoveredTool {
	return m.collectTools(false)
}

// GetEnabledTools filters out tools whose original name sits in the server's
// disabledTools blacklist. This is the model-facing view: the single injection
// point is buildToolsWithMcp.
func (m *McpManager) GetEnabledTools() []McpDiscoveredTool {
	return m.collectTools(true)
}

func (m *McpManager) collectTools(enabledOnly bool) []McpDiscoveredTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []McpDiscoveredTool
	for _, handle := range m.clients {
		if handle.Status != "connected" {
			continue
		}
		var disabled map[string]bool
		if enabledOnly {
			disabled = disabledToolSet(handle.Config.DisabledTools)
		}
		for _, tool := range handle.ToolDefs {
			if disabled[tool.Name] {
				continue
			}
			all = append(all, tool)
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

// mcpDeclaredParameters returns the parameter declaration Ally sent the model
// for an MCP tool, or nil when that tool is not part of the current set.
func (a *App) mcpDeclaredParameters(functionName string) map[string]any {
	if a.mcpManager == nil {
		return nil
	}
	for _, dt := range a.mcpManager.GetEnabledTools() {
		name := dt.FunctionName
		if name == "" {
			name = mcpToolFunctionName(dt.ServerName, dt.Name)
		}
		if name == functionName {
			return dt.Schema
		}
	}
	return nil
}

// mcpUnknownArgWarnings names the arguments an MCP call carries that its
// declaration never promised. MCP tools keep the tolerant path — there is no
// built-in schema for the argument gate to enforce — so without this notice a
// typo reaches the server and is dropped there with the model none the wiser.
// A tool with no declared properties reports nothing: silence beats flagging
// every argument of an unknown shape.
func (a *App) mcpUnknownArgWarnings(functionName string, args map[string]any) []string {
	properties, ok := a.mcpDeclaredParameters(functionName)["properties"].(map[string]any)
	if !ok || len(properties) == 0 || len(args) == 0 {
		return nil
	}
	unknown := make([]string, 0, len(args))
	for key := range args {
		if _, declared := properties[key]; !declared {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return []string{fmt.Sprintf("以下参数不在工具声明中，已原样转发给 MCP 服务端（服务端可能忽略）：%s", strings.Join(unknown, ", "))}
}

// IsToolDisabled reports whether a server's tool sits in its injection
// blacklist. Unknown servers/tools are never disabled (absence = enabled, so
// server-side additions default on).
func (m *McpManager) IsToolDisabled(serverName, toolName string) bool {
	m.mu.RLock()
	handle, ok := m.clients[serverName]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	return disabledToolSet(handle.Config.DisabledTools)[strings.TrimSpace(toolName)]
}

func mcpToolFunctionName(serverName, toolName string) string {
	return mcpFunctionNamePrefix + safeMcpFunctionPart(serverName) + "__" + safeMcpFunctionPart(toolName)
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

const (
	// mcpStatusToolDescriptionLimit bounds each tool description inside the
	// mcp:status payload / GetMcpServers binding; the UI only previews it.
	mcpStatusToolDescriptionLimit = 200
)

func (m *McpManager) GetServerStatuses() []map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []map[string]any
	for name, handle := range m.clients {
		// Per-tool injection state rides along so the settings page can offer
		// checkboxes without a second binding. Disabled servers never
		// discovered tools, so their list is empty by construction.
		disabled := disabledToolSet(handle.Config.DisabledTools)
		tools := make([]map[string]any, 0, len(handle.ToolDefs))
		for _, tool := range handle.ToolDefs {
			description := strings.TrimSpace(tool.Description)
			if len(description) > mcpStatusToolDescriptionLimit {
				description = strings.TrimSpace(description[:mcpStatusToolDescriptionLimit]) + "…"
			}
			tools = append(tools, map[string]any{
				"name":        tool.Name,
				"description": description,
				"disabled":    disabled[tool.Name],
			})
		}
		sort.SliceStable(tools, func(i, j int) bool {
			return tools[i]["name"].(string) < tools[j]["name"].(string)
		})
		result = append(result, map[string]any{
			"name":      name,
			"status":    handle.Status,
			"error":     handle.Error,
			"toolCount": len(handle.ToolDefs),
			"transport": mcpTransportName(handle.Config),
			"tools":     tools,
		})
	}
	return result
}

// mcpTransportName 是 MCP transport 推断的唯一入口：显式配置优先，未配置时
// 按 command/url 推断，http/rest 是 streamable-http 的别名。调用点（连接、
// 状态展示）共用本函数，不要各自实现。
func mcpTransportName(cfg McpServerConfig) string {
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
// MCP tools go through GetEnabledTools so a server's disabledTools blacklist
// keeps those schemas out of the model request entirely.
func (a *App) buildToolsWithMcp() []openai.Tool {
	tools := chatTools()
	if a.mcpManager == nil {
		return tools
	}
	mcpTools := a.mcpManager.GetEnabledTools()
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

// buildToolsForSession returns the model-visible tool schemas for a chat
// session, frozen at the session's first request (see sessionToolsets on App
// for the rationale). Stateless callers (sub-agents, scheduled tasks) use
// buildToolsForConfig instead and always see the live set.
func (a *App) buildToolsForSession(sessionID string, cfg ConfigState) []openai.Tool {
	if strings.TrimSpace(sessionID) == "" {
		return a.buildToolsForConfig(cfg)
	}
	a.mu.Lock()
	if frozen, ok := a.sessionToolsets[sessionID]; ok {
		a.mu.Unlock()
		return cloneTools(frozen)
	}
	a.mu.Unlock()

	tools := a.buildToolsForConfig(cfg)
	a.mu.Lock()
	if a.sessionToolsets == nil {
		a.sessionToolsets = map[string][]openai.Tool{}
	}
	a.sessionToolsets[sessionID] = cloneTools(tools)
	a.mu.Unlock()
	return tools
}

// sessionToolsetForBreakdown returns the tool list the footer context
// breakdown counts: the session-frozen set once frozen, the live set before
// that (peek semantics — footer polling must not pin the session's toolset
// ahead of the first real request).
func (a *App) sessionToolsetForBreakdown(sessionID string, cfg ConfigState) []openai.Tool {
	if strings.TrimSpace(sessionID) != "" {
		a.mu.Lock()
		frozen, ok := a.sessionToolsets[sessionID]
		a.mu.Unlock()
		if ok {
			return cloneTools(frozen)
		}
	}
	return a.buildToolsForConfig(cfg)
}

// cloneTools deep-copies the schema maps so a frozen session toolset can never
// be mutated through a shared map reference.
func cloneTools(tools []openai.Tool) []openai.Tool {
	if tools == nil {
		return nil
	}
	out := make([]openai.Tool, len(tools))
	for i, t := range tools {
		if t.Function != nil {
			if params, ok := t.Function.Parameters.(map[string]any); ok {
				copied := make(map[string]any, len(params))
				for k, v := range params {
					copied[k] = v
				}
				fn := *t.Function
				fn.Parameters = copied
				t.Function = &fn
			} else {
				fn := *t.Function
				t.Function = &fn
			}
		}
		out[i] = t
	}
	return out
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
			Enabled:     true,
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
				Enabled:     !a.mcpManager.IsToolDisabled(tool.ServerName, tool.Name),
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

// drainMcpStderr consumes a stdio MCP subprocess's stderr for the lifetime of the
// client. mcp-go creates the stderr pipe but never reads it (it only exposes it
// through client.GetStderr), so a chatty npx/node/python server fills the ~64 KiB
// pipe buffer, blocks on its next write, and stops answering JSON-RPC: every tool
// call then runs into the call timeout while the server still reports connected.
func drainMcpStderr(c *client.Client) {
	reader, ok := client.GetStderr(c)
	if !ok || reader == nil {
		return
	}
	go func() {
		// The drain must never stop early: dropping the reader would let the pipe
		// buffer fill up again and bring the hang back.
		_, _ = io.Copy(io.Discard, reader)
	}()
}

// SaveMcpConfig 写盘时保留配置里的未知字段：解析成 RawMessage 后原样写回，只做
// "结构校验 + 统一缩进"。按固定 struct 重建会把 cwd、超时与外部客户端
// （Claude Desktop / Cursor）的专有键整段吃掉——用户从面板导入一次就少一半。
func (a *App) SaveMcpConfig(raw string) error {
	data, err := normalizeMcpConfigJSON(raw)
	if err != nil {
		return err
	}
	path := mcpUserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// mcp.json 与 config.json 同规格：走原子写。原地截断写在崩溃时留下半截 JSON，
	// 下次启动整份 MCP 配置回默认值，用户自己写的服务器列表就没了。
	return writeAtomicBytes(path, data, 0o600)
}

// defaultMcpConfigJSON 是空配置的落盘形态。
const defaultMcpConfigJSON = "{\"mcpServers\":{}}"

// normalizeMcpConfigJSON 校验并规整一份 mcp.json：mcpServers 必须是对象，每个
// server 的已知字段类型必须合法（写错了当场报错，而不是等到启动才发现全部加载
// 失败），其余键一律原样保留。键序由 map 序列化保证（升序）。
func normalizeMcpConfigJSON(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultMcpConfigJSON
	}
	doc := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("invalid MCP JSON: %w", err)
	}
	servers := map[string]json.RawMessage{}
	if rawServers, ok := doc["mcpServers"]; ok {
		// null 按"没有 server"收（与缺席同义，落盘成 {}）：它是空配置的一种
		// 写法，不是语法错误。数组/字符串/数字才是真写错了，当场报错，别静默
		// 写成一份空配置让用户以为保存成功了。
		if err := json.Unmarshal(rawServers, &servers); err != nil {
			return nil, fmt.Errorf("invalid mcpServers (expected an object): %w", err)
		}
	}
	if servers == nil {
		servers = map[string]json.RawMessage{}
	}
	for name, rawServer := range servers {
		// server 值是 null（而不是对象）也是写错：它会变成一个既无 command
		// 也无 url 的"服务端"，启动时只报一句难以定位的错。
		if trimmed := strings.TrimSpace(string(rawServer)); trimmed == "null" {
			return nil, fmt.Errorf("invalid MCP server %q (expected an object, got null)", name)
		}
		var cfg McpServerConfig
		if err := json.Unmarshal(rawServer, &cfg); err != nil {
			return nil, fmt.Errorf("invalid MCP server %q: %w", name, err)
		}
	}
	rawServers, err := json.Marshal(servers)
	if err != nil {
		return nil, err
	}
	doc["mcpServers"] = rawServers
	packed, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	// RawMessage 在 MarshalIndent 里只走"原样紧凑"路径（自定义 Marshaler 的输出
	// 不会被重新缩进），不补这一步的话整份 server 清单会挤成一长行——面板里那个
	// 手写 JSON 的地方就没法看了。json.Indent 按文本重新排版，键序不变。
	var out bytes.Buffer
	if err := json.Indent(&out, packed, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
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

// ReconcileMcpServers converges the running servers onto the saved config:
// only added, removed, or changed servers are (re)connected, while unchanged
// servers keep their live connections. Used by the MCP settings page so
// toggling or editing one server does not restart every other one.
func (a *App) ReconcileMcpServers() error {
	if _, err := a.getConfig(); err != nil {
		return err
	}
	if a.ctx == nil {
		return errors.New("application context is not ready")
	}
	if a.mcpManager == nil {
		return a.RestartMcpServers()
	}
	_, err := a.mcpManager.ReconcileConfigs(a.ctx)
	a.emitMcpStatus()
	return err
}

// ── MCP tool execution ───────────────────────────────────────

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
	if a.mcpManager == nil || !isMcpToolFunctionName(functionName) {
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
