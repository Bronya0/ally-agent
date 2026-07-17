package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRollingBufferKeepsLatestOutput(t *testing.T) {
	buffer := newRollingBuffer(8)
	_, _ = buffer.Write([]byte("12345"))
	_, _ = buffer.Write([]byte("67890"))
	output, total, truncated := buffer.Snapshot()
	if output != "34567890" || total != 10 || !truncated {
		t.Fatalf("unexpected rolling buffer snapshot: output=%q total=%d truncated=%v", output, total, truncated)
	}
}

func TestLooksLikeLongRunningServiceWhitelistOnly(t *testing.T) {
	// Only exact known dev-server patterns are blocked; everything else is allowed.
	allow := []string{
		"ls -la",
		"npx vite build",
		"vite build",
		"vite",
		"npx vite",
		"vite --host 0.0.0.0",
		"./node_modules/.bin/vite",
		"npm run build",
		"pnpm build",
		"yarn build",
		"next build",
		"nuxt build",
		"wails build",
		"npm test",
		"pnpm lint",
		"go test ./...",
		"python app.py",
	}
	for _, command := range allow {
		if looksLikeLongRunningService(command) {
			t.Fatalf("non-whitelist command must not be blocked: %q", command)
		}
	}

	block := []string{
		"vite preview",
		"vite dev",
		"npm run dev",
		"pnpm run dev",
		"pnpm dev",
		"yarn run dev",
		"bun run dev",
		"next dev",
		"wails dev",
		"python manage.py runserver",
		"uvicorn app:app --reload",
		"flask run",
		"fastapi dev",
	}
	for _, command := range block {
		if !looksLikeLongRunningService(command) {
			t.Fatalf("whitelist dev-server command must be blocked: %q", command)
		}
	}
}

func TestServiceHistoryRoundTrip(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	buffer := newRollingBuffer(serviceOutputLimit)
	_, _ = buffer.Write([]byte("service output\n"))
	service := &managedService{
		info:   ServiceInfo{ID: "svc_test", Name: "test", Command: "test-command", Cwd: root, Status: "stopped", StartedAt: 10, StoppedAt: 20},
		output: buffer,
	}
	if err := app.persistService(service); err != nil {
		t.Fatal(err)
	}

	reloaded := NewApp()
	reloaded.configPath = app.configPath
	if err := reloaded.loadServiceHistory(); err != nil {
		t.Fatal(err)
	}
	listed := reloaded.ListServices()
	if len(listed.Services) != 1 || listed.Services[0].ID != "svc_test" {
		t.Fatalf("unexpected persisted services: %#v", listed.Services)
	}
	output, err := reloaded.GetServiceOutput("svc_test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.Output, "service output") || output.Bytes != int64(len("service output\n")) {
		t.Fatalf("unexpected persisted output: %#v", output)
	}
}
