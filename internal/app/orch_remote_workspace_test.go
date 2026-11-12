package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Scheduled tasks run without a session id, but they still execute against
// their own workspace. A registered node authorized in another workspace must
// not be usable from the task workspace.
func TestRemoteTargetAuthorizationUsesRequestWorkspace(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)
	t.Setenv("USERPROFILE", tempDir)
	app := NewApp()
	app.configPath = filepath.Join(tempDir, "config.json")
	ws := filepath.Join(tempDir, "task-workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveSSHServer(SSHServerNode{
		Alias:       "my-srv",
		Host:        "192.168.1.88",
		Port:        22,
		Username:    "ubuntu",
		Description: "Ubuntu test server",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := withToolWorkspace(context.Background(), ws)
	if _, err := app.resolveAndAuthorizeRemoteTarget(ctx, "my-srv"); toolErrorCode(err) != "E_SERVER_UNAUTHORIZED" {
		t.Fatalf("expected E_SERVER_UNAUTHORIZED for the task workspace, got %v", err)
	}

	app.AuthorizeServerForWorkspace(ws, "my-srv")
	connectCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	rt, err := app.resolveAndAuthorizeRemoteTarget(connectCtx, "my-srv")
	if err != nil {
		t.Fatalf("expected the authorized node to resolve: %v", err)
	}
	if rt.Host != "ubuntu@192.168.1.88" {
		t.Fatalf("unexpected resolved target: %+v", rt)
	}
}
