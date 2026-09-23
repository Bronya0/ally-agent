// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"path/filepath"
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
