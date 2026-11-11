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
	"path/filepath"
	"testing"
	"time"
)

func TestSSHClusterListAndRemovedAdd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	app := NewApp()
	app.configPath = filepath.Join(dir, "config.json")
	ws := filepath.Join(dir, "workspace")
	if err := app.SaveSSHServer(SSHServerNode{Alias: "node", Host: "192.0.2.1", Username: "root", Description: "test"}); err != nil {
		t.Fatal(err)
	}
	result, err := app.executeSSHClusterTool(ws, SSHClusterRequest{Action: "list"})
	if err != nil || result["count"] != 0 {
		t.Fatalf("unapproved listing: %v, %v", result, err)
	}
	if err := app.AuthorizeServerForWorkspace(ws, "node"); err != nil {
		t.Fatal(err)
	}
	result, err = app.executeSSHClusterTool(ws, SSHClusterRequest{Action: "list"})
	if err != nil || result["count"] != 1 {
		t.Fatalf("approved listing: %v, %v", result, err)
	}
	if _, err := app.executeSSHClusterTool(ws, SSHClusterRequest{Action: "add"}); err == nil {
		t.Fatal("removed add action was accepted")
	}
	if len(app.ListAllSSHServers()) != 1 || pendingAskCount(app) != 0 {
		t.Fatal("removed add action changed the inventory or requested approval")
	}
}

func TestRemoteTargetWorkspaceAuthorization(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)
	t.Setenv("USERPROFILE", tempDir)
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")
	ws := filepath.Join(tempDir, "test-ws")
	sessionID := "sess-remote-gate"

	// Register a server in cluster
	s := SSHServerNode{
		Alias:       "my-srv",
		Host:        "192.168.1.88",
		Port:        22,
		Username:    "ubuntu",
		Description: "Ubuntu test server",
	}
	if err := app.SaveSSHServer(s); err != nil {
		t.Fatal(err)
	}

	app.mu.Lock()
	app.sessionWorkspaces[sessionID] = ws
	app.mu.Unlock()

	ctx := context.WithValue(context.Background(), toolExecutionMetaContextKey{}, toolExecutionMeta{
		sessionID: sessionID,
	})

	// Without workspace authorization, resolving the alias must fail.
	_, err := app.resolveAndAuthorizeRemoteTarget(ctx, "my-srv:/var/www")
	if err == nil {
		t.Fatal("expected E_SERVER_UNAUTHORIZED for un-whitelisted server")
	}

	// Workspace authorization is sufficient for a registered alias; no second ask.
	app.AuthorizeServerForWorkspace(ws, "my-srv")
	connectCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	rt, err := app.resolveAndAuthorizeRemoteTarget(connectCtx, "my-srv:/var/www")
	cancel()
	if err != nil {
		t.Fatalf("expected workspace-authorized alias to connect without an ask: %v", err)
	}
	if rt.Host != "ubuntu@192.168.1.88" || rt.WorkspaceRoot != "/var/www" {
		t.Fatalf("unexpected resolved target: %+v", rt)
	}
	rt, err = app.resolveAndAuthorizeRemoteTarget(ctx, "192.168.1.99:/var/www")
	if err != nil {
		t.Fatalf("direct endpoint should not request approval: %v", err)
	}
	if rt.Host != "192.168.1.99" || rt.WorkspaceRoot != "/var/www" {
		t.Fatalf("unexpected direct target: %+v", rt)
	}
	if pendingAskCount(app) != 0 {
		t.Fatal("remote target resolution created an approval prompt")
	}
}

// TestSSHClusterEndpointLookup 锁定“真实端点也是同一个身份”：命中唯一节点（主机、
// 端口、可选用户名全部一致）才认，歧义或端口不同一律不认，让调用方回到原始目标
// 路径而不是猜一个。
func TestSSHClusterEndpointLookup(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)
	t.Setenv("USERPROFILE", tempDir)
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")

	if err := app.SaveSSHServer(SSHServerNode{Alias: "prod-web", Host: "10.0.0.7", Port: 2222, Username: "deploy", Description: "web"}); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveSSHServer(SSHServerNode{Alias: "prod-web-root", Host: "10.0.0.7", Port: 2222, Username: "root", Description: "web as root"}); err != nil {
		t.Fatal(err)
	}

	if node, ok := app.findSSHServerByEndpoint("deploy@10.0.0.7", "2222"); !ok || node.Alias != "prod-web" {
		t.Fatalf("user@host:port must resolve to its node, got ok=%v node=%+v", ok, node)
	}
	if _, ok := app.findSSHServerByEndpoint("Deploy@10.0.0.7", "2222"); ok {
		t.Fatal("usernames with different case must not resolve to the same account")
	}
	if node, ok := app.findSSHServerByEndpoint("10.0.0.7", "2222"); ok {
		t.Fatalf("two nodes share host:port, the bare spelling must stay ambiguous: %+v", node)
	}
	if _, ok := app.findSSHServerByEndpoint("deploy@10.0.0.7", "22"); ok {
		t.Fatal("a different port is a different machine and must not match")
	}
	if _, ok := app.findSSHServerByEndpoint("deploy@10.0.0.8", "2222"); ok {
		t.Fatal("a different host must not match")
	}
}

// TestRemoteTargetEndpointSpellingUsesRegisteredNode 锁定端点的两种写法等价：
// 用 ssh://user@host:port/path 访问已登记、已授权节点时不弹连接审批，而且该节点的
// 凭据会装进与别名路径同一个缓存槽位（旧实现只认别名，换写法就拿不到密码，
// 失败提示还会把原因归到“密码不对”）。
func TestRemoteTargetEndpointSpellingUsesRegisteredNode(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)
	t.Setenv("USERPROFILE", tempDir)
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")
	ws := filepath.Join(tempDir, "ws")
	sessionID := "sess-endpoint-spelling"

	if err := app.SaveSSHServer(SSHServerNode{Alias: "prod-web", Host: "10.0.0.7", Port: 2222, Username: "deploy", Password: "pw", Description: "web"}); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.sessionWorkspaces[sessionID] = ws
	app.mu.Unlock()
	ctx := context.WithValue(context.Background(), toolExecutionMetaContextKey{}, toolExecutionMeta{sessionID: sessionID})

	// 未授权时按白名单拒绝（而不是降级成“陌生主机、弹一次连接确认”）。
	if _, err := app.resolveAndAuthorizeRemoteTarget(ctx, "ssh://deploy@10.0.0.7:2222/srv/www"); err == nil {
		t.Fatal("an unauthorized registered node must be rejected when addressed by endpoint")
	}

	app.AuthorizeServerForWorkspace(ws, "prod-web")
	rt, err := app.resolveAndAuthorizeRemoteTarget(ctx, "ssh://deploy@10.0.0.7:2222/srv/www")
	if err != nil {
		t.Fatalf("authorized endpoint spelling must resolve without another ask: %v", err)
	}
	if rt.Host != "deploy@10.0.0.7" || rt.Port != "2222" || rt.WorkspaceRoot != "/srv/www" {
		t.Fatalf("unexpected resolved target: %+v", rt)
	}
	if rt.credential == nil || rt.credential.authType != sshAuthTypePassword || rt.credential.password != "pw" {
		t.Fatal("resolved target must carry its own credential")
	}
}

// TestSSHClustersPurgeCredentialOnNodeChange 锁定缓存失效：节点被改写（密码清空）
// 或被删除后，先前装进内存的密码必须立刻失效。旧实现里凭据只进不出——
// 删节点/清密码后旧密码还会继续用到 12h TTL 到期。
func TestSSHClustersPurgeCredentialOnNodeChange(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)
	t.Setenv("USERPROFILE", tempDir)
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")

	node := SSHServerNode{Alias: "n1", Host: "10.1.1.1", Username: "root", Password: "secret", Description: "d"}
	if err := app.SaveSSHServer(node); err != nil {
		t.Fatal(err)
	}
	key := sshCredentialKey("root@10.1.1.1", "")

	app.sshCredentials.store(key, sshAuthTypePassword, "secret", "")
	if _, ok := app.sshCredentials.lookup(key); !ok {
		t.Fatal("precondition: credential should be cached")
	}

	node.Password = ""
	if err := app.SaveSSHServer(node); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.sshCredentials.lookup(key); ok {
		t.Fatal("clearing the password must drop the cached credential")
	}

	app.sshCredentials.store(key, sshAuthTypePassword, "secret", "")
	if err := app.DeleteSSHServer("n1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.sshCredentials.lookup(key); ok {
		t.Fatal("deleting the node must drop the cached credential")
	}
}

// TestSSHCredentialCacheStoreLookup covers the cache lifecycle production uses:
// store, overwrite, same-mode field retention, mode changes, and TTL expiry.
func TestSSHCredentialCacheStoreLookup(t *testing.T) {
	cache := newSSHCredentialCache()
	if _, ok := cache.lookup("root@h1"); ok {
		t.Fatal("empty cache returned a credential")
	}
	cache.store("root@h1", sshAuthTypePassword, "p@ss word", "")
	entry, ok := cache.lookup("root@h1")
	if !ok || entry.password != "p@ss word" || entry.authType != sshAuthTypePassword {
		t.Fatalf("lookup after store: ok=%v entry=%+v", ok, entry)
	}
	cache.store("root@h1", sshAuthTypePassword, "second", "")
	if entry, _ := cache.lookup("root@h1"); entry.password != "second" {
		t.Fatalf("overwrite failed: %+v", entry)
	}

	cache.store("root@h1", sshAuthTypeKey, "key-passphrase", "")
	cache.store("root@h1", sshAuthTypeKey, "", "/keys/id_test.pem")
	entry, ok = cache.lookup("root@h1")
	if !ok || entry.authType != sshAuthTypeKey || entry.password != "key-passphrase" || entry.keyPath != "/keys/id_test.pem" {
		t.Fatalf("same-mode key fields were not retained: ok=%v entry=%+v", ok, entry)
	}

	cache.store("root@h1", sshAuthTypePassword, "account-password", "")
	entry, ok = cache.lookup("root@h1")
	if !ok || entry.authType != sshAuthTypePassword || entry.password != "account-password" || entry.keyPath != "" {
		t.Fatalf("mode change retained an incompatible credential: ok=%v entry=%+v", ok, entry)
	}

	cache.mu.Lock()
	cache.items["root@h1"] = sshCredentialEntry{password: "stale", expiresAt: time.Now().Add(-time.Minute)}
	cache.mu.Unlock()
	if _, ok := cache.lookup("root@h1"); ok {
		t.Fatal("expired credential must not be returned")
	}
	cache.mu.Lock()
	remaining := len(cache.items)
	cache.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expired entry should be evicted, cache still holds %d", remaining)
	}
}

// TestSSHCredentialKey keeps usernames case-sensitive, folds host case and
// includes the effective port (empty means 22).
func TestSSHCredentialKey(t *testing.T) {
	cases := []struct {
		host, port, want string
	}{
		{"root@47.1.2.3", "", "root@47.1.2.3:22"},
		{"Deploy@Example.COM", "2222", "Deploy@example.com:2222"},
		{" user@host ", " 2222 ", "user@host:2222"},
	}
	for _, tc := range cases {
		if got := sshCredentialKey(tc.host, tc.port); got != tc.want {
			t.Errorf("sshCredentialKey(%q, %q) = %q; want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

// TestSSHCredentialPurgeHost covers the invalidation production relies on:
// editing or deleting a cluster node must drop every port slot of that endpoint
// so a password the user removed cannot keep authenticating until the TTL
// expires, while neighbouring hosts (and prefixes of them) stay untouched.
func TestSSHCredentialPurgeHost(t *testing.T) {
	cache := newSSHCredentialCache()
	cache.store(sshCredentialKey("root@h1", ""), sshAuthTypePassword, "pw", "")
	cache.store(sshCredentialKey("root@h1", "2222"), sshAuthTypePassword, "pw2222", "")
	cache.store(sshCredentialKey("root@h10", ""), sshAuthTypePassword, "neighbour", "")
	cache.store(sshCredentialKey("Root@h1", ""), sshAuthTypePassword, "other-user", "")

	cache.purgeHost("root@h1")
	if _, ok := cache.lookup(sshCredentialKey("root@h1", "")); ok {
		t.Error("purged host still returns a credential on the default port")
	}
	if _, ok := cache.lookup(sshCredentialKey("root@h1", "2222")); ok {
		t.Error("purged host still returns a credential on the nonstandard port")
	}
	if _, ok := cache.lookup(sshCredentialKey("root@h10", "")); !ok {
		t.Error("a host whose name merely starts with the purged name must survive")
	}
	if entry, ok := cache.lookup(sshCredentialKey("Root@H1", "")); !ok || entry.password != "other-user" {
		t.Error("purging one username must not purge another account with different case")
	}
}

// TestNormalizeSSHCredentialKey 已被 TestSSHCredentialKey 取代（键现在由解析后的
// 端点得出，不再从原始 target 字符串里取主机段）。

func TestResolvedSSHCredentialsAreIsolated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	a := NewApp()
	a.configPath = filepath.Join(dir, "config.json")
	ws := filepath.Join(dir, "workspace")
	for _, n := range []SSHServerNode{
		{Alias: "password-node", Host: "example.test", Username: "root", Port: 22, Description: "test", AuthType: "password", Password: "secret"},
		{Alias: "agent-node", Host: "example.test", Username: "root", Port: 22, Description: "test", AuthType: "agent"},
	} {
		if err := a.SaveSSHServer(n); err != nil {
			t.Fatal(err)
		}
		if err := a.AuthorizeServerForWorkspace(ws, n.Alias); err != nil {
			t.Fatal(err)
		}
	}
	ctx := withToolWorkspace(context.Background(), ws)
	password, err := a.resolveAndAuthorizeRemoteTarget(ctx, "password-node:/srv/test")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := a.resolveAndAuthorizeRemoteTarget(ctx, "ssh://agent-node:2222/srv/test")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Port != "22" || agent.credential == nil || agent.credential.authType != sshAuthTypeAgent || agent.credential.password != "" {
		t.Fatalf("incorrect agent target: %+v", agent)
	}
	if password.credential == nil || password.credential.password != "secret" {
		t.Fatal("password credential was overwritten")
	}
}
