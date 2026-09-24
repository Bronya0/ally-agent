// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSSHClusterPersistenceAndScoping verifies that:
// 1. Cluster nodes are persisted to and reloaded from disk in a sandbox.
// 2. Sensitive fields (Password, KeyPath) are scrubbed from model-facing summaries.
// 3. Tab/Workspace scoping enforces Default Deny (unauthorized servers are omitted).
func TestSSHClusterPersistenceAndScoping(t *testing.T) {
	tempDir := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")

	// 1. Add servers
	s1 := SSHServerNode{
		Alias:       "dev-api",
		Host:        "192.168.1.101",
		Port:        22,
		Username:    "deploy",
		Password:    "supersecretpassword",
		KeyPath:     "/keys/id_rsa",
		Description: "Dev API server",
	}
	s2 := SSHServerNode{
		Alias:       "prod-db",
		Host:        "10.0.0.50",
		Port:        3306,
		Username:    "root",
		Password:    "prodpassword",
		Description: "Production database node",
	}

	// Verify empty description is rejected
	sEmptyDesc := SSHServerNode{
		Alias:    "no-desc",
		Host:     "1.2.3.4",
		Username: "root",
	}
	if err := app.SaveSSHServer(sEmptyDesc); err == nil {
		t.Fatal("expected error when saving server without description")
	}

	if err := app.SaveSSHServer(s1); err != nil {
		t.Fatalf("SaveSSHServer s1 failed: %v", err)
	}
	if err := app.SaveSSHServer(s2); err != nil {
		t.Fatalf("SaveSSHServer s2 failed: %v", err)
	}

	// 2. Reload into fresh app instance to verify persistence
	app2 := NewApp()
	app2.configPath = app.configPath
	if err := app2.loadSSHClusters(); err != nil {
		t.Fatalf("loadSSHClusters failed: %v", err)
	}

	all := app2.ListAllSSHServers()
	if len(all) != 2 {
		t.Fatalf("expected 2 servers persisted, got %d", len(all))
	}

	// 3. Verify Default Deny: without authorization, workspace sees no servers
	ws1 := filepath.Join(tempDir, "workspace1")
	summaries := app2.ListAuthorizedSSHServers(ws1)
	if len(summaries) != 0 {
		t.Fatalf("expected 0 servers authorized for ws1 by default, got %d", len(summaries))
	}
	if app2.IsServerAuthorizedForWorkspace(ws1, "dev-api") {
		t.Fatal("dev-api should not be authorized for ws1 before grant")
	}

	// 4. Authorize dev-api for ws1
	app2.AuthorizeServerForWorkspace(ws1, "dev-api")
	if !app2.IsServerAuthorizedForWorkspace(ws1, "dev-api") {
		t.Fatal("dev-api should be authorized for ws1 after grant")
	}
	if app2.IsServerAuthorizedForWorkspace(ws1, "prod-db") {
		t.Fatal("prod-db should remain unauthorized for ws1")
	}

	// 5. Verify model-facing summary scrubbed secrets
	summaries = app2.ListAuthorizedSSHServers(ws1)
	if len(summaries) != 1 || summaries[0].Alias != "dev-api" {
		t.Fatalf("expected only dev-api authorized, got %+v", summaries)
	}
	// Verify sensitive fields cannot leak
	if summaries[0].Host != "192.168.1.101" || summaries[0].Username != "deploy" || summaries[0].Description != "Dev API server" {
		t.Fatalf("unexpected summary: %+v", summaries[0])
	}

	// 5b. Verify workspace allowed servers reload on fresh app instance
	app3 := NewApp()
	app3.configPath = app.configPath
	if err := app3.loadSSHClusters(); err != nil {
		t.Fatalf("loadSSHClusters app3 failed: %v", err)
	}
	if !app3.IsServerAuthorizedForWorkspace(ws1, "dev-api") {
		t.Fatal("dev-api should remain authorized for ws1 after reload")
	}
	if app3.IsServerAuthorizedForWorkspace(ws1, "prod-db") {
		t.Fatal("prod-db should remain unauthorized for ws1 after reload")
	}

	// 6. Delete server
	if err := app2.DeleteSSHServer("dev-api"); err != nil {
		t.Fatalf("DeleteSSHServer failed: %v", err)
	}
	if _, ok := app2.GetSSHServer("dev-api"); ok {
		t.Fatal("dev-api should be deleted")
	}
}

// TestSSHSessionApprovalLifecycle tests session-level connection approval.
func TestSSHSessionApprovalLifecycle(t *testing.T) {
	app := NewApp()
	sessionID := "sess-123"
	alias := "dev-box"

	if app.IsServerConnectionApproved(sessionID, alias) {
		t.Fatal("connection should not be approved initially")
	}

	app.ApproveServerConnection(sessionID, alias)
	if !app.IsServerConnectionApproved(sessionID, alias) {
		t.Fatal("connection should be approved after approval")
	}

	// Different session must not inherit approval
	if app.IsServerConnectionApproved("sess-other", alias) {
		t.Fatal("approval must be scoped to sessionID")
	}
}

func TestNormalizeSSHAuthType(t *testing.T) {
	cases := []struct {
		name string
		node SSHServerNode
		want string
	}{
		{"explicit key uses passphrase semantics", SSHServerNode{AuthType: sshAuthTypeKey, Password: "key-passphrase", KeyPath: "id_ed25519"}, sshAuthTypeKey},
		{"explicit password stays password", SSHServerNode{AuthType: sshAuthTypePassword, Password: "account-password"}, sshAuthTypePassword},
		{"legacy key inferred", SSHServerNode{Password: "key-passphrase", KeyPath: "id_ed25519"}, sshAuthTypeKey},
		{"legacy password inferred", SSHServerNode{Password: "account-password"}, sshAuthTypePassword},
		{"empty credentials use agent", SSHServerNode{}, sshAuthTypeAgent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSSHAuthType(tc.node); got != tc.want {
				t.Fatalf("normalizeSSHAuthType() = %q, want %q", got, tc.want)
			}
		})
	}

	tempDir := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")
	if err := app.SaveSSHServer(SSHServerNode{Alias: "legacy", Host: "127.0.0.1", Username: "user", Password: "pw", Description: "test"}); err != nil {
		t.Fatalf("SaveSSHServer: %v", err)
	}
	stored, ok := app.GetSSHServer("legacy")
	if !ok || stored.AuthType != sshAuthTypePassword {
		t.Fatalf("saved node authType = %q, ok=%v; want password mode", stored.AuthType, ok)
	}

	agentNode := normalizeSSHServerNode(SSHServerNode{AuthType: sshAuthTypeAgent, Password: "stale-secret", KeyPath: "stale-key"})
	if agentNode.Password != "" || agentNode.KeyPath != "" {
		t.Fatalf("agent mode retained hidden credentials: %+v", agentNode)
	}
}

func TestLoadSSHClustersInfersLegacyKeyMode(t *testing.T) {
	tempDir := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")
	legacy := `{"servers":[{"id":"legacy-id","alias":"legacy-key","host":"127.0.0.1","username":"user","keyPath":"id_ed25519","password":"key-passphrase","description":"test"},{"id":"agent-id","alias":"stale-agent","host":"127.0.0.2","username":"user","authType":"agent","keyPath":"stale-key","password":"stale-secret","description":"test"}]}`
	if err := os.WriteFile(app.sshClustersFilePath(), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy cluster file: %v", err)
	}
	if err := app.loadSSHClusters(); err != nil {
		t.Fatalf("load legacy clusters: %v", err)
	}
	node, ok := app.GetSSHServer("legacy-key")
	if !ok || node.AuthType != sshAuthTypeKey {
		t.Fatalf("loaded authType = %q, ok=%v; want key mode", node.AuthType, ok)
	}
	agentNode, ok := app.GetSSHServer("stale-agent")
	if !ok || agentNode.AuthType != sshAuthTypeAgent || agentNode.Password != "" || agentNode.KeyPath != "" {
		t.Fatalf("loaded agent node retained hidden credentials: %+v, ok=%v", agentNode, ok)
	}
}

func TestTestSSHServerValidationAndLookup(t *testing.T) {
	tempDir := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")

	// 1. Empty host and empty alias
	res := app.TestSSHServer(SSHServerNode{})
	if res.OK || res.Error == "" {
		t.Fatalf("expected error for empty node, got ok=%v, err=%q", res.OK, res.Error)
	}

	// 2. Unknown alias
	res = app.TestSSHServer(SSHServerNode{Alias: "unknown-alias"})
	if res.OK || !strings.Contains(res.Error, "not found") {
		t.Fatalf("expected 'not found' error, got %q", res.Error)
	}

	// 3. Saved server resolved by alias (pointing to a closed port on 127.0.0.1)
	if err := app.SaveSSHServer(SSHServerNode{
		Alias:       "local-closed",
		Host:        "127.0.0.1",
		Port:        59999,
		Username:    "testuser",
		Description: "test closed port",
		AuthType:    "password",
		Password:    "testpw",
	}); err != nil {
		t.Fatalf("SaveSSHServer: %v", err)
	}

	res = app.TestSSHServer(SSHServerNode{Alias: "local-closed"})
	if res.OK {
		t.Fatalf("expected connection failure for closed port, got ok=true")
	}
	if strings.Contains(res.Error, "password authentication requires") {
		t.Fatalf("unexpected password authentication parameter error: %q", res.Error)
	}

	// 4. Password auth with empty password
	resEmptyPw := app.TestSSHServer(SSHServerNode{
		Host:     "127.0.0.1",
		AuthType: "password",
	})
	if resEmptyPw.OK || !strings.Contains(resEmptyPw.Error, "密码认证需要填写密码") {
		t.Fatalf("expected empty password error, got %q", resEmptyPw.Error)
	}

	// 5. Key auth with empty key path
	resEmptyKey := app.TestSSHServer(SSHServerNode{
		Host:     "127.0.0.1",
		AuthType: "key",
	})
	if resEmptyKey.OK || !strings.Contains(resEmptyKey.Error, "密钥认证需要指定私钥路径") {
		t.Fatalf("expected empty key path error, got %q", resEmptyKey.Error)
	}
}
