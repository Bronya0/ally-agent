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

// waitForAskID polls app.pendingAsks until an ask request is registered.
func waitForAskID(t *testing.T, app *App) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		app.askMu.Lock()
		for id := range app.pendingAsks {
			app.askMu.Unlock()
			return id
		}
		app.askMu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for ask registration")
	return ""
}

// TestSSHClusterToolListAndAddApproval tests:
// 1. action=list returns authorized servers for the workspace.
// 2. action=add triggers Approval Gate 1:
//   - Rejection fails with E_APPROVAL_REJECTED.
//   - Approval successfully registers and authorizes the server.
func TestSSHClusterToolListAndAddApproval(t *testing.T) {
	tempDir := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")
	ws := filepath.Join(tempDir, "test-workspace")
	sessionID := "sess-cluster-test"

	// 1. Initial list should be empty
	ctx := context.WithValue(context.Background(), toolExecutionMetaContextKey{}, toolExecutionMeta{
		sessionID: sessionID,
	})
	listRes, err := app.executeSSHClusterTool(ctx, sessionID, ws, SSHClusterRequest{Action: "list"})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if listRes["count"].(int) != 0 {
		t.Fatalf("expected 0 servers initially, got %v", listRes["count"])
	}

	// 1b. action=add without description should fail immediately
	_, errNoDesc := app.executeSSHClusterTool(ctx, sessionID, ws, SSHClusterRequest{
		Action:   "add",
		Alias:    "dev-api",
		Host:     "192.168.1.50",
		Port:     22,
		Username: "root",
		Reason:   "test server",
	})
	if errNoDesc == nil {
		t.Fatal("expected error for action=add without description, got nil")
	}

	// 2. Test action=add with REJECTION
	type resultPair struct {
		data map[string]any
		err  error
	}
	doneReject := make(chan resultPair, 1)
	go func() {
		res, err := app.executeSSHClusterTool(ctx, sessionID, ws, SSHClusterRequest{
			Action:      "add",
			Alias:       "dev-api",
			Host:        "192.168.1.50",
			Port:        22,
			Username:    "root",
			Description: "API dev server",
			Reason:      "test server",
		})
		doneReject <- resultPair{res, err}
	}()

	askID1 := waitForAskID(t, app)
	if err := app.SubmitAskResponse(AskSubmitRequest{
		AskID:     askID1,
		SessionID: sessionID,
		Answers: []AskSubmittedAnswer{
			{QuestionID: "approve_cluster_add", SelectedOptionIDs: []string{"reject"}},
		},
	}); err != nil {
		t.Fatalf("SubmitAskResponse reject failed: %v", err)
	}

	resReject := <-doneReject
	if resReject.err == nil {
		t.Fatal("expected error on rejected server add, got nil")
	}

	// Server must NOT be registered
	if _, ok := app.GetSSHServer("dev-api"); ok {
		t.Fatal("dev-api should not be registered after rejection")
	}

	// 3. Test action=add with APPROVAL
	doneApprove := make(chan resultPair, 1)
	go func() {
		res, err := app.executeSSHClusterTool(ctx, sessionID, ws, SSHClusterRequest{
			Action:      "add",
			Alias:       "dev-api",
			Host:        "192.168.1.50",
			Port:        22,
			Username:    "root",
			Description: "API dev server",
			Reason:      "test server",
		})
		doneApprove <- resultPair{res, err}
	}()

	askID2 := waitForAskID(t, app)
	if err := app.SubmitAskResponse(AskSubmitRequest{
		AskID:     askID2,
		SessionID: sessionID,
		Answers: []AskSubmittedAnswer{
			{QuestionID: "approve_cluster_add", SelectedOptionIDs: []string{"approve"}},
		},
	}); err != nil {
		t.Fatalf("SubmitAskResponse approve failed: %v", err)
	}

	resApprove := <-doneApprove
	if resApprove.err != nil {
		t.Fatalf("expected success on approved server add, got: %v", resApprove.err)
	}
	if !resApprove.data["ok"].(bool) {
		t.Fatalf("expected ok=true, got %v", resApprove.data)
	}

	// Server must be registered and authorized in workspace
	if _, ok := app.GetSSHServer("dev-api"); !ok {
		t.Fatal("dev-api should be registered in cluster after approval")
	}
	if !app.IsServerAuthorizedForWorkspace(ws, "dev-api") {
		t.Fatal("dev-api should be authorized for workspace after approval")
	}

	// 4. Verify list now contains dev-api
	listRes2, err := app.executeSSHClusterTool(ctx, sessionID, ws, SSHClusterRequest{Action: "list"})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if listRes2["count"].(int) != 1 {
		t.Fatalf("expected 1 server authorized, got %v", listRes2["count"])
	}
}

// TestRemoteTargetTabAuthorizationAndConnectionApproval tests:
// 1. Calling remote target with unauthorized server alias fails with E_SERVER_UNAUTHORIZED.
// 2. A workspace-authorized server alias connects without another session prompt.
// 3. A direct, unregistered SSH target still prompts on first connection.
func TestRemoteTargetTabAuthorizationAndConnectionApproval(t *testing.T) {
	tempDir := t.TempDir()
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
	if app.IsServerConnectionApproved(sessionID, "my-srv") {
		t.Fatal("workspace authorization should not create session-level approval state")
	}

	// A direct, unregistered host has no workspace alias authorization and keeps
	// the first-connect approval prompt.
	type resolveResult struct {
		rt  remoteTarget
		err error
	}
	doneDirect := make(chan resolveResult, 1)
	go func() {
		rt, err := app.resolveAndAuthorizeRemoteTarget(ctx, "192.168.1.99:/var/www")
		doneDirect <- resolveResult{rt, err}
	}()

	askID := waitForAskID(t, app)
	if err := app.SubmitAskResponse(AskSubmitRequest{
		AskID:     askID,
		SessionID: sessionID,
		Answers: []AskSubmittedAnswer{
			{QuestionID: "approve_remote_connect", SelectedOptionIDs: []string{"allow_session"}},
		},
	}); err != nil {
		t.Fatalf("SubmitAskResponse approve connection failed: %v", err)
	}

	res := <-doneDirect
	if res.err != nil {
		t.Fatalf("expected direct-target approval success, got: %v", res.err)
	}
	if res.rt.Host != "192.168.1.99" || res.rt.WorkspaceRoot != "/var/www" {
		t.Fatalf("unexpected direct target: %+v", res.rt)
	}
}

// TestDestructiveCommandDetection 锁定审批闸门的判据来自 shell 解析 + 共享风险表，
// 而不是子串匹配：旧实现里 `git add .`（含子串 "dd "）、`echo confirm this`（含
// 子串 "rm "）与任何 `>` 重定向都会被误判成高危，真正危险的调子反而被弹窗洪水淹没。
// 越界的 `>` 重定向不在闸门里拦：远端 helper 已按 workspaceRoot 做字面写入目标校验。
func TestDestructiveCommandDetection(t *testing.T) {
	destructive := []string{
		"rm -rf /var/log/*",
		"rm file.txt",
		"sudo rm -rf /srv/app",
		"sh -c 'rm -rf /srv/app'",
		"dd if=/dev/zero of=/dev/sda",
		"mkfs.ext4 /dev/sdb",
		"truncate -s 0 file.log",
		"shred -u /srv/app/secret.txt",
		"shutdown -h now",
	}
	for _, cmd := range destructive {
		if !isDestructiveRemoteCommand(cmd) {
			t.Errorf("expected isDestructiveRemoteCommand(%q) = true", cmd)
		}
	}

	safe := []string{
		"ls -la",
		"cat /etc/os-release",
		"go test ./...",
		"echo hello",
		"grep pattern file.txt",
		"git add .",
		"git add -A",
		"echo confirm this",
		"npm run build > build.log 2>&1",
		"git rm --cached old.txt",
	}
	for _, cmd := range safe {
		if isDestructiveRemoteCommand(cmd) {
			t.Errorf("expected isDestructiveRemoteCommand(%q) = false", cmd)
		}
	}
}

// TestSSHClusterAddExistingAliasOnlyAuthorizes 锁定“已登记别名绝不覆盖”：
// add 命中已有节点时只走授权审批，节点本身（密码/密钥/端口/描述/风险级/来源）
// 原样保留。这是 E_SERVER_UNAUTHORIZED 恢复提示指向的调用，一失手就把用户的
// 凭据抹掉。
func TestSSHClusterAddExistingAliasOnlyAuthorizes(t *testing.T) {
	tempDir := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")
	ws := filepath.Join(tempDir, "ws")
	sessionID := "sess-existing-alias"

	if err := app.SaveSSHServer(SSHServerNode{
		Alias:       "prod-web",
		Host:        "10.0.0.7",
		Port:        2222,
		Username:    "deploy",
		Password:    "keep-me",
		KeyPath:     "/keys/id_prod.pem",
		Description: "生产 Web 前端",
		RiskLevel:   "high",
		CreatedBy:   "user",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), toolExecutionMetaContextKey{}, toolExecutionMeta{sessionID: sessionID})
	type pair struct {
		data map[string]any
		err  error
	}
	done := make(chan pair, 1)
	go func() {
		res, err := app.executeSSHClusterTool(ctx, sessionID, ws, SSHClusterRequest{
			Action:      "add",
			Alias:       "prod-web",
			Host:        "10.0.0.7",
			Username:    "deploy",
			Description: "model supplied description",
			Reason:      "need access",
		})
		done <- pair{res, err}
	}()

	askID := waitForAskID(t, app)
	if err := app.SubmitAskResponse(AskSubmitRequest{
		AskID:     askID,
		SessionID: sessionID,
		Answers:   []AskSubmittedAnswer{{QuestionID: "approve_cluster_authorize", SelectedOptionIDs: []string{"approve"}}},
	}); err != nil {
		t.Fatalf("SubmitAskResponse approve failed: %v", err)
	}
	res := <-done
	if res.err != nil {
		t.Fatalf("authorizing an existing alias failed: %v", res.err)
	}
	if !app.IsServerAuthorizedForWorkspace(ws, "prod-web") {
		t.Fatal("existing node must be authorized for the workspace after approval")
	}

	stored, ok := app.GetSSHServer("prod-web")
	if !ok {
		t.Fatal("node disappeared")
	}
	if stored.Password != "keep-me" || stored.KeyPath != "/keys/id_prod.pem" {
		t.Fatalf("add must not touch stored credentials: %+v", stored)
	}
	if stored.Port != 2222 || stored.RiskLevel != "high" || stored.Description != "生产 Web 前端" || stored.CreatedBy != "user" {
		t.Fatalf("add must not rewrite node identity fields: %+v", stored)
	}
}

// TestSSHClusterEndpointLookup 锁定“真实端点也是同一个身份”：命中唯一节点（主机、
// 端口、可选用户名全部一致）才认，歧义或端口不同一律不认，让调用方回到原始目标
// 路径而不是猜一个。
func TestSSHClusterEndpointLookup(t *testing.T) {
	tempDir := t.TempDir()
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
	if entry, ok := app.sshCredentials.lookup(sshCredentialKey(rt.Host, rt.Port)); !ok || entry.authType != sshAuthTypePassword || entry.password != "pw" {
		t.Fatalf("resolving a registered node must cache its credential in the endpoint slot: ok=%v entry=%+v", ok, entry)
	}
}

// TestSSHClustersPurgeCredentialOnNodeChange 锁定缓存失效：节点被改写（密码清空）
// 或被删除后，先前装进内存的密码必须立刻失效。旧实现里凭据只进不出——
// 删节点/清密码后旧密码还会继续用到 12h TTL 到期。
func TestSSHClustersPurgeCredentialOnNodeChange(t *testing.T) {
	tempDir := t.TempDir()
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
