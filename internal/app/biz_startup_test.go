package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInitializationLeavesCleanupOutsideConfigLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ALLY_DATA_DIR", filepath.Join(home, ".ally_agent"))
	tempRoot := t.TempDir()
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, tempRoot)
	}
	workspace := t.TempDir()
	spill := filepath.Join(workspace, ".tmp", "command-logs", "ally-run-old.log")
	if err := os.MkdirAll(filepath.Dir(spill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spill, []byte("old output"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(spill, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDataDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfigState()
	cfg.Workspace = workspace
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDataDir(), "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spill); err != nil {
		t.Fatalf("config initialization performed cleanup: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app.runStartupMaintenance(ctx, workspace)
	if _, err := os.Stat(spill); err != nil {
		t.Fatalf("cancelled maintenance changed disk: %v", err)
	}
	app.runStartupMaintenance(context.Background(), workspace)
	if _, err := os.Stat(spill); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("background maintenance did not clean stale output: %v", err)
	}
}
