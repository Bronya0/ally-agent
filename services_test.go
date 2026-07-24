package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestServiceHistoryCleanup(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	dir := app.serviceHistoryDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"svc_old.json", "svc_old.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("legacy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.loadServiceHistory(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "svc_old.json")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy metadata to be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "svc_old.log")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy output to be removed, got err=%v", err)
	}
	if listed := app.ListServices(); len(listed.Services) != 0 {
		t.Fatalf("expected no completed services after cleanup, got %#v", listed.Services)
	}
}

// TestServiceListForToolOmitsOutputTail verifies that the model-facing list
// action returns only metadata (no outputTail) so listing several services
// cannot dominate the model context. The model must call read to inspect
// output.
func TestServiceListForToolOmitsOutputTail(t *testing.T) {
	app := NewApp()
	buffer := newRollingBuffer(serviceOutputLimit)
	_, _ = buffer.Write([]byte("sensitive startup log\n"))
	app.servicesMu.Lock()
	app.services["svc_a"] = &managedService{
		info: ServiceInfo{
			ID: "svc_a", Name: "frontend", Command: "npm run dev",
			Cwd: "/tmp", PID: 111, Status: "running", StartedAt: time.Now().Unix(),
		},
		output: buffer,
	}
	app.servicesMu.Unlock()

	result := app.listServicesForTool()
	if result.MaxActive != maxActiveServices {
		t.Fatalf("expected maxActive=%d, got %d", maxActiveServices, result.MaxActive)
	}
	if result.ActiveCount != 1 || len(result.Services) != 1 {
		t.Fatalf("expected 1 active service, got %#v", result)
	}
	summary := result.Services[0]
	if summary.ID != "svc_a" || summary.Status != "running" || summary.PID != 111 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	// The summary type intentionally has no OutputTail field; verify by
	// round-tripping through JSON that no output content leaks.
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	if strings.Contains(encoded, "outputTail") {
		t.Fatalf("list summary must not include outputTail: %s", encoded)
	}
	if strings.Contains(encoded, "sensitive startup log") {
		t.Fatalf("list summary must not include output content: %s", encoded)
	}
}

// TestServiceReadReturnsBoundedTail verifies that read returns at most
// tailBytes of recent output and reports the buffer/total byte accounting so
// the model can decide whether older output was discarded.
func TestServiceReadReturnsBoundedTail(t *testing.T) {
	app := NewApp()
	buffer := newRollingBuffer(serviceOutputLimit)
	payload := strings.Repeat("x", 4096) + "TAIL_MARKER"
	_, _ = buffer.Write([]byte(payload))

	app.servicesMu.Lock()
	app.services["svc_b"] = &managedService{
		info:   ServiceInfo{ID: "svc_b", Command: "demo", Status: "running"},
		output: buffer,
	}
	app.servicesMu.Unlock()

	// Default tail (8 KiB) should return the full payload since it is < 8 KiB.
	res, err := app.readServiceOutput(ServiceReadRequest{ID: "svc_b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "TAIL_MARKER") {
		t.Fatalf("expected tail marker in default read: %q", res.Output)
	}
	if res.TotalBytes != int64(len(payload)) || res.BufferBytes != int64(len(payload)) {
		t.Fatalf("unexpected byte accounting: %#v", res)
	}
	if res.FromByte != 0 {
		t.Fatalf("expected fromByte=0 for small buffer, got %d", res.FromByte)
	}

	// Explicit small tailBytes should clamp.
	res, err = app.readServiceOutput(ServiceReadRequest{ID: "svc_b", TailBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Output) != 16 || !strings.HasSuffix(res.Output, "TAIL_MARKER") {
		t.Fatalf("expected 16-byte tail ending in marker, got %d bytes: %q", len(res.Output), res.Output)
	}
	if res.FromByte != len(payload)-16 {
		t.Fatalf("expected fromByte=%d, got %d", len(payload)-16, res.FromByte)
	}

	// tailBytes above the hard cap must be clamped to maxServiceReadTailBytes.
	res, err = app.readServiceOutput(ServiceReadRequest{ID: "svc_b", TailBytes: 10 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	if res.ReturnedBytes > maxServiceReadTailBytes {
		t.Fatalf("read exceeded max tail bytes: %d", res.ReturnedBytes)
	}
}

// TestServiceReadErrors verifies the error codes for missing id and unknown id.
func TestServiceReadErrors(t *testing.T) {
	app := NewApp()
	if _, err := app.readServiceOutput(ServiceReadRequest{}); err == nil || toolErrorCode(err) != "E_BAD_SERVICE_ID" {
		t.Fatalf("expected E_BAD_SERVICE_ID for empty id, got %v", err)
	}
	if _, err := app.readServiceOutput(ServiceReadRequest{ID: "svc_missing"}); err == nil || toolErrorCode(err) != "E_SERVICE_NOT_FOUND" {
		t.Fatalf("expected E_SERVICE_NOT_FOUND for unknown id, got %v", err)
	}
}
