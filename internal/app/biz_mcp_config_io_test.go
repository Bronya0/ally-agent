package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMcpConfigBOMAndExactValuesSurviveSaveLoad(t *testing.T) {
	t.Parallel()
	app := newApiTestApp(t)
	raw := "\ufeff" + `{"extraRoot":{"keep":true},"mcpServers":{"demo":{"command":"node","args":["C:\\Program Files\\server.js","hello world",""],"env":{"PAD":" x ","LINES":"a\nb"},"vendor":{"value":1}}}}`
	if err := app.SaveMcpConfig(raw); err != nil {
		t.Fatal(err)
	}
	stored, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	var expected, actual map[string]any
	if err := json.Unmarshal([]byte(raw[len("\ufeff"):]), &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(stored), &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("MCP data changed: got %#v, want %#v", actual, expected)
	}
	// Also support a BOM in a hand-written file read by the runtime manager.
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewMcpManager(t.TempDir(), nil)
	manager.configPaths = []string{path}
	configs, err := manager.LoadConfigs()
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := configs["demo"]
	if !ok || !reflect.DeepEqual(cfg.Args, []string{`C:\Program Files\server.js`, "hello world", ""}) || cfg.Env["PAD"] != " x " || cfg.Env["LINES"] != "a\nb" {
		t.Fatalf("runtime load changed values: %+v", configs)
	}
}

func TestMcpInvalidConfigNeverReplacesSavedFile(t *testing.T) {
	t.Parallel()
	app := newApiTestApp(t)
	const original = `{"mcpServers":{"keep":{"command":"node","args":["a b",""]}},"extra":true}`
	if err := app.SaveMcpConfig(original); err != nil {
		t.Fatal(err)
	}
	before, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `[]`, `{"mcpServers":{"":{}}}`, `{"mcpServers":{"s":null}}`,
		`{"mcpServers":{"s":{"command":null}}}`, `{"mcpServers":{"s":{"args":null}}}`,
		`{"mcpServers":{"s":{"args":[null]}}}`, `{"mcpServers":{"s":{"disabledTools":[null]}}}`,
		`{"mcpServers":{"s":{"env":{"X":null}}}}`, `{"mcpServers":{"s":{"headers":{"X":null}}}}`,
		`{"mcpServers":{"s":{"enabled":null}}}`, `{"mcpServers":{"s":{"startupTimeoutSec":null}}}`} {
		if err := app.SaveMcpConfig(raw); err == nil {
			t.Fatalf("accepted invalid payload: %s", raw)
		}
		after, err := app.GetMcpConfig()
		if err != nil || after != before {
			t.Fatalf("invalid import changed saved config: %s, %v", raw, err)
		}
	}
}
