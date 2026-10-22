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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ally-dev/internal/tools/sshclient"
	"github.com/google/uuid"
)

// SSHServerNode represents a persisted SSH server node in the cluster inventory.
type SSHServerNode struct {
	ID          string `json:"id"`                  // Stable local identity, independent of alias and endpoint
	Alias       string `json:"alias"`               // Editable, case-insensitively unique nickname
	Group       string `json:"group"`               // 分组（纯归类，仅供 UI 折叠/筛选）；空值归入 sshUngroupedGroup
	Host        string `json:"host"`                // IP or domain, e.g. "192.168.1.101"
	Port        int    `json:"port"`                // Port, default 22
	Username    string `json:"username"`            // User, e.g. "root"
	AuthType    string `json:"authType"`            // "key", "password", "agent"
	KeyPath     string `json:"keyPath,omitempty"`   // Path to private key file
	Password    string `json:"password,omitempty"`  // Stored password (local only)
	Description string `json:"description"`         // Required purpose/role description for model and user context
	RiskLevel   string `json:"riskLevel,omitempty"` // "low", "high"
	Status      string `json:"status"`              // "approved", "pending_approval"
	CreatedBy   string `json:"createdBy"`           // "user", "agent"
	CreatedAtMS int64  `json:"createdAtMs"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

const (
	sshAuthTypeAgent    = "agent"
	sshAuthTypeKey      = "key"
	sshAuthTypePassword = "password"
)

// sshUngroupedGroup 是未指定分组的节点（含改版前的历史节点）所归入的组。
// 前端 utils/sshClusterGroups.mjs 用同一字面量作默认值，改动须两边同步（有测试钉住）。
const sshUngroupedGroup = "未分组"

// normalizeSSHGroup 去掉首尾空白；空值一律落到未分组，历史节点加载时同样补齐。
func normalizeSSHGroup(group string) string {
	group = strings.TrimSpace(group)
	if group == "" {
		return sshUngroupedGroup
	}
	return group
}

// normalizeSSHAuthType keeps the credential interpretation tied to the node's
// declared mode. Legacy nodes without authType infer it from their credentials.
func normalizeSSHAuthType(node SSHServerNode) string {
	switch strings.ToLower(strings.TrimSpace(node.AuthType)) {
	case sshAuthTypeAgent:
		return sshAuthTypeAgent
	case sshAuthTypeKey:
		return sshAuthTypeKey
	case sshAuthTypePassword:
		return sshAuthTypePassword
	default:
		if strings.TrimSpace(node.KeyPath) != "" {
			return sshAuthTypeKey
		}
		if node.Password != "" {
			return sshAuthTypePassword
		}
		return sshAuthTypeAgent
	}
}

// normalizeSSHServerNode makes stored secrets match the declared auth mode.
// In particular, agent nodes must not retain credentials hidden from the UI.
func normalizeSSHServerNode(node SSHServerNode) SSHServerNode {
	node.Group = normalizeSSHGroup(node.Group)
	node.AuthType = normalizeSSHAuthType(node)
	switch node.AuthType {
	case sshAuthTypeAgent:
		node.Password = ""
		node.KeyPath = ""
	case sshAuthTypePassword:
		node.KeyPath = ""
	}
	return node
}

// SSHServerSummary is the model-facing DTO: sensitive fields (password, keyPath)
// are intentionally omitted to prevent secret exposure to LLM contexts.
type SSHServerSummary struct {
	Alias       string `json:"alias"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Description string `json:"description"`
	RiskLevel   string `json:"riskLevel,omitempty"`
	Status      string `json:"status"`
}

type sshClusterStore struct {
	Version          int                 `json:"version"` // v2: workspaceAllowed contains IDs, v1/absent: aliases
	Servers          []SSHServerNode     `json:"servers"`
	WorkspaceAllowed map[string][]string `json:"workspaceAllowed,omitempty"`
}

func normalizeWorkspaceKey(ws string) string {
	ws = strings.TrimSpace(ws)
	if ws == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(ws))
}

// nodeSSHHost 是节点在 ssh 命令行里的目的地，也是凭据槽位的主机部分：
// 节点没写 user@ 时由 Username 补上。别名解析与凭据失效共用这一处，
// 两边各写一遍就会把凭据存进一个谁也找不到的槽位。
func nodeSSHHost(node SSHServerNode) string {
	host := strings.TrimSpace(node.Host)
	if node.Username != "" && !strings.Contains(host, "@") {
		return node.Username + "@" + host
	}
	return host
}

// splitSSHUserHost 把 user@host / host 拆成小写的用户名与主机名。
func splitSSHUserHost(value string) (string, string) {
	value = strings.ToLower(strings.TrimSpace(value))
	if at := strings.LastIndex(value, "@"); at >= 0 {
		return value[:at], value[at+1:]
	}
	return "", value
}

// findSSHServerByEndpoint 用真实端点（user@host[:port]）反查已登记节点：
// 别名只是模型的写法之一，用户给模型的多半是真地址，只认别名会让已授权、
// 已存密码的节点在换一种写法后被当成陌生主机（再要一次审批、且拿不到凭据）。
// 主机、端口（缺省均为 22）必须完全一致；查询里写了用户名时还要用户名一致。
// 命中多个节点时视为歧义，返回未命中，让调用方回到原始目标路径而不是猜一个。
func (a *App) findSSHServerByEndpoint(host, port string) (SSHServerNode, bool) {
	queryUser, queryHost := splitSSHUserHost(host)
	if queryHost == "" {
		return SSHServerNode{}, false
	}
	queryPort := strings.TrimSpace(port)
	if queryPort == "" {
		queryPort = "22"
	}

	a.sshClustersMu.RLock()
	defer a.sshClustersMu.RUnlock()
	var match SSHServerNode
	found := false
	for _, node := range a.sshClusters {
		_, nodeHost := splitSSHUserHost(node.Host)
		if nodeHost != queryHost {
			continue
		}
		nodePort := "22"
		if node.Port > 0 {
			nodePort = strconv.Itoa(node.Port)
		}
		if nodePort != queryPort {
			continue
		}
		if queryUser != "" && queryUser != strings.ToLower(strings.TrimSpace(node.Username)) {
			continue
		}
		if found && match.ID != node.ID {
			return SSHServerNode{}, false
		}
		match = node
		found = true
	}
	return match, found
}

func (a *App) sshClustersFilePath() string {
	if strings.TrimSpace(a.configPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(a.configPath), "ssh_clusters.json")
}

// loadSSHClusters loads the cluster configuration from disk into memory.
func (a *App) loadSSHClusters() error {
	path := a.sshClustersFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read ssh clusters: %w", err)
	}
	var store sshClusterStore
	if err := json.Unmarshal(data, &store); err != nil {
		return fmt.Errorf("unmarshal ssh clusters: %w", err)
	}

	a.sshClustersMu.Lock()
	defer a.sshClustersMu.Unlock()
	servers := make(map[string]SSHServerNode, len(store.Servers))
	aliases := make(map[string]string, len(store.Servers))
	idCounts := make(map[string]int, len(store.Servers))
	for _, s := range store.Servers {
		idCounts[strings.TrimSpace(s.ID)]++
	}
	migrated := store.Version < 2
	for _, s := range store.Servers {
		s = normalizeSSHServerNode(s)
		alias := strings.ToLower(strings.TrimSpace(s.Alias))
		if alias == "" {
			continue
		}
		s.ID = strings.TrimSpace(s.ID)
		if s.ID == "" || idCounts[s.ID] > 1 {
			// Repair ambiguous IDs rather than letting one node overwrite another.
			// v2 grants to duplicate IDs are discarded below; legacy alias grants
			// can still be migrated unambiguously.
			for {
				s.ID = "ssh_" + uuid.NewString()
				if _, exists := servers[s.ID]; idCounts[s.ID] == 0 && !exists {
					break
				}
			}
			migrated = true
		}
		if previousID := aliases[alias]; previousID != "" {
			delete(servers, previousID)
		}
		servers[s.ID] = s
		aliases[alias] = s.ID
	}

	a.sshWorkspaceAllowedMu.Lock()
	allowed := make(map[string]map[string]bool, len(store.WorkspaceAllowed))
	for ws, refs := range store.WorkspaceAllowed {
		cleanWS := normalizeWorkspaceKey(ws)
		if cleanWS == "" {
			continue
		}
		if allowed[cleanWS] == nil {
			allowed[cleanWS] = make(map[string]bool, len(refs))
		}
		for _, ref := range refs {
			id := strings.TrimSpace(ref)
			if store.Version < 2 {
				id = aliases[strings.ToLower(id)]
			}
			if _, exists := servers[id]; exists {
				allowed[cleanWS][id] = true
			}
		}
	}
	previousServers, previousAllowed := a.sshClusters, a.sshWorkspaceAllowed
	a.sshClusters, a.sshWorkspaceAllowed = servers, allowed
	a.sshWorkspaceAllowedMu.Unlock()

	// Persist generated IDs before exposing them, so restarting cannot change
	// identity or lose a migrated selection even when no user edit follows.
	if migrated {
		if err := a.saveSSHClustersLocked(); err != nil {
			a.sshWorkspaceAllowedMu.Lock()
			a.sshClusters, a.sshWorkspaceAllowed = previousServers, previousAllowed
			a.sshWorkspaceAllowedMu.Unlock()
			return fmt.Errorf("migrate ssh clusters: %w", err)
		}
	}
	return nil
}

// saveSSHClustersLocked writes the in-memory cluster map to disk atomically.
// Must be called with a.sshClustersMu held.
func (a *App) saveSSHClustersLocked() error {
	path := a.sshClustersFilePath()
	if path == "" {
		return nil
	}
	servers := a.sortedSSHServersLocked()

	a.sshWorkspaceAllowedMu.RLock()
	wsAllowed := make(map[string][]string, len(a.sshWorkspaceAllowed))
	for ws, m := range a.sshWorkspaceAllowed {
		cleanWS := normalizeWorkspaceKey(ws)
		if cleanWS == "" {
			continue
		}
		var list []string
		for id, allowed := range m {
			if _, exists := a.sshClusters[id]; allowed && exists {
				list = append(list, id)
			}
		}
		if len(list) > 0 {
			sort.Strings(list)
			wsAllowed[cleanWS] = list
		}
	}
	a.sshWorkspaceAllowedMu.RUnlock()

	store := sshClusterStore{
		Version:          2,
		Servers:          servers,
		WorkspaceAllowed: wsAllowed,
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmpFile := filepath.Join(dir, fmt.Sprintf(".ssh_clusters.%d.tmp", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpFile, path)
}

// sortedSSHServersLocked snapshots nodes in case-insensitive alias order, shared
// by persistence and UI lists. Caller holds sshClustersMu for reading or writing.
func (a *App) sortedSSHServersLocked() []SSHServerNode {
	servers := make([]SSHServerNode, 0, len(a.sshClusters))
	for _, s := range a.sshClusters {
		servers = append(servers, s)
	}
	sort.Slice(servers, func(i, j int) bool {
		return strings.ToLower(servers[i].Alias) < strings.ToLower(servers[j].Alias)
	})
	return servers
}

// ListAllSSHServers returns all configured servers in alias order (for UI settings).
func (a *App) ListAllSSHServers() []SSHServerNode {
	a.sshClustersMu.RLock()
	defer a.sshClustersMu.RUnlock()
	return a.sortedSSHServersLocked()
}

// ListAuthorizedSSHServers returns clean summaries of servers authorized for
// the given workspace. Sensitive secrets are scrubbed.
func (a *App) ListAuthorizedSSHServers(workspace string) []SSHServerSummary {
	a.sshClustersMu.RLock()
	defer a.sshClustersMu.RUnlock()

	normWS := normalizeWorkspaceKey(workspace)
	a.sshWorkspaceAllowedMu.RLock()
	defer a.sshWorkspaceAllowedMu.RUnlock()
	allowedMap := a.sshWorkspaceAllowed[normWS]

	res := make([]SSHServerSummary, 0, len(a.sshClusters))
	for id, s := range a.sshClusters {
		if allowedMap != nil && !allowedMap[id] {
			continue
		}
		// If no explicit whitelist is configured yet, or server is whitelisted:
		if allowedMap == nil && len(a.sshClusters) > 0 {
			// When no whitelist is configured for this workspace, default to denying
			// unless explicitly whitelisted or authorized.
			continue
		}
		res = append(res, SSHServerSummary{
			Alias:       s.Alias,
			Host:        s.Host,
			Port:        s.Port,
			Username:    s.Username,
			Description: s.Description,
			RiskLevel:   s.RiskLevel,
			Status:      s.Status,
		})
	}
	sort.Slice(res, func(i, j int) bool {
		return strings.ToLower(res[i].Alias) < strings.ToLower(res[j].Alias)
	})
	return res
}

// GetSSHServer looks up a server node by alias (case-insensitive).
func (a *App) GetSSHServer(alias string) (SSHServerNode, bool) {
	a.sshClustersMu.RLock()
	defer a.sshClustersMu.RUnlock()
	return a.sshServerByAliasLocked(alias)
}

// Model-facing aliases stay case-insensitive; persisted identity is always ID.
// Caller holds sshClustersMu.
func (a *App) sshServerByAliasLocked(alias string) (SSHServerNode, bool) {
	alias = strings.TrimSpace(alias)
	for _, node := range a.sshClusters {
		if strings.EqualFold(strings.TrimSpace(node.Alias), alias) {
			return node, true
		}
	}
	return SSHServerNode{}, false
}

// UI calls use IDs. Accept aliases for older callers and model tools, but
// canonicalize them at the boundary; never store aliases as grants again.
func (a *App) sshServerByRefLocked(ref string) (SSHServerNode, bool) {
	ref = strings.TrimSpace(ref)
	if node, ok := a.sshClusters[ref]; ok {
		return node, true
	}
	return a.sshServerByAliasLocked(ref)
}

// SaveSSHServer creates or updates a server node in the cluster.
func (a *App) SaveSSHServer(node SSHServerNode) error {
	alias := strings.ToLower(strings.TrimSpace(node.Alias))
	if alias == "" {
		return errors.New("server alias is required")
	}
	if strings.TrimSpace(node.Host) == "" {
		return errors.New("server host is required")
	}
	if strings.TrimSpace(node.Username) == "" {
		return errors.New("server username is required")
	}
	if strings.TrimSpace(node.Description) == "" {
		return errors.New("server description is required")
	}
	node.Description = strings.TrimSpace(node.Description)
	node.Alias = strings.TrimSpace(node.Alias)
	node = normalizeSSHServerNode(node)
	if node.Port <= 0 {
		node.Port = 22
	}
	if err := a.storeSSHServerLocked(alias, node); err != nil {
		return err
	}
	a.emitSSHClustersChanged()
	return nil
}

// storeSSHServerLocked 落盘一份节点，并失效被它改掉的凭据槽位。调用方不得持有锁。
func (a *App) storeSSHServerLocked(alias string, node SSHServerNode) error {
	a.sshClustersMu.Lock()
	defer a.sshClustersMu.Unlock()
	if a.sshClusters == nil {
		a.sshClusters = make(map[string]SSHServerNode)
	}
	node.ID = strings.TrimSpace(node.ID)
	previous, updating := a.sshClusters[node.ID]
	if node.ID != "" && !updating {
		return errors.New("server no longer exists; reload the server list")
	}
	if sameAlias, exists := a.sshServerByAliasLocked(alias); exists {
		if node.ID != "" && node.ID != sameAlias.ID {
			return fmt.Errorf("server alias %q is already in use", node.Alias)
		}
		// Legacy callers and imports upsert by alias without changing identity.
		previous, updating = sameAlias, true
		node.ID = sameAlias.ID
	}
	if node.ID == "" {
		for {
			node.ID = "ssh_" + uuid.NewString()
			if _, exists := a.sshClusters[node.ID]; !exists {
				break
			}
		}
	}
	if updating {
		node.CreatedAtMS = previous.CreatedAtMS
		node.CreatedBy = previous.CreatedBy
	}
	now := time.Now().UnixMilli()
	if node.CreatedAtMS == 0 {
		node.CreatedAtMS = now
	}
	node.UpdatedAtMS = now
	a.sshClusters[node.ID] = node
	if err := a.saveSSHClustersLocked(); err != nil {
		if updating {
			a.sshClusters[node.ID] = previous
		} else {
			delete(a.sshClusters, node.ID)
		}
		return err
	}
	// Invalidate both endpoints, including when the nickname was changed.
	if updating {
		a.sshCredentials.purgeHost(nodeSSHHost(previous))
	}
	a.sshCredentials.purgeHost(nodeSSHHost(node))
	return nil
}

// emitSSHClustersChanged 通知 UI 集群清单/工作区授权变了（模型也能改这份清单），
// 让输入框上方的授权浮层不必等到切 Tab 才刷新。刻意在锁外发：emit 会回到 WebView，
// 持锁发事件就把前端回调锁在门外了。
func (a *App) emitSSHClustersChanged() {
	a.emit("ssh:clusters-changed", map[string]any{})
}

// DeleteSSHServer removes a server node by ID (legacy aliases also accepted).
func (a *App) DeleteSSHServer(ref string) error {
	if err := a.deleteSSHServerLocked(ref); err != nil {
		return err
	}
	a.emitSSHClustersChanged()
	return nil
}

// deleteSSHServerLocked 删节点并失效它的凭据槽位。调用方不得持有锁。
func (a *App) deleteSSHServerLocked(ref string) error {
	a.sshClustersMu.Lock()
	defer a.sshClustersMu.Unlock()
	previous, ok := a.sshServerByRefLocked(ref)
	if !ok {
		return nil
	}
	delete(a.sshClusters, previous.ID)
	// 删除节点必须同时撤销所有工作区的授权，避免计数残留以及同名节点继承旧授权。
	a.sshWorkspaceAllowedMu.Lock()
	for ws, allowed := range a.sshWorkspaceAllowed {
		delete(allowed, previous.ID)
		if len(allowed) == 0 {
			delete(a.sshWorkspaceAllowed, ws)
		}
	}
	a.sshWorkspaceAllowedMu.Unlock()
	a.sshCredentials.purgeHost(nodeSSHHost(previous))
	return a.saveSSHClustersLocked()
}

// AuthorizeServerForWorkspace grants access by ID (legacy aliases accepted).
func (a *App) AuthorizeServerForWorkspace(workspace, alias string) {
	normWS := normalizeWorkspaceKey(workspace)
	alias = strings.TrimSpace(alias)
	if normWS == "" || alias == "" {
		return
	}
	a.authorizeServerForWorkspaceLocked(normWS, alias)
	a.emitSSHClustersChanged()
}

// authorizeServerForWorkspaceLocked 落盘工作区白名单的增量授权。调用方不得持有锁。
func (a *App) authorizeServerForWorkspaceLocked(normWS, alias string) {
	a.sshClustersMu.Lock()
	defer a.sshClustersMu.Unlock()
	node, exists := a.sshServerByRefLocked(alias)
	if !exists {
		return
	}

	a.sshWorkspaceAllowedMu.Lock()
	if a.sshWorkspaceAllowed == nil {
		a.sshWorkspaceAllowed = make(map[string]map[string]bool)
	}
	if a.sshWorkspaceAllowed[normWS] == nil {
		a.sshWorkspaceAllowed[normWS] = make(map[string]bool)
	}
	a.sshWorkspaceAllowed[normWS][node.ID] = true
	a.sshWorkspaceAllowedMu.Unlock()

	_ = a.saveSSHClustersLocked()
}

// SetWorkspaceAllowedServers replaces a workspace's grants with canonical IDs.
// Legacy aliases are accepted and resolved once at this boundary.
func (a *App) SetWorkspaceAllowedServers(workspace string, aliases []string) error {
	normWS := normalizeWorkspaceKey(workspace)
	if normWS == "" {
		return errors.New("workspace is required")
	}
	if err := a.setWorkspaceAllowedServersLocked(normWS, aliases); err != nil {
		return err
	}
	a.emitSSHClustersChanged()
	return nil
}

// setWorkspaceAllowedServersLocked 整份替换一个工作区的白名单。调用方不得持有锁。
func (a *App) setWorkspaceAllowedServersLocked(normWS string, aliases []string) error {
	a.sshClustersMu.Lock()
	defer a.sshClustersMu.Unlock()

	m := make(map[string]bool, len(aliases))
	for _, ref := range aliases {
		if node, exists := a.sshServerByRefLocked(ref); exists {
			m[node.ID] = true
		}
	}
	a.sshWorkspaceAllowedMu.Lock()
	if a.sshWorkspaceAllowed == nil {
		a.sshWorkspaceAllowed = make(map[string]map[string]bool)
	}
	a.sshWorkspaceAllowed[normWS] = m
	a.sshWorkspaceAllowedMu.Unlock()

	return a.saveSSHClustersLocked()
}

// GetWorkspaceAllowedServers returns stable server IDs allowed for a workspace.
func (a *App) GetWorkspaceAllowedServers(workspace string) []string {
	normWS := normalizeWorkspaceKey(workspace)
	a.sshClustersMu.RLock()
	defer a.sshClustersMu.RUnlock()
	a.sshWorkspaceAllowedMu.RLock()
	defer a.sshWorkspaceAllowedMu.RUnlock()
	if a.sshWorkspaceAllowed == nil {
		return nil
	}
	m := a.sshWorkspaceAllowed[normWS]
	if m == nil {
		return nil
	}
	res := make([]string, 0, len(m))
	for id, allowed := range m {
		if _, exists := a.sshClusters[id]; allowed && exists {
			res = append(res, id)
		}
	}
	sort.Strings(res)
	return res
}

// IsServerAuthorizedForWorkspace checks a node's ID (legacy aliases accepted).
func (a *App) IsServerAuthorizedForWorkspace(workspace, alias string) bool {
	normWS := normalizeWorkspaceKey(workspace)
	alias = strings.TrimSpace(alias)
	a.sshClustersMu.RLock()
	defer a.sshClustersMu.RUnlock()
	node, exists := a.sshServerByRefLocked(alias)
	if !exists {
		return false
	}
	a.sshWorkspaceAllowedMu.RLock()
	defer a.sshWorkspaceAllowedMu.RUnlock()
	if a.sshWorkspaceAllowed == nil {
		return false
	}
	m := a.sshWorkspaceAllowed[normWS]
	if m == nil {
		return false
	}
	return m[node.ID]
}

// IsServerConnectionApproved checks if connection to alias was approved in this session.
func (a *App) IsServerConnectionApproved(sessionID, alias string) bool {
	sessionID = strings.TrimSpace(sessionID)
	alias = strings.ToLower(strings.TrimSpace(alias))
	if sessionID == "" || alias == "" {
		return false
	}
	a.sshSessionApprovalsMu.Lock()
	defer a.sshSessionApprovalsMu.Unlock()
	if a.sshSessionApprovals == nil {
		return false
	}
	m := a.sshSessionApprovals[sessionID]
	if m == nil {
		return false
	}
	return m[alias]
}

// ApproveServerConnection marks a server connection as approved for the given session.
func (a *App) ApproveServerConnection(sessionID, alias string) {
	sessionID = strings.TrimSpace(sessionID)
	alias = strings.ToLower(strings.TrimSpace(alias))
	if sessionID == "" || alias == "" {
		return
	}
	a.sshSessionApprovalsMu.Lock()
	defer a.sshSessionApprovalsMu.Unlock()
	if a.sshSessionApprovals == nil {
		a.sshSessionApprovals = make(map[string]map[string]bool)
	}
	if a.sshSessionApprovals[sessionID] == nil {
		a.sshSessionApprovals[sessionID] = make(map[string]bool)
	}
	a.sshSessionApprovals[sessionID][alias] = true
}

// SSHTestResult carries the outcome of an SSH connectivity test.
type SSHTestResult struct {
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// TestSSHServer tests basic SSH connectivity and authentication for a server node.
// If node.Host is empty and node.Alias is specified, the node is loaded from storage.
func (a *App) TestSSHServer(node SSHServerNode) SSHTestResult {
	if strings.TrimSpace(node.Host) == "" && strings.TrimSpace(node.Alias) != "" {
		saved, ok := a.GetSSHServer(node.Alias)
		if !ok {
			return SSHTestResult{OK: false, Error: fmt.Sprintf("server %q not found", node.Alias)}
		}
		node = saved
	} else if strings.TrimSpace(node.Alias) != "" && node.Password == "" {
		if saved, ok := a.GetSSHServer(node.Alias); ok && saved.Password != "" {
			node.Password = saved.Password
		}
	}

	node = normalizeSSHServerNode(node)

	host := nodeSSHHost(node)
	if strings.TrimSpace(host) == "" {
		return SSHTestResult{OK: false, Error: "server host is required"}
	}

	if node.AuthType == sshAuthTypePassword && node.Password == "" {
		return SSHTestResult{OK: false, Error: "密码认证需要填写密码 (Password is required)"}
	}
	if node.AuthType == sshAuthTypeKey && strings.TrimSpace(node.KeyPath) == "" {
		return SSHTestResult{OK: false, Error: "密钥认证需要指定私钥路径 (Key path is required)"}
	}

	portStr := "22"
	if node.Port > 0 {
		portStr = strconv.Itoa(node.Port)
	}

	sshConfig := sshclient.Config{
		Host:           host,
		Port:           portStr,
		AuthMode:       sshclient.AuthModeAgent,
		KnownHostsPath: a.sshKnownHostsPath,
	}

	switch node.AuthType {
	case sshAuthTypeKey:
		sshConfig.AuthMode = sshclient.AuthModeKey
		sshConfig.KeyPath = strings.TrimSpace(node.KeyPath)
		sshConfig.KeyPassphrase = node.Password
	case sshAuthTypePassword:
		sshConfig.AuthMode = sshclient.AuthModePassword
		sshConfig.Password = node.Password
	case sshAuthTypeAgent:
		sshConfig.AuthMode = sshclient.AuthModeAgent
	}

	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// 指纹记录与服务器不一致时同样是直接换记录再连一次（与远程工具口径一致，
	// 见 orch_remote.go 的 refreshHostKeyAndRetry），所以「测试」按钮不会再卡在
	// 一个需要用户核对身份的失败上。
	duration, err := refreshHostKeyAndRetry(func() (time.Duration, error) {
		return sshclient.Test(testCtx, sshConfig)
	})
	if err != nil {
		if errors.Is(err, sshclient.ErrAuthentication) {
			return SSHTestResult{
				OK:         false,
				DurationMS: duration.Milliseconds(),
				Error:      "认证失败，请检查用户名、密码或私钥配置",
			}
		}
		return SSHTestResult{
			OK:         false,
			DurationMS: duration.Milliseconds(),
			Error:      err.Error(),
		}
	}

	return SSHTestResult{
		OK:         true,
		DurationMS: duration.Milliseconds(),
	}
}
