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

	toolshared "ally-dev/internal/tools/shared"
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

// McpManager 的连接生命周期总纲：**无状态——模型用则连，不用就不管**。
//
// 连接是易耗品，不是要守护的资产。远程 MCP 的长连流随时会被对端回收（服务端/
// 网关/代理的空闲超时、会话过期后直接 RST，mcp-go 的 SSE 传输连干净的 EOF 都
// 触发断连且无内部重连），"用久了就断"是常态而非故障。Ally 因此不做保活、
// 不做后台重连、也不做连接重试：心跳换不来什么（重连+握手只要几百毫秒），却添
// 一套常驻状态机。连接动作只在三处发生——启动、配置变更、调用时发现没有可用
// 连接；每次只试一次，成不成如实记账（失败是面板上的一行字，不是待办）。
//
// 与"重试"配套的那条纪律：请求**发出过**之后再断的调用绝不重放（服务端可能
// 已经执行了工具，重放等于把副作用做第二遍），只修连接并把判断权交回模型；
// 调用前连接就是死的（请求从未发出）才继续执行。
//
// 注入必须跟着配置开关走，绝不跟连接状态走（collectTools 只排除 disabled）。
// 反过来做（曾经的做法：failed 服务端不参与注入）会两头都坏：
//  1. 模型侧：一次断流就把工具从模型视图整个抽走，冻结的会话工具集缺了它，
//     整段会话再也调不到，只能重启应用；
//  2. 缓存侧：工具集中途增减 = 请求前缀漂移，毒化 KV 前缀缓存——注入视图
//     必须在一个会话内恒定，而连接状态恰恰是会话内最不稳定的东西。
//
// 与此配套的两条纪律：断连绝不清 ToolDefs（handleConnectionLost 保留清单，
// 注入与重连都靠它）；会话工具集冻结在首次请求（sessionToolsets），运行期的
// 清单变化只影响新会话。
type McpManager struct {
	mu             sync.RWMutex
	reconnectLocks sync.Map // serverName -> *sync.Mutex，per-server 重连互斥
	clients        map[string]*McpClientHandle
	toolLookup     map[string]mcpToolRef
	workDir        string // 构造后不再改，读它无需加锁
	listener       func()
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
	// configPaths overrides the default mcp.json path when set; hermetic tests
	// point it at a temp file so reconcile tests never touch the real user
	// config.
	configPaths []string
	// configSnapshot 是最近一次 LoadConfigs 的结果（StartAll 与 ReconcileConfigs
	// 都先加载再拨号），connectOne 拿到拨号锁后拿它对账：拨号携带的 cfg 已被
	// 更新的加载结果取代就放弃，否则旧配置的拨号会把用户刚保存的新配置顶掉。
	configSnapshot map[string]McpServerConfig
	// closed 标记 manager 已被 Shutdown（重启换新 manager）：在飞拨号不得再把
	// 连接提交进来，否则连接挂在无人引用的旧 manager 上，stdio 子进程与传输
	// goroutine 会悬到进程退出。
	closed bool
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

func NewMcpManager(workDir string, listener func()) *McpManager {
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

func mcpUserConfigPath() string {
	return filepath.Join(appDataDir(), "mcp.json")
}

func (m *McpManager) LoadConfigs() (map[string]McpServerConfig, error) {
	merged := make(map[string]McpServerConfig)
	paths := m.configPaths
	if len(paths) == 0 {
		paths = []string{mcpUserConfigPath()}
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
	m.mu.Lock()
	m.configSnapshot = merged
	m.mu.Unlock()
	return merged, nil
}

// dialConfigSuperseded 报告本次拨号携带的配置已被更新的加载结果取代。生产路径
// 上 connectOne 的 cfg 总来自某次 LoadConfigs（StartAll / ReconcileConfigs），
// configSnapshot 就是其中最新的一份：两者对不上，说明拿到拨号锁之前又发生过
// 一次保存/重载，这次拨号已经过期。snapshot 为 nil（从未加载过，直连拨号的
// 测试路径）时无从对账，放行。
func (m *McpManager) dialConfigSuperseded(name string, cfg McpServerConfig) bool {
	m.mu.RLock()
	snapshot := m.configSnapshot
	m.mu.RUnlock()
	if snapshot == nil {
		return false
	}
	current, ok := snapshot[name]
	if !ok {
		return true
	}
	return !mcpServerConfigEqual(current, cfg)
}

// isClosed 报告 manager 是否已被 Shutdown。
func (m *McpManager) isClosed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closed
}

// registerDisabledServer 只登记一条 "disabled" 记录，不拨号不建客户端：状态
// 面板因此能看到"配置了但关着"的服务端，而不是把它静默藏掉。登记后照常推送。
func (m *McpManager) registerDisabledServer(name string, cfg McpServerConfig) {
	m.mu.Lock()
	m.clients[name] = &McpClientHandle{ServerName: name, Config: cfg, Status: "disabled"}
	m.mu.Unlock()
	m.notifyChange()
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
			m.registerDisabledServer(name, cfg)
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
		// 连接语义未变（含"上一次连失败"）：一律不动连接。保存/勾选只负责让新配置
		// 生效，成不成是调用时的事——连接是无状态的，下一次工具调用自己会把连接
		// 建起来，面板上的失败只是一行如实记账，不值得为它花一次拨号的钱（更不
		// 该由"保存"这个无关动作触发）。
		// 勾选变化要原地生效：替换 disabledTools，保持 live 连接，只让下一次
		// buildToolsWithMcp 的注入过滤生效。touched 驱动上层失效上下文缓存并发 status。
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
	// Close 在锁外执行：stdio Close 可能等待子进程退出，持锁调用会拖住整个
	// manager（与 reconnectServer 的锁外 Close 保持对称）。且与拨号同理放
	// 后台：一次 stdio Close 最长可等 2s 优雅 + 3s+3s 强杀（约 8s），同步
	// 等会把面板的"保存"拖成一直转圈。被换下记录的 client 已从 m.clients
	// 摘除、读路径不再触达，迟几秒 Close 不影响正确性；stdio 进程另有
	// KILL_ON_JOB_CLOSE 兜底，退出时不会被这批后台 Close 拖住。
	if len(closing) > 0 {
		go func() {
			for _, staleClient := range closing {
				_ = staleClient.Close()
			}
		}()
	}
	if len(stale) > 0 {
		m.notifyChange()
	}

	// (Re)connect everything missing or changed, mirroring StartAll: disabled
	// configs register a disabled handle instead of a connection attempt.
	// 拨号放后台：握手与工具清单各有一档超时，同步等会把面板的"保存"按在
	// 转圈里；状态变化照常经 connectOne 的 notifyChange 推给界面，调用方立即返回。
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
			m.registerDisabledServer(name, cfg)
			continue
		}
		go m.connectOne(ctx, name, cfg)
	}
	return len(touched), nil
}

func (m *McpManager) connectOne(ctx context.Context, name string, cfg McpServerConfig) {
	// 拨号互斥与重连共用：同名服务端任何时刻只允许一次拨号在飞，否则并发拨号
	// 会互相覆盖记录，输的那次连出来的子进程再也没人 Close（常驻孤儿）。
	unlock := m.lockServerDial(name)
	defer unlock()

	// 拨号前对账：cfg 是某次 ReconcileConfigs / StartAll 快照里的配置，从快照
	// 到拿到拨号锁之间用户可能又保存了新配置，而那次保存的拨号可能排在本拨号
	// 之后才拿锁。旧配置若照常连接并提交，会把用户刚保存的新配置整个顶掉，直到
	// 下次保存——盘是唯一事实源，已被取代的拨号就地放弃，由携带新配置的那次
	// 拨号完成连接。
	if m.dialConfigSuperseded(name, cfg) {
		return
	}
	// manager 已被 Shutdown（重启换新 manager）：继续拨号没有意义，连接提交进
	// 无人引用的旧 manager 只会悬空。
	if m.isClosed() {
		return
	}

	// 拿到拨号锁后核对现状：两次并发的 ReconcileConfigs（面板快速连点保存）会为
	// 同一台刚变动的服务端各排一次拨号，赢的那次提交连接后，输的那次若照常
	// 覆盖 handle，会把刚建好的连接整个换掉、其 client 永远无人 Close。配置相同
	// 的已连接/拨号在飞直接放弃（按需重连排的那次也走这里）。配置不同的不能
	// 放弃：旧配置的拨号可能在对账之后才把 handle 登记
	// 回来（对账拦的是"配置已被更新的加载结果取代"的拨号，拦不住这条——盘上
	// 已是新配置、新配置的拨号正排在锁后面），这里走整体换记录路径把新配置连上。
	m.mu.RLock()
	existing, ok := m.clients[name]
	status := ""
	var existingCfg McpServerConfig
	var existingToolDefs []McpDiscoveredTool
	var existingClient *client.Client
	if ok {
		status = existing.Status
		// Config/ToolDefs 必须在锁内拷出、出锁后再比对：ReconcileConfigs 会
		// 持写锁原地替换 handle.Config.DisabledTools，refreshServerTools 会
		// 持写锁整体换 handle.ToolDefs，锁外裸读这两个字段（结构体与 slice
		// header）就是与它们的数据竞争——status 先拷再出锁是同一课。Client
		// 的写者都持拨号锁（本函数已持有，天然互斥），一并收进快照只为让
		// "读 handle"只有一种写法。
		existingCfg = existing.Config
		existingToolDefs = existing.ToolDefs
		existingClient = existing.Client
	}
	m.mu.RUnlock()
	if ok && (status == "connected" || status == "connecting") && mcpServerConfigEqual(existingCfg, cfg) {
		return
	}

	handle := &McpClientHandle{ServerName: name, Config: cfg, Status: "connecting"}
	var staleClient *client.Client
	if ok {
		// 整体换记录：旧记录按设计留着上一次断连的 client（handleConnectionLost
		// 不关，见其注释），换记录时必须由这里替它 Close，否则 socket 与传输
		// goroutine 悬空。ToolDefs 则要继承：注入跟配置走、不跟连接状态走，换
		// 记录不能让注入视图在本次连接建立期间出现空洞、漂移会话的请求前缀。
		handle.ToolDefs = existingToolDefs
		staleClient = existingClient
	}
	m.mu.Lock()
	m.clients[name] = handle
	m.mu.Unlock()
	m.notifyChange()
	if staleClient != nil {
		_ = staleClient.Close()
	}

	// 一次尝试，不重试：重试会把同一个服务端的拨号锁占住十几秒（一次工具调用撞
	// 上去只能干等，最后拿到的还是同一个失败），失败就如实落 failed 等下次用到
	// 再连（见 McpManager 总纲）。
	established, err := m.initializeMcpClient(ctx, name, cfg)
	if err != nil {
		m.mu.Lock()
		handle.Status = "failed"
		handle.Error = err.Error()
		m.mu.Unlock()
		m.notifyChange()
		return
	}

	// 拨号期间记录可能被并发的 ReconcileConfigs 整体换掉（stale 扫描不跳过
	// connecting，配置变更会先删再连）：把连接提交到幽灵 handle 上，它永远
	// 进不了 m.clients，client 也没人关。Shutdown 清空 map（!live）与 closed
	// 标记（handle 已登记进来）分别拦住两种"提交进废弃 manager"的窗口。与
	// reconnectServer 的 current != handle 核对对称。
	m.mu.Lock()
	current, live := m.clients[name]
	if !live || current != handle || m.closed {
		m.mu.Unlock()
		_ = established.client.Close()
		return
	}
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
		// page 从 0 计数，已完成 page+1 次请求：上限按已完成的请求数对齐，
		// 最多发 maxMcpListToolsPages 次（此前 page >= max 会多发第 101 次）。
		if toolsResult.NextCursor == "" || page+1 >= maxMcpListToolsPages {
			break
		}
		cursor = toolsResult.NextCursor
	}
	return discovered, nil
}

// maxMcpListToolsPages bounds the ListTools pagination loop against servers
// that always return a nextCursor.
const maxMcpListToolsPages = 100

func (m *McpManager) newMcpClient(ctx context.Context, name string, cfg McpServerConfig, token *mcpConnToken) (*client.Client, error) {
	transportName := mcpTransportName(cfg)
	var mcpClient *client.Client
	var err error
	// stdio 专有：cmd 与 job 必须等 transport 的 Start 真正跑完再读（Start 里才
	// spawn 进程）。两者由同一个 goroutine 按序写入、按序读取，无数据竞争。
	var stdioCmd *exec.Cmd
	var stdioJob uintptr
	// 网络配置只取一次：stdio 的代理变量与 http/sse 的私网许可都由它决定，
	// 同一条连接里两处必须看到同一份配置。
	networkCfg := m.currentNetworkConfig()
	allowPrivate := networkCfg.allowPrivateNetworkEnabled()
	switch transportName {
	case "stdio":
		if strings.TrimSpace(cfg.Command) == "" {
			return nil, errors.New("stdio MCP server requires command")
		}
		env := mcpStdioEnv(networkCfg, cfg)
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
		// 私网许可与 http_request / web_fetch 同一个开关：关掉 allowPrivateNetwork
		// 时 MCP 服务端也不该能把请求打到 localhost / 内网（旧实现写死放行，等于
		// 绕过用户刚关掉的那道 SSRF 围栏）。
		httpClient := proxyHTTPClient(networkCfg, allowPrivate, 0)
		mcpClient, err = client.NewSSEMCPClient(cfg.URL, client.WithHeaders(cfg.Headers), client.WithHTTPClient(httpClient))
		if err != nil {
			return nil, fmt.Errorf("sse client failed: %w", err)
		}
	case "streamable-http", "http", "rest":
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, errors.New("http MCP server requires url")
		}
		// 超时不经 http.Client 设定（传 0，与 SSE 分支一致）：Client.Timeout 覆盖
		// 整个请求生命周期（含响应体读取），会把常驻的 GET 监听流（mcp-go
		// listenForever，服务端→客户端通知通道）每 toolTimeoutSec 掐断一次、靠
		// 自愈重试白转一圈。单次工具调用的超时由 CallTool 的 per-call ctx 兜底
		// （mcpToolCallTimeout），握手与 tools/list 各有 initCtx/toolsCtx 上限，
		// 都不依赖这里。WithHTTPTimeout 与 WithHTTPBasicClient 不能并用——后者
		// 直接替换 client，会把前者的赋值整个丢掉。
		mcpClient, err = client.NewStreamableHttpClient(
			cfg.URL,
			transport.WithHTTPHeaders(cfg.Headers),
			transport.WithHTTPBasicClient(proxyHTTPClient(networkCfg, allowPrivate, 0)),
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
//
// 绝对路径的判定不能只看 filepath.IsAbs：Windows 上它要求盘符，配置里从
// Mac/Linux 文档抄来的 "/opt/mcp" 会被误判成相对路径而拼到工作区根下。以 "/"
// 开头的 POSIX 风格根路径同样按绝对路径处理（原样保留斜杠方向，不翻成 "\",
// 用户写什么交什么）。
func (m *McpManager) mcpStdioDir(cfg McpServerConfig) string {
	dir := strings.TrimSpace(cfg.Cwd)
	if dir == "" {
		return m.workDir
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	if strings.HasPrefix(dir, "/") {
		return filepath.ToSlash(filepath.Clean(dir))
	}
	base := strings.TrimSpace(m.workDir)
	if base == "" {
		return ""
	}
	return filepath.Join(base, dir)
}

// mcpStdioEnvAllowlist 是交给 stdio MCP 服务端的环境变量白名单。
//
// 为什么是白名单而不是「全量继承 + 敏感名黑名单」：stdio 服务端是第三方程序
// （npx / uvx / node 起的包），全量继承等于把它放进一个什么都有的 shell 里
// （旧实现就是 os.Environ() 原样传下去）；而按名字猜密钥既有漏（自定义名猜不
// 到）也有误伤。所以默认只给「跑一个子进程本来就需要」的那几个进程级变量，服
// 务端真正需要的变量由它自己的 env 字段显式声明（显式项覆盖同名白名单项，见
// mcpStdioEnv）。代理变量不在此列：它由 prov_proxy.go 一处定义，且由
// mcpStdioBaseEnv 在过滤之后注入。
var mcpStdioEnvAllowlist = []string{
	// 进程启动与可执行查找。PWD 刻意不给：子进程的 cwd 是工作区根，继承 Ally 自己
	// 的 PWD 只会是个错值（Go 只在 Env == nil 时才会替我改它）。
	"PATH", "HOME", "SHELL", "TMPDIR",
	"LANG", "LC_ALL", "LC_CTYPE", "TZ",
	// 企业代理下的自签根证书：丢了这几条会让服务端自己发出的 HTTPS 请求全失败
	// （各家运行时认的变量名不一样，一并给上）。
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	"REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE",
	// uv / npm 一类运行时的缓存与数据目录：缺了会回落到 ~/.cache 重新下载。
	"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
	// Windows：缺 SystemRoot/windir 时 cmd 与部分运行时起不来；其余是常规进程变量。
	"SystemRoot", "SystemDrive", "windir", "ComSpec", "PATHEXT", "OS",
	"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "USERPROFILE", "APPDATA",
	"LOCALAPPDATA", "ProgramData", "ProgramFiles", "ProgramFiles(x86)",
	"TEMP", "TMP", "USERNAME", "USERDOMAIN", "HOMEDRIVE", "HOMEPATH",
}

// mcpStdioBaseEnv 是交给 stdio 服务端的基础环境：从**共享的进程环境**
// （commandBaseEnv）里挑出白名单内的变量，再按当前配置注入代理变量。
//
// 来源刻意不是 os.Environ()，而是 command / 后台服务子进程用的那一份：它多做了
// 一件事——把用户登录 shell 的 PATH 条目并进来（见 infra_shell_env.go）。从 Dock
// 启动 Ally 时父进程的 PATH 很短（没有 Homebrew，也没有 nvm 装的 node），配置里
// 写的 `npx …` 会直接「命令找不到」；接上这份环境，MCP 服务端才和命令行工具看到
// 同一批命令。登录 shell 进程内只探一次（启动时已预热）。
//
// 顺序要紧：**先过滤、再注入代理**。白名单里本来就不该有代理键名（那是
// prov_proxy.go 的唯一来源），反过来写会把已经写好的代理整批滤掉——对配了代理的
// 用户，服务端就从「走代理」静默变成「直连出去」。
//
// 名称比对刻意不分大小写：Windows 的环境变量名不区分大小写（实际常写成
// `Path`），用大小写敏感的白名单会把 PATH 整条丢掉，服务端连命令都找不到。
func mcpStdioBaseEnv(cfg ConfigState) []string {
	return proxyEnvironment(cfg, mcpStdioFilterEnv(commandBaseEnv()))
}

// mcpStdioFilterEnv 是白名单过滤本身，候选环境由调用方给：这条链上唯一会变的
// 就是来源，测试因此可以直接喂一份构造好的环境，不必去跑用户的登录 shell。
func mcpStdioFilterEnv(base []string) []string {
	allowed := make(map[string]struct{}, len(mcpStdioEnvAllowlist))
	for _, name := range mcpStdioEnvAllowlist {
		allowed[strings.ToLower(name)] = struct{}{}
	}
	kept := make([]string, 0, len(mcpStdioEnvAllowlist))
	for _, item := range base {
		key, _, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		if _, keep := allowed[strings.ToLower(key)]; keep {
			kept = append(kept, item)
		}
	}
	return kept
}

// mcpStdioEnv 是 stdio 服务端环境的唯一入口：白名单基础环境 + 服务端声明的
// env。
func mcpStdioEnv(cfg ConfigState, srv McpServerConfig) []string {
	return mcpStdioEnvFromBase(mcpStdioBaseEnv(cfg), srv)
}

// mcpStdioEnvFromBase 把服务端声明的 env 叠到基础环境上：显式项排在最后，
// os/exec 对同名键保留最后一个，显式声明因此总能覆盖基础环境里的同名项（例如服
// 务端要自带 PATH）。按键名排序是为了结果确定：同一份配置每次起的子进程环境逐
// 字节一致。
func mcpStdioEnvFromBase(base []string, srv McpServerConfig) []string {
	env := append(make([]string, 0, len(base)+len(srv.Env)), base...)
	keys := make([]string, 0, len(srv.Env))
	for key := range srv.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+srv.Env[key])
	}
	return env
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
// 工具清单与 client 都保留：失败服务端的工具照常注入（collectTools 只排除
// disabled），而"下一次调用先重连再试"要靠它们把连接救回来（对齐 ZCode 的调用
// 前按需重连）。client 不在这里关闭——本函数可能就跑在传输层的回调/读取路径上，
// 同步关闭有自锁风险；它持有的管道与句柄会在下一次重连、配置调和或退出时随
// Close 一起释放。
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
	// mcpCallConnectionDead：调用前就没有可用连接（上一次的连接定格在 failed）。
	// 请求从未发出去，所以重连之后把这次调用执行一遍是安全的——这是"用则连"，
	// 不是重放。
	mcpCallConnectionDead
	// mcpCallTransportBroken：请求已经发出、链路中途断了（子进程死了、管道关了、
	// 会话 404 过期）。服务端可能已经跑过这个工具、只是结果没回来，所以只修连接、
	// 绝不重放这次调用（见 replaySafeAfterReconnect）。
	mcpCallTransportBroken
	// mcpCallToolFailed：服务端跑过这个工具并回了错误结果（isError:true）。
	mcpCallToolFailed
	// mcpCallUnavailable：服务端被停用、仍在连接中、工具不存在，或调用超时
	// 取消（服务端可能还在跑）——没有可重连的连接。
	mcpCallUnavailable
)

// replaySafeAfterReconnect 报告一次失败后连接已经修好时，能不能把这次调用继续
// 跑下去。唯一判据是"请求有没有发出去过"：没发出去过（连接本来就是死的）可以；
// 其余一律不行——服务端可能已经执行过并产生了副作用，重放就是把副作用做第二遍，
// 交给模型结合工具语义自己判断更安全。
func replaySafeAfterReconnect(failure mcpCallFailure) bool {
	return failure == mcpCallConnectionDead
}

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
	if outcome.failure != mcpCallConnectionDead && outcome.failure != mcpCallTransportBroken {
		return "", err
	}

	// 连接不可用了：先把连接建回来（用则连）。重连与调用共用 callCtx 的预算——
	// 重连若超出剩余额度，整个调用按超时失败，而不是"重连成功了、调用却立即
	// 超时"的自相矛盾结果。
	if reconnectErr := m.reconnectServer(callCtx, serverName, outcome.client); reconnectErr != nil {
		return "", fmt.Errorf("MCP call failed: %w; reconnect failed: %w", err, reconnectErr)
	}
	if !replaySafeAfterReconnect(outcome.failure) {
		// 请求已经发出去过：服务端可能跑完了这个工具，再跑一遍就是把副作用做第二遍。
		// 连接已经修好，把"要不要再来一次"交回模型——它比这里更懂这个工具重做一次
		// 安不安全。
		return "", fmt.Errorf("MCP call failed: %w; connection re-established, this call was NOT replayed (the server may already have executed it)", err)
	}
	result, callErr := m.callToolOnce(callCtx, serverName, toolName, args)
	return result.text, callErr
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
		// failed 是"上一次连接已经死了"，允许调用前重连（请求从未发出，重连后
		// 照常执行）；disabled 与 connecting 都不该由一次工具调用拉起来（后者已有
		// 连接在飞）。
		failure := mcpCallUnavailable
		if status == "failed" {
			failure = mcpCallConnectionDead
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
			failure = mcpCallTransportBroken
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

	established, err := m.initializeMcpClient(ctx, serverName, cfg)
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
	m.closed = true
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

// GetAllTools returns every discovered tool regardless of the per-server
// injection blacklist — the inventory/status layer stays all-visible so the
// UI can offer toggles for hidden tools.
func (m *McpManager) GetAllTools() []McpDiscoveredTool {
	return m.collectTools(false)
}

// GetEnabledTools filters out tools whose original name sits in the server's
// disabledTools blacklist. This is the model-facing view: the single injection
// point is buildToolsWithMcp.
func (m *McpManager) GetEnabledTools() []McpDiscoveredTool {
	return m.collectTools(true)
}

// collectTools 按"配置开了的 server 就注入"收敛：连接是无状态的，注入不能跟着
// 连接状态走。曾经只注入 connected 的清单，一次远端断流（长连 SSE 被服务端/代理
// 回收是常态）就把工具从模型视图里整个抽走——冻结的工具集不再含它，模型从此调
// 不到，只能重启应用。现在 connecting/failed 的服务端照常注入已发现的 ToolDefs
// （断连不清清单，见 handleConnectionLost），调用时由 CallTool 先把连接建回来
// 再说：请求没发出去过就正常执行一次，发出过则只报错、不重放；首次连接还没拿到
// 清单时 ToolDefs 为空，自然无可注入。"disabled" 是用户显式关掉的 server，永不注入。
func (m *McpManager) collectTools(enabledOnly bool) []McpDiscoveredTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []McpDiscoveredTool
	for _, handle := range m.clients {
		if handle.Status == "disabled" {
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

// activeMcpManager 返回当前 MCP manager 的引用快照。mcpManager 指针在运行期
// 会被 RestartMcpServers 更换（网络配置变更从后台 goroutine 触发），而聊天流
// 式路径、前端绑定与 manager 回调都可能同时读它——所有读必须经 a.mu 与写同
// 步。锁内只拷指针、锁外使用：持 a.mu 时绝不调 manager 方法，避免与回调里的
// emitMcpStatus → a.mu 形成反向锁序。
func (a *App) activeMcpManager() *McpManager {
	a.mu.Lock()
	manager := a.mcpManager
	a.mu.Unlock()
	return manager
}

// mcpDeclaredParameters returns the parameter declaration Ally sent the model
// for an MCP tool, or nil when that tool is not part of the current set.
func (a *App) mcpDeclaredParameters(functionName string) map[string]any {
	manager := a.activeMcpManager()
	if manager == nil {
		return nil
	}
	for _, dt := range manager.GetEnabledTools() {
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

// safeMcpFunctionPart 把 server/tool 的原始名字压成模型可见函数名里的安全段：
// 只保留 ASCII 字母数字与连字符，中文等字符直接丢弃；尾部追加原始值的 sha256
// 前 6 位，保证不同原始值（包括被清洗后同形的）不碰撞。空结果兜底为 "mcp"。
func safeMcpFunctionPart(value string) string {
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
	base := strings.Trim(b.String(), "_-")
	if base == "" {
		base = "mcp"
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
	// map 遍历顺序随机，先按 server 名排一遍：设置页每次刷新的服务端顺序要稳定。
	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	sort.Strings(names)
	var result []map[string]any
	for _, name := range names {
		handle := m.clients[name]
		// Per-tool injection state rides along so the settings page can offer
		// checkboxes without a second binding. Disabled servers never
		// discovered tools, so their list is empty by construction.
		disabled := disabledToolSet(handle.Config.DisabledTools)
		tools := make([]map[string]any, 0, len(handle.ToolDefs))
		for _, tool := range handle.ToolDefs {
			description := strings.TrimSpace(tool.Description)
			// 按字符数截断而不是按字节：中文等多字节字符从中间切开会产生
			// 非法 UTF-8，序列化后前端看到的是 replacement char 乱码。
			if runes := []rune(description); len(runes) > mcpStatusToolDescriptionLimit {
				description = strings.TrimSpace(string(runes[:mcpStatusToolDescriptionLimit])) + "…"
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
	m.listener()
}

// ── MCP tool exposure and frontend bindings ──────────────────

// buildToolsWithMcp combines static tools with dynamically discovered MCP tools.
// MCP tools go through GetEnabledTools so a server's disabledTools blacklist
// keeps those schemas out of the model request entirely.
// buildToolsWithMcp appends the enabled MCP tools to the given builtin set.
func (a *App) buildToolsWithMcp(builtins []openai.Tool) []openai.Tool {
	tools := builtins
	manager := a.activeMcpManager()
	if manager == nil {
		return tools
	}
	mcpTools := manager.GetEnabledTools()
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

// buildToolsForConfig is the live toolset for stateless callers (sub-agents,
// scheduled tasks) and the pre-freeze set for new chat sessions: the built-in
// list minus the user-disabled tools (cfg.DisabledTools; the local
// read/edit/create/delete/command core always survives), plus MCP tools.
func (a *App) buildToolsForConfig(cfg ConfigState) []openai.Tool {
	builtins := toolshared.FilterTools(chatTools(), cfg.DisabledTools)
	return a.buildToolsWithMcp(builtins)
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

// cloneTools deep-copies the tool list so a frozen session toolset can never
// be mutated through a shared reference: the schema maps are copied recursively
// (nested properties/required included), not just one level.
func cloneTools(tools []openai.Tool) []openai.Tool {
	if tools == nil {
		return nil
	}
	out := make([]openai.Tool, len(tools))
	for i, t := range tools {
		if t.Function != nil {
			fn := *t.Function
			if params, ok := fn.Parameters.(map[string]any); ok {
				fn.Parameters = deepCopyJSONValue(params)
			}
			t.Function = &fn
		}
		out[i] = t
	}
	return out
}

// deepCopyJSONValue 递归拷贝 JSON Schema 的容器节点（map 与 slice）；标量本就
// 不可变，原样返回。
func deepCopyJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, val := range typed {
			out[key] = deepCopyJSONValue(val)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, val := range typed {
			out[i] = deepCopyJSONValue(val)
		}
		return out
	default:
		return value
	}
}

func (a *App) GetMcpServers() []map[string]any {
	manager := a.activeMcpManager()
	if manager == nil {
		return nil
	}
	return manager.GetServerStatuses()
}

func (a *App) ListTools() []ToolDefinitionSummary {
	// 内置工具的 Enabled 跟随设置页的停用名单（cfg.DisabledTools）：被停用的
	// 工具不再注入模型，清单里以 Enabled=false 呈现（与 MCP 行的 per-server
	// 黑名单同一口径，UI 按此过滤计数）。
	a.mu.Lock()
	disabled := append([]string(nil), a.config.DisabledTools...)
	a.mu.Unlock()
	all := chatTools()
	enabled := make(map[string]bool, len(all))
	for _, tool := range all {
		if tool.Function != nil {
			enabled[tool.Function.Name] = true
		}
	}
	for _, name := range toolshared.SanitizeDisabledTools(disabled) {
		enabled[name] = false
	}
	tools := make([]ToolDefinitionSummary, 0, len(all))
	for _, tool := range all {
		if tool.Function == nil {
			continue
		}
		tools = append(tools, ToolDefinitionSummary{
			Name:        tool.Function.Name,
			Description: strings.TrimSpace(tool.Function.Description),
			Source:      "built-in",
			Enabled:     enabled[tool.Function.Name],
			Protected:   toolshared.IsProtectedTool(tool.Function.Name),
		})
	}
	if manager := a.activeMcpManager(); manager != nil {
		mcpTools := manager.GetAllTools()
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
				Enabled:     !manager.IsToolDisabled(tool.ServerName, tool.Name),
			})
		}
	}
	return tools
}

func (a *App) emitMcpStatus() {
	manager := a.activeMcpManager()
	if a.ctx == nil || manager == nil {
		return
	}
	a.emit("mcp:status", map[string]any{"servers": manager.GetServerStatuses()})
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
	// 重启互斥（见 App.mcpRestartMu）：第二条重启必须等第一条把新 manager
	// 完整落地（StartAll 返回）再动手，否则它 Shutdown 的是前者刚建好、还在
	// 拨号中的 manager，整轮拨号白跑。调用方都是后台路径（网络配置变更、
	// manager 缺失时的 Reconcile），串行等待可接受。
	a.mcpRestartMu.Lock()
	defer a.mcpRestartMu.Unlock()
	if old := a.activeMcpManager(); old != nil {
		old.Shutdown()
	}
	manager := NewMcpManager(root, func() {
		a.emitMcpStatus()
	})
	manager.SetNetworkConfigProvider(func() ConfigState { return a.effectiveConfig(ConfigState{}) })
	manager.SetWarningHandler(func(message string) {
		a.emit("config:warning", map[string]any{"field": "mcp", "message": message})
	})
	// 指针发布持 a.mu：全仓读点都经 activeMcpManager 持锁读，运行期换指针
	// 必须与它们同步（此前裸赋值是与所有读点的数据竞争）。
	a.mu.Lock()
	a.mcpManager = manager
	a.mu.Unlock()
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
	manager := a.activeMcpManager()
	if manager == nil {
		return a.RestartMcpServers()
	}
	_, err := manager.ReconcileConfigs(a.ctx)
	a.emitMcpStatus()
	return err
}

// ── MCP tool execution ───────────────────────────────────────

func (a *App) executeMcpFunctionTool(ctx context.Context, functionName string, args map[string]any) (any, error) {
	manager := a.activeMcpManager()
	if manager == nil {
		return nil, fmt.Errorf("MCP not initialized")
	}
	result, err := manager.CallToolByFunctionName(ctx, functionName, args)
	if err != nil {
		return nil, fmt.Errorf("MCP tool %s failed: %w", functionName, err)
	}
	return map[string]any{"output": result}, nil
}

func (a *App) mcpToolEventMeta(functionName string) map[string]any {
	if !isMcpToolFunctionName(functionName) {
		return nil
	}
	manager := a.activeMcpManager()
	if manager == nil {
		return nil
	}
	ref, ok := manager.DescribeFunctionTool(functionName)
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
