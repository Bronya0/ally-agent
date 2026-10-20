package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerOptionsPrecedenceAndValidation(t *testing.T) {
	t.Setenv("WAILS_SERVER_HOST", "")
	t.Setenv("WAILS_SERVER_PORT", "")
	dir := t.TempDir()
	config := filepath.Join(dir, "server.json")
	secret := filepath.Join(dir, "token")
	if err := os.WriteFile(secret, []byte("file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"host":"0.0.0.0","port":49001,"token":"config-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadServerOptions([]string{"--config", config, "--port", "49002", "--token-file", secret, "--data-dir", dir}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "0.0.0.0" || cfg.Port != 49002 || cfg.Token != "file-secret" || cfg.DataDir != dir {
		t.Fatal("server options did not honor explicit overrides")
	}
	for _, args := range [][]string{
		{"--port", "0"}, {"--port", "65536"}, {"--host", ""}, {"--host", "1.2.3.4:5"}, {"--host", "host/name"}, {"--unknown"}, {"--token", "a", "--token-file", secret}, {"unexpected"},
	} {
		if _, err := loadServerOptions(args, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	for _, content := range []string{"null", `{"port":1,"typo":true}`, `{} {}`} {
		if err := os.WriteFile(config, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadServerOptions([]string{"--config", config}, io.Discard); err == nil {
			t.Fatalf("accepted %s", content)
		}
	}
}

func TestRuntimeDataDirectoryLock(t *testing.T) {
	a := newApiTestApp(t)
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	if err := a.acquireRuntimeLock(); err != nil {
		t.Fatal(err)
	}
	b := NewApp()
	if err := b.acquireRuntimeLock(); err == nil {
		t.Fatal("second process acquired same data directory")
	}
	if err := a.stopRuntime(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := b.acquireRuntimeLock(); err != nil {
		t.Fatal(err)
	}
	if err := b.stopRuntime(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeShutdownTimeoutKeepsDirectoryLocked(t *testing.T) {
	a := newApiTestApp(t)
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	if err := a.acquireRuntimeLock(); err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(context.Background())
	a.runs["blocked"] = cancel
	if err := a.stopRuntime(10 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "checkpoints") {
		t.Fatalf("expected bounded checkpoint failure: %v", err)
	}
	b := NewApp()
	if err := b.acquireRuntimeLock(); err == nil {
		t.Fatal("released data lock while a task could still write")
	}
	delete(a.runs, "blocked")
	if err := a.stopRuntime(time.Second); err != nil {
		t.Fatal(err)
	}
}
