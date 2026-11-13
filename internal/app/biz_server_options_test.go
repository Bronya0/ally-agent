package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestServerOptionsPrecedenceAndValidation(t *testing.T) {
	t.Setenv("ALLY_SERVER_HOST", "")
	t.Setenv("ALLY_SERVER_PORT", "")
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

func TestServerOptionsEnvironmentAndDefaults(t *testing.T) {
	t.Setenv("ALLY_SERVER_HOST", "")
	t.Setenv("ALLY_SERVER_PORT", "")
	t.Setenv("WAILS_SERVER_HOST", "")
	t.Setenv("WAILS_SERVER_PORT", "")
	cfg, err := loadServerOptions(nil, io.Discard)
	if err != nil || cfg.Host != "127.0.0.1" || cfg.Port != apiDefaultPort {
		t.Fatalf("default options: %+v %v", cfg, err)
	}
	t.Setenv("WAILS_SERVER_HOST", "localhost")
	t.Setenv("WAILS_SERVER_PORT", "49001")
	t.Setenv("ALLY_SERVER_HOST", "0.0.0.0")
	t.Setenv("ALLY_SERVER_PORT", "49002")
	cfg, err = loadServerOptions(nil, io.Discard)
	if err != nil || cfg.Host != "0.0.0.0" || cfg.Port != 49002 {
		t.Fatalf("environment options: %+v %v", cfg, err)
	}
	cfg, err = loadServerOptions([]string{"--host", "::1", "--port", "49003"}, io.Discard)
	if err != nil || cfg.address() != "[::1]:49003" {
		t.Fatalf("CLI options: %+v %v", cfg, err)
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

func TestSplitHeadlessArgs(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		headless bool
		rest     []string
	}{
		{[]string{"--headless", "--port", "1"}, true, []string{"--port", "1"}},
		{[]string{"--port", "1", "--headless"}, true, []string{"--port", "1"}},
		{[]string{"-headless", "--host", "::1"}, true, []string{"--host", "::1"}},
		{[]string{"--headless=false", "--port", "1"}, false, []string{"--port", "1"}},
		{[]string{"--port", "1"}, false, []string{"--port", "1"}},
		{nil, false, []string{}},
	} {
		rest, headless, err := SplitHeadlessArgs(tc.args)
		if err != nil || headless != tc.headless || !slices.Equal(rest, tc.rest) {
			t.Fatalf("%v -> %v %v %v", tc.args, rest, headless, err)
		}
	}
	// A value that happens to look like the switch is untouched.
	if rest, headless, err := SplitHeadlessArgs([]string{"--token=--headless"}); err != nil || headless || len(rest) != 1 {
		t.Fatalf("token value parsed as a switch: %v %v %v", rest, headless, err)
	}
	if _, _, err := SplitHeadlessArgs([]string{"--headless=maybe"}); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}
