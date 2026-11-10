// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// SSH 集群与凭据一个文件：集群清单落盘、别名/端点解析、工作区授权，以及从清单读出的
// 内存凭据缓存（TTL 到期即丢；密钥口令只解本地密钥，绝不回用为账号密码）。

import (
	"errors"
	"strings"
	"sync"
	"time"

	"ally-dev/internal/tools/sshclient"
)

// remoteSSHConfig is the shared credential-to-transport boundary for tools and connection tests.
func (a *App) remoteSSHConfig(rt remoteTarget, entry sshCredentialEntry) sshclient.Config {
	cfg := sshclient.Config{
		Host: rt.Host, Port: rt.Port, KnownHostsPath: a.sshKnownHostsPath,
		AuthMode: sshclient.AuthModeAgent,
	}
	switch entry.authType {
	case sshAuthTypeKey:
		cfg.AuthMode = sshclient.AuthModeKey
		cfg.KeyPath = entry.keyPath
		cfg.KeyPassphrase = entry.password
	case sshAuthTypePassword:
		cfg.AuthMode = sshclient.AuthModePassword
		cfg.Password = entry.password
	}
	return cfg
}

// SSHClusterRequest selects the workspace server listing.
type SSHClusterRequest struct {
	Action string `json:"action"`
}

func (a *App) executeSSHClusterTool(workspace string, req SSHClusterRequest) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "" && action != "list" {
		return nil, codedToolError("E_BAD_SSH_CLUSTER", errors.New("action must be 'list'; add servers in the SSH cluster manager and authorize them in the workspace footer"))
	}
	servers := a.ListAuthorizedSSHServers(workspace)
	return map[string]any{"ok": true, "workspace": workspace, "count": len(servers), "servers": servers}, nil
}

// In-memory SSH credentials loaded from the local cluster inventory. Secret
// fields are never exposed to model-facing server summaries; remote operations
// pass them directly to the internal Go SSH client.

const sshCredentialTTL = 12 * time.Hour

type sshCredentialEntry struct {
	authType  string
	password  string
	keyPath   string
	expiresAt time.Time
}

type sshCredentialCache struct {
	mu    sync.Mutex
	items map[string]sshCredentialEntry
}

func newSSHCredentialCache() *sshCredentialCache {
	return &sshCredentialCache{items: make(map[string]sshCredentialEntry)}
}

// sshCredentialKey reduces a **resolved** ssh endpoint to the cache slot it
// uses: case-sensitive user, lowercase host and effective port (empty means 22). Callers
// pass the resolved host/port, never the raw target string — the raw target may
// name a cluster alias, and keying on it would put the credential in a slot
// nothing else can find. host:2222 and host:22 are different machines
// (container port maps, forwarded services) and keep separate slots, while
// every workspace root on one endpoint shares a slot.
func sshCredentialKey(host, port string) string {
	port = strings.TrimSpace(port)
	if port == "" {
		port = "22"
	}
	user, hostname := splitSSHUserHost(host)
	if user != "" {
		hostname = user + "@" + hostname
	}
	return hostname + ":" + port
}

// purgeHost drops every slot of a resolved endpoint host (any port). Node edits
// and deletions call it so a password the user removed cannot keep
// authenticating until the TTL expires.
func (c *sshCredentialCache) purgeHost(host string) {
	if c == nil {
		return
	}
	host = strings.TrimSuffix(sshCredentialKey(host, ""), ":22")
	if host == "" {
		return
	}
	prefix := host + ":"
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.items {
		if strings.HasPrefix(key, prefix) {
			delete(c.items, key)
		}
	}
}

// store records one authentication mode for the resolved endpoint. Empty
// fields only inherit an unexpired credential when its mode is unchanged.
func (c *sshCredentialCache) store(key, authType, password, keyPath string) {
	authType = normalizeSSHAuthType(SSHServerNode{AuthType: authType, Password: password, KeyPath: keyPath})
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if entry, ok := c.items[key]; ok && entry.authType == authType && now.Before(entry.expiresAt) {
		if password == "" {
			password = entry.password
		}
		if keyPath == "" {
			keyPath = entry.keyPath
		}
	}
	c.items[key] = sshCredentialEntry{authType: authType, password: password, keyPath: keyPath, expiresAt: now.Add(sshCredentialTTL)}
}

// lookup returns the live credential entry for key, if any.
func (c *sshCredentialCache) lookup(key string) (sshCredentialEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return sshCredentialEntry{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.items, key)
		return sshCredentialEntry{}, false
	}
	return entry, true
}
