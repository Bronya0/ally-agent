package app

import (
	"encoding/json"
	"testing"
)

// mcpServersOf decodes just the server map so assertions do not depend on the
// writer's key order or indentation.
func mcpServersOf(t *testing.T, raw string) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decode mcp config: %v\n%s", err, raw)
	}
	return doc.Servers
}

func TestMergeMcpConfigKeepsConcurrentEntriesAndTopLevelKeys(t *testing.T) {
	t.Parallel()
	app := newIsolatedChatTestApp(t)
	if err := app.SaveMcpConfig(`{"mcpServers":{"a":{"command":"node","args":["a.js"]},"b":{"command":"node"}},"vendor":{"keep":1}}`); err != nil {
		t.Fatal(err)
	}
	// The page snapshot the edit was based on.
	base, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	// Another writer changes a different server while the editor was open.
	if err := app.SaveMcpConfig(`{"mcpServers":{"a":{"command":"node","args":["a.js"]},"b":{"command":"npx","args":["-y","x"]}},"vendor":{"keep":1}}`); err != nil {
		t.Fatal(err)
	}
	merged, err := app.MergeMcpConfig(base, `{"mcpServers":{"a":{"command":"node","args":["a.js"],"enabled":false},"b":{"command":"node"}},"vendor":{}}`)
	if err != nil {
		t.Fatalf("merge rejected an edit that touched a different server: %v", err)
	}
	servers := mcpServersOf(t, merged)
	if enabled, ok := servers["a"]["enabled"].(bool); !ok || enabled {
		t.Fatalf("own edit lost: %#v", servers["a"])
	}
	if command, _ := servers["b"]["command"].(string); command != "npx" {
		t.Fatalf("concurrent change to another server was overwritten: %#v", servers["b"])
	}
	var top map[string]map[string]any
	if err := json.Unmarshal([]byte(merged), &top); err != nil {
		t.Fatal(err)
	}
	if keep, ok := top["vendor"]["keep"].(float64); !ok || keep != 1 {
		t.Fatalf("top-level keys must come from the saved file, got %#v", top["vendor"])
	}
	stored, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mcpServersOf(t, stored)["a"]["enabled"]; !ok {
		t.Fatalf("merge did not reach disk: %s", stored)
	}
}

func TestMergeMcpConfigRejectsSameServerConflictWithoutWriting(t *testing.T) {
	t.Parallel()
	app := newIsolatedChatTestApp(t)
	if err := app.SaveMcpConfig(`{"mcpServers":{"a":{"command":"node"}}}`); err != nil {
		t.Fatal(err)
	}
	base, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SaveMcpConfig(`{"mcpServers":{"a":{"command":"other"}}}`); err != nil {
		t.Fatal(err)
	}
	before, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.MergeMcpConfig(base, `{"mcpServers":{"a":{"command":"mine"}}}`); err == nil {
		t.Fatal("expected a conflict error for a server changed on disk")
	}
	after, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("conflicting merge wrote the file: %s", after)
	}
}

func TestMergeMcpConfigDeleteKeepsConcurrentlyAddedServer(t *testing.T) {
	t.Parallel()
	app := newIsolatedChatTestApp(t)
	if err := app.SaveMcpConfig(`{"mcpServers":{"a":{"command":"node"},"b":{"command":"node"}}}`); err != nil {
		t.Fatal(err)
	}
	base, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SaveMcpConfig(`{"mcpServers":{"a":{"command":"node"},"b":{"command":"node"},"c":{"command":"new"}}}`); err != nil {
		t.Fatal(err)
	}
	merged, err := app.MergeMcpConfig(base, `{"mcpServers":{"b":{"command":"node"}}}`)
	if err != nil {
		t.Fatalf("delete merge failed: %v", err)
	}
	servers := mcpServersOf(t, merged)
	if _, ok := servers["a"]; ok {
		t.Fatal("deleted server still present")
	}
	if _, ok := servers["b"]; !ok {
		t.Fatal("untouched server lost")
	}
	if _, ok := servers["c"]; !ok {
		t.Fatal("server added by another writer was dropped")
	}
}

func TestSaveMcpConfigIfUnchangedRejectsStaleSnapshot(t *testing.T) {
	t.Parallel()
	app := newIsolatedChatTestApp(t)
	if err := app.SaveMcpConfig(`{"mcpServers":{"keep":{"command":"node"}}}`); err != nil {
		t.Fatal(err)
	}
	fresh, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	// An unchanged snapshot may replace the whole document (import path).
	if err := app.SaveMcpConfigIfUnchanged(fresh, `{"mcpServers":{"imported":{"command":"npx"}}}`); err != nil {
		t.Fatalf("import on an unchanged snapshot failed: %v", err)
	}
	imported, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mcpServersOf(t, imported)["imported"]; !ok {
		t.Fatalf("import did not reach disk: %s", imported)
	}
	// The pre-import snapshot is stale now and must not erase the import.
	if err := app.SaveMcpConfigIfUnchanged(fresh, `{"mcpServers":{}}`); err == nil {
		t.Fatal("stale import snapshot was accepted")
	}
	after, err := app.GetMcpConfig()
	if err != nil {
		t.Fatal(err)
	}
	if after != imported {
		t.Fatalf("stale import wrote the file: %s", after)
	}
}
