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
	"os"
	"path/filepath"
	"testing"
)

func newSSHIdentityTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	a := NewApp()
	a.configPath = filepath.Join(dir, "config.json")
	return a
}

func TestSSHIdentityLegacySelectionSurvivesRenameAndReload(t *testing.T) {
	a := newSSHIdentityTestApp(t)
	legacy := `{"servers":[
		{"id":"Old-ID","alias":"Dev","host":"10.0.0.1","username":"root","description":"dev","createdAtMs":123,"createdBy":"user"},
		{"alias":"other","host":"10.0.0.2","username":"root","description":"other"}
	],"workspaceAllowed":{"ws1":[" DEV ","missing"],"ws2":["dev","OTHER"]}}`
	if err := os.WriteFile(a.sshClustersFilePath(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.loadSSHClusters(); err != nil {
		t.Fatal(err)
	}
	node, _ := a.GetSSHServer("dev")
	other, _ := a.GetSSHServer("other")
	if node.ID != "Old-ID" || other.ID == "" || other.ID == node.ID {
		t.Fatalf("legacy identities not preserved/repaired: %q, %q", node.ID, other.ID)
	}
	assertSelection := func(instance *App, ws string, ids ...string) {
		t.Helper()
		got := instance.GetWorkspaceAllowedServers(ws)
		if len(got) != len(ids) {
			t.Fatalf("%s selection = %v, want %v", ws, got, ids)
		}
		for _, id := range ids {
			if !instance.IsServerAuthorizedForWorkspace(ws, id) {
				t.Fatalf("%s lost grant to %s", ws, id)
			}
		}
	}
	assertSelection(a, "ws1", node.ID)
	assertSelection(a, "ws2", node.ID, other.ID)
	// Migration must be durable without requiring a later edit or selection.
	b := NewApp()
	b.configPath = a.configPath
	if err := b.loadSSHClusters(); err != nil {
		t.Fatal(err)
	}
	reloadedOther, _ := b.GetSSHServer("other")
	if reloadedOther.ID != other.ID {
		t.Fatal("generated identity changed on restart")
	}
	assertSelection(b, "ws2", node.ID, other.ID)
	node.Alias, node.Host, node.Port = "Renamed", "10.0.0.9", 2222
	if err := a.SaveSSHServer(node); err != nil {
		t.Fatal(err)
	}
	if _, exists := a.GetSSHServer("dev"); exists || len(a.ListAllSSHServers()) != 2 {
		t.Fatal("rename left a duplicate node under the old nickname")
	}
	assertSelection(a, "ws1", node.ID)
	if !a.IsServerAuthorizedForWorkspace("ws1", " RENAMED ") {
		t.Fatal("legacy alias callers cannot resolve renamed server")
	}
	// Import/old clients still upsert by alias, retaining ID and creation data.
	imported := node
	imported.ID, imported.CreatedAtMS, imported.CreatedBy = "", 0, "agent"
	if err := a.SaveSSHServer(imported); err != nil {
		t.Fatal(err)
	}
	saved, _ := a.GetSSHServer("renamed")
	if saved.ID != node.ID || saved.CreatedAtMS != 123 || saved.CreatedBy != "user" {
		t.Fatal("alias upsert changed server identity or provenance")
	}
	if err := b.loadSSHClusters(); err != nil {
		t.Fatal(err)
	}
	assertSelection(b, "ws1", node.ID)
	assertSelection(b, "ws2", node.ID, other.ID)
	data, err := os.ReadFile(a.sshClustersFilePath())
	if err != nil {
		t.Fatal(err)
	}
	var store sshClusterStore
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	if store.Version != 2 || len(store.WorkspaceAllowed["ws1"]) != 1 || store.WorkspaceAllowed["ws1"][0] != node.ID {
		t.Fatal("persistent grants are not canonical IDs")
	}
	// Reusing a nickname is a new identity, with no inherited selection.
	if err := a.DeleteSSHServer(node.ID); err != nil {
		t.Fatal(err)
	}
	replacement := node
	replacement.ID = ""
	if err := a.SaveSSHServer(replacement); err != nil {
		t.Fatal(err)
	}
	recreated, _ := a.GetSSHServer("renamed")
	if recreated.ID == node.ID || a.IsServerAuthorizedForWorkspace("ws1", recreated.ID) {
		t.Fatal("recreated node inherited old identity/grants")
	}
	if err := a.SaveSSHServer(node); err == nil {
		t.Fatal("stale edit to deleted identity should fail")
	}
	if err := b.loadSSHClusters(); err != nil {
		t.Fatal(err)
	}
	assertSelection(b, "ws1")
	assertSelection(b, "ws2", other.ID)
}

func TestSSHIdentityRenameRejectsCollisionAndRollsBackWriteFailure(t *testing.T) {
	a := newSSHIdentityTestApp(t)
	for _, alias := range []string{"dev", "other"} {
		if err := a.SaveSSHServer(SSHServerNode{Alias: alias, Host: "10.0.0.1", Username: "root", Description: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	node, _ := a.GetSSHServer("dev")
	other, _ := a.GetSSHServer("other")
	a.AuthorizeServerForWorkspace("ws", node.ID)
	node.Alias = " OTHER "
	if err := a.SaveSSHServer(node); err == nil {
		t.Fatal("rename overwrote another server")
	}
	unchanged, _ := a.GetSSHServer("other")
	if unchanged.ID != other.ID || a.IsServerAuthorizedForWorkspace("ws", other.ID) {
		t.Fatal("collision changed another server or its authorization")
	}
	// An unwritable destination is simulated entirely within the temp sandbox.
	blocked := filepath.Join(filepath.Dir(a.configPath), "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.configPath = filepath.Join(blocked, "config.json")
	node.Alias = "new-name"
	if err := a.SaveSSHServer(node); err == nil {
		t.Fatal("expected persistence failure")
	}
	old, ok := a.GetSSHServer("dev")
	if !ok || old.ID != node.ID || !a.IsServerAuthorizedForWorkspace("ws", node.ID) {
		t.Fatal("failed rename changed in-memory identity/grants")
	}
	if _, ok := a.GetSSHServer("new-name"); ok {
		t.Fatal("failed rename leaked into memory")
	}
}

func TestSSHIdentityRepairsDuplicateIDsWithoutTransferringGrants(t *testing.T) {
	for _, version := range []int{0, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			a := newSSHIdentityTestApp(t)
			store := sshClusterStore{
				Version: version,
				Servers: []SSHServerNode{
					{ID: "duplicate", Alias: "first", Host: "10.0.0.1"},
					{ID: "duplicate", Alias: "second", Host: "10.0.0.2"},
					{ID: "stable", Alias: "stale-id", Host: "10.0.0.3"},
				},
				WorkspaceAllowed: map[string][]string{"ws": {"first", "duplicate", "stale-id"}},
			}
			data, err := json.Marshal(store)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(a.sshClustersFilePath(), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := a.loadSSHClusters(); err != nil {
				t.Fatal(err)
			}
			first, _ := a.GetSSHServer("first")
			second, _ := a.GetSSHServer("second")
			if len(a.ListAllSSHServers()) != 3 || first.ID == second.ID || first.ID == "duplicate" || second.ID == "duplicate" {
				t.Fatal("duplicate IDs were not repaired")
			}
			if version == 0 {
				if !a.IsServerAuthorizedForWorkspace("ws", first.ID) || a.IsServerAuthorizedForWorkspace("ws", second.ID) {
					t.Fatal("legacy aliases did not migrate to the intended identity")
				}
			} else if len(a.GetWorkspaceAllowedServers("ws")) != 0 {
				t.Fatal("ambiguous or stale v2 IDs were interpreted as alias grants")
			}
		})
	}
}
