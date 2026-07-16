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
