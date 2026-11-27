// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func TestSSHExportSelectedGroupsRoundTrip(t *testing.T) {
	servers := []backend.SSHServerNode{
		{ID: "foreign-ops", Alias: "运维一号", Group: "运维", AuthType: "key", KeyPath: `C:\密钥\id_ed25519`, Password: "测试口令"},
		{ID: "foreign-prod", Alias: "prod", Group: "生产", AuthType: "password", Password: "not-selected"},
		{ID: "foreign-ops2", Alias: "运维二号", Group: "运维", AuthType: "agent"},
		{ID: "foreign-old", Alias: "legacy", AuthType: "agent"},
		{ID: "foreign-new", Alias: "ungrouped", Group: sshUngrouped, AuthType: "agent"},
	}
	a := &desktop{prefs: defaultPrefs(), sshServers: servers}
	a.openSSHExport()
	var name, content string
	saves := 0
	tt := ui.NewTesterWith(func(c *ui.Context) {
		c.SetTheme(a.theme())
		a.sshExportDialog(c, func(n, data string) { name, content = n, data; saves++ })
	}, 900, 650, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("所有分组")
	if button, ok := tt.AccessByRole(ui.RoleButton, "导出"); !ok || !button.Disabled {
		t.Fatal("export should be disabled when all groups are unchecked")
	}
	tt.Key(0, ui.KeyEscape)
	if a.sshF.ExportOpen || saves != 0 {
		t.Fatal("dismissing the selection dialog must not export")
	}

	a.openSSHExport()
	tt.Frame()
	if all, ok := tt.AccessByRole(ui.RoleCheckBox, "所有分组"); !ok || !all.Checked {
		t.Fatal("reopening export should select all groups by default")
	}
	click("所有分组")
	click("运维")
	click(sshUngrouped)
	click("导出")
	if saves != 1 || a.sshF.ExportOpen || !strings.HasSuffix(name, ".json") {
		t.Fatalf("export saved %d times, dialog open = %v, filename = %q", saves, a.sshF.ExportOpen, name)
	}

	// The unchanged import path reads this envelope and keeps SSH settings intact.
	var payload struct {
		Nodes []backend.SSHServerNode `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		t.Fatal(err)
	}
	want := []backend.SSHServerNode{servers[0], servers[2], servers[3], servers[4]}
	if !slices.Equal(payload.Nodes, want) {
		t.Fatalf("exported nodes = %+v, want %+v", payload.Nodes, want)
	}
	store := &fakeSSHImportStore{nodes: []backend.SSHServerNode{{ID: "local-ops", Alias: servers[0].Alias}}}
	added, updated, failures := importSSHNodes(store, payload.Nodes)
	if added != 3 || updated != 1 || len(failures) != 0 {
		t.Fatalf("re-import added %d, updated %d, failures %v", added, updated, failures)
	}
	want[0].ID = "local-ops"
	if got := store.nodes[0]; got != want[0] || store.nodes[2].Group != sshUngrouped {
		t.Fatalf("re-imported nodes = %+v", store.nodes)
	}
}
