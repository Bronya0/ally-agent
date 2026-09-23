// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SSHServerNode represents a persisted SSH server node in the cluster inventory.
type SSHServerNode struct {
	ID          string `json:"id"`
	Alias       string `json:"alias"`               // Unique identifier, e.g. "dev-node-1"
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
		if found && !strings.EqualFold(match.Alias, node.Alias) {
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
	if a.sshClusters == nil {
		a.sshClusters = make(map[string]SSHServerNode, len(store.Servers))
	}
	for _, s := range store.Servers {
		s = normalizeSSHServerNode(s)
		alias := strings.ToLower(strings.TrimSpace(s.Alias))
		if alias != "" {
			a.sshClusters[alias] = s
		}
	}

	a.sshWorkspaceAllowedMu.Lock()
	if a.sshWorkspaceAllowed == nil {
		a.sshWorkspaceAllowed = make(map[string]map[string]bool)
	}
	for ws, aliases := range store.WorkspaceAllowed {
		cleanWS := normalizeWorkspaceKey(ws)
		if cleanWS == "" {
			continue
		}
		if a.sshWorkspaceAllowed[cleanWS] == nil {
			a.sshWorkspaceAllowed[cleanWS] = make(map[string]bool, len(aliases))
		}
		for _, alias := range aliases {
			clean := strings.ToLower(strings.TrimSpace(alias))
			if clean != "" {
				a.sshWorkspaceAllowed[cleanWS][clean] = true
			}
		}
	}
	a.sshWorkspaceAllowedMu.Unlock()

	return nil
}

// saveSSHClustersLocked writes the in-memory cluster map to disk atomically.
// Must be called with a.sshClustersMu held.
func (a *App) saveSSHClustersLocked() error {
	path := a.sshClustersFilePath()
	if path == "" {
		return nil
	}
	servers := make([]SSHServerNode, 0, len(a.sshClusters))
	for _, s := range a.sshClusters {
		servers = append(servers, s)
	}

	a.sshWorkspaceAllowedMu.RLock()
	wsAllowed := make(map[string][]string, len(a.sshWorkspaceAllowed))
	for ws, m := range a.sshWorkspaceAllowed {
		cleanWS := normalizeWorkspaceKey(ws)
		if cleanWS == "" {
			continue
		}
		var list []string
		for alias, allowed := range m {
			if allowed {
				list = append(list, alias)
			}
		}
		if len(list) > 0 {
			wsAllowed[cleanWS] = list
		}
	}
	a.sshWorkspaceAllowedMu.RUnlock()

	store := sshClusterStore{
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

// ListAllSSHServers returns all configured servers (for UI settings).
func (a *App) ListAllSSHServers() []SSHServerNode {
	a.sshClustersMu.RLock()
	defer a.sshClustersMu.RUnlock()
	servers := make([]SSHServerNode, 0, len(a.sshClusters))
	for _, s := range a.sshClusters {
		servers = append(servers, s)
	}
	return servers
}

// ListAuthorizedSSHServers returns clean summaries of servers authorized for
// the given workspace. Sensitive secrets are scrubbed.
func (a *App) ListAuthorizedSSHServers(workspace string) []SSHServerSummary {
	a.sshClustersMu.RLock()
	defer a.sshClustersMu.RUnlock()

	normWS := normalizeWorkspaceKey(workspace)
	a.sshWorkspaceAllowedMu.RLock()
	allowedMap := a.sshWorkspaceAllowed[normWS]
	a.sshWorkspaceAllowedMu.RUnlock()

	res := make([]SSHServerSummary, 0, len(a.sshClusters))
	for alias, s := range a.sshClusters {
		if allowedMap != nil && !allowedMap[alias] {
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
	return res
}

// GetSSHServer looks up a server node by alias (case-insensitive).
func (a *App) GetSSHServer(alias string) (SSHServerNode, bool) {
	a.sshClustersMu.RLock()
	defer a.sshClustersMu.RUnlock()
	node, ok := a.sshClusters[strings.ToLower(strings.TrimSpace(alias))]
	return node, ok
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
	node = normalizeSSHServerNode(node)
	if node.Port <= 0 {
		node.Port = 22
	}
	if node.ID == "" {
		node.ID = newID()
	}
	now := time.Now().UnixMilli()
	if node.CreatedAtMS == 0 {
		node.CreatedAtMS = now
	}
	node.UpdatedAtMS = now

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
	// 节点是凭据的唯一来源：更新前后两套端点都要丢掉缓存槽，否则用户刚清掉的
	// 密码/口令会继续用到 TTL 到期（见 orch_ssh_credential.go 的 purgeHost）。
	if previous, ok := a.sshClusters[alias]; ok {
		a.sshCredentials.purgeHost(nodeSSHHost(previous))
	}
	a.sshClusters[alias] = node
	a.sshCredentials.purgeHost(nodeSSHHost(node))
	return a.saveSSHClustersLocked()
}

// emitSSHClustersChanged 通知 UI 集群清单/工作区授权变了（模型也能改这份清单），
// 让输入框上方的授权浮层不必等到切 Tab 才刷新。刻意在锁外发：emit 会回到 WebView，
// 持锁发事件就把前端回调锁在门外了。
func (a *App) emitSSHClustersChanged() {
	a.emit("ssh:clusters-changed", map[string]any{})
}

// DeleteSSHServer removes a server node by alias.
func (a *App) DeleteSSHServer(alias string) error {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if err := a.deleteSSHServerLocked(alias); err != nil {
		return err
	}
	a.emitSSHClustersChanged()
	return nil
}

// deleteSSHServerLocked 删节点并失效它的凭据槽位。调用方不得持有锁。
func (a *App) deleteSSHServerLocked(alias string) error {
	a.sshClustersMu.Lock()
	defer a.sshClustersMu.Unlock()
	if a.sshClusters == nil {
		return nil
	}
	if previous, ok := a.sshClusters[alias]; ok {
		a.sshCredentials.purgeHost(nodeSSHHost(previous))
	}
	delete(a.sshClusters, alias)
	return a.saveSSHClustersLocked()
}

// AuthorizeServerForWorkspace grants a workspace access to a server alias.
func (a *App) AuthorizeServerForWorkspace(workspace, alias string) {
	normWS := normalizeWorkspaceKey(workspace)
	alias = strings.ToLower(strings.TrimSpace(alias))
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

	a.sshWorkspaceAllowedMu.Lock()
	if a.sshWorkspaceAllowed == nil {
		a.sshWorkspaceAllowed = make(map[string]map[string]bool)
	}
	if a.sshWorkspaceAllowed[normWS] == nil {
		a.sshWorkspaceAllowed[normWS] = make(map[string]bool)
	}
	a.sshWorkspaceAllowed[normWS][alias] = true
	a.sshWorkspaceAllowedMu.Unlock()

	_ = a.saveSSHClustersLocked()
}

// SetWorkspaceAllowedServers updates the full list of allowed server aliases for a workspace.
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

	a.sshWorkspaceAllowedMu.Lock()
	if a.sshWorkspaceAllowed == nil {
		a.sshWorkspaceAllowed = make(map[string]map[string]bool)
	}
	m := make(map[string]bool, len(aliases))
	for _, alias := range aliases {
		clean := strings.ToLower(strings.TrimSpace(alias))
		if clean != "" {
			m[clean] = true
		}
	}
	a.sshWorkspaceAllowed[normWS] = m
	a.sshWorkspaceAllowedMu.Unlock()

	return a.saveSSHClustersLocked()
}

// GetWorkspaceAllowedServers returns the list of server aliases allowed for a workspace.
func (a *App) GetWorkspaceAllowedServers(workspace string) []string {
	normWS := normalizeWorkspaceKey(workspace)
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
	for alias, allowed := range m {
		if allowed {
			res = append(res, alias)
		}
	}
	return res
}

// IsServerAuthorizedForWorkspace checks if a server alias is allowed in the workspace.
func (a *App) IsServerAuthorizedForWorkspace(workspace, alias string) bool {
	normWS := normalizeWorkspaceKey(workspace)
	alias = strings.ToLower(strings.TrimSpace(alias))
	a.sshWorkspaceAllowedMu.RLock()
	defer a.sshWorkspaceAllowedMu.RUnlock()
	if a.sshWorkspaceAllowed == nil {
		return false
	}
	m := a.sshWorkspaceAllowed[normWS]
	if m == nil {
		return false
	}
	return m[alias]
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
