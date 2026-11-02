package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveSSHServerForWorkspaceRegistersAndAuthorizes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	a := NewApp()
	a.configPath = filepath.Join(dir, "config.json")
	ws := filepath.Join(dir, "workspace")
	node := SSHServerNode{Alias: "prod-web", Host: "127.0.0.1", Username: "deploy", Description: "deploy target"}
	if err := a.SaveSSHServerForWorkspace(node, ws); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := a.GetWorkspaceAllowedServers(ws); len(got) != 1 {
		t.Fatalf("workspace grants = %v, want one server", got)
	}
	// Both halves must be on disk, not only in memory.
	reloaded := NewApp()
	reloaded.configPath = a.configPath
	if err := reloaded.loadSSHClusters(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.GetWorkspaceAllowedServers(ws); len(got) != 1 {
		t.Fatalf("reloaded workspace grants = %v, want one server", got)
	}
	if nodes := reloaded.ListAllSSHServers(); len(nodes) != 1 || nodes[0].Alias != "prod-web" {
		t.Fatalf("reloaded servers = %+v", nodes)
	}

	// A second registration for the same alias must not inherit the grant.
	if err := a.SaveSSHServerForWorkspace(node, ws); err == nil {
		t.Fatal("duplicate alias was accepted by the registration flow")
	}
	// This entry point only creates nodes; updating one must go through SaveSSHServer.
	existing := a.ListAllSSHServers()[0]
	if err := a.SaveSSHServerForWorkspace(SSHServerNode{ID: existing.ID, Alias: "renamed", Host: "127.0.0.2", Username: "deploy", Description: "update"}, ws); err == nil {
		t.Fatal("registration flow accepted an update to an existing server")
	}
	// Without a workspace there is nothing to authorize.
	if err := a.SaveSSHServerForWorkspace(SSHServerNode{Alias: "elsewhere", Host: "127.0.0.3", Username: "deploy", Description: "no ws"}, "  "); err == nil {
		t.Fatal("registration flow accepted an empty workspace")
	}
}

func TestSaveSSHServerForWorkspaceRollsBackWhenPersistFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	a := NewApp()
	a.configPath = filepath.Join(dir, "config.json")
	ws := filepath.Join(dir, "workspace")
	if err := a.SaveSSHServerForWorkspace(SSHServerNode{Alias: "kept", Host: "127.0.0.1", Username: "deploy", Description: "keep"}, ws); err != nil {
		t.Fatalf("seed: %v", err)
	}
	serversBefore := len(a.ListAllSSHServers())
	grantsBefore := a.GetWorkspaceAllowedServers(ws)

	// Point the store inside a regular file so persistence fails after the
	// in-memory node and grant have been staged.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.configPath = filepath.Join(blocker, "config.json")

	if err := a.SaveSSHServerForWorkspace(SSHServerNode{Alias: "half", Host: "127.0.0.9", Username: "deploy", Description: "must not persist"}, ws); err == nil {
		t.Fatal("expected the registration to fail when the cluster file cannot be written")
	}
	if got := len(a.ListAllSSHServers()); got != serversBefore {
		t.Fatalf("failed registration left %d servers, want %d", got, serversBefore)
	}
	if got := a.GetWorkspaceAllowedServers(ws); len(got) != len(grantsBefore) {
		t.Fatalf("failed registration left grants %v, want %v", got, grantsBefore)
	}
}
